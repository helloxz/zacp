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
func (h *SessionHandler) CreateSession(c *gin.Context) {
	var req struct {
		WorkspaceID uint   `json:"workspaceId" binding:"required"`
		AgentID     string `json:"agentId" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "invalid_request", "message": err.Error()},
		})
		return
	}

	session, err := h.svc.CreateSession(c.Request.Context(), req.WorkspaceID, req.AgentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "create_session_failed", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"session": session})
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
