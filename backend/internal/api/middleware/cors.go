// Package middleware 提供 HTTP 中间件。
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS 返回跨域访问中间件。
//
// 背景：前端开发环境直连后端（VITE_API_BASE_URL=http://192.168.50.20:8680，
// 页面在 :8681），跨端口即跨域，浏览器会先发 OPTIONS 预检并检查 CORS 响应头。
//
// allowedOrigins 为空或含 "*" 时允许所有来源（开发默认）。
// 注意：Access-Control-Allow-Origin 与 credentials 同时使用时不能为 "*"；
// 当前无鉴权 Cookie 场景用 "*" 合法。生产接入鉴权/账号体系后，
// 应改为白名单回显（把允许的来源传入本中间件），并配合 WS Origin 校验收紧。
func CORS(allowedOrigins ...string) gin.HandlerFunc {
	allowAll := len(allowedOrigins) == 0
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAll = true
		}
	}

	// 计算允许回显的 Origin：全部允许时返回 "*"，否则白名单内回显原值
	originAllowed := func(origin string) string {
		if allowAll {
			return "*"
		}
		for _, o := range allowedOrigins {
			if strings.EqualFold(o, origin) {
				return origin
			}
		}
		return ""
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allow := originAllowed(origin); allow != "" {
			c.Header("Access-Control-Allow-Origin", allow)
			c.Header("Vary", "Origin")
		}

		// 预检请求（OPTIONS）：补齐允许的方法/头后直接短路，不进业务路由
		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization")
			c.Header("Access-Control-Max-Age", "86400")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
