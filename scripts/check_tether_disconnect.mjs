// Exercise the popup's parent/child connection transitions against the real worker.
import assert from "node:assert/strict";

const data = {};
let popupMessage;
let nativeMessage;
let nativeDisconnect;
let portCount = 0;
let portClosed = 0;
let attached = 0;
let detached = 0;
let closedTab = false;
let ssh = { state: "disconnected", host: "" };
let proxy = { mode: "system" };
const replies = new Map();
const listen = { addListener() {} };

const nativePort = {
  onMessage: { addListener(fn) { nativeMessage = fn; } },
  onDisconnect: { addListener(fn) { nativeDisconnect = fn; } },
  postMessage(message) {
    if (message.type === "system_ssh_connect") {
      ssh = { state: "connected", host: message.targetHost, sessionId: "session-1", proxyPort: 14567 };
    } else if (message.type === "system_ssh_disconnect") {
      ssh = { state: "disconnected", host: "" };
    }
    if (message.type?.startsWith("system_")) {
      queueMicrotask(() => nativeMessage({ id: message.id, result: ssh }));
    } else if (replies.has(message.id)) {
      replies.get(message.id)(message);
      replies.delete(message.id);
    }
  },
  disconnect() { portClosed++; nativeDisconnect(); },
};

globalThis.self = { addEventListener() {} };
globalThis.chrome = {
  storage: { local: {
    async get(keys) {
      const names = Array.isArray(keys) ? keys : [keys];
      return Object.fromEntries(names.filter((name) => name in data).map((name) => [name, data[name]]));
    },
    async set(values) { Object.assign(data, values); },
    async remove(key) { delete data[key]; },
  } },
  proxy: { settings: {
    onChange: listen,
    get(_details, callback) { callback({ value: proxy, levelOfControl: proxy.mode === "system" ? "controllable_by_this_extension" : "controlled_by_this_extension" }); },
    set({ value }, callback) { proxy = value; callback(); },
    clear(_details, callback) { proxy = { mode: "system" }; callback(); },
  } },
  action: { setBadgeText() {}, setBadgeBackgroundColor() {} },
  alarms: { create() {}, onAlarm: listen },
  runtime: {
    id: "extension-id", lastError: null, onMessage: { addListener(fn) { popupMessage = fn; } },
    getURL: (path) => `chrome-extension://extension-id/${path}`,
    getManifest: () => ({ version: "test" }),
    connectNative() { portCount++; return nativePort; },
  },
  debugger: {
    onDetach: listen, onEvent: listen,
    async attach() { attached++; },
    async detach() {
      detached++;
      if (closedTab) throw new Error("No tab with given id 1.");
    },
    sendCommand(_target, method, _params, callback) {
      const result = method === "Page.getFrameTree" ? { frameTree: { frame: { id: "main" } } } :
        method === "Page.createIsolatedWorld" ? { executionContextId: 1 } :
          method === "Runtime.evaluate" ? { result: { value: "[]" } } : {};
      callback?.(result);
      return Promise.resolve(result);
    },
  },
  tabs: {
    onActivated: listen, onUpdated: listen, onCreated: listen,
    get: async (id) => ({ id, url: "https://example.test/", groupId: id === 1 ? 7 : -1 }),
    query: async () => [{ id: 1, url: "https://example.test/", groupId: 7 }],
    group: async () => 7,
  },
  tabGroups: { query: async () => [{ id: 7 }], get: async () => ({ id: 7 }) },
};

await import("../packages/extension/background.js");
const sender = { id: chrome.runtime.id, url: chrome.runtime.getURL("popup.html") };
function popup(type, fields = {}, from = sender) {
  return new Promise((resolve) => popupMessage({ type, ...fields }, from, resolve));
}
function remote(method, params) {
  const id = `rpc-${replies.size}`;
  const response = new Promise((resolve) => replies.set(id, resolve));
  nativeMessage({ id, method, params });
  return response;
}

