// Tether Browser Bridge - Manifest V3 Background Service Worker
// Provides remote-to-local browser automation via chrome.debugger
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
let activeNotesCache = [];
let isDaemonConnected = false;
const pendingNative = new Map();
let nativeReqSeq = 0;

function nativeRequest(msg) {
  return new Promise((resolve, reject) => {
    if (!nativePort) {
      reject(new Error("Native host not connected"));
      return;
    }
    const id = `nr_${Date.now()}_${nativeReqSeq++}`;
    pendingNative.set(id, { resolve, reject });
    nativePort.postMessage({ ...msg, id });
    setTimeout(() => {
      const p = pendingNative.get(id);
      if (p) {
        pendingNative.delete(id);
        p.reject(new Error("Native request timed out"));
      }
    }, 15000);
  });
}
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

  // Use chrome.windows.getAll to find user's visible normal window
  // (In service workers, currentWindow:true returns [] when Chrome is unfocused)
  try {
    const windows = await chrome.windows.getAll({ populate: true, windowTypes: ["normal"] });
    if (windows && windows.length > 0) {
      const targetWin = windows.find((w) => w.focused) || windows[0];
      const tab = await chrome.tabs.create({ windowId: targetWin.id, url: "about:blank", active: true });
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
  const win = await chrome.windows.create({ focused: true, url: "about:blank" });
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
  const url = params.url || "about:blank";
  await ensureTabGroup(true);

  let targetTabId = resolveTargetTabId(params);
  if (params.newTab || !targetTabId || !tabGroupTabs.has(targetTabId)) {
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
  const sel = params.selector || params.ref;
  if (sel && typeof sel === "string") {
    const coords = await resolveCoordinates(tabId, sel);
    if (coords) {
      x = coords.x;
      y = coords.y;
    }
  }

  if (typeof x !== "number" || typeof y !== "number") {
    throw new Error(`Click requires either a valid @ref, selector, or (x, y) coordinates; received: ${sel || "none"}`);
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
    clickCount,
  });

  await cdp(tabId, "Input.dispatchMouseEvent", {
    type: "mouseReleased",
    x,
    y,
    button: "left",
    clickCount,
  });

  return { tabId, clicked: { x, y } };
}

async function handleDblClick(params = {}) {
  return await handleClick(params, 2);
}

async function handleFill(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  const sel = params.selector || params.ref;
  if (sel && typeof sel === "string") {
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
  if (sel && typeof sel === "string") {
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

  await handleClick({ targetId: tabId, selector: sel });
  return { tabId, focused: sel };
}

async function handlePress(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");
  if (!params.key) throw new Error("Press requires a key");

  await ensureDomain(tabId, "Input");

  const keyMap = {
    Enter: { key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, text: "\r" },
    Tab: { key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 },
    Escape: { key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 },
    Backspace: { key: "Backspace", code: "Backspace", windowsVirtualKeyCode: 8 },
    ArrowDown: { key: "ArrowDown", code: "ArrowDown", windowsVirtualKeyCode: 40 },
    ArrowUp: { key: "ArrowUp", code: "ArrowUp", windowsVirtualKeyCode: 38 },
    ArrowLeft: { key: "ArrowLeft", code: "ArrowLeft", windowsVirtualKeyCode: 37 },
    ArrowRight: { key: "ArrowRight", code: "ArrowRight", windowsVirtualKeyCode: 39 },
    PageDown: { key: "PageDown", code: "PageDown", windowsVirtualKeyCode: 34 },
    PageUp: { key: "PageUp", code: "PageUp", windowsVirtualKeyCode: 33 },
  };

  const keyDef = keyMap[params.key] || {
    key: params.key,
    code: params.key,
    windowsVirtualKeyCode: params.key.charCodeAt(0) || 0,
  };

  await cdp(tabId, "Input.dispatchKeyEvent", {
    type: "rawKeyDown",
    key: keyDef.key,
    code: keyDef.code,
    windowsVirtualKeyCode: keyDef.windowsVirtualKeyCode,
    text: keyDef.text,
    unmodifiedText: keyDef.text,
  });

  await cdp(tabId, "Input.dispatchKeyEvent", {
    type: "keyUp",
    key: keyDef.key,
    code: keyDef.code,
    windowsVirtualKeyCode: keyDef.windowsVirtualKeyCode,
  });

  return { tabId, pressed: params.key };
}

async function handleWait(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");

  if (params.durationMs && params.durationMs > 0) {
    await new Promise((resolve) => setTimeout(resolve, params.durationMs));
    return { tabId, waitedMs: params.durationMs };
  }

  if (params.selector) {
    const timeoutMs = params.timeoutMs || 30000;
    const start = Date.now();
    await ensureDomain(tabId, "DOM");
    while (Date.now() - start < timeoutMs) {
      try {
        const doc = await cdp(tabId, "DOM.getDocument");
        const res = await cdp(tabId, "DOM.querySelector", {
          nodeId: doc.root.nodeId,
          selector: params.selector,
        });
        if (res && res.nodeId && res.nodeId > 0) {
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
  if (params.all) {
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
    connected: true,
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

  await ensureDomain(tabId, "Runtime");
  await ensureDomain(tabId, "Page");
  const url = chrome.runtime.getURL("review/overlay.js");
  const resp = await fetch(url);
  const code = await resp.text();

  await cdp(tabId, "Runtime.evaluate", {
    expression: code,
  });

  await cdp(tabId, "Runtime.evaluate", {
    expression: "window.__tetherReview ? window.__tetherReview.start() : false",
  });

  return { ok: true, active: true };
}

async function handleReviewList(params = {}) {
  const tabId = resolveTargetTabId(params);
  if (!tabId) throw new Error("No active tab in Tether group");
  await ensureDomain(tabId, "Runtime");

  const res = await cdp(tabId, "Runtime.evaluate", {
    expression: "window.__tetherReview ? JSON.stringify(window.__tetherReview.getNotes()) : '[]'",
    returnByValue: true,
  });

  let notes = [];
  try {
    notes = JSON.parse(res.result?.value || "[]");
    activeNotesCache = notes;
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
  await ensureDomain(tabId, "Runtime");

  await cdp(tabId, "Runtime.evaluate", {
    expression: "window.__tetherReview ? (window.__tetherReview.clear ? window.__tetherReview.clear() : (window.__tetherReview.clearNotes ? window.__tetherReview.clearNotes() : false)) : false",
  });

  activeNotesCache = [];
  return { ok: true };
}
// --- Request Router ---

async function handleNativeMessage(msg) {
  if (!msg) return;

  if (msg.type === "bridge_status") {
    isDaemonConnected = (msg.clientCount || 0) > 0;
    return;
  }

  if (msg.id && pendingNative.has(msg.id)) {
    const p = pendingNative.get(msg.id);
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
      case "browser.dblclick":
        result = await handleDblClick(params);
        break;
      case "browser.fill":
        result = await handleFill(params);
        break;
      case "browser.type":
        result = await handleType(params);
        break;
      case "browser.press":
        result = await handlePress(params);
        break;
      case "browser.hover":
        result = await handleHover(params);
        break;
      case "browser.focus":
        result = await handleFocus(params);
        break;
      case "browser.wait":
        result = await handleWait(params);
        break;
      case "browser.close":
        result = await handleClose(params);
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
      case "browser.review.start":
        result = await handleReviewStart(params);
        break;
      case "browser.review.list":
        result = await handleReviewList(params);
        break;
      case "browser.review.clear":
        result = await handleReviewClear(params);
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

// --- Internal Message Router (for popup UI) ---

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  (async () => {
    try {
      switch (msg.type) {
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
          if (targetTabId) {
            try {
              const res = await handleReviewList({ tabId: targetTabId });
              notes = res.notes || [];
            } catch (err) {
              notes = activeNotesCache || [];
            }
          }

          sendResponse({
            connected: nativePort !== null && isDaemonConnected,
            activeTabId: targetTabId || activeTabId,
            tabs: tabList.tabs,
            notes,
          });
          break;
        }
        case "popup_ssh_disconnect": {
          try {
            const res = await nativeRequest({ type: "system_ssh_disconnect" });
            isDaemonConnected = false;
            sendResponse(res);
          } catch (err) {
            sendResponse({ error: err.message || String(err) });
          }
          break;
        }
        case "popup_ssh_connect": {
          try {
            const res = await nativeRequest({ type: "system_ssh_connect", targetHost: msg.targetHost });
            sendResponse(res);
          } catch (err) {
            sendResponse({ error: err.message || String(err) });
          }
          break;
        }
        case "popup_switch_tab": {
          await handleTabSwitch({ targetId: msg.tabId });
          sendResponse({ ok: true });
          break;
        }
        case "popup_start_review": {
          const targetTabId = msg.tabId || activeTabId;
          if (targetTabId) {
            await handleReviewStart({ tabId: targetTabId });
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
          await ensureDomain(targetTabId, "Runtime");
          await ensureDomain(targetTabId, "Page");

          const url = chrome.runtime.getURL("review/overlay.js");
          const resp = await fetch(url);
          const code = await resp.text();
          await cdp(targetTabId, "Runtime.evaluate", { expression: code });

          const requestId = crypto.randomUUID();
          const started = await cdp(targetTabId, "Runtime.evaluate", {
            expression: `window.__tetherReview.startCropMode(${JSON.stringify(requestId)})`,
            returnByValue: true,
          });
          if (started.exceptionDetails || started.result?.value !== true) {
            throw new Error("Could not start area selection. Reload the page and try again.");
          }

          (async () => {
            let failure = "";
            try {
              for (let i = 0; i < 150; i++) {
                await new Promise((r) => setTimeout(r, 200));
                const check = await cdp(targetTabId, "Runtime.evaluate", {
                  expression: `window.__tetherReview?.cropRequestId === ${JSON.stringify(requestId)} ? window.__tetherReview.cropResult : {cancelled:true}`,
                  returnByValue: true,
                });
                if (check.exceptionDetails) throw new Error("Could not read the selected area");
                const cropData = check.result?.value;
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
                  id: `shot-${Date.now()}`,
                  filename,
                  data: shotRes.data,
                  label: "Area Crop",
                  title: cropData.title || "Area Crop",
                  url: cropData.url || "",
                  dimensions: `${shotRes.width}×${shotRes.height} px`,
                  remotePath: saveResult?.remotePath || `/tmp/tether-screenshots/${filename}`,
                  localPath: saveResult?.localPath || "",
                  mirrored: saveResult?.mirrored || false,
                  comment: "",
                  createdAt: new Date().toISOString(),
                };
                await chrome.storage.local.set({ tether_screenshots: [newEntry, ...existing] });
                break;
              }
            } catch (err) {
              failure = "Area capture failed: " + err.message;
              console.error("[Tether]", failure);
            } finally {
              await cdp(targetTabId, "Runtime.evaluate", {
                expression: `if (window.__tetherReview?.cropRequestId === ${JSON.stringify(requestId)}) window.__tetherReview.finishCrop?.(${JSON.stringify(failure)})`,
              }).catch(() => {});
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
