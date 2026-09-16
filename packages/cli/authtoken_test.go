package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func isolateHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("TETHER_AUTH_TOKEN", "")
	return tmp
}

func TestResolveClientTokenPrecedence(t *testing.T) {
	home := isolateHome(t)

	if got := ResolveClientToken(); got != "" {
		t.Fatalf("expected empty token with no config, got %q", got)
	}

	configDir := filepath.Join(home, ".config", "tether")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "auth_token"), []byte("daemon-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveClientToken(); got != "daemon-secret" {
		t.Fatalf("expected daemon file token, got %q", got)
	}

	cacheDir := filepath.Join(home, ".cache", "tether")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "auth"), []byte("server-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveClientToken(); got != "server-secret" {
		t.Fatalf("expected server cache to win over daemon file, got %q", got)
	}

	t.Setenv("TETHER_AUTH_TOKEN", "env-secret")
	if got := ResolveClientToken(); got != "env-secret" {
		t.Fatalf("expected env to win over files, got %q", got)
	}
}

func TestEnsureDaemonTokenPersists(t *testing.T) {
	home := isolateHome(t)

	first, err := EnsureDaemonToken()
	if err != nil {
		t.Fatalf("first EnsureDaemonToken: %v", err)
	}
	if first == "" {
		t.Fatal("expected non-empty generated token")
	}
	second, err := EnsureDaemonToken()
	if err != nil {
		t.Fatalf("second EnsureDaemonToken: %v", err)
	}
	if first != second {
		t.Fatalf("expected stable token across calls, got %q then %q", first, second)
	}

	info, err := os.Stat(filepath.Join(home, ".config", "tether", "auth_token"))
	if err != nil {
		t.Fatalf("expected token file to exist: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected token file 0600, got %04o", perm)
	}

	t.Setenv("TETHER_AUTH_TOKEN", "explicit-override")
	if got, _ := EnsureDaemonToken(); got != "explicit-override" {
		t.Fatalf("expected env override, got %q", got)
	}
}
