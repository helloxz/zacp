package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/zacp/zacp/internal/acp/manager"
)

// ChatHandler exposes minimal chat HTTP endpoints.
type ChatHandler struct {
	Mgr *manager.Manager
}

type chatRequest struct {
	Message string `json:"message" binding:"required"`
}

// Health returns service status.
func (h *ChatHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"service":   "zacp",
		"sessionId": h.Mgr.SessionID(),
	})
}

// Chat handles POST /api/v1/chat — send one user message, wait for full agent turn.
func (h *ChatHandler) Chat(c *gin.Context) {
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "bad_request", "message": err.Error()},
		})
		return
	}

	result, err := h.Mgr.Chat(c.Request.Context(), req.Message)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{"code": "agent_error", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, result)
}

// Cancel handles POST /api/v1/cancel — cancel current prompt.
func (h *ChatHandler) Cancel(c *gin.Context) {
	if err := h.Mgr.Cancel(c.Request.Context()); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{"code": "cancel_error", "message": err.Error()},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
