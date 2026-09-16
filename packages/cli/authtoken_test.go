package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"":                "''",
		"plain-token-123": "'plain-token-123'",
		"with space":      "'with space'",
		"it's":            "'it'\\''s'",
		"a$b`c\"d":        "'a$b`c\"d'",
		"line1\nline2":    "'line1\nline2'",
		"-leading-dash":   "'-leading-dash'",
		"100%":            "'100%'",
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
	// Roundtrip through a real POSIX shell: printf %s must echo the input back.
	// (Newlines excluded: printf %s passes them through literally by design,
	// and real tokens never contain them; single-quote wrapping is still pinned above.)
	for in := range cases {
		if in == "" || strings.Contains(in, "\n") {
			continue
		}
		out, err := exec.Command("sh", "-c", "printf %s "+ShellQuote(in)).Output()
		if err != nil {
			t.Fatalf("shell roundtrip %q: %v", in, err)
		}
		if string(out) != in {
			t.Errorf("shell roundtrip %q = %q", in, string(out))
		}
	}
}
