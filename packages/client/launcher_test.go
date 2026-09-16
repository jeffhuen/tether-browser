package client

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	portFile := filepath.Join(dir, "DevToolsActivePort")

	if _, ok := probeActivePort(dir); ok {
		t.Fatalf("missing DevToolsActivePort must report unhealthy")
	}
	if err := os.WriteFile(portFile, []byte("notaport\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := probeActivePort(dir); ok {
		t.Fatalf("garbage port file must report unhealthy")
	}

	// A live TCP listener that is NOT Chrome must be rejected.
	plain, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	go func() {
		for {
			c, err := plain.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	plainPort := plain.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(portFile, []byte(fmt.Sprintf("%d\n/devtools/browser/dead-id\n", plainPort)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := probeActivePort(dir); ok {
		t.Fatalf("non-CDP listener must report unhealthy")
	}

	// A fake Chrome speaking /json/version must be accepted.
	chrome := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"Browser":"Chrome/153.0","Protocol-Version":"1.3","User-Agent":"test","V8-Version":"1.0","WebKit-Version":"537.36","webSocketDebuggerUrl":"ws://127.0.0.1:%d/devtools/browser/abc"}`, 0)
	}))
	defer chrome.Close()
	chromePort := chrome.Listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(portFile, []byte(fmt.Sprintf("%d\n/devtools/browser/abc\n", chromePort)), 0600); err != nil {
		t.Fatal(err)
	}
	if p, ok := probeActivePort(dir); !ok || p != chromePort {
		t.Fatalf("DevTools handshake must report healthy port %d, got %d, %v", chromePort, p, ok)
	}
}
func TestProfileKillPattern(t *testing.T) {
	dir := "/tmp/xyz/kill-profile"
	re := regexp.MustCompile(profileKillPattern(dir))

	mustMatch := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=/tmp/xyz/kill-profile --remote-debugging-port=0",
		"google-chrome --user-data-dir=/tmp/xyz/kill-profile",
		"python3 sleeper.py --user-data-dir=/tmp/xyz/kill-profile",
	}
	for _, cmd := range mustMatch {
		if !re.MatchString(cmd) {
			t.Errorf("expected pattern to match %q", cmd)
		}
	}

	// Reviewer's exact decoys: sibling-suffix dir and lookalike flag.
	mustNotMatch := []string{
		"python3 sleeper.py --user-data-dir=/tmp/xyz/kill-profile2",
		"python3 sleeper.py --backup=/tmp/xyz/kill-profile",
		"/usr/bin/google-chrome --user-data-dir=/home/u/.config/other",
		"tether daemon --workspace default",
	}
	for _, cmd := range mustNotMatch {
		if re.MatchString(cmd) {
			t.Errorf("expected pattern to spare %q", cmd)
		}
	}
}
func TestKillStaleProfileProcessesScoped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pkill-based cleanup is unix-only")
	}
	markerDir := t.TempDir()
	spawnSleeper := func(extraArgs ...string) *exec.Cmd {
		args := append([]string{"-c", "import time; time.sleep(60)"}, extraArgs...)
		return exec.Command("python3", args...)
	}

	victim := spawnSleeper("--user-data-dir=" + markerDir)
	if err := victim.Start(); err != nil {
		t.Skip("python3 not available")
	}
	victimDone := make(chan struct{})
	go func() {
		_ = victim.Wait()
		close(victimDone)
	}()

	sibling := spawnSleeper("--user-data-dir=" + markerDir + "2")
	if err := sibling.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sibling.Process.Kill()
		_ = sibling.Wait()
	}()
	unrelated := spawnSleeper("--backup=" + markerDir)
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = unrelated.Process.Kill()
		_ = unrelated.Wait()
	}()

	killStaleProfileProcesses(markerDir)

	select {
	case <-victimDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("stale profile process was not reaped")
	}
	if sibling.ProcessState != nil && sibling.ProcessState.Exited() {
		t.Fatalf("cleanup killed the sibling-suffix process")
	}
	if unrelated.ProcessState != nil && unrelated.ProcessState.Exited() {
		t.Fatalf("cleanup killed a process with a lookalike flag")
	}
}

func TestEnsureProfileName(t *testing.T) {
	dir := t.TempDir()
	defPrefs := filepath.Join(dir, "Default", "Preferences")

	// Missing file: created under Default/ with the Tether name.
	ensureProfileName(dir)
	data, err := os.ReadFile(defPrefs)
	if err != nil {
		t.Fatalf("expected Default/Preferences to be created: %v", err)
	}
	if !strings.Contains(string(data), `"name":"Tether"`) {
		t.Fatalf("expected Tether name in fresh Preferences, got: %s", data)
	}

	// Existing file: merged, other keys and big integers preserved byte-for-byte.
	if err := os.WriteFile(defPrefs, []byte(`{"profile":{"avatar_index":7},"browser":{"show_home_button":true},"big":9007199254740993}`), 0600); err != nil {
		t.Fatal(err)
	}
	ensureProfileName(dir)
	data, err = os.ReadFile(defPrefs)
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
	if !strings.Contains(content, `9007199254740993`) {
		t.Errorf("expected big integer preserved exactly, got: %s", content)
	}

	// Null root: left alone, no panic.
	if err := os.WriteFile(defPrefs, []byte(`null`), 0600); err != nil {
		t.Fatal(err)
	}
	ensureProfileName(dir)
	data, err = os.ReadFile(defPrefs)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `null` {
		t.Errorf("expected null root untouched, got: %s", data)
	}

	// Corrupt file: left alone.
	if err := os.WriteFile(defPrefs, []byte(`{not json`), 0600); err != nil {
		t.Fatal(err)
	}
	ensureProfileName(dir)
	data, err = os.ReadFile(defPrefs)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{not json` {
		t.Errorf("expected corrupt file untouched, got: %s", data)
	}
}

func TestEnsureProfileNameLocalState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "Local State")

	// Existing cache: display entry updated, siblings preserved.
	if err := os.WriteFile(statePath, []byte(`{"profile":{"info_cache":{"Default":{"name":"Your Chrome","is_using_default_name":true}},"other":1}}`), 0600); err != nil {
		t.Fatal(err)
	}
	ensureProfileName(dir)
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"name":"Tether"`) || !strings.Contains(content, `"is_using_default_name":false`) {
		t.Errorf("expected info_cache display entry updated, got: %s", content)
	}
	if !strings.Contains(content, `"other":1`) {
		t.Errorf("expected sibling keys preserved, got: %s", content)
	}

	// Missing cache: not created (Chrome builds it from Preferences).
	dir2 := t.TempDir()
	ensureProfileName(dir2)
	if _, err := os.Stat(filepath.Join(dir2, "Local State")); !os.IsNotExist(err) {
		t.Errorf("expected no Local State to be created")
	}
}
