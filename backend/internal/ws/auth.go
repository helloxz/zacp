package ws

import (
	"net/http"
	"strings"

	"github.com/helloxz/zacp/internal/auth"
)

// wsAuthProtocolPrefix WebSocket 子协议前缀，登录 token 经它携带：
// 浏览器 WebSocket 无法设置自定义 header（Authorization 带不过去），
// 而把 token 放进 ?token= 会进访问日志——子协议由浏览器只在握手时发送、
// 不回显到 URL，是折中方案。格式："zacp-auth.<token>"（token 为 hex，
// 满足 RFC 7230 tchar 子协议字符集）。
const wsAuthProtocolPrefix = "zacp-auth."

// firstSubprotocol 从握手请求取首个客户端子协议（RFC 6455 只要求服务端回显一个；
// 客户端可能一次请求多个，浏览器端固定只发一个，此函数兜底第三方/手动客户端）。
func firstSubprotocol(r *http.Request) string {
	proto := r.Header.Get("Sec-WebSocket-Protocol")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	return strings.TrimSpace(proto)
}

// AuthSubprotocol 校验 WebSocket 握手携带的主认证 token，并返回需要回显的子协议。
// 认证未启用时放行；若客户端携带子协议，仍返回它以满足 RFC 6455 的握手要求。
func AuthSubprotocol(r *http.Request, authSvc *auth.Service) (string, bool) {
	if authSvc == nil || !authSvc.Enabled() {
		return firstSubprotocol(r), true
	}

	proto := firstSubprotocol(r)
	if !strings.HasPrefix(proto, wsAuthProtocolPrefix) {
		return "", false
	}
	if !authSvc.ValidateMain(strings.TrimPrefix(proto, wsAuthProtocolPrefix)) {
		return "", false
	}
	return proto, true
}
