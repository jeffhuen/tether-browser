package client

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateWorkspaceID(t *testing.T) {
	// Valid IDs
	valid := []string{"ws-123", "default", "workspace_42", "PROJ-1"}
	for _, id := range valid {
		if err := ValidateWorkspaceID(id); err != nil {
			t.Errorf("expected valid id %q, got error: %v", id, err)
		}
	}

	// Invalid path traversal IDs
	invalid := []string{"", "../../etc", "ws/123", "ws\\123", "ws..test"}
	for _, id := range invalid {
		if err := ValidateWorkspaceID(id); err == nil {
			t.Errorf("expected invalid id %q to fail validation", id)
		}
	}
}

func TestGetProfileDir(t *testing.T) {
	dir, err := GetProfileDir("test-ws")
	if err != nil {
		t.Fatalf("get profile dir: %v", err)
	}

	if !strings.HasSuffix(dir, filepath.Join("profiles", "test-ws")) && !strings.HasSuffix(dir, filepath.Join("Profiles", "test-ws")) {
		t.Errorf("expected profile dir ending in profiles/test-ws, got: %s", dir)
	}
}

func TestChromeProcessClose(t *testing.T) {
	// Spawn a real benign command (sleep 10) to test graceful shutdown
	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Skip("skipping process test; sleep command not available")
	}

	cp := &ChromeProcess{
		Cmd: cmd,
	}

	if err := cp.Close(); err != nil {
		t.Fatalf("expected clean close, got: %v", err)
	}

	if cmd.ProcessState == nil {
		t.Errorf("expected process state to be recorded after close")
	}
}

func TestProbeActivePort(t *testing.T) {
	dir := t.TempDir()

	if _, ok := probeActivePort(dir); ok {
		t.Fatalf("missing DevToolsActivePort must report unhealthy")
	}
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte("notaport\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := probeActivePort(dir); ok {
		t.Fatalf("garbage port file must report unhealthy")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(fmt.Sprintf("%d\n/devtools/browser/abc\n", port)), 0600); err != nil {
		t.Fatal(err)
	}
	if p, ok := probeActivePort(dir); !ok || p != port {
		t.Fatalf("live port file must report healthy port %d, got %d, %v", port, p, ok)
	}

	stale, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stalePort := stale.Addr().(*net.TCPAddr).Port
	stale.Close()
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(fmt.Sprintf("%d\n", stalePort)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := probeActivePort(dir); ok {
		t.Fatalf("dead port file must report unhealthy")
	}
}

func TestKillStaleProfileProcessesScoped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pkill-based cleanup is unix-only")
	}
	marker := filepath.Join(t.TempDir(), "tether-profile")

	victim := exec.Command("sleep", "60", marker)
	if err := victim.Start(); err != nil {
		t.Skip("sleep command not available")
	}
	victimDone := make(chan struct{})
	go func() {
		_ = victim.Wait()
		close(victimDone)
	}()

	control := exec.Command("sleep", "60")
	if err := control.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = control.Process.Kill()
		_ = control.Wait()
	}()

	killStaleProfileProcesses(marker)

	select {
	case <-victimDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("stale profile process was not reaped")
	}
	if control.ProcessState != nil && control.ProcessState.Exited() {
		t.Fatalf("cleanup killed an unrelated process")
	}
}

func TestEnsureProfileName(t *testing.T) {
	dir := t.TempDir()
	prefsPath := filepath.Join(dir, "Preferences")

	// Missing file: created with the Tether name.
	ensureProfileName(dir)
	data, err := os.ReadFile(prefsPath)
	if err != nil {
		t.Fatalf("expected Preferences to be created: %v", err)
	}
	if !strings.Contains(string(data), `"name":"Tether"`) {
		t.Fatalf("expected Tether name in fresh Preferences, got: %s", data)
	}

	// Existing file: merged, other keys preserved.
	if err := os.WriteFile(prefsPath, []byte(`{"profile":{"avatar_index":7},"browser":{"show_home_button":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	ensureProfileName(dir)
	data, err = os.ReadFile(prefsPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"name":"Tether"`) {
		t.Errorf("expected merged Tether name, got: %s", content)
	}
	if !strings.Contains(content, `"avatar_index":7`) || !strings.Contains(content, `"show_home_button":true`) {
		t.Errorf("expected other keys preserved, got: %s", content)
	}

	// Corrupt file: left alone.
	if err := os.WriteFile(prefsPath, []byte(`{not json`), 0600); err != nil {
		t.Fatal(err)
	}
	ensureProfileName(dir)
	data, err = os.ReadFile(prefsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{not json` {
		t.Errorf("expected corrupt file untouched, got: %s", data)
	}
}
