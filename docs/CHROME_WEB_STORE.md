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
* **Zero Host Conflicts**: Both extensions use the same native host (`com.tether_browser.host`) and communicate over the same user-scoped Unix socket (`/tmp/tether-<uid>/bridge.sock`).
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

---

## 5. Chrome Web Store Permission Justifications

When submitting to the Chrome Web Store, enter these justifications under the **Privacy practices** tab:

### Justification for `proxy`:
> The proxy permission is required for the optional 'Remote Browsing' feature. When enabled by the user in the extension popup, Tether routes browser automation traffic through a verified local SOCKS5 proxy established over an encrypted SSH reverse tunnel to the developer's remote development server. This allows AI coding agents running in remote Linux/cloud environments to browse internal development networks while browser rendering and authentication stay on the local workstation. The proxy is strictly user-toggled, disabled by default, and only active when connected to an authorized SSH host.

### Justification for `unlimitedStorage`:
> The unlimitedStorage permission is required for the in-extension visual review system. The extension allows developers to capture area crops, viewport screenshots, and full-page captures as high-resolution PNG data URLs and attach element-level design review notes. Because multiple full-page or high-DPI screenshots quickly exceed Chrome's default 5MB storage limit, unlimitedStorage ensures user-captured visual feedback and review notes can be saved locally on the device without data loss before being exported to AI coding agents. All stored data remains strictly local on the developer's machine.
