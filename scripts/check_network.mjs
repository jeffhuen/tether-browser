// node scripts/check_network.mjs
// State/authentication checks; actual HTTP proxy routing is exercised in disposable Chrome.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createContext, runInContext } from "node:vm";

const storage = {};
const system = { mode: "system" };
let settings = { levelOfControl: "controllable_by_this_extension", value: system };
let settingScope;
let connection = { state: "disconnected", host: "" };
let nativeFailure = false;
let pendingReply;
const opened = [];
let failCreate = false;
let badge = "";
let statusRequests = 0;
let legacyHelper = false;
let connectRequests = 0;
let failProxySet = false;

let heldProxyGet;
const capability = { proxyAuthSupported: true, proxyToken: "a".repeat(64), proxyRealm: `tether-${"b".repeat(32)}` };
const event = () => ({
  listeners: [],
  addListener(listener) { this.listeners.push(listener); },
  removeListener(listener) { this.listeners = this.listeners.filter((item) => item !== listener); },
  emit(message) { for (const listener of [...this.listeners]) listener(message); },
});
globalThis.chrome = {
  runtime: {},
  webRequest: { onAuthRequired: event(), onCompleted: event(), onErrorOccurred: event() },
  action: { async setBadgeText({ text }) { badge = text; }, async setBadgeBackgroundColor() {} },
  storage: { local: {
    async get() { return structuredClone(storage); },
    async set(value) { Object.assign(storage, structuredClone(value)); },
    async remove(key) { delete storage[key]; },
  } },
  tabs: { async create({ url }) {
    if (failCreate) throw new Error("No current window");
    opened.push({ url, proxy: structuredClone(settings.value) });
    return { id: opened.length };
  } },
  proxy: { settings: {
    get(_, callback) {
      if (heldProxyGet) heldProxyGet(callback);
      else callback(structuredClone(settings));
    },
    set({ scope, value }, callback) {
      if (failProxySet) {
        chrome.runtime.lastError = { message: "Proxy migration denied" };
        callback();
        delete chrome.runtime.lastError;
        return;
      }
      settingScope = scope;
      settings = { levelOfControl: "controlled_by_this_extension", value: structuredClone(value) };
      callback();
    },
    clear({ scope }, callback) {
      if (scope === settingScope) settings = { levelOfControl: "controllable_by_this_extension", value: system };
      callback();
    },
  } },
};
async function request(message) {
  if (message.type === "system_ssh_status") statusRequests++;
  if (nativeFailure) throw new Error("Native host disconnected");
  if (pendingReply) return pendingReply;
  if (message.type === "system_ssh_connect") {
    connectRequests++;
    connection = { ...capability, state: "connecting", host: message.targetHost, proxyPort: message.proxyPort || 32123, sessionId: "a" };
  }
  if (message.type === "system_ssh_disconnect") connection = { ...connection, state: "disconnected" };
  const result = { ...structuredClone(connection), proxyAuthSupported: true };
  if (legacyHelper) delete result.proxyAuthSupported;
  return result;
}
let authSequence = 0;
const authenticate = (overrides = {}) => new Promise((resolve) =>
  chrome.webRequest.onAuthRequired.listeners.at(-1)({
    requestId: `auth-${++authSequence}`, isProxy: true, incognito: false, scheme: "basic",
    realm: connection.proxyRealm, challenger: { host: "127.0.0.1", port: 32123 }, ...overrides,
  }, resolve));
