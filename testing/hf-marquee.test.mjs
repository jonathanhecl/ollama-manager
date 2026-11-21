import assert from "node:assert/strict";
import { test } from "node:test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import vm from "node:vm";

const web = join(dirname(fileURLToPath(import.meta.url)), "..", "web");
const helpersSrc = readFileSync(join(web, "app-helpers.js"), "utf8");
const chatSrc = readFileSync(join(web, "app-chat.js"), "utf8");
const downloadsSrc = readFileSync(join(web, "app-downloads.js"), "utf8");
const hfSrc = readFileSync(join(web, "app-hf-search.js"), "utf8");

function extractFunction(src, name) {
  const start = src.indexOf(`function ${name}(`);
  assert.notEqual(start, -1, `${name} not found`);
  const braceStart = src.indexOf("{", start);
  let depth = 0;
  for (let i = braceStart; i < src.length; i++) {
    const ch = src[i];
    if (ch === "{") depth++;
    else if (ch === "}") {
      depth--;
      if (depth === 0) return src.slice(start, i + 1);
    }
  }
  throw new Error(`unbalanced braces in ${name}`);
}

function decodeHtml(str) {
  return String(str)
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&");
}

function kebabToCamel(s) {
  return s.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
}

function parseHTMLFragment(html, ownerDocument) {
  const root = new MockElement("fragment", ownerDocument);
  const stack = [root];
  const tokenRe = /<\/?([a-zA-Z0-9-]+)([^>]*)>|([^<]+)/g;
  let match;
  while ((match = tokenRe.exec(html)) !== null) {
    if (match[3] !== undefined) {
      const text = decodeHtml(match[3]);
      if (text) stack[stack.length - 1]._textChunks.push(text);
      continue;
    }
    const raw = match[0];
    const tag = match[1].toLowerCase();
    const attrStr = match[2] || "";
    const isClosing = raw.startsWith("</");
    const isSelfClosing = raw.endsWith("/>") || tag === "input" || tag === "br" || tag === "hr";

    if (isClosing) {
      for (let i = stack.length - 1; i >= 1; i--) {
        if (stack[i].tagName === tag) {
          stack.length = i;
          break;
        }
      }
      continue;
    }

    const el = new MockElement(tag, ownerDocument);
    const attrRe = /([a-zA-Z0-9_:-]+)(?:\s*=\s*"([^"]*)")?/g;
    let a;
    while ((a = attrRe.exec(attrStr)) !== null) {
      const name = a[1];
      const val = a[2] !== undefined ? decodeHtml(a[2]) : "";
      el.setAttribute(name, val);
    }
    stack[stack.length - 1].appendChild(el);
    if (!isSelfClosing) stack.push(el);
  }
  return root.children;
}

class MockClassList {
  constructor(el) {
    this.el = el;
    this.set = new Set();
    this.log = [];
  }
  _syncFromString(str) {
    this.set = new Set(String(str || "").split(/\s+/).filter(Boolean));
  }
  _toString() {
    return [...this.set].join(" ");
  }
  contains(cls) {
    return this.set.has(cls);
  }
  add(...classes) {
    for (const c of classes) {
      this.log.push({ op: "add", cls: c });
      this.set.add(c);
    }
  }
  remove(...classes) {
    for (const c of classes) {
      this.log.push({ op: "remove", cls: c });
      this.set.delete(c);
    }
  }
  toggle(cls, force) {
    const next = force !== undefined ? !!force : !this.set.has(cls);
    if (next) this.add(cls);
    else this.remove(cls);
    return next;
  }
}

class MockStyle {
  constructor() {
    this.props = new Map();
    this.transform = "";
  }
  setProperty(k, v) {
    this.props.set(k, String(v));
  }
  getPropertyValue(k) {
    return this.props.get(k) || "";
  }
  removeProperty(k) {
    this.props.delete(k);
  }
}

