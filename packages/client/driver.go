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
	OpenTab(ctx context.Context, params protocol.OpenParams) (*protocol.OpenResult, error)
	CloseTab(ctx context.Context, params protocol.CloseParams) error
	Snapshot(ctx context.Context, params protocol.SnapshotParams) (*protocol.SnapshotResult, error)
	Click(ctx context.Context, params protocol.ClickParams) error
	DblClick(ctx context.Context, params protocol.ClickParams) error
	Fill(ctx context.Context, params protocol.FillParams) error
	Type(ctx context.Context, params protocol.TypeParams) error
	Press(ctx context.Context, params protocol.PressParams) error
	Hover(ctx context.Context, params protocol.HoverParams) error
	Focus(ctx context.Context, params protocol.FocusParams) error
	Eval(ctx context.Context, params protocol.EvalParams) (*protocol.EvalResult, error)
	Wait(ctx context.Context, params protocol.WaitParams) error
	Screenshot(ctx context.Context, params protocol.ScreenshotParams) (*protocol.ScreenshotResult, error)
	Status(ctx context.Context, params protocol.StatusParams) (*protocol.StatusResult, error)
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
	startTime    time.Time
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
		startTime:  time.Now(),
	}
}

type targetInfo struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	Title                string `json:"title"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// OpenTab opens a new tab or navigates the active tab.
func (d *CDPDriver) OpenTab(ctx context.Context, params protocol.OpenParams) (*protocol.OpenResult, error) {
	if params.URL == "" {
		return nil, errors.New("url cannot be empty")
	}

	endpoint := fmt.Sprintf("%s/json/new?%s", d.browserURL, url.QueryEscape(params.URL))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create open request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute open request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("open tab failed (%d): %s", resp.StatusCode, string(body))
	}

	var info targetInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode target info: %w", err)
	}

	targetID := protocol.TargetID(info.ID)

	if info.WebSocketDebuggerURL == "" {
		return nil, fmt.Errorf("no webSocketDebuggerUrl returned for target %s", info.ID)
	}
	wsConn, err := DialWebSocket(ctx, info.WebSocketDebuggerURL)
	if err != nil {
		return nil, fmt.Errorf("connect cdp websocket for target %s: %w", info.ID, err)
	}
	client := NewCDPClient(wsConn)
	_, _ = client.Call(ctx, "Page.enable", nil)
	_, _ = client.Call(ctx, "Runtime.enable", nil)
	_, _ = client.Call(ctx, "DOM.enable", nil)

	d.mu.Lock()
	d.targets[targetID] = client
	d.activeTarget = targetID
	d.mu.Unlock()

	return &protocol.OpenResult{
		TargetID: targetID,
		URL:      info.URL,
		Title:    info.Title,
	}, nil
}

// CloseTab closes an open tab.
func (d *CDPDriver) CloseTab(ctx context.Context, params protocol.CloseParams) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	targetID := params.TargetID
	if targetID == "" {
		targetID = d.activeTarget
	}

	if params.CloseAll {
		for tid, client := range d.targets {
			_ = client.Close()
			endpoint := fmt.Sprintf("%s/json/close/%s", d.browserURL, tid)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err == nil {
				if resp, err := d.httpClient.Do(req); err == nil {
					_ = resp.Body.Close()
				}
			}
		}
		d.targets = make(map[protocol.TargetID]*CDPClient)
		d.refTables = make(map[protocol.TargetID]map[string]int64)
		d.activeTarget = ""
		return nil
	}

	if targetID == "" {
		return protocol.ErrTargetNotFound
	}

	client, exists := d.targets[targetID]
	if exists {
		_ = client.Close()
		delete(d.targets, targetID)
		delete(d.refTables, targetID)
	}

	endpoint := fmt.Sprintf("%s/json/close/%s", d.browserURL, targetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err == nil {
		resp, err := d.httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}

	if d.activeTarget == targetID {
		d.activeTarget = ""
		for tid := range d.targets {
			d.activeTarget = tid
			break
		}
	}

	return nil
}

func (d *CDPDriver) getTargetClient(targetID protocol.TargetID) (*CDPClient, protocol.TargetID, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	tid := targetID
	if tid == "" {
		tid = d.activeTarget
	}
	if tid == "" {
		return nil, "", protocol.ErrTargetNotFound
	}

	client, exists := d.targets[tid]
	if !exists {
		return nil, tid, protocol.ErrTargetNotFound
	}
	return client, tid, nil
}

// Snapshot generates an accessibility tree snapshot with @eN action refs.
func (d *CDPDriver) Snapshot(ctx context.Context, params protocol.SnapshotParams) (*protocol.SnapshotResult, error) {
	client, tid, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return nil, err
	}

	// Script extracts the accessibility tree with password redaction and full state observation
	script := `
