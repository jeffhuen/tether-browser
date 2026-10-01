//go:build !linux && !darwin

package protocol

import (
	"errors"
	"net"
)

func VerifyPeerCredentials(net.Conn) error {
	return errors.New("private IPC peer verification is unsupported on this platform")
}
