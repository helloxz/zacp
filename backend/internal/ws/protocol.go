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
	MsgTypeTurnStarted       MessageType = "turn.started"       // 一轮对话真正开始执行（排队门闩获取成功；前端据此把「排队中」切换为流式）
	MsgTypeTurnDone          MessageType = "turn.done"          // 一轮对话完成
	MsgTypePermissionRequest MessageType = "permission.request" // 权限请求
	MsgTypeConfigOptions     MessageType = "configOptions"      // 配置项更新（agent 推送，如切模型后出现思维强度选项）
	MsgTypeSlashCommands     MessageType = "slashCommands"      // 可用 / 命令更新（agent 经 available_commands_update 推送）
	MsgTypeSessionInfo       MessageType = "sessionInfo"        // 会话信息更新（agent 经 session_info_update 推送，如 AI 总结标题）
	MsgTypeSessionRecovered  MessageType = "session.recovered"  // ACP 会话恢复/重建完成：旧 id → 新 id（订阅已自动迁移，前端据此更新 id 映射）
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

	// configOptions 消息字段（agent 经 session/update 推送的配置项列表）
	ConfigOptions interface{} `json:"configOptions,omitempty"`

	// slashCommands 消息字段（agent 经 available_commands_update 推送的 / 命令列表）
	SlashCommands interface{} `json:"slashCommands,omitempty"`

	// sessionInfo 消息字段（agent 经 session_info_update 推送的会话信息，如 { title }）
	SessionInfo interface{} `json:"sessionInfo,omitempty"`

	// session.recovered 消息字段：ACP 会话恢复/重建完成时的旧/新 session id
	// （旧 id 的订阅已自动迁移到新 id，前端按旧 id 更新本地映射）
	OldSessionID string `json:"oldSessionId,omitempty"`
	NewSessionID string `json:"newSessionId,omitempty"`

	// error 消息字段
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}
