// Package manager 管理多个 Agent 进程和多个 ACP Session。
// 核心设计：
// - Manager 管理多个 AgentConnection（每个 agent ID 一个进程）
// - 每个 AgentConnection 可以管理多个 Session
// - 对外暴露：启动/停止 agent、创建/加载 session、发送 prompt、取消等
package manager

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	acpclient "github.com/zacp/zacp/internal/acp/client"
	"github.com/zacp/zacp/internal/acp/providers"
)

// Manager 管理多个 agent 连接和 session。
type Manager struct {
	log         *slog.Logger
	registry    *providers.ProviderRegistry
	autoApprove bool
	defaultCwd  string

	mu     sync.Mutex
	agents map[string]*AgentConnection // agentID -> connection
}

// Config 管理器配置。
type Config struct {
	// Registry 是 Provider 注册表。
	Registry *providers.ProviderRegistry
	// AutoApprove 是否自动批准权限请求。
	AutoApprove bool
	// DefaultCwd 是默认工作目录。
	DefaultCwd string
}

// New 创建 Manager（不启动任何 agent）。
func New(log *slog.Logger, cfg Config) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if cfg.DefaultCwd == "" {
		cfg.DefaultCwd = "."
	}
	return &Manager{
		log:         log,
		registry:    cfg.Registry,
		autoApprove: cfg.AutoApprove,
		defaultCwd:  cfg.DefaultCwd,
		agents:      make(map[string]*AgentConnection),
	}
}

// AgentStatus agent 运行状态。
type AgentStatus struct {
	AgentID   string `json:"agentId"`
	Name      string `json:"name"`
	Running   bool   `json:"running"`
	SessionID string `json:"sessionId,omitempty"` // 当前活跃 session（如果有）
}

// GetAgentStatus 返回指定 agent 的状态。
func (m *Manager) GetAgentStatus(agentID string) (*AgentStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	provider, ok := m.registry.Get(agentID)
	if !ok {
		return nil, fmt.Errorf("agent '%s' not found", agentID)
	}

	status := &AgentStatus{
		AgentID: agentID,
		Name:    provider.Name,
		Running: false,
	}

	if conn, exists := m.agents[agentID]; exists {
		conn.mu.Lock()
		status.Running = conn.started
		if conn.currentSession != nil {
			status.SessionID = string(conn.currentSession.ID)
		}
		conn.mu.Unlock()
	}

	return status, nil
}

// ListAgents 返回所有已注册 agent 的状态列表。
func (m *Manager) ListAgents() []*AgentStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := m.registry.List()
	result := make([]*AgentStatus, 0, len(ids))
	for _, id := range ids {
		provider, _ := m.registry.Get(id)
		status := &AgentStatus{
			AgentID: id,
			Name:    provider.Name,
			Running: false,
		}
		if conn, exists := m.agents[id]; exists {
			conn.mu.Lock()
			status.Running = conn.started
			if conn.currentSession != nil {
				status.SessionID = string(conn.currentSession.ID)
			}
			conn.mu.Unlock()
		}
		result = append(result, status)
	}
	return result
}

// StartAgent 启动指定 agent 进程（如果未启动）。
func (m *Manager) StartAgent(ctx context.Context, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	provider, ok := m.registry.Get(agentID)
	if !ok {
		return fmt.Errorf("agent '%s' not found", agentID)
	}

	// 检查是否已启动
	if conn, exists := m.agents[agentID]; exists {
		conn.mu.Lock()
		running := conn.started
		conn.mu.Unlock()
		if running {
			return nil // 已启动
		}
	}

	// 创建新连接
	conn := NewAgentConnection(m.log, provider, m.autoApprove)
	if err := conn.Start(ctx); err != nil {
		return fmt.Errorf("start agent '%s': %w", agentID, err)
	}

	m.agents[agentID] = conn
	return nil
}

// StopAgent 停止指定 agent 进程。
func (m *Manager) StopAgent(agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn, exists := m.agents[agentID]
	if !exists {
		return nil // 未启动，无需停止
	}

	if err := conn.Close(); err != nil {
		return fmt.Errorf("stop agent '%s': %w", agentID, err)
	}

	delete(m.agents, agentID)
	return nil
}

