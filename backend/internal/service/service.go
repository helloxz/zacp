// Package service 实现业务逻辑编排
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	acpclient "github.com/zacp/zacp/internal/acp/client"
	"github.com/zacp/zacp/internal/acp/manager"
	"github.com/zacp/zacp/internal/model"
	"github.com/zacp/zacp/internal/store"
	"gorm.io/gorm"
)

// 配置设置相关的可区分错误（handler 据此映射 HTTP 状态码）
var (
	// ErrSessionNotFound 会话不存在或已删除（映射 404）
	ErrSessionNotFound = errors.New("session not found")
	// ErrInvalidArgument 客户端参数非法（空标题/超长等，映射 400）
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrNoACPSession 会话尚未建立 ACP 连接（草稿/连接中断，映射 409）
	ErrNoACPSession = errors.New("session has no acp session")
)

// WorkspaceService 工作目录服务
type WorkspaceService struct {
	repo *store.WorkspaceRepository
}

// NewWorkspaceService 创建工作目录服务
func NewWorkspaceService(repo *store.WorkspaceRepository) *WorkspaceService {
	return &WorkspaceService{repo: repo}
}

// CreateWorkspace 创建工作目录（验证路径存在性）
func (s *WorkspaceService) CreateWorkspace(path string) (*model.Workspace, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("path does not exist: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", absPath)
	}

	// 检查是否已存在（未删除的）
	existing, err := s.repo.GetByPath(absPath)
	if err == nil && existing != nil {
		_ = s.repo.Touch(existing.ID)
		return s.repo.GetByPath(absPath)
	}

	// 同路径存在已软删除记录（曾被「移除」）：整体恢复（项目 + 其下会话 + 消息），
	// 语义对齐设计约定「同 path 再添加可解除归档」。恢复不插入新行，避开 path 唯一索引。
	deleted, derr := s.repo.GetByPathIncludingDeleted(absPath)
	if derr == nil && deleted != nil {
		if rerr := s.repo.Restore(deleted.ID); rerr != nil {
			return nil, fmt.Errorf("failed to restore workspace: %w", rerr)
		}
		_ = s.repo.Touch(deleted.ID)
		restored, gerr := s.repo.GetByPath(absPath)
		if gerr != nil {
			return nil, gerr
		}
		return restored, nil
	}

	workspace := &model.Workspace{
		Path:     absPath,
		// 未显式提供 name 时，默认取路径末尾段作为显示名（如 /data/apps/51job → 51job），
		// 侧栏只展示项目名而非完整路径（见设计文档「项目列表展示」）。
		Name:     defaultWorkspaceName(absPath),
		LastUsed: time.Now(),
	}

	if err := s.repo.Create(workspace); err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	return workspace, nil
}

// GetWorkspace 获取工作目录
func (s *WorkspaceService) GetWorkspace(id uint) (*model.Workspace, error) {
	workspace, err := s.repo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}
	return workspace, nil
}

// ListWorkspaces 列出所有工作目录（按最近使用排序）
func (s *WorkspaceService) ListWorkspaces() ([]model.Workspace, error) {
	return s.repo.List()
}

// DeleteWorkspace 删除工作目录（软删除）
func (s *WorkspaceService) DeleteWorkspace(id uint) error {
	return s.repo.Delete(id)
}

// SessionService 会话服务
type SessionService struct {
	workspaceRepo *store.WorkspaceRepository
	sessionRepo   *store.SessionRepository
	msgRepo       *store.MessageRepository
	mgr           *manager.Manager
	// defaultCwd 是 config session.default_cwd；创建会话未指定工作区时的回退路径。
	defaultCwd string
}

// NewSessionService 创建会话服务
func NewSessionService(workspaceRepo *store.WorkspaceRepository, sessionRepo *store.SessionRepository, msgRepo *store.MessageRepository, mgr *manager.Manager, defaultCwd string) *SessionService {
	return &SessionService{
		workspaceRepo: workspaceRepo,
		sessionRepo:   sessionRepo,
		msgRepo:       msgRepo,
		mgr:           mgr,
		defaultCwd:    defaultCwd,
	}
}

