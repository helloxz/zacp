package tty

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/helloxz/zacp/internal/model"
)

var (
	// ErrSessionLimit 表示进程级临时终端数量已达到上限。
	ErrSessionLimit = errors.New("tty session limit reached")
	// ErrInvalidSession 表示创建终端时缺少必要的工作区或 shell 信息。
	ErrInvalidSession = errors.New("invalid tty session")
)

// Manager 管理当前 zacp 进程内的临时 TTY Session。
// Session 不落库；Manager 的 map 是进程级资源上限和优雅关闭的唯一登记点。
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	max      int
	log      *slog.Logger
}

// NewManager 创建 TTY Manager。
func NewManager(log *slog.Logger, maxSessions int) *Manager {
	if maxSessions <= 0 {
		maxSessions = 1
	}
	return &Manager{
		sessions: make(map[string]*Session),
		max:      maxSessions,
		log:      log,
	}
}

// Create 登记一个尚未启动的 TTY Session。
// 调用方必须随后调用 Session.start；启动失败时由调用方 Close 回滚登记。
func (m *Manager) Create(ctx context.Context, workspace *model.Workspace, shellPath string, shellArgs []string) (*Session, error) {
	if workspace == nil || workspace.ID == 0 || workspace.Path == "" || shellPath == "" {
		return nil, ErrInvalidSession
	}
	if ctx == nil {
		ctx = context.Background()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= m.max {
		return nil, ErrSessionLimit
	}

	id := uuid.NewString()
	sessionCtx, cancel := context.WithCancel(ctx)
	session := newSession(m, sessionCtx, cancel, id, workspace, shellPath, shellArgs)
	m.sessions[id] = session
	return session, nil
}

// Count 返回当前登记的 TTY Session 数量。
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// CloseAll 关闭所有临时终端。
// 先复制快照再逐个关闭，避免持有 Manager 锁等待 Session 清理而死锁。
func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	for _, session := range sessions {
		if err := session.Close("manager shutdown"); err != nil && m.log != nil {
			m.log.Warn("failed to close tty session", "sessionID", session.ID(), "err", err)
		}
	}
}

func (m *Manager) String() string {
	return fmt.Sprintf("tty manager sessions=%d/%d", m.Count(), m.max)
}
