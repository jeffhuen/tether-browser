package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

const usageText = `Usage: tether [global flags] <command> [args...]

Global flags:
  --json             Output raw JSON instead of formatted text
  --timeout <ms>     Timeout in milliseconds
  --session <name>   Browser session identifier

Commands:
  open <url>                              Navigate to a URL
  snapshot [-i] [-c] [-d depth] [-s sel]  Capture accessibility tree snapshot
  click <sel>                             Click an element by selector or ref
  dblclick <sel>                          Double-click an element
  fill <sel> <text>                       Fill a form field with text
  type <sel> <text>                       Type text into a field
  press <key>                             Send a key event (Enter, Tab, Escape)
  hover <sel>                             Hover over an element
  focus <sel>                             Focus an element
  eval <expr>                             Evaluate a JavaScript expression
  wait <sel|ms>                           Wait for selector or duration in ms
  screenshot [path]                       Capture viewport screenshot
  close [--all]                           Close current tab or all tabs
  status                                  Show daemon connectivity status
  broker [start|stop|status|run]          Manage persistent session broker
`

func main() {
	code := Run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}

// Run executes the CLI with the given arguments, writing output to stdout and stderr.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 1
	}

	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usageText)
		return 0
	}

	cmd, err := ParseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	if cmd.Name == "broker" {
		return HandleBrokerCommand(cmd.BrokerSubcmd, stdout, stderr)
	}

	// Select transport: broker socket if active, otherwise direct daemon TCP
	var client *Client
	socketPath := DefaultBrokerSocket()
	if IsBrokerAlive(socketPath) {
		client = NewClient("unix:" + socketPath)
	} else {
		client = NewClient(DefaultDaemonAddr)
	}

	timeout := 30 * time.Second
	if cmd.Global.TimeoutMs > 0 {
		timeout = time.Duration(cmd.Global.TimeoutMs) * time.Millisecond
	}
	client.SetTimeout(timeout)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resp, err := client.Call(ctx, cmd.Method, cmd.Params)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	switch cmd.Name {
	case "open":
		var res protocol.OpenResult
		_ = resp.UnmarshalResult(&res)
		out, err := FormatOpen(&res, cmd.Global.JSON)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, out)

	case "snapshot":
		var res protocol.SnapshotResult
		_ = resp.UnmarshalResult(&res)
		out, err := FormatSnapshot(&res, cmd.Global.JSON)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, out)

	case "click", "dblclick", "fill", "type", "press", "hover", "focus", "wait", "close":
		var res protocol.ActionResult
		_ = resp.UnmarshalResult(&res)
		out, err := FormatAction(&res, cmd.Global.JSON)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, out)

	case "eval":
		var res protocol.EvalResult
		_ = resp.UnmarshalResult(&res)
		out, err := FormatEval(&res, cmd.Global.JSON)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, out)

	case "screenshot":
		var res protocol.ScreenshotResult
		_ = resp.UnmarshalResult(&res)
		if cmd.ScreenshotPath != "" && len(res.Base64) > 0 {
			if data, err := base64.StdEncoding.DecodeString(res.Base64); err == nil {
				_ = os.WriteFile(cmd.ScreenshotPath, data, 0644)
			}
		}
		out, err := FormatScreenshot(&res, cmd.ScreenshotPath, cmd.Global.JSON)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, out)

	case "status":
		var res protocol.StatusResult
		_ = resp.UnmarshalResult(&res)
		out, err := FormatStatus(&res, cmd.Global.JSON)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, out)

	default:
		fmt.Fprintf(stdout, "%s\n", string(resp.Result))
	}

	return 0
}
