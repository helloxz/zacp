package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/service"
)

// ToolHandler 处理本机 GUI 工具枚举与启动请求。
type ToolHandler struct {
	svc *service.ToolService
}

// NewToolHandler 创建本地工具处理器。
func NewToolHandler(svc *service.ToolService) *ToolHandler {
	return &ToolHandler{svc: svc}
}

// ListTools 返回当前平台已安装且在白名单中的工具。
// GET /api/v1/tools
func (h *ToolHandler) ListTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.svc.ListAvailable()})
}

// OpenSessionTool 在 Session 对应的 Workspace 中启动指定工具。
// POST /api/v1/sessions/:id/open-tool，请求体为 {"tool":"zed"}。
func (h *ToolHandler) OpenSessionTool(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_session_id", "会话 ID 必须是数字")
		return
	}

	var req struct {
		Tool string `json:"tool" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "工具参数无效")
		return
	}

	if err := h.svc.OpenSessionTool(uint(id), req.Tool); err != nil {
		writeToolError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeToolError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidTool):
		writeError(c, http.StatusBadRequest, "invalid_tool", "不支持该本地工具")
	case errors.Is(err, service.ErrToolUnavailable):
		writeError(c, http.StatusConflict, "tool_unavailable", "该工具未安装或当前不可用")
	case errors.Is(err, service.ErrSessionNotFound):
		writeError(c, http.StatusNotFound, "session_not_found", "会话不存在")
	case errors.Is(err, service.ErrToolWorkspace):
		writeError(c, http.StatusConflict, "workspace_unavailable", "当前会话工作目录不存在或不可用")
	case errors.Is(err, service.ErrToolLaunch):
		writeError(c, http.StatusInternalServerError, "tool_launch_failed", "启动本地工具失败")
	default:
		writeError(c, http.StatusInternalServerError, "tool_error", "本地工具操作失败")
	}
}