const noSecret = (value) => {
  for (const token of new Set([capability.proxyToken, connection.proxyToken].filter((value) => typeof value === "string"))) {
    assert.equal(JSON.stringify(value).includes(token), false, "Public/storage state must not contain the proxy token");
  }
  assert.equal(Object.hasOwn(value.ssh || value, "proxyToken"), false, "Public SSH snapshots must redact tokens");
};
let network = await import("../packages/extension/network.js");
network.initializeNetwork(request);
assert.equal((await network.networkStatus()).canEnable, false);
await assert.rejects(network.setNetworkEnabled(true, "a"), /Connect to an SSH host/);
legacyHelper = true;
await assert.rejects(network.connectSSH("dev-a"), /Update the local tether binary/);
assert.equal(connectRequests, 0, "An incompatible helper must never start SSH forwarding");
storage.tether_remote_network = { host: "dev-a", port: 32123 };
settings = { levelOfControl: "controlled_by_this_extension", value: { mode: "fixed_servers", rules: {
  singleProxy: { scheme: "socks5", host: "127.0.0.1", port: 32123 },
  bypassList: ["<-loopback>"],
} } };
settingScope = "regular_only";
network.initializeNetwork(request);
failProxySet = true;
await assert.rejects(network.connectSSH("dev-a"), /Proxy migration denied/);
assert.equal(connectRequests, 0, "Failed proxy migration must not activate an old helper");
assert.equal(settings.value.mode, "fixed_servers", "Migration failure must not fall back to direct networking");
failProxySet = false;
await network.setNetworkEnabled(false);
assert.deepEqual(settings.value, system, "Explicit OFF must work after failed migration");
assert.equal(storage.tether_remote_network, undefined);
legacyHelper = false;
await network.connectSSH("dev-a");
assert.equal((await network.networkStatus()).canEnable, false, "SSH startup is not readiness");
connection.state = "connected";
await assert.rejects(network.setNetworkEnabled(true, "old-session"), /connection changed/);
await network.setNetworkEnabled(true, "a");
assert.equal((await network.networkStatus()).state, "on");
assert.deepEqual(await authenticate(), { authCredentials: { username: "tether", password: capability.proxyToken } });
noSecret(await network.networkStatus());
noSecret(storage);
await assert.rejects(network.connectSSH("dev-b"), /off before switching/);
await network.disconnectSSH();
assert.equal((await network.networkStatus()).state, "unavailable");
assert.equal((await network.networkStatus()).enabled, true, "Disconnect must not silently restore local routing");

// Worker restart + native-host loss preserves the requested route and OFF.
nativeFailure = true;
network = await import("../packages/extension/network.js?restart");
network.initializeNetwork(request);
assert.equal((await network.networkStatus()).state, "unavailable");
const beforeRestartAuth = statusRequests;
assert.deepEqual(await authenticate(), { cancel: true }, "Restart cannot answer proxy auth from persisted routing alone");
assert.equal(statusRequests, beforeRestartAuth, "Proxy challenges must not start or poll the native bridge");
await network.setNetworkEnabled(false);
assert.equal((await network.networkStatus()).enabled, false);
assert.deepEqual(settings.value, system);
assert.equal(storage.tether_remote_network, undefined);

// Policy/extension ownership is visible, not mistaken for working routing.
nativeFailure = false;
connection = { ...capability, state: "connected", host: "dev-a", proxyPort: 32123, sessionId: "b" };
settings.levelOfControl = "controlled_by_other_extensions";
assert.equal((await network.networkStatus()).canEnable, false);
await assert.rejects(network.setNetworkEnabled(true, "b"), /browser policy controls/);
settings.levelOfControl = "controllable_by_this_extension";
await network.setNetworkEnabled(true, "b");
settings.levelOfControl = "controlled_by_other_extensions";
assert.equal((await network.networkStatus()).state, "conflict");
settings.levelOfControl = "controlled_by_this_extension";
await network.setNetworkEnabled(false);

// An OFF queued while enable waits for verification wins over its late reply.
let release;
pendingReply = new Promise((resolve) => { release = resolve; });
const enabling = network.setNetworkEnabled(true, "b");
const disabling = network.setNetworkEnabled(false);
release(connection);
await Promise.all([enabling, disabling]);
pendingReply = null;
assert.equal((await network.networkStatus()).enabled, false);
assert.deepEqual(settings.value, system);

// A stale status response must not resurrect a lost native connection.
pendingReply = new Promise((resolve) => { release = resolve; });
const refreshing = network.networkStatus();
await new Promise((resolve) => setImmediate(resolve));
network.networkDisconnected();
release(connection);
assert.equal((await refreshing).ssh.state, "disconnected");
pendingReply = null;

