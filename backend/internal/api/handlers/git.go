package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/model"
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

// gitWriteError 把写操作错误映射为 HTTP 错误响应（Commit/Push 共用）。
func gitWriteError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrGitInvalidRequest):
		writeError(c, http.StatusBadRequest, "git_invalid_request", err.Error())
	case errors.Is(err, service.ErrGitNotInstalled):
		writeError(c, http.StatusBadRequest, "git_not_installed", err.Error())
	case errors.Is(err, service.ErrGitNotRepository):
		writeError(c, http.StatusBadRequest, "git_not_repository", err.Error())
	case errors.Is(err, service.ErrGitConflictedFiles):
		writeError(c, http.StatusConflict, "git_conflicted_files", err.Error())
	case errors.Is(err, service.ErrGitStagedFilesNotSelected):
		writeError(c, http.StatusConflict, "git_staged_files_not_selected", err.Error())
	case errors.Is(err, service.ErrGitStatusTimeout), errors.Is(err, service.ErrGitWriteTimeout):
		writeError(c, http.StatusGatewayTimeout, "git_timeout", "Git 操作超时")
	case errors.Is(err, service.ErrGitPushFailed):
		writeError(c, http.StatusBadGateway, "git_push_failed", err.Error())
	default:
		writeError(c, http.StatusInternalServerError, "git_write_failed", err.Error())
	}
}

// Commit POST /api/v1/workspaces/:id/git/commit
// 请求体 { message, files, push }；仅提交选中文件（含暂存区一致性校验）。
// push=true 时 commit 成功后立即推送；推送失败返回 200 + { committed, pushError }，
// 前端据此展示「已提交但推送失败」并可调用 /git/push 重试。
func (h *GitHandler) Commit(c *gin.Context) {
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}
	var req model.GitCommitRequestDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "请求体格式错误")
		return
	}
	result, err := h.svc.Commit(c.Request.Context(), id, req.Message, req.Files, req.Push)
	if err != nil {
		gitWriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Push POST /api/v1/workspaces/:id/git/push
// 重试推送当前分支全部已提交内容（commit+push 失败后的独立重试入口）。
func (h *GitHandler) Push(c *gin.Context) {
	id, err := parseWorkspaceID(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_workspace_id", err.Error())
		return
	}
	result, err := h.svc.Push(c.Request.Context(), id)
	if err != nil {
		gitWriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