// CreateSession 在指定 agent 上创建新 session，返回 session ID 与 agent 下发的配置项
// （模型/思考强度/mode 等，来自 session/new 响应的 configOptions）。
func (m *Manager) CreateSession(ctx context.Context, agentID, cwd string) (string, []acp.SessionConfigOption, error) {
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()

	if !exists {
		return "", nil, fmt.Errorf("agent '%s' not started", agentID)
	}

	provider, _ := m.registry.Get(agentID)
	if cwd == "" {
		cwd = provider.ResolveCwd(m.defaultCwd)
	}

	sessionID, configOptions, err := conn.CreateSession(ctx, cwd)
	if err != nil {
		return "", nil, err
	}
	return string(sessionID), configOptions, nil
}

// SetSessionConfigOption 设置会话配置项（如切换模型/思考强度/mode）。
func (m *Manager) SetSessionConfigOption(ctx context.Context, agentID, sessionID, configID, valueID string) error {
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()
	if !exists {
		return fmt.Errorf("agent '%s' not started", agentID)
	}
	return conn.SetSessionConfigOption(ctx, sessionID, configID, valueID)
}

// SetSessionConfigOptionBoolean 设置会话配置项（boolean 型开关）。
func (m *Manager) SetSessionConfigOptionBoolean(ctx context.Context, agentID, sessionID, configID string, value bool) error {
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()
	if !exists {
		return fmt.Errorf("agent '%s' not started", agentID)
	}
	return conn.SetSessionConfigOptionBoolean(ctx, sessionID, configID, value)
}

// LoadSession 恢复已有 session（用于会话恢复）。
func (m *Manager) LoadSession(ctx context.Context, agentID, sessionID string) error {
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("agent '%s' not started", agentID)
	}

	return conn.LoadSession(ctx, acp.SessionId(sessionID))
}

// IsUnknownSessionErr 判断 ACP 错误是否为「agent 侧会话不存在/已失效」。
// 后端或 agent 重启后，DB 中记录的 acp_session_id 在 agent 内存中已丢失，
// agent 会返回形如 `session/xxx: unknown session <uuid>` 的 JSON-RPC 错误。
// ws bridge（prompt 路径）与 service（config-options 路径）共用此判断触发自动恢复。
func IsUnknownSessionErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unknown session")
}

// RecoverSession 恢复 agent 侧已失效的 ACP session（unknown session 时调用）：
//  1. 优先 ACP session/load：agent 支持持久化会话时（reasonix 等），可原样恢复对话上下文
//  2. 失败则 session/new 重建（上下文丢失，但会话可继续使用）
//
// 返回最终可用的 ACP session id，以及是否发生了重建（load 成功时 id 不变、rebuilt=false）。
// 调用方在 rebuilt=true 时需要把新 id 更新到 DB，并（如适用）重新绑定事件回调。
func (m *Manager) RecoverSession(ctx context.Context, agentID, oldAcpID, cwd string) (string, bool, error) {
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()
	if !exists {
		return "", false, fmt.Errorf("agent '%s' not started", agentID)
	}

	if err := conn.LoadSession(ctx, acp.SessionId(oldAcpID)); err == nil {
		m.log.Info("acp session recovered via load", "agent", agentID, "sessionId", oldAcpID)
		return oldAcpID, false, nil
	}

	// load 失败：新建 ACP session（沿用 cwd；空时回退 provider 默认工作区）
	provider, _ := m.registry.Get(agentID)
	if cwd == "" && provider != nil {
		cwd = provider.ResolveCwd(m.defaultCwd)
	}
	newID, _, err := conn.CreateSession(ctx, cwd)
	if err != nil {
		return "", false, fmt.Errorf("recreate acp session: %w", err)
	}
	m.log.Info("acp session recreated", "agent", agentID, "old", oldAcpID, "new", newID)
	return string(newID), true, nil
}

// Prompt 向指定 session 发送消息。
func (m *Manager) Prompt(ctx context.Context, agentID, sessionID, message string) (*PromptResult, error) {
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()

	if !exists {
		return nil, fmt.Errorf("agent '%s' not started", agentID)
	}

	return conn.Prompt(ctx, acp.SessionId(sessionID), message)
}

// Cancel 取消指定 session 的当前 prompt。
func (m *Manager) Cancel(ctx context.Context, agentID, sessionID string) error {
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("agent '%s' not started", agentID)
	}

	return conn.Cancel(ctx, acp.SessionId(sessionID))
}

