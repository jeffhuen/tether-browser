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
* **`tabs`**: Used to navigate, discover, and switch tabs within the Tether tab group.
* **`nativeMessaging`**: Used to communicate with the local `tether native-host` process running on your workstation via standard operating system input/output (stdio).
* **`proxy`**: Used only when you enable **Remote** in the popup. HTTP, HTTPS, WebSockets, and destination DNS use the selected SSH host, including localhost destinations. The setting covers all regular tabs, not just Tether tabs. The remote host can observe destinations and unencrypted HTTP traffic; HTTPS keeps its normal end-to-end encryption. OFF restores your previous proxy settings. Incognito, WebRTC, and other non-web traffic are not covered.
* **`storage`**: Used to store recent SSH hosts, user preferences, and the requested remote host and local proxy port in your browser. Tether does not send this stored configuration to its maintainers.
* **`alarms`**: Used to maintain periodic keepalive signals to prevent the background service worker from going idle during active automation sessions.
* **Host Permissions (`<all_urls>`)**: Required so that you can automate any web page you specify (such as `localhost`, internal staging portals, or production websites).

---

## 3. Security and Authentication

* **Local Token Storage**: Authentication tokens used to protect the local daemon are stored on your local workstation in your user home directory (`~/.cache/tether/auth`) with strict user-only file permissions (`0600`).
* **Open Source**: All source code for the extension, native messaging host, and daemon is publicly available for audit at [https://github.com/jeffhuen/tether-browser](https://github.com/jeffhuen/tether-browser).

---

## 4. Third-Party Sharing

We do not sell, rent, trade, or share any user data with third parties under any circumstances.

---

## 5. Contact and Inquiries

If you have questions about this Privacy Policy or Tether Browser Bridge, contact us via:
* **Email**: `32542276+jeffhuen@users.noreply.github.com`
* **GitHub Issues**: [https://github.com/jeffhuen/tether-browser/issues](https://github.com/jeffhuen/tether-browser/issues)
