// node scripts/check_network.mjs
// State-transition checks; actual SOCKS routing is exercised in disposable Chrome.
import assert from "node:assert/strict";

const storage = {};
const system = { mode: "system" };
let settings = { levelOfControl: "controllable_by_this_extension", value: system };
let settingScope;
let connection = { state: "disconnected", host: "" };
let nativeFailure = false;
let pendingReply;

globalThis.chrome = {
  runtime: {},
  action: { async setBadgeText() {}, async setBadgeBackgroundColor() {} },
  storage: { local: {
    async get() { return structuredClone(storage); },
    async set(value) { Object.assign(storage, structuredClone(value)); },
    async remove(key) { delete storage[key]; },
  } },
  proxy: { settings: {
    get(_, callback) { callback(structuredClone(settings)); },
    set({ scope, value }, callback) {
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
  if (nativeFailure) throw new Error("Native host disconnected");
  if (pendingReply) return pendingReply;
  if (message.type === "system_ssh_connect") {
    connection = { state: "connecting", host: message.targetHost, proxyPort: message.proxyPort || 32123, sessionId: "a" };
  }
  if (message.type === "system_ssh_disconnect") connection = { ...connection, state: "disconnected" };
  return structuredClone(connection);
}
let network = await import("../packages/extension/network.js");
network.initializeNetwork(request);
assert.equal((await network.networkStatus()).canEnable, false);
await assert.rejects(network.setNetworkEnabled(true, "a"), /Connect to an SSH host/);
await network.connectSSH("dev-a");
assert.equal((await network.networkStatus()).canEnable, false, "SSH startup is not readiness");
connection.state = "connected";
await assert.rejects(network.setNetworkEnabled(true, "old-session"), /connection changed/);
await network.setNetworkEnabled(true, "a");
assert.equal((await network.networkStatus()).state, "on");
await assert.rejects(network.connectSSH("dev-b"), /off before switching/);
await network.disconnectSSH();
assert.equal((await network.networkStatus()).state, "unavailable");
assert.equal((await network.networkStatus()).enabled, true, "Disconnect must not silently restore local routing");

// Worker restart + native-host loss preserves the requested route and OFF.
nativeFailure = true;
network = await import("../packages/extension/network.js?restart");
network.initializeNetwork(request);
assert.equal((await network.networkStatus()).state, "unavailable");
await network.setNetworkEnabled(false);
assert.equal((await network.networkStatus()).enabled, false);
assert.deepEqual(settings.value, system);
assert.equal(storage.tether_remote_network, undefined);

// Policy/extension ownership is visible, not mistaken for working routing.
nativeFailure = false;
connection = { state: "connected", host: "dev-a", proxyPort: 32123, sessionId: "b" };
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
console.log("PASS: readiness, consent identity, host pinning, disconnect, restart, offline OFF, proxy ownership, late enable/OFF and stale status");
