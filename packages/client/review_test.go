package client

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestReviewOverlayCopiesMatch(t *testing.T) {
	extensionScript, err := os.ReadFile("../extension/review/overlay.js")
	if err != nil {
		t.Fatal(err)
	}
	if string(extensionScript) != reviewOverlayScript {
		t.Fatal("review overlay copies differ; run: cp packages/extension/review/overlay.js packages/client/review/")
	}
}

func TestMultiFrameworkFeedbackReport(t *testing.T) {
	notes := []*protocol.ReviewNote{
		{
			ID:        "note-1",
			Index:     1,
			Intent:    "design_fix",
			Comment:   "Button padding too narrow on mobile.",
			CreatedAt: time.Now(),
			Payload: &protocol.ReviewPayload{
				Page: protocol.PageInfo{
					SanitizedURL:  "http://localhost:3000/orders/42",
					Title:         "Orders",
					ViewportWidth: 1440,
				},
				Target: protocol.TargetInfo{
					TagName:        "button",
					Role:           "button",
					AccessibleName: "Confirm Order",
					Selector:       "form#checkout-form button.btn-submit",
					ElementPath:    "main > form#checkout-form > div.actions > button",
					TextSnippet:    "Confirm Order",
					CSSClasses:     "btn btn-submit rounded-lg",
					RectViewport:   protocol.Rect{X: 540, Y: 720, Width: 200, Height: 48},
					HTMLSnippet:    `<button class="btn btn-submit" phx-click="confirm_order">Confirm Order</button>`,
					ComputedStyles: map[string]string{
						"display":          "flex",
						"background-color": "rgb(37, 99, 235)",
					},
					Framework: protocol.FrameworkInfo{
						Name:           "Elixir Phoenix LiveView",
						Component:      "OrderLive",
						SourceLocation: "lib/my_app_web/live/order_live.html.heex:42",
						Provenance:     "exact",
					},
				},
			},
		},
		{
			ID:        "note-2",
			Index:     2,
			Intent:    "bug",
			Comment:   "Astro island cart count desyncs on click.",
			CreatedAt: time.Now(),
			Payload: &protocol.ReviewPayload{
				Page: protocol.PageInfo{
					SanitizedURL:  "http://localhost:3000/shop",
					Title:         "Shop",
					ViewportWidth: 1440,
				},
				Target: protocol.TargetInfo{
					TagName:        "div",
					Role:           "region",
					AccessibleName: "Cart Island",
					Selector:       "astro-island#cart",
					ElementPath:    "header > nav > astro-island#cart",
					RectViewport:   protocol.Rect{X: 1200, Y: 20, Width: 120, Height: 40},
					Framework: protocol.FrameworkInfo{
						Name:           "Astro",
						Component:      "CartIcon",
						SourceLocation: "src/components/CartIcon.svelte",
						Provenance:     "exact",
					},
				},
			},
		},
	}

	report := protocol.FormatDesignFeedbackReport(notes, "http://localhost:3000/orders/42", "1440x900")

	expectedLines := []string{
		"## Design Feedback: http://localhost:3000/orders/42",
		"### 1. OrderLive button \"Confirm Order\"",
		"**Framework:** Elixir Phoenix LiveView (OrderLive)",
		"**Source:** lib/my_app_web/live/order_live.html.heex:42 (provenance: exact)",
		"**Feedback:** Button padding too narrow on mobile.",
		"### 2. CartIcon div (region) \"Cart Island\"",
		"**Framework:** Astro (CartIcon)",
		"**Source:** src/components/CartIcon.svelte (provenance: exact)",
		"**Feedback:** Astro island cart count desyncs on click.",
	}

	for _, line := range expectedLines {
		if !strings.Contains(report, line) {
			t.Errorf("report missing expected line: %q\nFull report:\n%s", line, report)
		}
	}
}
