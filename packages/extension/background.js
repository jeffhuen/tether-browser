// Tether Browser Bridge - Manifest V3 Background Service Worker
// Provides high-performance, zero-latency browser automation via chrome.debugger
// and native tab groups inside the developer's active Chromium browser.

const NATIVE_HOST_NAME = "com.tether_browser.host";
const CDP_TIMEOUT_MS = 25000;

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
    nativePort = chrome.runtime.connectNative(NATIVE_HOST_NAME);
    nativePort.onMessage.addListener(handleNativeMessage);
    nativePort.onDisconnect.addListener(handleNativeDisconnect);
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

  // Create dedicated window with a new tab and Tether tab group
  const win = await chrome.windows.create({ focused: false, url: "about:blank" });
  const tab = win.tabs[0];
  const groupId = await chrome.tabs.group({ tabIds: [tab.id] });
  await chrome.tabGroups.update(groupId, { title: "Tether", color: "blue" });
  tabGroupId = groupId;
  tabGroupTabs = new Set([tab.id]);
  activeTabId = tab.id;
  return tabGroupId;
}

// --- CDP Connection Management ---

async function ensureAttached(tabId) {
  if (attachedTabs.has(tabId)) return;
  if (attachingTabs.has(tabId)) return attachingTabs.get(tabId);

  const attachPromise = (async () => {
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
  if (!state || state.enabledDomains.has(domain)) return;
  await cdp(tabId, `${domain}.enable`, {});
  state.enabledDomains.add(domain);
}

function cdp(tabId, method, params = {}) {
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

function resolveTargetTabId(params = {}) {
  const raw = params.targetId || params.tabId;
  if (raw !== undefined && raw !== null && raw !== "") {
    const parsed = parseInt(raw, 10);
    if (!isNaN(parsed) && parsed > 0) return parsed;
  }
  return activeTabId;
}

async function getLiveTabs() {
  let tabs = [];
  if (tabGroupId !== null) {
    try {
      tabs = await chrome.tabs.query({ groupId: tabGroupId });
    } catch {}
  }
  if (tabs.length === 0) {
    try {
      tabs = await chrome.tabs.query({ currentWindow: true });
    } catch {}
  }
  for (const t of tabs) {
    tabGroupTabs.add(t.id);
  }
  return tabs;
}

// Track user tab switching in Chrome
chrome.tabs.onActivated.addListener(async (activeInfo) => {
  activeTabId = activeInfo.tabId;
  tabGroupTabs.add(activeInfo.tabId);
});

// Track user navigation, redirects, and title updates
chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
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

chrome.tabs.onRemoved.addListener((tabId) => {
  tabGroupTabs.delete(tabId);
  attachedTabs.delete(tabId);
  elementRefsByTab.delete(tabId);
  if (activeTabId === tabId) {
    const next = tabGroupTabs.values().next();
    activeTabId = next.done ? null : next.value;
  }
});

// --- Action Implementations ---

async function handleOpen(params = {}) {
  const url = params.url || "about:blank";
  await ensureTabGroup(true);

  let targetTabId = resolveTargetTabId(params);
  if (!targetTabId || !tabGroupTabs.has(targetTabId)) {
    // Create new tab in group
    const tab = await chrome.tabs.create({ url, active: true });
    await chrome.tabs.group({ tabIds: [tab.id], groupId: tabGroupId });
    tabGroupTabs.add(tab.id);
    activeTabId = tab.id;
    targetTabId = tab.id;
  } else {
    // Navigate existing tab
    await chrome.tabs.update(targetTabId, { url, active: true });
  }

  await ensureDomain(targetTabId, "Page");
  return {
    targetId: String(targetTabId),
    tabId: targetTabId,
    url,
  };
}

async function handleSnapshot(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");
  await ensureDomain(tabId, "Accessibility");
  await ensureDomain(tabId, "DOM");

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

    const isInteractive = [
      "button", "link", "textbox", "checkbox", "radio", "combobox",
      "menuitem", "tab", "searchbox", "switch"
    ].includes(role);

    let ref = "";
    if (isInteractive || name) {
      ref = `@e${refCounter++}`;
      refMap.set(ref, {
        backendDOMNodeId: node.backendDOMNodeId,
        role,
        name,
      });
    }

    simplifiedTree.push({
      ref,
      role,
      name,
      value,
      disabled: node.disabled?.value || false,
    });
  }

  elementRefsByTab.set(tabId, refMap);

  return {
    targetId: String(tabId),
    tabId,
    nodes: simplifiedTree,
    nodeCount: simplifiedTree.length,
    rootHash: `hash-${Date.now()}`,
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
  return { x: Math.round(x), y: Math.round(y) };
}

async function handleClick(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  let x = params.x;
  let y = params.y;
  const sel = params.selector || params.ref;
  if (sel && typeof sel === "string" && sel.startsWith("@")) {
    const coords = await resolveRefCoordinates(tabId, sel);
    x = coords.x;
    y = coords.y;
  }

  if (typeof x !== "number" || typeof y !== "number") {
    throw new Error(`Click requires either a valid @ref or (x, y) coordinates; received: ${sel || "none"}`);
  }

  await ensureDomain(tabId, "Input");

  // Move mouse
  await cdp(tabId, "Input.dispatchMouseEvent", {
    type: "mouseMoved",
    x,
    y,
  });

  // Mouse down & up
  await cdp(tabId, "Input.dispatchMouseEvent", {
    type: "mousePressed",
    x,
    y,
    button: "left",
    clickCount: 1,
  });

  await cdp(tabId, "Input.dispatchMouseEvent", {
    type: "mouseReleased",
    x,
    y,
    button: "left",
    clickCount: 1,
  });

  return { tabId, clicked: { x, y } };
}

async function handleFill(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  const sel = params.selector || params.ref;
  if (sel && typeof sel === "string" && sel.startsWith("@")) {
    await handleClick({ targetId: tabId, selector: sel });
  }
  await ensureDomain(tabId, "Input");

  // Select all and clear
  await cdp(tabId, "Input.dispatchKeyEvent", {
    type: "rawKeyDown",
    commands: ["selectAll", "delete"],
  });

  // Insert text
  if (params.text) {
    await cdp(tabId, "Input.insertText", { text: params.text });
  }

  return { tabId, filled: params.text };
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
  const res = await cdp(tabId, "Page.captureScreenshot", {
    format,
    quality: params.quality || 80,
  });

  return {
    targetId: String(tabId),
    tabId,
    format,
    data: res.data,
    base64: res.data,
  };
}
async function handleStatus() {
  await ensureTabGroup(false);
  const tabs = await getLiveTabs();
  return {
    connected: true,
    tabGroupId,
    tabs: tabs.map((t) => t.id),
    targetCount: tabs.length,
    activeTargetId: String(activeTabId || (tabs.length > 0 ? tabs[0].id : "")),
    mode: "extension",
    version: "0.1.14",
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
      active: t.active || t.id === activeTabId,
    })),
    activeId: String(activeTabId || (tabs.length > 0 ? tabs[0].id : "")),
  };
}

