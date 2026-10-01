package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jeffhuen/tether-browser/packages/protocol"
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
	if err := protocol.ValidatePrivateFile(path); err != nil {
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
	if err := protocol.PrivateDir(filepath.Dir(path)); err != nil {
		return "", fmt.Errorf("create token dir: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".auth-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(tok + "\n"); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	// Publishing with a hard link is atomic and first-writer-wins, including
	// concurrent native hosts. Readers never see a partially written key.
	if err := os.Link(file.Name(), path); err != nil {
		if existing := readTokenFile(path); os.IsExist(err) && existing != "" {
			return existing, nil
		}
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