// resolveWorkspace 解析工作区：
//  1. 显式指定 workspaceID → 校验存在；
//  2. workspaceID 为 0（前端未选工作区）→ 按 is_default → defaultCwd 路径 → 按 defaultCwd 新建 的顺序回退。
//
// 保证「未选工作区也能建会话」，语义对齐 config session.default_cwd（见设计文档 §4.1）。
func (s *SessionService) resolveWorkspace(workspaceID uint) (*model.Workspace, error) {
	if workspaceID > 0 {
		ws, err := s.workspaceRepo.GetByID(workspaceID)
		if err != nil {
			return nil, fmt.Errorf("workspace not found: %w", err)
		}
		return ws, nil
	}

	// 1) 已有 is_default 标记
	if ws, err := s.workspaceRepo.GetDefault(); err == nil && ws != nil {
		return ws, nil
	}

	// 2) defaultCwd 已登记为 workspace
	absCwd, err := filepath.Abs(s.defaultCwd)
	if err != nil {
		return nil, fmt.Errorf("invalid default_cwd: %w", err)
	}
	if ws, err := s.workspaceRepo.GetByPath(absCwd); err == nil && ws != nil {
		return ws, nil
	}

	// 3) 校验目录存在并按 defaultCwd 新建（复用 CreateWorkspace 的路径校验语义）
	info, err := os.Stat(absCwd)
	if err != nil {
		return nil, fmt.Errorf("no default workspace and default_cwd unavailable: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("default_cwd is not a directory: %s", absCwd)
	}
	ws := &model.Workspace{Path: absCwd, LastUsed: time.Now()}
	if err := s.workspaceRepo.Create(ws); err != nil {
		return nil, fmt.Errorf("failed to create default workspace: %w", err)
	}
	return ws, nil
}

// CreateSession 创建会话（启动 agent + 创建 ACP session + 持久化）
//
// isDraft=true 表示「隐式草稿会话」：用于空态预览各 agent 的配置项（模型/思考强度），
// 不进侧栏列表；用户发出首条 prompt 时由 HandlePrompt 转正（isDraft=false）。
// 见设计文档「新建会话流程：隐式草稿 → 转正」。
//
// 返回 CreateSessionResult，携带 session 与 agent 下发的 configOptions，
// 供前端空态直接展示配置项下拉（无需再单独请求 /config-options）。
func (s *SessionService) CreateSession(ctx context.Context, workspaceID uint, agentID string, isDraft bool) (*model.CreateSessionResult, error) {
	// 解析工作区（0 → 回退默认工作区）
	workspace, err := s.resolveWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	// 验证 agent 存在
	_, err = s.mgr.GetAgentStatus(agentID)
	if err != nil {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}

	// 启动 agent（如果未启动）
	agentStatus, _ := s.mgr.GetAgentStatus(agentID)
	if !agentStatus.Running {
		if err := s.mgr.StartAgent(ctx, agentID); err != nil {
			return nil, fmt.Errorf("failed to start agent: %w", err)
		}
	}

	// 创建 ACP session（返回 agent 下发的配置项：模型/思考强度/mode 等）
	acpSessionID, configOptions, err := s.mgr.CreateSession(ctx, agentID, workspace.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to create ACP session: %w", err)
	}

	// 序列化配置项 JSON（会话级持久化，前端经 /config-options 端点读取）
	configJSON := ""
	optionDTOs := acpclient.ToConfigOptionDTOs(configOptions)
	if len(optionDTOs) > 0 {
		data, marshalErr := json.Marshal(optionDTOs)
		if marshalErr == nil {
			configJSON = string(data)
		}
	}

	// 创建数据库记录
	// 注意：必须用解析后的 workspace.ID（入参 workspaceID 为 0 时回退到默认工作区，
	// 若仍写回 0 会触发 sessions 外键约束失败）
	session := &model.Session{
		WorkspaceID:   workspace.ID,
		AgentID:       agentID,
		ACPSessionID:  acpSessionID,
		Title:         "新会话",
		Status:        model.SessionStatusActive,
		IsDraft:       isDraft,
		ConfigOptions: configJSON,
	}

	if err := s.sessionRepo.Create(session); err != nil {
		_ = s.mgr.StopAgent(agentID)
		return nil, fmt.Errorf("failed to save session: %w", err)
	}

	// 响应携带 workspace 关联（Create 不预加载）：前端转正后侧栏立即按父项目分组，
	// 无需等待下一次列表刷新（见前端 promoteDraftSession 的兜底逻辑）
	session.Workspace = *workspace

	return &model.CreateSessionResult{
		Session:       session,
		ConfigOptions: optionDTOs,
	}, nil
}

// GetSession 获取会话
func (s *SessionService) GetSession(id uint) (*model.Session, error) {
	session, err := s.sessionRepo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	return session, nil
}

// RenameSession 重命名会话标题（用户手动重命名，仅更新本地 DB 的 title 字段）。
// 不触发 ACP session_info_update；若 agent 后续再推送 AI 总结标题，
// 前端会依据「用户已手动改名」标记跳过覆盖（见 stores/session.ts）。
func (s *SessionService) RenameSession(id uint, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("%w: title must not be empty", ErrInvalidArgument)
	}
	if len([]rune(title)) > 200 {
		return fmt.Errorf("%w: title too long (max 200 chars)", ErrInvalidArgument)
	}
	if _, err := s.sessionRepo.GetByID(id); err != nil {
		return fmt.Errorf("%w: %v", ErrSessionNotFound, err)
	}
	return s.sessionRepo.UpdateTitle(id, title)
}