async function handleTabSwitch(params = {}) {
  const target = params.targetId || params.tabId;
  const tabId = parseInt(target, 10);
  if (isNaN(tabId)) {
    throw new Error(`Invalid tab ID: ${target}`);
  }
  await chrome.tabs.update(tabId, { active: true });
  activeTabId = tabId;
  await ensureAttached(tabId);
  return { ok: true, activeId: String(tabId) };
}
// --- Request Router ---

async function handleNativeMessage(msg) {
  if (!msg || !msg.id) return;
  const { id, method, params = {} } = msg;

  try {
    let result;
    switch (method) {
      case "browser.open":
        result = await handleOpen(params);
        break;
      case "browser.snapshot":
        result = await handleSnapshot(params);
        break;
      case "browser.click":
        result = await handleClick(params);
        break;
      case "browser.fill":
        result = await handleFill(params);
        break;
      case "browser.eval":
        result = await handleEval(params);
        break;
      case "browser.screenshot":
        result = await handleScreenshot(params);
        break;
      case "browser.status":
        result = await handleStatus();
        break;
      case "browser.tab.list":
        result = await handleTabList();
        break;
      case "browser.tab.switch":
        result = await handleTabSwitch(params);
        break;
      default:
        sendError(id, -32601, `Method ${method} not found in extension dispatcher`);
        return;
    }
    sendResponse(id, result);
  } catch (err) {
    sendError(id, -32000, err.message || String(err));
  }
}

// Start connection on service worker load
connectNativeHost();
