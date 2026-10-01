package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jeffhuen/tether-browser/packages/cli"
	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func usesNetBirdProxy(ctx context.Context, host string, extra []string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	args := append([]string{"-G"}, extra...)
	args = append(args, "--", host)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cli.ManageCommand(cmd)
	defer cli.KillCommand(cmd)
	out := &boundedOutput{limit: 1 << 20}
	cmd.Stdout = out
	if cmd.Run() != nil {
		return false
	}
	return netBirdConfig(out.String())
}
func netBirdConfig(config string) bool {
	for _, line := range strings.Split(config, "\n") {
		words := strings.Fields(line)
		if len(words) < 4 || words[0] != "proxycommand" {
			continue
		}
		words = words[1:]
		if words[0] == "exec" {
			words = words[1:]
		}
		if len(words) >= 3 && filepath.Base(strings.Trim(words[0], "\"'")) == "netbird" && words[1] == "ssh" && words[2] == "proxy" {
			return true
		}
	}
	return false
}

func checkControl(ctx context.Context, control, host string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := protocol.ValidatePrivateSocket(control); err != nil {
		return err
	}
	args := []string{"-S", control, "-O", "check", "-o", "ControlMaster=no", "-o", "ProxyCommand=false", "-o", "ProxyJump=none", "-o", "BatchMode=yes", "--", host}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cli.ManageCommand(cmd)
	defer cli.KillCommand(cmd)
	out := &boundedOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SSH control master unavailable: %w", err)
	}
	return nil
}

func ensureSSHReverse(ctx context.Context, control, host, socketPath, token, session string) error {
	if err := probeSOCKS(ctx, socketPath, token, session); err == nil {
		return nil
	}
	forwardCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := protocol.ValidatePrivateSocket(control); err != nil {
		return err
	}
	cmd := exec.CommandContext(forwardCtx, "ssh", "-F", "none", "-S", control, "-O", "forward", "-o", "ControlMaster=no", "-o", "ExitOnForwardFailure=yes", "-R", "127.0.0.1:9333:127.0.0.1:9333", "--", host)
	cli.ManageCommand(cmd)
	defer cli.KillCommand(cmd)
	out := &boundedOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SSH reverse forwarding failed: %s (%w)", strings.TrimSpace(out.String()), err)
	}
	if err := probeSOCKS(ctx, socketPath, token, session); err != nil {
		return fmt.Errorf("SSH authenticated forwarding failed: %w", err)
	}
	return nil
}

type controlMetadata struct {
	Host        string `json:"host"`
	ControlPath string `json:"controlPath"`
}

func controlMetadataPath() string {
	dir := localScreenshotsDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(dir), "terminal-control.json")
}
func publishTerminalControl(host, control string) error {
	path := controlMetadataPath()
	if path == "" {
		return errors.New("private control metadata cache unavailable")
	}
	if err := protocol.ValidatePrivateSocket(control); err != nil {
		return err
	}
	data, err := json.Marshal(controlMetadata{host, control})
	if err != nil {
		return err
	}
	if err := protocol.PrivateScreenshotCache(filepath.Dir(filepath.Dir(path))); err != nil {
		return err
	}
	return protocol.WritePrivateFile(path, data)
}
func terminalControl(ctx context.Context) (string, string, error) {
	path := controlMetadataPath()
	if path == "" {
		return "", "", errors.New("no authenticated SSH session is available")
	}
	if err := protocol.ValidatePrivateFile(path); err != nil {
		return "", "", errors.New("no authenticated SSH session is available")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return "", "", err
	}
	if len(data) > 4096 {
		return "", "", errors.New("invalid SSH control metadata")
	}
	var meta controlMetadata
	if json.Unmarshal(data, &meta) != nil || meta.Host == "" || strings.HasPrefix(meta.Host, "-") || strings.ContainsAny(meta.Host, "\r\n\t ") {
		return "", "", errors.New("invalid SSH control metadata")
	}
	if err := checkControl(ctx, meta.ControlPath, meta.Host); err != nil {
		return meta.Host, "", err
	}
	return meta.Host, meta.ControlPath, nil
}
func unpublishTerminalControl(control string) {
	path := controlMetadataPath()
	if protocol.ValidatePrivateFile(path) != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var meta controlMetadata
	if json.Unmarshal(data, &meta) == nil && meta.ControlPath == control {
		_ = os.Remove(path)
	}
}

func netBirdAuthFailure(text string) bool {
	lower := strings.ToLower(text)
	failed := strings.Contains(lower, "failed") || strings.Contains(lower, "error") || strings.Contains(lower, "expired") || strings.Contains(lower, "denied")
	return failed && (strings.Contains(lower, "authentication") || strings.Contains(lower, "jwt") || strings.Contains(lower, "oidc"))
}
