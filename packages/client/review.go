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
		"frameId":              frameID,
		"worldName":            "tether-review",
		"grantUniversalAccess": true,
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

// StartPassive activates the review session chrome (dock, markers) without
// arming the element selector, so automation clicks pass through untouched.
func (rc *ReviewController) StartPassive(ctx context.Context, client *CDPClient) error {
	if err := rc.InjectReviewScript(ctx, client); err != nil {
		return err
	}
	_, err := rc.evalInIsolatedWorld(ctx, client, "window.__tetherReview ? window.__tetherReview.startPassive() : false")
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

	// Enrich framework metadata from main world for components that weren't detected via standard DOM attributes
	for _, note := range notes {
		if note == nil || note.Payload == nil {
			continue
		}
		if note.Payload.Target.Framework.Name == "" || note.Payload.Target.Framework.Name == "Static" {
			selector := note.Payload.Target.Selector
			if fw := rc.probeMainWorldFramework(ctx, client, note.ID, selector); fw != nil {
				note.Payload.Target.Framework = *fw
			}
		}
	}

	return notes, nil
}

func (rc *ReviewController) probeMainWorldFramework(ctx context.Context, client *CDPClient, pinID, selector string) *protocol.FrameworkInfo {
	probeScript := fmt.Sprintf(`
(function(pinId, sel) {
	let el = null;
	if (pinId) {
		el = document.querySelector('[data-tether-pin~="' + pinId + '"]');
		if (!el) return null;
	} else if (sel) {
		el = document.querySelector(sel);
	}
	if (!el) return null;
	// 1. React Fiber probe in main world
	try {
		for (const k of Object.keys(el)) {
			if (k.startsWith('__reactFiber$') || k.startsWith('__reactInternalInstance$')) {
				let fiber = el[k];
				const components = [];
				let sourceLoc = '';
				let depth = 0;
				while (fiber && depth < 30) {
					const type = fiber.type || fiber.elementType;
					if (type && typeof type !== 'string') {
						const name = type.displayName || type.name;
						if (name && !/^(Fragment|Root|Provider|Consumer|Suspense)$/.test(name) && !components.includes(name)) {
							components.push(name);
						}
					}
					if (!sourceLoc && fiber._debugSource) {
						sourceLoc = fiber._debugSource.fileName + ':' + fiber._debugSource.lineNumber;
					}
					fiber = fiber.return;
					depth++;
				}
				return {
					name: 'React',
					component: components.length > 0 ? components.slice(0, 4).reverse().map(c => '<' + c + '>').join(' ') : '',
					sourceLocation: sourceLoc,
					provenance: sourceLoc ? 'exact' : 'inferred'
				};
			}
		}
	} catch (e) {}

	// 2. Vue probe in main world
	try {
		if (el.__vueParentComponent) {
			const vnode = el.__vueParentComponent;
			const compName = vnode.type?.__name || vnode.type?.name || '';
			return {
				name: 'Vue',
				component: compName ? '<' + compName + '>' : '',
				provenance: 'inferred'
			};
		}
	} catch (e) {}

	// 3. Svelte probe in main world
	try {
		if (el.__svelte_meta && el.__svelte_meta.loc) {
			const loc = el.__svelte_meta.loc;
			return {
				name: 'Svelte',
				sourceLocation: loc.file + ':' + loc.line,
				provenance: 'exact'
			};
		}
	} catch (e) {}

	return null;
})(%q, %q)
`, pinID, selector)

	// Execute in main world (WITHOUT contextId) to access DOM expando properties
	call := map[string]any{
		"expression":    probeScript,
		"returnByValue": true,
	}
	resp, err := client.Call(ctx, "Runtime.evaluate", call)
	if err != nil {
		return nil
	}

	var evalOut struct {
		Result struct {
			Value *protocol.FrameworkInfo `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &evalOut); err == nil {
		return evalOut.Result.Value
	}
	return nil
}

// Clear removes all pinned notes and badges from the page.
func (rc *ReviewController) Clear(ctx context.Context, client *CDPClient) error {
	_, err := rc.evalInIsolatedWorld(ctx, client, "window.__tetherReview ? window.__tetherReview.clear() : true")
	return err
}
