// Run against a disposable Chrome profile with the unpacked extension loaded:
// node scripts/check_popup.mjs http://127.0.0.1:9349
// Chrome needs --remote-debugging-port=9349 and --enable-unsafe-extension-debugging.
// For headless capture, add --disable-gpu --run-all-compositor-stages-before-draw.
// Also add --disable-background-timer-throttling --disable-renderer-backgrounding.
// Capture checks move this page to a background tab. Throttled timers there can
// exceed the 35-second command limit even when the extension behaves correctly.
// Uses synthetic notes plus real extension storage, page captures, and pointer input.
import assert from "node:assert/strict";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const endpoint = process.argv[2] || "http://127.0.0.1:9349";
const targets = await (await fetch(`${endpoint}/json/list`)).json();
const target = targets.find((target) => /^chrome-extension:\/\/[^/]+\/popup.html$/.test(target.url));
assert(target, "Open the Tether popup in the disposable browser before running this check");
const socket = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((resolve, reject) => {
  socket.addEventListener("open", resolve, { once: true });
  socket.addEventListener("error", reject, { once: true });
});
let nextId = 0;
const pending = new Map();
socket.addEventListener("message", ({ data }) => {
  const message = JSON.parse(data);
  const request = pending.get(message.id);
  if (!request) return;
  pending.delete(message.id);
  clearTimeout(request.timer);
  if (message.error) request.reject(new Error(message.error.message));
  else request.resolve(message.result);
});
function cdp(method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = ++nextId;
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error(`Timed out: ${method}`));
    }, 35000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params }));
  });
}
async function evaluate(fn, ...args) {
  const result = await cdp("Runtime.evaluate", {
    expression: `(${fn})(${args.map((arg) => JSON.stringify(arg)).join(",")})`,
    awaitPromise: true,
    returnByValue: true,
  });
  assert(!result.exceptionDetails, JSON.stringify(result.exceptionDetails));
  return result.result.value;
}
const captures = await mkdtemp(join(tmpdir(), "tether-popup-check-"));
async function screenshot(name) {
  const { data } = await cdp("Page.captureScreenshot", { format: "png" });
  await writeFile(join(captures, `${name}.png`), Buffer.from(data, "base64"));
}
async function checkLayout() {
  const layout = await evaluate(() => {
    const panel = document.querySelector('.tab-panel:not([hidden])');
    const boxes = [...panel.children].map((element) => element.getBoundingClientRect());
    const brand = document.querySelector(".brand").getBoundingClientRect();
    const actions = document.querySelector(".header-actions").getBoundingClientRect();
    return {
      headerOneRow: brand.bottom > actions.top,
      overflow: document.documentElement.scrollWidth > innerWidth,
      stacked: boxes.every((box, index) => index === 0 || box.top >= boxes[index - 1].bottom),
      buttonsFit: [...document.querySelectorAll("button")].filter((button) => button.getClientRects().length).every((button) => {
        const box = button.getBoundingClientRect();
        return box.left >= 0 && box.right <= innerWidth && box.height >= 24;
      }),
      statusesVisible: [...document.querySelectorAll(".connection-state")].every((element) => {
        const box = element.getBoundingClientRect();
        return box.width > 0 && box.left >= 0 && box.right <= innerWidth && box.bottom <= innerHeight;
      }),
    };
  });
  assert.equal(layout.overflow, false, "Popup must not scroll horizontally");
  assert(layout.headerOneRow, "Connection status must fit beside the brand in the top bar");
  assert(layout.stacked, "Workspace sections must stack vertically");
  assert(layout.buttonsFit, "Buttons must stay within the popup and remain usable");
  assert(layout.statusesVisible, "All connection indicators must remain visible when settings are collapsed");
}
let saved;
try {
  await evaluate(async (endpoint) => {
    const privilegedBlob = URL.createObjectURL(new Blob(["private"], { type: "text/html" }));
    try {
      for (const url of [chrome.runtime.getURL("popup.html"), "popup.html", privilegedBlob, "about:extensions", "about:blank"]) {
        const response = await chrome.runtime.sendMessage({ type: "popup_navigate", url, newTab: true });
        if (!response.error) throw new Error("Automation opened a privileged browser or extension page");
      }
    } finally {
      URL.revokeObjectURL(privilegedBlob);
    }
    const previousTabs = new Set((await chrome.tabs.query({})).map((tab) => tab.id));
    const opened = await chrome.runtime.sendMessage({ type: "popup_navigate", newTab: false });
    if (opened.error) throw new Error(opened.error);
    const tab = (await chrome.tabs.query({ active: true })).find((tab) => !previousTabs.has(tab.id));
    if (!tab) throw new Error("Default automation tab was not created");
    const waitComplete = async (previousURL) => {
      for (let i = 0; i < 100; i++) {
        const current = await chrome.tabs.get(tab.id);
        if (current.status === "complete" && current.url !== previousURL) return;
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      throw new Error("Guard check tab did not finish loading");
    };
    try {
      await waitComplete();
      const emptyURL = (await chrome.tabs.get(tab.id)).url;
      const createdOrigin = await chrome.debugger.sendCommand({ tabId: tab.id }, "Runtime.evaluate", {
        expression: "origin", returnByValue: true,
      });
      if (createdOrigin.result.value !== "null") throw new Error("Empty automation tab inherited a privileged origin");
      await chrome.debugger.sendCommand({ tabId: tab.id }, "Runtime.evaluate", {
        expression: `location.href = ${JSON.stringify(chrome.runtime.getURL("popup.html"))}`,
      });
      await waitComplete(emptyURL);
      if ((await chrome.tabs.get(tab.id)).url === chrome.runtime.getURL("popup.html")) {
        throw new Error("An empty automation page reached the extension controls");
      }
      await chrome.tabs.update(tab.id, { url: `${endpoint}/json/version` });
      await waitComplete();
      const started = await chrome.runtime.sendMessage({ type: "popup_start_review", tabId: tab.id });
      if (started.error) throw new Error(started.error);
      const blob = await chrome.debugger.sendCommand({ tabId: tab.id }, "Runtime.evaluate", {
        expression: "URL.createObjectURL(new Blob(['<h1>Web blob</h1>'], {type: 'text/html'}))",
        returnByValue: true,
      });
      await chrome.tabs.update(tab.id, { url: blob.result.value });
      await waitComplete();
      const blobReview = await chrome.runtime.sendMessage({ type: "popup_start_review", tabId: tab.id });
      if (blobReview.error) throw new Error(`Ordinary web blob lost automation access: ${blobReview.error}`);
      const switched = await chrome.runtime.sendMessage({ type: "popup_switch_tab", tabId: tab.id });
      if (switched.error) throw new Error(switched.error);
      const blankNavigation = await chrome.runtime.sendMessage({ type: "popup_navigate", url: "about:blank", newTab: false });
      if (!blankNavigation.error) throw new Error("Existing-tab navigation admitted an inherited-origin blank page");
      await chrome.tabs.update(tab.id, { url: "about:blank" });
      await waitComplete();
      const blankSwitch = await chrome.runtime.sendMessage({ type: "popup_switch_tab", tabId: tab.id });
      if (!blankSwitch.error) throw new Error("Tab switching admitted an inherited-origin blank page");
      await chrome.tabs.update(tab.id, { url: chrome.runtime.getURL("popup.html") });
      await waitComplete();
      const blocked = await chrome.runtime.sendMessage({ type: "popup_clear_notes", tabId: tab.id });
      if (!blocked.error) throw new Error("An attached tab kept automation access after navigating into the extension");
    } finally {
      await chrome.tabs.remove(tab.id);
    }
  }, endpoint);
  saved = await evaluate(async () => {
    const saved = await chrome.storage.local.get(["recent_hosts", "tether_screenshots", "tether_popup_tab"]);
    window.popupCheckSend = chrome.runtime.sendMessage.bind(chrome.runtime);
    window.popupCheckNetwork = { enabled: false, state: "off", canEnable: false, ssh: { state: "disconnected" } };
    chrome.runtime.sendMessage = (message, callback) => {
      if (message.type === "popup_get_status") {
        callback(window.popupCheckStatus);
        if (window.popupCheckRefreshed) {
          setTimeout(window.popupCheckRefreshed, 0);
          delete window.popupCheckRefreshed;
        }
        return;
      }
      if (message.type === "popup_network_status") return callback(window.popupCheckNetwork);
      if (message.type === "popup_network_set") {
        if (window.popupCheckActionError) return callback({ error: window.popupCheckActionError });
        window.popupCheckNetwork = { ...window.popupCheckNetwork, enabled: message.enabled, state: message.enabled ? "on" : "off" };
        return callback({ ok: true });
      }
      return window.popupCheckSend(message, callback);
    };
    window.popupCheckRefresh = () => new Promise((resolve) => {
      window.popupCheckRefreshed = resolve;
      document.querySelector("#btn-refresh-tabs").click();
    });
    return saved;
  });
  await evaluate(async () => {
    window.popupCheckStatus = { connected: true, tabs: [], notes: [] };
    const refresh = window.popupCheckRefresh;
    await refresh();
    const toggle = document.querySelector("#network-toggle");
    if (!toggle.disabled) throw new Error("A browser bridge alone enabled remote browsing");
    if (document.querySelector("#status-text").textContent !== "Connected" || document.querySelector("#network-ready").textContent !== "Not ready") {
      throw new Error("The original connection must remain separate from remote readiness");
    }
    window.popupCheckNetwork.ssh = { state: "connecting", host: "dev-host", sessionId: "fixture" };
    await refresh();
    if (!toggle.disabled) throw new Error("Unverified SSH startup enabled remote browsing");
    window.popupCheckStatus.connected = false;
    await refresh();
    if (document.querySelector("#status-text").textContent !== "Disconnected" ||
        document.querySelector("#ssh-status").textContent !== "Connecting...") {
      throw new Error("SSH progress must not replace agent connection status");
    }
    window.popupCheckNetwork = { enabled: false, state: "off", host: "dev-host", canEnable: true, ssh: { state: "connected", host: "dev-host", sessionId: "fixture", proxyPort: 12345 } };
    await refresh();
    if (document.querySelector("#status-text").textContent !== "Disconnected" ||
        document.querySelector("#ssh-status").textContent !== "Connected" ||
        document.querySelector("#remote-status").textContent !== "Off" ||
        document.querySelector("#network-ready").textContent !== "Ready") {
      throw new Error("Remote readiness incorrectly implied that the original connection was up");
    }
    window.popupCheckStatus.connected = true;
    await refresh();
    if (document.querySelector("#connection-toggle").getAttribute("aria-expanded") !== "true") document.querySelector("#connection-toggle").click();
    if (!toggle.getClientRects().length) throw new Error("Connection settings did not reveal the remote switch");
    toggle.click();
    const enable = document.querySelector("#network-enable");
    if (document.querySelector("#network-confirm").hidden || enable.disabled || document.activeElement !== enable) {
      throw new Error("Enable confirmation must be immediately usable and focused");
    }
    window.popupCheckActionError = "Proxy policy changed";
    enable.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await refresh();
    if (toggle.checked || document.querySelector("#network-error").hidden) throw new Error("Polling hid a failed enable action");
    if (document.querySelectorAll(".connection-error:not([hidden])").length !== 1 ||
        document.querySelector("#network-error-details").open ||
        document.querySelector("#network-error-detail").textContent !== window.popupCheckActionError ||
        document.querySelector("#network-error").textContent.includes(window.popupCheckActionError)) {
      throw new Error("A failed action needs one plain message with its original error in collapsed details");
    }
    delete window.popupCheckActionError;
    toggle.click();
    enable.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    if (!toggle.checked) throw new Error("Confirmed remote routing was not shown as on");
    if (document.querySelector("#status-text").textContent !== "Connected" ||
        document.querySelector("#remote-status").textContent !== "On") {
      throw new Error("Agent connection and enabled remote browsing must have separate indicators");
    }
    document.querySelector("#connection-toggle").click();
    window.popupCheckNetwork = { ...window.popupCheckNetwork, state: "unavailable", canEnable: false, ssh: { state: "disconnected", error: "Helper unavailable" } };
    await refresh();
    if (!toggle.checked || toggle.disabled) throw new Error("SSH loss must leave the checked OFF control usable");
    if (document.querySelectorAll(".connection-error:not([hidden])").length !== 1) {
      throw new Error("SSH loss and remote browsing failure must share one warning");
    }
    if (document.querySelector("#network-ready").textContent !== "Not ready" || document.querySelector("#network-error").hidden) {
      throw new Error("A failed remote route was presented as healthy");
    }
    if (document.querySelector("#status-text").textContent !== "Connected" ||
        document.querySelector("#ssh-status").textContent !== "Error" ||
        document.querySelector("#remote-status").textContent !== "On, not ready") {
      throw new Error("SSH loss must not hide a working agent bridge or a retained remote route");
    }
    document.querySelector("#connection-toggle").click();
    toggle.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    if (toggle.checked) throw new Error("OFF remained checked after helper loss");
    const previous = window.popupCheckNetwork;
    window.popupCheckNetwork = { error: "Status poll failed" };
    await refresh();
    if (document.querySelector("#network-error").hidden) throw new Error("A failed status poll was hidden");
    if (document.querySelector("#ssh-status").textContent !== "Unknown" ||
        document.querySelector("#remote-status").textContent !== "Unknown") {
      throw new Error("Failed polling must not present cached SSH or route status as current");
    }
    window.popupCheckNetwork = previous;
    await refresh();
    if (document.querySelector("#network-error-detail").textContent.includes("Status poll failed")) {
      throw new Error("A recovered poll left stale details instead of the current SSH error");
    }
    window.popupCheckNetwork = {
      enabled: false, state: "off", canEnable: false,
      ssh: { state: "disconnected", error: "Tailscale SSH requires reauthentication at https://login.tailscale.com/a/fixture-with-a-long-authentication-token" },
    };
    await refresh();
    if (document.querySelector("#status-text").textContent !== "Connected" ||
        document.querySelector("#ssh-status").textContent !== "Error" ||
        document.querySelector("#remote-status").textContent !== "Off" ||
        document.querySelector("#network-error").hidden) {
      throw new Error("SSH authentication failure must stay visible without changing agent or browsing status");
    }
    window.popupCheckNetwork.message = window.popupCheckNetwork.ssh.error;
    await refresh();
    const details = document.querySelector("#network-error-details");
    if (details.hidden || details.open || document.querySelector("#network-error-detail").textContent !== window.popupCheckNetwork.ssh.error) {
      throw new Error("SSH diagnostics must remain available once, with details initially collapsed");
    }
    details.querySelector("summary").click();
    if (!document.querySelector("#network-error-detail").getClientRects().length) throw new Error("Technical details cannot be expanded");
    details.querySelector("summary").click();
    document.querySelector("#connection-toggle").click();
    if (!document.querySelector("#ssh-status").getClientRects().length) throw new Error("Collapsing settings hid the SSH failure");
    if (!document.querySelector("#network-error").getClientRects().length) throw new Error("Collapsing settings hid the only explanation of the failure");
    window.popupCheckNetwork = { enabled: false, state: "off", canEnable: true, ssh: { state: "connected", host: "dev-host", sessionId: "fixture" } };
    await refresh();
    if (!document.querySelector("#network-error").hidden || !details.hidden) throw new Error("A recovered connection left a stale warning");
    window.popupCheckNetwork.ssh.error = "Stale message from a previous failure";
    await refresh();
    if (document.querySelector("#ssh-status").textContent !== "Connected" ||
        !document.querySelector("#network-error").hidden || !details.hidden) {
      throw new Error("A live tunnel must not report a stale helper message as a failure");
    }
  });
  for (const width of [384, 320]) {
    await cdp("Emulation.setDeviceMetricsOverride", { width, height: 600, deviceScaleFactor: 1, mobile: false });
    for (const state of ["disconnected", "ready", "on", "blocked", "offline", "reconnecting", "ssh-error"]) {
      await evaluate(async (state) => {
        const enabled = !["ready", "disconnected", "ssh-error"].includes(state);
        const disconnected = state === "disconnected" || state === "offline" || state === "blocked";
        window.popupCheckStatus = { connected: ["ready", "on", "blocked", "ssh-error"].includes(state), tabs: [], notes: [] };
        window.popupCheckNetwork = {
          enabled, state: !enabled ? "off" : state === "on" ? "on" : "unavailable",
          host: "dev-host", canEnable: state === "ready",
          ssh: {
            state: state === "reconnecting" ? "connecting" : disconnected || state === "ssh-error" ? "disconnected" : "connected",
            host: "dev-host", sessionId: "fixture", proxyPort: 12345,
            error: state === "ssh-error" ? "Tailscale SSH requires reauthentication at https://login.tailscale.com/a/fixture-with-a-long-authentication-token" : "",
          },
        };
        await window.popupCheckRefresh();
        if (document.querySelector("#connection-toggle").getAttribute("aria-expanded") !== "true") document.querySelector("#connection-toggle").click();
        const toggle = document.querySelector("#network-toggle");
        if (state === "disconnected" || state === "ssh-error") {
          if (!toggle.disabled) throw new Error("An unavailable remote route must not be enabled");
        } else {
          toggle.focus();
          if (document.activeElement !== toggle || !toggle.getClientRects().length) throw new Error("Remote control must stay keyboard-accessible in connection settings");
        }
      }, state);
      await checkLayout();
      await screenshot(`${width}-remote-${state}`);
    }
  }
  await evaluate(() => document.querySelector("#connection-toggle").click());
  for (const width of [384, 320]) {
    await cdp("Emulation.setDeviceMetricsOverride", { width, height: 600, deviceScaleFactor: 1, mobile: false });
    for (const populated of [false, true]) {
      await evaluate(async (populated) => {
        const canvas = document.createElement("canvas");
        canvas.width = 1200;
        canvas.height = 800;
        const context = canvas.getContext("2d");
        context.fillStyle = "#f1f5f9";
        context.fillRect(0, 0, 1200, 800);
        context.fillStyle = "#0284c7";
        context.fillRect(64, 180, 500, 200);
        window.popupCheckStatus = {
          connected: populated,
          activeTabId: "10",
          tabs: populated ? [{ id: "10", active: true, inGroup: true, title: "A long page title that must not squeeze the toolbar or buttons", url: "https://example.test/a/long/path" }] : [],
          notes: populated ? [{ comment: "Keep the primary action aligned with the text input.", payload: { target: { selector: 'main > section.settings-panel > form.account-settings > button[type="submit"]' } } }] : [],
        };
        window.popupCheckNetwork = {
          enabled: false, state: "off", canEnable: populated,
          ssh: { state: populated ? "connected" : "disconnected", host: populated ? "dev-host" : "", sessionId: "fixture" },
        };
        await chrome.storage.local.set({
          recent_hosts: ["reviewer@very-long-remote-development-host.example.test"],
          tether_screenshots: populated ? [{ filename: "popup-layout-check.png", data: canvas.toDataURL("image/png").split(",")[1], label: "A long screenshot title that must not squeeze the delete button", url: "https://example.test/a/long/path", dimensions: "1200 × 800", remotePath: "/tmp/tether-screenshots/a-long-screenshot-filename.png", comment: "Align the action with the form fields." }] : [],
        });
        await window.popupCheckRefresh();
      }, populated);
      for (const panel of ["notes", "shots"]) {
        await evaluate(async (panel, populated) => {
          document.querySelector(`#tab-btn-${panel}`).click();
          for (let i = 0; i < 100; i++) {
            if (document.querySelector(`#${panel}-badge`).textContent === String(Number(populated))) return;
            await new Promise((resolve) => setTimeout(resolve, 20));
          }
          throw new Error("Popup did not render the expected fixture");
        }, panel, populated);
        await checkLayout();
        if (width === 384 && !populated) {
          const height = await evaluate(() => document.documentElement.scrollHeight);
          assert(height <= 600, `Empty ${panel} popup is ${height}px; Chrome's height limit is 600px`);
        }
        await screenshot(`${width}-${populated ? "populated" : "empty"}-${panel}`);
      }
    }
  }
  await evaluate(() => document.querySelector("#tab-btn-notes").focus());
  await cdp("Input.dispatchKeyEvent", { type: "keyDown", key: "ArrowRight", code: "ArrowRight", windowsVirtualKeyCode: 39 });
  assert(await evaluate(() => document.activeElement.id === "tab-btn-shots" && !document.querySelector("#panel-shots").hidden), "Arrow keys must switch and focus tabs");
  await evaluate(async () => {
    const input = document.querySelector(".shot-comment");
    input.focus();
    input.value = "Feedback must survive status polling";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 2200));
    if (document.activeElement !== input) throw new Error("Status refresh stole feedback focus");
    const saved = await chrome.storage.local.get("tether_screenshots");
    if (saved.tether_screenshots[0].comment !== input.value) throw new Error("Feedback was not saved");
    document.querySelector(".shot-thumb-wrapper").focus();
    document.querySelector(".shot-thumb-wrapper").click();
  });
  assert(await evaluate(() => document.querySelector("dialog").open), "Thumbnail must open the preview");
  await screenshot("preview");
  await cdp("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
  assert(await evaluate(() => !document.querySelector("dialog").open && document.activeElement.matches(".shot-thumb-wrapper")), "Escape must dismiss the preview and restore focus");
  const largeCapture = await evaluate(async () => {
    const canvas = document.createElement("canvas");
    canvas.width = 2048;
    canvas.height = 1536;
    const context = canvas.getContext("2d");
    const pixels = context.createImageData(canvas.width, canvas.height);
    let seed = 1;
    for (let i = 0; i < pixels.data.length; i += 4) {
      seed ^= seed << 13;
      seed ^= seed >>> 17;
      seed ^= seed << 5;
      pixels.data[i] = seed & 255;
      pixels.data[i + 1] = (seed >>> 8) & 255;
      pixels.data[i + 2] = (seed >>> 16) & 255;
      pixels.data[i + 3] = 255;
    }
    context.putImageData(pixels, 0, 0);
    const data = canvas.toDataURL("image/png").split(",")[1];
    if (data.length <= chrome.storage.local.QUOTA_BYTES) throw new Error("Fixture must exceed the default local-storage quota");
    const before = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
    const filename = "popup-quota-check.png";
    const send = chrome.runtime.sendMessage;
    chrome.runtime.sendMessage = (message, callback) => {
      if (message.type === "popup_capture_screenshot") return callback({ data, filename });
      if (message.type === "popup_delete_screenshot" && message.filename === filename) return callback({ ok: true });
      return send(message, callback);
    };
    const capture = document.querySelector("#btn-capture-viewport");
    capture.click();
    while (capture.disabled) await new Promise((resolve) => setTimeout(resolve, 20));
    const shots = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
    if (shots[0].data !== data || JSON.stringify(shots.slice(1)) !== JSON.stringify(before)) {
      throw new Error("Large capture failed to persist or discarded previous screenshots");
    }
    const thumbnail = document.querySelector(".shot-thumb");
    await thumbnail.decode();
    if (thumbnail.naturalWidth !== 2048 || thumbnail.naturalHeight !== 1536) throw new Error("Large capture preview is invalid");
    const removed = new Promise((resolve) => {
      const listener = (changes, area) => {
        if (area !== "local" || !changes.tether_screenshots) return;
        chrome.storage.onChanged.removeListener(listener);
        resolve();
      };
      chrome.storage.onChanged.addListener(listener);
    });
    document.querySelector(".btn-del-shot").click();
    await removed;
    const remaining = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
    if (JSON.stringify(remaining) !== JSON.stringify(before)) throw new Error("Deleting the large capture changed other screenshots");
    chrome.runtime.sendMessage = send;
    return { storedBytes: data.length, defaultQuotaBytes: chrome.storage.local.QUOTA_BYTES };
  });
  console.log(`PASS: large PNG stored and deleted without losing previous screenshots (${largeCapture.storedBytes} bytes; default quota ${largeCapture.defaultQuotaBytes}).`);
  await evaluate(async () => {
    const status = await window.popupCheckSend({ type: "popup_get_status" });
    if (status.connected) throw new Error("Capture checks require a disposable, disconnected profile");
    window.captureCheckTab = await chrome.tabs.create({ url: "data:text/html,", active: true });
    window.captureCheckCommand = (method, params = {}) => chrome.debugger.sendCommand({ tabId: window.captureCheckTab.id }, method, params);
    const attached = await window.popupCheckSend({ type: "popup_capture_screenshot", tabId: window.captureCheckTab.id });
    if (attached.error) throw new Error(attached.error);
    await window.captureCheckCommand("Emulation.setDeviceMetricsOverride", { width: 900, height: 600, deviceScaleFactor: 1, mobile: false });
    await window.captureCheckCommand("Runtime.evaluate", { expression: `
      document.title = 'Capture regression fixture';
      document.body.style.cssText = 'margin:0;width:1200px;height:2400px;background:linear-gradient(to bottom,#dc2626 0 600px,#16a34a 600px 1600px,#2563eb 1600px);background-size:1200px 2400px;background-repeat:no-repeat';
      window.capturePageClicks = 0;
      document.addEventListener('click', () => window.capturePageClicks++);
    ` });
    window.captureCheckStart = async () => {
      const result = await window.popupCheckSend({ type: "popup_start_crop", tabId: window.captureCheckTab.id });
      if (result.error) throw new Error(result.error);
    };
    window.captureCheckLatest = async (previous) => {
      for (let i = 0; i < 1500; i++) {
        const shots = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
        if (shots[0]?.filename !== previous) return shots[0];
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      throw new Error("Capture did not reach the gallery");
    };
    window.captureCheckImage = async (shot) => {
      const image = new Image();
      image.src = "data:image/png;base64," + shot.data;
      await image.decode();
      const canvas = document.createElement("canvas");
      canvas.width = image.naturalWidth;
      canvas.height = image.naturalHeight;
      const context = canvas.getContext("2d");
      context.drawImage(image, 0, 0);
      const pixel = (x, y) => [...context.getImageData(x, y, 1, 1).data];
      return { data: shot.data, width: canvas.width, height: canvas.height, top: pixel(2, 2), bottom: pixel(canvas.width - 30, canvas.height - 30), dimensions: shot.dimensions };
    };
  });
  for (const [scale, zoom] of [[1, 1], [1.25, 1], [2, 1.25], [2, 1]]) {
    await evaluate(async (scale, zoom) => {
      await window.captureCheckCommand("Emulation.setDeviceMetricsOverride", { width: 900, height: 600, deviceScaleFactor: scale, mobile: false });
      await chrome.tabs.setZoom(window.captureCheckTab.id, zoom);
      await window.captureCheckCommand("Runtime.evaluate", { expression: "scrollTo(0,0); new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))", awaitPromise: true });
    }, scale, zoom);
    for (const mode of ["viewport", "full"]) {
      const image = await evaluate(async (mode, scale) => {
        if (mode === "full" && scale === 2) {
          await window.captureCheckCommand("Runtime.evaluate", { expression: "scrollTo(100,700); new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))", awaitPromise: true });
        }
        const previous = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots[0].filename;
        document.querySelector(`#btn-capture-${mode}`).click();
        return window.captureCheckImage(await window.captureCheckLatest(previous));
      }, mode, scale);
      await writeFile(join(captures, `${mode}-${scale}x-zoom-${zoom}.png`), Buffer.from(image.data, "base64"));
      assert.equal(image.width, mode === "full" ? 1200 : 900 / zoom);
      assert.equal(image.height, mode === "full" ? 2400 : 600 / zoom);
      assert.deepEqual(image.top, [220, 38, 38, 255]);
      assert.deepEqual(image.bottom, mode === "full" ? [37, 99, 235, 255] : [220, 38, 38, 255], `${mode} at ${scale}x: ${captures}`);
      assert.equal(image.dimensions, `${image.width}×${image.height}`, "Gallery dimensions must describe the PNG, not the popup");
    }
  }
  const previousCrop = await evaluate(async () => {
    await window.captureCheckCommand("Runtime.evaluate", { expression: "scrollTo(100,700)" });
    const previous = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots[0].filename;
    await window.captureCheckStart();
    await window.captureCheckStart(); // Replacing a selection must not let its old poll capture or dismiss the new one.
    await new Promise((resolve) => setTimeout(resolve, 250));
    await window.captureCheckCommand("Input.dispatchMouseEvent", { type: "mousePressed", x: 400, y: 320, button: "left", buttons: 1, clickCount: 1 });
    await window.captureCheckCommand("Input.dispatchMouseEvent", { type: "mouseMoved", x: 100, y: 140, button: "left", buttons: 1 });
    return previous;
  });
  const selection = await evaluate(() => window.captureCheckCommand("Page.captureScreenshot", { format: "png" }));
  await writeFile(join(captures, "area-selection.png"), Buffer.from(selection.data, "base64"));
  const cropImage = await evaluate(async (previous) => {
    await window.captureCheckCommand("Input.dispatchMouseEvent", { type: "mouseReleased", x: 100, y: 140, button: "left", buttons: 0, clickCount: 1 });
    return window.captureCheckImage(await window.captureCheckLatest(previous));
  }, previousCrop);
  assert.equal(cropImage.width, 300, "Reverse drag must capture 300 image pixels for 300 CSS pixels on a retina display");
  assert.equal(cropImage.height, 180, "Reverse drag must capture 180 image pixels for 180 CSS pixels on a retina display");
  assert.deepEqual(cropImage.top, [22, 163, 74, 255], "Crop must use scroll offsets and exclude the dimming overlay");
  assert.deepEqual(cropImage.bottom, [22, 163, 74, 255], "Crop must exclude the selection border and controls");
  const keyboardImage = await evaluate(async () => {
    const previous = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots[0].filename;
    await window.captureCheckStart();
    await window.captureCheckCommand("Input.dispatchKeyEvent", { type: "keyDown", key: "ArrowRight" });
    await window.captureCheckCommand("Input.dispatchKeyEvent", { type: "keyDown", key: "ArrowDown", modifiers: 8 });
    await window.captureCheckCommand("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter" });
    return window.captureCheckImage(await window.captureCheckLatest(previous));
  });
  assert.equal(keyboardImage.width, 450);
  assert.equal(keyboardImage.height, 310, "Keyboard resize must change the captured rectangle");
  assert.deepEqual(keyboardImage.top, [22, 163, 74, 255]);
  const cancelled = await evaluate(async () => {
    const before = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
    await window.captureCheckStart();
    await window.captureCheckCommand("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
    await new Promise((resolve) => setTimeout(resolve, 300));
    const after = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
    const beforeClick = await window.captureCheckCommand("Runtime.evaluate", { expression: "window.capturePageClicks", returnByValue: true });
    await window.captureCheckCommand("Input.dispatchMouseEvent", { type: "mousePressed", x: 200, y: 200, button: "left", buttons: 1, clickCount: 1 });
    await window.captureCheckCommand("Input.dispatchMouseEvent", { type: "mouseReleased", x: 200, y: 200, button: "left", buttons: 0, clickCount: 1 });
    const afterClick = await window.captureCheckCommand("Runtime.evaluate", { expression: "window.capturePageClicks", returnByValue: true });
    return { unchanged: JSON.stringify(before) === JSON.stringify(after), beforeClick: beforeClick.result.value, afterClick: afterClick.result.value };
  });
  assert(cancelled.unchanged, "Escape must not save a screenshot");
  assert.equal(cancelled.beforeClick, 0, "Selecting a crop must not click the underlying page");
  assert.equal(cancelled.afterClick, 1, "Cancellation must restore page interaction");
  console.log("PASS: CSS-resolution viewport/full-page PNGs at 1x/1.25x/2x display density; reverse drag on a scrolled page; replacement, keyboard resize, Escape, and no click-through.");
  await evaluate(async () => {
    chrome.runtime.sendMessage = window.popupCheckSend;
    const pause = () => new Promise((resolve) => setTimeout(resolve, 30));
    const popup = () => chrome.extension.getViews({ type: "popup" })[0];
    const waitPopup = async (panel, text = "") => {
      for (let i = 0; i < 200; i++) {
        const view = popup();
        const content = view?.document.querySelector(`#panel-${panel}`);
        if (content && !content.hidden && content.textContent.includes(text)) return view;
        await pause();
      }
      throw new Error(`Native popup did not return to ${panel}: ${text}; ${JSON.stringify({
        popup: popup()?.location.href,
        content: popup()?.document.querySelector(`#panel-${panel}`)?.textContent,
        focused: (await chrome.windows.get(window.captureCheckTab.windowId)).focused,
        notes: await window.captureCheckCommand("Runtime.evaluate", { expression: "window.__tetherReview.getNotes().map(n => n.comment)", returnByValue: true }),
      })}`);
    };
    const waitClosed = async () => {
      for (let i = 0; i < 100 && popup(); i++) await pause();
      if (popup()) throw new Error("Starting a capture did not close the native popup");
    };
    const clickTarget = async () => {
      await window.captureCheckCommand("Input.dispatchMouseEvent", { type: "mousePressed", x: 100, y: 75, button: "left", buttons: 1, clickCount: 1 });
      await window.captureCheckCommand("Input.dispatchMouseEvent", { type: "mouseReleased", x: 100, y: 75, button: "left", buttons: 0, clickCount: 1 });
    };
    const editor = async (expression) => {
      const result = await window.captureCheckCommand("Runtime.evaluate", { expression });
      if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    };
    popup()?.close();
    await waitClosed();
    await editor(`
      const target = document.createElement('button');
      target.style.cssText = 'position:fixed;left:50px;top:50px;width:180px;height:60px';
      target.textContent = 'Review target';
      document.body.append(target);
    `);
    await chrome.windows.update(window.captureCheckTab.windowId, { focused: true });
    await chrome.action.openPopup({ windowId: window.captureCheckTab.windowId });
    let view = await waitPopup("shots");
    view.document.querySelector("#tab-btn-notes").click();
    view.document.querySelector("#btn-inspect").click();
    await waitClosed();
    await clickTarget();
    await editor(`
      if (!window.__tetherReview.modal.isConnected || !window.__tetherReview.modal.getBoundingClientRect().height) throw new Error('Note editor is not visible');
      window.__tetherReview.modal.querySelector('#card-comment').value = 'Automatic popup return';
      window.__tetherReview.modal.querySelector('#card-save').click();
    `);
    view = await waitPopup("notes", "Automatic popup return");
    for (const panel of ["shots", "notes"]) {
      const shotsTab = view.document.querySelector("#tab-btn-shots");
      if (panel === "shots") shotsTab.click();
      else shotsTab.dispatchEvent(new view.KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true }));
      view.close();
      await waitClosed();
      await chrome.action.openPopup({ windowId: window.captureCheckTab.windowId });
      view = await waitPopup(panel, panel === "notes" ? "Automatic popup return" : "Area Crop");
    }
    view.document.querySelector("#btn-inspect").click();
    await waitClosed();
    await clickTarget();
    await editor("window.__tetherReview.modal.querySelector('#card-cancel').click()");
    await new Promise((resolve) => setTimeout(resolve, 300));
    if (popup()) throw new Error("Cancelling a note reopened the popup");

    await editor(`
      window.__tetherReview.markerElements.values().next().value.element.click();
      window.__tetherReview.modal.querySelector('#card-comment').value = 'Updated popup note';
      window.__tetherReview.modal.querySelector('#card-save').click();
    `);
    view = await waitPopup("notes", "Updated popup note");
    if (view.document.querySelectorAll(".note-item").length !== 1) throw new Error("Editing a note created a duplicate");
    view.document.querySelector("#tab-btn-shots").click();
    view.document.querySelector("#btn-capture-area").click();
    await waitClosed();
    await window.captureCheckCommand("Input.dispatchKeyEvent", { type: "keyDown", key: "ArrowRight" });
    await window.captureCheckCommand("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter" });
    view = await waitPopup("shots", "450×300 px");
    view.document.querySelector("#btn-capture-area").click();
    await waitClosed();
    await window.captureCheckCommand("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
    await new Promise((resolve) => setTimeout(resolve, 300));
    if (popup()) throw new Error("Cancelling a crop reopened the popup");

    const otherTab = await chrome.tabs.create({ url: "about:blank", active: true });
    try {
      await editor(`
        window.__tetherReview.markerElements.values().next().value.element.click();
        window.__tetherReview.modal.querySelector('#card-save').click();
      `);
      await new Promise((resolve) => setTimeout(resolve, 300));
      if (popup() || !(await chrome.tabs.get(otherTab.id)).active) throw new Error("Saving a background-tab note stole focus");
    } finally {
      await chrome.tabs.remove(otherTab.id);
    }
    const otherWindow = await chrome.windows.create({ url: "about:blank", focused: true });
    try {
      await editor(`
        window.__tetherReview.markerElements.values().next().value.element.click();
        window.__tetherReview.modal.querySelector('#card-save').click();
      `);
      await new Promise((resolve) => setTimeout(resolve, 300));
      if (popup() || (await chrome.windows.getLastFocused()).id !== otherWindow.id) throw new Error("Saving a background-window note stole focus");
    } finally {
      await chrome.windows.remove(otherWindow.id);
    }
  });
  console.log("PASS: native popup remembers mouse/keyboard tab selection across openings; saves return to the correct panel; cancellations and background saves preserve focus.");
  console.log(`PASS: 320px/384px, empty/populated tabs, overflow, keyboard, preview, feedback persistence. Captures: ${captures}`);
} finally {
  if (saved) await evaluate(async (saved) => {
    if (window.captureCheckTab) await chrome.tabs.remove(window.captureCheckTab.id);
    for (const key of ["captureCheckTab", "captureCheckCommand", "captureCheckStart", "captureCheckLatest", "captureCheckImage"]) delete window[key];
    chrome.runtime.sendMessage = window.popupCheckSend;
    delete window.popupCheckSend;
    delete window.popupCheckStatus;
    delete window.popupCheckNetwork;
    delete window.popupCheckActionError;
    delete window.popupCheckRefresh;
    delete window.popupCheckRefreshed;
    await chrome.storage.local.remove(["recent_hosts", "tether_screenshots", "tether_popup_tab"]);
    await chrome.storage.local.set(saved);
  }, saved);
  socket.close();
}
