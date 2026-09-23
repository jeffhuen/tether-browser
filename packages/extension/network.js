// Only the local popup may change these settings. Remote browser RPC has no
// route-management methods. SSH loss must never clear the profile's proxy.
const ROUTE_KEY = "tether_remote_network";
let nativeRequest;
let ready;
let route = null;
let ssh = { state: "disconnected", host: "" };
let revision = 0;
let mutations = Promise.resolve();
let polling = null;
let lastBadge = "";

export function initializeNetwork(request) {
  nativeRequest = request;
  ready = chrome.storage.local.get(ROUTE_KEY).then((data) => {
    route = data[ROUTE_KEY] || null;
  });
}

function proxySetting(method, details) {
  return new Promise((resolve, reject) => {
    chrome.proxy.settings[method](details, (result) => {
      const error = chrome.runtime.lastError;
      if (error) reject(new Error(error.message));
      else resolve(result);
    });
  });
}

function validSSH(value) {
  if (!value || !["connecting", "connected", "disconnected"].includes(value.state)) {
    throw new Error("Update the local tether binary to use remote browsing.");
  }
  if (value.state === "connected" && (!value.host || !value.sessionId || !Number.isInteger(value.proxyPort) || value.proxyPort < 1024 || value.proxyPort > 65535)) {
    throw new Error("The local helper returned an invalid SSH connection.");
  }
  return value;
}

export function networkDisconnected() {
  revision++;
  ssh = { state: "disconnected", host: route?.host || "", error: "Local helper unavailable. Reconnect, or turn remote browsing off." };
}

async function refreshSSH() {
  if (polling) return polling;
  const current = revision;
  polling = (async () => {
    try {
      const result = validSSH(await nativeRequest({ type: "system_ssh_status" }, 2500));
      if (current === revision) ssh = result;
    } catch (error) {
      if (current === revision) ssh = { state: "disconnected", host: route?.host || "", error:
        error.message.startsWith("unknown system message type:")
          ? "Update the local tether binary to use remote browsing."
          : error.message };
    }
  })().finally(() => { polling = null; });
  return polling;
}

function mutate(fn) {
  const result = mutations.then(async () => {
    await ready;
    revision++;
    return fn();
  });
  mutations = result.catch(() => {});
  return result;
}

function applied(settings) {
  const rules = settings.value?.rules;
  const proxy = rules?.singleProxy;
  return Boolean(route && settings.levelOfControl === "controlled_by_this_extension" &&
    settings.value.mode === "fixed_servers" && proxy?.scheme === "socks5" &&
    proxy.host === "127.0.0.1" && proxy.port === route.port &&
    rules.bypassList?.length === 1 && rules.bypassList[0] === "<-loopback>");
}

export async function networkStatus(pollSSH = true) {
  await ready;
  const [, settings] = await Promise.all([pollSSH ? refreshSSH() : null, proxySetting("get", { incognito: false })]);
  const enabled = Boolean(route || settings.levelOfControl === "controlled_by_this_extension");
  const matches = applied(settings);
  const connected = ssh.state === "connected" && ssh.host === route?.host && ssh.proxyPort === route?.port;
  const state = !enabled ? "off" : !matches ? "conflict" : connected ? "on" : "unavailable";
  const badge = state === "off" ? "" : state === "on" ? "R" : "!";
  if (badge !== lastBadge) {
    lastBadge = badge;
    void chrome.action.setBadgeText({ text: badge });
    void chrome.action.setBadgeBackgroundColor({ color: state === "on" ? "#0284c7" : "#b45309" });
  }
  return {
    enabled, state, host: route?.host || ssh.host || "", ssh,
    canEnable: !enabled && ssh.state === "connected" &&
      ["controllable_by_this_extension", "controlled_by_this_extension"].includes(settings.levelOfControl),
    message: state === "conflict"
      ? "Remote proxy is not active. Another extension or browser policy may control it. Turn this switch off before trying again."
      : !["controllable_by_this_extension", "controlled_by_this_extension"].includes(settings.levelOfControl)
        ? "Another extension or browser policy controls this profile's proxy."
        : ssh.error || "",
  };
}

export function connectSSH(host) {
  return mutate(async () => {
    host = String(host || "").trim();
    if (route && route.host !== host) throw new Error("Turn remote browsing off before switching SSH hosts.");
    ssh = validSSH(await nativeRequest({ type: "system_ssh_connect", targetHost: host, proxyPort: route?.port || 0 }, 2500));
    return ssh;
  });
}

export function disconnectSSH() {
  return mutate(async () => {
    ssh = validSSH(await nativeRequest({ type: "system_ssh_disconnect" }, 2500));
    return ssh;
  });
}

export function setNetworkEnabled(enabled, sessionId) {
  return mutate(async () => {
    if (!enabled) {
      // Clearing our override restores the profile's previous system/policy
      // proxy. This must work even when SSH and native messaging are both down.
      await proxySetting("clear", { scope: "regular_only" });
      await chrome.storage.local.remove(ROUTE_KEY);
      route = null;
      return;
    }
    const current = validSSH(await nativeRequest({ type: "system_ssh_status" }, 2500));
    if (current.state !== "connected") throw new Error("Connect to an SSH host before enabling remote browsing.");
    if (!sessionId || sessionId !== current.sessionId) throw new Error("The SSH connection changed. Check the host and enable remote browsing again.");
    if (route && (route.host !== current.host || route.port !== current.proxyPort)) {
      throw new Error("Turn remote browsing off before changing the remote route.");
    }
    const settings = await proxySetting("get", { incognito: false });
    if (!["controllable_by_this_extension", "controlled_by_this_extension"].includes(settings.levelOfControl)) {
      throw new Error("Another extension or browser policy controls this profile's proxy.");
    }
    const next = { host: current.host, port: current.proxyPort };
    // Persist before applying: a worker restart must not forget an active proxy.
    await chrome.storage.local.set({ [ROUTE_KEY]: next });
    route = next;
    ssh = current;
    await proxySetting("set", {
      scope: "regular_only",
      value: { mode: "fixed_servers", rules: {
        singleProxy: { scheme: "socks5", host: "127.0.0.1", port: route.port },
        bypassList: ["<-loopback>"],
      } },
    });
    if (!applied(await proxySetting("get", { incognito: false }))) {
      throw new Error("Chrome did not apply the remote proxy. Turn the switch off and check other proxy extensions or browser policy.");
    }
  });
}
