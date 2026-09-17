# Privacy Policy for Tether Browser Bridge

**Last updated:** September 17, 2026

Tether Browser Bridge ("Tether", "we", "us", or "our") is an open-source remote-to-local browser automation bridge for developers and AI coding agents.

This Privacy Policy explains how Tether handles information when you use our software and Chrome extension.

---

## 1. Zero Data Collection

Tether operates on a strict **zero data collection** policy:

* **No Personal Data Collected**: We do not collect, store, transmit, or monitor your name, email address, IP address, browsing history, keystrokes, form submissions, or passwords.
* **No Telemetry or Tracking**: The Tether Chrome extension and command-line tools contain zero tracking scripts, analytics SDKs, advertising beacons, or telemetry probes.
* **No External Servers**: Tether does not operate any central servers or cloud databases. All communication occurs strictly over encrypted point-to-point connections (such as your own SSH tunnel or Tailscale network) between your remote development machine and your local computer.

---

## 2. Permissions and Local Data Handling

The Tether extension requests specific Chrome permissions solely to provide browser automation functionality requested by the developer:

* **`debugger`**: Used exclusively to inspect DOM accessibility trees and synthesize input events (clicks, text input, keystrokes) on web pages you explicitly direct the agent to automate.
* **`tabGroups`**: Used to create and manage the isolated "Tether" tab group so automation is confined to dedicated tabs and cannot touch your personal tabs.
* **`tabs`**: Used to navigate, discover, and switch tabs within the Tether tab group.
* **`nativeMessaging`**: Used to communicate with the local `tether native-host` process running on your workstation via standard operating system input/output (stdio).
* **`storage`**: Used to store recent host connection strings and user preferences locally in your browser. This data never leaves your device.
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