// Challenge boundaries and lifetime: credentials belong to this helper/route only.
connection = { ...capability, state: "connected", host: "dev-a", proxyPort: 32123, sessionId: "auth" };
await network.setNetworkEnabled(true, "auth");
const authRequestsBefore = statusRequests;
for (const overrides of [
  { isProxy: false },
  { challenger: { host: "origin.example", port: 32123 } },
  { challenger: { host: "127.0.0.1", port: 32124 } },
  { challenger: { host: "localhost", port: 32123 } },
  { incognito: true },
]) assert.deepEqual(await authenticate(overrides), {}, "Origin/other proxy/profile authentication is untouched");
for (const overrides of [
  { realm: "another-proxy" }, { realm: capability.proxyRealm + "\n" }, { scheme: "digest" },
]) assert.deepEqual(await authenticate(overrides), { cancel: true }, "Untrusted Tether challenges must not open password prompts");
assert.equal(statusRequests, authRequestsBefore);
assert.deepEqual(await authenticate({ requestId: "wrong-password" }),
  { authCredentials: { username: "tether", password: capability.proxyToken } });
assert.deepEqual(await authenticate({ requestId: "wrong-password" }), { cancel: true }, "Wrong credentials must not loop");
assert.deepEqual(await authenticate({ requestId: "wrong-password", isProxy: false }), {},
  "Origin authentication after proxy authentication must still work");
chrome.webRequest.onCompleted.emit({ requestId: "wrong-password" });
assert.deepEqual(await authenticate({ requestId: "wrong-password" }),
  { authCredentials: { username: "tether", password: capability.proxyToken } }, "Completed requests release retry tracking");
chrome.webRequest.onErrorOccurred.emit({ requestId: "wrong-password" });
assert.deepEqual(await authenticate({ requestId: "wrong-password" }),
  { authCredentials: { username: "tether", password: capability.proxyToken } }, "Failed requests release retry tracking");
const concurrentAuth = await Promise.all([
  authenticate({ requestId: "concurrent-auth" }), authenticate({ requestId: "concurrent-auth" }),
]);
assert.deepEqual(concurrentAuth, [
  { authCredentials: { username: "tether", password: capability.proxyToken } }, { cancel: true },
], "Concurrent challenges for one request must not duplicate credentials");
chrome.webRequest.onCompleted.emit({ requestId: "concurrent-auth" });
chrome.runtime.lastError = { message: "Proxy settings unavailable" };
assert.deepEqual(await authenticate(), { cancel: true }, "Unavailable ownership checks must not open proxy password prompts");
assert.deepEqual(await authenticate({ isProxy: false }), {}, "Origin credentials are unaffected by proxy API failures");
delete chrome.runtime.lastError;
settings.levelOfControl = "controlled_by_other_extensions";
assert.deepEqual(await authenticate(), { cancel: true }, "Proxy ownership is required before sending credentials");
settings.levelOfControl = "controlled_by_this_extension";
for (const changes of [{ host: "dev-other" }, { proxyPort: 32124 }]) {
  connection = { ...connection, ...changes };
  assert.equal((await network.networkStatus()).state, "unavailable");
  assert.deepEqual(await authenticate(), { cancel: true }, "A different native route must not authenticate this proxy");
  connection = { ...capability, state: "connected", host: "dev-a", proxyPort: 32123, sessionId: "auth" };
}
for (const changes of [
  { proxyToken: undefined }, { proxyRealm: undefined }, { proxyToken: "short" },
  { proxyToken: capability.proxyToken + "\n" }, { proxyRealm: capability.proxyRealm + "\n" },
]) {
  connection = { ...capability, state: "connected", host: "dev-a", proxyPort: 32123, sessionId: "auth", ...changes };
  const incompatible = await network.networkStatus();
  assert.equal(incompatible.state, "unavailable");
  assert.match(incompatible.message, /Update the local tether binary/);
  await assert.rejects(network.setNetworkEnabled(true, "auth"), /Update the local tether binary/);
  assert.deepEqual(await authenticate(), { cancel: true }, "Missing/malformed native capabilities fail closed without a SOCKS fallback");
  assert.equal(settings.value.mode, "fixed_servers", "Rejected helper versions must not restore local routing");
}
connection = { ...capability, state: "connected", host: "dev-a", proxyPort: 32123, sessionId: "auth" };
await network.networkStatus();
pendingReply = Promise.resolve(connection);
noSecret(await network.connectSSH("dev-a"));
pendingReply = null;
noSecret(await network.networkStatus());
noSecret(storage);
assert.deepEqual(await authenticate({ requestId: "wrong-password" }), { cancel: true },
  "A new revision must not reset an in-flight request's failed credential attempt");

