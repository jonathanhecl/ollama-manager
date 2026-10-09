import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import vm from "node:vm";

const web = join(dirname(fileURLToPath(import.meta.url)), "..", "web");

const svgSrc = readFileSync(join(web, "app-svg.js"), "utf8");
const sessionsSrc = readFileSync(join(web, "app-sessions.js"), "utf8");
const ragSrc = readFileSync(join(web, "app-rag.js"), "utf8");

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
const extract = (src, name) => {
  const i = src.indexOf(`async function ${name}(`);
  if (i >= 0) {
    const s = extractFunction(src.slice(i).replace("async function", "function"), name);
    return `async function ${name}` + s.slice(`function ${name}`.length);
  }
  return extractFunction(src, name);
};

class FakeEl {
  constructor(tag) {
    this.tagName = String(tag || "").toUpperCase();
    this.children = [];
    this.listeners = {};
    this.dataset = {};
    this.style = {};
    this.attrs = {};
    this._cls = "";
    this._classes = new Set();
    this._html = "";
    this.value = "";
    this.textContent = "";
    this.title = "";
    this.hidden = false;
    this.checked = false;
    this.disabled = false;
    this.selectedOptions = [];
    this.files = [];
    const self = this;
    this.classList = {
      add: (c) => self._classes.add(c),
      remove: (c) => self._classes.delete(c),
      contains: (c) => self._classes.has(c),
      toggle(c, force) {
        const on = force === undefined ? !self._classes.has(c) : !!force;
        if (on) self._classes.add(c); else self._classes.delete(c);
        return on;
      },
    };
  }
  get className() { return this._cls; }
  set className(v) {
    this._cls = v;
    this._classes = new Set(String(v).split(/\s+/).filter(Boolean));
  }
  get innerHTML() { return this._html; }
  set innerHTML(v) {
    this._html = v;
    if (v === "") this.children = [];
  }
  appendChild(c) { this.children.push(c); if (c instanceof FakeEl) c.parentEl = this; return c; }
  remove() {
    if (this.parentEl) {
      this.parentEl.children = this.parentEl.children.filter((x) => x !== this);
      this.parentEl = null;
    }
  }
  setAttribute(k, v) { this.attrs[k] = v; }
  getAttribute(k) { return this.attrs[k]; }
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
  dispatch(name, ev = {}) {
    if (!ev.preventDefault) ev.preventDefault = () => { ev.defaultPrevented = true; };
    if (!ev.stopPropagation) ev.stopPropagation = () => { ev.propagationStopped = true; };
    for (const fn of this.listeners[name] || []) fn(ev);
  }
  click() { this.dispatch("click"); }
  _matches(sel) {
    if (sel.startsWith(".")) return this._classes.has(sel.slice(1));
    return this.tagName === sel.toUpperCase();
  }
  querySelectorAll(sel) {
    const sels = String(sel).split(",").map((s) => s.trim()).filter(Boolean);
    const out = [];
    const walk = (el) => {
      for (const c of el.children || []) {
        if (c instanceof FakeEl) {
          if (sels.some((s) => c._matches(s))) out.push(c);
          walk(c);
        }
      }
    };
    walk(this);
    return out;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  closest() { return null; }
  focus() {}
  select() {}
  scrollIntoView() {}
}

const findDeep = (root, pred) => {
  const out = [];
  const walk = (el) => {
    for (const c of el.children || []) {
      if (c instanceof FakeEl) {
        if (pred(c)) out.push(c);
        walk(c);
      }
    }
  };
  walk(root);
  return out;
};

const els = new Map();
const boundEls = new Map();
const fakeEl = (id) => {
  if (boundEls.has(id)) {
    const el = boundEls.get(id);
    els.set(id, el);
    return el;
  }
  const el = new FakeEl("div");
  el.id = id;
  boundEls.set(id, el);
  els.set(id, el);
  return el;
};

class FakeFileReader {
  constructor() { FakeFileReader.instances.push(this); }
  readAsText(file) {
    if (file && file.__throwOnRead) throw new Error("readAsText failed");
    this.file = file;
  }
  readAsDataURL() {}
}
FakeFileReader.instances = [];

class FakeFormData {
  constructor() { this._f = []; }
  append(name, value) { this._f.push([name, value]); }
  get(name) {
    const hits = this._f.filter(([k]) => k === name);
    return hits.length ? hits[hits.length - 1][1] : null;
  }
  count(name) { return this._f.filter(([k]) => k === name).length; }
}

class FakeBlob {
  constructor(parts) { this.parts = parts || []; }
}

const createdURLs = [];
class FakeURL extends URL {
  static createObjectURL(blob) {
    const u = "blob:fake-" + createdURLs.length;
    createdURLs.push({ url: u, blob, revoked: false });
    return u;
  }
  static revokeObjectURL(u) {
    const rec = createdURLs.find((x) => x.url === u);
    if (rec) rec.revoked = true;
  }
}

const toasts = [];
const imageOnly = { value: false };
const subscribed = [];

const sandbox = {
  console,
  URL,
  t: (k, p) => `${k}:${Object.entries(p || {}).map(([pk, pv]) => `${pk}=${pv}`).join(",")}`,
  toast: (msg, type) => toasts.push({ msg, type }),
  requestAnimationFrame: (fn) => fn(),
  setTimeout: (fn) => fn(),
  localStorage: { getItem: () => null, setItem() {} },
  flushSegmentToTimeline() {},
  scheduleRenderChatMessages() {},
  flushChatRender() {},
  renderChatMessages() {},
  renderChatQueue() {},
  renderSessionList() {},
  renderSessionBadges() {},
  scrollChatToBottom() {},
  updateStreamBar() {},
  updateChatSendEnabled() {},
  updateLiveAssistantTPS() {},
  updateArtifactResourceBtn() {},
  startThinkTicker() {},
  stopThinkTicker() {},
  stopStreamTicker() {},
  markChatSessionSeen() {},
  mergeSessionSummary() {},
  settleSessionRun() {},
  resetChatState() {},
  resetDecisionState() {},
  syncChatPanels() {},
  showArtifactPanel() {},
  closeChatSessionStream() {},
  loadChatSessions() {},
  handleArtifactScreenshotRequest() {},
  handleArtifactEvalRequest() {},
  saveChatOptionsForCurrentModel() {},
  adjustChatSystemPromptHeight() {},
  chatRagPersistLocal() {},
  chatRagRenderSelection() { sandbox._ragRenders = (sandbox._ragRenders || 0) + 1; },
  ragRefreshList: async () => { sandbox._ragRefreshes = (sandbox._ragRefreshes || 0) + 1; },
  ragSubview: () => "list",
  currentView: "chat",
  chatRagSyncSession: async () => {},
  chatRagCommit(syncSession) {
    if (!sandbox.chatRagEnabled) sandbox.chatRagPaths = [];
    sandbox.chatRagEditable = (sandbox.chatRagEditable || []).filter((p) => sandbox.chatRagPaths.includes(p));
    if (syncSession) void sandbox.chatRagSyncSession();
  },
  normalizeChatRagPaths: (list) => [...new Set((list || []).map((p) => String(p || "").trim()).filter((p) => p && p.endsWith(".db") && !p.includes("/")))],
  normalizeChatRagEditable: (list, paths) => sandbox.normalizeChatRagPaths(list).filter((p) => paths.includes(p)),
  chatRagFetchList: async () => {
    sandbox._ragFetches = (sandbox._ragFetches || 0) + 1;
    return [{ filename: "a.db" }, { filename: "b.db" }];
  },
  ragConfiguredDefault: () => "",
  splitThink: () => ({ think: "", answer: "", inThink: false }),
  modelCaps: () => new Set(),
  isImageGenerationOnlyCaps: () => imageOnly.value,
  isAbortError: () => false,
  isOomError: () => false,
  chatMessages: [],
  chatPendingQueue: [],
  chatSessions: new Map(),
  chatSessionId: null,
  chatSessionRun: null,
  chatSessionRunPending: false,
  chatSessionResetting: false,
  chatSessionStream: null,
  chatStreamLock: false,
  activeStreamMessage: null,
  sessionMessageToChatMessage: (m) => m,
  newAssistantMessage: () => ({ role: "assistant", toolLog: [] }),
  $: (id) => els.get(id) || null,
  document: {
    getElementById: (id) => fakeEl(id),
    querySelectorAll: () => [],
    querySelector: () => null,
    createElement: (tag) => new FakeEl(tag),
    createTextNode: (text) => ({ textContent: String(text) }),
    body: new FakeEl("body"),
    documentElement: {},
    readyState: "complete",
    addEventListener() {},
  },
  FileReader: FakeFileReader,
  FormData: FakeFormData,
  Blob: FakeBlob,
  URL: FakeURL,
  _apiCalls: [],
  api: async (url, opts = {}, responseType = "json") => {
    sandbox._apiCalls.push({ url, opts, responseType });
    if (url === "/api/rags" && !(opts && opts.method)) {
      return { rags: [{ filename: "a.db" }, { filename: "b.db" }] };
    }
    if (String(url).startsWith("/api/rags/external/open")) {
      if (sandbox._failOpen) throw new Error("open boom");
      return sandbox._externalDetail;
    }
    if (url === "/api/rags/models") {
      return { models: [{ name: "embed-multimodal:latest", capabilities: "embedding,vision,audio" }], default_embedding: "", warnings: [] };
    }
    if (url === "/api/rags/external/save") {
      if (sandbox._failSave) throw new Error("save boom");
      if (responseType === "blob") return new FakeBlob(["edited-db"]);
      return { ok: true, rag: { filename: "saved-1.db", name: "External local" } };
    }
    return {};
  },
  fmtDate: (s) => String(s),
  fmtBytes: (n) => String(n),
  escapeHtml: (s) => String(s),
  currentConfig: {},
  chatRagEnabled: false,
  chatRagPaths: [],
  chatRagEditable: [],
  location: { pathname: "/" },
  history: {
    pushState(_a, _b, p) { sandbox.location.pathname = p; },
    replaceState(_a, _b, p) { sandbox.location.pathname = p; },
  },
  hideAllMainViews() {},
  showSettingsView: async () => {},
  showModelsView() {},
  EventSource: class FakeEventSource {
    constructor(url) {
      this.url = url;
      this.readyState = 1;
    }
    addEventListener(name) { subscribed.push(name); }
    close() { this.readyState = 3; }
  },
};
sandbox.window = sandbox;
sandbox.globalThis = sandbox;
vm.createContext(sandbox);

for (const name of ["applyChatStreamEvent"]) {
  vm.runInContext(extract(svgSrc, name), sandbox, { filename: `app-svg.js:${name}` });
}
for (const name of ["updateChatRagAvailability", "chatRagSetEnabled", "chatRagValidateSelection"]) {
  vm.runInContext(extract(ragSrc, name), sandbox, { filename: `app-rag.js:${name}` });
}
for (const name of ["handleChatSessionStreamEvent", "openChatSessionStream"]) {
  vm.runInContext(extract(sessionsSrc, name), sandbox, { filename: `app-sessions.js:${name}` });
}
vm.runInContext(ragSrc, sandbox, { filename: "app-rag.js" });
sandbox.chatRagFetchList = async () => {
  sandbox._ragFetches = (sandbox._ragFetches || 0) + 1;
  return [{ filename: "a.db" }, { filename: "b.db" }];
};
sandbox.ragRefreshList = async () => { sandbox._ragRefreshes = (sandbox._ragRefreshes || 0) + 1; };
sandbox.chatRagRenderSelection = () => { sandbox._ragRenders = (sandbox._ragRenders || 0) + 1; };
sandbox.chatRagSyncSession = async () => {};
sandbox.chatRagPersistLocal = () => {};
const ragEval = (expr) => vm.runInContext(expr, sandbox);

const {
  applyChatStreamEvent, updateChatRagAvailability, chatRagSetEnabled,
  chatRagValidateSelection, handleChatSessionStreamEvent, openChatSessionStream,
} = sandbox;

{
  const msg = { role: "assistant", content: "partial answer" };
  assert.doesNotThrow(() => applyChatStreamEvent(msg, "warning", { message: "base broke" }, { raw: "" }));
  assert.equal(toasts.length, 1);
  assert.equal(toasts[0].type, "error");
  assert.match(toasts[0].msg, /rag\.chat_warning/);
  assert.match(toasts[0].msg, /base broke/);
  assert.equal(msg.content, "partial answer");
  assert.equal(msg.isError, undefined);
}

{
  els.clear();
  fakeEl("chat-rag-wrap");
  fakeEl("chat-rag-panel");
  fakeEl("chat-model").value = "chat-model";
  imageOnly.value = false;
  sandbox.chatRagEnabled = true;
  updateChatRagAvailability();
  assert.equal(els.get("chat-rag-wrap").hidden, false, "wrap must stay visible without a configured default");
  imageOnly.value = true;
  updateChatRagAvailability();
  assert.equal(els.get("chat-rag-wrap").hidden, true, "wrap must hide for image-only models");
  imageOnly.value = false;
}

{
  sandbox.chatRagEnabled = false;
  sandbox.chatRagPaths = [];
  sandbox.chatRagEditable = [];
  chatRagSetEnabled(true, false);
  assert.equal(sandbox.chatRagEnabled, true, "enable must not require a default embedding model");
  chatRagSetEnabled(false, false);
  assert.equal(sandbox.chatRagEnabled, false);
  assert.equal(sandbox.chatRagPaths.length, 0);
  assert.equal(sandbox.chatRagEditable.length, 0);
}

{
  sandbox.chatRagEnabled = true;
  sandbox.chatRagPaths = ["a.db", "gone.db"];
  await chatRagValidateSelection(false);
  assert.equal(sandbox.chatRagEnabled, true, "selection survives without a configured default");
  assert.deepEqual(Array.from(sandbox.chatRagPaths), ["a.db"], "missing bases are dropped, the rest stay");
}

{
  const before = toasts.length;
  assert.equal(sandbox.chatMessages.length, 0);
  handleChatSessionStreamEvent("warning", { data: JSON.stringify({ data: { message: "base broke" } }) });
  assert.equal(toasts.length, before + 1);
  assert.equal(toasts[toasts.length - 1].type, "error");
  assert.match(toasts[toasts.length - 1].msg, /base broke/);
  assert.equal(sandbox.chatMessages.length, 0, "warning must not create or alter an assistant message");
}

{
  sandbox.chatSessionStream = null;
  openChatSessionStream("sess-1", 0);
  assert.ok(subscribed.includes("warning"), "session stream must subscribe to warning events");
  assert.ok(subscribed.includes("rag_updated"), "session stream must subscribe to rag_updated events");
  sandbox.chatSessionStream.close();
  sandbox.chatSessionStream = null;
}

{
  sandbox._ragFetches = 0;
  sandbox._ragRenders = 0;
  sandbox._ragRefreshes = 0;
  sandbox.currentView = "chat";
  const msg = { role: "assistant", content: "answer" };
  assert.doesNotThrow(() => applyChatStreamEvent(msg, "rag_updated", { filename: "a.db" }, { raw: "" }));
  await new Promise((r) => setImmediate(r));
  assert.equal(sandbox._ragFetches, 1, "rag_updated must refresh the rag list");
  assert.equal(sandbox._ragRenders, 1, "rag_updated must re-render the chat selection");
  assert.equal(sandbox._ragRefreshes, 0, "no list refresh while not on the rags view");
  assert.equal(msg.content, "answer");
  sandbox.currentView = "rags";
  applyChatStreamEvent(msg, "rag_updated", { filename: "a.db" }, { raw: "" });
  await new Promise((r) => setImmediate(r));
  assert.equal(sandbox._ragRefreshes, 1, "rags list view must refresh on rag_updated");
  sandbox.currentView = "chat";
}

{
  sandbox._ragFetches = 0;
  sandbox._ragRenders = 0;
  sandbox.chatMessages.length = 0;
  handleChatSessionStreamEvent("rag_updated", { data: JSON.stringify({ data: { filename: "a.db" } }) });
  await new Promise((r) => setImmediate(r));
  assert.equal(sandbox._ragFetches, 1, "session rag_updated must refresh the list");
  assert.equal(sandbox.chatMessages.length, 0, "rag_updated must not create an assistant message");
}

{
  els.clear();
  toasts.length = 0;
  FakeFileReader.instances.length = 0;
  const host = fakeEl("rag-entries");
  fakeEl("rag-entry-list");
  fakeEl("rag-entry-count");
  fakeEl("rag-new-model").value = "";
  const box = fakeEl("rag-create-box");
  const boxBtn = new FakeEl("button");
  box.appendChild(boxBtn);
  const status = fakeEl("rag-create-status");

  const ragState = ragEval("ragState");
  ragState.entries = [];
  ragState.creating = false;
  const e1 = sandbox.ragNewEntry();
  e1.term = "first";
  e1.content = "old text";
  e1.inputMode = "combined";
  const e2 = sandbox.ragNewEntry();
  e2.term = "second";
  e2.content = "other";
  ragState.entries.push(e1, e2);
  ragState.activeEntry = e1.key;

  const findTextarea = () => findDeep(host, (c) => c.tagName === "TEXTAREA" && c._classes.has("rag-entry-content"))[0];
  const findFileBtn = () => findDeep(host, (c) => c._classes.has("rag-content-file-btn"))[0];
  const findFileInput = () => findDeep(host, (c) => c.tagName === "INPUT" && c.type === "file" && String(c.accept || "").includes(".txt"))[0];

  sandbox.ragRenderEntries();
  let ta = findTextarea();
  const btn = findFileBtn();
  let fileIn = findFileInput();
  assert.ok(ta, "content textarea must render");
  assert.equal(ta.value, "old text");
  assert.ok(btn, "content load button must render");
  assert.match(btn.title, /rag\.load_content/);
  assert.match(btn.getAttribute("aria-label"), /rag\.load_content/);
  assert.ok(fileIn, "hidden file input must render");
  assert.match(fileIn.accept, /\.txt.*\*\/\*/);

  let chooserClicks = 0;
  fileIn.click = () => { chooserClicks++; };
  btn.dispatch("click", {});
  assert.equal(chooserClicks, 1, "button must open the file chooser");

  const f1 = { name: "a.txt", type: "text/plain" };
  fileIn.value = "C:\\fake\\a.txt";
  fileIn.files = [f1];
  fileIn.dispatch("change", {});
  assert.equal(fileIn.value, "", "picker must reset so the same file can reload");
  assert.equal(e1.contentPending, true, "entry must be pending while FileReader runs");
  assert.equal(ragEval("ragBusy()"), true, "ragBusy must cover contentPending");
  assert.equal(boxBtn.disabled, true, "form controls must be disabled while reading");

  sandbox.ragLoadContentFile(e1, { name: "b.txt" });
  assert.equal(FakeFileReader.instances.length, 1, "no second reader while busy");
  await sandbox.ragSubmitCreate();
  assert.match(status.textContent, /rag\.file_pending/, "submit must be blocked while content is pending");

  const reader = FakeFileReader.instances[0];
  reader.result = "file text";
  reader.onload();
  assert.equal(e1.contentPending, false);
  assert.equal(e1.content, "file text", "file replaces previous content");
  assert.equal(boxBtn.disabled, false, "form must unlock after read");
  ta = findTextarea();
  assert.equal(ta.value, "file text", "rerendered textarea shows the file text");
  assert.equal(e1.term, "first");
  assert.equal(Object.keys(e1.media).length, 0, "media untouched");
  assert.equal(e1.inputMode, "combined");
  assert.equal(e2.content, "other", "captured entry only, not the active entry");

  ta.value = "manual edit";
  ta.dispatch("input", {});
  assert.equal(e1.content, "manual edit", "native manual input still works");

  const mkDrag = (dt) => ({
    dataTransfer: dt,
    preventDefault() { this.defaultPrevented = true; },
    stopPropagation() { this.propagationStopped = true; },
  });
  const fA = { name: "a.txt" };
  const fB = { name: "b.txt" };
  let ev = mkDrag({ types: ["Files"], files: [] });
  ta.dispatch("dragenter", ev);
  assert.equal(ev.defaultPrevented, true);
  assert.equal(ev.propagationStopped, true);
  assert.equal(ta.classList.contains("drag-over"), true, "file drag must highlight");
  ev = mkDrag({ types: ["Files"], files: [] });
  ta.dispatch("dragover", ev);
  assert.equal(ev.defaultPrevented, true);
  assert.equal(ev.dataTransfer.dropEffect, "copy");
  ev = mkDrag({ types: ["Files"], files: [fA, fB] });
  ta.dispatch("drop", ev);
  assert.equal(ev.defaultPrevented, true);
  assert.equal(ev.propagationStopped, true);
  assert.equal(ta.classList.contains("drag-over"), false, "drop clears highlight");
  assert.equal(FakeFileReader.instances.length, 2);
  FakeFileReader.instances[1].result = "first file";
  FakeFileReader.instances[1].onload();
  assert.equal(e1.content, "first file", "drop imports only the first file");

  ev = mkDrag({ types: ["Files"], files: [] });
  ta = findTextarea();
  ta.dispatch("dragenter", ev);
  assert.equal(ta.classList.contains("drag-over"), true);
  ev = mkDrag({ types: ["Files"], files: [] });
  ta.dispatch("dragleave", ev);
  assert.equal(ev.defaultPrevented, true);
  assert.equal(ta.classList.contains("drag-over"), false, "dragleave clears highlight");

  ev = mkDrag({ types: ["text/plain"], files: [] });
  ta.dispatch("dragenter", ev);
  assert.equal(ev.defaultPrevented, undefined, "non-file drag must not be canceled");
  assert.equal(ta.classList.contains("drag-over"), false);
  ev = mkDrag({ types: ["text/plain"], files: [] });
  ta.dispatch("dragover", ev);
  assert.equal(ev.defaultPrevented, undefined);
  ev = mkDrag({ types: ["text/plain"], files: [] });
  ta.dispatch("drop", ev);
  assert.equal(ev.defaultPrevented, undefined, "text drop keeps native behavior");
  assert.equal(ev.propagationStopped, undefined);

  const toastBase = toasts.length;
  fileIn = findFileInput();
  fileIn.files = [{ name: "err.txt" }];
  fileIn.dispatch("change", {});
  const errReader = FakeFileReader.instances[2];
  errReader.onerror();
  assert.equal(e1.contentPending, false);
  assert.equal(e1.content, "first file", "failed read preserves previous content");
  assert.equal(boxBtn.disabled, false);
  assert.equal(toasts.length, toastBase + 1);
  assert.equal(toasts[toasts.length - 1].type, "error");
  assert.match(toasts[toasts.length - 1].msg, /rag\.file_read_error/);

  fileIn = findFileInput();
  fileIn.files = [{ name: "abort.txt" }];
  fileIn.dispatch("change", {});
  FakeFileReader.instances[3].onabort();
  assert.equal(e1.contentPending, false, "abort must clear pending");
  assert.equal(e1.content, "first file");
  assert.equal(boxBtn.disabled, false);

  fileIn = findFileInput();
  fileIn.files = [{ name: "boom.txt", __throwOnRead: true }];
  fileIn.dispatch("change", {});
  assert.equal(e1.contentPending, false, "sync readAsText throw uses the failure path");
  assert.equal(e1.content, "first file");
  assert.equal(boxBtn.disabled, false);

  sandbox.ragLoadContentFile(e1, { name: "stale.txt" });
  assert.equal(e1.contentPending, true);
  ragState.entries = ragState.entries.filter((x) => x !== e1);
  ragState.activeEntry = e2.key;
  const staleReader = FakeFileReader.instances[4];
  staleReader.result = "stale";
  staleReader.onload();
  assert.equal(e2.content, "other", "stale entry cannot alter remaining state");
  assert.equal(ragState.entries.length, 1);
  assert.equal(boxBtn.disabled, false);

  sandbox.ragLoadContentFile(e2, null);
  assert.equal(FakeFileReader.instances.length, 6, "no file means no reader");

  const i18nSrc = readFileSync(join(web, "i18n.js"), "utf8");
  assert.equal((i18nSrc.match(/"rag\.load_content"/g) || []).length, 2, "EN+ES labels for load content");
  assert.equal((i18nSrc.match(/"rag\.content_label"/g) || []).length, 2, "EN+ES labels for content label");
}

{
  els.clear();
  toasts.length = 0;
  createdURLs.length = 0;
  FakeFileReader.instances.length = 0;
  sandbox._apiCalls = [];
  sandbox._failOpen = false;
  sandbox._failSave = false;
  const ragState = ragEval("ragState");
  ragState.entries = [];
  ragState.creating = false;
  ragState.externalFile = null;
  ragState.externalName = "";
  ragState.externalLoading = false;
  sandbox.location.pathname = "/rags";

  const openBtn = fakeEl("rags-open-file-btn");
  const fileInput = fakeEl("rags-external-file-input");
  const dlBtn = fakeEl("rag-download-edited-btn");
  const createBtn = fakeEl("rag-create-btn");
  const extHint = fakeEl("rag-external-hint");
  fakeEl("rag-create-box");
  fakeEl("rag-entries");
  fakeEl("rag-entry-list");
  fakeEl("rag-entry-count");
  fakeEl("rag-new-name");
  fakeEl("rag-new-desc");
  fakeEl("rag-new-model");
  fakeEl("rag-edit-meta");
  fakeEl("rag-create-status");

  sandbox._externalDetail = {
    filename: "staged-abc.db",
    size_bytes: 2048,
    meta: { id: "m1", name: "Ext", description: "d", created_at: 100, updated_at: 200, embedding_model: "embed-multimodal:latest" },
    entries: [
      { id: 7, term: "alpha", content: "first", input_mode: "combined", created_at: 100, updated_at: 100,
        media: [{ type: "image", name: "logo.png", mime: "image/png", size: 3, base64: "QUJD" }] },
      { id: 8, term: "beta", content: "second", input_mode: "combined", created_at: 100, updated_at: 100 },
    ],
  };

  let opened = 0;
  fileInput.click = () => { opened++; };
  openBtn.dispatch("click", {});
  assert.equal(opened, 1, "open button must trigger the file picker");

  const extFile = { name: "external.db", size: 2048 };
  fileInput.value = "C:\\fake\\external.db";
  fileInput.files = [extFile];
  fileInput.dispatch("change", { target: fileInput });
  assert.equal(fileInput.value, "", "picker resets so the same file reloads");
  assert.equal(ragState.externalLoading, true, "open marks external loading");
  assert.equal(ragEval("ragBusy()"), true, "busy covers externalLoading");
  await sandbox.ragSubmitCreate();
  assert.ok(!sandbox._apiCalls.some((c) => c.url === "/api/rags/external/save"),
    "submit blocked while external open is pending");

  await new Promise((r) => setImmediate(r));
  await new Promise((r) => setImmediate(r));
  assert.equal(ragState.externalLoading, false);
  assert.equal(ragState.externalFile, extFile, "source file retained for save");
  assert.equal(sandbox.location.pathname, "/rags/external", "navigates to external editor");
  const openCall = sandbox._apiCalls.find((c) => String(c.url).includes("/api/rags/external/open"));
  assert.ok(openCall, "open request sent");
  assert.match(openCall.url, /name=external\.db/);
  assert.equal(openCall.opts.method, "POST");
  assert.equal(ragState.entries.length, 2);
  const e0 = ragState.entries[0];
  assert.equal(e0.id, 7);
  assert.equal(e0.media.image.existing, true);
  assert.equal(e0.media.image.entryID, 7);
  assert.equal(e0.media.image.previewBase64, "QUJD", "external media keeps preview base64");
  assert.equal(ragState.editingFilename, "", "external edit is not a local base");
  assert.equal(ragState.createPreferredModel, "embed-multimodal:latest", "imported model retained");
  assert.equal(els.get("rag-new-model").value, "embed-multimodal:latest");
  assert.equal(sandbox.ragMediaDataURL(e0.media.image), "data:image/png;base64,QUJD");

  await ragEval("showRagsView()");
  assert.match(createBtn.textContent, /rag\.save_local/, "save button switches to Save as local");
  assert.equal(createBtn.dataset.i18n, "rag.save_local", "language switch keeps the external label");
  assert.equal(dlBtn.hidden, false, "download button visible on external route");
  assert.equal(extHint.hidden, false, "external hint visible");

  await sandbox.ragSubmitCreate("download");
  let saveCall = sandbox._apiCalls.filter((c) => c.url === "/api/rags/external/save").pop();
  assert.equal(saveCall.responseType, "blob", "download asks for a blob");
  const fd = saveCall.opts.body;
  assert.ok(fd instanceof FakeFormData, "external save posts FormData");
  assert.equal(fd.count("file"), 1);
  assert.equal(fd.get("file"), extFile, "original file is sent back unchanged");
  assert.equal(fd.get("destination"), "download");
  const changes = JSON.parse(fd.get("changes"));
  assert.equal(changes.entries.length, 2);
  assert.deepEqual(
    { type: changes.entries[0].media[0].type, existing: changes.entries[0].media[0].existing, entry_id: changes.entries[0].media[0].entry_id, hasB64: "base64" in changes.entries[0].media[0] },
    { type: "image", existing: true, entry_id: 7, hasB64: false },
    "existing media referenced by id without bytes",
  );
  assert.equal(createdURLs.length, 1);
  assert.equal(createdURLs[0].revoked, true, "object URL revoked");
  assert.equal(sandbox.document.body.children.length, 0, "download anchor removed");
  assert.equal(ragState.externalFile, extFile, "draft retained after download");
  assert.equal(ragState.entries.length, 2);
  assert.equal(sandbox.location.pathname, "/rags/external", "no list navigation after download");

  sandbox._failSave = true;
  const toastBase = toasts.length;
  await sandbox.ragSubmitCreate();
  assert.equal(ragState.externalFile, extFile, "save failure keeps the draft");
  assert.equal(ragState.entries.length, 2);
  sandbox._failSave = false;
  void toastBase;

  await sandbox.ragSubmitCreate();
  saveCall = sandbox._apiCalls.filter((c) => c.url === "/api/rags/external/save").pop();
  assert.equal(saveCall.responseType, "json");
  assert.equal(saveCall.opts.body.get("destination"), "local");
  assert.equal(ragState.externalFile, null, "local save clears external state");
  assert.equal(ragState.entries.length, 0);
  assert.equal(sandbox.location.pathname, "/rags", "local save returns to the list");
  assert.equal(dlBtn.hidden, true, "download button hidden after leaving external");
  assert.equal(openBtn.disabled, false, "open button re-enabled after save-local");
  assert.equal(els.get("rags-new-btn").disabled, false, "new button re-enabled after save-local");
  assert.equal(els.get("rags-reload-btn").disabled, false, "reload button re-enabled after save-local");

  sandbox.location.pathname = "/rags/external";
  const refreshCount = sandbox._ragRefreshes || 0;
  await ragEval("showRagsView()");
  assert.equal(sandbox.location.pathname, "/rags", "external route without a source redirects to list");
  assert.equal(sandbox._ragRefreshes, refreshCount + 1, "redirect lands on the list");

  const staleFile = { name: "stale.db", size: 1 };
  const openPromise = sandbox.ragOpenExternalFile(staleFile);
  assert.equal(ragState.externalLoading, true);
  sandbox.ragStartCreate();
  await openPromise;
  assert.equal(ragState.externalFile, null, "stale open cannot replace newer form state");
  assert.equal(ragState.externalLoading, false);
  assert.equal(ragState.entries.length, 1, "new form gets a fresh entry");
  assert.equal(ragState.entries[0].id, 0, "external entry ids are not reused");
  assert.equal(Object.keys(ragState.entries[0].media).length, 0, "external media refs are not reused");
  assert.equal(els.get("rag-new-name").value, "", "name cleared when leaving external");

  sandbox.location.pathname = "/rags";
  const navOpen = sandbox.ragOpenExternalFile(extFile);
  assert.equal(ragState.externalLoading, true);
  sandbox.location.pathname = "/";
  await navOpen;
  assert.equal(ragState.externalFile, null, "navigation away drops a pending open");
  assert.equal(ragState.externalLoading, false, "stale open still unlocks");
  assert.equal(sandbox.location.pathname, "/", "stale open must not hijack the new view");

  ragState.externalFile = extFile;
  ragState.entries = [sandbox.ragNewEntry()];
  ragState.entries[0].id = 7;
  ragState.entries[0].media.image = { existing: true, entryID: 7 };
  els.get("rag-new-name").value = "dirty name";
  els.get("rag-new-desc").value = "dirty desc";
  ragState.createPreferredModel = "stale-external-model";
  sandbox.location.pathname = "/rags";
  await ragEval("showRagsView()");
  assert.equal(ragState.externalFile, null, "leaving external to the list clears the source");
  assert.equal(ragState.entries.length, 0, "external entries cleared on leaving external");
  assert.equal(els.get("rag-new-name").value, "", "list reset clears external name");
  assert.equal(els.get("rag-new-desc").value, "", "list reset clears external description");
  assert.notEqual(ragState.createPreferredModel, "stale-external-model", "list reset clears preferred model");

  sandbox.currentConfig = { rag: { default_embedding: "embed-multimodal:latest" } };
  sandbox.location.pathname = "/rags/new";
  await ragEval("showRagsView()");
  await new Promise((r) => setImmediate(r));
  assert.equal(ragState.entries.length, 1, "new after external gets a fresh entry");
  assert.equal(ragState.entries[0].id, 0, "new after external reuses no external id");
  assert.equal(Object.keys(ragState.entries[0].media).length, 0, "new after external reuses no media");
  ragState.entries[0].term = "n";
  ragState.entries[0].content = "c";
  els.get("rag-new-name").value = "Ordinary";
  await sandbox.ragSubmitCreate();
  assert.equal(sandbox.location.pathname, "/rags", "ordinary create navigates to the list");
  assert.equal(openBtn.disabled, false, "open button re-enabled after ordinary create");
  assert.equal(els.get("rags-new-btn").disabled, false, "new button re-enabled after ordinary create");
  sandbox.currentConfig = {};

  sandbox._failOpen = true;
  toasts.length = 0;
  await sandbox.ragOpenExternalFile(extFile);
  assert.equal(ragState.externalFile, null);
  assert.equal(ragState.externalLoading, false, "failed open unlocks");
  assert.equal(toasts[0] && toasts[0].type, "error");
  sandbox._failOpen = false;

  ragState.externalFile = extFile;
  els.get("rag-create-cancel-btn").dispatch("click", {});
  assert.equal(ragState.externalFile, null, "cancel clears external state");
  assert.equal(sandbox.location.pathname, "/rags");

  const i18nSrc2 = readFileSync(join(web, "i18n.js"), "utf8");
  for (const key of ["rag.open_external", "rag.save_local", "rag.download_edited", "rag.external_hint"]) {
    assert.equal((i18nSrc2.match(new RegExp(`"${key.replace(".", "\\.")}"`, "g")) || []).length, 2, `EN+ES label ${key}`);
  }
}

{
  const ragState = ragEval("ragState");
  const entryOf = (k) => ragState.entries.find((e) => e.key === k);
  const len = (v) => sandbox.ragContentLength(v);
  assert.equal(len(""), 0);
  assert.equal(len("  padded  "), 6, "surrounding whitespace is not counted");
  assert.equal(len("\u00a0x\u00a0"), 1, "NBSP belongs to the Go whitespace class");
  assert.equal(len("\u0085x\u0085"), 1, "U+0085 belongs to the Go whitespace class");
  assert.equal(len("\uFEFFx"), 2, "FEFF is kept, matching Go TrimSpace");
  assert.equal(len("\u{1F600}"), 1, "non-BMP characters count as one codepoint");
  assert.equal(len("e\u0301"), 2, "combining marks count per rune");
  assert.equal(len("a".repeat(32000)), 32000);
  assert.equal(len("a".repeat(32001)), 32001);

  const goSrc = readFileSync(join(web, "..", "internal/server/rag.go"), "utf8");
  const jsSrc = readFileSync(join(web, "app-rag.js"), "utf8");
  assert.match(goSrc, /ragMaxContentLen\s*=\s*32000/, "backend limit stays 32000");
  assert.match(jsSrc, /RAG_MAX_CONTENT_CHARS\s*=\s*32000/, "frontend constant matches backend");
  const css = readFileSync(join(web, "style.css"), "utf8");
  assert.match(css, /textarea\.rag-entry-content\s*\{[^}]*min-height:\s*340px/, "larger content area");
  assert.match(jsSrc, /content\.rows\s*=\s*14/, "textarea rows increased");

  const extFile2 = { name: "counter.db", size: 1 };
  await sandbox.ragOpenExternalFile(extFile2);
  assert.equal(ragState.externalFile, extFile2);
  const host = els.get("rag-entries");
  const findTa = () => findDeep(host, (c) => c.tagName === "TEXTAREA")[0];
  const findCount = () => findDeep(host, (c) => c._classes && c._classes.has("rag-content-count"))[0];
  const findWarn = () => findDeep(host, (c) => c._classes && c._classes.has("rag-content-warn"))[0];
  const createBtn2 = els.get("rag-create-btn");
  const dlBtn2 = els.get("rag-download-edited-btn");
  const saveCalls = () => sandbox._apiCalls.filter((c) => c.url === "/api/rags/external/save").length;
  const saveBase = saveCalls();

  let ta = findTa();
  let count = findCount();
  assert.ok(ta && count, "counter rendered on external detail load");
  const loc = (n) => ragEval(`(${n}).toLocaleString()`);
  const expected = (n) => `rag.content_count:count=${loc(n)},max=${loc(32000)}`;
  assert.equal(count.textContent, expected(len(ragState.entries[0].content)), "counter reflects loaded content");
  assert.equal(count.attrs["aria-live"], "polite", "count announces politely");
  assert.equal(ta.attrs["aria-invalid"], "false");

  ta.value = "x".repeat(32000);
  ta.dispatch("input", {});
  assert.equal(createBtn2.disabled, false, "32000 exact stays saveable");
  assert.equal(findTa().classList.contains("rag-limit-warn"), true, "near-limit warn at >=90%");
  assert.equal(findWarn().hidden, true, "no inline warning while within limit");

  ta.value = "x".repeat(32001);
  ta.dispatch("input", {});
  ta = findTa();
  assert.equal(ta.classList.contains("rag-limit-over"), true, "over-limit styling");
  assert.equal(ta.attrs["aria-invalid"], "true", "aria-invalid only over limit");
  assert.equal(findWarn().hidden, false, "inline warning visible");
  assert.match(findWarn().textContent, /rag\.content_too_long:max=/);
  assert.equal(createBtn2.disabled, true, "over-limit disables save");
  assert.equal(dlBtn2.disabled, true, "over-limit disables download");
  ragEval("ragSetCreateDisabled(false)");
  assert.equal(createBtn2.disabled, true, "unlock never bypasses the limit guard");
  const navFor0 = () => els.get("rag-entry-list").querySelectorAll(".rag-entry-nav")
    .find((b) => b.dataset.entryKey === String(ragState.entries[0].key));
  assert.equal(navFor0().classList.contains("over-limit"), true, "input marks the nav entry without rerender");
  assert.match(navFor0().title, /alpha.*rag\.content_too_long/, "nav title carries entry and warning");
  assert.equal(navFor0().attrs["aria-label"], navFor0().title);

  await sandbox.ragSubmitCreate();
  assert.equal(saveCalls(), saveBase, "over-limit submit sends no request");
  assert.match(els.get("rag-create-status").textContent, /rag\.content_too_long_entry/);

  const bigEntry = ragState.entries[0];
  ragState.entries.push(sandbox.ragNewEntry());
  ragState.activeEntry = ragState.entries[ragState.entries.length - 1].key;
  ragEval("ragRenderEntries()");
  assert.equal(createBtn2.disabled, true, "inactive over-limit entry still disables save");
  assert.equal(findCount().textContent, expected(0), "switching shows the active entry's count");
  findTa().value = "ok";
  findTa().dispatch("input", {});
  const navBtns = els.get("rag-entry-list").querySelectorAll("button");
  const overNav = navBtns.find((b) => b.classList.contains("over-limit"));
  assert.ok(overNav, "nav marks the over-limit entry");
  assert.match(overNav.title, /rag\.content_too_long/, "nav title carries the error");
  sandbox.ragSetActiveEntry(bigEntry.key);
  findDeep(host, (c) => c._classes && c._classes.has("rag-entry-remove"))[0].dispatch("click", {});
  assert.equal(createBtn2.disabled, false, "removing the invalid entry re-enables save");

  const fileIn = findDeep(host, (c) => c.tagName === "INPUT" && c.type === "file" && String(c.accept || "").includes(".txt"))[0];
  const overText = "imp".repeat(12000);
  fileIn.files = [{ name: "big.txt", size: overText.length }];
  fileIn.dispatch("change", { target: fileIn });
  FakeFileReader.instances.at(-1).result = overText;
  FakeFileReader.instances.at(-1).onload();
  await new Promise((r) => setImmediate(r));
  assert.equal(ragState.entries[0].content, overText, "over-limit import keeps the full string");
  assert.equal(ragEval("ragBusy()"), false, "editor unlocked after over-limit import");
  assert.equal(createBtn2.disabled, true, "save stays disabled until corrected");
  const fixTa = findDeep(host, (c) => c.tagName === "TEXTAREA")[0];
  fixTa.value = "fixed";
  fixTa.dispatch("input", {});
  assert.equal(createBtn2.disabled, false, "correcting re-enables save");
  assert.equal(navFor0().classList.contains("over-limit"), false, "correction clears nav marker");
  assert.equal(navFor0().title, "", "stale nav error title cleared");
  assert.equal(navFor0().attrs["aria-label"], "", "stale nav aria-label cleared");

  ragState.externalFile = null;
  ragState.entries = [sandbox.ragNewEntry()];
  ragEval("ragRenderEntries()");
  assert.equal(findDeep(host, (c) => c._classes && c._classes.has("rag-content-count"))[0].textContent, expected(0),
    "empty draft counts zero");
}

{
  const apiSrc = readFileSync(join(web, "app-api.js"), "utf8");
  const apiSandbox = {
    console,
    window: { location: { href: "" } },
    fetchCalls: [],
    reauths: 0,
    _queue: [],
    _enqueue(res) { this._queue.push(res); },
    fetch: null,
    promptInPlaceAuth: null,
    Blob: FakeBlob,
    FormData: FakeFormData,
  };
  apiSandbox.fetch = (path, opts) => {
    apiSandbox.fetchCalls.push({ path, opts });
    return Promise.resolve(apiSandbox._queue.shift());
  };
  apiSandbox.promptInPlaceAuth = () => {
    apiSandbox.reauths++;
    return Promise.resolve();
  };
  apiSandbox.globalThis = apiSandbox;
  vm.createContext(apiSandbox);
  vm.runInContext(apiSrc.slice(apiSrc.indexOf("async function api(")), apiSandbox, { filename: "app-api.js:api" });
  const api = apiSandbox.api;
  const mkRes = (o) => ({
    ok: o.ok !== false,
    status: o.status || 200,
    statusText: o.statusText || "",
    json: async () => (o.json !== undefined ? o.json : {}),
    blob: async () => (o.blob !== undefined ? o.blob : new FakeBlob([])),
  });
  const blobOut = new FakeBlob(["db"]);

  apiSandbox._enqueue(mkRes({ json: { hello: 1 } }));
  assert.deepEqual(await api("/api/x"), { hello: 1 }, "default response stays JSON");

  apiSandbox._enqueue(mkRes({ blob: blobOut }));
  assert.equal(await api("/api/x", {}, "blob"), blobOut, "blob responseType returns res.blob()");

  apiSandbox._enqueue(mkRes({ ok: false, status: 401 }));
  apiSandbox._enqueue(mkRes({ blob: blobOut }));
  const callsBefore = apiSandbox.fetchCalls.length;
  assert.equal(await api("/api/x", {}, "blob"), blobOut, "401 re-auths and retries");
  assert.equal(apiSandbox.reauths, 1);
  assert.equal(apiSandbox.fetchCalls.length - callsBefore, 2, "401 triggers exactly one retried request");

  apiSandbox._enqueue(mkRes({ ok: false, status: 502, statusText: "Bad Gateway", json: { error: "embed broke" } }));
  await assert.rejects(() => api("/api/x", {}, "blob"), (e) => {
    assert.equal(e.message, "embed broke");
    assert.equal(e.status, 502, "blob errors keep the status");
    return true;
  });
}

assert.match(svgSrc, /if \(!isImageModel && typeof chatRagOptionPayload === "function"\)/);
assert.match(svgSrc, /payload\.rag_enabled = true;/);
assert.match(svgSrc, /payload\.rag_paths = ragOpts\.rag_paths;/);
assert.match(svgSrc, /payload\.rag_editable = ragOpts\.rag_editable;/);

console.log("chat-rag.test.mjs: all assertions passed");
