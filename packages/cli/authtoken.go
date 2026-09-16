package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// daemonTokenPath is the workstation-side persisted daemon token.
func daemonTokenPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "tether", "auth_token")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "tether", "auth_token")
}

// serverTokenPath is the server-side cached token written by `tether connect`.
func serverTokenPath() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "tether", "auth")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "tether", "auth")
}

func readTokenFile(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// EnsureDaemonToken returns the workstation daemon token, generating and
// persisting one (0600) on first use. An explicit env value always wins and
// is never written to disk.
func EnsureDaemonToken() (string, error) {
	if tok := strings.TrimSpace(os.Getenv("TETHER_AUTH_TOKEN")); tok != "" {
		return tok, nil
	}
	path := daemonTokenPath()
	if tok := readTokenFile(path); tok != "" {
		return tok, nil
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate auth token: %w", err)
	}
	tok := hex.EncodeToString(buf)
	if path == "" {
		return tok, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("create token dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(tok+"\n"), 0600); err != nil {
		return "", fmt.Errorf("persist auth token: %w", err)
	}
	return tok, nil
}

// ResolveClientToken returns the token the CLI should present: explicit env,
// then the connect-written server cache, then the local daemon file
// (single-machine `tether daemon` + `tether snapshot` with zero config).
func ResolveClientToken() string {
	if tok := strings.TrimSpace(os.Getenv("TETHER_AUTH_TOKEN")); tok != "" {
		return tok
	}
	if tok := readTokenFile(serverTokenPath()); tok != "" {
		return tok
	}
	return readTokenFile(daemonTokenPath())
}

// ShellQuote renders s safe for single-quoted POSIX shell embedding.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
