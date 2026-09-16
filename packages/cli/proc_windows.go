//go:build windows

package cli

import (
	"net"
	"os/exec"
)

func setSysProcAttr(cmd *exec.Cmd) {
	// Windows does not support Setsid; process runs in its own process group by default.
}

func verifyPeerCredentials(conn net.Conn) error {
	return nil
}
