package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

// BrowserDriver defines the modular abstraction for browser automation.
type BrowserDriver interface {
	OpenTab(ctx context.Context, url string) (protocol.TargetID, error)
	CloseTab(ctx context.Context, target protocol.TargetID) error
	Snapshot(ctx context.Context, target protocol.TargetID, interactiveOnly bool) (*protocol.SnapshotResult, error)
	Click(ctx context.Context, target protocol.TargetID, selector string) error
	Fill(ctx context.Context, target protocol.TargetID, selector, text string) error
	Type(ctx context.Context, target protocol.TargetID, selector, text string) error
	Press(ctx context.Context, target protocol.TargetID, key string) error
	Hover(ctx context.Context, target protocol.TargetID, selector string) error
	Focus(ctx context.Context, target protocol.TargetID, selector string) error
	Eval(ctx context.Context, target protocol.TargetID, script string) (any, error)
	Wait(ctx context.Context, target protocol.TargetID, selector string, timeoutMs int) error
	Screenshot(ctx context.Context, target protocol.TargetID, fullPage bool) (*protocol.ScreenshotResult, error)
	StartReview(ctx context.Context, target protocol.TargetID) error
	GetReviewNotes(ctx context.Context, target protocol.TargetID) (*protocol.ReviewPayload, error)
}

// CDPDriver implements BrowserDriver by communicating with Chrome over CDP.
type CDPDriver struct {
	browserURL   string
	httpClient   *http.Client
	mu           sync.RWMutex
	activeTarget protocol.TargetID
	targets      map[protocol.TargetID]*CDPClient
	refTables    map[protocol.TargetID]map[string]int64
	generation   atomic.Uint64
}

// NewCDPDriver creates a new driver connected to the Chrome debugging endpoint.
func NewCDPDriver(browserURL string) *CDPDriver {
	if !strings.HasPrefix(browserURL, "http://") && !strings.HasPrefix(browserURL, "https://") {
		browserURL = "http://" + browserURL
	}
	return &CDPDriver{
		browserURL: browserURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		targets:    make(map[protocol.TargetID]*CDPClient),
		refTables:  make(map[protocol.TargetID]map[string]int64),
	}
}

// targetInfo holds metadata returned by Chrome's /json/new and /json/list endpoints.
type targetInfo struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	Type                 string `json:"type"`
}

// OpenTab launches a new browser tab or navigates an existing one.
func (d *CDPDriver) OpenTab(ctx context.Context, targetURL string) (protocol.TargetID, error) {
	newEndpoint := fmt.Sprintf("%s/json/new", d.browserURL)
	if targetURL != "" {
		newEndpoint = fmt.Sprintf("%s/json/new?%s", d.browserURL, url.QueryEscape(targetURL))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, newEndpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create new tab request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("open tab http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("open tab failed with status %d: %s", resp.StatusCode, string(body))
	}

	var info targetInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("decode tab response: %w", err)
	}

	if info.WebSocketDebuggerURL == "" {
		return "", errors.New("no websocket debugger url provided by chrome")
	}

	ws, err := DialWebSocket(ctx, info.WebSocketDebuggerURL)
	if err != nil {
		return "", fmt.Errorf("connect cdp websocket: %w", err)
	}

	client := NewCDPClient(ws)
	_ = d.initDomains(ctx, client)

	targetID := protocol.TargetID(info.ID)
	d.mu.Lock()
	d.targets[targetID] = client
	d.activeTarget = targetID
	d.mu.Unlock()

	return targetID, nil
}

func (d *CDPDriver) initDomains(ctx context.Context, c *CDPClient) error {
	domains := []string{"Page.enable", "Runtime.enable", "DOM.enable"}
	for _, domain := range domains {
		if _, err := c.Call(ctx, domain, nil); err != nil {
			return err
		}
	}
	return nil
}

// CloseTab closes a specified tab or the active tab.
func (d *CDPDriver) CloseTab(ctx context.Context, target protocol.TargetID) error {
	client, id, err := d.resolveClient(target)
	if err != nil {
		return err
	}

	_ = client.Close()

	closeEndpoint := fmt.Sprintf("%s/json/close/%s", d.browserURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, closeEndpoint, nil)
	if err == nil {
		if resp, err := d.httpClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}

	d.mu.Lock()
	delete(d.targets, id)
	delete(d.refTables, id)
	if d.activeTarget == id {
		d.activeTarget = ""
		for otherID := range d.targets {
			d.activeTarget = otherID
			break
		}
	}
	d.mu.Unlock()

	return nil
}

