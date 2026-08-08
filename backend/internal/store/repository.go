package store

import (
	"time"

	"gorm.io/gorm"

	"github.com/helloxz/zacp/internal/model"
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

// GetByPathIncludingDeleted 根据路径查找工作目录（含已软删除记录）。
// 用于「移除项目后再次添加相同路径」时找回旧记录并整体恢复（项目 + 其下会话 + 消息）。
func (r *WorkspaceRepository) GetByPathIncludingDeleted(path string) (*model.Workspace, error) {
	var workspace model.Workspace
	err := r.db.Unscoped().Where("path = ?", path).First(&workspace).Error
	if err != nil {
		return nil, err
	}
	return &workspace, nil
}

// Restore 恢复软删除的工作目录（清空 deleted_at；恢复后项目连同其下会话重新可见）。
func (r *WorkspaceRepository) Restore(id uint) error {
	return r.db.Unscoped().Model(&model.Workspace{}).
		Where("id = ?", id).
		Update("deleted_at", nil).Error
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

// ListByWorkspace 列出工作目录下的所有会话（排除草稿：草稿不进项目会话列表）
func (r *SessionRepository) ListByWorkspace(workspaceID uint) ([]model.Session, error) {
	var sessions []model.Session
	err := r.db.Where("workspace_id = ? AND is_draft = ?", workspaceID, false).
		Order("updated_at DESC").
		Find(&sessions).Error
	return sessions, err
}

// ListRecent 列出最近活跃的会话（全局，跨工作区；预加载 Workspace 供前端分组展示）。
// 草稿会话（is_draft=true）不进侧栏列表：它们是为预览配置项而隐式创建的，
// 尚无对话内容，只有发首条 prompt 转正后才展示。
func (r *SessionRepository) ListRecent(limit int) ([]model.Session, error) {
	var sessions []model.Session
	err := r.db.Preload("Workspace").
		Where("is_draft = ?", false).
		Order("updated_at DESC").
		Limit(limit).
		Find(&sessions).Error
	return sessions, err
}

// Update 更新会话
func (r *SessionRepository) Update(session *model.Session) error {
	return r.db.Save(session).Error
}

// PromoteFromDraft 草稿转正：is_draft 置 false（发首条 prompt 后调用，使会话进入侧栏列表）。
func (r *SessionRepository) PromoteFromDraft(id uint) error {
	return r.db.Model(&model.Session{}).
		Where("id = ?", id).
		Update("is_draft", false).Error
}

// UpdateACPSessionID 更新 ACP Session ID
func (r *SessionRepository) UpdateACPSessionID(id uint, acpSessionID string) error {
	return r.db.Model(&model.Session{}).
		Where("id = ?", id).
		Update("acp_session_id", acpSessionID).Error
}

// GetByACPSessionID 根据 ACP 协议层 session ID 获取会话（WS prompt 落库路由用）。
// 预加载 Workspace：服务端重启后 ACP session 失效需重建时，用 workspace.Path 作为新 session 的 cwd。
func (r *SessionRepository) GetByACPSessionID(acpSessionID string) (*model.Session, error) {
	var session model.Session
	err := r.db.Preload("Workspace").
		Where("acp_session_id = ?", acpSessionID).
		First(&session).Error
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

// UpdateAvailableCommands 更新会话可用 / 命令 JSON（agent 经 available_commands_update 通告）
func (r *SessionRepository) UpdateAvailableCommands(id uint, commandsJSON string) error {
	return r.db.Model(&model.Session{}).
		Where("id = ?", id).
		Update("available_commands", commandsJSON).Error
}

// Delete 物理删除会话。
// 说明：消息已在 service.DeleteSession 先行物理删除，会话本身也没有任何恢复入口
// （前端无恢复 UI），软删除只会留下无关联消息的空行，故这里直接 Unscoped 物理删除。
// Workspace 的归档（Archived）软删除语义不受影响。
func (r *SessionRepository) Delete(id uint) error {
	return r.db.Unscoped().Delete(&model.Session{}, id).Error
}

// PurgeSoftDeletedDrafts 物理删除「已软删的草稿会话」及其消息，返回清理条数。
// 背景：草稿（is_draft=true）是隐式 /new 探测产生的临时会话，用户不可见；
// 历史软删的草稿既无恢复入口也无保留价值（当前数据 125 条全部无消息），
// 每次启动时清一次，防止回收站永久堆积。
// 非草稿的软删会话（用户手动删除）不在清理范围，保持既有语义。
func (r *SessionRepository) PurgeSoftDeletedDrafts() (int64, error) {
	var ids []uint
	if err := r.db.Model(&model.Session{}).
		Unscoped().
		Where("deleted_at IS NOT NULL AND is_draft = ?", true).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// 先物理删消息（Message 无软删列，Delete 即物理删），避免孤儿行
		if err := tx.Where("session_id IN ?", ids).Delete(&model.Message{}).Error; err != nil {
			return err
		}
		// 物理删会话行（Unscoped 绕过软删过滤器）
		return tx.Unscoped().Where("id IN ?", ids).Delete(&model.Session{}).Error
	})
	if err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
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

// ListBySessionPaginated 从最新消息开始分页，并将当前窗口按消息 ID 升序返回。
// offset 以最新端为基准：offset=0 返回最新 limit 条；恢复升序是为了保持聊天 UI 的时间线顺序。
func (r *MessageRepository) ListBySessionPaginated(sessionID uint, limit, offset int) ([]model.Message, error) {
	var messages []model.Message
	err := r.db.Where("session_id = ?", sessionID).
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Find(&messages).Error
	if err != nil {
		return nil, err
	}

	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	return messages, nil
}

// ListBySessionAfterID 列出指定消息 ID 之后新增的消息。
// ID 是 messages 表的自增主键，用于 turn 完成后的增量同步；只读取新行，
// 不重新扫描会话已有历史，返回顺序与消息创建顺序一致。
func (r *MessageRepository) ListBySessionAfterID(sessionID, afterID uint) ([]model.Message, error) {
	var messages []model.Message
	err := r.db.Where("session_id = ? AND id > ?", sessionID, afterID).
		Order("id ASC").
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
