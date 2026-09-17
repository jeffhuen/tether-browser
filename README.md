# tether-browser

Remote-to-local browser bridge for AI coding agents across Herdr, tmux, and SSH sessions.

No pixel streaming. No VNC. No cloud browser subscription.

---

## 1. The Core Problem

When developers run coding agents on remote Linux servers or cloud VMs, browser automation breaks down:

* **Commercial web services mandate human 2FA**: Portals like AWS, Google, Shopify, and enterprise SaaS require two-factor authentication, TOTP codes, and Passkeys.
* **Server-side pixel streaming fails over remote networks**: Tools like VNC and Sixel stream raw or compressed graphical frames over SSH. At 1080p, raw frames produce up to 250 MB/s, saturating network bandwidth and introducing 150–300ms of input latency.
* **Headless cloud servers lack authentication hardware**: A remote Linux box in a cloud data center cannot access your laptop's Touch ID sensor, Windows Hello camera, 1Password autofill, or Bluetooth proximity for phone passkeys.

---

## 2. The Solution: Remote-to-Local Bridge

`tether-browser` moves browser execution to the developer's local machine where authenticators and sessions already live:

```text
┌────────────────────────────────────────────────────────┐           SSH Reverse Tunnel (-R 9333:localhost:9333)           ┌────────────────────────────────────────────────────────┐
│                      LOCAL MAC / PC                    │ ◄─────────────────────────────────────────────────────────────► │                     REMOTE SERVER                      │
│                                                        │                                                                 │                    (Linux / Cloud)                     │
│  ┌──────────────────────────────────────────────────┐  │   Control Channel: 127.0.0.1:9333                               │                                                        │
│  │   Active Chrome / Brave / Edge Window            │  │   App Tunnel: Origin-Preserving Proxy                           │  ┌──────────────────────────────────────────────────┐  │
│  │   • Native 120Hz Retina rendering                │  │                                                                 │  │   AI Coding Agent (Claude, Codex, OMP, Herdr)     │  │
│  │   • Touch ID, Passkeys, 1Password autofill       │  │                                                                 │  │                                                  │  │
│  │   • Dedicated "Tether" Tab Group                 │  │                                                                 │  │   $ tether tabs                                  │  │
│  │   • In-Page Review Inspector & Pins              │  │                                                                 │  │   $ tether snapshot -i                           │  │
│  └────────────────────────▲─────────────────────────┘  │                                                                 │  │   $ tether click @e14                            │  │
│                           │ Chrome Debugger & Tabs     │                                                                 │  │   $ tether review send                           │  │
│  ┌────────────────────────┴─────────────────────────┐  │                                                                 │  └──────────────────────────┬───────────────────────┘  │
│  │   Tether Extension (Manifest V3)                 │  │                                                                 │                             │                          │
│  │   • Dedicated popup menu with recent hosts       │  │                                                                 │                             │                          │
│  │   • chrome.tabGroups, chrome.debugger            │  │                                                                 │                             │                          │
│  └────────────────────────▲─────────────────────────┘  │                                                                 │                             │                          │
│                           │ Native Messaging (stdio)   │                                                                 │                             │                          │
│  ┌────────────────────────┴─────────────────────────┐  │                                                                 │                             │                          │
│  │   tether native-host                             │  │                                                                 │                             │                          │
│  │   • Unix domain socket: bridge.sock              │  │        JSON Commands (~200 bytes)                               │                             │                          │
│  └────────────────────────▲─────────────────────────┘  │ ◄───────────────────────────────────────────────────────────────┤  ┌──────────────────────────▼───────────────────────┐  │
│                           │ Unix Socket IPC            │                                                                 │  │   tether CLI                                     │  │
│  ┌────────────────────────┴─────────────────────────┐  │                                                                 │  │   • agent-browser syntax                         │  │
│  │   tether daemon                                  │  │ ├───────────────────────────────────────────────────────────────►  │   • Multi-tab routing (--tab <id>)              │  │
│  │   • Sole owner of TCP port 9333                  │  │        DOM Accessibility Snapshots & Review Reports             │  │   • Background session broker                    │  │
│  │   • Mode A forward proxy                         │  │                                                                 │  └──────────────────────────────────────────────────┘  │
│  └──────────────────────────────────────────────────┘  │                                                                 │                                                        │
└────────────────────────────────────────────────────────┘                                                                 └────────────────────────────────────────────────────────┘
```

1. **Client Execution**: Google Chrome runs on your laptop. Passwords, session cookies, Touch ID, and 1Password autofill execute locally.
2. **Reverse SSH Tunnel**: An SSH tunnel (`-R 9333:localhost:9333`) forwards commands from the remote coding agent to your workstation. No open firewall ports required.
3. **Command-Over-Wire**: The remote agent sends ~200-byte JSON requests (`browser.open`, `browser.snapshot`, `browser.click`).
4. **Structured Results**: The client returns compact ~20KB accessibility snapshots with `@eN` references. Zero video streaming bandwidth, 0ms input lag.

