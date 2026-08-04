package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/zacp/zacp/internal/acp/client"
	"github.com/zacp/zacp/internal/acp/manager"
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
	// activeSessionID 最近一次 HandlePrompt 的会话（ACP session id）。
	// 权限请求发生时 prompt 由 AgentConnection.promptMu 串行，故当前活动会话即权限所属会话。
	activeSessionID string
	// pendingPermissions 等待前端回传的权限请求（permissionID → 响应通道）
	pendingPermissions sync.Map
}

// permissionTimeout 前端未响应权限请求的等待上限；超时自动取消，避免阻塞 agent turn。
const permissionTimeout = 60 * time.Second

// NewEventBridge 创建事件桥接器
func NewEventBridge(handler *Handler, mgr *manager.Manager, sessionRepo *store.SessionRepository, msgRepo *store.MessageRepository, log *slog.Logger) *EventBridge {
	return &EventBridge{
		handler:     handler,
		manager:     mgr,
		sessionRepo: sessionRepo,
		msgRepo:     msgRepo,
		log:         log,
	}
}

// SetupEventCallback 为 Agent 连接设置事件回调（agent 级；每次 prompt 前覆盖注册）。
func (b *EventBridge) SetupEventCallback(agentID, sessionID string) error {
	bridge, err := b.manager.GetBridge(agentID)
	if err != nil {
		return err
	}

	b.mu.Lock()
	b.activeSessionID = sessionID
	b.mu.Unlock()

	bridge.SetOnEvent(func(event client.Event) {
		b.handleEvent(sessionID, event)
	})

	// 非自动批准模式下，把 agent 的权限请求转发给前端交互式选择（见 RequestPermission）
	bridge.SetPermissionHandler(func(req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
		return b.HandlePermissionRequest(req)
	})

	// 接收 agent 经 session/update 通知下发的配置项（模型/思考强度/mode 等），
	// 覆盖 session/new 响应未带 configOptions 的情况，实时落库供前端读取
	bridge.SetConfigOptionsHandler(func(opts []acp.SessionConfigOption) {
		b.handleConfigOptions(sessionID, opts)
	})

	b.log.Info("event callback setup for agent", "agentID", agentID, "sessionID", sessionID)
	return nil
}

// handleConfigOptions 收到 agent 下发的配置项后落库（按 ACP session id 反查 DB 会话）。
func (b *EventBridge) handleConfigOptions(sessionID string, opts []acp.SessionConfigOption) {
	dbSession, err := b.sessionRepo.GetByACPSessionID(sessionID)
	if err != nil {
		b.log.Warn("config options for unknown session", "sessionID", sessionID, "err", err)
		return
	}
	data, err := json.Marshal(client.ToConfigOptionDTOs(opts))
	if err != nil {
		b.log.Warn("marshal config options failed", "sessionID", sessionID, "err", err)
		return
	}
	if err := b.sessionRepo.UpdateConfigOptions(dbSession.ID, string(data)); err != nil {
		b.log.Warn("save config options failed", "sessionID", sessionID, "err", err)
		return
	}
	b.log.Info("config options updated", "sessionID", sessionID, "count", len(opts))
}

// handleEvent 处理 ACP 事件并广播到 WebSocket
func (b *EventBridge) handleEvent(sessionID string, event client.Event) {
	// 将 ACP 事件转换为 WebSocket 事件
	wsEvent := map[string]interface{}{
		"type":    event.Type,
		"text":    event.Text,
		"title":   event.Title,
		"status":  event.Status,
		"toolId":  event.ToolID,
	}

	// 广播事件到该会话的所有连接
	b.handler.BroadcastEvent(sessionID, wsEvent)
}

// HandlePermissionRequest 处理 agent 的权限请求（在 RequestPermission 回调中同步调用）：
// 生成 permissionID 并广播 permission.request 给前端弹窗，等待用户选择回传；
// 超时（permissionTimeout）自动取消，避免阻塞 agent turn。
func (b *EventBridge) HandlePermissionRequest(req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	permissionID := fmt.Sprintf("perm-%d", time.Now().UnixNano())
	ch := make(chan acp.RequestPermissionResponse, 1)
	b.pendingPermissions.Store(permissionID, ch)

	b.mu.Lock()
	sessionID := b.activeSessionID
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

// HandlePrompt 处理 WebSocket 的 prompt 消息：
//  1. 把 agent 事件桥接到本次会话（agent 级回调，prompt 由 AgentConnection.promptMu 串行，
//     回调触发时必属当前 prompt，事件不会串到其它会话）
//  2. 按 ACP session id 反查 DB 会话并落库用户消息（首条消息生成标题）
//  3. 调用 agent 等待回复
//  4. 落库助手回复、touch 会话（驱动侧栏排序），广播 turn.done
func (b *EventBridge) HandlePrompt(ctx context.Context, sessionID, agentID, message string) error {
	if err := b.SetupEventCallback(agentID, sessionID); err != nil {
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
	if err != nil && isUnknownSession(err) {
		// ACP session 失效（服务端/agent 重启后 DB 记录仍在、agent 端已丢失）：
		// 自动恢复并重试一次，前端无感知
		b.log.Warn("acp session invalid, recovering", "sessionID", sessionID, "err", err)
		if newID, ok := b.recoverSession(ctx, dbSession, agentID, sessionID); ok {
			sessionID = newID
			// 事件回调与广播绑定到新 session
			_ = b.SetupEventCallback(agentID, sessionID)
			result, err = b.manager.Prompt(ctx, agentID, sessionID, message)
		}
	}
	if err != nil {
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
	return b.manager.Cancel(ctx, agentID, sessionID)
}

// recoverSession 处理 ACP session 失效（服务端/agent 重启后 DB 记录仍在但 agent 端丢失）：
//  1. 优先 ACP session/load（agent 支持持久化会话时保留对话上下文）
//  2. 失败则新建 ACP session 并更新 DB 记录，返回新的 ACP session id
func (b *EventBridge) recoverSession(ctx context.Context, dbSession *model.Session, agentID, oldAcpID string) (string, bool) {
	if err := b.manager.LoadSession(ctx, agentID, oldAcpID); err == nil {
		b.log.Info("acp session recovered via load", "sessionID", oldAcpID)
		return oldAcpID, true
	}

	cwd := "."
	if dbSession.Workspace.Path != "" {
		cwd = dbSession.Workspace.Path
	}
	newID, _, err := b.manager.CreateSession(ctx, agentID, cwd)
	if err != nil {
		b.log.Error("failed to recreate acp session", "err", err)
		return "", false
	}
	if err := b.sessionRepo.UpdateACPSessionID(dbSession.ID, newID); err != nil {
		b.log.Error("failed to update acp session id in db", "err", err)
	}
	b.log.Info("acp session recreated", "old", oldAcpID, "new", newID)
	return newID, true
}

// isUnknownSession 判断 ACP session 失效错误（agent 端 session 不存在）
func isUnknownSession(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unknown session")
}
