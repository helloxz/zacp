// Package manager owns ACP agent processes and sessions.
package manager

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	acpclient "github.com/zacp/zacp/internal/acp/client"
)

// Config for launching an ACP agent over stdio.
type Config struct {
	// Command is the agent binary, e.g. "reasonix".
	Command string
	// Args defaults to []string{"--acp"} when empty.
	Args []string
	// Cwd is the working directory for the agent process and NewSession.
	Cwd string
	// AutoApprove auto-allows tool permission requests (demo default true).
	AutoApprove bool
	// Env extra env vars for the agent process.
	Env []string
}

// Manager holds one agent connection and one demo session (minimal).
type Manager struct {
	log    *slog.Logger
	cfg    Config
	bridge *acpclient.Bridge

	mu      sync.Mutex
	cmd     *exec.Cmd
	conn    *acp.ClientSideConnection
	stdin   io.WriteCloser
	session acp.SessionId
	started bool
	// procCancel cancels the agent process lifetime (separate from Start handshake timeout).
	procCancel context.CancelFunc
	// promptMu serializes Prompt turns (ACP typically one active prompt per session).
	promptMu sync.Mutex
}

// New creates a manager (does not start the agent yet).
func New(log *slog.Logger, cfg Config) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Command == "" {
		cfg.Command = resolveReasonix()
	}
	if len(cfg.Args) == 0 {
		cfg.Args = []string{"--acp"}
	}
	if cfg.Cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cfg.Cwd = wd
		} else {
			cfg.Cwd = "."
		}
	}
	if abs, err := filepath.Abs(cfg.Cwd); err == nil {
		cfg.Cwd = abs
	}
	return &Manager{
		log:    log,
		cfg:    cfg,
		bridge: acpclient.New(log, cfg.AutoApprove),
	}
}

// Bridge returns the underlying ACP client bridge (for live event hooks).
func (m *Manager) Bridge() *acpclient.Bridge { return m.bridge }

// SessionID returns the current ACP session id (empty if not started).
func (m *Manager) SessionID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.session)
}

// Start launches the agent, initializes ACP, and opens a session.
// ctx bounds only the handshake (Initialize / NewSession), not the agent process lifetime.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return nil
	}

	// Process lifetime is independent of the handshake timeout context.
	procCtx, procCancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, m.cfg.Command, m.cfg.Args...)
	cmd.Dir = m.cfg.Cwd
	cmd.Env = append(os.Environ(), m.cfg.Env...)
	// Agent stderr -> our log for debugging.
	cmd.Stderr = &logWriter{log: m.log, prefix: "reasonix"}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		procCancel()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		procCancel()
		_ = stdin.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	m.log.Info("starting agent", "command", m.cfg.Command, "args", m.cfg.Args, "cwd", m.cfg.Cwd)
	if err := cmd.Start(); err != nil {
		procCancel()
		_ = stdin.Close()
		return fmt.Errorf("start %s: %w (is reasonix on PATH? set REASONIX_BIN)", m.cfg.Command, err)
	}

	conn := acp.NewClientSideConnection(m.bridge, stdin, stdout)
	conn.SetLogger(m.log)

	initResp, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs:       acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
			Terminal: true,
		},
		ClientInfo: &acp.Implementation{
			Name:    "zacp",
			Title:   acp.Ptr("zacp demo"),
			Version: "0.1.0",
		},
	})
	if err != nil {
		procCancel()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return fmt.Errorf("acp initialize: %w", err)
	}
	m.log.Info("acp initialized", "protocol", initResp.ProtocolVersion)

	sess, err := conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        m.cfg.Cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		procCancel()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return fmt.Errorf("acp newSession: %w", err)
	}
	m.log.Info("session created", "sessionId", sess.SessionId)

	m.cmd = cmd
	m.conn = conn
	m.stdin = stdin
	m.session = sess.SessionId
	m.started = true
	m.procCancel = procCancel

	// Reap process in background.
	go func() {
		err := cmd.Wait()
		m.log.Info("agent process exited", "err", err)
		m.mu.Lock()
		m.started = false
		m.mu.Unlock()
	}()

	return nil
}

// ChatResult is the outcome of one prompt turn.
type ChatResult struct {
	SessionID  string             `json:"sessionId"`
	Reply      string             `json:"reply"`
	StopReason string             `json:"stopReason,omitempty"`
	Events     []acpclient.Event  `json:"events"`
	DurationMs int64              `json:"durationMs"`
}

// Chat sends a user message and waits for the agent turn to complete.
func (m *Manager) Chat(ctx context.Context, message string) (*ChatResult, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("empty message")
	}

	m.mu.Lock()
	if !m.started || m.conn == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("agent not started")
	}
	conn := m.conn
	sessionID := m.session
	m.mu.Unlock()

	m.promptMu.Lock()
	defer m.promptMu.Unlock()

	m.bridge.Reset()
	start := time.Now()

	resp, err := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock(message)},
	})
	if err != nil {
		return nil, fmt.Errorf("prompt: %w", err)
	}

	return &ChatResult{
		SessionID:  string(sessionID),
		Reply:      m.bridge.AgentText(),
		StopReason: string(resp.StopReason),
		Events:     m.bridge.Events(),
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// Cancel cancels the current prompt turn if any.
func (m *Manager) Cancel(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started || m.conn == nil {
		return fmt.Errorf("agent not started")
	}
	return m.conn.Cancel(ctx, acp.CancelNotification{SessionId: m.session})
}

// Close kills the agent process.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.procCancel != nil {
		m.procCancel()
		m.procCancel = nil
	}
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
	}
	if m.stdin != nil {
		_ = m.stdin.Close()
	}
	m.started = false
	return nil
}

// resolveReasonix finds the reasonix binary.
func resolveReasonix() string {
	if v := os.Getenv("REASONIX_BIN"); v != "" {
		return v
	}
	if p, err := exec.LookPath("reasonix"); err == nil {
		return p
	}
	// Common install locations on this machine / pnpm global.
	candidates := []string{
		"/home/xiaoz/.local/share/pnpm/global/5/.pnpm/@reasonix+cli-linux-x64@1.17.1-rc.1/node_modules/@reasonix/cli-linux-x64/bin/reasonix",
		"/data/apps/znode/bin/reasonix",
		filepath.Join(os.Getenv("HOME"), ".local/share/pnpm/reasonix"),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return "reasonix"
}

// logWriter forwards agent stderr lines to slog.
type logWriter struct {
	log    *slog.Logger
	prefix string
	buf    []byte
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := -1
		for j, b := range w.buf {
			if b == '\n' {
				i = j
				break
			}
		}
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		if line != "" {
			w.log.Info(w.prefix, "stderr", line)
		}
	}
	return len(p), nil
}
