package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/zacp/zacp/internal/service"
)

// SessionHandler 会话 HTTP 处理器
type SessionHandler struct {
	svc *service.SessionService
}

// NewSessionHandler 创建会话处理器
func NewSessionHandler(svc *service.SessionService) *SessionHandler {
	return &SessionHandler{svc: svc}
}

// CreateSession 创建新会话
// POST /api/v1/sessions
// workspaceId 可选：为 0 / 缺省时回退默认工作区（见 service.resolveWorkspace）。
// isDraft 可选：true 表示隐式草稿会话（预览配置项，不进侧栏），默认 false。
// 响应携带 session 与 agent 下发的 configOptions，供前端空态直接展示。
func (h *SessionHandler) CreateSession(c *gin.Context) {
	var req struct {
		WorkspaceID uint   `json:"workspaceId"`
		AgentID     string `json:"agentId" binding:"required"`
		IsDraft     bool   `json:"isDraft"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_request", "message": err.Error()},
		})
		return
	}

	result, err := h.svc.CreateSession(c.Request.Context(), req.WorkspaceID, req.AgentID, req.IsDraft)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "create_session_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"session":       result.Session,
		"configOptions": result.ConfigOptions,
	})
}

// GetSession 获取会话详情
// GET /api/v1/sessions/:id
func (h *SessionHandler) GetSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	session, err := h.svc.GetSession(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "session_not_found", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"session": session})
}

// ListSessions 列出工作目录下的所有会话
// GET /api/v1/workspaces/:id/sessions
func (h *SessionHandler) ListSessions(c *gin.Context) {
	workspaceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid workspace id"},
		})
		return
	}

	sessions, err := h.svc.ListSessions(uint(workspaceID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "list_sessions_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

// ListRecentSessions 列出最近活跃的会话（全局，侧栏数据源）
// GET /api/v1/sessions?limit=50
func (h *SessionHandler) ListRecentSessions(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	sessions, err := h.svc.ListRecentSessions(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "list_sessions_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

// GetConfigOptions 获取会话配置项（模型/思考强度/mode 等）
// GET /api/v1/sessions/:id/config-options
func (h *SessionHandler) GetConfigOptions(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	opts, err := h.svc.GetConfigOptions(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "session_not_found", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"configOptions": opts})
}

// SetConfigOption 设置会话配置项（如切换模型/思考强度/mode）
// POST /api/v1/sessions/:id/config-options
func (h *SessionHandler) SetConfigOption(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	var req struct {
		OptionID string `json:"optionId" binding:"required"`
		ValueID  string `json:"valueId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_request", "message": err.Error()},
		})
		return
	}

	if err := h.svc.SetConfigOption(c.Request.Context(), uint(id), req.OptionID, req.ValueID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "set_config_option_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteSession 删除会话
// DELETE /api/v1/sessions/:id
func (h *SessionHandler) DeleteSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	if err := h.svc.DeleteSession(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "delete_session_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusNoContent, nil)
}

// DeleteDraftSession 删除草稿会话（切 tab / 离开空态时释放旧隐式草稿）
// DELETE /api/v1/sessions/:id/draft
func (h *SessionHandler) DeleteDraftSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	if err := h.svc.DeleteDraftSession(c.Request.Context(), uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "delete_draft_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusNoContent, nil)
}

// SendMessage 发送消息到会话
// POST /api/v1/sessions/:id/messages
func (h *SessionHandler) SendMessage(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	var req struct {
		Content string `json:"content" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_request", "message": err.Error()},
		})
		return
	}

	message, err := h.svc.SendMessage(c.Request.Context(), uint(id), req.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "send_message_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": message})
}

// GetMessages 获取会话的消息历史
// GET /api/v1/sessions/:id/messages
func (h *SessionHandler) GetMessages(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_id", "message": "invalid session id"},
		})
		return
	}

	// 分页参数
	limit := 50
	offset := 0

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	messages, err := h.svc.GetMessages(uint(id), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "get_messages_failed", "message": err.Error()},
		})
		return
	}

	// 获取总数
	total, err := h.svc.CountMessages(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "count_messages_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"messages": messages,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}