func (d *CDPDriver) resolveClient(target protocol.TargetID) (*CDPClient, protocol.TargetID, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	id := target
	if id == "" {
		id = d.activeTarget
	}
	if id == "" {
		return nil, "", protocol.ErrTargetNotFound
	}

	client, ok := d.targets[id]
	if !ok {
		return nil, "", protocol.ErrTargetNotFound
	}
	return client, id, nil
}

// Eval executes a JavaScript expression in the page context.
func (d *CDPDriver) Eval(ctx context.Context, target protocol.TargetID, script string) (any, error) {
	client, _, err := d.resolveClient(target)
	if err != nil {
		return nil, err
	}

	params := map[string]any{
		"expression":    script,
		"returnByValue": true,
		"awaitPromise":  true,
	}

	raw, err := client.Call(ctx, "Runtime.evaluate", params)
	if err != nil {
		return nil, fmt.Errorf("eval call: %w", err)
	}

	var evalRes struct {
		Result struct {
			Type  string `json:"type"`
			Value any    `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}

	if err := json.Unmarshal(raw, &evalRes); err != nil {
		return nil, fmt.Errorf("unmarshal eval result: %w", err)
	}

	if evalRes.ExceptionDetails != nil {
		return nil, fmt.Errorf("eval exception: %s", evalRes.ExceptionDetails.Text)
	}

	return evalRes.Result.Value, nil
}

// Snapshot extracts accessibility elements, assigns @eN refs, and computes root tree hash.
func (d *CDPDriver) Snapshot(ctx context.Context, target protocol.TargetID, interactiveOnly bool) (*protocol.SnapshotResult, error) {
	script := fmt.Sprintf(`
(function(interactiveOnly) {
	window.__tether_refs = window.__tether_refs || {};
	const refMap = {};
	let counter = 1;

	function isVisible(el) {
		const r = el.getBoundingClientRect();
		return r.width > 0 && r.height > 0;
	}

	function isElementInteractive(el) {
		const tag = el.tagName.toLowerCase();
		if (['a', 'button', 'input', 'select', 'textarea'].includes(tag)) return true;
		const role = el.getAttribute('role');
		if (['button', 'link', 'checkbox', 'menuitem', 'tab', 'textbox'].includes(role)) return true;
		if (el.hasAttribute('onclick') || el.tabIndex >= 0) return true;
		return false;
	}

	const all = Array.from(document.querySelectorAll('*'));
	const nodes = [];

	for (const el of all) {
		if (!isVisible(el)) continue;
		const interactive = isElementInteractive(el);
		if (interactiveOnly && !interactive) continue;

		const tag = el.tagName.toLowerCase();
		const role = el.getAttribute('role') || tag;
		const name = el.getAttribute('aria-label') || el.innerText || el.value || el.placeholder || '';
		const trimmedName = name.trim().slice(0, 100);

		const ref = '@e' + counter++;
		refMap[ref] = el;

		const rect = el.getBoundingClientRect();

		nodes.push({
			ref: ref,
			backendNodeId: counter,
			role: role,
			name: trimmedName,
			value: el.value || '',
			isInteractive: interactive,
			rect: {
				x: rect.x,
				y: rect.y,
				width: rect.width,
				height: rect.height
			}
		});
	}

	window.__tether_refs = refMap;
	return {
		title: document.title,
		url: location.href,
		nodes: nodes
	};
})(%t)
`, interactiveOnly)

	val, err := d.Eval(ctx, target, script)
	if err != nil {
		return nil, fmt.Errorf("snapshot extraction script: %w", err)
	}

	var data []byte
	if strVal, ok := val.(string); ok {
		data = []byte(strVal)
	} else {
		var err error
		data, err = json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("marshal raw snapshot: %w", err)
		}
	}

	var extracted struct {
		Title string             `json:"title"`
		URL   string             `json:"url"`
		Nodes []*protocol.AXNode `json:"nodes"`
	}

	if err := json.Unmarshal(data, &extracted); err != nil {
		return nil, fmt.Errorf("unmarshal extracted snapshot: %w", err)
	}

	refTable := make(map[string]int64, len(extracted.Nodes))
	for i, n := range extracted.Nodes {
		if n.Ref != "" {
			if n.BackendNodeID == 0 {
				n.BackendNodeID = int64(i + 1)
			}
			refTable[n.Ref] = n.BackendNodeID
		}
	}

	_, id, _ := d.resolveClient(target)
	d.mu.Lock()
	d.refTables[id] = refTable
	d.mu.Unlock()

	rootHash := protocol.ComputeTreeHash(extracted.Nodes)
	gen := d.generation.Add(1)

	return &protocol.SnapshotResult{
		Generation: gen,
		RootHash:   rootHash,
		Modified:   true,
		Nodes:      extracted.Nodes,
		RefTable:   refTable,
		TargetURL:  extracted.URL,
		Title:      extracted.Title,
	}, nil
}

// Click triggers a click event on an element matching selector or @eN ref.
func (d *CDPDriver) Click(ctx context.Context, target protocol.TargetID, selector string) error {
	script := fmt.Sprintf(`
(function(sel) {
	let el;
	if (sel.startsWith('@e')) {
		el = window.__tether_refs ? window.__tether_refs[sel] : null;
	} else {
		el = document.querySelector(sel);
	}
	if (!el) return { ok: false, notFound: true };

	el.scrollIntoView({ block: 'center', inline: 'center' });
	el.focus();
	el.click();
	return { ok: true };
})(%q)
`, selector)

	val, err := d.Eval(ctx, target, script)
	if err != nil {
		return err
	}

	var res struct {
		OK       bool `json:"ok"`
		NotFound bool `json:"notFound"`
	}
	data, _ := json.Marshal(val)
	_ = json.Unmarshal(data, &res)

	if res.NotFound {
		return protocol.ErrTargetNotFound
	}
	return nil
}

// Fill clears and populates a form input.
func (d *CDPDriver) Fill(ctx context.Context, target protocol.TargetID, selector, text string) error {
	script := fmt.Sprintf(`
(function(sel, val) {
	let el;
	if (sel.startsWith('@e')) {
		el = window.__tether_refs ? window.__tether_refs[sel] : null;
	} else {
		el = document.querySelector(sel);
	}
	if (!el) return { ok: false, notFound: true };

	el.scrollIntoView({ block: 'center', inline: 'center' });
	el.focus();
	el.value = val;
	el.dispatchEvent(new Event('input', { bubbles: true }));
	el.dispatchEvent(new Event('change', { bubbles: true }));
	return { ok: true };
})(%q, %q)
`, selector, text)

	val, err := d.Eval(ctx, target, script)
	if err != nil {
		return err
	}

	var res struct {
		OK       bool `json:"ok"`
		NotFound bool `json:"notFound"`
	}
	data, _ := json.Marshal(val)
	_ = json.Unmarshal(data, &res)

	if res.NotFound {
		return protocol.ErrTargetNotFound
	}
	return nil
}

// Type simulates sequential character entry into the focused element or selector.
func (d *CDPDriver) Type(ctx context.Context, target protocol.TargetID, selector, text string) error {
	script := fmt.Sprintf(`
(function(sel, val) {
	let el = document.activeElement;
	if (sel) {
		if (sel.startsWith('@e')) {
			el = window.__tether_refs ? window.__tether_refs[sel] : null;
		} else {
			el = document.querySelector(sel);
		}
	}
	if (!el) return { ok: false, notFound: true };

	el.focus();
	if ('value' in el) {
		el.value += val;
		el.dispatchEvent(new Event('input', { bubbles: true }));
		el.dispatchEvent(new Event('change', { bubbles: true }));
	} else {
		document.execCommand('insertText', false, val);
	}
	return { ok: true };
})(%q, %q)
`, selector, text)

	val, err := d.Eval(ctx, target, script)
	if err != nil {
		return err
	}

	var res struct {
		OK       bool `json:"ok"`
		NotFound bool `json:"notFound"`
	}
	data, _ := json.Marshal(val)
	_ = json.Unmarshal(data, &res)

	if res.NotFound {
		return protocol.ErrTargetNotFound
	}
	return nil
}

// Press triggers a keyboard key event.
func (d *CDPDriver) Press(ctx context.Context, target protocol.TargetID, key string) error {
	script := fmt.Sprintf(`
(function(k) {
	const el = document.activeElement || document.body;
	const init = { key: k, code: k, bubbles: true, cancelable: true };
	el.dispatchEvent(new KeyboardEvent('keydown', init));
	el.dispatchEvent(new KeyboardEvent('keypress', init));
	el.dispatchEvent(new KeyboardEvent('keyup', init));
	if (k === 'Enter' && el.form) {
		el.form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
	}
	return { ok: true };
})(%q)
`, key)

	_, err := d.Eval(ctx, target, script)
	return err
}

// Hover triggers mouseover and mousemove events on an element.
func (d *CDPDriver) Hover(ctx context.Context, target protocol.TargetID, selector string) error {
	script := fmt.Sprintf(`
(function(sel) {
	let el;
	if (sel.startsWith('@e')) {
		el = window.__tether_refs ? window.__tether_refs[sel] : null;
	} else {
		el = document.querySelector(sel);
	}
	if (!el) return { ok: false, notFound: true };

	el.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));
	el.dispatchEvent(new MouseEvent('mouseenter', { bubbles: true }));
	el.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }));
	return { ok: true };
})(%q)
`, selector)

	val, err := d.Eval(ctx, target, script)
	if err != nil {
		return err
	}

	var res struct {
		OK       bool `json:"ok"`
		NotFound bool `json:"notFound"`
	}
	data, _ := json.Marshal(val)
	_ = json.Unmarshal(data, &res)

	if res.NotFound {
		return protocol.ErrTargetNotFound
	}
	return nil
}

// Focus focuses the element matching selector or @eN ref.
func (d *CDPDriver) Focus(ctx context.Context, target protocol.TargetID, selector string) error {
	script := fmt.Sprintf(`
