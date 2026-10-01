//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// ManageCommand makes cancellation include SSH ProxyCommand descendants.
func ManageCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return KillCommand(cmd) }
	cmd.WaitDelay = time.Second
}
func KillCommand(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return os.ErrProcessDone
	}
	return err
}