(function(interactiveOnly, compact) {
	let refCounter = 1;
	const refs = {};
	const refMap = {};

	function isInteractive(el) {
		if (!el || el.nodeType !== Node.ELEMENT_NODE) return false;
		const tag = el.tagName.toLowerCase();
		if (tag === 'button' || tag === 'select' || tag === 'textarea') return true;
		if (tag === 'input' && el.type !== 'hidden') return true;
		if (tag === 'a' && el.hasAttribute('href')) return true;
		const role = el.getAttribute('role');
		if (role && /^(button|link|checkbox|radio|textbox|combobox|menuitem|tab)$/.test(role)) return true;
		if (el.hasAttribute('tabindex') && el.getAttribute('tabindex') !== '-1') return true;
		if (el.hasAttribute('onclick')) return true;
		return false;
	}

	function walk(el) {
		if (!el || el.nodeType !== Node.ELEMENT_NODE) return null;
		const tag = el.tagName.toLowerCase();
		if (tag === 'script' || tag === 'style' || tag === 'noscript') return null;

		const interactive = isInteractive(el);
		if (interactiveOnly && !interactive && el.children.length === 0) {
			return null;
		}

		let ref = "";
		let backendId = 0;
		if (interactive) {
			ref = "@e" + refCounter++;
			refs[ref] = el;
			backendId = refCounter;
			refMap[ref] = backendId;
		}

		let rect = null;
		try {
			const r = el.getBoundingClientRect();
			rect = { x: r.x, y: r.y, width: r.width, height: r.height };
		} catch(e) {}

		let name = "";
		if (el.getAttribute('aria-label')) {
			name = String(el.getAttribute('aria-label'));
		} else if (el.getAttribute('alt')) {
			name = String(el.getAttribute('alt'));
		} else if (el.innerText) {
			name = String(el.innerText).trim().slice(0, 80);
		}

		let val = "";
		if (el.value !== undefined && el.value !== null) {
			if (el.type === 'password') {
				val = "[redacted]";
			} else {
				val = String(el.value).trim().slice(0, 80);
			}
		}

		let role = el.getAttribute('role') || tag;

		const childNodes = [];
		for (let i = 0; i < el.children.length; i++) {
			const c = walk(el.children[i]);
			if (c) childNodes.push(c);
		}

		if (compact && !interactive && childNodes.length === 0 && !name) {
			return null;
		}

		return {
			ref: ref,
			backendNodeId: backendId,
			role: role,
			name: name,
			value: val,
			disabled: Boolean(el.disabled || el.getAttribute('aria-disabled') === 'true'),
			focused: document.activeElement === el,
			checked: el.checked !== undefined ? String(el.checked) : (el.getAttribute('aria-checked') || ""),
			selected: Boolean(el.selected || el.getAttribute('aria-selected') === 'true'),
			expanded: Boolean(el.getAttribute('aria-expanded') === 'true'),
			rect: rect,
			children: childNodes.length > 0 ? childNodes : null,
			isInteractive: interactive
		};
	}

	window.__tether_refs = refs;
	const root = walk(document.body || document.documentElement);
	return {
		root: root ? [root] : [],
		refMap: refMap,
		title: document.title || "",
		url: window.location.href
	};
})(%t, %t)
`
	evalCall := map[string]any{
		"expression":    fmt.Sprintf(script, params.InteractiveOnly, params.Compact),
		"returnByValue": true,
	}

	evalResp, err := client.Call(ctx, "Runtime.evaluate", evalCall)
	if err != nil {
		return nil, fmt.Errorf("evaluate snapshot script: %w", err)
	}

	var evalOut struct {
		Result struct {
			Value struct {
				Root   []*protocol.AXNode `json:"root"`
				RefMap map[string]int64   `json:"refMap"`
				Title  string             `json:"title"`
				URL    string             `json:"url"`
			} `json:"value"`
		} `json:"result"`
	}

	if err := json.Unmarshal(evalResp, &evalOut); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot output: %w", err)
	}

	val := evalOut.Result.Value
	gen := d.generation.Add(1)
	rootHash := protocol.ComputeTreeHash(val.Root)

	d.mu.Lock()
	d.refTables[tid] = val.RefMap
	d.mu.Unlock()

	return &protocol.SnapshotResult{
		Generation: gen,
		RootHash:   rootHash,
		Modified:   true,
		Nodes:      val.Root,
		RefTable:   val.RefMap,
		TargetURL:  val.URL,
		Title:      val.Title,
	}, nil
}

// Click resolves an element, scrolls it into view, and dispatches native mouse events.
func (d *CDPDriver) Click(ctx context.Context, params protocol.ClickParams) error {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return err
	}

	resolveScript := fmt.Sprintf(`
