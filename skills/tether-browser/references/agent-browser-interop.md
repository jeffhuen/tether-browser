# Interoperability: Tether and agent-browser

This document explains how `tether` and `agent-browser` complement each other, how to connect them, and when to use each tool.

---

## 1. Architectural Differences

| Capability | Tether CLI (`tether`) | `agent-browser` |
|---|---|---|
| **Primary Target** | Developer local Chrome through extension | Headless Chrome or raw CDP endpoint |
| **Authentication** | Native Passkeys, Touch ID, local password managers | Saved cookie or storage JSON files |
| **Tab Safety** | Isolated to dedicated blue **Tether** tab group | Unrestricted tab access across the browser |
| **Network Protocol** | Single-request JSON-RPC over port 9333 | Bidirectional CDP WebSocket over port 9222 |
| **WAN Efficiency** | High (one 200-byte request per interaction) | Low (5 to 10 sequential round-trips per interaction) |
| **Server Dependencies** | Zero (single static Go binary) | Requires Node, Bun, or Rust toolchain |

---

## 2. Feature Gaps

### What `agent-browser` can do that `tether` does not:
* **Accessibility Audits**: `agent-browser a11y` runs automated WCAG audits via embedded axe-core.
* **Network Mocking**: `agent-browser network route` can intercept, stub, or block HTTP requests.
* **React Profiling**: `agent-browser react suspense` walks component trees and profiles renders.
* **Annotated Vision Captures**: `agent-browser screenshot --annotate` draws numbered badges for multimodal vision models.
* **Device Presets**: `agent-browser set device "iPhone 15"` applies preconfigured viewports and user agents.

### What `tether` can do that `agent-browser` does not:
* **Live Extension Adoption**: Attaches to the developer running Chrome without browser restarts or command-line flags.
* **Native Biometrics and 2FA**: Works with hardware authenticators, Touch ID, and Passkeys on the developer physical machine.
* **Tab Isolation**: Restricts automation strictly to the Tether tab group, preventing accidental access to personal tabs.
* **Low Latency over SSH**: Calculates bounding boxes and input events on the client loopback rather than streaming raw CDP over SSH.
* **Standalone Server Binary**: Runs as an independent Go binary with zero external package dependencies.

---

## 3. Connect agent-browser to Tether

To use `agent-browser` over SSH, forward the Chrome DevTools Protocol port:
```bash
# On the developer workstation, forward both Tether RPC (9333) and raw CDP (9222)
ssh -R 9333:localhost:9333 -R 9222:localhost:9222 user@server
```

On the remote server, point `agent-browser` at the forwarded CDP port:

```bash
# Execute deep accessibility audits via agent-browser
agent-browser --cdp 9222 a11y

# Intercept and mock network requests
agent-browser --cdp 9222 network route https://api.example.com/* --status 200

# Inspect React Suspense trees
agent-browser --cdp 9222 react suspense
```

Or set the environment variable for the current session:

```bash
export AGENT_BROWSER_CDP=9222
agent-browser snapshot -i
```

---

## 4. Choose Between tether and agent-browser
* **Use `tether` for:**
  * Interactive browser tasks, testing, and debugging.
  * Logging into sites requiring human 2FA, Passkeys, or Touch ID.
  * Running agents over high-latency SSH, Tailscale, or cellular connections.
  * Preserving personal tabs during development on the developer workstation.

* **Use `agent-browser` for:**
  * Running formal WCAG accessibility audits (`a11y`).
  * Simulating network outages or mocking backend endpoints (`network`).
  * Profiling React renders and Suspense boundaries (`react`).
  * Generating vision-annotated screenshots for multimodal AI models.