class MockElement {
  constructor(tagName = "div", ownerDocument = null) {
    this.tagName = String(tagName).toLowerCase();
    this.ownerDocument = ownerDocument;
    this.id = "";
    this.title = "";
    this.hidden = false;
    this.parentNode = null;
    this.children = [];
    this.attributes = new Map();
    this.dataset = {};
    this.classList = new MockClassList(this);
    this.style = new MockStyle();
    this._textChunks = [];
    this._rawInnerHTML = "";
    this.clientWidth = 0;
    this.scrollWidth = 0;
    this.offsetWidthReads = 0;
    this.insertBeforeCount = 0;
    this.listeners = {};
  }
  get className() {
    return this.classList._toString();
  }
  set className(v) {
    this.classList._syncFromString(v);
  }
  get offsetWidth() {
    this.offsetWidthReads++;
    return this.clientWidth;
  }
  get isConnected() {
    if (!this.ownerDocument || !this.ownerDocument.body) return true;
    let cur = this;
    while (cur) {
      if (cur === this.ownerDocument.body) return true;
      cur = cur.parentNode;
    }
    return false;
  }
  get firstElementChild() {
    return this.children[0] || null;
  }
  get nextElementSibling() {
    if (!this.parentNode) return null;
    const idx = this.parentNode.children.indexOf(this);
    return idx >= 0 ? (this.parentNode.children[idx + 1] || null) : null;
  }
  get textContent() {
    if (this.children.length === 0) return this._textChunks.join("");
    let out = this._textChunks.join("");
    for (const c of this.children) out += c.textContent;
    return out;
  }
  set textContent(v) {
    this.children.forEach((c) => { c.parentNode = null; });
    this.children = [];
    this._textChunks = [String(v ?? "")];
    this._rawInnerHTML = "";
  }
  get innerHTML() {
    if (this._rawInnerHTML && this.children.length === 0) return this._rawInnerHTML;
    return this.children.map((c) => c.outerHTML).join("") + this._textChunks.join("");
  }
  set innerHTML(v) {
    this.children.forEach((c) => { c.parentNode = null; });
    this.children = [];
    this._textChunks = [];
    this._rawInnerHTML = String(v ?? "");
    if (!this._rawInnerHTML.trim()) return;
    const parsed = parseHTMLFragment(this._rawInnerHTML, this.ownerDocument);
    for (const node of parsed) this.appendChild(node);
  }
  get outerHTML() {
    const attrs = [];
    if (this.id) attrs.push(`id="${this.id}"`);
    if (this.className) attrs.push(`class="${this.className}"`);
    if (this.title) attrs.push(`title="${this.title}"`);
    for (const [k, val] of Object.entries(this.dataset)) {
      const kebab = k.replace(/[A-Z]/g, (m) => `-${m.toLowerCase()}`);
      attrs.push(`data-${kebab}="${val}"`);
    }
    const attrStr = attrs.length ? " " + attrs.join(" ") : "";
    return `<${this.tagName}${attrStr}>${this.innerHTML}</${this.tagName}>`;
  }
  setAttribute(name, val) {
    const k = name.toLowerCase();
    this.attributes.set(k, String(val));
    if (k === "id") this.id = String(val);
    else if (k === "class") this.className = String(val);
    else if (k === "title") this.title = String(val);
    else if (k.startsWith("data-")) {
      this.dataset[kebabToCamel(k.slice(5))] = String(val);
    }
  }
  getAttribute(name) {
    return this.attributes.get(name.toLowerCase()) ?? null;
  }
  appendChild(child) {
    if (child.parentNode) child.remove();
    child.parentNode = this;
    this.children.push(child);
    return child;
  }
  insertBefore(newChild, refChild) {
    this.insertBeforeCount++;
    if (newChild.parentNode) newChild.remove();
    newChild.parentNode = this;
    if (!refChild) {
      this.children.push(newChild);
      return newChild;
    }
    const idx = this.children.indexOf(refChild);
    if (idx === -1) this.children.push(newChild);
    else this.children.splice(idx, 0, newChild);
    return newChild;
  }
  remove() {
    if (!this.parentNode) return;
    const idx = this.parentNode.children.indexOf(this);
    if (idx >= 0) this.parentNode.children.splice(idx, 1);
    this.parentNode = null;
  }
  matchesSelectorPart(part) {
    if (!part) return false;
    const tagMatch = part.match(/^[a-zA-Z0-9-]+/);
    const tag = tagMatch ? tagMatch[0].toLowerCase() : null;
    const idMatch = part.match(/#([a-zA-Z0-9_-]+)/);
    const id = idMatch ? idMatch[1] : null;
    const classes = [...part.matchAll(/\.([a-zA-Z0-9_-]+)/g)].map((m) => m[1]);
    if (tag && this.tagName !== tag) return false;
    if (id && this.id !== id) return false;
    for (const c of classes) {
      if (!this.classList.contains(c)) return false;
    }
    return true;
  }
  querySelectorAll(selector) {
    const groups = selector.split(",").map((s) => s.trim()).filter(Boolean);
    const results = [];
    const seen = new Set();
    const walk = (node) => {
      for (const child of node.children) {
        for (const g of groups) {
          const parts = g.split(/\s+/).filter(Boolean);
          const leaf = parts[parts.length - 1];
          if (child.matchesSelectorPart(leaf)) {
            let ok = true;
            if (parts.length > 1) {
              let ancestor = child.parentNode;
              for (let p = parts.length - 2; p >= 0; p--) {
                while (ancestor && !ancestor.matchesSelectorPart(parts[p])) {
                  ancestor = ancestor.parentNode;
                }
                if (!ancestor) { ok = false; break; }
                ancestor = ancestor.parentNode;
              }
            }
            if (ok && !seen.has(child)) {
              seen.add(child);
              results.push(child);
            }
          }
        }
        walk(child);
      }
    };
    walk(this);
    return results;
  }
  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }
  addEventListener(ev, fn) {
    (this.listeners[ev] ||= []).push(fn);
  }
}