(function(sel) {
	let el = sel.startsWith('@e') ? (window.__tether_refs ? window.__tether_refs[sel] : null) : document.querySelector(sel);
	if (!el) return { notFound: true };
	if (!el.isConnected) return { stale: true };
	el.scrollIntoView({ block: 'center', inline: 'center' });
	const r = el.getBoundingClientRect();
	return { ok: true, x: r.left + r.width / 2, y: r.top + r.height / 2 };
})(%q)
`, params.Selector)

	var res struct {
		OK       bool    `json:"ok"`
		NotFound bool    `json:"notFound"`
		Stale    bool    `json:"stale"`
		X        float64 `json:"x"`
		Y        float64 `json:"y"`
	}

	val, err := d.evalRaw(ctx, client, resolveScript)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(val)
	_ = json.Unmarshal(data, &res)

	if res.Stale {
		return protocol.ErrStaleRef
	}
	if res.NotFound || !res.OK {
		return protocol.ErrTargetNotFound
	}

	button := params.Button
	if button == "" {
		button = "left"
	}
	clickCount := params.ClickCount
	if clickCount <= 0 {
		clickCount = 1
	}

	// Dispatch native CDP mouse events
	pressCall := map[string]any{
		"type":       "mousePressed",
		"button":     button,
		"clickCount": clickCount,
		"x":          res.X,
		"y":          res.Y,
	}
	if _, err := client.Call(ctx, "Input.dispatchMouseEvent", pressCall); err != nil {
		return fmt.Errorf("mouse press: %w", err)
	}

	releaseCall := map[string]any{
		"type":       "mouseReleased",
		"button":     button,
		"clickCount": clickCount,
		"x":          res.X,
		"y":          res.Y,
	}
	if _, err := client.Call(ctx, "Input.dispatchMouseEvent", releaseCall); err != nil {
		return fmt.Errorf("mouse release: %w", err)
	}

	return nil
}

// DblClick triggers a double click action.
func (d *CDPDriver) DblClick(ctx context.Context, params protocol.ClickParams) error {
	params.ClickCount = 2
	return d.Click(ctx, params)
}

// Fill clears and fills a form field.
func (d *CDPDriver) Fill(ctx context.Context, params protocol.FillParams) error {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return err
	}

	script := fmt.Sprintf(`
(function(sel, txt) {
	let el = sel.startsWith('@e') ? (window.__tether_refs ? window.__tether_refs[sel] : null) : document.querySelector(sel);
	if (!el) return { notFound: true };
	if (!el.isConnected) return { stale: true };
	el.focus();
	el.value = txt;
	el.dispatchEvent(new Event('input', { bubbles: true }));
	el.dispatchEvent(new Event('change', { bubbles: true }));
	return { ok: true };
})(%q, %q)
`, params.Selector, params.Text)

	var res struct {
		OK       bool `json:"ok"`
		NotFound bool `json:"notFound"`
		Stale    bool `json:"stale"`
	}

	val, err := d.evalRaw(ctx, client, script)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(val)
	_ = json.Unmarshal(data, &res)

	if res.Stale {
		return protocol.ErrStaleRef
	}
	if res.NotFound {
		return protocol.ErrTargetNotFound
	}
	return nil
}

// Type inserts text into an element preserving selection and caret.
func (d *CDPDriver) Type(ctx context.Context, params protocol.TypeParams) error {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return err
	}

	script := fmt.Sprintf(`
