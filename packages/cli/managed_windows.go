//go:build windows

package cli

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func ManageCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200}
	cmd.Cancel = func() error { return KillCommand(cmd) }
	cmd.WaitDelay = time.Second
}
func KillCommand(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctxCmd := exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	ctxCmd.WaitDelay = time.Second
	if err := ctxCmd.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
