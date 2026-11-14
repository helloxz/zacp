package web

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// etagCache 缓存文件内容 ETag。内嵌产物在进程生命周期内不可变，
// 每个文件的内容哈希只需计算一次（避免每次请求重复读文件 + 哈希）。
var etagCache sync.Map // map[string]string

// WeakETag 生成弱 ETag：W/"<内容 SHA-256 前 8 字节 hex>"。
//
// 用弱校验而不是强校验：同一份内容经 gzip 压缩后字节不同、语义相同，
// 而浏览器的 If-None-Match 走弱比较，可正确复用缓存，也不会因
// Content-Encoding 差异误判为「内容已变」。
func WeakETag(data []byte) string {
	sum := sha256.Sum256(data)
	return `W/"` + hex.EncodeToString(sum[:8]) + `"`
}

// FileETag 返回内嵌 dist 文件的弱 ETag；文件不存在时返回空串。
//
// 为什么需要它：embed.FS 里的文件没有修改时间，http.ServeContent 因此
// 不会输出 Last-Modified，浏览器只能无条件全量下载。对 index.html、sw.js
// 这类必须回源的资源来说，没有校验头就等于每次刷新都重传正文；补上 ETag
// 后第二次起以 304 结束。
func FileETag(name string) string {
	if cached, ok := etagCache.Load(name); ok {
		return cached.(string)
	}
	data, err := FS.ReadFile("dist/" + name)
	if err != nil {
		return ""
	}
	tag := WeakETag(data)
	etagCache.Store(name, tag)
	return tag
}
