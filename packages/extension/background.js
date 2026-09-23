// Tether Browser Bridge - Manifest V3 Background Service Worker
// Provides remote-to-local browser automation via chrome.debugger
// and native tab groups inside the developer's active Chromium browser.
import { initializeNetwork, networkStatus, networkDisconnected, connectSSH, disconnectSSH, setNetworkEnabled } from "./network.js";

const NATIVE_HOST_NAME = "com.tether_browser.host";
const CDP_TIMEOUT_MS = 25000;
const REVIEW_SAVED_BINDING = "__tetherReviewSaved";
const REVIEW_WORLD = "tether-review";

let nativePort = null;
let reconnectTimer = null;
let heartbeatTimer = null;
let tabGroupId = null;
let tabGroupTabs = new Set();
let activeTabId = null;

// Attached CDP tabs: tabId -> { enabledDomains: Set }
const attachedTabs = new Map();
const attachingTabs = new Map();

// Node reference registry for compact @e1, @e2 element references: tabId -> Map<ref, nodeInfo>
const elementRefsByTab = new Map();
// Tab snapshot generation and fingerprint cache: tabId -> { generation: number, lastHash: string }
const tabSnapshotStates = new Map();
const NO_ENABLE_DOMAINS = new Set(["Input"]);
let isDaemonConnected = false;
const pendingNative = new Map();
let nativeReqSeq = 0;

function nativeRequest(msg, timeout = 15000) {
  return new Promise((resolve, reject) => {
    if (!nativePort) {
      reject(new Error("Native host not connected"));
      return;
    }
    const id = `nr_${Date.now()}_${nativeReqSeq++}`;
    const timer = setTimeout(() => {
      const p = pendingNative.get(id);
      if (p) {
        pendingNative.delete(id);
        p.reject(new Error("Native request timed out"));
      }
    }, timeout);
    pendingNative.set(id, { resolve, reject, timer });
    try {
      nativePort.postMessage({ ...msg, id });
    } catch (error) {
      clearTimeout(timer);
      pendingNative.delete(id);
      reject(error);
    }
  });
}

initializeNetwork(nativeRequest);
chrome.proxy.settings.onChange.addListener(() => {
  void networkStatus().catch((error) => console.warn("[Tether] Remote network:", error.message));
});
// Prevent unhandled rejections from terminating the service worker
self.addEventListener("unhandledrejection", (event) => {
  event.preventDefault();
});

// Alarm keep-alive backstop for MV3 service worker
chrome.alarms.create("tether_keepalive", { periodInMinutes: 1 });
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === "tether_keepalive" && !nativePort) {
    connectNativeHost();
  }
});

// --- Native Messaging Connection ---

function connectNativeHost() {
  if (nativePort) return;
  try {
    const port = chrome.runtime.connectNative(NATIVE_HOST_NAME);
    nativePort = port;
    port.onMessage.addListener((message) => {
      if (nativePort === port) void handleNativeMessage(message);
    });
    port.onDisconnect.addListener(() => {
      if (nativePort === port) handleNativeDisconnect();
    });
    startHeartbeat();
    console.log("[Tether] Connected to native messaging host:", NATIVE_HOST_NAME);
  } catch (err) {
    console.warn("[Tether] Failed to connect to native messaging host:", err.message);
    scheduleReconnect();
  }
}

function handleNativeDisconnect() {
  stopHeartbeat();
  const lastError = chrome.runtime.lastError;
  if (lastError) {
    console.warn("[Tether] Native host disconnected:", lastError.message);
  } else {
    console.log("[Tether] Native host disconnected");
  }
  nativePort = null;
  isDaemonConnected = false;
  networkDisconnected();
  for (const pending of pendingNative.values()) {
    clearTimeout(pending.timer);
    pending.reject(new Error("Native host disconnected"));
  }
  pendingNative.clear();
  scheduleReconnect();
}

function scheduleReconnect() {
  if (reconnectTimer) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connectNativeHost();
  }, 1500);
}

function startHeartbeat() {
  stopHeartbeat();
  heartbeatTimer = setInterval(() => {
    if (!nativePort) return;
    try {
      nativePort.postMessage({ type: "heartbeat", timestamp: Date.now() });
    } catch {
      // Handled by onDisconnect
    }
    void networkStatus().catch((error) => console.warn("[Tether] Remote network:", error.message));
  }, 15000);
}

function stopHeartbeat() {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer);
    heartbeatTimer = null;
  }
}

function sendResponse(id, result) {
  if (!nativePort) return;
  try {
    nativePort.postMessage({ id, type: "response", result });
  } catch (err) {
    console.warn("[Tether] Failed to send response:", err.message);
  }
}

function sendError(id, code, message) {
  if (!nativePort) return;
  try {
    nativePort.postMessage({ id, type: "error", error: { code, message } });
  } catch (err) {
    console.warn("[Tether] Failed to send error:", err.message);
  }
}

// --- Tab Group Lifecycle ---

async function ensureTabGroup(createIfEmpty = true) {
  if (tabGroupId !== null) {
    try {
      const group = await chrome.tabGroups.get(tabGroupId);
      if (group) {
        const tabs = await chrome.tabs.query({ groupId: tabGroupId });
        tabGroupTabs = new Set(tabs.map((t) => t.id));
        if (tabGroupTabs.size > 0) return tabGroupId;
      }
    } catch {
      tabGroupId = null;
      tabGroupTabs.clear();
    }
  }

  // Look for any existing Tether tab group in current windows
  try {
    const existingGroups = await chrome.tabGroups.query({ title: "Tether" });
    if (existingGroups.length > 0) {
      const group = existingGroups[0];
      tabGroupId = group.id;
      const tabs = await chrome.tabs.query({ groupId: tabGroupId });
      tabGroupTabs = new Set(tabs.map((t) => t.id));
      if (tabGroupTabs.size > 0) return tabGroupId;
    }
  } catch {}

  if (!createIfEmpty) return null;

  // Use chrome.windows.getAll to find user's visible normal window
  // (In service workers, currentWindow:true returns [] when Chrome is unfocused)
  try {
    const windows = await chrome.windows.getAll({ populate: true, windowTypes: ["normal"] });
    if (windows && windows.length > 0) {
      const targetWin = windows.find((w) => w.focused) || windows[0];
      const tab = await chrome.tabs.create({ windowId: targetWin.id, url: "data:text/html,", active: true });
      const groupId = await chrome.tabs.group({ tabIds: [tab.id] });
      await chrome.tabGroups.update(groupId, { title: "Tether", color: "blue" });
      tabGroupId = groupId;
      tabGroupTabs = new Set([tab.id]);
      activeTabId = tab.id;
      return tabGroupId;
    }
  } catch (err) {
    console.warn("[Tether] Failed to create tab in existing window:", err);
  }

  // Fallback: create focused window if no normal window exists
  const win = await chrome.windows.create({ focused: true, url: "data:text/html," });
  const tab = win.tabs[0];
  const groupId = await chrome.tabs.group({ tabIds: [tab.id] });
  await chrome.tabGroups.update(groupId, { title: "Tether", color: "blue" });
  tabGroupId = groupId;
  tabGroupTabs = new Set([tab.id]);
  activeTabId = tab.id;
  return tabGroupId;
}

