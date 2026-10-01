package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/jeffhuen/tether-browser/packages/cli"
	"github.com/jeffhuen/tether-browser/packages/protocol"
)

var mirrorTimeout = 12 * time.Second

func screenshotName(name string) error {
	if name == "" || name == "." || strings.Contains(name, "..") || strings.ContainsAny(name, "/\\") || strings.ContainsFunc(name, unicode.IsControl) {
		return errors.New("invalid screenshot filename")
	}
	return nil
}

func remoteScreenshotSaveCommand(name string) string {
	prepare := cli.RemotePrivateCache + `d=$d/screenshots
[ ! -L "$d" ] || exit 1
if [ ! -d "$d" ]; then mkdir -m 700 "$d"; fi
[ -O "$d" ] && [ "$(stat -c %a "$d" 2>/dev/null || stat -f %Lp "$d")" = 700 ] || exit 1
`
	prepare += "name=" + cli.ShellQuote(name) + "\np=$d/$name\n[ ! -L \"$p\" ] || exit 1\n[ ! -e \"$p\" ] || { [ -f \"$p\" ] && [ -O \"$p\" ]; } || exit 1\n"
	return cli.RemoteLoginCommand(prepare + `tmp=$(mktemp "$d/.shot.XXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM
cat > "$tmp"
chmod 600 "$tmp"
mv -f "$tmp" "$p"
printf '%s/%s\n' "$(cd "$d" && pwd -P)" "$name"
`)
}

func remoteScreenshotDeleteCommand(name, remotePath string) string {
	return remoteScreenshotDeleteCommandAt(name, remotePath, "/tmp/tether-screenshots")
}

func remoteScreenshotDeleteCommandAt(name, remotePath, legacyDir string) string {
	prepare := cli.RemotePrivateCache + "name=" + cli.ShellQuote(name) + "\nexpected=" + cli.ShellQuote(remotePath) + "\nold=" + cli.ShellQuote(legacyDir) + "\n"
	return cli.RemoteLoginCommand(prepare + `if [ "$expected" = "$old/$name" ]; then
q=$(cd "${old%/*}" && pwd -P)
while :; do
safe_dir "$q" || exit 1
[ "$q" != / ] || break
q=${q%/*}; [ -n "$q" ] || q=/
done
[ ! -L "$old" ] || exit 1
if [ ! -e "$old" ]; then exit 0; fi
[ -d "$old" ] && [ -O "$old" ] || exit 1
mode=$(stat -c %a "$old" 2>/dev/null || stat -f %Lp "$old")
[ "$((0$mode & ~0755))" -eq 0 ] || exit 1
p=$old/$name
else
d=$d/screenshots
[ ! -L "$d" ] || exit 1
if [ ! -d "$d" ]; then mkdir -m 700 "$d"; fi
[ -O "$d" ] && [ "$(stat -c %a "$d" 2>/dev/null || stat -f %Lp "$d")" = 700 ] || exit 1
[ "$expected" = "$(cd "$d" && pwd -P)/$name" ] || exit 1
p=$d/$name
fi
[ ! -L "$p" ] || exit 1
if [ -e "$p" ]; then
[ -f "$p" ] && [ -O "$p" ] || exit 1
mode=$(stat -c %a "$p" 2>/dev/null || stat -f %Lp "$p")
[ "$((0$mode & 022))" -eq 0 ] || exit 1
fi
if [ "$expected" = "$old/$name" ]; then chmod 700 "$old"; fi
rm -f -- "$p"
`)
}

// strictControlArgs cannot silently create a fresh SSH connection when a
// control master disappears: the fallback transport is the false command.
func strictControlArgs(control, host string, command string) []string {
	return []string{"-T", "-S", control, "-o", "ControlMaster=no", "-o", "ControlPersist=no", "-o", "ProxyCommand=false", "-o", "ProxyJump=none", "-o", "BatchMode=yes", "-o", "ConnectTimeout=1", "-o", "ClearAllForwardings=yes", "-o", "SessionType=default", "-o", "RemoteCommand=none", "--", host, command}
}

func (s *sshSession) mirrorConnection(hostCtx context.Context) (context.Context, context.CancelFunc, string, string, error) {
	s.mu.Lock()
	host, control, sessionCtx, connected := s.status.Host, s.controlPath, s.ctx, s.status.State == "connected"
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(hostCtx, mirrorTimeout)
	if connected && control != "" && sessionCtx != nil {
		stop := context.AfterFunc(sessionCtx, cancel)
		return ctx, func() { stop(); cancel() }, host, control, nil
	}
	// A terminal-created master is usable only through privately published
	// metadata and an active-master check, never an active_host guess.
	terminalHost, terminalPath, err := terminalControl(ctx)
	if err != nil {
		cancel()
		if terminalHost != "" {
			host = terminalHost
		}
		return nil, nil, host, "", err
	}
	return ctx, cancel, terminalHost, terminalPath, nil
}

func runMirrorSSH(ctx context.Context, control, host, command string, data []byte) (string, error) {
	if err := protocol.ValidatePrivateSocket(control); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "ssh", strictControlArgs(control, host, command)...)
	cli.ManageCommand(cmd)
	defer cli.KillCommand(cmd)
	cmd.Stdin = bytes.NewReader(data)
	out, detail := &boundedOutput{limit: 4096}, &sshErrorBuffer{}
	cmd.Stdout, cmd.Stderr = out, detail
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("screenshot remote operation failed: %w%s", err, sshDetail(detail))
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("SSH output exceeds limit")
	}
	return b.Buffer.Write(p)
}