class MockDocument {
  constructor() {
    this.body = new MockElement("body", this);
    this.byId = new Map();
  }
  createElement(tag) {
    return new MockElement(tag, this);
  }
  register(id, el) {
    el.id = id;
    if (!el.parentNode) this.body.appendChild(el);
    this.byId.set(id, el);
    return el;
  }
  getElementById(id) {
    return this.byId.get(id) || null;
  }
  querySelector(sel) {
    return this.body.querySelector(sel);
  }
  querySelectorAll(sel) {
    return this.body.querySelectorAll(sel);
  }
}

test("app-chat.js does not shadow updateMarquee from app-helpers.js", () => {
  assert.equal(
    chatSrc.includes("function updateMarquee("),
    false,
    "app-chat.js should rely on shared updateMarquee in app-helpers.js",
  );
});

test("updateMarquee is idempotent, respects hidden tracks, coalesces rAFs, and resets only on text change or end of overflow", () => {
  const doc = new MockDocument();
  let nextRafId = 1;
  const scheduled = new Map();
  const cancelled = [];

  const sandbox = {
    WeakMap,
    requestAnimationFrame: (cb) => {
      const id = nextRafId++;
      scheduled.set(id, cb);
      return id;
    },
    cancelAnimationFrame: (id) => {
      cancelled.push(id);
      scheduled.delete(id);
    },
  };
  vm.createContext(sandbox);
  vm.runInContext(
    `const marqueeRafIds = typeof WeakMap !== "undefined" ? new WeakMap() : null;\n` +
      extractFunction(helpersSrc, "updateMarquee"),
    sandbox,
  );

  const flushRaf = () => {
    const entries = [...scheduled.entries()];
    scheduled.clear();
    for (const [, cb] of entries) cb();
  };

  const track = doc.register("track", new MockElement("div", doc));
  const text = new MockElement("span", doc);
  track.appendChild(text);

  // 1. Hidden container (clientWidth === 0): safe no-op.
  track.clientWidth = 0;
  text.scrollWidth = 300;
  text.textContent = "very-long-model-name-that-overflows";
  sandbox.updateMarquee(track, text);
  flushRaf();
  assert.equal(text.classList.contains("is-bouncing"), false);
  assert.equal(text.style.getPropertyValue("--marquee-dist"), "");

  // 2. Initial setup when overflowing.
  track.clientWidth = 200;
  text.scrollWidth = 310;
  sandbox.updateMarquee(track, text);
  flushRaf();
  assert.equal(text.classList.contains("is-bouncing"), true);
  assert.equal(text.style.getPropertyValue("--marquee-dist"), "-118px");
  assert.equal(text.style.getPropertyValue("--marquee-dur"), "5.9s");
  assert.equal(text.dataset.marqueeText, "very-long-model-name-that-overflows");

  // 3. Repeated calls with identical metrics must NOT remove .is-bouncing or touch styles.
  text.classList.log.length = 0;
  sandbox.updateMarquee(track, text);
  flushRaf();
  assert.deepEqual(text.classList.log, [], "repeated identical updateMarquee call must not mutate classList");
  assert.equal(text.classList.contains("is-bouncing"), true);

  // 4. Rapid calls cancel the superseded frame so only the final frame runs.
  sandbox.updateMarquee(track, text);
  sandbox.updateMarquee(track, text);
  assert.equal(cancelled.length, 1);
  assert.equal(scheduled.size, 1);
  flushRaf();
  assert.deepEqual(text.classList.log, []);

  // 5. Text change on an already-bouncing element restarts cleanly from 0%.
  text.textContent = "another-long-model-name-that-overflows";
  text.scrollWidth = 340;
  text.classList.log.length = 0;
  const prevReads = text.offsetWidthReads;
  sandbox.updateMarquee(track, text);
  flushRaf();
  assert.deepEqual(
    text.classList.log,
    [
      { op: "remove", cls: "is-bouncing" },
      { op: "add", cls: "is-bouncing" },
    ],
    "changing text on a bouncing node should remove, reflow, and re-add .is-bouncing",
  );
  assert.equal(text.offsetWidthReads, prevReads + 1);
  assert.equal(text.style.getPropertyValue("--marquee-dist"), "-148px");

  // 6. Overflow disappears (e.g. viewport widened): clean up classes/props.
  track.clientWidth = 400;
  text.style.transform = "translateX(-10px)";
  sandbox.updateMarquee(track, text);
  flushRaf();
  assert.equal(text.classList.contains("is-bouncing"), false);
  assert.equal(text.style.getPropertyValue("--marquee-dist"), "");
  assert.equal(text.style.getPropertyValue("--marquee-dur"), "");
  assert.equal(text.style.transform, "");
  assert.equal("marqueeText" in text.dataset, false);
});

