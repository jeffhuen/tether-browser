package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jeffhuen/tether-browser/packages/cli"
	"github.com/jeffhuen/tether-browser/packages/client"
)

const helpText = `tether - zero-latency remote browser automation bridge for AI coding agents

Usage:
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
		proc, err := client.LaunchChrome(ctx, *workspace, proxy.Port())
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

	fmt.Printf("Tether daemon listening on 127.0.0.1:%d (Mode A proxy on 127.0.0.1:%d)\n", *port, proxy.Port())

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

	if args[0] == "daemon" {
		code := runDaemon(args[1:])
		os.Exit(code)
	}

	// Dispatch CLI automation and broker commands
	code := cli.Run(args, os.Stdout, os.Stderr)
	os.Exit(code)
}
