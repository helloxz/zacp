package ws

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
)

// Handler WebSocket HTTP 处理器
type Handler struct {
	hub *Hub
	log *slog.Logger
}

// NewHandler 创建 WebSocket 处理器
func NewHandler(hub *Hub, log *slog.Logger) *Handler {
	return &Handler{
		hub: hub,
		log: log,
	}
}

// ServeHTTP 处理 WebSocket 升级请求
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, bridge *EventBridge) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // 开发环境允许所有 Origin，生产环境需要配置
	})
	if err != nil {
		h.log.Error("websocket accept error", "error", err)
		return
	}

	client := h.hub.NewClient(conn)
	client.SetBridge(bridge)

	// 注意：不能用 r.Context() 作为连接生命周期——HTTP handler 返回后 request context
	// 会被取消，导致读写协程立即退出、连接关闭。必须使用独立的 context，
	// 连接的生命周期由 conn 关闭（ReadPump 出错 → unregister → 关 send → WritePump 退出）驱动。
	ctx := context.Background()
	go client.WritePump(ctx)
	go client.ReadPump(ctx)

	h.log.Info("websocket connection established", "remote", r.RemoteAddr)
}

// ServeHTTPWithSession 处理带会话绑定的 WebSocket 升级请求
func (h *Handler) ServeHTTPWithSession(sessionID, agentID string, bridge *EventBridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			h.log.Error("websocket accept error", "error", err)
			return
		}

		client := h.hub.NewClient(conn)
		client.BindSession(sessionID, agentID)
		client.SetBridge(bridge)

		// 发送会话就绪消息
		client.Send(ServerMessage{
			Type:      MsgTypeSessionReady,
			SessionID: sessionID,
			AgentID:   agentID,
		})

		// 与 ServeHTTP 同理：使用独立 context，不能用 r.Context()
		ctx := context.Background()
		go client.WritePump(ctx)
		go client.ReadPump(ctx)

		h.log.Info("websocket connection established with session",
			"remote", r.RemoteAddr,
			"sessionID", sessionID,
			"agentID", agentID)
	}
}

// BroadcastToSession 向指定会话的所有连接广播消息（按订阅集合匹配：
// 同一连接可同时订阅多个会话，进行中会话的广播不会因切到其它会话而丢失）
func (h *Handler) BroadcastToSession(sessionID string, msg ServerMessage) {
	h.hub.mu.RLock()
	defer h.hub.mu.RUnlock()

	for client := range h.hub.clients {
		if client.IsSubscribed(sessionID) {
			client.Send(msg)
		}
	}
}

// BroadcastEvent 向指定会话广播事件（携带 sessionId 供前端按会话路由）
func (h *Handler) BroadcastEvent(sessionID string, event interface{}) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:      MsgTypeEvent,
		SessionID: sessionID,
		Event:     event,
	})
}

// BroadcastTurnDone 向指定会话广播轮次完成消息（携带 sessionId；
// stopReason="cancelled" 表示排队中的 prompt 被用户取消，前端复位「排队中」状态）
func (h *Handler) BroadcastTurnDone(sessionID, reply, stopReason string) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:       MsgTypeTurnDone,
		SessionID:  sessionID,
		Reply:      reply,
		StopReason: stopReason,
	})
}

// BroadcastTurnStarted 向指定会话广播轮次开始执行消息（携带 sessionId）：
// 后端排队门闩获取成功、agent 已开始处理本会话 prompt 时发出。
// 前端据此把「排队中（queued）」切换为「流式（streaming）」——立即执行的
// 会话几乎瞬间收到（不显示排队中），真正排队的会话在轮到自己时才收到。
func (h *Handler) BroadcastTurnStarted(sessionID string) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:      MsgTypeTurnStarted,
		SessionID: sessionID,
	})
}

// BroadcastPermissionRequest 向指定会话广播权限请求（携带 sessionId）
func (h *Handler) BroadcastPermissionRequest(sessionID, permissionID string, toolCall, options interface{}) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:         MsgTypePermissionRequest,
		SessionID:    sessionID,
		PermissionID: permissionID,
		ToolCall:     toolCall,
		Options:      options,
	})
}

// BroadcastConfigOptions 向指定会话广播配置项更新（agent 经 session/update 推送，
// 如切换模型后下发思维强度等新选项；前端据此实时刷新下拉，无需重新进入会话）。
// 携带 sessionId：前端仅当前会话的广播才更新本地状态，避免多会话时串台。
func (h *Handler) BroadcastConfigOptions(sessionID string, configOptions interface{}) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:          MsgTypeConfigOptions,
		SessionID:     sessionID,
		ConfigOptions: configOptions,
	})
}

// BroadcastSlashCommands 向指定会话广播可用 / 命令更新（agent 经 available_commands_update 推送；
// 前端据此实时刷新 / 命令候选面板，无需重新进入会话）。携带 sessionId（同上）。
func (h *Handler) BroadcastSlashCommands(sessionID string, commands interface{}) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:          MsgTypeSlashCommands,
		SessionID:     sessionID,
		SlashCommands: commands,
	})
}

// BroadcastSessionInfo 向指定会话广播会话信息更新（agent 经 session_info_update 推送，
// 如 AI 总结的会话标题；前端据此实时刷新侧栏标题，无需重新进入会话）。携带 sessionId（同上）。
func (h *Handler) BroadcastSessionInfo(sessionID string, sessionInfo interface{}) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:        MsgTypeSessionInfo,
		SessionID:   sessionID,
		SessionInfo: sessionInfo,
	})
}

// BroadcastError 向指定会话广播错误消息（携带 sessionId）
func (h *Handler) BroadcastError(sessionID, code, message string) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:      MsgTypeError,
		SessionID: sessionID,
		Code:      code,
		Message:   message,
	})
}

// GetClientCount 获取当前连接数
func (h *Handler) GetClientCount() int {
	h.hub.mu.RLock()
	defer h.hub.mu.RUnlock()
	return len(h.hub.clients)
}

// GetSessionClients 获取指定会话的连接数
func (h *Handler) GetSessionClients(sessionID string) int {
	h.hub.mu.RLock()
	defer h.hub.mu.RUnlock()

	count := 0
	for client := range h.hub.clients {
		if client.IsSubscribed(sessionID) {
			count++
		}
	}
	return count
}

// CloseAll 关闭所有连接（用于优雅关闭）。
// 注意：不能持有 hub.mu 遍历时逐个 Close——client.Close 会向无缓冲 unregister
// channel 发送，hub.Run 消费时需获取写锁，而写锁等待本函数持有的读锁 → 死锁。
// 因此先快照再释放锁，最后逐个关闭。
func (h *Handler) CloseAll() {
	h.hub.mu.RLock()
	clients := make([]*Client, 0, len(h.hub.clients))
	for client := range h.hub.clients {
		clients = append(clients, client)
	}
	h.hub.mu.RUnlock()

	for _, client := range clients {
		client.Close()
	}
}
