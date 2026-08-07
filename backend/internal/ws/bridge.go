package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/zacp/zacp/internal/acp/client"
	"github.com/zacp/zacp/internal/acp/manager"
	"github.com/zacp/zacp/internal/acp/providers"
	"github.com/zacp/zacp/internal/model"
	"github.com/zacp/zacp/internal/store"
)

// EventBridge 将 ACP 事件桥接到 WebSocket，并负责 WS prompt 流程的消息落库与权限交互。
type EventBridge struct {
	handler     *Handler
	manager     *manager.Manager
	sessionRepo *store.SessionRepository
	msgRepo     *store.MessageRepository
	log         *slog.Logger

	mu sync.Mutex
	// activeSessionByAgent 各 agent 当前「真正在执行」的会话（ACP session id）。
	// 由 SetupEventCallback 在排队门闩获取成功（promptStartedHook）时按 agent 更新，
	// 排队中的 prompt 不会覆盖它。按 agent 而非全局单值：跨 agent 并行时，
	// 每个 agent 的权限请求路由到各自正在执行的会话，不串台。
	activeSessionByAgent map[string]string
	// pendingPermissions 等待前端回传的权限请求（permissionID → 响应通道）
	pendingPermissions sync.Map
}

// permissionTimeout 前端未响应权限请求的等待上限；超时自动取消，避免阻塞 agent turn。
const permissionTimeout = 60 * time.Second

// NewEventBridge 创建事件桥接器
func NewEventBridge(handler *Handler, mgr *manager.Manager, sessionRepo *store.SessionRepository, msgRepo *store.MessageRepository, log *slog.Logger) *EventBridge {
	return &EventBridge{
		handler:              handler,
		manager:              mgr,
		sessionRepo:          sessionRepo,
		msgRepo:              msgRepo,
		log:                  log,
		activeSessionByAgent: make(map[string]string),
	}
}

// NewEventBridge 组装完成后，由调用方（cmd/server）注入「prompt 开始执行」钩子：
// 排队门闩获取成功（真正执行）时才注册该会话的事件回调（SetupEventCallback），
// 排队期间不注册——执行中会话的回调不被后到的 prompt 覆盖（见 manager.promptGate）。

// OnPromptStarted 在 manager 排队门闩获取成功（本会话 prompt 真正开始执行）时调用：
//  1. 注册该会话的事件回调（SetupEventCallback，见上）；
//  2. 广播 turn.started——前端据此把「排队中（queued）」切换为「流式（streaming）」。
//     立即执行的会话几乎瞬间收到（不显示排队中），真正排队的会话在轮到自己时才收到。
func (b *EventBridge) OnPromptStarted(agentID, sessionID string) {
	if err := b.SetupEventCallback(agentID, sessionID); err != nil {
		b.log.Warn("setup event callback on prompt started", "agentID", agentID, "sessionID", sessionID, "err", err)
		return
	}
	b.handler.BroadcastTurnStarted(sessionID)
}

// Log 返回 EventBridge 的日志器，供外部（如 handler）记录桥接相关事件。
func (b *EventBridge) Log() *slog.Logger {
	return b.log
}

// EnsureSessionUpdateHandlers 提前注册「按通知 sessionId 分发」的 session/update 处理器
// （configOptions、availableCommands），用于在会话创建（session/new）之前调用。
// 原因：部分 agent（如 reasonix）在 session/new 响应返回的同一时刻同步推送
// available_commands_update；若等 CreateSession 返回后才注册，SDK 的 read loop
// 可能已先读到该通知而丢弃（omp 有 ~50ms 延迟，此前未暴露此竞态）。
// 这两个处理器不依赖调用方闭包（按 SDK 通知自带 sessionId 分发），可安全提前注册；
// 而 onEvent / 权限等依赖具体会话的处理器仍由 SetupEventCallback 按需注册，
// 避免用空 sessionID 覆盖正在 prompt 的会话回调。
func (b *EventBridge) EnsureSessionUpdateHandlers(agentID string) error {
	bridge, err := b.manager.GetBridge(agentID)
	if err != nil {
		return err
	}

	bridge.SetConfigOptionsHandler(func(sid string, opts []acp.SessionConfigOption) {
		b.handleConfigOptions(sid, opts)
	})
	bridge.SetAvailableCommandsHandler(func(sid string, cmds []acp.AvailableCommand) {
		b.handleAvailableCommands(sid, cmds)
	})
	bridge.SetSessionInfoHandler(func(sid string, info acp.SessionSessionInfoUpdate) {
		b.handleSessionInfo(sid, info)
	})

	b.log.Info("session update handlers ensured for agent", "agentID", agentID)
	return nil
}

