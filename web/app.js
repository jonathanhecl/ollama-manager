"use strict";

const t = (k, v) => window.I18n.t(k, v);

// ---------- helpers ----------
const $ = (id) => document.getElementById(id);
const fmtBytes = (n) => {
  if (!n || n <= 0) return "—";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n < 10 && i > 0 ? 2 : 1)} ${u[i]}`;
};
const fmtDate = (s) => {
  if (!s) return "—";
  const d = new Date(s);
  if (isNaN(d)) return "—";
  const now = new Date();
  const diffDays = Math.floor((now - d) / 86400000);
  if (diffDays < 1) return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  if (diffDays < 7) return getRelativeTimeFormatter().format(-diffDays, "day");
  return d.toLocaleDateString();
};
const RELATIVE_UNITS = [
  { unit: "year", ms: 365 * 24 * 60 * 60 * 1000 },
  { unit: "month", ms: 30 * 24 * 60 * 60 * 1000 },
  { unit: "day", ms: 24 * 60 * 60 * 1000 },
  { unit: "hour", ms: 60 * 60 * 1000 },
  { unit: "minute", ms: 60 * 1000 },
  { unit: "second", ms: 1000 },
];
const _rtfCache = new Map();
function getRelativeTimeFormatter() {
  const lang = window.I18n?.getLang?.() || "en";
  const locale = lang === "es" ? "es-AR" : "en-US";
  const key = `${locale}:auto`;
  if (_rtfCache.has(key)) return _rtfCache.get(key);
  const fmt = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  _rtfCache.set(key, fmt);
  return fmt;
}
function fmtRelativeTime(s) {
  if (!s) return "—";
  const d = new Date(s);
  if (isNaN(d)) return "—";
  const diff = d.getTime() - Date.now();
  const abs = Math.abs(diff);
  const rtf = getRelativeTimeFormatter();
  if (abs < 1000) return rtf.format(0, "second");
  for (const u of RELATIVE_UNITS) {
    if (abs >= u.ms || u.unit === "second") {
      const value = Math.round(diff / u.ms);
      return rtf.format(value, u.unit);
    }
  }
  return "—";
}
function fmtDateTimeFull(s) {
  if (!s) return "—";
  const d = new Date(s);
  if (isNaN(d)) return "—";
  const lang = window.I18n?.getLang?.() || "en";
  const locale = lang === "es" ? "es-AR" : "en-US";
  return d.toLocaleString(locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}
function fmtDuration(ms) {
  if (ms == null || ms === undefined || ms < 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = (ms / 1000).toFixed(2);
  const lang = window.I18n?.getLang?.() || "en";
  return lang === "es" ? `${s.replace(".", ",")}s` : `${s}s`;
}
const fmtCtx = (n) => {
  if (!n) return "—";
  if (n >= 1024) return `${(n / 1024).toFixed(0)}K`;
  return String(n);
};

function formatMetaElapsed(ms) {
  const n = Math.max(0, Number(ms) || 0);
  if (n < 1000) {
    return t("chat.meta_time_ms", { ms: Math.round(n) });
  }
  const sec = n / 1000;
  if (sec < 10) {
    return t("chat.meta_time_s_dec", { s: sec.toFixed(1) });
  }
  return t("chat.meta_time", { s: Math.round(sec) });
}
function fmtETA(totalSeconds) {
  if (!isFinite(totalSeconds) || totalSeconds <= 0) return "";
  const secs = Math.ceil(totalSeconds);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  const s = secs % 60;
  if (mins < 60) return `${mins}m ${s}s`;
  const hrs = Math.floor(mins / 60);
  const m = mins % 60;
  return `${hrs}h ${m}m`;
}
function fmtSpeed(bps) {
  if (!bps || bps <= 0) return "";
  const u = ["B/s", "KB/s", "MB/s", "GB/s"];
  let i = 0;
  let n = bps;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n < 10 && i > 0 ? 2 : 1)} ${u[i]}`;
}
const escapeHtml = (s) => String(s ?? "")
  .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
  .replace(/"/g, "&quot;").replace(/'/g, "&#39;");

function attachmentImageSrc(a) {
  if (!a || a.kind !== "image" || !a.data) return "";
  const mime = (a.mime && String(a.mime).trim()) || "image/jpeg";
  return `data:${mime};base64,${a.data}`;
}

function attachmentAudioSrc(a) {
  if (!a || a.kind !== "audio" || !a.data) return "";
  const mime = (a.mime && String(a.mime).trim()) || "audio/webm";
  return `data:${mime};base64,${a.data}`;
}

function attachmentTextPreview(a, max = 140) {
  const txt = String(a?.text || "").replace(/\s+/g, " ").trim();
  if (!txt) return "";
  if (txt.length <= max) return txt;
  return `${txt.slice(0, max - 1)}…`;
}

function isTextAttachmentFile(file) {
  const name = String(file?.name || "").toLowerCase();
  const type = String(file?.type || "").toLowerCase();
  return type === "text/plain"
    || type === "text/markdown"
    || name.endsWith(".txt")
    || name.endsWith(".md")
    || name.endsWith(".markdown");
}

function openImagePreview(src, name) {
  const modal = $("image-preview-modal");
  const img = $("image-preview-img");
  const cap = $("image-preview-caption");
  if (!modal || !img) return;
  img.src = src;
  img.alt = name || "";
  if (cap) {
    cap.textContent = name || "";
    cap.hidden = !String(name || "").trim();
  }
  modal.hidden = false;
  document.body.style.overflow = "hidden";
}

function closeImagePreview() {
  const modal = $("image-preview-modal");
  const img = $("image-preview-img");
  if (!modal) return;
  modal.hidden = true;
  if (img) {
    img.removeAttribute("src");
    img.alt = "";
  }
  document.body.style.overflow = "";
}

function toast(msg, kind = "") {
  const div = document.createElement("div");
  div.className = "toast-item " + kind;
  div.textContent = msg;
  $("toast").appendChild(div);
  setTimeout(() => { div.style.opacity = "0"; div.style.transition = "opacity .3s"; }, 3500);
  setTimeout(() => div.remove(), 4000);
}

function speechLangFromUi() {
  const lang = window.I18n?.getLang?.() || "en";
  return lang === "es" ? "es-ES" : "en-US";
}

function findBestVoice(langTag) {
  if (!window.speechSynthesis) return null;
  const voices = window.speechSynthesis.getVoices() || [];
  if (!voices.length) return null;
  const want = String(langTag || "").toLowerCase();
  const exact = voices.find((v) => String(v.lang || "").toLowerCase() === want);
  if (exact) return exact;
  const prefix = want.split("-")[0];
  const byPrefix = voices.find((v) => String(v.lang || "").toLowerCase().startsWith(prefix));
  return byPrefix || voices[0] || null;
}

function textForSpeech(raw) {
  let s = String(raw || "");
  s = s.replace(/```[\s\S]*?```/g, " ");
  s = s.replace(/`([^`]+)`/g, "$1");
  s = s.replace(/\[([^\]]+)\]\([^)]+\)/g, "$1");
  s = s.replace(/[#*_>~-]+/g, " ");
  s = s.replace(/\s+/g, " ").trim();
  return s;
}

// ---------- state ----------
let models = [];
let activeName = null;
let jobs = new Map();   // id -> job
let jobsStream = null;  // EventSource for /api/jobs/events
let jobsBackoffMs = 1000;
let queuePaused = false;
let currentView = "models";
let showArchivedOnly = false;
let chatMessages = [];
let chatAttachments = [];
let chatStreamLock = false;
let chatRenderRaf = null;
let chatAbortController = null;
let chatThinkTicker = null;
let chatLastUsedTokens = 0;
let chatDndDepth = 0;
let chatPendingQueue = [];
let chatIsRecording = false;
let chatRecorderStream = null;
let chatAudioContext = null;
let chatAudioSource = null;
let chatAudioProcessor = null;
let chatAudioBuffers = [];
let chatAudioSampleRate = 0;
let speakingMsgId = "";
let activeStreamMessage = null;
const CHAT_OPTION_FALLBACKS = { temperature: 0.7, top_k: 40, top_p: 0.9 };
const STATUS_REFRESH_MS = 1000;
const chatModelDefaultsCache = new Map();
let chatDefaultsReqSeq = 0;
let lastChatDefaultsModel = "";
/** /api/status succeeded since last call */
let managerApiOk = false;
/** Ollama host reachable (from /api/status) */
let ollamaHostOk = false;
let lastSystemStatus = null;
let runningModels = [];
let runningRefreshTimer = null;

// Tests panel state.
let testsGroups = [];
let tests = [];
let selectedGroupId = "";
let currentTestId = null; // null for new, id for edit
let testEditorAttachments = []; // {id, kind, name, mime, data}

// Battery runner state.
let batteryModels = []; // installed models fetched for picker
let batterySelectedModels = new Set();
let currentBatteryRun = null;

// Agent session state.
let currentAgentSession = null; // session object from API
let currentAgentTestId = null;

// Sorting: persisted across reloads.
const SORT_KEY = "ollamaMgr.sort";
let sort = { col: "modified_at", dir: "desc" };
try {
  const saved = JSON.parse(localStorage.getItem(SORT_KEY) || "null");
  if (saved && saved.col) sort = saved;
} catch {}

// ---------- API ----------
async function api(path, opts = {}) {
  const res = await fetch(path, { credentials: "same-origin", ...opts });
  if (res.status === 401) {
    window.location.href = "/login";
    throw new Error("unauthorized");
  }
  if (!res.ok) {
    let err = res.statusText;
    try { const j = await res.json(); if (j.error) err = j.error; } catch {}
    throw new Error(err);
  }
  return res.json();
}

// ---------- status ----------
async function refreshStatus() {
  try {
    const s = await api("/api/status");
    lastSystemStatus = s;
    managerApiOk = true;
    ollamaHostOk = !!s.ollama_reachable;
    if (s.language && s.language !== window.I18n.getLang()) {
      window.I18n.setLang(s.language);
    }
    const pill = $("status-pill");
    if (s.ollama_reachable) {
      pill.textContent = t("status.online");
      pill.className = "pill pill-good";
    } else {
      pill.textContent = t("status.offline");
      pill.className = "pill pill-bad";
    }
    $("settings-logout-btn").hidden = !s.has_password;
    updateSystemWidgets(s);
    updateChatSendEnabled();
  } catch (e) {
    lastSystemStatus = null;
    managerApiOk = false;
    ollamaHostOk = false;
    $("status-pill").textContent = t("status.unreachable");
    $("status-pill").className = "pill pill-bad";
    updateSystemWidgets(null);
    updateChatSendEnabled();
  }
}

function updateSystemWidgets(status) {
  const compact = window.matchMedia("(max-width: 900px)").matches;

  updateMetricWidget({
    wrapId: "cpu-widget",
    fillId: "cpu-widget-fill",
    textId: "cpu-widget-text",
    pct: Number(status?.cpu_used_pct),
    text: t("status.cpu_short", { pct: Math.round(Number(status?.cpu_used_pct) || 0) }),
    title: t("status.cpu_title", { pct: Math.round(Number(status?.cpu_used_pct) || 0) }),
    warn: true,
  });

  updateMemoryWidget(status, compact);

  updateDiskWidget(status, compact);
}

function installedModelsBytes() {
  return models.reduce((acc, m) => {
    const size = Number(m?.size);
    if (!Number.isFinite(size) || size <= 0) return acc;
    return acc + size;
  }, 0);
}

function loadedModelsTotalEstimateBytes() {
  return models.reduce((acc, m) => {
    if (!m || !m.loaded) return acc;
    const size = Number(m?.size);
    const total = Number.isFinite(size) && size > 0 ? size : 0;
    return acc + total;
  }, 0);
}

function updateMemoryWidget(status, compact) {
  const wrap = $("memory-widget");
  const modelsFill = $("memory-widget-fill-models");
  const otherFill = $("memory-widget-fill");
  const textNode = $("memory-widget-text");
  if (!wrap || !modelsFill || !otherFill || !textNode) return;

  const memoryTotal = Number(status?.memory_total) || 0;
  const memoryUsedRaw = Number(status?.memory_used) || 0;
  const memoryPct = Number(status?.memory_used_pct);
  if (memoryTotal <= 0 || !Number.isFinite(memoryPct)) {
    wrap.hidden = true;
    return;
  }

  const memoryUsed = Math.max(0, Math.min(memoryUsedRaw, memoryTotal));
  const hasServerLoadedTotal = !!(status && Object.prototype.hasOwnProperty.call(status, "models_loaded_bytes"));
  const loadedModelsApprox = hasServerLoadedTotal
    ? (Number(status?.models_loaded_bytes) || 0)
    : loadedModelsTotalEstimateBytes();
  const modelUsed = Math.min(Math.max(0, loadedModelsApprox), memoryUsed);
  const otherUsed = Math.max(0, memoryUsed - modelUsed);

  const modelsPct = (modelUsed / memoryTotal) * 100;
  const otherPct = (otherUsed / memoryTotal) * 100;
  const freePct = ((memoryTotal - memoryUsed) / memoryTotal) * 100;

  modelsFill.style.width = `${Math.max(0, Math.min(100, modelsPct)).toFixed(1)}%`;
  otherFill.style.width = `${Math.max(0, Math.min(100, otherPct)).toFixed(1)}%`;
  textNode.textContent = compact
    ? t("status.percent_short", { pct: Math.round(memoryPct) })
    : t("status.memory_short", { used: fmtBytes(memoryUsed), total: fmtBytes(memoryTotal) });
  wrap.title = t("status.memory_breakdown_title", {
    models: fmtBytes(modelUsed),
    other: fmtBytes(otherUsed),
    free: fmtBytes(Math.max(0, memoryTotal - memoryUsed)),
    total: fmtBytes(memoryTotal),
    pct: Math.round(freePct),
  });
  wrap.hidden = false;
}

function updateDiskWidget(status, compact) {
  const wrap = $("disk-widget");
  const modelsFill = $("disk-widget-fill-models");
  const otherFill = $("disk-widget-fill");
  const textNode = $("disk-widget-text");
  if (!wrap || !modelsFill || !otherFill || !textNode) return;

  const diskTotal = Number(status?.disk_total_bytes) || 0;
  const diskFree = Number(status?.disk_free_bytes) || 0;
  if (diskTotal <= 0) {
    wrap.hidden = true;
    return;
  }

  const clampedFree = Math.max(0, Math.min(diskFree, diskTotal));
  const diskUsed = Math.max(0, diskTotal - clampedFree);
  const modelUsed = Math.min(Math.max(0, installedModelsBytes()), diskUsed);
  const otherUsed = Math.max(0, diskUsed - modelUsed);

  const modelsPct = (modelUsed / diskTotal) * 100;
  const otherPct = (otherUsed / diskTotal) * 100;
  const freePct = (clampedFree / diskTotal) * 100;
  modelsFill.style.width = `${Math.max(0, Math.min(100, modelsPct)).toFixed(1)}%`;
  otherFill.style.width = `${Math.max(0, Math.min(100, otherPct)).toFixed(1)}%`;

  textNode.textContent = compact
    ? fmtBytes(clampedFree)
    : t("status.disk_free_short", { free: fmtBytes(clampedFree), total: fmtBytes(diskTotal) });
  wrap.title = t("status.disk_breakdown_title", {
    models: fmtBytes(modelUsed),
    other: fmtBytes(otherUsed),
    free: fmtBytes(clampedFree),
    total: fmtBytes(diskTotal),
    pct: Math.round(freePct),
  });
  wrap.hidden = false;
}

function updateMetricWidget({ wrapId, fillId, textId, pct, text, title, warn = false, bad = false, hideWhenInvalid = true }) {
  const wrap = $(wrapId);
  const fill = $(fillId);
  const textNode = $(textId);
  if (!wrap || !fill || !textNode) return;

  if (!Number.isFinite(pct)) {
    if (hideWhenInvalid) {
      wrap.hidden = true;
      return;
    }
    fill.style.width = "0%";
    fill.classList.remove("warn", "bad");
    textNode.textContent = text || "—";
    wrap.title = title || "";
    wrap.hidden = false;
    return;
  }

  const clampedPct = Math.max(0, Math.min(100, pct));
  fill.style.width = `${clampedPct.toFixed(1)}%`;
  fill.classList.toggle("warn", !!warn && !bad);
  fill.classList.toggle("bad", !!bad);
  textNode.textContent = text || "—";
  wrap.title = title || "";
  wrap.hidden = false;
}

function updateChatSendEnabled() {
  const btn = $("chat-send-btn");
  if (!btn) return;
  let ok = managerApiOk && ollamaHostOk;
  if (!ok) {
    if (!managerApiOk) {
      btn.title = t("chat.send_disabled_manager");
    } else {
      btn.title = t("chat.send_disabled_ollama");
    }
  } else {
    btn.title = t("chat.send");
  }
  btn.disabled = !ok;
}

async function copyTextToClipboard(text) {
  const s = String(text || "");
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(s);
      return true;
    }
  } catch {
    // fall back
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = s;
    ta.style.position = "fixed";
    ta.style.left = "-9999px";
    document.body.appendChild(ta);
    ta.select();
    const r = document.execCommand("copy");
    ta.remove();
    return r;
  } catch {
    return false;
  }
}

// ---------- list ----------
async function refreshModels() {
  try {
    const data = await api("/api/models");
    models = data.models || [];
    updateSystemWidgets(lastSystemStatus);
    renderTable();
    syncChatModelOptions();
    updateChatCapabilityUI();
    updateChatContextMeter();
    await handleRouting();
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
    $("models-tbody").innerHTML = `<tr class="empty"><td colspan="9">${escapeHtml(t("state.error_prefix") + e.message)}</td></tr>`;
  }
}

function applyRunning(running) {
  const byName = new Map((running || []).map((r) => [r.name, r]));
  for (const m of models) {
    const rm = byName.get(m.name);
    m.loaded = !!rm;
    m.size_vram = rm ? (rm.size_vram || 0) : 0;
    m.expires_at = rm && rm.expires_at != null ? rm.expires_at : null;
  }
}

function updateLoadedDotsOnly() {
  if (!models.length) return;
  const dotLoadedTxt = t("detail.dot_loaded");
  const dotNotLoadedTxt = t("detail.dot_not_loaded");
  const byName = new Map(models.map((m) => [m.name, m]));
  $("models-tbody").querySelectorAll("tr.row").forEach((tr) => {
    const name = tr.dataset.name;
    if (!name) return;
    const m = byName.get(name);
    if (!m) return;
    const dot = tr.querySelector(".state-dot");
    if (!dot) return;
    dot.classList.toggle("loaded", !!m.loaded);
    dot.title = m.loaded ? dotLoadedTxt : dotNotLoadedTxt;
  });
}

function updateChatModelLoadDot() {
  const dot = $("chat-model-load-dot");
  const sel = $("chat-model");
  if (!dot || !sel) return;
  const m = modelByName(sel.value);
  const loaded = !!(m && m.loaded);
  dot.classList.toggle("loaded", loaded);
  dot.title = loaded ? t("detail.dot_loaded") : t("detail.dot_not_loaded");
}

async function refreshLoadedState() {
  try {
    const data = await api("/api/running");
    applyRunning(data.running);
    updateLoadedDotsOnly();
    updateChatModelLoadDot();
    patchDetailLoadedState();
  } catch {
    // Evita toasts ruidosos al sondear; el listado completo o el status ya avisan si hace falta.
  }
}

function renderRunningModalList() {
  const list = $("running-list");
  const empty = $("running-empty");
  const badge = $("running-count-badge");
  if (!list || !empty || !badge) return;

  const rows = [...(runningModels || [])]
    .sort((a, b) => (Number(b.size_vram) || 0) - (Number(a.size_vram) || 0));

  badge.textContent = String(rows.length);
  badge.hidden = rows.length === 0;
  empty.hidden = rows.length !== 0;

  if (!rows.length) {
    list.innerHTML = "";
    return;
  }

  list.innerHTML = rows.map((r) => {
    const name = String(r?.name || "").trim();
    const vram = fmtBytes(Number(r?.size_vram) || 0);
    const expires = r?.expires_at ? fmtRelativeTime(r.expires_at) : "—";
    const expiresFull = r?.expires_at ? fmtDateTimeFull(r.expires_at) : "—";
    return `
      <div class="running-item">
        <div class="running-main">
          <div class="running-name">${escapeHtml(name || "—")}</div>
          <div class="running-meta">
            <span>${escapeHtml(t("running.vram", { size: vram }))}</span>
            <span title="${escapeHtml(expiresFull)}">${escapeHtml(t("running.expires", { when: expires }))}</span>
          </div>
        </div>
        <button class="danger running-unload-btn" data-name="${escapeHtml(name)}">${escapeHtml(t("running.unload"))}</button>
      </div>
    `;
  }).join("");

  list.querySelectorAll(".running-unload-btn").forEach((btn) => {
    btn.addEventListener("click", async (e) => {
      const name = String(e.currentTarget?.dataset?.name || "").trim();
      if (!name) return;
      btn.disabled = true;
      try {
        await api("/api/models/unload", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ name }),
        });
        toast(t("running.unloaded", { name }), "success");
      } catch (err) {
        toast(t("running.unload_failed", { name, msg: err.message }), "error");
      } finally {
        btn.disabled = false;
      }
      await refreshRunningModalList({ silent: true });
      refreshLoadedState();
      refreshStatus();
    });
  });
}

async function refreshRunningModalList({ silent = false } = {}) {
  try {
    const data = await api("/api/running");
    runningModels = data.running || [];
    renderRunningModalList();
  } catch (e) {
    runningModels = [];
    renderRunningModalList();
    if (!silent) {
      toast(t("toast.error", { msg: e.message }), "error");
    }
  }
}

function closeRunningModal() {
  $("running-modal").hidden = true;
  if (runningRefreshTimer) {
    clearInterval(runningRefreshTimer);
    runningRefreshTimer = null;
  }
}

function openRunningModal() {
  $("running-modal").hidden = false;
  refreshRunningModalList();
  if (runningRefreshTimer) clearInterval(runningRefreshTimer);
  runningRefreshTimer = setInterval(() => {
    const modal = $("running-modal");
    if (!modal || modal.hidden) return;
    refreshRunningModalList({ silent: true });
  }, 3000);
}

function patchDetailLoadedState() {
  const el = $("detail-state-value");
  if (!el || !activeName || $("detail-panel").hidden) return;
  const m = models.find((x) => x.name === activeName);
  if (!m) return;
  const stateText = m.loaded
    ? t("detail.loaded_vram", { size: fmtBytes(m.size_vram) })
    : t("detail.not_loaded");
  el.textContent = stateText;
}

function sortKey(m, col) {
  switch (col) {
    case "name":           return (m.name || "").toLowerCase();
    case "family":         return (m.family || "").toLowerCase();
    case "parameter_size": return parseParamSize(m.parameter_size);
    case "quantization":   return (m.quantization || "").toLowerCase();
    case "context_length": return Number(m.context_length) || 0;
    case "size":           return Number(m.size) || 0;
    case "modified_at":    return new Date(m.modified_at).getTime() || 0;
    default:               return "";
  }
}

// Parse "8.2B", "268.10M", "137M" into a comparable number of parameters.
function parseParamSize(s) {
  if (!s) return 0;
  const m = String(s).match(/([\d.]+)\s*([KMBT])?/i);
  if (!m) return 0;
  const n = parseFloat(m[1]);
  const mult = { K: 1e3, M: 1e6, B: 1e9, T: 1e12 }[(m[2] || "").toUpperCase()] || 1;
  return n * mult;
}

function applySort(arr) {
  const { col, dir } = sort;
  // Special ordering for modified_at: queued first (newest), running second, installed last (newest)
  if (col === "modified_at") {
    return [...arr].sort((a, b) => {
      const isQueuedA = a.job?.status === "queued";
      const isQueuedB = b.job?.status === "queued";
      const typeA = isQueuedA ? 0 : a.job?.status === "running" ? 1 : 2;
      const typeB = isQueuedB ? 0 : b.job?.status === "running" ? 1 : 2;
      if (typeA !== typeB) return dir === "asc" ? (typeB - typeA) : (typeA - typeB);
      const timeA = a.job?.created_at ? new Date(a.job.created_at).getTime() : new Date(a.modified_at).getTime();
      const timeB = b.job?.created_at ? new Date(b.job.created_at).getTime() : new Date(b.modified_at).getTime();
      return dir === "asc" ? (timeA - timeB) : (timeB - timeA);
    });
  }
  const mul = dir === "asc" ? 1 : -1;
  return [...arr].sort((a, b) => {
    const ka = sortKey(a, col);
    const kb = sortKey(b, col);
    if (typeof ka === "string" || typeof kb === "string") {
      return mul * String(ka).localeCompare(String(kb));
    }
    return mul * (ka - kb);
  });
}

function renderTable() {
  updateSortIndicators();
  const tbody = $("models-tbody");
  
  // Filter models based on archived state
  const filteredModels = models.filter(m => !!m.archived === showArchivedOnly).map(m => ({...m}));

  if (!filteredModels.length && showArchivedOnly) {
    tbody.innerHTML = `<tr class="empty"><td colspan="9">${escapeHtml(t("state.empty_archived"))}</td></tr>`;
    return;
  }
  if (!filteredModels.length && !showArchivedOnly) {
    tbody.innerHTML = `<tr class="empty"><td colspan="9">${escapeHtml(t("state.empty_models"))}</td></tr>`;
    return;
  }
  // Attach active jobs to installed models so they participate in sorting and display.
  // Only queued and running jobs appear in the main list; paused ones stay in the downloads modal.
  const installedNames = new Set(models.map(m => m.name));
  const pendingModels = [];
  const runningJobByName = new Map();
  if (!showArchivedOnly) {
    for (const j of jobs.values()) {
      if (j.status === "running") runningJobByName.set(j.name, j);
      if (j.status === "running" || j.status === "queued") {
        const model = filteredModels.find(m => m.name === j.name);
        if (model) {
          model.job = j;
        } else {
          pendingModels.push({
            name: j.name,
            isPending: true,
            job: j,
            family: "—",
            parameter_size: "—",
            quantization: "—",
            context_length: 0,
            size: 0,
            modified_at: j.created_at,
            capabilities: []
          });
        }
      }
    }
  }

  const allToRender = applySort([...filteredModels, ...pendingModels]);

  const dotLoadedTxt = t("detail.dot_loaded");
  const dotNotLoadedTxt = t("detail.dot_not_loaded");
  const deleteTitle = t("detail.delete_title");
  const infoTitle = t("detail.info_btn");
  const archiveTitle = t("detail.archive_title");
  const unarchiveTitle = t("detail.unarchive_title");
  const renderCapabilities = (caps) => (caps || [])
    .map((c) => `<span class="pill">${escapeHtml(c)}</span>`)
    .join("");

  tbody.innerHTML = allToRender.map((m) => {
    const capsHtml = renderCapabilities(m.capabilities);
    const rowClass = m.isPending ? "row pending" : `row${m.name === activeName ? " active" : ""}`;
    const job = m.job || runningJobByName.get(m.name);
    const pct = job && job.status === "running" ? Math.max(0, Math.min(100, job.percent || 0)) : 0;
    const progressHtml = job && job.status === "running"
      ? `<div class="model-progress"><div class="model-progress-bar" style="width:${pct.toFixed(1)}%"></div></div>`
      : "";
    return `
    <tr class="${rowClass}" data-name="${escapeHtml(m.name)}" ${m.isPending ? 'title="Downloading..." style="pointer-events: none;"' : ''}>
      <td class="col-state"><span class="state-dot${m.loaded ? " loaded" : ""}" title="${m.loaded ? dotLoadedTxt : dotNotLoadedTxt}"></span></td>
      <td class="cell-name">
        <div class="model-name-wrap">
          <div class="model-name-block">
            <div class="model-name">${escapeHtml(m.name)}</div>
            ${progressHtml}
            ${capsHtml ? `<div class="cap-list model-cap-list">${capsHtml}</div>` : ""}
          </div>
          ${!m.isPending ? `<button type="button" class="btn-icon info-btn" data-name="${escapeHtml(m.name)}" title="${escapeHtml(infoTitle)}" aria-label="${escapeHtml(infoTitle)}"><span class="info-glyph" aria-hidden="true">i</span></button>` : ""}
        </div>
      </td>
      <td>${escapeHtml(m.family || "—")}</td>
      <td class="cell-params">${escapeHtml(m.parameter_size || "—")}</td>
      <td class="cell-quant">${escapeHtml(m.quantization || "—")}</td>
      <td class="cell-ctx">${m.isPending ? "—" : fmtCtx(m.context_length)}</td>
      <td class="cell-size">${m.isPending ? "—" : fmtBytes(m.size)}</td>
      <td class="cell-modified">${m.isPending ? "—" : fmtDate(m.modified_at)}</td>
      <td class="col-actions">
        ${!m.isPending ? `
          <button class="btn-icon delete-btn" title="${escapeHtml(deleteTitle)}" data-name="${escapeHtml(m.name)}">×</button>
        ` : ""}
      </td>
    </tr>
  `;
  }).join("");

  tbody.querySelectorAll("tr.row").forEach((tr) => {
    tr.addEventListener("click", (e) => {
      if (e.target.closest(".info-btn")) return;
      if (e.target.closest(".delete-btn")) return;
      showChatViewWithModel(tr.dataset.name);
    });
  });
  tbody.querySelectorAll(".info-btn").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      openDetail(btn.dataset.name);
    });
  });
  tbody.querySelectorAll(".delete-btn").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      confirmDelete(btn.dataset.name);
    });
  });
}

function updateSortIndicators() {
  document.querySelectorAll("#models-table th.sortable").forEach((th) => {
    th.classList.remove("sort-asc", "sort-desc");
    if (th.dataset.sort === sort.col) {
      th.classList.add(sort.dir === "asc" ? "sort-asc" : "sort-desc");
    }
  });
}

// Header click handlers (delegated; works for the static thead).
document.querySelectorAll("#models-table th.sortable").forEach((th) => {
  th.addEventListener("click", () => {
    const col = th.dataset.sort;
    if (sort.col === col) {
      sort.dir = sort.dir === "asc" ? "desc" : "asc";
    } else {
      sort.col = col;
      // Numeric defaults: largest first; text defaults: A→Z.
      sort.dir = ["size", "context_length", "modified_at", "parameter_size"].includes(col) ? "desc" : "asc";
    }
    localStorage.setItem(SORT_KEY, JSON.stringify(sort));
    renderTable();
  });
});

// ---------- detail ----------
function openDetail(name) {
  activeName = name;
  const panel = $("detail-panel");
  panel.hidden = false;
  $("detail-name").textContent = name;
  if ($("detail-delete")) {
    $("detail-delete").hidden = false;
    $("detail-delete").dataset.name = name;
  }
  if ($("detail-chat")) {
    $("detail-chat").hidden = false;
    $("detail-chat").dataset.name = name;
  }
  if ($("detail-archive")) {
    $("detail-archive").hidden = false;
    $("detail-archive").dataset.name = name;
    const m = models.find(x => x.name === name);
    const isArchived = !!(m && m.archived);
    $("detail-archive").textContent = isArchived ? "📥" : "📦";
    $("detail-archive").title = isArchived ? t("detail.unarchive_title") : t("detail.archive_title");
  }
  $("detail-body").innerHTML = `<div class="muted">${escapeHtml(t("state.loading"))}</div>`;
  document.querySelectorAll("tbody tr.row").forEach((tr) => {
    tr.classList.toggle("active", tr.dataset.name === name);
  });
  api("/api/models/" + encodeURIComponent(name)).then(renderDetail).catch((e) => {
    $("detail-body").innerHTML = `<div class="muted">${escapeHtml(t("state.error_prefix") + e.message)}</div>`;
  });
}

function renderDetail(d) {
  const m = models.find((x) => x.name === d.name) || {};
  const stateText = m.loaded
    ? t("detail.loaded_vram", { size: fmtBytes(m.size_vram) })
    : t("detail.not_loaded");
  const rows = [
    [t("detail.family"), d.details?.family || "—", false],
    [t("detail.architecture"), d.architecture || "—", false],
    [t("detail.params"), d.details?.parameter_size || (d.parameter_count ? `${(d.parameter_count / 1e9).toFixed(2)}B` : "—"), false],
    [t("detail.quant"), d.details?.quantization_level || "—", false],
    [t("detail.format"), d.details?.format || "—", false],
    [t("detail.context"), fmtCtx(d.context_length), false],
    [t("detail.size"), fmtBytes(m.size), false],
    [t("detail.state"), stateText, true],
    [t("detail.modified"), new Date(d.modified_at).toLocaleString(), false],
    [t("detail.digest"), `<span class="mono">${escapeHtml((m.digest || "").slice(0, 16))}…</span>`, false],
  ];
  const grid = rows.map(([k, v, isState]) =>
    `<div class="k">${escapeHtml(k)}</div><div class="v"${isState ? " id=\"detail-state-value\"" : ""}>${isState ? escapeHtml(v) : v}</div>`).join("");

  const caps = (d.capabilities || []).map((c) => `<span class="pill">${escapeHtml(c)}</span>`).join("");
  const capsBlock = caps ? `<div class="detail-section"><h3>${escapeHtml(t("detail.capabilities"))}</h3><div class="cap-list">${caps}</div></div>` : "";

  const paramsBlock = d.parameters ? `<div class="detail-section"><h3>${escapeHtml(t("detail.parameters_section"))}</h3><pre>${escapeHtml(d.parameters)}</pre></div>` : "";
  const tmplBlock = d.template ? `<div class="detail-section"><h3>${escapeHtml(t("detail.template"))}</h3><pre>${escapeHtml(d.template)}</pre></div>` : "";
  const repairBlock = renderRepairEntry(d);

  const updateBlock = `<div class="detail-section detail-update-section">
    <button type="button" class="ghost detail-update-btn" id="detail-update-btn" data-name="${escapeHtml(d.name)}">⟳ ${escapeHtml(t("detail.update_btn"))}</button>
  </div>`;

  $("detail-body").innerHTML = `<div class="detail-grid">${grid}</div>${updateBlock}${capsBlock}${repairBlock}${paramsBlock}${tmplBlock}`;
  bindRepairEntry(d);
  bindUpdateButton();
}

function bindUpdateButton() {
  const btn = $("detail-update-btn");
  if (!btn) return;
  btn.addEventListener("click", async () => {
    const name = btn.dataset.name;
    if (!name) return;
    btn.disabled = true;
    try {
      await api("/api/pull", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      toast(t("detail.update_enqueued", { name }), "success");
      openDownloads();
    } catch (err) {
      toast(t("toast.error", { msg: err.message }), "error");
    } finally {
      btn.disabled = false;
    }
  });
}

$("detail-close").addEventListener("click", () => {
  $("detail-panel").hidden = true;
  if ($("detail-delete")) {
    $("detail-delete").hidden = true;
    $("detail-delete").dataset.name = "";
  }
  if ($("detail-archive")) {
    $("detail-archive").hidden = true;
    $("detail-archive").dataset.name = "";
  }
  if ($("detail-chat")) {
    $("detail-chat").hidden = true;
    $("detail-chat").dataset.name = "";
  }
  activeName = null;
  document.querySelectorAll("tbody tr.row.active").forEach((tr) => tr.classList.remove("active"));
});

$("detail-chat")?.addEventListener("click", (e) => {
  const name = e.currentTarget?.dataset?.name || activeName;
  if (!name) return;
  $("detail-panel").hidden = true;
  if ($("detail-delete")) {
    $("detail-delete").hidden = true;
    $("detail-delete").dataset.name = "";
  }
  if ($("detail-archive")) {
    $("detail-archive").hidden = true;
    $("detail-archive").dataset.name = "";
  }
  if ($("detail-chat")) {
    $("detail-chat").hidden = true;
    $("detail-chat").dataset.name = "";
  }
  activeName = null;
  document.querySelectorAll("tbody tr.row.active").forEach((tr) => tr.classList.remove("active"));
  showChatViewWithModel(name);
});

$("detail-archive")?.addEventListener("click", (e) => {
  const name = e.currentTarget?.dataset?.name || activeName;
  if (!name) return;
  const m = models.find(x => x.name === name);
  if (m) {
    toggleArchived(name, !m.archived);
  }
});

$("detail-delete")?.addEventListener("click", (e) => {
  const name = e.currentTarget?.dataset?.name || activeName;
  if (!name) return;
  confirmDelete(name);
});

async function toggleArchived(name, toArchive) {
  try {
    const endpoint = toArchive ? "/api/models/archive" : "/api/models/unarchive";
    await api(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    toast(toArchive ? t("toast.archived", { name }) : t("toast.unarchived", { name }), "success");
    await refreshModels();
    if (activeName === name) {
      openDetail(name);
    }
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

const REPAIR_CAPS = ["completion", "tools", "thinking", "vision", "audio", "embedding"];

function isFixedModelName(name) {
  return String(name || "").trim().endsWith(":fixed");
}

function fixedBaseName(name) {
  return isFixedModelName(name) ? String(name).trim().slice(0, -":fixed".length) : String(name || "").trim();
}

function fixedModelName(name) {
  const s = String(name || "").trim();
  if (!s) return "";
  const slash = s.lastIndexOf("/");
  const colon = s.lastIndexOf(":");
  if (colon > slash) return `${s.slice(0, colon)}:fixed`;
  return `${s}:fixed`;
}

function repairDefaultTemplate(d) {
  if (String(d?.template || "").trim()) return "keep";
  const arch = String(d?.architecture || d?.details?.family || "").toLowerCase();
  if (arch.includes("qwen")) return "qwen35";
  if (arch.includes("llama")) return "llama3";
  if (arch.includes("gemma4") || arch.includes("gemma-4")) return "gemma4";
  if (arch.includes("gemma")) return "gemma";
  return "generic";
}

function renderRepairEntry(d) {
  if (isFixedModelName(d.name)) {
    const base = fixedBaseName(d.name);
    return `<div class="detail-section repair-entry">
      <h3>${escapeHtml(t("repair.title"))}</h3>
      <div class="repair-note">${escapeHtml(t("repair.fixed_note", { base }))}</div>
      <button type="button" class="ghost repair-open-base" data-base="${escapeHtml(base)}">${escapeHtml(t("repair.open_base"))}</button>
    </div>`;
  }
  const target = fixedModelName(d.name);
  return `<div class="detail-section repair-entry">
    <h3>${escapeHtml(t("repair.title"))}</h3>
    <div class="repair-note">${escapeHtml(t("repair.entry_note", { name: target }))}</div>
    <button type="button" class="ghost repair-open-modal">${escapeHtml(t("repair.options_btn"))}</button>
  </div>`;
}

function renderRepairModalContent(d) {
  if (isFixedModelName(d.name)) {
    const base = fixedBaseName(d.name);
    return `<div class="repair-card">
      <div class="repair-note">${escapeHtml(t("repair.fixed_note", { base }))}</div>
      <button type="button" class="ghost repair-open-base" data-base="${escapeHtml(base)}">${escapeHtml(t("repair.open_base"))}</button>
    </div>`;
  }

  const detected = new Set((d.capabilities || []).map((c) => String(c).toLowerCase()));
  const detectedHtml = (d.capabilities || []).length
    ? `<div class="cap-list repair-detected">${(d.capabilities || []).map((c) => `<span class="pill">${escapeHtml(c)}</span>`).join("")}</div>`
    : `<div class="muted">${escapeHtml(t("repair.detected_none"))}</div>`;
  const capsHtml = REPAIR_CAPS.map((cap) => {
    const label = t(`chat.cap.${cap}`);
    const isDetected = detected.has(cap);
    const hint = isDetected ? `<span>${escapeHtml(t("repair.detected"))}</span>` : "";
    return `<label class="repair-check">
      <input type="checkbox" name="repair-cap" value="${escapeHtml(cap)}"${isDetected ? " checked disabled" : ""}>
      <span>${escapeHtml(label)}</span>
      ${hint}
    </label>`;
  }).join("");
  const target = fixedModelName(d.name);
  const template = repairDefaultTemplate(d);
  return `<div class="repair-card">
    <label class="repair-check repair-fix-load">
      <input type="checkbox" id="repair-fix-load">
      <span>${escapeHtml(t("repair.fix_load"))}</span>
    </label>
    <div class="repair-warning">${escapeHtml(t("repair.warning"))}</div>
    <div class="repair-subtitle">${escapeHtml(t("repair.detected_caps"))}</div>
    ${detectedHtml}
    <div class="repair-subtitle">${escapeHtml(t("repair.flags"))}</div>
    <div class="repair-caps">${capsHtml}</div>
    <div class="repair-form-grid">
      <label>
        <span>${escapeHtml(t("repair.template"))}</span>
        <select id="repair-template">
          <option value="keep"${template === "keep" ? " selected" : ""}>${escapeHtml(t("repair.template_keep"))}</option>
          <option value="qwen35"${template === "qwen35" ? " selected" : ""}>Qwen 3 / 3.5</option>
          <option value="llama3"${template === "llama3" ? " selected" : ""}>Llama 3</option>
          <option value="gemma"${template === "gemma" ? " selected" : ""}>Gemma</option>
          <option value="gemma4"${template === "gemma4" ? " selected" : ""}>Gemma 4</option>
          <option value="gemma2_unsloth"${template === "gemma2_unsloth" ? " selected" : ""}>Gemma 2 / 4 (Unsloth)</option>
          <option value="hf_generic"${template === "hf_generic" ? " selected" : ""}>HuggingFace / GGUF</option>
          <option value="generic"${template === "generic" ? " selected" : ""}>ChatML</option>
        </select>
      </label>
      <label>
        <span>${escapeHtml(t("repair.context"))}</span>
        <select id="repair-context">
          <option value="safe">${escapeHtml(t("repair.context_safe"))}</option>
          <option value="thinking">${escapeHtml(t("repair.context_thinking"))}</option>
          <option value="keep">${escapeHtml(t("repair.keep"))}</option>
        </select>
      </label>
      <label>
        <span>${escapeHtml(t("repair.temperature"))}</span>
        <select id="repair-temperature">
          <option value="keep">${escapeHtml(t("repair.keep"))}</option>
          <option value="tools">${escapeHtml(t("repair.temp_tools"))}</option>
          <option value="low">${escapeHtml(t("repair.temp_low"))}</option>
        </select>
      </label>
    </div>
    <div class="repair-target">${escapeHtml(t("repair.target", { name: target }))}</div>
    <label class="repair-confirm">
      <input id="repair-confirm" type="checkbox">
      <span>${escapeHtml(t("repair.confirm"))}</span>
    </label>
    <div class="repair-actions">
      <button type="button" class="ghost" id="repair-preview-btn">${escapeHtml(t("repair.preview"))}</button>
      <button type="button" class="primary" id="repair-apply-btn" disabled>${escapeHtml(t("repair.apply"))}</button>
    </div>
    <div id="repair-status" class="muted repair-status"></div>
    <div id="repair-warnings" class="repair-warnings" hidden></div>
    <textarea id="repair-preview" class="repair-preview" spellcheck="false" hidden></textarea>
  </div>`;
}

function bindRepairEntry(d) {
  const openBase = document.querySelector(".repair-open-base");
  if (openBase) {
    openBase.addEventListener("click", () => openDetail(openBase.dataset.base));
  }
  const openModal = document.querySelector(".repair-open-modal");
  if (openModal) {
    openModal.addEventListener("click", () => openRepairModal(d));
  }
}

function openRepairModal(d) {
  $("repair-modal-title").textContent = `${t("repair.title")} · ${d.name}`;
  $("repair-modal-body").innerHTML = renderRepairModalContent(d);
  $("repair-modal").hidden = false;
  bindRepairControls(d);
}

function closeRepairModal() {
  const modal = $("repair-modal");
  if (!modal) return;
  modal.hidden = true;
  $("repair-modal-body").innerHTML = "";
}

function bindRepairControls(d) {
  const root = $("repair-modal-body");
  const openBase = root?.querySelector(".repair-open-base");
  if (openBase) {
    openBase.addEventListener("click", () => {
      closeRepairModal();
      openDetail(openBase.dataset.base);
    });
    return;
  }

  const previewBtn = $("repair-preview-btn");
  const applyBtn = $("repair-apply-btn");
  const confirm = $("repair-confirm");
  if (!previewBtn || !applyBtn || !confirm) return;

  let hasPreview = false;
  const updateApply = () => {
    const modelfile = $("repair-preview")?.value?.trim() || "";
    applyBtn.disabled = !(hasPreview && confirm.checked && modelfile);
  };
  const resetPreview = () => {
    hasPreview = false;
    const pre = $("repair-preview");
    if (pre) {
      pre.hidden = true;
      pre.value = "";
    }
    const warnings = $("repair-warnings");
    if (warnings) {
      warnings.hidden = true;
      warnings.innerHTML = "";
    }
    $("repair-status").textContent = "";
    updateApply();
  };
  confirm.addEventListener("change", updateApply);
  $("repair-preview")?.addEventListener("input", updateApply);
  root.querySelectorAll("input[name='repair-cap'], select").forEach((el) => {
    el.addEventListener("change", resetPreview);
  });
  $("repair-fix-load")?.addEventListener("change", resetPreview);

  previewBtn.addEventListener("click", async () => {
    try {
      previewBtn.disabled = true;
      $("repair-status").textContent = t("repair.previewing");
      const out = await api("/api/model-repair/preview", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(collectRepairRequest(d, false)),
      });
      renderRepairPreview(out);
      hasPreview = true;
      $("repair-status").textContent = t("repair.preview_ready");
    } catch (e) {
      hasPreview = false;
      $("repair-status").textContent = t("state.error_prefix") + e.message;
    } finally {
      previewBtn.disabled = false;
      updateApply();
    }
  });

  applyBtn.addEventListener("click", async () => {
    if (!confirm.checked) return;
    const target = fixedModelName(d.name);
    const exists = models.some((m) => m.name === target || m.model === target);
    const msg = exists ? t("repair.replace_confirm", { name: target }) : t("repair.apply_confirm", { name: target });
    const { ok } = await askConfirm({
      title: t("repair.apply"),
      text: msg,
      okText: exists ? t("repair.replace") : t("repair.create"),
      okClass: "primary",
      mono: target,
    });
    if (!ok) return;
    try {
      applyBtn.disabled = true;
      $("repair-status").textContent = t("repair.applying");
      const out = await api("/api/model-repair/apply", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(collectRepairRequest(d, true)),
      });
      toast(t(out.replaced ? "repair.replaced" : "repair.created", { name: out.target_name }), "success");
      await refreshModels();
      closeRepairModal();
      openDetail(out.target_name);
    } catch (e) {
      toast(t("toast.error", { msg: e.message }), "error");
      $("repair-status").textContent = t("state.error_prefix") + e.message;
      updateApply();
    }
  });
}

function collectRepairRequest(d, confirmed) {
  const capabilities = Array.from(document.querySelectorAll("input[name='repair-cap']"))
    .filter((el) => el.checked)
    .map((el) => el.value);
  const modelfile = $("repair-preview")?.value || "";
  return {
    model: d.name,
    capabilities,
    template_preset: $("repair-template")?.value || "generic",
    context_preset: $("repair-context")?.value || "safe",
    temperature_preset: $("repair-temperature")?.value || "keep",
    fix_load: $("repair-fix-load")?.checked || false,
    modelfile: confirmed ? modelfile : "",
    confirm: !!confirmed,
  };
}

function renderRepairPreview(out) {
  const pre = $("repair-preview");
  pre.hidden = false;
  pre.value = out.modelfile || "";
  const warnings = $("repair-warnings");
  const list = out.warnings || [];
  warnings.hidden = !list.length;
  warnings.innerHTML = list.map((w) => `<div>${escapeHtml(w)}</div>`).join("");
}

$("repair-modal-x")?.addEventListener("click", closeRepairModal);
$("repair-modal-close")?.addEventListener("click", closeRepairModal);

// ---------- chat ----------
function nanoid() {
  return Math.random().toString(36).slice(2, 10);
}

function modelByName(name) {
  return models.find((m) => m.name === name) || null;
}

function modelCaps(name) {
  const m = modelByName(name);
  const out = new Set();
  for (const c of (m?.capabilities || [])) out.add(String(c).toLowerCase());
  return out;
}

function formatCapabilityLabel(raw) {
  const k = String(raw || "").toLowerCase().trim();
  if (!k) return "";
  const i18nKey = `chat.cap.${k.replace(/[^a-z0-9]+/g, "_")}`;
  const tr = t(i18nKey);
  if (tr !== i18nKey) return tr;
  return k.charAt(0).toUpperCase() + k.slice(1);
}

function syncChatModelOptions() {
  const sel = $("chat-model");
  if (!sel) return;
  const previous = sel.value;
  // Filter out archived models so they do not clutter chat view select dropdown
  const activeModels = models.filter(m => !m.archived);
  const sorted = applySort(activeModels);
  sel.innerHTML = sorted.map((m) => `<option value="${escapeHtml(m.name)}">${escapeHtml(m.name)}</option>`).join("");
  if (!sorted.length) return;
  if (previous && sorted.some((m) => m.name === previous)) {
    sel.value = previous;
  } else if (activeName && sorted.some((m) => m.name === activeName)) {
    sel.value = activeName;
  } else {
    const loaded = sorted.find((m) => m.loaded);
    sel.value = (loaded || sorted[0]).name;
  }
  updateChatModelLoadDot();
}

function updateChatCapabilityUI() {
  const model = $("chat-model").value;
  if ($("chat-model-name-value")) {
    $("chat-model-name-value").textContent = model;
  }
  const caps = modelCaps(model);
  const isImageModel = caps.has("image");
  const canVision = caps.has("vision") || isImageModel;
  const canAudio = caps.has("audio");
  const canThinkToggle = caps.has("thinking");
  const canTools = caps.has("tools");
  $("chat-image-btn").hidden = !canVision;
  $("chat-audio-btn").hidden = !canAudio;
  $("chat-record-btn").hidden = !canAudio;
  $("chat-think-wrap").hidden = !canThinkToggle;
  $("chat-web-tools-wrap").hidden = !canTools;
  
  const imgOpts = $("chat-image-options-wrap");
  if (imgOpts) imgOpts.hidden = !isImageModel;
  const sysField = $("chat-system-field");
  if (sysField) sysField.hidden = isImageModel;
  const tempField = $("chat-temperature-field");
  if (tempField) tempField.hidden = isImageModel;
  const topKField = $("chat-top-k-field");
  if (topKField) topKField.hidden = isImageModel;
  const topPField = $("chat-top-p-field");
  if (topPField) topPField.hidden = isImageModel;

  const inputEl = $("chat-input");
  if (inputEl && !chatIsRecording) {
    inputEl.placeholder = isImageModel
      ? (t("chat.image_input_placeholder") || "Describe the image you want to generate…")
      : (t("chat.input_placeholder") || "Write your message…");
  }

  if (!canAudio && chatIsRecording) {
    stopAudioRecording(true);
  }
  const webW = $("chat-web-tools");
  if (webW) webW.checked = !isImageModel && !!canTools;
  restoreChatOptionsFromSession();

  const m = modelByName(model);
  const list = m?.capabilities && m.capabilities.length
    ? [...m.capabilities].sort((a, b) => String(a).localeCompare(String(b)))
    : [];
  const capBlock = $("chat-cap-block");
  const capHost = $("chat-cap-flags");
  if (capBlock && capHost) {
    if (!list.length) {
      capBlock.hidden = true;
      capHost.innerHTML = "";
    } else {
      capBlock.hidden = false;
      capHost.innerHTML = list.map((c) => {
        const raw = String(c);
        const slug = raw.toLowerCase().replace(/[^a-z0-9]+/g, "-");
        const label = formatCapabilityLabel(raw);
        return `<span class="pill chat-cap-pill" data-cap="${escapeHtml(slug)}">${escapeHtml(label)}</span>`;
      }).join("");
    }
  }
  updateChatModelLoadDot();
}

function updateChatContextMeter() {
  const meter = $("chat-context-meter");
  const selectedName = $("chat-model")?.value || "";
  const selected = modelByName(selectedName);
  const maxCtx = Number(selected?.context_length) || 0;
  const ring = $("chat-context-ring");
  if (!maxCtx) {
    if (meter) meter.textContent = "—";
    if (ring) {
      ring.style.setProperty("--ctx-pct", "0%");
      ring.title = "";
    }
    return;
  }
  const used = Math.max(0, Number(chatLastUsedTokens) || 0);
  const pct = Math.min(999, Math.round((used / maxCtx) * 100));
  const ringPct = `${Math.max(0, Math.min(100, (used / maxCtx) * 100))}%`;
  if (meter) meter.textContent = `${fmtCtx(used)} / ${fmtCtx(maxCtx)} (${pct}%)`;
  if (ring) {
    ring.style.setProperty("--ctx-pct", ringPct);
    ring.title = `${fmtCtx(used)} / ${fmtCtx(maxCtx)} (${pct}%)`;
  }
}

function showModelsView() {
  const chatView = $("chat-view");
  const modelsView = $("models-view");
  chatView?.classList.remove("chat-options-open");
  stopSpeechPlayback();
  currentView = "models";
  if (modelsView) modelsView.hidden = false;
  if (chatView) chatView.hidden = true;
  $("tests-view") && ($("tests-view").hidden = true);
  $("test-editor-view") && ($("test-editor-view").hidden = true);
  $("chat-btn")?.classList.remove("active");
  if (window.location.pathname !== "/") {
    history.pushState(null, "", "/");
  }
}

function resetChatState() {
  stopAudioRecording(true);
  stopSpeechPlayback();
  if (chatAbortController) {
    try { chatAbortController.abort(); } catch (_) {}
    chatAbortController = null;
  }
  chatStreamLock = false;
  chatMessages = [];
  chatAttachments = [];
  chatPendingQueue = [];
  chatLastUsedTokens = 0;
  chatDndDepth = 0;
  stopThinkTicker();
  updateStreamBar();
  closeImagePreview();
  $("chat-dropzone").hidden = true;
  $("chat-attachments").hidden = true;
  $("chat-attachments").innerHTML = "";
  renderChatQueue();
  $("chat-messages").innerHTML = `<div class="chat-empty muted">${escapeHtml(t("chat.empty"))}</div>`;
  $("chat-input").value = "";
}

function showChatView() {
  const chatView = $("chat-view");
  const modelsView = $("models-view");
  if (!chatView || !modelsView) {
    toast(t("toast.error", { msg: "chat UI is not available; refresh the page" }), "error");
    return;
  }
  currentView = "chat";
  chatView.classList.remove("chat-options-open");
  modelsView.hidden = true;
  chatView.hidden = false;
  $("chat-btn")?.classList.add("active");
  if ($("detail-panel") && !$("detail-panel").hidden) {
    $("detail-panel").hidden = true;
    activeName = null;
    document.querySelectorAll("tbody tr.row.active").forEach((tr) => tr.classList.remove("active"));
  }
  syncChatModelOptions();
  updateChatCapabilityUI();
  updateChatContextMeter();
  updateChatSendEnabled();
  void applyChatDefaultsForModel($("chat-model").value);
  setTimeout(() => $("chat-input").focus(), 20);
}

// ---------- tests views ----------

function hideAllMainViews() {
  $("models-view").hidden = true;
  $("chat-view").hidden = true;
  $("tests-view").hidden = true;
  $("test-editor-view").hidden = true;
  $("agent-session-view").hidden = true;
  $("battery-progress-view").hidden = true;
  $("battery-results-view").hidden = true;
  $("battery-history-view").hidden = true;
  $("detail-panel").hidden = true;
}

function showTestsView() {
  hideAllMainViews();
  stopSpeechPlayback();
  currentView = "tests";
  $("tests-view").hidden = false;
  if (!window.location.pathname.startsWith("/tests")) {
    history.pushState(null, "", "/tests");
  }
  void refreshTests();
}

async function showTestEditorView(id) {
  hideAllMainViews();
  currentView = "test-editor";
  $("test-editor-view").hidden = false;
  currentTestId = id;
  if (id) {
    let test = tests.find((x) => x.id === id);
    if (!test && tests.length === 0) {
      await refreshTests();
      test = tests.find((x) => x.id === id);
    }
    populateTestEditorGroupSelect();
    if (test) {
      $("test-editor-title").textContent = t("tests.edit_test");
      $("te-name").value = test.name || "";
      $("te-description").value = test.description || "";
      $("te-group").value = test.group_id || "";
      $("te-active").checked = !!test.active;
      $("te-prompt").value = test.prompt || "";
      $("te-system").value = test.system_prompt || "";
      $("te-eval-type").value = test.evaluation_type || "exact_match";
      updateAgentSettingsVisibility();
      updateEvalConfigVisibility();
      const cfg = test.evaluation_config || {};
      $("te-eval-expected").value = cfg.expected || "";
      $("te-eval-pattern").value = cfg.pattern || "";
      $("te-eval-config").value = test.evaluation_config ? JSON.stringify(test.evaluation_config, null, 2) : "";
      if (test.evaluation_type === "agent" && test.evaluation_config) {
        const cfg = test.evaluation_config;
        $("te-agent-max-turns").value = String(cfg.max_turns || 10);
        $("te-agent-initial-files").value = cfg.initial_files ? JSON.stringify(cfg.initial_files, null, 2) : "";
        const toolChecks = $("te-agent-settings")?.querySelectorAll('input[type="checkbox"]');
        if (toolChecks) {
          const allowed = new Set(cfg.tools || []);
          toolChecks.forEach((cb) => { cb.checked = allowed.has(cb.value); });
        }
      }
      $("te-required-caps").value = (test.required_caps || []).join(", ");
      $("te-order").value = String(test.order || 0);
      testEditorAttachments = (test.attachments || []).map((a) => ({ ...a }));
      renderTestEditorAttachments();
      $("test-editor-delete").hidden = false;
      if (window.location.pathname !== "/tests/edit/" + id) {
        history.pushState(null, "", "/tests/edit/" + id);
      }
      return;
    }
  }
  populateTestEditorGroupSelect();
  $("test-editor-title").textContent = t("tests.new_test");
  $("te-name").value = "";
  $("te-description").value = "";
  $("te-group").value = selectedGroupId || "";
  $("te-active").checked = true;
  $("te-prompt").value = "";
  $("te-system").value = "";
  $("te-eval-type").value = "exact_match";
  $("te-eval-expected").value = "";
  $("te-eval-pattern").value = "";
  $("te-eval-config").value = "";
  $("te-required-caps").value = "";
  $("te-order").value = "0";
  testEditorAttachments = [];
  renderTestEditorAttachments();
  $("test-editor-delete").hidden = true;
  if (window.location.pathname !== "/tests/new") {
    history.pushState(null, "", "/tests/new");
  }
}

async function refreshTests() {
  try {
    const data = await api("/api/tests");
    testsGroups = data.groups || [];
    tests = data.tests || [];
    renderTestsSidebar();
    renderTestsList();
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
}

function renderTestsSidebar() {
  const container = $("tests-groups-list");
  if (!container) return;
  const allBtnClass = selectedGroupId === "" ? "tests-group-item active" : "tests-group-item";
  let html = `<div class="${allBtnClass}" data-group-id="">
    <span class="tests-group-name">${escapeHtml(t("tests.all_tests"))}</span>
    <span class="tests-group-count">${tests.length}</span>
  </div>`;
  for (const g of testsGroups) {
    const cls = selectedGroupId === g.id ? "tests-group-item active" : "tests-group-item";
    const count = tests.filter((t) => t.group_id === g.id).length;
    html += `<div class="${cls}" data-group-id="${escapeHtml(g.id)}">
      <span class="tests-group-name">${escapeHtml(g.name)}</span>
      <span class="tests-group-actions">
        <button type="button" class="btn-icon te-group-rename" data-group-id="${escapeHtml(g.id)}" data-group-name="${escapeHtml(g.name)}" title="Rename">✎</button>
        <button type="button" class="btn-icon te-group-delete" data-group-id="${escapeHtml(g.id)}" title="Delete">×</button>
      </span>
      <span class="tests-group-count">${count}</span>
    </div>`;
  }
  container.innerHTML = html;
  container.querySelectorAll(".tests-group-item").forEach((el) => {
    el.addEventListener("click", (e) => {
      if (e.target.closest(".te-group-rename") || e.target.closest(".te-group-delete")) return;
      selectedGroupId = el.dataset.groupId;
      const newPath = selectedGroupId ? "/tests/group/" + encodeURIComponent(selectedGroupId) : "/tests";
      if (window.location.pathname !== newPath) {
        history.pushState(null, "", newPath);
      }
      renderTestsSidebar();
      renderTestsList();
    });
  });
  container.querySelectorAll(".te-group-rename").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      void renameGroup(btn.dataset.groupId, btn.dataset.groupName);
    });
  });
  container.querySelectorAll(".te-group-delete").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      void deleteGroup(btn.dataset.groupId);
    });
  });
}

function renderTestsList() {
  const list = $("tests-list");
  const empty = $("tests-empty");
  const title = $("tests-group-title");
  if (!list || !empty || !title) return;

  let filtered = tests;
  if (selectedGroupId !== "") {
    filtered = tests.filter((t) => t.group_id === selectedGroupId);
    const g = testsGroups.find((x) => x.id === selectedGroupId);
    title.textContent = g ? g.name : t("tests.all_tests");
  } else {
    title.textContent = t("tests.all_tests");
  }

  const runBtn = $("tests-run-battery-btn");
  if (runBtn) {
    const hasActiveNonAgent = filtered.some((t) => t.active && t.evaluation_type !== "agent");
    runBtn.hidden = selectedGroupId === "" || !hasActiveNonAgent;
  }
  const groupHistBtn = $("tests-group-history-btn");
  if (groupHistBtn) {
    groupHistBtn.hidden = selectedGroupId === "";
  }

  if (!filtered.length) {
    list.innerHTML = "";
    empty.hidden = false;
    return;
  }
  empty.hidden = true;

  list.innerHTML = filtered.map((test) => {
    const activeClass = test.active ? "tests-item-active" : "tests-item-suspended";
    const activeLabel = test.active ? t("tests.status_active") : t("tests.status_suspended");
    const evalLabel = t("tests.eval_" + test.evaluation_type) || test.evaluation_type;
    const caps = (test.required_caps || []).map((c) => `<span class="pill">${escapeHtml(c)}</span>`).join("");
    return `
      <div class="tests-item" data-id="${escapeHtml(test.id)}">
        <div class="tests-item-main">
          <div class="tests-item-name">${escapeHtml(test.name)}</div>
          <div class="tests-item-meta">
            <span class="pill ${activeClass}">${escapeHtml(activeLabel)}</span>
            <span class="pill">${escapeHtml(evalLabel)}</span>
            ${caps}
          </div>
          ${test.description ? `<div class="tests-item-desc muted">${escapeHtml(test.description)}</div>` : ""}
        </div>
        <div class="tests-item-actions">
          ${test.evaluation_type === "agent" ? `<button class="ghost tests-item-run" data-id="${escapeHtml(test.id)}">${t("tests.agent_run")}</button>` : ""}
          <button class="ghost tests-item-history" data-id="${escapeHtml(test.id)}">${t("tests.history_short")}</button>
          <button class="ghost tests-item-edit" data-i18n="action.edit">Edit</button>
          <button class="ghost tests-item-toggle" data-id="${escapeHtml(test.id)}">${test.active ? t("tests.suspend") : t("tests.activate")}</button>
          <button class="ghost danger-text tests-item-delete" data-id="${escapeHtml(test.id)}">×</button>
        </div>
      </div>
    `;
  }).join("");

  list.querySelectorAll(".tests-item").forEach((el) => {
    el.addEventListener("click", (e) => {
      if (e.target.closest("button")) return;
      void showTestEditorView(el.dataset.id);
    });
  });
  list.querySelectorAll(".tests-item-edit").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const id = btn.closest(".tests-item")?.dataset?.id;
      if (id) void showTestEditorView(id);
    });
  });
  list.querySelectorAll(".tests-item-history").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const id = btn.dataset.id;
      if (id) openTestHistoryModal(id);
    });
  });
  list.querySelectorAll(".tests-item-toggle").forEach((btn) => {
    btn.addEventListener("click", async (e) => {
      e.stopPropagation();
      const id = btn.dataset.id;
      const test = tests.find((t) => t.id === id);
      if (!test) return;
      try {
        await api("/api/tests/" + encodeURIComponent(id), {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ ...test, active: !test.active }),
        });
        await refreshTests();
      } catch (err) {
        toast(t("toast.error", { msg: err.message }), "error");
      }
    });
  });
  list.querySelectorAll(".tests-item-delete").forEach((btn) => {
    btn.addEventListener("click", async (e) => {
      e.stopPropagation();
      const id = btn.dataset.id;
      const ok = await askConfirm({
        title: t("tests.delete_title"),
        text: t("tests.delete_text"),
        okText: t("action.delete"),
        okClass: "danger",
      });
      if (!ok.ok) return;
      try {
        await api("/api/tests/" + encodeURIComponent(id), { method: "DELETE" });
        await refreshTests();
      } catch (err) {
        toast(t("toast.error", { msg: err.message }), "error");
      }
    });
  });
  list.querySelectorAll(".tests-item-run").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const id = btn.dataset.id;
      if (id) showAgentSessionView(id);
    });
  });
}

function getAutoCapsFromAttachments() {
  const caps = new Set();
  for (const a of testEditorAttachments) {
    if (a.kind === "image") caps.add("vision");
    if (a.kind === "audio") caps.add("audio");
  }
  return Array.from(caps);
}

function updateTestEditorAutoCaps() {
  const el = $("te-auto-caps");
  if (!el) return;
  const auto = getAutoCapsFromAttachments();
  const userRaw = $("te-required-caps")?.value || "";
  const user = userRaw.split(",").map((s) => s.trim()).filter(Boolean);
  const all = new Set([...user, ...auto]);
  if (!all.size) {
    el.innerHTML = "";
    return;
  }
  const pills = Array.from(all).map((c) => {
    const isAuto = auto.includes(c) && !user.includes(c);
    return `<span class="pill${isAuto ? " te-auto-pill" : ""}" title="${isAuto ? "Auto-detected from attachments" : "Manually set"}">${escapeHtml(c)}</span>`;
  }).join("");
  el.innerHTML = `<div class="te-auto-caps-label">Effective capabilities:</div><div class="te-auto-caps-pills">${pills}</div>`;
}

function renderTestEditorAttachments() {
  const list = $("te-attach-list");
  if (!list) return;
  if (!testEditorAttachments.length) {
    list.innerHTML = "";
    updateTestEditorAutoCaps();
    return;
  }
  list.innerHTML = testEditorAttachments.map((a) => {
    if (a.kind === "image") {
      const src = `data:${a.mime};base64,${a.data}`;
      return `<div class="te-attach-item" data-id="${escapeHtml(a.id)}">
        <img src="${src}" alt="" class="te-attach-thumb">
        <span class="te-attach-name mono">${escapeHtml(a.name)}</span>
        <button type="button" class="btn-icon te-attach-remove" data-id="${escapeHtml(a.id)}" title="Remove">×</button>
      </div>`;
    }
    if (a.kind === "audio") {
      const src = `data:${a.mime};base64,${a.data}`;
      return `<div class="te-attach-item te-attach-item-audio" data-id="${escapeHtml(a.id)}">
        <span class="te-attach-name mono">${escapeHtml(a.name)}</span>
        <audio controls preload="metadata" src="${src}" class="te-attach-audio"></audio>
        <button type="button" class="btn-icon te-attach-remove" data-id="${escapeHtml(a.id)}" title="Remove">×</button>
      </div>`;
    }
    return "";
  }).join("");
  list.querySelectorAll(".te-attach-remove").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      e.preventDefault();
      testEditorAttachments = testEditorAttachments.filter((x) => x.id !== btn.dataset.id);
      renderTestEditorAttachments();
    });
  });
  updateTestEditorAutoCaps();
}

async function handleTestEditorFileInput(files, kind) {
  for (const file of files) {
    const data = await toBase64(file);
    testEditorAttachments.push({
      id: nanoid(),
      kind,
      name: file.name,
      mime: file.type || (kind === "image" ? "image/jpeg" : "audio/webm"),
      data,
    });
  }
  renderTestEditorAttachments();
}

function populateTestEditorGroupSelect() {
  const sel = $("te-group");
  if (!sel) return;
  sel.innerHTML = `<option value="">${escapeHtml(t("tests.no_group"))}</option>` +
    testsGroups.map((g) => `<option value="${escapeHtml(g.id)}">${escapeHtml(g.name)}</option>`).join("");
}

async function saveTestEditor() {
  const evalType = $("te-eval-type").value;
  let evalConfig = null;
  if (evalType === "exact_match" || evalType === "contains") {
    const expected = $("te-eval-expected").value.trim();
    if (expected) evalConfig = { expected };
  } else if (evalType === "regex") {
    const pattern = $("te-eval-pattern").value.trim();
    if (pattern) evalConfig = { pattern };
  } else if (evalType === "json_schema") {
    const raw = $("te-eval-config").value.trim();
    if (raw) {
      try { evalConfig = JSON.parse(raw); } catch {
        toast(t("tests.invalid_json"), "error");
        return;
      }
    }
  }
  let agentConfig = evalConfig;
  if ($("te-eval-type").value === "agent") {
    const maxTurns = Number($("te-agent-max-turns").value) || 10;
    const initialFilesRaw = $("te-agent-initial-files").value.trim();
    let initialFiles = [];
    if (initialFilesRaw) {
      try { initialFiles = JSON.parse(initialFilesRaw); } catch { /* ignore */ }
    }
    const toolChecks = $("te-agent-settings")?.querySelectorAll('input[type="checkbox"]');
    const tools = [];
    if (toolChecks) {
      toolChecks.forEach((cb) => { if (cb.checked) tools.push(cb.value); });
    }
    agentConfig = {
      max_turns: maxTurns,
      initial_files: Array.isArray(initialFiles) ? initialFiles : [],
      tools: tools,
      human_review: true,
    };
  }

  const autoCaps = getAutoCapsFromAttachments();
  const userCaps = $("te-required-caps").value.split(",").map((s) => s.trim()).filter(Boolean);
  const payload = {
    name: $("te-name").value.trim(),
    description: $("te-description").value.trim(),
    group_id: $("te-group").value,
    active: $("te-active").checked,
    prompt: $("te-prompt").value,
    system_prompt: $("te-system").value,
    evaluation_type: evalType,
    evaluation_config: agentConfig,
    required_caps: Array.from(new Set([...userCaps, ...autoCaps])),
    attachments: testEditorAttachments.map((a) => ({ id: a.id, kind: a.kind, name: a.name, mime: a.mime, data: a.data })),
    order: Number($("te-order").value) || 0,
  };
  try {
    if (currentTestId) {
      await api("/api/tests/" + encodeURIComponent(currentTestId), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
    } else {
      await api("/api/tests", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
    }
    await refreshTests();
    const groupId = payload.group_id;
    selectedGroupId = groupId || "";
    const newPath = selectedGroupId ? "/tests/group/" + encodeURIComponent(selectedGroupId) : "/tests";
    if (window.location.pathname !== newPath) {
      history.pushState(null, "", newPath);
    }
    showTestsView();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function deleteTestEditor() {
  if (!currentTestId) return;
  const confirmed = await askConfirm({
    title: t("tests.delete_title"),
    text: t("tests.delete_text"),
    okText: t("action.delete"),
    okClass: "danger",
  });
  if (!confirmed.ok) return;
  try {
    await api("/api/tests/" + encodeURIComponent(currentTestId), { method: "DELETE" });
    await refreshTests();
    showTestsView();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function createNewGroup() {
  const name = prompt(t("tests.group_name_prompt"));
  if (!name || !name.trim()) return;
  try {
    await api("/api/test-groups", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: name.trim(), order: testsGroups.length }),
    });
    await refreshTests();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function renameGroup(id, currentName) {
  const name = prompt("Rename group:", currentName);
  if (!name || !name.trim() || name.trim() === currentName) return;
  try {
    await api("/api/test-groups/" + encodeURIComponent(id), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: name.trim() }),
    });
    await refreshTests();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function deleteGroup(id) {
  const group = testsGroups.find((g) => g.id === id);
  const name = group ? group.name : id;
  const { ok } = await askConfirm({
    title: "Delete group",
    text: `Delete "${name}"? Tests in this group will become ungrouped.`,
    okText: t("action.delete"),
    okClass: "danger",
  });
  if (!ok) return;
  try {
    await api("/api/test-groups/" + encodeURIComponent(id), { method: "DELETE" });
    if (selectedGroupId === id) {
      selectedGroupId = "";
      if (window.location.pathname !== "/tests") {
        history.pushState(null, "", "/tests");
      }
    }
    await refreshTests();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

function showChatViewWithModel(name) {
  showChatView();
  if (!$("chat-view") || $("chat-view").hidden) return;
  $("chat-view")?.classList.remove("chat-options-open");
  if (!name) return;
  const sel = $("chat-model");
  const exists = Array.from(sel?.options || []).some((o) => o.value === name);
  if (!exists) return;
  sel.value = name;
  updateChatCapabilityUI();
  updateChatContextMeter();
  void applyChatDefaultsForModel(name, true);
  
  const model = modelByName(name);
  if (model && model.digest) {
    const urlDigest = model.digest.replace(":", "-");
    const newPath = "/chat/" + urlDigest;
    if (window.location.pathname !== newPath) {
      history.pushState(null, "", newPath);
    }
  }
}

async function handleRouting() {
  const path = window.location.pathname;
  if (path.startsWith("/chat/")) {
    const urlDigest = path.substring(6);
    const model = models.find(m => m.digest && m.digest.replace(":", "-") === urlDigest);
    if (model) {
      showChatViewWithModel(model.name);
    } else {
      showModelsView();
    }
  } else if (path === "/tests" || path === "/tests/") {
    showTestsView();
  } else if (path.startsWith("/tests/group/")) {
    selectedGroupId = path.substring(13);
    showTestsView();
  } else if (path === "/tests/new") {
    await showTestEditorView(null);
  } else if (path.startsWith("/tests/edit/")) {
    const id = path.substring(12);
    await showTestEditorView(id);
  } else if (path.startsWith("/tests/agent/")) {
    const id = path.substring(13);
    showAgentSessionView(id);
  } else if (path.startsWith("/tests/battery/results/")) {
    const id = path.substring(23);
    void showBatteryResultsView(id);
  } else if (path === "/tests/battery/history") {
    showBatteryHistoryView();
  } else if (path === "/") {
    showModelsView();
  }
}

function isGFMTableRow(s) {
  s = s.trim();
  return s.length > 2 && s.startsWith("|") && s.endsWith("|");
}

function isGFMTableSeparator(s) {
  s = s.trim();
  if (!s.includes("|") || !/[-:]{2,}/.test(s)) return false;
  const parts = s.split("|").map((p) => p.trim()).filter((p) => p.length > 0);
  if (parts.length < 1) return false;
  return parts.every((p) => /^:?-{2,}:?$/.test(p));
}

function gfmTableCell(s) {
  if (!s) return "";
  let t = escapeHtml(s);
  t = t.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  t = t.replace(/`([^`]+)`/g, "<code>$1</code>");
  return t;
}

function gfmTableBlockToHTML(rows) {
  if (rows.length < 2) return "";
  const parseRow = (line) => {
    const t = line.trim();
    const inner = t.startsWith("|") ? t.slice(1) : t;
    const core = inner.endsWith("|") ? inner.slice(0, -1) : inner;
    return core.split("|").map((c) => c.trim());
  };
  const header = parseRow(rows[0]);
  const body = rows.slice(2).map(parseRow);
  const n = Math.max(
    header.length,
    body.reduce((m, r) => Math.max(m, r.length), 0),
  );
  const pad = (r) => {
    const x = r.slice();
    while (x.length < n) x.push("");
    return x;
  };
  const th = pad(header);
  const br = body.map(pad);
  let h = "<div class=\"chat-md-table-wrap\"><table class=\"chat-md-table\"><thead><tr>";
  th.forEach((c) => {
    h += `<th>${gfmTableCell(c)}</th>`;
  });
  h += "</tr></thead><tbody>";
  br.forEach((row) => {
    h += "<tr>";
    row.forEach((c) => {
      h += `<td>${gfmTableCell(c)}</td>`;
    });
    h += "</tr>";
  });
  h += "</tbody></table></div>";
  return h;
}

/** After fenced code: replace GFM tables with placeholders. */
function extractGFMTables(text, outTables) {
  const lines = text.split("\n");
  const out = [];
  let i = 0;
  while (i < lines.length) {
    if (
      i + 1 < lines.length
      && isGFMTableRow(lines[i])
      && isGFMTableSeparator(lines[i + 1])
    ) {
      const block = [lines[i], lines[i + 1]];
      i += 2;
      while (i < lines.length && isGFMTableRow(lines[i]) && !isGFMTableSeparator(lines[i])) {
        block.push(lines[i]);
        i += 1;
      }
      const idx = outTables.length;
      outTables.push(gfmTableBlockToHTML(block));
      out.push(`@@GFMTABLE_${idx}@@`);
      continue;
    }
    out.push(lines[i]);
    i += 1;
  }
  return out.join("\n");
}

function scheduleRenderChatMessages() {
  if (chatRenderRaf != null) return;
  chatRenderRaf = requestAnimationFrame(() => {
    chatRenderRaf = null;
    renderChatMessages();
    scrollChatToBottom();
    scrollActiveBlocks();
  });
}

function flushChatRender() {
  if (chatRenderRaf != null) {
    cancelAnimationFrame(chatRenderRaf);
    chatRenderRaf = null;
  }
  renderChatMessages();
  scrollChatToBottom();
  scrollActiveBlocks();
}

function renderMarkdownSafe(input) {
  const text = String(input || "").replace(/\r\n/g, "\n");
  const codeBlocks = [];
  let work = text.replace(/```([\w-]+)?\n([\s\S]*?)```/g, (_m, _lang, code) => {
    const key = `@@CODEBLOCK_${codeBlocks.length}@@`;
    codeBlocks.push(`<pre class="chat-code"><code>${escapeHtml(code)}</code></pre>`);
    return key;
  });
  const tableBlocks = [];
  work = extractGFMTables(work, tableBlocks);

  const mathBlocks = [];
  work = work.replace(/\$\$([\s\S]*?)\$\$/g, (_m, math) => {
    const key = `@@MATHDISP_${mathBlocks.length}@@`;
    mathBlocks.push({ type: "display", math: math.trim() });
    return key;
  });
  work = work.replace(/\$([^$\n]+?)\$/g, (_m, math) => {
    const key = `@@MATHINLINE_${mathBlocks.length}@@`;
    mathBlocks.push({ type: "inline", math: math.trim() });
    return key;
  });

  let html = escapeHtml(work);
  html = html.replace(/`([^`\n]+)`/g, "<code>$1</code>");
  html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  html = html.replace(/\*([^*\n]+)\*/g, "<em>$1</em>");
  html = html.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, `<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>`);
  html = html.replace(/^###\s+(.+)$/gm, "<h4>$1</h4>");
  html = html.replace(/^##\s+(.+)$/gm, "<h3>$1</h3>");
  html = html.replace(/^#\s+(.+)$/gm, "<h2>$1</h2>");

  const lines = html.split("\n");
  const out = [];
  let inList = false;
  for (const line of lines) {
    const trimmed = line.trim();
    if (/^[-*]\s+/.test(trimmed)) {
      if (!inList) {
        out.push("<ul>");
        inList = true;
      }
      out.push(`<li>${trimmed.replace(/^[-*]\s+/, "")}</li>`);
      continue;
    }
    if (inList) {
      out.push("</ul>");
      inList = false;
    }
    if (trimmed === "") {
      out.push("");
    } else if (/^@@GFMTABLE_(\d+)@@$/.test(trimmed)) {
      const m = trimmed.match(/^@@GFMTABLE_(\d+)@@$/);
      const tIdx = m ? Number(m[1]) : -1;
      if (tIdx >= 0 && tIdx < tableBlocks.length) {
        out.push(tableBlocks[tIdx]);
      } else {
        out.push("<p></p>");
      }
    } else if (/^@@CODEBLOCK_\d+@@$/.test(trimmed)) {
      out.push(trimmed);
    } else if (/^<h[234]>/.test(trimmed)) {
      out.push(trimmed);
    } else {
      out.push(`<p>${trimmed}</p>`);
    }
  }
  if (inList) out.push("</ul>");
  html = out.join("\n").replace(/\n{3,}/g, "\n\n");

  codeBlocks.forEach((block, i) => {
    html = html.replace(`@@CODEBLOCK_${i}@@`, block);
  });
  mathBlocks.forEach((block, i) => {
    if (block.type === "display") {
      html = html.replace(`@@MATHDISP_${i}@@`, `<span class="math-display">${escapeHtml(block.math)}</span>`);
    } else {
      html = html.replace(`@@MATHINLINE_${i}@@`, `<span class="math-inline">${escapeHtml(block.math)}</span>`);
    }
  });
  return html;
}

function renderChatMath(container) {
  if (typeof katex === "undefined") return;
  container.querySelectorAll(".math-inline").forEach((el) => {
    try {
      katex.render(el.textContent, el, { throwOnError: false, displayMode: false });
    } catch (e) { /* ignore */ }
  });
  container.querySelectorAll(".math-display").forEach((el) => {
    try {
      katex.render(el.textContent, el, { throwOnError: false, displayMode: true });
    } catch (e) { /* ignore */ }
  });
}

function splitThink(raw) {
  const text = String(raw || "");
  const open = text.indexOf("<think>");
  if (open === -1) return { think: "", answer: text, inThink: false };
  const close = text.indexOf("</think>", open + 7);
  if (close === -1) {
    return {
      think: text.slice(open + 7),
      answer: (text.slice(0, open)).replace(/<\/?think>/g, ""),
      inThink: true,
    };
  }
  const before = text.slice(0, open);
  const think = text.slice(open + 7, close);
  const after = text.slice(close + 8);
  return {
    think,
    answer: (before + after).replace(/<\/?think>/g, ""),
    inThink: false,
  };
}

function thinkLabel(ms, streaming) {
  const dur = formatMetaElapsed(ms || 0);
  if (streaming) return t("chat.think_running", { t: dur });
  return t("chat.think_done", { t: dur });
}

function assistantMetricParts(m, opts = {}) {
  if (!m || m.role !== "assistant") return [];
  const parts = [];
  const elapsed = Math.max(0, Number(m.elapsedMs) || 0);
  const tokens = Math.max(0, Math.round(Number(m.completionTokens || m.tokens || 0)));
  const tps = Number(m.tps);
  if (elapsed > 0 || opts.showZero) parts.push(formatMetaElapsed(elapsed));
  if (tokens > 0 || opts.showZero) parts.push(t("chat.meta_tokens", { n: tokens }));
  if ((Number.isFinite(tps) && tps > 0) || opts.showZero) {
    parts.push(t("chat.meta_tps", { rate: (Number.isFinite(tps) && tps > 0 ? tps : 0).toFixed(2) }));
  }
  if (opts.streaming) parts.push(t("chat.streaming"));
  return parts;
}

function assistantMetricText(m, opts = {}) {
  return assistantMetricParts(m, opts).join(" · ");
}

/**
 * Añade al timeline el texto desde segmentFlushIndex hasta ahora, como think (y opcional bloque md).
 * @param {object} assistantMsg
 * @param {string} assistantRaw
 * @param {boolean} isFinal - si true, no mete "answer" en el timeline (va en m.content)
 */
function flushSegmentToTimeline(assistantMsg, assistantRaw, isFinal) {
  const start = Number(assistantMsg.segmentFlushIndex) || 0;
  if (assistantRaw.length <= start) return;
  const seg = assistantRaw.slice(start);
  assistantMsg.segmentFlushIndex = assistantRaw.length;
  if (!assistantMsg.timeline) assistantMsg.timeline = [];
  const parts = splitThink(seg);
  if (parts.think && String(parts.think).trim()) {
    assistantMsg.timeline.push({ type: "think", think: parts.think, segId: nanoid() });
  }
  if (!isFinal && parts.answer && String(parts.answer).trim()) {
    assistantMsg.timeline.push({ type: "md", content: parts.answer });
  }
}

/** Long tool error bodies (e.g. raw HTML) go inside a &lt;details&gt; with a one-line peek. */
const TOOL_ERR_COLLAPSE_LEN = 360;

function toolErrOneLinePeek(s, max) {
  const t = String(s).replace(/\s+/g, " ").trim();
  if (t.length <= max) return t;
  return t.slice(0, max - 1) + "…";
}

function renderToolErrorBlock(err) {
  const text = String(err);
  if (text.length <= TOOL_ERR_COLLAPSE_LEN) {
    return `<div class="chat-tool-err mono">${escapeHtml(text)}</div>`;
  }
  const peek = toolErrOneLinePeek(text, 96);
  return `<details class="chat-tool-preview chat-tool-err-details">
  <summary class="chat-tool-err-summary">
    <span class="chat-tool-err-peek mono">${escapeHtml(peek)}</span>
    <span class="chat-tool-err-expand muted">${escapeHtml(t("chat.tool.error_expand", { n: String(text.length) }))}</span>
  </summary>
  <pre class="chat-tool-err-body mono">${escapeHtml(text)}</pre>
</details>`;
}

function renderAssistantToolLogEntry(e) {
  const isSearch = e.name === "web_search";
  const isFetch = e.name === "web_fetch";
  const title = isSearch ? t("chat.tool.web_search")
    : isFetch ? t("chat.tool.web_fetch")
      : escapeHtml(e.name);
  let detailHtml = "";
  if (isSearch && e.query) {
    let d = escapeHtml(e.query);
    if (e.max_results) d += ` · ${escapeHtml(t("chat.tool.max_results", { n: e.max_results }))}`;
    detailHtml = `<div class="chat-tool-detail mono">${d}</div>`;
  } else if (isFetch && e.url) {
    const u = escapeHtml(e.url);
    detailHtml = `<div class="chat-tool-detail"><a href="${u}" target="_blank" rel="noopener noreferrer" class="chat-tool-link mono">${u}</a></div>`;
  }
  const st = e.status || "unknown";
  const icon = st === "running" ? "◌" : st === "ok" ? "✓" : st === "error" ? "✗" : "·";
  let tail = "";
  if (st === "error" && e.error) {
    tail += renderToolErrorBlock(e.error);
  }
  if (st === "ok" && (e.result_preview || e.result_runes)) {
    const metaBits = [];
    if (e.result_runes) metaBits.push(t("chat.tool.chars", { n: e.result_runes }));
    const meta = metaBits.length ? `<span class="chat-tool-runes mono">${escapeHtml(metaBits.join(" · "))}</span>` : "";
    const prev = e.result_preview
      ? `<details class="chat-tool-preview"><summary>${escapeHtml(t("chat.tool.result_preview"))}</summary><pre>${escapeHtml(e.result_preview)}</pre></details>`
      : "";
    tail += `<div class="chat-tool-result-head">${meta}</div>${prev}`;
  }
  return `<div class="chat-tool-line chat-tool-line--${st}"><span class="chat-tool-ic" aria-hidden="true">${icon}</span><div class="chat-tool-main"><span class="chat-tool-name">${title}</span>${detailHtml}${tail}</div>${st === "running" ? "<span class=\"chat-tool-pulse\" aria-hidden=\"true\"></span>" : ""}</div>`;
}

function renderAssistantToolLog(m) {
  const entries = m.toolLog || [];
  if (!entries.length) return "";
  const lines = entries.map((e) => renderAssistantToolLogEntry(e));
  return `<div class="chat-tool-log" role="region" aria-label="${escapeHtml(t("chat.tool.region_label"))}">${lines.join("")}</div>`;
}

function renderAssistantTimeline(m) {
  const items = m.timeline || [];
  if (!items.length) return "";
  const segs = items.map((it) => {
    if (it.type === "think") {
      const o = it.thinkOpen !== false;
      return `<details class="chat-think" ${o ? "open" : ""} data-id="${escapeHtml(m.id)}" data-tl-seg="${escapeHtml(it.segId || "")}">
          <summary>${escapeHtml(t("chat.cap.thinking"))}</summary>
          <pre>${escapeHtml(it.think)}</pre>
        </details>`;
    }
    if (it.type === "md") {
      return `<div class="chat-timeline-md">${renderMarkdownSafe(it.content || "")}</div>`;
    }
    if (it.type === "tool" && it.entry) {
      return `<div class="chat-tool-log chat-tool-log--tl" role="region" aria-label="${escapeHtml(t("chat.tool.region_label"))}">${renderAssistantToolLogEntry(it.entry)}</div>`;
    }
    return "";
  });
  return `<div class="chat-timeline">${segs.join("")}</div>`;
}

function buildAssistantDebugFooter(m) {
  if (m.role !== "assistant" || m.streaming || !m.hasDebug) return "";
  const used = Math.max(0, Number(m.promptTokens) || 0);
  if (!used) return "";
  const maxCtx = Math.max(0, Number(m.contextMax) || 0);
  let ctxLine;
  if (maxCtx > 0) {
    const pct = Math.min(999, Math.round((used / maxCtx) * 100));
    ctxLine = t("chat.debug_context", { used: String(used), max: fmtCtx(maxCtx), pct });
  } else {
    ctxLine = t("chat.debug_context_plain", { used: String(used), max: "—" });
  }
  return `<footer class="chat-debug mono">${escapeHtml(ctxLine)}</footer>`;
}

function renderChatMessages() {
  const host = $("chat-messages");
  if (!chatMessages.length) {
    host.innerHTML = `<div class="chat-empty muted">${escapeHtml(t("chat.empty"))}</div>`;
    return;
  }
  host.innerHTML = chatMessages.map((m, i) => {
    const meta = [];
    if (m.role === "assistant" && !m.streaming && m.stopped) {
      meta.push(t("chat.stopped_badge_short"));
    }

    const files = (m.attachments || []).map((a) => {
      if (a.kind === "image" && a.data) {
        const src = attachmentImageSrc(a);
        if (!src) {
          return `<span class="chat-file-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)}</span>`;
        }
        return `<div class="chat-file-item chat-file-item-image">
          <button type="button" class="image-preview-open chat-msg-file-thumb" data-name="${escapeHtml(a.name)}">
            <img src="${src}" alt="" />
          </button>
          <span class="chat-file-name mono">${escapeHtml(a.name)}</span>
        </div>`;
      }
      if (a.kind === "audio" && a.data) {
        const src = attachmentAudioSrc(a);
        if (!src) {
          return `<span class="chat-file-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)}</span>`;
        }
        return `<div class="chat-file-item chat-file-item-audio">
          <audio class="chat-audio-player" controls preload="metadata" src="${src}"></audio>
          <span class="chat-file-name mono">${escapeHtml(a.name)}</span>
        </div>`;
      }
      if (a.kind === "text") {
        const prev = attachmentTextPreview(a);
        return `<div class="chat-file-item chat-file-item-text">
          <div class="chat-text-snippet mono">${escapeHtml(prev || "text file")}</div>
          <span class="chat-file-name mono">${escapeHtml(a.name)}</span>
        </div>`;
      }
      return `<span class="chat-file-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)}</span>`;
    }).join("");

    const hasTl = m.role === "assistant" && m.timeline && m.timeline.length > 0;
    const acc = m._accRaw || "";
    const flushI = hasTl ? (Number(m.segmentFlushIndex) || 0) : 0;
    const tailStr = hasTl && m.streaming && acc && acc.length > flushI ? acc.slice(flushI) : "";
    const tailParts = tailStr ? splitThink(tailStr) : { think: "", inThink: false, answer: "" };
    const showTailThink = hasTl && (Boolean((tailParts.think || "").trim()) || (m.streaming && tailParts.inThink));

    const thinkBlock = hasTl || !m.thinkContent
      ? ""
      : `<details class="chat-think" ${m.thinkOpen ? "open" : ""} data-id="${escapeHtml(m.id)}">
          <summary>${escapeHtml(thinkLabel(m.thinkMs || 0, !!m.streaming && !!m.inThink))}</summary>
          <pre>${escapeHtml(m.thinkContent)}</pre>
        </details>`;

    const toolLogBlock = m.role === "assistant" && !hasTl && m.toolLog?.length
      ? renderAssistantToolLog(m)
      : "";
    const timelineBlock = m.role === "assistant" && hasTl
      ? renderAssistantTimeline(m)
      : "";
    const tailThinkBlock = showTailThink
      ? `<details class="chat-think" ${m.tailThinkOpen !== false ? "open" : ""} data-id="${escapeHtml(m.id)}" data-tail="1">
          <summary>${escapeHtml(thinkLabel(m.thinkMs || 0, !!m.streaming && tailParts.inThink))}</summary>
          <pre>${escapeHtml(tailParts.think || "")}</pre>
        </details>`
      : "";

    let bodyHTML = "";
    const isImageModel = m.role === "assistant" && m.model && modelCaps(m.model).has("image");
    if (m.role === "assistant") {
      if (isImageModel) {
        if (m.streaming) {
          let progressInfo = "";
          if (m.completedSteps != null && m.totalSteps) {
            const pct = Math.min(100, Math.round((m.completedSteps / m.totalSteps) * 100));
            progressInfo = `<div class="chat-image-progress-text">Step ${m.completedSteps}/${m.totalSteps} (${pct}%)</div>
            <div class="chat-image-progress-bar-wrap">
              <div class="chat-image-progress-bar" style="width: ${pct}%"></div>
            </div>`;
          }
          bodyHTML = `<div class="chat-image-generating-card">
            <div class="chat-image-generating">
              <span class="chat-tool-ic chat-tool-pulse"></span>
              <span>${escapeHtml(t("chat.generating_image"))}</span>
            </div>
            ${progressInfo}
          </div>`;
        } else {
          const cleanedContent = String(m.content || "").replace(/\s+/g, "");
          const isError = m.isError || String(m.content || "").startsWith("Error:") || String(m.content || "").startsWith("Error ");
          if (isError) {
            bodyHTML = renderMarkdownSafe(m.content || "");
          } else if (cleanedContent) {
            const imgSrc = `data:image/png;base64,${cleanedContent}`;
            const imgName = `${m.model.replace(/[^a-zA-Z0-9]/g, "_")}_${m.id}.png`;
            bodyHTML = `<div class="chat-generated-image-container">
              <button type="button" class="image-preview-open chat-generated-image-thumb" data-name="${escapeHtml(imgName)}">
                <img src="${imgSrc}" alt="${escapeHtml(imgName)}" class="chat-generated-image" />
              </button>
              <div class="chat-generated-image-actions">
                <a href="${imgSrc}" download="${escapeHtml(imgName)}" class="btn-icon download-generated-image-btn" title="${escapeHtml(t("chat.download_image"))}">
                  <svg class="chat-download-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" width="18" height="18">
                    <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
                    <polyline points="7 10 12 15 17 10"/>
                    <line x1="12" y1="15" x2="12" y2="3"/>
                  </svg>
                </a>
              </div>
            </div>`;
          } else {
            bodyHTML = `<p class="muted">${escapeHtml(t("state.error_prefix"))} Empty response</p>`;
          }
        }
      } else {
        bodyHTML = renderMarkdownSafe(m.content || "");
      }
    } else {
      bodyHTML = `<p>${escapeHtml(m.content || "")}</p>`;
    }
    const roleLabel = m.role === "user" ? t("chat.role_user") : t("chat.role_assistant");
    const modelLabel = m.role === "assistant" && m.model
      ? `<span class="chat-model-used mono">${escapeHtml(m.model)}</span>`
      : "";
    const hideActions = (m.role === "assistant" && m.streaming) || isImageModel;
    const ttsPlaying = m.id === speakingMsgId;
    const ttsLabel = ttsPlaying ? t("chat.tts_stop") : t("chat.tts_play");
    const ttsBtn = hideActions ? "" : `<button type="button" class="btn-icon chat-tts-btn${ttsPlaying ? " active" : ""}" data-msg-id="${escapeHtml(m.id)}" title="${escapeHtml(ttsLabel)}" aria-label="${escapeHtml(ttsLabel)}">
<svg class="chat-tts-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
<path d="M11 5L6 9H3v6h3l5 4V5z"/>
<path d="M15.5 9.5a4 4 0 0 1 0 5"/>
<path d="M18.5 7a8 8 0 0 1 0 10"/>
</svg></button>`;
    const copyLabel = m.role === "user" ? t("chat.copy_user") : t("chat.copy_assistant");
    const copyBtn = hideActions ? "" : `<button type="button" class="btn-icon chat-copy-btn" data-msg-id="${escapeHtml(m.id)}" title="${escapeHtml(copyLabel)}" aria-label="${escapeHtml(copyLabel)}">
<svg class="chat-copy-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
<rect x="9" y="9" width="11" height="11" rx="2"/>
<path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
</svg></button>`;
    const isLast = i === chatMessages.length - 1;
    const canRegen = m.role === "assistant" && isLast && !m.streaming;
    const regenBtn = canRegen
      ? `<button type="button" class="btn-icon chat-regenerate-btn" data-msg-id="${escapeHtml(m.id)}" title="${escapeHtml(t("chat.regenerate_title"))}" aria-label="${escapeHtml(t("chat.regenerate"))}">
<svg class="chat-regenerate-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
<path d="M21 12a9 9 0 1 1-3-6.7"/><path d="M21 3v7h-7"/>
</svg></button>`
      : "";

    const footActions = `${regenBtn}${ttsBtn}${copyBtn}`;
    const finalMetrics = m.role === "assistant" && !m.streaming ? assistantMetricText(m) : "";
    const footBlock = footActions || finalMetrics
      ? `<div class="chat-msg-foot">
          ${finalMetrics ? `<span class="chat-msg-final-meta mono">${escapeHtml(finalMetrics)}</span>` : ""}
          <div class="chat-msg-foot-actions">
            ${footActions}
          </div>
        </div>`
      : "";
    const streamCls = m.role === "assistant" && m.streaming ? " chat-streaming" : "";
    return `
      <article class="chat-msg ${m.role === "user" ? "chat-user" : "chat-assistant"}${streamCls}" data-id="${escapeHtml(m.id)}">
        <header class="chat-msg-head">
          <div class="chat-msg-head-main">
            <span class="chat-role">${escapeHtml(roleLabel)}</span>
            ${modelLabel}
          </div>
        </header>
        ${meta.length ? `<div class="chat-msg-meta-line"><span class="chat-meta mono">${escapeHtml(meta.join(" · "))}</span></div>` : ""}
        ${files ? `<div class="chat-file-list">${files}</div>` : ""}
        ${timelineBlock}
        ${toolLogBlock}
        ${thinkBlock}
        ${tailThinkBlock}
        <div class="chat-md">${bodyHTML || "<p></p>"}</div>
        ${footBlock}
      </article>
    `;
  }).join("");

  host.querySelectorAll("details.chat-think").forEach((el) => {
    el.addEventListener("toggle", () => {
      const msg = chatMessages.find((x) => x.id === el.dataset.id);
      if (!msg) return;
      if (el.dataset.tail === "1") {
        msg.tailThinkOpen = el.open;
      } else if (el.dataset.tlSeg && msg.timeline) {
        const item = msg.timeline.find((i) => i.segId === el.dataset.tlSeg);
        if (item) item.thinkOpen = el.open;
      } else {
        msg.thinkOpen = el.open;
      }
    });
  });
  renderChatMath(host);
  scrollChatToBottom();
}

