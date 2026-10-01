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
Version: 0.1.41
Daemon Version: 0.1.41
Mode: extension
Targets: 3
Active Target: 103
Uptime: 42s
```

`Version` is the extension's version in extension mode. `Daemon Version` is the `tether` binary running on the workstation. For the 0.1.40 protocol change, update both the workstation and remote binaries and the extension together, then click **Reconnect** in the popup. You can also reconnect with `tether connect user@server` on the workstation. Tether replaces an older local daemon only after operating-system checks verify the listener's owner and executable. It does not trust a process ID supplied by an unauthenticated network peer.

If macOS 0.1.40 reports that port `127.0.0.1:9333` is occupied even though its listener is your own Tether daemon, update to 0.1.41 and click **Reconnect**. This fixes a truncated process-name check; it does not permit stopping an unknown listener or a current daemon with a different key. Do not delete the authentication key to work around this error.

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

### Symptom: `daemon authentication key is missing` or `authenticate daemon: ...`

The daemon requires TLS 1.3 mutual authentication with standard certificates derived from Tether's shared key. You do not need to configure certificates. There is no HTTP bearer-token or plaintext fallback.

A missing-key error means the CLI could not load a key. A TLS authentication error can mean mismatched keys, an old plaintext binary, or the wrong listener on port `9333`. It is not proof of a wrong key alone.

1. Update the workstation and remote `tether` binaries and the extension together.
2. Click **Reconnect** in the popup for the intended host. Connecting syncs the key automatically. If you use a terminal-owned tunnel, reconnect it with `tether connect user@server` on the workstation.
3. Expand **Technical details** in the popup to inspect any remaining error. A current daemon with a different key is not automatically stopped. An unverified listener is not replaced.

The workstation key is at `$XDG_CONFIG_HOME/tether/auth_token`, or `~/.config/tether/auth_token` by default. The remote copy is at `$XDG_CACHE_HOME/tether/auth`, or `~/.cache/tether/auth` by default. The remote login environment determines the cache path. Sync uses encrypted SSH stdin and an atomic write with mode `0600` in a private directory. Do not print the key or copy it into a shell export. If you already set `TETHER_AUTH_TOKEN`, that value overrides the stored key, so a stale override can keep authentication failing after reconnecting.

### Symptom: Remote browsing requires an updated local binary

Remote browsing requires an authenticated HTTP proxy. Update the workstation binary and extension together, approve `webRequest` and `webRequestAuthProvider`, then click **Reconnect**.

An older helper cannot enable Remote browsing. If a saved route remains on after a helper failure, new requests fail closed. Turn Remote browsing off explicitly to restore your previous proxy.

### Symptom: SSH sign-in is pending

For Tailscale SSH check mode, the popup identifies the SSH host that supplied the sign-in link. Verify that host, then click **Open Tailscale sign-in**. If Remote browsing is on, click **Turn Remote browsing off and open sign-in**. This explicit action restores the previous proxy before opening the current validated Tailscale link. Remote browsing stays off until you enable it again. Tether never opens the page automatically. Check the device and request on Tailscale before approving.

If your SSH configuration uses `netbird ssh proxy`, NetBird opens its own SSO browser. Tether uses a 5-minute SSH connect timeout instead of 10 seconds for that configured proxy and labels the host and **NetBird** authentication wait. If Remote browsing is on, click **Turn off Remote browsing for sign-in**. This action does not open a provider URL and leaves Remote browsing off.

Tether allows up to 5 minutes after a sign-in wait is reported, with no authentication-origin proxy bypass. If the wait expires or the helper fails, automatic retries stop. Complete or resolve the provider's sign-in, then click **Reconnect** for another attempt.

Use a terminal SSH connection only for genuine SSH password, key-unlock, or host-key enrollment prompts. Routine Tailscale and NetBird authentication do not require a separate terminal workaround.

### Symptom: Screenshot shows `Local only` or remote deletion failed

Popup captures remain in the gallery when mirroring fails. The native helper saves files under `$XDG_CACHE_HOME/tether/screenshots`, or `~/.cache/tether/screenshots` by default. Local and remote cache directories use mode `0700`, and new files use mode `0600`.

Mirroring reuses an existing authenticated SSH control master with a 12-second operation limit. It can also use a `tether connect` master after checking private metadata, socket ownership, and that the master is active. It does not start a new SSH authentication attempt. Reconnect to the intended host before retrying a remote operation. A server path appears only after a successful mirror returns the resolved path.

Delete and **Clear** distinguish local deletion from remote deletion and use the capture's recorded host and path. If a mirrored server copy cannot be deleted while offline, the popup retains its gallery entry with an error. Reconnect to its original host and retry. Older mirrored entries with no host require explicit confirmation naming the connected host. Local deletion alone does not remove the server copy. Local cleanup while Tether is off does not reconnect the automation bridge or start SSH authentication.

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
