//go:build windows

package tty

import pty "github.com/aymanbagabas/go-pty"

func terminateProcess(cmd *pty.Cmd) error {
	if cmd == nil {
		return nil
	}
	if cmd.Cancel != nil {
		return cmd.Cancel()
	}
	if cmd.Process != nil {
		return cmd.Process.Kill()
	}
	return nil
}
