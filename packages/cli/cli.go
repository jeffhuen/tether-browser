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
	var optionTokens []string
	var literalTokens []string
	inEndOptions := false

	// Extract global flags and tokens, honoring "--" delimiter
	for i := 0; i < len(args); i++ {
		arg := args[i]

		if !inEndOptions && arg == "--" {
			inEndOptions = true
			continue
		}

		if inEndOptions {
			literalTokens = append(literalTokens, arg)
			continue
		}

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
			optionTokens = append(optionTokens, arg)
		}
	}

	var cmdName string
	var optionsArgs []string
	var literalArgs = literalTokens

	if len(optionTokens) > 0 {
		cmdName = optionTokens[0]
		optionsArgs = optionTokens[1:]
	} else if len(literalTokens) > 0 {
		cmdName = literalTokens[0]
		literalArgs = literalTokens[1:]
	} else {
		return nil, errors.New("no command specified")
	}

	// All positional arguments combined in order
	allArgs := append(append([]string{}, optionsArgs...), literalArgs...)

	cmd := &Command{
		Name:    cmdName,
		Global:  global,
		RawArgs: args,
	}

	switch cmdName {
	case "open":
		if len(allArgs) == 0 {
			return nil, errors.New("usage: tether open <url>")
		}
		cmd.Method = protocol.MethodOpen
		cmd.Params = protocol.OpenParams{
			URL:       allArgs[0],
			TimeoutMs: global.TimeoutMs,
		}

	case "snapshot":
		p := protocol.SnapshotParams{}
		// Flags are scanned only from optionsArgs before the delimiter
		for i := 0; i < len(optionsArgs); i++ {
			a := optionsArgs[i]
			switch a {
			case "-i", "--interactive":
				p.InteractiveOnly = true
			case "-c", "--compact":
				p.Compact = true
			case "-d", "--depth":
				if i+1 >= len(optionsArgs) {
					return nil, fmt.Errorf("flag %s requires an argument", a)
				}
				i++
				depth, err := strconv.Atoi(optionsArgs[i])
				if err != nil {
					return nil, fmt.Errorf("invalid depth value %q: %w", optionsArgs[i], err)
				}
				p.MaxDepth = depth
			case "-s", "--selector":
				if i+1 >= len(optionsArgs) {
					return nil, fmt.Errorf("flag %s requires an argument", a)
				}
				i++
				p.Selector = optionsArgs[i]
			default:
				if strings.HasPrefix(a, "-d=") || strings.HasPrefix(a, "--depth=") {
					val := strings.TrimPrefix(strings.TrimPrefix(a, "--depth="), "-d=")
					depth, err := strconv.Atoi(val)
					if err != nil {
						return nil, fmt.Errorf("invalid depth value %q: %w", val, err)
					}
					p.MaxDepth = depth
				} else if strings.HasPrefix(a, "-s=") || strings.HasPrefix(a, "--selector=") {
					p.Selector = strings.TrimPrefix(strings.TrimPrefix(a, "--selector="), "-s=")
				}
			}
		}
		cmd.Method = protocol.MethodSnapshot
		cmd.Params = p

	case "click":
		if len(allArgs) == 0 {
			return nil, errors.New("usage: tether click <selector>")
		}
		cmd.Method = protocol.MethodClick
		cmd.Params = protocol.ClickParams{
			Selector:  allArgs[0],
			TimeoutMs: global.TimeoutMs,
		}

	case "dblclick":
		if len(allArgs) == 0 {
			return nil, errors.New("usage: tether dblclick <selector>")
		}
		cmd.Method = protocol.MethodDblClick
		cmd.Params = protocol.ClickParams{
			Selector:   allArgs[0],
			ClickCount: 2,
			TimeoutMs:  global.TimeoutMs,
		}

	case "fill":
		if len(allArgs) < 2 {
			return nil, errors.New("usage: tether fill <selector> <text>")
		}
		cmd.Method = protocol.MethodFill
		cmd.Params = protocol.FillParams{
			Selector:   allArgs[0],
			Text:       strings.Join(allArgs[1:], " "),
			ClearFirst: true,
			TimeoutMs:  global.TimeoutMs,
		}

	case "type":
		if len(allArgs) < 2 {
			return nil, errors.New("usage: tether type <selector> <text>")
		}
		cmd.Method = protocol.MethodType
		cmd.Params = protocol.TypeParams{
			Selector:  allArgs[0],
			Text:      strings.Join(allArgs[1:], " "),
			TimeoutMs: global.TimeoutMs,
		}

	case "press":
		if len(allArgs) == 0 {
			return nil, errors.New("usage: tether press <key>")
		}
		cmd.Method = protocol.MethodPress
		cmd.Params = protocol.PressParams{
			Key: allArgs[0],
		}

	case "hover":
		if len(allArgs) == 0 {
			return nil, errors.New("usage: tether hover <selector>")
		}
		cmd.Method = protocol.MethodHover
		cmd.Params = protocol.HoverParams{
			Selector:  allArgs[0],
			TimeoutMs: global.TimeoutMs,
		}

	case "focus":
		if len(allArgs) == 0 {
			return nil, errors.New("usage: tether focus <selector>")
		}
		cmd.Method = protocol.MethodFocus
		cmd.Params = protocol.FocusParams{
			Selector: allArgs[0],
		}

	case "eval":
		if len(allArgs) == 0 {
			return nil, errors.New("usage: tether eval <expression>")
		}
		cmd.Method = protocol.MethodEval
		cmd.Params = protocol.EvalParams{
			Expression:   strings.Join(allArgs, " "),
			AwaitPromise: true,
		}

	case "wait":
		if len(allArgs) == 0 {
			return nil, errors.New("usage: tether wait <selector|durationMs>")
		}
		arg := allArgs[0]
		p := protocol.WaitParams{
			TimeoutMs: global.TimeoutMs,
		}
		numStr := strings.TrimSuffix(strings.TrimSuffix(arg, "ms"), "s")
		if ms, err := strconv.Atoi(numStr); err == nil && ms > 0 {
			if strings.HasSuffix(arg, "s") && !strings.HasSuffix(arg, "ms") {
				ms *= 1000
			}
			p.DurationMs = ms
		} else {
			p.Selector = arg
			p.State = "visible"
		}
		cmd.Method = protocol.MethodWait
		cmd.Params = p

	case "screenshot":
		p := protocol.ScreenshotParams{
			Format:    "png",
			TimeoutMs: global.TimeoutMs,
		}
		for _, a := range optionsArgs {
			if !strings.HasPrefix(a, "-") {
				cmd.ScreenshotPath = a
			}
		}
		// Positional literal arguments take precedence for path
		if len(literalArgs) > 0 {
			cmd.ScreenshotPath = literalArgs[0]
		}
		cmd.Method = protocol.MethodScreenshot
		cmd.Params = p

	case "close":
		p := protocol.CloseParams{}
		// Flags are scanned only from optionsArgs before "--"
		for _, a := range optionsArgs {
			if a == "--all" {
				p.CloseAll = true
			}
		}
		cmd.Method = protocol.MethodClose
		cmd.Params = p

	case "status":
		cmd.Method = protocol.MethodStatus
		cmd.Params = protocol.StatusParams{}

	case "broker":
		subcmd := "run"
		if len(allArgs) > 0 {
			subcmd = allArgs[0]
		}
		cmd.BrokerSubcmd = subcmd

	default:
		return nil, fmt.Errorf("unknown command: %q", cmdName)
	}

	return cmd, nil
}