// CloseSession 关闭指定 ACP session（释放 agent 端会话资源）。
// 用于切 tab 时释放旧隐式草稿会话；agent 不支持 sessionCapabilities.close 时返回 nil（尽力释放，不报错）。
// 注意：这是 ACP 协议层会话关闭，不影响 agent 进程本身（StopAgent 才停进程）。
func (m *Manager) CloseSession(ctx context.Context, agentID, sessionID string) error {
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("agent '%s' not started", agentID)
	}

	return conn.CloseSession(ctx, acp.SessionId(sessionID))
}

// Close 关闭所有 agent 连接。
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for id, conn := range m.agents {
		if err := conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close agent '%s': %w", id, err))
		}
	}
	m.agents = make(map[string]*AgentConnection)

	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}
	return nil
}

// GetBridge 返回指定 agent 的 bridge（用于事件订阅）。
func (m *Manager) GetBridge(agentID string) (*acpclient.Bridge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn, exists := m.agents[agentID]
	if !exists {
		return nil, fmt.Errorf("agent '%s' not started", agentID)
	}

	return conn.Bridge(), nil
}

// AgentConnection 管理单个 agent 进程的连接。
type AgentConnection struct {
	log      *slog.Logger
	provider *providers.Provider
	bridge   *acpclient.Bridge

	mu            sync.Mutex
	cmd           *exec.Cmd
	conn          *acp.ClientSideConnection
	stdin         io.WriteCloser
	currentSession *SessionState
	started       bool
	procCancel    context.CancelFunc
	promptMu      sync.Mutex // 序列化 prompt 调用
}

// SessionState 单个 session 的状态。
type SessionState struct {
	ID      acp.SessionId
	Cwd     string
	Created time.Time
}

// NewAgentConnection 创建 agent 连接（不启动进程）。
func NewAgentConnection(log *slog.Logger, provider *providers.Provider, autoApprove bool) *AgentConnection {
	if log == nil {
		log = slog.Default()
	}
	return &AgentConnection{
		log:      log,
		provider: provider,
		bridge:   acpclient.New(log, autoApprove),
	}
}

// Bridge 返回底层 bridge。
func (c *AgentConnection) Bridge() *acpclient.Bridge {
	return c.bridge
}

// Start 启动 agent 进程并初始化 ACP。
func (c *AgentConnection) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return nil
	}

	// 启动进程
	procCtx, procCancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, c.provider.Command, c.provider.Args...)
	cmd.Dir = c.provider.ResolveCwd("")
	cmd.Env = append(os.Environ(), c.provider.Env...)
	cmd.Stderr = &logWriter{log: c.log, prefix: c.provider.ID}

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

	c.log.Info("starting agent", "id", c.provider.ID, "command", c.provider.Command, "cwd", cmd.Dir)
	if err := cmd.Start(); err != nil {
		procCancel()
		_ = stdin.Close()
		return fmt.Errorf("start %s: %w", c.provider.Command, err)
	}

	// 初始化 ACP
	conn := acp.NewClientSideConnection(c.bridge, stdin, stdout)
	conn.SetLogger(c.log)

	initResp, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs:       acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
			Terminal: true,
		},
		ClientInfo: &acp.Implementation{
			Name:    "zacp",
			Title:   acp.Ptr("zacp gateway"),
			Version: "0.1.0",
		},
	})
	if err != nil {
		procCancel()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return fmt.Errorf("acp initialize: %w", err)
	}
	c.log.Info("acp initialized", "agent", c.provider.ID, "protocol", initResp.ProtocolVersion)

	c.cmd = cmd
	c.conn = conn
	c.stdin = stdin
	c.started = true
	c.procCancel = procCancel

	// 后台回收进程
	go func() {
		err := cmd.Wait()
		c.log.Info("agent process exited", "agent", c.provider.ID, "err", err)
		c.mu.Lock()
		c.started = false
		c.mu.Unlock()
	}()

	return nil
}

// CreateSession 创建新 session。
func (c *AgentConnection) CreateSession(ctx context.Context, cwd string) (acp.SessionId, []acp.SessionConfigOption, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started || c.conn == nil {
		return "", nil, fmt.Errorf("agent not started")
	}

	sess, err := c.conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		return "", nil, fmt.Errorf("create session: %w", err)
	}

	c.currentSession = &SessionState{
		ID:      sess.SessionId,
		Cwd:     cwd,
		Created: time.Now(),
	}

	c.log.Info("session created", "agent", c.provider.ID, "sessionId", sess.SessionId, "cwd", cwd, "configOptions", len(sess.ConfigOptions))
	return sess.SessionId, sess.ConfigOptions, nil
}

