package providers

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"unicode/utf8"
)

// Zlite 安装：通过官方安装脚本在用户本机安装 zlite 二进制。
//
// 命令按后端运行平台选择（后端与浏览器同机部署，本地 stdio agent 假设成立）：
//   - macOS / Linux：curl -fsSL .../install.sh | bash
//   - Windows：irm .../install.ps1 | iex
//
// 这是**远程代码执行类操作**：脚本内容取决于 helloxz/zlite 仓库，调用方
// （设置页确认弹窗 + 已认证 API）必须确保用户知情同意。脚本安装到 ~/.zlite
// 目录（由脚本自身决定，后端不限制）。
//
// 超时/取消语义：由调用方通过 ctx 控制（设置页约定 5 分钟）；平台实现负责
// 在超时时按进程组杀干净管道下游进程（bash 派生出的 curl），避免残留孤儿进程。

// ErrZliteInstalling 表示已有安装任务进行中（并发幂等：同一时刻只允许一个安装）。
var ErrZliteInstalling = errors.New("zlite install already in progress")

// zliteInstalling 并发标志：防止多个请求同时执行安装脚本写坏 ~/.zlite。
var zliteInstalling atomic.Bool

// zliteInstallShellScript 官方安装脚本 URL (GitHub raw；用户需能访问 GitHub)。
const zliteInstallShellScript = "https://raw.githubusercontent.com/helloxz/zlite/main/install.sh"

// zliteInstallPS1Script 官方安装脚本 URL（Windows PowerShell）。
const zliteInstallPS1Script = "https://raw.githubusercontent.com/helloxz/zlite/main/install.ps1"

// zliteInstallTimeout 设置页约定安装超时（handler 层使用）。
const zliteInstallTimeout = 5 * 60 // 秒（仅文档用途；实际超时在 handler 的 context 上）

// zliteInstallCommand 返回按平台执行安装命令的 argv；未知平台返回 nil。
// 抽成纯函数便于单测（goos 可注入，无需真实切换系统）。
func zliteInstallCommand(goos string) []string {
	switch goos {
	case "darwin", "linux":
		return []string{"bash", "-c", "curl -fsSL " + zliteInstallShellScript + " | bash"}
	case "windows":
		return []string{
			"powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass",
			"-Command", "irm " + zliteInstallPS1Script + " | iex",
		}
	}
	return nil
}

// InstallZlite 执行 zlite 安装：
//  1. 并发幂等：已有安装任务时立即返回 ErrZliteInstalling
//  2. 按 GOOS 分支选取安装命令；不支持的平台返回错误
//  3. 以脚本退出码为准；脚本成功后再复核 IsInstalled，双保险
//
// 返回值 tail 为脚本 stdout+stderr 的尾部内容（供失败排查；成功时通常为空）。
// 注意：调用方应事先用 IsInstalled 做幂等（已安装直接拒绝），本函数只负责执行。
func InstallZlite(ctx context.Context) (tail string, err error) {
	if !zliteInstalling.CompareAndSwap(false, true) {
		return "", ErrZliteInstalling
	}
	defer zliteInstalling.Store(false)

	argv := zliteInstallCommand(runtime.GOOS)
	if argv == nil {
		return "", fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	tail, err = runInstallCmd(ctx, argv)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// 统一为 ctx 错误（超时/取消），便于 handler 区分错误类型
			return tail, ctxErr
		}
		return tail, err
	}
	if !IsInstalled("zlite") {
		return tail, fmt.Errorf(
			"install script exited 0 but 'zlite' not found in PATH, script output tail: %s", tail)
	}
	return tail, nil
}

// tailBuffer 只保留最近 max 字节的环形缓冲写入器：安装脚本输出可能很长，
// 只保留尾部即可满足失败排查，同时避免前端错误信息/内存被刷爆。
type tailBuffer struct {
	buf []byte
	max int
}

func newTailBuffer(max int) *tailBuffer {
	if max <= 0 {
		max = 1 << 12 // 默认 4KiB
	}
	return &tailBuffer{max: max}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

// String 返回尾部内容（保证 UTF-8 合法：截断点可能落在多字节字符中间，
// 从头部剥离不完整字节直到落在有效字符边界，避免显示乱码）。
func (t *tailBuffer) String() string {
	b := t.buf
	if len(b) > t.max {
		b = b[len(b)-t.max:]
	}
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[1:] // 去掉头部不完整字节（截断点之前的半个字符）
	}
	return string(b)
}
