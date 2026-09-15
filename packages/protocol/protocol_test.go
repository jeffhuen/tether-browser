package protocol

import (
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

	// Recomputing hash on identical tree should yield exact same hash
	hash2 := ComputeTreeHash(nodes)
	if hash1 != hash2 {
		t.Errorf("expected deterministic hash: %s != %s", hash1, hash2)
	}

	// Mutating child should change hash
	nodes[0].Children[0].Disabled = true
	hash3 := ComputeTreeHash(nodes)
	if hash1 == hash3 {
		t.Errorf("expected mutated tree to produce different hash")
	}

	formatted := FormatCompactText(nodes, 0)
	if !strings.Contains(formatted, "[@e2] button \"Submit Order\" (disabled)") {
		t.Errorf("expected formatted output to contain disabled button, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "[@e3] textbox \"Email\" value=\"user@example.com\" (focused)") {
		t.Errorf("expected formatted output to contain focused textbox, got:\n%s", formatted)
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
	if len(large) < CompressionThreshold {
		t.Fatalf("payload should be > threshold")
	}
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

func TestDesignFeedbackReportFormatting(t *testing.T) {
	notes := []*ReviewNote{
		{
			ID:      "note-1",
			Index:   1,
			Intent:  "design_fix",
			Comment: "Button padding is too narrow on mobile.",
			CreatedAt: time.Now(),
			Payload: &ReviewPayload{
				Page: PageInfo{
					SanitizedURL:  "http://localhost:3000/orders/42",
					Title:         "Orders",
					ViewportWidth: 1440,
					ViewportHeight: 900,
				},
				Target: TargetInfo{
					TagName:      "button",
					Selector:     "form#checkout-form button.btn-submit",
					ElementPath:  "main > form#checkout-form > div.actions > button",
					TextSnippet:  "Confirm Order",
					CSSClasses:   "btn btn-submit rounded-lg",
					RectViewport: Rect{X: 540, Y: 720, Width: 200, Height: 48},
					HTMLSnippet:  `<button class="btn btn-submit" phx-click="confirm_order">Confirm Order</button>`,
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

	expectedSubstrings := []string{
		"## Design Feedback: http://localhost:3000/orders/42",
		"### 1. OrderLive button \"Confirm Order\"",
		"**Intent:** design_fix",
		"**Framework:** Elixir Phoenix LiveView (OrderLive)",
		"**Source:** lib/my_app_web/live/order_live.html.heex:42 (provenance: exact)",
		"**Selector:** form#checkout-form button.btn-submit",
		"**Bounds:** x=540, y=720, 200x48",
		"**Classes:** `btn btn-submit rounded-lg`",
		"**Feedback:** Button padding is too narrow on mobile.",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(report, sub) {
			t.Errorf("report missing expected substring %q\nFull report:\n%s", sub, report)
		}
	}
}
