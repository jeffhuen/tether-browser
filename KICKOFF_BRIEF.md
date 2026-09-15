# Welcome to tether-browser

## The Mission
You are the lead systems architect and engineer for `tether-browser`.
Your objective is to build a high-performance, low-latency remote browser automation bridge for AI coding agents across Herdr, tmux, and SSH sessions.

---

## 1. The Core Problem
When developers run coding agents on remote Linux servers, browser automation faces three critical failures:

1. **Commercial services mandate human authentication**: Services like AWS, Cloudflare, and Shopify require interactive login, TOTP codes, and Passkeys.
2. **Server-side pixel streaming fails over remote networks**: Tools like VNC, Sixel, and video streamers transmit raw graphical frames over SSH. A 1080p stream consumes up to 250 MB per second. It introduces 150 to 300 milliseconds of latency and degrades display quality.
3. **Headless servers lack local authenticators**: A remote data center server cannot access your Touch ID sensor, Windows Hello camera, 1Password vault, or Bluetooth proximity for phone passkeys.

---

## 2. The Discovery: How Orca Solved It
Inspection of Orca's open-source runtime (`stablyai/orca` in `/opt/orca/1.4.201/squashfs-root/resources/app.asar`) revealed the solution:

**The browser does not run on the remote server. The browser runs locally on the developer workstation.**

In Orca's architecture:
* Browser tabs use **Client Placement** (`kind: 'client'`).
* Chromium runs locally on the user laptop. It uses local GPU acceleration at native 60Hz or 120Hz display refresh rates.
* Passwords, cookies, Touch ID credentials, and 1Password autofill execute natively in the local session.
* The remote Linux server sends only compact JSON commands (`browser.snapshot`, `browser.click`, `browser.eval`) over a network connection.
* The local client returns compact JSON accessibility trees and element coordinates.
* Local pointer movement has zero network lag. Remote automation commands execute in a single round trip.

---

## 3. Product Strategy: Modular V1 with Optional V2 Extension

To avoid installation friction and eliminate store dependencies, `tether-browser` follows a two-phase architecture:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                    V1 CORE ARCHITECTURE (ZERO EXTENSION)                    │
│ • Single standalone 12 MB Go binary (tether)                                │
│ • Connects directly to Chrome via CDP (Managed profile or Chrome 144+)      │
│ • Zero Chrome Web Store dependency, zero developer mode warnings           │
│ • Injects review overlay into isolated execution world (worldName)          │
│ • Full local GPU acceleration, password managers, and biometrics           │
├─────────────────────────────────────────────────────────────────────────────┤
│                    V2 COMPANION EXTENSION (OPTIONAL ENHANCEMENT)            │
│ • Pluggable via the daemon's BrowserDriver interface                        │
│ • Auto-detected when installed; requires zero daemon reconfiguration        │
│ • Delivers measurable, substantial value:                                   │
│   1. Native Chrome Side Panel: Dedicated review notes tray and live log     │
│   2. Navigation Immunity: Review notes survive page crashes and reloads     │
│   3. Per-Tab Security Scoping: Agent sees only explicitly attached tabs    │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 4. The `tether-browser` Architecture

```text
┌────────────────────────────────────────┐         Tailscale or SSH Forward Channels    ┌────────────────────────────────────────┐
│               LOCAL CLIENT             │ ◄──────────────────────────────────────────► │             REMOTE SERVER              │
│            (macOS or Windows)          │                                              │            (Linux or Cloud)            │
│                                        │   Control Channel: 127.0.0.1:9333            │                                        │
│  ┌──────────────────────────────────┐  │   App Tunnel: CONNECT & ReverseProxy         │  ┌──────────────────────────────────┐  │
│  │    Local Chrome Window           │  │                                              │  │       Herdr or Tmux Session      │  │
│  │   • Full Retina and 4K display   │  │                                              │  │                                  │  │
│  │   • 120Hz native rendering       │  │                                              │  │  $ tether snapshot -i            │  │
│  │   • Touch ID, 1Password, Passkeys│  │                                              │  │  $ tether click @e14             │  │
│  │   • Tether Review visual badges  │  │                                              │  │  $ tether review send            │  │
│  └──────────────────▲───────────────┘  │                                              │  └──────────────────┬───────────────┘  │
│                     │                  │                                              │                     │                  │
│  ┌──────────────────┴───────────────┐  │        JSON Commands (200 bytes)             │                     │                  │
│  │          tether daemon           │ ◄───────────────────────────────────────────────┤  ┌──────────────────▼───────────────┐  │
│  │   • Lightweight Go binary        │                                                 │  │            tether CLI            │  │
│  │   • Modular BrowserDriver        │ ├───────────────────────────────────────────────►  │   • agent-browser syntax         │  │
│  │   • Forward proxy + CONNECT      │  │        DOM Snapshots and Review Markdown     │  │   • Persistent session broker    │  │
│  └──────────────────────────────────┘  │                                              │  └──────────────────────────────────┘  │
└────────────────────────────────────────┘                                              └────────────────────────────────────────┘
```

