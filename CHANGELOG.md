# Changelog

All notable changes to `tether-browser` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.37] - 2026-09-26

### Changed
- The popup has two connection controls: the **Connect Tether** button and the **Remote browsing** switch. Remote browsing needs a connected Tether. Turning Remote browsing off restores Chrome's previous proxy and keeps the SSH session and agent access up.
- **Disconnect Tether** turns Remote browsing off, closes the SSH session that Tether opened, closes the connection to the local `tether` helper, and detaches Chrome's debugger from every tab. Agent commands fail until you connect again.
- The extension reconnects on its own after Chrome restarts only if Tether was connected when Chrome closed. After you update, click **Connect Tether** once, unless Remote browsing was on.
- When Tether loses its server connection, the popup shows **Reconnect** next to **Disconnect**. Before, it offered only **Disconnect Tether**.
- The Remote browsing confirmation appears once for each server and proxy port. Turning the switch on again for the same server skips it.
- In the screenshot panel, **Copy for Agent** is now **Copy** and **Clear All** is now **Clear**, matching the notes panel.

### Security
- The review overlay runs in an isolated JavaScript world. A page can no longer define `window.__tetherReview` to change the notes and report that the popup copies or that `tether review list` returns.
- Agent commands act only on tabs in the Tether tab group. The popup's status check no longer attaches Chrome's debugger to tabs outside the group.

## [0.1.36] - 2026-09-23

### Changed
- `tether connect` and the extension's **Connect** button replace a running daemon from a different `tether` version, so a reinstall takes effect when you reconnect. Before, they reused whatever daemon was already running. They only stop a `tether daemon` process on the same machine. A daemon older than 0.1.36 reports no process ID, so `tether connect` warns and keeps it: stop it once with `pkill -f 'tether daemon'`.
- `tether status` shows `Daemon Version`, the version of the `tether` binary running the daemon. In extension mode, `Version` is the extension's version.

### Fixed
- Shells without `XDG_RUNTIME_DIR` now use the same broker socket as login shells (`/run/user/<uid>/tether/broker.sock` on systemd hosts). Before, they could start a second broker that other shells never saw.

## [0.1.35] - 2026-09-23

### Changed
- Overhauled popup contrast and visual separation: replaced low-contrast warm darks with a high-contrast surface ladder (`#090a0f` canvas, `#141722` surface, `#1e2230` elevated), brightened hairline borders (`rgba(255, 255, 255, 0.13)`), added visible surfaces to secondary buttons (`Copy`, `Clear`), and eliminated muddy background washes so all components have clear edges.
- The connection status strip now opens connection settings, replacing the separate **Connections** button. The version link moved beside the reload button.
- The connection button is now state-aware (`Connect`, `Disconnect`, `Switch`, `Reconnect`, `Cancel`), replacing the separate header power icon with inline actions and input locking. Pressing Enter in the host field while connected to that host does nothing. Enter with a different host switches. Clicking **Disconnect** still asks for confirmation.
- Clarified remote browsing description: "Open websites through your remote server. Use `localhost` to reach remote services as if they were running locally."
- Elevated muted text contrast to `#a4b3cd` (WCAG AAA 8.5:1, APCA $L_c$ 67.5) and borders to `rgba(255, 255, 255, 0.16)` for WCAG 3 perceptual contrast compliance.
- Optimized vertical rhythm and container heights to prevent bottom clipping under Chrome's 600px popup ceiling.
- Upgraded Review Notes and Screenshots into connected physical folder tabs: distributed 50/50 across the full card width, with uppercase tracked typography matching section headers and the active tab merging directly into the tool panel below.
- Added dedicated ambient icon wells to tool action cards, and styled section headers in uppercase tracked monospace for distinct visual hierarchy.
- Renamed "Open Tabs" to "Tethered Tabs" with an aligned empty state, and removed the redundant `[TETHER]` badge from tab rows to free up horizontal space for titles while highlighting `[ACTIVE]`.
- Updated screenshot mirror destination notice to "Mirroring to Local and Remote Server" for platform neutrality across Mac, Linux, and Windows.
- `--json` prints the daemon result as returned, for every command. `tether eval --json` prints an error result to standard output and exits 1.
- The popup reads its version from the installed extension. The reload button reloads the extension without checking GitHub.
- Extension and fallback drivers now agree: `tether focus` focuses without clicking, `tether wait` accepts `@ref` selectors and stops after 25 seconds by default, `tether press` sends the correct key codes for `Delete`, `Home`, and `End`, and `tether scroll` uses the same wheel point.

