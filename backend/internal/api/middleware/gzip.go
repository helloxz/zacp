package middleware

import (
	"compress/gzip"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Gzip 返回按请求协商 gzip 的响应中间件。
//
// 中间件只负责响应压缩，不改变业务响应体。调用方可通过 GzipIf 将压缩
// 限定在特定路由；这样消息历史和生产静态资源可以压缩，其它 API 与 WebSocket
// 保持原有行为。浏览器会根据 Accept-Encoding 自动解压，前端无需感知。
func Gzip() gin.HandlerFunc {
	return GzipIf(func(*gin.Context) bool { return true })
}

// GzipIf 返回按条件压缩响应的 gzip 中间件。
//
// 条件在业务 handler 执行前判断，因此静态资源（由 NoRoute 提供）也可以复用
// 同一中间件。只有客户端明确接受 gzip、请求不是 HEAD/Range/Upgrade，且响应
// 尚未声明其它 Content-Encoding 时才会启用压缩。
func GzipIf(shouldCompress func(*gin.Context) bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if shouldCompress == nil || !shouldCompress(c) || !acceptsGzip(c.GetHeader("Accept-Encoding")) || !gzipSafeRequest(c) {
			c.Next()
			return
		}

		writer := &gzipResponseWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()
		_ = writer.close()
	}
}

// StaticAssetPath 判断生产静态资源是否属于本阶段压缩范围。
// 仅压缩 JS/CSS，避免误处理下载、图片、字体和其它二进制资源。
func StaticAssetPath(requestPath string) bool {
	ext := strings.ToLower(path.Ext(requestPath))
	return ext == ".js" || ext == ".mjs" || ext == ".css"
}

// gzipResponseWriter 延迟发送响应头，确保 Content-Encoding 在真正写入正文前设置。
// gzip.Writer 写入的是底层 Gin ResponseWriter，因此 Gin 仍负责状态码和连接管理。
type gzipResponseWriter struct {
	gin.ResponseWriter
	gzipWriter *gzip.Writer
	started    bool
}

func (w *gzipResponseWriter) WriteHeaderNow() {
	if w.ResponseWriter.Written() {
		return
	}
	if bodyAllowed(w.ResponseWriter.Status()) {
		w.start()
		return
	}
	w.ResponseWriter.WriteHeaderNow()
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	w.start()
	return w.gzipWriter.Write(data)
}

func (w *gzipResponseWriter) WriteString(value string) (int, error) {
	w.start()
	return w.gzipWriter.Write([]byte(value))
}

func (w *gzipResponseWriter) Flush() {
	w.start()
	_ = w.gzipWriter.Flush()
	w.ResponseWriter.Flush()
}

func (w *gzipResponseWriter) start() {
	if w.started {
		return
	}
	w.started = true

	header := w.ResponseWriter.Header()
	header.Set("Content-Encoding", "gzip")
	appendVary(header, "Accept-Encoding")
	// 压缩后长度与原始响应长度不同，不能沿用 handler/ServeFile 写入的值。
	header.Del("Content-Length")

	w.ResponseWriter.WriteHeaderNow()
	w.gzipWriter = gzip.NewWriter(w.ResponseWriter)
}

func (w *gzipResponseWriter) close() error {
	if !w.started || w.gzipWriter == nil {
		return nil
	}
	return w.gzipWriter.Close()
}

func gzipSafeRequest(c *gin.Context) bool {
	request := c.Request
	if request.Method == http.MethodHead || request.Header.Get("Range") != "" {
		return false
	}
	if strings.TrimSpace(request.Header.Get("Upgrade")) != "" {
		return false
	}
	return c.Writer.Header().Get("Content-Encoding") == ""
}

func acceptsGzip(value string) bool {
	wildcardQuality := -1.0
	for _, item := range strings.Split(value, ",") {
		parts := strings.Split(item, ";")
		encoding := strings.TrimSpace(strings.ToLower(parts[0]))
		q := quality(parts[1:])
		switch encoding {
		case "*":
			wildcardQuality = q
		case "gzip":
			// 显式 gzip 优先于通配符，避免 gzip;q=0 被 * 重新放开。
			return q > 0
		}
	}
	return wildcardQuality > 0
}

func quality(parameters []string) float64 {
	for _, parameter := range parameters {
		keyValue := strings.SplitN(strings.TrimSpace(parameter), "=", 2)
		if len(keyValue) != 2 || !strings.EqualFold(keyValue[0], "q") {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(keyValue[1]), 64)
		if err != nil {
			return 0
		}
		return value
	}
	return 1
}

func appendVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

func bodyAllowed(status int) bool {
	return status >= 200 && status != http.StatusNoContent && status != http.StatusNotModified
}
