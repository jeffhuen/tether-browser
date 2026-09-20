(() => {
  'use strict';

  if (window.__tetherReview) {
    return;
  }

  const SECRET_PATTERNS = [
    /access_token/i,
    /auth_token/i,
    /refresh_token/i,
    /refresh_?token/i,
    /id_token/i,
    /session_?token/i,
    /\btoken\b/i,
    /auth_code/i,
    /oauth_state/i,
    /[?&](code|state)=/i,
    /\bnonce\b/i,
    /api_?key/i,
    /client_secret/i,
    /x-amz-/i,
    /session_?id/i,
    /csrf/i,
    /secret/i,
    /password/i,
    /passwd/i,
    /bearer/i,
    /\bjwt\b/i,
    /\botp\b/i,
    /\btotp\b/i,
    /credit_?card/i,
    /card_?number/i,
    /\bcvv\b/i,
    /\bssn\b/i
  ];

  function containsSecret(str) {
    if (!str || typeof str !== 'string') return false;
    return SECRET_PATTERNS.some(p => p.test(str));
  }

  function sanitizeText(str, maxLen = 200) {
    if (!str) return '';
    let s = String(str).replace(/\s+/g, ' ').trim();
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
      const base = document.baseURI || window.location.href;
      const isRelative = !rawURL.includes('://');
      const u = new URL(rawURL, base);
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
      if (isRelative) {
        return u.pathname + (u.search ? u.search : '');
      }
      return u.toString();
    } catch (e) {
      return rawURL.split('#')[0].split('?')[0];
    }
  }

  function extractSanitizedText(elem) {
    if (!elem) return '';
    try {
      const clone = elem.cloneNode(true);
      const allInputs = clone.querySelectorAll ? Array.from(clone.querySelectorAll('input, textarea')) : [];
      if (clone.tagName && (clone.tagName.toLowerCase() === 'input' || clone.tagName.toLowerCase() === 'textarea')) {
        allInputs.push(clone);
      }
      allInputs.forEach(input => {
        const name = input.getAttribute('name') || '';
        const id = input.getAttribute('id') || '';
        const rawType = (input.getAttribute('type') || '').toLowerCase();
        const tag = input.tagName ? input.tagName.toLowerCase() : '';
        const isSecret = rawType === 'password' ||
          containsSecret(rawType) ||
          containsSecret(name) ||
          containsSecret(id) ||
          containsSecret(input.value) ||
          (tag === 'textarea' && containsSecret(input.textContent));
        if (isSecret) {
          if (rawType === 'file' || input.type === 'file') {
            input.value = '';
          } else {
            input.value = '[redacted]';
            input.setAttribute('value', '[redacted]');
          }
          if (tag === 'textarea') {
            input.textContent = '[redacted]';
          }
        }
      });

      const allEls = clone.querySelectorAll ? [clone, ...clone.querySelectorAll('*')] : [clone];
      allEls.forEach(el => {
        if (!el.attributes) return;
        for (let i = 0; i < el.attributes.length; i++) {
          const attr = el.attributes[i];
          if (containsSecret(attr.name) || containsSecret(attr.value)) {
            el.setAttribute(attr.name, '[redacted]');
          }
        }
      });

      const text = clone.innerText || clone.textContent || '';
      return sanitizeText(text, 120);
    } catch (e) {
      return '[redacted]';
    }
  }

  // --- 1. Universal DOM Baseline Extractor ---
  function looksHashy(value) {
    return /^[A-Za-z0-9_-]{12,}$/.test(value) && /\d/.test(value) && /[A-Z]/.test(value);
  }

  function getStableClasses(el, maxCount = 2) {
    if (!el || !el.classList) return [];
    const result = [];
    for (let i = 0; i < el.classList.length && result.length < maxCount; i++) {
      const cls = el.classList[i];
      if (!cls || cls.length > 60 || containsSecret(cls)) continue;
      if (/^css-[a-z0-9]+$/i.test(cls) || looksHashy(cls)) continue;
      result.push(cls);
    }
    return result;
  }

  function buildSelector(el) {
    if (!el || el.nodeType !== Node.ELEMENT_NODE) return '';
    if (el.id && !containsSecret(el.id)) {
      const sel = '#' + CSS.escape(el.id);
      try {
        if (document.querySelectorAll(sel).length === 1) return sel;
      } catch (e) {}
    }
    const tag = el.tagName.toLowerCase();
    let sel = tag;
    const classes = getStableClasses(el, 2);
    if (classes.length > 0) {
      sel = tag + '.' + classes.map(c => CSS.escape(c)).join('.');
      try {
        if (document.querySelectorAll(sel).length === 1) return sel;
      } catch (e) {}
    }
    const parent = el.parentElement;
    if (!parent) return sel;
    const siblings = Array.from(parent.children).filter(c => c.tagName === el.tagName);
    const index = siblings.indexOf(el) + 1;
    if (parent === document.documentElement) {
      return tag + ':nth-of-type(' + index + ')';
    }
    const parentSel = buildSelector(parent);
    if (parentSel) {
      return parentSel + ' > ' + tag + ':nth-of-type(' + index + ')';
    }
    return tag + ':nth-of-type(' + index + ')';
  }

  function getAccessibility(el) {
    const role = el.getAttribute('role') || el.tagName.toLowerCase();
    let accessibleName = '';

    // 1. aria-labelledby takes top precedence
    const ariaLabelledBy = el.getAttribute('aria-labelledby');
    if (ariaLabelledBy) {
      const ids = ariaLabelledBy.trim().split(/\s+/);
      const names = [];
      for (const id of ids) {
        const ref = document.getElementById(id);
        if (ref) {
          const text = ref.innerText || ref.textContent;
          if (text) names.push(text.trim());
        }
      }
      if (names.length > 0) {
        accessibleName = names.join(' ');
      }
    }

    // 2. aria-label
    if (!accessibleName) {
      const ariaLabel = el.getAttribute('aria-label');
      if (ariaLabel) {
        accessibleName = ariaLabel.trim();
      }
    }

    // 3. HTML label for form controls
    if (!accessibleName && el.id) {
      try {
        const label = document.querySelector('label[for="' + CSS.escape(el.id) + '"]');
        if (label) {
          accessibleName = (label.innerText || label.textContent || '').trim();
        }
      } catch (e) {}
    }
    if (!accessibleName) {
      const parentLabel = el.closest('label');
      if (parentLabel) {
        accessibleName = (parentLabel.innerText || parentLabel.textContent || '').trim();
      }
    }

    // 4. title, alt, placeholder
    if (!accessibleName && el.getAttribute('title')) {
      accessibleName = el.getAttribute('title').trim();
    }
    if (!accessibleName && el.getAttribute('alt')) {
      accessibleName = el.getAttribute('alt').trim();
    }
    if (!accessibleName && el.getAttribute('placeholder')) {
      accessibleName = el.getAttribute('placeholder').trim();
    }

    // 5. innerText for non-input elements
    if (!accessibleName && el.tagName.toLowerCase() !== 'input' && el.innerText) {
      accessibleName = el.innerText.trim().slice(0, 60);
    }

    return {
      role: role,
      accessibleName: sanitizeText(accessibleName, 80)
    };
  }

  function isElementFixed(el) {
    let curr = el;
    while (curr && curr !== document.body && curr !== document.documentElement) {
      try {
        const pos = window.getComputedStyle(curr).position;
        if (pos === 'fixed' || pos === 'sticky') return true;
      } catch (e) {}
      curr = curr.parentElement;
    }
    return false;
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
      const text = extractSanitizedText(siblings[i]);
      if (text && text.trim().length > 0 && text !== '[redacted]') {
        nearby.push(text);
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
      if (s.id && !containsSecret(s.id)) desc += '#' + s.id;
      if (s.className && typeof s.className === 'string') {
        const c = s.className.trim().split(/\s+/)[0];
        if (c && !containsSecret(c)) desc += '.' + c;
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
        const isSecret = input.type === 'password' ||
          containsSecret(rawType) ||
          containsSecret(name) ||
          containsSecret(id) ||
          containsSecret(input.value) ||
          (tag === 'textarea' && containsSecret(input.textContent));
        if (isSecret) {
          if (input.type === 'file' || rawType.toLowerCase() === 'file') {
            input.value = '';
          } else {
            input.value = '[redacted]';
            input.setAttribute('value', '[redacted]');
          }
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
        const url = sanitizeURL(curr.getAttribute('component-url') || '');
        const exportName = curr.getAttribute('component-export') || '';
        return {
          name: 'Astro',
          component: exportName,
          Component: exportName,
          sourceLocation: url,
          SourceLocation: url,
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
        const endpoint = sanitizeURL(curr.getAttribute('hx-get') || curr.getAttribute('hx-post') || '');
        return {
          name: 'HTMX',
          component: target,
          Component: target,
          sourceLocation: endpoint,
          SourceLocation: endpoint,
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
  function getAncestorPath(el) {
    const path = [];
    let curr = el ? el.parentElement : null;
    while (curr && curr !== document.documentElement && path.length < 8) {
      const tag = curr.tagName.toLowerCase();
      const role = curr.getAttribute('role');
      path.push(role ? tag + '[role=' + role + ']' : tag);
      curr = curr.parentElement;
    }
    return path;
  }

  // --- 3. Complete Target Payload Extraction ---

  function extractPayload(el) {
    const rect = el.getBoundingClientRect();
    const fw = sniffFramework(el);
    const scrollX = window.scrollX || window.pageXOffset || 0;
    const scrollY = window.scrollY || window.pageYOffset || 0;

    const ax = getAccessibility(el);
    const role = ax.role;
    const accessibleName = ax.accessibleName;

    const textSnippet = extractSanitizedText(el);

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
        textSnippet: textSnippet,
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
        isFixed: isElementFixed(el),
        computedStyles: getComputedStylesSubset(el),
        framework: fw
      },
      nearbyText: getNearbyText(el),
      nearbyElements: getNearbyElements(el),
      ancestorPath: getAncestorPath(el)
    };
  }

  // --- 4. Shadow DOM Overlay UI & Badge Pins ---

  class TetherReviewManager {
    constructor() {
      this.active = false;
      this.armed = false;
      this.notes = [];
      this.markerElements = new Map();
      this.host = null;
      this.shadowRoot = null;
      this.reticle = null;
      this.tooltip = null;
      this.modal = null;
      this.dock = null;
      this.dockSelect = null;
      this.dockCount = null;
      this.summary = null;
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
      host.style.cssText = 'position:fixed;inset:0;z-index:2147483646;pointer-events:none;overflow:hidden;cursor:default;';
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
        .btn-primary { background: #2563eb; color: #fff; }
        .btn-secondary { background: #f1f5f9; color: #475569; }
        .dock {
          position: fixed;
          left: 50%;
          bottom: 16px;
          transform: translateX(-50%);
          display: none;
          align-items: center;
          gap: 8px;
          background: #0f172a;
          color: #fff;
          border-radius: 9999px;
          padding: 6px 8px 6px 14px;
          font: 600 12px/1 -apple-system, BlinkMacSystemFont, sans-serif;
          box-shadow: 0 10px 24px rgba(0,0,0,.35);
          pointer-events: auto;
          z-index: 150;
          user-select: none;
          white-space: nowrap;
        }
        .dock-title { opacity: .7; }
        .dock-count { background: #2563eb; border-radius: 9999px; min-width: 20px; height: 20px; display: inline-flex; align-items: center; justify-content: center; padding: 0 6px; }
        .dock-btn { border: none; border-radius: 9999px; padding: 6px 12px; font: inherit; cursor: pointer; background: #2563eb; color: #fff; }
        .dock-btn.active { background: #16a34a; }
        .dock-done { background: transparent; color: #cbd5e1; }
        .summary {
          position: fixed;
          left: 50%;
          bottom: 64px;
          transform: translateX(-50%);
          width: 360px;
          max-height: 240px;
          overflow-y: auto;
          background: #0f172a;
          color: #e2e8f0;
          border-radius: 12px;
          box-shadow: 0 10px 24px rgba(0,0,0,.35);
          pointer-events: auto;
          z-index: 160;
          font: 12px/1.5 -apple-system, BlinkMacSystemFont, sans-serif;
          padding: 10px 12px;
          display: none;
          user-select: none;
        }
        .summary-header { display: flex; justify-content: space-between; align-items: center; font-weight: 700; margin-bottom: 6px; }
        .summary-row { padding: 4px 6px; border-radius: 6px; cursor: pointer; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
        .summary-row:hover { background: #1e293b; }
        .summary-empty { opacity: .6; }
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

      card.addEventListener('click', (e) => {
        e.stopPropagation();
      });
      card.addEventListener('wheel', (e) => {
        e.stopPropagation();
      });
      card.addEventListener('keydown', (e) => {
        e.stopPropagation();
      });
      card.addEventListener('keyup', (e) => {
        e.stopPropagation();
      });
      card.addEventListener('keypress', (e) => {
        e.stopPropagation();
      });

      card.querySelector('#card-cancel').addEventListener('click', () => this.closeModal());
      card.querySelector('#card-save').addEventListener('click', () => this.saveModal());

      const textarea = card.querySelector('#card-comment');
      textarea.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
          e.preventDefault();
          this.saveModal();
        }
      });

      const dock = document.createElement('div');
      dock.className = 'dock';
      dock.innerHTML = `
        <span class="dock-title">Tether</span>
        <button class="dock-btn" id="dock-select" title="Select an element to annotate">⊕ Select</button>
        <span class="dock-count" id="dock-count">0</span>
        <button class="dock-btn" id="dock-list" title="Show all pins">☰ List</button>
        <button class="dock-btn dock-done" id="dock-done" title="End review session">✕ Done</button>
      `;
      shadow.appendChild(dock);
      this.dock = dock;
      this.dockSelect = dock.querySelector('#dock-select');
      this.dockCount = dock.querySelector('#dock-count');
      dock.addEventListener('click', (e) => { e.stopPropagation(); });
      dock.querySelector('#dock-select').addEventListener('click', () => this.setArmed(true));
      dock.querySelector('#dock-list').addEventListener('click', () => this.toggleSummary());
      dock.querySelector('#dock-done').addEventListener('click', () => this.stop());

      const summary = document.createElement('div');
      summary.className = 'summary';
      summary.innerHTML = `
        <div class="summary-header"><span>Review notes</span><button class="dock-btn" id="summary-copy" title="Copy all notes to clipboard">⧉ Copy all</button></div>
        <div class="summary-list" id="summary-list"></div>
      `;
      shadow.appendChild(summary);
      this.summary = summary;
      summary.addEventListener('click', (e) => { e.stopPropagation(); });
      summary.querySelector('#summary-copy').addEventListener('click', () => this.copyAll());
      const mount = () => {
        if (host.isConnected) return;
        const parent = document.body || document.documentElement;
        if (parent) {
          parent.appendChild(host);
        } else {
          setTimeout(mount, 20);
        }
      };
      mount();
      this.host = host;
      this.shadowRoot = shadow;

      return shadow;
    }

    start(armed = true) {
      this.active = true;
      try { window.sessionStorage.removeItem('tether-review-off'); } catch (e) {}
      this.ensureOverlay();
      if (this.modalOpen) this.closeModal();
      this.markerElements.forEach(entry => {
        if (entry && entry.element) entry.element.style.pointerEvents = 'auto';
      });
      window.addEventListener('mousemove', this.boundOnPointerMove, true);
      window.addEventListener('click', this.boundOnClick, true);
      window.addEventListener('keydown', this.boundOnKeyDown, true);
      if (this.dock) this.dock.style.display = 'flex';
      if (this.summary) this.summary.style.display = 'none';
      this.updateDockCount();
      this.renderSummary();
      this.setArmed(armed);
      this.startTracking();
    }
    startPassive() {
      this.start(false);
    }

    startCropMode() {
      this.ensureOverlay();
      if (this.dock) this.dock.style.display = 'none';
      if (this.summary) this.summary.style.display = 'none';

      let cropBadge = this.shadow ? this.shadow.querySelector('#tether-crop-badge') : null;
      if (!cropBadge && this.shadow) {
        cropBadge = document.createElement('div');
        cropBadge.id = 'tether-crop-badge';
        cropBadge.style.cssText = 'position:fixed;top:16px;left:50%;transform:translateX(-50%);background:#0f172a;color:#38bdf8;padding:8px 16px;border-radius:999px;font-size:12px;font-weight:600;box-shadow:0 8px 24px rgba(0,0,0,0.6);border:1px solid rgba(56,189,248,0.4);z-index:2147483647;pointer-events:none;display:flex;align-items:center;gap:8px;font-family:-apple-system,BlinkMacSystemFont,sans-serif;';
        cropBadge.innerHTML = '<span>📸 Click element to crop screenshot</span><span style="color:#94a3b8;font-size:10px;">(Esc to cancel)</span>';
        this.shadow.appendChild(cropBadge);
      } else if (cropBadge) {
        cropBadge.style.display = 'flex';
      }

      const onCropMove = (e) => {
        const el = this.leafTargetFromPoint(e.clientX, e.clientY);
        if (!el || el === this.host || (el.closest && el.closest('#tether-review-root'))) {
          if (this.reticle) this.reticle.style.display = 'none';
          return;
        }
        const rect = el.getBoundingClientRect();
        if (this.reticle) {
          this.reticle.style.display = 'block';
          this.reticle.style.left = rect.left + 'px';
          this.reticle.style.top = rect.top + 'px';
          this.reticle.style.width = rect.width + 'px';
          this.reticle.style.height = rect.height + 'px';
          this.reticle.style.borderColor = '#00f5ff';
          this.reticle.style.boxShadow = '0 0 16px rgba(0, 245, 255, 0.5)';
        }
      };

      const cleanup = () => {
        window.removeEventListener('mousemove', onCropMove, true);
        window.removeEventListener('click', onCropClick, true);
        window.removeEventListener('keydown', onCropKey, true);
        if (this.reticle) this.reticle.style.display = 'none';
        if (cropBadge) cropBadge.style.display = 'none';
        if (this.dock) this.dock.style.display = 'flex';
      };

      const onCropClick = (e) => {
        e.preventDefault();
        e.stopPropagation();
        const el = this.leafTargetFromPoint(e.clientX, e.clientY);
        if (!el || el === this.host || (el.closest && el.closest('#tether-review-root'))) {
          cleanup();
          return;
        }
        const rect = el.getBoundingClientRect();
        const scrollX = window.scrollX || window.pageXOffset || 0;
        const scrollY = window.scrollY || window.pageYOffset || 0;
        const selector = buildSelector(el) || (el.tagName ? el.tagName.toLowerCase() : 'element');
        const detail = {
          rect: {
            x: rect.left + scrollX,
            y: rect.top + scrollY,
            width: rect.width,
            height: rect.height,
          },
          selector,
          tagName: el.tagName ? el.tagName.toLowerCase() : 'element',
          title: document.title,
          url: window.location.href,
        };
        cleanup();
        try {
          window.sessionStorage.setItem('tether_last_crop', JSON.stringify(detail));
        } catch {}
      };

      const onCropKey = (e) => {
        if (e.key === 'Escape') {
          cleanup();
        }
      };

      window.addEventListener('mousemove', onCropMove, true);
      window.addEventListener('click', onCropClick, true);
      window.addEventListener('keydown', onCropKey, true);
    }

    toggleSummary() {
      if (!this.summary) return;
      const show = this.summary.style.display === 'none';
      if (show) this.renderSummary();
      this.summary.style.display = show ? 'block' : 'none';
    }

    noteLabel(n) {
      if (!n || !n.payload || !n.payload.target) return 'note';
      const t = n.payload.target;
      return t.accessibleName || (t.tagName + (t.cssClasses ? '.' + t.cssClasses.split(/\s+/)[0] : ''));
    }

    renderSummary() {
      if (!this.summary) return;
      const list = this.summary.querySelector('#summary-list');
      if (!list) return;
      list.innerHTML = '';
      if (this.notes.length === 0) {
        list.innerHTML = '<div class="summary-empty">No pins yet — press Select, then click an element.</div>';
        return;
      }
      this.notes.forEach(n => {
        const row = document.createElement('div');
        row.className = 'summary-row';
        row.textContent = '[' + n.index + '] ' + this.noteLabel(n) + ' — ' + (n.comment || n.intent || '');
        row.title = 'Open note ' + n.index;
        row.addEventListener('click', () => this.openExistingModal(n));
        list.appendChild(row);
      });
    }
    formatReport() {
      return formatDesignFeedbackReport(this.notes);
    }


    buildSummaryText() {
      return formatDesignFeedbackReport(this.notes);
    }

    copyAll() {
      const text = this.buildSummaryText();
      if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
        navigator.clipboard.writeText(text).catch(() => this.fallbackCopy(text));
      } else {
        this.fallbackCopy(text);
      }
    }

    fallbackCopy(text) {
      try {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.cssText = 'position:fixed;opacity:0;';
        (document.body || document.documentElement).appendChild(ta);
        ta.select();
        document.execCommand('copy');
        ta.remove();
      } catch (e) {}
    }

    stop() {
      this.active = false;
      try { window.sessionStorage.setItem('tether-review-off', '1'); } catch (e) {}
      this.setArmed(false);
      if (this.dock) this.dock.style.display = 'none';
      if (this.summary) this.summary.style.display = 'none';
      if (this.rafId) {
        cancelAnimationFrame(this.rafId);
        this.rafId = 0;
      }
      if (this.host) {
        this.host.style.pointerEvents = 'none';
        this.host.style.cursor = 'default';
      }
      this.markerElements.forEach(entry => {
        if (entry && entry.element) entry.element.style.pointerEvents = 'none';
      });
      window.removeEventListener('mousemove', this.boundOnPointerMove, true);
      window.removeEventListener('click', this.boundOnClick, true);
      window.removeEventListener('keydown', this.boundOnKeyDown, true);
      if (this.reticle) this.reticle.style.display = 'none';
      if (this.tooltip) this.tooltip.style.display = 'none';
      this.closeModal();
    }
    clear() {
      // Remove data-tether-pin from all target elements before clearing notes array
      this.notes.forEach(note => {
        if (note && note.targetEl && typeof note.targetEl.removeAttribute === 'function') {
          try {
            note.targetEl.removeAttribute('data-tether-pin');
          } catch (e) {}
        }
      });
      this.notes = [];

      this.markerElements.forEach(item => {
        if (item && item.element && typeof item.element.remove === 'function') {
          item.element.remove();
        }
        if (item && item.note && item.note.targetEl && typeof item.note.targetEl.removeAttribute === 'function') {
          try {
            item.note.targetEl.removeAttribute('data-tether-pin');
          } catch (e) {}
        }
      });
      this.markerElements.clear();
      this.updateDockCount();
      this.renderSummary();
      try {
        document.querySelectorAll('[data-tether-pin]').forEach(el => el.removeAttribute('data-tether-pin'));
      } catch (e) {}

      this.closeModal();
    }

    getElementUnderPointer(x, y) {
      if (typeof document.elementsFromPoint === 'function') {
        const els = document.elementsFromPoint(x, y);
        const valid = [];
        for (let i = 0; i < els.length; i++) {
          const el = els[i];
          if (el && el !== this.host && !this.host.contains(el) && el !== document.body && el !== document.documentElement) {
            valid.push(el);
          }
        }
        if (valid.length === 0) return null;

        // Leaf snapping: prioritize interactive or content leaf elements over broad layout containers
        const leafTags = new Set(['a', 'button', 'input', 'select', 'textarea', 'label', 'img', 'svg', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'p', 'span', 'strong', 'em', 'code', 'b', 'i', 'li', 'td', 'th']);
        for (let i = 0; i < valid.length; i++) {
          const tag = valid[i].tagName.toLowerCase();
          if (leafTags.has(tag)) {
            return valid[i];
          }
        }

        // Otherwise pick the most specific (smallest area) element
        let best = valid[0];
        let bestArea = Infinity;
        for (let i = 0; i < Math.min(valid.length, 5); i++) {
          const r = valid[i].getBoundingClientRect();
          const area = r.width * r.height;
          if (area > 0 && area < bestArea) {
            bestArea = area;
            best = valid[i];
          }
        }
        return best;
      }
      return null;
    }

    clearNotes() {
      this.clear();
    }

    onPointerMove(e) {
      if (!this.active || !this.armed || this.modalOpen) return;

      const target = this.getElementUnderPointer(e.clientX, e.clientY);
      if (!target) {
        if (this.reticle) this.reticle.style.display = 'none';
        if (this.tooltip) this.tooltip.style.display = 'none';
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
      const cName = fw.component || fw.Component;
      const sLoc = fw.sourceLocation || fw.SourceLocation;
      if (cName) label = cName + ' (' + label + ')';
      if (sLoc) label += ' • ' + sLoc;

      this.tooltip.style.display = 'block';
      this.tooltip.textContent = label;
      this.tooltip.style.left = Math.max(10, Math.min(window.innerWidth - 200, rect.left)) + 'px';
      this.tooltip.style.top = Math.max(10, rect.top - 30) + 'px';
    }

    onKeyDown(e) {
      if (this.modalOpen) {
        const path = e.composedPath ? e.composedPath() : [];
        const inCard = path.some(node => node && node.classList && node.classList.contains('card'));
        if (inCard || e.target === this.host) {
          if (e.key === 'Escape') {
            e.preventDefault();
            e.stopPropagation();
            e.stopImmediatePropagation();
            this.closeModal();
            return;
          }
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            e.stopPropagation();
            e.stopImmediatePropagation();
            this.saveModal();
            return;
          }
          e.stopPropagation();
          e.stopImmediatePropagation();
          return;
        }
      }
      if (e.key === 'Escape') {
        if (this.modalOpen) {
          this.closeModal();
        } else if (this.active) {
          this.stop();
        }
      }
    }

    isEventInModal(e) {
      if (!this.modalOpen || !this.modal) return false;
      if (e.target !== this.host && (!this.host || !this.host.contains(e.target))) {
        return false;
      }
      const r = this.modal.getBoundingClientRect();
      return (e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom);
    }

    findBadgeAtPoint(x, y) {
      const entries = Array.from(this.markerElements.values());
      for (let i = entries.length - 1; i >= 0; i--) {
        const entry = entries[i];
        if (!entry || !entry.element) continue;
        const r = entry.element.getBoundingClientRect();
        if (x >= r.left && x <= r.right && y >= r.top && y <= r.bottom) {
          return entry.note;
        }
      }
      return null;
    }

    setArmed(on) {
      this.armed = !!on;
      if (this.host) {
        this.host.style.pointerEvents = this.armed ? 'all' : 'none';
        this.host.style.cursor = this.armed ? 'crosshair' : 'default';
      }
      if (!this.armed) {
        if (this.reticle) this.reticle.style.display = 'none';
        if (this.tooltip) this.tooltip.style.display = 'none';
      }
      if (this.dockSelect) this.dockSelect.classList.toggle('active', this.armed);
    }

    hitChrome(el, x, y) {
      if (!el || el.style.display === 'none') return false;
      const r = el.getBoundingClientRect();
      return (x >= r.left && x <= r.right && y >= r.top && y <= r.bottom);
    }

    isEventInDock(e) {
      if (e.target !== this.host && (!this.host || !this.host.contains(e.target))) return false;
      if (this.hitChrome(this.dock, e.clientX, e.clientY)) return true;
      return this.hitChrome(this.summary, e.clientX, e.clientY);
    }

    updateDockCount() {
      if (this.dockCount) this.dockCount.textContent = String(this.notes.length);
    }
    onClick(e) {
      if (!this.active) return;
      if (this.isEventInModal(e)) {
        return;
      }
      if (this.isEventInDock(e)) {
        return;
      }
      if (!this.armed) return;

      e.preventDefault();
      e.stopPropagation();
      e.stopImmediatePropagation();
      const isOverlayTarget = e.target === this.host || (this.host && this.host.contains(e.target));
      const hitNote = isOverlayTarget ? this.findBadgeAtPoint(e.clientX, e.clientY) : null;
      if (hitNote) {
        this.openExistingModal(hitNote);
        return;
      }

      if (this.modalOpen) {
        this.closeModal();
        this.setArmed(false);
        return;
      }

      const target = (e.target && e.target !== this.host && e.target !== document.documentElement && e.target !== document.body)
        ? e.target
        : this.getElementUnderPointer(e.clientX, e.clientY);
      if (!target || target === document.documentElement || target === document.body) return;

      this.selectedEl = target;
      this.pendingPayload = extractPayload(target);
      this.openModal(e.clientX, e.clientY);
    }

    openModal(clickX, clickY) {
      this.modalOpen = true;
      this.editingNote = null;
      const card = this.modal;
      const meta = card.querySelector('#card-meta');
      const target = this.pendingPayload.target;
      const fw = target.framework;

      const cName = fw.component || fw.Component || target.tagName;
      const sLoc = fw.sourceLocation || fw.SourceLocation || target.selector;
      meta.textContent = `${cName} • ${sLoc}`;
      card.querySelector('#card-comment').value = '';

      if (this.host) this.host.style.cursor = 'default';
      this.setArmed(false);
      card.style.display = 'block';
      card.style.left = Math.max(20, Math.min(window.innerWidth - 340, clickX + 10)) + 'px';
      card.style.top = Math.max(20, Math.min(window.innerHeight - 300, clickY + 10)) + 'px';

      setTimeout(() => card.querySelector('#card-comment').focus(), 50);
    }

    closeModal() {
      this.modalOpen = false;
      this.editingNote = null;
      if (this.host) this.host.style.cursor = (this.active && this.armed) ? 'crosshair' : 'default';
      if (this.modal) this.modal.style.display = 'none';
      this.selectedEl = null;
      this.pendingPayload = null;
    }

    saveModal() {
      if (this.editingNote) {
        this.editingNote.intent = this.modal.querySelector('#card-intent').value;
        this.editingNote.comment = this.modal.querySelector('#card-comment').value.trim();
        const entry = this.markerElements.get(this.editingNote.id);
        if (entry && entry.element) {
          entry.element.title = 'Pin [' + this.editingNote.index + ']: ' + (this.editingNote.comment || this.editingNote.intent) + ' (click to view/edit)';
        }
        this.editingNote = null;
        this.closeModal();
        return;
      }
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
        const existingPins = (this.selectedEl.getAttribute('data-tether-pin') || '').split(/\s+/).filter(Boolean);
        if (!existingPins.includes(note.id)) {
          existingPins.push(note.id);
          this.selectedEl.setAttribute('data-tether-pin', existingPins.join(' '));
        }
      } catch (e) {}
      this.notes.push(note);
      this.createBadge(note);
      this.updateDockCount();
      this.renderSummary();
      this.closeModal();
    }

    createBadge(note) {
      const badge = document.createElement('div');
      badge.className = 'badge';
      badge.textContent = String(note.index);
      badge.title = 'Pin [' + note.index + ']: ' + (note.comment || note.intent) + ' (click to view/edit)';
      badge.style.pointerEvents = this.active ? 'auto' : 'none';
      badge.addEventListener('click', (e) => {
        e.stopPropagation();
        if (!this.active) return;
        this.openExistingModal(note);
      });
      this.shadowRoot.appendChild(badge);
      this.markerElements.set(note.id, { element: badge, note: note });
      this.updatePositions();
    }

    openExistingModal(note) {
      if (!this.active || !note) return;
      this.modalOpen = true;
      this.editingNote = note;
      const card = this.modal;
      const meta = card.querySelector('#card-meta');
      const target = note.payload.target;
      const fw = target.framework;

      const cName = fw.component || fw.Component || target.tagName;
      const sLoc = fw.sourceLocation || fw.SourceLocation || target.selector;
      meta.textContent = '[Pin ' + note.index + '] ' + cName + ' • ' + sLoc;

      card.querySelector('#card-intent').value = note.intent || 'design_fix';
      card.querySelector('#card-comment').value = note.comment || '';

      const badgeEntry = this.markerElements.get(note.id);
      let posX = window.innerWidth / 2 - 160;
      let posY = window.innerHeight / 2 - 100;
      if (badgeEntry && badgeEntry.element) {
        const r = badgeEntry.element.getBoundingClientRect();
        posX = Math.max(20, Math.min(window.innerWidth - 340, r.left + 30));
        posY = Math.max(20, Math.min(window.innerHeight - 300, r.top));
      }

      if (this.host) this.host.style.cursor = 'default';
      card.style.display = 'block';
      card.style.left = posX + 'px';
      card.style.top = posY + 'px';

      setTimeout(() => card.querySelector('#card-comment').focus(), 50);
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
        element.style.left = (rect.left + rect.width / 2 - 12) + 'px';
        element.style.top = (rect.top - 12) + 'px';
      });
    }

    startTracking() {
      if (this.rafId) {
        cancelAnimationFrame(this.rafId);
        this.rafId = 0;
      }
      const update = () => {
        if (!this.active) {
          this.rafId = 0;
          return;
        }
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