(function(sel) {
	let el;
	if (sel.startsWith('@e')) {
		el = window.__tether_refs ? window.__tether_refs[sel] : null;
	} else {
		el = document.querySelector(sel);
	}
	if (!el) return { ok: false, notFound: true };

	el.focus();
	return { ok: true };
})(%q)
`, selector)

	val, err := d.Eval(ctx, target, script)
	if err != nil {
		return err
	}

	var res struct {
		OK       bool `json:"ok"`
		NotFound bool `json:"notFound"`
	}
	data, _ := json.Marshal(val)
	_ = json.Unmarshal(data, &res)

	if res.NotFound {
		return protocol.ErrTargetNotFound
	}
	return nil
}

// Wait polls until a selector is present in the DOM or timeout expires.
func (d *CDPDriver) Wait(ctx context.Context, target protocol.TargetID, selector string, timeoutMs int) error {
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}

	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	script := fmt.Sprintf(`
(function(sel) {
	let el;
	if (sel.startsWith('@e')) {
		el = window.__tether_refs ? window.__tether_refs[sel] : null;
	} else {
		el = document.querySelector(sel);
	}
	return el !== null;
})(%q)
`, selector)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			val, err := d.Eval(ctx, target, script)
			if err == nil {
				if found, ok := val.(bool); ok && found {
					return nil
				}
			}
			if time.Now().After(deadline) {
				return protocol.ErrActionTimeout
			}
		}
	}
}

// Screenshot captures a viewport or full page screenshot as base64.
func (d *CDPDriver) Screenshot(ctx context.Context, target protocol.TargetID, fullPage bool) (*protocol.ScreenshotResult, error) {
	client, _, err := d.resolveClient(target)
	if err != nil {
		return nil, err
	}

	params := map[string]any{
		"format":                "png",
		"captureBeyondViewport": fullPage,
	}

	raw, err := client.Call(ctx, "Page.captureScreenshot", params)
	if err != nil {
		return nil, fmt.Errorf("capture screenshot: %w", err)
	}

	var res struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("unmarshal screenshot data: %w", err)
	}

	return &protocol.ScreenshotResult{
		Base64: res.Data,
		Format: "png",
	}, nil
}

// StartReview initializes the visual inspection overlay.
func (d *CDPDriver) StartReview(ctx context.Context, target protocol.TargetID) error {
	script := `
