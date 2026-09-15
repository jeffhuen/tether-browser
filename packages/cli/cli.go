package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

// GlobalFlags holds flags that apply across commands.
type GlobalFlags struct {
	JSON      bool
	TimeoutMs int
	Session   string
}

// Command represents a parsed CLI command ready for execution.
type Command struct {
	Name           string
	Method         string
	Params         any
	Global         GlobalFlags
	ScreenshotPath string
	BrokerSubcmd   string
	RawArgs        []string
}

// ParseArgs parses command line arguments into a structured Command.
func ParseArgs(args []string) (*Command, error) {
	if len(args) == 0 {
		return nil, errors.New("no command specified")
	}

	var global GlobalFlags
	var cmdTokens []string

	// Extract global flags and command tokens
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			global.JSON = true
		case arg == "--timeout":
			if i+1 >= len(args) {
				return nil, errors.New("flag --timeout requires an argument")
			}
			i++
			ms, err := strconv.Atoi(args[i])
			if err != nil {
				return nil, fmt.Errorf("invalid timeout value %q: %w", args[i], err)
			}
			global.TimeoutMs = ms
		case strings.HasPrefix(arg, "--timeout="):
			val := strings.TrimPrefix(arg, "--timeout=")
			ms, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid timeout value %q: %w", val, err)
			}
			global.TimeoutMs = ms
		case arg == "--session":
			if i+1 >= len(args) {
				return nil, errors.New("flag --session requires an argument")
			}
			i++
			global.Session = args[i]
		case strings.HasPrefix(arg, "--session="):
			global.Session = strings.TrimPrefix(arg, "--session=")
		default:
			cmdTokens = append(cmdTokens, arg)
		}
	}

	if len(cmdTokens) == 0 {
		return nil, errors.New("no command specified")
	}

	cmdName := cmdTokens[0]
	cmdArgs := cmdTokens[1:]

	cmd := &Command{
		Name:    cmdName,
		Global:  global,
		RawArgs: cmdTokens,
	}

	switch cmdName {
	case "open":
		if len(cmdArgs) < 1 {
			return nil, errors.New("command 'open' requires a url")
		}
		cmd.Method = protocol.MethodOpen
		cmd.Params = protocol.OpenParams{
			URL:       cmdArgs[0],
			TimeoutMs: global.TimeoutMs,
		}

	case "snapshot":
		params := protocol.SnapshotParams{}
		for i := 0; i < len(cmdArgs); i++ {
			switch cmdArgs[i] {
			case "-i", "--interactive":
				params.InteractiveOnly = true
			case "-c", "--compact":
				params.Compact = true
			case "-d", "--depth":
				if i+1 >= len(cmdArgs) {
					return nil, errors.New("flag -d requires a depth value")
				}
				i++
				depth, err := strconv.Atoi(cmdArgs[i])
				if err != nil {
					return nil, fmt.Errorf("invalid depth value %q: %w", cmdArgs[i], err)
				}
				params.MaxDepth = depth
			case "-s", "--selector":
				if i+1 >= len(cmdArgs) {
					return nil, errors.New("flag -s requires a selector value")
				}
				i++
				params.Selector = cmdArgs[i]
			default:
				if strings.HasPrefix(cmdArgs[i], "-") {
					return nil, fmt.Errorf("unknown snapshot flag: %s", cmdArgs[i])
				}
			}
		}
		cmd.Method = protocol.MethodSnapshot
		cmd.Params = params

	case "click":
		if len(cmdArgs) < 1 {
			return nil, errors.New("command 'click' requires a selector")
		}
		cmd.Method = protocol.MethodClick
		cmd.Params = protocol.ClickParams{
			Selector:  cmdArgs[0],
			TimeoutMs: global.TimeoutMs,
		}

	case "dblclick":
		if len(cmdArgs) < 1 {
			return nil, errors.New("command 'dblclick' requires a selector")
		}
		cmd.Method = protocol.MethodDblClick
		cmd.Params = protocol.ClickParams{
			Selector:   cmdArgs[0],
			ClickCount: 2,
			TimeoutMs:  global.TimeoutMs,
		}

	case "fill":
		if len(cmdArgs) < 2 {
			return nil, errors.New("command 'fill' requires a selector and text")
		}
		cmd.Method = protocol.MethodFill
		cmd.Params = protocol.FillParams{
			Selector:   cmdArgs[0],
			Text:       strings.Join(cmdArgs[1:], " "),
			ClearFirst: true,
			TimeoutMs:  global.TimeoutMs,
		}

	case "type":
		if len(cmdArgs) < 2 {
			return nil, errors.New("command 'type' requires a selector and text")
		}
		cmd.Method = protocol.MethodType
		cmd.Params = protocol.TypeParams{
			Selector:  cmdArgs[0],
			Text:      strings.Join(cmdArgs[1:], " "),
			TimeoutMs: global.TimeoutMs,
		}

	case "press":
		if len(cmdArgs) < 1 {
			return nil, errors.New("command 'press' requires a key")
		}
		cmd.Method = protocol.MethodPress
		cmd.Params = protocol.PressParams{
			Key: cmdArgs[0],
		}

	case "hover":
		if len(cmdArgs) < 1 {
			return nil, errors.New("command 'hover' requires a selector")
		}
		cmd.Method = protocol.MethodHover
		cmd.Params = protocol.HoverParams{
			Selector:  cmdArgs[0],
			TimeoutMs: global.TimeoutMs,
		}

	case "focus":
		if len(cmdArgs) < 1 {
			return nil, errors.New("command 'focus' requires a selector")
		}
		cmd.Method = protocol.MethodFocus
		cmd.Params = protocol.FocusParams{
			Selector: cmdArgs[0],
		}

	case "eval":
		if len(cmdArgs) < 1 {
			return nil, errors.New("command 'eval' requires an expression")
		}
		cmd.Method = protocol.MethodEval
		cmd.Params = protocol.EvalParams{
			Expression:   strings.Join(cmdArgs, " "),
			AwaitPromise: true,
		}

	case "wait":
		if len(cmdArgs) < 1 {
			return nil, errors.New("command 'wait' requires a selector or duration")
		}
		arg := cmdArgs[0]
		params := protocol.WaitParams{
			TimeoutMs: global.TimeoutMs,
		}
		trimmed := strings.TrimSuffix(arg, "ms")
		if ms, err := strconv.Atoi(trimmed); err == nil {
			params.DurationMs = ms
		} else {
			params.Selector = arg
			params.State = "visible"
		}
		cmd.Method = protocol.MethodWait
		cmd.Params = params

	case "screenshot":
		cmd.Method = protocol.MethodScreenshot
		cmd.Params = protocol.ScreenshotParams{
			TimeoutMs: global.TimeoutMs,
		}
		if len(cmdArgs) > 0 && !strings.HasPrefix(cmdArgs[0], "-") {
			cmd.ScreenshotPath = cmdArgs[0]
		}

	case "close":
		closeAll := false
		for _, arg := range cmdArgs {
			if arg == "--all" {
				closeAll = true
			}
		}
		cmd.Method = protocol.MethodClose
		cmd.Params = protocol.CloseParams{
			CloseAll: closeAll,
		}

	case "status":
		cmd.Method = protocol.MethodStatus
		cmd.Params = protocol.StatusParams{}

	case "broker":
		subcmd := "status"
		if len(cmdArgs) > 0 {
			subcmd = cmdArgs[0]
		}
		cmd.BrokerSubcmd = subcmd

	default:
		return nil, fmt.Errorf("unknown command %q", cmdName)
	}

	return cmd, nil
}
