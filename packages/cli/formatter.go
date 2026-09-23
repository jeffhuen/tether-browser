package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

// escapeAXField strips newlines, tabs, and control characters from page-controlled
// fields (Role, Checked) so an attacker cannot forge tree rows into terminal output.
func escapeAXField(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return r < 0x20 }) == -1 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\r' || r == '\n' || r == '\t' || r < 0x20 {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

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

		sb.WriteString(escapeAXField(n.Role))

		if n.Name != "" {
			sb.WriteString(fmt.Sprintf(" %q", n.Name))
		}
		if n.Value != "" {
			sb.WriteString(fmt.Sprintf(" value=%q", n.Value))
		}
		if n.Checked != "" {
			sb.WriteString(fmt.Sprintf(" checked=%s", escapeAXField(n.Checked)))
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

// FormatSnapshot formats a SnapshotResult as indented terminal text.
func FormatSnapshot(res *protocol.SnapshotResult) string {
	treeText := FormatAXTree(res.Nodes, 0)
	if treeText == "" {
		treeText = "(empty accessibility tree)\n"
	}

	return treeText
}

// FormatOpen formats an OpenResult as human-readable text.
func FormatOpen(res *protocol.OpenResult) string {
	if res.Title != "" {
		return fmt.Sprintf("Opened %s (%s) [target: %s]\n", res.URL, res.Title, res.TargetID)
	}
	return fmt.Sprintf("Opened %s [target: %s]\n", res.URL, res.TargetID)
}

// FormatAction formats an ActionResult as human-readable confirmation.
func FormatAction(res *protocol.ActionResult) string {
	if res.OK {
		return "ok\n"
	}
	return "failed\n"
}

// FormatEval formats an EvalResult as evaluated expression string.
func FormatEval(res *protocol.EvalResult) string {
	if res.Error != "" {
		return fmt.Sprintf("eval error: %s\n", res.Error)
	}
	switch v := res.Value.(type) {
	case string:
		return fmt.Sprintf("%s\n", v)
	case nil:
		return "null\n"
	default:
		data, _ := json.Marshal(v)
		return fmt.Sprintf("%s\n", string(data))
	}
}

// FormatScreenshot formats a ScreenshotResult as a confirmation note.
func FormatScreenshot(res *protocol.ScreenshotResult, path string) string {
	if path != "" {
		return fmt.Sprintf("Screenshot saved to %s (%dx%d %s)\n", path, res.Width, res.Height, res.Format)
	}
	return fmt.Sprintf("Screenshot captured (%dx%d %s, %d bytes base64)\n", res.Width, res.Height, res.Format, len(res.Base64))
}

// FormatStatus formats a StatusResult as tabular status information.
func FormatStatus(res *protocol.StatusResult) string {
	statusStr := "connected"
	if !res.Connected {
		statusStr = "disconnected"
	}
	return fmt.Sprintf("Daemon Status: %s\nVersion: %s\nMode: %s\nTargets: %d\nActive Target: %s\nUptime: %ds\n",
		statusStr, res.Version, res.Mode, res.TargetCount, res.ActiveTargetID, res.DaemonUptimeS)
}

// FormatReview formats the response of a review subcommand.
func FormatReview(subcmd string, resp *protocol.Response) string {
	switch subcmd {
	case "start":
		return "Tether Review activated in browser. Move cursor to inspect elements, click to add notes.\n"
	case "clear":
		return "Tether Review notes cleared.\n"
	case "list":
		var res protocol.ReviewListResult
		_ = resp.UnmarshalResult(&res)
		if len(res.Notes) == 0 {
			return "No active review notes. Run 'tether review start' to begin.\n"
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Active Review Notes (%d):\n", len(res.Notes)))
		for _, n := range res.Notes {
			target := n.Payload.Target
			sb.WriteString(fmt.Sprintf("[%d] %s (%s): %s\n", n.Index, target.TagName, target.Selector, n.Comment))
		}
		return sb.String()
	case "send":
		var res protocol.ReviewSendResult
		_ = resp.UnmarshalResult(&res)
		if res.Markdown == "" {
			return "No review notes to send.\n"
		}
		return res.Markdown + "\n"
	default:
		return string(resp.Result) + "\n"
	}
}

// FormatTabList formats a TabListResult as a numbered list of open browser tabs.
func FormatTabList(res *protocol.TabListResult) string {
	if len(res.Tabs) == 0 {
		return "No open tabs found.\n"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Open Tabs (%d):\n", len(res.Tabs)))
	for i, t := range res.Tabs {
		marker := " "
		if t.Active || t.ID == res.ActiveID {
			marker = "*"
		}
		title := t.Title
		if title == "" {
			title = "Untitled"
		}
		sb.WriteString(fmt.Sprintf(" %s [%d] %q\n     URL: %s\n     ID:  %s\n", marker, i+1, title, t.URL, t.ID))
	}
	return sb.String()
}
