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
	"github.com/zacp/zacp/internal/config"
)

// Manager 管理多个 agent 连接和 session。
type Manager struct {
	log         *slog.Logger
	registry    *providers.ProviderRegistry
	autoApprove bool
	defaultCwd  string
	idleTimeout time.Duration

	mu     sync.Mutex
	agents map[string]*AgentConnection // agentID -> connection

	// 空闲回收器控制通道；idleTimeout <= 0 时不启动
	stopReaper chan struct{}
	reaperDone chan struct{}
}

// Config 管理器配置。
type Config struct {
	// Registry 是 Provider 注册表。
	Registry *providers.ProviderRegistry
	// AutoApprove 是否自动批准权限请求。
	AutoApprove bool
	// DefaultCwd 是默认工作目录。
	DefaultCwd string
	// IdleTimeout 空闲回收超时：agent 超过该时长无活跃操作（且无进行中 prompt）
	// 即被停止以释放内存；<=0 表示禁用空闲回收。
	IdleTimeout time.Duration
}

// New 创建 Manager（不启动任何 agent）。
func New(log *slog.Logger, cfg Config) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if cfg.DefaultCwd == "" {
		cfg.DefaultCwd = "."
	}
	m := &Manager{
		log:         log,
		registry:    cfg.Registry,
		autoApprove: cfg.AutoApprove,
		defaultCwd:  cfg.DefaultCwd,
		idleTimeout: cfg.IdleTimeout,
		agents:      make(map[string]*AgentConnection),
	}

	// 启用空闲回收：后台定时扫描（固定 5 分钟间隔；扫描只依赖 lastUsed
	// 时间戳，频率只影响回收延迟，最坏多等 5 分钟）。
	if m.idleTimeout > 0 {
		m.stopReaper = make(chan struct{})
		m.reaperDone = make(chan struct{})
		go m.reapLoop(m.stopReaper, m.reaperDone)
	}

	return m
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

// EnsureStarted 确保 agent 已启动（幂等）：已运行则 no-op，未启动则按需启动。
// 与 acquireAgent 的区别：不标记活跃、不重试——用于「事件回调注册 / 配置下发」
// 等非 prompt 操作的前置保证（这些操作本身不参与空闲活跃度计数）。
func (m *Manager) EnsureStarted(ctx context.Context, agentID string) error {
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()
	if exists {
		conn.mu.Lock()
		running := conn.started
		conn.mu.Unlock()
		if running {
			return nil
		}
	}
	return m.StartAgent(ctx, agentID)
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

// SetAgentEnabled 设置页开关的运行时热更新（不落盘，文件写入由调用方负责）：
//   - 启用：按配置创建 Provider 注册进 registry，之后前端可立即创建会话
//   - 停用：先从 registry 移除注册，再停止该 agent 已启动的进程（若有）
//
// 与 ListAgents/CreateSession 等共享 m.mu，保证注册表与连接状态一致。
func (m *Manager) SetAgentEnabled(cfg config.AgentConfig, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if enabled {
		p, err := providers.NewFromConfig(cfg)
		if err != nil {
			return fmt.Errorf("enable agent '%s': %w", cfg.ID, err)
		}
		m.registry.Add(p)
		m.log.Info("agent enabled (hot)", "agent", cfg.ID)
		return nil
	}

	// 停用：先停进程再移除注册
	if conn, exists := m.agents[cfg.ID]; exists {
		if err := conn.Close(); err != nil {
			return fmt.Errorf("stop agent '%s': %w", cfg.ID, err)
		}
		delete(m.agents, cfg.ID)
	}
	m.registry.Remove(cfg.ID)
	m.log.Info("agent disabled (hot)", "agent", cfg.ID)
	return nil
}

// reapLoop 空闲回收主循环：固定 5 分钟扫描一次，直到 stop 关闭。
func (m *Manager) reapLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			m.recycleIdle()
		}
	}
}

// recycleIdle 回收超过 idleTimeout 未活跃且当前无进行中 prompt 的 agent。
// 回收 = 杀掉 agent 进程并移除连接；DB 中该 agent 的 session 会在下次
// 使用时经 unknown-session 自动恢复逻辑（RecoverSession）重建。
func (m *Manager) recycleIdle() {
	type victim struct {
		id   string
		conn *AgentConnection
	}

	// 第一步：在 m.mu 持锁下「原子摘除」候选（从 map 移除并标记未启动）。
	// 与 acquireAgent 互斥：摘除后新的 Prompt 只能按需重启新进程，
	// 绝不会拿到即将被杀的连接（消除“prompt 进行中被回收”的竞态窗口）。
	var victims []victim
	m.mu.Lock()
	now := time.Now()
	for id, conn := range m.agents {
		conn.mu.Lock()
		if conn.started && conn.activePrompts == 0 && now.Sub(conn.lastUsed) > m.idleTimeout {
			conn.started = false
			victims = append(victims, victim{id: id, conn: conn})
		}
		conn.mu.Unlock()
	}
	for _, v := range victims {
		delete(m.agents, v.id)
	}
	m.mu.Unlock()

	// 第二步：摘除后再真正杀进程（不持锁，避免长时间阻塞其它操作）。
	// 注：摘除与杀进程之间，acquireAgent 可能已按需启动新进程，
	// 新旧进程会短暂并存（stdio 连接无端口冲突，旧进程关闭后即消失）。
	for _, v := range victims {
		if err := v.conn.Close(); err != nil {
			m.log.Warn("failed to recycle idle agent", "agent", v.id, "err", err)
			continue
		}
		m.log.Info("recycled idle agent", "agent", v.id, "idleTimeout", m.idleTimeout.String())
	}
}

