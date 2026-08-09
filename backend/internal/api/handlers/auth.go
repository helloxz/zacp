package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/auth"
)

// AuthHandler 账号认证相关接口（登录 / 状态 / 改凭证）。
type AuthHandler struct {
	svc *auth.Service
}

// NewAuthHandler 创建认证处理器。
func NewAuthHandler(svc *auth.Service) *AuthHandler {
	return &AuthHandler{svc: svc}
}

// loginRequest 登录请求体。
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login POST /api/v1/auth/login
//
// 校验用户名密码；成功签发主 token（7 天，存内存，重启失效）。
// 认证未启用时恒返回 401（前端守卫只在 enabled 时展示登录页）。
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	token, ok := h.svc.Login(strings.TrimSpace(req.Username), req.Password)
	if !ok {
		// 统一提示，不区分「用户名不存在」与「密码错误」，避免枚举
		writeError(c, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":     token,
		"tokenType": "bearer",
		"expiresIn": int(auth.MainTokenTTL.Seconds()),
		"username":  h.svc.Username(),
	})
}

// Status GET /api/v1/auth/status
//
// 返回认证启用状态与用户名。免认证：前端路由守卫与登录页依赖它决定是否拦截，
// 且该接口不泄露任何敏感信息（username 为空说明未启用）。
func (h *AuthHandler) Status(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"enabled":  h.svc.Enabled(),
		"username": h.svc.Username(),
	})
}

// UpdateCredentialsRequest 修改凭证请求体。
type UpdateCredentialsRequest struct {
	Username string `json:"username"`
	// Password 为空 = 清除密码（关闭认证，恢复无需登录）；非空 = 启用认证。
	Password string `json:"password"`
}

// UpdateCredentials PUT /api/v1/auth/credentials
//
// 修改用户名/密码：热更新内存 + 原子写回 config.toml，无需重启。
// 成功后所有已签发 token 被吊销，前端应清理本地 token；
// 若新状态为启用，需用新凭证重新登录。
func (h *AuthHandler) UpdateCredentials(c *gin.Context) {
	var req UpdateCredentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.svc.UpdateCredentials(req.Username, req.Password); err != nil {
		writeError(c, http.StatusBadRequest, "update_credentials_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":  h.svc.Enabled(),
		"username": h.svc.Username(),
	})
}
