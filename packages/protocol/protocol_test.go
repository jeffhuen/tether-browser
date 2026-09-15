package protocol

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func TestSnapshotTreeHashAndFormatting(t *testing.T) {
	nodes := []*AXNode{
		{
			Ref:           "@e1",
			BackendNodeID: 101,
			Role:          "main",
			Children: []*AXNode{
				{
					Ref:           "@e2",
					BackendNodeID: 102,
					Role:          "button",
					Name:          "Submit Order",
					IsInteractive: true,
					Disabled:      false,
					Rect:          &Rect{X: 100, Y: 200, Width: 150, Height: 44},
				},
				{
					Ref:           "@e3",
					BackendNodeID: 103,
					Role:          "textbox",
					Name:          "Email",
					Value:         "user@example.com",
					IsInteractive: true,
					Focused:       true,
				},
			},
		},
	}

	hash1 := ComputeTreeHash(nodes)
	if hash1 == "" {
		t.Fatalf("expected non-empty hash")
	}

	// Determinism check
	hash2 := ComputeTreeHash(nodes)
	if hash1 != hash2 {
		t.Errorf("expected deterministic hash: %s != %s", hash1, hash2)
	}

	// Checked state change must change hash
	nodes[0].Children[0].Checked = "true"
	hashChecked := ComputeTreeHash(nodes)
	if hash1 == hashChecked {
		t.Errorf("expected Checked state mutation to change hash")
	}
	nodes[0].Children[0].Checked = ""

	// Focus state change must change hash
	nodes[0].Children[1].Focused = false
	hashFocus := ComputeTreeHash(nodes)
	if hash1 == hashFocus {
		t.Errorf("expected Focused state mutation to change hash")
	}
	nodes[0].Children[1].Focused = true

	// Selected state change must change hash
	nodes[0].Children[0].Selected = true
	hashSelected := ComputeTreeHash(nodes)
	if hash1 == hashSelected {
		t.Errorf("expected Selected state mutation to change hash")
	}
	nodes[0].Children[0].Selected = false

	// Expanded state change must change hash
	nodes[0].Children[0].Expanded = true
	hashExpanded := ComputeTreeHash(nodes)
	if hash1 == hashExpanded {
		t.Errorf("expected Expanded state mutation to change hash")
	}
	nodes[0].Children[0].Expanded = false

	// Delimiter injection test: Name="a:b", Value="c" vs Name="a", Value="b:c"
	nodeA := []*AXNode{{Role: "item", Name: "a:b", Value: "c"}}
	nodeB := []*AXNode{{Role: "item", Name: "a", Value: "b:c"}}
	if ComputeTreeHash(nodeA) == ComputeTreeHash(nodeB) {
		t.Errorf("length-prefixed fields should prevent delimiter collision")
	}

	// Structural boundary test: tree vs forest
	// Tree: parent has child
	tree := []*AXNode{{Role: "root", Children: []*AXNode{{Role: "child"}}}}
	// Forest: two sibling roots
	forest := []*AXNode{{Role: "root"}, {Role: "child"}}
	if ComputeTreeHash(tree) == ComputeTreeHash(forest) {
		t.Errorf("structural delimiters should prevent tree vs forest collision")
	}

	// Compact text formatting checks
	nodes[0].Children[0].Selected = true
	nodes[0].Children[0].Expanded = true
	formatted := FormatCompactText(nodes, 0)
	if !strings.Contains(formatted, "(selected)") {
		t.Errorf("expected formatted output to contain (selected), got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "(expanded)") {
		t.Errorf("expected formatted output to contain (expanded), got:\n%s", formatted)
	}
}

func TestCompressionRoundtrip(t *testing.T) {
	// 1. Small payload under threshold (raw)
	small := []byte("hello world")
	compressedSmall, err := CompressPayload(small)
	if err != nil {
		t.Fatalf("compress small: %v", err)
	}
	if compressedSmall[0] != FormatRaw {
		t.Errorf("expected FormatRaw, got 0x%02x", compressedSmall[0])
	}
	decompressedSmall, err := DecompressPayload(compressedSmall)
	if err != nil {
		t.Fatalf("decompress small: %v", err)
	}
	if string(decompressedSmall) != string(small) {
		t.Errorf("expected %s, got %s", small, decompressedSmall)
	}

	// 2. Large repetitive payload (zstd)
	large := []byte(strings.Repeat(`{"role":"button","backendNodeId":1234,"name":"Submit Order"},`, 100))
	compressedLarge, err := CompressPayload(large)
	if err != nil {
		t.Fatalf("compress large: %v", err)
	}
	if compressedLarge[0] != FormatZstd {
		t.Errorf("expected FormatZstd, got 0x%02x", compressedLarge[0])
	}
	if len(compressedLarge) >= len(large) {
		t.Errorf("expected compressed size (%d) < original size (%d)", len(compressedLarge), len(large))
	}

	decompressedLarge, err := DecompressPayload(compressedLarge)
	if err != nil {
		t.Fatalf("decompress large: %v", err)
	}
	if string(decompressedLarge) != string(large) {
		t.Errorf("decompressed content mismatch")
	}
}

func TestOversizedFrameRejection(t *testing.T) {
	// Malicious frame advertising 4 GB uncompressed size
	malicious := make([]byte, 5)
	malicious[0] = FormatZstd
	binary.BigEndian.PutUint32(malicious[1:5], 0xffffffff)

	_, err := DecompressPayload(malicious)
	if err == nil {
		t.Fatalf("expected error for oversized payload, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum allowed limit") {
		t.Errorf("expected ErrPayloadTooLarge, got: %v", err)
	}
}

func TestDesignFeedbackReportFormatting(t *testing.T) {
	// Snippet containing triple backticks
	trickyHTML := "<pre>```\ncode block\n```</pre>"

	notes := []*ReviewNote{
		nil, // Nil entry should be safely ignored
		{
			ID:        "note-3",
			Index:     3, // Pin 3 explicitly
			Intent:    "design_fix",
			Comment:   "Button padding is too narrow on mobile.",
			CreatedAt: time.Now(),
			Payload: &ReviewPayload{
				Page: PageInfo{
					SanitizedURL:   "http://localhost:3000/orders/42",
					Title:          "Orders",
					ViewportWidth:  1440,
					ViewportHeight: 900,
				},
				Target: TargetInfo{
					TagName:        "button",
					Role:           "button",
					AccessibleName: "Confirm Order",
					Selector:       "form#checkout-form button.btn-submit",
					ElementPath:    "main > form#checkout-form > div.actions > button",
					TextSnippet:    "Confirm Order",
					CSSClasses:     "btn btn-submit rounded-lg",
					RectViewport:   Rect{X: 540, Y: 720, Width: 200, Height: 48},
					HTMLSnippet:    trickyHTML,
					ComputedStyles: map[string]string{
						"display":          "flex",
						"background-color": "rgb(37, 99, 235)",
					},
					Framework: FrameworkInfo{
						Name:           "Elixir Phoenix LiveView",
						Component:      "OrderLive",
						SourceLocation: "lib/my_app_web/live/order_live.html.heex:42",
						Provenance:     "exact",
					},
				},
				NearbyText: []string{
					"Total: $49.00",
					"Free shipping included",
				},
			},
		},
	}

	report := FormatDesignFeedbackReport(notes, "http://localhost:3000/orders/42", "1440x900")

	// Heading must preserve pin index 3
	if !strings.Contains(report, "### 3. OrderLive button \"Confirm Order\"") {
		t.Errorf("heading should preserve pin index 3, got:\n%s", report)
	}

	// Code block must use quadruple backticks to avoid premature closure by triple backticks in snippet
	if !strings.Contains(report, "````html\n<pre>```\ncode block\n```</pre>\n````") {
		t.Errorf("expected 4-backtick safe code block, got:\n%s", report)
	}
}
