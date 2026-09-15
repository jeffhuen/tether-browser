# tether-browser

High-performance, zero-latency remote browser automation for AI coding agents across Herdr, tmux, and SSH sessions.

No pixel streaming. No VNC. No Electron.

---

## 1. The Core Problem

When developers run coding agents on remote Linux servers or cloud VMs, browser automation faces a fundamental conflict:

* **Commercial web services require human interaction**: Services like Cin7, AWS, Shopify, and internal portals mandate two-factor authentication, TOTP codes, and Passkeys.
* **Server-side pixel streaming fails over remote networks**: Tools like `terminal-browser`, VNC, and Sixel stream raw or compressed graphical frames over SSH. At $1920 \times 1080$, raw 32-bit RGBA requires **~250 MB/s**, saturating network bandwidth, introducing 150–300ms of input latency, and locking the browser into a tiny, blurry canvas.
* **Headless cloud servers lack authentication hardware**: A remote Linux box in a data center cannot access your laptop's Touch ID, Windows Hello camera, 1Password autofill, or Bluetooth proximity for phone passkeys.

---

## 2. The Discovery: How Orca Solved It

An architectural inspection of Orca's runtime (`stablyai/orca`, inspected in `/opt/orca/1.4.201/squashfs-root/resources/app.asar`) revealed why Orca's remote browser feels native:

**Orca does not stream pixels or HTML over the network. The browser runs locally on the user's Mac.**

In Orca's source code (`out/shared/browser-client-host-placement.js`), browser tabs use **Client Placement** (`kind: 'client'`):

1. **Client-side execution**: The Chromium browser instance runs locally on the user's desktop hardware, utilizing the local GPU/Metal at native 60Hz/120Hz resolution.
2. **Local credential management**: Passwords, 1Password, Touch ID, and session cookies exist locally in the client's session partition (`persist:orca-browser`).
3. **Command-only network traffic**: The remote Linux server (where the agent lives) connects to the client over a multiplexed WebSocket and sends only lightweight **JSON-RPC automation commands** (`browser.snapshot`, `browser.click`, `browser.eval`).
4. **Bandwidth consumption**: Instead of streaming hundreds of megabytes of video, only **~200 bytes of command JSON** and **~20KB of DOM accessibility snapshots** travel across the wire.

---

## 3. The `tether-browser` Architecture

`tether-browser` adopts Orca's command-over-wire model, but removes the Electron desktop app requirement. Developers already have Google Chrome or Safari installed on their laptops.

```text
┌────────────────────────────────────────┐         SSH Reverse Tunnel (Port 9333)       ┌────────────────────────────────────────┐
│               LOCAL CLIENT             │ ◄──────────────────────────────────────────► │             REMOTE SERVER              │
│                 (macOS/PC)             │                                              │             (Linux / Cloud)            │
│                                        │                                              │                                        │
│  ┌──────────────────────────────────┐  │                                              │  ┌──────────────────────────────────┐  │
│  │    Local Chrome / Safari Tab     │  │                                              │  │       Herdr / Tmux Session       │  │
│  │   • Full Retina/4K resolution    │  │                                              │  │                                  │  │
│  │   • 120Hz/60Hz native rendering  │  │                                              │  │  $ tether snapshot -i            │  │
│  │   • Touch ID, 1Password, 2FA     │  │                                              │  │  $ tether click @e14             │  │
│  └──────────────────▲───────────────┘  │                                              │  │  $ tether fill @e3 "query"       │  │
│                     │                  │                                              │  └──────────────────┬───────────────┘  │
│  ┌──────────────────┴───────────────┐  │        JSON Commands (200 bytes)             │                     │                  │
│  │          tether daemon           │ ◄───────────────────────────────────────────────┤  ┌──────────────────▼───────────────┐  │
│  │   (Lightweight 5MB Go / Rust)    │                                                 │  │            tether CLI            │  │
│  │   Drives local Chrome via CDP    │ ├───────────────────────────────────────────────►  │     Transmits JSON over SSH      │  │
│  └──────────────────────────────────┘  │        DOM Snapshots / Results (20KB)        │  └──────────────────────────────────┘  │
└────────────────────────────────────────┘                                              └────────────────────────────────────────┘
```

### Components

#### 1. Client Daemon (`packages/client`)
* A lightweight binary written in Go or Rust running on your local machine (`tether daemon`).
* Connects to your local Chrome instance via the Chrome DevTools Protocol (CDP):
  ```bash
  google-chrome --remote-debugging-port=9222 --user-data-dir=~/.config/tether-profile
  ```
* Listens on `localhost:9333` for incoming RPC commands forwarded through SSH.
* Executes actions directly against local Chrome tabs and returns JSON results.

#### 2. Transport Substrate
* Standard SSH reverse port forwarding:
  ```bash
  ssh -R 9333:localhost:9333 user@server
  ```
* Requires zero open firewall ports, zero cloud intermediaries, and zero separate daemon listeners. All traffic runs encrypted inside your existing SSH session.

#### 3. Remote Server CLI (`packages/cli`)
* A drop-in command-line tool on the Linux server (`tether`).
* Exposes standard `agent-browser` syntax so coding agents can use it immediately:
  ```bash
  tether open https://example.com
  tether snapshot -i
  tether click @e2
  tether fill @e3 "admin"
  tether eval "document.title"
  ```
* Sends commands to `localhost:9333` over the SSH tunnel and outputs structured results to stdout.

---

## 4. Performance Comparison

| Metric | Server Frame Streaming (`terminal-browser` / VNC) | Command Bridge (`tether-browser`) |
|---|---|---|
| **Network Throughput** | 100–250 MB/s (saturates bandwidth) | **< 50 KB/s** (imperceptible) |
| **Input Latency** | 150–300ms network round trips | **0ms** (local hardware rendering) |
| **Display Quality** | Fixed 800×600 canvas | **Full native Retina / 4K resolution** |
| **Authentication** | Fails on Touch ID, Passkeys, YubiKey | **Native support** via local client OS |
| **Server Memory** | Consumes 500MB–2GB RAM on server | **0 MB** (browser runs on client) |
| **Agent Tooling** | Requires video decoders or ASCII scrapers | Clean, structured JSON accessibility trees |

---

## 5. Repository Plan

```text
tether-browser/
├── README.md                 # Architectural specification and usage
├── packages/
│   ├── protocol/             # JSON-RPC command and event definitions
│   ├── client/               # Local desktop daemon (CDP bridge)
│   └── cli/                  # Remote Linux CLI (agent-browser compatible)
└── scripts/
```
