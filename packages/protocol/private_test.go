//go:build linux || darwin

package protocol

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type foreignInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (f foreignInfo) Sys() any { return &f.stat }

func TestPrivatePathsRejectRedirectionPermissionsAndForeignOwner(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "private")
	if err := PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if PrivateDir(dir) == nil {
		t.Fatal("permissive directory was repaired or accepted")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	stat := *fi.Sys().(*syscall.Stat_t)
	stat.Uid++
	if ownedByCurrentUser(foreignInfo{fi, stat}) == nil || safeAncestor(foreignInfo{fi, stat}) {
		t.Fatal("foreign directory owner accepted")
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if PrivateDir(alias) == nil {
		t.Fatal("symlinked private directory accepted")
	}
	if _, err := os.Lstat(alias); err != nil {
		t.Fatal("untrusted symlink was deleted")
	}
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "shot.png")
	if err := os.Symlink(victim, target); err != nil {
		t.Fatal(err)
	}
	if WritePrivateFile(target, []byte("replacement")) == nil {
		t.Fatal("symlink target overwritten")
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "original" {
		t.Fatalf("victim changed: %q %v", data, err)
	}
}

func TestPrivateFilesAllowSafeCacheAncestorAliasesNotUnsafeTargets(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.Mkdir(cache, 0755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "cache-alias")
	if err := os.Symlink(cache, alias); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(alias, "tether", "auth")
	if err := WritePrivateFile(path, []byte("private-key")); err != nil {
		t.Fatalf("ordinary owned cache alias was refused: %v", err)
	}
	if err := ValidatePrivateFile(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cache, "tether", "auth"))
	if err != nil || string(data) != "private-key" {
		t.Fatal("cache alias did not resolve to intended private target")
	}
	if err := os.Chmod(cache, 0777); err != nil {
		t.Fatal(err)
	}
	if PrivateDir(filepath.Join(alias, "other-app")) == nil {
		t.Fatal("world-writable alias target ancestor accepted")
	}
	if _, err := os.Lstat(alias); err != nil {
		t.Fatal("unsafe ancestor alias was unlinked")
	}
}
