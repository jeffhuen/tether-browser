# Connection Diagnostics and Troubleshooting

`tether` sends commands through this network path:

```text
[Remote Agent] -> [SSH Tunnel :9333] -> [Tether Daemon] -> [Unix Socket] -> [Native Host] -> [Chrome Extension]
```

---

## 1. Quick Connection Verification

Run on the remote server:

```bash
tether status
```

Expected output when fully connected:
```text
Daemon Status: connected
Version: 0.1.29
Mode: extension
Targets: 3
Active Target: 103
Uptime: 42s
```

---

## 2. Common Symptoms & Solutions

### Symptom: `Cannot connect to tether daemon on 127.0.0.1:9333`

**Cause**: Port `9333` on the remote server is not forwarded to the developer's laptop, or the local daemon is not running.

**Resolution**:
1. On the developer's laptop:
   * Open Chrome and click the **Tether extension icon** in the toolbar.
   * If the badge says `Disconnected`, enter `user@server` in the **Connect to Remote Server** card and click **Connect**.
   * Or run in the laptop terminal:
     ```bash
     tether connect user@server
     ```
2. Verify reverse tunnel listener on the remote server:
   ```bash
   nc -zv 127.0.0.1 9333
   ```
   If the connection fails, verify that the SSH server permits reverse port forwarding.

---

### Symptom: `Unauthorized (401)` or `Authentication Failed`

**Cause**: The authentication token on the remote server does not match the token on the developer's laptop.

**Resolution**:
1. Check the remote token file:
   ```bash
   cat ~/.cache/tether/auth
   ```
2. If missing or invalid, re-run `tether connect` from the laptop, which automatically syncs the token:
   ```bash
   tether connect user@server
   ```
3. Or manually export the token:
   ```bash
   export TETHER_AUTH_TOKEN="<token-from-laptop>"
   ```

---

### Symptom: `Waiting for Daemon` in Chrome Extension Popup

**Cause**: The Chrome background service worker was sleeping or Chrome was not connected to the native messaging host.

**Resolution**:
1. Click the reload button in the header of the Tether popup.
2. If still disconnected, re-register the native messaging host:
   ```bash
   tether extension install
   ```
3. Ensure the extension is loaded from `packages/extension` in `chrome://extensions` with Developer Mode enabled.

---

### Symptom: `No active tab in Tether group` or `target not found or tab closed`

**Cause**: The active tab was closed, or commands are targeting a tab ID that has navigated away or closed.

**Resolution**:
1. Discover open tabs in the Tether group:
   ```bash
   tether tabs
   ```
2. Switch active focus to a live tab:
   ```bash
   tether switch <tabId>
   ```
3. Or open a new tab:
   ```bash
   tether open https://example.com
   ```

---

### Symptom: `Element reference @eN not found; run snapshot first`

**Cause**: The DOM changed after navigation, or references from another tab were reused. References are tab-scoped and reset on each snapshot.

**Resolution**:
Run `tether snapshot -i` on the target tab before interacting:
```bash
tether snapshot -i
```

---

### Symptom: `session "x" has no active target`

**Cause**: The session specified with `--session <name>` has not opened an initial page.

**Resolution**:
Seed the session with an initial `open` command:
```bash
tether open --session <name> https://example.com
```
