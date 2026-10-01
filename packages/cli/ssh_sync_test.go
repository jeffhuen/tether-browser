//go:build linux || darwin

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestTokenSyncUsesPrivateXDGCacheAndRefusesSymlink(t *testing.T) {
	home := isolateHome(t)
	t.Setenv("SHELL", "/bin/sh")
	cache := filepath.Join(home, "separate-cache")
	t.Setenv("XDG_CACHE_HOME", cache)
	syncKey := func(key string) error {
		cmd := exec.Command("sh", "-c", TokenSyncCommand())
		cmd.Stdin = strings.NewReader(key + "\n")
		return cmd.Run()
	}
	if err := syncKey("remote-key"); err != nil {
		t.Fatal(err)
	}
	if got := ResolveClientToken(); got != "remote-key" {
		t.Fatalf("remote CLI did not resolve synced XDG key: %q", got)
	}
	path := filepath.Join(cache, "tether", "auth")
	if err := protocol.ValidatePrivateFile(path); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(home, "victim")
	if err := os.WriteFile(victim, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}
	if syncKey("new-key") == nil {
		t.Fatal("token sync followed symlink")
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("victim changed: %q %v", data, err)
	}
}

func TestConcurrentDaemonTokenCreationUsesOneIdentity(t *testing.T) {
	isolateHome(t)
	const count = 12
	keys := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() { defer wg.Done(); key, err := EnsureDaemonToken(); keys <- key; errs <- err }()
	}
	wg.Wait()
	close(keys)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first string
	for key := range keys {
		if first == "" {
			first = key
		}
		if key != first || key == "" {
			t.Fatal("concurrent callers acquired different daemon identities")
		}
	}
}

func TestLoginOnlyXDGAndNoisyProfileKeepMachineOutputClean(t *testing.T) {
	home := isolateHome(t)
	t.Setenv("SHELL", "/bin/sh")
	profile := `export XDG_CACHE_HOME="$HOME/profile-cache"
printf 'noisy login profile\n'
`
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	syncCmd := exec.Command("sh", "-c", TokenSyncCommand())
	syncCmd.Stdin = strings.NewReader("profile-key\n")
	if output, err := syncCmd.Output(); err != nil || len(output) != 0 {
		t.Fatalf("token sync machine stdout contaminated: %q %v", output, err)
	}
	cache := filepath.Join(home, "profile-cache")
	t.Setenv("XDG_CACHE_HOME", cache)
	if got := ResolveClientToken(); got != "profile-key" {
		t.Fatalf("login CLI cache differs from sync: %q", got)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	command := exec.Command("sh", "-c", RemoteLoginCommand(`printf '%s\n' "$XDG_CACHE_HOME/tether"`))
	output, err := command.Output()
	if err != nil || string(output) != filepath.Join(cache, "tether")+"\n" {
		t.Fatalf("login profile replaced machine path result: %q %v", output, err)
	}
}