// SetupEventCallback 为 Agent 连接设置事件回调（agent 级，幂等；多次调用覆盖注册）。
// 各 agent 当前「真正在执行」的会话（权限请求按此路由）；调用时机由
// manager 的 promptStartedHook 保证——排队门闩获取成功后才会调用，因此
// 排队期间该 agent 的槽位仍指向真正在执行的那个会话，不串台。
func (b *EventBridge) SetupEventCallback(agentID, sessionID string) error {
	bridge, err := b.manager.GetBridge(agentID)
	if err != nil {
		return err
	}

	b.mu.Lock()
	b.activeSessionByAgent[agentID] = sessionID
	b.mu.Unlock()

	// 事件按事件自身携带的 ACP session id 分发（client.Event.SessionID 来自
	// SDK session/update 通知的 SessionId）——多会话排队/并行时，执行中会话
	// 的事件始终路由回原会话，不再依赖「注册时的闭包 sessionID」。
	bridge.SetOnEvent(func(event client.Event) {
		b.handleEvent(event.SessionID, event)
	})

	// 非自动批准模式下，把 agent 的权限请求转发给前端交互式选择（见 RequestPermission）。
	// 闭包捕获 agentID：跨 agent 并行时权限路由到各自正在执行的会话。
	bridge.SetPermissionHandler(func(req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
		return b.HandlePermissionRequest(agentID, req)
	})

	// 接收 agent 经 session/update 通知下发的配置项（模型/思考强度/mode 等），
	// 覆盖 session/new 响应未带 configOptions 的情况，实时落库供前端读取。
	// 回调自带 SDK 通知的 ACP session id，按会话分发（见 client.Bridge.SessionUpdate）。
	bridge.SetConfigOptionsHandler(func(sid string, opts []acp.SessionConfigOption) {
		b.handleConfigOptions(sid, opts)
	})

	// 接收 agent 经 session/update 的 available_commands_update 通知下发的可用 / 命令，
	// 落库供重进会话恢复，并实时广播给前端刷新候选面板。
	// 回调自带 SDK 通知的 ACP session id（同上）。
	bridge.SetAvailableCommandsHandler(func(sid string, cmds []acp.AvailableCommand) {
		b.handleAvailableCommands(sid, cmds)
	})

	// 接收 agent 经 session/update 的 session_info_update 通知下发的会话信息
	// （AI 总结标题等），落库供侧栏/信息面板展示，并实时广播给前端。
	// 回调自带 SDK 通知的 ACP session id（同上）。
	bridge.SetSessionInfoHandler(func(sid string, info acp.SessionSessionInfoUpdate) {
		b.handleSessionInfo(sid, info)
	})

	b.log.Info("event callback setup for agent", "agentID", agentID, "sessionID", sessionID)
	return nil
}

// handleConfigOptions 收到 agent 下发的配置项后落库 + 实时广播给前端。
// 场景：切换模型后 agent 推送新的 configOptions（如 deepseek 官方模型带思维强度选项），
// 前端收到广播立即刷新下拉，无需重新进入会话。
func (b *EventBridge) handleConfigOptions(sessionID string, opts []acp.SessionConfigOption) {
	dbSession, err := b.sessionRepo.GetByACPSessionID(sessionID)
	if err != nil {
		// reasonix 等 agent 在 session/new 响应同一毫秒同步推送通告，
		// 此时 DB 会话记录可能尚未落库（svc.CreateSession 仍在进行中）；
		// 延迟重试一次，避免「通告先到、落库后到」的竞态丢数据。
		go func() {
			if s := b.findSessionWithRetry(sessionID); s != nil {
				b.applyConfigOptions(s, opts)
			}
		}()
		return
	}
	b.applyConfigOptions(dbSession, opts)
}

