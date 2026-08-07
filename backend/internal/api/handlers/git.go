package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/service"
)

// GitHandler 暴露工作区 Git 只读状态。
type GitHandler struct {
	svc *service.GitService
}

// NewGitHandler 创建 Git 状态处理器。
func NewGitHandler(svc *service.GitService) *GitHandler {
	return &GitHandler{svc: svc}
}

// Status GET /api/v1/workspaces/:id/git/status
func (h *GitHandler) Status(c *gin.Context) {
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}
	result, err := h.svc.Status(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrGitStatusTimeout):
			writeError(c, http.StatusGatewayTimeout, "git_status_timeout", "读取 Git 状态超时")
		default:
			writeError(c, http.StatusInternalServerError, "git_status_failed", "读取 Git 状态失败")
		}
		return
	}
	c.JSON(http.StatusOK, result)
}
