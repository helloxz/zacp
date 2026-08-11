//go:build !windows

package tty

import (
	"errors"
	"syscall"

	pty "github.com/aymanbagabas/go-pty"
)

func terminateProcess(cmd *pty.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// go-pty 在 Unix 启动时为 shell 建立独立 session；优先向进程组发送
	// SIGTERM，避免关闭浏览器 Tab 后只留下编译器/子 shell 等孤儿进程。
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