// Restart keeps the route ON but cannot use the dead worker's private capability.
network = await import("../packages/extension/network.js?auth-restart");
network.initializeNetwork(request);
const beforeFreshStatus = statusRequests;
assert.equal((await network.networkStatus(false)).state, "unavailable");
assert.deepEqual(await authenticate(), { cancel: true });
assert.equal(statusRequests, beforeFreshStatus);
connection = { ...connection, proxyToken: "c".repeat(64), proxyRealm: `tether-${"d".repeat(32)}`, sessionId: "new-helper" };
assert.equal((await network.networkStatus()).state, "on");
assert.deepEqual(await authenticate({ realm: capability.proxyRealm }), { cancel: true }, "The old helper's realm is stale");
assert.deepEqual(await authenticate(),
  { authCredentials: { username: "tether", password: connection.proxyToken } });
noSecret(await network.networkStatus());
noSecret(storage);

// Native EOF and explicit OFF both invalidate callbacks already awaiting ownership.
let releaseProxy;
heldProxyGet = (callback) => { releaseProxy = callback; };
const eofAuth = authenticate();
await new Promise((resolve) => setImmediate(resolve));
network.networkDisconnected();
heldProxyGet = null;
releaseProxy(structuredClone(settings));
assert.deepEqual(await eofAuth, { cancel: true }, "EOF must win over an in-flight auth callback");
assert.deepEqual(await authenticate(), { cancel: true });
assert.equal((await network.networkStatus(false)).enabled, true);
await network.networkStatus();
heldProxyGet = (callback) => { releaseProxy = callback; };
const offAuth = authenticate();
await new Promise((resolve) => setImmediate(resolve));
await network.setNetworkEnabled(false);
heldProxyGet = null;
releaseProxy(structuredClone(settings));
assert.deepEqual(await offAuth, { cancel: true }, "OFF must win over an in-flight auth callback");
assert.deepEqual(await authenticate(), {}, "Disabled remote browsing must leave unrelated proxy authentication alone");
assert.deepEqual(settings.value, system);
assert.equal(storage.tether_remote_network, undefined);

// A connect response arriving after native loss must not resurrect its capability.
pendingReply = new Promise((resolve) => { release = resolve; });
const lostConnect = network.connectSSH("dev-a");
await new Promise((resolve) => setImmediate(resolve));
network.networkDisconnected();
release(connection);
await assert.rejects(lostConnect, /connection changed/);
pendingReply = null;
assert.equal((await network.networkStatus(false)).ssh.state, "disconnected");
noSecret(await network.disconnectSSH());
connection = { ...capability, state: "connected", host: "dev-a", proxyPort: 32123, sessionId: "b" };

// Polling/startup-style refreshes and proxy changes never open or focus sign-in.
const challenge = "https://login.tailscale.com/a/ABC123";
connection = { ...capability, state: "connecting", host: "dev-a", sessionId: "login", signInUrl: challenge };
await Promise.all(Array.from({ length: 40 }, () => network.networkStatus()));
assert.equal(opened.length, 0);
assert.equal(badge, "!", "Sign-in needs attention even with Remote browsing off");
const requestsBefore = statusRequests;
assert.equal((await network.networkStatus(false)).signInUrl, "", "A disconnected bridge must not expose a cached challenge");
assert.equal(statusRequests, requestsBefore, "pollSSH=false must not contact the native host");
for (const signInUrl of [
  "http://login.tailscale.com/a/ABC123", "https://evil.test/a/ABC123",
  "https://login.tailscale.com.evil.test/a/ABC123", challenge + "?redirect=evil",
  challenge + "\n", challenge + "/", "javascript:alert(1)", "https://login.tailscale.com/a/",
]) {
  connection.signInUrl = signInUrl;
  assert.equal((await network.networkStatus()).signInUrl, "");
  await assert.rejects(network.openSignIn("login", false), /No valid sign-in/);
}
connection.signInUrl = challenge;
for (const state of ["connected", "disconnected"]) {
  connection = { ...connection, state, proxyPort: 32123 };
  assert.equal((await network.networkStatus()).signInUrl, "");
  await assert.rejects(network.openSignIn("login", false), /No valid sign-in/);
}
connection.state = "connecting";
await assert.rejects(network.openSignIn("old-session", false), /connection changed/);
connection.sessionId = "replacement";
await assert.rejects(network.openSignIn("login", false), /connection changed/);
connection.sessionId = "login";
// A popup may have seen an earlier challenge; click must open the latest native URL.
await network.networkStatus();
connection.signInUrl = "https://login.tailscale.com/a/LATEST";
await network.openSignIn("login", false);
assert.equal(opened.at(-1).url, connection.signInUrl);
assert.deepEqual(opened.at(-1).proxy, system);
failCreate = true;
await assert.rejects(network.openSignIn("login", false), /No current window/);
const attempts = opened.length;
failCreate = false;
await network.openSignIn("login", false);
assert.equal(opened.length, attempts + 1, "A failed tab creation must not consume the next explicit attempt");

