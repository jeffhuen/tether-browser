// Tether Browser Bridge - Popup UI Controller
// Provides a clean, FireShot-style developer panel directly inside Chrome.

document.addEventListener("DOMContentLoaded", async () => {
  const connectionToggle = document.getElementById("connection-toggle");
  const connectionAnnouncement = document.getElementById("connection-announcement");
  const statusText = document.getElementById("status-text");
  const sshStatus = document.getElementById("ssh-status");
  const remoteStatus = document.getElementById("remote-status");
  const btnInspect = document.getElementById("btn-inspect");
  const btnCopyNotes = document.getElementById("btn-copy-notes");
  const btnClearNotes = document.getElementById("btn-clear-notes");
  const tabBtnNotes = document.getElementById("tab-btn-notes");
  const tabBtnShots = document.getElementById("tab-btn-shots");
  const panelNotes = document.getElementById("panel-notes");
  const panelShots = document.getElementById("panel-shots");
  const btnCaptureArea = document.getElementById("btn-capture-area");
  const btnCaptureViewport = document.getElementById("btn-capture-viewport");
  const btnCaptureFull = document.getElementById("btn-capture-full");
  const btnCopyShots = document.getElementById("btn-copy-shots");
  const btnClearShots = document.getElementById("btn-clear-shots");
  const shotsList = document.getElementById("shots-list");
  const shotsBadge = document.getElementById("shots-badge");
  const lightboxModal = document.getElementById("shot-lightbox");
  const lightboxClose = document.getElementById("lightbox-close");
  const lightboxImg = document.getElementById("lightbox-img");
  const lightboxTitle = document.getElementById("lightbox-title");
  const btnRefreshTabs = document.getElementById("btn-refresh-tabs");
  const notesList = document.getElementById("notes-list");
  const tabsList = document.getElementById("tabs-list");
  const navForm = document.getElementById("nav-form");
  const navInput = document.getElementById("nav-input");
  const activeTabTitleEl = document.getElementById("active-tab-title");
  const extVersionEl = document.getElementById("ext-version");
  const btnReloadExt = document.getElementById("btn-reload-ext");

  extVersionEl.textContent = "v" + chrome.runtime.getManifest().version;
  btnReloadExt.addEventListener("click", () => chrome.runtime.reload());

  function showToast(text, duration = 2000) {
    let toast = document.getElementById("update-toast");
    if (!toast) {
      toast = document.createElement("div");
      toast.id = "update-toast";
      toast.setAttribute("role", "status");
      toast.className = "update-toast";
      document.body.appendChild(toast);
    }
    toast.textContent = text;
    toast.style.display = "flex";
    clearTimeout(toast._timer);
    toast._timer = setTimeout(() => {
      toast.style.display = "none";
    }, duration);
  }

  let currentNotes = [];
  let currentReport = "";
  let currentShots = [];

  async function loadScreenshots() {
    try {
      const data = await chrome.storage.local.get(["tether_screenshots"]);
      currentShots = data.tether_screenshots || [];
      updateShotsUI(currentShots);
    } catch {}
  }

  async function saveScreenshots(shots) {
    currentShots = shots;
    await chrome.storage.local.set({ tether_screenshots: shots });
    updateShotsUI(currentShots);
  }

  function updateShotsUI(shots) {
    shotsBadge.textContent = shots.length;
    btnCopyShots.disabled = shots.length === 0;
    btnClearShots.disabled = shots.length === 0;
    if (!shots || shots.length === 0) {
      shotsList.innerHTML = '<div class="empty-state">No screenshots captured yet. Click <b>Crop Area</b> or <b>Viewport</b> above.</div>';
      return;
    }
    shotsList.innerHTML = "";
    shots.forEach((shot, idx) => {
      const card = document.createElement("div");
      card.className = "shot-card";
      const label = shot.label || shot.title || "Screenshot";
      const remotePath = shot.mirrored && shot.remotePath ? shot.remotePath : "Local only";
      card.innerHTML = `
        <div class="shot-top-row">
          <button type="button" class="shot-thumb-wrapper" aria-label="Enlarge screenshot" title="Click to enlarge">
            <img class="shot-thumb" src="data:image/png;base64,${shot.data}" alt="${escapeHTML(label)}">
          </button>
          <div class="shot-details">
            <div class="shot-header">
              <span class="shot-title">${escapeHTML(label)}</span>
              <button type="button" class="btn-del-shot" aria-label="Delete screenshot" title="Delete screenshot">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <polyline points="3 6 5 6 21 6"/>
                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                </svg>
              </button>
            </div>
            <div class="shot-meta">${escapeHTML(shot.dimensions || "")} · ${escapeHTML(URL.parse(shot.url)?.pathname || shot.url || "")}</div>
            <div class="shot-path-chip" title="${escapeHTML(remotePath)}">${escapeHTML(remotePath)}</div>
          </div>
        </div>
        <textarea class="shot-comment" aria-label="Screenshot feedback" placeholder="Add feedback for your agent..." rows="2">${escapeHTML(shot.comment || "")}</textarea>
      `;
      const thumb = card.querySelector(".shot-thumb-wrapper");
      thumb.addEventListener("click", () => {
        openLightbox(shot.data, label, shot.dimensions);
      });
      const delBtn = card.querySelector(".btn-del-shot");
      delBtn.addEventListener("click", async () => {
        const updated = currentShots.filter((_, i) => i !== idx);
        await sendMessage({ type: "popup_delete_screenshot", filename: shot.filename });
        saveScreenshots(updated);
      });
      const textarea = card.querySelector(".shot-comment");
      textarea.addEventListener("input", () => {
        shot.comment = textarea.value;
        chrome.storage.local.set({ tether_screenshots: currentShots });
      });
      shotsList.appendChild(card);
    });
  }

  function openLightbox(base64Data, title, meta) {
    lightboxImg.src = `data:image/png;base64,${base64Data}`;
    lightboxImg.alt = title;
    lightboxTitle.textContent = meta ? `${title} (${meta})` : title;
    lightboxModal.showModal();
  }

  lightboxClose.addEventListener("click", () => lightboxModal.close());
  lightboxModal.addEventListener("click", (e) => {
    if (e.target === lightboxModal) lightboxModal.close();
  });

  const workspaceTabs = [tabBtnNotes, tabBtnShots];
  let selectedTab = null;
  function selectTab(tab) {
    if (selectedTab !== tab) {
      selectedTab = tab;
      chrome.storage.local.set({ tether_popup_tab: tab === tabBtnShots ? "shots" : "notes" })
        .catch((err) => console.warn("[Tether] Could not save popup tab:", err.message));
    }
    workspaceTabs.forEach((button) => {
      const selected = button === tab;
      button.classList.toggle("active", selected);
      button.setAttribute("aria-selected", String(selected));
      button.tabIndex = selected ? 0 : -1;
    });
    panelNotes.hidden = tab !== tabBtnNotes;
    panelShots.hidden = tab !== tabBtnShots;
    if (tab === tabBtnShots) loadScreenshots();
  }
  workspaceTabs.forEach((tab, index) => {
    tab.addEventListener("click", () => selectTab(tab));
    tab.addEventListener("keydown", (event) => {
      let next;
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") next = workspaceTabs[1 - index];
      else if (event.key === "Home") next = tabBtnNotes;
      else if (event.key === "End") next = tabBtnShots;
      else return;
      event.preventDefault();
      selectTab(next);
      next.focus();
    });
  });
  // 1. Load Status & Initial Data
  async function refresh() {
    void refreshNetwork();
    try {
      const currentTab = await getActiveTab();
      const currentTabId = currentTab?.id || null;

      const status = await sendMessage({ type: "popup_get_status", currentTabId });
      const tabs = status.tabs || [];
      const activeTab = tabs.find((t) => t.id === String(status.activeTabId) || t.active) ||
                        (currentTab ? { id: String(currentTab.id), title: currentTab.title, url: currentTab.url } : null);
      updateStatusUI(status);
      updateTabsUI(tabs, status.activeTabId);
      currentReport = status.report || "";
      updateNotesUI(status.notes || [], activeTab);
    } catch (err) {
      console.warn("Failed to load status:", err);
      updateStatusUI({ connected: false });
    }
  }

  const connectSection = document.getElementById("connect-section");
  const connectHostInput = document.getElementById("connect-host-input");
  const btnDoConnect = document.getElementById("btn-do-connect");
  const recentHostsWrapper = document.getElementById("recent-hosts-wrapper");
  const recentHostsList = document.getElementById("recent-hosts-list");
  const networkToggle = document.getElementById("network-toggle");
  const networkReadiness = document.getElementById("network-ready");
  const networkValue = document.getElementById("network-value");
  const networkConfirm = document.getElementById("network-confirm");
  const networkEnable = document.getElementById("network-enable");
  const networkError = document.getElementById("network-error");
  const networkErrorDetails = document.getElementById("network-error-details");
  const networkErrorDetail = document.getElementById("network-error-detail");
  let network = { enabled: false, state: "checking", canEnable: false, ssh: { state: "checking" } };
  let bridgeConnected = false;
  let connectionPanelOpen = null;
  let networkBusy = false;
  let networkRevision = 0;
  let confirmedSession = "";
  let actionError = "";
  let actionErrorDetails = "";
  let statusError = "";

  async function refreshNetwork() {
    const current = networkRevision;
    try {
      const result = await sendMessage({ type: "popup_network_status" });
      if (current !== networkRevision || networkBusy) return;
      statusError = "";
      network = result;
      renderConnection();
    } catch (error) {
      if (current !== networkRevision || networkBusy) return;
      statusError = error.message;
      renderConnection();
    }
  }

  async function networkAction(action, failureMessage) {
    const current = ++networkRevision;
    networkBusy = true;
    actionError = "";
    actionErrorDetails = "";
    renderConnection();
    try {
      await action();
    } catch (error) {
      if (current === networkRevision) {
        actionError = failureMessage;
        actionErrorDetails = error.message;
      }
    } finally {
      if (current === networkRevision) {
        networkBusy = false;
        networkRevision++;
        renderConnection();
        await refreshNetwork();
      }
    }
  }

  networkToggle.addEventListener("change", () => {
    networkToggle.checked = network.enabled;
    if (network.enabled) {
      networkConfirm.hidden = true;
      void networkAction(async () => {
        await sendMessage({ type: "popup_network_set", enabled: false });
        network = { ...network, enabled: false, state: "off" };
        document.getElementById("network-reload").hidden = false;
      }, "Could not turn off remote browsing.");
    } else {
      confirmedSession = network.ssh.sessionId;
      document.getElementById("network-confirm-host").textContent = network.ssh.host;
      networkConfirm.hidden = false;
      renderConnection();
      networkEnable.focus();
    }
  });
  document.getElementById("network-cancel").addEventListener("click", () => {
    networkConfirm.hidden = true;
    networkToggle.focus();
  });
  networkEnable.addEventListener("click", () => {
    networkConfirm.hidden = true;
    void networkAction(async () => {
      await sendMessage({ type: "popup_network_set", enabled: true, sessionId: confirmedSession });
      document.getElementById("network-reload").hidden = false;
    }, "Could not turn on remote browsing.");
  });
  document.getElementById("network-reload-tabs").addEventListener("click", () => {
    if (confirm("Reload every tab in the Tether group? Unsaved changes may be lost. Other tabs will not be reloaded.")) {
      void networkAction(() => sendMessage({ type: "popup_reload_remote_tabs" }), "Could not reload the Tether tabs.");
    }
  });

  function renderConnection() {
    const ssh = network.ssh || {};
    const connecting = ssh.state === "connecting";
    const connected = ssh.state === "connected";
    // canEnable and "on" come from the controller's validated SSH/proxy state.
    const remoteReady = !statusError && (network.canEnable || network.state === "on");
    const remoteProblem = network.enabled && !remoteReady;
    // A live tunnel is never an error, even when the helper reports a stale message.
    const hasSshError = Boolean(ssh.error) && !connected;

    statusText.textContent = bridgeConnected ? "Connected" : "Disconnected";
    statusText.dataset.state = bridgeConnected ? "connected" : "disconnected";
    sshStatus.textContent = statusError ? "Unknown" : connecting ? "Connecting..." :
      hasSshError ? "Error" : connected ? "Connected" : ssh.state === "checking" ? "Checking..." : "Disconnected";
    sshStatus.dataset.state = statusError || hasSshError ? "error" : ssh.state;
    sshStatus.title = ssh.host || "";
    remoteStatus.textContent = statusError ? "Unknown" : network.state === "checking" ? "Checking..." :
      network.enabled ? remoteReady ? "On" : "On, not ready" : "Off";
    const announcement = `Agent bridge ${statusText.textContent}. SSH tunnel ${sshStatus.textContent}. Remote browsing ${remoteStatus.textContent}.`;
    // Only changed text should be announced by screen readers.
    if (connectionAnnouncement.textContent !== announcement) connectionAnnouncement.textContent = announcement;
    remoteStatus.dataset.state = statusError || remoteProblem ? "error" : network.enabled ? "connected" : "off";
    connectSection.style.display = (connectionPanelOpen ?? (!bridgeConnected || connecting || hasSshError || remoteProblem || statusError)) ? "block" : "none";
    connectionToggle.setAttribute("aria-expanded", String(connectSection.style.display !== "none"));
    connectHostInput.disabled = connecting || network.enabled;
    if (network.enabled) {
      connectHostInput.value = network.host || ssh.host || "";
    } else if (!connectHostInput.value && ssh.host && document.activeElement !== connectHostInput) {
      connectHostInput.value = ssh.host;
    }

    const currentInput = connectHostInput.value.trim();
    const isSameHost = Boolean(ssh.host && currentInput.toLowerCase() === ssh.host.toLowerCase());

    btnDoConnect.classList.remove("btn-connect-secondary", "btn-connect-danger");
    if (connecting) {
      btnDoConnect.textContent = "Cancel";
      btnDoConnect.disabled = networkBusy;
      btnDoConnect.classList.add("btn-connect-secondary");
      btnDoConnect.title = "Cancel in-flight SSH connection";
    } else if (connected) {
      if (isSameHost || network.enabled) {
        btnDoConnect.textContent = "Disconnect";
        btnDoConnect.disabled = networkBusy;
        if (network.enabled) {
          btnDoConnect.classList.add("btn-connect-danger");
          btnDoConnect.title = "Disconnect SSH tunnel (remote browsing is on)";
        } else {
          btnDoConnect.classList.add("btn-connect-secondary");
          btnDoConnect.title = "Disconnect SSH tunnel";
        }
      } else {
        btnDoConnect.textContent = "Switch";
        btnDoConnect.disabled = networkBusy || !currentInput;
        btnDoConnect.title = currentInput ? `Switch connection to ${currentInput}` : "Enter remote host to switch";
      }
    } else if (network.enabled && !connected) {
      btnDoConnect.textContent = "Reconnect";
      btnDoConnect.disabled = networkBusy;
      btnDoConnect.title = `Reconnect to ${network.host || ssh.host}`;
    } else {
      btnDoConnect.textContent = "Connect";
      btnDoConnect.disabled = networkBusy || !currentInput;
      btnDoConnect.title = currentInput ? `Connect to ${currentInput}` : "Enter remote host to connect";
    }
    recentHostsList.querySelectorAll("button").forEach((button) => { button.disabled = connecting || network.enabled || networkBusy; });
    networkToggle.checked = network.enabled;
    // Turning OFF remains available during reconnection and helper failure.
    networkToggle.disabled = !network.enabled && (!remoteReady || networkBusy);
    networkEnable.disabled = !remoteReady || networkBusy || confirmedSession !== ssh.sessionId;
    networkReadiness.textContent = remoteReady ? "Ready" : "Not ready";
    networkReadiness.dataset.ready = String(Boolean(remoteReady));
    networkValue.textContent = network.enabled ? "ON" : "OFF";
    networkToggle.closest("label").title = `${remoteReady ? "Ready" : "Not ready"}${ssh.host ? ` · ${ssh.host}` : ""}. Applies to all tabs in this Chrome profile, except Incognito.`;

    // Show one explanation; retain all diagnostic messages without repeating them.
    if (actionError) {
      networkError.textContent = `${actionError} Check the details below, then try again.`;
    } else if (statusError) {
      networkError.textContent = "Tether can't check the connection right now. Reopen this popup to try again.";
    } else if (network.state === "conflict" || (!network.enabled && connected && !network.canEnable)) {
      networkError.textContent = network.enabled
        ? "Remote browsing isn't active. Turn it off, then check your proxy extensions or Chrome's network settings."
        : "Chrome won't let Tether use your server's network. Check your proxy extensions or Chrome's network settings.";
    } else if (remoteProblem) {
      networkError.textContent = "Remote browsing can't reach your server. New pages may not load. Reconnect, or turn Remote browsing off to browse normally.";
    } else if (hasSshError) {
      networkError.textContent = "Couldn't connect to your server. Check the address and your SSH sign-in, then try again.";
    } else {
      networkError.textContent = "";
    }
    networkError.hidden = !networkError.textContent;
    networkErrorDetail.textContent = [...new Set([actionErrorDetails, statusError, ssh.error, network.message].filter(Boolean))].join("\n\n");
    networkErrorDetails.hidden = networkError.hidden || !networkErrorDetail.textContent;
    if (networkErrorDetails.hidden) networkErrorDetails.open = false;
    if (network.enabled) document.getElementById("network-reload").hidden = false;
  }

  async function loadRecentHosts() {
    try {
      const data = await chrome.storage.local.get(["recent_hosts"]);
      const hosts = data.recent_hosts || [];
      if (hosts.length === 0) {
        recentHostsWrapper.style.display = "none";
        return;
      }
      recentHostsWrapper.style.display = "flex";
        recentHostsList.innerHTML = "";
        hosts.forEach((h) => {
          const chip = document.createElement("button");
          chip.type = "button";
          chip.className = "recent-chip";
          chip.textContent = h;
          chip.disabled = network.enabled || network.ssh?.state === "connecting" || networkBusy;
          chip.title = `Connect to ${h}`;
          chip.addEventListener("click", () => {
            connectHostInput.value = h;
            doConnect(h);
          });
          recentHostsList.appendChild(chip);
        });
    } catch {}
  }

  async function saveRecentHost(host) {
    try {
      const data = await chrome.storage.local.get(["recent_hosts"]);
      let hosts = data.recent_hosts || [];
      hosts = [host, ...hosts.filter((item) => item !== host)].slice(0, 5);
      await chrome.storage.local.set({ recent_hosts: hosts });
      loadRecentHosts();
    } catch {}
  }

  async function doConnect(host) {
    if (!host) return;
    connectionPanelOpen = null;
    await networkAction(async () => {
      const ssh = await sendMessage({ type: "popup_ssh_connect", targetHost: host });
      network = { ...network, ssh };
      await saveRecentHost(host);
    }, "Could not connect to the server.");
  }

  async function doDisconnect() {
    const warning = network.enabled
      ? "Disconnect from the server? Remote browsing will stay on, so new pages may not load until you reconnect or turn it off."
      : "Disconnect from the server?";
    if (network.ssh?.state === "connecting" || confirm(warning)) {
      await networkAction(async () => {
        const ssh = await sendMessage({ type: "popup_ssh_disconnect" });
        network = { ...network, ssh };
      }, "Could not disconnect from the server.");
    }
  }

  async function handleConnectAction() {
    const ssh = network.ssh || {};
    const connecting = ssh.state === "connecting";
    const connected = ssh.state === "connected";
    const inputVal = connectHostInput.value.trim();
    const isSameHost = Boolean(ssh.host && inputVal.toLowerCase() === ssh.host.toLowerCase());

    if (connecting || (connected && (isSameHost || network.enabled))) {
      await doDisconnect();
      return;
    }

    const targetHost = (connected && !isSameHost) ? inputVal : (inputVal || ssh.host || network.host);
    if (targetHost) {
      await doConnect(targetHost);
    }
  }

  btnDoConnect.addEventListener("click", (e) => {
    e.preventDefault();
    handleConnectAction();
  });

  connectHostInput.addEventListener("input", renderConnection);
  connectHostInput.addEventListener("keydown", (e) => {
    const ssh = network.ssh || {};
    if (e.key === "Enter") {
      e.preventDefault();
      const host = connectHostInput.value.trim();
      if (!networkBusy && !network.enabled && ssh.state !== "connecting" &&
          !(ssh.state === "connected" && host.toLowerCase() === ssh.host?.toLowerCase())) {
        void doConnect(host);
      }
    } else if (e.key === "Escape" && ssh.state === "connected" && ssh.host) {
      e.preventDefault();
      connectHostInput.value = ssh.host;
      renderConnection();
    }
  });

  connectionToggle.addEventListener("click", () => {
    const isShown = connectSection.style.display !== "none";
    connectionPanelOpen = !isShown;
    connectSection.style.display = isShown ? "none" : "block";
    connectionToggle.setAttribute("aria-expanded", String(!isShown));
    if (!isShown) loadRecentHosts();
  });

  function updateStatusUI(status) {
    bridgeConnected = Boolean(status?.connected);
    renderConnection();
  }
  loadRecentHosts();

  function updateTabsUI(tabs, activeId) {
    if (!tabs || tabs.length === 0) {
      tabsList.innerHTML = '<div class="empty-state">No tethered tabs found. Open a URL below or click <b>+ New Tab</b> to start.</div>';
      tabsList.dataset.rendered = "";
      return;
    }

    const focusedTabId = document.activeElement && tabsList.contains(document.activeElement)
      ? document.activeElement.dataset?.tabId
      : null;

    const signature = JSON.stringify({
      activeId: String(activeId),
      tabs: tabs.map((t) => ({ id: String(t.id), active: Boolean(t.active), inGroup: Boolean(t.inGroup), title: t.title, url: t.url })),
    });

    if (tabsList.dataset.rendered === signature) {
      return;
    }
    tabsList.dataset.rendered = signature;

    tabsList.innerHTML = "";
    tabs.forEach((tab) => {
      const isActive = tab.id === activeId || tab.active;
      const item = document.createElement("button");
      item.type = "button";
      item.dataset.tabId = String(tab.id);
      item.className = `tab-item ${isActive ? "active" : ""}`;
      item.title = `${tab.title}\n${tab.url}`;

      item.innerHTML = `
        <span class="tab-info">
          <span class="tab-title">${escapeHTML(tab.title || "Untitled")}</span>
          <span class="tab-url">${escapeHTML(tab.url || "")}</span>
        </span>
        ${isActive ? '<span class="tab-badges"><span class="tab-badge">ACTIVE</span></span>' : ''}
      `;

      item.addEventListener("click", async () => {
        await sendMessage({ type: "popup_switch_tab", tabId: tab.id });
        refresh();
      });

      tabsList.appendChild(item);
    });

    if (focusedTabId) {
      const el = tabsList.querySelector(`[data-tab-id="${focusedTabId}"]`);
      if (el) el.focus();
    }
  }

  function updateNotesUI(notes, activeTab) {
    currentNotes = notes || [];
    btnCopyNotes.disabled = !currentReport;
    btnClearNotes.disabled = currentNotes.length === 0;
    document.getElementById("notes-badge").textContent = currentNotes.length;
      if (activeTab && activeTab.title) {
        const cleanTitle = activeTab.title.replace(/^\[Tether\]\s*/, "");
        activeTabTitleEl.textContent = cleanTitle;
        activeTabTitleEl.title = activeTab.title + "\n" + (activeTab.url || "");
      } else {
        activeTabTitleEl.textContent = "Active Tab";
      }

    const serializedNotes = JSON.stringify(
      currentNotes.map((n) => ({
        id: n.id,
        comment: n.comment,
        selector: n.payload?.target?.selector,
      }))
    );

    if (notesList.dataset.rendered === serializedNotes) return;
    notesList.dataset.rendered = serializedNotes;

    if (currentNotes.length === 0) {
      notesList.innerHTML = '<div class="empty-state">No pinned notes yet. Click <b>Inspect & Pin Notes</b> to review elements on this page.</div>';
      return;
    }

    notesList.innerHTML = "";
    currentNotes.forEach((note) => {
      const item = document.createElement("div");
      item.className = "note-item";
      const target = note.payload?.target || {};
      const selector = target.selector || target.tagName || "element";

      item.innerHTML = `
        <span class="note-target">${escapeHTML(selector)}</span>
        <span class="note-comment">${escapeHTML(note.comment || "")}</span>
      `;
      notesList.appendChild(item);
    });
  }
  // 2. Action: Inspect & Pin Notes (FireShot style)
  btnInspect.addEventListener("click", async () => {
    btnInspect.disabled = true;
    try {
      const currentTabId = (await getActiveTab())?.id || null;
      await sendMessage({ type: "popup_start_review", tabId: currentTabId });
      // Close popup so user can click elements immediately
      window.close();
    } catch (err) {
      alert("Failed to start review mode: " + err.message);
      btnInspect.disabled = false;
    }
  });

  // 3. Action: Take Screenshot
  for (const [button, label, fullPage] of [[btnCaptureViewport, "Viewport", false], [btnCaptureFull, "Full Page", true]]) {
    button.addEventListener("click", async () => {
      button.disabled = true;
      showToast(`Capturing ${label.toLowerCase()}...`, 2000);
      try {
        const tab = await getActiveTab();
        const res = await sendMessage({ type: "popup_capture_screenshot", tabId: tab?.id || null, fullPage });
        if (res?.data) {
          await saveScreenshots([{
            filename: res.filename,
            data: res.data,
            label,
            title: tab?.title || label,
            url: tab?.url || "",
            dimensions: res.width && res.height ? `${res.width}×${res.height}` : label,
            remotePath: res.saveResult?.mirrored ? res.saveResult.remotePath : "",
            mirrored: res.saveResult?.mirrored || false,
            comment: "",
          }, ...currentShots]);
          showToast(`${label} screenshot captured!`, 2000);
        }
      } catch (err) {
        showToast("Capture failed: " + err.message, 3000);
      } finally {
        button.disabled = false;
      }
    });
  }

  btnCaptureArea.addEventListener("click", async () => {
    btnCaptureArea.disabled = true;
    try {
      const currentTabId = (await getActiveTab())?.id || null;
      await sendMessage({ type: "popup_start_crop", tabId: currentTabId });
      window.close();
    } catch (err) {
      showToast("Failed to start area crop: " + err.message, 3000);
      btnCaptureArea.disabled = false;
    }
  });

  function formatScreenshotsReport(shots) {
    if (!shots || shots.length === 0) return "";
    const count = shots.length;
    const lines = [
      `## Visual Review: ${count} Screenshot${count === 1 ? "" : "s"} Captured`,
      "",
    ];
    shots.forEach((s, idx) => {
      lines.push(`### ${idx + 1}. ${s.label || s.title || "Screenshot"}`);
      lines.push(s.mirrored && s.remotePath ? `- **Server Path:** \`${s.remotePath}\`` : "- **Storage:** Local only");
      if (s.url) lines.push(`- **URL:** ${s.url}`);
      if (s.dimensions) lines.push(`- **Dimensions:** ${s.dimensions}`);
      if (s.comment) lines.push(`- **Feedback:** ${s.comment}`);
      lines.push("");
    });
    return lines.join("\n");
  }

    btnCopyShots.addEventListener("click", () => {
      if (!currentShots || currentShots.length === 0) {
        alert("No screenshots to copy. Capture a screenshot first.");
        return;
      }
      const md = formatScreenshotsReport(currentShots);
      navigator.clipboard.writeText(md).then(() => {
        showToast("✓ Copied screenshots report for agent!", 2000);
      });
    });

    btnClearShots.addEventListener("click", async () => {
      if (confirm("Delete all screenshots both locally on your Mac and remotely on the server?")) {
        await sendMessage({ type: "popup_clear_screenshots" });
        await saveScreenshots([]);
        showToast("✓ Cleared all screenshots", 2000);
      }
    });

  // 4. Action: Copy Notes for AI Agent
  btnCopyNotes.addEventListener("click", () => {
    if (currentNotes.length === 0) {
      alert("No review notes to copy. Click 'Inspect & Pin Notes' to add notes first.");
      return;
    }

    navigator.clipboard.writeText(currentReport).then(() => {
      const copyText = document.getElementById("copy-text");
      copyText.textContent = "Copied!";
      setTimeout(() => { copyText.textContent = "Copy"; }, 1500);
    });
  });

  // 5. Action: Clear Notes
  btnClearNotes.addEventListener("click", async () => {
    if (confirm("Clear all review notes on this page?")) {
      const currentTabId = (await getActiveTab())?.id || null;
      await sendMessage({ type: "popup_clear_notes", tabId: currentTabId });
      currentNotes = [];
      currentReport = "";
      updateNotesUI([]);
      refresh();
    }
  });

  // 6. Refresh Tabs
  btnRefreshTabs.addEventListener("click", () => {
    refresh();
  });

  // 7. Navigation Form
  navForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    let url = navInput.value.trim();
    if (!url) return;
    if (!url.startsWith("http://") && !url.startsWith("https://")) {
      url = "https://" + url;
    }
    navInput.value = "";
    await sendMessage({ type: "popup_navigate", url, newTab: false });
    refresh();
  });

  const btnNavNew = document.getElementById("btn-nav-new");
    btnNavNew.addEventListener("click", async () => {
      let url = navInput.value.trim();
      if (url && !url.startsWith("http://") && !url.startsWith("https://")) {
        url = "https://" + url;
      }
      navInput.value = "";
      await sendMessage({ type: "popup_navigate", url, newTab: true });
      refresh();
    });
  // Helpers
  async function getActiveTab() {
    try {
      return (await chrome.tabs.query({ active: true, currentWindow: true }))[0] || null;
    } catch {
      return null;
    }
  }

  function sendMessage(msg) {
    return new Promise((resolve, reject) => {
      chrome.runtime.sendMessage(msg, (response) => {
        if (chrome.runtime.lastError) {
          reject(new Error(chrome.runtime.lastError.message));
        } else if (response?.error) {
          reject(new Error(response.error));
        } else {
          resolve(response || {});
        }
      });
    });
  }

  function escapeHTML(str) {
    return String(str)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#039;");
  }

  // Initial load + live 2-second status & tab poll while popup is open
  const savedTab = await chrome.storage.local.get("tether_popup_tab").catch(() => ({}));
  // Do not override a selection made while the saved preference was loading.
  if (!selectedTab) {
    selectedTab = savedTab.tether_popup_tab === "shots" ? tabBtnShots : tabBtnNotes;
    selectTab(selectedTab);
  }
  if (panelShots.hidden) loadScreenshots();
  refresh();
  const pollInterval = setInterval(refresh, 2000);
  window.addEventListener("unload", () => clearInterval(pollInterval));
});
