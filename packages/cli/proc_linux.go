//go:build linux

package cli

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

func verifyPeerCredentials(conn net.Conn) error {
	uconn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("expected unix connection, got %T", conn)
	}
	raw, err := uconn.SyscallConn()
	if err != nil {
		return err
	}
	var credErr error
	err = raw.Control(func(fd uintptr) {
		ucred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			credErr = fmt.Errorf("getsockopt SO_PEERCRED: %w", err)
			return
		}
		expectedUID := os.Getuid()
		if int(ucred.Uid) != expectedUID {
			credErr = fmt.Errorf("unauthorized caller UID %d (expected %d)", ucred.Uid, expectedUID)
		}
	})
	if err != nil {
		return err
	}
	return credErr
}
