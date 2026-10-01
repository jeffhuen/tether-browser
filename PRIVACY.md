# Privacy Policy for Tether Browser Bridge

**Last updated:** September 20, 2026

Tether Browser Bridge ("Tether", "we", "us", or "our") is an open-source remote-to-local browser automation bridge for developers and AI coding agents.

This Privacy Policy explains how Tether handles information when you use our software and Chrome extension.

---

## 1. Zero Data Collection

Tether operates on a strict **zero data collection** policy:

* **No Browsing Data Sent to Maintainers**: Tether does not collect browsing data for its maintainers. Automation and optional remote browsing send data to the machines and websites you choose.
* **No Telemetry or Tracking**: The Tether Chrome extension and command-line tools contain zero tracking scripts, analytics SDKs, advertising beacons, or telemetry probes.
* **No Tether-Operated Servers**: Tether does not operate central servers or cloud databases. The browser bridge connects your remote development machine to your workstation. When you enable remote browsing, web traffic travels over SSH to your chosen host, then from that host to the destination website.

---

## 2. Permissions and Local Data Handling

The Tether extension requests specific Chrome permissions solely to provide browser automation functionality requested by the developer:

* **`debugger`**: Used exclusively to inspect DOM accessibility trees and synthesize input events (clicks, text input, keystrokes) on web pages you explicitly direct the agent to automate.
* **`tabGroups`**: Used to organize automation tabs in the "Tether" group. Tab groups are not security boundaries; remote browsing affects all regular tabs in the profile.
* **`tabs`**: Used to navigate, discover, and switch tabs within the Tether tab group, and to open a validated Tailscale SSH sign-in link after an explicit popup action. The popup identifies the SSH host that supplied the link. Tether does not open sign-in tabs automatically.
* **`nativeMessaging`**: Used to communicate with the local `tether native-host` process running on your workstation via standard operating system input/output (stdio).
* **`proxy`**: Used only when you enable **Remote browsing** in the popup. HTTP, HTTPS, WebSockets, and destination DNS use the selected SSH host, including localhost destinations. The setting covers all regular tabs, not just Tether tabs. The remote host can observe destinations and unencrypted HTTP traffic; HTTPS keeps its normal end-to-end encryption. OFF restores your previous proxy settings. SSH loss leaves Remote browsing on and new proxied requests fail rather than use your usual network. Authentication websites have no proxy bypass. The explicit sign-in preparation action turns Remote browsing off and leaves it off until you enable it again. Incognito, WebRTC, and other non-web traffic are not covered.
* **`webRequest` and `webRequestAuthProvider`**: Used to answer authentication challenges from Tether's active local HTTP proxy. The extension supplies a separate, temporary proxy credential, not website credentials or the daemon key. Website authentication remains unchanged.
* **`storage` and `unlimitedStorage`**: Used to store recent SSH hosts, preferences, proxy configuration, review notes, and screenshot gallery images locally in Chrome. Tether does not send this stored data to its maintainers. Screenshot mirroring sends a copy to your chosen SSH host through an existing authenticated connection. A mirror failure leaves the gallery capture available locally. Server paths appear only after successful mirroring, and delete or clear errors do not claim that an offline server copy was removed.
* **`alarms`**: Used to maintain periodic keepalive signals to prevent the background service worker from going idle during active automation sessions.
* **Host Permissions (`<all_urls>`)**: Required so that you can automate any web page you specify (such as `localhost`, internal staging portals, or production websites).

---

## 3. Security and Authentication

* **Daemon authentication**: Daemon TCP connections require TLS 1.3 mutual authentication. Tether derives standard certificates from its shared authentication key and uses standard certificate verification. No user certificate setup is required. Tether does not send the key in RPC messages or HTTP headers, and it has no bearer-token or plaintext fallback.
* **Key storage and sync**: The workstation key is stored at `$XDG_CONFIG_HOME/tether/auth_token`, or `~/.config/tether/auth_token` by default. The remote copy is stored at `$XDG_CACHE_HOME/tether/auth`, or `~/.cache/tether/auth` by default, using the remote login environment. Connecting sends the key through encrypted SSH stdin, not command arguments, and writes it atomically with mode `0600` inside a private directory.
* **Screenshot files**: The native helper saves local images and mirrors them to the selected SSH host's cache. Each machine uses `$XDG_CACHE_HOME/tether/screenshots`, or `~/.cache/tether/screenshots` by default. Cache directories use mode `0700`, and new screenshot files use mode `0600`. Mirroring reuses an authenticated SSH control master with a bounded operation time and does not start a new sign-in.
* **Local IPC**: Unix sockets prefer `$XDG_RUNTIME_DIR/tether`. The broker can also use `/run/user/<uid>/tether` when `XDG_RUNTIME_DIR` is absent. The fallback is a private `tether-<uid>` directory under the system temporary directory. Tether checks ownership, permissions, and unsafe symlinks. Socket directories use mode `0700`, sockets use mode `0600`, and the broker log uses mode `0600` in the broker's private directory.
* **Local proxy isolation**: The local HTTP proxy authenticates callers with a random per-helper credential. The extension receives it through native messaging, keeps it in memory, and excludes it from stored routes and popup responses. SOCKS and SSH control sockets use private temporary directories with ownership and permission checks. Tether-owned SSH masters suppress configured forwarding listeners. This boundary excludes other unprivileged OS accounts, not same-account programs or administrators. Local proxy authentication does not authenticate the proxy server or prevent port takeover after the helper exits.
* **Open Source**: All source code for the extension, native messaging host, and daemon is publicly available for audit at [https://github.com/jeffhuen/tether-browser](https://github.com/jeffhuen/tether-browser).

---

## 4. Third-Party Sharing

We do not sell, rent, trade, or share any user data with third parties under any circumstances.

---

## 5. Contact and Inquiries

If you have questions about this Privacy Policy or Tether Browser Bridge, contact us via:
* **Email**: `32542276+jeffhuen@users.noreply.github.com`
* **GitHub Issues**: [https://github.com/jeffhuen/tether-browser/issues](https://github.com/jeffhuen/tether-browser/issues)