(function(sel, txt) {
	let el;
	if (sel) {
		el = sel.startsWith('@e') ? (window.__tether_refs ? window.__tether_refs[sel] : null) : document.querySelector(sel);
		if (!el) return { notFound: true };
		if (!el.isConnected) return { stale: true };
		el.focus();
	} else {
		el = document.activeElement || document.body;
	}
	if (typeof el.setRangeText === 'function') {
		el.setRangeText(txt, el.selectionStart || 0, el.selectionEnd || 0, 'end');
	} else {
		el.value = (el.value || '') + txt;
	}
	el.dispatchEvent(new Event('input', { bubbles: true }));
	return { ok: true };
})(%q, %q)
`, params.Selector, params.Text)

	var res struct {
		OK       bool `json:"ok"`
		NotFound bool `json:"notFound"`
		Stale    bool `json:"stale"`
	}

	val, err := d.evalRaw(ctx, client, script)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(val)
	_ = json.Unmarshal(data, &res)

	if res.Stale {
		return protocol.ErrStaleRef
	}
	if res.NotFound {
		return protocol.ErrTargetNotFound
	}
	return nil
}

// Press dispatches native CDP keyboard events.
func (d *CDPDriver) Press(ctx context.Context, params protocol.PressParams) error {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return err
	}

	key := params.Key
	modifiers := 0

	// Handle modifiers e.g. "Control+a"
	if strings.Contains(key, "+") {
		parts := strings.Split(key, "+")
		key = parts[len(parts)-1]
		for _, m := range parts[:len(parts)-1] {
			switch strings.ToLower(m) {
			case "ctrl", "control":
				modifiers |= 2
			case "alt":
				modifiers |= 1
			case "shift":
				modifiers |= 8
			case "meta", "cmd", "command":
				modifiers |= 4
			}
		}
	}

	var vkCode int
	var keyText string
	if len(key) == 1 {
		keyText = key
	}

	switch key {
	case "Enter":
		vkCode = 13
		keyText = "\r"
	case "Tab":
		vkCode = 9
		keyText = "\t"
	case "Backspace":
		vkCode = 8
	case "Escape":
		vkCode = 27
	case "ArrowLeft":
		vkCode = 37
	case "ArrowUp":
		vkCode = 38
	case "ArrowRight":
		vkCode = 39
	case "ArrowDown":
		vkCode = 40
	case "Delete":
		vkCode = 46
	case "Home":
		vkCode = 36
	case "End":
		vkCode = 35
	case "PageUp":
		vkCode = 33
	case "PageDown":
		vkCode = 34
	case "a", "A":
		vkCode = 65
	case "c", "C":
		vkCode = 67
	case "v", "V":
		vkCode = 86
	case "x", "X":
		vkCode = 88
	default:
		if len(key) == 1 {
			vkCode = int(strings.ToUpper(key)[0])
		}
	}

	keyDown := map[string]any{
		"type":                  "rawKeyDown",
		"key":                   key,
		"modifiers":             modifiers,
		"windowsVirtualKeyCode": vkCode,
	}
	if _, err := client.Call(ctx, "Input.dispatchKeyEvent", keyDown); err != nil {
		return fmt.Errorf("key down: %w", err)
	}

	hasNonTextModifiers := (modifiers & (2 | 1 | 4)) != 0
	if keyText != "" && !hasNonTextModifiers {
		charEvt := map[string]any{
			"unmodifiedText": keyText,
			"key":            key,
			"modifiers":      modifiers,
		}
		_, _ = client.Call(ctx, "Input.dispatchKeyEvent", charEvt)
	}

	keyUp := map[string]any{
		"type":                  "keyUp",
		"key":                   key,
		"modifiers":             modifiers,
		"windowsVirtualKeyCode": vkCode,
	}
	if _, err := client.Call(ctx, "Input.dispatchKeyEvent", keyUp); err != nil {
		return fmt.Errorf("key up: %w", err)
	}

	return nil
}

// Hover resolves element bounds and dispatches native mouse move events.
func (d *CDPDriver) Hover(ctx context.Context, params protocol.HoverParams) error {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return err
	}

	script := fmt.Sprintf(`
(function(sel) {
	let el = sel.startsWith('@e') ? (window.__tether_refs ? window.__tether_refs[sel] : null) : document.querySelector(sel);
	if (!el) return { notFound: true };
	if (!el.isConnected) return { stale: true };
	el.scrollIntoView({ block: 'center', inline: 'center' });
	const r = el.getBoundingClientRect();
	return { ok: true, x: r.left + r.width / 2, y: r.top + r.height / 2 };
})(%q)
`, params.Selector)

	var res struct {
		OK       bool    `json:"ok"`
		NotFound bool    `json:"notFound"`
		Stale    bool    `json:"stale"`
		X        float64 `json:"x"`
		Y        float64 `json:"y"`
	}

	val, err := d.evalRaw(ctx, client, script)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(val)
	_ = json.Unmarshal(data, &res)

	if res.Stale {
		return protocol.ErrStaleRef
	}
	if res.NotFound || !res.OK {
		return protocol.ErrTargetNotFound
	}

	moveCall := map[string]any{
		"type": "mouseMoved",
		"x":    res.X,
		"y":    res.Y,
	}
	_, err = client.Call(ctx, "Input.dispatchMouseEvent", moveCall)
	return err
}

// Focus focuses the target element.
func (d *CDPDriver) Focus(ctx context.Context, params protocol.FocusParams) error {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return err
	}

	script := fmt.Sprintf(`
