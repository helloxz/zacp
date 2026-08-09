package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/auth"
)

// 认证中间件：把「token 校验」与 Gin 请求上下文粘合。
//
// 两种 token 的携带方式（按泄露面区分）：
//   - 主 token：仅 Authorization: Bearer <token>（绝不进 URL，避免出现在访问日志）；
//   - 资源 token：仅直链 URL 的 ?token=（文件 raw 预览专用，12 小时有效，
//     与主 token 分离，即使被日志记录也无法冒充登录态）。
const (
	headerAuthorization = "Authorization"
	headerBearerPrefix  = "Bearer "
	queryResourceToken  = "token"
)

// bearerToken 从 Authorization header 提取 Bearer token；缺失/格式不符返回空。
func bearerToken(c *gin.Context) string {
	h := c.GetHeader(headerAuthorization)
	if len(h) > len(headerBearerPrefix) && strings.EqualFold(h[:len(headerBearerPrefix)], headerBearerPrefix) {
		return strings.TrimSpace(h[len(headerBearerPrefix):])
	}
	return ""
}

// RequireMain 要求有效主 token；认证未启用时直接放行（保持默认无需登录的现状）。
// 挂到除 auth/login、auth/status、files/raw 外的所有 /api/v1 业务端点。
func RequireMain(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !svc.Enabled() {
			c.Next()
			return
		}
		if !svc.ValidateMain(bearerToken(c)) {
			abortUnauthorized(c)
			return
		}
		c.Next()
	}
}

// FileRaw 用于 /workspaces/:id/files/raw 的双模式校验：
//   - Authorization 主 token：下载/调试等常规场景；
//   - ?token= 资源 token：图片预览直链（<img src> 无法带自定义 header，
//     只能走 query），校验时比对绑定的 workspace+path，防止越权访问其它文件。
//
// 任一模通过即放行，否则 401。
func FileRaw(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !svc.Enabled() {
			c.Next()
			return
		}
		// 优先主 token（Authorization）
		if token := bearerToken(c); token != "" && svc.ValidateMain(token) {
			c.Next()
			return
		}
		// 其次资源 token（?token=），绑定校验用路由参数与 query 原始值
		workspace := c.Param("id")
		path := c.Query("path")
		if token := c.Query(queryResourceToken); token != "" &&
			svc.ValidateResourceToken(token, workspace, path) {
			c.Next()
			return
		}
		abortUnauthorized(c)
	}
}

// abortUnauthorized 统一的 401 响应（与其它端点错误结构一致）。
func abortUnauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{"code": "unauthorized", "message": "未登录或登录已过期"},
	})
}
