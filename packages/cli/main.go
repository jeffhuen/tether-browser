package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

const usageText = `tether v0.1.37 - remote-to-local browser bridge for AI agents
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
  scroll [dir|px]            Scroll page (up, down, top, bottom, or deltaY)
  screenshot [path]          Capture screenshot
  close [--all]              Close active tab or all tabs
  status                     Show daemon and target connectivity
  tabs                       List all open browser tabs
  switch <targetId>          Switch active tab
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
  --tab <id>, -t <id>        Target a specific browser tab ID
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
	if cmd.Global.Session != "" || cmd.Global.TimeoutMs > 0 || cmd.Global.Tab != "" {
		params = injectSessionAndTimeout(params, cmd.Global.Session, cmd.Global.TimeoutMs, cmd.Global.Tab)
	}

	resp, err := client.Call(ctx, cmd.Method, params)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	if cmd.Name == "screenshot" && cmd.ScreenshotPath != "" {
		var res protocol.ScreenshotResult
		_ = resp.UnmarshalResult(&res)
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
	if cmd.Global.JSON {
		var out bytes.Buffer
		if err := json.Indent(&out, resp.Result, "", "  "); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, out.String())
		if cmd.Name == "eval" {
			var res protocol.EvalResult
			_ = resp.UnmarshalResult(&res)
			if res.Error != "" {
				return 1
			}
		}
		return 0
	}

	switch cmd.Name {
	case "open":
		var res protocol.OpenResult
		_ = resp.UnmarshalResult(&res)
		fmt.Fprint(stdout, FormatOpen(&res))

	case "snapshot":
		var res protocol.SnapshotResult
		_ = resp.UnmarshalResult(&res)
		fmt.Fprint(stdout, FormatSnapshot(&res))

	case "click", "dblclick", "fill", "type", "press", "hover", "focus", "wait", "close", "scroll":
		var res protocol.ActionResult
		_ = resp.UnmarshalResult(&res)
		fmt.Fprint(stdout, FormatAction(&res))

	case "eval":
		var res protocol.EvalResult
		_ = resp.UnmarshalResult(&res)
		out := FormatEval(&res)
		if res.Error != "" {
			fmt.Fprint(stderr, out)
			return 1
		}
		fmt.Fprint(stdout, out)

	case "screenshot":
		var res protocol.ScreenshotResult
		_ = resp.UnmarshalResult(&res)
		fmt.Fprint(stdout, FormatScreenshot(&res, cmd.ScreenshotPath))

	case "status":
		var res protocol.StatusResult
		_ = resp.UnmarshalResult(&res)
		fmt.Fprint(stdout, FormatStatus(&res))
	case "tabs":
		var res protocol.TabListResult
		_ = resp.UnmarshalResult(&res)
		fmt.Fprint(stdout, FormatTabList(&res))

	case "switch", "tab":
		targetID := ""
		if p, ok := cmd.Params.(protocol.TabSwitchParams); ok {
			targetID = string(p.TargetID)
		}
		fmt.Fprintf(stdout, "Switched active tab to %s\n", targetID)

	case "review":
		fmt.Fprint(stdout, FormatReview(cmd.BrokerSubcmd, resp))

	default:
		fmt.Fprintf(stderr, "Unknown command: %s\n", cmd.Name)
		return 1
	}

	return 0
}

func injectSessionAndTimeout(params any, session string, timeoutMs int, tab string) any {
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
	if tab != "" {
		m["targetId"] = tab
	}
	return m
}