// --- CDP Connection Management ---

// about:blank can inherit our extension origin; empty automation tabs must be opaque.
const AUTOMATION_PROTOCOLS = new Set(["http:", "https:", "data:", "file:"]);

function assertAutomationURL(url) {
  url = new URL(String(url), chrome.runtime.getURL(""));
  if (url.protocol === "blob:" && url.pathname.startsWith("null/")) return;
  if (url.protocol === "blob:" || url.protocol === "filesystem:") url = new URL(url.pathname);
  if (!AUTOMATION_PROTOCOLS.has(url.protocol)) {
    throw new Error("Remote automation cannot access browser or extension pages");
  }
}

async function assertAutomationTab(tabId) {
  const tab = await chrome.tabs.get(tabId);
  if (!tab.url && !tab.pendingUrl) throw new Error("Wait for the tab to finish navigating");
  if (tab.url) assertAutomationURL(tab.url);
  if (tab.pendingUrl) assertAutomationURL(tab.pendingUrl);
}

async function ensureAttached(tabId) {
  if (attachedTabs.has(tabId)) return;
  if (attachingTabs.has(tabId)) return attachingTabs.get(tabId);

  const attachPromise = (async () => {
    await assertAutomationTab(tabId);
    await chrome.debugger.attach({ tabId }, "1.3");
    attachedTabs.set(tabId, { enabledDomains: new Set() });

    // Focus emulation: allows background/unselected tabs to receive input
    // without stealing user's desktop window focus
    try {
      await chrome.debugger.sendCommand({ tabId }, "Emulation.setFocusEmulationEnabled", {
        enabled: true,
      });
    } catch (e) {
      console.warn("[Tether] setFocusEmulationEnabled unavailable:", e.message);
    }
  })();

  attachingTabs.set(tabId, attachPromise);
  try {
    await attachPromise;
  } finally {
    attachingTabs.delete(tabId);
  }
}

async function ensureDomain(tabId, domain) {
  await ensureAttached(tabId);
  const state = attachedTabs.get(tabId);
  if (!state || NO_ENABLE_DOMAINS.has(domain) || state.enabledDomains.has(domain)) return;
  await cdp(tabId, `${domain}.enable`, {});
  state.enabledDomains.add(domain);
}

async function cdp(tabId, method, params = {}) {
  // A tab can navigate after attachment. Recheck at the command boundary.
  await assertAutomationTab(tabId);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      reject(new Error(`CDP command ${method} timed out after ${CDP_TIMEOUT_MS}ms`));
    }, CDP_TIMEOUT_MS);

    chrome.debugger.sendCommand({ tabId }, method, params, (result) => {
      clearTimeout(timer);
      const lastError = chrome.runtime.lastError;
      if (lastError) {
        reject(new Error(lastError.message));
      } else {
        resolve(result || {});
      }
    });
  });
}

// Handle debugger detach (user closed infobar or tab closed)
chrome.debugger.onDetach.addListener((source) => {
  attachedTabs.delete(source.tabId);
  elementRefsByTab.delete(source.tabId);
});

async function reopenPopup(tabId, panel) {
  try {
    const tab = await chrome.tabs.get(tabId);
    const window = await chrome.windows.getLastFocused();
    // A completed capture must not interrupt another tab or window.
    if (tab.active && window.focused && window.id === tab.windowId) {
      await chrome.storage.local.set({ tether_popup_tab: panel });
      await chrome.action.openPopup({ windowId: tab.windowId });
    }
  } catch (err) {
    console.warn("[Tether] Could not reopen popup:", err.message);
  }
}

chrome.debugger.onEvent.addListener((source, method, params) => {
  if (method === "Runtime.bindingCalled" && params.name === REVIEW_SAVED_BINDING
      && params.payload === "saved" && source.tabId !== undefined) {
    reopenPopup(source.tabId, "notes");
  }
});

function resolveTargetTabId(params = {}) {
  const raw = params.targetId || params.tabId;
  if (raw !== undefined && raw !== null && raw !== "") {
    const parsed = parseInt(raw, 10);
    if (!isNaN(parsed) && parsed > 0) return parsed;
  }
  return activeTabId;
}
async function getLiveTabs() {
  if (tabGroupId === null) {
    await ensureTabGroup(false);
  }
  if (tabGroupId !== null) {
    try {
      const tabs = await chrome.tabs.query({ groupId: tabGroupId });
      if (tabs.length > 0) return tabs;
    } catch {}
  }
  return [];
}

// Track user tab switching in Chrome
chrome.tabs.onActivated.addListener(async (activeInfo) => {
  try {
    const tab = await chrome.tabs.get(activeInfo.tabId);
    if (tab && tabGroupId !== null && tab.groupId === tabGroupId) {
      activeTabId = activeInfo.tabId;
      tabGroupTabs.add(activeInfo.tabId);
    }
  } catch {}
});

// Track user navigation, redirects, and title updates
chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
  if (changeInfo.status === "loading" || changeInfo.url) {
    elementRefsByTab.delete(tabId);
    tabSnapshotStates.delete(tabId);
  }
  if (changeInfo.url) {
    try {
      assertAutomationURL(changeInfo.url);
    } catch {
      tabGroupTabs.delete(tabId);
      if (attachedTabs.has(tabId)) void chrome.debugger.detach({ tabId }).catch(() => {});
      return;
    }
  }
  if (tabGroupId !== null && tab.groupId === tabGroupId) {
    tabGroupTabs.add(tabId);
  }
});

