//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// localDaemonOwner uses kernel socket ownership, never an RPC-supplied PID.
func localDaemonOwner(exe string) (int, bool) {
	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return 0, false
	}
	inodes := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) > 9 && f[1] == "0100007F:2475" && f[3] == "0A" && f[7] == strconv.Itoa(os.Getuid()) {
			inodes["socket:["+f[9]+"]"] = true
		}
	}
	if len(inodes) == 0 {
		return 0, false
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		base := filepath.Join("/proc", entry.Name())
		fi, err := os.Stat(base)
		if err != nil {
			continue
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || int(st.Uid) != os.Getuid() {
			continue
		}
		actual, err := os.Readlink(filepath.Join(base, "exe"))
		if err != nil {
			continue
		}
		actual = strings.TrimSuffix(actual, " (deleted)")
		if filepath.Clean(actual) != filepath.Clean(exe) {
			continue
		}
		args, err := os.ReadFile(filepath.Join(base, "cmdline"))
		if err != nil {
			continue
		}
		argv := strings.Split(string(args), "\x00")
		if len(argv) < 2 || argv[1] != "daemon" {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(base, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, _ := os.Readlink(filepath.Join(base, "fd", fd.Name()))
			if inodes[target] {
				return pid, true
			}
		}
	}
	return 0, false
}

func runningDaemonVersion(pid int) string {
	return binaryVersion(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
}
