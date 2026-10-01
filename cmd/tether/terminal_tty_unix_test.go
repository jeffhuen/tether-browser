//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/cli"
)

func TestTerminalStopObservationDoesNotReapMasterExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	changes := make(chan os.Signal, 1)
	signal.Notify(changes, syscall.SIGCHLD)
	defer signal.Stop(changes)
	master := exec.CommandContext(ctx, "sh", "-c", "kill -STOP $$; exit 7")
	cli.ManageCommand(master)
	if err := master.Start(); err != nil {
		t.Fatal(err)
	}
	var waitErr error
	done := make(chan struct{})
	go func() { waitErr = master.Wait(); close(done) }()
	defer func() {
		select {
		case <-done:
		default:
			_ = cli.KillCommand(master)
			<-done
		}
	}()
	stopped := false
	for !stopped {
		select {
		case <-changes:
			var err error
			stopped, err = terminalChildStopped(master.Process.Pid)
			if err != nil {
				t.Fatal(err)
			}
		case <-done:
			t.Fatalf("master exited instead of remaining suspended: %v", waitErr)
		case <-ctx.Done():
			t.Fatal("master stop was not observed")
		}
	}
	if err := syscall.Kill(-master.Process.Pid, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("continued master did not exit")
	}
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("stop observer consumed or corrupted Cmd.Wait exit status: %v", waitErr)
	}
}
