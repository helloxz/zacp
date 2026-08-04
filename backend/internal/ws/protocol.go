package ws

// MessageType 定义 WebSocket 消息类型
type MessageType string

const (
	// 客户端 → 服务端
	MsgTypePrompt     MessageType = "prompt"     // 发送用户消息
	MsgTypeCancel     MessageType = "cancel"     // 取消当前操作
	MsgTypePermission MessageType = "permission" // 权限选择结果
	MsgTypePing       MessageType = "ping"       // 心跳

	// 服务端 → 客户端
	MsgTypeSessionReady      MessageType = "session.ready"      // 会话就绪确认
	MsgTypeEvent             MessageType = "event"              // 流式事件（token、工具调用等）
	MsgTypeTurnDone          MessageType = "turn.done"          // 一轮对话完成
	MsgTypePermissionRequest MessageType = "permission.request" // 权限请求
	MsgTypeError             MessageType = "error"              // 错误通知
	MsgTypePong              MessageType = "pong"               // 心跳响应
)

// ClientMessage 客户端发送的消息
type ClientMessage struct {
	Type MessageType `json:"type"`

	// prompt / cancel 消息字段
	SessionID string `json:"sessionId,omitempty"`
	// AgentID 用于无绑定连接（GET /api/v1/ws）时标识 agent；绑定连接可省略。
	AgentID string `json:"agentId,omitempty"`
	Message string `json:"message,omitempty"`

	// permission 消息字段
	PermissionID string `json:"permissionId,omitempty"`
	OptionID     string `json:"optionId,omitempty"`
}

// ServerMessage 服务端发送的消息
type ServerMessage struct {
	Type MessageType `json:"type"`

	// session.ready 消息字段
	SessionID string `json:"sessionId,omitempty"`
	AgentID   string `json:"agentId,omitempty"`

	// event 消息字段
	Event interface{} `json:"event,omitempty"`

	// turn.done 消息字段
	Reply      string `json:"reply,omitempty"`
	StopReason string `json:"stopReason,omitempty"`

	// permission.request 消息字段
	PermissionID string      `json:"permissionId,omitempty"`
	ToolCall     interface{} `json:"toolCall,omitempty"`
	Options      interface{} `json:"options,omitempty"`

	// error 消息字段
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}