func (b *EventBridge) applyConfigOptions(dbSession *model.Session, opts []acp.SessionConfigOption) {
	sessionID := dbSession.ACPSessionID
	dtos := client.ToConfigOptionDTOs(opts)
	data, err := json.Marshal(dtos)
	if err != nil {
		b.log.Warn("marshal config options failed", "sessionID", sessionID, "err", err)
		return
	}
	if err := b.sessionRepo.UpdateConfigOptions(dbSession.ID, string(data)); err != nil {
		b.log.Warn("save config options failed", "sessionID", sessionID, "err", err)
		return
	}
	b.handler.BroadcastConfigOptions(sessionID, dtos)
	b.log.Info("config options updated", "sessionID", sessionID, "count", len(opts))
}

// findSessionWithRetry 按 ACP session id 查 DB 会话；首次查不到时延迟 300ms 重试一次。
// 用于 reasonix 等 agent 在 session/new 响应同一毫秒同步推送 session/update 通告、
// 而 DB 记录尚未落库的竞态窗口。重试后仍无则返回 nil（调用方丢弃）。
func (b *EventBridge) findSessionWithRetry(acpSessionID string) *model.Session {
	dbSession, err := b.sessionRepo.GetByACPSessionID(acpSessionID)
	if err == nil {
		return dbSession
	}
	time.Sleep(300 * time.Millisecond)
	dbSession, err = b.sessionRepo.GetByACPSessionID(acpSessionID)
	if err == nil {
		return dbSession
	}
	b.log.Warn("session update for unknown session (after retry)",
		"sessionID", acpSessionID, "err", err)
	return nil
}

// handleAvailableCommands 收到 agent 通告的可用 / 命令后落库 + 实时广播给前端。
// 与 handleConfigOptions 同理：命令列表可能随会话状态动态变化（agent 随时可重新通告），
// 前端收到广播立即刷新候选面板；落库保证重进会话时无需等待 agent 重新通告即可恢复。
func (b *EventBridge) handleAvailableCommands(sessionID string, cmds []acp.AvailableCommand) {
	dbSession, err := b.sessionRepo.GetByACPSessionID(sessionID)
	if err != nil {
		// 同 handleConfigOptions：通告可能在 DB 落库前到达，延迟重试一次。
		go func() {
			if s := b.findSessionWithRetry(sessionID); s != nil {
				b.applyAvailableCommands(s, cmds)
			}
		}()
		return
	}
	b.applyAvailableCommands(dbSession, cmds)
}

func (b *EventBridge) applyAvailableCommands(dbSession *model.Session, cmds []acp.AvailableCommand) {
	sessionID := dbSession.ACPSessionID
	dtos := client.ToAvailableCommandDTOs(cmds)
	// 落库保持 agent 通告原样（静态命令不入库，避免 DB 语义被污染；
	// 重进会话时由 REST GetSlashCommands 动态合并兜底）。
	data, err := json.Marshal(dtos)
	if err != nil {
		b.log.Warn("marshal slash commands failed", "sessionID", sessionID, "err", err)
		return
	}
	if err := b.sessionRepo.UpdateAvailableCommands(dbSession.ID, string(data)); err != nil {
		b.log.Warn("save slash commands failed", "sessionID", sessionID, "err", err)
		return
	}
	// 广播合并后的列表：agent 不通告命令（如 grok）时静态命令仍能展示；
	// 同名以 agent 通告为准，见 providers.MergeSlashCommands。
	broadcast := providers.MergeSlashCommands(dtos, providers.DefaultSlashCommands(dbSession.AgentID))
	b.handler.BroadcastSlashCommands(sessionID, broadcast)
	b.log.Info("slash commands updated", "sessionID", sessionID, "count", len(broadcast))
}

// handleSessionInfo 收到 agent 通告的会话信息（AI 总结标题等）后落库 + 实时广播给前端。
// 标题优先级约定：agent 经 session_info_update 推送的标题优先于 zacp 本地的
// deriveTitle 截取逻辑（见 HandlePrompt）——agent 没推过标题时维持截取结果，
// 推过则以 agent 标题为准（覆盖）。与 handleConfigOptions 同理：通告可能早于
// DB 落库到达，查不到时延迟重试一次。
func (b *EventBridge) handleSessionInfo(sessionID string, info acp.SessionSessionInfoUpdate) {
	if info.Title == nil || strings.TrimSpace(*info.Title) == "" {
		// agent 未提供标题（或显式清空）：保持现有标题不变，避免误覆盖
		return
	}
	title := strings.TrimSpace(*info.Title)

	dbSession, err := b.sessionRepo.GetByACPSessionID(sessionID)
	if err != nil {
		// 同 handleConfigOptions：通告可能在 DB 落库前到达，延迟重试一次。
		go func() {
			if s := b.findSessionWithRetry(sessionID); s != nil {
				b.applySessionInfo(s, title)
			}
		}()
		return
	}
	b.applySessionInfo(dbSession, title)
}

