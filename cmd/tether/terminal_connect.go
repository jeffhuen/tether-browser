package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/jeffhuen/tether-browser/packages/cli"
)

func connectTerminal(host string, extra []string, token string) int {
	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsFunc(host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		fmt.Fprintln(os.Stderr, "Error: invalid SSH host")
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	dir, err := os.MkdirTemp("", "tether-ssh-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	control := filepath.Join(dir, "s")
	socketPath := filepath.Join(dir, "p")
	timeout := "10"
	netbird := usesNetBirdProxy(ctx, host, extra)
	if netbird {
		timeout = "300"
	}
	// OpenSSH keeps the first -o value; put the private control path last
	// because -S, unlike -o, replaces a value already supplied on the CLI.
	args := []string{"-o", "ControlMaster=yes", "-o", "ControlPersist=no", "-o", "ForkAfterAuthentication=yes", "-o", "ClearAllForwardings=yes", "-o", "StreamLocalBindMask=0177", "-o", "ExitOnForwardFailure=yes", "-o", "LogLevel=INFO", "-o", "ConnectTimeout=" + timeout, "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4"}
	args = append(args, extra...)
	args = append(args, "-N", "-S", control, "--", host)
	master := exec.CommandContext(ctx, "ssh", args...)
	cli.ManageCommand(master)
	terminal := terminalMasterForeground(master)
	restored := false
	restoreTTY := func() error {
		if restored {
			return nil
		}
		restored = true
		return terminal.restore()
	}
	defer restoreTTY()
	// OpenSSH detaches only after authentication. The detached mux master can
	// handle the interactive slave's TTY without background-group stop signals.
	master.Stdout, master.Stderr = os.Stdout, os.Stderr
	if err := master.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "SSH master:", err)
		return 1
	}
	exited := make(chan error, 1)
	go func() { exited <- master.Wait() }()
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer closeCancel()
		closeMaster := exec.CommandContext(closeCtx, "ssh", "-S", control, "-O", "exit", "-o", "ControlMaster=no", "-o", "ProxyCommand=false", "-o", "ProxyJump=none", "-o", "BatchMode=yes", "--", host)
		cli.ManageCommand(closeMaster)
		_ = closeMaster.Run()
		_ = cli.KillCommand(closeMaster)
		_ = cli.KillCommand(master)
		<-exited
	}()
	// Wait for authenticated control setup, not a separate check-mode connection.
	authDeadline := time.Now().Add(signInTimeout)
	timer := time.NewTimer(signInTimeout)
	defer timer.Stop()
authentication:
	for {
		if err := checkControl(ctx, control, host); err == nil {
			break
		}
		select {
		case err := <-exited:
			exited <- err
			if err == nil && checkControl(ctx, control, host) == nil {
				break authentication
			}
			fmt.Fprintln(os.Stderr, "SSH authentication failed:", err)
			return 1
		case <-terminal.changes:
			remaining := time.Until(authDeadline)
			stopped, err := terminal.suspendIfStopped()
			if err != nil {
				fmt.Fprintln(os.Stderr, "SSH terminal job control:", err)
				return 1
			}
			if stopped {
				authDeadline = time.Now().Add(remaining)
				timer.Reset(remaining)
			}
		case <-timer.C:
			fmt.Fprintln(os.Stderr, "SSH authentication timed out")
			return 1
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "SSH authentication:", ctx.Err())
			return 1
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := restoreTTY(); err != nil {
		fmt.Fprintln(os.Stderr, "Restore terminal:", err)
		return 1
	}
	syncCtx, syncCancel := context.WithTimeout(ctx, 10*time.Second)
	syncCmd := exec.CommandContext(syncCtx, "ssh", strictControlArgs(control, host, cli.TokenSyncCommand())...)
	cli.ManageCommand(syncCmd)
	syncCmd.Stdin = strings.NewReader(token + "\n")
	syncCmd.Stdout, syncCmd.Stderr = os.Stdout, os.Stderr
	err = syncCmd.Run()
	_ = cli.KillCommand(syncCmd)
	syncCancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "SSH credential sync:", err)
		return 1
	}
	closeProxy, err := openPrivateSSHProxy(ctx, control, host, socketPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeProxy()
	if err := ensureSSHReverse(ctx, control, host, socketPath, token, "terminal"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := publishTerminalControl(host, control); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	saveActiveHost(host)
	defer func() {
		unpublishTerminalControl(control)
		if getActiveHost() == host {
			clearActiveHost()
		}
	}()
	// Only this child inherits the caller's foreground group and stdin/TTY.
	shellArgs := []string{"-t", "-o", "ControlMaster=no", "-o", "ControlPersist=no", "-o", "ProxyCommand=false", "-o", "ProxyJump=none", "-o", "BatchMode=yes", "-o", "ConnectTimeout=1", "-o", "ClearAllForwardings=yes", "-o", "SessionType=default"}
	for i := 0; i < len(extra); i++ {
		// The master's jump route is already authenticated. A slave must never
		// construct another jump-host transport, even if the master disappears.
		if strings.HasPrefix(extra[i], "-J") {
			if extra[i] == "-J" {
				i++
			}
			continue
		}
		shellArgs = append(shellArgs, extra[i])
	}
	shellArgs = append(shellArgs, "-S", control, "--", host)
	shell := exec.CommandContext(ctx, "ssh", shellArgs...)
	shell.Stdin, shell.Stdout, shell.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := shell.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "SSH shell:", err)
		return 1
	}
	return 0
}