// ListSessions 列出工作目录下的所有会话
func (s *SessionService) ListSessions(workspaceID uint) ([]model.Session, error) {
	return s.sessionRepo.ListByWorkspace(workspaceID)
}

// ListRecentSessions 列出最近活跃的会话（全局，侧栏数据源）。
func (s *SessionService) ListRecentSessions(limit int) ([]model.Session, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return s.sessionRepo.ListRecent(limit)
}

// DeleteSession 删除会话（停止 agent + 删除消息 + 删除会话）
func (s *SessionService) DeleteSession(id uint) error {
	session, err := s.sessionRepo.GetByID(id)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// 停止 agent
	_ = s.mgr.StopAgent(session.AgentID)

	// 删除消息
	if err := s.msgRepo.DeleteBySession(id); err != nil {
		return fmt.Errorf("failed to delete messages: %w", err)
	}

	return s.sessionRepo.Delete(id)
}

// DeleteDraftSession 删除草稿会话（切 tab / 离开空态时释放旧隐式草稿）。
// 与 DeleteSession 区别：草稿无消息，仅关闭 ACP session + 删 DB 记录，不停 agent 进程
// （agent 进程可能仍在服务其他会话/草稿）。
func (s *SessionService) DeleteDraftSession(ctx context.Context, id uint) error {
	session, err := s.sessionRepo.GetByID(id)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// 尽力关闭 ACP session（agent 不支持 close 能力时忽略错误）
	if session.ACPSessionID != "" {
		_ = s.mgr.CloseSession(ctx, session.AgentID, session.ACPSessionID)
	}

	return s.sessionRepo.Delete(id)
}