test("onJobsChanged skips HF re-renders on download progress ticks and runs on state transitions", () => {
  const doc = new MockDocument();
  doc.register("downloads-modal", new MockElement("div", doc)).hidden = true;
  doc.register("hf-detail-modal", new MockElement("div", doc)).hidden = true;

  let hfListRenders = 0;
  let hfQuantRenders = 0;
  const timers = [];

  const sandbox = {
    $: (id) => doc.getElementById(id),
    jobs: new Map(),
    currentView: "models",
    hfActiveModel: { id: "mradermacher/Test-GGUF" },
    updateDownloadsBadge: () => {},
    renderDownloads: () => {},
    throttleRenderTable: () => {},
    renderHFModelsList: () => { hfListRenders++; },
    renderHFQuantsTable: () => { hfQuantRenders++; },
    setTimeout: (fn) => { timers.push(fn); return timers.length; },
  };
  vm.createContext(sandbox);
  vm.runInContext(
    [
      extractFunction(downloadsSrc, "hfJobStateBucket"),
      extractFunction(downloadsSrc, "computeHFJobsFingerprint"),
      `let lastHFJobsFingerprint = null;`,
      `let hfRenderThrottled = false;`,
      `let hfRenderPending = false;`,
      extractFunction(downloadsSrc, "throttleHFRender"),
      extractFunction(downloadsSrc, "onJobsChanged"),
    ].join("\n"),
    sandbox,
  );

  // First snapshot while not on HF view: fingerprint initializes, list render skipped.
  sandbox.jobs.set("j1", { id: "j1", name: "huggingface.co/a/b:Q4_K_M", status: "queued", completed: 0, percent: 0, speed: 0 });
  sandbox.onJobsChanged();
  assert.equal(hfListRenders, 0);
  assert.equal(hfQuantRenders, 0);

  // Flush throttle window and switch to HF view.
  timers.shift()();
  sandbox.currentView = "hf";

  // Transition queued -> running triggers HF list render.
  sandbox.jobs.set("j1", { id: "j1", name: "huggingface.co/a/b:Q4_K_M", status: "running", completed: 100, percent: 1, speed: 1000 });
  sandbox.onJobsChanged();
  assert.equal(hfListRenders, 1);

  timers.shift()();

  // Repeated progress ticks while still running must NOT trigger HF renders.
  sandbox.jobs.set("j1", { id: "j1", name: "huggingface.co/a/b:Q4_K_M", status: "running", completed: 500, percent: 5, speed: 2000 });
  sandbox.onJobsChanged();
  sandbox.jobs.set("j1", { id: "j1", name: "huggingface.co/a/b:Q4_K_M", status: "running", completed: 900, percent: 9, speed: 3000 });
  sandbox.onJobsChanged();
  assert.equal(hfListRenders, 1, "progress-only ticks must not call renderHFModelsList");

  // Transition running -> done changes fingerprint and triggers HF list render.
  sandbox.jobs.set("j1", { id: "j1", name: "huggingface.co/a/b:Q4_K_M", status: "done", completed: 1000, percent: 100, speed: 0 });
  sandbox.onJobsChanged();
  assert.equal(hfListRenders, 2, "terminal transition must trigger HF list render");
});

