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

	acpclient "github.com/helloxz/zacp/internal/acp/client"
	"github.com/helloxz/zacp/internal/acp/providers"
	"github.com/helloxz/zacp/internal/config"
	"github.com/helloxz/zacp/internal/model"
)

// Manager 管理多个 agent 连接和 session。
type Manager struct {
	log         *slog.Logger
	registry    *providers.ProviderRegistry
	autoApprove bool
	defaultCwd  string
	idleTimeout time.Duration

	mu     sync.Mutex
	agents map[string]*AgentConnection // agentID -> connection；一个连接可承载多个 ACP session

	// promptGate 是所有 Agent 共享的执行槽位：最多 3 个 prompt 同时进入 ACP，
	// 其余按到达顺序等待；等待上下文可由 Cancel 撤销。全局而非按 Agent，
	// 保证不同 Agent 的并发也计入同一个上限。
	promptGate promptGate
	promptMu   sync.Mutex
	prompts    map[promptKey]*promptEntry

	// starting 记录进行中的 agent 启动（StartAgent 并发去重与等待）。
	// 启动中的 agent 不在 agents 里，读者（ListAgents/GetAgentStatus/acquireAgent
	// 等）语义与「未启动」一致，无需适配。entry 的删除与 done 关闭只由
	// 发起者（第一个拿到占位的调用）完成；cancelStartingLocked 只取消不删除，
	// 保证等待者一定能等到结果，不会因 entry 被提前删掉而永久阻塞。
	starting map[string]*startEntry

	// closed 标记 Close 已调用：回填时若已关闭则回收刚启动的进程、不再写入
	// agents（防止 Close 清空 map 后发起者把连接写回，泄漏进程）。
	closed bool

	// promptStartedHook 由外部（EventBridge）注入：全局槽位获取成功、prompt
	// 即将发送到 ACP 时回调，用于注册按 session 路由的事件处理。
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
		starting:    make(map[string]*startEntry),
		promptGate:  newPromptGate(maxConcurrentPrompts),
		prompts:     make(map[promptKey]*promptEntry),
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
		if conn.lastSessionID != "" {
			status.SessionID = string(conn.lastSessionID)
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
			if conn.lastSessionID != "" {
				status.SessionID = string(conn.lastSessionID)
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

// cancelConfirmTimeout 是 ACP cancel 无确认时的 Agent 进程级强杀兜底。
// 保持为变量，便于测试缩短等待时间。
var cancelConfirmTimeout = 20 * time.Second

// ErrPromptCancelled 排队中的 prompt 被用户取消（撤销排队）。
// 与「正在执行的 prompt 被 ACP cancel」不同：撤销排队时 agent 尚未收到任何内容，
// 调用方（ws bridge）据此广播 turn.done(cancelled) 复位前端「排队中」状态，不报错。
var ErrPromptCancelled = errors.New("prompt cancelled while queued")

// ErrPromptInProgress 表示同一 ACP session 已有一轮 prompt 在执行或排队。
// 前端本身会禁用输入框，后端仍保留该保护，避免绕过 WebSocket 状态机后串联两轮。
var ErrPromptInProgress = errors.New("prompt already in progress")

const maxConcurrentPrompts = 3

type promptKey struct {
	agentID   string
	sessionID string
}

type promptEntry struct {
	cancelled bool
	started   bool
	cancel    context.CancelFunc
}

// promptWaiter 表示等待全局执行槽位的 prompt。
type promptWaiter struct {
	ch chan struct{}
}

// promptGate 是可取消的有界 FIFO 执行门闩。
// active 表示已获得槽位的 prompt 数；槽位释放时按队列顺序唤醒等待者。
type promptGate struct {
	mu       sync.Mutex
	capacity int
	active   int
	queue    []*promptWaiter
}

func newPromptGate(capacity int) promptGate {
	if capacity < 1 {
		capacity = 1
	}
	return promptGate{capacity: capacity}
}

// IsPromptCancelledErr 判断错误是否为排队撤销。
func IsPromptCancelledErr(err error) bool {
	return errors.Is(err, ErrPromptCancelled)
}

// HasPromptInProgress 报告指定 agent+ACP session 是否正处于 prompt 执行/排队中。
// 供 WS resync 使用：页面刷新/重连后据此恢复「正在执行」会话的实时流。
func (m *Manager) HasPromptInProgress(agentID, sessionID string) bool {
	m.promptMu.Lock()
	defer m.promptMu.Unlock()
	_, exists := m.prompts[promptKey{agentID: agentID, sessionID: sessionID}]
	return exists
}

// acquire 获取全局槽位；ctx 取消时从 FIFO 队列撤销，不占用槽位。
func (g *promptGate) acquire(ctx context.Context) (func(), error) {
	return g.acquireWithAdmission(ctx, nil)
}

// acquireWithAdmission 在请求已经登记为 FIFO 队列成员后调用 admitted。
// 这样后继请求可以及时登记并支持取消，同时槽位分配仍保持先来先得。
func (g *promptGate) acquireWithAdmission(ctx context.Context, admitted func()) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	g.mu.Lock()
	if g.active < g.capacity {
		g.active++
		g.mu.Unlock()
		if admitted != nil {
			admitted()
		}
		return g.release, nil
	}
	w := &promptWaiter{ch: make(chan struct{})}
	g.queue = append(g.queue, w)
	g.mu.Unlock()
	if admitted != nil {
		admitted()
	}

	select {
	case <-w.ch:
		// release 已把槽位计入 active 并移出队列。取消与唤醒同时到达时，
		// 不能让已取消的 prompt 占用槽位，必须立即转交给下一个等待者。
		if err := ctx.Err(); err != nil {
			g.release()
			return nil, err
		}
		return g.release, nil
	case <-ctx.Done():
		g.mu.Lock()
		found := false
		for i, queued := range g.queue {
			if queued == w {
				g.queue = append(g.queue[:i], g.queue[i+1:]...)
				found = true
				break
			}
		}
		g.mu.Unlock()
		if !found {
			// 槽位已经传给本 waiter，但 ctx 分支赢得了 select；归还槽位。
			g.release()
		}
		return nil, ctx.Err()
	}
}

// release 归还一个槽位，并按 FIFO 顺序唤醒尽可能多的等待者。
func (g *promptGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active > 0 {
		g.active--
	}
	for g.active < g.capacity && len(g.queue) > 0 {
		w := g.queue[0]
		g.queue = g.queue[1:]
		g.active++
		close(w.ch)
	}
}

// startEntry 记录一次进行中的 agent 启动：并发调用 StartAgent 时，发起者
// （第一个拿到占位的调用）在锁外执行握手，其余调用者等待 done 关闭后读取
// err；Close/StopAgent 通过 cancel 提前中止握手。只有发起者会写 err、删
// entry、close(done)——cancel 只取消握手，不做任何 map 操作。
type startEntry struct {
	done   chan struct{}
	err    error
	cancel context.CancelFunc
	// stopped 由 cancelStartingLocked 置位：握手可能恰好在取消生效前完成
	//（Initialize 已返回），回填时据此回收进程，避免「StopAgent 返回成功但
	// 进程仍在跑」的竞态。
	stopped bool
}

// StartAgent 启动指定 agent 进程（如果未启动）。
//
// 并发语义：同一 agent 同时只允许一次真实启动——后到的调用者等待进行中的
// 启动结果（成功返回 nil，失败返回同一错误），各自的 ctx 可独立超时退出。
//
// 关键设计：握手（进程拉起 + ACP Initialize）在 m.mu 之外执行。慢握手不再
// 独占全局锁，其它 agent 的 GetAgentStatus / CreateSession / ListAgents 等
// 读者操作不会被阻塞（修复「切到握手挂起的 agent 时全局卡顿」的根因）。
func (m *Manager) StartAgent(ctx context.Context, agentID string) error {
	// 第一步（锁内）：查注册、查已启动、占位。锁内路径都很短，不阻塞读者。
	m.mu.Lock()
	provider, ok := m.registry.Get(agentID)
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("agent '%s' not found", agentID)
	}
	if conn, exists := m.agents[agentID]; exists {
		conn.mu.Lock()
		running := conn.started
		conn.mu.Unlock()
		if running {
			m.mu.Unlock()
			return nil // 已启动
		}
	}
	// 已有启动在进行：锁外等待结果（等待期间绝不持 m.mu）。
	if entry, exists := m.starting[agentID]; exists {
		m.mu.Unlock()
		select {
		case <-entry.done:
			return entry.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	startCtx, cancel := context.WithCancel(ctx)
	entry := &startEntry{done: make(chan struct{}), cancel: cancel}
	m.starting[agentID] = entry
	// 创建连接并注入 prompt 开始及 Agent 强杀回调（锁内完成）。全局槽位
	// 获取成功后才注册会话事件；强杀时撤销该 Agent 仍在全局队列中的 prompt。
	conn := NewAgentConnection(m.log, provider, m.autoApprove)
	conn.onPromptStarted = func(sessionID string) {
		m.mu.Lock()
		fn := m.promptStartedHook
		m.mu.Unlock()
		if fn != nil {
			fn(agentID, sessionID)
		}
	}
	conn.onAgentForceKilled = func() {
		m.cancelQueuedPrompts(agentID)
	}
	m.mu.Unlock()

	// 第二步（锁外）：执行握手。失败时 conn.Start 内部会 kill 子进程防僵尸。
	err := conn.Start(startCtx)
	cancel() // 释放 startCtx 资源（幂等；Close/StopAgent 的 cancel 已含在内）

	// 第三步（锁内）：回填结果并唤醒等待者。
	m.mu.Lock()
	if m.starting[agentID] == entry {
		if err != nil {
			entry.err = fmt.Errorf("start agent '%s': %w", agentID, err)
		} else if m.closed || entry.stopped {
			// manager 已关闭，或启动期间被 StopAgent/SetAgentEnabled(false)
			// 请求停止（握手恰好在取消生效前完成）：回收刚启动的进程，
			// 不写回 agents，等待者拿到明确错误而非假成功。
			_ = conn.Close()
			entry.err = errors.New("agent start cancelled")
		} else {
			m.agents[agentID] = conn
		}
		delete(m.starting, agentID)
	} else {
		// entry 已不在 map（Close 清空过）：本次启动结果无处安放。
		// 握手成功时回收进程（防泄漏）；无论成败都给等待者明确错误，
		// 防止其拿到 nil 假成功（Close 必然 cancel，失败是常态路径）。
		if err == nil {
			_ = conn.Close()
		}
		entry.err = errors.New("manager closed")
	}
	// 无论 entry 是否仍属于自己（Close 清空过 map），done 都必须关闭：
	// 等待者与 Close 的收尾都依赖它，漏关会让等待者永久阻塞。
	close(entry.done)
	m.mu.Unlock()

	if err != nil {
		return fmt.Errorf("start agent '%s': %w", agentID, err)
	}
	return entry.err
}

// cancelStartingLocked 取消指定 agent 进行中的启动（调用方必须已持 m.mu）。
// 只取消不删除 entry：删除与 close(done) 由发起者负责，等待者才能收到结果。
// stopped 置位兜底「握手恰好在取消生效前完成」的窗口，见 startEntry 注释。
func (m *Manager) cancelStartingLocked(agentID string) {
	if entry, ok := m.starting[agentID]; ok {
		entry.stopped = true
		entry.cancel()
	}
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

	// 有进行中的启动则取消：握手 ctx 被取消后 conn.Start 快速失败并回收进程；
	// 发起者随后自行清理 entry，此处不等待（stop 请求应快速返回）。
	m.cancelStartingLocked(agentID)

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

	// 停用：先取消进行中的启动，再停进程、移除注册（同上：不等待发起者收尾）
	m.cancelStartingLocked(cfg.ID)
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
// （典型：修复前重建的会话，文件里是重建时的默认模型，DB 里是用户选的模型）。
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
// （stored：用户最后选择的配置，权威），返回 currentValue 不一致、需要回放的项。
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
		m.reconcileAfterLoad(ctx, agentID, oldAcpID, conn.LoadedConfigOptions(acp.SessionId(oldAcpID)), storedConfigJSON)
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

// Prompt 向指定 session 发送消息，并受全局三槽位 FIFO 调度控制。
func (m *Manager) Prompt(ctx context.Context, agentID, sessionID, message string) (*PromptResult, error) {
	return m.prompt(ctx, agentID, sessionID, message, nil)
}

// PromptWithAdmission 与 Prompt 相同；admitted 在请求登记进全局 FIFO 后调用，
// 供 WebSocket 入口按帧到达顺序放行下一条请求，避免前置落库耗时造成 FIFO 反转。
func (m *Manager) PromptWithAdmission(ctx context.Context, agentID, sessionID, message string, admitted func()) (*PromptResult, error) {
	return m.prompt(ctx, agentID, sessionID, message, admitted)
}

func (m *Manager) prompt(ctx context.Context, agentID, sessionID, message string, admitted func()) (*PromptResult, error) {
	key := promptKey{agentID: agentID, sessionID: sessionID}
	qctx, qcancel := context.WithCancel(ctx)
	entry := &promptEntry{cancel: qcancel}

	m.promptMu.Lock()
	if _, exists := m.prompts[key]; exists {
		m.promptMu.Unlock()
		qcancel()
		return nil, fmt.Errorf("%w: session %s", ErrPromptInProgress, sessionID)
	}
	m.prompts[key] = entry
	m.promptMu.Unlock()
	defer func() {
		m.promptMu.Lock()
		delete(m.prompts, key)
		m.promptMu.Unlock()
		qcancel()
	}()

	release, err := m.promptGate.acquireWithAdmission(qctx, admitted)
	if err != nil {
		return nil, fmt.Errorf("%w: session %s", ErrPromptCancelled, sessionID)
	}

	m.promptMu.Lock()
	if entry.cancelled || qctx.Err() != nil {
		m.promptMu.Unlock()
		release()
		return nil, fmt.Errorf("%w: session %s", ErrPromptCancelled, sessionID)
	}
	entry.started = true
	m.promptMu.Unlock()
	defer release()

	// 只有拿到全局槽位后才获取活跃 Agent；排队中的 prompt 不计入运行中状态。
	conn, err := m.acquireAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	defer m.releaseAgent(conn)
	return conn.Prompt(ctx, acp.SessionId(sessionID), message)
}

// Cancel 取消指定 session 的 prompt：排队中的撤销 FIFO 等待，执行中的发送 ACP cancel。
func (m *Manager) Cancel(ctx context.Context, agentID, sessionID string) error {
	key := promptKey{agentID: agentID, sessionID: sessionID}
	m.promptMu.Lock()
	entry, tracked := m.prompts[key]
	if tracked {
		entry.cancelled = true
		entry.cancel()
	}
	started := tracked && entry.started
	m.promptMu.Unlock()

	if tracked && !started {
		return nil
	}

	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()
	if !exists {
		if tracked {
			return nil
		}
		return fmt.Errorf("agent '%s' not started", agentID)
	}

	return conn.Cancel(ctx, acp.SessionId(sessionID))
}

// cancelQueuedPrompts 撤销指定 Agent 在全局 FIFO 中尚未取得槽位的 prompt。
// Agent 进程级强杀后，这些请求不能继续复用已经失效的连接。
func (m *Manager) cancelQueuedPrompts(agentID string) {
	m.promptMu.Lock()
	defer m.promptMu.Unlock()
	for key, entry := range m.prompts {
		if key.agentID == agentID && !entry.started {
			entry.cancelled = true
			entry.cancel()
		}
	}
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

// DeleteSession 删除指定 ACP session（agent 侧，ACP session/delete，UNSTABLE 能力）。
// 用于删除会话时同步清理 agent 侧会话数据；agent 未实现该能力时返回错误，
// 调用方应降级到 CloseSession（session/close）。
// 注意：仅作用于 ACP 协议层，不影响 agent 进程本身。
func (m *Manager) DeleteSession(ctx context.Context, agentID, sessionID string) error {
	m.mu.Lock()
	conn, exists := m.agents[agentID]
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("agent '%s' not started", agentID)
	}

	return conn.DeleteSession(ctx, acp.SessionId(sessionID))
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

	// 取消所有进行中的启动并收集 done：cancel 后 conn.Start 快速失败并杀
	// 子进程；发起者回填需要 m.mu，因此等待必须放在锁外（见下方）。
	var startingDone []<-chan struct{}
	for _, entry := range m.starting {
		entry.cancel()
		startingDone = append(startingDone, entry.done)
	}
	m.closed = true

	var errs []error
	for id, conn := range m.agents {
		if err := conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close agent '%s': %w", id, err))
		}
	}
	m.agents = make(map[string]*AgentConnection)
	m.starting = make(map[string]*startEntry)
	m.mu.Unlock()

	// 锁已释放，此时等待回收器退出不会死锁
	if reaperDone != nil {
		<-reaperDone
	}
	// 等所有启动 goroutine 收尾：m.closed 使回填分支回收进程而非写回 map，
	// 保证 Close 返回后无残留连接与子进程（发起者此时已能拿到 m.mu）。
	for _, done := range startingDone {
		<-done
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

	mu            sync.Mutex
	cmd           *exec.Cmd
	conn          *acp.ClientSideConnection
	stdin         io.WriteCloser
	sessions      map[acp.SessionId]*SessionState
	lastSessionID acp.SessionId
	started       bool
	procCancel    context.CancelFunc

	// onPromptStarted 在 Manager 的全局槽位获取成功后调用：此时 prompt
	// 即将发送，事件回调按 ACP session id 路由，不再依赖单一当前 session。
	onPromptStarted    func(sessionID string)
	onAgentForceKilled func()

	// 空闲回收用：lastUsed 为最后一次活跃操作时间，activePrompts 为已获得
	// 全局槽位且仍在进行中的 prompt 数。
	lastUsed      time.Time
	activePrompts int

	// 取消确认（cancel 兜底）：每个 session 独立记录完成信号，便于并发 prompt
	// 同时等待取消确认。强杀仍是 Agent 进程级行为，见 waitCancelConfirm。
	cancelConfirmMu sync.Mutex
	cancelConfirm   map[acp.SessionId]chan struct{}
	// forceCancelled 被强制 kill 的会话标记：kill 兜底触发时置位，
	// Prompt 返回错误时据此把进程错误识别为「已取消」。
	forceCancelledMu sync.Mutex
	forceCancelled   map[acp.SessionId]bool
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
		log:            log,
		provider:       provider,
		bridge:         acpclient.New(log, autoApprove),
		lastUsed:       time.Now(),
		sessions:       make(map[acp.SessionId]*SessionState),
		cancelConfirm:  make(map[acp.SessionId]chan struct{}),
		forceCancelled: make(map[acp.SessionId]bool),
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
			Fs: acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
			// 注意：不要声明 Terminal 能力 —— terminal 尚未真正实现（CreateTerminal 等
			// 目前是 stub）。声明后 omp（pi-coding-agent）的 bash 工具会走 ACP 远程终端
			// 协议，拿不到输出与 exit code，导致 "missing exit status" 并崩溃退出。
			// 待 terminal 完整实现后再开启。
			Terminal: false,
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
		if c.cmd == cmd {
			c.started = false
			// 进程退出后底层 ACP 连接和其中所有 session 都失效；清空内存索引，
			// 后续 prompt 由恢复逻辑按 DB 中的 session id 逐个 load/recreate。
			c.sessions = make(map[acp.SessionId]*SessionState)
			c.lastSessionID = ""
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

	c.sessions[sess.SessionId] = &SessionState{
		ID:      sess.SessionId,
		Cwd:     cwd,
		Created: time.Now(),
	}
	c.lastSessionID = sess.SessionId
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

	// 会话恢复（session/load）期间，agent 会把磁盘会话文件里的历史上下文回放成
	// session/update 通知（如 agent_message_chunk，内容是历史轮次的 AI 消息）。
	// 这些是历史回放、不是本轮输出：先对该 session 静音（见 client.Bridge.SetMuted），
	// 期间事件丢弃不入缓存、不广播，避免前端把历史消息追加到当前 turn 的占位消息上
	//（表现为「用户发送后立即显示上一轮 AI 消息」）；历史内容已由 DB 持久化，无损失。
	c.bridge.SetMuted(string(sessionID), true)
	defer c.bridge.SetMuted(string(sessionID), false)

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

	c.sessions[sessionID] = &SessionState{
		ID:            sessionID,
		Cwd:           cwd,
		Created:       time.Now(),
		ConfigOptions: res.ConfigOptions,
	}
	c.lastSessionID = sessionID

	c.log.Info("session loaded", "agent", c.provider.ID, "sessionId", sessionID, "cwd", cwd)
	return nil
}

// LoadedConfigOptions 返回指定 session 最近一次 load 的配置快照（锁内读）。
func (c *AgentConnection) LoadedConfigOptions(sessionID acp.SessionId) []acp.SessionConfigOption {
	c.mu.Lock()
	defer c.mu.Unlock()
	session := c.sessions[sessionID]
	if session == nil {
		return nil
	}
	return session.ConfigOptions
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
// Manager 已在调用前取得全局执行槽位；同一 Agent 的多个 session 可以并发向
// ACP 发送 prompt。事件缓存按 session id 隔离，取消确认也按 session 隔离。
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

	// Manager 的全局槽位已获得：现在才注册本 session 的事件回调和取消确认。
	if c.onPromptStarted != nil {
		c.onPromptStarted(string(sessionID))
	}
	c.bridge.ResetSession(string(sessionID))
	defer c.bridge.ResetSession(string(sessionID))
	start := time.Now()

	done := make(chan struct{})
	c.cancelConfirmMu.Lock()
	c.cancelConfirm[sessionID] = done
	c.cancelConfirmMu.Unlock()
	defer func() {
		c.cancelConfirmMu.Lock()
		delete(c.cancelConfirm, sessionID)
		c.cancelConfirmMu.Unlock()
		close(done)
	}()

	resp, err := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock(message)},
	})
	if err != nil {
		// 强制 kill 兜底触发时，进程死亡使 conn.Prompt 返回连接错误；
		// 识别为「已取消」，避免对已点停止的用户再弹错误。
		c.forceCancelledMu.Lock()
		forced := c.forceCancelled[sessionID]
		delete(c.forceCancelled, sessionID)
		c.forceCancelledMu.Unlock()
		if forced {
			return nil, fmt.Errorf("%w: session %s", ErrPromptCancelled, sessionID)
		}
		return nil, fmt.Errorf("prompt: %w", err)
	}

	return &PromptResult{
		SessionID:  string(sessionID),
		Reply:      c.bridge.AgentText(string(sessionID)),
		StopReason: string(resp.StopReason),
		Events:     c.bridge.Events(string(sessionID)),
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// Cancel 取消正在执行的 prompt；排队中的 prompt 由 Manager.Cancel 在全局 FIFO
// 调度器中撤销。ACP cancel 无响应时仍沿用 Agent 进程级强杀兜底。
func (c *AgentConnection) Cancel(ctx context.Context, sessionID acp.SessionId) error {
	c.mu.Lock()
	if !c.started || c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("agent not started")
	}
	conn := c.conn
	c.mu.Unlock()

	if err := conn.Cancel(ctx, acp.CancelNotification{SessionId: sessionID}); err != nil {
		return err
	}

	// ACP cancel 是尽力而为的通知，agent 可能不响应（卡死的工具调用等）。
	// 在后台等待该 session 的完成信号，超时后仍强杀整个 Agent 进程。
	c.cancelConfirmMu.Lock()
	done, ok := c.cancelConfirm[sessionID]
	c.cancelConfirmMu.Unlock()
	if ok {
		go c.waitCancelConfirm(sessionID, done)
	}
	return nil
}

// waitCancelConfirm 等待取消确认，超时未确认则强制 kill agent 进程（最后手段）。
// 三个退出出口，不泄漏 goroutine：确认到达、兜底 kill 完成、或 turn 恰好结束
// （done 被关闭，与确认同路）。
func (c *AgentConnection) waitCancelConfirm(sessionID acp.SessionId, done chan struct{}) {
	select {
	case <-done:
		// agent 已响应取消（或 turn 恰好自然结束）：无需兜底
		return
	case <-time.After(cancelConfirmTimeout):
		c.log.Warn("cancel not confirmed, force killing agent",
			"agent", c.provider.ID, "sessionId", sessionID,
			"timeout", cancelConfirmTimeout)
	}

	// Agent 级强杀前，先通知 Manager 撤销该 Agent 仍在全局 FIFO 中排队的
	// prompt，避免它们在旧连接被杀后又拿到槽位继续发送。
	if c.onAgentForceKilled != nil {
		c.onAgentForceKilled()
	}

	c.forceCancelledMu.Lock()
	c.forceCancelled[sessionID] = true
	c.forceCancelledMu.Unlock()

	// kill 进程：cmd.Wait goroutine 自动清理状态（started=false、conn=nil），
	// 下次使用按需重启；进程内其它 session 由 RecoverSession 机制自动恢复。
	// Close 幂等：即使并发被其它路径触发，重复调用无害。
	if err := c.Close(); err != nil {
		c.log.Error("force kill agent failed", "agent", c.provider.ID, "err", err)
	}
}

// removeSessionLocked 从内存会话表移除指定 session（须持 c.mu 调用）。
// 同时校正 lastSessionID：被移除的是当前默认会话时，回退到剩余会话中的任意一个。
func (c *AgentConnection) removeSessionLocked(sessionID acp.SessionId) {
	delete(c.sessions, sessionID)
	if c.lastSessionID == sessionID {
		c.lastSessionID = ""
		for id := range c.sessions {
			c.lastSessionID = id
			break
		}
	}
}

// CloseSession 关闭单个 ACP session（释放 agent 端会话资源）。
// 用于切 tab 时释放旧隐式草稿会话。ACP CloseSession 是可选能力，
// agent 不支持时可能报错，调用方按尽力释放处理。
func (c *AgentConnection) CloseSession(ctx context.Context, sessionID acp.SessionId) error {
	c.mu.Lock()
	if !c.started || c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("agent not started")
	}
	conn := c.conn
	c.mu.Unlock()

	_, err := conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: sessionID})
	if err == nil {
		c.mu.Lock()
		c.removeSessionLocked(sessionID)
		c.mu.Unlock()
	}
	return err
}

// DeleteSession 删除 agent 侧 ACP session（ACP session/delete，UNSTABLE 能力）。
// 语义比 close 更彻底：agent 从其会话列表删除该会话（含持久化会话数据，若 agent 实现了）。
// 返回 nil 的两种情况：
//   - 删除成功；
//   - agent 已不认该 session（unknown session，视为不存在/已被删除），同样清理内存表。
//
// 其余错误原样返回（如 method not found，agent 未实现该 unstable 能力），由调用方降级。
func (c *AgentConnection) DeleteSession(ctx context.Context, sessionID acp.SessionId) error {
	c.mu.Lock()
	if !c.started || c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("agent not started")
	}
	conn := c.conn
	c.mu.Unlock()

	_, err := conn.UnstableDeleteSession(ctx, acp.UnstableDeleteSessionRequest{SessionId: sessionID})
	if err == nil || IsUnknownSessionErr(err) {
		// 成功或 agent 侧已无此 session：内存表同步移除，与 agent 侧状态保持一致
		// （残留条目会让后续针对该 session 的操作误判其仍活跃）。
		c.mu.Lock()
		c.removeSessionLocked(sessionID)
		c.mu.Unlock()
		return nil
	}
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