function renderAttachments() {
  const box = $("chat-attachments");
  if (!chatAttachments.length) {
    box.hidden = true;
    box.innerHTML = "";
    return;
  }
  box.hidden = false;
  box.innerHTML = chatAttachments.map((a) => {
    if (a.kind === "image") {
      const src = attachmentImageSrc(a);
      if (!src) {
        return `<span class="chat-attach-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)} <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button></span>`;
      }
      return `<div class="chat-attach-card">
        <button type="button" class="image-preview-open chat-attach-thumb" data-name="${escapeHtml(a.name)}" title="${escapeHtml(t("chat.image_preview_title"))}">
          <img src="${src}" alt="" />
        </button>
        <div class="chat-attach-foot">
          <span class="chat-attach-name mono" title="${escapeHtml(a.name)}">${escapeHtml(a.name)}</span>
          <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button>
        </div>
      </div>`;
    }
    if (a.kind === "audio" && a.data) {
      const src = attachmentAudioSrc(a);
      if (!src) {
        return `<span class="chat-attach-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)} <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button></span>`;
      }
      return `<div class="chat-attach-card chat-attach-card-audio">
        <audio class="chat-audio-player" controls preload="metadata" src="${src}"></audio>
        <div class="chat-attach-foot">
          <span class="chat-attach-name mono" title="${escapeHtml(a.name)}">${escapeHtml(a.name)}</span>
          <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button>
        </div>
      </div>`;
    }
    if (a.kind === "text") {
      const prev = attachmentTextPreview(a);
      return `<div class="chat-attach-card chat-attach-card-text">
        <div class="chat-text-snippet mono">${escapeHtml(prev || "text file")}</div>
        <div class="chat-attach-foot">
          <span class="chat-attach-name mono" title="${escapeHtml(a.name)}">${escapeHtml(a.name)}</span>
          <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button>
        </div>
      </div>`;
    }
    return `<span class="chat-attach-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)} <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button></span>`;
  }).join("");
  box.querySelectorAll(".chat-attach-x").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      e.preventDefault();
      chatAttachments = chatAttachments.filter((x) => x.id !== btn.dataset.id);
      renderAttachments();
    });
  });
}