// SendMessage 发送消息（保存用户消息 + 发送到 ACP + 保存助手回复）
func (s *SessionService) SendMessage(ctx context.Context, sessionID uint, content string) (*model.Message, error) {
	session, err := s.sessionRepo.GetByID(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}

	if session.Status != model.SessionStatusActive {
		return nil, errors.New("session is not active")
	}

	// 保存用户消息
	userMsg := &model.Message{
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
		CreatedAt: time.Now(),
	}
	if err := s.msgRepo.Create(userMsg); err != nil {
		return nil, fmt.Errorf("failed to save user message: %w", err)
	}

	// 发送到 ACP
	response, err := s.mgr.Prompt(ctx, session.AgentID, session.ACPSessionID, content)
	if err != nil {
		return nil, fmt.Errorf("failed to send to agent: %w", err)
	}

	// 将事件序列化为 JSON 字符串
	eventsJSON := ""
	if len(response.Events) > 0 {
		data, _ := json.Marshal(response.Events)
		eventsJSON = string(data)
	}

	// 保存助手回复
	assistantMsg := &model.Message{
		SessionID: sessionID,
		Role:      "assistant",
		Content:   response.Reply,
		Events:    eventsJSON,
		CreatedAt: time.Now(),
	}
	if err := s.msgRepo.Create(assistantMsg); err != nil {
		return nil, fmt.Errorf("failed to save assistant message: %w", err)
	}

	// 更新会话时间
	_ = s.sessionRepo.Update(session)

	return assistantMsg, nil
}

// GetMessages 获取会话消息（分页）
func (s *SessionService) GetMessages(sessionID uint, limit, offset int) ([]model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.msgRepo.ListBySessionPaginated(sessionID, limit, offset)
}

// CountMessages 统计消息数量
func (s *SessionService) CountMessages(sessionID uint) (int64, error) {
	return s.msgRepo.CountBySession(sessionID)
}

