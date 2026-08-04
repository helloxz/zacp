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
	hub       *Hub
	conn      *websocket.Conn
	send      chan []byte
	sessionID string // 绑定的会话 ID
	agentID   string // 使用的 Agent ID
	bridge    *EventBridge // 事件桥接器
	closed    bool
	mu        sync.Mutex
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
		// 无绑定连接（GET /api/v1/ws）通过消息里的 sessionId/agentId 动态绑定，
		// 使后续事件/turn.done 广播（按 GetSessionID 匹配）能回送到本连接。
		if msg.SessionID != "" {
			c.BindSession(msg.SessionID, msg.AgentID)
		}
		if bridge != nil {
			go func() {
				if err := bridge.HandlePrompt(ctx, msg.SessionID, msg.AgentID, msg.Message); err != nil {
					c.hub.log.Error("handle prompt error", "error", err)
					c.Send(ServerMessage{
						Type:    MsgTypeError,
						Code:    "PROMPT_ERROR",
						Message: err.Error(),
					})
				}
			}()
		}

	case MsgTypeCancel:
		c.hub.log.Info("received cancel", "sessionID", msg.SessionID)
		if msg.SessionID != "" {
			c.BindSession(msg.SessionID, msg.AgentID)
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

// BindSession 绑定会话
func (c *Client) BindSession(sessionID, agentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionID = sessionID
	c.agentID = agentID
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
