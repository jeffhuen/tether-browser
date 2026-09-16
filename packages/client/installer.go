package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	// NativeHostName is the unique identifier registered with Chrome for native messaging.
	NativeHostName = "com.tether_browser.host"

	// ExtensionID is the deterministic 32-character Chrome extension ID computed
	// from the fixed RSA public key in packages/extension/manifest.json.
	ExtensionID = "kaloekddddlgghmifoaapnhekggjcggn"
)

// NativeHostManifest represents the Chrome Native Messaging Host manifest schema.
type NativeHostManifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

// GetNativeHostDirectories returns the standard platform paths where Chromium-based
// browsers look for NativeMessagingHosts manifests.
func GetNativeHostDirectories() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	var dirs []string
	switch runtime.GOOS {
	case "darwin":
		dirs = []string{
			filepath.Join(home, "Library/Application Support/Google/Chrome/NativeMessagingHosts"),
			filepath.Join(home, "Library/Application Support/Chromium/NativeMessagingHosts"),
			filepath.Join(home, "Library/Application Support/BraveSoftware/Brave-Browser/NativeMessagingHosts"),
			filepath.Join(home, "Library/Application Support/Microsoft Edge/NativeMessagingHosts"),
		}
	case "linux":
		dirs = []string{
			filepath.Join(home, ".config/google-chrome/NativeMessagingHosts"),
			filepath.Join(home, ".config/chromium/NativeMessagingHosts"),
			filepath.Join(home, ".config/BraveSoftware/Brave-Browser/NativeMessagingHosts"),
			filepath.Join(home, ".config/microsoft-edge/NativeMessagingHosts"),
		}
	case "windows":
		// Handled via registry or local app data
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData != "" {
			dirs = []string{
				filepath.Join(localAppData, "Google/Chrome/User Data/NativeMessagingHosts"),
			}
		}
	}
	return dirs
}

// InstallNativeHostManifest generates and registers the manifest file across all
// installed Chromium browsers on the user's system.
func InstallNativeHostManifest(executablePath string) ([]string, error) {
	if executablePath == "" {
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve executable path: %w", err)
		}
		executablePath = self
	}

	manifest := NativeHostManifest{
		Name:        NativeHostName,
		Description: "Tether Browser Native Messaging Host for AI coding agents",
		Path:        executablePath,
		Type:        "stdio",
		AllowedOrigins: []string{
			fmt.Sprintf("chrome-extension://%s/", ExtensionID),
		},
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}

	dirs := GetNativeHostDirectories()
	var installedPaths []string

	for _, dir := range dirs {
		parent := filepath.Dir(dir)
		// Only install if the browser's parent configuration directory exists
		if _, err := os.Stat(parent); err != nil {
			continue
		}

		if err := os.MkdirAll(dir, 0755); err != nil {
			continue
		}

		manifestPath := filepath.Join(dir, NativeHostName+".json")
		if err := os.WriteFile(manifestPath, manifestBytes, 0644); err == nil {
			installedPaths = append(installedPaths, manifestPath)
		}
	}

	return installedPaths, nil
}
