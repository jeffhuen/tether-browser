//go:build linux

package cli

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestManagedCancellationTerminatesProxyDescendants(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 600 & printf '%s\\n' \"$!\"; wait")
	ManageCommand(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		KillCommand(cmd)
		cmd.Wait()
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		KillCommand(cmd)
		cmd.Wait()
		t.Fatal(err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	cancel()
	_ = cmd.Wait()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		data, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		_, state, _ := strings.Cut(string(data), ") ")
		if strings.HasPrefix(state, "Z ") {
			return
		} // exited; the init process owns reaping
	}
	t.Fatal("ProxyCommand descendant survived managed cancellation")
}