### Fixed
- Separate the popup's agent bridge, SSH tunnel, and remote browsing indicators. SSH readiness no longer implies an agent connection, and failed status polls show unknown SSH and browsing state.
- Rewrite remote browsing help in plain language and combine connection errors into one warning, with the original errors under **Technical details**.
- Preserved active keyboard focus in Open Tabs across the 2-second background status poll, eliminating focus drops to `<body>` (WCAG 2.4.3 Level A, WCAG 3 Barrier).
- Added semantic heading hierarchy with `<h1>` for title and `<h2>` for all major sections (WCAG 1.3.1 Level A, WCAG 3 Friction).
- `tether review list` and `tether review send` return pinned notes when Chrome runs through the extension. Before, they always returned no notes.
- `tether close --all` closes every Tether tab through the extension. Before, it closed only the active tab.
- The popup's **Copy** button copies the report built in the reviewed page. Page text and comments can no longer close the HTML code fence or add headings to the report. `tether review send` also sanitizes the page URL, viewport, element tag, and carriage returns in comments.
- Text and HTML snippets in review notes use one redaction path, so both redact passwords and secret attributes the same way.
- `tether tabs --json` prints JSON when no tabs are open.
- Extension results that fail to decode now return an error instead of an empty result.
- Screenshot entries and the **Copy for Agent** report show a server path only when the file was mirrored to the server. Other captures say **Local only**.
- The Tethered Tabs list height cap now applies.

### Removed
- The unused zstd and raw binary frame formats on the daemon port, with the `klauspost/compress` dependency. The daemon accepts newline-delimited JSON-RPC and HTTP POST, as before, and has no third-party Go dependencies.
- Duplicate code in the RPC dispatcher, element lookup, isolated-world evaluation, popup capture handlers, and review report formatter, and tests that only checked copied fields or mock echoes.

## [0.1.34] - 2026-09-22

### Added
- **First-Class Scroll Primitive:** Added `tether scroll [up|down|top|bottom|px]` across protocol (`browser.scroll`), Go CLI/driver, and Chrome extension background service worker.
- **Act-Time Hit-Testing & Occlusion Detection:** Added `document.elementFromPoint(x, y)` and `DOM.getNodeForLocation` validation in `handleClick` to prevent false-success clicks when elements are occluded by modal dialogs, cookie banners, or sticky overlays.
- **Pre-Click Scroll-Into-View:** Added automatic `DOM.scrollIntoViewIfNeeded` before bounding box calculation, ensuring off-viewport elements below the fold are properly centered.
- **Deterministic Focus in Fill/Type:** Added `DOM.focus` on the target `backendDOMNodeId` before dispatching keystrokes in `handleFill` and `handleType`.
- **Complete Snapshot State:** Extended `tether snapshot` to capture `checked`, `selected`, `expanded`, `focused`, `disabled`, `targetUrl`, and `title`.
- **Deterministic Content Fingerprint:** Replaced timestamp-based root hashes with an FNV-1a content hash, including real `generation` and `modified` state tracking.
- **Keyboard Modifier Parsing:** Added parsing for `Control+`, `Alt+`, `Shift+`, and `Meta+` key chords with proper CDP modifier bitmasks and virtual key code derivation in `handlePress`.
- **Mouse Button Masks:** Added `buttons: 1` on mouse press and `buttons: 0` on mouse release in `Input.dispatchMouseEvent`.
- **New Skill `tether-jev`:** Added documentation and reference recipe (`examples/tether_jev_loop.py`) for pairing Tether with TypeSafe Jev System One models.

### Fixed
- **CDP Input.enable Error:** Bypassed `.enable` calls for the `Input` domain in `ensureDomain()`, resolving `rpc error -32601: 'Input.enable' wasn't found` on native input.
- **Scroll Ladder Branching:** Fixed branch ordering in extension and Go driver where `top` and `bottom` directions were unreachable behind default delta checks.
- **ReferenceError in handleClick:** Properly declared and captured `backendNodeId` from resolved element coordinates.
- **Stale Reference Invalidation:** Cleared `elementRefsByTab` and `tabSnapshotStates` on tab navigation and loading states to prevent clicks from targeting recycled backend node IDs.
- **React Synthetic Event Tracking:** Updated `CDPDriver.Fill` to use HTMLInputElement prototype native value setter so React components observe programmatic edits.
- **Snapshot Ref Table Off-By-One:** Corrected ref counter increment in `CDPDriver.Snapshot`.

---

## [0.1.33] - 2026-09-16

### Added
- Verified SSH remote browsing toggle in extension popup.
- Separate connection and remote browsing status indicators.
- Initial support for session broker daemons.
