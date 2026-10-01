//go:build !windows

package protocol

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func ownedByCurrentUser(fi os.FileInfo) error {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("path is not owned by current user: %s", fi.Name())
	}
	return nil
}

func safeAncestor(fi os.FileInfo) bool {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != 0 && int(stat.Uid) != os.Getuid()) || !fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0022 == 0 || fi.Mode()&os.ModeSticky != 0
}

// Safe user cache aliases and macOS's /var and /tmp are normal ancestor paths.
// Only protected root/current-user paths may redirect; the final private target
// is still checked with Lstat and may never itself be a symlink.
func trustedAncestorSymlink(path string, fi os.FileInfo) bool {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != 0 && int(stat.Uid) != os.Getuid()) {
		return false
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || !safeAncestor(parent) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	for p := resolved; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !safeAncestor(info) {
			return false
		}
		if filepath.Dir(p) == p {
			return true
		}
	}
}
