// Package manager 管理多个 Agent 进程和多个 ACP Session。
// 核心设计：
// - Manager 管理多个 AgentConnection（每个 agent ID 一个进程）
// - 每个 AgentConnection 可以管理多个 Session
// - 对外暴露：启动/停止 agent、创建/加载 session、发送 prompt、取消等
package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	acpclient "github.com/zacp/zacp/internal/acp/client"
	"github.com/zacp/zacp/internal/acp/providers"
	"github.com/zacp/zacp/internal/config"
	"github.com/zacp/zacp/internal/model"
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

	// promptStartedHook 由外部（EventBridge）注入：agent 连接每次真正开始执行
	// prompt（排队门闩获取成功）时回调，用于按会话注册事件回调——排队期间
	// 不注册，执行中会话的回调不会被后到的 prompt 覆盖（见 ws.EventBridge）。
	promptStartedHook func(agentID, sessionID string)

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

// SetPromptStartedHook 注入「prompt 开始执行」钩子（ws.EventBridge 组装时调用，
// 内部实现为 SetupEventCallback 的按会话注册）。持 m.mu 写入，StartAgent 读，无竞态。
func (m *Manager) SetPromptStartedHook(fn func(agentID, sessionID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promptStartedHook = fn
}

// ErrPromptCancelled 排队中的 prompt 被用户取消（撤销排队）。
// 与「正在执行的 prompt 被 ACP cancel」不同：撤销排队时 agent 尚未收到任何内容，
// 调用方（ws bridge）据此广播 turn.done(cancelled) 复位前端「排队中」状态，不报错。
var ErrPromptCancelled = errors.New("prompt cancelled while queued")

// IsPromptCancelledErr 判断错误是否为排队撤销。
func IsPromptCancelledErr(err error) bool {
	return errors.Is(err, ErrPromptCancelled)
}

// promptGate 可取消排队门闩：同一 AgentConnection 的 prompt 串行执行，
// 后续 prompt 进入 FIFO 等待队列；等待者可通过 context 取消撤销排队。
// 锁在持有者之间「传递」：release 唤醒队首（locked 保持 true），队首被取消时
// 继续唤醒下一个，队列清空才置 locked=false——因此任意时刻只有一人持有。
type promptGate struct {
	mu     sync.Mutex
	locked bool
	queue  []chan struct{}
}

// acquire 获取门闩；ctx 取消时撤销排队并返回 ctx.Err()（不持有门闩）。
func (g *promptGate) acquire(ctx context.Context) (func(), error) {
	g.mu.Lock()
	if !g.locked {
		g.locked = true
		g.mu.Unlock()
		return g.release, nil
	}
	ch := make(chan struct{})
	g.queue = append(g.queue, ch)
	g.mu.Unlock()

	select {
	case <-ch:
		// 轮到自己：release 已把队首（自己）移出队列并 close，锁已传递，无需再竞争。
		// 但 ch 与 ctx.Done() 可能同时就绪而被随机选中——若调用方已取消，
		// 不得把已取消的排队提升为执行者，锁必须继续传给下一个。
		if ctx.Err() != nil {
			g.mu.Lock()
			g.passLockLocked()
			g.mu.Unlock()
			return nil, ctx.Err()
		}
		return g.release, nil
	case <-ctx.Done():
		// 撤销排队：从队列移除自己。若队列中找不到自己，说明 release 已把
		// 锁传给自己（队首被移出并 close）而自己选择放弃——此时必须继续把锁
		// 传给下一个等待者（或解锁），否则队列卡死。
		g.mu.Lock()
		found := false
		for i, c := range g.queue {
			if c == ch {
				g.queue = append(g.queue[:i], g.queue[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			// 锁已传给自己但放弃：必须把锁传给下一个等待者（pop+close，
			// 与 release 对称——被唤醒者已在队列之外，其 release 不会重复 close），
			// 队列为空则解锁。
			g.passLockLocked()
		}
		g.mu.Unlock()
		return nil, ctx.Err()
	}
}

// passLockLocked 在持有 g.mu 的前提下把锁传给下一个等待者（队首 pop+close），
// 队列为空则解锁。release 与「唤醒后放弃」的取消分支共用。
func (g *promptGate) passLockLocked() {
	if len(g.queue) > 0 {
		ch := g.queue[0]
		g.queue = g.queue[1:]
		close(ch)
	} else {
		g.locked = false
	}
}

// release 释放门闩：把队首移出队列并唤醒（锁随之传递到队首，locked 保持 true）；
// 队列为空时解锁。队首元素被移出后其 ch 恰好被 close 一次，不会重复 close。
func (g *promptGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.passLockLocked()
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
	// 注入「prompt 开始执行」钩子：排队门闩获取成功（真正执行）时才注册
	// 该会话的事件回调；排队期间不注册，执行中会话的回调不被覆盖。
	// 锁内快照 hook（StartAgent 持 m.mu 写、此处读，避免热重载期间的竞态）。
	conn.onPromptStarted = func(sessionID string) {
		m.mu.Lock()
		fn := m.promptStartedHook
		m.mu.Unlock()
		if fn != nil {
			fn(agentID, sessionID)
		}
	}
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

// ReplaySessionConfigOptions 在 ACP 会话重建后回放用户配置（model/mode/thinking 等）。
// 重建 = 全新 ACP 会话，agent 侧配置回到默认值（omp 等把配置存在 session 文件里，
// load 成功时随上下文原样恢复，无需回放；此处兜底 load 失败/不支持 load 的 agent）。
// 按 DB 存档的 ConfigOptionDTO 数组逐个 set：select 型用 currentValue（值 id），
// boolean 型用布尔值；按存档顺序执行（model 切换可能影响 thinking 等选项的可用性）。
// 逐项尽力而为：单项失败只打 WARN，不阻断会话继续；回放成功后 agent 会经
// config_options_update 通告新值，由上层落库，DB 与 agent 自动保持一致。
// 供 ws bridge 与 REST service 两条恢复路径共用。
func (m *Manager) ReplaySessionConfigOptions(ctx context.Context, agentID, sessionID, configOptionsJSON string) {
	if configOptionsJSON == "" {
		return
	}
	var options []model.ConfigOptionDTO
	if err := json.Unmarshal([]byte(configOptionsJSON), &options); err != nil {
		m.log.Warn("parse stored config options for replay", "err", err)
		return
	}
	m.replayOptions(ctx, agentID, sessionID, options)
}

// replayOptions 逐项 set 配置（select/boolean 分支），尽力而为：单项失败只打 WARN，
// 不阻断会话继续。ReplaySessionConfigOptions（重建后全量回放）与
// reconcileAfterLoad（load 后差异回放）共用。
func (m *Manager) replayOptions(ctx context.Context, agentID, sessionID string, options []model.ConfigOptionDTO) {
	for _, opt := range options {
		switch opt.Type {
		case "boolean":
			v, ok := opt.CurrentValue.(bool)
			if !ok {
				continue
			}
			if err := m.SetSessionConfigOptionBoolean(ctx, agentID, sessionID, opt.ID, v); err != nil {
				m.log.Warn("replay config option failed",
					"agent", agentID, "sessionId", sessionID, "configId", opt.ID, "err", err)
			}
		default: // select 型
			v, ok := opt.CurrentValue.(string)
			if !ok || v == "" {
				continue
			}
			if err := m.SetSessionConfigOption(ctx, agentID, sessionID, opt.ID, v); err != nil {
				m.log.Warn("replay config option failed",
					"agent", agentID, "sessionId", sessionID, "configId", opt.ID, "value", v, "err", err)
			}
		}
	}
}

// reconcileAfterLoad 在 session/load 成功后核对配置：load 恢复的是「会话文件快照」，
// 若文件是历史重建产物/损坏快照，其配置可能与用户最后选择（DB 存档）不一致
//（典型：修复前重建的会话，文件里是重建时的默认模型，DB 里是用户选的模型）。
// 以 DB 存档为权威：逐项比较 currentValue，不一致的才回放（正常会话零操作）；
// 回放成功后 agent 会把新值写回会话文件（如 omp 的 model_change），一次性自愈。
func (m *Manager) reconcileAfterLoad(ctx context.Context, agentID, sessionID string, loaded []acp.SessionConfigOption, storedConfigJSON string) {
	if storedConfigJSON == "" || len(loaded) == 0 {
		return
	}
	var stored []model.ConfigOptionDTO
	if err := json.Unmarshal([]byte(storedConfigJSON), &stored); err != nil {
		m.log.Warn("parse stored config options for reconcile", "err", err)
		return
	}

	// load 返回的配置 → id → currentValue 映射
	loadedVals := make(map[string]any, len(loaded))
	for _, opt := range loaded {
		switch {
		case opt.Select != nil:
			loadedVals[string(opt.Select.Id)] = string(opt.Select.CurrentValue)
		case opt.Boolean != nil:
			loadedVals[string(opt.Boolean.Id)] = opt.Boolean.CurrentValue
		}
	}

	// 逐项对账：只比 currentValue；agent 当前没有的选项（版本差异）跳过
	diff := configDiff(loadedVals, stored)
	if len(diff) == 0 {
		return
	}

	m.log.Info("config mismatch after load, replaying stored config",
		"agent", agentID, "sessionId", sessionID, "count", len(diff))
	m.replayOptions(ctx, agentID, sessionID, diff)
}

// configDiff 比较 load 返回的配置（loadedVals：id → currentValue）与 DB 存档
//（stored：用户最后选择的配置，权威），返回 currentValue 不一致、需要回放的项。
// agent 当前没有的选项（版本差异）跳过；一致项不返回。
func configDiff(loadedVals map[string]any, stored []model.ConfigOptionDTO) []model.ConfigOptionDTO {
	var diff []model.ConfigOptionDTO
	for _, opt := range stored {
		lv, ok := loadedVals[opt.ID]
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", lv) == fmt.Sprintf("%v", opt.CurrentValue) {
			continue
		}
		diff = append(diff, opt)
	}
	return diff
}

// LoadSession 恢复已有 session（用于会话恢复）。
// cwd 必须与创建该 session 时的工作区一致：agent（如 omp）按 cwd 定位
// 磁盘会话文件，漏传或传错会 load 失败（见 AgentConnection.LoadSession）。
func (m *Manager) LoadSession(ctx context.Context, agentID, sessionID, cwd string) error {
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("agent '%s' not started", agentID)
	}

	return conn.LoadSession(ctx, acp.SessionId(sessionID), cwd)
}

// uuidLikeRe 匹配 uuid 或 uuid 的十六进制前缀（如 "019fd945" / "019fd945-ff8c-..."），
// 作为「错误文本里确实带 session id」的证据，用于收窄会话失效判定。
var uuidLikeRe = regexp.MustCompile(`[0-9a-f]{8,}`)

// IsUnknownSessionErr 判断 ACP 错误是否为「agent 侧会话不存在/已失效」。
// 后端或 agent 重启后，DB 中记录的 acp_session_id 在 agent 内存中已丢失，
// 不同 agent 对「会话失效」返回的错误文本不同，这里统一做大小写不敏感匹配：
//   - omp（pi 系）：`{"code":-32603,...,"details":"Unsupported ACP session: <uuid>"}`
//   - reasonix 等：`session/<uuid>: unknown session <uuid>`
//   - qoder 等：`{"code":-32603,...,"details":"Session not found: <uuid>"}`
//
// 判定前置条件：错误文本必须同时含 "session" 与 session id（uuid / hex 前缀）。
// 已知 agent 的失效错误都带 id，要求 id 共现可排除无 id 的误伤文本，例如：
//   - "unsupported session config option: foo"（会话正常，参数问题）
//   - "write /workspace/session.json: no such file or directory"（文件路径）
//
// 误判代价可控：至多触发一次 session/load（失败则重建）重试；对支持 load 的
// agent（如 omp 可从磁盘恢复）误判也只是多一次无害的 load + 重试。
//
// ws bridge（prompt 路径）与 service（config-options 路径）共用此判断触发自动恢复。
func IsUnknownSessionErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "session") || !uuidLikeRe.MatchString(msg) {
		return false
	}
	// 失效语义词：与 "session" + session id 组合出现即视为会话失效，
	// 组合判断可覆盖未来新增 agent 的措辞变体，不必每接入一个 agent 改白名单。
	// "invalid" 不纳入：语义过宽（如 "invalid session config option: <uuid>" 是
	// 参数错误而非会话失效），误判会触发 RecoverSession 重建会话、静默丢失上下文。
	for _, word := range []string{
		"unknown",
		"not found",
		"unsupported",
		"not exist",
		"doesn't exist",
		"no such",
		"not recognized",
	} {
		if strings.Contains(msg, word) {
			return true
		}
	}
	return false
}

// RecoverSession 恢复 agent 侧已失效的 ACP session（unknown session 时调用）：
//  1. 优先 ACP session/load：agent 支持持久化会话时（omp/reasonix 等），可原样恢复对话上下文。
//     cwd 必须与创建时一致（agent 按 cwd 定位磁盘会话文件），空时回退 provider 默认工作区。
//  2. 失败则 session/new 重建（上下文丢失，但会话可继续使用）
//
// 返回最终可用的 ACP session id，以及是否发生了重建（load 成功时 id 不变、rebuilt=false）。
// 调用方在 rebuilt=true 时需要把新 id 更新到 DB，并（如适用）重新绑定事件回调。
// storedConfigJSON 是 DB 里该会话的 config_options 存档（用户最后选择的配置，权威），
// load 成功后据其对账（见 reconcileAfterLoad），修复「文件快照与用户选择不一致」的历史错位。
func (m *Manager) RecoverSession(ctx context.Context, agentID, oldAcpID, cwd, storedConfigJSON string) (string, bool, error) {
	// conn 与 provider 在同一临界区取得，避免热更新间隙错配
	//（旧 conn + 新 provider 的 cwd 语义，见 CreateSession 同款注释）。
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	var provider *providers.Provider
	if cwd == "" {
		provider, _ = m.registry.Get(agentID)
	}
	m.mu.Unlock()
	if !exists {
		return "", false, fmt.Errorf("agent '%s' not started", agentID)
	}

	// 空 cwd 时先解析默认工作区再 load：provider 已在锁内取得
	//（避免与热更新写 registry 产生 data race）。
	if provider != nil {
		cwd = provider.ResolveCwd(m.defaultCwd)
	}

	if err := conn.LoadSession(ctx, acp.SessionId(oldAcpID), cwd); err == nil {
		m.log.Info("acp session recovered via load", "agent", agentID, "sessionId", oldAcpID, "cwd", cwd)
		// load 恢复的是会话文件快照；若文件是历史重建产物/损坏快照，其配置可能与
		// 用户最后选择（DB 存档）不一致（如会话 276 修复前重建后文件里是 gpt-5.5
		// 而 DB 是 Luna）。以 DB 为权威对账，差异项回放（omp 会把新值写回文件，
		// 一次性自愈；正常会话对账一致，零操作）。
		m.reconcileAfterLoad(ctx, agentID, oldAcpID, conn.LoadedConfigOptions(), storedConfigJSON)
		return oldAcpID, false, nil
	} else {
		// load 失败不能静默吞掉：原因不同（cwd 不匹配 / agent 不支持 load /
		// 磁盘会话文件已清理）修复手段不同，打 WARN 便于诊断。
		m.log.Warn("acp session load failed, will recreate",
			"agent", agentID, "sessionId", oldAcpID, "cwd", cwd, "err", err)
	}

	// load 失败：新建 ACP session（沿用 cwd；空时回退 provider 默认工作区）。
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
	promptGate     promptGate // 同 agent prompt 串行门闩（可取消排队）

	// 排队撤销：queuedCancels 记录「等待门闩中」的 prompt 的取消函数（按会话），
	// Cancel 先查这里撤销排队（不向 agent 发任何内容），再发 ACP cancel（针对正在执行的 prompt）
	cancelMu      sync.Mutex
	queuedCancels map[acp.SessionId]context.CancelFunc

	// onPromptStarted 在排队门闩获取成功（本会话真正开始执行）后调用，
	// 由 EventBridge 注入：此时才注册该会话的事件回调，保证排队期间
	// 执行中会话的事件仍按原会话路由（修复旧「每次 prompt 前覆盖注册」的串台）。
	onPromptStarted func(sessionID string)

	// 空闲回收用：lastUsed 为最后一次活跃操作时间，activePrompts 为进行中的 prompt 数
	lastUsed      time.Time
	activePrompts int
}

// SessionState 单个 session 的状态。
type SessionState struct {
	ID      acp.SessionId
	Cwd     string
	Created time.Time
	// ConfigOptions 是最近一次 session/load 返回的配置快照，供 load 后对账
	//（见 reconcileAfterLoad：文件快照可能与 DB 存档不一致）。
	ConfigOptions []acp.SessionConfigOption
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
		lastUsed:      time.Now(),
		queuedCancels: make(map[acp.SessionId]context.CancelFunc),
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
		// 仅当 c.cmd 仍是本 goroutine 启动的进程时才清理（compare-and-clear）：
		// 清理不得清掉更新的进程状态（如进程退出后、抢到锁前已按需重启的新进程），
		// 否则新进程会变成 started=false、无法被 Close 杀掉的孤儿，并引发反复重启。
		if c.cmd == cmd {
			c.started = false
			// 进程已退出：底层 ACP 连接必然失效，进程内存中的 session 也随进程丢失。
			// 清空引用，避免 ListAgents 把已死的 session 当活跃展示，也避免后续
			// 操作拿到悬挂的 conn/stdin；重启后旧 session id 在 agent 内存中不存在，
			// Prompt 返回 unknown/unsupported session 错误，由 ws bridge / service 的
			// 恢复逻辑（IsUnknownSessionErr + RecoverSession）自动 load 或重建。
			c.currentSession = nil
			c.conn = nil
			c.stdin = nil
			c.cmd = nil
			c.procCancel = nil
		}
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
// cwd 必须与创建该 session 时的工作区一致：agent（如 omp）的 session/load 按
// cwd 定位磁盘会话文件（cwd 映射到 workspace 目录，session 文件存在其下），
// 漏传/传错会在「找不到会话」与「工作区不匹配」之间摇摆，导致恢复永远失败、
// 每次都走重建（丢失上下文与用户配置）。空 cwd 由调用方（RecoverSession）先解析。
func (c *AgentConnection) LoadSession(ctx context.Context, sessionID acp.SessionId, cwd string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started || c.conn == nil {
		return fmt.Errorf("agent not started")
	}

	// 会话恢复属于活跃操作：先刷新空闲计时再开始耗时操作
	c.lastUsed = time.Now()

	res, err := c.conn.LoadSession(ctx, acp.LoadSessionRequest{
		SessionId: sessionID,
		Cwd:       cwd,
		// MCP 服务器列表必须是非 nil 空数组：SDK Validate 要求非 nil，
		// nil 会序列化为 JSON null，omp 等 agent 内部对 null 调用 .length 直接崩溃
		//（表现为 load 永远失败、每次都走重建丢上下文）。
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	c.currentSession = &SessionState{
		ID:            sessionID,
		Cwd:           cwd,
		Created:       time.Now(),
		ConfigOptions: res.ConfigOptions,
	}

	c.log.Info("session loaded", "agent", c.provider.ID, "sessionId", sessionID, "cwd", cwd)
	return nil
}

// LoadedConfigOptions 返回最近一次 session/load 恢复的配置快照（锁内读，供对账）。
func (c *AgentConnection) LoadedConfigOptions() []acp.SessionConfigOption {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.currentSession == nil {
		return nil
	}
	return c.currentSession.ConfigOptions
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
// 同 agent 的 prompt 经 promptGate 串行：后续 prompt 进入 FIFO 排队，
// 排队期间不向 agent 发送任何内容（agent 端始终只有一个 turn 在执行）。
// 排队中的 prompt 可被 Cancel 撤销（按会话，不向 agent 发任何东西）；
// 已开始执行的 turn 由 ACP cancel 中断。
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

	// 排队等待门闩用 qctx：可被 Cancel 触发取消（撤销排队）。
	// 执行阶段仍用原 ctx——排队撤销只影响「等待」，不误伤已开始的 turn
	// （取消执行中 turn 走下方 ACP cancel 路径）。
	qctx, qcancel := context.WithCancel(ctx)
	defer qcancel()
	c.cancelMu.Lock()
	c.queuedCancels[sessionID] = qcancel
	c.cancelMu.Unlock()

	release, err := c.promptGate.acquire(qctx)

	c.cancelMu.Lock()
	delete(c.queuedCancels, sessionID)
	c.cancelMu.Unlock()

	if err != nil {
		// 排队期间被取消（撤销排队）：agent 尚未收到任何内容，
		// 返回可识别错误，调用方据此广播 turn.done(cancelled) 复位前端「排队中」
		return nil, fmt.Errorf("%w: session %s", ErrPromptCancelled, sessionID)
	}
	defer release()

	// 真正开始执行：通知外部（EventBridge）注册本会话的事件回调，
	// 使后续事件/turn.done 按本会话路由（排队期间不覆盖其它会话的回调）。
	if c.onPromptStarted != nil {
		c.onPromptStarted(string(sessionID))
	}

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

// Cancel 取消 prompt：排队中的先撤销排队（不向 agent 发任何内容），
// 正在执行的发 ACP cancel 中断。两步都做（幂等），任意状态都能取消。
func (c *AgentConnection) Cancel(ctx context.Context, sessionID acp.SessionId) error {
	// 第一步：撤销排队（若该会话的 prompt 仍在等待门闩）。触发后等待中的
	// Prompt 会从队列移除并返回 ErrPromptCancelled，不落任何内容。
	c.cancelMu.Lock()
	if cancel, ok := c.queuedCancels[sessionID]; ok {
		cancel()
	}
	c.cancelMu.Unlock()

	// 第二步：ACP cancel（针对正在执行的 prompt；对未执行的会话是 no-op，无害）
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
