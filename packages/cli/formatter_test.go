package main

import (
	"encoding/json"
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

func TestFormatSnapshotJSONAndText(t *testing.T) {
	snap := &protocol.SnapshotResult{
		Generation: 1,
		RootHash:   "abc12345",
		TargetURL:  "http://localhost:3000/orders",
		Title:      "Orders App",
		RefTable: map[string]int64{
			"@e1": 101,
			"@e2": 102,
		},
		Nodes: []*protocol.AXNode{
			{
				Ref:  "@e1",
				Role: "button",
				Name: "Place Order",
			},
		},
	}

	// 1. Text format
	textOut, err := FormatSnapshot(snap, false)
	if err != nil {
		t.Fatalf("unexpected error formatting text: %v", err)
	}
	if !strings.Contains(textOut, "[@e1] button \"Place Order\"") {
		t.Errorf("expected text output to contain ref badge [@e1], got:\n%s", textOut)
	}

	// 2. JSON format
	jsonOut, err := FormatSnapshot(snap, true)
	if err != nil {
		t.Fatalf("unexpected error formatting json: %v", err)
	}

	var parsed protocol.SnapshotResult
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v", err)
	}
	if parsed.TargetURL != snap.TargetURL {
		t.Errorf("expected TargetURL %q, got %q", snap.TargetURL, parsed.TargetURL)
	}
	if len(parsed.Nodes) != 1 || parsed.Nodes[0].Ref != "@e1" {
		t.Errorf("expected nodes with @e1, got %+v", parsed.Nodes)
	}
}

func TestFormatSnapshotEmpty(t *testing.T) {
	snap := &protocol.SnapshotResult{
		Nodes: []*protocol.AXNode{},
	}
	textOut, err := FormatSnapshot(snap, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(textOut, "(empty accessibility tree)") {
		t.Errorf("expected empty notice, got %q", textOut)
	}
}

func TestFormatOtherCommands(t *testing.T) {
	// Open
	openRes := &protocol.OpenResult{
		TargetID: "target-42",
		URL:      "http://example.com",
		Title:    "Example Domain",
	}
	openText, _ := FormatOpen(openRes, false)
	if !strings.Contains(openText, "Opened http://example.com (Example Domain) [target: target-42]") {
		t.Errorf("unexpected open text: %s", openText)
	}

	// Action
	actionRes := &protocol.ActionResult{OK: true}
	actText, _ := FormatAction(actionRes, false)
	if actText != "ok\n" {
		t.Errorf("expected 'ok\n', got %q", actText)
	}

	// Eval
	evalRes := &protocol.EvalResult{Value: "Page Title"}
	evalText, _ := FormatEval(evalRes, false)
	if evalText != "Page Title\n" {
		t.Errorf("expected 'Page Title\n', got %q", evalText)
	}

	// Status
	statusRes := &protocol.StatusResult{
		Connected:     true,
		Version:       "1.0.0",
		Mode:          "managed",
		TargetCount:   2,
		DaemonUptimeS: 120,
	}
	statusText, _ := FormatStatus(statusRes, false)
	if !strings.Contains(statusText, "Daemon Status: connected") {
		t.Errorf("unexpected status text: %s", statusText)
	}
}
