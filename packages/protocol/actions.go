package protocol

// Standard method names for browser automation over JSON-RPC.
const (
	// Version is the current release version of tether-browser.
	Version = "0.1.16"

	MethodOpen        = "browser.open"
	MethodSnapshot    = "browser.snapshot"
	MethodClick       = "browser.click"
	MethodDblClick    = "browser.dblclick"
	MethodFill        = "browser.fill"
	MethodType        = "browser.type"
	MethodPress       = "browser.press"
	MethodHover       = "browser.hover"
	MethodFocus       = "browser.focus"
	MethodEval        = "browser.eval"
	MethodWait        = "browser.wait"
	MethodScreenshot  = "browser.screenshot"
	MethodPDF         = "browser.pdf"
	MethodClose       = "browser.close"
	MethodStatus      = "browser.status"
	MethodTabList     = "browser.tab.list"
	MethodTabSwitch   = "browser.tab.switch"
	MethodAttach      = "browser.attach"
	MethodDetach      = "browser.detach"
	MethodReviewStart = "browser.review.start"
	MethodReviewList  = "browser.review.list"
	MethodReviewClear = "browser.review.clear"
	MethodReviewSend  = "browser.review.send"
)

// TargetID is an opaque identifier representing an active browser tab.
type TargetID string

// TabInfo describes an open browser tab.
type TabInfo struct {
	ID     TargetID `json:"id"`
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	Active bool     `json:"active"`
}

// TabListResult returns all discovered open tabs in the browser.
type TabListResult struct {
	Tabs     []TabInfo `json:"tabs"`
	ActiveID TargetID  `json:"activeId"`
}

// TabSwitchParams specifies a target tab to focus and switch to.
type TabSwitchParams struct {
	TargetID TargetID `json:"targetId"`
}

// OpenParams specifies the parameters for navigating to a URL.
type OpenParams struct {
	URL       string `json:"url"`
	TimeoutMs int    `json:"timeoutMs,omitempty"`
	WaitUntil string `json:"waitUntil,omitempty"` // "load", "domcontentloaded", "networkidle"
}

// OpenResult returns the newly opened or focused target details.
type OpenResult struct {
	TargetID TargetID `json:"targetId"`
	URL      string   `json:"url"`
	Title    string   `json:"title"`
}

// ClickParams specifies an element to click by selector or @eN ref.
type ClickParams struct {
	TargetID   TargetID `json:"targetId,omitempty"`
	Selector   string   `json:"selector"`
	Button     string   `json:"button,omitempty"` // "left", "right", "middle"
	ClickCount int      `json:"clickCount,omitempty"`
	TimeoutMs  int      `json:"timeoutMs,omitempty"`
}

// ActionResult is a generic confirmation for commands that have no structured return data.
type ActionResult struct {
	OK bool `json:"ok"`
}

// FillParams clears and fills a form field.
type FillParams struct {
	TargetID   TargetID `json:"targetId,omitempty"`
	Selector   string   `json:"selector"`
	Text       string   `json:"text"`
	ClearFirst bool     `json:"clearFirst,omitempty"`
	TimeoutMs  int      `json:"timeoutMs,omitempty"`
}

// TypeParams types characters sequentially with optional delay.
type TypeParams struct {
	TargetID  TargetID `json:"targetId,omitempty"`
	Selector  string   `json:"selector,omitempty"`
	Text      string   `json:"text"`
	DelayMs   int      `json:"delayMs,omitempty"`
	TimeoutMs int      `json:"timeoutMs,omitempty"`
}

// PressParams sends a keyboard key event (Enter, Tab, Control+a).
type PressParams struct {
	TargetID TargetID `json:"targetId,omitempty"`
	Key      string   `json:"key"`
}

// HoverParams moves the cursor over an element.
type HoverParams struct {
	TargetID  TargetID `json:"targetId,omitempty"`
	Selector  string   `json:"selector"`
	TimeoutMs int      `json:"timeoutMs,omitempty"`
}

// FocusParams focuses an element.
type FocusParams struct {
	TargetID TargetID `json:"targetId,omitempty"`
	Selector string   `json:"selector"`
}

// EvalParams runs a JavaScript expression in the page context.
type EvalParams struct {
	TargetID     TargetID `json:"targetId,omitempty"`
	Expression   string   `json:"expression"`
	AwaitPromise bool     `json:"awaitPromise,omitempty"`
}

// EvalResult returns the evaluated JavaScript output.
type EvalResult struct {
	Value any    `json:"value"`
	Error string `json:"error,omitempty"`
}

// WaitParams pauses execution until an element or time condition is satisfied.
type WaitParams struct {
	TargetID   TargetID `json:"targetId,omitempty"`
	Selector   string   `json:"selector,omitempty"`
	DurationMs int      `json:"durationMs,omitempty"`
	State      string   `json:"state,omitempty"` // "visible", "hidden", "attached"
	TimeoutMs  int      `json:"timeoutMs,omitempty"`
}

// ScreenshotParams captures an image of the viewport or element.
type ScreenshotParams struct {
	TargetID  TargetID `json:"targetId,omitempty"`
	FullPage  bool     `json:"fullPage,omitempty"`
	Format    string   `json:"format,omitempty"` // "png", "jpeg"
	Quality   int      `json:"quality,omitempty"`
	Selector  string   `json:"selector,omitempty"`
	Annotate  bool     `json:"annotate,omitempty"`
	TimeoutMs int      `json:"timeoutMs,omitempty"`
}

// ScreenshotResult returns the image bytes encoded as base64.
type ScreenshotResult struct {
	Base64 string `json:"base64"`
	Format string `json:"format"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// CloseParams closes a specific tab or all managed tabs.
type CloseParams struct {
	TargetID TargetID `json:"targetId,omitempty"`
	CloseAll bool     `json:"closeAll,omitempty"`
}

// StatusParams queries daemon connection status.
type StatusParams struct{}

// StatusResult returns daemon and browser connectivity information.
type StatusResult struct {
	Connected      bool     `json:"connected"`
	ActiveTargetID TargetID `json:"activeTargetId,omitempty"`
	TargetCount    int      `json:"targetCount"`
	Version        string   `json:"version"`
	Mode           string   `json:"mode"` // "managed", "daily-chrome", "extension"
	DaemonUptimeS  int64    `json:"daemonUptimeS"`
}

// ReviewParams specifies target and options for Tether Review.
type ReviewParams struct {
	TargetID TargetID `json:"targetId,omitempty"`
}

// ReviewListResult returns active pinned review notes.
type ReviewListResult struct {
	Notes    []*ReviewNote `json:"notes"`
	PageURL  string        `json:"pageUrl"`
	Viewport string        `json:"viewport"`
}

// ReviewSendResult returns the formatted markdown design feedback report.
type ReviewSendResult struct {
	Markdown string        `json:"markdown"`
	Notes    []*ReviewNote `json:"notes"`
	PageURL  string        `json:"pageUrl"`
}