// GetConfigOptions 返回会话配置项（模型/思考强度/mode 等，agent 下发的 configOptions）。
// agent 不支持时返回空数组（前端据此隐藏配置 UI）。
func (s *SessionService) GetConfigOptions(sessionID uint) ([]model.ConfigOptionDTO, error) {
	session, err := s.sessionRepo.GetByID(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	if session.ConfigOptions == "" {
		return []model.ConfigOptionDTO{}, nil
	}
	var opts []model.ConfigOptionDTO
	if err := json.Unmarshal([]byte(session.ConfigOptions), &opts); err != nil {
		return nil, fmt.Errorf("parse config options: %w", err)
	}
	return opts, nil
}

// GetSlashCommands 返回会话可用 / 命令（agent 经 available_commands_update 通告的列表）。
// agent 未通告时返回空数组（前端据此不显示候选面板）。
func (s *SessionService) GetSlashCommands(sessionID uint) ([]model.AvailableCommandDTO, error) {
	session, err := s.sessionRepo.GetByID(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	if session.AvailableCommands == "" {
		return []model.AvailableCommandDTO{}, nil
	}
	var cmds []model.AvailableCommandDTO
	if err := json.Unmarshal([]byte(session.AvailableCommands), &cmds); err != nil {
		return nil, fmt.Errorf("parse slash commands: %w", err)
	}
	return cmds, nil
}

// SetConfigOption 设置会话配置项（如切换模型/思考强度/mode），并回写 DB 中该选项的 currentValue。
// 按选项类型分流：select 走 ValueId，boolean 走 Boolean 变体。
func (s *SessionService) SetConfigOption(ctx context.Context, sessionID uint, optionID, valueID string) error {
	session, err := s.sessionRepo.GetByID(sessionID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("get session: %w", err)
	}
	if session.ACPSessionID == "" {
		return ErrNoACPSession
	}

	// 从已存配置项判断类型（缺省按 select 处理）
	optType := "select"
	var opts []model.ConfigOptionDTO
	if session.ConfigOptions != "" {
		if err := json.Unmarshal([]byte(session.ConfigOptions), &opts); err == nil {
			for _, o := range opts {
				if o.ID == optionID {
					optType = o.Type
					break
				}
			}
		}
	}

	if optType == "boolean" {
		val := valueID == "true" || valueID == "1"
		if err := s.setConfigOptionBooleanWithRecovery(ctx, session, optionID, val); err != nil {
			return err
		}
	} else {
		if err := s.setConfigOptionWithRecovery(ctx, session, optionID, valueID); err != nil {
			return err
		}
	}

	// 回写 DB：更新对应选项的 currentValue（boolean 存 bool，select 存字符串；失败不影响设置结果）
	if len(opts) > 0 {
		for i := range opts {
			if opts[i].ID == optionID {
				if opts[i].Type == "boolean" {
					opts[i].CurrentValue = valueID == "true" || valueID == "1"
				} else {
					opts[i].CurrentValue = valueID
				}
			}
		}
		if data, err := json.Marshal(opts); err == nil {
			_ = s.sessionRepo.UpdateConfigOptions(sessionID, string(data))
		}
	}
	return nil
}

// setConfigOptionWithRecovery 执行一次 select 型配置设置；
// 失败且为「agent 侧会话不存在/已失效」（unknown session，如后端或 agent 重启后
// DB 中的 acp_session_id 在 agent 内存中已丢失）时，自动恢复会话并重试一次，用户无感知。
// 恢复策略与 ws bridge 的 prompt 路径一致（manager.RecoverSession）：
// 优先 ACP session/load 保留 agent 持久化上下文，失败则 session/new 重建并更新 DB。
func (s *SessionService) setConfigOptionWithRecovery(ctx context.Context, session *model.Session, optionID, valueID string) error {
	if err := s.mgr.SetSessionConfigOption(ctx, session.AgentID, session.ACPSessionID, optionID, valueID); err == nil {
		return nil
	} else if !manager.IsUnknownSessionErr(err) {
		return err
	}
	// 会话失效：恢复后重试一次
	if err := s.recoverACPSession(ctx, session); err != nil {
		return fmt.Errorf("session %s lost on agent, recover failed: %w", session.ACPSessionID, err)
	}
	return s.mgr.SetSessionConfigOption(ctx, session.AgentID, session.ACPSessionID, optionID, valueID)
}

// setConfigOptionBooleanWithRecovery boolean 型变体，逻辑同 setConfigOptionWithRecovery。
func (s *SessionService) setConfigOptionBooleanWithRecovery(ctx context.Context, session *model.Session, optionID string, value bool) error {
	if err := s.mgr.SetSessionConfigOptionBoolean(ctx, session.AgentID, session.ACPSessionID, optionID, value); err == nil {
		return nil
	} else if !manager.IsUnknownSessionErr(err) {
		return err
	}
	if err := s.recoverACPSession(ctx, session); err != nil {
		return fmt.Errorf("session %s lost on agent, recover failed: %w", session.ACPSessionID, err)
	}
	return s.mgr.SetSessionConfigOptionBoolean(ctx, session.AgentID, session.ACPSessionID, optionID, value)
}

// recoverACPSession 恢复失效的 ACP session；重建时把新 id 更新到 DB（session 对象同步刷新）。
// cwd 取会话工作区路径（与创建时一致，ACP 协议要求），空则回退 defaultCwd。
func (s *SessionService) recoverACPSession(ctx context.Context, session *model.Session) error {
	cwd := session.Workspace.Path
	if cwd == "" {
		cwd = s.defaultCwd
	}
	newID, rebuilt, err := s.mgr.RecoverSession(ctx, session.AgentID, session.ACPSessionID, cwd)
	if err != nil {
		return err
	}
	if rebuilt {
		if err := s.sessionRepo.UpdateACPSessionID(session.ID, newID); err != nil {
			return fmt.Errorf("update acp session id: %w", err)
		}
		session.ACPSessionID = newID
	}
	return nil
}

// defaultWorkspaceName 取路径末尾段作为默认项目名（/data/apps/51job → 51job）。
// 路径以 / 结尾时取最后一段非空目录名；全空则回退整段路径。
func defaultWorkspaceName(path string) string {
	// 去掉末尾分隔符后再取末尾段，兼容 /data/apps/51job/
	trimmed := strings.TrimRight(path, string(filepath.Separator))
	if trimmed == "" {
		return path
	}
	if idx := strings.LastIndex(trimmed, string(filepath.Separator)); idx >= 0 {
		return trimmed[idx+1:]
	}
	return trimmed
}