---

## 5. Modular Client Driver Architecture

`packages/client` defines a modular `BrowserDriver` interface. The daemon uses direct CDP by default. It switches to the extension driver automatically if the companion extension connects:

```go
type BrowserDriver interface {
    OpenTab(ctx context.Context, url string) (TargetID, error)
    CloseTab(ctx context.Context, target TargetID) error
    Snapshot(ctx context.Context, target TargetID, interactiveOnly bool) (*SnapshotResult, error)
    Click(ctx context.Context, target TargetID, selector string) error
    Fill(ctx context.Context, target TargetID, selector, text string) error
    Eval(ctx context.Context, target TargetID, script string) (any, error)
    StartReview(ctx context.Context, target TargetID) error
    GetReviewNotes(ctx context.Context, target TargetID) (*ReviewPayload, error)
}
```

* **`CDPDriver` (V1 Implementation)**: Connects to Chrome over raw WebSocket CDP on port 9222 or `--remote-debugging-pipe`.
* **`ExtensionDriver` (V2 Implementation)**: Communicates with the optional companion extension over Chrome Native Messaging (`com.tether.browser`).

---

## 6. Application Routing: Mode A Origin-Preserving Proxy

Chromium features documented, cross-platform proxy bypass syntax that allows subtracting loopback from default bypass rules: `--proxy-bypass-list="<-loopback>"`.

### How Mode A Works
1. **Managed Chrome Launch**:
   Chrome launches with `--proxy-server="http://127.0.0.1:<daemon-port>"` and `--proxy-bypass-list="<-loopback>"`.
2. **Proxy Request Dispatching**:
   * **Plain HTTP (`GET http://localhost:3000/`)**: The daemon handles the request via Go's streaming reverse proxy, forwarding it over Tailscale or SSH to the remote dev server.
   * **WebSockets & HTTPS (`CONNECT localhost:3000`)**: Chromium tunnels all WebSockets through proxies using `HTTP CONNECT`. The daemon intercepts the `CONNECT` request, dials the remote dev server socket, and splices the bidirectional TCP stream (`io.Copy`).
   * **External Traffic**: External HTTPS sites tunnel through CONNECT to the real internet with zero TLS tampering.
3. **Guarantees**:
   * The browser URL remains `http://localhost:3000`.
   * Google OAuth, Passkeys, WebAuthn, cookies, and backend redirects work identically to local development.
   * Local port 3000 on the developer's Mac is never touched.

---

## 7. Multi-Framework Tether Review Architecture

Web applications use many technologies beyond React. Tether Review works reliably on Elixir Phoenix LiveView, Astro, Svelte, SolidJS, Vue, Nuxt, Django, Rails, and plain HTML.

Tether Review uses a two-tier inspection model:

### Tier 1: Universal DOM Baseline (Works on Every Website)
Every browser page produces this data regardless of backend language or JavaScript framework:
* **Element Semantics**: ARIA role (`button`, `dialog`, `navigation`) and accessible name.
* **Structural Breadcrumbs**: Unique CSS selector (e.g. `form#payment button.btn-primary`) and full DOM path.
* **Visual Properties**: Viewport bounding box and 16 computed CSS styles (display, padding, margin, colors, typography).
* **Surrounding Context**: Nearby text content, sibling elements, and cleaned outer HTML snippets.
* **Code Search Value**: Even without framework metadata, an AI coding agent uses the selector, classes, and text to locate the exact template file in any codebase using grep.

### Tier 2: Progressive Framework Sniffing
When specific frameworks run, Tether Review enriches the baseline with framework-specific clues:
* **Elixir Phoenix & LiveView**: Sniffs `phx-click`, `phx-change`, `phx-view`, and `phx-component`.
* **Astro**: Sniffs `<astro-island component-url="..." component-export="...">`.
* **Svelte & SvelteKit**: Sniffs `__svelte_meta` in development builds for source filenames and line numbers.
* **Vue & Nuxt**: Inspects `el.__vueParentComponent.type.__name`.
* **HTMX**: Sniffs `hx-get`, `hx-post`, `hx-target`, and `hx-swap`.
* **React**: Uses `react-grab/primitives` for component hierarchy and `fiber._debugSource` or `_debugStack`.

---

## 8. Package Plan

```text
tether-browser/
├── README.md                 # Architectural specification and usage
├── KICKOFF_BRIEF.md          # Architectural decisions and mission brief
├── packages/
│   ├── protocol/             # Go types, JSON-RPC, zstd framing, diffing, and review schemas
│   ├── client/               # Go desktop daemon (CDP bridge, proxy, embedded review scripts)
│   ├── cli/                  # Go remote CLI (agent-browser compatible, persistent broker)
│   └── extension/            # Optional Manifest V3 companion extension (Side Panel, tab scoping)
└── scripts/
```
