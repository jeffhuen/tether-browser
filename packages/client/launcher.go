package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
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

// probeActivePort reads DevToolsActivePort and verifies the port answers.
// A stale file from a dead instance fails the dial and reports unhealthy.
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
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return 0, false
	}
	defer conn.Close()
	// A bare TCP dial proves nothing: the port may have been recycled by a
	// non-CDP service. Require a real DevTools version handshake.
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, _ = fmt.Fprintf(conn, "GET /json/version HTTP/1.0\r\n\r\n")
	resp, err := io.ReadAll(io.LimitReader(conn, 8192))
	if err != nil {
		return 0, false
	}
	if !strings.Contains(string(resp), "200") || !strings.Contains(string(resp), "webSocketDebuggerUrl") {
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

// profileKillPattern matches only processes launched with exactly
// our --user-data-dir argument. The trailing boundary rejects sibling dirs
// (.../profile2) and the --user-data-dir= prefix rejects lookalike flags
// (... --backup=<dir>). Any process carrying this exact flag claims our
// dedicated profile dir, which only our Chrome instances do.
func profileKillPattern(profileDir string) string {
	return `--user-data-dir=` + regexp.QuoteMeta(profileDir) + `($| )`
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
	waitForPatternExit(pattern, 5*time.Second)
}

// waitForPatternExit polls until no process matches pattern (unix only).
func waitForPatternExit(pattern string, timeout time.Duration) {
	if runtime.GOOS == "windows" {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// "--" keeps patterns starting with "--" from parsing as options.
		if err := exec.Command("pgrep", "-f", "--", pattern).Run(); err != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// acquireProfileLaunchLock serializes concurrent launchers for one profile
// via an atomic lock dir. Stale locks (crashed holder) break after 60s.
// It returns a release func for defer.
func acquireProfileLaunchLock(profileDir string, timeout time.Duration) (func(), error) {
	lockDir := filepath.Join(profileDir, "tether-launch.lock")
	release := func() { _ = os.RemoveAll(lockDir) }
	deadline := time.Now().Add(timeout)
	for {
		if err := os.Mkdir(lockDir, 0700); err == nil {
			return release, nil
		}
		if fi, err := os.Stat(lockDir); err == nil && time.Since(fi.ModTime()) > time.Minute {
			_ = os.RemoveAll(lockDir)
			continue
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