test("renderHFModelsList reconciles cards in-place and preserves .hf-row-title-inner identity", () => {
  const doc = new MockDocument();
  const listEl = doc.register("hf-models-list", new MockElement("div", doc));
  const emptyEl = doc.register("hf-empty", new MockElement("div", doc));
  const loadMoreWrap = doc.register("hf-load-more-wrap", new MockElement("div", doc));

  let marqueeCalls = 0;
  const sandbox = {
    window: { I18n: { getLang: () => "en" } },
    document: doc,
    $: (id) => doc.getElementById(id),
    hfModels: [
      {
        id: "mradermacher/Omega-Darker_The-Final-Directive-Longform-ERP-1-GGUF",
        author: "mradermacher",
        name: "Omega-Darker_The-Final-Directive-Longform-ERP-1-GGUF",
        gguf_count: 11,
        downloads: 120,
        likes: 15,
        last_modified: "2026-09-20T12:00:00Z",
      },
      {
        id: "mradermacher/DarkThink-DirectiveReasoner-12B-GGUF",
        author: "mradermacher",
        name: "DarkThink-DirectiveReasoner-12B-GGUF",
        gguf_count: 11,
        downloads: 80,
        likes: 9,
        last_modified: "2026-09-19T12:00:00Z",
      },
    ],
    models: [],
    ghostModels: [],
    jobs: new Map(),
    hfCurrentFilter: "all",
    hfCurrentTimeFilter: "all",
    hfHasMore: true,
    escapeHtml: (s) => String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;"),
    t: (k, p) => (p ? `${k}:${JSON.stringify(p)}` : k),
    fmtRelativeTime: () => "2d ago",
    fmtDateTimeFull: () => "2026-09-20",
    getHFToksColor: () => "",
    getHFBenchHeatStyle: () => "",
    updateMarquee: () => { marqueeCalls++; },
  };
  vm.createContext(sandbox);
  vm.runInContext(
    [
      extractFunction(hfSrc, "normalizeHFModelName"),
      extractFunction(hfSrc, "getHFModelInstallStatus"),
      extractFunction(hfSrc, "getHFModelDownloadStatus"),
      extractFunction(hfSrc, "hfListHeadInnerHTML"),
      extractFunction(hfSrc, "renderHFModelsList"),
      extractFunction(hfSrc, "updateHFMarquees"),
      extractFunction(hfSrc, "hfModelCardData"),
      extractFunction(hfSrc, "hfModelCardHTMLFromData"),
      extractFunction(hfSrc, "hfModelCardHTML"),
      extractFunction(hfSrc, "patchHFModelCard"),
    ].join("\n"),
    sandbox,
  );

  // Initial render.
  sandbox.renderHFModelsList();
  assert.equal(emptyEl.hidden, true);
  assert.equal(loadMoreWrap.hidden, false);
  const firstCards = listEl.querySelectorAll(".hf-model-row");
  assert.equal(firstCards.length, 2);
  const card0 = firstCards[0];
  const card1 = firstCards[1];
  const titleInner0 = card0.querySelector(".hf-row-title-inner");
  const titleInner1 = card1.querySelector(".hf-row-title-inner");
  assert.ok(titleInner0 && titleInner1);
  assert.equal(marqueeCalls, 2);

  // Repeated render with unchanged state: ZERO insertBefore moves, identical DOM nodes.
  listEl.insertBeforeCount = 0;
  sandbox.renderHFModelsList();
  assert.equal(listEl.insertBeforeCount, 0, "unchanged re-render must not move existing nodes");
  assert.equal(listEl.querySelectorAll(".hf-model-row")[0], card0);
  assert.equal(listEl.querySelectorAll(".hf-model-row")[1], card1);
  assert.equal(listEl.querySelectorAll(".hf-model-row")[0].querySelector(".hf-row-title-inner"), titleInner0);

  // Status transition: card0 becomes downloading -> patched in-place, title node preserved.
  sandbox.jobs.set("dl1", {
    id: "dl1",
    name: "huggingface.co/mradermacher/Omega-Darker_The-Final-Directive-Longform-ERP-1-GGUF:Q4_K_M",
    status: "running",
  });
  listEl.insertBeforeCount = 0;
  sandbox.renderHFModelsList();
  assert.equal(listEl.insertBeforeCount, 0, "status patch must not move card node");
  assert.equal(card0.classList.contains("hf-card-downloading"), true);
  assert.equal(card0.querySelector(".hf-status-tag")?.textContent, "⏳");
  assert.equal(card0.querySelector(".hf-row-title-inner"), titleInner0, "title marquee node must stay identical");

  // Status transition: download finishes -> model installed -> patched in-place, title node preserved.
  sandbox.jobs.clear();
  sandbox.models.push({
    name: "huggingface.co/mradermacher/Omega-Darker_The-Final-Directive-Longform-ERP-1-GGUF:Q4_K_M",
  });
  sandbox.renderHFModelsList();
  assert.equal(card0.classList.contains("hf-card-installed"), true);
  assert.equal(card0.classList.contains("hf-card-downloading"), false);
  assert.equal(card0.querySelector(".hf-status-tag")?.textContent, "💾");
  assert.equal(card0.querySelector(".hf-row-title-inner"), titleInner0);

  // "Load more" appends a third model: existing cards keep exact identities.
  sandbox.hfModels.push({
    id: "mradermacher/Third-Model-GGUF",
    author: "mradermacher",
    name: "Third-Model-GGUF",
    gguf_count: 5,
    downloads: 40,
    likes: 2,
    last_modified: "2026-09-18T12:00:00Z",
  });
  listEl.insertBeforeCount = 0;
  sandbox.renderHFModelsList();
  const afterAppend = listEl.querySelectorAll(".hf-model-row");
  assert.equal(afterAppend.length, 3);
  assert.equal(listEl.insertBeforeCount, 1, "only the newly appended card should be inserted");
  assert.equal(afterAppend[0], card0);
  assert.equal(afterAppend[1], card1);
  assert.equal(afterAppend[0].querySelector(".hf-row-title-inner"), titleInner0);
  assert.equal(afterAppend[1].querySelector(".hf-row-title-inner"), titleInner1);
});