// Track popups and links with target="_blank"
chrome.tabs.onCreated.addListener(async (tab) => {
  if (tab.openerTabId && tabGroupTabs.has(tab.openerTabId)) {
    if (tabGroupId !== null) {
      try {
        await chrome.tabs.group({ tabIds: [tab.id], groupId: tabGroupId });
      } catch {}
    }
    tabGroupTabs.add(tab.id);
    activeTabId = tab.id;
  }
});
async function handleOpen(params = {}) {
  const url = params.url || "data:text/html,";
  assertAutomationURL(url);
  await ensureTabGroup(true);

  let targetTabId = resolveTargetTabId(params);
  // Empty requests need a new tab: Chrome blocks data: navigation in existing tabs.
  if (!params.url || params.newTab || !targetTabId || !tabGroupTabs.has(targetTabId)) {
    // Create new tab in group
    const tab = await chrome.tabs.create({ url, active: true });
    await chrome.tabs.group({ tabIds: [tab.id], groupId: tabGroupId });
    tabGroupTabs.add(tab.id);
    activeTabId = tab.id;
    targetTabId = tab.id;
  } else {
    // Navigate existing tab
    await assertAutomationTab(targetTabId);
    await chrome.tabs.update(targetTabId, { url, active: true });
  }

  await ensureDomain(targetTabId, "Page");
  return {
    targetId: String(targetTabId),
    tabId: targetTabId,
    url,
  };
}

function computeTreeHash(nodes, targetUrl, title) {
  let h = 0x811c9dc5;
  const str = (targetUrl || "") + "|" + (title || "") + "|" + nodes.map((n) => `${n.ref}:${n.role}:${n.name}:${n.value || ""}:${n.checked || ""}:${n.selected || ""}:${n.expanded || ""}:${n.disabled || ""}`).join(";");
  for (let i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return `hash-${(h >>> 0).toString(16)}`;
}

async function handleSnapshot(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");
  await ensureDomain(tabId, "Accessibility");
  await ensureDomain(tabId, "DOM");

  let targetUrl = "";
  let title = "";
  try {
    const tab = await chrome.tabs.get(tabId);
    targetUrl = tab.url || "";
    title = tab.title || "";
  } catch {}

  const axResult = await cdp(tabId, "Accessibility.getFullAXTree", {});
  const nodes = axResult.nodes || [];

  // Index interactive elements and assign @e1, @e2 references
  const refMap = new Map();
  let refCounter = 1;
  const simplifiedTree = [];

  for (const node of nodes) {
    if (node.ignored) continue;
    const role = node.role?.value || "";
    const name = node.name?.value || "";
    const value = node.value?.value;

    const props = {};
    for (const p of node.properties || []) {
      props[p.name] = p.value?.value;
    }

    const isInteractive = [
      "button", "link", "textbox", "checkbox", "radio", "combobox",
      "menuitem", "tab", "searchbox", "switch"
    ].includes(role);

    if (params.interactiveOnly && !isInteractive) continue;
    if (params.compact && !name && !value && !isInteractive) continue;

    let ref = "";
    if (params.interactiveOnly ? isInteractive : (isInteractive || name)) {
      ref = `@e${refCounter++}`;
      refMap.set(ref, {
        backendDOMNodeId: node.backendDOMNodeId,
        role,
        name,
      });
    }

    const nodeData = {
      ref,
      role,
      name,
      value,
      disabled: props.disabled === true || node.disabled?.value === true || false,
      focused: props.focused === true || false,
      selected: props.selected === true || false,
      expanded: props.expanded === true || false,
    };
    if (props.checked !== undefined) {
      nodeData.checked = String(props.checked);
    }

    simplifiedTree.push(nodeData);
  }
  elementRefsByTab.set(tabId, refMap);

  let tabState = tabSnapshotStates.get(tabId) || { generation: 0, lastHash: "" };
  const currentHash = computeTreeHash(simplifiedTree, targetUrl, title);
  if (currentHash !== tabState.lastHash) {
    tabState.generation++;
    tabState.lastHash = currentHash;
  }
  tabSnapshotStates.set(tabId, tabState);

  const refTable = {};
  for (const [r, info] of refMap.entries()) {
    if (info.backendDOMNodeId) {
      refTable[r] = info.backendDOMNodeId;
    }
  }

  return {
    targetId: String(tabId),
    tabId,
    targetUrl,
    title,
    generation: tabState.generation,
    rootHash: currentHash,
    modified: !params.lastGeneration || params.lastGeneration !== tabState.generation,
    nodes: simplifiedTree,
    nodeCount: simplifiedTree.length,
    refTable,
  };
}


async function resolveRefCoordinates(tabId, ref) {
  const refMap = elementRefsByTab.get(tabId);
  if (!refMap || !refMap.has(ref)) {
    throw new Error(`Element reference ${ref} not found; run snapshot first`);
  }
  const item = refMap.get(ref);
  if (!item.backendDOMNodeId) {
    throw new Error(`Element reference ${ref} has no backend node ID`);
  }

  await ensureDomain(tabId, "DOM");
  try {
    await cdp(tabId, "DOM.scrollIntoViewIfNeeded", { backendNodeId: item.backendDOMNodeId });
  } catch {}

  const boxModel = await cdp(tabId, "DOM.getBoxModel", {
    backendNodeId: item.backendDOMNodeId,
  });

  const content = boxModel.model?.content;
  if (!content || content.length < 8) {
    throw new Error(`Element reference ${ref} has no visible bounding box`);
  }

  // Calculate center of quad: [x1, y1, x2, y2, x3, y3, x4, y4]
  const x = (content[0] + content[2] + content[4] + content[6]) / 4;
  const y = (content[1] + content[3] + content[5] + content[7]) / 4;
  return { x: Math.round(x), y: Math.round(y), backendNodeId: item.backendDOMNodeId };
}
async function resolveSelectorCoordinates(tabId, sel) {
  await ensureDomain(tabId, "DOM");
  const doc = await cdp(tabId, "DOM.getDocument");
  const result = await cdp(tabId, "DOM.querySelector", {
    nodeId: doc.root.nodeId,
    selector: sel,
  });
  if (!result || !result.nodeId) {
    throw new Error(`Element with selector "${sel}" not found`);
  }
  const boxModel = await cdp(tabId, "DOM.getBoxModel", {
    nodeId: result.nodeId,
  });
  const content = boxModel.model?.content;
  if (!content || content.length < 8) {
    throw new Error(`Element "${sel}" has no visible bounding box`);
  }
  const x = (content[0] + content[2] + content[4] + content[6]) / 4;
  const y = (content[1] + content[3] + content[5] + content[7]) / 4;
  return { x: Math.round(x), y: Math.round(y) };
}

async function resolveCoordinates(tabId, sel) {
  if (!sel || typeof sel !== "string") return null;
  if (sel.startsWith("@")) {
    return await resolveRefCoordinates(tabId, sel);
  }
  return await resolveSelectorCoordinates(tabId, sel);
}

async function handleClick(params = {}, clickCount = 1) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  let x = params.x;
  let y = params.y;
  let backendNodeId = null;
  const sel = params.selector || params.ref;
  if (sel && typeof sel === "string") {
    const coords = await resolveCoordinates(tabId, sel);
    if (coords) {
      x = coords.x;
      y = coords.y;
      backendNodeId = coords.backendNodeId;
    }
  }

  if (typeof x !== "number" || typeof y !== "number") {
    throw new Error(`Click requires either a valid @ref, selector, or (x, y) coordinates; received: ${sel || "none"}`);
  }

  // Act-time hit-test, occlusion check, and viewport validation
  if (backendNodeId) {
    const hitCheck = await cdp(tabId, "Runtime.evaluate", {
      expression: `(() => {
        const el = document.elementFromPoint(${x}, ${y});
        if (!el) return { ok: false, reason: "off-viewport" };
        return { ok: true, tag: el.tagName };
      })()`,
      returnByValue: true,
    }).catch(() => null);

    if (hitCheck?.result?.value && !hitCheck.result.value.ok) {
      throw new Error(`Click target at (${x}, ${y}) is outside visible viewport`);
    }

    try {
      const loc = await cdp(tabId, "DOM.getNodeForLocation", {
        x: Math.round(x),
        y: Math.round(y),
        includeUserAgentShadowDOM: true,
      });
      if (loc?.backendNodeId && loc.backendNodeId !== backendNodeId) {
        const desc = await cdp(tabId, "DOM.describeNode", { backendNodeId: loc.backendNodeId, depth: 1 });
        const hitTag = desc?.node?.nodeName || "ELEMENT";
        const attrs = JSON.stringify(desc?.node?.attributes || []);
        if (hitTag === "BODY" || hitTag === "HTML" || /dialog|modal|overlay|backdrop|cookie|banner|mantine/i.test(attrs)) {
          throw new Error(`Click target at (${x}, ${y}) is occluded by <${hitTag}>`);
        }
      }
    } catch (err) {
      if (err.message?.includes("occluded")) throw err;
    }
  }
  await ensureDomain(tabId, "Input");

  await cdp(tabId, "Input.dispatchMouseEvent", {
    type: "mouseMoved",
    x,
    y,
  });

  await cdp(tabId, "Input.dispatchMouseEvent", {
    type: "mousePressed",
    x,
    y,
    button: "left",
    buttons: 1,
    clickCount,
  });

  await cdp(tabId, "Input.dispatchMouseEvent", {
    type: "mouseReleased",
    x,
    y,
    button: "left",
    buttons: 0,
    clickCount,
  });

  return { tabId, clicked: { x, y } };
}


