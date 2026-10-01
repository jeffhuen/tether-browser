package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jeffhuen/tether-browser/packages/cli"
	"github.com/jeffhuen/tether-browser/packages/client"
	"github.com/jeffhuen/tether-browser/packages/protocol"
)

const helpText = `tether v0.1.40 - remote-to-local browser bridge for AI agents
Usage:
  tether connect <host>        Link local Chrome to a remote server via SSH in one command
  tether extension install     Register native messaging host for Chrome, Brave, and Edge
  tether daemon [options]      Start the local workstation daemon (drives Chrome via extension or CDP)
  tether broker [options]      Manage the remote session broker (run, start, stop, status)
  tether <command> [args]      Run browser automation commands (open, snapshot, click, review, etc.)

Run 'tether help' or 'tether <command> --help' for details.
`

func enrollRoutes(proxy *client.Proxy, enrollStr string) {
	if enrollStr == "" {
		return
	}
	pairs := strings.Split(enrollStr, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.Split(pair, "=")
		if len(parts) == 2 {
			local := strings.TrimSpace(parts[0])
			remote := strings.TrimSpace(parts[1])
			if local != "" && remote != "" {
				proxy.Enroll(local, remote)
			}
		}
	}
}

func runDaemon(args []string) int {
	// Automatically ensure native messaging host is registered
	_, _ = client.InstallNativeHostManifest("")

	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	port := fs.Int("port", 9333, "Daemon RPC listen port (default 9333)")
	proxyPort := fs.Int("proxy-port", 0, "Forward proxy port (0 for ephemeral)")
	workspace := fs.String("workspace", "default", "Workspace profile identifier")
	noChrome := fs.Bool("no-chrome", false, "Do not launch Chrome automatically")
	chromeURL := fs.String("chrome-url", "", "Custom Chrome CDP URL to attach to")
	enroll := fs.String("enroll", "localhost:3000=127.0.0.1:3000,localhost:5173=127.0.0.1:5173,localhost:8000=127.0.0.1:8000,localhost:8080=127.0.0.1:8080", "Comma-separated route enrollments")
	tokenFlag := fs.String("token", "", "Private TLS identity key (default: persisted workstation key)")
	_ = fs.Parse(args)

	token := strings.TrimSpace(*tokenFlag)
	if token == "" {
		var err error
		token, err = cli.EnsureDaemonToken()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving auth token: %v\n", err)
			return 1
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Synchronously bind the forward proxy listener to guarantee readiness
	// before Chrome launches. The port is stable per workspace so a relaunched
	// daemon re-matches an adopted Chrome instance's launch-time flags.
	proxyLn, actualProxyPort, err := client.ResolveProxyListener(*workspace, *proxyPort)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting forward proxy listener: %v\n", err)
		return 1
	}
	defer proxyLn.Close()

	proxy := client.NewProxy()
	enrollRoutes(proxy, *enroll)

	proxyErrChan := make(chan error, 1)
	go func() {
		proxyErrChan <- proxy.Serve(proxyLn)
	}()
	defer proxy.Close()

	var chromeProc *client.ChromeProcess
	cdpURL := *chromeURL
	var driver client.BrowserDriver
	extDriver := client.NewExtensionDriver("")
	if !extDriver.IsAvailable() && !*noChrome {
		client.EnsureBrowserRunning()
	}
	for i := 0; i < 25; i++ {
		if extDriver.IsAvailable() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if extDriver.IsAvailable() {
		fmt.Printf("✓ Using Tether Chrome Extension bridge (%s)\n", client.GetBridgeSocketPath())
		driver = extDriver
	} else if !*noChrome && cdpURL == "" {
		proc, err := client.LaunchChrome(ctx, *workspace, actualProxyPort)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error launching Chrome: %v\n", err)
			return 1
		}
		chromeProc = proc
		defer chromeProc.Close()
		cdpURL = fmt.Sprintf("http://127.0.0.1:%d", proc.CDPPort)
		driver = client.NewCDPDriver(cdpURL)
	} else {
		if cdpURL == "" {
			cdpURL = "http://127.0.0.1:9222"
		}
		driver = client.NewCDPDriver(cdpURL)
	}

	server := client.NewServer(driver)
	server.SetAuthToken(token)
	serverErrChan := make(chan error, 1)
	go func() {
		serverErrChan <- server.ListenAndServe(*port)
	}()
	defer server.Close()

	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down daemon...")
		return 0
	case err := <-serverErrChan:
		if err != nil && !isClosedError(err) {
			fmt.Fprintf(os.Stderr, "Daemon RPC server error: %v\n", err)
			return 1
		}
		return 0
	case err := <-proxyErrChan:
		if err != nil && !isClosedError(err) {
			fmt.Fprintf(os.Stderr, "Daemon forward proxy error: %v\n", err)
			return 1
		}
		return 0
	}
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	var netOpErr *net.OpError
	return err.Error() == "use of closed network connection" ||
		(err != nil && (err.Error() == "closed" || err.Error() == "server closed")) ||
		(err != nil && (netOpErr != nil && netOpErr.Err.Error() == "use of closed network connection"))
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(helpText)
		os.Exit(0)
	}
	if args[0] == "version" || args[0] == "-v" || args[0] == "--version" || args[0] == "-version" {
		isJSON := false
		for _, a := range args[1:] {
			if a == "--json" {
				isJSON = true
				break
			}
		}
		if isJSON {
			fmt.Printf("{\"version\":%q}\n", protocol.Version)
		} else {
			fmt.Printf("tether v%s\n", protocol.Version)
		}
		os.Exit(0)
	}
	if args[0] == "connect" {
		code := runConnect(args[1:])
		os.Exit(code)
	}
	if args[0] == "extension" {
		code := runExtension(args[1:])
		os.Exit(code)
	}
	if args[0] == "native-host" || strings.HasPrefix(args[0], "chrome-extension://") {
		code := runNativeHost(args[1:])
		os.Exit(code)
	}
	if args[0] == "daemon" {
		code := runDaemon(args[1:])
		os.Exit(code)
	}
	code := cli.Run(args, os.Stdout, os.Stderr)
	os.Exit(code)
}
func runExtension(args []string) int {
	if len(args) > 0 && args[0] == "install" {
		paths, err := client.InstallNativeHostManifest("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error installing native host manifest: %v\n", err)
			return 1
		}
		if len(paths) == 0 {
			fmt.Println("No Chromium browser installation directories found.")
			fmt.Println("Supported: Google Chrome, Chromium, Brave, Microsoft Edge.")
			return 1
		}
		fmt.Printf("✓ Registered Tether Native Messaging Host in %d browser location(s):\n", len(paths))
		for _, p := range paths {
			fmt.Printf("  • %s\n", p)
		}
		fmt.Println("\nTo load the extension:")
		fmt.Println("  1. Open chrome://extensions (or brave://extensions / edge://extensions)")
		fmt.Println("  2. Enable 'Developer mode' (toggle in top right)")
		fmt.Println("  3. Click 'Load unpacked' and select the 'packages/extension' directory")
		fmt.Println("  4. Extension ID will match automatically: " + client.ExtensionID)
		return 0
	}
	fmt.Println("Usage: tether extension install")
	return 0
}
func activeHostPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "tether", "active_host")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "tether", "active_host")
}