type screenshotRecord struct {
	Filename   string `json:"filename"`
	TargetHost string `json:"targetHost"`
	RemotePath string `json:"remotePath"`
	Mirrored   *bool  `json:"mirrored,omitempty"`
}

func deleteRemoteScreenshot(ctx context.Context, session *sshSession, shot screenshotRecord) error {
	if shot.Mirrored != nil && !*shot.Mirrored {
		return nil
	}
	if shot.TargetHost == "" || strings.HasPrefix(shot.TargetHost, "-") || strings.ContainsFunc(shot.TargetHost, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return errors.New("original screenshot SSH host is unknown")
	}
	if !filepath.IsAbs(shot.RemotePath) || filepath.Clean(shot.RemotePath) != shot.RemotePath || filepath.Base(shot.RemotePath) != shot.Filename || strings.ContainsFunc(shot.RemotePath, unicode.IsControl) {
		return errors.New("original screenshot remote path is invalid or unknown")
	}
	remoteCtx, cancel, host, control, err := session.mirrorConnection(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	if host != shot.TargetHost {
		return fmt.Errorf("screenshot belongs to SSH host %s, not the connected host %s", shot.TargetHost, host)
	}
	_, err = runMirrorSSH(remoteCtx, control, host, remoteScreenshotDeleteCommand(shot.Filename, shot.RemotePath), nil)
	return err
}

func handleScreenshotSystem(ctx context.Context, session *sshSession, kind string, payload []byte) (any, error) {
	ctx, deadlineCancel := context.WithTimeout(ctx, mirrorTimeout)
	defer deadlineCancel()
	var req struct {
		screenshotRecord
		Base64      string             `json:"base64"`
		Screenshots []screenshotRecord `json:"screenshots"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	if kind == "system_save_screenshot" && req.Filename == "" {
		req.Filename = fmt.Sprintf("tether-shot-%d.png", time.Now().UnixNano())
	}
	if kind != "system_clear_screenshots" {
		if err := screenshotName(req.Filename); err != nil {
			return nil, err
		}
	}
	for _, shot := range req.Screenshots {
		if err := screenshotName(shot.Filename); err != nil {
			return nil, err
		}
	}
	dir := localScreenshotsDir()
	if dir == "" {
		return nil, errors.New("private screenshot cache is unavailable")
	}
	if err := protocol.PrivateScreenshotCache(filepath.Dir(filepath.Dir(dir))); err != nil {
		return nil, err
	}
	var data []byte
	localPath := filepath.Join(dir, req.Filename)
	switch kind {
	case "system_save_screenshot":
		var err error
		data, err = base64.StdEncoding.DecodeString(req.Base64)
		if err != nil {
			return nil, fmt.Errorf("decode screenshot: %w", err)
		}
		if err := protocol.WritePrivateFile(localPath, data); err != nil {
			return nil, fmt.Errorf("save local screenshot: %w", err)
		}
	case "system_delete_screenshot":
		if _, err := os.Lstat(localPath); err == nil {
			if err := protocol.ValidatePrivateFile(localPath); err != nil {
				return nil, err
			}
			if err := os.Remove(localPath); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	case "system_clear_screenshots":
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		// Validate everything first, rather than half-clearing unsafe entries.
		for _, entry := range entries {
			if err := protocol.ValidatePrivateFile(filepath.Join(dir, entry.Name())); err != nil {
				return nil, err
			}
		}
		for _, entry := range entries {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return nil, err
			}
		}
	default:
		return nil, errors.New("unknown screenshot operation")
	}
	if kind == "system_save_screenshot" {
		remoteCtx, cancel, host, control, err := session.mirrorConnection(ctx)
		remotePath := ""
		if err == nil {
			defer cancel()
			remotePath, err = runMirrorSSH(remoteCtx, control, host, remoteScreenshotSaveCommand(req.Filename), data)
		}
		result := map[string]any{"ok": true, "filename": req.Filename, "localPath": localPath, "targetHost": host, "mirrored": err == nil}
		if err != nil {
			result["mirrorError"] = err.Error()
		} else if !filepath.IsAbs(remotePath) || filepath.Base(remotePath) != req.Filename {
			result["mirrored"], result["mirrorError"] = false, "remote SSH did not return a resolved screenshot path"
		} else {
			result["remotePath"] = remotePath
		}
		return result, nil
	}
	result := map[string]any{"ok": true, "localDeleted": true, "remoteDeleted": true}
	if kind == "system_delete_screenshot" {
		if err := deleteRemoteScreenshot(ctx, session, req.screenshotRecord); err != nil {
			result["ok"], result["remoteDeleted"], result["remoteError"] = false, false, err.Error()
		}
		return result, nil
	}
	if req.Screenshots == nil {
		result["ok"], result["remoteDeleted"], result["remoteError"] = false, false, "original screenshot destinations are unknown"
		result["results"] = []map[string]any{}
		return result, nil
	}
	results := make([]map[string]any, 0, len(req.Screenshots))
	for _, shot := range req.Screenshots {
		item := map[string]any{"filename": shot.Filename, "remoteDeleted": true}
		if err := deleteRemoteScreenshot(ctx, session, shot); err != nil {
			item["remoteDeleted"], item["remoteError"] = false, err.Error()
			result["ok"], result["remoteDeleted"], result["remoteError"] = false, false, "some screenshots remain on their original SSH hosts"
		}
		results = append(results, item)
	}
	result["results"] = results
	return result, nil
}
