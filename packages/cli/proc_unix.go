//go:build !windows

package cli
import (
	"os/exec"
	"syscall"
)

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func setRestrictiveUmask() int {
	return syscall.Umask(0o177)
}

func restoreUmask(mask int) {
	syscall.Umask(mask)
}
