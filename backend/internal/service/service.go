// Package service 实现业务逻辑编排
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/zacp/zacp/internal/acp/manager"
	"github.com/zacp/zacp/internal/model"
	"github.com/zacp/zacp/internal/store"
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

	// 检查是否已存在（已存在则更新最近使用时间并返回）
	existing, err := s.repo.GetByPath(absPath)
	if err == nil && existing != nil {
		_ = s.repo.Touch(existing.ID)
		return s.repo.GetByPath(absPath)
	}

	workspace := &model.Workspace{
		Path:     absPath,
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
func (s *SessionService) CreateSession(ctx context.Context, workspaceID uint, agentID string) (*model.Session, error) {
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
	if len(configOptions) > 0 {
		data, marshalErr := json.Marshal(toConfigOptionDTOs(configOptions))
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
		ConfigOptions: configJSON,
	}

	if err := s.sessionRepo.Create(session); err != nil {
		_ = s.mgr.StopAgent(agentID)
		return nil, fmt.Errorf("failed to save session: %w", err)
	}

	return session, nil
}

// GetSession 获取会话
func (s *SessionService) GetSession(id uint) (*model.Session, error) {
	session, err := s.sessionRepo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	return session, nil
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

// SetConfigOption 设置会话配置项（如切换模型/思考强度/mode），并回写 DB 中该选项的 currentValue。
// 按选项类型分流：select 走 ValueId，boolean 走 Boolean 变体。
func (s *SessionService) SetConfigOption(ctx context.Context, sessionID uint, optionID, valueID string) error {
	session, err := s.sessionRepo.GetByID(sessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}
	if session.ACPSessionID == "" {
		return errors.New("session has no acp session")
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
		if err := s.mgr.SetSessionConfigOptionBoolean(ctx, session.AgentID, session.ACPSessionID, optionID, val); err != nil {
			return err
		}
	} else {
		if err := s.mgr.SetSessionConfigOption(ctx, session.AgentID, session.ACPSessionID, optionID, valueID); err != nil {
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

// toConfigOptionDTOs 将 SDK 的 SessionConfigOption 转为对外 DTO（select/boolean 变体展开）。
func toConfigOptionDTOs(opts []acp.SessionConfigOption) []model.ConfigOptionDTO {
	out := make([]model.ConfigOptionDTO, 0, len(opts))
	for _, opt := range opts {
		if sel := opt.Select; sel != nil {
			dto := model.ConfigOptionDTO{
				ID:           string(sel.Id),
				Name:         sel.Name,
				Type:         "select",
				CurrentValue: string(sel.CurrentValue),
			}
			if sel.Description != nil {
				dto.Description = *sel.Description
			}
			if sel.Category != nil {
				dto.Category = string(*sel.Category)
			}
			dto.Options = flattenSelectOptions(sel.Options)
			out = append(out, dto)
		} else if b := opt.Boolean; b != nil {
			dto := model.ConfigOptionDTO{
				ID:           string(b.Id),
				Name:         b.Name,
				Type:         "boolean",
				CurrentValue: b.CurrentValue,
			}
			if b.Description != nil {
				dto.Description = *b.Description
			}
			if b.Category != nil {
				dto.Category = string(*b.Category)
			}
			out = append(out, dto)
		}
	}
	return out
}

// flattenSelectOptions 展开 select 选项（分组结构拍平成平铺列表，供前端下拉）。
func flattenSelectOptions(opts acp.SessionConfigSelectOptions) []model.ConfigOptionValueDTO {
	var values []model.ConfigOptionValueDTO
	if opts.Ungrouped != nil {
		for _, v := range *opts.Ungrouped {
			values = append(values, configOptionValue(v))
		}
	}
	if opts.Grouped != nil {
		for _, g := range *opts.Grouped {
			for _, v := range g.Options {
				values = append(values, configOptionValue(v))
			}
		}
	}
	return values
}

func configOptionValue(v acp.SessionConfigSelectOption) model.ConfigOptionValueDTO {
	dto := model.ConfigOptionValueDTO{Value: string(v.Value), Name: v.Name}
	if v.Description != nil {
		dto.Description = *v.Description
	}
	return dto
}
