package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jeffhuen/tether-browser/packages/cli"
	"github.com/jeffhuen/tether-browser/packages/client"
	"github.com/jeffhuen/tether-browser/packages/protocol"
)

const helpText = `tether v0.1.14 - zero-latency remote browser automation bridge for AI coding agents
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
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	port := fs.Int("port", 9333, "Daemon RPC listen port (default 9333)")
	proxyPort := fs.Int("proxy-port", 0, "Forward proxy port (0 for ephemeral)")
	workspace := fs.String("workspace", "default", "Workspace profile identifier")
	noChrome := fs.Bool("no-chrome", false, "Do not launch Chrome automatically")
	chromeURL := fs.String("chrome-url", "", "Custom Chrome CDP URL to attach to")
	enroll := fs.String("enroll", "localhost:3000=127.0.0.1:3000,localhost:5173=127.0.0.1:5173,localhost:8000=127.0.0.1:8000,localhost:8080=127.0.0.1:8080", "Comma-separated route enrollments")
	tokenFlag := fs.String("token", "", "Bearer authentication token for daemon RPC (default: persisted workstation token)")
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
	for i := 0; i < 15; i++ {
		if extDriver.IsAvailable() {
			break
		}
		time.Sleep(100 * time.Millisecond)
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

func runNativeHost(args []string) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	if err := client.RunNativeHostServer(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "[Tether NativeHost] Error: %v\n", err)
		return 1
	}
	return 0
}

func runConnect(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println("Usage: tether connect [options] [user@]host")
		fmt.Println("\nLinks your local workstation Chrome to a remote server over SSH in one step:")
		fmt.Println("  1. Starts local tether daemon (if not already running)")
		fmt.Println("  2. Opens SSH reverse tunnel (ssh -R 9333:localhost:9333)")
		fmt.Println("  3. Sets TETHER_AUTH_TOKEN in the remote shell session")
		fmt.Println("\nExample:")
		fmt.Println("  tether connect user@my-server.com")
		return 0
	}

	targetHost := args[0]
	sshExtraArgs := args[1:]

	token, err := cli.EnsureDaemonToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving auth token: %v\n", err)
		return 1
	}
	// Check if local daemon is already running and authenticated.
	// A bare TCP dial is rejected: the port could belong to an alien service
	// or a daemon with an unmatching token, which would fail subsequent RPCs.
	if daemonHealthy(token) {
		fmt.Println("✓ Local workstation daemon already running on 127.0.0.1:9333 (authenticated)")
	} else {
		// If port 9333 responds to TCP but failed daemonHealthy, reject rather
		// than tunneling an unauthenticated or alien listener.
		if conn, err := net.DialTimeout("tcp", "127.0.0.1:9333", 300*time.Millisecond); err == nil {
			_ = conn.Close()
			fmt.Fprintln(os.Stderr, "Error: port 127.0.0.1:9333 is occupied by an unauthenticated process or daemon with a different token")
			return 1
		}

		fmt.Println("Starting local workstation daemon...")
		selfExe, err := os.Executable()
		if err != nil {
			selfExe = "tether"
		}
		daemonCmd := exec.Command(selfExe, "daemon")
		daemonEnv := make([]string, 0, len(os.Environ())+1)
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "TETHER_AUTH_TOKEN=") {
				daemonEnv = append(daemonEnv, kv)
			}
		}
		daemonEnv = append(daemonEnv, "TETHER_AUTH_TOKEN="+token)
		daemonCmd.Env = daemonEnv
		daemonCmd.Stdout = os.Stdout
		daemonCmd.Stderr = os.Stderr
		if err := daemonCmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Error starting daemon: %v\n", err)
			return 1
		}
		defer func() {
			if daemonCmd.Process != nil {
				_ = daemonCmd.Process.Kill()
			}
		}()

		// Wait up to 75s for daemon readiness. The RPC port opens only after
		// Chrome is up, and the launcher allows cold starts 30s plus lock
		// acquisition 30s and process termination 5s. A bare TCP dial is not
		// enough: confirm the daemon actually serves authenticated RPC, so
		// a slow-but-healthy startup is never killed.
		ready := false
		deadline := time.Now().Add(75 * time.Second)
		for time.Now().Before(deadline) {
			if daemonHealthy(token) {
				ready = true
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if !ready {
			fmt.Fprintln(os.Stderr, "Error: local daemon failed to become ready on 127.0.0.1:9333 within 75s")
			return 1
		}
		fmt.Println("✓ Local workstation daemon and Chrome started")
	}

	fmt.Printf("Connecting to %s with SSH reverse tunnel (-R 9333:localhost:9333)...\n", targetHost)
	q := cli.ShellQuote(token)
	remoteCmd := fmt.Sprintf("export TETHER_AUTH_TOKEN=%s; mkdir -p ~/.cache/tether && chmod 700 ~/.cache/tether && printf %%s %s > ~/.cache/tether/auth && chmod 600 ~/.cache/tether/auth; exec ${SHELL:-bash} -l", q, q)
	sshArgs := []string{
		"-R", "9333:localhost:9333",
		"-t", targetHost,
	}
	sshArgs = append(sshArgs, sshExtraArgs...)
	sshArgs = append(sshArgs, remoteCmd)

	cmd := exec.Command("ssh", sshArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "SSH error: %v\n", err)
		return 1
	}
	return 0
}

// daemonHealthy dials the local daemon and performs an authenticated status
// call, proving the port serves valid JSON-RPC 2.0, the token matches, and
// the browser is actively connected.
func daemonHealthy(token string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := cli.NewClient("127.0.0.1:9333")
	c.SetTimeout(2 * time.Second)
	c.SetToken(token)
	resp, err := c.Call(ctx, protocol.MethodStatus, protocol.StatusParams{})
	if err != nil || resp == nil || resp.Error != nil || resp.JSONRPC != "2.0" || len(resp.Result) == 0 {
		return false
	}
	var status protocol.StatusResult
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		return false
	}
	return status.Connected
}
