//go:build windows

package cli

import (
	"fmt"
	"net"
	"os/exec"
)

func setSysProcAttr(cmd *exec.Cmd) {
	// Windows does not support Setsid; process runs in its own process group by default.
}

func verifyPeerCredentials(conn net.Conn) error {
	return fmt.Errorf("peer credential verification unsupported on windows")
}

func setRestrictiveUmask() int { return 0 }
func restoreUmask(int)         {}
