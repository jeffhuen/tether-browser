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
let proxyCredentials = null;
let authRegistered = false;
const authAttempts = new Set();
// SSH output is untrusted, even when it names Tailscale's own sign-in page.
const SIGN_IN_URL = /^https:\/\/login\.tailscale\.com\/a\/[0-9A-Za-z]+(?![\s\S])/;

function signInURL(value) {
  return value.state === "connecting" && value.authProvider !== "NetBird" && typeof value.signInUrl === "string" &&
    value.host && value.sessionId && SIGN_IN_URL.test(value.signInUrl) ? value.signInUrl : "";
}

export function initializeNetwork(request) {
  nativeRequest = request;
  proxyCredentials = null;
  if (!authRegistered) {
    // Register during worker startup, before any storage/native await.
    chrome.webRequest.onAuthRequired.addListener((details, callback) => {
      void proxyAuth(details, callback).catch(() => callback({ cancel: true }));
    }, { urls: ["<all_urls>"] }, ["asyncBlocking"]);
    const completed = ({ requestId }) => authAttempts.delete(requestId);
    chrome.webRequest.onCompleted.addListener(completed, { urls: ["<all_urls>"] });
    chrome.webRequest.onErrorOccurred.addListener(completed, { urls: ["<all_urls>"] });
    authRegistered = true;
  }
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

async function migrateLegacyRoute(current) {
  if (!route) return;
  const expected = route;
  const settings = await proxySetting("get", { incognito: false });
  if (current !== revision || route !== expected) return;
  const proxy = settings.value?.rules?.singleProxy;
  if (settings.levelOfControl === "controlled_by_this_extension" &&
      settings.value.mode === "fixed_servers" && proxy?.scheme === "socks5" &&
      proxy.host === "127.0.0.1" && proxy.port === route.port) {
    // Keep existing ON consent, but never keep an unauthenticated SOCKS route.
    proxy.scheme = "http";
    settings.value.rules.bypassList = ["<-loopback>"];
    await proxySetting("set", { scope: "regular_only", value: settings.value });
  }
}

function validSSH(value) {
  if (!value || !["connecting", "connected", "disconnected"].includes(value.state)) {
    throw new Error("Update the local tether binary to use remote browsing.");
  }
  if (value.proxyAuthSupported !== true) {
    throw new Error("Update the local tether binary to use authenticated remote browsing.");
  }
  if (value.state === "connected" && (!value.host || !value.sessionId || !Number.isInteger(value.proxyPort) || value.proxyPort < 1024 || value.proxyPort > 65535)) {
    throw new Error("The local helper returned an invalid SSH connection.");
  }
  if (value.state === "connected" &&
      (typeof value.proxyToken !== "string" || !/^[0-9a-f]{64}(?![\s\S])/.test(value.proxyToken) ||
       typeof value.proxyRealm !== "string" || !/^tether-[0-9a-f]{32}(?![\s\S])/.test(value.proxyRealm))) {
    throw new Error("Update the local tether binary to use authenticated remote browsing.");
  }
  return value;
}

async function requestSSHStatus() {
  const current = revision;
  const value = await nativeRequest({ type: "system_ssh_status" }, 2500);
  if (current !== revision) throw new Error("The SSH connection changed. Check the host and try again.");
  if (value?.proxyAuthSupported !== true) {
    // An old helper may already have an active unauthenticated forward.
    await nativeRequest({ type: "system_ssh_disconnect" }, 2500);
    await migrateLegacyRoute(current);
    throw new Error("Update the local tether binary to use authenticated remote browsing.");
  }
  return validSSH(value);
}

function acceptSSH(value, current) {
  if (current !== revision) throw new Error("The SSH connection changed. Check the host and try again.");
  const { proxyToken, proxyRealm, ...status } = value;
  ssh = status;
  proxyCredentials = value.state === "connected" ? { token: proxyToken, realm: proxyRealm, revision: current } : null;
  return ssh;
}

async function proxyAuth(details, callback) {
  // Ordinary origin authentication and other profiles/proxies are not ours.
  if (details.isProxy !== true || details.incognito || details.challenger?.host !== "127.0.0.1") {
    callback({});
    return;
  }
  const current = revision;
  const previousRoute = route;
  await ready;
  const endpoint = route || previousRoute;
  if (details.challenger.port !== endpoint?.port) {
    callback(authAttempts.has(details.requestId) ? { cancel: true } : {});
    return;
  }
  const settings = await proxySetting("get", { incognito: false });
  if (current !== revision || !applied(settings) || ssh.state !== "connected" ||
      ssh.host !== route?.host || ssh.proxyPort !== route?.port ||
      !proxyCredentials || proxyCredentials.revision !== revision ||
      details.realm !== proxyCredentials.realm || details.scheme?.toLowerCase() !== "basic" ||
      authAttempts.has(details.requestId)) {
    callback({ cancel: true });
    return;
  }
  // One attempt per request, even across disable/reconnect/revision changes.
  authAttempts.add(details.requestId);
  callback({ authCredentials: { username: "tether", password: proxyCredentials.token } });
}

export function networkDisconnected() {
  revision++;
  proxyCredentials = null;
  ssh = { state: "disconnected", host: route?.host || "", error: "Local helper unavailable. Reconnect, or turn remote browsing off." };
}

async function refreshSSH() {
  if (polling) return polling;
  const current = revision;
  polling = (async () => {
    try {
      const result = await requestSSHStatus();
      if (current === revision) acceptSSH(result, current);
    } catch (error) {
      if (current === revision) {
        proxyCredentials = null;
        ssh = { state: "disconnected", host: route?.host || "", error:
          error.message.startsWith("unknown system message type:")
            ? "Update the local tether binary to use remote browsing."
            : error.message };
      }
    }
  })().finally(() => { polling = null; });
  return polling;
}

function mutate(fn) {
  const result = mutations.then(async () => {
    await ready;
    revision++;
    proxyCredentials = null;
    return fn();
  });
  mutations = result.catch(() => {});
  return result;
}

function applied(settings) {
  const rules = settings.value?.rules;
  const proxy = rules?.singleProxy;
  return Boolean(route && settings.levelOfControl === "controlled_by_this_extension" &&
    settings.value.mode === "fixed_servers" && proxy?.scheme === "http" &&
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
  const signInUrl = pollSSH ? signInURL(ssh) : "";
  const signInNeeded = Boolean(signInUrl || (pollSSH && ssh.authMessage && ssh.state === "connecting") || ssh.signInRequired);
  const badge = state === "off" && !signInNeeded ? "" : state === "on" ? "R" : "!";
  if (badge !== lastBadge) {
    lastBadge = badge;
    void chrome.action.setBadgeText({ text: badge });
    void chrome.action.setBadgeBackgroundColor({ color: state === "on" ? "#0284c7" : "#b45309" });
  }
  return {
    enabled, state, host: route?.host || ssh.host || "", ssh, signInUrl,
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
    const current = revision;
    await requestSSHStatus();
    await migrateLegacyRoute(current);
    if (current !== revision) throw new Error("The SSH connection changed. Check the host and try again.");
    return acceptSSH(validSSH(await nativeRequest({ type: "system_ssh_connect", targetHost: host, proxyPort: route?.port || 0 }, 2500)), current);
  });
}

export function disconnectSSH() {
  return mutate(async () => {
    const current = revision;
    return acceptSSH(validSSH(await nativeRequest({ type: "system_ssh_disconnect" }, 2500)), current);
  });
}

async function clearNetwork() {
  // Explicit consent only: SSH loss must leave the requested route fail-closed.
  await proxySetting("clear", { scope: "regular_only" });
  await chrome.storage.local.remove(ROUTE_KEY);
  route = null;
}

export function openSignIn(sessionId, turnOff) {
  return mutate(async () => {
    const currentRevision = revision;
    const verify = async () => {
      const current = await requestSSHStatus();
      if (revision !== currentRevision || !sessionId || current.sessionId !== sessionId) {
        throw new Error("The SSH connection changed. Check the host and try sign-in again.");
      }
      const netBirdAuth = current.state === "connecting" && current.authProvider === "NetBird" &&
        typeof current.authMessage === "string" && current.authMessage && current.host && current.sessionId;
      if (!signInURL(current) && !netBirdAuth) throw new Error("No valid sign-in request is pending. Reconnect and try again.");
      acceptSSH(current, currentRevision);
      return current;
    };
    await verify();
    const settings = await proxySetting("get", { incognito: false });
    if (route || settings.levelOfControl === "controlled_by_this_extension") {
      if (turnOff !== true) throw new Error("Remote browsing is now on. Review the sign-in action and try again.");
      await clearNetwork();
    }
    // Routing changes take time; verify the session and latest challenge again.
    const current = await verify();
    if (current.authProvider === "NetBird") {
      return { ok: true, authProvider: "NetBird", authMessage: current.authMessage };
    }
    const tab = await chrome.tabs.create({ url: signInURL(current) });
    return { ok: true, tabId: tab.id };
  });
}

export function setNetworkEnabled(enabled, sessionId) {
  return mutate(async () => {
    if (!enabled) {
      // OFF must work even when SSH and native messaging are both down.
      await clearNetwork();
      return;
    }
    const currentRevision = revision;
    const current = await requestSSHStatus();
    if (current.state !== "connected") throw new Error("Connect to an SSH host before enabling remote browsing.");
    if (currentRevision !== revision) throw new Error("The SSH connection changed. Check the host and enable remote browsing again.");
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
    acceptSSH(current, currentRevision);
    await proxySetting("set", {
      scope: "regular_only",
      value: { mode: "fixed_servers", rules: {
        singleProxy: { scheme: "http", host: "127.0.0.1", port: route.port },
        bypassList: ["<-loopback>"],
      } },
    });
    if (!applied(await proxySetting("get", { incognito: false }))) {
      throw new Error("Chrome did not apply the remote proxy. Turn the switch off and check other proxy extensions or browser policy.");
    }
  });
}
