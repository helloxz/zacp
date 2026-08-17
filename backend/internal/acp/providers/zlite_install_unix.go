//go:build !windows

package providers

import (
	"context"
	"os/exec"
	"syscall"
)

// runInstallCmd 在 Unix（macOS/Linux）上执行安装命令（bash -c "curl ... | bash"）。
//
// 关键点：管道是两个独立进程（bash 与 curl），exec.CommandContext 超时默认只杀
// 直接子进程 bash，curl 会残留为孤儿继续下载。这里用 Setpgid 让整个命令组独占
// 一个进程组，超时/取消时对进程组整体 SIGKILL，确保管道下游一并终止。
func runInstallCmd(ctx context.Context, argv []string) (string, error) {
	tail := newTailBuffer(0)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = tail
	cmd.Stderr = tail
	cmd.Stdin = nil // 关闭 stdin，避免脚本阻塞等待交互输入
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return tail.String(), err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return tail.String(), err
	case <-ctx.Done():
		// 超时/取消：杀整个进程组（bash + 它派生的 curl 等），再等收尾
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return tail.String(), ctx.Err()
	}
}
