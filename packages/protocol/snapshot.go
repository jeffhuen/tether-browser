package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	Ref           string    `json:"ref,omitempty"` // Compact ref tag, e.g. "@e1"
	BackendNodeID int64     `json:"backendNodeId,omitempty"`
	Role          string    `json:"role"`
	Name          string    `json:"name,omitempty"`
	Value         string    `json:"value,omitempty"`
	Description   string    `json:"description,omitempty"`
	Disabled      bool      `json:"disabled,omitempty"`
	Focused       bool      `json:"focused,omitempty"`
	Checked       string    `json:"checked,omitempty"` // "true", "false", "mixed"
	Selected      bool      `json:"selected,omitempty"`
	Expanded      bool      `json:"expanded,omitempty"`
	Rect          *Rect     `json:"rect,omitempty"`
	Children      []*AXNode `json:"children,omitempty"`
	ParentRef     string    `json:"parentRef,omitempty"`
	IsInteractive bool      `json:"isInteractive,omitempty"`
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
	Generation uint64           `json:"generation"`
	RootHash   string           `json:"rootHash"`
	Modified   bool             `json:"modified"`
	Nodes      []*AXNode        `json:"nodes"`
	RefTable   map[string]int64 `json:"refTable"` // Maps "@e1" -> BackendNodeID
	TargetURL  string           `json:"targetUrl"`
	Title      string           `json:"title"`
}

// writeField hashes a length-prefixed string field to prevent delimiter injection.
func writeField(h interface{ Write([]byte) (int, error) }, s string) {
	fmt.Fprintf(h, "%d:%s;", len(s), s)
}

// ComputeTreeHash generates an unambiguous SHA-256 hash of the tree structure and all observable states.
func ComputeTreeHash(nodes []*AXNode) string {
	h := sha256.New()
	var walk func(n *AXNode)
	walk = func(n *AXNode) {
		if n == nil {
			return
		}
		// Write node opening with length-prefixed fields
		h.Write([]byte("N["))
		writeField(h, n.Ref)
		writeField(h, n.Role)
		writeField(h, n.Name)
		writeField(h, n.Value)
		writeField(h, n.Description)
		writeField(h, n.Checked)
		fmt.Fprintf(h, "d:%t;f:%t;s:%t;e:%t;", n.Disabled, n.Focused, n.Selected, n.Expanded)

		// Explicit child structure delimiters prevent tree-forest ambiguity
		h.Write([]byte("C("))
		for _, child := range n.Children {
			walk(child)
		}
		h.Write([]byte(")N]"))
	}

	h.Write([]byte("ROOTS("))
	for _, n := range nodes {
		walk(n)
	}
	h.Write([]byte(")ROOTS"))

	return hex.EncodeToString(h.Sum(nil))[:16]
}