async function handleFill(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  const sel = params.selector || params.ref;
  let coords = null;
  if (sel && typeof sel === "string") {
    coords = await resolveCoordinates(tabId, sel);
  }

  if (coords?.backendNodeId) {
    await ensureDomain(tabId, "DOM");
    try {
      await cdp(tabId, "DOM.focus", { backendNodeId: coords.backendNodeId });
    } catch {}
  }
  if (coords) {
    await handleClick({ targetId: tabId, selector: sel });
  }

  await ensureDomain(tabId, "Input");

  await cdp(tabId, "Input.dispatchKeyEvent", {
    type: "rawKeyDown",
    commands: ["selectAll", "delete"],
  });

  if (params.text) {
    await cdp(tabId, "Input.insertText", { text: params.text });
  }

  return { tabId, filled: params.text };
}

async function handleType(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  const sel = params.selector || params.ref;
  let coords = null;
  if (sel && typeof sel === "string") {
    coords = await resolveCoordinates(tabId, sel);
  }

  if (coords?.backendNodeId) {
    await ensureDomain(tabId, "DOM");
    try {
      await cdp(tabId, "DOM.focus", { backendNodeId: coords.backendNodeId });
    } catch {}
  }
  if (coords) {
    await handleClick({ targetId: tabId, selector: sel });
  }

  await ensureDomain(tabId, "Input");

  if (params.text) {
    await cdp(tabId, "Input.insertText", { text: params.text });
  }

  return { tabId, typed: params.text };
}


async function handleHover(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  let x = params.x;
  let y = params.y;
  const sel = params.selector || params.ref;
  if (sel && typeof sel === "string") {
    const coords = await resolveCoordinates(tabId, sel);
    if (coords) {
      x = coords.x;
      y = coords.y;
    }
  }

  if (typeof x !== "number" || typeof y !== "number") {
    throw new Error(`Hover requires either a valid @ref, selector, or (x, y) coordinates; received: ${sel || "none"}`);
  }

  await ensureDomain(tabId, "Input");
  await cdp(tabId, "Input.dispatchMouseEvent", {
    type: "mouseMoved",
    x,
    y,
  });

  return { tabId, hovered: { x, y } };
}

async function handleFocus(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  const sel = params.selector || params.ref;
  if (!sel) throw new Error("Focus requires a valid @ref or selector");

  await ensureDomain(tabId, "DOM");
  if (sel.startsWith("@")) {
    const item = elementRefsByTab.get(tabId)?.get(sel);
    if (!item?.backendDOMNodeId) throw new Error(`Element reference ${sel} not found; run snapshot first`);
    await cdp(tabId, "DOM.focus", { backendNodeId: item.backendDOMNodeId });
  } else {
    const doc = await cdp(tabId, "DOM.getDocument");
    const node = await cdp(tabId, "DOM.querySelector", { nodeId: doc.root.nodeId, selector: sel });
    if (!node.nodeId) throw new Error(`Element with selector "${sel}" not found`);
    await cdp(tabId, "DOM.focus", { nodeId: node.nodeId });
  }
  return { tabId, focused: sel };
}

