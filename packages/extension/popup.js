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

  let currentNotes = [];

  // 1. Load Status & Initial Data
  async function refresh() {
    try {
      const status = await sendMessage({ type: "popup_get_status" });
      updateStatusUI(status);
      updateTabsUI(status.tabs || [], status.activeTabId);
      updateNotesUI(status.notes || []);
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
        ${isActive ? '<span class="tab-badge">ACTIVE</span>' : ""}
      `;

      item.addEventListener("click", async () => {
        await sendMessage({ type: "popup_switch_tab", tabId: tab.id });
        refresh();
      });

      tabsList.appendChild(item);
    });
  }

  function updateNotesUI(notes) {
    currentNotes = notes || [];
    notesCount.textContent = currentNotes.length;

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

    let md = "# Developer Review Notes\n\n";
    currentNotes.forEach((n, idx) => {
      const t = n.payload?.target || {};
      md += `### ${idx + 1}. \`${t.selector || t.tagName || "element"}\`\n`;
      md += `* **Feedback**: ${n.comment || "No comment"}\n`;
      if (t.elementPath) md += `* **DOM Path**: \`${t.elementPath}\`\n`;
      if (t.framework?.component) md += `* **Component**: \`${t.framework.component}\`\n`;
      md += "\n";
    });

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
    await sendMessage({ type: "popup_navigate", url });
    refresh();
  });

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