connection = { ...connection, state: "connected", proxyPort: 32123 };
await network.setNetworkEnabled(true, "login");
connection.state = "connecting";
for (const state of ["connecting", "disconnected", "connected", "connecting"]) {
  connection.state = state;
  await Promise.all(Array.from({ length: 20 }, () => network.networkStatus()));
}
assert.equal(opened.length, attempts + 1, "Concurrent polls and reconnect flaps must not create tabs");
assert.equal((await network.networkStatus()).enabled, true, "Authentication waits must keep the requested route fail-closed");
await assert.rejects(network.openSignIn("login", false), /Remote browsing is now on/);
assert.equal((await network.networkStatus()).enabled, true, "An outdated off-state action must not clear a newly enabled route");
await network.openSignIn("login", true);
assert.deepEqual(opened.at(-1).proxy, system, "One explicitly consented action must turn Remote browsing off before opening sign-in");
assert.equal((await network.networkStatus()).enabled, false);
await network.networkStatus();
assert.equal((await network.networkStatus()).enabled, false, "Polling must never restore routing after sign-in");
// Loss while the fresh verification is in flight rejects rather than opening stale auth.
pendingReply = new Promise((resolve) => { release = resolve; });
const signingIn = network.openSignIn("login", false);
await new Promise((resolve) => setImmediate(resolve));
network.networkDisconnected();
release(connection);
await assert.rejects(signingIn, /connection changed/);
pendingReply = null;

// NetBird owns its SSO browser. Tether only releases a stalled route on consent.
connection.state = "connected";
await network.setNetworkEnabled(true, "login");
connection = { ...connection, state: "connecting", authProvider: "NetBird",
  authMessage: "Complete sign-in in the browser opened by NetBird.", signInUrl: "https://sso.example/login" };
const beforeNetBird = opened.length;
assert.equal((await network.networkStatus()).signInUrl, "");
assert.equal((await network.networkStatus()).enabled, true);
await assert.rejects(network.openSignIn("old", true), /connection changed/);
assert.equal((await network.networkStatus()).enabled, true);
const netBird = await network.openSignIn("login", true);
assert.equal(netBird.authProvider, "NetBird");
assert.equal((await network.networkStatus()).enabled, false);
assert.equal(opened.length, beforeNetBird, "NetBird preparation must not create an arbitrary SSO tab");
connection = { state: "connecting", host: "dev-a", sessionId: "login", signInUrl: challenge };