assert.equal(portCount, 0, "fresh extension must not open an agent bridge");
assert.match((await popup("popup_navigate", { url: "https://example.test/" })).error, /Connect Tether/);
assert.equal((await popup("popup_network_status")).tetherEnabled, false);
assert.equal(portCount, 0, "status polling must not reconnect the bridge");
assert.match((await popup("popup_tether_connect", { targetHost: "dev" }, { id: "extension-id", url: "https://example.test/" })).error, /Tether popup/);
assert.equal(portCount, 0);
assert.equal((await popup("popup_tether_connect", { targetHost: "dev" })).state, "connected");
assert.equal(portCount, 1);
await popup("popup_network_set", { enabled: true, sessionId: "session-1" });
assert.equal(proxy.mode, "fixed_servers");
await popup("popup_get_status", { currentTabId: 2 });
assert.equal(attached, 0, "polling an unrelated tab must not attach debugger");
assert.match((await remote("browser.review.list", { tabId: 2 })).error.message, /Tether tab group/);
assert.equal((await remote("browser.review.list", { tabId: 1 })).type, "response");
assert.equal(attached, 1);
await popup("popup_network_set", { enabled: false });
assert.equal(portClosed, 0, "remote browsing OFF must keep the agent bridge");
assert.equal((await popup("popup_network_status")).ssh.state, "connected");
await popup("popup_network_set", { enabled: true, sessionId: "session-1" });
assert.equal((await popup("popup_tether_disconnect")).ok, true);
assert.equal(proxy.mode, "system");
assert.equal(data.tether_remote_network, undefined);
assert.equal(data.tether_enabled, false);
assert.equal(portClosed, 1);
assert.equal(detached, 1);
assert.equal((await popup("popup_network_status")).enabled, false);
assert.match((await popup("popup_start_review", { tabId: 1 })).error, /Connect Tether/);
await import("../packages/extension/background.js?restart");
assert.equal(portCount, 1, "service-worker restart must preserve explicit disconnect");
assert.equal((await popup("popup_network_status")).tetherEnabled, false);
delete data.tether_enabled;
delete data.tether_host;
data.tether_remote_network = { host: "dev", port: 14567 };
proxy = { mode: "fixed_servers", rules: {
  singleProxy: { scheme: "socks5", host: "127.0.0.1", port: 14567 },
  bypassList: ["<-loopback>"],
} };
await import("../packages/extension/background.js?upgrade");
assert.equal(portCount, 2, "an existing active route must keep its connection on upgrade");
assert.equal((await popup("popup_network_status")).tetherEnabled, true);
assert.equal(data.tether_enabled, true, "migration must persist parent consent before child OFF");
assert.equal(data.tether_host, "dev");
await popup("popup_network_set", { enabled: false });
assert.equal(data.tether_remote_network, undefined);
await import("../packages/extension/background.js?legacy-child-off");
assert.equal(portCount, 3, "child OFF after upgrade must keep Tether connected across restart");
assert.equal((await popup("popup_network_status")).tetherEnabled, true);
assert.equal((await remote("browser.review.list", { tabId: 1 })).type, "response");
let releaseFetch;
globalThis.fetch = () => new Promise((resolve) => {
  releaseFetch = () => resolve({ text: async () => "" });
});
void remote("browser.review.start", { tabId: 1 });
await new Promise((resolve) => setImmediate(resolve));
assert.equal(typeof releaseFetch, "function");
closedTab = true;
const stopping = popup("popup_tether_disconnect");
await new Promise((resolve) => setImmediate(resolve));
assert.match((await popup("popup_tether_connect", { targetHost: "other" })).error, /disconnecting/);
const attachesBefore = attached;
releaseFetch();
assert.equal((await stopping).ok, true, "closed tabs are already detached");
assert.equal((await popup("popup_tether_connect", { targetHost: "other" })).state, "connected");
assert.equal(attached, attachesBefore, "old browser commands cannot attach after reconnect");
assert.equal((await popup("popup_tether_disconnect")).ok, true);
console.log("PASS: master disconnect closes agent bridge, SSH, debugger and proxy; child OFF preserves bridge across legacy upgrade/restart; stale commands cannot cross reconnect");
process.exit(0);