// LoadSession 恢复已有 session。
func (c *AgentConnection) LoadSession(ctx context.Context, sessionID acp.SessionId) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started || c.conn == nil {
		return fmt.Errorf("agent not started")
	}

	_, err := c.conn.LoadSession(ctx, acp.LoadSessionRequest{
		SessionId: sessionID,
	})
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	c.currentSession = &SessionState{
		ID:      sessionID,
		Created: time.Now(),
	}

	c.log.Info("session loaded", "agent", c.provider.ID, "sessionId", sessionID)
	return nil
}

// PromptResult prompt 执行结果。
type PromptResult struct {
	SessionID  string            `json:"sessionId"`
	Reply      string            `json:"reply"`
	StopReason string            `json:"stopReason,omitempty"`
	Events     []acpclient.Event `json:"events"`
	DurationMs int64             `json:"durationMs"`
}

// Prompt 发送消息并等待响应。
func (c *AgentConnection) Prompt(ctx context.Context, sessionID acp.SessionId, message string) (*PromptResult, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("empty message")
	}

	c.mu.Lock()
	if !c.started || c.conn == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("agent not started")
	}
	conn := c.conn
	c.mu.Unlock()

	c.promptMu.Lock()
	defer c.promptMu.Unlock()

	c.bridge.Reset()
	start := time.Now()

	resp, err := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock(message)},
	})
	if err != nil {
		return nil, fmt.Errorf("prompt: %w", err)
	}

	return &PromptResult{
		SessionID:  string(sessionID),
		Reply:      c.bridge.AgentText(),
		StopReason: string(resp.StopReason),
		Events:     c.bridge.Events(),
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// Cancel 取消当前 prompt。
func (c *AgentConnection) Cancel(ctx context.Context, sessionID acp.SessionId) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started || c.conn == nil {
		return fmt.Errorf("agent not started")
	}

	return c.conn.Cancel(ctx, acp.CancelNotification{SessionId: sessionID})
}

// CloseSession 关闭单个 ACP session（释放 agent 端会话资源）。
// 用于切 tab 时释放旧隐式草稿会话。ACP CloseSession 是可选能力
// （sessionCapabilities.close），agent 不支持时可能报错，调用方按尽力释放处理。
func (c *AgentConnection) CloseSession(ctx context.Context, sessionID acp.SessionId) error {
	c.mu.Lock()
	if !c.started || c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("agent not started")
	}
	conn := c.conn
	c.mu.Unlock()

	_, err := conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: sessionID})
	return err
}

// SetSessionConfigOption 设置会话配置项（select 型，如模型/思考强度/mode 切换）。
func (c *AgentConnection) SetSessionConfigOption(ctx context.Context, sessionID, configID, valueID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started || c.conn == nil {
		return fmt.Errorf("agent not started")
	}

	_, err := c.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: acp.SessionId(sessionID),
			ConfigId:  acp.SessionConfigId(configID),
			Value:     acp.SessionConfigValueId(valueID),
		},
	})
	if err != nil {
		return fmt.Errorf("set config option: %w", err)
	}
	return nil
}

// SetSessionConfigOptionBoolean 设置会话配置项（boolean 型开关）。
func (c *AgentConnection) SetSessionConfigOptionBoolean(ctx context.Context, sessionID, configID string, value bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started || c.conn == nil {
		return fmt.Errorf("agent not started")
	}

	_, err := c.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
		Boolean: &acp.SetSessionConfigOptionBoolean{
			SessionId: acp.SessionId(sessionID),
			ConfigId:  acp.SessionConfigId(configID),
			Type:      "boolean",
			Value:     value,
		},
	})
	if err != nil {
		return fmt.Errorf("set config option: %w", err)
	}
	return nil
}

// Close 关闭 agent 连接。
func (c *AgentConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.procCancel != nil {
		c.procCancel()
		c.procCancel = nil
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	c.started = false
	return nil
}

// logWriter 将 agent stderr 输出转发到 slog。
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