// Real native transport helpers: actual serialized UTF-8 limits are per-message,
// and both failed native requests and oversized RPC responses leave the port usable.
const messages = [];
const intervals = [];
const context = createContext({
  console, AbortController, atob, setTimeout, clearTimeout,
  setInterval(callback) { intervals.push(callback); return intervals.length; }, clearInterval() {},
  initializeNetwork() {}, networkStatus: network.networkStatus, openSignIn: network.openSignIn,
  self: { addEventListener() {} },
  chrome: {
    storage: { local: { async get() { return {}; } } },
    runtime: { id: "check", getURL: (path) => `chrome-extension://check/${path}`, onMessage: event() },
    proxy: { settings: { onChange: event() } },
    alarms: { create() {}, onAlarm: event() },
    debugger: { onDetach: event(), onEvent: event() },
    tabs: { onActivated: event(), onUpdated: event(), onCreated: event() },
  },
  port: { postMessage(message) { messages.push(message); } },
});
const source = await readFile(new URL("../packages/extension/background.js", import.meta.url), "utf8");
runInContext(source.replace(/^import .+;\n/m, ""), context);
await runInContext("tetherReady", context);
runInContext("nativePort = port", context);
const beforeBackground = opened.length;
runInContext("startHeartbeat()", context);
intervals.at(-1)();
context.chrome.proxy.settings.onChange.listeners[0]();
await network.networkStatus();
assert.equal(opened.length, beforeBackground, "Heartbeat and proxy-change listeners must never open sign-in");
runInContext("tetherEnabled = true", context);
for (const sender of [
  { id: "other", url: "chrome-extension://check/popup.html" },
  { id: "check", url: "chrome-extension://check/popup.html?query=1" },
  { id: "check", url: "https://example.test" },
]) {
  const reply = await new Promise((resolve) => context.chrome.runtime.onMessage.listeners[0](
    { type: "popup_network_signin", sessionId: "login", turnOff: false }, sender, resolve));
  assert.match(reply.error, /require the Tether popup/);
}
assert.equal(opened.length, beforeBackground, "Non-popup senders must not trigger sign-in");
const limit = 64 * 1024 * 1024;
context.payload = "a".repeat(limit - JSON.stringify({ type: "response", id: "edge", result: { base64: "" } }).length);
runInContext('sendResponse("edge", {base64: payload})', context);
assert.equal(messages.at(-1).type, "response", "An exactly 64 MiB JSON message is legal");
context.payload += "a";
runInContext('sendResponse("oversized", {base64: payload})', context);
assert.equal(messages.at(-1).type, "error");
assert.match(messages.at(-1).error.message, /64 MiB/);
context.payload = "é".repeat(limit / 2);
await assert.rejects(runInContext('nativeRequest({type:"system_save_screenshot",base64:payload})', context), /64 MiB/);
runInContext('sendError("oversized-error", -32000, payload)', context);
assert.equal(messages.at(-1).id, "oversized-error");
assert.match(messages.at(-1).error.message, /64 MiB/, "An oversized error must be replaced by a small per-request error");
context.payload = null;
const small = runInContext('nativeRequest({type:"system_ssh_status"})', context);
const smallMessage = messages.at(-1);
await context.handleNativeMessage({ id: smallMessage.id, result: { state: "connecting" } });
assert.equal((await small).state, "connecting", "Oversized messages must not disconnect the next native request");
runInContext('sendResponse("after-large", {ok:true})', context);
assert.equal(messages.at(-1).type, "response");
// A legal large capture must fit as one payload, not become an oversized
// response merely because the dispatcher repeats it under another field.
messages.length = 0;
context.screenshotPayload = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB".padEnd(36 * 1024 * 1024, "A");
runInContext(`
  ensureDomain = async () => {};
  cdp = async (_, method) => {
    if (method === "Runtime.evaluate") return {result:{value:{x:0,y:0,width:1,height:1,scale:1}}};
    if (method === "Page.getLayoutMetrics") return {cssVisualViewport:{zoom:1}};
    if (method === "Page.captureScreenshot") return {data:screenshotPayload};
    throw new Error("Unexpected screenshot CDP command: " + method);
  };
`, context);
const capture = await context.handleScreenshot({ tabId: 42 });
context.capture = capture;
runInContext('sendResponse("large-capture", capture)', context);
assert.equal(messages.at(-1).type, "response", "A legal 36 MiB capture must not be duplicated into an oversized native response");

