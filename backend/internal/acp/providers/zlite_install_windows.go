//go:build windows

package providers

import (
	"context"
	"os/exec"
)

// runInstallCmd 在 Windows 上执行安装命令（powershell -Command "irm ... | iex"）。
//
// 与 Unix 的 curl | bash 不同：irm/iex 均是 PowerShell cmdlet，在同一个
// powershell 进程内解释执行，exec.CommandContext 超时杀掉 powershell 即整体终止，
// 无残留子进程问题，无需 Setpgid（Windows 也没有进程组概念）。
func runInstallCmd(ctx context.Context, argv []string) (string, error) {
	tail := newTailBuffer(0)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = tail
	cmd.Stderr = tail
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return tail.String(), err
	}
	return tail.String(), nil
}
