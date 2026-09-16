package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

const helpText = `tether v0.1.2 - zero-latency remote browser automation bridge for AI coding agents
Usage:
  tether connect <host>      Link local Chrome to a remote server via SSH in one command
  tether daemon [options]    Start the local workstation daemon (drives Chrome via CDP)
  tether broker [options]    Manage the remote session broker (run, start, stop, status)
  tether <command> [args]    Run browser automation commands (open, snapshot, click, review, etc.)

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
	token := fs.String("token", os.Getenv("TETHER_AUTH_TOKEN"), "Bearer authentication token for daemon RPC")
	_ = fs.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Synchronously bind the forward proxy listener to guarantee readiness before Chrome launches
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", *proxyPort)
	proxyLn, err := net.Listen("tcp", proxyAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting forward proxy listener on %s: %v\n", proxyAddr, err)
		return 1
	}
	defer proxyLn.Close()
	actualProxyPort := proxyLn.Addr().(*net.TCPAddr).Port

	proxy := client.NewProxy()
	enrollRoutes(proxy, *enroll)

	proxyErrChan := make(chan error, 1)
	go func() {
		proxyErrChan <- proxy.Serve(proxyLn)
	}()
	defer proxy.Close()

	var chromeProc *client.ChromeProcess
	cdpURL := *chromeURL

	if !*noChrome && cdpURL == "" {
		proc, err := client.LaunchChrome(ctx, *workspace, actualProxyPort)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error launching Chrome: %v\n", err)
			return 1
		}
		chromeProc = proc
		defer chromeProc.Close()
		cdpURL = fmt.Sprintf("http://127.0.0.1:%d", proc.CDPPort)
	}

	if cdpURL == "" {
		cdpURL = "http://127.0.0.1:9222"
	}

	driver := client.NewCDPDriver(cdpURL)
	server := client.NewServer(driver)
	if *token != "" {
		server.SetAuthToken(*token)
		fmt.Printf("Tether daemon listening on 127.0.0.1:%d [authenticated] (Mode A proxy on 127.0.0.1:%d)\n", *port, actualProxyPort)
	} else {
		fmt.Printf("Tether daemon listening on 127.0.0.1:%d (Mode A proxy on 127.0.0.1:%d)\n", *port, actualProxyPort)
	}
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

	if args[0] == "daemon" {
		code := runDaemon(args[1:])
		os.Exit(code)
	}
	code := cli.Run(args, os.Stdout, os.Stderr)
	os.Exit(code)
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

	token := os.Getenv("TETHER_AUTH_TOKEN")
	if token == "" {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err == nil {
			token = hex.EncodeToString(buf)
		} else {
			token = fmt.Sprintf("tether-%d", time.Now().UnixNano())
		}
	}

	// Check if local daemon is already running
	conn, err := net.DialTimeout("tcp", "127.0.0.1:9333", 300*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		fmt.Println("✓ Local workstation daemon already running on 127.0.0.1:9333")
	} else {
		fmt.Println("Starting local workstation daemon...")
		selfExe, err := os.Executable()
		if err != nil {
			selfExe = "tether"
		}
		daemonCmd := exec.Command(selfExe, "daemon", "--token="+token)
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

		// Wait up to 5s for daemon readiness
		ready := false
		for i := 0; i < 50; i++ {
			c, err := net.DialTimeout("tcp", "127.0.0.1:9333", 100*time.Millisecond)
			if err == nil {
				_ = c.Close()
				ready = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !ready {
			fmt.Fprintln(os.Stderr, "Error: local daemon failed to become ready on 127.0.0.1:9333")
			return 1
		}
		fmt.Println("✓ Local workstation daemon and Chrome started")
	}

	fmt.Printf("Connecting to %s with SSH reverse tunnel (-R 9333:localhost:9333)...\n", targetHost)
	remoteCmd := fmt.Sprintf("export TETHER_AUTH_TOKEN=%q; exec ${SHELL:-bash} -l", token)

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
