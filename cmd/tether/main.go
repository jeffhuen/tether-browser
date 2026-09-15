package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
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

func runDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	port := fs.Int("port", 9333, "Daemon RPC listen port (default 9333)")
	proxyPort := fs.Int("proxy-port", 0, "Forward proxy port (0 for ephemeral)")
	workspace := fs.String("workspace", "default", "Workspace profile identifier")
	noChrome := fs.Bool("no-chrome", false, "Do not launch Chrome automatically")
	chromeURL := fs.String("chrome-url", "", "Custom Chrome CDP URL to attach to")
	_ = fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var chromeProc *client.ChromeProcess
	cdpURL := *chromeURL

	proxy := client.NewProxy()
	go func() {
		_ = proxy.ListenAndServe(*proxyPort)
	}()
	defer proxy.Close()

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

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.ListenAndServe(*port)
	}()

	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down daemon...")
		_ = server.Close()
		return 0
	case err := <-errChan:
		if err != nil && !isClosedError(err) {
			fmt.Fprintf(os.Stderr, "Daemon error: %v\n", err)
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
