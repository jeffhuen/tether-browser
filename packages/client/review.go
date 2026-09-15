package client

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

//go:embed review/overlay.js
var reviewOverlayScript string

// ReviewController manages the Tether Review in-page inspector for active tabs.
type ReviewController struct{}

func (rc *ReviewController) getIsolatedContextID(ctx context.Context, client *CDPClient) (int64, error) {
	// Call Page.getFrameTree to find the root frame ID
	treeResp, err := client.Call(ctx, "Page.getFrameTree", nil)
	if err != nil {
		return 0, fmt.Errorf("get frame tree: %w", err)
	}

	var treeOut struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := json.Unmarshal(treeResp, &treeOut); err != nil {
		return 0, fmt.Errorf("unmarshal frame tree: %w", err)
	}
	frameID := treeOut.FrameTree.Frame.ID
	if frameID == "" {
		return 0, errors.New("main frame ID not found")
	}

	// Call Page.createIsolatedWorld to get execution context ID for tether-review world
	createCall := map[string]any{
		"frameId":             frameID,
		"worldName":           "tether-review",
		"grantUniveralAccess": true,
	}
	createResp, err := client.Call(ctx, "Page.createIsolatedWorld", createCall)
	if err != nil {
		return 0, fmt.Errorf("create isolated world: %w", err)
	}

	var createOut struct {
		ExecutionContextID int64 `json:"executionContextId"`
	}
	if err := json.Unmarshal(createResp, &createOut); err != nil {
		return 0, fmt.Errorf("unmarshal execution context id: %w", err)
	}

	return createOut.ExecutionContextID, nil
}

func (rc *ReviewController) evalInIsolatedWorld(ctx context.Context, client *CDPClient, expression string) (json.RawMessage, error) {
	contextID, err := rc.getIsolatedContextID(ctx, client)
	if err != nil {
		return nil, err
	}

	call := map[string]any{
		"expression":    expression,
		"contextId":     contextID,
		"returnByValue": true,
		"awaitPromise":  true,
		"userGesture":   true,
	}
	resp, err := client.Call(ctx, "Runtime.evaluate", call)
	if err != nil {
		return nil, err
	}

	var out struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, err
	}
	if out.ExceptionDetails != nil {
		desc := out.ExceptionDetails.Exception.Description
		if desc == "" {
			desc = out.ExceptionDetails.Text
		}
		return nil, fmt.Errorf("javascript error: %s", desc)
	}
	return out.Result.Value, nil
}

// InjectReviewScript injects the review overlay script into the isolated world context.
func (rc *ReviewController) InjectReviewScript(ctx context.Context, client *CDPClient) error {
	_, err := rc.evalInIsolatedWorld(ctx, client, reviewOverlayScript)
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
	_, err := rc.evalInIsolatedWorld(ctx, client, "window.__tetherReview ? window.__tetherReview.start() : false")
	return err
}

// GetNotes retrieves the captured review notes and element context from the isolated world.
func (rc *ReviewController) GetNotes(ctx context.Context, client *CDPClient) ([]*protocol.ReviewNote, error) {
	val, err := rc.evalInIsolatedWorld(ctx, client, "window.__tetherReview ? window.__tetherReview.getNotes() : []")
	if err != nil {
		return nil, fmt.Errorf("call getNotes: %w", err)
	}

	var notes []*protocol.ReviewNote
	if len(val) > 0 && string(val) != "null" {
		if err := json.Unmarshal(val, &notes); err != nil {
			return nil, fmt.Errorf("unmarshal review notes: %w", err)
		}
	}
	return notes, nil
}

// Clear removes all pinned notes and badges from the page.
func (rc *ReviewController) Clear(ctx context.Context, client *CDPClient) error {
	_, err := rc.evalInIsolatedWorld(ctx, client, "window.__tetherReview ? window.__tetherReview.clear() : true")
	return err
}
