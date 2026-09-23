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

func (rc *ReviewController) evalInIsolatedWorld(ctx context.Context, client *CDPClient, expression string) (json.RawMessage, error) {
	contextID, err := isolatedWorld(ctx, client, "tether-review")
	if err != nil {
		return nil, err
	}
	return evaluate(ctx, client, expression, contextID, true)
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

// autoReviewSnippet re-arms the passive review session on every new document.
// Top-frame only: iframes must never sprout their own dock. The sessionStorage
// gate keeps a stopped session stopped across navigations. The gate is
// per-origin by design: Done silences the current site, while a new site
// starts a fresh passive session (notes are per-page payloads anyway).
const autoReviewSnippet = `;try {
  if (window.top === window && window.__tetherReview && !window.__tetherReview.active) {
    var off = false;
    try { off = window.sessionStorage.getItem('tether-review-off') === '1'; } catch (e) {}
    if (!off) { window.__tetherReview.startPassive(); }
  }
} catch (e) {}`

// EnsureAutoReview registers the overlay to self-install in every future
// document of the target's main frame, so navigations cannot wipe the bar.
func (rc *ReviewController) EnsureAutoReview(ctx context.Context, client *CDPClient) error {
	call := map[string]any{
		"source":    reviewOverlayScript + autoReviewSnippet,
		"worldName": "tether-review",
	}
	if _, err := client.Call(ctx, "Page.addScriptToEvaluateOnNewDocument", call); err != nil {
		return fmt.Errorf("register auto review script: %w", err)
	}
	return nil
}
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
