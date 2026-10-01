# Chrome Web Store Publication & Extension Identity

This document records the official Chrome Web Store metadata, extension identifiers, and the dual-origin reconciliation strategy to prevent configuration collisions between local development and production store builds.

---

## 1. Extension Identifiers

| Environment | Extension ID | Origin | Purpose |
|---|---|---|---|
| **Production (Chrome Web Store)** | `ddfmonciifjlnpdjbijgeachabjebbfh` | `chrome-extension://ddfmonciifjlnpdjbijgeachabjebbfh/` | Official public release installed from the Chrome Web Store with automatic background updates. |
| **Local Development (Unpacked)** | `kaloekddddlgghmifoaapnhekggjcggn` | `chrome-extension://kaloekddddlgghmifoaapnhekggjcggn/` | Computed from the static RSA public key in `packages/extension/manifest.json` for local unpacked development. |

---

## 2. Native Messaging Host Reconciliation

Chrome restricts Native Messaging to origins explicitly listed in the host manifest:
* **Host Name**: `com.tether_browser.host`
* **Configuration File**: `packages/client/installer.go`

`InstallNativeHostManifest` registers **both** origins in the native messaging host manifest:

```json
{
  "name": "com.tether_browser.host",
  "description": "Tether Browser Native Messaging Host for AI coding agents",
  "path": "/path/to/tether",
  "type": "stdio",
  "allowed_origins": [
    "chrome-extension://kaloekddddlgghmifoaapnhekggjcggn/",
    "chrome-extension://ddfmonciifjlnpdjbijgeachabjebbfh/"
  ]
}
```

### Why this prevents collisions:
* Both extensions use the same native host (`com.tether_browser.host`) and user-scoped bridge socket. On Unix, the socket prefers `$XDG_RUNTIME_DIR/tether/bridge.sock` and otherwise uses `tether-<uid>/bridge.sock` under the system temporary directory, normally `/tmp`. The directory uses mode `0700`, the socket uses mode `0600`, and Tether checks ownership, permissions, and unsafe symlinks. The broker's `broker.log` stays in its private socket directory with mode `0600`.
* **Side-by-Side Compatibility**: A developer can run the local unpacked extension during feature development, or the production Web Store build on another machine, with zero native host reconfiguration.

---

## 3. Web Store Packaging Workflow

Google Chrome Web Store strictly rejects extension `.zip` uploads that contain the `"key"` property in `manifest.json`.

Use the automated packaging script:

```bash
python3 scripts/package_extension.py
```

### The script automatically:
1. Copies all extension source files, icons, and overlays from `packages/extension/`.
2. Strips the `"key"` property from `manifest.json` inside the `.zip` archive while leaving the source repository untouched.
3. Excludes temporary files, dotfiles, and test artifacts.
4. Outputs the publication bundle to `dist/tether-extension-v<version>.zip`.

---

## 4. Release Checklist for Future Updates

When publishing a new release:
1. Bump version in `packages/extension/manifest.json`.
2. Run `python3 scripts/package_extension.py`.
3. Go to the [Chrome Web Store Developer Console](https://chrome.google.com/webstore/devconsole).
4. Click **Tether Browser Bridge** (`ddfmonciifjlnpdjbijgeachabjebbfh`).
5. In the **Package** tab, click **Upload new package** and upload the new `.zip`.
6. Click **Submit for Review**.

Version 0.1.40 requires TLS 1.3 mutual authentication with standard certificates derived from Tether's shared key. There is no user certificate setup, bearer-token authentication, or plaintext fallback. Update the workstation and remote binaries and the extension together. Approve the additional `webRequest` and `webRequestAuthProvider` permissions, then click **Reconnect**. Reconnection replaces an older local daemon only after operating-system ownership and executable checks, not an unauthenticated status response. Darwin builds passed, but actual macOS runtime acceptance remains pending.

---

## 5. Chrome Web Store Permission Justifications

When submitting to the Chrome Web Store, enter these justifications under the **Privacy practices** tab:

### Justification for `proxy`:
> The proxy permission supports optional Remote browsing. When enabled in the popup, regular tabs use an authenticated local HTTP proxy over encrypted SSH. This covers HTTP, HTTPS, WebSockets, and destination DNS. Localhost destinations refer to the selected remote host. Incognito, WebRTC, and other non-web traffic are not covered. The feature is off by default. SSH loss leaves the requested proxy on and new requests fail instead of using the usual network. OFF restores the previous proxy. Authentication websites have no proxy bypass. For Tailscale, an explicit host-labelled action turns Remote browsing off before opening a validated sign-in link. For NetBird, an explicit action turns it off so NetBird's own sign-in browser can load. Neither action turns it back on automatically.

### Justification for `webRequest`:
> Tether observes proxy authentication challenges and request completion or failure to track one credential attempt per request. It does not collect browsing history or send request data to its maintainers.

### Justification for `webRequestAuthProvider`:
> Tether supplies a temporary native-helper credential only to its active loopback HTTP proxy, with a matching port, realm, and Basic challenge. This prevents another local OS account from using the user's SSH transport. The credential stays in memory and is separate from the daemon key. Tether does not supply this credential to website authentication challenges.

### Justification for `unlimitedStorage`:
> The unlimitedStorage permission supports locally stored review notes and screenshot gallery images, which can exceed Chrome's default 10 MB local storage quota. Users can capture area crops, viewports, and full pages as PNG images. The native helper also saves private screenshot files and mirrors them to the user's selected SSH host through an existing authenticated connection with a bounded operation time. A mirror error preserves the local capture and gallery. Reports include a server path only after a successful mirror returns the resolved path. Delete and clear report remote failures, including when the host is offline. Tether does not send these images or notes to its maintainers.

### Justification for sign-in tab use under `tabs`:
> Tether opens a Tailscale SSH sign-in tab only after an explicit popup action. The popup identifies the SSH host that supplied the link and asks the user to verify the device and request before approving. The background validates the current SSH session and accepts only `https://login.tailscale.com/a/` links with an ASCII alphanumeric ID. No sign-in tab opens automatically. NetBird's configured SSH proxy opens its own SSO browser, and Tether does not open arbitrary provider URLs.
