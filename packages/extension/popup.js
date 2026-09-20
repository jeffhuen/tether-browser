// Tether Browser Bridge - Popup UI Controller
// Provides a clean, FireShot-style developer panel directly inside Chrome.

document.addEventListener("DOMContentLoaded", async () => {
  const statusBadge = document.getElementById("status-badge");
  const statusText = document.getElementById("status-text");
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
  const notesCount = document.getElementById("notes-count");
  const notesList = document.getElementById("notes-list");
  const tabsList = document.getElementById("tabs-list");
  const navForm = document.getElementById("nav-form");
  const navInput = document.getElementById("nav-input");
  const activeTabTitleEl = document.getElementById("active-tab-title");
  const extVersionEl = document.getElementById("ext-version");
  const btnReloadExt = document.getElementById("btn-reload-ext");

  // Set dynamic version from manifest
  let localVersion = "0.1.21";
  try {
    const manifest = chrome.runtime.getManifest();
    if (manifest.version) {
      localVersion = manifest.version;
      if (extVersionEl) extVersionEl.textContent = "v" + localVersion;
    }
  } catch {}

  // Check updates and reload extension directly from disk
  if (btnReloadExt) {
    btnReloadExt.addEventListener("click", async () => {
      btnReloadExt.style.transform = "rotate(360deg)";
      btnReloadExt.style.transition = "transform 0.4s ease";

      showToast("Checking updates...", 1500);

      try {
        const controller = new AbortController();
        const timeoutId = setTimeout(() => controller.abort(), 2000);
        const res = await fetch("https://raw.githubusercontent.com/jeffhuen/tether-browser/main/packages/extension/manifest.json", {
          signal: controller.signal,
          cache: "no-store",
        });
        clearTimeout(timeoutId);
        if (res.ok) {
          const remoteManifest = await res.json();
          if (remoteManifest.version && remoteManifest.version > localVersion) {
            showToast(`Update available: v${remoteManifest.version} (run git pull)`, 3000);
            setTimeout(() => chrome.runtime.reload(), 1500);
            return;
          } else {
            showToast(`✓ Up to date (v${localVersion})`, 2000);
            setTimeout(() => chrome.runtime.reload(), 800);
            return;
          }
        }
      } catch (e) {
        // Offline or check failed: reload directly
      }

      showToast(`✓ Reloaded from disk (v${localVersion})`, 1500);
      setTimeout(() => chrome.runtime.reload(), 400);
    });
  }

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
    if (shotsBadge) shotsBadge.textContent = shots.length;
    btnCopyShots.disabled = shots.length === 0;
    btnClearShots.disabled = shots.length === 0;
    if (!shotsList) return;
    if (!shots || shots.length === 0) {
      shotsList.innerHTML = '<div class="empty-state">No screenshots captured yet. Click <b>Crop Area</b> or <b>Viewport</b> above.</div>';
      return;
    }
    shotsList.innerHTML = "";
    shots.forEach((shot, idx) => {
      const card = document.createElement("div");
      card.className = "shot-card";
      const remotePath = shot.remotePath || `/tmp/tether-screenshots/${shot.filename}`;
      card.innerHTML = `
        <div class="shot-top-row">
          <button type="button" class="shot-thumb-wrapper" aria-label="Enlarge screenshot" title="Click to enlarge">
            <img class="shot-thumb" src="data:image/png;base64,${shot.data}" alt="${escapeHTML(shot.label || shot.title || "Screenshot")}">
          </button>
          <div class="shot-details">
            <div class="shot-header">
              <span class="shot-title">${escapeHTML(shot.label || shot.title || "Screenshot")}</span>
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
        openLightbox(shot.data, shot.label || shot.title || "Screenshot", shot.dimensions);
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
    if (!lightboxModal || !lightboxImg) return;
    lightboxImg.src = `data:image/png;base64,${base64Data}`;
    lightboxImg.alt = title;
    if (lightboxTitle) lightboxTitle.textContent = meta ? `${title} (${meta})` : title;
    lightboxModal.showModal();
  }

  if (lightboxClose) {
    lightboxClose.addEventListener("click", () => {
      lightboxModal.close();
    });
  }
  if (lightboxModal) {
    lightboxModal.addEventListener("click", (e) => {
      if (e.target === lightboxModal) lightboxModal.close();
    });
  }

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
    try {
      let currentTabId = null;
      let currentTab = null;
      try {
        const [active] = await chrome.tabs.query({ active: true, currentWindow: true });
        if (active) {
          currentTab = active;
          currentTabId = active.id;
        }
      } catch {}

      const status = await sendMessage({ type: "popup_get_status", currentTabId });
      const tabs = status.tabs || [];
      const activeTab = tabs.find((t) => t.id === String(status.activeTabId) || t.active) ||
                        (currentTab ? { id: String(currentTab.id), title: currentTab.title, url: currentTab.url } : null);
      updateStatusUI(status);
      updateTabsUI(tabs, status.activeTabId);
      updateNotesUI(status.notes || [], activeTab);
    } catch (err) {
      console.warn("Failed to load status:", err);
      updateStatusUI({ connected: false });
    }
  }

  const connectSection = document.getElementById("connect-section");
  const connectForm = document.getElementById("connect-form");
  const connectHostInput = document.getElementById("connect-host-input");
  const btnDoConnect = document.getElementById("btn-do-connect");
  const recentHostsWrapper = document.getElementById("recent-hosts-wrapper");
  const recentHostsList = document.getElementById("recent-hosts-list");

  async function loadRecentHosts() {
    try {
      const data = await chrome.storage.local.get(["recent_hosts"]);
      const hosts = data.recent_hosts || [];
      if (hosts.length === 0) {
        if (recentHostsWrapper) recentHostsWrapper.style.display = "none";
        return;
      }
      if (recentHostsWrapper) recentHostsWrapper.style.display = "flex";
      if (recentHostsList) {
        recentHostsList.innerHTML = "";
        hosts.forEach((h) => {
          const chip = document.createElement("button");
          chip.type = "button";
          chip.className = "recent-chip";
          chip.textContent = h;
          chip.title = `Connect to ${h}`;
          chip.addEventListener("click", () => {
            if (connectHostInput) connectHostInput.value = h;
            doConnect(h);
          });
          recentHostsList.appendChild(chip);
        });
      }
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
    if (btnDoConnect) {
      btnDoConnect.disabled = true;
      btnDoConnect.textContent = "Connecting...";
    }
    showToast(`Connecting to ${host}...`, 3000);

    try {
      await saveRecentHost(host);
      await sendMessage({ type: "popup_ssh_connect", targetHost: host });
      showToast(`✓ Connected to ${host}!`, 2000);
      refresh();
    } catch (err) {
      showToast("Connect error: " + err.message, 3000);
    } finally {
      if (btnDoConnect) {
        btnDoConnect.disabled = false;
        btnDoConnect.textContent = "Connect";
      }
    }
  }

  if (connectForm) {
    connectForm.addEventListener("submit", (e) => {
      e.preventDefault();
      const host = connectHostInput ? connectHostInput.value.trim() : "";
      if (host) doConnect(host);
    });
  }

  if (statusBadge) {
    statusBadge.style.cursor = "pointer";
    statusBadge.addEventListener("click", () => {
      if (connectSection) {
        const isShown = connectSection.style.display !== "none";
        connectSection.style.display = isShown ? "none" : "block";
        statusBadge.setAttribute("aria-expanded", String(!isShown));
        if (!isShown) loadRecentHosts();
      }
    });
  }

  const btnDisconnect = document.getElementById("btn-disconnect");
  if (btnDisconnect) {
    btnDisconnect.addEventListener("click", async () => {
      if (confirm("Disconnect SSH tunnel?")) {
        await sendMessage({ type: "popup_ssh_disconnect" });
        refresh();
      }
    });
  }

  function updateStatusUI(status) {
    if (status && status.connected) {
      statusBadge.className = "badge badge-connected";
      statusText.textContent = "Connected";
      if (connectSection) connectSection.style.display = "none";
      if (btnDisconnect) btnDisconnect.style.display = "inline-flex";
    } else {
      statusBadge.className = "badge badge-disconnected";
      statusText.textContent = "Disconnected";
      if (connectSection) connectSection.style.display = "block";
      if (btnDisconnect) btnDisconnect.style.display = "none";
      loadRecentHosts();
    }
    statusBadge.setAttribute("aria-expanded", String(connectSection.style.display !== "none"));
  }

  function updateTabsUI(tabs, activeId) {
    if (!tabs || tabs.length === 0) {
      tabsList.innerHTML = '<div class="empty-state">No open tabs found.</div>';
      return;
    }

    tabsList.innerHTML = "";
    tabs.forEach((tab) => {
      const isActive = tab.id === activeId || tab.active;
      const item = document.createElement("button");
      item.type = "button";
      item.className = `tab-item ${isActive ? "active" : ""}`;
      item.title = `${tab.title}\n${tab.url}`;

      item.innerHTML = `
        <span class="tab-info">
          <span class="tab-title">${escapeHTML(tab.title || "Untitled")}</span>
          <span class="tab-url">${escapeHTML(tab.url || "")}</span>
        </span>
        <span class="tab-badges">
          ${tab.inGroup ? '<span class="tab-badge">TETHER</span>' : ''}
          ${isActive ? '<span class="tab-badge">ACTIVE</span>' : ''}
        </span>
      `;

      item.addEventListener("click", async () => {
        await sendMessage({ type: "popup_switch_tab", tabId: tab.id });
        refresh();
      });

      tabsList.appendChild(item);
    });
  }

  function updateNotesUI(notes, activeTab) {
    currentNotes = notes || [];
    btnCopyNotes.disabled = currentNotes.length === 0;
    btnClearNotes.disabled = currentNotes.length === 0;
    const notesBadge = document.getElementById("notes-badge") || document.getElementById("notes-count");
    if (notesBadge) notesBadge.textContent = currentNotes.length;

    if (activeTabTitleEl) {
      if (activeTab && activeTab.title) {
        const cleanTitle = activeTab.title.replace(/^\[Tether\]\s*/, "");
        activeTabTitleEl.textContent = cleanTitle;
        activeTabTitleEl.title = activeTab.title + "\n" + (activeTab.url || "");
      } else {
        activeTabTitleEl.textContent = "Active Tab";
      }
    }

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
      let currentTabId = null;
      try {
        const [active] = await chrome.tabs.query({ active: true, currentWindow: true });
        if (active) currentTabId = active.id;
      } catch {}
      await sendMessage({ type: "popup_start_review", tabId: currentTabId });
      // Close popup so user can click elements immediately
      window.close();
    } catch (err) {
      alert("Failed to start review mode: " + err.message);
      btnInspect.disabled = false;
    }
  });

  // 3. Action: Take Screenshot
  if (btnCaptureViewport) {
    btnCaptureViewport.addEventListener("click", async () => {
      btnCaptureViewport.disabled = true;
      showToast("Capturing viewport...", 2000);
      try {
        let currentTabId = null;
        let tabInfo = null;
        try {
          const [active] = await chrome.tabs.query({ active: true, currentWindow: true });
          if (active) {
            currentTabId = active.id;
            tabInfo = active;
          }
        } catch {}
        const res = await sendMessage({
          type: "popup_capture_screenshot",
          tabId: currentTabId,
        });
        if (res && res.data) {
          const newShot = {
            id: `shot-${Date.now()}`,
            filename: res.filename,
            data: res.data,
            label: "Viewport",
            title: tabInfo?.title || "Page Viewport",
            url: tabInfo?.url || "",
            dimensions: res.width && res.height ? `${res.width}×${res.height}` : "Viewport",
            remotePath: res.saveResult?.remotePath || `/tmp/tether-screenshots/${res.filename}`,
            localPath: res.saveResult?.localPath || "",
            mirrored: res.saveResult?.mirrored || false,
            comment: "",
            createdAt: new Date().toISOString(),
          };
          const updated = [newShot, ...currentShots];
          await saveScreenshots(updated);
          showToast("✓ Viewport screenshot captured!", 2000);
        }
      } catch (err) {
        showToast("Capture failed: " + err.message, 3000);
      } finally {
        btnCaptureViewport.disabled = false;
      }
    });
  }

  if (btnCaptureFull) {
    btnCaptureFull.addEventListener("click", async () => {
      btnCaptureFull.disabled = true;
      showToast("Capturing full page...", 3000);
      try {
        let currentTabId = null;
        let tabInfo = null;
        try {
          const [active] = await chrome.tabs.query({ active: true, currentWindow: true });
          if (active) {
            currentTabId = active.id;
            tabInfo = active;
          }
        } catch {}
        const res = await sendMessage({
          type: "popup_capture_screenshot",
          tabId: currentTabId,
          fullPage: true,
        });
        if (res && res.data) {
          const newShot = {
            id: `shot-${Date.now()}`,
            filename: res.filename,
            data: res.data,
            label: "Full Page",
            title: tabInfo?.title || "Full Page",
            url: tabInfo?.url || "",
            dimensions: res.width && res.height ? `${res.width}×${res.height}` : "Full Page",
            remotePath: res.saveResult?.remotePath || `/tmp/tether-screenshots/${res.filename}`,
            localPath: res.saveResult?.localPath || "",
            mirrored: res.saveResult?.mirrored || false,
            comment: "",
            createdAt: new Date().toISOString(),
          };
          const updated = [newShot, ...currentShots];
          await saveScreenshots(updated);
          showToast("✓ Full-page screenshot captured!", 2000);
        }
      } catch (err) {
        showToast("Full capture failed: " + err.message, 3000);
      } finally {
        btnCaptureFull.disabled = false;
      }
    });
  }

  if (btnCaptureArea) {
    btnCaptureArea.addEventListener("click", async () => {
      btnCaptureArea.disabled = true;
      try {
        let currentTabId = null;
        try {
          const [active] = await chrome.tabs.query({ active: true, currentWindow: true });
          if (active) currentTabId = active.id;
        } catch {}
        await sendMessage({ type: "popup_start_crop", tabId: currentTabId });
        window.close();
      } catch (err) {
        showToast("Failed to start area crop: " + err.message, 3000);
        btnCaptureArea.disabled = false;
      }
    });
  }

  function formatScreenshotsReport(shots) {
    if (!shots || shots.length === 0) return "";
    const count = shots.length;
    const lines = [
      `## Visual Review: ${count} Screenshot${count === 1 ? "" : "s"} Captured`,
      "",
    ];
    shots.forEach((s, idx) => {
      lines.push(`### ${idx + 1}. ${s.label || s.title || "Screenshot"}`);
      lines.push(`- **Server Path:** \`${s.remotePath || `/tmp/tether-screenshots/${s.filename}`}\``);
      if (s.url) lines.push(`- **URL:** ${s.url}`);
      if (s.dimensions) lines.push(`- **Dimensions:** ${s.dimensions}`);
      if (s.comment) lines.push(`- **Feedback:** ${s.comment}`);
      lines.push("");
    });
    return lines.join("\n");
  }

  if (btnCopyShots) {
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
  }

  if (btnClearShots) {
    btnClearShots.addEventListener("click", async () => {
      if (confirm("Delete all screenshots both locally on your Mac and remotely on the server?")) {
        await sendMessage({ type: "popup_clear_screenshots" });
        await saveScreenshots([]);
        showToast("✓ Cleared all screenshots", 2000);
      }
    });
  }

  // 4. Action: Copy Notes for AI Agent
  btnCopyNotes.addEventListener("click", () => {
    if (currentNotes.length === 0) {
      alert("No review notes to copy. Click 'Inspect & Pin Notes' to add notes first.");
      return;
    }

    const md = formatDesignFeedbackReport(currentNotes);
    navigator.clipboard.writeText(md).then(() => {
      const copyText = document.getElementById("copy-text");
      copyText.textContent = "Copied!";
      setTimeout(() => { copyText.textContent = "Copy"; }, 1500);
    });
  });

  // 5. Action: Clear Notes
  btnClearNotes.addEventListener("click", async () => {
    if (confirm("Clear all review notes on this page?")) {
      let currentTabId = null;
      try {
        const [active] = await chrome.tabs.query({ active: true, currentWindow: true });
        if (active) currentTabId = active.id;
      } catch {}
      await sendMessage({ type: "popup_clear_notes", tabId: currentTabId });
      currentNotes = [];
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
  if (btnNavNew) {
    btnNavNew.addEventListener("click", async () => {
      let url = navInput.value.trim();
      if (!url) url = "about:blank";
      if (url !== "about:blank" && !url.startsWith("http://") && !url.startsWith("https://")) {
        url = "https://" + url;
      }
      navInput.value = "";
      await sendMessage({ type: "popup_navigate", url, newTab: true });
      refresh();
    });
  }
  // Helpers
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
  function formatDesignFeedbackReport(notes) {
    if (!notes || notes.length === 0) return '';
    const first = notes[0];
    const page = first.payload?.page || {};
    const title = page.title || document.title || 'Page Review';
    const url = page.sanitizedUrl || window.location.href;
    const vp = page.viewportWidth ? (page.viewportWidth + 'x' + page.viewportHeight) : (window.innerWidth + 'x' + window.innerHeight);

    const lines = [
      '## Design Feedback: ' + title,
      '',
      '**URL:** ' + url,
      '**Viewport:** ' + vp,
      ''
    ];

    notes.forEach((n, idx) => {
      const p = n.payload || {};
      const t = p.target || {};
      const fw = t.framework || {};
      const rect = t.rectViewport || {};

      const pinIndex = n.index || (idx + 1);
      const componentName = fw.component || t.tagName || 'element';
      const label = t.textSnippet ? t.textSnippet.slice(0, 50) : (t.accessibleName || t.selector || '');
      lines.push('### ' + pinIndex + '. ' + componentName + (label ? (' - "' + label + '"') : ''));

      if (n.intent) {
        lines.push('**Intent:** ' + n.intent);
      }
      lines.push('**Selector:** `' + (t.selector || 'element') + '`');
      if (t.elementPath) {
        lines.push('**Location:** `' + t.elementPath + '`');
      }
      if (fw.name && fw.name !== 'Static') {
        let fwLine = fw.name;
        if (fw.component) fwLine += ' (' + fw.component + ')';
        lines.push('**Framework:** ' + fwLine);
      }
      if (fw.sourceLocation) {
        lines.push('**Source:** `' + fw.sourceLocation + '` (provenance: ' + (fw.provenance || 'inferred') + ')');
      }
      if (rect && typeof rect.width === 'number') {
        lines.push('**Bounds:** viewport x=' + Math.round(rect.x) + ', y=' + Math.round(rect.y) + ', ' + Math.round(rect.width) + 'x' + Math.round(rect.height));
      }
      if (t.cssClasses) {
        lines.push('**Classes:** `' + t.cssClasses + '`');
      }
      if (t.selectedText) {
        lines.push('**Selected text:** "' + t.selectedText + '"');
      } else if (t.textSnippet) {
        lines.push('**Text:** "' + t.textSnippet + '"');
      }
      if (p.nearbyText && p.nearbyText.length > 0) {
        lines.push('**Nearby text:**');
        p.nearbyText.slice(0, 4).forEach(txt => {
          if (txt && txt.trim()) lines.push('- "' + txt.trim() + '"');
        });
      }
      if (p.nearbyElements && p.nearbyElements.length > 0) {
        lines.push('**Nearby elements:**');
        p.nearbyElements.slice(0, 4).forEach(el => {
          if (el && el.trim()) lines.push('- `' + el.trim() + '`');
        });
      }
      if (t.computedStyles && Object.keys(t.computedStyles).length > 0) {
        lines.push('**Computed styles:**');
        for (const [k, v] of Object.entries(t.computedStyles)) {
          if (v && v !== 'auto' && v !== 'normal' && v !== 'static' && v !== 'rgba(0, 0, 0, 0)') {
            lines.push('- ' + k + ': ' + v);
          }
        }
      }
      if (t.htmlSnippet) {
        lines.push('**HTML:**');
        lines.push('```html');
        lines.push(t.htmlSnippet.trim());
        lines.push('```');
      }
      lines.push('**Feedback:** ' + (n.comment || 'No comment'));
      lines.push('');
    });

    return lines.join('\n').trim();
  }