func (b *EventBridge) applySessionInfo(dbSession *model.Session, title string) {
	sessionID := dbSession.ACPSessionID
	if err := b.sessionRepo.UpdateTitle(dbSession.ID, title); err != nil {
		b.log.Warn("save session title failed", "sessionID", sessionID, "err", err)
		return
	}
	b.handler.BroadcastSessionInfo(sessionID, map[string]any{"title": title})
	b.log.Info("session title updated by agent", "sessionID", sessionID, "title", title)
}

// isNilOrEmpty 严格判空：interface 为 nil、nil 指针/切片/map/channel/func、空字符串
// 都视为「无值」。SDK 的 RawInput/RawOutput 可能是类型化 nil（如 json.RawMessage），
// 直接 `!= nil` 判不准确，会把 nil 广播成 "input":null，前端据此覆盖掉已有人参。
func isNilOrEmpty(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return rv.IsNil()
	case reflect.String:
		return rv.Len() == 0
	}
	return false
}

// handleEvent 处理 ACP 事件并广播到 WebSocket
func (b *EventBridge) handleEvent(sessionID string, event client.Event) {
	// 将 ACP 事件转换为 WebSocket 事件
	wsEvent := map[string]interface{}{
		"type":   event.Type,
		"text":   event.Text,
		"title":  event.Title,
		"status": event.Status,
		"toolId": event.ToolID,
	}
	// 工具调用入参/出参：严格判空后省略字段，避免广播冗余的 null
	if !isNilOrEmpty(event.Input) {
		wsEvent["input"] = event.Input
	}
	if !isNilOrEmpty(event.Output) {
		wsEvent["output"] = event.Output
	}
	// 执行计划（TODO 列表）：整体替换语义，随事件原样透传；nil 时省略
	if event.Plan != nil {
		wsEvent["plan"] = event.Plan
	}

	// 广播事件到该会话的所有连接
	b.handler.BroadcastEvent(sessionID, wsEvent)
}

// HandlePermissionRequest 处理 agent 的权限请求（在 RequestPermission 回调中同步调用）：
// 按 agentID 查「正在执行的会话」广播 permission.request 给前端弹窗，等待用户选择回传；
// 超时（permissionTimeout）自动取消，避免阻塞 agent turn。
func (b *EventBridge) HandlePermissionRequest(agentID string, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	permissionID := fmt.Sprintf("perm-%d", time.Now().UnixNano())
	ch := make(chan acp.RequestPermissionResponse, 1)
	b.pendingPermissions.Store(permissionID, ch)

	b.mu.Lock()
	sessionID := b.activeSessionByAgent[agentID]
	b.mu.Unlock()

	// 转成前端友好结构（SDK 类型直接序列化字段不稳定，显式挑字段）
	toolCall := map[string]interface{}{
		"toolCallId": string(req.ToolCall.ToolCallId),
	}
	if req.ToolCall.Title != nil {
		toolCall["title"] = *req.ToolCall.Title
	}
	if req.ToolCall.Status != nil {
		toolCall["status"] = string(*req.ToolCall.Status)
	}
	if req.ToolCall.RawInput != nil {
		toolCall["rawInput"] = req.ToolCall.RawInput
	}

	options := make([]map[string]interface{}, 0, len(req.Options))
	for _, o := range req.Options {
		options = append(options, map[string]interface{}{
			"optionId": string(o.OptionId),
			"name":     o.Name,
			"kind":     string(o.Kind),
		})
	}

	b.handler.BroadcastPermissionRequest(sessionID, permissionID, toolCall, options)
	b.log.Info("permission requested", "permissionID", permissionID, "sessionID", sessionID)

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(permissionTimeout):
		b.pendingPermissions.Delete(permissionID)
		b.log.Warn("permission request timed out", "permissionID", permissionID)
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
		}, nil
	}
}