function renderChatQueue() {
  const host = $("chat-queue");
  if (!host) return;
  if (!chatPendingQueue.length) {
    host.hidden = true;
    host.innerHTML = "";
    return;
  }
  host.hidden = false;
  const n = chatPendingQueue.length;
  const rows = chatPendingQueue.map((q, i) => {
    const hasText = String(q.text || "").trim().length > 0;
    const preview = hasText ? String(q.text).trim() : t("chat.queue_attachments_only");
    const short = preview.length > 100 ? `${preview.slice(0, 100)}…` : preview;
    const nAtt = (q.attachments || []).length;
    const fileLine = nAtt
      ? `<div class="chat-queue-files mono">${escapeHtml(t("chat.queue_files", { n: nAtt }))}</div>`
      : "";
    return `
      <div class="chat-queue-item" data-id="${escapeHtml(q.id)}">
        <div class="chat-queue-row1">
          <span class="chat-queue-n mono">${i + 1}</span>
          <p class="chat-queue-preview">${escapeHtml(short)}</p>
          <button type="button" class="btn-icon chat-queue-x" data-id="${escapeHtml(q.id)}" title="${escapeHtml(t("chat.queue_remove"))}">×</button>
        </div>
        ${fileLine}
      </div>`;
  }).join("");
  host.innerHTML = `
    <details class="chat-queue-details" open>
      <summary class="chat-queue-summary">
        <span class="chat-queue-chev" aria-hidden="true">▾</span>
        <span>${escapeHtml(t("chat.queue_title"))}</span>
        <span class="chat-queue-count mono">${n}</span>
      </summary>
      <div class="chat-queue-list">${rows}</div>
    </details>`;
  host.querySelectorAll(".chat-queue-x").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      chatPendingQueue = chatPendingQueue.filter((q) => q.id !== btn.dataset.id);
      renderChatQueue();
    });
  });
}

