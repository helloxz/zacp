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
	Username    string `json:"username"`
	Password    string `json:"password"`
	CaptchaID   string `json:"captchaId"`
	CaptchaCode string `json:"captcha"`
}

// Captcha GET /api/v1/auth/captcha
//
// 生成图形验证码（免认证，5 分钟过期，单次有效）。
// 认证未启用时也允许调用，前端可按需决定是否展示。
func (h *AuthHandler) Captcha(c *gin.Context) {
	id, b64 := h.svc.GenerateCaptcha()
	c.JSON(http.StatusOK, gin.H{
		"id":    id,
		"image": "data:image/png;base64," + b64,
	})
}

// Login POST /api/v1/auth/login
//
// 校验：IP 黑名单 → 验证码 → 用户名密码；成功签发主 token（7 天，存内存，重启失效）。
// 认证未启用时恒返回 401（前端守卫只在 enabled 时展示登录页）。
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	clientIP := auth.ClientIP(c)
	// 1. IP 黑名单检查（5 次失败后 24 小时，重启即清）
	if h.svc.IsBlocked(clientIP) {
		writeError(c, http.StatusTooManyRequests, "ip_blocked", "IP已被拉黑，请重启服务解除限制")
		return
	}
	// 2. 验证码校验（仅认证启用时强制）
	if h.svc.Enabled() {
		if strings.TrimSpace(req.CaptchaID) == "" || strings.TrimSpace(req.CaptchaCode) == "" {
			writeError(c, http.StatusBadRequest, "captcha_required", "请输入图形验证码")
			return
		}
		if !h.svc.VerifyCaptcha(req.CaptchaID, req.CaptchaCode) {
			writeError(c, http.StatusBadRequest, "captcha_invalid", "图形验证码错误或已过期")
			return
		}
	}
	token, ok := h.svc.Login(strings.TrimSpace(req.Username), req.Password)
	if !ok {
		// 密码错误计入 IP 维度（验证码错误不计入）
		h.svc.RecordFailure(clientIP)
		// 若本次失败后刚好被拉黑，下次请求会 429；本次仍按 401 提示
		writeError(c, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}
	h.svc.RecordSuccess(clientIP)
	c.JSON(http.StatusOK, gin.H{
		"token":     token,
		"tokenType": "bearer",
		"expiresIn": int(auth.MainTokenTTL.Seconds()),
		"username":  h.svc.Username(),
	})
}


// Status GET /api/v1/auth/status
//
// 返回认证启用状态。免认证：前端路由守卫依赖它决定是否拦截。
// 刻意不回传 username：该接口无需任何凭证即可访问，回传用户名会
// 与 Login 的防枚举提示（不区分「用户名不存在/密码错误」）相矛盾，
// 相当于对外公开了正确用户名，此处只暴露 enabled 布尔值。
func (h *AuthHandler) Status(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"enabled": h.svc.Enabled(),
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