async function handlePress(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");
  if (!params.key) throw new Error("Press requires a key");

  await ensureDomain(tabId, "Input");

  let key = params.key;
  let modifiers = 0;

  if (key.includes("+")) {
    const parts = key.split("+");
    key = parts.pop() || "+";
    for (const mod of parts) {
      switch (mod.toLowerCase()) {
        case "ctrl":
        case "control":
          modifiers |= 2;
          break;
        case "alt":
          modifiers |= 1;
          break;
        case "shift":
          modifiers |= 8;
          break;
        case "meta":
        case "cmd":
        case "command":
          modifiers |= 4;
          break;
      }
    }
  }

  const keyMap = {
    Enter: { key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, text: "\r" },
    Tab: { key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 },
    Escape: { key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 },
    Backspace: { key: "Backspace", code: "Backspace", windowsVirtualKeyCode: 8 },
    Delete: { key: "Delete", code: "Delete", windowsVirtualKeyCode: 46 },
    Home: { key: "Home", code: "Home", windowsVirtualKeyCode: 36 },
    End: { key: "End", code: "End", windowsVirtualKeyCode: 35 },
    ArrowDown: { key: "ArrowDown", code: "ArrowDown", windowsVirtualKeyCode: 40 },
    ArrowUp: { key: "ArrowUp", code: "ArrowUp", windowsVirtualKeyCode: 38 },
    ArrowLeft: { key: "ArrowLeft", code: "ArrowLeft", windowsVirtualKeyCode: 37 },
    ArrowRight: { key: "ArrowRight", code: "ArrowRight", windowsVirtualKeyCode: 39 },
    PageDown: { key: "PageDown", code: "PageDown", windowsVirtualKeyCode: 34 },
    PageUp: { key: "PageUp", code: "PageUp", windowsVirtualKeyCode: 33 },
  };

  let keyDef = keyMap[key];
  if (!keyDef) {
    const isSingleChar = key.length === 1;
    let code = key;
    let vk = key.charCodeAt(0) || 0;
    if (isSingleChar) {
      if (key >= "a" && key <= "z") {
        code = `Key${key.toUpperCase()}`;
        vk = key.toUpperCase().charCodeAt(0);
      } else if (key >= "A" && key <= "Z") {
        code = `Key${key}`;
        vk = key.charCodeAt(0);
      } else if (key >= "0" && key <= "9") {
        code = `Digit${key}`;
        vk = key.charCodeAt(0);
      }
    }
    keyDef = {
      key,
      code,
      windowsVirtualKeyCode: vk,
      text: isSingleChar && !(modifiers & (2 | 1 | 4)) ? key : undefined,
    };
  }

  await cdp(tabId, "Input.dispatchKeyEvent", {
    type: "rawKeyDown",
    key: keyDef.key,
    code: keyDef.code,
    modifiers,
    windowsVirtualKeyCode: keyDef.windowsVirtualKeyCode,
    text: keyDef.text,
    unmodifiedText: keyDef.text,
  });

  if (keyDef.text) {
    await cdp(tabId, "Input.dispatchKeyEvent", {
      type: "char",
      key: keyDef.key,
      code: keyDef.code,
      modifiers,
      windowsVirtualKeyCode: keyDef.windowsVirtualKeyCode,
      text: keyDef.text,
      unmodifiedText: keyDef.text,
    });
  }

  await cdp(tabId, "Input.dispatchKeyEvent", {
    type: "keyUp",
    key: keyDef.key,
    code: keyDef.code,
    modifiers,
    windowsVirtualKeyCode: keyDef.windowsVirtualKeyCode,
  });

  return { tabId, pressed: params.key };
}

async function handleScroll(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");
  await ensureDomain(tabId, "Input");

  let deltaY = params.deltaY || 0;
  let deltaX = params.deltaX || 0;

  if (params.direction === "top") {
    await cdp(tabId, "Runtime.evaluate", { expression: "window.scrollTo(0, 0)" });
    return { tabId, scrolled: "top" };
  } else if (params.direction === "bottom") {
    await cdp(tabId, "Runtime.evaluate", { expression: "window.scrollTo(0, document.body.scrollHeight)" });
    return { tabId, scrolled: "bottom" };
  } else if (params.direction === "up") {
    deltaY = -600;
  } else if (params.direction === "down") {
    deltaY = 600;
  } else if (!deltaY && !deltaX) {
    deltaY = 600;
  }

  await cdp(tabId, "Input.dispatchMouseEvent", {
    type: "mouseWheel",
    x: 400,
    y: 300,
    deltaX,
    deltaY,
  });

  return { tabId, scrolled: { deltaX, deltaY } };
}

async function handleWait(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  if (params.durationMs && params.durationMs > 0) {
    await new Promise((resolve) => setTimeout(resolve, params.durationMs));
    return { tabId, waitedMs: params.durationMs };
  }

  if (params.selector) {
    const timeoutMs = params.timeoutMs || 25000;
    const start = Date.now();
    await ensureDomain(tabId, "DOM");
    while (Date.now() - start < timeoutMs) {
      try {
        let matched = false;
        if (params.selector.startsWith("@")) {
          const item = elementRefsByTab.get(tabId)?.get(params.selector);
          if (item?.backendDOMNodeId) {
            const { object } = await cdp(tabId, "DOM.resolveNode", { backendNodeId: item.backendDOMNodeId });
            try {
              const res = await cdp(tabId, "Runtime.callFunctionOn", {
                objectId: object.objectId, functionDeclaration: "function() { return this.isConnected; }", returnByValue: true,
              });
              matched = res.result?.value === true;
            } finally {
              await cdp(tabId, "Runtime.releaseObject", { objectId: object.objectId });
            }
          }
        } else {
          const doc = await cdp(tabId, "DOM.getDocument");
          const res = await cdp(tabId, "DOM.querySelector", {
            nodeId: doc.root.nodeId,
            selector: params.selector,
          });
          matched = res.nodeId > 0;
        }
        if (matched) {
          return { tabId, matched: params.selector, elapsedMs: Date.now() - start };
        }
      } catch (err) {
        // continue polling
      }
      await new Promise((r) => setTimeout(r, 200));
    }
    throw new Error(`Timeout waiting for selector "${params.selector}" after ${timeoutMs}ms`);
  }

  return { tabId, waited: true };
}

