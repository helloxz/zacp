// Package web 将「构建产物 + 配置示例」打包进单一二进制。
//
// 目录结构（本包随源码入库，内容由 scripts/build.sh 在构建时刷新）：
//
//	web/
//	├── dist/               # 前端构建产物（frontend/dist 的拷贝，仅保留 .gitkeep 入库）
//	└── config.example.toml # 配置示例（backend/configs/config.example.toml 的拷贝）
//
// 为什么需要拷贝而不是直接 embed 原目录：
// go:embed 只能引用源码包所在目录内的文件，而 frontend/dist 在 backend 之外，
// 因此由 build.sh 把产物拷进本包再编译。
//
// dist 目录以 .gitkeep 占位入库，保证未执行 build.sh（纯 go build / 开发模式）时
// 也能正常编译；此时 StaticEnabled() 返回 false，后端不注册静态路由，
// 前端开发由 vite dev server（:8681）独立承担。
package web

import (
	"embed"
	"io/fs"
)

// 嵌入前端产物（all: 前缀用于包含以 . 开头的 .gitkeep）与配置示例。
// 注意：编译时若 dist/ 目录缺失会导致构建失败，因此该目录必须始终存在（.gitkeep）。
//
//go:embed all:dist
//go:embed config.example.toml
var FS embed.FS

// StaticEnabled 报告是否嵌入了真实的前端产物（以 index.html 是否存在为准）。
func StaticEnabled() bool {
	_, err := FS.Open("dist/index.html")
	return err == nil
}

// StaticFS 返回 dist 子文件系统；未嵌入前端产物时返回 nil。
func StaticFS() fs.FS {
	sub, err := fs.Sub(FS, "dist")
	if err != nil {
		return nil
	}
	return sub
}

// IndexHTML 返回内嵌首页内容，供 SPA fallback（history 路由）使用。
func IndexHTML() ([]byte, error) {
	return FS.ReadFile("dist/index.html")
}

// ExampleConfig 返回内嵌的配置文件示例内容（首次启动生成 ~/.zacp/config.toml 用）。
func ExampleConfig() ([]byte, error) {
	return FS.ReadFile("config.example.toml")
}
