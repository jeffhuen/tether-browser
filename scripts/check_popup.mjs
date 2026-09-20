// Run against a disposable Chrome profile with the unpacked extension loaded:
// node scripts/check_popup.mjs http://127.0.0.1:9349
// Chrome needs --remote-debugging-port=9349 and --enable-unsafe-extension-debugging.
// The check uses synthetic connection/notes data, real extension storage and UI.
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
    }, 10000);
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
    return {
      overflow: document.documentElement.scrollWidth > innerWidth,
      stacked: boxes.every((box, index) => index === 0 || box.top >= boxes[index - 1].bottom),
      buttonsFit: [...document.querySelectorAll("button")].filter((button) => button.getClientRects().length).every((button) => {
        const box = button.getBoundingClientRect();
        return box.left >= 0 && box.right <= innerWidth && box.height >= 24;
      }),
    };
  });
  assert.equal(layout.overflow, false, "Popup must not scroll horizontally");
  assert(layout.stacked, "Workspace sections must stack vertically");
  assert(layout.buttonsFit, "Buttons must stay within the popup and remain usable");
}
let saved;
try {
  saved = await evaluate(async () => {
    const saved = await chrome.storage.local.get(["recent_hosts", "tether_screenshots"]);
    window.popupCheckSend = chrome.runtime.sendMessage.bind(chrome.runtime);
    chrome.runtime.sendMessage = (message, callback) => message.type === "popup_get_status"
      ? callback(window.popupCheckStatus)
      : window.popupCheckSend(message, callback);
    return saved;
  });
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
        await chrome.storage.local.set({
          recent_hosts: ["reviewer@very-long-remote-development-host.example.test"],
          tether_screenshots: populated ? [{ filename: "popup-layout-check.png", data: canvas.toDataURL("image/png").split(",")[1], label: "A long screenshot title that must not squeeze the delete button", url: "https://example.test/a/long/path", dimensions: "1200 × 800", remotePath: "/tmp/tether-screenshots/a-long-screenshot-filename.png", comment: "Align the action with the form fields." }] : [],
        });
        document.querySelector("#btn-refresh-tabs").click();
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
          assert(await evaluate(() => document.documentElement.scrollHeight <= 600), "Empty popup must fit within Chrome's 600px height limit");
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
  console.log(`PASS: 320px/384px, empty/populated tabs, overflow, keyboard, preview, feedback persistence. Captures: ${captures}`);
} finally {
  if (saved) await evaluate(async (saved) => {
    chrome.runtime.sendMessage = window.popupCheckSend;
    delete window.popupCheckSend;
    delete window.popupCheckStatus;
    await chrome.storage.local.remove(["recent_hosts", "tether_screenshots"]);
    await chrome.storage.local.set(saved);
  }, saved);
  socket.close();
}
