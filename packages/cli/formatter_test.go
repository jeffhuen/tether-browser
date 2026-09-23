package cli

import (
	"strings"
	"testing"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestFormatAXTreeWithRefBadges(t *testing.T) {
	nodes := []*protocol.AXNode{
		{
			Ref:  "@e1",
			Role: "navigation",
			Name: "Primary",
			Children: []*protocol.AXNode{
				{
					Ref:      "@e2",
					Role:     "link",
					Name:     "Home",
					Selected: true,
				},
				{
					Ref:      "e3", // without leading @
					Role:     "button",
					Name:     "Sign In",
					Focused:  true,
					Disabled: false,
				},
			},
		},
		{
			Ref:      "@e4",
			Role:     "checkbox",
			Name:     "Remember me",
			Checked:  "true",
			Disabled: true,
		},
	}

	formatted := FormatAXTree(nodes, 0)

	expectedSubstrings := []string{
		"[@e1] navigation \"Primary\"",
		"  [@e2] link \"Home\" (selected)",
		"  [@e3] button \"Sign In\" (focused)",
		"[@e4] checkbox \"Remember me\" checked=true (disabled)",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(formatted, sub) {
			t.Errorf("expected output to contain %q, but got:\n%s", sub, formatted)
		}
	}
}

func TestFormatSnapshotText(t *testing.T) {
	snap := &protocol.SnapshotResult{
		Nodes: []*protocol.AXNode{
			{
				Ref:  "@e1",
				Role: "button",
				Name: "Place Order",
			},
		},
	}

	textOut := FormatSnapshot(snap)
	if !strings.Contains(textOut, "[@e1] button \"Place Order\"") {
		t.Errorf("expected text output to contain ref badge [@e1], got:\n%s", textOut)
	}
}

