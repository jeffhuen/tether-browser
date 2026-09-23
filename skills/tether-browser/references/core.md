# Core Command Reference

`tether` uses `agent-browser` command syntax. It sends JSON-RPC requests to `127.0.0.1:9333`, which routes to Google Chrome on the developer workstation.
---

## 1. Page Navigation

### `tether open <url>`
Navigates the active tab to the specified URL. If no tab exists in the Tether group, a new tab is created.

```bash
# Use a full HTTP(S) URL
tether open https://example.com

# Extension mode: open a clean, opaque tab
tether open ""
```

In extension mode, an empty quoted URL creates a new tab without closing existing tabs. Use it to recover from an old `about:blank` target, which the extension now blocks. Direct CDP mode still accepts `about:blank`.

---

## 2. Inspecting Pages: The Snapshot System

`tether snapshot` extracts the accessibility tree with `@eN` references.

### `tether snapshot`
Options:
* `-i`, `--interactive`: Filter to interactive elements (buttons, links, inputs, tabs).
* `-c`, `--compact`: Exclude empty structural containers.
* `-d <n>`, `--depth <n>`: Limit traversal depth.
* `-s <selector>`, `--selector <selector>`: Scope snapshot to a CSS selector.
* `--json`: Output structured JSON.
# Preferred: compact interactive tree
tether snapshot -i

# Scope to a modal or form
tether snapshot -i -s "form#login"

# Machine-readable JSON output
tether snapshot -i --json
```

### Snapshot Structure Example

```text
[@e1] RootWebArea "Application Dashboard"
[@e2] navigation "Main"
  [@e3] link "Overview"
  [@e4] link "Settings"
[@e5] main
  [@e6] heading "System Status"
  [@e7] textbox "Filter results..."
  [@e8] button "Search"
  [@e9] table "Data Table"
```

---

## 3. Interacting with Elements

### `tether click <@ref or selector>`
Clicks the center of the element bounding box.

```bash
tether click @e3
tether click "button.submit-btn"
```

### `tether dblclick <@ref or selector>`
Double-clicks an element.

```bash
tether dblclick @e10
```

### `tether fill <@ref or selector> <text>`
Focuses the element, clears existing text, and types the new text.

```bash
tether fill @e8 "search query"
```

### `tether type <@ref or selector> <text>`
Appends text to the focused element without clearing existing text.

```bash
tether type @e8 " filter"
```

### `tether press <key>`
Sends a discrete keyboard event. Supports `Enter`, `Tab`, `Escape`, `Backspace`, `Delete`, `Home`, `End`, and arrow keys.

```bash
tether press Enter
tether press Tab
tether press Escape
```

### `tether hover <@ref or selector>`
Moves the mouse cursor over an element to trigger hover states or tooltips.

```bash
tether hover @e3
```

### `tether focus <@ref or selector>`
Focuses an element without clicking it.

```bash
tether focus @e8
```
---


### `tether scroll [direction or deltaY]`
Scrolls the active tab using mouse wheel emulation or window positioning. Accepts `up`, `down`, `top`, `bottom`, or a numerical pixel delta (default: `down`).

```bash
# Scroll down by one viewport
tether scroll down
tether scroll

# Scroll up
tether scroll up

# Jump to top or bottom of document
tether scroll top
tether scroll bottom

# Scroll by specific pixel delta
tether scroll 450
```
## 4. Evaluation and Extraction

### `tether eval "<javascript>"`
Evaluates a JavaScript expression in the context of the page and prints the result.

```bash
# Get page title
tether eval "document.title"

# Check URL
tether eval "window.location.href"

# Query localStorage
tether eval "localStorage.getItem('auth_token')"

# Count table rows
tether eval "document.querySelectorAll('table tr').length"
```

---

## 5. Visual Capture

### `tether screenshot [path]`
Captures a PNG screenshot of the active tab. Pass `--full` to capture the entire scrollable page.

```bash
# Save viewport screenshot
tether screenshot /tmp/dashboard.png

# Capture entire scrollable page
tether screenshot --full /tmp/full-page.png
```
---

## 6. Waiting

### `tether wait <selector or duration>`
Waits until a selector or `@ref` matches, or until a duration passes. Durations accept integers in milliseconds (`1500`), or unit suffixes (`1.5s`, `500ms`). A selector wait stops after 25 seconds unless you pass `--timeout <ms>`.

```bash
# Wait 1.5 seconds
tether wait 1500
tether wait 1.5s

# Wait for element to become visible
tether wait "div.alert-success"
```

---

## 7. Closing

### `tether close [--all]`
Closes the active tab. Pass `--all` to close every tab in the Tether group.

```bash
tether close
tether close --all
```