function stopThinkTicker() {
  if (!chatThinkTicker) return;
  clearInterval(chatThinkTicker);
  chatThinkTicker = null;
}

function startThinkTicker(msg) {
  stopThinkTicker();
  chatThinkTicker = setInterval(() => {
    if (!msg || !msg.inThink || !msg.thinkStartedAt) return;
    msg.thinkMs = Date.now() - msg.thinkStartedAt;
    msg.elapsedMs = Math.max(msg.elapsedMs || 0, Date.now() - (msg.streamStartedAt || msg.thinkStartedAt));
    updateLiveAssistantMetrics(msg, "");
    updateStreamBar();
    scheduleRenderChatMessages();
  }, 250);
}

function updateLiveAssistantMetrics(msg, deltaText) {
  if (!msg) return;
  const chunkEval = Number(msg.lastChunkEvalCount);
  if (Number.isFinite(chunkEval) && chunkEval > msg.completionTokens) {
    msg.completionTokens = chunkEval;
  } else if (deltaText) {
    msg.completionTokens += Math.max(1, Math.round(String(deltaText).length / 4));
  } else {
    const visibleText = `${msg.thinkContent || ""}${msg.content || ""}`;
    const estimate = Math.max(0, Math.round(visibleText.length / 4));
    if (estimate > msg.completionTokens) {
      msg.completionTokens = estimate;
    }
  }
  msg.tokens = msg.completionTokens;
  if (msg.elapsedMs > 0) {
    msg.tps = msg.completionTokens / (msg.elapsedMs / 1000);
  }
}

