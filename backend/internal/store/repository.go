package store

import (
	"time"

	"gorm.io/gorm"

	"github.com/zacp/zacp/internal/model"
)

// WorkspaceRepository 工作目录数据访问
type WorkspaceRepository struct {
	db *gorm.DB
}

// NewWorkspaceRepository 创建工作目录仓储
func NewWorkspaceRepository(db *gorm.DB) *WorkspaceRepository {
	return &WorkspaceRepository{db: db}
}

// Create 创建工作目录
func (r *WorkspaceRepository) Create(workspace *model.Workspace) error {
	return r.db.Create(workspace).Error
}

// GetByID 根据 ID 获取工作目录
func (r *WorkspaceRepository) GetByID(id uint) (*model.Workspace, error) {
	var workspace model.Workspace
	err := r.db.First(&workspace, id).Error
	if err != nil {
		return nil, err
	}
	return &workspace, nil
}

// GetByPath 根据路径获取工作目录
func (r *WorkspaceRepository) GetByPath(path string) (*model.Workspace, error) {
	var workspace model.Workspace
	err := r.db.Where("path = ?", path).First(&workspace).Error
	if err != nil {
		return nil, err
	}
	return &workspace, nil
}

// List 列出所有工作目录（按最近使用排序）
func (r *WorkspaceRepository) List() ([]model.Workspace, error) {
	var workspaces []model.Workspace
	err := r.db.Order("last_used DESC").Find(&workspaces).Error
	return workspaces, err
}

// GetDefault 获取默认工作区（is_default = true；config session.default_cwd 对应的路径）。
// 未标记时返回 error，调用方回退到 defaultCwd 路径。
func (r *WorkspaceRepository) GetDefault() (*model.Workspace, error) {
	var workspace model.Workspace
	err := r.db.Where("is_default = ?", true).First(&workspace).Error
	if err != nil {
		return nil, err
	}
	return &workspace, nil
}

// Update 更新工作目录
func (r *WorkspaceRepository) Update(workspace *model.Workspace) error {
	return r.db.Save(workspace).Error
}

// Touch 更新最近使用时间
func (r *WorkspaceRepository) Touch(id uint) error {
	return r.db.Model(&model.Workspace{}).
		Where("id = ?", id).
		Update("last_used", time.Now()).Error
}

// Delete 删除工作目录（软删除）
func (r *WorkspaceRepository) Delete(id uint) error {
	return r.db.Delete(&model.Workspace{}, id).Error
}

// SessionRepository 会话数据访问
type SessionRepository struct {
	db *gorm.DB
}

// NewSessionRepository 创建会话仓储
func NewSessionRepository(db *gorm.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

// Create 创建会话
func (r *SessionRepository) Create(session *model.Session) error {
	return r.db.Create(session).Error
}

// GetByID 根据 ID 获取会话
func (r *SessionRepository) GetByID(id uint) (*model.Session, error) {
	var session model.Session
	err := r.db.Preload("Workspace").First(&session, id).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// ListByWorkspace 列出工作目录下的所有会话
func (r *SessionRepository) ListByWorkspace(workspaceID uint) ([]model.Session, error) {
	var sessions []model.Session
	err := r.db.Where("workspace_id = ?", workspaceID).
		Order("updated_at DESC").
		Find(&sessions).Error
	return sessions, err
}

// ListRecent 列出最近活跃的会话（全局，跨工作区；预加载 Workspace 供前端分组展示）。
func (r *SessionRepository) ListRecent(limit int) ([]model.Session, error) {
	var sessions []model.Session
	err := r.db.Preload("Workspace").
		Order("updated_at DESC").
		Limit(limit).
		Find(&sessions).Error
	return sessions, err
}

// Update 更新会话
func (r *SessionRepository) Update(session *model.Session) error {
	return r.db.Save(session).Error
}

// UpdateACPSessionID 更新 ACP Session ID
func (r *SessionRepository) UpdateACPSessionID(id uint, acpSessionID string) error {
	return r.db.Model(&model.Session{}).
		Where("id = ?", id).
		Update("acp_session_id", acpSessionID).Error
}

// GetByACPSessionID 根据 ACP 协议层 session ID 获取会话（WS prompt 落库路由用）
func (r *SessionRepository) GetByACPSessionID(acpSessionID string) (*model.Session, error) {
	var session model.Session
	err := r.db.Where("acp_session_id = ?", acpSessionID).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// Touch 更新会话最近活跃时间（WS turn 完成后驱动侧栏排序）
func (r *SessionRepository) Touch(id uint) error {
	return r.db.Model(&model.Session{}).
		Where("id = ?", id).
		Update("updated_at", time.Now()).Error
}

// UpdateStatus 更新会话状态
func (r *SessionRepository) UpdateStatus(id uint, status model.SessionStatus) error {
	return r.db.Model(&model.Session{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// UpdateTitle 更新会话标题
func (r *SessionRepository) UpdateTitle(id uint, title string) error {
	return r.db.Model(&model.Session{}).
		Where("id = ?", id).
		Update("title", title).Error
}

// UpdateConfigOptions 更新会话配置项 JSON（模型/思考强度/mode 等）
func (r *SessionRepository) UpdateConfigOptions(id uint, configJSON string) error {
	return r.db.Model(&model.Session{}).
		Where("id = ?", id).
		Update("config_options", configJSON).Error
}

// Delete 删除会话（软删除）
func (r *SessionRepository) Delete(id uint) error {
	return r.db.Delete(&model.Session{}, id).Error
}

// MessageRepository 消息数据访问
type MessageRepository struct {
	db *gorm.DB
}

// NewMessageRepository 创建消息仓储
func NewMessageRepository(db *gorm.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

// Create 创建消息
func (r *MessageRepository) Create(message *model.Message) error {
	return r.db.Create(message).Error
}

// ListBySession 列出会话的所有消息
func (r *MessageRepository) ListBySession(sessionID uint) ([]model.Message, error) {
	var messages []model.Message
	err := r.db.Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Find(&messages).Error
	return messages, err
}

// ListBySessionPaginated 分页列出会话消息
func (r *MessageRepository) ListBySessionPaginated(sessionID uint, limit, offset int) ([]model.Message, error) {
	var messages []model.Message
	err := r.db.Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Limit(limit).
		Offset(offset).
		Find(&messages).Error
	return messages, err
}

// CountBySession 统计会话消息数量
func (r *MessageRepository) CountBySession(sessionID uint) (int64, error) {
	var count int64
	err := r.db.Model(&model.Message{}).
		Where("session_id = ?", sessionID).
		Count(&count).Error
	return count, err
}

// DeleteBySession 删除会话的所有消息
func (r *MessageRepository) DeleteBySession(sessionID uint) error {
	return r.db.Where("session_id = ?", sessionID).
		Delete(&model.Message{}).Error
}
