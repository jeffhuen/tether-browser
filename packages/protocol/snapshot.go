package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Rect represents a bounding rectangle in CSS pixels.
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// AXNode represents a node in the accessibility tree with action ref tags.
type AXNode struct {
	Ref             string    `json:"ref,omitempty"` // Compact ref tag, e.g. "@e1"
	BackendNodeID   int64     `json:"backendNodeId,omitempty"`
	Role            string    `json:"role"`
	Name            string    `json:"name,omitempty"`
	Value           string    `json:"value,omitempty"`
	Description     string    `json:"description,omitempty"`
	Disabled        bool      `json:"disabled,omitempty"`
	Focused         bool      `json:"focused,omitempty"`
	Checked         string    `json:"checked,omitempty"` // "true", "false", "mixed"
	Selected        bool      `json:"selected,omitempty"`
	Expanded        bool      `json:"expanded,omitempty"`
	Rect            *Rect     `json:"rect,omitempty"`
	Children        []*AXNode `json:"children,omitempty"`
	ParentRef       string    `json:"parentRef,omitempty"`
	IsInteractive   bool      `json:"isInteractive,omitempty"`
}

// SnapshotParams specifies parameters for generating an accessibility snapshot.
type SnapshotParams struct {
	TargetID        TargetID `json:"targetId,omitempty"`
	InteractiveOnly bool     `json:"interactiveOnly,omitempty"` // Filter to interactive elements only (-i)
	Compact         bool     `json:"compact,omitempty"`         // Prune empty structural elements (-c)
	MaxDepth        int      `json:"maxDepth,omitempty"`        // Maximum tree depth (-d)
	Selector        string   `json:"selector,omitempty"`        // Scope tree to a specific selector (-s)
	LastGeneration  uint64   `json:"lastGeneration,omitempty"`  // Previous generation for diffing
}

// SnapshotResult contains the serialized tree and reference lookup table.
type SnapshotResult struct {
	Generation uint64            `json:"generation"`
	RootHash   string            `json:"rootHash"`
	Modified   bool              `json:"modified"`
	Nodes      []*AXNode         `json:"nodes"`
	RefTable   map[string]int64  `json:"refTable"` // Maps "@e1" -> BackendNodeID
	TargetURL  string            `json:"targetUrl"`
	Title      string            `json:"title"`
}

// ComputeTreeHash generates a deterministic SHA-256 hash of the tree structure.
func ComputeTreeHash(nodes []*AXNode) string {
	h := sha256.New()
	var walk func(n *AXNode)
	walk = func(n *AXNode) {
		if n == nil {
			return
		}
		fmt.Fprintf(h, "%s:%s:%s:%s:%t;", n.Ref, n.Role, n.Name, n.Value, n.Disabled)
		for _, child := range n.Children {
			walk(child)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// FormatCompactText formats an accessibility tree into agent-readable indented text.
func FormatCompactText(nodes []*AXNode, indent int) string {
	var sb strings.Builder
	prefix := strings.Repeat("  ", indent)

	for _, n := range nodes {
		if n == nil {
			continue
		}
		sb.WriteString(prefix)
		if n.Ref != "" {
			sb.WriteString(fmt.Sprintf("[%s] ", n.Ref))
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
		sb.WriteString("\n")

		if len(n.Children) > 0 {
			sb.WriteString(FormatCompactText(n.Children, indent+1))
		}
	}
	return sb.String()
}