(function() {
	window.__tether_review_active = true;
	window.__tether_review_notes = window.__tether_review_notes || [];
	return true;
})()
`
	_, err := d.Eval(ctx, target, script)
	return err
}

// GetReviewNotes returns captured review metadata and notes.
func (d *CDPDriver) GetReviewNotes(ctx context.Context, target protocol.TargetID) (*protocol.ReviewPayload, error) {
	script := `
(function() {
	return {
		page: {
			sanitizedUrl: location.href,
			title: document.title,
			viewportWidth: window.innerWidth,
			viewportHeight: window.innerHeight,
			scrollX: window.scrollX,
			scrollY: window.scrollY,
			capturedAt: new Date().toISOString()
		},
		target: {
			tagName: 'body',
			role: 'document',
			selector: 'body',
			elementPath: 'body',
			fullPath: 'html > body',
			rectViewport: { x: 0, y: 0, width: window.innerWidth, height: window.innerHeight },
			rectPage: { x: 0, y: 0, width: window.innerWidth, height: window.innerHeight },
			framework: {
				name: 'Static',
				provenance: 'unavailable'
			}
		}
	};
})()
`
	val, err := d.Eval(ctx, target, script)
	if err != nil {
		return nil, err
	}

	var data []byte
	if strVal, ok := val.(string); ok {
		data = []byte(strVal)
	} else {
		var err error
		data, err = json.Marshal(val)
		if err != nil {
			return nil, err
		}
	}

	var payload protocol.ReviewPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}

	return &payload, nil
}
