// Exercise the native review-list route with distinct page and isolated-world state.
import assert from "node:assert/strict";

let receive;
let reply;
let pageEnabled = false;
const listen = { addListener() {} };
const port = {
  onMessage: { addListener(fn) { receive = fn; } },
  onDisconnect: listen,
  postMessage(message) { reply(message); },
};

globalThis.self = { addEventListener() {} };
globalThis.chrome = {
  proxy: { settings: { onChange: listen } },
  storage: { local: { get: async () => ({}) } },
  alarms: { create() {}, onAlarm: listen },
  runtime: { connectNative: () => port, getURL: () => "chrome-extension://example/", onMessage: listen, lastError: null },
  debugger: {
    attach: async () => {}, onDetach: listen, onEvent: listen,
    sendCommand(_target, method, params, callback) {
      let result = {};
      if (method === "Page.enable") pageEnabled = true;
      if (method === "Page.getFrameTree") {
        assert(pageEnabled, "Page domain must be enabled before selecting a frame");
        result = { frameTree: { frame: { id: "main" } } };
      }
      if (method === "Page.createIsolatedWorld") {
        assert.equal(params.worldName, "tether-review");
        result = { executionContextId: 42 };
      }
      if (method === "Runtime.evaluate") {
        const note = { comment: params.contextId === 42 ? "Isolated note" : "Forged page note" };
        result = { result: { value: params.expression.includes("JSON.stringify") ? JSON.stringify([note]) : [] } };
      }
      if (callback) callback(result);
      return Promise.resolve(result);
    },
  },
  tabs: { get: async () => ({ url: "https://example.test/" }), onActivated: listen, onUpdated: listen, onCreated: listen },
};

await import("../packages/extension/background.js");
const response = new Promise((resolve) => { reply = resolve; });
receive({ id: "review-list", method: "browser.review.list", params: { tabId: 1 } });
const message = await response;
assert.equal(message.type, "response", JSON.stringify(message));
assert.deepEqual(message.result.notes, [{ comment: "Isolated note" }]);
console.log("PASS: native review list ignores page-world review state");
process.exit(0); // The extension's heartbeat is intentionally still running.