// Offline gallery requests are independent of normal Connect. Model file ownership
// at the native boundary so lost provenance changes deletion outcomes, not just messages.
const localFiles = new Set(["denied.png", "clear.png", "mixed.png", "delayed.png", "connecting.png", "pending.png"]);
const remoteFiles = new Set([
  "dev-original:/shots/clear.png", "dev-original:/shots/delayed.png",
  "dev-mixed:/shots/mixed.png", "dev-original:/shots/mixed.png",
  "dev-original:/shots/connecting.png", "dev-b:/shots/connecting.png",
]);
function deleteFile(record) {
  const localDeleted = localFiles.delete(record.filename);
  const remoteDeleted = !record.mirrored || remoteFiles.delete(`${record.targetHost}:${record.remotePath}`);
  return { ok: localDeleted && remoteDeleted, localDeleted, remoteDeleted,
    ...(!remoteDeleted ? { remoteError: "Original destination unavailable" } : {}) };
}
function applyFileRequest(message) {
  if (message.type === "system_delete_screenshot") return deleteFile(message);
  assert.equal(message.type, "system_clear_screenshots", "Offline file requests must never authenticate SSH");
  const results = message.screenshots.map(deleteFile);
  return { ok: results.every((result) => result.ok), results };
}
const fileOperations = [];
const normalPorts = [];
let transportSSH = { state: "disconnected", host: "" };
const queueFile = (message, resolve, reject) => fileOperations.push({
  complete() { resolve({ id: message.id, type: "response", result: applyFileRequest(message) }); },
  respond(reply) { resolve({ id: message.id, type: "response", ...reply }); },
  reject,
});
context.chrome.runtime.sendNativeMessage = (_host, message) => new Promise((resolve, reject) => {
  queueFile(message, resolve, reject);
});
context.chrome.runtime.connectNative = () => {
  const port = {
    onMessage: event(), onDisconnect: event(),
    firstFrame: true, oneShot: false, closed: false,
    postMessage(message) {
      if (this.closed) throw new Error("Native process already exited");
      if (this.firstFrame) {
        this.firstFrame = false;
        this.oneShot = message.type === "system_delete_screenshot" || message.type === "system_clear_screenshots";
      }
      if (message.type === "system_ssh_connect") {
        transportSSH = { state: "connecting", host: message.targetHost };
        this.connect = () => {
          transportSSH = { ...capability, state: "connected", host: message.targetHost, sessionId: "overlap", proxyPort: 32123 };
          this.onMessage.emit({ id: message.id, type: "response", result: transportSSH });
        };
      } else if (message.type === "system_ssh_status" || message.type === "system_ssh_disconnect") {
        if (message.type === "system_ssh_disconnect") transportSSH = { state: "disconnected", host: "" };
        queueMicrotask(() => this.onMessage.emit({ id: message.id, type: "response",
          result: { ...transportSSH, proxyAuthSupported: true } }));
      } else if (message.type === "system_delete_screenshot" || message.type === "system_clear_screenshots") {
        queueFile(message, (reply) => {
          this.onMessage.emit(reply);
          if (this.oneShot) {
            this.closed = true;
            this.onDisconnect.emit();
          }
        }, () => {});
      } else {
        assert.equal(message.type, "heartbeat");
      }
    },
    disconnect() { this.onDisconnect.emit(); },
  };
  normalPorts.push(port);
  return port;
};
context.connectSSH = network.connectSSH;
context.disconnectSSH = network.disconnectSSH;
context.setNetworkEnabled = network.setNetworkEnabled;
context.networkDisconnected = network.networkDisconnected;
network.initializeNetwork(context.nativeRequest);
context.chrome.storage.local.set = async () => {};
let automationTabs = 0;
context.chrome.tabs.create = async () => { automationTabs++; };
runInContext("nativePort = null; tetherEnabled = false", context);
const popupRequest = (message) => new Promise((resolve) => context.chrome.runtime.onMessage.listeners[0](
  message, { id: "check", url: "chrome-extension://check/popup.html" }, resolve));
const turn = () => new Promise((resolve) => setImmediate(resolve));
const record = (filename) => ({
  filename, mirrored: true, targetHost: "dev-original", remotePath: `/shots/${filename}`,
});
const deletingFile = popupRequest({ type: "popup_delete_screenshot", filename: "denied.png" });
const clearingFiles = popupRequest({ type: "popup_clear_screenshots", screenshots: [
  record("clear.png"), { ...record("mixed.png"), targetHost: "dev-mixed" },
] });
await turn();
fileOperations.at(-1).reject(new Error("Local screenshot permission denied"));
assert.match((await deletingFile).error, /permission denied/);
assert.equal(localFiles.has("denied.png"), true, "A failed deletion must preserve the gallery file");
await turn();
fileOperations.at(-1).complete();
assert.equal((await clearingFiles).ok, true, "An earlier error must not poison the next gallery action");
assert.equal(localFiles.has("clear.png"), false);
assert.equal(remoteFiles.has("dev-original:/shots/clear.png"), false);
assert.equal(localFiles.has("mixed.png"), false);
assert.equal(remoteFiles.has("dev-mixed:/shots/mixed.png"), false);
assert.equal(remoteFiles.has("dev-original:/shots/mixed.png"), true,
  "Mixed-host Clear must use each gallery record's own destination");
