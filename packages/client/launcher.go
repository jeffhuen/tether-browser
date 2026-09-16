package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var validWorkspaceIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

// ChromeProcess manages the lifecycle of an isolated Chrome instance.
type ChromeProcess struct {
	Cmd        *exec.Cmd
	ProfileDir string
	CDPPort    int
	ProxyPort  int
}

// ValidateWorkspaceID ensures a workspace ID does not contain path traversal characters.
func ValidateWorkspaceID(id string) error {
	if id == "" {
		return errors.New("workspace ID cannot be empty")
	}
	if !validWorkspaceIDRegex.MatchString(id) {
		return fmt.Errorf("invalid workspace ID %q: must contain only alphanumeric characters, underscores, and dashes", id)
	}
	return nil
}

// GetProfileDir returns a safe, platform-appropriate user data directory for a workspace.
func GetProfileDir(workspaceID string) (string, error) {
	if err := ValidateWorkspaceID(workspaceID); err != nil {
		return "", err
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get user home directory: %w", err)
	}

	var baseDir string
	switch runtime.GOOS {
	case "darwin":
		baseDir = filepath.Join(homeDir, "Library", "Application Support", "Tether", "Profiles")
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		baseDir = filepath.Join(appData, "Tether", "Profiles")
	default:
		// Linux and other Unixes
		configDir := os.Getenv("XDG_CONFIG_HOME")
		if configDir == "" {
			configDir = filepath.Join(homeDir, ".config")
		}
		baseDir = filepath.Join(configDir, "tether", "profiles")
	}

	return filepath.Join(baseDir, workspaceID), nil
}

// FindChromeExecutable locates Google Chrome or Chromium on the host machine.
func FindChromeExecutable() (string, error) {
	// 1. Check environment variable override
	if envPath := os.Getenv("TETHER_CHROME_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
	}

	// 2. Platform-specific default locations
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		}
	case "windows":
		programFiles := os.Getenv("ProgramFiles")
		programFilesX86 := os.Getenv("ProgramFiles(x86)")
		localAppData := os.Getenv("LOCALAPPDATA")
		candidates = []string{
			filepath.Join(programFiles, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(programFilesX86, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(localAppData, "Google", "Chrome", "Application", "chrome.exe"),
		}
	default:
		// Linux
		candidates = []string{
			"google-chrome",
			"google-chrome-stable",
			"chromium",
			"chromium-browser",
		}
	}

	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path, nil
		}
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	return "", errors.New("google chrome executable not found; install Chrome or set TETHER_CHROME_PATH")
}

// LaunchChrome launches an isolated Chrome instance configured for Mode A proxying.
func LaunchChrome(ctx context.Context, workspaceID string, proxyPort int) (*ChromeProcess, error) {
	profileDir, err := GetProfileDir(workspaceID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return nil, fmt.Errorf("create profile directory: %w", err)
	}
	unlock, err := acquireProfileLaunchLock(profileDir, 30*time.Second)
	if err != nil {
		return nil, err
	}
	defer unlock()

	// Self-heal order: adopt a live instance only when it provably serves CDP
	// for OUR proxy configuration (its launch-time --proxy-server flags are
	// immutable, so a mismatched adoption would route traffic into the void).
	// Otherwise kill only our own stale profile processes (never the user's
	// regular Chrome) and launch fresh. No manual recovery.
	if port, ok := probeActivePort(profileDir); ok && proxyPortPersisted(profileDir, proxyPort) {
		return &ChromeProcess{
			ProfileDir: profileDir,
			CDPPort:    port,
			ProxyPort:  proxyPort,
		}, nil
	}
	killStaleProfileProcesses(profileDir)

	// Name our dedicated profile "Tether" so Chrome's own profile chip
	// identifies the driven browser. Merges into existing Preferences so
	// long-lived profiles (which predate the feature) get labeled too;
	// other keys are never touched.
	// (A custom banner is impossible via flags: bad_flags_prompt.cc renders the
	// raw flag text, and --enable-automation would disable password managers.)
	ensureProfileName(profileDir)

	// Clean up any stale DevToolsActivePort file from previous crashes
	activePortFile := filepath.Join(profileDir, "DevToolsActivePort")
	_ = os.Remove(activePortFile)

	chromePath, err := FindChromeExecutable()
	if err != nil {
		return nil, err
	}

	args := []string{
		"--user-data-dir=" + profileDir,
		"--remote-debugging-port=0", // Ephemeral port allocation
		"--no-first-run",
		"--no-default-browser-check",
		"--new-window",
		"about:blank",
	}
	if proxyPort > 0 {
		args = append(args,
			fmt.Sprintf("--proxy-server=http://127.0.0.1:%d", proxyPort),
			`--proxy-bypass-list=<-loopback>`,
		)
	}

	cmd := exec.CommandContext(ctx, chromePath, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start chrome process: %w", err)
	}

	// Poll for DevToolsActivePort to discover the allocated ephemeral port.
	// Cold starts (component updates, profile migration) can exceed 10s.
	cdpPort, err := waitForActivePort(profileDir, 30*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("wait for devtools active port: %w (profile %s)", err, profileDir)
	}
	// Persist the proxy port configured for this running Chrome instance.
	// Only written when a new Chrome is successfully launched.
	_ = recordChromeProxyPort(profileDir, proxyPort)

	return &ChromeProcess{
		Cmd:        cmd,
		ProfileDir: profileDir,
		CDPPort:    cdpPort,
		ProxyPort:  proxyPort,
	}, nil
}

