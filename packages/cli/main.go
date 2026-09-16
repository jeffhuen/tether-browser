package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

const usageText = `tether v0.1.11 - remote browser automation for AI coding agents
Usage: tether <command> [args] [options]
Commands:
  open <url>                 Navigate to URL
  snapshot                   Accessibility tree with [@eN] refs
  click <sel>                Click element by selector or @eN ref
  dblclick <sel>             Double-click element
  fill <sel> <text>          Clear and fill form field
  type <sel> <text>          Type text into element
  press <key>                Press key (Enter, Tab, Control+a)
  hover <sel>                Hover over element
  focus <sel>                Focus element
  eval <js>                  Run JavaScript expression
  wait <sel|ms>              Wait for element or duration
  screenshot [path]          Capture screenshot
  close [--all]              Close active tab or all tabs
  status                     Show daemon and target connectivity
  review [start|list|clear|send] In-page developer review inspector
  broker [run|start|stop]    Manage session broker daemon

Snapshot Options:
  -i, --interactive          Interactive elements only
  -c, --compact              Remove empty structural elements
  -d, --depth <n>            Limit tree depth
  -s, --selector <sel>       Scope tree to CSS selector

Global Options:
  --json                     Output JSON instead of formatted text
  --timeout <ms>             Command timeout in milliseconds
  --session <name>           Target isolated browser session
`

// Run parses arguments, executes commands, and prints results to stdout/stderr.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usageText)
		return 0
	}

	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usageText)
		return 0
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
			fmt.Fprintf(stdout, "{\"version\":%q}\n", protocol.Version)
		} else {
			fmt.Fprintf(stdout, "tether v%s\n", protocol.Version)
		}
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

	daemonAddr := os.Getenv("TETHER_DAEMON_ADDR")
	if daemonAddr == "" {
		daemonAddr = DefaultDaemonAddr
	}

	socketPath := DefaultBrokerSocket()
	// Named sessions require the broker for isolated target tracking
	var client *Client
	if cmd.Global.Session != "" && cmd.Global.Session != "default" {
		if !IsBrokerAlive(socketPath) {
			_ = StartBackgroundBroker()
		}
		if !IsBrokerAlive(socketPath) {
			fmt.Fprintf(stderr, "Error: session %q requires the tether broker; start it with 'tether broker start'\n", cmd.Global.Session)
			return 1
		}
		client = NewClient("unix:" + socketPath)
	} else if IsBrokerAlive(socketPath) {
		client = NewClient("unix:" + socketPath)
	} else {
		client = NewClient(daemonAddr)
	}

	timeout := 30 * time.Second
	if cmd.Global.TimeoutMs > 0 {
		timeout = time.Duration(cmd.Global.TimeoutMs) * time.Millisecond
	}
	client.SetTimeout(timeout)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	params := cmd.Params
	if cmd.Global.Session != "" || cmd.Global.TimeoutMs > 0 {
		params = injectSessionAndTimeout(params, cmd.Global.Session, cmd.Global.TimeoutMs)
	}

	resp, err := client.Call(ctx, cmd.Method, params)
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
		if res.Error != "" {
			fmt.Fprint(stderr, out)
			return 1
		}
		fmt.Fprint(stdout, out)

	case "screenshot":
		var res protocol.ScreenshotResult
		_ = resp.UnmarshalResult(&res)
		if cmd.ScreenshotPath != "" {
			if len(res.Base64) == 0 {
				fmt.Fprintln(stderr, "Error: empty screenshot data received")
				return 1
			}
			data, err := base64.StdEncoding.DecodeString(res.Base64)
			if err != nil {
				fmt.Fprintf(stderr, "Error: failed to decode screenshot base64: %v\n", err)
				return 1
			}
			if err := os.WriteFile(cmd.ScreenshotPath, data, 0644); err != nil {
				fmt.Fprintf(stderr, "Error: failed to write screenshot to %s: %v\n", cmd.ScreenshotPath, err)
				return 1
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

	case "review":
		out, err := FormatReview(cmd.BrokerSubcmd, resp, cmd.Global.JSON)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, out)

	default:
		fmt.Fprintf(stderr, "Unknown command: %s\n", cmd.Name)
		return 1
	}

	return 0
}

func injectSessionAndTimeout(params any, session string, timeoutMs int) any {
	var m map[string]any
	if params != nil {
		data, err := json.Marshal(params)
		if err == nil {
			_ = json.Unmarshal(data, &m)
		}
	}
	if m == nil {
		m = make(map[string]any)
	}
	if session != "" {
		m["session"] = session
	}
	if timeoutMs > 0 {
		m["timeoutMs"] = timeoutMs
	}
	return m
}
