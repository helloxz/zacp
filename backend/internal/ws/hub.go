package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Client 代表一个 WebSocket 客户端连接
type Client struct {
	hub        *Hub
	conn       *websocket.Conn
	send       chan []byte
	sessionID  string          // 最近一次订阅的会话 ID（兼容旧查询语义）
	agentID    string          // 使用的 Agent ID
	subscribed map[string]bool // 订阅的会话集合（事件按会话路由；连接关闭前不清空）
	bridge     *EventBridge    // 事件桥接器
	closed     bool
	mu         sync.Mutex
}

// Hub 管理所有 WebSocket 连接
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
	log        *slog.Logger
}

// NewHub 创建新的 Hub
func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
		log:        log,
	}
}

// Run 启动 Hub 的主循环
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			h.log.Info("client connected", "total", len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			h.log.Info("client disconnected", "total", len(h.clients))

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// 客户端发送缓冲区满，断开连接
					go func(c *Client) {
						h.unregister <- c
					}(client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// NewClient 创建新的客户端连接
func (h *Hub) NewClient(conn *websocket.Conn) *Client {
	client := &Client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, 256),
	}
	h.register <- client
	return client
}

// ReadPump 从 WebSocket 读取消息
func (c *Client) ReadPump(ctx context.Context) {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close(websocket.StatusNormalClosure, "")
	}()

	c.conn.SetReadLimit(512 * 1024) // 512KB

	for {
		_, message, err := c.conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
				websocket.CloseStatus(err) == websocket.StatusGoingAway {
				c.hub.log.Info("client closed connection")
			} else {
				c.hub.log.Error("read error", "error", err)
			}
			break
		}

		var msg ClientMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			c.hub.log.Error("unmarshal error", "error", err)
			continue
		}

		c.handleMessage(ctx, msg)
	}
}

// WritePump 向 WebSocket 写入消息
func (c *Client) WritePump(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close(websocket.StatusNormalClosure, "")
	}()

	for {
		select {
		case message, ok := <-c.send:
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Write(writeCtx, websocket.MessageText, message)
			cancel()
			if err != nil {
				c.hub.log.Error("write error", "error", err)
				return
			}
			if !ok {
				// send channel was closed
				return
			}

		case <-ticker.C:
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Ping(writeCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// Send 发送消息给客户端
func (c *Client) Send(msg ServerMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		c.hub.log.Error("marshal error", "error", err)
		return
	}

	select {
	case c.send <- data:
	default:
		c.hub.log.Warn("send buffer full, dropping message")
	}
}

// handleMessage 处理客户端消息
func (c *Client) handleMessage(ctx context.Context, msg ClientMessage) {
	c.mu.Lock()
	bridge := c.bridge
	c.mu.Unlock()

	switch msg.Type {
	case MsgTypePrompt:
		c.hub.log.Info("received prompt", "sessionID", msg.SessionID, "message", msg.Message)
		// 无绑定连接（GET /api/v1/ws）通过消息里的 sessionId/agentId 动态订阅，
		// 使该会话的事件/turn.done 广播（按订阅集合匹配）能回送到本连接。
		// 订阅为集合语义：同一连接可同时跟踪多个会话（同 agent 排队、跨 agent 并行），
		// 避免旧「单绑定覆盖」导致 A 会话进行中发 B 时 A 的广播丢失。
		if msg.SessionID != "" {
			c.SubscribeSession(msg.SessionID, msg.AgentID)
		}
		if bridge != nil {
			go func() {
				if err := bridge.HandlePrompt(ctx, msg.SessionID, msg.AgentID, msg.Message); err != nil {
					c.hub.log.Error("handle prompt error", "error", err)
					// 错误广播必须带 sessionId：前端按会话路由复位状态，
					// 缺省时回退当前会话，A 会话出错会误伤排队中的 B（见 session.ts error 分支）
					c.Send(ServerMessage{
						Type:      MsgTypeError,
						SessionID: msg.SessionID,
						Code:      "PROMPT_ERROR",
						Message:   err.Error(),
					})
				}
			}()
		}

	case MsgTypeCancel:
		c.hub.log.Info("received cancel", "sessionID", msg.SessionID)
		if msg.SessionID != "" {
			c.SubscribeSession(msg.SessionID, msg.AgentID)
		}
		if bridge != nil {
			go func() {
				if err := bridge.HandleCancel(ctx, msg.SessionID, msg.AgentID); err != nil {
					c.hub.log.Error("handle cancel error", "error", err)
				}
			}()
		}

	case MsgTypePermission:
		c.hub.log.Info("received permission", "permissionID", msg.PermissionID, "optionID", msg.OptionID)
		if bridge != nil && msg.PermissionID != "" && msg.OptionID != "" {
			bridge.ResolvePermission(msg.PermissionID, msg.OptionID)
		}

	case MsgTypePing:
		c.Send(ServerMessage{Type: MsgTypePong})

	default:
		c.hub.log.Warn("unknown message type", "type", msg.Type)
	}
}

// SetBridge 设置事件桥接器
func (c *Client) SetBridge(bridge *EventBridge) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bridge = bridge
}

// BindSession 绑定会话（兼容旧调用方）：语义等同 SubscribeSession。
func (c *Client) BindSession(sessionID, agentID string) {
	c.SubscribeSession(sessionID, agentID)
}

// SubscribeSession 订阅会话：本连接将收到该会话的事件/turn.done/权限等广播。
// 与旧「单绑定覆盖」不同，订阅是集合语义——同一连接可同时跟踪多个会话
// （同 agent 排队、跨 agent 并行时，执行中会话的广播必须继续送达）。
// 订阅保留到连接关闭才清空：会话结束后无新广播，残留无害，实现最简单。
func (c *Client) SubscribeSession(sessionID, agentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sessionID != "" {
		if c.subscribed == nil {
			c.subscribed = make(map[string]bool)
		}
		c.subscribed[sessionID] = true
		c.sessionID = sessionID
	}
	if agentID != "" {
		c.agentID = agentID
	}
}

// RebindSubscription 把本连接对 oldID 的订阅迁移到 newID（ACP 会话恢复/重建后调用）。
// 若「最近订阅」指针（sessionID）正指向旧 id，也一并更新，避免后续按
// GetSessionID 的旧语义（如 cancel 兜底）继续用失效 id。
func (c *Client) RebindSubscription(oldID, newID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscribed[oldID] {
		delete(c.subscribed, oldID)
		c.subscribed[newID] = true
	}
	if c.sessionID == oldID {
		c.sessionID = newID
	}
}

// IsSubscribed 检查是否订阅了指定会话（广播按会话匹配用）
func (c *Client) IsSubscribed(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subscribed[sessionID]
}

// GetSessionID 获取绑定的会话 ID
func (c *Client) GetSessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

// GetAgentID 获取使用的 Agent ID
func (c *Client) GetAgentID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agentID
}

// Close 关闭客户端连接
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.hub.unregister <- c
	}
}