---

## 3. Quick Start

### Step 1: Install `tether` on your Mac / PC

```bash
curl -fsSL https://raw.githubusercontent.com/jeffhuen/tether-browser/main/install.sh | bash
tether extension install
```

### Step 2: Load the Extension in Chrome

1. Open `chrome://extensions` in Chrome, Brave, or Edge.
2. Toggle **Developer mode** on (top-right corner).
3. Click **Load unpacked** (top-left) and select the `packages/extension` folder from your cloned repository.
4. The extension loads with deterministic ID `kaloekddddlgghmifoaapnhekggjcggn`.

### Step 3: Connect to your Remote Server

In your terminal:
```bash
tether connect user@remote-server
```
*(Or click the **Tether extension icon** in your Chrome toolbar, enter `user@host`, and click **Connect** directly from the browser!)*

### Step 4: Run Automation on your Remote Server

```bash
# Navigate to a URL (creates and manages tabs inside a blue "Tether" group)
tether open https://example.com

# List all open browser tabs
tether tabs

# Inspect page accessibility tree with @e1, @e2 element references
tether snapshot -i

# Click an element by reference or coordinates
tether click @e1

# Fill form fields
tether fill @e2 "user@example.com"

# Take high-resolution desktop screenshot
tether screenshot output.png
```

---

## 4. Multi-Tab Management & Tab Groups

Tether organizes all automated tabs inside a dedicated blue **Tether** tab group in your browser:

* **Live Tab Discovery**: `tether tabs` queries the browser in real time to show all open tabs, titles, URLs, and active target:
  ```text
  Open Tabs (2):
   * [1] "Application Dashboard"
         URL: https://app.example.com/dashboard
         ID:  430852225
     [2] "Overview Dashboard"
         URL: https://app.example.com/overview
         ID:  430852222
  ```
* **Switch Active Tab**:
  ```bash
  tether switch <tabId>
  ```
* **Direct Tab Targeting**: Run any command against a specific tab without changing active view:
  ```bash
  tether snapshot --tab <tabId> -i
  tether click --tab <tabId> @e3
  tether eval --tab <tabId> "document.title"
  ```

---

## 5. In-Page Developer Review Inspector

Tether includes an in-page element inspection overlay that generates structured, Orca-grade Markdown design feedback reports for AI coding agents:

```bash
tether review start    # Arm inspection overlay on the page
tether review list     # List pinned notes and comments
tether review send     # Export full Markdown report with computed styles and HTML
tether review clear    # Clear annotations
```

### Report Output Structure

When notes are pinned and exported, Tether produces an actionable report with component metadata, bounding boxes, and computed CSS:

```markdown
## Design Feedback: Application Dashboard

**URL:** https://app.example.com/dashboard
**Viewport:** 2602x1258

### 1. a - "Products"
**Intent:** design_fix
**Selector:** `li.hs-menu > a`
**Location:** `header > div.hdrBtm > div.page-center > div.hdrMenu > ul > li > a`
**Bounds:** viewport x=120, y=40, 90x24
**Classes:** `nav-link`
**Text:** "Products"
**Nearby text:**
- "Effectron Corp"
- "Overview dashboard"
**Computed styles:**
- display: inline-flex
- width: 90px
- height: 24px
- color: rgb(33, 57, 76)
- fontFamily: Inter, sans-serif
- fontSize: 14px
- fontWeight: 500
**HTML:**
```html
<a class="nav-link" href="/products">Products</a>
```
**Feedback:** Align baseline with search input on desktop
```

---

## 6. Architecture & Security Invariants

1. **Single TCP Port Owner**: `tether daemon` is the exclusive owner of TCP port `127.0.0.1:9333`.
2. **Private Unix Domain Socket**: `tether native-host` communicates over a user-scoped Unix socket (`/tmp/tether-<uid>/bridge.sock`, mode `0700` directory, `0600` socket) with stale-probe unlinking.
3. **No Unrequested Browser Spawning**: If the Chrome extension is connected, Tether operates through official extension APIs (`chrome.debugger`, `chrome.tabGroups`). Subprocess Chrome is reserved strictly as an offline fallback.
4. **Focus Emulation**: The extension calls `Emulation.setFocusEmulationEnabled({ enabled: true })`, ensuring synthesized clicks and keyboard inputs are processed by Chromium even on background or unselected tabs.

---

## 7. Package Layout

```text
tether-browser/
├── cmd/
│   └── tether/               # Main CLI entrypoint (connect, daemon, extension, native-host)
├── packages/
│   ├── protocol/             # JSON-RPC schemas, action types, review report formatting
│   ├── client/               # Native host bridge, extension driver, CDP driver, proxy
│   ├── cli/                  # Remote agent CLI, terminal formatting, broker client
│   └── extension/            # Manifest V3 extension (popup UI, tab groups, review overlay)
└── install.sh                # Universal one-line installer
```

---

*Tether Browser | Remote-to-local browser bridge for AI agents*