// Close gracefully terminates the Chrome process owned by Tether.
func (cp *ChromeProcess) Close() error {
	if cp == nil || cp.Cmd == nil || cp.Cmd.Process == nil {
		return nil
	}
	// Try graceful termination first
	if err := cp.Cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cp.Cmd.Process.Kill()
	}

	done := make(chan error, 1)
	go func() {
		done <- cp.Cmd.Wait()
	}()

	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		return cp.Cmd.Process.Kill()
	}
}

// probeActivePort reads DevToolsActivePort and strictly validates the port.
// It returns the CDP port only if: (1) the port responds to HTTP, (2) the
// response status is 200, (3) the parsed /json/version body contains a
// usable ws:// WebSocket debugger URL, and (4) the debugger's target path
// matches the browser identity recorded on line 2 of DevToolsActivePort.
// A stale file from a dead instance, an HTTP error page, a response without
// a valid debugger URL, or a port reused by a different Chrome is rejected.
func probeActivePort(profileDir string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(profileDir, "DevToolsActivePort"))
	if err != nil || len(data) == 0 {
		return 0, false
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || port <= 0 {
		return 0, false
	}
	expectedTarget := ""
	if len(lines) >= 2 {
		expectedTarget = strings.TrimSpace(lines[1])
	}

	// Chrome CDP requires a valid Host header (rejects missing Host header as HTTP 500).
	// Using standard http.Client ensures Host: 127.0.0.1:<port> is properly sent.
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return 0, false
	}
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &version); err != nil {
		return 0, false
	}
	wsURL := strings.TrimSpace(version.WebSocketDebuggerURL)
	if !strings.HasPrefix(wsURL, "ws://") {
		return 0, false
	}
	if expectedTarget != "" && !strings.HasSuffix(wsURL, expectedTarget) {
		return 0, false
	}
	return port, true
}

func waitForActivePort(profileDir string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	activePortFile := filepath.Join(profileDir, "DevToolsActivePort")

	for time.Now().Before(deadline) {
		if port, ok := probeActivePort(profileDir); ok {
			return port, nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return 0, fmt.Errorf("timed out after %v waiting for %s", timeout, activePortFile)
}

// profileKillPattern matches only processes launched with our exact
// --user-data-dir argument. An argument boundary prefix (^|[ \t"'])
// rejects lookalike argument prefixes (e.g. --saved-arg=--user-data-dir=...),
// and a boundary suffix ([ \t"']|$) rejects sibling directories
// (.../profile2) while supporting Windows and Go argument quoting.
func profileKillPattern(profileDir string) string {
	return `(^|[ \t"'])--user-data-dir="?` + regexp.QuoteMeta(profileDir) + `"?([ \t"']|$)`
}

// killStaleProfileProcesses terminates leftover Chrome processes bound to our
// dedicated profile dir (orphaned when a daemon is killed). Best-effort:
// failures are ignored so a missing pkill can never fail launch.
func killStaleProfileProcesses(profileDir string) {
	pattern := profileKillPattern(profileDir)
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			"$d=$env:TETHER_PROFILE_DIR; Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -match $d } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }")
		cmd.Env = append(os.Environ(), "TETHER_PROFILE_DIR="+pattern)
		_ = cmd.Run()
		return
	}
	_ = exec.Command("pkill", "-f", "--", pattern).Run()
	if !waitForPatternExit(pattern, 3*time.Second) {
		// Escalate to SIGKILL if any processes refused graceful SIGTERM
		_ = exec.Command("pkill", "-9", "-f", "--", pattern).Run()
		_ = waitForPatternExit(pattern, 2*time.Second)
	}
}

// waitForPatternExit polls until no process matches pattern (unix only).
// Returns true if all matching processes exited within timeout.
func waitForPatternExit(pattern string, timeout time.Duration) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// "--" keeps patterns starting with "--" from parsing as options.
		if err := exec.Command("pgrep", "-f", "--", pattern).Run(); err != nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// acquireProfileLaunchLock serializes concurrent launchers for one profile
