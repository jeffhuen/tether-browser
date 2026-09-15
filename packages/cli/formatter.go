package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

// FormatAXTree formats an accessibility node slice into indented terminal text with [@eN] badges.
func FormatAXTree(nodes []*protocol.AXNode, indent int) string {
	var sb strings.Builder
	prefix := strings.Repeat("  ", indent)

	for _, n := range nodes {
		if n == nil {
			continue
		}
		sb.WriteString(prefix)

		if n.Ref != "" {
			ref := n.Ref
			if !strings.HasPrefix(ref, "@") {
				ref = "@" + ref
			}
			sb.WriteString(fmt.Sprintf("[%s] ", ref))
		}

		sb.WriteString(n.Role)

		if n.Name != "" {
			sb.WriteString(fmt.Sprintf(" %q", n.Name))
		}
		if n.Value != "" {
			sb.WriteString(fmt.Sprintf(" value=%q", n.Value))
		}
		if n.Checked != "" {
			sb.WriteString(fmt.Sprintf(" checked=%s", n.Checked))
		}
		if n.Disabled {
			sb.WriteString(" (disabled)")
		}
		if n.Focused {
			sb.WriteString(" (focused)")
		}
		if n.Selected {
			sb.WriteString(" (selected)")
		}
		if n.Expanded {
			sb.WriteString(" (expanded)")
		}
		sb.WriteString("\n")

		if len(n.Children) > 0 {
			sb.WriteString(FormatAXTree(n.Children, indent+1))
		}
	}

	return sb.String()
}

// FormatSnapshot formats a SnapshotResult as either raw JSON or indented terminal text.
func FormatSnapshot(res *protocol.SnapshotResult, jsonOutput bool) (string, error) {
	if res == nil {
		return "", nil
	}

	if jsonOutput {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal snapshot json: %w", err)
		}
		return string(data), nil
	}

	treeText := FormatAXTree(res.Nodes, 0)
	if treeText == "" {
		treeText = "(empty accessibility tree)\n"
	}

	return treeText, nil
}

// FormatOpen formats an OpenResult as either raw JSON or human-readable text.
func FormatOpen(res *protocol.OpenResult, jsonOutput bool) (string, error) {
	if res == nil {
		return "", nil
	}
	if jsonOutput {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal open json: %w", err)
		}
		return string(data), nil
	}
	if res.Title != "" {
		return fmt.Sprintf("Opened %s (%s) [target: %s]\n", res.URL, res.Title, res.TargetID), nil
	}
	return fmt.Sprintf("Opened %s [target: %s]\n", res.URL, res.TargetID), nil
}

// FormatAction formats an ActionResult as either raw JSON or human-readable confirmation.
func FormatAction(res *protocol.ActionResult, jsonOutput bool) (string, error) {
	if res == nil {
		return "ok\n", nil
	}
	if jsonOutput {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal action json: %w", err)
		}
		return string(data), nil
	}
	if res.OK {
		return "ok\n", nil
	}
	return "failed\n", nil
}

// FormatEval formats an EvalResult as either raw JSON or evaluated expression string.
func FormatEval(res *protocol.EvalResult, jsonOutput bool) (string, error) {
	if res == nil {
		return "", nil
	}
	if jsonOutput {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal eval json: %w", err)
		}
		return string(data), nil
	}
	if res.Error != "" {
		return fmt.Sprintf("eval error: %s\n", res.Error), nil
	}
	switch v := res.Value.(type) {
	case string:
		return fmt.Sprintf("%s\n", v), nil
	case nil:
		return "null\n", nil
	default:
		data, _ := json.Marshal(v)
		return fmt.Sprintf("%s\n", string(data)), nil
	}
}

// FormatScreenshot formats a ScreenshotResult as JSON or a confirmation note.
func FormatScreenshot(res *protocol.ScreenshotResult, path string, jsonOutput bool) (string, error) {
	if res == nil {
		return "", nil
	}
	if jsonOutput {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal screenshot json: %w", err)
		}
		return string(data), nil
	}
	if path != "" {
		return fmt.Sprintf("Screenshot saved to %s (%dx%d %s)\n", path, res.Width, res.Height, res.Format), nil
	}
	return fmt.Sprintf("Screenshot captured (%dx%d %s, %d bytes base64)\n", res.Width, res.Height, res.Format, len(res.Base64)), nil
}

// FormatStatus formats a StatusResult as JSON or tabular status information.
func FormatStatus(res *protocol.StatusResult, jsonOutput bool) (string, error) {
	if res == nil {
		return "", nil
	}
	if jsonOutput {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal status json: %w", err)
		}
		return string(data), nil
	}

	statusStr := "connected"
	if !res.Connected {
		statusStr = "disconnected"
	}
	return fmt.Sprintf("Daemon Status: %s\nVersion: %s\nMode: %s\nTargets: %d\nActive Target: %s\nUptime: %ds\n",
		statusStr, res.Version, res.Mode, res.TargetCount, res.ActiveTargetID, res.DaemonUptimeS), nil
}
