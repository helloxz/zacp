package ws

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"github.com/helloxz/zacp/internal/auth"
)

// wsAuthProtocolPrefix WebSocket 子协议前缀，登录 token 经它携带：
// 浏览器 WebSocket 无法设置自定义 header（Authorization 带不过去），
// 而把 token 放进 ?token= 会进访问日志——子协议由浏览器只在握手时发送、
// 不回显到 URL，是折中方案。格式："zacp-auth.<token>"（token 为 hex，
// 满足 RFC 7230 tchar 子协议字符集）。
const wsAuthProtocolPrefix = "zacp-auth."

// Handler WebSocket  HTTP 处理器
type Handler struct {
	hub     *Hub
	log     *slog.Logger
	authSvc *auth.Service // 认证服务；nil 或未启用时不校验握手
}

// NewHandler 创建 WebSocket 处理器
// authSvc 可为 nil（认证未启用时保持原有行为）。
func NewHandler(hub *Hub, log *slog.Logger, authSvc *auth.Service) *Handler {
	return &Handler{
		hub:     hub,
		log:     log,
		authSvc: authSvc,
	}
}

// firstSubprotocol 从握手请求取首个客户端子协议（RFC 6455 只要求服务端回显一个；
// 客户端可能一次请求多个，逗号分隔，前端固定只发一个，此函数兜底第三方/手动客户端）。
func firstSubprotocol(r *http.Request) string {
	proto := r.Header.Get("Sec-WebSocket-Protocol")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	return proto
}

// authSubprotocol 从握手请求提取并校验登录 token（子协议形式 "zacp-auth.<token>"）。
// 返回（token 校验通过后的完整子协议值, 是否放行）：
//   - 认证未启用：放行（保持默认无需登录）；
//   - 认证启用且 token 有效：放行，同时把客户端协议值回传（握手必须回显客户端协议，
//     否则浏览器按 RFC 6455 判定握手失败）；
//   - 其余：拒绝。
func (h *Handler) authSubprotocol(r *http.Request) (string, bool) {
	if h.authSvc == nil || !h.authSvc.Enabled() {
		// 认证未启用：不校验 token，但若客户端带了子协议必须回显——RFC 6455 §4.2.2
		// 要求服务端要么回显一个子协议、要么拒绝握手；101 却不回显会被浏览器判定
		// 握手失败（如 localStorage 残留旧 token、认证随后被关闭的场景）。
		return firstSubprotocol(r), true
	}
	proto := firstSubprotocol(r)
	if proto == "" || !h.authSvc.ValidateMain(strings.TrimPrefix(proto, wsAuthProtocolPrefix)) {
		return "", false
	}
	return proto, true
}

// ServeHTTP 处理 WebSocket 升级请求
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, bridge *EventBridge) {
	proto, ok := h.authSubprotocol(r)
	if !ok {
		h.log.Warn("websocket rejected: invalid auth token", "remote", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	opts := &websocket.AcceptOptions{InsecureSkipVerify: true} // 开发环境允许所有 Origin
	if proto != "" {
		// 回显客户端子协议，浏览器才会确认握手成功
		opts.Subprotocols = []string{proto}
	}
	conn, err := websocket.Accept(w, r, opts)
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
		proto, ok := h.authSubprotocol(r)
		if !ok {
			h.log.Warn("websocket rejected: invalid auth token", "remote", r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		opts := &websocket.AcceptOptions{InsecureSkipVerify: true}
		if proto != "" {
			opts.Subprotocols = []string{proto}
		}
		conn, err := websocket.Accept(w, r, opts)
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

// RebindSession 在 ACP 会话恢复/重建后迁移订阅：旧 session id 失效、
// 事件广播改走新 id，若订阅不迁移，前端将收不到任何事件（表现为「一直 loading」）。
// 对被迁移的连接额外发送 session.recovered（带旧/新 id），前端据此更新本地
// id→DB 会话映射，使后续流式事件能正确路由。
func (h *Handler) RebindSession(oldID, newID string) {
	if oldID == "" || newID == "" || oldID == newID {
		return
	}
	h.hub.mu.RLock()
	defer h.hub.mu.RUnlock()

	for client := range h.hub.clients {
		if client.IsSubscribed(oldID) {
			client.RebindSubscription(oldID, newID)
			client.Send(ServerMessage{
				Type:         MsgTypeSessionRecovered,
				OldSessionID: oldID,
				NewSessionID: newID,
			})
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
// 全局三槽位获取成功、agent 已开始处理本会话 prompt 时发出；前端据此把
// 「排队中（queued）」切换为「流式（streaming）」。
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
