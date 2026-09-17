# tether-browser

Remote-to-local browser bridge for AI coding agents across Herdr, tmux, and SSH sessions.

No pixel streaming. No VNC. No cloud browser subscription.

---

## 1. Quick Start

### Step 1: Install `tether` on your workstation (Mac or PC)

```bash
curl -fsSL https://raw.githubusercontent.com/jeffhuen/tether-browser/main/install.sh | bash
tether extension install
```

### Step 2: Load the Extension in Chrome

1. Open `chrome://extensions` in Chrome, Brave, or Edge.
2. Toggle **Developer mode** on in the top-right corner.
3. Click **Load unpacked** and select the `packages/extension` folder from your repository clone.
4. The extension loads with deterministic ID `kaloekddddlgghmifoaapnhekggjcggn`.

### Step 3: Connect to your Remote Server

Run in your local terminal:
```bash
tether connect user@remote-server
```
Alternatively, click the **Tether extension icon** in your Chrome toolbar, enter `user@host`, and click **Connect**.

### Step 4: Install `tether` on your Remote Server

In your remote terminal (SSH session, Herdr pane, or cloud container):
```bash
curl -fsSL https://raw.githubusercontent.com/jeffhuen/tether-browser/main/install.sh | bash
```

### Step 5: Run Automation from your Remote Server

Run commands from your remote terminal:
```bash
# Open a URL in the Tether tab group
tether open https://example.com

# Inspect the interactive elements with @e1, @e2 references
tether snapshot -i

# Click elements and fill form fields
tether fill @e1 "user@example.com" && tether click @e2

# Capture a full-resolution Retina screenshot
tether screenshot output.png
```

---

## 2. How It Works: Remote-to-Local Bridge

`tether-browser` moves browser execution to the developer local machine where authenticators and sessions already live:

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
│                           │ Chrome Debugger & Tabs     │                                                                 │  │   $ tether fill @e2 "alice"                      │  │
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

1. **Client execution**: Google Chrome runs on your workstation. Passwords, session cookies, Touch ID, and 1Password autofill execute locally.
2. **Reverse SSH tunnel**: An SSH tunnel (`-R 9333:localhost:9333`) forwards commands from the remote coding agent to your workstation. No open firewall ports required.
3. **Commands over wire**: The remote agent sends 200-byte JSON requests (`browser.open`, `browser.snapshot`, `browser.click`).
4. **Structured results**: The client returns compact 20 KB accessibility trees with `@eN` references. No video bandwidth consumed; zero input lag.

---

## 3. Workflow Context: Who Needs This (and Who Doesn't)

* **Local development**: If your coding agent runs directly on your workstation with a desktop screen, you do not need Tether. Use local browser automation or open Chrome directly.
* **Remote development**: If your coding agent runs on a remote Linux server (AWS, Hetzner, dev containers, Herdr, or tmux over SSH), browser automation breaks down:
  * **Headless servers lack authenticators**: A remote data center server cannot access your Touch ID sensor, Windows Hello camera, 1Password vault, or phone Passkeys.
  * **Web services require human verification**: Portals like AWS console, Google Cloud, Shopify, and enterprise SaaS enforce two-factor authentication, TOTP codes, and Passkeys.

---

## 4. The Limits of Streaming Graphics and Pixel Data

Streaming graphical display frames and raw pixel data over remote networks introduces fundamental physical limits. Over remote Wi-Fi, mobile cellular connections (4G or 5G), or tethered hotspots, pixel-streaming approaches struggle with three constraints:

* **Bandwidth consumption**: Streaming 1080p graphical frames at 30 to 60 frames per second requires 10 to 30 Mbps. On metered cellular connections, video streaming exhausts data allowances in minutes.
* **Input latency**: Transmitting visual frames across the network adds 150 to 300 milliseconds of round-trip delay. Typing, clicking, and waiting for visual confirmation feel sluggish.
* **Compression artifacts**: Network packet loss and bitrate throttling degrade image sharpness, making small fonts and form inputs difficult to read.

### The Command-Over-Wire Alternative

Tether does not stream pixels or video frames. It sends lightweight ~200-byte JSON commands (`click`, `fill`, `open`) and returns structured ~20 KB accessibility trees:

