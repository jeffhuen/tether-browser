package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func isDaemonCommand(exe, command string) bool {
	args, ok := strings.CutPrefix(strings.TrimSpace(command), filepath.Clean(exe)+" ")
	return ok && (args == "daemon" || strings.HasPrefix(args, "daemon "))
}

func replaceOwnedLegacyDaemon(ctx context.Context, token, exe string) error {
	if _, ok := daemonStatus(ctx, token); ok {
		return nil
	}
	pid, ok := localDaemonOwner(exe)
	if !ok {
		return nil
	}
	version := runningDaemonVersion(pid)
	if version == "" || version == binaryVersion(exe) {
		return errors.New("local daemon authentication failed; refusing to stop a current daemon with a different key")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Recheck ownership immediately before signaling; remote status never drives it.
	if current, ok := localDaemonOwner(exe); !ok || current != pid {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if current, ok := localDaemonOwner(exe); !ok || current != pid {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("local legacy daemon %s (pid %d) did not stop within 10s", version, pid)
}