function resizeImageToBase64(file, maxDim = 1024) {
  return new Promise((resolve, reject) => {
    const img = new Image();
    const url = URL.createObjectURL(file);
    img.onload = () => {
      URL.revokeObjectURL(url);
      let width = img.width;
      let height = img.height;
      if (width > maxDim || height > maxDim) {
        if (width > height) {
          height = Math.round((height * maxDim) / width);
          width = maxDim;
        } else {
          width = Math.round((width * maxDim) / height);
          height = maxDim;
        }
      }
      const canvas = document.createElement("canvas");
      canvas.width = width;
      canvas.height = height;
      const ctx = canvas.getContext("2d");
      if (!ctx) {
        reject(new Error("could not get canvas 2d context"));
        return;
      }
      ctx.drawImage(img, 0, 0, width, height);
      const mime = file.type || "image/jpeg";
      const dataUrl = canvas.toDataURL(mime);
      resolve(dataUrl.includes(",") ? dataUrl.split(",")[1] : dataUrl);
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error("failed to load image for resizing"));
    };
    img.src = url;
  });
}

function toBase64(file) {
  if (file.type && file.type.startsWith("image/")) {
    return resizeImageToBase64(file, 1024);
  }
  return new Promise((resolve, reject) => {
    const fr = new FileReader();
    fr.onload = () => {
      const res = String(fr.result || "");
      resolve(res.includes(",") ? res.split(",")[1] : res);
    };
    fr.onerror = () => reject(new Error("read failed"));
    fr.readAsDataURL(file);
  });
}

async function addFiles(files) {
  const selectedModel = $("chat-model").value;
  const caps = modelCaps(selectedModel);
  const canVision = caps.has("vision") || caps.has("image");
  const canAudio = caps.has("audio");
  const accepted = [];
  for (const file of files) {
    const type = String(file.type || "");
    if (type.startsWith("image/") && canVision) accepted.push({ file, kind: "image" });
    if (type.startsWith("audio/") && canAudio) accepted.push({ file, kind: "audio" });
    if (isTextAttachmentFile(file)) accepted.push({ file, kind: "text" });
  }
  if (!accepted.length) {
    toast(t("chat.attach_not_supported"), "error");
    return;
  }

  for (const item of accepted) {
    if (item.file.size > 20 * 1024 * 1024) {
      toast(t("chat.file_too_large", { name: item.file.name }), "error");
      continue;
    }
    if (item.kind === "text") {
      const text = await item.file.text();
      chatAttachments.push({
        id: nanoid(),
        kind: item.kind,
        name: item.file.name,
        mime: item.file.type || "text/plain",
        text,
      });
    } else {
      const data = await toBase64(item.file);
      chatAttachments.push({
        id: nanoid(),
        kind: item.kind,
        name: item.file.name,
        mime: item.file.type,
        data,
      });
    }
  }
  renderAttachments();
}

function setRecordButtonState(isRecording) {
  const btn = $("chat-record-btn");
  if (!btn) return;
  btn.classList.toggle("is-recording", !!isRecording);
  const key = isRecording ? "chat.record_audio_stop" : "chat.record_audio_start";
  const label = t(key);
  btn.title = label;
  btn.setAttribute("aria-label", label);

  const inputEl = $("chat-input");
  if (inputEl) {
    if (isRecording) {
      inputEl.placeholder = t("chat.recording_placeholder") || "Recording audio... Speak now...";
      inputEl.classList.add("recording-active");
    } else {
      inputEl.placeholder = t("chat.input_placeholder") || "Write your message...";
      inputEl.classList.remove("recording-active");
    }
  }
}

function releaseAudioRecorder() {
  if (chatAudioProcessor) {
    try { chatAudioProcessor.disconnect(); } catch {}
    chatAudioProcessor.onaudioprocess = null;
    chatAudioProcessor = null;
  }
  if (chatAudioSource) {
    try { chatAudioSource.disconnect(); } catch {}
    chatAudioSource = null;
  }
  if (chatAudioContext) {
    try { chatAudioContext.close(); } catch {}
    chatAudioContext = null;
  }
  if (chatRecorderStream) {
    for (const tr of chatRecorderStream.getTracks()) {
      try { tr.stop(); } catch {}
    }
    chatRecorderStream = null;
  }
  chatAudioBuffers = [];
  chatAudioSampleRate = 0;
  chatIsRecording = false;
  setRecordButtonState(false);
}

async function startAudioRecording() {
  if (!navigator.mediaDevices?.getUserMedia) {
    toast(t("chat.record_audio_unsupported"), "error");
    return;
  }
  const caps = modelCaps($("chat-model").value);
  if (!caps.has("audio")) {
    toast(t("chat.attach_not_supported"), "error");
    return;
  }
  if (chatIsRecording) return;
  try {
    chatRecorderStream = await navigator.mediaDevices.getUserMedia({ audio: true });
    const AudioContextClass = window.AudioContext || window.webkitAudioContext;
    chatAudioContext = new AudioContextClass();
    if (chatAudioContext.state === "suspended") {
      await chatAudioContext.resume();
    }
    chatAudioSampleRate = chatAudioContext.sampleRate;
    chatAudioBuffers = [];
    chatAudioSource = chatAudioContext.createMediaStreamSource(chatRecorderStream);
    chatAudioProcessor = chatAudioContext.createScriptProcessor(4096, 1, 1);
    chatAudioProcessor.onaudioprocess = (event) => {
      chatAudioBuffers.push(new Float32Array(event.inputBuffer.getChannelData(0)));
    };
    chatAudioSource.connect(chatAudioProcessor);
    chatAudioProcessor.connect(chatAudioContext.destination);
    chatIsRecording = true;
    setRecordButtonState(true);
  } catch (err) {
    toast(t("toast.error", { msg: err?.message || t("chat.record_audio_error") }), "error");
    releaseAudioRecorder();
  }
}

function stopAudioRecording(silent = false) {
  if (!chatIsRecording) return;
  chatIsRecording = false;
  setRecordButtonState(false);
  
  if (silent) {
    if (chatAudioProcessor) {
      chatAudioProcessor.onaudioprocess = null;
    }
    releaseAudioRecorder();
    return;
  }
  
  const buffers = chatAudioBuffers.slice();
  const sampleRate = chatAudioSampleRate;
  releaseAudioRecorder();
  
  if (!buffers.length || !sampleRate) return;
  const blob = createWavBlob(buffers, sampleRate);
  if (blob.size === 0) return;
  
  const file = new File([blob], `recording-${Date.now()}.wav`, { type: "audio/wav" });
  addFiles([file]);
}

function createWavBlob(buffers, sampleRate) {
  if (!buffers.length || !sampleRate) return new Blob([], { type: "audio/wav" });
  const samples = mergeAudioBuffers(buffers);
  const buffer = new ArrayBuffer(44 + samples.length * 2);
  const view = new DataView(buffer);
  writeAscii(view, 0, "RIFF");
  view.setUint32(4, 36 + samples.length * 2, true);
  writeAscii(view, 8, "WAVE");
  writeAscii(view, 12, "fmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  writeAscii(view, 36, "data");
  view.setUint32(40, samples.length * 2, true);
  writePcm16(view, 44, samples);
  return new Blob([view], { type: "audio/wav" });
}

function mergeAudioBuffers(buffers) {
  const length = buffers.reduce((sum, buffer) => sum + buffer.length, 0);
  const merged = new Float32Array(length);
  let offset = 0;
  for (const buffer of buffers) {
    merged.set(buffer, offset);
    offset += buffer.length;
  }
  return merged;
}

function writePcm16(view, offset, samples) {
  for (let i = 0; i < samples.length; i++) {
    const sample = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(offset + i * 2, sample < 0 ? sample * 0x8000 : sample * 0x7fff, true);
  }
}

function writeAscii(view, offset, value) {
  for (let i = 0; i < value.length; i++) {
    view.setUint8(offset + i, value.charCodeAt(i));
  }
}

function buildOutboundMessages() {
  const out = [];
  const selectedModel = $("chat-model").value;
  const isImageModel = selectedModel && modelCaps(selectedModel).has("image");
  const systemPrompt = isImageModel ? "" : $("chat-system").value.trim();
  if (systemPrompt) out.push({ role: "system", content: systemPrompt });
  for (const m of chatMessages) {
    if (m.role !== "user" && m.role !== "assistant") continue;
    if (m.role === "assistant" && (m.streaming || !String(m.content || "").trim())) continue;
    const payload = { role: m.role, content: m.content || "" };
    if (m.role === "user" && m.attachments?.length) {
      const imgs = m.attachments.filter((a) => a.kind === "image").map((a) => a.data);
      const auds = m.attachments.filter((a) => a.kind === "audio").map((a) => a.data);
      const txts = m.attachments.filter((a) => a.kind === "text" && String(a.text || "").trim());
      const media = [...imgs, ...auds];
      if (media.length) payload.images = media;
      if (auds.length) payload.audios = auds;
      if (txts.length) {
        const blocks = txts.map((a) =>
          `--- ${a.name || "text"} ---\n${String(a.text || "").trim()}`).join("\n\n");
        const prefix = t("chat.attached_text_files");
        const extra = `${prefix}\n\n${blocks}`;
        payload.content = payload.content
          ? `${payload.content}\n\n${extra}`
          : extra;
      }
    }
    out.push(payload);
  }
  return out;
}

function readOptionNumber(id, fallback) {
  const n = Number($(id).value);
  return Number.isFinite(n) ? n : fallback;
}

function parseModelChatOptionsText(text) {
  const out = {};
  const src = String(text || "");
  if (!src.trim()) return out;
  for (const rawLine of src.split(/\r?\n/)) {
    let line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    line = line.replace(/^parameter\s+/i, "");
    const parts = line.split(/\s+/);
    if (parts.length < 2) continue;
    const key = parts[0].toLowerCase();
    const val = Number(parts[1]);
    if (!Number.isFinite(val)) continue;
    if (key === "temperature") out.temperature = val;
    else if (key === "top_k" || key === "top-k" || key === "topk") out.top_k = val;
    else if (key === "top_p" || key === "top-p" || key === "topp") out.top_p = val;
  }
  return out;
}

function extractModelChatDefaults(detail) {
  const fromParams = parseModelChatOptionsText(detail?.parameters);
  const fromModelfile = parseModelChatOptionsText(detail?.modelfile);
  return { ...fromParams, ...fromModelfile };
}

function setChatOptionsValues(opts) {
  if (opts.temperature != null) $("chat-temperature").value = String(opts.temperature);
  if (opts.top_k != null) $("chat-top-k").value = String(Math.round(opts.top_k));
  if (opts.top_p != null) $("chat-top-p").value = String(opts.top_p);
}

function saveChatOptionsToSession() {
  const opts = {
    system: $("chat-system")?.value,
    temperature: $("chat-temperature")?.value,
    top_k: $("chat-top-k")?.value,
    top_p: $("chat-top-p")?.value,
    no_think: $("chat-no-think")?.checked,
    web_tools: $("chat-web-tools")?.checked,
    image_width: $("chat-image-width")?.value,
    image_height: $("chat-image-height")?.value,
    image_steps: $("chat-image-steps")?.value,
    image_seed: $("chat-image-seed")?.value
  };
  localStorage.setItem("ollama_manager_chat_options", JSON.stringify(opts));
}

function restoreChatOptionsFromSession() {
  const raw = localStorage.getItem("ollama_manager_chat_options");
  if (!raw) return;
  try {
    const opts = JSON.parse(raw);
    if (!opts) return;
    if (opts.system !== undefined && $("chat-system")) {
      $("chat-system").value = opts.system;
    }
    if (opts.temperature !== undefined && $("chat-temperature")) {
      $("chat-temperature").value = opts.temperature;
    }
    if (opts.top_k !== undefined && $("chat-top-k")) {
      $("chat-top-k").value = opts.top_k;
    }
    if (opts.top_p !== undefined && $("chat-top-p")) {
      $("chat-top-p").value = opts.top_p;
    }
    if (opts.no_think !== undefined && $("chat-no-think")) {
      $("chat-no-think").checked = opts.no_think;
    }
    if (opts.web_tools !== undefined && $("chat-web-tools")) {
      $("chat-web-tools").checked = opts.web_tools;
    }
    if (opts.image_width !== undefined && $("chat-image-width")) {
      $("chat-image-width").value = opts.image_width;
    }
    if (opts.image_height !== undefined && $("chat-image-height")) {
      $("chat-image-height").value = opts.image_height;
    }
    if (opts.image_steps !== undefined && $("chat-image-steps")) {
      $("chat-image-steps").value = opts.image_steps;
    }
    if (opts.image_seed !== undefined && $("chat-image-seed")) {
      $("chat-image-seed").value = opts.image_seed;
    }
  } catch (e) {
    console.error("Error restoring chat options", e);
  }
}

async function applyChatDefaultsForModel(name, force = false) {
  const model = String(name || "").trim();
  if (!model) return;
  if (!force && lastChatDefaultsModel === model) return;

  const reqSeq = ++chatDefaultsReqSeq;
  let defaults = chatModelDefaultsCache.get(model);
  if (!defaults) {
    try {
      const detail = await api(`/api/models/${encodeURIComponent(model)}`);
      defaults = extractModelChatDefaults(detail);
      chatModelDefaultsCache.set(model, defaults);
    } catch {
      defaults = {};
    }
  }

  if (reqSeq !== chatDefaultsReqSeq) return;
  if ($("chat-model").value !== model) return;

  setChatOptionsValues({ ...CHAT_OPTION_FALLBACKS, ...defaults });
  lastChatDefaultsModel = model;
  restoreChatOptionsFromSession();
}

function isEmbeddingOnlyModel(modelName) {
  const caps = modelCaps(modelName);
  return caps.has("embedding") && !caps.has("completion");
}

function buildEmbeddingInputText() {
  const outbound = buildOutboundMessages();
  for (let i = outbound.length - 1; i >= 0; i -= 1) {
    const m = outbound[i];
    if (m.role === "user" && String(m.content || "").trim()) {
      return String(m.content).trim();
    }
  }
  return "";
}

function formatEmbeddingResult(vec) {
  const dims = Array.isArray(vec) ? vec.length : 0;
  const preview = (Array.isArray(vec) ? vec : [])
    .slice(0, 24)
    .map((n) => Number(n).toFixed(6))
    .join(", ");
  return t("chat.embed_result", {
    dims,
    preview: `[${preview}${dims > 24 ? ", ..." : ""}]`,
  });
}

function stopSpeechPlayback() {
  if (!window.speechSynthesis) return;
  try { window.speechSynthesis.cancel(); } catch {}
  speakingMsgId = "";
}

function speakMessage(msg) {
  if (!window.speechSynthesis || typeof SpeechSynthesisUtterance === "undefined") {
    toast(t("chat.tts_unsupported"), "error");
    return;
  }
  const text = textForSpeech(msg?.content || "");
  if (!text) return;
  const isSame = speakingMsgId && speakingMsgId === msg.id;
  if (isSame && window.speechSynthesis.speaking) {
    stopSpeechPlayback();
    renderChatMessages();
    return;
  }
  stopSpeechPlayback();

  const u = new SpeechSynthesisUtterance(text);
  const lang = speechLangFromUi();
  const voice = findBestVoice(lang);
  u.lang = voice?.lang || lang;
  if (voice) u.voice = voice;
  u.rate = 1;
  u.pitch = 1;
  speakingMsgId = msg.id;
  u.onend = () => {
    speakingMsgId = "";
    if (currentView === "chat") renderChatMessages();
  };
  u.onerror = () => {
    speakingMsgId = "";
    if (currentView === "chat") renderChatMessages();
  };
  window.speechSynthesis.speak(u);
  renderChatMessages();
}

async function readSSEStream(response, onEvent) {
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    while (true) {
      const splitAt = buf.indexOf("\n\n");
      if (splitAt < 0) break;
      const block = buf.slice(0, splitAt);
      buf = buf.slice(splitAt + 2);
      let event = "message";
      const dataLines = [];
      for (const line of block.split("\n")) {
        if (!line || line.startsWith(":")) continue;
        if (line.startsWith("event:")) event = line.slice(6).trim();
        if (line.startsWith("data:")) dataLines.push(line.slice(5).trimStart());
      }
      if (!dataLines.length) continue;
      let data = {};
      try {
        data = JSON.parse(dataLines.join("\n"));
      } catch {
        data = { raw: dataLines.join("\n") };
      }
      await onEvent(event, data);
    }
  }
}

function isAbortError(e) {
  if (!e) return false;
  if (e.name === "AbortError") return true;
  if (e.code === 20) return true; // legacy DOMException
  const msg = String(e.message || "").toLowerCase();
  return msg.includes("aborted") || msg.includes("user aborted");
}

function updateStreamBar() {
  const bar = $("chat-stream-bar");
  const btn = $("chat-stop-btn");
  const label = bar?.querySelector(".chat-stream-label");
  if (bar) bar.hidden = !chatStreamLock;
  if (label) {
    const metrics = activeStreamMessage ? assistantMetricText(activeStreamMessage, { showZero: true }) : "";
    label.textContent = metrics ? `${t("chat.generating")} ${metrics}` : t("chat.generating");
  }
  if (btn) {
    btn.disabled = !chatStreamLock;
    btn.title = t("chat.stop_hint");
  }
}

function stopChatGeneration() {
  if (!chatStreamLock || !chatAbortController) return;
  try { chatAbortController.abort(); } catch (_) {}
}

function newAssistantMessage() {
  return {
    id: nanoid(),
    role: "assistant",
    model: "",
    content: "",
    thinkContent: "",
    thinkOpen: true,
    inThink: false,
    thinkMs: 0,
    thinkStartedAt: 0,
    streaming: true,
    elapsedMs: 0,
    tokens: 0,
    hasDebug: false,
    promptTokens: 0,
    completionTokens: 0,
    evalDurationNs: 0,
    contextMax: 0,
    tps: null,
    lastChunkEvalCount: null,
    streamStartedAt: 0,
    toolLog: [],
    thinkBlockStarted: false,
    thinkBlockClosed: false,
    timeline: [],
    segmentFlushIndex: 0,
    tailThinkOpen: true,
  };
}

/** Keep the chat pane pinned to the latest content (streaming + layout). */
function scrollChatToBottom() {
  const host = $("chat-scroll-shell") || $("chat-messages");
  if (!host) return;
  const go = () => {
    host.scrollTop = host.scrollHeight;
    $("chat-messages")?.lastElementChild?.scrollIntoView({ block: "end" });
  };
  go();
  requestAnimationFrame(() => {
    go();
    requestAnimationFrame(go);
  });
}

/** Smoothly scroll thinking and response blocks of the currently streaming message. */
function scrollActiveBlocks() {
  const streamingMsg = document.querySelector("article.chat-msg.chat-streaming");
  if (!streamingMsg) return;
  const thinkPres = streamingMsg.querySelectorAll("details.chat-think[open] pre");
  thinkPres.forEach((el) => {
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  });
  const content = streamingMsg.querySelector(".chat-md");
  if (content) {
    content.scrollTo({ top: content.scrollHeight, behavior: "smooth" });
  }
}

async function runChatRequest(assistantMsg) {
  const modelName = $("chat-model").value;
  assistantMsg.model = modelName;
  if (isEmbeddingOnlyModel(modelName)) {
    const input = buildEmbeddingInputText();
    if (!input) {
      throw new Error(t("chat.embed_empty_input"));
    }
    const started = Date.now();
    const data = await api("/api/embed", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ model: modelName, input }),
    });
    assistantMsg.streaming = false;
    assistantMsg.elapsedMs = Date.now() - started;
    assistantMsg.hasDebug = false;
    assistantMsg.content = formatEmbeddingResult(data.embedding || []);
    flushChatRender();
    return;
  }

  const caps = modelCaps(modelName);
  const isImageModel = caps.has("image");
  const canThinkToggle = caps.has("thinking");
  const noThink = canThinkToggle ? $("chat-no-think").checked : false;
  const canTools = caps.has("tools");
  const webToolsOn = !isImageModel && canTools && $("chat-web-tools").checked;

  const options = {};
  const imageParams = {};
  if (isImageModel) {
    imageParams.width = Math.min(1024, Math.max(128, Math.round(readOptionNumber("chat-image-width", 512))));
    imageParams.height = Math.min(1024, Math.max(128, Math.round(readOptionNumber("chat-image-height", 512))));
    imageParams.steps = Math.max(1, Math.round(readOptionNumber("chat-image-steps", 4)));
    const seedVal = Math.round(readOptionNumber("chat-image-seed", 0));
    if (seedVal > 0) {
      imageParams.seed = seedVal;
    }
  } else {
    options.temperature = readOptionNumber("chat-temperature", CHAT_OPTION_FALLBACKS.temperature);
    options.top_k = Math.round(readOptionNumber("chat-top-k", CHAT_OPTION_FALLBACKS.top_k));
    options.top_p = readOptionNumber("chat-top-p", CHAT_OPTION_FALLBACKS.top_p);
  }

  const payload = {
    model: modelName,
    think: canThinkToggle ? !noThink : undefined,
    options,
    messages: buildOutboundMessages(),
    ...imageParams,
  };
  if (webToolsOn) payload.web_tools = true;

  chatAbortController = new AbortController();
  chatStreamLock = true;
  activeStreamMessage = assistantMsg;
  updateStreamBar();
  const turnStartedAt = Date.now();
  assistantMsg.streamStartedAt = turnStartedAt;
  let assistantRaw = "";
  try {
    const res = await fetch("/api/chat", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal: chatAbortController.signal,
    });
    if (!res.ok) {
      let msg = res.statusText;
      try {
        const j = await res.json();
        if (j.error) msg = j.error;
      } catch {}
      throw new Error(msg || "chat failed");
    }
    await readSSEStream(res, async (event, data) => {
      if (event === "tool") {
        if (!assistantMsg.toolLog) assistantMsg.toolLog = [];
        if (data?.phase === "start") {
          flushSegmentToTimeline(assistantMsg, assistantRaw, false);
          const entry = {
            name: data.name,
            query: data.query,
            url: data.url,
            max_results: data.max_results,
            status: "running",
          };
          assistantMsg.toolLog.push(entry);
          if (!assistantMsg.timeline) assistantMsg.timeline = [];
          assistantMsg.timeline.push({ type: "tool", entry });
        } else if (data?.phase === "done") {
          for (let i = assistantMsg.toolLog.length - 1; i >= 0; i -= 1) {
            const e = assistantMsg.toolLog[i];
            if (e.name === data.name && e.status === "running") {
              e.status = data.ok ? "ok" : "error";
              e.error = data.error || "";
              e.result_preview = data.result_preview || "";
              e.result_runes = data.result_runes;
              break;
            }
          }
        }
        assistantMsg._accRaw = assistantRaw;
        scheduleRenderChatMessages();
      } else if (event === "chunk") {
        if (data?.completed != null) {
          assistantMsg.completedSteps = data.completed;
        }
        if (data?.total != null) {
          assistantMsg.totalSteps = data.total;
        }
        const thinkDelta = data?.message?.thinking || "";
        const contentDelta = data?.message?.content || "";
        if (thinkDelta) {
          if (!assistantMsg.thinkBlockStarted) {
            assistantRaw += "<think>\n";
            assistantMsg.thinkBlockStarted = true;
          } else if (assistantMsg.thinkBlockClosed) {
            assistantRaw += "\n<think>\n";
            assistantMsg.thinkBlockClosed = false;
          }
          assistantRaw += thinkDelta;
        }
        if (contentDelta) {
          if (assistantMsg.thinkBlockStarted && !assistantMsg.thinkBlockClosed) {
            assistantRaw += "\n</think>\n";
            assistantMsg.thinkBlockClosed = true;
          }
          assistantRaw += contentDelta;
        }
        const parts = splitThink(assistantRaw);
        assistantMsg.thinkContent = parts.think;
        assistantMsg.content = parts.answer;
        assistantMsg.inThink = parts.inThink;
        assistantMsg.elapsedMs = Date.now() - turnStartedAt;
        const chunkEval = Number(data?.eval_count);
        if (Number.isFinite(chunkEval) && chunkEval >= 0) {
          assistantMsg.lastChunkEvalCount = chunkEval;
        }
        updateLiveAssistantMetrics(assistantMsg, thinkDelta || contentDelta);
        if (parts.inThink && !assistantMsg.thinkStartedAt) {
          assistantMsg.thinkStartedAt = Date.now();
          startThinkTicker(assistantMsg);
        }
        if (!parts.inThink && assistantMsg.thinkStartedAt) {
          assistantMsg.thinkMs = Date.now() - assistantMsg.thinkStartedAt;
          stopThinkTicker();
        }
        assistantMsg._accRaw = assistantRaw;
        updateStreamBar();
        scheduleRenderChatMessages();
      } else if (event === "error") {
        throw new Error(data?.error || "stream error");
      } else if (event === "done") {
        if (assistantMsg.thinkBlockStarted && !assistantMsg.thinkBlockClosed) {
          assistantRaw += "\n</think>\n";
          assistantMsg.thinkBlockClosed = true;
        }
        const p2 = splitThink(assistantRaw);
        assistantMsg.thinkContent = p2.think;
        assistantMsg.content = p2.answer;
        assistantMsg.inThink = p2.inThink;
        if (assistantMsg.toolLog && assistantMsg.toolLog.length > 0) {
          flushSegmentToTimeline(assistantMsg, assistantRaw, true);
        }
        assistantMsg._accRaw = "";
        assistantMsg.streaming = false;
        assistantMsg.inThink = false;
        assistantMsg.elapsedMs = Number(data.elapsed_ms) || (Date.now() - turnStartedAt);
        assistantMsg.tokens = Number(data.total_tokens) || 0;
        assistantMsg.promptTokens = Number(data.prompt_tokens) || 0;
        assistantMsg.completionTokens = Number(data.completion_tokens) || 0;
        assistantMsg.evalDurationNs = Number(data.eval_duration_ns) || 0;
        const mdl = modelByName(assistantMsg.model || modelName);
        assistantMsg.contextMax = Number(mdl?.context_length) || 0;
        const evNs = assistantMsg.evalDurationNs;
        const comp = assistantMsg.completionTokens;
        if (evNs > 0 && comp >= 0) {
          assistantMsg.tps = comp / (evNs / 1e9);
        } else if (comp > 0 && assistantMsg.elapsedMs > 0) {
          assistantMsg.tps = comp / (assistantMsg.elapsedMs / 1000);
        } else {
          assistantMsg.tps = null;
        }
        assistantMsg.hasDebug = true;
        chatLastUsedTokens = assistantMsg.tokens || (assistantMsg.promptTokens + assistantMsg.completionTokens);
        updateChatContextMeter();
        flushChatRender();
      }
    });
    assistantMsg.streaming = false;
    if (assistantMsg.thinkStartedAt && assistantMsg.thinkMs === 0) {
      assistantMsg.thinkMs = Date.now() - assistantMsg.thinkStartedAt;
    }
    assistantMsg.elapsedMs = assistantMsg.elapsedMs || (Date.now() - turnStartedAt);
  } catch (e) {
    assistantMsg.streaming = false;
    assistantMsg.inThink = false;
    if (isAbortError(e)) {
      assistantMsg.stopped = true;
      const hasAnswer = String(assistantMsg.content || "").trim().length > 0;
      const hasThink = String(assistantMsg.thinkContent || "").trim().length > 0;
      if (!hasAnswer && !hasThink) {
        assistantMsg.content = t("chat.stopped_empty");
      }
    } else {
      let errMsg = e.message || "failed";
      if (errMsg.includes("mlx runner failed") || errMsg.includes("failed to initialize MLX") || errMsg.includes("failed to load MLX")) {
        errMsg = t("chat.error_mlx_unsupported");
      }
      assistantMsg.isError = true;
      const isImg = assistantMsg.model && modelCaps(assistantMsg.model).has("image");
      if (!assistantMsg.content || isImg) {
        assistantMsg.content = t("chat.error_reply", { msg: errMsg });
      }
      toast(t("toast.error", { msg: errMsg }), "error");
    }
  } finally {
    chatAbortController = null;
    stopThinkTicker();
    chatStreamLock = false;
    activeStreamMessage = null;
    updateStreamBar();
    flushChatRender();
    if (chatPendingQueue.length > 0) {
      const next = chatPendingQueue.shift();
      renderChatQueue();
      setTimeout(() => { runOneChatTurn(next.text, next.attachments); }, 0);
    } else {
      renderChatQueue();
    }
  }
}

