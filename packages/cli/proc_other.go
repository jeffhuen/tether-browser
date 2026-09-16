//go:build !linux && !windows

package cli

import (
	"net"
)

func verifyPeerCredentials(conn net.Conn) error {
	// Socket permissions (0600) and directory permissions (0700) protect Unix domain sockets on macOS/BSD.
	return nil
}
