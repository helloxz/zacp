package auth

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ClientIP 尝试从多级反代/CDN 头中提取真实客户端 IP。
// 优先级（按用户要求不过度防伪造，直接取第一个非空合法 IP）：
// CF-Connecting-IP > True-Client-IP > X-Real-IP > X-Forwarded-For(首个) > RemoteAddr
// 每步均做 net.ParseIP 校验，剥离端口；非法则回退下一级。
func ClientIP(c *gin.Context) string {
	return ClientIPFromRequest(c.Request)
}

// ClientIPFromRequest 供非 Gin 场景复用。
func ClientIPFromRequest(r *http.Request) string {
	// 1. Cloudflare
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		if parsed := parseIP(ip); parsed != "" {
			return parsed
		}
	}
	// 2. Akamai / Cloudflare Enterprise
	if ip := strings.TrimSpace(r.Header.Get("True-Client-IP")); ip != "" {
		if parsed := parseIP(ip); parsed != "" {
			return parsed
		}
	}
	// 3. Nginx X-Real-IP
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
		if parsed := parseIP(ip); parsed != "" {
			return parsed
		}
	}
	// 4. X-Forwarded-For：可能为 "client, proxy1, proxy2"
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// 按用户要求不做可信代理校验，直接取第一个
		parts := strings.Split(xff, ",")
		for _, p := range parts {
			if ip := strings.TrimSpace(p); ip != "" {
				if parsed := parseIP(ip); parsed != "" {
					return parsed
				}
			}
		}
	}
	// 5. RFC 标准 Forwarded: for=1.1.1.1;proto=https
	if fwd := r.Header.Get("Forwarded"); fwd != "" {
		// 简单解析 for= 段
		for _, part := range strings.Split(fwd, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(strings.ToLower(part), "for=") {
				ip := strings.Trim(strings.TrimPrefix(part, "for="), "\"[]")
				// for 可能带端口 for="_proto://[ip]:port" 极少见，尽力剥离
				if parsed := parseIP(ip); parsed != "" {
					return parsed
				}
			}
		}
		// 备选：逗号分隔多值 Forwarded 头
		for _, seg := range strings.Split(fwd, ",") {
			seg = strings.TrimSpace(seg)
			if strings.Contains(seg, "for=") {
				// 已在上面处理，跳过
				continue
			}
		}
	}
	// 6. 回退 RemoteAddr
	if ip := parseIP(r.RemoteAddr); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

// parseIP 剥离端口后校验 IP 合法性，非法返回 ""。
func parseIP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// 带端口的 RemoteAddr 如 "1.1.1.1:1234" 或 "[::1]:1234"
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	// 去除可能的引号/括号
	s = strings.Trim(s, "\"'[]")
	if net.ParseIP(s) == nil {
		return ""
	}
	return s
}