(function(sel) {
	let el = sel.startsWith('@e') ? (window.__tether_refs ? window.__tether_refs[sel] : null) : document.querySelector(sel);
	if (!el) return { notFound: true };
	if (!el.isConnected) return { stale: true };
	el.focus();
	return { ok: true };
})(%q)
`, params.Selector)

	var res struct {
		OK       bool `json:"ok"`
		NotFound bool `json:"notFound"`
		Stale    bool `json:"stale"`
	}

	val, err := d.evalRaw(ctx, client, script)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(val)
	_ = json.Unmarshal(data, &res)

	if res.Stale {
		return protocol.ErrStaleRef
	}
	if res.NotFound {
		return protocol.ErrTargetNotFound
	}
	return nil
}

// Eval evaluates a JavaScript expression in the page context.
func (d *CDPDriver) Eval(ctx context.Context, params protocol.EvalParams) (*protocol.EvalResult, error) {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return nil, err
	}

	val, err := d.evalRaw(ctx, client, params.Expression)
	if err != nil {
		return &protocol.EvalResult{Error: err.Error()}, nil
	}
	return &protocol.EvalResult{Value: val}, nil
}

func (d *CDPDriver) evalRaw(ctx context.Context, client *CDPClient, expression string) (any, error) {
	call := map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
	}
	resp, err := client.Call(ctx, "Runtime.evaluate", call)
	if err != nil {
		return nil, err
	}

	var out struct {
		Result struct {
			Type  string `json:"type"`
			Value any    `json:"value"`
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

// Wait waits for a duration or for a selector condition with a bounded deadline.
func (d *CDPDriver) Wait(ctx context.Context, params protocol.WaitParams) error {
	if params.DurationMs > 0 {
		select {
		case <-time.After(time.Duration(params.DurationMs) * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if params.Selector == "" {
		return nil
	}

	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return err
	}

	timeout := 25 * time.Second
	if params.TimeoutMs > 0 {
		timeout = time.Duration(params.TimeoutMs) * time.Millisecond
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	state := params.State
	if state == "" {
		state = "visible"
	}

	checkScript := fmt.Sprintf(`
(function(sel, st) {
	let el = sel.startsWith('@e') ? (window.__tether_refs ? window.__tether_refs[sel] : null) : document.querySelector(sel);
	const isAttached = Boolean(el && el.isConnected);
	if (st === 'attached') {
		return isAttached;
	}
	if (!isAttached) {
		return st === 'hidden';
	}
	const r = el.getBoundingClientRect();
	const style = window.getComputedStyle(el);
	const visible = style.display !== 'none' &&
					style.visibility !== 'hidden' &&
					style.opacity !== '0' &&
					(r.width > 0 || r.height > 0);
	if (st === 'hidden') return !visible;
	return visible;
})(%q, %q)
`, params.Selector, state)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			return protocol.ErrActionTimeout
		case <-ticker.C:
			val, err := d.evalRaw(waitCtx, client, checkScript)
			if err == nil {
				if ok, _ := val.(bool); ok {
					return nil
				}
			}
		}
	}
}

// Screenshot captures a viewport or full-page screenshot.
func (d *CDPDriver) Screenshot(ctx context.Context, params protocol.ScreenshotParams) (*protocol.ScreenshotResult, error) {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return nil, err
	}

	format := params.Format
	if format == "" {
		format = "png"
	}

	call := map[string]any{
		"format": format,
	}
	if params.Quality > 0 && format == "jpeg" {
		call["quality"] = params.Quality
	}

	resp, err := client.Call(ctx, "Page.captureScreenshot", call)
	if err != nil {
		return nil, fmt.Errorf("capture screenshot: %w", err)
	}

	var out struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("unmarshal screenshot: %w", err)
	}

	return &protocol.ScreenshotResult{
		Base64: out.Data,
		Format: format,
	}, nil
}

// Status returns driver and connected tab status.
func (d *CDPDriver) Status(ctx context.Context, params protocol.StatusParams) (*protocol.StatusResult, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return &protocol.StatusResult{
		Connected:      len(d.targets) > 0,
		ActiveTargetID: d.activeTarget,
		TargetCount:    len(d.targets),
		Version:        "1.0.0",
		Mode:           "managed",
		DaemonUptimeS:  int64(time.Since(d.startTime).Seconds()),
	}, nil
}
