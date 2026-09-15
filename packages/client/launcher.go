package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LauncherConfig specifies options for starting a managed Chrome instance.
type LauncherConfig struct {
	WorkspaceID string
	ProxyPort   int
	ExecPath    string
	ExtraArgs   []string
	Headless    bool
}

// ChromeProcess represents a running Chrome instance managed by the launcher.
type ChromeProcess struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	profileDir string
	port       int
	wsPath     string
	wsURL      string
}

// ResolveProfileDir resolves the dedicated user data directory for a workspace.
func ResolveProfileDir(workspaceID string) (string, error) {
	if workspaceID == "" {
		workspaceID = "default"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
		if home == "" {
			return "", errors.New("cannot determine user home directory")
		}
	}
	return filepath.Join(home, ".config", "tether", "profiles", workspaceID), nil
}

// FindChromeExecutable locates a Chrome or Chromium binary on the system.
func FindChromeExecutable() (string, error) {
	envVars := []string{"CHROME_PATH", "GOOGLE_CHROME_BIN", "TETHER_CHROME_BIN"}
	for _, env := range envVars {
		if path := os.Getenv(env); path != "" {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}

	binaries := []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
		"chrome",
	}
	for _, bin := range binaries {
		if path, err := exec.LookPath(bin); err == nil {
			return path, nil
		}
	}

	standardPaths := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	}
	for _, path := range standardPaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", errors.New("chrome executable not found")
}

// BuildChromeArgs constructs the command-line arguments for Chrome.
func BuildChromeArgs(cfg LauncherConfig, profileDir string) []string {
	args := []string{
		"--remote-debugging-port=0",
		fmt.Sprintf("--user-data-dir=%s", profileDir),
		"--disable-blink-features=AutomationControlled",
		"--no-first-run",
		"--no-default-browser-check",
	}

	if cfg.ProxyPort > 0 {
		args = append(args,
			fmt.Sprintf("--proxy-server=http://127.0.0.1:%d", cfg.ProxyPort),
			"--proxy-bypass-list=<-loopback>",
		)
	}

	if cfg.Headless {
		args = append(args, "--headless=new")
	}

	args = append(args, cfg.ExtraArgs...)
	return args
}

// ParseDevToolsActivePort reads port and websocket path from a DevToolsActivePort file.
func ParseDevToolsActivePort(filePath string) (int, string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, "", errors.New("empty DevToolsActivePort file")
	}
	line1 := strings.TrimSpace(scanner.Text())
	port, err := strconv.Atoi(line1)
	if err != nil {
		return 0, "", fmt.Errorf("invalid port in DevToolsActivePort: %w", err)
	}

	wsPath := ""
	if scanner.Scan() {
		wsPath = strings.TrimSpace(scanner.Text())
	}

	return port, wsPath, nil
}

// WaitForDevToolsActivePort polls for the DevToolsActivePort file until ready or timeout.
func WaitForDevToolsActivePort(ctx context.Context, filePath string, timeout time.Duration) (int, string, error) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(filePath); err == nil {
				port, wsPath, err := ParseDevToolsActivePort(filePath)
				if err == nil && port > 0 {
					return port, wsPath, nil
				}
			}
			if time.Now().After(deadline) {
				return 0, "", fmt.Errorf("timeout waiting for %s", filePath)
			}
		}
	}
}

// LaunchChrome starts Chrome with configured flags and reads the assigned debugging port.
func LaunchChrome(ctx context.Context, cfg LauncherConfig) (*ChromeProcess, error) {
	execPath := cfg.ExecPath
	if execPath == "" {
		found, err := FindChromeExecutable()
		if err != nil {
			return nil, err
		}
		execPath = found
	}

	profileDir, err := ResolveProfileDir(cfg.WorkspaceID)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return nil, fmt.Errorf("create profile directory: %w", err)
	}

	activePortFile := filepath.Join(profileDir, "DevToolsActivePort")
	_ = os.Remove(activePortFile)

	args := BuildChromeArgs(cfg, profileDir)
	cmd := exec.Command(execPath, args...)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start chrome process: %w", err)
	}

	proc := &ChromeProcess{
		cmd:        cmd,
		profileDir: profileDir,
	}

	port, wsPath, err := WaitForDevToolsActivePort(ctx, activePortFile, 15*time.Second)
	if err != nil {
		_ = proc.Close()
		return nil, fmt.Errorf("detect allocated port: %w", err)
	}

	proc.port = port
	proc.wsPath = wsPath
	proc.wsURL = fmt.Sprintf("ws://127.0.0.1:%d%s", port, wsPath)
	return proc, nil
}

// Port returns the allocated ephemeral debugging port.
func (cp *ChromeProcess) Port() int {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.port
}

// WebSocketURL returns the browser WebSocket debugging URL.
func (cp *ChromeProcess) WebSocketURL() string {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.wsURL
}

// ProfileDir returns the profile directory path.
func (cp *ChromeProcess) ProfileDir() string {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.profileDir
}

// Close terminates only the launched Chrome process.
func (cp *ChromeProcess) Close() error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	if cp.cmd == nil || cp.cmd.Process == nil {
		return nil
	}

	// Request clean termination
	_ = cp.cmd.Process.Signal(os.Interrupt)

	done := make(chan error, 1)
	go func() {
		done <- cp.cmd.Wait()
	}()

	select {
	case <-time.After(2 * time.Second):
		_ = cp.cmd.Process.Kill()
		<-done
	case <-done:
	}

	cp.cmd = nil
	return nil
}