| Metric | Remote Pixel Streaming (VNC, Video, Sixel) | Tether Browser Bridge |
|---|---|---|
| **Bandwidth** | 10 to 30 Mbps continuous video | ~200 bytes per command (~0 MB/s) |
| **Input Lag** | 150 to 300 ms video round-trip delay | 0 ms (renders on your local GPU at 120Hz) |
| **Cellular Hotspot Support** | No (drains data caps, stutters on packet loss) | Yes (minimal packet size, immune to jitter) |
| **Authentication** | Fails (remote server has no biometric hardware) | Native (uses your laptop Touch ID and 1Password) |
| **Server RAM Usage** | 500 MB to 2 GB per browser instance | 0 MB (browser runs on your laptop) |

---

## 5. Primary Use Cases

* **Cloud devboxes and remote agents**: Equip headless agents on AWS, Hetzner, GCP, or dev containers with full browser automation without configuring X11, VNC, or cloud browser subscriptions.
* **Cellular and travel workflows**: Run browser automation over high-latency cellular connections, train Wi-Fi, or mobile hotspots without video bandwidth degradation.
* **Protected enterprise portals**: Let coding agents test and interact with portals behind corporate SSO, Passkeys, YubiKeys, and hardware two-factor authentication.
* **UI and layout verification**: Let remote agents capture full-resolution Retina screenshots and test web forms in your real browser without modifying personal tabs.

---
## 6. Multi-Tab Management and Tab Groups

Tether organizes automated tabs in a blue **Tether** tab group in your browser:

* **Live Tab Discovery**: `tether tabs` queries the browser in real time to show all open tabs, titles, URLs, and active target:
  ```text
  Open Tabs (2):
   * [1] "Application Dashboard"
         URL: https://app.example.com/dashboard
         ID:  101
     [2] "Overview Dashboard"
         URL: https://app.example.com/overview
         ID:  102
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

## 7. Developer Visual Feedback

To capture visual feedback on a page, click **Inspect & Pin Notes** in the Tether popup. Pin notes on elements, then click **Copy to Clipboard** to paste structured component context into your agent prompt.

---

## 8. Interoperability with `agent-browser`

`tether` and `agent-browser` complement each other:

* **Use `tether`** for everyday browser automation, interactive workflows, and sites requiring Passkeys, 2FA, or Touch ID in your personal Chrome browser.
* **Use `agent-browser`** when your workflow requires specialized tools not in Tether, such as automated accessibility audits (`agent-browser a11y`), network request mocking (`agent-browser network`), or React Suspense profiling (`agent-browser react`).

### Network Latency Comparison (50ms SSH / 100ms RTT)

| Protocol | Action Execution | Round-Trips over SSH | Wire Latency |
|---|---|:---:|:---:|
| **Raw CDP (`agent-browser`)** | Remote agent issues 5 sequential CDP calls across the tunnel:<br>1. `DOM.resolveNode`<br>2. `DOM.getBoxModel`<br>3. `Input.dispatchMouseEvent` (move)<br>4. `Input.dispatchMouseEvent` (down)<br>5. `Input.dispatchMouseEvent` (up) | **5 RTTs** | **~500 ms** |
| **Tether RPC (`tether`)** | Remote agent sends **one** JSON request:<br>`{"method":"browser.click","params":{"ref":"@e1"}}`<br>All coordinate resolution and mouse events execute **locally on your client loopback at 0ms**. | **1 RTT** | **~100 ms** |

### Connecting Both

Forward Chrome's CDP port alongside Tether (`ssh -R 9333:localhost:9333 -R 9222:localhost:9222 user@server`) to run `agent-browser --cdp 9222` directly against Chrome whenever specialized devtools are needed.

---

## 9. Architecture and Security Invariants

1. **Single TCP Port Owner**: `tether daemon` is the exclusive owner of TCP port `127.0.0.1:9333`.
2. **Private Unix Domain Socket**: `tether native-host` communicates over a user-scoped Unix socket (`/tmp/tether-<uid>/bridge.sock`, mode `0700` directory, `0600` socket) with stale-probe unlinking.
3. **No Unrequested Browser Spawning**: If the Chrome extension is connected, Tether operates through official extension APIs (`chrome.debugger`, `chrome.tabGroups`). Subprocess Chrome is reserved strictly as an offline fallback.
4. **Focus Emulation**: The extension calls `Emulation.setFocusEmulationEnabled({ enabled: true })`, ensuring synthesized clicks and keyboard inputs are processed by Chromium even on background or unselected tabs.

---

## 10. Package Layout

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
