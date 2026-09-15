package client

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
