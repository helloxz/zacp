package ws

import (
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

	// 启动读写协程
	ctx := r.Context()
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

		// 启动读写协程
		ctx := r.Context()
		go client.WritePump(ctx)
		go client.ReadPump(ctx)

		h.log.Info("websocket connection established with session",
			"remote", r.RemoteAddr,
			"sessionID", sessionID,
			"agentID", agentID)
	}
}

// BroadcastToSession 向指定会话的所有连接广播消息
func (h *Handler) BroadcastToSession(sessionID string, msg ServerMessage) {
	h.hub.mu.RLock()
	defer h.hub.mu.RUnlock()

	for client := range h.hub.clients {
		if client.GetSessionID() == sessionID {
			client.Send(msg)
		}
	}
}

// BroadcastEvent 向指定会话广播事件
func (h *Handler) BroadcastEvent(sessionID string, event interface{}) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:  MsgTypeEvent,
		Event: event,
	})
}

// BroadcastTurnDone 向指定会话广播轮次完成消息
func (h *Handler) BroadcastTurnDone(sessionID, reply, stopReason string) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:       MsgTypeTurnDone,
		Reply:      reply,
		StopReason: stopReason,
	})
}

// BroadcastPermissionRequest 向指定会话广播权限请求
func (h *Handler) BroadcastPermissionRequest(sessionID, permissionID string, toolCall, options interface{}) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:         MsgTypePermissionRequest,
		PermissionID: permissionID,
		ToolCall:     toolCall,
		Options:      options,
	})
}

// BroadcastError 向指定会话广播错误消息
func (h *Handler) BroadcastError(sessionID, code, message string) {
	h.BroadcastToSession(sessionID, ServerMessage{
		Type:    MsgTypeError,
		Code:    code,
		Message: message,
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
		if client.GetSessionID() == sessionID {
			count++
		}
	}
	return count
}

// CloseAll 关闭所有连接（用于优雅关闭）
func (h *Handler) CloseAll() {
	h.hub.mu.RLock()
	defer h.hub.mu.RUnlock()

	for client := range h.hub.clients {
		client.Close()
	}
}