func saveActiveHost(host string) {
	p := activeHostPath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0700)
	_ = os.WriteFile(p, []byte(strings.TrimSpace(host)), 0600)
}

func getActiveHost() string {
	p := activeHostPath()
	if p == "" {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func clearActiveHost() {
	p := activeHostPath()
	if p != "" {
		_ = os.Remove(p)
	}
}

func localScreenshotsDir() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "tether", "screenshots")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "tether", "screenshots")
}
func runNativeHost(args []string) int {
	// Keep the OS signal defaults while waiting for Chrome's first frame.
	// A sendNativeMessage file operation must never claim the automation socket.
	firstFrame, err := client.ReadNativeMessage(os.Stdin)
	if errors.Is(err, io.EOF) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Tether NativeHost] Error: %v\n", err)
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := serveNativeHost(ctx, os.Stdin, os.Stdout, firstFrame); err != nil {
		fmt.Fprintf(os.Stderr, "[Tether NativeHost] Error: %v\n", err)
		return 1
	}
	return 0
}

func serveNativeHost(ctx context.Context, in io.Reader, out io.Writer, firstFrame []byte) error {
	var first struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(firstFrame, &first); err != nil {
		return err
	}
	var ssh sshSession
	defer ssh.Close()
	if first.Type == "system_delete_screenshot" || first.Type == "system_clear_screenshots" {
		result, err := handleScreenshotSystem(ctx, &ssh, first.Type, firstFrame)
		return client.WriteNativeSystemResponse(out, first.ID, result, err)
	}
	onSys := func(msgType string, payload []byte) (any, error) {
		switch msgType {
		case "system_ssh_connect":
			var req struct {
				TargetHost string `json:"targetHost"`
				ProxyPort  int    `json:"proxyPort"`
			}
			if err := json.Unmarshal(payload, &req); err != nil {
				return nil, err
			}
			return ssh.Connect(ctx, req.TargetHost, req.ProxyPort)
		case "system_ssh_status":
			return ssh.Status(), nil
		case "system_ssh_disconnect":
			return ssh.Disconnect(), nil
		case "system_save_screenshot", "system_clear_screenshots", "system_delete_screenshot":
			return handleScreenshotSystem(ctx, &ssh, msgType, payload)
		}
		return nil, fmt.Errorf("unknown system message type: %s", msgType)
	}

	return client.RunNativeHostServer(ctx, in, out, firstFrame, onSys)
}

