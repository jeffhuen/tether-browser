//go:build !linux && !darwin && !windows

package cli

import (
	"fmt"
	"net"
)

func verifyPeerCredentials(conn net.Conn) error {
	return fmt.Errorf("peer credential verification unsupported on this platform")
}
