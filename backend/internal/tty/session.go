package tty

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	pty "github.com/aymanbagabas/go-pty"
	"github.com/helloxz/zacp/internal/model"
)

// SessionState 表示临时 TTY 的生命周期状态。
type SessionState string

const (
	SessionStarting SessionState = "starting"
	SessionRunning  SessionState = "running"
	SessionExited   SessionState = "exited"
	SessionClosing  SessionState = "closing"
	SessionClosed   SessionState = "closed"
	SessionError    SessionState = "error"
)

// Session 把一个 workspace、shell、PTY 和取消上下文绑定成一个临时终端。
// WebSocket 读写由同包 handler 负责；Session 只拥有 PTY/进程和关闭不变量。
type Session struct {
	manager      *Manager
	ctx          context.Context
	cancel       context.CancelFunc
	id           string
	workspaceID  uint
	workspaceDir string
	shellPath    string
	shellArgs    []string

	mu          sync.RWMutex
	state       SessionState
	pty         pty.Pty
	cmd         *pty.Cmd
	writeMu     sync.Mutex
	closeOnce   sync.Once
	done        chan struct{}
	processDone chan struct{}
	exitErr     error
}

func newSession(
	manager *Manager,
	ctx context.Context,
	cancel context.CancelFunc,
	id string,
	workspace *model.Workspace,
	shellPath string,
	shellArgs []string,
) *Session {
	return &Session{
		manager:      manager,
		ctx:          ctx,
		cancel:       cancel,
		id:           id,
		workspaceID:  workspace.ID,
		workspaceDir: workspace.Path,
		shellPath:    shellPath,
		shellArgs:    append([]string(nil), shellArgs...),
		state:        SessionStarting,
		done:         make(chan struct{}),
		processDone:  make(chan struct{}),
	}
}

// ID 返回短期终端 ID。
func (s *Session) ID() string { return s.id }

// WorkspaceID 返回 Session 所属工作区 ID。
func (s *Session) WorkspaceID() uint { return s.workspaceID }

// State 返回当前 Session 状态。
func (s *Session) State() SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Done 在 Session 完成清理后关闭。
func (s *Session) Done() <-chan struct{} { return s.done }

// ExitError 返回 shell 的退出错误；正常退出时为 nil。
func (s *Session) ExitError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.exitErr
}

// ExitCode 返回 shell 退出码；进程尚未结束或退出码不可用时返回 -1。
func (s *Session) ExitCode() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cmd == nil || s.cmd.ProcessState == nil {
		return -1
	}
	return s.cmd.ProcessState.ExitCode()
}

// start 创建 PTY 并启动 shell。必须由 TTY handler 在 Manager.Create 后调用。
func (s *Session) start() error {
	s.mu.Lock()
	if s.state != SessionStarting {
		s.mu.Unlock()
		return fmt.Errorf("start tty session %s: state=%s", s.id, s.state)
	}
	s.mu.Unlock()

	ptmx, err := pty.New()
	if err != nil {
		s.finish(SessionError, fmt.Errorf("create pty: %w", err), true)
		return err
	}

	cmd := ptmx.CommandContext(s.ctx, s.shellPath, s.shellArgs...)
	cmd.Dir = s.workspaceDir
	cmd.Env = terminalEnv()
	if err := cmd.Start(); err != nil {
		_ = ptmx.Close()
		s.finish(SessionError, fmt.Errorf("start shell %q: %w", s.shellPath, err), true)
		return err
	}

	s.mu.Lock()
	s.pty = ptmx
	s.cmd = cmd
	s.state = SessionRunning
	s.mu.Unlock()

	go s.waitProcess()
	return nil
}

func terminalEnv() []string {
	env := append([]string(nil), os.Environ()...)
	if runtime.GOOS == "windows" {
		return env
	}
	for _, item := range env {
		if strings.HasPrefix(item, "TERM=") {
			return env
		}
	}
	return append(env, "TERM=xterm-256color")
}

func (s *Session) waitProcess() {
	s.mu.RLock()
	cmd := s.cmd
	s.mu.RUnlock()
	if cmd == nil {
		s.finish(SessionError, errors.New("shell command missing"), true)
		return
	}

	err := cmd.Wait()
	s.mu.Lock()
	s.exitErr = err
	if s.state == SessionRunning {
		s.state = SessionExited
	}
	s.mu.Unlock()
	close(s.processDone)
}

func (s *Session) processDoneChan() <-chan struct{} {
	return s.processDone
}

// finalizeExit 在 PTY reader 读到最终 EOF 后关闭 master。
// 进程先退出、reader 后排空，避免关闭 PTY 时丢失 shell 的最后输出。
func (s *Session) finalizeExit() {
	s.mu.RLock()
	err := s.exitErr
	s.mu.RUnlock()
	s.finish(SessionExited, err, false)
}

// Read 从 PTY 读取原始终端输出。
func (s *Session) Read(buf []byte) (int, error) {
	s.mu.RLock()
	ptmx := s.pty
	state := s.state
	s.mu.RUnlock()
	if ptmx == nil {
		return 0, errors.New("tty pty is not started")
	}
	if state == SessionClosed || state == SessionError {
		return 0, errors.New("tty session is closed")
	}
	return ptmx.Read(buf)
}

// WriteInput 把浏览器发来的原始字节写入 PTY。
func (s *Session) WriteInput(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.RLock()
	ptmx := s.pty
	state := s.state
	s.mu.RUnlock()
	if ptmx == nil || (state != SessionRunning && state != SessionStarting) {
		return 0, errors.New("tty session is not running")
	}
	return ptmx.Write(data)
}

// Resize 更新 PTY 的行列尺寸；调用方负责先做协议范围校验。
func (s *Session) Resize(cols, rows int) error {
	s.mu.RLock()
	ptmx := s.pty
	state := s.state
	s.mu.RUnlock()
	if ptmx == nil || state != SessionRunning {
		return errors.New("tty session is not running")
	}
	if err := ptmx.Resize(cols, rows); err != nil {
		return fmt.Errorf("resize tty session %s to %dx%d: %w", s.id, cols, rows, err)
	}
	return nil
}

// Close 终止 shell、关闭 PTY 并从 Manager 移除 Session。
// 关闭操作幂等；连接断开、页面离开、进程退出和服务 shutdown 都走这里。
func (s *Session) Close(reason string) error {
	s.finish(SessionClosed, fmt.Errorf("tty session closed: %s", reason), true)
	return nil
}

func (s *Session) finish(final SessionState, exitErr error, terminate bool) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		if final == SessionClosed || final == SessionError {
			s.state = SessionClosing
		}
		cmd := s.cmd
		ptmx := s.pty
		s.exitErr = exitErr
		s.mu.Unlock()

		s.cancel()
		if terminate && cmd != nil {
			_ = terminateProcess(cmd)
		}
		if ptmx != nil {
			_ = ptmx.Close()
		}

		s.mu.Lock()
		s.state = final
		s.mu.Unlock()
		if s.manager != nil {
			s.manager.remove(s.id)
		}
		close(s.done)
	})
}