async function runOneChatTurn(text, attachments) {
  const userMsg = {
    id: nanoid(),
    role: "user",
    content: text,
    attachments: (attachments || []).map((a) => ({ ...a })),
  };
  chatMessages.push(userMsg);
  const assistantMsg = newAssistantMessage();
  assistantMsg.model = $("chat-model").value;
  chatMessages.push(assistantMsg);
  renderChatMessages();
  await runChatRequest(assistantMsg);
}

async function regenerateLastAssistantMessage(clickedId) {
  if (chatStreamLock) {
    toast(t("chat.regenerate_busy"), "error");
    return;
  }
  if (!chatMessages.length) return;
  const last = chatMessages[chatMessages.length - 1];
  if (last.id !== clickedId || last.role !== "assistant" || last.streaming) return;
  if (chatMessages.length < 2) return;
  const prev = chatMessages[chatMessages.length - 2];
  if (!prev || prev.role !== "user") return;
  if ($("chat-send-btn")?.disabled) {
    const why = $("chat-send-btn")?.title || t("status.unreachable");
    toast(why, "error");
    return;
  }
  if (!models.length) {
    toast(t("chat.no_models"), "error");
    return;
  }
  chatMessages.pop();
  const assistantMsg = newAssistantMessage();
  assistantMsg.model = $("chat-model").value;
  chatMessages.push(assistantMsg);
  renderChatMessages();
  await runChatRequest(assistantMsg);
}

async function sendChatMessage() {
  if ($("chat-send-btn")?.disabled) return;
  const text = $("chat-input").value.trim();
  if (!text && chatAttachments.length === 0) return;
  if (!models.length) {
    toast(t("chat.no_models"), "error");
    return;
  }

  const snapText = text;
  const snapAtt = chatAttachments.map((a) => ({ ...a }));
  $("chat-input").value = "";
  chatAttachments = [];
  renderAttachments();

  if (chatStreamLock) {
    chatPendingQueue.push({ id: nanoid(), text: snapText, attachments: snapAtt });
    renderChatQueue();
    return;
  }

  await runOneChatTurn(snapText, snapAtt);
}

function bindChatEvents() {
  const chatView = $("chat-view");
  if (!chatView) return;
  window.addEventListener("pagehide", stopSpeechPlayback);
  window.addEventListener("beforeunload", stopSpeechPlayback);
  window.addEventListener("popstate", async () => {
    stopSpeechPlayback();
    await handleRouting();
  });
  window.addEventListener("resize", () => {
    if (window.innerWidth > 900) $("chat-view")?.classList.remove("chat-options-open");
  });
  $("chat-btn")?.addEventListener("click", showChatView);
  $("chat-back-btn")?.addEventListener("click", () => {
    showModelsView();
    resetChatState();
  });
  $("chat-options-toggle")?.addEventListener("click", () => {
    $("chat-view")?.classList.toggle("chat-options-open");
  });
  $("chat-options-close")?.addEventListener("click", () => {
    $("chat-view")?.classList.remove("chat-options-open");
  });
  chatView.addEventListener("click", (e) => {
    if (!$("chat-view")?.classList.contains("chat-options-open")) return;
    if (e.target.closest(".chat-side")) return;
    if (e.target.closest("#chat-options-toggle")) return;
    $("chat-view")?.classList.remove("chat-options-open");
  });
  $("chat-model")?.addEventListener("change", () => {
    updateChatCapabilityUI();
    updateChatContextMeter();
    void applyChatDefaultsForModel($("chat-model").value, true);
    
    const name = $("chat-model").value;
    const model = modelByName(name);
    if (model && model.digest) {
      const urlDigest = model.digest.replace(":", "-");
      const newPath = "/chat/" + urlDigest;
      if (window.location.pathname !== newPath) {
        history.replaceState(null, "", newPath);
      }
    }
  });
  $("chat-model-copy-btn")?.addEventListener("click", async () => {
    const val = $("chat-model-name-value")?.textContent || "";
    if (!val) return;
    const ok = await copyTextToClipboard(val);
    toast(ok ? t("chat.copied") : t("chat.copy_failed"), ok ? "success" : "error");
  });
  $("chat-send-btn")?.addEventListener("click", sendChatMessage);
  ($("chat-scroll-shell") || $("chat-messages"))?.addEventListener("click", async (e) => {
    const regenB = e.target.closest(".chat-regenerate-btn");
    if (regenB) {
      e.preventDefault();
      const id = regenB.getAttribute("data-msg-id");
      if (id) await regenerateLastAssistantMessage(id);
      return;
    }
    const ttsB = e.target.closest(".chat-tts-btn");
    if (ttsB) {
      e.preventDefault();
      const id = ttsB.getAttribute("data-msg-id");
      if (!id) return;
      const msg = chatMessages.find((x) => x.id === id);
      if (!msg) return;
      speakMessage(msg);
      return;
    }
    const btn = e.target.closest(".chat-copy-btn");
    if (!btn) return;
    e.preventDefault();
    const id = btn.getAttribute("data-msg-id");
    if (!id) return;
    const msg = chatMessages.find((x) => x.id === id);
    if (!msg) return;
    const text = String(msg.content || "");
    const ok = await copyTextToClipboard(text);
    toast(ok ? t("chat.copied") : t("chat.copy_failed"), ok ? "success" : "error");
  });
  const stopBtn = $("chat-stop-btn");
  if (stopBtn) {
    stopBtn.addEventListener("click", () => stopChatGeneration());
  }
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && $("chat-view")?.classList.contains("chat-options-open")) {
      $("chat-view")?.classList.remove("chat-options-open");
      return;
    }
    if (e.code !== "Backspace" || !e.ctrlKey || !e.shiftKey) return;
    if (currentView !== "chat" || !chatStreamLock) return;
    e.preventDefault();
    stopChatGeneration();
  });
  $("chat-input")?.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      if ($("chat-send-btn")?.disabled) return;
      sendChatMessage();
    }
  });
  $("chat-input")?.addEventListener("paste", async (e) => {
    if (currentView !== "chat") return;
    const cd = e.clipboardData;
    if (!cd?.items?.length) return;
    const imageFiles = [];
    for (const item of cd.items) {
      if (item.kind === "file" && String(item.type || "").startsWith("image/")) {
        const f = item.getAsFile();
        if (f) imageFiles.push(f);
      }
    }
    if (!imageFiles.length) return;
    e.preventDefault();
    const extraText = cd.getData("text/plain") || "";
    await addFiles(imageFiles);
    if (extraText) {
      const ta = $("chat-input");
      const start = ta.selectionStart ?? ta.value.length;
      const end = ta.selectionEnd ?? ta.value.length;
      const v = ta.value;
      ta.value = v.slice(0, start) + extraText + v.slice(end);
      const pos = start + extraText.length;
      ta.setSelectionRange(pos, pos);
    }
  });
  $("chat-image-btn")?.addEventListener("click", () => $("chat-image-input")?.click());
  $("chat-audio-btn")?.addEventListener("click", () => $("chat-audio-input")?.click());
  $("chat-text-btn")?.addEventListener("click", () => $("chat-text-input")?.click());
  $("chat-record-btn")?.addEventListener("click", async () => {
    if (chatIsRecording) {
      stopAudioRecording();
    } else {
      await startAudioRecording();
    }
  });
  $("chat-image-input")?.addEventListener("change", async () => {
    await addFiles(Array.from($("chat-image-input").files || []));
    $("chat-image-input").value = "";
  });
  $("chat-audio-input")?.addEventListener("change", async () => {
    await addFiles(Array.from($("chat-audio-input").files || []));
    $("chat-audio-input").value = "";
  });
  $("chat-text-input")?.addEventListener("change", async () => {
    await addFiles(Array.from($("chat-text-input").files || []));
    $("chat-text-input").value = "";
  });

  const dropHost = chatView;
  dropHost.addEventListener("dragenter", (e) => {
    if (currentView !== "chat") return;
    e.preventDefault();
    chatDndDepth += 1;
    $("chat-dropzone").hidden = false;
  });
  dropHost.addEventListener("dragover", (e) => {
    if (currentView !== "chat") return;
    e.preventDefault();
  });
  dropHost.addEventListener("dragleave", (e) => {
    if (currentView !== "chat") return;
    e.preventDefault();
    chatDndDepth = Math.max(0, chatDndDepth - 1);
    if (chatDndDepth === 0) $("chat-dropzone").hidden = true;
  });
  dropHost.addEventListener("drop", async (e) => {
    if (currentView !== "chat") return;
    e.preventDefault();
    chatDndDepth = 0;
    $("chat-dropzone").hidden = true;
    const files = Array.from(e.dataTransfer?.files || []);
    if (!files.length) return;
    await addFiles(files);
  });

  document.addEventListener("click", (e) => {
    const open = e.target.closest(".image-preview-open");
    if (!open) return;
    const im = open.querySelector("img");
    if (!im || !im.getAttribute("src")) return;
    e.preventDefault();
    openImagePreview(im.src, open.getAttribute("data-name") || "");
  });

  const imgPrevBack = $("image-preview-backdrop");
  if (imgPrevBack) {
    imgPrevBack.addEventListener("click", closeImagePreview);
  }
  const imgPrevClose = $("image-preview-close");
  if (imgPrevClose) {
    imgPrevClose.addEventListener("click", closeImagePreview);
  }
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    const modal = $("image-preview-modal");
    if (modal && !modal.hidden) {
      e.preventDefault();
      closeImagePreview();
    }
  });

  const optionIds = [
    "chat-system",
    "chat-temperature",
    "chat-top-k",
    "chat-top-p",
    "chat-no-think",
    "chat-web-tools",
    "chat-image-width",
    "chat-image-height",
    "chat-image-steps",
    "chat-image-seed"
  ];
  for (const id of optionIds) {
    const el = $(id);
    if (el) {
      const eventName = el.type === "checkbox" ? "change" : "input";
      el.addEventListener(eventName, saveChatOptionsToSession);
    }
  }
}

// ---------- delete ----------
let pendingDelete = null;
let pendingConfirmResolve = null;

function getSelectedDeleteReason() {
  const checked = document.querySelector("input[name='confirm-delete-reason']:checked");
  return checked ? String(checked.value || "").trim() : "";
}

function closeConfirmModal(result) {
  const reasonWrap = $("confirm-delete-reason-wrap");
  const reason = (reasonWrap && !reasonWrap.hidden) ? getSelectedDeleteReason() : "";
  $("confirm-modal").hidden = true;
  if (pendingConfirmResolve) {
    const resolve = pendingConfirmResolve;
    pendingConfirmResolve = null;
    resolve({ ok: !!result, reason });
  }
}

function askConfirm({ title, text, okText, okClass = "primary", mono = "", showDeleteReason = false }) {
  if (pendingConfirmResolve) closeConfirmModal(false);
  $("confirm-title").textContent = title || t("confirm.title");
  const safe = escapeHtml(text || "").replace(
    mono ? escapeHtml(mono) : "{__NO_MONO__}",
    mono ? `<span class="mono">${escapeHtml(mono)}</span>` : "{__NO_MONO__}",
  );
  $("confirm-text").innerHTML = safe;
  const reasonWrap = $("confirm-delete-reason-wrap");
  if (reasonWrap) {
    reasonWrap.hidden = !showDeleteReason;
    reasonWrap.querySelectorAll("input[name='confirm-delete-reason']").forEach((r) => { r.checked = false; });
  }
  const ok = $("confirm-ok");
  ok.textContent = okText || t("confirm.title");
  ok.className = okClass;
  $("confirm-modal").hidden = false;
  return new Promise((resolve) => { pendingConfirmResolve = resolve; });
}

function confirmDelete(name) {
  pendingDelete = name;
  // Substitute {name} ourselves so we can wrap it in a mono span.
  const text = t("confirm.delete_text", { name: "{__NAME__}" });
  askConfirm({
    title: t("detail.delete_title"),
    text: text.replace("{__NAME__}", name),
    okText: t("action.delete"),
    okClass: "danger",
    mono: name,
    showDeleteReason: true,
  }).then(async ({ ok, reason }) => {
    const delName = pendingDelete;
    pendingDelete = null;
    if (!ok || !delName) return;
    try {
      await api("/api/models/" + encodeURIComponent(delName), {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ reason: reason || "" }),
      });
      toast(t("toast.deleted", { name: delName }), "success");
      if (activeName === delName) { $("detail-panel").hidden = true; activeName = null; }
      refreshModels();
    } catch (e) {
      toast(t("toast.delete_error", { msg: e.message }), "error");
    }
  });
}
$("confirm-cancel").addEventListener("click", () => { pendingDelete = null; closeConfirmModal(false); });
$("confirm-ok").addEventListener("click", () => closeConfirmModal(true));

// ---------- downloads queue ----------

/** Pasted "ollama pull|run X", a bare model name, or text containing a URL → value for /api/pull. */
function normalizePullInput(raw) {
  let s = String(raw || "").replace(/\r\n/g, " ").trim();
  if (!s) return "";
  if ((s.startsWith('"') && s.endsWith('"')) || (s.startsWith("'") && s.endsWith("'"))) {
    s = s.slice(1, -1).trim();
  }
  for (let d = 0; d < 2; d++) {
    const m = s.match(/^\s*ollama\s+(?:pull|run)\s+(.+?)\s*$/i);
    if (m) s = m[1].trim();
    else break;
  }
  s = s.replace(/\s+/g, " ").trim();
  if (/^https?:\/\//i.test(s)) {
    s = s.split(/\s+/)[0];
    return s.replace(/[),.;:>\]}]+$/g, "");
  }
  const u = s.match(/https?:\/\/[^\s<>"'()]+/i);
  if (u) {
    return u[0].replace(/[),.;:>\]}]+$/g, "");
  }
  return s;
}

// Open a single long-lived SSE connection to the job manager. On
// disconnect, exponential backoff up to 30s.
function connectJobsStream() {
  if (jobsStream) return;
  try {
    jobsStream = new EventSource("/api/jobs/events", { withCredentials: true });
  } catch (e) {
    scheduleJobsReconnect();
    return;
  }

  jobsStream.addEventListener("snapshot", (ev) => {
    try {
      const data = JSON.parse(ev.data);
      jobs = new Map((data.jobs || []).map((j) => [j.id, j]));
      queuePaused = !!data.queue_paused;
      onJobsChanged();
    } catch {}
  });

  jobsStream.addEventListener("update", (ev) => {
    try {
      const data = JSON.parse(ev.data);
      if ("queue_paused" in data) queuePaused = !!data.queue_paused;
      const j = data.job;
      if (!j || !j.id) return;
      const prev = jobs.get(j.id);
      jobs.set(j.id, j);
      onJobsChanged();
      // If a job just transitioned to a terminal state, refresh the model list
      // so a freshly installed model appears and a cancelled/failed one doesn't
      // leave stale entries.
      if (prev && !isTerminal(prev.status) && isTerminal(j.status) && j.status === "done") {
        toast(t("downloads.installed", { name: j.name || "model" }), "success");
        refreshModels();
      }
    } catch {}
  });

  jobsStream.addEventListener("remove", (ev) => {
    try {
      const data = JSON.parse(ev.data);
      if ("queue_paused" in data) queuePaused = !!data.queue_paused;
      if (!data.id) return;
      jobs.delete(data.id);
      onJobsChanged();
    } catch {}
  });

  jobsStream.onopen = () => { jobsBackoffMs = 1000; };
  jobsStream.onerror = () => {
    if (jobsStream) { jobsStream.close(); jobsStream = null; }
    scheduleJobsReconnect();
  };
}

function scheduleJobsReconnect() {
  const delay = jobsBackoffMs;
  jobsBackoffMs = Math.min(jobsBackoffMs * 2, 30000);
  setTimeout(connectJobsStream, delay);
}

function isTerminal(status) {
  return status === "done" || status === "error" || status === "cancelled";
}

function onJobsChanged() {
  updateDownloadsBadge();
  if (!$("downloads-modal").hidden) renderDownloads();
  renderTable(); // Update main model list to show/hide pending downloads
}

function jobsByStatus() {
  const buckets = { active: [], queued: [], paused: [], finished: [] };
  for (const j of jobs.values()) {
    if (j.status === "running") buckets.active.push(j);
    else if (j.status === "queued") buckets.queued.push(j);
    else if (j.status === "paused") buckets.paused.push(j);
    else buckets.finished.push(j);
  }
  // Active and queued keep their natural (creation) order; finished shows
  // most recent first.
  const byCreated = (a, b) => new Date(a.created_at) - new Date(b.created_at);
  buckets.active.sort(byCreated);
  buckets.queued.sort(byCreated);
  buckets.paused.sort(byCreated);
  buckets.finished.sort((a, b) => new Date(b.finished_at || b.created_at) - new Date(a.finished_at || a.created_at));
  return buckets;
}

function updateDownloadsBadge() {
  let activeCount = 0;
  for (const j of jobs.values()) {
    if (j.status === "running" || j.status === "queued" || j.status === "paused") activeCount++;
  }
  const badge = $("downloads-count");
  if (activeCount > 0) {
    badge.textContent = String(activeCount);
    badge.hidden = false;
  } else {
    badge.hidden = true;
  }
}

function renderDownloads() {
  const buckets = jobsByStatus();
  $("dl-count-active").textContent = String(buckets.active.length);
  $("dl-count-queued").textContent = String(buckets.queued.length);
  $("dl-count-paused").textContent = String(buckets.paused.length);
  $("dl-count-finished").textContent = String(buckets.finished.length);
  $("dl-list-active").innerHTML = buckets.active.map(jobCardHTML).join("") || emptyRow();
  $("dl-list-queued").innerHTML = buckets.queued.map(jobCardHTML).join("") || emptyRow();
  $("dl-list-paused").innerHTML = buckets.paused.map(jobCardHTML).join("") || emptyRow();
  $("dl-list-finished").innerHTML = buckets.finished.map(jobCardHTML).join("") || emptyRow();
  const hasAny = jobs.size > 0;
  $("dl-empty").hidden = hasAny;
  $("dl-total-badge").hidden = !hasAny;
  if (hasAny) {
    $("dl-total-badge").textContent = t("downloads.jobs_count", { n: jobs.size });
  }
  $("dl-clear-btn").disabled = buckets.finished.length === 0;

  // Section-level pause button (queued -> paused)
  const pauseBtn = $("dl-pause-btn");
  if (buckets.queued.length > 0) {
    pauseBtn.hidden = false;
    pauseBtn.title = t("downloads.pause_queue");
    pauseBtn.textContent = "⏸";
  } else {
    pauseBtn.hidden = true;
  }

  // Section-level resume button (paused -> queued)
  const resumeBtn = $("dl-resume-btn");
  if (buckets.paused.length > 0) {
    resumeBtn.hidden = false;
    resumeBtn.title = t("downloads.resume_queue");
    resumeBtn.textContent = "▶";
  } else {
    resumeBtn.hidden = true;
  }

  // Wire per-card buttons.
  document.querySelectorAll("#downloads-modal .dl-item [data-action]").forEach((btn) => {
    btn.addEventListener("click", async (e) => {
      e.stopPropagation();
      const id = btn.dataset.id;
      const action = btn.dataset.action;
      if (!id || !action) return;
      if (action === "cancel") {
        try {
          await api(`/api/jobs/${encodeURIComponent(id)}/cancel`, { method: "POST" });
        } catch (err) {
          toast(t("toast.error", { msg: err.message }), "error");
        }
      } else if (action === "remove") {
        try {
          await api(`/api/jobs/${encodeURIComponent(id)}`, { method: "DELETE" });
        } catch (err) {
          toast(t("toast.error", { msg: err.message }), "error");
        }
      } else if (action === "pause") {
        try {
          await api(`/api/jobs/${encodeURIComponent(id)}/pause`, { method: "POST" });
        } catch (err) {
          toast(t("toast.error", { msg: err.message }), "error");
        }
      } else if (action === "resume") {
        try {
          await api(`/api/jobs/${encodeURIComponent(id)}/resume`, { method: "POST" });
        } catch (err) {
          toast(t("toast.error", { msg: err.message }), "error");
        }
      } else if (action === "retry") {
        const j = jobs.get(id);
        if (!j) return;
        try {
          await api("/api/pull", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ name: j.name }),
          });
        } catch (err) {
          toast(t("toast.error", { msg: err.message }), "error");
        }
      }
    });
  });

  // Click on a finished download card opens chat with that model
  // if the model is still installed.
  document.querySelectorAll("#downloads-modal .dl-item").forEach((card) => {
    card.addEventListener("click", async () => {
      const id = card.dataset.id;
      if (!id) return;
      const j = jobs.get(id);
      if (!j || j.status !== "done" || !j.name) return;
      await refreshModels();
      if (!modelByName(j.name)) return;
      closeDownloads();
      showChatViewWithModel(j.name);
    });
  });
}

function emptyRow() {
  return `<div class="dl-empty-row muted">${escapeHtml(t("downloads.section_empty"))}</div>`;
}

function jobCardHTML(j) {
  const pct = Math.max(0, Math.min(100, j.percent || 0));
  const sizeLine = j.total > 0
    ? (j.status === "done" ? fmtBytes(j.total) : `${fmtBytes(j.completed || 0)} / ${fmtBytes(j.total)}`)
    : "";
  const showFinishedAt = (j.status === "done" || j.status === "error") && !!j.finished_at;
  const finishedLine = showFinishedAt ? fmtRelativeTime(j.finished_at) : "";
  const finishedTitle = showFinishedAt ? fmtDateTimeFull(j.finished_at) : "";
  const finishedHTML = finishedLine
    ? `<span class="dl-finished muted" title="${escapeHtml(finishedTitle)}">${escapeHtml(finishedLine)}</span>`
    : "";
  const statusText = jobStatusLabel(j);
  const showBar = j.status === "running" || j.status === "done" || j.status === "paused" || (j.total > 0);
  const progress = showBar
    ? `<div class="dl-progress"><div class="dl-progress-bar dl-progress-${j.status}" style="width:${pct.toFixed(1)}%"></div></div>`
    : "";
  const pctText = j.status === "running" || j.status === "paused"
    ? `<span class="dl-pct mono">${pct.toFixed(1)}%</span>`
    : "";
  let speedHTML = "";
  if (j.status === "running" && j.speed > 0) {
    speedHTML = `<span class="dl-speed muted">${fmtSpeed(j.speed)}</span>`;
  }
  let etaHTML = "";
  if (j.status === "running" && j.total > 0 && j.speed > 0) {
    const remaining = (j.total - (j.completed || 0)) / j.speed;
    etaHTML = `<span class="dl-eta muted">~${fmtETA(remaining)}</span>`;
  }

  let actionBtn = "";
  if (j.status === "running" || j.status === "queued") {
    actionBtn = `
      <button class="ghost dl-pause" data-action="pause" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.pause"))}">⏸</button>
      <button class="btn-icon" data-action="cancel" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.cancel"))}">×</button>`;
  } else if (j.status === "paused") {
    actionBtn = `
      <button class="ghost dl-resume" data-action="resume" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.resume"))}">▶</button>
      <button class="btn-icon" data-action="cancel" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.cancel"))}">×</button>`;
  } else if (j.status === "error" || j.status === "cancelled") {
    actionBtn = `
      <button class="ghost dl-retry" data-action="retry" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.retry"))}">↻</button>
      <button class="btn-icon" data-action="remove" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.remove"))}">×</button>`;
  } else {
    // done
    actionBtn = `<button class="btn-icon" data-action="remove" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.remove"))}">×</button>`;
  }

  const errBlock = j.error ? `<div class="dl-error">${escapeHtml(j.error)}</div>` : "";

  const cardClass = j.status === "done"
    ? `dl-item dl-${j.status} dl-clickable`
    : `dl-item dl-${j.status}`;
  return `
    <div class="${cardClass}" data-id="${escapeHtml(j.id)}">
      <div class="dl-row1">
        <span class="dl-name mono">${escapeHtml(j.name)}</span>
        <span class="dl-status dl-status-${j.status}">${escapeHtml(statusText)}</span>
        <span class="dl-actions">${actionBtn}</span>
      </div>
      ${progress}
      <div class="dl-row2">
        <div class="dl-left">
          ${pctText}
          ${speedHTML}
          ${etaHTML}
        </div>
        <div class="dl-right">
          <span class="dl-bytes muted">${escapeHtml(sizeLine)}</span>
          ${finishedHTML}
        </div>
      </div>
      ${errBlock}
    </div>
  `;
}

