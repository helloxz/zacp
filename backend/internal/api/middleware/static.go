package middleware

import (
	"bytes"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/web"
)

// 静态资源缓存策略（均配合 ETag / 内容哈希使用）。
const (
	// cacheImmutable 用于文件名带内容哈希的产物：内容变则文件名变，
	// 30 天（2592000s）内浏览器连刷新都不回源。
	// 到期后浏览器仍会带 ETag 校验一次，命中即 304，不会整包重下。
	cacheImmutable = "public, max-age=2592000, immutable"
	// cacheRevalidate 用于入口类文件（index.html、sw.js、manifest）：
	// 允许本地存储，但每次使用前必须用 ETag 校验，命中即 304。
	// 入口 HTML 负责指向新的哈希文件名，若被强缓存用户会一直卡在旧版本。
	cacheRevalidate = "no-cache"
	// cacheShort 用于不带内容哈希的公共资源（favicon、PWA 图标、音效等）：
	// 短缓存 + ETag，兼顾首屏速度与更新可见性。
	cacheShort = "public, max-age=86400"
)

// hashedAssetName 匹配 Vite 产物文件名末尾的内容哈希，例如 index-sVYrO560.js。
// 只有确认带哈希才给 immutable，避免把用户放在 assets/ 下的同名固定文件长期缓存。
var hashedAssetName = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.[a-z0-9]+$`)

// StaticCacheControl 返回静态资源应使用的 Cache-Control 值。
//
// existed 表示该文件在内嵌产物中真实存在，必须由调用方在确认之后再传 true：
// 未命中的路径会被 SPA fallback 替换成入口 HTML，若按 URL 前缀无条件给
// immutable，浏览器会把这段 HTML 当成 JS 长期缓存（30 天）。
func StaticCacheControl(requestPath string, existed bool) string {
	if !existed {
		return cacheRevalidate
	}
	p := strings.ToLower(requestPath)
	// 入口与 Service Worker 必须回源校验
	if p == "/" || strings.HasSuffix(p, "/index.html") || p == "/sw.js" || p == "/manifest.webmanifest" {
		return cacheRevalidate
	}
	if strings.HasPrefix(p, "/assets/") && hashedAssetName.MatchString(p) {
		return cacheImmutable
	}
	// vite-plugin-pwa 生成的 workbox 运行时同样带内容哈希
	if strings.HasPrefix(p, "/workbox-") && strings.HasSuffix(p, ".js") {
		return cacheImmutable
	}
	return cacheShort
}

// ServeStaticFile 为内嵌产物设置缓存与校验头后写出。
//
// 必须在确认 name 存在后再调用（见 StaticCacheControl 的 existed 说明）。
// etag 用于生成弱 ETag；为 nil 时退化为不设置校验头。
// 哈希产物虽然 30 天内带 immutable（刷新也不回源），但 fresh 期结束后浏览器
// 必然重新校验一次，有 ETag 才能以 304 结束而不是整包重下。
// 实际响应体交给 http.ServeFileFS：它会补齐 Content-Type / Content-Length、
// 支持 Range，并在 If-None-Match 命中时返回 304。
func ServeStaticFile(c *gin.Context, fsys fs.FS, name string, etag func(string) string) {
	header := c.Writer.Header()
	header.Set("Cache-Control", StaticCacheControl("/"+name, true))
	if etag != nil {
		if tag := etag(name); tag != "" {
			header.Set("ETag", tag)
		}
	}
	http.ServeFileFS(c.Writer, c.Request, fsys, name)
}

// ServeSPAHTML 输出入口 HTML（含前端 history 路由的 SPA fallback）。
//
// 用 http.ServeContent 而不是 c.Data，才能利用 ETag 做条件请求：
// 命中 If-None-Match 时返回 304 且不带正文，静态内容几乎零成本。
func ServeSPAHTML(c *gin.Context, html []byte) {
	header := c.Writer.Header()
	header.Set("Cache-Control", cacheRevalidate)
	header.Set("ETag", web.WeakETag(html))
	// modtime 传零值：内嵌产物没有可靠的时间戳，交给 ETag 校验。
	http.ServeContent(c.Writer, c.Request, "index.html", time.Time{}, bytes.NewReader(html))
}
