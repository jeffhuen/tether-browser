(() => {
  'use strict';

  if (window.__tetherReview) {
    return;
  }

  const SECRET_PATTERNS = [
    /access_token/i,
    /auth_token/i,
    /refresh_token/i,
    /id_token/i,
    /\btoken\b/i,
    /\bcode\b/i,
    /\bstate\b/i,
    /\bnonce\b/i,
    /api_key/i,
    /apikey/i,
    /client_secret/i,
    /oauth_state/i,
    /x-amz-/i,
    /session_id/i,
    /sessionid/i,
    /csrf/i,
    /secret/i,
    /password/i,
    /passwd/i
  ];

  function containsSecret(str) {
    if (!str || typeof str !== 'string') return false;
    return SECRET_PATTERNS.some(p => p.test(str));
  }

  function sanitizeText(str, maxLen = 200) {
    if (!str) return '';
    let s = String(str).trim();
    if (containsSecret(s)) {
      return '[redacted]';
    }
    if (s.length > maxLen) {
      s = s.slice(0, maxLen) + '...';
    }
    return s;
  }
  function sanitizeURL(rawURL) {
    if (!rawURL) return '';
    try {
      const u = new URL(rawURL);
      u.hash = '';
      const params = new URLSearchParams(u.search);
      const toDelete = [];
      params.forEach((val, key) => {
        if (containsSecret(key) || containsSecret(val)) {
          toDelete.push(key);
        }
      });
      toDelete.forEach(k => params.delete(k));
      u.search = params.toString();
      return u.toString();
    } catch (e) {
      return rawURL.split('#')[0];
    }
  }

  // --- 1. Universal DOM Baseline Extractor ---

  function buildSelector(el) {
    if (!el || el.nodeType !== Node.ELEMENT_NODE) return '';
    if (el.id && !containsSecret(el.id)) {
      return '#' + CSS.escape(el.id);
    }
    const tag = el.tagName.toLowerCase();
    if (el.className && typeof el.className === 'string') {
      const classes = el.className.trim().split(/\s+/).filter(c => c && !c.includes(':') && !containsSecret(c));
      if (classes.length > 0) {
        return tag + '.' + classes.map(c => CSS.escape(c)).slice(0, 3).join('.');
      }
    }
    const parent = el.parentElement;
    if (!parent) return tag;
    const siblings = Array.from(parent.children).filter(c => c.tagName === el.tagName);
    if (siblings.length > 1) {
      const index = siblings.indexOf(el) + 1;
      return buildSelector(parent) + ' > ' + tag + ':nth-of-type(' + index + ')';
    }
    return buildSelector(parent) + ' > ' + tag;
  }

  function buildElementPath(el) {
    const parts = [];
    let curr = el;
    let depth = 0;
    while (curr && curr !== document.body && curr !== document.documentElement && depth < 5) {
      const tag = curr.tagName.toLowerCase();
      let label = tag;
      if (curr.id && !containsSecret(curr.id)) {
        label += '#' + curr.id;
      } else if (curr.className && typeof curr.className === 'string') {
        const firstClass = curr.className.trim().split(/\s+/)[0];
        if (firstClass && !containsSecret(firstClass)) {
          label += '.' + firstClass;
        }
      }
      parts.unshift(label);
      curr = curr.parentElement;
      depth++;
    }
    return parts.join(' > ');
  }

  function getComputedStylesSubset(el) {
    const props = [
      'display', 'position', 'width', 'height', 'margin', 'padding',
      'color', 'backgroundColor', 'border', 'borderRadius',
      'fontFamily', 'fontSize', 'fontWeight', 'lineHeight', 'textAlign', 'zIndex'
    ];
    const out = {};
    try {
      const cs = window.getComputedStyle(el);
      for (const p of props) {
        const val = cs.getPropertyValue(p.replace(/([A-Z])/g, '-$1').toLowerCase());
        if (val && val !== 'none' && val !== 'auto' && val !== 'normal') {
          out[p] = val;
        }
      }
    } catch (e) {}
    return out;
  }

  function getNearbyText(el) {
    const nearby = [];
    if (!el || !el.parentElement) return nearby;
    const siblings = Array.from(el.parentElement.children);
    const index = siblings.indexOf(el);
    for (let i = Math.max(0, index - 2); i <= Math.min(siblings.length - 1, index + 2); i++) {
      if (i === index) continue;
      const text = siblings[i].innerText || siblings[i].textContent;
      if (text && text.trim().length > 0) {
        nearby.push(sanitizeText(text, 100));
      }
      if (nearby.length >= 4) break;
    }
    return nearby;
  }

  function getNearbyElements(el) {
    const elements = [];
    if (!el || !el.parentElement) return elements;
    const siblings = Array.from(el.parentElement.children);
    const index = siblings.indexOf(el);
    for (let i = Math.max(0, index - 2); i <= Math.min(siblings.length - 1, index + 2); i++) {
      if (i === index) continue;
      const s = siblings[i];
      const tag = s.tagName.toLowerCase();
      let desc = tag;
      if (s.id) desc += '#' + s.id;
      if (s.className && typeof s.className === 'string') {
        const c = s.className.trim().split(/\s+/)[0];
        if (c) desc += '.' + c;
      }
      elements.push(desc);
    }
    return elements;
  }

  function getHTMLSnippet(el) {
    try {
      const clone = el.cloneNode(true);
      const allInputs = clone.querySelectorAll ? Array.from(clone.querySelectorAll('input, textarea')) : [];
      if (clone.tagName && (clone.tagName.toLowerCase() === 'input' || clone.tagName.toLowerCase() === 'textarea')) {
        allInputs.push(clone);
      }
      allInputs.forEach(input => {
        const name = input.getAttribute('name') || '';
        const id = input.getAttribute('id') || '';
        const rawType = input.getAttribute('type') || '';
        const tag = input.tagName ? input.tagName.toLowerCase() : '';
        if (input.type === 'password' || containsSecret(rawType) || containsSecret(name) || containsSecret(id)) {
          input.value = '[redacted]';
          input.setAttribute('value', '[redacted]');
          if (tag === 'textarea') {
            input.textContent = '[redacted]';
          }
        }
      });

      const allEls = clone.querySelectorAll ? [clone, ...clone.querySelectorAll('*')] : [clone];
      allEls.forEach(elem => {
        if (!elem.attributes) return;
        for (let i = 0; i < elem.attributes.length; i++) {
          const attr = elem.attributes[i];
          if (containsSecret(attr.name) || containsSecret(attr.value)) {
            elem.setAttribute(attr.name, '[redacted]');
          }
        }
      });

      let html = clone.outerHTML || '';
      if (html.length > 2048) {
        html = html.slice(0, 2048) + '...';
      }
      return html;
    } catch (e) {
      return '';
    }
  }

  // --- 2. Progressive Multi-Framework Sniffer ---

  function sniffFramework(el) {
    // 1. Elixir Phoenix LiveView
    let curr = el;
    while (curr && curr !== document.body) {
      if (curr.hasAttribute('phx-click') || curr.hasAttribute('phx-change') || curr.hasAttribute('phx-submit') || curr.hasAttribute('data-phx-view')) {
        const view = curr.getAttribute('data-phx-view') || curr.getAttribute('phx-target') || 'LiveView';
        const event = curr.getAttribute('phx-click') || curr.getAttribute('phx-change') || curr.getAttribute('phx-submit') || '';
        return {
          name: 'Elixir Phoenix LiveView',
          component: view,
          sourceLocation: '',
          provenance: 'inferred',
          attributes: {
            'phx-view': view,
            'phx-event': event
          }
        };
      }
      curr = curr.parentElement;
    }

    // 2. Astro
    curr = el;
    while (curr && curr !== document.body) {
      if (curr.tagName && curr.tagName.toLowerCase() === 'astro-island') {
        const url = curr.getAttribute('component-url') || '';
        const exportName = curr.getAttribute('component-export') || '';
        return {
          name: 'Astro',
          component: exportName,
          sourceLocation: url,
          provenance: 'exact',
          attributes: { 'component-url': url }
        };
      }
      curr = curr.parentElement;
    }

    // 3. Svelte / SvelteKit
    curr = el;
    while (curr && curr !== document.body) {
      if (curr.__svelte_meta && curr.__svelte_meta.loc) {
        const loc = curr.__svelte_meta.loc;
        const file = loc.file + ':' + loc.line;
        return {
          name: 'Svelte',
          component: '',
          sourceLocation: file,
          provenance: 'exact'
        };
      }
      curr = curr.parentElement;
    }

    // 4. Vue / Nuxt
    curr = el;
    while (curr && curr !== document.body) {
      if (curr.__vueParentComponent) {
        const vnode = curr.__vueParentComponent;
        const compName = vnode.type?.__name || vnode.type?.name || '';
        return {
          name: 'Vue',
          component: compName ? '<' + compName + '>' : '',
          sourceLocation: '',
          provenance: 'inferred'
        };
      }
      curr = curr.parentElement;
    }

    // 5. HTMX
    curr = el;
    while (curr && curr !== document.body) {
      if (curr.hasAttribute('hx-get') || curr.hasAttribute('hx-post') || curr.hasAttribute('hx-target')) {
        const target = curr.getAttribute('hx-target') || '';
        const endpoint = curr.getAttribute('hx-get') || curr.getAttribute('hx-post') || '';
        return {
          name: 'HTMX',
          component: target,
          sourceLocation: endpoint,
          provenance: 'inferred'
        };
      }
      curr = curr.parentElement;
    }

    // 6. React (Fiber inspection)
    try {
      const keys = Object.keys(el);
      for (const k of keys) {
        if (k.startsWith('__reactFiber$') || k.startsWith('__reactInternalInstance$')) {
          let fiber = el[k];
          const components = [];
          let sourceLoc = '';
          let depth = 0;
          while (fiber && depth < 30) {
            const type = fiber.type || fiber.elementType;
            if (type && typeof type !== 'string') {
              const name = type.displayName || type.name;
              if (name && !/^(Fragment|Root|Provider|Consumer|Suspense)$/.test(name) && !components.includes(name)) {
                components.push(name);
              }
            }
            if (!sourceLoc && fiber._debugSource) {
              sourceLoc = fiber._debugSource.fileName + ':' + fiber._debugSource.lineNumber;
            }
            fiber = fiber.return;
            depth++;
          }
          return {
            name: 'React',
            component: components.length > 0 ? components.slice(0, 4).reverse().map(c => '<' + c + '>').join(' ') : '',
            sourceLocation: sourceLoc,
            provenance: sourceLoc ? 'exact' : 'inferred'
          };
        }
      }
    } catch (e) {}

    return {
      name: 'Static',
      component: '',
      sourceLocation: '',
      provenance: 'unavailable'
    };
  }

  // --- 3. Complete Target Payload Extraction ---

  function extractPayload(el) {
    const rect = el.getBoundingClientRect();
    const fw = sniffFramework(el);
    const scrollX = window.scrollX || window.pageXOffset || 0;
    const scrollY = window.scrollY || window.pageYOffset || 0;

    const role = el.getAttribute('role') || el.tagName.toLowerCase();
    const rawAccessibleName = el.getAttribute('aria-label') || el.getAttribute('alt') || (el.innerText ? el.innerText.trim().slice(0, 60) : '');
    const accessibleName = sanitizeText(rawAccessibleName, 80);

    return {
      page: {
        sanitizedUrl: sanitizeURL(window.location.href),
        title: document.title || '',
        viewportWidth: window.innerWidth,
        viewportHeight: window.innerHeight,
        scrollX: Math.round(scrollX),
        scrollY: Math.round(scrollY),
        capturedAt: new Date().toISOString()
      },
      target: {
        tagName: el.tagName.toLowerCase(),
        role: role,
        accessibleName: accessibleName,
        selector: buildSelector(el),
        elementPath: buildElementPath(el),
        fullPath: el.tagName.toLowerCase(),
        cssClasses: sanitizeText(el.className || '', 100),
        selectedText: window.getSelection() ? sanitizeText(window.getSelection().toString(), 100) : '',
        textSnippet: sanitizeText(el.innerText || el.textContent || '', 120),
        htmlSnippet: getHTMLSnippet(el),
        rectViewport: {
          x: Math.round(rect.x),
          y: Math.round(rect.y),
          width: Math.round(rect.width),
          height: Math.round(rect.height)
        },
        rectPage: {
          x: Math.round(rect.x + scrollX),
          y: Math.round(rect.y + scrollY),
          width: Math.round(rect.width),
          height: Math.round(rect.height)
        },
        isFixed: window.getComputedStyle(el).position === 'fixed',
        computedStyles: getComputedStylesSubset(el),
        framework: fw
      },
      nearbyText: getNearbyText(el),
      nearbyElements: getNearbyElements(el),
      ancestorPath: []
    };
  }

  // --- 4. Shadow DOM Overlay UI & Badge Pins ---

  class TetherReviewManager {
    constructor() {
      this.active = false;
      this.notes = [];
      this.markerElements = new Map();
      this.host = null;
      this.shadowRoot = null;
      this.reticle = null;
      this.tooltip = null;
      this.modal = null;
      this.hoveredEl = null;
      this.selectedEl = null;
      this.pendingPayload = null;
      this.rafId = 0;
      this.boundOnPointerMove = this.onPointerMove.bind(this);
      this.boundOnClick = this.onClick.bind(this);
      this.boundOnKeyDown = this.onKeyDown.bind(this);
    }

    ensureOverlay() {
      if (this.host && this.shadowRoot) return this.shadowRoot;

      const host = document.createElement('div');
      host.setAttribute('data-tether-annotation-overlay', '');
      host.style.cssText = 'position:fixed;inset:0;z-index:2147483646;pointer-events:none;overflow:hidden;';
      const shadow = host.attachShadow({ mode: 'closed' });

      const style = document.createElement('style');
      style.textContent = `
        .reticle {
          position: fixed;
          border: 2px solid #2563eb;
          background: rgba(37, 99, 235, 0.08);
          border-radius: 4px;
          pointer-events: none;
          transition: all 0.05s ease-out;
          display: none;
          z-index: 100;
        }
        .tooltip {
          position: fixed;
          background: #1e293b;
          color: #fff;
          font: 600 11px/1.4 -apple-system, BlinkMacSystemFont, sans-serif;
          padding: 4px 8px;
          border-radius: 6px;
          box-shadow: 0 4px 12px rgba(0,0,0,0.25);
          pointer-events: none;
          display: none;
          z-index: 101;
          white-space: nowrap;
        }
        .badge {
          position: absolute;
          width: 24px;
          height: 24px;
          background: #2563eb;
          color: #fff;
          border: 2px solid #fff;
          border-radius: 9999px;
          display: flex;
          align-items: center;
          justify-content: center;
          font: 700 11px/1 -apple-system, BlinkMacSystemFont, sans-serif;
          box-shadow: 0 4px 12px rgba(0,0,0,0.3);
          pointer-events: auto;
          cursor: pointer;
          user-select: none;
          z-index: 102;
        }
        .card {
          position: fixed;
          width: 320px;
          background: #ffffff;
          color: #0f172a;
          border: 1px solid #e2e8f0;
          border-radius: 12px;
          box-shadow: 0 20px 25px -5px rgba(0,0,0,0.1), 0 8px 10px -6px rgba(0,0,0,0.1);
          padding: 16px;
          font: 13px/1.5 -apple-system, BlinkMacSystemFont, sans-serif;
          pointer-events: auto;
          z-index: 200;
          display: none;
        }
        .card-header { font-weight: 700; font-size: 14px; margin-bottom: 8px; color: #1e293b; }
        .card-meta { font-size: 11px; color: #64748b; margin-bottom: 12px; font-family: monospace; }
        .card select, .card textarea {
          width: 100%; box-sizing: border-box; border: 1px solid #cbd5e1; border-radius: 6px;
          padding: 6px 8px; font-size: 13px; margin-bottom: 10px; font-family: inherit;
        }
        .card textarea { height: 72px; resize: vertical; }
        .card-actions { display: flex; justify-content: flex-end; gap: 8px; }
        .btn { padding: 6px 12px; border-radius: 6px; font-size: 12px; font-weight: 600; cursor: pointer; border: none; }
        .btn-primary { background: #2563eb; color: #fff; }
        .btn-secondary { background: #f1f5f9; color: #475569; }
      `;
      shadow.appendChild(style);

      const reticle = document.createElement('div');
      reticle.className = 'reticle';
      shadow.appendChild(reticle);
      this.reticle = reticle;

      const tooltip = document.createElement('div');
      tooltip.className = 'tooltip';
      shadow.appendChild(tooltip);
      this.tooltip = tooltip;

      const card = document.createElement('div');
      card.className = 'card';
      card.innerHTML = `
        <div class="card-header">Add Review Note</div>
        <div class="card-meta" id="card-meta"></div>
        <label style="font-size: 11px; font-weight: 600; color: #475569;">Intent</label>
        <select id="card-intent">
          <option value="design_fix">Design Fix</option>
          <option value="bug">Bug</option>
          <option value="clarification">Clarification</option>
        </select>
        <label style="font-size: 11px; font-weight: 600; color: #475569;">Feedback</label>
        <textarea id="card-comment" placeholder="What should be changed?"></textarea>
        <div class="card-actions">
          <button class="btn btn-secondary" id="card-cancel">Cancel</button>
          <button class="btn btn-primary" id="card-save">Save Note</button>
        </div>
      `;
      shadow.appendChild(card);
      this.modal = card;

      card.querySelector('#card-cancel').addEventListener('click', () => this.closeModal());
      card.querySelector('#card-save').addEventListener('click', () => this.saveModal());

      (document.body || document.documentElement).appendChild(host);
      this.host = host;
      this.shadowRoot = shadow;

      return shadow;
    }

    start() {
      this.active = true;
      this.ensureOverlay();
      window.addEventListener('mousemove', this.boundOnPointerMove, true);
      window.addEventListener('click', this.boundOnClick, true);
      window.addEventListener('keydown', this.boundOnKeyDown, true);
      this.startTracking();
    }

    stop() {
      this.active = false;
      window.removeEventListener('mousemove', this.boundOnPointerMove, true);
      window.removeEventListener('click', this.boundOnClick, true);
      window.removeEventListener('keydown', this.boundOnKeyDown, true);
      if (this.reticle) this.reticle.style.display = 'none';
      if (this.tooltip) this.tooltip.style.display = 'none';
      this.closeModal();
    }

    clear() {
      this.notes = [];
      this.markerElements.forEach(item => {
        if (item && item.element && typeof item.element.remove === 'function') {
          item.element.remove();
        }
      });
      this.markerElements.clear();
      try {
        document.querySelectorAll('[data-tether-pin]').forEach(el => el.removeAttribute('data-tether-pin'));
      } catch (e) {}
      this.closeModal();
    }

    onPointerMove(e) {
      if (!this.active || this.modalOpen) return;

      const target = document.elementFromPoint(e.clientX, e.clientY);
      if (!target || target === this.host || this.host.contains(target)) {
        return;
      }

      this.hoveredEl = target;
      const rect = target.getBoundingClientRect();
      const fw = sniffFramework(target);

      this.reticle.style.display = 'block';
      this.reticle.style.left = rect.left + 'px';
      this.reticle.style.top = rect.top + 'px';
      this.reticle.style.width = rect.width + 'px';
      this.reticle.style.height = rect.height + 'px';

      let label = target.tagName.toLowerCase();
      if (fw.Component) label = fw.Component + ' (' + label + ')';
      if (fw.SourceLocation) label += ' • ' + fw.SourceLocation;

      this.tooltip.style.display = 'block';
      this.tooltip.textContent = label;
      this.tooltip.style.left = Math.max(10, Math.min(window.innerWidth - 200, rect.left)) + 'px';
      this.tooltip.style.top = Math.max(10, rect.top - 30) + 'px';
    }

    onClick(e) {
      if (!this.active) return;
      if (this.modalOpen && this.modal.contains(e.target)) return;

      const target = document.elementFromPoint(e.clientX, e.clientY);
      if (!target || target === this.host || this.host.contains(target)) return;

      e.preventDefault();
      e.stopPropagation();

      this.selectedEl = target;
      this.pendingPayload = extractPayload(target);
      this.openModal(e.clientX, e.clientY);
    }

    onKeyDown(e) {
      if (e.key === 'Escape') {
        if (this.modalOpen) {
          this.closeModal();
        } else if (this.active) {
          this.stop();
        }
      }
    }

    openModal(clickX, clickY) {
      this.modalOpen = true;
      const card = this.modal;
      const meta = card.querySelector('#card-meta');
      const target = this.pendingPayload.target;
      const fw = target.framework;

      meta.textContent = `${fw.Component || target.tagName} • ${fw.SourceLocation || target.selector}`;
      card.querySelector('#card-comment').value = '';

      card.style.display = 'block';
      card.style.left = Math.max(20, Math.min(window.innerWidth - 340, clickX + 10)) + 'px';
      card.style.top = Math.max(20, Math.min(window.innerHeight - 300, clickY + 10)) + 'px';

      setTimeout(() => card.querySelector('#card-comment').focus(), 50);
    }

    closeModal() {
      this.modalOpen = false;
      if (this.modal) this.modal.style.display = 'none';
      this.selectedEl = null;
      this.pendingPayload = null;
    }

    saveModal() {
      if (!this.pendingPayload || !this.selectedEl) return;
      const intent = this.modal.querySelector('#card-intent').value;
      const comment = this.modal.querySelector('#card-comment').value.trim();

      const note = {
        id: 'note-' + (this.notes.length + 1),
        index: this.notes.length + 1,
        intent: intent,
        comment: comment,
        createdAt: new Date().toISOString(),
        payload: this.pendingPayload,
        targetEl: this.selectedEl
      };
      try {
        this.selectedEl.setAttribute('data-tether-pin', note.id);
      } catch (e) {}

      this.notes.push(note);
      this.createBadge(note);
      this.closeModal();
    }

    createBadge(note) {
      const badge = document.createElement('div');
      badge.className = 'badge';
      badge.textContent = String(note.index);
      this.shadowRoot.appendChild(badge);
      this.markerElements.set(note.id, { element: badge, note: note });
      this.updatePositions();
    }

    updatePositions() {
      const scrollX = window.scrollX || window.pageXOffset || 0;
      const scrollY = window.scrollY || window.pageYOffset || 0;

      this.markerElements.forEach(({ element, note }) => {
        if (!note.targetEl || !note.targetEl.isConnected) {
          element.style.display = 'none';
          return;
        }
        const rect = note.targetEl.getBoundingClientRect();
        if (rect.width === 0 && rect.height === 0) {
          element.style.display = 'none';
          return;
        }
        element.style.display = 'flex';
        element.style.left = (rect.left + scrollX + rect.width / 2 - 12) + 'px';
        element.style.top = (rect.top + scrollY - 12) + 'px';
      });
    }

    startTracking() {
      const update = () => {
        if (!this.active) return;
        this.updatePositions();
        this.rafId = requestAnimationFrame(update);
      };
      this.rafId = requestAnimationFrame(update);
    }

    getNotes() {
      return this.notes.map(n => ({
        id: n.id,
        index: n.index,
        intent: n.intent,
        comment: n.comment,
        createdAt: n.createdAt,
        payload: n.payload
      }));
    }
  }

  window.__tetherReview = new TetherReviewManager();
})();
