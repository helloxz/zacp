package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/zacp/zacp/internal/acp/manager"
)

// ChatHandler 暴露最小化的 chat HTTP 端点（兼容旧 demo）。
type ChatHandler struct {
	Mgr *manager.Manager
}

type chatRequest struct {
	Message string `json:"message" binding:"required"`
}

// Health 返回服务状态。
func (h *ChatHandler) Health(c *gin.Context) {
	agents := h.Mgr.ListAgents()
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "zacp",
		"agents":  agents,
	})
}

// ListAgents 返回所有 agent 状态。
func (h *ChatHandler) ListAgents(c *gin.Context) {
	agents := h.Mgr.ListAgents()
	c.JSON(http.StatusOK, gin.H{"agents": agents})
}

// GetAgentStatus 返回指定 agent 状态。
func (h *ChatHandler) GetAgentStatus(c *gin.Context) {
	agentID := c.Param("agentId")
	status, err := h.Mgr.GetAgentStatus(agentID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "agent_not_found", "message": err.Error()},
		})
		return
	}
	c.JSON(http.StatusOK, status)
}

// Chat 处理 POST /api/v1/chat — 发送消息并等待完整响应（兼容旧 demo）。
// 使用第一个可用的 agent 和 session。
func (h *ChatHandler) Chat(c *gin.Context) {
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "bad_request", "message": err.Error()},
		})
		return
	}

	// 获取第一个可用 agent
	agents := h.Mgr.ListAgents()
	if len(agents) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "no_agents", "message": "no agents configured"},
		})
		return
	}

	agentID := agents[0].AgentID
	sessionID := agents[0].SessionID

	if sessionID == "" {
		// 尝试创建 session
		newSessionID, err := h.Mgr.CreateSession(c.Request.Context(), agentID, "")
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{
				"error": gin.H{"code": "session_error", "message": err.Error()},
			})
			return
		}
		sessionID = newSessionID
	}

	result, err := h.Mgr.Prompt(c.Request.Context(), agentID, sessionID, req.Message)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{"code": "agent_error", "message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, result)
}

// Cancel 处理 POST /api/v1/cancel — 取消当前 prompt（兼容旧 demo）。
func (h *ChatHandler) Cancel(c *gin.Context) {
	// 获取第一个可用 agent
	agents := h.Mgr.ListAgents()
	if len(agents) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "no_agents", "message": "no agents configured"},
		})
		return
	}

	agentID := agents[0].AgentID
	sessionID := agents[0].SessionID

	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "no_session", "message": "no active session"},
		})
		return
	}

	if err := h.Mgr.Cancel(c.Request.Context(), agentID, sessionID); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{"code": "cancel_error", "message": err.Error()},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
