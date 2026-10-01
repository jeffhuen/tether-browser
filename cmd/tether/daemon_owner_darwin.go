//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jeffhuen/tether-browser/packages/cli"
)

func localDaemonOwner(exe string) (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lsof", "-nP", "-a", "-iTCP@127.0.0.1:9333", "-sTCP:LISTEN", "-Fpu")
	cli.ManageCommand(cmd)
	data, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	pid, uid := 0, -1
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "p") {
			pid, _ = strconv.Atoi(line[1:])
		}
		if strings.HasPrefix(line, "u") {
			uid, _ = strconv.Atoi(line[1:])
		}
	}
	if pid <= 0 || uid != os.Getuid() {
		return 0, false
	}
	check := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=", "-o", "args=")
	cli.ManageCommand(check)
	data, err = check.Output()
	if err != nil {
		return 0, false
	}
	text := strings.TrimSpace(string(data))
	if !strings.HasPrefix(text, filepath.Clean(exe)+" ") && !strings.HasPrefix(text, filepath.Clean(exe)+"\n") {
		return 0, false
	}
	if !strings.Contains(text, " daemon") {
		return 0, false
	}
	return pid, true
}

// Compare the kernel's loaded executable vnode with the installed image. An
// unlinked old image cannot be executed through /proc on macOS.
func runningDaemonVersion(pid int) string {
	exe := selfExecutable()
	fi, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-Fin")
	cli.ManageCommand(cmd)
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	inode := uint64(0)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "i") {
			inode, _ = strconv.ParseUint(line[1:], 10, 64)
		}
		if strings.HasPrefix(line, "n") && strings.TrimSuffix(line[1:], " (deleted)") == exe {
			if inode != 0 && inode != stat.Ino {
				return "replaced-executable"
			}
			if inode == stat.Ino {
				return binaryVersion(exe)
			}
		}
	}
	return ""
}