const offlineStatus = await popupRequest({ type: "popup_network_status" });
assert.equal(offlineStatus.tetherEnabled, false);
assert.equal(offlineStatus.nativeConnected, false, "Offline file actions must leave the agent bridge closed");
assert.equal(normalPorts.length, 0);
context.payload = "é".repeat(limit / 2);
const oversizedFile = await popupRequest({ type: "popup_delete_screenshot", filename: context.payload });
assert.match(oversizedFile.error, /64 MiB/);
context.payload = null;
// A one-shot response cannot masquerade as an unrelated reply or automation.
for (const reply of [
  { id: "unrelated", result: { ok: true } },
  { type: "request", method: "browser.open", params: { url: "https://example.test" } },
]) {
  const invalid = popupRequest({ type: "popup_delete_screenshot", filename: "denied.png" });
  await turn();
  fileOperations.at(-1).respond(reply);
  assert.match((await invalid).error, /Invalid local screenshot helper response/);
}
const helperError = popupRequest({ type: "popup_delete_screenshot", filename: "denied.png" });
await turn();
fileOperations.at(-1).respond({ error: { message: "Screenshot directory unavailable" } });
assert.match((await helperError).error, /directory unavailable/);
assert.equal(automationTabs, 0);
assert.equal(localFiles.has("denied.png"), true);
assert.equal((await popupRequest({ type: "popup_network_status" })).tetherEnabled, false);

// File-first: Connect finishes without waiting for the bounded offline process.
const delayedDelete = popupRequest({ type: "popup_delete_screenshot", ...record("delayed.png") });
await turn();
const delayedOperation = fileOperations.at(-1);
const connectingSecond = popupRequest({ type: "popup_tether_connect", targetHost: "dev-a" });
await turn();
normalPorts.at(-1).connect();
assert.equal((await connectingSecond).state, "connected");
noSecret(await connectingSecond);
noSecret(await popupRequest({ type: "popup_network_status" }));
assert.equal(localFiles.has("delayed.png"), true, "Connect must complete before the delayed file reply");
delayedOperation.complete();
assert.equal((await delayedDelete).remoteDeleted, true);
assert.equal(localFiles.has("delayed.png"), false);
assert.equal(remoteFiles.has("dev-original:/shots/delayed.png"), false);
assert.equal((await popupRequest({ type: "popup_tether_disconnect" })).ok, true);

// Hold network initialization: Delete is the first operation after the port opens.
let releaseNetwork;
const networkHeld = new Promise((resolve) => { releaseNetwork = resolve; });
context.connectSSH = async (...args) => { await networkHeld; return network.connectSSH(...args); };
const connectingFirst = popupRequest({ type: "popup_tether_connect", targetHost: "dev-b" });
await turn();
const normalPort = normalPorts.at(-1);
const deletingDuringConnect = popupRequest({ type: "popup_delete_screenshot", ...record("connecting.png") });
await turn();
fileOperations.at(-1).complete();
assert.equal((await deletingDuringConnect).remoteDeleted, true);
assert.equal(remoteFiles.has("dev-original:/shots/connecting.png"), false);
assert.equal(remoteFiles.has("dev-b:/shots/connecting.png"), true,
  "Gallery provenance must protect an identically named file on the current connection");
releaseNetwork();
await turn();
normalPort.connect();
assert.equal((await connectingFirst).host, "dev-b", "First file operation must not exit the persistent native host");
assert.equal((await popupRequest({ type: "popup_tether_disconnect" })).ok, true);
context.connectSSH = network.connectSSH;

// Master Disconnect does not wait on an independent file process or enable automation.
const pendingDelete = popupRequest({ type: "popup_delete_screenshot", ...record("pending.png") });
await turn();
const pendingOperation = fileOperations.at(-1);
assert.equal((await popupRequest({ type: "popup_tether_disconnect" })).ok, true);
assert.equal(localFiles.has("pending.png"), true);
pendingOperation.complete();
const pendingResult = await pendingDelete;
assert.equal(pendingResult.localDeleted, true);
assert.equal(pendingResult.remoteDeleted, false);
assert.match(pendingResult.remoteError, /Original destination unavailable/);
assert.equal((await popupRequest({ type: "popup_network_status" })).tetherEnabled, false);
assert.equal(automationTabs, 0);
console.log("PASS: HTTP proxy auth boundaries/nonleak/retries/restart/EOF/OFF/reconnect, route consent/readiness/lifecycle, explicit fresh-session sign-in, hostile URLs, polls/flaps/timers, NetBird guidance, popup sender boundary, native UTF-8 limits and independent offline/Connect operations");
