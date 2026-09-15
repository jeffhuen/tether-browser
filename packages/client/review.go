package client

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

//go:embed review/overlay.js
var reviewOverlayScript string

// ReviewController manages the Tether Review in-page inspector for active tabs.
type ReviewController struct{}

// InjectReviewScript injects the review overlay script into the page context.
func (rc *ReviewController) InjectReviewScript(ctx context.Context, client *CDPClient) error {
	call := map[string]any{
		"expression":    reviewOverlayScript,
		"returnByValue": true,
		"worldName":     "tether-review",
	}
	_, err := client.Call(ctx, "Runtime.evaluate", call)
	if err != nil {
		return fmt.Errorf("inject review script: %w", err)
	}
	return nil
}

// Start activates review mode on the active tab.
func (rc *ReviewController) Start(ctx context.Context, client *CDPClient) error {
	if err := rc.InjectReviewScript(ctx, client); err != nil {
		return err
	}
	call := map[string]any{
		"expression":    "window.__tetherReview ? window.__tetherReview.start() : false",
		"returnByValue": true,
		"worldName":     "tether-review",
	}
	_, err := client.Call(ctx, "Runtime.evaluate", call)
	return err
}

// GetNotes retrieves the captured review notes and element context from the page.
func (rc *ReviewController) GetNotes(ctx context.Context, client *CDPClient) ([]*protocol.ReviewNote, error) {
	call := map[string]any{
		"expression":    "window.__tetherReview ? window.__tetherReview.getNotes() : []",
		"returnByValue": true,
		"worldName":     "tether-review",
	}
	resp, err := client.Call(ctx, "Runtime.evaluate", call)
	if err != nil {
		return nil, fmt.Errorf("call getNotes: %w", err)
	}

	var evalOut struct {
		Result struct {
			Value []*protocol.ReviewNote `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &evalOut); err != nil {
		return nil, fmt.Errorf("unmarshal review notes: %w", err)
	}
	return evalOut.Result.Value, nil
}

// Clear removes all pinned notes and badges from the page.
func (rc *ReviewController) Clear(ctx context.Context, client *CDPClient) error {
	call := map[string]any{
		"expression":    "window.__tetherReview ? window.__tetherReview.clear() : true",
		"returnByValue": true,
		"worldName":     "tether-review",
	}
	_, err := client.Call(ctx, "Runtime.evaluate", call)
	return err
}