// via an atomic lock dir. An owner token written inside the directory prevents
// delayed releases or breakers from deleting a replacement lock. Stale locks
// from dead processes break immediately; abandoned locks break after timeout.
func acquireProfileLaunchLock(profileDir string, timeout time.Duration) (func(), error) {
	lockDir := filepath.Join(profileDir, "tether-launch.lock")
	ownerFile := filepath.Join(lockDir, "owner")
	token := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())

	release := func() {
		data, err := os.ReadFile(ownerFile)
		if err == nil && strings.TrimSpace(string(data)) == token {
			_ = os.Remove(ownerFile)
			_ = os.Remove(lockDir)
		}
	}

	deadline := time.Now().Add(timeout)
	for {
		if err := os.Mkdir(lockDir, 0700); err == nil {
			_ = os.WriteFile(ownerFile, []byte(token), 0600)
			return release, nil
		}
		// break immediately; abandoned locks break after 30s.
		if fi, err := os.Stat(lockDir); err == nil {
			isStale := false
			data, err := os.ReadFile(ownerFile)
			if err == nil {
				parts := strings.Split(strings.TrimSpace(string(data)), "-")
				if len(parts) >= 1 {
					if pid, err := strconv.Atoi(parts[0]); err == nil && pid > 0 {
						if p, err := os.FindProcess(pid); err == nil {
							if err := p.Signal(syscall.Signal(0)); err != nil {
								// Process is confirmed dead: break immediately
								isStale = true
							}
						}
					}
				}
			}
			if isStale || time.Since(fi.ModTime()) > 30*time.Second {
				_ = os.RemoveAll(lockDir)
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another launcher holds %s", lockDir)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// ensureProfileName labels our dedicated profile "Tether" in Chrome's own
// profile chip. Chrome keeps per-profile state under <user-data-dir>/Default,
// with the display name cached in <user-data-dir>/Local State, so both are
// updated: Default/Preferences always, Local State only when it already
// exists (a fresh Chrome builds its own cache from Preferences on first run).
// Decoding preserves unrelated values byte-for-byte; corrupt or non-object
// files are left alone for Chrome to rebuild.
func ensureProfileName(profileDir string) {
	setProfileName(filepath.Join(profileDir, "Default", "Preferences"), true)
	if _, err := os.Stat(filepath.Join(profileDir, "Local State")); err == nil {
		setLocalStateName(filepath.Join(profileDir, "Local State"))
	}
}

// setProfileName writes {"profile":{"name":"Tether"}} into a Preferences file,
// preserving every other byte. When create is true a missing file is created.
func setProfileName(path string, create bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !create {
			return
		}
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		_ = os.WriteFile(path, []byte(`{"profile":{"name":"Tether"}}`), 0600)
		return
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil || root == nil {
		return
	}
	var prof map[string]json.RawMessage
	if raw, ok := root["profile"]; ok {
		if err := json.Unmarshal(raw, &prof); err != nil {
			return
		}
	}
	if prof == nil {
		prof = map[string]json.RawMessage{}
	}
	if name, ok := prof["name"]; ok {
		var current string
		if err := json.Unmarshal(name, &current); err == nil && current == "Tether" {
			return
		}
	}
	named, err := json.Marshal("Tether")
	if err != nil {
		return
	}
	prof["name"] = named
	updated, err := json.Marshal(prof)
	if err != nil {
		return
	}
	root["profile"] = updated
	out, err := json.Marshal(root)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, out, 0600)
}

// setLocalStateName updates the profile info_cache display entry that Chrome's
// profile menu actually renders, creating it when the cache exists.
func setLocalStateName(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil || root == nil {
		return
	}
	var profile map[string]json.RawMessage
	if raw, ok := root["profile"]; ok {
		if err := json.Unmarshal(raw, &profile); err != nil {
			return
		}
	}
	if profile == nil {
		profile = map[string]json.RawMessage{}
	}
	var cache map[string]json.RawMessage
	if raw, ok := profile["info_cache"]; ok {
		if err := json.Unmarshal(raw, &cache); err != nil {
			return
		}
	}
	if cache == nil {
		cache = map[string]json.RawMessage{}
	}
	var entry map[string]json.RawMessage
	if raw, ok := cache["Default"]; ok {
		if err := json.Unmarshal(raw, &entry); err != nil {
			return
		}
	}
	if entry == nil {
		entry = map[string]json.RawMessage{}
	}
	nameBytes, err := json.Marshal("Tether")
	if err != nil {
		return
	}
	entry["name"] = nameBytes
	entry["is_using_default_name"], _ = json.Marshal(false)
	updatedEntry, err := json.Marshal(entry)
	if err != nil {
		return
	}
	cache["Default"] = updatedEntry
	updatedCache, err := json.Marshal(cache)
	if err != nil {
		return
	}
	profile["info_cache"] = updatedCache
	updatedProfile, err := json.Marshal(profile)
	if err != nil {
		return
	}
	root["profile"] = updatedProfile
	out, err := json.Marshal(root)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, out, 0600)
}