// ResolvePermission 处理前端回传的权限选择结果（WS permission 消息），
// 唤醒等待中的 HandlePermissionRequest。
func (b *EventBridge) ResolvePermission(permissionID, optionID string) {
	v, ok := b.pendingPermissions.LoadAndDelete(permissionID)
	if !ok {
		b.log.Warn("permission not pending", "permissionID", permissionID)
		return
	}
	ch, ok := v.(chan acp.RequestPermissionResponse)
	if !ok {
		return
	}
	select {
	case ch <- acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(optionID)},
		},
	}:
	default:
		// 通道已满（理论上不会），丢弃
	}
}

// deriveTitle 从首条用户消息生成会话标题（最多 24 个字符）
func deriveTitle(message string) string {
	r := []rune(strings.TrimSpace(message))
	if len(r) <= 24 {
		return string(r)
	}
	return string(r[:24]) + "…"
}

// marshalEvents 将事件列表序列化为 JSON 字符串（对齐 REST SendMessage 的 events 字段语义）
func marshalEvents(events []client.Event) string {
	if len(events) == 0 {
		return ""
	}
	data, err := json.Marshal(events)
	if err != nil {
		return ""
	}
	return string(data)
}

// HandlePrompt 处理 WebSocket 的 prompt 消息（每帧一个 goroutine，可并发进入）。
// 并发语义（方案 B「展示并发、执行串行」）：
//  1. 同 agent 的 prompt 由 manager.promptGate 串行执行，后续 prompt 进入 FIFO 排队；
//     排队期间不注册事件回调（promptStartedHook 在真正执行时才注册），
//     因此执行中会话的流式事件不会串到排队中的会话；
//  2. 排队中的 prompt 被 Cancel 撤销时返回 ErrPromptCancelled，
//     此处广播 turn.done(cancelled) 让前端复位「排队中」状态，不报错、不落库；
//  3. 跨 agent 的 prompt 天然并行（门闩按 agent 连接隔离）。
//
// 流程：
//  1. 按需启动 agent 进程
//  2. 按 ACP session id 反查 DB 会话并落库用户消息（首条消息生成标题）
//  3. 草稿转正
//  4. 经 manager.Prompt 排队执行（事件回调由 onStarted 钩子注册）
//  5. 落库助手回复、touch 会话（驱动侧栏排序），广播 turn.done
func (b *EventBridge) HandlePrompt(ctx context.Context, sessionID, agentID, message string) error {
	// 按需启动兜底：服务端重启后仅预启动第一个 agent，用户直接对其它
	// agent 的旧会话发消息时，这里先确保进程已启动——否则事件回调注册
	// 的 GetBridge 会返回 "agent not started"，根本走不到 manager.Prompt。
	if err := b.manager.EnsureStarted(ctx, agentID); err != nil {
		return err
	}

	dbSession, err := b.sessionRepo.GetByACPSessionID(sessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// 落库用户消息（即使 agent 调用失败也保留）
	userMsg := &model.Message{
		SessionID: dbSession.ID,
		Role:      "user",
		Content:   message,
		CreatedAt: time.Now(),
	}
	if err := b.msgRepo.Create(userMsg); err != nil {
		return fmt.Errorf("failed to save user message: %w", err)
	}

	// 草稿转正：隐式草稿会话在发出首条 prompt 时转为正常会话（is_draft=false），
	// 此后进入侧栏列表展示。设计约定「转正时机=发出首条 prompt 即转正，不等回复」。
	if dbSession.IsDraft {
		if err := b.sessionRepo.PromoteFromDraft(dbSession.ID); err != nil {
			b.log.Warn("promote draft session failed", "sessionID", sessionID, "err", err)
		} else {
			b.log.Info("draft session promoted", "sessionID", sessionID, "dbID", dbSession.ID)
		}
	}

	// 首条消息生成会话标题（仅当仍是默认标题时）
	if dbSession.Title == "" || dbSession.Title == "新会话" {
		_ = b.sessionRepo.UpdateTitle(dbSession.ID, deriveTitle(message))
	}

	result, err := b.manager.Prompt(ctx, agentID, sessionID, message)
	if err != nil && manager.IsUnknownSessionErr(err) {
		// ACP session 失效（服务端/agent 重启后 DB 记录仍在、agent 端已丢失）：
		// 自动恢复并重试一次，前端无感知。事件回调由 onStarted 钩子注册，
		// 闭包捕获的 sessionID 变量在重试前已更新，会绑定到新 session。
		b.log.Warn("acp session invalid, recovering", "sessionID", sessionID, "err", err)
		if newID, ok := b.recoverSession(ctx, dbSession, agentID, sessionID); ok {
			sessionID = newID
			result, err = b.manager.Prompt(ctx, agentID, sessionID, message)
		}
	}
	if err != nil {
		if manager.IsPromptCancelledErr(err) {
			// 排队中的 prompt 被用户取消（撤销排队）：agent 尚未收到任何内容，
			// 用户消息已落库（保留），不落 assistant、不报错；广播 turn.done(cancelled)
			// 让前端复位「排队中」状态并刷新消息（占位消息替换为 DB 版本）。
			b.log.Info("queued prompt cancelled", "sessionID", sessionID)
			b.handler.BroadcastTurnDone(sessionID, "", "cancelled")
			return nil
		}
		return err
	}

	// 落库助手回复
	assistantMsg := &model.Message{
		SessionID: dbSession.ID,
		Role:      "assistant",
		Content:   result.Reply,
		Events:    marshalEvents(result.Events),
		CreatedAt: time.Now(),
	}
	if err := b.msgRepo.Create(assistantMsg); err != nil {
		return fmt.Errorf("failed to save assistant message: %w", err)
	}

	// touch 会话驱动侧栏排序
	_ = b.sessionRepo.Touch(dbSession.ID)

	b.handler.BroadcastTurnDone(sessionID, result.Reply, result.StopReason)
	return nil
}

// HandleCancel 处理 WebSocket 的 cancel 消息
func (b *EventBridge) HandleCancel(ctx context.Context, sessionID, agentID string) error {
	// 与 HandlePrompt 一致：先确保 agent 已启动（幂等），避免对未启动
	// agent 的旧会话发 cancel 时报 "agent not started"。
	if err := b.manager.EnsureStarted(ctx, agentID); err != nil {
		return err
	}
	return b.manager.Cancel(ctx, agentID, sessionID)
}

// recoverSession 处理 ACP session 失效（服务端/agent 重启后 DB 记录仍在但 agent 端丢失）：
// 委托 manager.RecoverSession：优先 ACP session/load（agent 支持持久化会话时保留对话上下文），
// 失败则新建 ACP session；重建时更新 DB 记录并迁移 WS 订阅（旧 id → 新 id，
// 否则广播按新 id 匹配不到订阅者、前端一直 loading），返回最终可用的 ACP session id。
func (b *EventBridge) recoverSession(ctx context.Context, dbSession *model.Session, agentID, oldAcpID string) (string, bool) {
	// cwd 为空时传 ""，由 manager.RecoverSession 统一按 provider 默认工作区解析
	//（与创建会话的路径语义一致，均为绝对路径；传 "." 等相对路径会导致
	//  omp 等按 cwd 定位会话文件的 agent load 永远失败、每次都走重建）。
	cwd := ""
	if dbSession.Workspace.Path != "" {
		cwd = dbSession.Workspace.Path
	}
	newID, rebuilt, err := b.manager.RecoverSession(ctx, agentID, oldAcpID, cwd, dbSession.ConfigOptions)
	if err != nil {
		b.log.Error("failed to recover acp session", "err", err)
		return "", false
	}
	if rebuilt {
		if err := b.sessionRepo.UpdateACPSessionID(dbSession.ID, newID); err != nil {
			b.log.Error("failed to update acp session id in db", "err", err)
		}
		// 订阅迁移必须在重试 prompt 之前完成：HandlePrompt 的重试在 recoverSession
		// 返回后执行，随后的 turn.started/event/turn.done 广播都走新 id，
		// 只有迁移后的连接才能收到（另有 session.recovered 消息让前端更新映射）。
		b.handler.RebindSession(oldAcpID, newID)
		b.log.Info("rebound ws subscriptions", "old", oldAcpID, "new", newID)
		// 回放用户配置：重建 = 全新 ACP 会话，agent 侧配置回到默认值。
		//（load 成功时配置随上下文原样恢复，无需回放；此处兜底 load 失败/
		//  不支持 load 的 agent，按 DB 存档逐项 set 回去，尽力而为。）
		b.manager.ReplaySessionConfigOptions(ctx, agentID, newID, dbSession.ConfigOptions)
	}
	return newID, true
}