// CreateSession 在指定 agent 上创建新 session，返回 session ID 与 agent 下发的配置项
// （模型/思考强度/mode 等，来自 session/new 响应的 configOptions）。
func (m *Manager) CreateSession(ctx context.Context, agentID, cwd string) (string, []acp.SessionConfigOption, error) {
	// conn 与 provider 须在同一把锁内取得：设置页热更新（SetAgentEnabled）
	// 会在锁内写 registry，锁外读会与热更新写产生 data race。
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	provider, _ := m.registry.Get(agentID)
	m.mu.Unlock()

	if !exists {
		return "", nil, fmt.Errorf("agent '%s' not started", agentID)
	}

	if cwd == "" && provider != nil {
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
// 不同 agent 对「会话失效」返回的错误文本不同，这里统一做大小写不敏感匹配：
//   - reasonix 等：`session/xxx: unknown session <uuid>`
//   - qoder 等：`{"code":-32603,...,"details":"Session not found: <uuid>"}`
// ws bridge（prompt 路径）与 service（config-options 路径）共用此判断触发自动恢复。
func IsUnknownSessionErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown session") || strings.Contains(msg, "session not found")
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

	// load 失败：新建 ACP session（沿用 cwd；空时回退 provider 默认工作区）。
	// provider 须在锁内取得（避免与热更新写 registry 产生 data race）；
	// conn 已在上方同锁取得，若此时已被热更新停用，下方 CreateSession 会报错返回。
	m.mu.Lock()
	provider, _ := m.registry.Get(agentID)
	m.mu.Unlock()
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

// acquireAgent 获取 agent 连接并将本次调用计入活跃（activePrompts+1）。
// 与空闲回收器互斥的关键：取连接与活跃标记在同一把 m.mu 持锁下完成，
// 回收器也在持锁下原子摘除，二者不会交错——要么本调用先标记活跃使
// 回收器跳过，要么回收器先摘除使本调用走按需重启，不存在拿到半死连接。
func (m *Manager) acquireAgent(ctx context.Context, agentID string) (*AgentConnection, error) {
	for attempt := 0; attempt < 2; attempt++ {
		m.mu.Lock()
		if conn, ok := m.agents[agentID]; ok {
			// started 必须持 conn.mu 读：cmd.Wait goroutine 只持 conn.mu 写它
			conn.mu.Lock()
			running := conn.started
			if running {
				conn.activePrompts++
				conn.lastUsed = time.Now()
			}
			conn.mu.Unlock()
			if running {
				m.mu.Unlock()
				return conn, nil
			}
		}
		m.mu.Unlock()

		if attempt == 0 {
			// 未启动（含刚被回收）：按需启动后重试一次。
			if err := m.StartAgent(ctx, agentID); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("agent '%s' unavailable after start", agentID)
	}
	return nil, fmt.Errorf("agent '%s' unavailable", agentID)
}

// releaseAgent 释放 acquireAgent 取得的活跃标记。
func (m *Manager) releaseAgent(conn *AgentConnection) {
	conn.mu.Lock()
	conn.activePrompts--
	conn.lastUsed = time.Now()
	conn.mu.Unlock()
}

// Prompt 向指定 session 发送消息。
func (m *Manager) Prompt(ctx context.Context, agentID, sessionID, message string) (*PromptResult, error) {
	// 兜底按需启动：空闲回收后用户可能持有旧引用直接发消息，
	// 此时 agent 进程已停，acquireAgent 会自动重启（已启动时为幂等 no-op）。
	conn, err := m.acquireAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	defer m.releaseAgent(conn)

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

// Close 停止空闲回收器并关闭所有 agent 连接。
func (m *Manager) Close() error {
	m.mu.Lock()
	// 先发停止信号：回收器可能正阻塞在 m.mu 上，若持锁等待 reaperDone
	// 会死锁，因此 stop 信号必须在持锁期间发出，等锁释放后回收器才能退出。
	if m.stopReaper != nil {
		close(m.stopReaper)
		m.stopReaper = nil
	}
	reaperDone := m.reaperDone
	m.reaperDone = nil

	var errs []error
	for id, conn := range m.agents {
		if err := conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close agent '%s': %w", id, err))
		}
	}
	m.agents = make(map[string]*AgentConnection)
	m.mu.Unlock()

	// 锁已释放，此时等待回收器退出不会死锁
	if reaperDone != nil {
		<-reaperDone
	}

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

	mu             sync.Mutex
	cmd            *exec.Cmd
	conn           *acp.ClientSideConnection
	stdin          io.WriteCloser
	currentSession *SessionState
	started        bool
	procCancel     context.CancelFunc
	promptMu       sync.Mutex // 序列化 prompt 调用

	// 空闲回收用：lastUsed 为最后一次活跃操作时间，activePrompts 为进行中的 prompt 数
	lastUsed      time.Time
	activePrompts int
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
		// 初始视为活跃：刚启动的连接不应被立即回收
		lastUsed: time.Now(),
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

	// 会话创建属于活跃操作：先刷新空闲计时再开始耗时操作，
	// 避免长耗时创建过程中被空闲回收器摘除
	c.lastUsed = time.Now()

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

	// 会话恢复属于活跃操作：先刷新空闲计时再开始耗时操作
	c.lastUsed = time.Now()

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

	// 配置切换属于活跃操作，刷新空闲计时
	c.lastUsed = time.Now()

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

	// 配置切换属于活跃操作，刷新空闲计时
	c.lastUsed = time.Now()

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
