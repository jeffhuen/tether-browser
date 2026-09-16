//go:build darwin

package cli

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"syscall"
	"unsafe"
)

// LOCAL_PEERCRED on macOS: getsockopt(SOL_LOCAL, LOCAL_PEERCRED, &xucred, &len).
// SOL_LOCAL = 0, LOCAL_PEERCRED = 1.
// struct xucred: offset 0 = cr_version (uint32), offset 4 = cr_uid (uint32).
const (
	solLocal      = 0
	localPeerCred = 1
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
		buf := make([]byte, 128)
		lenBuf := uint32(len(buf))
		_, _, errno := syscall.Syscall6(
			syscall.SYS_GETSOCKOPT,
			fd,
			uintptr(solLocal),
			uintptr(localPeerCred),
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&lenBuf)),
			0,
		)
		if errno != 0 {
			credErr = fmt.Errorf("getsockopt LOCAL_PEERCRED: %w", errno)
			return
		}
		if lenBuf < 8 {
			credErr = fmt.Errorf("LOCAL_PEERCRED returned short buffer (%d bytes)", lenBuf)
			return
		}
		// Read cr_uid as native endian uint32 at offset 4
		crUID := binary.NativeEndian.Uint32(buf[4:8])
		expectedUID := uint32(os.Getuid())
		if crUID != expectedUID {
			credErr = fmt.Errorf("unauthorized caller UID %d (expected %d)", crUID, expectedUID)
		}
	})
	if err != nil {
		return err
	}
	return credErr
}
