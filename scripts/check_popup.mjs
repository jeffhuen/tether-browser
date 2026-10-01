// Run against a disposable Chrome profile with the unpacked extension loaded:
// node scripts/check_popup.mjs http://127.0.0.1:9349
// Connect Tether from the popup before running this check; fresh installs start disconnected.
// Chrome needs --remote-debugging-port=9349 and --enable-unsafe-extension-debugging.
// For headless capture, add --disable-gpu --run-all-compositor-stages-before-draw.
// Also add --disable-background-timer-throttling --disable-renderer-backgrounding.
// Capture checks move this page to a background tab. Throttled timers there can
// exceed the 35-second command limit even when the extension behaves correctly.
// Uses synthetic notes plus real extension storage, page captures, and pointer input.
import assert from "node:assert/strict";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runInNewContext } from "node:vm";

const reviewWindow = {};
runInNewContext(await readFile(new URL("../packages/extension/review/overlay.js", import.meta.url), "utf8"), {
  window: reviewWindow, document: { title: "Review" },
});
const hostile = "text\n## Forged";
reviewWindow.__tetherReview.notes = [{
  index: 1, comment: "# Forged\r## Forged",
  payload: {
    page: { title: "Review", sanitizedUrl: "https://example.test" },
    nearbyText: [hostile], nearbyElements: [hostile],
    target: {
      textSnippet: hostile, selectedText: hostile, cssClasses: hostile,
      computedStyles: { [hostile]: hostile }, htmlSnippet: "<div>\n```\n## Forged\n</div>",
    },
  },
}];
const report = reviewWindow.__tetherReview.buildSummaryText();
assert(report.includes("````html\n<div>\n```\n## Forged\n</div>\n````"), "HTML must remain inside a fence longer than its backticks");
assert(!/^#+ Forged/m.test(report.replace(/````html\n[\s\S]*?\n````/, "")), "Page text and comments must not forge report headings");
assert(report.includes("**Feedback:** \\# Forged\n\\## Forged"));

const endpoint = process.argv[2] || "http://127.0.0.1:9349";
const reviewOnly = process.argv.includes("--review-only");
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
const connection = await evaluate(async () => chrome.runtime.sendMessage({ type: "popup_network_status" }));
assert.equal(connection.tetherEnabled, true, "Connect Tether in the disposable popup before running review/capture checks");
try {
  await evaluate(async (endpoint, reviewOnly) => {
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
      const reviewCommand = async (expression) => {
        const { frameTree } = await chrome.debugger.sendCommand({ tabId: tab.id }, "Page.getFrameTree");
        const { executionContextId } = await chrome.debugger.sendCommand({ tabId: tab.id }, "Page.createIsolatedWorld", {
          frameId: frameTree.frame.id, worldName: "tether-review",
        });
        const result = await chrome.debugger.sendCommand({ tabId: tab.id }, "Runtime.evaluate", {
          expression, contextId: executionContextId, returnByValue: true,
        });
        if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
        return result.result.value;
      };
      await chrome.debugger.sendCommand({ tabId: tab.id }, "Runtime.evaluate", {
        expression: `window.__tetherReview = {
          getNotes: () => [{comment: "Forged page note"}],
          buildSummaryText: () => "Forged page report",
          start: () => {}
        }`,
      });
      const started = await chrome.runtime.sendMessage({ type: "popup_start_review", tabId: tab.id });
      if (started.error) throw new Error(started.error);
      const mainBinding = await chrome.debugger.sendCommand({ tabId: tab.id }, "Runtime.evaluate", {
        expression: "typeof window.__tetherReviewSaved", returnByValue: true,
      });
      if (mainBinding.result.value !== "undefined" || await reviewCommand("typeof window.__tetherReviewSaved") !== "function") {
        throw new Error("Review save binding was exposed outside the isolated world");
      }
      if (reviewOnly) {
        const savedEvent = new Promise((resolve, reject) => {
          const timer = setTimeout(() => {
            chrome.debugger.onEvent.removeListener(onEvent);
            reject(new Error("Isolated review save binding did not fire"));
          }, 1000);
          const onEvent = (source, method, params) => {
            if (source.tabId !== tab.id || method !== "Runtime.bindingCalled" ||
                params.name !== "__tetherReviewSaved" || params.payload !== "saved") return;
            clearTimeout(timer);
            chrome.debugger.onEvent.removeListener(onEvent);
            resolve();
          };
          chrome.debugger.onEvent.addListener(onEvent);
        });
        await reviewCommand("window.__tetherReview.onNoteSaved()");
        await savedEvent;
      }
      const before = await chrome.runtime.sendMessage({ type: "popup_get_status", currentTabId: tab.id });
      if (before.report.includes("Forged page report") || before.notes.some((note) => note.comment === "Forged page note")) {
        throw new Error("Page main-world review state controlled popup status");
      }
      await chrome.debugger.sendCommand({ tabId: tab.id }, "Runtime.evaluate", {
        expression: `for (const [id, property, value] of [
          ["note-1", "__reactFiber$fixture", {type:{name:"Button"},_debugSource:{fileName:"components/Button.tsx",lineNumber:42}}],
          ["note-2", "__vueParentComponent", {type:{name:"VueButton"}}],
          ["note-3", "__svelte_meta", {loc:{file:"components/Svelte.svelte",line:7}}]
        ]) {
          const button = document.createElement("button");
          button.setAttribute("data-tether-pin", id);
          button[property] = value;
          document.body.append(button);
        }`,
      });
      await reviewCommand(`window.__tetherReview.notes = [1, 2, 3].map(index => ({
        id: "note-" + index, index, comment: "Copied from the page",
        payload: {target: {tagName: "button", framework: {name: "Static"}}}
      }))`);
      const status = await chrome.runtime.sendMessage({ type: "popup_get_status", currentTabId: tab.id });
      const reportCheck = await reviewCommand("window.__tetherReview.buildSummaryText()");
      if (status.report !== reportCheck || !status.report.includes("Copied from the page") ||
          !status.report.includes("components/Button.tsx:42") || !status.report.includes("components/Svelte.svelte:7") ||
          status.notes[0]?.payload?.target?.framework?.name !== "React" ||
          status.notes[1]?.payload?.target?.framework?.component !== "<VueButton>" ||
          status.notes[2]?.payload?.target?.framework?.name !== "Svelte") {
        throw new Error("Popup report lost isolated notes or page framework metadata");
      }
      const cleared = await chrome.runtime.sendMessage({ type: "popup_clear_notes", tabId: tab.id });
      if (cleared.error || (await reviewCommand("window.__tetherReview.getNotes()")).length) {
        throw new Error("Could not clear isolated review notes");
      }
      const crop = await chrome.runtime.sendMessage({ type: "popup_start_crop", tabId: tab.id });
      if (crop.error || !await reviewCommand("Boolean(window.__tetherReview.cropRequestId)")) {
        throw new Error("Could not start isolated area selection");
      }
      await chrome.debugger.sendCommand({ tabId: tab.id }, "Input.dispatchKeyEvent", {
        type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27,
      });
      let cancelled = false;
      for (let i = 0; i < 20 && !cancelled; i++) {
        cancelled = await reviewCommand("window.__tetherReview.cropResult?.cancelled === true && window.__tetherReview.finishCrop === null");
        if (!cancelled) await new Promise((resolve) => setTimeout(resolve, 20));
      }
      if (!cancelled) throw new Error("Isolated area selection did not cancel");
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
  }, endpoint, reviewOnly);
  if (reviewOnly) {
    console.log("PASS: main-world spoof cannot control review report, notes, save binding, or crop");
    socket.close();
    process.exit(0);
  }
  saved = await evaluate(async () => {
    const saved = await chrome.storage.local.get(["recent_hosts", "tether_screenshots", "tether_popup_tab"]);
    window.popupCheckSend = chrome.runtime.sendMessage.bind(chrome.runtime);
    window.popupCheckNetwork = { tetherEnabled: true, nativeConnected: true, enabled: false, state: "off", canEnable: true,
      ssh: { state: "connected", host: "dev-host", sessionId: "fixture", proxyPort: 12345 } };
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
      if (message.type === "popup_network_signin") {
        return window.popupCheckSignInNetwork.openSignIn(message.sessionId, message.turnOff).then((result) => {
          window.popupCheckNetwork = { ...window.popupCheckNetwork, enabled: false, state: "off" };
          callback(result);
        }, (error) => callback({ error: error.message }));
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
    window.popupCheckStatus = { tetherEnabled: true, connected: true, tabs: [], notes: [] };
    await window.popupCheckRefresh();
  });
  for (const width of [384, 320]) {
    await cdp("Emulation.setDeviceMetricsOverride", { width, height: 600, deviceScaleFactor: 1, mobile: false });
    for (const state of ["disconnected", "ready", "on", "blocked", "offline", "reconnecting", "ssh-error", "signin", "signin-on", "netbird", "netbird-on", "auth-expired"]) {
      await evaluate(async (state) => {
        const tetherEnabled = state !== "disconnected";
        const enabled = ["on", "blocked", "offline", "reconnecting", "signin-on", "netbird-on"].includes(state);
        const sshConnected = ["ready", "on", "blocked"].includes(state);
        window.popupCheckStatus = { tetherEnabled, connected: tetherEnabled, tabs: [], notes: [] };
        window.popupCheckNetwork = {
          tetherEnabled, nativeConnected: tetherEnabled && state !== "offline",
          enabled, state: !enabled ? "off" : state === "on" ? "on" : "unavailable",
          host: "dev-host", canEnable: state === "ready",
          signInUrl: ["signin", "signin-on"].includes(state) ? "https://login.tailscale.com/a/fixture" : "",
          ssh: {
            state: ["reconnecting", "signin", "signin-on", "netbird", "netbird-on"].includes(state) ? "connecting" : sshConnected ? "connected" : "disconnected",
            host: "dev-host", sessionId: "fixture", proxyPort: 12345,
            authProvider: ["netbird", "netbird-on", "auth-expired"].includes(state) ? "NetBird" : "",
            authMessage: ["netbird", "netbird-on"].includes(state) ? "Complete sign-in in the browser opened by NetBird." : "",
            signInRequired: state === "auth-expired",
            error: state === "ssh-error" ? "Remote host refused the connection" : "",
          },
        };
        await window.popupCheckRefresh();
        if (document.querySelector("#connection-toggle").getAttribute("aria-expanded") !== "true") document.querySelector("#connection-toggle").click();
        const toggle = document.querySelector("#network-toggle");
        if (["disconnected", "ssh-error", "signin", "netbird", "auth-expired"].includes(state)) {
          if (!toggle.disabled) throw new Error("An unavailable remote route must not be enabled");
        }
        const signIn = document.querySelector("#network-signin");
        if (["signin", "signin-on", "netbird-on"].includes(state)) {
          if (signIn.hidden || signIn.disabled || !signIn.getClientRects().length) throw new Error("Pending sign-in must offer an explicit usable action, including with Remote on");
          signIn.focus();
          if (document.activeElement !== signIn) throw new Error("Sign-in must remain keyboard-accessible");
          if (!document.querySelector("#network-error").textContent.includes("dev-host")) throw new Error("Sign-in must identify the SSH source host");
        } else if (!signIn.hidden) {
          throw new Error("Non-Tailscale or expired authentication must not offer an arbitrary URL action");
        }
        if (["netbird", "netbird-on", "auth-expired"].includes(state)) {
          if (!document.querySelector("#network-error").textContent.includes("NetBird")) throw new Error("Provider-specific auth guidance was lost");
        }
        if (state === "ssh-error" && !document.querySelector("#network-error-detail").textContent.includes("Remote host refused")) {
          throw new Error("Actual remote error details must survive authentication guidance");
        }
        if (!["disconnected", "ssh-error", "signin", "netbird", "auth-expired"].includes(state)) {
          toggle.focus();
          if (document.activeElement !== toggle || !toggle.getClientRects().length) throw new Error("Remote control must stay keyboard-accessible in connection settings");
        }
      }, state);
      await checkLayout();
      await screenshot(`${width}-remote-${state}`);
    }
  }
  // Exercise actual popup clicks and production challenge validation in Chrome.
  // Proxy mocks are confined to this popup's JS context; tabs are really created.
  await evaluate(async () => {
    window.popupCheckProxyMethods = Object.fromEntries(["get", "set", "clear"].map((method) => [method, chrome.proxy.settings[method]]));
    window.popupCheckProxy = { levelOfControl: "controllable_by_this_extension", value: { mode: "system" } };
    chrome.proxy.settings.get = (_, callback) => callback(structuredClone(window.popupCheckProxy));
    chrome.proxy.settings.set = ({ value }, callback) => {
      window.popupCheckProxy = { levelOfControl: "controlled_by_this_extension", value };
      callback();
    };
    chrome.proxy.settings.clear = (_, callback) => {
      window.popupCheckProxy = { levelOfControl: "controllable_by_this_extension", value: { mode: "system" } };
      callback();
    };
    window.popupCheckSignInNetwork = await import("./network.js?popup-check");
    window.popupCheckStorageMethods = Object.fromEntries(["get", "set", "remove"].map((method) => [method, chrome.storage.local[method]]));
    chrome.storage.local.get = (key) => key === "tether_remote_network" ? Promise.resolve({}) :
      window.popupCheckStorageMethods.get.call(chrome.storage.local, key);
    chrome.storage.local.set = (value) => Object.hasOwn(value, "tether_remote_network") ? Promise.resolve() :
      window.popupCheckStorageMethods.set.call(chrome.storage.local, value);
    chrome.storage.local.remove = (key) => key === "tether_remote_network" ? Promise.resolve() :
      window.popupCheckStorageMethods.remove.call(chrome.storage.local, key);
    window.popupCheckSignInNetwork.initializeNetwork(async () => structuredClone(window.popupCheckSSH));
    await window.popupCheckSignInNetwork.networkStatus(false);
    window.popupCheckSignInTabs = [];
    const realCreate = chrome.tabs.create.bind(chrome.tabs);
    window.popupCheckRealCreate = chrome.tabs.create;
    chrome.tabs.create = async (details) => {
      if (window.popupCheckCreateFailure) throw new Error("No current window");
      const tab = await realCreate(details);
      window.popupCheckSignInTabs.push({ id: tab.id, url: details.url, proxy: structuredClone(window.popupCheckProxy.value) });
      return tab;
    };
    const click = async () => {
      const button = document.querySelector("#network-signin");
      if (button.hidden || button.disabled) throw new Error("Sign-in action unavailable");
      button.click();
      for (let i = 0; i < 200 && button.disabled; i++) await new Promise((resolve) => setTimeout(resolve, 20));
      if (button.disabled) throw new Error("Sign-in action never finished");
      // networkAction refreshes after it releases the busy state.
      await window.popupCheckRefresh();
    };
    const fixture = async (enabled) => {
      window.popupCheckStatus = { tetherEnabled: true, connected: false, tabs: [], notes: [] };
      window.popupCheckNetwork = { tetherEnabled: true, nativeConnected: true, enabled,
        state: enabled ? "unavailable" : "off", canEnable: false, host: "dev-host",
        signInUrl: "https://login.tailscale.com/a/OLD",
        ssh: { state: "connecting", host: "dev-host", sessionId: "signin-click", proxyPort: 12345,
          signInUrl: "https://hostile.example/not-validated" } };
      window.popupCheckSSH = { ...window.popupCheckNetwork.ssh, signInUrl: "https://login.tailscale.com/a/LATEST" };
      await window.popupCheckRefresh();
    };
    try {
      await fixture(false);
      await click();
      const latest = window.popupCheckSignInTabs.at(-1);
      const opened = await chrome.tabs.get(latest.id);
      if ((opened.pendingUrl || opened.url) !== window.popupCheckSSH.signInUrl || latest.url !== window.popupCheckSSH.signInUrl) {
        throw new Error("Popup click did not open the latest validated native challenge");
      }
      window.popupCheckSSH.sessionId = "replaced";
      await click();
      if (window.popupCheckSignInTabs.length !== 1 || !document.querySelector("#network-error-detail").textContent.includes("connection changed")) {
        throw new Error("Stale popup session opened sign-in instead of reporting the changed connection");
      }
      window.popupCheckSSH.sessionId = "signin-click";
      window.popupCheckSSH.signInUrl = "https://login.tailscale.com.evil.test/a/ABC";
      await click();
      if (window.popupCheckSignInTabs.length !== 1) throw new Error("Popup action accepted an invalid native URL");
      window.popupCheckSSH.signInUrl = "https://login.tailscale.com/a/RETRY";
      window.popupCheckCreateFailure = true;
      await click();
      if (!document.querySelector("#network-error-detail").textContent.includes("No current window")) throw new Error("Tab creation failure was not shown");
      window.popupCheckCreateFailure = false;
      await click();
      if (window.popupCheckSignInTabs.length !== 2 || window.popupCheckSignInTabs.at(-1).url !== window.popupCheckSSH.signInUrl) {
        throw new Error("The same sign-in button could not retry a failed open");
      }
      // One click, not a separate manual route switch, must handle a stalled route.
      window.popupCheckSSH = { ...window.popupCheckSSH, state: "connected" };
      await window.popupCheckSignInNetwork.setNetworkEnabled(true, "signin-click");
      await fixture(true);
      await click();
      if (window.popupCheckSignInTabs.length !== 3 || window.popupCheckSignInTabs.at(-1).proxy.mode !== "system" ||
          document.querySelector("#network-toggle").checked) throw new Error("Consented sign-in did not turn Remote browsing off before opening");
      await window.popupCheckRefresh();
      if (window.popupCheckProxy.value.mode !== "system") throw new Error("Remote browsing silently restored after sign-in");
      window.popupCheckSSH = { ...window.popupCheckSSH, state: "connected" };
      await window.popupCheckSignInNetwork.setNetworkEnabled(true, "signin-click");
      await fixture(true);
      window.popupCheckNetwork.signInUrl = "";
      window.popupCheckNetwork.ssh = { ...window.popupCheckNetwork.ssh, authProvider: "NetBird",
        authMessage: "Complete sign-in in the NetBird-opened browser." };
      window.popupCheckSSH = { ...window.popupCheckNetwork.ssh, signInUrl: "https://arbitrary-sso.example/login" };
      await window.popupCheckRefresh();
      await click();
      if (window.popupCheckProxy.value.mode !== "system" || window.popupCheckSignInTabs.length !== 3 ||
          !document.querySelector("#network-signin").hidden) {
        throw new Error("NetBird sign-in preparation must clear only the consented route, not open an arbitrary SSO tab");
      }
    } finally {
      chrome.tabs.create = window.popupCheckRealCreate;
      for (const [method, original] of Object.entries(window.popupCheckProxyMethods)) chrome.proxy.settings[method] = original;
      for (const [method, original] of Object.entries(window.popupCheckStorageMethods)) chrome.storage.local[method] = original;
      await Promise.all(window.popupCheckSignInTabs.map((tab) => chrome.tabs.remove(tab.id)));
      for (const key of ["popupCheckProxyMethods", "popupCheckProxy", "popupCheckSignInNetwork", "popupCheckSignInTabs",
        "popupCheckStorageMethods", "popupCheckRealCreate", "popupCheckCreateFailure", "popupCheckSSH"]) delete window[key];
    }
  });
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
          tetherEnabled: populated, connected: populated,
          activeTabId: "10",
          tabs: populated ? [{ id: "10", active: true, inGroup: true, title: "A long page title that must not squeeze the toolbar or buttons", url: "https://example.test/a/long/path" }] : [],
          notes: populated ? [{ comment: "Keep the primary action aligned with the text input.", payload: { target: { selector: 'main > section.settings-panel > form.account-settings > button[type="submit"]' } } }] : [],
        };
        window.popupCheckNetwork = {
          tetherEnabled: populated, nativeConnected: populated, enabled: false, state: "off", canEnable: populated,
          ssh: { state: populated ? "connected" : "disconnected", host: populated ? "dev-host" : "", sessionId: "fixture" },
        };
        await chrome.storage.local.set({
          recent_hosts: ["reviewer@very-long-remote-development-host.example.test"],
          tether_screenshots: populated ? [{ filename: "popup-layout-check.png", data: canvas.toDataURL("image/png").split(",")[1], label: "A long screenshot title that must not squeeze the delete button", url: "https://example.test/a/long/path", dimensions: "1200 × 800", mirrored: true, targetHost: "dev-host", remotePath: "/tmp/tether-screenshots/popup-layout-check.png", comment: "Align the action with the form fields." }] : [],
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
    let deletion = { ok: true, localDeleted: true, remoteDeleted: true };
    let deferDelete = false;
    let completeDeletion;
    chrome.runtime.sendMessage = (message, callback) => {
      if (message.type === "popup_capture_screenshot") return callback({ base64: data, filename,
        saveResult: { mirrored: false, remotePath: "/must-not-be-claimed.png", mirrorError: "Remote mirror unavailable" } });
      if (message.type === "popup_delete_screenshot" && message.filename === filename) {
        if (deferDelete) { completeDeletion = () => callback(deletion); return; }
        return callback(deletion);
      }
      if (message.type === "popup_clear_screenshots") return callback(deletion);
      return send(message, callback);
    };
    const capture = document.querySelector("#btn-capture-viewport");
    capture.click();
    while (capture.disabled) await new Promise((resolve) => setTimeout(resolve, 20));
    const shots = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
    if (shots[0].data !== data || JSON.stringify(shots.slice(1)) !== JSON.stringify(before)) {
      throw new Error("Large capture failed to persist or discarded previous screenshots");
    }
    if (shots[0].remotePath || shots[0].mirrored || !document.querySelector(".shot-path-chip").textContent.includes("Remote mirror unavailable")) {
      throw new Error("An unsaved capture must not claim a server path");
    }
    const writeText = navigator.clipboard.writeText;
    let copied = "";
    navigator.clipboard.writeText = async (text) => { copied = text; };
    document.querySelector("#btn-copy-shots").click();
    navigator.clipboard.writeText = writeText;
    if (!copied.includes("Remote mirror unavailable") || copied.includes("/must-not-be-claimed.png")) {
      throw new Error("The screenshot report invented a server path for an unsaved capture");
    }
    const thumbnail = document.querySelector(".shot-thumb");
    await thumbnail.decode();
    if (thumbnail.naturalWidth !== 2048 || thumbnail.naturalHeight !== 1536) throw new Error("Large capture preview is invalid");
    // Native failures must not remove gallery data or claim deletion succeeded.
    const clickDelete = async () => {
      const button = document.querySelector(".btn-del-shot");
      button.click();
      if (deferDelete) {
        while (!completeDeletion) await new Promise((resolve) => setTimeout(resolve, 20));
        const pending = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
        if (JSON.stringify(pending) !== JSON.stringify(shots)) throw new Error("Gallery disappeared before native deletion completed");
        completeDeletion();
        completeDeletion = null;
      }
      while (button.disabled) await new Promise((resolve) => setTimeout(resolve, 20));
    };
    deletion = { error: "Local screenshot file is not writable" };
    deferDelete = true;
    await clickDelete();
    deferDelete = false;
    let retained = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
    if (JSON.stringify(retained) !== JSON.stringify(shots) ||
        !document.querySelector("#update-toast").textContent.includes(deletion.error)) {
      throw new Error("Local deletion failure removed the screenshot or hid its error");
    }
    const realConfirm = window.confirm;
    window.confirm = () => true;
    try {
      const clear = document.querySelector("#btn-clear-shots");
      clear.click();
      while (clear.disabled) await new Promise((resolve) => setTimeout(resolve, 20));
      retained = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
      if (JSON.stringify(retained) !== JSON.stringify(shots)) throw new Error("Local clear failure discarded gallery data");
    } finally {
      window.confirm = realConfirm;
    }
    // Retaining remote failures is only appropriate for an actual mirrored copy.
    await chrome.storage.local.set({ tether_screenshots: [
      { ...shots[0], mirrored: true, targetHost: "dev-host", remotePath: "/remote/large-capture.png" }, ...before,
    ] });
    document.querySelector("#tab-btn-shots").click();
    while (!document.querySelector(".shot-path-chip")?.textContent.includes("/remote/large-capture.png")) {
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
    deletion = { ok: false, localDeleted: true, remoteDeleted: false, remoteError: "Existing remote screenshot could not be deleted" };
    await clickDelete();
    retained = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
    if (retained[0]?.data !== data || retained[0]?.mirrorError !== deletion.remoteError ||
        JSON.stringify(retained.slice(1)) !== JSON.stringify(before)) {
      throw new Error("Remote deletion failure lost a screenshot or failed to expose the remote error");
    }
    deletion = { ok: true, localDeleted: true, remoteDeleted: true };
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
    const original = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
    const send = chrome.runtime.sendMessage;
    const confirm = window.confirm;
    const priorStatus = window.popupCheckStatus;
    const priorNetwork = window.popupCheckNetwork;
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = 1;
    const data = canvas.toDataURL("image/png").split(",")[1];
    const local = { filename: "offline-local.png", label: "Offline local", data, mirrored: false, remotePath: "" };
    const mirrored = { ...local, filename: "offline-mirrored.png", label: "Offline mirrored",
      mirrored: true, targetHost: "dev-host", remotePath: "/remote/offline-mirrored.png" };
    const remoteError = "SSH is disconnected; remote files could not be deleted";
    chrome.runtime.sendMessage = (message, callback) => {
      if (["popup_delete_screenshot", "popup_clear_screenshots"].includes(message.type)) {
        return callback({ ok: false, localDeleted: true, remoteDeleted: false, remoteError,
          results: (message.screenshots || []).map((shot) => ({ filename: shot.filename,
            remoteDeleted: !shot.mirrored, ...(shot.mirrored ? { remoteError } : {}) })) });
      }
      return send(message, callback);
    };
    window.confirm = () => true;
    const pause = () => new Promise((resolve) => setTimeout(resolve, 20));
    const display = async (shots) => {
      await chrome.storage.local.set({ tether_screenshots: shots });
      document.querySelector("#tab-btn-shots").click();
      for (let i = 0; i < 100; i++) {
        if (document.querySelector("#shots-badge").textContent === String(shots.length) &&
            (!shots.length || document.querySelector(".shot-title").textContent === shots[0].label)) return;
        await pause();
      }
      throw new Error("Offline gallery fixture did not load");
    };
    const clickAndWait = async (selector) => {
      const changed = new Promise((resolve) => {
        const listener = (changes, area) => {
          if (area !== "local" || !changes.tether_screenshots) return;
          chrome.storage.onChanged.removeListener(listener);
          resolve();
        };
        chrome.storage.onChanged.addListener(listener);
      });
      const button = document.querySelector(selector);
      if (button.disabled) throw new Error("Offline gallery action is disabled");
      button.click();
      await changed;
      await pause();
      return (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
    };
    try {
      window.popupCheckStatus = { tetherEnabled: false, connected: false, tabs: [], notes: [] };
      window.popupCheckNetwork = { tetherEnabled: false, nativeConnected: false, enabled: false,
        state: "off", canEnable: false, ssh: { state: "disconnected" } };
      await window.popupCheckRefresh();
      await display([local]);
      if ((await clickAndWait(".btn-del-shot")).length) throw new Error("Deleting a local-only offline screenshot required remote authentication");
      await display([local]);
      if ((await clickAndWait("#btn-clear-shots")).length) throw new Error("Clearing a local-only offline gallery required remote authentication");
      await display([mirrored, local]);
      const retained = await clickAndWait("#btn-clear-shots");
      if (retained.length !== 1 || retained[0].filename !== mirrored.filename ||
          retained[0].data !== data || retained[0].mirrorError !== remoteError) {
        throw new Error("Partial remote clear must remove local-only records and retain only retryable mirrored records");
      }

      // Save provenance must survive real capture clicks and a host switch.
      // Model distinct remote stores, rather than echoing the requested host.
      const copies = new Map([["host-a", new Set(["viewport-host.png", "full-host.png"])],
        ["host-b", new Set(["viewport-host.png", "host-b.png", "legacy.png"])]]);
      let currentHost = "host-b";
      const remotePath = (filename) => `/home/dev/.cache/tether/screenshots/${filename}`;
      const removeRemote = (shot) => {
        if (shot.mirrored === false) return { filename: shot.filename, remoteDeleted: true };
        if (!shot.targetHost || shot.targetHost !== currentHost) return {
          filename: shot.filename, remoteDeleted: false,
          remoteError: `Reconnect to the screenshot's original host ${shot.targetHost || "(unknown)"}`,
        };
        if (shot.remotePath !== remotePath(shot.filename) &&
            shot.remotePath !== `/tmp/tether-screenshots/${shot.filename}`) return {
          filename: shot.filename, remoteDeleted: false, remoteError: "Screenshot cache path does not match",
        };
        copies.get(currentHost).delete(shot.filename);
        return { filename: shot.filename, remoteDeleted: true };
      };
      chrome.runtime.sendMessage = (message, callback) => {
        if (message.type === "popup_capture_screenshot") {
          const filename = message.fullPage ? "full-host.png" : "viewport-host.png";
          return callback({ filename, base64: data, width: 1, height: 1,
            saveResult: { mirrored: true, targetHost: "host-a", remotePath: remotePath(filename) } });
        }
        if (message.type === "popup_delete_screenshot") return callback({
          localDeleted: true, ...removeRemote(message),
        });
        if (message.type === "popup_clear_screenshots") {
          const results = (message.screenshots || []).map(removeRemote);
          return callback({ localDeleted: true, remoteDeleted: results.every((item) => item.remoteDeleted), results });
        }
        return send(message, callback);
      };
      window.popupCheckStatus = { ...priorStatus, tetherEnabled: true };
      window.popupCheckNetwork = { ...priorNetwork, tetherEnabled: true, nativeConnected: true,
        ssh: { state: "connected", host: "host-b", sessionId: "host-b-session" } };
      await window.popupCheckRefresh();
      await display([]);
      for (const mode of ["viewport", "full"]) {
        const button = document.querySelector(`#btn-capture-${mode}`);
        button.click();
        while (button.disabled) await pause();
      }
      const captured = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
      if (captured.length !== 2 || captured.some((shot) =>
        shot.targetHost !== "host-a" || shot.remotePath !== remotePath(shot.filename))) {
        throw new Error("Capture lost its actual mirror destination while another host was selected");
      }
      let remaining = await clickAndWait(".btn-del-shot");
      if (remaining.length !== 2 || !remaining[0].mirrorError ||
          !copies.get("host-a").has("full-host.png") || !copies.get("host-b").has("viewport-host.png")) {
        throw new Error("Deleting a host-A capture through host B discarded its provenance or touched an unrelated copy");
      }
      const onB = { ...local, filename: "host-b.png", label: "Host B", mirrored: true,
        targetHost: "host-b", remotePath: remotePath("host-b.png") };
      await display([...remaining, onB, local]);
      remaining = await clickAndWait("#btn-clear-shots");
      if (remaining.length !== 2 || remaining.some((shot) => shot.targetHost !== "host-a" || !shot.mirrorError) ||
          copies.get("host-b").has("host-b.png") || !copies.get("host-a").has("viewport-host.png") ||
          !copies.get("host-a").has("full-host.png") || !copies.get("host-b").has("viewport-host.png")) {
        throw new Error("Mixed-host Clear must remove only successful/local records and retain original-host failures");
      }
      currentHost = "host-a";
      window.popupCheckNetwork.ssh = { state: "connected", host: "host-a", sessionId: "host-a-session" };
      remaining = await clickAndWait("#btn-clear-shots");
      if (remaining.length || copies.get("host-a").size) throw new Error("Reconnecting to the recorded host did not complete pending cleanup");

      const legacy = { ...local, filename: "legacy.png", label: "Legacy", mirrored: true,
        remotePath: "/tmp/tether-screenshots/legacy.png" };
      currentHost = "host-b";
      window.popupCheckNetwork.ssh = { state: "connected", host: "host-b", sessionId: "host-b-session" };
      const confirmations = [];
      let consent = false;
      window.confirm = (text) => { confirmations.push(text); return consent; };
      await display([legacy]);
      remaining = await clickAndWait(".btn-del-shot");
      if (remaining.length !== 1 || remaining[0].targetHost || !remaining[0].mirrorError ||
          !copies.get("host-b").has("legacy.png") || !confirmations.at(-1)?.includes("host-b")) {
        throw new Error("Legacy deletion must require explicit confirmation identifying the authenticated host");
      }
      window.popupCheckNetwork.ssh = { state: "disconnected", host: "host-b" };
      consent = true;
      const promptsBefore = confirmations.length;
      remaining = await clickAndWait(".btn-del-shot");
      if (remaining.length !== 1 || remaining[0].targetHost || confirmations.length !== promptsBefore ||
          !copies.get("host-b").has("legacy.png")) {
        throw new Error("Legacy cleanup guessed a host from disconnected or saved state");
      }
      window.popupCheckNetwork.ssh = { state: "connected", host: "host-b", sessionId: "host-b-session" };
      await clickAndWait(".btn-del-shot");
      for (let i = 0; i < 100; i++) {
        remaining = (await chrome.storage.local.get("tether_screenshots")).tether_screenshots;
        if (!remaining.length) break;
        await pause();
      }
      if (remaining.length || copies.get("host-b").has("legacy.png")) {
        throw new Error("Confirmed legacy cleanup did not finish on the explicitly selected host");
      }
    } finally {
      chrome.runtime.sendMessage = send;
      window.confirm = confirm;
      window.popupCheckStatus = priorStatus;
      window.popupCheckNetwork = priorNetwork;
      await chrome.storage.local.set({ tether_screenshots: original });
      document.querySelector("#tab-btn-shots").click();
      await window.popupCheckRefresh();
    }
  });
  await evaluate(async () => {
    const status = await window.popupCheckSend({ type: "popup_get_status" });
    if (status.connected) throw new Error("Capture checks require a disposable, disconnected profile");
    window.captureCheckTab = await chrome.tabs.create({ url: "data:text/html,", active: true });
    window.captureCheckCommand = (method, params = {}) => chrome.debugger.sendCommand({ tabId: window.captureCheckTab.id }, method, params);
    window.captureCheckReviewCommand = async (expression) => {
      const { frameTree } = await window.captureCheckCommand("Page.getFrameTree");
      const { executionContextId } = await window.captureCheckCommand("Page.createIsolatedWorld", {
        frameId: frameTree.frame.id, worldName: "tether-review",
      });
      const result = await window.captureCheckCommand("Runtime.evaluate", {
        expression, contextId: executionContextId, returnByValue: true,
      });
      if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
      return result;
    };
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
        notes: await window.captureCheckReviewCommand("window.__tetherReview.getNotes().map(n => n.comment)"),
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
    const editor = (expression) => window.captureCheckReviewCommand(expression);
    const focusCaptureWindow = async () => {
      await chrome.windows.update(window.captureCheckTab.windowId, { focused: true });
      for (let i = 0; i < 200; i++) {
        if ((await chrome.windows.get(window.captureCheckTab.windowId)).focused) {
          return;
        }
        await pause();
      }
      throw new Error("Disposable Chrome window did not become active");
    };
    const openPopup = async () => {
      await focusCaptureWindow();
      await chrome.action.openPopup({ windowId: window.captureCheckTab.windowId });
    };
    popup()?.close();
    await waitClosed();
    await editor(`
      const target = document.createElement('button');
      target.style.cssText = 'position:fixed;left:50px;top:50px;width:180px;height:60px';
      target.textContent = 'Review target';
      document.body.append(target);
    `);
    await openPopup();
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
      await openPopup();
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
    await focusCaptureWindow();
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
    if (window.popupCheckRealCreate) chrome.tabs.create = window.popupCheckRealCreate;
    for (const [method, original] of Object.entries(window.popupCheckProxyMethods || {})) chrome.proxy.settings[method] = original;
    for (const [method, original] of Object.entries(window.popupCheckStorageMethods || {})) chrome.storage.local[method] = original;
    await Promise.all((window.popupCheckSignInTabs || []).map((tab) => chrome.tabs.remove(tab.id).catch(() => {})));
    if (window.captureCheckTab) await chrome.tabs.remove(window.captureCheckTab.id);
    for (const key of ["captureCheckTab", "captureCheckCommand", "captureCheckReviewCommand", "captureCheckStart", "captureCheckLatest", "captureCheckImage"]) delete window[key];
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
