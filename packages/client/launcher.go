package client

import (
	"context"
	"errors"
	"fmt"
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

	// Self-heal order: adopt a live instance if the previous daemon died but
	// Chrome survived; otherwise kill only our own stale profile processes
	// (never the user's regular Chrome) and launch fresh. No manual recovery.
	if port, ok := probeActivePort(profileDir); ok {
		return &ChromeProcess{
			ProfileDir: profileDir,
			CDPPort:    port,
			ProxyPort:  proxyPort,
		}, nil
	}
	killStaleProfileProcesses(profileDir)

	// Clean up any stale DevToolsActivePort file from previous crashes
	activePortFile := filepath.Join(profileDir, "DevToolsActivePort")
	_ = os.Remove(activePortFile)

	// Name fresh profiles "Tether" so Chrome's own profile chip identifies the
	// driven browser. Only written when absent; never clobbers existing state.
	// (A custom banner is impossible via flags: bad_flags_prompt.cc renders the
	// raw flag text, and --enable-automation would disable password managers.)
	if _, err := os.Stat(filepath.Join(profileDir, "Preferences")); os.IsNotExist(err) {
		_ = os.WriteFile(filepath.Join(profileDir, "Preferences"), []byte(`{"profile":{"name":"Tether"}}`), 0600)
	}

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
	conn.Close()
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

// killStaleProfileProcesses terminates leftover Chrome processes bound to our
// dedicated profile dir (orphaned when a daemon is killed). The match is
// scoped to our dir only: the user's regular Chrome profile never matches.
// Best-effort: failures are ignored so a missing pkill can never fail launch.
func killStaleProfileProcesses(profileDir string) {
	switch runtime.GOOS {
	case "windows":
		pattern := strings.ReplaceAll(profileDir, "'", "''")
		_ = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			fmt.Sprintf("Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like '*%s*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }", pattern)).Run()
	default:
		_ = exec.Command("pkill", "-f", regexp.QuoteMeta(profileDir)).Run()
	}
}
