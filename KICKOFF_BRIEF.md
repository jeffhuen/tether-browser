# Welcome to tether-browser

## The Mission
You are the lead systems architect and engineer for `tether-browser`.
Your objective is to design and build a high-performance, zero-latency remote browser automation bridge for AI coding agents across Herdr, tmux, and SSH sessions.

**Do not implement code yet.** Your immediate task is to review this brief, verify your environment, and prepare the foundational architecture.

---

## 1. The Core Problem & Pain Point
When developers run coding agents on remote Linux servers or cloud VMs, browser automation faces an architectural breakdown:
1. **Commercial services mandate human 2FA**: Portals like AWS, Cloudflare, and Shopify require interactive login, TOTP codes, and Passkeys.
2. **Server-side pixel streaming fails over remote networks**: Tools like `terminal-browser`, VNC, and Sixel stream raw or compressed graphical frames over SSH. At $1920 \times 1080$, raw 32-bit RGBA produces **~250 MB/s**, saturating network bandwidth, creating 150–300ms of input lag, and rendering fixed, blurry viewports.
3. **Headless servers lack local authenticators**: A remote Linux box in a data center cannot access a developer's local Touch ID, Windows Hello, 1Password autofill, or Bluetooth proximity for phone passkeys.

---

## 2. The Discovery: How Orca Solved It
Inspection of Orca's open-source runtime (`stablyai/orca`, verified in `/opt/orca/1.4.201/squashfs-root/resources/app.asar:/out/shared/browser-client-host-placement.js`) revealed why Orca's remote browser feels native:

**Orca does not stream pixels or HTML over the network. The browser runs locally on the user's Mac.**

In Orca's architecture:
* Browser tabs use **Client Placement** (`kind: 'client'`).
* The Chromium instance runs on the client's laptop, using the local GPU at native 60Hz/120Hz with local password managers and biometrics.
* The remote Linux server (where the agent runs) only sends **~200-byte JSON-RPC commands** (`browser.snapshot`, `browser.click`, `browser.eval`) over a multiplexed WebSocket.
* The client sends back **~20KB JSON accessibility snapshots**.
* Zero video bandwidth is consumed, input latency is 0ms, and sessions persist in local storage.

---

## 3. The `tether-browser` Design (Without Electron)
`tether-browser` adopts Orca's command-over-wire architecture, but removes the heavy Electron app requirement:
1. **Client Daemon (`packages/client`)**:
   A lightweight binary in Go or Rust running on the developer's laptop (`tether daemon`). It launches or attaches to local Chrome via standard Chrome DevTools Protocol (CDP) on port 9222, and listens on `localhost:9333` for forwarded commands.
2. **Transport**:
   Standard SSH reverse port forward (`ssh -R 9333:localhost:9333 user@server`). Encrypted, runs over existing SSH sessions, zero firewall rules.
3. **Remote Server CLI (`packages/cli`)**:
   A drop-in command-line tool on the Linux server (`tether`) that speaks `agent-browser` syntax (`tether open`, `tether snapshot -i`, `tether click @ref`, `tether eval`). It sends JSON commands over `localhost:9333` and prints structured results to stdout.

---

## 4. Repository Standards & Environment
- **Worktree Root**: Dedicated NVMe root at `/home/jeffhuen/herdr/workspaces/tether-browser/<slice-name>`.
- **Workflow**: Governed by `skill://herdr-workflow`.
- **Standards**: Orwell writing (active voice, short sentences <=25 words, no slashes, condition-first) and Technical Writing (`/skill:technical-writing`).

---

## Next Action
Review `README.md`. Summarize your understanding of the architecture and present an initial component breakdown for `packages/protocol`, `packages/client`, and `packages/cli`. Do not write implementation code until approved.
