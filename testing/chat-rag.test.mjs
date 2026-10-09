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

const els = new Map();
const fakeEl = (id) => {
  if (!els.has(id)) {
    els.set(id, {
      id, value: "", innerHTML: "", textContent: "", title: "", hidden: false,
      checked: false, disabled: false,
      selectedOptions: [],
      addEventListener() {}, focus() {}, select() {}, scrollIntoView() {},
      closest: () => null,
      querySelectorAll: () => [],
      classList: { toggle() {}, add() {}, remove() {} },
      dataset: {},
    });
  }
  return els.get(id);
};

const toasts = [];
const imageOnly = { value: false };
const subscribed = [];

const sandbox = {
  console,
  URL,
  t: (k, p) => `${k}:${p && p.msg ? p.msg : ""}`,
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
  chatRagRenderSelection() {},
  chatRagSyncSession: async () => {},
  chatRagCommit(syncSession) {
    if (!sandbox.chatRagEnabled) sandbox.chatRagPaths = [];
    sandbox.chatRagEditable = (sandbox.chatRagEditable || []).filter((p) => sandbox.chatRagPaths.includes(p));
    if (syncSession) void sandbox.chatRagSyncSession();
  },
  normalizeChatRagPaths: (list) => [...new Set((list || []).map((p) => String(p || "").trim()).filter((p) => p && p.endsWith(".db") && !p.includes("/")))],
  normalizeChatRagEditable: (list, paths) => sandbox.normalizeChatRagPaths(list).filter((p) => paths.includes(p)),
  chatRagFetchList: async () => [{ filename: "a.db" }, { filename: "b.db" }],
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
    getElementById: (id) => els.get(id) || null,
    querySelectorAll: () => [],
    documentElement: {},
    readyState: "complete",
    addEventListener() {},
  },
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
  sandbox.chatSessionStream.close();
  sandbox.chatSessionStream = null;
}

assert.match(svgSrc, /if \(!isImageModel && typeof chatRagOptionPayload === "function"\)/);
assert.match(svgSrc, /payload\.rag_enabled = true;/);
assert.match(svgSrc, /payload\.rag_paths = ragOpts\.rag_paths;/);

console.log("chat-rag.test.mjs: all assertions passed");
