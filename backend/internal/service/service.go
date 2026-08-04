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
}

// NewSessionService 创建会话服务
func NewSessionService(workspaceRepo *store.WorkspaceRepository, sessionRepo *store.SessionRepository, msgRepo *store.MessageRepository, mgr *manager.Manager) *SessionService {
	return &SessionService{
		workspaceRepo: workspaceRepo,
		sessionRepo:   sessionRepo,
		msgRepo:       msgRepo,
		mgr:           mgr,
	}
}

// CreateSession 创建会话（启动 agent + 创建 ACP session + 持久化）
func (s *SessionService) CreateSession(ctx context.Context, workspaceID uint, agentID string) (*model.Session, error) {
	// 验证工作目录存在
	workspace, err := s.workspaceRepo.GetByID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
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

	// 创建 ACP session
	acpSessionID, err := s.mgr.CreateSession(ctx, agentID, workspace.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to create ACP session: %w", err)
	}

	// 创建数据库记录
	session := &model.Session{
		WorkspaceID:  workspaceID,
		AgentID:      agentID,
		ACPSessionID: acpSessionID,
		Title:        "新会话",
		Status:       model.SessionStatusActive,
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