func ensureDaemonRunning(ctx context.Context, token string) error {
	if _, err := protocol.DaemonTLS(token, false); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	selfExe := selfExecutable()
	if err := stopStaleDaemon(ctx, token, selfExe); err != nil {
		return err
	}
	if daemonHealthy(ctx, token) {
		return nil
	}
	if _, live := daemonStatus(ctx, token); !live {
		if err := replaceOwnedLegacyDaemon(ctx, token, selfExe); err != nil {
			return err
		}
		if conn, err := (&net.Dialer{Timeout: 300 * time.Millisecond}).DialContext(ctx, "tcp", cli.DefaultDaemonAddr); err == nil {
			_ = conn.Close()
			return errors.New("port 127.0.0.1:9333 is occupied by an unauthenticated process or daemon with a different key")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		cmd := exec.Command(selfExe, "daemon")
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "TETHER_AUTH_TOKEN=") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		cmd.Env = append(cmd.Env, "TETHER_AUTH_TOKEN="+token)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start daemon: %w", err)
		}
		go func() { _ = cmd.Wait() }()
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("local daemon browser did not become ready within 15s")
		case <-ticker.C:
			if daemonHealthy(ctx, token) {
				return nil
			}
		}
	}
}

func runConnect(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println("Usage: tether connect [options] [user@]host")
		fmt.Println("\nLinks your local workstation Chrome to a remote server over SSH in one step:")
		fmt.Println("  1. Starts the local tether daemon, replacing one from another tether version")
		fmt.Println("  2. Opens SSH reverse tunnel (ssh -R 9333:localhost:9333)")
		fmt.Println("  3. Syncs the private auth cache, then opens your normal remote shell")
		fmt.Println("\nExample:")
		fmt.Println("  tether connect user@my-server.com")
		return 0
	}
	targetHost := args[0]
	if strings.HasPrefix(targetHost, "-") {
		fmt.Fprintf(os.Stderr, "Error: invalid target host %q\n", targetHost)
		return 1
	}
	sshExtraArgs := args[1:]
	// Automatically ensure native messaging host is registered
	_, _ = client.InstallNativeHostManifest("")

	token, err := cli.EnsureDaemonToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving auth token: %v\n", err)
		return 1
	}
	if err := ensureDaemonRunning(context.Background(), token); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if strings.ContainsAny(token, "\r\n") {
		fmt.Fprintln(os.Stderr, "Error: auth key contains a line break")
		return 1
	}
	return connectTerminal(targetHost, sshExtraArgs, token)
}

// daemonStatus performs an authenticated status call against the local daemon,
// proving the port serves valid JSON-RPC 2.0 and the token matches.
func daemonStatus(ctx context.Context, token string) (*protocol.StatusResult, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c := cli.NewClient("127.0.0.1:9333")
	c.SetTimeout(2 * time.Second)
	c.SetToken(token)
	resp, err := c.Call(ctx, protocol.MethodStatus, protocol.StatusParams{})
	if err != nil || resp == nil || resp.Error != nil || resp.JSONRPC != "2.0" || len(resp.Result) == 0 {
		return nil, false
	}
	var status protocol.StatusResult
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		return nil, false
	}
	return &status, true
}

// daemonHealthy reports whether the local daemon is authenticated and its browser is connected.
func daemonHealthy(ctx context.Context, token string) bool {
	status, ok := daemonStatus(ctx, token)
	return ok && status.Connected
}

// selfExecutable returns the tether binary to launch as the daemon.
func selfExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return "tether"
	}
	return exe
}

// stopStaleDaemon stops a local daemon whose version differs from exe, the
// binary about to be launched. The native host lives as long as Chrome, so its
// own compiled-in version can be older than the installed binary.
func stopStaleDaemon(ctx context.Context, token, exe string) error {
	status, ok := daemonStatus(ctx, token)
	if !ok {
		return nil
	}
	want := binaryVersion(exe)
	if status.DaemonVersion == want {
		return nil
	}
	if pid, owned := localDaemonOwner(exe); !owned || pid != status.DaemonPID {
		// Through a reverse tunnel, 127.0.0.1:9333 can be another machine's daemon,
		// and daemons older than 0.1.36 report no PID. Leave both running.
		version := status.DaemonVersion
		if version == "" {
			version = "older than 0.1.36"
		}
		fmt.Fprintf(os.Stderr, "Warning: reusing authenticated remote tether daemon (%s); the installed tether is %s\n", version, want)
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	proc, err := os.FindProcess(status.DaemonPID)
	if err == nil {
		err = proc.Signal(syscall.SIGTERM)
	}
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop tether daemon %s (pid %d): %w", status.DaemonVersion, status.DaemonPID, err)
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		conn, err := (&net.Dialer{Timeout: 300 * time.Millisecond}).DialContext(ctx, "tcp", "127.0.0.1:9333")
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		_ = conn.Close()
		// A concurrent connect may already have started the replacement.
		if current, ok := daemonStatus(ctx, token); ok && current.DaemonPID != status.DaemonPID {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("tether daemon %s (pid %d) did not stop within 10s", status.DaemonVersion, status.DaemonPID)
}

// binaryVersion returns the version exe reports, or this binary's version if it cannot be read.
func binaryVersion(exe string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "version", "--json")
	cli.ManageCommand(cmd)
	out, err := cmd.Output()
	var v struct {
		Version string `json:"version"`
	}
	if err != nil || json.Unmarshal(out, &v) != nil || v.Version == "" {
		return protocol.Version
	}
	return v.Version
}
