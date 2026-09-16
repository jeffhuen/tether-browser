//go:build !windows

package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
)

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func verifyPeerCredentials(conn net.Conn) error {
	uconn, ok := conn.(*net.UnixConn)
	if !ok {
		return nil
	}
	raw, err := uconn.SyscallConn()
	if err != nil {
		return err
	}
	var credErr error
	err = raw.Control(func(fd uintptr) {
		ucred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			// On platforms where SO_PEERCRED is not available, pass
			return
		}
		expectedUID := os.Getuid()
		if int(ucred.Uid) != expectedUID && expectedUID != 0 {
			credErr = fmt.Errorf("unauthorized caller UID %d (expected %d)", ucred.Uid, expectedUID)
		}
	})
	if err != nil {
		return err
	}
	return credErr
}
