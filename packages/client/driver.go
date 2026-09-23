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
	Scroll(ctx context.Context, params protocol.ScrollParams) error
	Screenshot(ctx context.Context, params protocol.ScreenshotParams) (*protocol.ScreenshotResult, error)
	Status(ctx context.Context, params protocol.StatusParams) (*protocol.StatusResult, error)
	ListTabs(ctx context.Context) (*protocol.TabListResult, error)
	SwitchTab(ctx context.Context, params protocol.TabSwitchParams) error
	StartReview(ctx context.Context, params protocol.ReviewParams) error
	GetReviewNotes(ctx context.Context, params protocol.ReviewParams) ([]*protocol.ReviewNote, error)
	ClearReview(ctx context.Context, params protocol.ReviewParams) error
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
	parsedURL, err := url.Parse(params.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", params.URL, err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" && params.URL != "about:blank" {
		return nil, fmt.Errorf("invalid URL scheme %q: only http, https, and about:blank are allowed", parsedURL.Scheme)
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

	// Always-on review bar: arm the session chrome passively so the dock and
	// markers are present without intercepting automation. Best-effort only.
	// EnsureAutoReview re-arms on every future navigation; StartPassive covers
	// the already-committed document.
	_ = (&ReviewController{}).EnsureAutoReview(ctx, client)
	_ = (&ReviewController{}).StartPassive(ctx, client)
	return &protocol.OpenResult{
		TargetID: targetID,
		URL:      info.URL,
		Title:    info.Title,
	}, nil
}

// CloseTab closes an open tab.
func (d *CDPDriver) CloseTab(ctx context.Context, params protocol.CloseParams) error {
	d.mu.Lock()
	targetID := params.TargetID
	if targetID == "" {
		targetID = d.activeTarget
	}

	if params.CloseAll {
		targetsToClose := make(map[protocol.TargetID]*CDPClient, len(d.targets))
		for tid, client := range d.targets {
			targetsToClose[tid] = client
		}
		d.targets = make(map[protocol.TargetID]*CDPClient)
		d.refTables = make(map[protocol.TargetID]map[string]int64)
		d.activeTarget = ""
		d.mu.Unlock()

		for tid, client := range targetsToClose {
			_ = client.Close()
			endpoint := fmt.Sprintf("%s/json/close/%s", d.browserURL, tid)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err == nil {
				if resp, err := d.httpClient.Do(req); err == nil {
					_ = resp.Body.Close()
				}
			}
		}
		return nil
	}

	if targetID == "" {
		d.mu.Unlock()
		return protocol.ErrTargetNotFound
	}

	client, exists := d.targets[targetID]
	if exists {
		delete(d.targets, targetID)
		delete(d.refTables, targetID)
	}
	if d.activeTarget == targetID {
		d.activeTarget = ""
		for tid := range d.targets {
			d.activeTarget = tid
			break
		}
	}
	d.mu.Unlock()

	if client != nil {
		_ = client.Close()
	}

	endpoint := fmt.Sprintf("%s/json/close/%s", d.browserURL, targetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err == nil {
		if resp, err := d.httpClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}

	return nil
}

func (d *CDPDriver) discoverTargets(ctx context.Context) ([]targetInfo, error) {
	endpoint := fmt.Sprintf("%s/json/list", d.browserURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list targets failed with status %d", resp.StatusCode)
	}
	var all []targetInfo
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, err
	}
	var pages []targetInfo
	for _, t := range all {
		if t.Type == "page" && !strings.HasPrefix(t.URL, "chrome-extension://") {
			pages = append(pages, t)
		}
	}
	return pages, nil
}

func (d *CDPDriver) ListTabs(ctx context.Context) (*protocol.TabListResult, error) {
	pages, err := d.discoverTargets(ctx)
	if err != nil {
		return nil, err
	}
	d.mu.RLock()
	activeID := d.activeTarget
	d.mu.RUnlock()

	res := &protocol.TabListResult{
		Tabs:     make([]protocol.TabInfo, 0, len(pages)),
		ActiveID: activeID,
	}

	for _, p := range pages {
		tid := protocol.TargetID(p.ID)
		res.Tabs = append(res.Tabs, protocol.TabInfo{
			ID:     tid,
			Title:  p.Title,
			URL:    p.URL,
			Active: tid == activeID,
		})
	}
	if (activeID == "" || len(res.Tabs) == 1) && len(pages) > 0 {
		latest := protocol.TargetID(pages[len(pages)-1].ID)
		d.mu.Lock()
		d.activeTarget = latest
		d.mu.Unlock()
		res.ActiveID = latest
		for i := range res.Tabs {
			if res.Tabs[i].ID == latest {
				res.Tabs[i].Active = true
			}
		}
	}
	return res, nil
}

func (d *CDPDriver) SwitchTab(ctx context.Context, params protocol.TabSwitchParams) error {
	if params.TargetID == "" {
		return errors.New("targetId cannot be empty")
	}
	pages, err := d.discoverTargets(ctx)
	if err != nil {
		return err
	}
	var found *targetInfo
	for _, p := range pages {
		if protocol.TargetID(p.ID) == params.TargetID {
			found = &p
			break
		}
	}
	if found == nil {
		return fmt.Errorf("target tab %q not found in open browser tabs", params.TargetID)
	}

	d.mu.Lock()
	client, exists := d.targets[params.TargetID]
	d.activeTarget = params.TargetID
	d.mu.Unlock()

	if !exists {
		wsConn, err := DialWebSocket(ctx, found.WebSocketDebuggerURL)
		if err != nil {
			return fmt.Errorf("connect to target %s: %w", params.TargetID, err)
		}
		newClient := NewCDPClient(wsConn)
		_, _ = newClient.Call(ctx, "Page.enable", nil)
		_, _ = newClient.Call(ctx, "Runtime.enable", nil)
		_, _ = newClient.Call(ctx, "DOM.enable", nil)

		d.mu.Lock()
		d.targets[params.TargetID] = newClient
		d.mu.Unlock()
		client = newClient
	}

	_, _ = client.Call(ctx, "Page.bringToFront", nil)
	return nil
}

func (d *CDPDriver) getTargetClient(targetID protocol.TargetID) (*CDPClient, protocol.TargetID, error) {
	tid := targetID
	d.mu.RLock()
	if tid == "" {
		tid = d.activeTarget
	}
	client, exists := d.targets[tid]
	d.mu.RUnlock()

	if exists && client != nil {
		return client, tid, nil
	}

	// Auto-discovery: connect to the target or most recent live page in Chrome
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pages, err := d.discoverTargets(ctx)
	if err != nil || len(pages) == 0 {
		return nil, tid, protocol.ErrTargetNotFound
	}

	var targetToAttach *targetInfo
	if tid != "" {
		for _, p := range pages {
			if protocol.TargetID(p.ID) == tid {
				targetToAttach = &p
				break
			}
		}
	}
	if targetToAttach == nil {
		targetToAttach = &pages[len(pages)-1]
	}

	resolvedTID := protocol.TargetID(targetToAttach.ID)
	d.mu.RLock()
	existingClient, exists := d.targets[resolvedTID]
	d.mu.RUnlock()
	if exists && existingClient != nil {
		d.mu.Lock()
		d.activeTarget = resolvedTID
		d.mu.Unlock()
		return existingClient, resolvedTID, nil
	}

	wsConn, err := DialWebSocket(ctx, targetToAttach.WebSocketDebuggerURL)
	if err != nil {
		return nil, resolvedTID, fmt.Errorf("connect to target %s: %w", resolvedTID, err)
	}

	newClient := NewCDPClient(wsConn)
	_, _ = newClient.Call(ctx, "Page.enable", nil)
	_, _ = newClient.Call(ctx, "Runtime.enable", nil)
	_, _ = newClient.Call(ctx, "DOM.enable", nil)

	d.mu.Lock()
	d.targets[resolvedTID] = newClient
	d.activeTarget = resolvedTID
	d.mu.Unlock()

	return newClient, resolvedTID, nil
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
	const SECRET_PATTERNS = [
		/access_token/i,
		/auth_token/i,
		/refresh_token/i,
		/refresh_?token/i,
		/id_token/i,
		/session_?token/i,
		/\btoken\b/i,
		/auth_code/i,
		/oauth_state/i,
		/[?&](code|state)=/i,
		/\bnonce\b/i,
		/api_?key/i,
		/client_secret/i,
		/x-amz-/i,
		/session_?id/i,
		/csrf/i,
		/secret/i,
		/password/i,
		/passwd/i,
		/bearer/i,
		/\bjwt\b/i,
		/\botp\b/i,
		/\btotp\b/i,
		/credit_?card/i,
		/card_?number/i,
		/\bcvv\b/i,
		/\bssn\b/i
	];

	function containsSecret(str) {
		if (!str || typeof str !== 'string') return false;
		return SECRET_PATTERNS.some(p => p.test(str));
	}

	function isSecretField(elem) {
		if (!elem) return false;
		if (elem.type === 'password') return true;
		const name = (elem.name || '').toLowerCase();
		const id = (elem.id || '').toLowerCase();
		const rawType = (elem.type || '').toLowerCase();
		const autocomplete = (elem.autocomplete || '').toLowerCase();
		const placeholder = (elem.placeholder || '').toLowerCase();
		const ariaLabel = (elem.getAttribute('aria-label') || '').toLowerCase();

		return SECRET_PATTERNS.some(p =>
			p.test(name) || p.test(id) || p.test(rawType) || p.test(autocomplete) || p.test(placeholder) || p.test(ariaLabel)
		);
	}

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
			ref = "@e" + refCounter;
			refs[ref] = el;
			backendId = refCounter;
			refMap[ref] = backendId;
			refCounter++;
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
			if (isSecretField(el) || containsSecret(String(el.value))) {
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
	evalResp, err := d.evalRaw(ctx, client, fmt.Sprintf(script, params.InteractiveOnly, params.Compact))
	if err != nil {
		return nil, fmt.Errorf("evaluate snapshot script: %w", err)
	}
	var val struct {
		Root []*protocol.AXNode `json:"root"`
		RefMap map[string]int64 `json:"refMap"`
		Title string `json:"title"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(evalResp, &val); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot output: %w", err)
	}
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
	if err := json.Unmarshal(val, &res); err != nil {
		return err
	}

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
	const nativeSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set ||
	                     Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')?.set;
	if (nativeSetter) {
		nativeSetter.call(el, txt);
	} else {
		el.value = txt;
	}
	el.dispatchEvent(new Event('input', { bubbles: true }));
	el.dispatchEvent(new Event('change', { bubbles: true }));
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
	if err := json.Unmarshal(val, &res); err != nil {
		return err
	}

	if res.Stale {
		return protocol.ErrStaleRef
	}
	if res.NotFound {
		return protocol.ErrTargetNotFound
	}
	return nil
}


// Scroll scrolls the active page using mouse wheel or window.scrollTo.
func (d *CDPDriver) Scroll(ctx context.Context, params protocol.ScrollParams) error {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return err
	}
	deltaY := params.DeltaY
	deltaX := params.DeltaX
	if params.Direction == "top" {
		_, err := client.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "window.scrollTo(0, 0)"})
		return err
	} else if params.Direction == "bottom" {
		_, err := client.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "window.scrollTo(0, document.body.scrollHeight)"})
		return err
	} else if params.Direction == "up" {
		deltaY = -600
	} else if params.Direction == "down" {
		deltaY = 600
	} else if deltaY == 0 && deltaX == 0 {
		deltaY = 600
	}
	wheel := map[string]any{
		"type":   "mouseWheel",
		"x":      500,
		"y":      400,
		"deltaX": deltaX,
		"deltaY": deltaY,
	}
	_, err = client.Call(ctx, "Input.dispatchMouseEvent", wheel)
	return err
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
	if err := json.Unmarshal(val, &res); err != nil {
		return err
	}

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
			"type":           "char",
			"text":           keyText,
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
	if err := json.Unmarshal(val, &res); err != nil {
		return err
	}

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
	if err := json.Unmarshal(val, &res); err != nil {
		return err
	}

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

	val, err := evaluate(ctx, client, params.Expression, 0, false)
	if err != nil {
		return &protocol.EvalResult{Error: err.Error()}, nil
	}
	if len(val) == 0 {
		val = json.RawMessage("null")
	}
	return &protocol.EvalResult{Value: val}, nil
}

func isolatedWorld(ctx context.Context, client *CDPClient, worldName string) (int64, error) {
	treeResp, err := client.Call(ctx, "Page.getFrameTree", nil)
	if err != nil {
		return 0, err
	}

	var treeOut struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := json.Unmarshal(treeResp, &treeOut); err != nil {
		return 0, err
	}
	frameID := treeOut.FrameTree.Frame.ID
	if frameID == "" {
		return 0, errors.New("main frame ID not found")
	}

	createCall := map[string]any{
		"frameId":              frameID,
		"worldName":            worldName,
		"grantUniversalAccess": true,
	}
	createResp, err := client.Call(ctx, "Page.createIsolatedWorld", createCall)
	if err != nil {
		return 0, err
	}

	var createOut struct {
		ExecutionContextID int64 `json:"executionContextId"`
	}
	if err := json.Unmarshal(createResp, &createOut); err != nil {
		return 0, err
	}

	return createOut.ExecutionContextID, nil
}

func (d *CDPDriver) evalRaw(ctx context.Context, client *CDPClient, expression string) (json.RawMessage, error) {
	contextID, _ := isolatedWorld(ctx, client, "tether-automation")
	return evaluate(ctx, client, expression, contextID, false)
}

func evaluate(ctx context.Context, client *CDPClient, expression string, contextID int64, userGesture bool) (json.RawMessage, error) {
	call := map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
		"userGesture": userGesture,
	}
	if contextID > 0 {
		call["contextId"] = contextID
	}
	resp, err := client.Call(ctx, "Runtime.evaluate", call)
	if err != nil {
		return nil, err
	}

	var out struct {
		Result struct {
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
				var ok bool
				if err := json.Unmarshal(val, &ok); err != nil {
					return err
				}
				if ok {
					return nil
				}
			}
		}
	}
}

// Screenshot captures a viewport or full page at one image pixel per CSS pixel.
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
		"format":                format,
		"captureBeyondViewport": params.FullPage,
	}
	if params.Quality > 0 && format == "jpeg" {
		call["quality"] = params.Quality
	}
	type screenshotArea struct {
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
		Scale  float64 `json:"scale"`
	}
	raw, err := client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "({x:scrollX,y:scrollY,width:innerWidth,height:innerHeight,scale:1/devicePixelRatio})",
		"returnByValue": true,
	})
	if err != nil {
		return nil, fmt.Errorf("get screenshot viewport: %w", err)
	}
	var viewport struct {
		Result struct {
			Value screenshotArea `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &viewport); err != nil {
		return nil, fmt.Errorf("unmarshal screenshot viewport: %w", err)
	}
	clip := viewport.Result.Value
	raw, err = client.Call(ctx, "Page.getLayoutMetrics", nil)
	if err != nil {
		return nil, fmt.Errorf("get screenshot dimensions: %w", err)
	}
	var metrics struct {
		Content  screenshotArea `json:"cssContentSize"`
		Viewport struct {
			Zoom float64 `json:"zoom"`
		} `json:"cssVisualViewport"`
	}
	if err := json.Unmarshal(raw, &metrics); err != nil {
		return nil, fmt.Errorf("unmarshal screenshot dimensions: %w", err)
	}
	if params.FullPage {
		metrics.Content.Scale = clip.Scale
		clip = metrics.Content
	}
	if clip.Width <= 0 || clip.Height <= 0 || clip.Scale <= 0 || metrics.Viewport.Zoom <= 0 {
		return nil, fmt.Errorf("screenshot dimensions and scale must be positive")
	}
	// CDP clip coordinates are device-independent pixels, not CSS pixels.
	clip.X *= metrics.Viewport.Zoom
	clip.Y *= metrics.Viewport.Zoom
	clip.Width *= metrics.Viewport.Zoom
	clip.Height *= metrics.Viewport.Zoom
	call["clip"] = clip

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
	pages, _ := d.discoverTargets(ctx)
	targetCount := len(pages)

	d.mu.RLock()
	activeTarget := d.activeTarget
	d.mu.RUnlock()

	return &protocol.StatusResult{
		Connected:      targetCount > 0,
		ActiveTargetID: activeTarget,
		TargetCount:    targetCount,
		Version:        protocol.Version,
		Mode:           "managed",
		DaemonUptimeS:  int64(time.Since(d.startTime).Seconds()),
	}, nil
}

// StartReview activates the in-page Tether Review inspector on the target tab.
func (d *CDPDriver) StartReview(ctx context.Context, params protocol.ReviewParams) error {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return err
	}
	rc := &ReviewController{}
	return rc.Start(ctx, client)
}

// GetReviewNotes returns all pinned review notes from the target tab.
func (d *CDPDriver) GetReviewNotes(ctx context.Context, params protocol.ReviewParams) ([]*protocol.ReviewNote, error) {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return nil, err
	}
	rc := &ReviewController{}
	return rc.GetNotes(ctx, client)
}

// ClearReview clears all review notes and badge pins from the target tab.
func (d *CDPDriver) ClearReview(ctx context.Context, params protocol.ReviewParams) error {
	client, _, err := d.getTargetClient(params.TargetID)
	if err != nil {
		return err
	}
	rc := &ReviewController{}
	return rc.Clear(ctx, client)
}
