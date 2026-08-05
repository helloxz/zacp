// Package version 提供构建版本信息。
//
// Version / Commit / BuildTime 为编译期注入变量：
// 构建时通过 `-ldflags "-X github.com/zacp/zacp/internal/version.Version=..."` 覆盖，
// 未注入时保持默认值（如本地 `go run` 开发）。
// 版本号单一来源为 frontend/package.json 的 version 字段（见 scripts/build.sh），
// 后端 --version、GET /api/v1/version、前端设置页显示的版本均来自此包。
package version

import "fmt"

var (
	// Version 语义化版本号，如 "0.1.0"（不含 v 前缀）。
	Version = "dev"
	// Commit 构建时的 git 短提交号，未知为 "unknown"。
	Commit = "unknown"
	// BuildTime 构建时间（UTC RFC3339），未知为 "unknown"。
	BuildTime = "unknown"
)

// String 返回命令行可读的版本描述，用于 `--version` 输出。
func String() string {
	return fmt.Sprintf("zacp v%s (commit %s, built %s)", Version, Commit, BuildTime)
}