async function handleClose(params = {}) {
  if (params.closeAll) {
    const tabs = await getLiveTabs();
    for (const t of tabs) {
      try {
        await chrome.tabs.remove(t.id);
      } catch (err) {}
    }
    return { closedAll: true, count: tabs.length };
  }
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");
  await chrome.tabs.remove(tabId);
  return { tabId, closed: true };
}


async function handleEval(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  await ensureDomain(tabId, "Runtime");
  const res = await cdp(tabId, "Runtime.evaluate", {
    expression: params.expression || params.expr || "",
    returnByValue: true,
    awaitPromise: true,
  });

  return {
    targetId: String(tabId),
    tabId,
    value: res.result?.value,
    type: res.result?.type,
    description: res.result?.description,
  };
}

async function handleScreenshot(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  await ensureDomain(tabId, "Page");
  const format = params.format || "png";
  const cdpParams = {
    format,
    captureBeyondViewport: Boolean(params.fullPage || params.clip),
  };
  if (format === "jpeg" && params.quality) {
    cdpParams.quality = params.quality;
  }
  const viewport = await cdp(tabId, "Runtime.evaluate", {
    expression: "({x:scrollX,y:scrollY,width:innerWidth,height:innerHeight,scale:1/devicePixelRatio})",
    returnByValue: true,
  });
  const scale = viewport.result?.value?.scale;
  const metrics = await cdp(tabId, "Page.getLayoutMetrics");
  const zoom = metrics.cssVisualViewport?.zoom;
  const clip = params.fullPage ? metrics.cssContentSize : (params.clip || viewport.result?.value);
  const { x, y, width, height } = clip || {};
  if (![x, y, width, height, scale, zoom].every(Number.isFinite) || width <= 0 || height <= 0 || scale <= 0 || zoom <= 0) {
    throw new Error("Screenshot area must have finite coordinates and positive dimensions and scale");
  }
  // CDP clips use device-independent pixels; page coordinates use CSS pixels.
  // Scale before encoding instead of transferring and resizing a retina image.
  cdpParams.clip = { x: x * zoom, y: y * zoom, width: width * zoom, height: height * zoom, scale };
  const res = await cdp(tabId, "Page.captureScreenshot", cdpParams);
  const png = format === "png" ? new DataView(Uint8Array.from(atob(res.data.slice(0, 32)), (c) => c.charCodeAt(0)).buffer) : null;
  return {
    targetId: String(tabId),
    tabId,
    format,
    width: png?.getUint32(16),
    height: png?.getUint32(20),
    data: res.data,
    base64: res.data,
  };
}
async function handleStatus() {
  await ensureTabGroup(false);
  const tabs = await getLiveTabs();
  return {
    tabGroupId,
    tabs: tabs.map((t) => t.id),
    targetCount: tabs.length,
    activeTargetId: String(activeTabId || (tabs.length > 0 ? tabs[0].id : "")),
    mode: "extension",
    version: chrome.runtime.getManifest().version,
    connected: isDaemonConnected,
  };
}

async function handleTabList() {
  await ensureTabGroup(false);
  const tabs = await getLiveTabs();
  return {
    tabs: tabs.map((t) => ({
      id: String(t.id),
      title: t.title || "Untitled",
      url: t.url || "",
      active: t.id === activeTabId || t.active,
      inGroup: true,
    })),
    activeId: String(activeTabId || (tabs.length > 0 ? tabs[0].id : "")),
    tabGroupId,
  };
}

async function handleTabSwitch(params = {}) {
  const target = params.targetId || params.tabId;
  const tabId = parseInt(target, 10);
  if (isNaN(tabId)) {
    throw new Error(`Invalid tab ID: ${target}`);
  }
  await assertAutomationTab(tabId);
  await chrome.tabs.update(tabId, { active: true });
  activeTabId = tabId;

  if (tabGroupId !== null) {
    try {
      const tab = await chrome.tabs.get(tabId);
      if (tab && tab.groupId !== tabGroupId) {
        await chrome.tabs.group({ tabIds: [tabId], groupId: tabGroupId });
        tabGroupTabs.add(tabId);
      }
    } catch {}
  }

  await ensureAttached(tabId);
  return { ok: true, activeId: String(tabId) };
}

async function reviewEval(tabId, expression) {
  await ensureDomain(tabId, "Page");
  await ensureDomain(tabId, "Runtime");
  const { frameTree } = await cdp(tabId, "Page.getFrameTree");
  const frameId = frameTree?.frame?.id;
  if (!frameId) throw new Error("Review main frame not found");
  const { executionContextId } = await cdp(tabId, "Page.createIsolatedWorld", {
    frameId,
    worldName: REVIEW_WORLD,
  });
  if (!executionContextId) throw new Error("Review isolated world not found");
  const result = await cdp(tabId, "Runtime.evaluate", {
    expression,
    contextId: executionContextId,
    returnByValue: true,
  });
  if (result.exceptionDetails) {
    throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text || "Review script failed");
  }
  return result.result?.value;
}