test("renderHFQuantsTable patches row state in-place without replacing .hf-filename-text", () => {
  const doc = new MockDocument();
  const tbody = doc.register("hf-quants-tbody", new MockElement("tbody", doc));
  doc.register("hf-quants-summary", new MockElement("div", doc));

  const sandbox = {
    window: { I18n: { getLang: () => "en" } },
    document: doc,
    $: (id) => doc.getElementById(id),
    lastSystemStatus: { vram_total: 24 * 1024 ** 3, memory_total: 64 * 1024 ** 3 },
    models: [],
    ghostModels: [],
    jobs: new Map(),
    escapeHtml: (s) => String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;"),
    t: (k, p) => (p ? `${k}:${JSON.stringify(p)}` : k),
    fmtBytes: (n) => `${n}B`,
    hfQuantUsageHTML: () => "—",
    updateMarquee: () => {},
  };
  vm.createContext(sandbox);
  vm.runInContext(
    [
      extractFunction(hfSrc, "computeMemoryFit"),
      extractFunction(hfSrc, "normalizeHFModelName"),
      extractFunction(hfSrc, "canonicalHFPullName"),
      extractFunction(hfSrc, "getHFQuantInstallStatus"),
      extractFunction(hfSrc, "isHFQuantDownloading"),
      extractFunction(hfSrc, "renderHFQuantsSummary"),
      extractFunction(hfSrc, "renderHFQuantsTable"),
      extractFunction(hfSrc, "updateHFQuantMarquees"),
    ].join("\n"),
    sandbox,
  );

  const model = {
    id: "mradermacher/Omega-Darker_The-Final-Directive-Longform-ERP-1-GGUF",
    gguf_files: [
      {
        quant: "Q4_K_M",
        filename: "Omega-Darker_The-Final-Directive-Longform-ERP-1.Q4_K_M.gguf",
        size_bytes: 7 * 1024 ** 3,
      },
    ],
  };

  sandbox.renderHFQuantsTable(model);
  const tr = tbody.querySelector("tr.hf-quant-row");
  const filenameSpan = tr.querySelector(".hf-filename-text");
  assert.ok(tr && filenameSpan);
  assert.ok(tr.querySelector(".hf-install-btn"));

  // Transition to queued -> running -> installed.
  sandbox.jobs.set("q1", {
    id: "q1",
    name: "huggingface.co/mradermacher/Omega-Darker_The-Final-Directive-Longform-ERP-1-GGUF:Q4_K_M",
    status: "running",
  });
  sandbox.renderHFQuantsTable(model);
  assert.equal(tbody.querySelector("tr.hf-quant-row"), tr);
  assert.equal(tr.querySelector(".hf-filename-text"), filenameSpan, ".hf-filename-text node must be preserved");
  assert.ok(tr.querySelector(".hf-badge-downloading"));

  sandbox.jobs.clear();
  sandbox.models.push({
    name: "huggingface.co/mradermacher/Omega-Darker_The-Final-Directive-Longform-ERP-1-GGUF:Q4_K_M",
  });
  sandbox.renderHFQuantsTable(model);
  assert.equal(tbody.querySelector("tr.hf-quant-row"), tr);
  assert.equal(tr.querySelector(".hf-filename-text"), filenameSpan);
  assert.equal(tr.classList.contains("hf-row-installed"), true);
  assert.ok(tr.querySelector(".hf-chat-btn"));
});