function jobStatusLabel(j) {
  switch (j.status) {
    case "running":
      return j.status_text ? j.status_text : t("downloads.status.running");
    case "queued":
      return t("downloads.status.queued");
    case "paused":
      return t("downloads.status.paused");
    case "done":
      return t("downloads.status.done");
    case "error":
      return t("downloads.status.error");
    case "cancelled":
      return t("downloads.status.cancelled");
    default:
      return j.status || "";
  }
}

function openDownloads() {
  renderDownloads();
  $("downloads-modal").hidden = false;
  setTimeout(() => $("dl-add-input").focus(), 20);
}
function closeDownloads() {
  $("downloads-modal").hidden = true;
}
function closeSettings() {
  $("settings-modal").hidden = true;
}

$("downloads-btn").addEventListener("click", openDownloads);
$("downloads-close").addEventListener("click", closeDownloads);
$("downloads-x").addEventListener("click", closeDownloads);
$("downloads-modal").addEventListener("click", (e) => {
  if (e.target === $("downloads-modal")) closeDownloads();
});

$("dl-pause-btn").addEventListener("click", async () => {
  try {
    await api("/api/jobs/pause", { method: "POST" });
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
});

$("dl-resume-btn").addEventListener("click", async () => {
  try {
    await api("/api/jobs/resume", { method: "POST" });
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
});

function uninstallReasonToText(reasonKey) {
  const key = String(reasonKey || "").trim();
  if (!key) return "";
  const byKey = {
    load_failed: "confirm.delete_reason_load_failed",
    missing_capabilities: "confirm.delete_reason_missing_capabilities",
    too_slow: "confirm.delete_reason_too_slow",
    obsolete_or_outdated: "confirm.delete_reason_obsolete_or_outdated",
  };
  const i18nKey = byKey[key];
  return i18nKey ? t(i18nKey) : "";
}

$("dl-add-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const input = $("dl-add-input");
  const name = normalizePullInput(input.value);
  if (!name) return;
  let installedNow = !!modelByName(name);
  if (!installedNow) {
    try {
      await refreshModels();
      installedNow = !!modelByName(name);
    } catch {
      // keep best-effort check
    }
  }
  let previous = null;
  let uninstallReason = "";
  try {
    const res = await api(`/api/download-history/${encodeURIComponent(name)}`);
    previous = res && res.exists ? res.history : null;
    uninstallReason = uninstallReasonToText(res?.uninstall?.reason);
  } catch {
    // history endpoint is best-effort for UX warning
  }
  let confirmMsg = "";
  if (installedNow || previous?.last_done_at) {
    confirmMsg = t("downloads.reenqueue_done_confirm", { name });
  } else if (previous?.last_error_at || (previous?.error_count || 0) > 0) {
    confirmMsg = t("downloads.reenqueue_error_confirm", { name });
  }
  if (uninstallReason) {
    const reasonLine = t("downloads.reenqueue_last_uninstall_reason", { reason: uninstallReason });
    if (!confirmMsg) {
      confirmMsg = t("downloads.reenqueue_with_reason_confirm", { name, reason: uninstallReason });
    } else {
      confirmMsg = `${confirmMsg}\n\n${reasonLine}`;
    }
  }
  if (confirmMsg && !window.confirm(confirmMsg)) return;
  try {
    await api("/api/pull", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    input.value = "";
    toast(t("downloads.enqueued", { name }), "success");
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
});

$("dl-clear-btn").addEventListener("click", async () => {
  try {
    await api("/api/jobs/clear", { method: "POST" });
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
});

$("memory-widget")?.addEventListener("click", () => {
  openRunningModal();
});
$("memory-widget")?.addEventListener("keydown", (e) => {
  if (e.key === "Enter" || e.key === " ") {
    e.preventDefault();
    openRunningModal();
  }
});
$("running-refresh")?.addEventListener("click", () => refreshRunningModalList());
$("running-close")?.addEventListener("click", closeRunningModal);
$("running-x")?.addEventListener("click", closeRunningModal);
$("running-modal")?.addEventListener("click", (e) => {
  if (e.target === $("running-modal")) closeRunningModal();
});
$("running-unload-all")?.addEventListener("click", async () => {
  const { ok } = await askConfirm({
    title: t("running.unload_all"),
    text: t("running.unload_all_confirm"),
    okText: t("running.unload_all"),
    okClass: "danger",
  });
  if (!ok) return;
  try {
    const res = await api("/api/running/unload-all", { method: "POST" });
    const unloaded = Array.isArray(res?.unloaded) ? res.unloaded : [];
    if (unloaded.length) {
      toast(t("running.unload_all_done", { n: unloaded.length }), "success");
    }
    const failed = res?.failed && typeof res.failed === "object" ? Object.entries(res.failed) : [];
    if (failed.length) {
      const [name, msg] = failed[0];
      toast(t("running.unload_failed", { name, msg }), "error");
    }
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
  await refreshRunningModalList({ silent: true });
  refreshLoadedState();
  refreshStatus();
});

// ---------- topbar buttons ----------
async function logoutAndRedirect() {
  await fetch("/logout", { method: "POST", credentials: "same-origin" });
  window.location.href = "/login";
}

document.querySelector(".brand")?.addEventListener("click", () => {
  showModelsView();
  history.pushState(null, "", "/");
});
$("refresh-btn").addEventListener("click", () => { refreshStatus(); refreshModels(); });
$("settings-logout-btn").addEventListener("click", logoutAndRedirect);

$("tests-btn")?.addEventListener("click", () => {
  showTestsView();
});
$("tests-new-test-btn")?.addEventListener("click", () => {
  void showTestEditorView(null);
});
$("tests-new-group-btn")?.addEventListener("click", () => {
  createNewGroup();
});
$("test-editor-back")?.addEventListener("click", () => {
  showTestsView();
});
$("test-editor-cancel")?.addEventListener("click", () => {
  showTestsView();
});
$("test-editor-save")?.addEventListener("click", () => {
  void saveTestEditor();
});
$("test-editor-delete")?.addEventListener("click", () => {
  void deleteTestEditor();
});
$("te-eval-type")?.addEventListener("change", () => {
  updateAgentSettingsVisibility();
  updateEvalConfigVisibility();
});

function updateAgentSettingsVisibility() {
  const isAgent = $("te-eval-type")?.value === "agent";
  const panel = $("te-agent-settings");
  if (panel) panel.hidden = !isAgent;
}

function updateEvalConfigVisibility() {
  const type = $("te-eval-type")?.value;
  const expectedWrap = $("te-eval-expected-wrap");
  const patternWrap = $("te-eval-pattern-wrap");
  const configWrap = $("te-eval-config-wrap");
  if (!expectedWrap || !patternWrap || !configWrap) return;
  expectedWrap.hidden = type !== "exact_match" && type !== "contains";
  patternWrap.hidden = type !== "regex";
  configWrap.hidden = type !== "json_schema" && type !== "agent";
}

// Test editor attachments
$("te-add-image-btn")?.addEventListener("click", () => {
  $("te-image-input")?.click();
});
$("te-add-audio-btn")?.addEventListener("click", () => {
  $("te-audio-input")?.click();
});
$("te-image-input")?.addEventListener("change", (e) => {
  const files = e.target.files;
  if (files?.length) {
    void handleTestEditorFileInput(Array.from(files), "image");
  }
  e.target.value = "";
});
$("te-audio-input")?.addEventListener("change", (e) => {
  const files = e.target.files;
  if (files?.length) {
    void handleTestEditorFileInput(Array.from(files), "audio");
  }
  e.target.value = "";
});
$("te-required-caps")?.addEventListener("input", () => {
  updateTestEditorAutoCaps();
});

// Disable autocomplete globally for all inputs, textareas, and selects.
document.querySelectorAll("input, textarea, select").forEach((el) => {
  if (!el.hasAttribute("autocomplete")) {
    el.setAttribute("autocomplete", "off");
  }
});

// ---------- settings ----------
let currentConfig = null;

async function openSettings() {
  try {
    currentConfig = await api("/api/config");
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
    return;
  }
  $("set-language").value = currentConfig.language;
  $("set-port").value = currentConfig.port;
  $("set-expose").checked = !!currentConfig.expose_network;
  $("set-password").value = "";
  updatePasswordSection();
  updateExposeWarning();
  updateBindPreview();
  $("settings-modal").hidden = false;
}

function updatePasswordSection() {
  if (!currentConfig) return;
  const badge = $("pwd-badge");
  if (currentConfig.has_password) {
    badge.textContent = t("settings.pwd_set");
    badge.className = "badge badge-good";
  } else {
    badge.textContent = t("settings.pwd_unset");
    badge.className = "badge badge-muted";
  }
  $("pwd-clear-btn").hidden = !currentConfig.has_password;
  $("settings-logout-btn").hidden = !currentConfig.has_password;
}

function updateBindPreview() {
  if (!currentConfig) return;
  const badge = $("bind-preview");
  const expose = $("set-expose").checked;
  if (expose) {
    badge.textContent = t("settings.bind_lan");
    badge.className = "badge badge-warn";
  } else {
    badge.textContent = t("settings.bind_local");
    badge.className = "badge badge-muted";
  }
}

function updateExposeWarning() {
  if (!currentConfig) return;
  const wantExpose = $("set-expose").checked;
  $("expose-warning").hidden = !(wantExpose && !currentConfig.has_password);
  updateBindPreview();
}

$("settings-btn").addEventListener("click", openSettings);
$("settings-close").addEventListener("click", closeSettings);
$("settings-x").addEventListener("click", closeSettings);
$("settings-modal").addEventListener("click", (e) => {
  if (e.target === $("settings-modal")) closeSettings();
});

// Live language switch on dropdown change.
$("set-language").addEventListener("change", () => {
  const lang = $("set-language").value;
  window.I18n.setLang(lang);
  // Re-render dynamic UI to pick up the new language.
  refreshStatus();
  renderTable();
  if (activeName) openDetail(activeName);
  updateChatContextMeter();
  renderAttachments();
  renderChatMessages();
  renderChatQueue();
  updateStreamBar();
  updateChatCapabilityUI();
  updateChatSendEnabled();
  updatePasswordSection();
});

$("set-expose").addEventListener("change", updateExposeWarning);

$("settings-save").addEventListener("click", async () => {
  if (!currentConfig) return;
  const port = parseInt($("set-port").value, 10);
  if (!Number.isFinite(port) || port < 1 || port > 65535) {
    toast(t("toast.error", { msg: "port 1..65535" }), "error");
    return;
  }
  const body = {
    language: $("set-language").value,
    port,
    expose_network: $("set-expose").checked,
  };
  try {
    const res = await api("/api/config", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    currentConfig = { ...currentConfig, ...res };
    window.I18n.setLang(res.language);
    toast(res.needs_restart ? t("settings.saved_restart") : t("settings.saved"), "success");
    $("settings-modal").hidden = true;
    refreshStatus();
    renderTable();
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
});

$("pwd-save-btn").addEventListener("click", async () => {
  const pwd = $("set-password").value;
  if (pwd.length < 4) {
    toast(t("settings.pwd_too_short"), "error");
    return;
  }
  try {
    const res = await api("/api/config/password", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password: pwd }),
    });
    currentConfig.has_password = res.has_password;
    $("set-password").value = "";
    updatePasswordSection();
    updateExposeWarning();
    refreshStatus();
    toast(t("settings.pwd_saved"), "success");
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
});

$("pwd-clear-btn").addEventListener("click", async () => {
  try {
    const res = await api("/api/config/password", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password: "" }),
    });
    currentConfig.has_password = res.has_password;
    $("set-password").value = "";
    updatePasswordSection();
    updateExposeWarning();
    refreshStatus();
    toast(t("settings.pwd_cleared"), "success");
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
});

$("settings-archived-btn")?.addEventListener("click", () => {
  closeSettings();
  showArchivedOnly = true;
  $("archived-banner").hidden = false;
  renderTable();
});

$("btn-back-active")?.addEventListener("click", () => {
  showArchivedOnly = false;
  $("archived-banner").hidden = true;
  renderTable();
});

// ---------- init ----------
// Show Tests button only on desktop.
if (window.innerWidth > 900) {
  $("tests-btn").hidden = false;
}
window.addEventListener("resize", () => {
  const btn = $("tests-btn");
  if (btn) btn.hidden = window.innerWidth <= 900;
});
// ---------- agent session ----------

async function showAgentSessionView(testId) {
  currentAgentTestId = testId;
  hideAllMainViews();
  currentView = "agent-session";
  $("agent-session-view").hidden = false;
  if (window.location.pathname !== "/tests/agent/" + testId) {
    history.pushState(null, "", "/tests/agent/" + testId);
  }

  // Look up the test to display title.
  const test = tests.find((t) => t.id === testId);
  $("agent-session-title").textContent = test ? test.name : "Agent Test";

  // Try to find an existing session for this test, or create one.
  try {
    const sessions = await api("/api/tests/agent/sessions");
    const existing = sessions.find((s) => s.test_id === testId);
    if (existing) {
      currentAgentSession = existing;
      renderAgentSession();
      return;
    }
  } catch {
    // ignore, will create below
  }

  try {
    const created = await api("/api/tests/agent/sessions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ test_id: testId, model_id: null }),
    });
    currentAgentSession = created;
    renderAgentSession();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function refreshAgentSession() {
  if (!currentAgentSession) return;
  try {
    const s = await api("/api/tests/agent/sessions/" + encodeURIComponent(currentAgentSession.id));
    currentAgentSession = s;
    renderAgentSession();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

function renderAgentSession() {
  const s = currentAgentSession;
  if (!s) return;

  $("agent-meta-max-turns").textContent = String(s.max_turns || "—");
  $("agent-meta-current-turn").textContent = String(s.turns?.length || 0);
  $("agent-meta-model").textContent = s.model_id || t("tests.agent_no_model");

  const statusBadge = $("agent-session-status");
  if (s.completed) {
    statusBadge.textContent = t("tests.agent_completed");
    statusBadge.className = "agent-status-badge completed";
    $("agent-feedback-area").hidden = true;
  } else {
    statusBadge.textContent = t("tests.agent_in_progress");
    statusBadge.className = "agent-status-badge in-progress";
    $("agent-feedback-area").hidden = false;
  }

  renderAgentTurns(s.turns || []);
  renderAgentSandbox(s.id);
}

function renderAgentTurns(turns) {
  const container = $("agent-turns-timeline");
  if (!container) return;
  if (!turns.length) {
    container.innerHTML = `<div class="muted">${escapeHtml(t("tests.agent_no_turns"))}</div>`;
    return;
  }
  container.innerHTML = turns.map((turn, idx) => {
    const roleClass = turn.role === "tool" ? "agent-turn-tool" : turn.role === "system" ? "agent-turn-system" : "agent-turn-user";
    const content = escapeHtml(turn.content || "");
    const toolCall = turn.tool_call ? `<pre class="agent-turn-tool-call">${escapeHtml(JSON.stringify(turn.tool_call, null, 2))}</pre>` : "";
    return `<div class="agent-turn ${roleClass}">
      <div class="agent-turn-header">#${idx + 1} — ${escapeHtml(turn.role)}</div>
      <div class="agent-turn-body">${content || ""}</div>
      ${toolCall}
      ${turn.tool_result ? `<pre class="agent-turn-tool-result">${escapeHtml(JSON.stringify(turn.tool_result, null, 2))}</pre>` : ""}
    </div>`;
  }).join("");
  container.scrollTop = container.scrollHeight;
}

async function renderAgentSandbox(sessionId) {
  const container = $("agent-sandbox-tree");
  if (!container) return;
  try {
    const files = await api("/api/tests/agent/sessions/" + encodeURIComponent(sessionId) + "/files");
    if (!files || !files.length) {
      container.innerHTML = `<div class="muted">${escapeHtml(t("tests.agent_empty_sandbox"))}</div>`;
      return;
    }
    container.innerHTML = files.map((f) => {
      const icon = f.is_dir ? "📁" : "📄";
      return `<div class="agent-sandbox-item" data-path="${escapeHtml(f.path)}">
        <span>${icon} ${escapeHtml(f.name)}</span>
      </div>`;
    }).join("");
  } catch {
    container.innerHTML = `<div class="muted">${escapeHtml(t("tests.agent_sandbox_error"))}</div>`;
  }
}

async function submitAgentFeedback() {
  if (!currentAgentSession) return;
  const input = $("agent-feedback-input");
  const content = input.value.trim();
  if (!content) return;
  try {
    await api("/api/tests/agent/sessions/" + encodeURIComponent(currentAgentSession.id) + "/message", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ role: "user", content }),
    });
    input.value = "";
    await refreshAgentSession();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function resetAgentSession() {
  if (!currentAgentSession) return;
  try {
    await api("/api/tests/agent/sessions/" + encodeURIComponent(currentAgentSession.id) + "/reset", { method: "POST" });
    await refreshAgentSession();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function deleteAgentSession() {
  if (!currentAgentSession) return;
  const ok = await askConfirm({
    title: t("tests.agent_delete_session"),
    text: t("tests.agent_delete_confirm"),
    okText: t("action.delete"),
    okClass: "danger",
  });
  if (!ok.ok) return;
  try {
    await api("/api/tests/agent/sessions/" + encodeURIComponent(currentAgentSession.id), { method: "DELETE" });
    currentAgentSession = null;
    showTestsView();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

// ---------- battery runner ----------

async function openBatteryModal() {
  if (selectedGroupId === "") return;
  batterySelectedModels.clear();
  $("battery-modal").hidden = false;
  void renderBatteryModalModels();
  // Load system info preview.
  const sysEl = $("battery-modal-sysinfo");
  if (sysEl) {
    sysEl.textContent = t("status.loading");
    try {
      const info = await api("/api/runner/sys-info");
      const parts = [];
      if (info.os) parts.push(`${t("battery.sys_os")}: ${info.os}`);
      if (info.cpu_model) parts.push(`${t("battery.sys_cpu")}: ${info.cpu_model}`);
      if (info.gpu_model) parts.push(`${t("battery.sys_gpu")}: ${info.gpu_model}`);
      if (info.ram_gb) parts.push(`${t("battery.sys_ram")}: ${info.ram_gb} GB`);
      if (info.vram_gb) parts.push(`${t("battery.sys_vram")}: ${info.vram_gb} GB`);
      sysEl.textContent = parts.length ? parts.join(" | ") : "";
      sysEl.hidden = !parts.length;
    } catch (e) {
      sysEl.textContent = "";
      sysEl.hidden = true;
    }
  }
}

function closeBatteryModal() {
  $("battery-modal").hidden = true;
}

async function renderBatteryModalModels() {
  const container = $("battery-modal-models");
  if (!container) return;
  container.innerHTML = `<div class="muted">${t("status.loading")}</div>`;
  try {
    const data = await api("/api/models");
    batteryModels = data.models || [];
  } catch (e) {
    container.innerHTML = `<div class="muted">${escapeHtml(e.message)}</div>`;
    return;
  }

  // Determine required caps for the selected group.
  const groupTests = tests.filter((t) => t.group_id === selectedGroupId && t.active && t.evaluation_type !== "agent");
  const requiredCaps = new Set();
  for (const t of groupTests) {
    for (const c of t.required_caps || []) requiredCaps.add(c);
  }

  const items = batteryModels
    .filter((m) => (m.capabilities || []).includes("completion"))
    .map((m) => {
      const caps = m.capabilities || [];
      const hasAnyRequired = [...requiredCaps].some((c) => caps.includes(c));
      const missing = [];
      for (const c of requiredCaps) {
        if (!caps.includes(c)) missing.push(c);
      }
      const disabled = !hasAnyRequired;
      const title = disabled ? t("battery.model_unsupported_caps") + ": " + missing.join(", ") : "";
      return { m, disabled, title };
    })
    .filter(({ disabled }) => !disabled);

  if (items.length === 0) {
    container.innerHTML = `<div class="muted">${t("state.empty_models")}</div>`;
    return;
  }

  container.innerHTML = items.map(({ m, disabled, title }) => {
    const capsHtml = (m.capabilities || []).map((c) => `<span class="pill">${escapeHtml(c)}</span>`).join("");
    return `
      <label class="battery-model-item" title="${escapeHtml(title)}">
        <input type="checkbox" value="${escapeHtml(m.name)}" ${disabled ? "disabled" : ""} />
        <span class="battery-model-name">${escapeHtml(m.name)}</span>
        <span class="battery-model-caps">${capsHtml}</span>
      </label>
    `;
  }).join("");

  container.querySelectorAll('input[type="checkbox"]').forEach((cb) => {
    cb.addEventListener("change", () => {
      if (cb.checked) batterySelectedModels.add(cb.value);
      else batterySelectedModels.delete(cb.value);
    });
  });
}

let batteryPollTimer = null;
let batteryCompletedTests = [];
let batteryLastTestSnapshot = null;

function showBatteryProgressView(modelIDs, runID) {
  batteryCompletedTests = [];
  batteryLastTestSnapshot = null;
  const completedEl = $("battery-completed-tests");
  if (completedEl) { completedEl.innerHTML = ""; completedEl.hidden = true; }
  hideAllMainViews();
  currentView = "battery-progress";
  $("battery-progress-view").hidden = false;
  const sub = $("battery-progress-sub");
  if (sub) sub.textContent = t("battery.progress_sub", { count: String(modelIDs.length) });
  const container = $("battery-progress-models");
  if (container) {
    container.innerHTML = modelIDs.map((m) => `
      <div class="battery-progress-model" data-model="${escapeHtml(m)}">
        <span class="battery-progress-dot"></span>
        <span class="battery-progress-name">${escapeHtml(m)}</span>
        <span class="battery-progress-status">${t("battery.status_pending")}</span>
      </div>
    `).join("");
  }
  void pollBatteryProgress(runID, modelIDs);
}

function renderBatteryCompletedTests() {
  const container = $("battery-completed-tests");
  const heading = $("battery-completed-tests-heading");
  if (!container) return;
  if (!batteryCompletedTests.length) {
    container.hidden = true;
    if (heading) heading.hidden = true;
    return;
  }
  container.hidden = false;
  if (heading) heading.hidden = false;
  container.innerHTML = batteryCompletedTests.map((item, idx) => {
    const thinkBlock = item.thinking
      ? `<div class="battery-completed-label">${escapeHtml(t("battery.stream_thinking"))}</div><div class="battery-completed-block">${escapeHtml(item.thinking)}</div>`
      : "";
    const responseText = item.response ? escapeHtml(item.response) : `<em class="muted">${escapeHtml(t("battery.no_response"))}</em>`;
    const respBlock = `<div class="battery-completed-label">${escapeHtml(t("battery.stream_response"))}</div><div class="battery-completed-block">${responseText}</div>`;
    return `<details class="battery-completed-item" ${idx === batteryCompletedTests.length - 1 ? "open" : ""}>
      <summary><span>${escapeHtml(item.name)}</span><span class="battery-completed-meta">${escapeHtml(item.model)}</span></summary>
      <div class="battery-completed-body">
        <div class="battery-completed-label">${escapeHtml(t("battery.prompt"))}</div>
        <div class="battery-completed-block">${escapeHtml(item.prompt || "")}</div>
        ${thinkBlock}
        ${respBlock}
      </div>
    </details>`;
  }).join("");
}

async function pollBatteryProgress(runID, modelIDs) {
  if (batteryPollTimer) {
    clearTimeout(batteryPollTimer);
    batteryPollTimer = null;
  }
  try {
    const p = await api("/api/runner/runs/" + encodeURIComponent(runID) + "/progress");
    // Detect test change: archive previous snapshot.
    if (batteryLastTestSnapshot && batteryLastTestSnapshot.testId && p.test_id && batteryLastTestSnapshot.testId !== p.test_id) {
      batteryCompletedTests.push(batteryLastTestSnapshot);
      renderBatteryCompletedTests();
    }
    // Update current test info.
    const currentDiv = $("battery-progress-current");
    if (currentDiv && p.test_name) {
      currentDiv.innerHTML = `
        <div class="test-name">${escapeHtml(p.test_name)}</div>
        <div class="test-meta">${escapeHtml(p.model)} — ${p.test_index} / ${p.total_tests}</div>
      `;
    } else if (currentDiv) {
      currentDiv.innerHTML = "";
    }
    // Update streaming panel.
    const streamPanel = $("battery-stream-panel");
    const currentTest = tests.find((t) => t.id === p.test_id);
    if (streamPanel && p.test_name && !p.done) {
      streamPanel.hidden = false;
      const promptName = $("battery-stream-prompt-name");
      const promptBlock = $("battery-stream-prompt");
      const thinkingBlock = $("battery-stream-thinking");
      const responseBlock = $("battery-stream-response");
      if (promptName) promptName.textContent = escapeHtml(p.test_name);
      if (promptBlock && currentTest) promptBlock.textContent = currentTest.prompt || "";
      if (thinkingBlock) {
        thinkingBlock.textContent = p.partial_thinking || "";
        thinkingBlock.parentElement.hidden = !p.partial_thinking;
        if (thinkingBlock.previousElementSibling) thinkingBlock.previousElementSibling.hidden = !p.partial_thinking;
        thinkingBlock.scrollTo({ top: thinkingBlock.scrollHeight, behavior: "smooth" });
      }
      if (responseBlock) {
        responseBlock.textContent = p.partial_response || "";
        responseBlock.scrollTo({ top: responseBlock.scrollHeight, behavior: "smooth" });
      }
    } else if (streamPanel) {
      streamPanel.hidden = true;
    }
    // Save snapshot for the current test.
    batteryLastTestSnapshot = {
      testId: p.test_id,
      name: p.test_name || "",
      model: p.model || "",
      prompt: currentTest ? (currentTest.prompt || "") : "",
      thinking: p.partial_thinking || "",
      response: p.partial_response || "",
    };

    // Update model cards.
    const container = $("battery-progress-models");
    if (container && p.model) {
      const currentModelIndex = modelIDs.indexOf(p.model);
      container.querySelectorAll(".battery-progress-model").forEach((el) => {
        const modelName = el.dataset.model;
        const statusEl = el.querySelector(".battery-progress-status");
        if (modelName === p.model) {
          el.classList.add("running");
          el.classList.remove("done");
          if (statusEl) statusEl.textContent = t(p.is_thinking ? "battery.status_thinking" : "battery.status_running");
        } else {
          el.classList.remove("running");
          const thisModelIndex = modelIDs.indexOf(modelName);
          if (thisModelIndex !== -1 && currentModelIndex !== -1 && thisModelIndex < currentModelIndex) {
            el.classList.add("done");
            if (statusEl) statusEl.textContent = t("battery.status_done");
          }
        }
      });
    }
    if (p.done) {
      // Archive final snapshot before finishing.
      if (batteryLastTestSnapshot) {
        batteryCompletedTests.push(batteryLastTestSnapshot);
        renderBatteryCompletedTests();
      }
      // Let the user read the last response for a moment.
      await new Promise((r) => setTimeout(r, 1500));
      // Fetch full run and show results.
      try {
        const run = await api("/api/runner/runs/" + encodeURIComponent(runID));
        currentBatteryRun = run;
        hideAllMainViews();
        currentView = "battery-results";
        $("battery-results-view").hidden = false;
        history.pushState(null, "", "/tests/battery/results/" + run.id);
        renderBatteryResults(run);
      } catch (err) {
        toast(t("toast.error", { msg: err.message }), "error");
        showTestsView();
      }
      return;
    }
    batteryPollTimer = setTimeout(() => pollBatteryProgress(runID, modelIDs), 2000);
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
    showTestsView();
  }
}

async function confirmBatteryRun() {
  if (batterySelectedModels.size === 0) {
    toast(t("battery.select_models"), "warn");
    return;
  }
  closeBatteryModal();
  const modelIDs = Array.from(batterySelectedModels);
  try {
    const data = await api("/api/runner/battery", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ group_id: selectedGroupId, model_ids: modelIDs }),
    });
    const runID = data.run_id;
    if (!runID) {
      toast(t("toast.error", { msg: "No run_id returned" }), "error");
      return;
    }
    showBatteryProgressView(modelIDs, runID);
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
    showTestsView();
  }
}

function showBatteryResultsView(runId) {
  hideAllMainViews();
  currentView = "battery-results";
  $("battery-results-view").hidden = false;
  void (async () => {
    try {
      if (tests.length === 0) {
        try {
          const data = await api("/api/tests");
          testsGroups = data.groups || [];
          tests = data.tests || [];
        } catch {
          // ignore; renderBatteryResults will treat all tests as non-human-review
        }
      }
      if (currentBatteryRun && currentBatteryRun.id === runId) {
        renderBatteryResults(currentBatteryRun);
      } else {
        const run = await api("/api/runner/runs/" + encodeURIComponent(runId));
        currentBatteryRun = run;
        renderBatteryResults(run);
      }
    } catch (err) {
      toast(t("toast.error", { msg: err.message }), "error");
      showTestsView();
    }
  })();
}

function showBatteryHistoryView() {
  hideAllMainViews();
  currentView = "battery-history";
  $("battery-history-view").hidden = false;
  if (window.location.pathname !== "/tests/battery/history") {
    history.pushState(null, "", "/tests/battery/history");
  }
  void renderBatteryHistory();
}

function renderBatteryResults(run) {
  if (!run) return;
  const title = $("battery-results-title");
  if (title) title.textContent = t("battery.results") + " — " + escapeHtml(run.group_name);

  const body = $("battery-results-body");
  if (!body) return;

  // Build per-model stats.
  const modelStats = {};
  for (const m of run.models) {
    modelStats[m] = { pass: 0, fail: 0, human: 0, total: 0, timeSum: 0, reasoning: 0 };
  }
  for (const r of run.results) {
    const s = modelStats[r.model];
    if (!s) continue;
    s.total++;
    s.timeSum += r.response_time_ms;
    if (r.reasoning_used) s.reasoning++;
    if (r.passed === true) s.pass++;
    else if (r.passed === false) s.fail++;
    else s.human++;
  }

  // Summary cards.
  let summaryHtml = `<div class="battery-summary">`;
  for (const m of run.models) {
    const s = modelStats[m];
    const avg = s.total > 0 ? Math.round(s.timeSum / s.total) : 0;
    summaryHtml += `
      <div class="battery-summary-card">
        <h4>${escapeHtml(m)}</h4>
        <div class="big">${s.pass} / ${s.total}</div>
        <div class="sub">${t("battery.response_time")}: ${avg}ms · ${t("battery.reasoning_used")}: ${s.reasoning}</div>
      </div>
    `;
  }
  summaryHtml += `</div>`;

  // Table.
  let rowsHtml = "";
  // Group results by test_id.
  const byTest = {};
  for (const r of run.results) {
    if (!byTest[r.test_id]) byTest[r.test_id] = [];
    byTest[r.test_id].push(r);
  }
  const testIds = Object.keys(byTest);

  for (const tid of testIds) {
    const results = byTest[tid];
    const test = tests.find((t) => t.id === tid);
    const isHumanReview = test?.evaluation_type === "human_review";
    const testName = results[0]?.test_name || tid;
    const promptBtn = isHumanReview
      ? `<div class="battery-prompt-link-wrap"><button type="button" class="battery-prompt-link" data-test-id="${escapeHtml(tid)}">prompt</button></div>`
      : "";
    const humanReviewLabel = isHumanReview
      ? `<div class="battery-human-review-label">${t("battery.human_review")}</div>`
      : "";

    for (let i = 0; i < results.length; i++) {
      const r = results[i];
      let resultCell = "";
      if (isHumanReview) {
        resultCell = `
          <div class="battery-pass-fail" data-test-id="${escapeHtml(r.test_id)}" data-model="${escapeHtml(r.model)}">
            <button type="button" data-passed="true" class="${r.passed === true ? "active" : ""}">${t("battery.pass")}</button>
            <button type="button" data-passed="false" class="${r.passed === false ? "active" : ""}">${t("battery.fail")}</button>
          </div>
        `;
      } else {
        const hasRealResponse = (r.tokens_per_sec || 0) > 0 && (r.model_response || "").trim().length > 0;
        if (r.error) {
          resultCell = `<span class="badge badge-na" title="${escapeHtml(r.error)}">${t("battery.error")}</span>`;
        } else if (!hasRealResponse && r.passed === false) {
          resultCell = `<span class="badge badge-na" title="${escapeHtml(r.model_response || t("battery.no_response"))}">${t("battery.error")}</span>`;
        } else if (r.passed === true) {
          resultCell = `<span class="badge badge-pass">${t("battery.pass")}</span>`;
        } else if (r.passed === false) {
          resultCell = `<span class="badge badge-fail">${t("battery.fail")}</span>`;
        } else {
          resultCell = `<span class="badge badge-human">${t("battery.human_review")}</span>`;
        }
      }

      const reasoningIcon = r.reasoning_used ? "🧠" : "";
      const tps = r.tokens_per_sec ? `${r.tokens_per_sec.toFixed(1)} tok/s` : "";
      const resp = r.model_response || "";
      const respId = `br-${run.id}-${r.test_id}-${escapeHtml(r.model)}`;
      const respShort = escapeHtml(resp.slice(0, 200));
      const respRest = escapeHtml(resp.slice(200));

      rowsHtml += `
        <tr>
          ${i === 0 ? `<td class="cell-test" rowspan="${results.length}"><strong>${escapeHtml(testName)}</strong>${humanReviewLabel}${promptBtn}</td>` : ""}
          <td class="cell-model">${escapeHtml(r.model)}</td>
          <td>${resultCell}</td>
          <td class="cell-time">${fmtDuration(r.response_time_ms)} ${reasoningIcon}<br><span class="muted" style="font-size:11px">${escapeHtml(tps)}</span></td>
          <td class="cell-response">
            <span class="resp-short">${respShort}${resp.length > 200 ? `<button type="button" class="resp-toggle" data-target="${respId}">…</button>` : ""}</span>
            ${resp.length > 200 ? `<span class="resp-rest" id="${respId}" hidden>${respRest}</span>` : ""}
          </td>
        </tr>
      `;
    }
  }

  body.innerHTML = summaryHtml + `
    <div class="battery-table-wrap">
      <table class="battery-table">
        <thead>
          <tr>
            <th>${t("tests.name")}</th>
            <th>${t("chat.model")}</th>
            <th>${t("battery.results")}</th>
            <th>${t("battery.response_time")}</th>
            <th>${t("chat.response")}</th>
          </tr>
        </thead>
        <tbody>${rowsHtml}</tbody>
      </table>
    </div>
  `;

  body.querySelectorAll(".resp-toggle").forEach((btn) => {
    btn.addEventListener("click", () => {
      const target = document.getElementById(btn.dataset.target);
      if (!target) return;
      target.hidden = !target.hidden;
      btn.textContent = target.hidden ? "…" : "▲";
    });
  });

  body.querySelectorAll(".battery-pass-fail button").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const wrap = btn.closest(".battery-pass-fail");
      const testId = wrap.dataset.testId;
      const model = wrap.dataset.model;
      const passed = btn.dataset.passed === "true";
      try {
        await submitTestResult(run, testId, model, passed);
      } catch (err) {
        toast(t("toast.error", { msg: err.message }), "error");
      }
    });
  });

  body.querySelectorAll(".battery-prompt-link").forEach((btn) => {
    btn.addEventListener("click", () => {
      const testId = btn.dataset.testId;
      const firstResult = run.results.find((r) => r.test_id === testId);
      if (firstResult) {
        openHumanReviewModal(run, testId, firstResult.model);
      }
    });
  });
}

async function submitTestResult(run, testId, model, passed) {
  await api("/api/runner/runs/" + encodeURIComponent(run.id) + "/rate", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ test_id: testId, model, passed }),
  });
  const result = run.results.find((r) => r.test_id === testId && r.model === model);
  if (result) {
    result.passed = passed;
  }
  renderBatteryResults(run);
  toast(t("battery.review_saved"), "success");
}