// DOM is shared, but framework expandos live only in the page's JS world.
// Keep review state isolated; import only bounded framework metadata for pinned notes.
async function enrichReviewFrameworks(tabId) {
  const notes = await reviewEval(tabId, "window.__tetherReview?.getNotes() || []");
  const updates = [];
  for (const note of notes) {
    if (note?.payload?.target?.framework?.name !== "Static" || !/^note-\d+$/.test(note.id)) continue;
    let value;
    try {
      const response = await cdp(tabId, "Runtime.evaluate", {
        expression: `((id) => {
          const el = document.querySelector('[data-tether-pin~="' + id + '"]');
          if (!el) return null;
          for (let node = el; node && node !== document.body; node = node.parentElement) {
            if (node.__svelte_meta?.loc) {
              const loc = node.__svelte_meta.loc;
              return { name: "Svelte", sourceLocation: loc.file + ":" + loc.line, provenance: "exact" };
            }
            if (node.__vueParentComponent) {
              const type = node.__vueParentComponent.type;
              const name = type?.__name || type?.name || "";
              return { name: "Vue", component: name ? "<" + name + ">" : "", provenance: "inferred" };
            }
          }
          for (const key of Object.keys(el)) {
            if (!key.startsWith("__reactFiber$") && !key.startsWith("__reactInternalInstance$")) continue;
            let fiber = el[key], source = "", depth = 0;
            const components = [];
            while (fiber && depth++ < 30) {
              const type = fiber.type || fiber.elementType;
              const name = type?.displayName || type?.name;
              if (typeof type !== "string" && typeof name === "string" &&
                  !/^(Fragment|Root|Provider|Consumer|Suspense)$/.test(name) && !components.includes(name)) {
                components.push(name);
              }
              if (!source && fiber._debugSource) {
                source = fiber._debugSource.fileName + ":" + fiber._debugSource.lineNumber;
              }
              fiber = fiber.return;
            }
            return { name: "React", component: components.slice(0, 4).reverse().map(n => "<" + n + ">").join(" "),
              sourceLocation: source, provenance: source ? "exact" : "inferred" };
          }
          return null;
        })(${JSON.stringify(note.id)})`,
        returnByValue: true,
      });
      value = response.exceptionDetails ? null : response.result?.value;
    } catch {
      continue;
    }
    if (!["React", "Vue", "Svelte"].includes(value?.name)) continue;
    updates.push([note.id, {
      name: value.name,
      component: typeof value.component === "string" ? value.component.slice(0, 200) : "",
      sourceLocation: typeof value.sourceLocation === "string" ? value.sourceLocation.slice(0, 300) : "",
      provenance: value.provenance === "exact" ? "exact" : "inferred",
    }]);
  }
  if (!updates.length) return;
  await reviewEval(tabId, `(() => {
    const updates = new Map(${JSON.stringify(updates)});
    for (const note of window.__tetherReview?.notes || []) {
      const framework = updates.get(note.id);
      if (framework && note.payload?.target?.framework?.name === "Static") note.payload.target.framework = framework;
    }
  })()`);
}

async function handleReviewStart(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  if (tabGroupId !== null) {
    try {
      await chrome.tabs.group({ tabIds: [tabId], groupId: tabGroupId });
      tabGroupTabs.add(tabId);
      activeTabId = tabId;
    } catch {}
  }

  const url = chrome.runtime.getURL("review/overlay.js");
  const resp = await fetch(url);
  const code = await resp.text();

  await reviewEval(tabId, code);

  if (params.returnToPopup) {
    await cdp(tabId, "Runtime.addBinding", { name: REVIEW_SAVED_BINDING, executionContextName: REVIEW_WORLD });
  }
  await reviewEval(tabId, `if (window.__tetherReview) {
    window.__tetherReview.onNoteSaved = ${params.returnToPopup ? `() => window.${REVIEW_SAVED_BINDING}("saved")` : "null"};
    window.__tetherReview.start();
  }`);

  return { ok: true, active: true };
}

async function handleReviewList(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");
  await enrichReviewFrameworks(tabId);
  const value = await reviewEval(tabId, "window.__tetherReview ? JSON.stringify(window.__tetherReview.getNotes()) : '[]'");

  let notes = [];
  try {
    notes = JSON.parse(value || "[]");
  } catch {}

  return {
    notes,
    pageUrl: "",
    viewport: "",
  };
}

async function handleReviewClear(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");
  await reviewEval(tabId, "window.__tetherReview ? window.__tetherReview.clear() : false");

  return { ok: true };
}
// --- Request Router ---

const nativeHandlers = {
  "browser.open": handleOpen,
  "browser.snapshot": handleSnapshot,
  "browser.click": handleClick,
  "browser.dblclick": (params) => handleClick(params, 2),
  "browser.fill": handleFill,
  "browser.type": handleType,
  "browser.press": handlePress,
  "browser.hover": handleHover,
  "browser.focus": handleFocus,
  "browser.wait": handleWait,
  "browser.scroll": handleScroll,
  "browser.close": handleClose,
  "browser.eval": handleEval,
  "browser.screenshot": handleScreenshot,
  "browser.status": handleStatus,
  "browser.tab.list": handleTabList,
  "browser.tab.switch": handleTabSwitch,
  "browser.review.start": handleReviewStart,
  "browser.review.list": handleReviewList,
  "browser.review.clear": handleReviewClear,
};

async function handleNativeMessage(msg) {
  if (!msg) return;

  if (msg.type === "bridge_status") {
    isDaemonConnected = (msg.clientCount || 0) > 0;
    return;
  }

  if (msg.id && pendingNative.has(msg.id)) {
    const p = pendingNative.get(msg.id);
    clearTimeout(p.timer);
    pendingNative.delete(msg.id);
    if (msg.error) {
      p.reject(new Error(msg.error.message || String(msg.error)));
    } else {
      p.resolve(msg.result || {});
    }
    return;
  }

  if (!msg.id) return;
  const { id, method, params = {} } = msg;
  try {
    if (!Object.hasOwn(nativeHandlers, method)) {
      sendError(id, -32601, `Method ${method} not found in extension dispatcher`);
      return;
    }
    sendResponse(id, await nativeHandlers[method](params));
  } catch (err) {
    sendError(id, -32000, err.message || String(err));
  }
}

// Start connection on service worker load

