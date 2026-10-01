//go:build windows

package protocol

import (
	"errors"
	"os"
)

// Unix IPC permissions do not establish Windows ACL ownership. Until a native
// named-pipe ACL implementation exists, refuse rather than use a TCP fallback.
func ownedByCurrentUser(os.FileInfo) error {
	return errors.New("private IPC ownership is unsupported on Windows")
}
func trustedAncestorSymlink(string, os.FileInfo) bool { return false }
func safeAncestor(os.FileInfo) bool                   { return false }