function openHumanReviewModal(run, testId, model) {
  const test = tests.find((t) => t.id === testId);
  if (!test) return;

  const titleEl = $("human-review-modal-title");
  if (titleEl) titleEl.textContent = t("battery.prompt") + " — " + escapeHtml(test.name);

  // Prompt
  const promptEl = $("human-review-prompt");
  if (promptEl) promptEl.textContent = test.prompt || "";

  // System prompt
  const sysEl = $("human-review-system");
  if (sysEl) {
    sysEl.textContent = test.system_prompt || "";
    sysEl.parentElement.hidden = !test.system_prompt;
    if (sysEl.previousElementSibling) sysEl.previousElementSibling.hidden = !test.system_prompt;
  }

  // Attachments
  const attachEl = $("human-review-attachments");
  if (attachEl) {
    const attHtml = (test.attachments || []).map((att) => {
      if (att.kind === "image") {
        const src = `data:${att.mime || "image/jpeg"};base64,${att.data}`;
        return `<div class="hr-attach-item"><img src="${src}" alt="${escapeHtml(att.name || "")}" class="hr-attach-img" /></div>`;
      }
      if (att.kind === "audio") {
        const src = `data:${att.mime || "audio/webm"};base64,${att.data}`;
        return `<div class="hr-attach-item"><audio controls src="${src}" class="hr-attach-audio"></audio><span class="hr-attach-name">${escapeHtml(att.name || "")}</span></div>`;
      }
      return "";
    }).join("");
    attachEl.innerHTML = attHtml || `<div class="muted">${t("battery.no_attachments")}</div>`;
  }

  $("human-review-modal").hidden = false;
}

function closeHumanReviewModal() {
  $("human-review-modal").hidden = true;
}

async function renderBatteryHistory() {
  const body = $("battery-history-body");
  if (!body) return;
  body.innerHTML = `<div class="muted">${t("status.loading")}</div>`;
  try {
    const data = await api("/api/runner/runs");
    const runs = data.runs || [];
    if (runs.length === 0) {
      body.innerHTML = `<div class="battery-empty">${t("battery.no_history")}</div>`;
      return;
    }
    body.innerHTML = runs.map((run) => {
      const date = fmtDateTimeFull(run.timestamp);
      return `
        <div class="battery-history-item" data-run-id="${escapeHtml(run.id)}">
          <span class="battery-history-date">${escapeHtml(date)}</span>
          <span class="battery-history-group">${escapeHtml(run.group_name)}</span>
          <span class="battery-history-models">${escapeHtml((run.models || []).join(", "))}</span>
          <span class="battery-history-counts">${run.pass_count || 0} / ${run.total_count || 0}</span>
          <button type="button" class="ghost danger-text battery-history-delete" data-run-id="${escapeHtml(run.id)}">×</button>
        </div>
      `;
    }).join("");

    body.querySelectorAll(".battery-history-item").forEach((el) => {
      el.addEventListener("click", (e) => {
        if (e.target.closest("button")) return;
        const id = el.dataset.runId;
        history.pushState(null, "", "/tests/battery/results/" + id);
        showBatteryResultsView(id);
      });
    });

    body.querySelectorAll(".battery-history-delete").forEach((btn) => {
      btn.addEventListener("click", async (e) => {
        e.stopPropagation();
        const id = btn.dataset.runId;
        const ok = await askConfirm({
          title: t("action.delete"),
          text: t("tests.delete_text"),
          okText: t("action.delete"),
          okClass: "danger",
        });
        if (!ok.ok) return;
        try {
          await api("/api/runner/runs/" + encodeURIComponent(id), { method: "DELETE" });
          await renderBatteryHistory();
        } catch (err) {
          toast(t("toast.error", { msg: err.message }), "error");
        }
      });
    });
  } catch (err) {
    body.innerHTML = `<div class="muted">${escapeHtml(err.message)}</div>`;
  }
}

// ---------- per-test history modal ----------

function openTestHistoryModal(testId) {
  const test = tests.find((t) => t.id === testId);
  const titleEl = $("test-history-modal-title");
  if (titleEl && test) titleEl.textContent = t("tests.history_title") + " — " + escapeHtml(test.name);
  $("test-history-modal").hidden = false;
  void renderTestHistoryModal(testId);
}

function closeTestHistoryModal() {
  $("test-history-modal").hidden = true;
}

async function renderTestHistoryModal(testId) {
  const body = $("test-history-modal-body");
  if (!body) return;
  body.innerHTML = `<div class="muted">${t("status.loading")}</div>`;
  try {
    const data = await api("/api/runner/test-history/" + encodeURIComponent(testId));
    const history = data.history || [];
    if (history.length === 0) {
      body.innerHTML = `<div class="battery-empty">${t("tests.no_history")}</div>`;
      return;
    }
    let rows = "";
    for (const h of history) {
      const date = fmtDateTimeFull(h.timestamp);
      let badge = "";
      const hasRealResponse = (h.tokens_per_sec || 0) > 0 && (h.model_response || "").trim().length > 0;
      if (h.error) {
        badge = `<span class="badge badge-na" title="${escapeHtml(h.error)}">${t("battery.error")}</span>`;
      } else if (!hasRealResponse && h.passed === false) {
        badge = `<span class="badge badge-na" title="${escapeHtml(h.model_response || t("battery.no_response"))}">${t("battery.error")}</span>`;
      } else if (h.passed === true) {
        badge = `<span class="badge badge-pass">${t("battery.pass")}</span>`;
      } else if (h.passed === false) {
        badge = `<span class="badge badge-fail">${t("battery.fail")}</span>`;
      } else {
        badge = `<span class="badge badge-human">${t("battery.human_review")}</span>`;
      }
      const rating = h.human_rating ? ` · ${t("battery.human_review")}: ${escapeHtml(h.human_rating)}` : "";
      const reasoning = h.reasoning_used ? "🧠" : "";
      const tps = h.tokens_per_sec ? `${h.tokens_per_sec.toFixed(1)} tok/s` : "";
      const resp = h.model_response || "";
      const respId = `th-${testId}-${escapeHtml(h.model)}-${new Date(h.timestamp).getTime()}`;
      const respShort = escapeHtml(resp.slice(0, 200));
      const respRest = escapeHtml(resp.slice(200));
      const sys = h.sys_info || {};
      const sysParts = [];
      if (sys.os) sysParts.push(`${t("battery.sys_os")}: ${escapeHtml(sys.os)}`);
      if (sys.cpu_model) sysParts.push(`${t("battery.sys_cpu")}: ${escapeHtml(sys.cpu_model)}`);
      if (sys.gpu_model) sysParts.push(`${t("battery.sys_gpu")}: ${escapeHtml(sys.gpu_model)}`);
      if (sys.ram_gb) sysParts.push(`${t("battery.sys_ram")}: ${escapeHtml(sys.ram_gb)} GB`);
      if (sys.vram_gb) sysParts.push(`${t("battery.sys_vram")}: ${escapeHtml(sys.vram_gb)} GB`);
      const sysTooltip = sysParts.join(" | ");
      const sysLabel = sys.os ? escapeHtml(sys.os + (sys.ram_gb ? ` · ${sys.ram_gb}GB` : "")) : "—";
      rows += `
        <tr>
          <td class="cell-time">${escapeHtml(date)}</td>
          <td class="cell-model">${escapeHtml(h.model)}</td>
          <td>${badge}</td>
          <td class="cell-time">${fmtDuration(h.response_time_ms)} ${reasoning}<br><span class="muted" style="font-size:11px">${escapeHtml(tps)}</span></td>
          <td class="cell-response">
            <span class="resp-short">${respShort}${resp.length > 200 ? `<button type="button" class="resp-toggle" data-target="${respId}">…</button>` : ""}</span>
            ${resp.length > 200 ? `<span class="resp-rest" id="${respId}" hidden>${respRest}</span>` : ""}
          </td>
          <td class="cell-sys" title="${escapeHtml(sysTooltip)}">${sysLabel}</td>
          <td class="cell-time">${escapeHtml(h.group_name)}${rating}</td>
        </tr>
      `;
    }
    body.innerHTML = `
      <div class="battery-table-wrap">
        <table class="battery-table">
          <thead>
            <tr>
              <th>${t("battery.date")}</th>
              <th>${t("chat.model")}</th>
              <th>${t("battery.results")}</th>
              <th>${t("battery.response_time")}</th>
              <th>${t("chat.response")}</th>
              <th>${t("battery.sys_info")}</th>
              <th>${t("tests.group")}</th>
            </tr>
          </thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
    `;
    body.querySelectorAll(".resp-toggle").forEach((btn) => {
      btn.addEventListener("click", () => {
        const target = document.getElementById(btn.dataset.target);
        if (!target) return;
        target.hidden = !target.hidden;
        btn.textContent = target.hidden ? "…" : "▲";
      });
    });
  } catch (err) {
    body.innerHTML = `<div class="muted">${escapeHtml(err.message)}</div>`;
  }
}

// ---------- group history modal ----------

function openGroupHistoryModal(groupId) {
  const g = testsGroups.find((x) => x.id === groupId);
  const titleEl = $("group-history-modal-title");
  if (titleEl && g) titleEl.textContent = t("battery.group_history") + " — " + escapeHtml(g.name);
  $("group-history-modal").hidden = false;
  void renderGroupHistoryModal(groupId);
}

function closeGroupHistoryModal() {
  $("group-history-modal").hidden = true;
}

function fmtTestTooltip(label, tests) {
  if (!tests || tests.length === 0) return "";
  const list = tests.map((n) => "• " + escapeHtml(n)).join("\n");
  return escapeHtml(label) + ":\n" + list;
}

async function renderGroupHistoryModal(groupId) {
  const body = $("group-history-modal-body");
  if (!body) return;
  body.innerHTML = `<div class="muted">${t("status.loading")}</div>`;
  try {
    const data = await api("/api/runner/group-history/" + encodeURIComponent(groupId));
    const summary = data.summary || [];
    if (summary.length === 0) {
      body.innerHTML = `<div class="battery-empty">${t("battery.no_history")}</div>`;
      return;
    }
    let rows = "";
    for (const s of summary) {
      const passRate = s.total_tests > 0 ? Math.round((s.passed / s.total_tests) * 100) : 0;
      const failRate = s.total_tests > 0 ? Math.round((s.failed / s.total_tests) * 100) : 0;
      const tps = s.avg_tokens_per_sec ? `${s.avg_tokens_per_sec.toFixed(1)} tok/s` : "";
      const date = s.last_run_at ? fmtDateTimeFull(s.last_run_at) : "—";
      const sys = s.sys_info || {};
      const sysParts = [];
      if (sys.os) sysParts.push(`${t("battery.sys_os")}: ${escapeHtml(sys.os)}`);
      if (sys.cpu_model) sysParts.push(`${t("battery.sys_cpu")}: ${escapeHtml(sys.cpu_model)}`);
      if (sys.gpu_model) sysParts.push(`${t("battery.sys_gpu")}: ${escapeHtml(sys.gpu_model)}`);
      if (sys.ram_gb) sysParts.push(`${t("battery.sys_ram")}: ${escapeHtml(sys.ram_gb)} GB`);
      if (sys.vram_gb) sysParts.push(`${t("battery.sys_vram")}: ${escapeHtml(sys.vram_gb)} GB`);
      const sysTooltip = sysParts.join(" | ");
      const passTooltip = fmtTestTooltip(t("battery.legend_pass"), s.passed_tests);
      const failTooltip = fmtTestTooltip(t("battery.legend_fail"), s.failed_tests);
      const humanTooltip = fmtTestTooltip(t("battery.legend_human"), s.human_review_tests);
      const errorTooltip = fmtTestTooltip(t("battery.legend_error"), s.error_tests);
      rows += `
        <tr>
          <td class="cell-model">${escapeHtml(s.model)}</td>
          <td class="cell-time">${s.total_tests}</td>
          <td>
            <span class="badge badge-pass" title="${passTooltip}">${s.passed}</span>
            <span class="badge badge-fail" title="${failTooltip}">${s.failed}</span>
            ${s.human_review > 0 ? `<span class="badge badge-human" title="${humanTooltip}">${s.human_review}</span>` : ""}
            ${s.errors > 0 ? `<span class="badge badge-na" title="${errorTooltip || t("battery.error_count")}">${s.errors}</span>` : ""}
            <span class="muted" style="font-size:11px; margin-left:4px">${passRate}%</span>
          </td>
          <td class="cell-time">${fmtDuration(s.avg_response_ms)}<br><span class="muted" style="font-size:11px">${escapeHtml(tps)}</span></td>
          <td class="cell-time">${escapeHtml(date)}</td>
          <td class="cell-sys" title="${escapeHtml(sysTooltip)}">${sys.os ? escapeHtml(sys.os + (sys.ram_gb ? ` · ${sys.ram_gb}GB` : "")) : "—"}</td>
        </tr>
      `;
    }
    const legend = `
      <div style="display:flex;gap:12px;flex-wrap:wrap;margin-bottom:12px;font-size:12px;align-items:center;">
        <span style="color:var(--muted);font-weight:600;">${t("battery.legend_title")}:</span>
        <span class="badge badge-pass">${t("battery.legend_pass")}</span>
        <span class="badge badge-fail">${t("battery.legend_fail")}</span>
        <span class="badge badge-human">${t("battery.legend_human")}</span>
        <span class="badge badge-na">${t("battery.legend_error")}</span>
      </div>
    `;
    body.innerHTML = legend + `
      <div class="battery-table-wrap">
        <table class="battery-table">
          <thead>
            <tr>
              <th>${t("chat.model")}</th>
              <th>${t("battery.total_tests")}</th>
              <th>${t("battery.results")}</th>
              <th>${t("battery.avg_response")}</th>
              <th>${t("battery.last_run")}</th>
              <th>${t("battery.sys_info")}</th>
            </tr>
          </thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
    `;
  } catch (err) {
    body.innerHTML = `<div class="muted">${escapeHtml(err.message)}</div>`;
  }
}

$("test-history-modal")?.addEventListener("click", (e) => {
  if (e.target === $("test-history-modal")) closeTestHistoryModal();
});
$("test-history-modal-close")?.addEventListener("click", closeTestHistoryModal);
$("test-history-modal-done")?.addEventListener("click", closeTestHistoryModal);

$("group-history-modal")?.addEventListener("click", (e) => {
  if (e.target === $("group-history-modal")) closeGroupHistoryModal();
});
$("group-history-modal-close")?.addEventListener("click", closeGroupHistoryModal);
$("group-history-modal-done")?.addEventListener("click", closeGroupHistoryModal);

$("human-review-modal")?.addEventListener("click", (e) => {
  if (e.target === $("human-review-modal")) closeHumanReviewModal();
});
$("human-review-modal-close")?.addEventListener("click", closeHumanReviewModal);

$("tests-group-history-btn")?.addEventListener("click", () => {
  if (selectedGroupId) openGroupHistoryModal(selectedGroupId);
});
$("tests-run-battery-btn")?.addEventListener("click", () => {
  openBatteryModal();
});
$("battery-modal")?.addEventListener("click", (e) => {
  if (e.target === $("battery-modal")) closeBatteryModal();
});
$("battery-modal-close")?.addEventListener("click", closeBatteryModal);
$("battery-modal-cancel")?.addEventListener("click", closeBatteryModal);
$("battery-modal-confirm")?.addEventListener("click", () => {
  void confirmBatteryRun();
});
$("battery-results-back")?.addEventListener("click", () => {
  showTestsView();
});
$("battery-results-history")?.addEventListener("click", () => {
  showBatteryHistoryView();
});
$("battery-history-back")?.addEventListener("click", () => {
  showTestsView();
});

$("agent-session-back")?.addEventListener("click", () => {
  showTestsView();
});
$("agent-session-reset")?.addEventListener("click", () => {
  void resetAgentSession();
});
$("agent-session-delete")?.addEventListener("click", () => {
  void deleteAgentSession();
});
$("agent-feedback-send")?.addEventListener("click", () => {
  void submitAgentFeedback();
});

window.I18n.setLang("en"); // applied immediately; refreshStatus may overwrite.
refreshStatus();
refreshModels();
connectJobsStream();
bindChatEvents();
updateStreamBar();
syncChatModelOptions();
updateChatCapabilityUI();
updateChatContextMeter();
updateChatSendEnabled();
restoreChatOptionsFromSession();
setInterval(refreshStatus, STATUS_REFRESH_MS);
setInterval(refreshLoadedState, 1000);
setInterval(() => {
  const modal = $("downloads-modal");
  if (modal && !modal.hidden) renderDownloads();
}, 60_000);
