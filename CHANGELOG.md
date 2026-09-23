# Changelog

All notable changes to `tether-browser` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- The connection status strip now opens connection settings, replacing the separate **Connections** button. The version link moved beside the reload button.

### Fixed
- Separate the popup's agent bridge, SSH tunnel, and remote browsing indicators. SSH readiness no longer implies an agent connection, and failed status polls show unknown SSH and browsing state.
- Rewrite remote browsing help in plain language and combine connection errors into one warning, with the original errors under **Technical details**.

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
