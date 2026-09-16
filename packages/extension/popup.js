// Tether Browser Bridge - Popup UI Controller
// Provides a clean, FireShot-style developer panel directly inside Chrome.

document.addEventListener("DOMContentLoaded", () => {
  const statusBadge = document.getElementById("status-badge");
  const statusText = document.getElementById("status-text");
  const btnInspect = document.getElementById("btn-inspect");
  const btnScreenshot = document.getElementById("btn-screenshot");
  const btnCopyNotes = document.getElementById("btn-copy-notes");
  const btnClearNotes = document.getElementById("btn-clear-notes");
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
  try {
    const manifest = chrome.runtime.getManifest();
    if (extVersionEl && manifest.version) {
      extVersionEl.textContent = "v" + manifest.version;
    }
  } catch {}

  // Reload extension directly from disk (after git pull)
  if (btnReloadExt) {
    btnReloadExt.addEventListener("click", () => {
      btnReloadExt.style.transform = "rotate(360deg)";
      btnReloadExt.style.transition = "transform 0.4s ease";
      setTimeout(() => {
        chrome.runtime.reload();
      }, 100);
    });
  }

  let currentNotes = [];

  // 1. Load Status & Initial Data
  async function refresh() {
    try {
      const status = await sendMessage({ type: "popup_get_status" });
      const tabs = status.tabs || [];
      const activeTab = tabs.find((t) => t.id === status.activeTabId || t.active);
      updateStatusUI(status);
      updateTabsUI(tabs, status.activeTabId);
      updateNotesUI(status.notes || [], activeTab);
    } catch (err) {
      console.warn("Failed to load status:", err);
      updateStatusUI({ connected: false });
    }
  }

  function updateStatusUI(status) {
    if (status && status.connected) {
      statusBadge.className = "badge badge-connected";
      statusText.textContent = "Bridge Active";
    } else {
      statusBadge.className = "badge badge-disconnected";
      statusText.textContent = "Waiting for Daemon";
    }
  }

  function updateTabsUI(tabs, activeId) {
    if (!tabs || tabs.length === 0) {
      tabsList.innerHTML = '<div class="empty-state">No open tabs found.</div>';
      return;
    }

    tabsList.innerHTML = "";
    tabs.forEach((tab) => {
      const isActive = tab.id === activeId || tab.active;
      const item = document.createElement("div");
      item.className = `tab-item ${isActive ? "active" : ""}`;
      item.title = `${tab.title}\n${tab.url}`;

      item.innerHTML = `
        <div class="tab-info">
          <div class="tab-title">${escapeHTML(tab.title || "Untitled")}</div>
          <div class="tab-url">${escapeHTML(tab.url || "")}</div>
        </div>
        <div class="tab-badges" style="display:flex;gap:4px;align-items:center;">
          ${tab.inGroup ? '<span class="tab-badge" style="background:#0284c7;color:#fff;">TETHER</span>' : ''}
          ${isActive ? '<span class="tab-badge">ACTIVE</span>' : ''}
        </div>
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
    notesCount.textContent = currentNotes.length;

    if (activeTabTitleEl) {
      if (activeTab && activeTab.title) {
        const cleanTitle = activeTab.title.replace(/^\[Tether\]\s*/, "");
        activeTabTitleEl.textContent = cleanTitle.length > 25 ? cleanTitle.slice(0, 25) + "..." : cleanTitle;
        activeTabTitleEl.title = activeTab.title + "\n" + (activeTab.url || "");
      } else {
        activeTabTitleEl.textContent = "Active Tab";
      }
    }

    if (currentNotes.length === 0) {
      notesList.innerHTML = '<div class="empty-state">No pinned notes yet. Click <b>Inspect & Pin Notes</b> to review elements on this page.</div>';
      return;
    }
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
      await sendMessage({ type: "popup_start_review" });
      // Close popup so user can click elements immediately
      window.close();
    } catch (err) {
      alert("Failed to start review mode: " + err.message);
      btnInspect.disabled = false;
    }
  });

  // 3. Action: Take Screenshot
  btnScreenshot.addEventListener("click", async () => {
    btnScreenshot.disabled = true;
    try {
      const res = await sendMessage({ type: "popup_capture_screenshot" });
      if (res && res.data) {
        // Download screenshot
        const a = document.createElement("a");
        a.href = "data:image/png;base64," + res.data;
        a.download = `tether-screenshot-${Date.now()}.png`;
        a.click();
      }
    } catch (err) {
      alert("Screenshot failed: " + err.message);
    } finally {
      btnScreenshot.disabled = false;
    }
  });

  // 4. Action: Copy Notes for AI Agent
  btnCopyNotes.addEventListener("click", () => {
    if (currentNotes.length === 0) {
      alert("No review notes to copy. Click 'Inspect & Pin Notes' to add notes first.");
      return;
    }

    const md = formatDesignFeedbackReport(currentNotes);
    navigator.clipboard.writeText(md).then(() => {
      const orig = btnCopyNotes.textContent;
      btnCopyNotes.textContent = "Copied!";
      setTimeout(() => { btnCopyNotes.textContent = orig; }, 1500);
    });
  });

  // 5. Action: Clear Notes
  btnClearNotes.addEventListener("click", async () => {
    if (confirm("Clear all review notes on this page?")) {
      await sendMessage({ type: "popup_clear_notes" });
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

  // Initial load
  refresh();
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