// --- Internal Message Router (for popup UI) ---

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  (async () => {
    try {
      if (["popup_network_status", "popup_network_set", "popup_ssh_connect", "popup_ssh_disconnect", "popup_reload_remote_tabs"].includes(msg.type) &&
          (sender.id !== chrome.runtime.id || sender.url !== chrome.runtime.getURL("popup.html"))) {
        throw new Error("Connection settings can only be changed from the Tether popup.");
      }
      switch (msg.type) {
        case "popup_network_status":
          if (!nativePort) connectNativeHost();
          sendResponse(await networkStatus());
          break;
        case "popup_network_set":
          if (typeof msg.enabled !== "boolean") throw new Error("Invalid network setting.");
          await setNetworkEnabled(msg.enabled, msg.sessionId);
          sendResponse({ ok: true });
          break;
        case "popup_reload_remote_tabs": {
          const tabs = await handleTabList();
          await Promise.all(tabs.tabs.filter((tab) => tab.inGroup).map((tab) => chrome.tabs.reload(Number(tab.id))));
          sendResponse({ ok: true });
          break;
        }
        case "popup_get_status": {
          if (!nativePort) {
            connectNativeHost();
          }
          const tabList = await handleTabList();
          let targetTabId = msg.currentTabId || activeTabId;
          if (!targetTabId && tabList.tabs && tabList.tabs.length > 0) {
            targetTabId = parseInt(tabList.tabs[0].id, 10);
          }

          let notes = [];
          let report = "";
          if (targetTabId) {
            try {
              await enrichReviewFrameworks(targetTabId);
              const value = await reviewEval(targetTabId, "window.__tetherReview ? {notes: window.__tetherReview.getNotes(), report: window.__tetherReview.buildSummaryText()} : {notes: [], report: ''}");
              notes = value?.notes || [];
              report = value?.report || "";
            } catch (err) {
              // Do not pair another tab's cached notes with an unavailable report.
            }
          }

          sendResponse({
            connected: nativePort !== null && isDaemonConnected,
            activeTabId: targetTabId || activeTabId,
            tabs: tabList.tabs,
            notes,
            report,
          });
          break;
        }
        case "popup_ssh_disconnect":
          sendResponse(await disconnectSSH());
          break;
        case "popup_ssh_connect":
          sendResponse(await connectSSH(msg.targetHost));
          break;
        case "popup_switch_tab": {
          await handleTabSwitch({ targetId: msg.tabId });
          sendResponse({ ok: true });
          break;
        }
        case "popup_start_review": {
          const targetTabId = msg.tabId || activeTabId;
          if (targetTabId) {
            await handleReviewStart({ tabId: targetTabId, returnToPopup: true });
          }
          sendResponse({ ok: true });
          break;
        }
        case "popup_clear_notes": {
          const targetTabId = msg.tabId || activeTabId;
          if (targetTabId) {
            await handleReviewClear({ tabId: targetTabId });
          }
          sendResponse({ ok: true });
          break;
        }
        case "popup_capture_screenshot": {
          const targetTabId = msg.tabId || activeTabId;
          const res = await handleScreenshot({
            tabId: targetTabId,
            clip: msg.clip,
            fullPage: Boolean(msg.fullPage),
            format: msg.format || "png",
          });
          const filename = `tether-shot-${Date.now()}.png`;
          let saveResult = null;
          if (nativePort && res && res.data) {
            try {
              saveResult = await nativeRequest({
                type: "system_save_screenshot",
                filename,
                base64: res.data,
              });
            } catch (err) {
              console.warn("[Tether] Native save failed:", err.message);
            }
          }
          sendResponse({
            ok: true,
            filename,
            data: res.data,
            width: res.width,
            height: res.height,
            saveResult,
          });
          break;
        }
        case "popup_start_crop": {
          const targetTabId = msg.tabId || activeTabId;
          if (!targetTabId) {
            sendResponse({ error: "No active tab" });
            break;
          }

          const url = chrome.runtime.getURL("review/overlay.js");
          const resp = await fetch(url);
          const code = await resp.text();
          await reviewEval(targetTabId, code);

          const requestId = crypto.randomUUID();
          const started = await reviewEval(targetTabId, `window.__tetherReview.startCropMode(${JSON.stringify(requestId)})`);
          if (started !== true) {
            throw new Error("Could not start area selection. Reload the page and try again.");
          }

          (async () => {
            let failure = "";
            let saved = false;
            try {
              for (let i = 0; i < 150; i++) {
                await new Promise((r) => setTimeout(r, 200));
                const cropData = await reviewEval(targetTabId, `window.__tetherReview?.cropRequestId === ${JSON.stringify(requestId)} ? window.__tetherReview.cropResult : {cancelled:true}`);
                if (!cropData) continue;
                if (cropData.cancelled) break;

                const shotRes = await handleScreenshot({ tabId: targetTabId, clip: cropData.rect });
                const filename = `tether-shot-${Date.now()}.png`;
                let saveResult = null;
                if (nativePort) {
                  try {
                    saveResult = await nativeRequest({
                      type: "system_save_screenshot",
                      filename,
                      base64: shotRes.data,
                    });
                  } catch (err) {
                    console.warn("[Tether] Native save failed:", err.message);
                  }
                }

                const storageData = await chrome.storage.local.get(["tether_screenshots"]);
                const existing = storageData.tether_screenshots || [];
                const newEntry = {
                  filename,
                  data: shotRes.data,
                  label: "Area Crop",
                  title: cropData.title || "Area Crop",
                  url: cropData.url || "",
                  dimensions: `${shotRes.width}×${shotRes.height} px`,
                  remotePath: saveResult?.mirrored ? saveResult.remotePath : "",
                  mirrored: saveResult?.mirrored || false,
                  comment: "",
                };
                await chrome.storage.local.set({ tether_screenshots: [newEntry, ...existing] });
                saved = true;
                break;
              }
            } catch (err) {
              failure = "Area capture failed: " + err.message;
              console.error("[Tether]", failure);
            } finally {
              const finished = await reviewEval(targetTabId, `window.__tetherReview?.cropRequestId === ${JSON.stringify(requestId)} && (window.__tetherReview.finishCrop?.(${JSON.stringify(failure)}), true)`).catch(() => null);
              if (saved && finished) await reopenPopup(targetTabId, "shots");
            }
          })();

          sendResponse({ ok: true });
          break;
        }
        case "popup_clear_screenshots": {
          let res = { ok: true };
          if (nativePort) {
            try {
              res = await nativeRequest({ type: "system_clear_screenshots" });
            } catch (err) {
              res = { error: err.message };
            }
          }
          sendResponse(res);
          break;
        }
        case "popup_delete_screenshot": {
          let res = { ok: true };
          if (nativePort && msg.filename) {
            try {
              res = await nativeRequest({ type: "system_delete_screenshot", filename: msg.filename });
            } catch (err) {
              res = { error: err.message };
            }
          }
          sendResponse(res);
          break;
        }
        case "popup_navigate": {
          await handleOpen({ url: msg.url, newTab: Boolean(msg.newTab) });
          sendResponse({ ok: true });
          break;
        }
        default:
          sendResponse({ error: "unknown popup message" });
      }
    } catch (err) {
      sendResponse({ error: err.message || String(err) });
    }
  })();
  return true; // Keep message channel open for async sendResponse
});
connectNativeHost();
