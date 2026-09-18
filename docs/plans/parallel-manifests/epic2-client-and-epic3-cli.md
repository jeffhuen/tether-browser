# Parallel Manifest: Epic 2 (Client Daemon) and Epic 3 (Remote Server CLI)

## 1. Common Baseline
- **Baseline Commit**: `48d8e82` (on `origin/main`)
- **Shared Seam**: `packages/protocol` (frozen contract, reviewed and verified with `REVIEW_PASS`)
- **Reconciliation Owner**: `Main` (Coordinator)
- **Max Concurrent Writers**: 2 (Agent A for Client Daemon, Agent B for Remote CLI)

---

## 2. Slice Allocations

### Writer 1: Epic 2 (Client Daemon & Forward Proxy)
- **Bead**: `tb-0nh`
- **Branch**: `tb-0nh-client`
- **Worktree Path**: `$HOME/herdr/workspaces/tether-browser/tb-0nh-client`
- **Owned Files**:
  - `packages/client/**`
- **Forbidden Files (Read-Only)**:
  - `packages/protocol/**` (frozen contract)
  - `packages/cli/**` (owned by Writer 2)
  - `go.mod` and `go.sum` (shared manifest, frozen)
  - `.beads/**` (managed by coordinator)
- **Scope**:
  - Chrome process manager with ephemeral port allocation (`DevToolsActivePort`).
  - Mode A Forward Proxy with `<-loopback>` and `HTTP CONNECT` bidirectional socket tunneling.
  - Raw WebSocket CDP client connector.
  - Modular `BrowserDriver` implementation (`CDPDriver`).
  - Unit tests for proxy, CONNECT tunneling, and driver interface.

---

### Writer 2: Epic 3 (Remote Server CLI & Session Broker)
- **Bead**: `tb-p0e`
- **Branch**: `tb-p0e-cli`
- **Worktree Path**: `$HOME/herdr/workspaces/tether-browser/tb-p0e-cli`
- **Owned Files**:
  - `packages/cli/**`
- **Forbidden Files (Read-Only)**:
  - `packages/protocol/**` (frozen contract)
  - `packages/client/**` (owned by Writer 1)
  - `go.mod` and `go.sum` (shared manifest, frozen)
  - `.beads/**` (managed by coordinator)
- **Scope**:
  - `agent-browser` compatible CLI parser (`open`, `snapshot -i`, `click @ref`, `fill`, `eval`).
  - Persistent remote session broker (`tether broker`) managing socket lifetime across CLI commands.
  - Tailscale WireGuard and SSH port forward auto-discovery.
  - Compact `@eN` accessibility tree terminal formatter and `--json` output.
  - Unit tests for CLI parsing, broker lifecycle, and formatting.

---

## 3. Merge Order and Reconciliation
1. **Merge Order**:
   - Merge Slice 1 (`tb-0nh-client`) first after independent review `REVIEW_PASS`.
   - Refresh Slice 2 (`tb-p0e-cli`) against updated `origin/main`.
   - Merge Slice 2 (`tb-p0e-cli`) second after independent review `REVIEW_PASS`.
2. **Reconciliation Authority**:
   - `Main` (Coordinator) owns staging, review verification, and fast-forward pull on `main`.
