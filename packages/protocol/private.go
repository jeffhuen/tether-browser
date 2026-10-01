package protocol

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PrivateDir never repairs an untrusted existing directory. Only missing
// components are created; final symlinks and untrusted ancestors are refused.
func PrivateDir(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := noSymlinkComponents(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return ValidatePrivateDir(dir)
}

// PrivateScreenshotCache upgrades only the known 0.1.39 cache layout. General
// IPC directories still use PrivateDir and never repair existing permissions.
func PrivateScreenshotCache(cache string) error {
	root := filepath.Join(cache, "tether")
	dir := filepath.Join(root, "screenshots")
	for _, path := range []string{root, dir} {
		if err := privateLegacyScreenshotPath(path, true); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := privateLegacyScreenshotPath(filepath.Join(dir, entry.Name()), false); err != nil {
			return err
		}
	}
	return nil
}

func privateLegacyScreenshotPath(path string, directory bool) error {
	if err := noSymlinkComponents(path); err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) && directory {
		return PrivateDir(path)
	}
	if err != nil {
		return err
	}
	mode, legacy := os.FileMode(0600), os.FileMode(0644)
	if directory {
		mode, legacy = 0700, 0755
	}
	if fi.Mode()&os.ModeSymlink != 0 || (directory && !fi.IsDir()) || (!directory && !fi.Mode().IsRegular()) {
		return fmt.Errorf("refusing unknown legacy screenshot cache entry: %s", path)
	}
	if err := ownedByCurrentUser(fi); err != nil {
		return err
	}
	if fi.Mode().Perm()&0077 == 0 {
		return nil
	}
	if fi.Mode().Perm()&^legacy != 0 || fi.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("refusing unknown legacy screenshot cache permissions: %s", path)
	}
	// Tighten the validated inode, not a replacement or symlink target.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(fi, opened) {
		return fmt.Errorf("legacy screenshot cache entry changed: %s", path)
	}
	return file.Chmod(mode)
}

func noSymlinkComponents(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 && !trustedAncestorSymlink(p, fi) {
				return fmt.Errorf("refusing untrusted symlink ancestor: %s", p)
			}
			if fi.IsDir() && !safeAncestor(fi) {
				return fmt.Errorf("refusing untrusted directory ancestor: %s", p)
			}
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}
func ValidatePrivateDir(dir string) error {
	if err := noSymlinkComponents(dir); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("directory is not private: %s", dir)
	}
	return ownedByCurrentUser(fi)
}
func ValidatePrivateFile(path string) error {
	if err := ValidatePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0077 != 0 {
		return errors.New("file is not private and regular")
	}
	return ownedByCurrentUser(fi)
}
func ValidatePrivateSocket(path string) error {
	if err := ValidatePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm()&0077 != 0 {
		return errors.New("socket is not private")
	}
	return ownedByCurrentUser(fi)
}
func WritePrivateFile(path string, data []byte) error {
	if err := PrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		if err := ValidatePrivateFile(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tether-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
