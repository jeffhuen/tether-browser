---
name: tether-browser
description: Remote-to-local browser automation bridge for AI coding agents across Tailscale, SSH, Herdr, and tmux sessions. Controls Google Chrome running natively on the developer's workstation over an SSH reverse tunnel. Use this skill whenever automating browser tasks from a remote server, SSH session, container, or Herdr workspace, especially when interacting with web pages, logging into sites requiring Passkeys, Touch ID, or 2FA, inspecting accessibility trees (@eN refs), clicking elements, filling forms, capturing screenshots, or managing tabs in the Tether tab group. Trigger this skill whenever browser automation is needed on a remote machine, even if the user does not explicitly say "tether".
metadata:
  short-description: Remote browser automation bridge for coding agents
allowed-tools: Bash(tether:*)
---

# tether-browser

`tether` controls Google Chrome running natively on the developer's workstation from a remote Linux server or container over SSH or Tailscale.

Command syntax follows `agent-browser` conventions.

---

## Tool Selection: `tether` vs `agent-browser`

* **Use `tether` (default)** when running on a remote server, SSH session, Docker container, or Herdr workspace. It connects to the developer's live workstation Chrome and supports Touch ID, Passkeys, and 2FA.
* **Use `agent-browser`** when running purely local, headless automation in CI, or when your task specifically requires automated WCAG accessibility audits (`a11y`) or network request mocking (`network`).

---

## 1. Core Workflow

Always follow the **Snapshot-Interact-Re-snapshot** loop:

```bash
# 1. Open the URL and inspect interactive elements
tether open https://example.com && tether snapshot -i

# 2. Interact using element references from the snapshot
tether fill @e2 "alice@example.com" && tether fill @e3 "secret" && tether click @e4

# 3. Always re-snapshot after actions that change the page
tether snapshot -i
```

### Why this loop is required:
* **Snapshot first**: `tether snapshot -i` extracts the page accessibility tree and assigns compact `@eN` references to interactive elements. Always inspect the snapshot before clicking or typing so you target verified elements instead of guessing brittle CSS selectors.
* **Re-snapshot after actions**: Clicks, form submits, and page navigations mutate the DOM. These mutations invalidate previous `@eN` references. Always run `tether snapshot -i` after an interaction before you issue your next command.
* **Dismiss modals & cookie banners on arrival**: Tether uses native CDP mouse events with act-time hit-testing (`elementFromPoint`). If a modal backdrop, cookie consent dialog, or notification overlay is open, click its confirmation or dismiss button first. Clicking is rejected when the target is outside the viewport or covered by a modal/dialog/cookie overlay.
* **Inspect state flags**: The snapshot outputs actionable states: `(disabled)`, `(focused)`, `(selected)`, `(expanded)`, and `checked=true|false`. Always check these attributes before toggling switches or attempting to fill already-populated inputs.

---

## 2. Command Reference

| Action | Command |
|---|---|
| Open page | `tether open <url>` |
| Inspect elements | `tether snapshot -i` |
| Click element | `tether click <@ref or selector>` |
| Double click | `tether dblclick <@ref or selector>` |
| Clear and fill text | `tether fill <@ref or selector> "text"` |
| Append text | `tether type <@ref or selector> "text"` |
| Press key | `tether press Enter` (supports `Tab`, `Escape`, `Backspace`, `Delete`, `Home`, `End`, arrow keys) |
| Hover mouse | `tether hover <@ref or selector>` |
| Focus input | `tether focus <@ref or selector>` |
| Run JavaScript | `tether eval "document.title"` |
| Wait for state | `tether wait "div.alert-success"` or `tether wait 1000` |
| Scroll page | `tether scroll [up\|down\|top\|bottom\|px]` |
| Screenshot | `tether screenshot /tmp/capture.png` (supports `--full`) |
| Close tab | `tether close` or `tether close --all` |
| Connection status | `tether status` |

### Snapshot Options
* `tether snapshot -i`: Preferred. Filters to interactive elements (buttons, links, inputs, tabs).
* `tether snapshot -c`: Compact. Strips empty structural nodes.
* `tether snapshot -s "form#login"`: Scopes snapshot to a specific CSS selector.
* `--json`: Outputs the raw structured JSON tree.

### Global Flags
* `--tab <id>`, `-t <id>`: Target a specific tab without changing active focus.
* `--session <name>`: Route commands to a named broker session.
* `--timeout <ms>`: Command timeout in milliseconds (default: 30000).
* `--json`: Emit raw structured JSON for machine parsing.

### Common Errors and Recovery
* `Element reference @eN not found; run snapshot first`: The page changed or the ref is from another tab. Run `tether snapshot -i` on the target tab before interacting.
* `No active tab in Tether group`: The active tab was closed. Run `tether open <url>` to start a tab or check `tether tabs`.
* `Cannot connect to tether daemon on 127.0.0.1:9333`: The SSH tunnel is down. Verify the reverse SSH tunnel or run `tether status`.

---

## 3. Multi-Tab Routing

Chrome organizes all automated tabs inside a blue **Tether** tab group. Tabs outside this group are completely isolated from automation.

```bash
# List open tabs in the Tether group with active indicator (*)
tether tabs

# Switch active tab focus
tether switch <tabId>

# Target a tab directly without switching active view
tether snapshot --tab <tabId> -i
tether click --tab <tabId> @e1
tether eval --tab <tabId> "document.title"
```

---

## 4. Two-Factor Authentication and Passkeys

Remote servers lack physical biometric sensors. When automating portals that require Touch ID, Passkeys, or authenticator codes:

1. Navigate to the portal:
   ```bash
   tether open https://portal.example.com/login && tether snapshot -i
   ```
2. Detect the authentication challenge in the snapshot output.
3. Pause and prompt the developer to complete the authentication on their physical machine:
   > "I navigated to the login portal in your Tether tab group. Complete the Passkey or 2FA login locally on your laptop, then reply when ready."
4. After the developer confirms they logged in, inspect the updated state and resume:
   ```bash
   tether tabs && tether snapshot -i
   ```

---

## 5. Command Chaining

Over SSH and remote networks, issuing commands one by one adds round-trip network latency. Chain commands with `&&` in a single shell turn so the client executes the sequence in one batch:

```bash
tether open https://example.com/login && tether snapshot -i
tether fill @e1 "admin" && tether fill @e2 "password" && tether click @e3
tether wait "div.dashboard" && tether screenshot /tmp/dash.png
```

---

## 6. Detailed Reference Guides

Follow these pointers when you need detailed documentation:

* **Read `references/core.md`** when you need detailed command syntax, keyboard key names, traversal depth parameters, or custom CSS selector targeting.
* **Read `references/tabs.md`** when managing complex multi-tab workflows, OAuth popup windows, or targeting background tabs with `--tab <id>`.
* **Read `references/troubleshooting.md`** when a command fails with connection errors, token mismatches, stale references, or closed tabs.
* **Read `references/agent-browser-interop.md`** when your task specifically requires forwarding Chrome DevTools Protocol to run `agent-browser a11y` audits or network request mocking.
