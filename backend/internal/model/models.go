// Package model 定义 GORM 数据模型和 DTO。
// 所有表结构集中在此处，store 层负责持久化操作。
package model

import (
	"time"

	"gorm.io/gorm"
)

// Workspace 工作目录（用户选择的项目路径）。
// 归档用 Archived 字段，不物理删除，会话与消息保留；同 path 再添加可解除归档。
type Workspace struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Path      string         `gorm:"uniqueIndex;not null" json:"path"` // 绝对路径
	Name      string         `gorm:"" json:"name,omitempty"`           // 可选显示名
	// IsDefault 标记是否为 config session.default_cwd 对应的默认工作区。
	IsDefault bool `gorm:"index;default:false" json:"isDefault"`
	// Archived 为 true 时侧栏隐藏，数据与下属 Session 仍保留。
	Archived  bool           `gorm:"index;default:false" json:"archived"`
	LastUsed  time.Time      `gorm:"index" json:"lastUsed"` // 最近使用时间
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// 关联
	Sessions []Session `gorm:"foreignKey:WorkspaceID" json:"sessions,omitempty"`
}

// TableName 指定表名。
func (Workspace) TableName() string { return "workspaces" }

// Session 会话窗口（属于某个工作目录，绑定某个 Agent）。
type Session struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	WorkspaceID  uint           `gorm:"index;not null" json:"workspaceId"` // 所属工作目录
	AgentID      string         `gorm:"index;not null" json:"agentId"`     // Agent 配置 ID
	ACPSessionID string         `gorm:"" json:"acpSessionId,omitempty"`    // ACP 协议层 session ID
	Title        string         `gorm:"" json:"title"`                     // 会话标题（可从首轮对话生成）
	Status       SessionStatus  `gorm:"index;default:'active'" json:"status"`
	// IsDraft 草稿标记：隐式 session/new 探测创建的会话为 true，不进侧栏列表；
	// 用户发出首条 prompt 即转正（置 false），此后作为正常会话展示。
	// 见设计文档「新建会话流程：隐式草稿 → 转正」。
	IsDraft bool `gorm:"index;default:false" json:"isDraft"`
	// ConfigOptions 会话配置项（模型/思考强度/mode 等）原始 JSON（model.ConfigOptionDTO 数组）；
	// 由 service 转换存储，不直接暴露给前端 JSON（经 /config-options 端点返回）。
	ConfigOptions string    `gorm:"type:text" json:"-"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`

	// 关联
	Workspace Workspace `gorm:"foreignKey:WorkspaceID" json:"workspace,omitempty"`
	Messages  []Message `gorm:"foreignKey:SessionID" json:"messages,omitempty"`
}

// TableName 指定表名。
func (Session) TableName() string { return "sessions" }

// SessionStatus 会话状态。
type SessionStatus string

const (
	SessionStatusActive SessionStatus = "active" // 活跃中
	SessionStatusClosed SessionStatus = "closed" // 已关闭
	SessionStatusError  SessionStatus = "error"  // 出错
)

// Message 对话消息（属于某个会话）。
type Message struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	SessionID uint      `gorm:"index;not null" json:"sessionId"` // 所属会话
	Role      string    `gorm:"not null" json:"role"`            // user | assistant | system
	Content   string    `gorm:"type:text" json:"content"`        // 消息文本内容
	Events    string    `gorm:"type:text" json:"events"`         // 完整事件 JSON（工具调用等）
	CreatedAt time.Time `json:"createdAt"`

	// 关联
	Session Session `gorm:"foreignKey:SessionID" json:"session,omitempty"`
}

// TableName 指定表名。
func (Message) TableName() string { return "messages" }

// SchemaMigration 记录已执行的数据库迁移版本。
type SchemaMigration struct {
	Version   int       `gorm:"primaryKey" json:"version"`
	AppliedAt time.Time `json:"appliedAt"`
}

// TableName 指定表名。
func (SchemaMigration) TableName() string { return "schema_migrations" }
