package client

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveProfileDir(t *testing.T) {
	dir, err := ResolveProfileDir("ws-123")
	if err != nil {
		t.Fatalf("resolve profile dir: %v", err)
	}

	if !strings.Contains(dir, filepath.Join(".config", "tether", "profiles", "ws-123")) {
		t.Fatalf("expected profile dir containing .config/tether/profiles/ws-123, got: %s", dir)
	}

	// Empty workspace ID defaults to "default"
	defaultDir, err := ResolveProfileDir("")
	if err != nil {
		t.Fatalf("resolve empty profile dir: %v", err)
	}
	if !strings.Contains(defaultDir, filepath.Join(".config", "tether", "profiles", "default")) {
		t.Fatalf("expected default profile dir, got: %s", defaultDir)
	}
}

func TestBuildChromeArgs(t *testing.T) {
	cfg := LauncherConfig{
		WorkspaceID: "test-ws",
		ProxyPort:   9444,
		Headless:    true,
		ExtraArgs:   []string{"--test-arg"},
	}

	args := BuildChromeArgs(cfg, "/tmp/tether-test-profile")
	argStr := strings.Join(args, " ")

	expected := []string{
		"--remote-debugging-port=0",
		"--user-data-dir=/tmp/tether-test-profile",
		"--disable-blink-features=AutomationControlled",
		"--proxy-server=http://127.0.0.1:9444",
		"--proxy-bypass-list=<-loopback>",
		"--headless=new",
		"--test-arg",
	}

	for _, exp := range expected {
		if !strings.Contains(argStr, exp) {
			t.Errorf("expected Chrome args to contain %q, got:\n%s", exp, argStr)
		}
	}
}

func TestParseDevToolsActivePort(t *testing.T) {
	tmpDir := t.TempDir()
	portFile := filepath.Join(tmpDir, "DevToolsActivePort")

	content := "54321\n/devtools/browser/f9b8c7d6-1234-5678-abcd-ef0123456789\n"
	if err := os.WriteFile(portFile, []byte(content), 0600); err != nil {
		t.Fatalf("write port file: %v", err)
	}

	port, wsPath, err := ParseDevToolsActivePort(portFile)
	if err != nil {
		t.Fatalf("parse active port: %v", err)
	}

	if port != 54321 {
		t.Errorf("expected port 54321, got: %d", port)
	}
	if wsPath != "/devtools/browser/f9b8c7d6-1234-5678-abcd-ef0123456789" {
		t.Errorf("expected wsPath /devtools/browser/f9b8c7d6-1234-5678-abcd-ef0123456789, got: %s", wsPath)
	}
}

func TestWaitForDevToolsActivePort(t *testing.T) {
	tmpDir := t.TempDir()
	portFile := filepath.Join(tmpDir, "DevToolsActivePort")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Write file asynchronously after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.WriteFile(portFile, []byte("41234\n/devtools/browser/test\n"), 0600)
	}()

	port, wsPath, err := WaitForDevToolsActivePort(ctx, portFile, 1*time.Second)
	if err != nil {
		t.Fatalf("wait for active port: %v", err)
	}

	if port != 41234 {
		t.Errorf("expected port 41234, got: %d", port)
	}
	if wsPath != "/devtools/browser/test" {
		t.Errorf("expected wsPath /devtools/browser/test, got: %s", wsPath)
	}
}

func TestChromeProcessClose(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mock process: %v", err)
	}

	proc := &ChromeProcess{
		cmd:        cmd,
		profileDir: "/tmp/mock-profile",
		port:       9999,
		wsPath:     "/mock",
		wsURL:      "ws://127.0.0.1:9999/mock",
	}

	if proc.Port() != 9999 {
		t.Errorf("expected port 9999, got: %d", proc.Port())
	}
	if proc.WebSocketURL() != "ws://127.0.0.1:9999/mock" {
		t.Errorf("expected wsURL ws://127.0.0.1:9999/mock, got: %s", proc.WebSocketURL())
	}

	if err := proc.Close(); err != nil {
		t.Fatalf("close process: %v", err)
	}

	// Ensure process has terminated
	err := cmd.Process.Signal(os.Interrupt)
	if err == nil {
		t.Errorf("process still running after Close()")
	}
}
