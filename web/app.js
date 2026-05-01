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
  if (diffDays < 7) return `hace ${diffDays}d`;
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

function detectSpeechLang(text) {
  const s = String(text || "").toLowerCase();
  if (!s.trim()) return "es-ES";
  const esHints = /[áéíóúñ¿¡]|\b(el|la|los|las|de|que|para|como|está|estoy|gracias|hola|modelo)\b/.test(s);
  return esHints ? "es-ES" : "en-US";
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
let currentView = "models";
let chatMessages = [];
let chatAttachments = [];
let chatStreamLock = false;
let chatRenderRaf = null;
let chatAbortController = null;
let chatThinkTicker = null;
let chatLastUsedTokens = 0;
let chatDndDepth = 0;
let chatPendingQueue = [];
let chatMediaRecorder = null;
let chatRecorderStream = null;
let chatRecorderChunks = [];
let speakingMsgId = "";
const CHAT_OPTION_FALLBACKS = { temperature: 0.7, top_k: 40, top_p: 0.9 };
const STATUS_REFRESH_MS = 5000;
const chatModelDefaultsCache = new Map();
let chatDefaultsReqSeq = 0;
let lastChatDefaultsModel = "";
/** /api/status succeeded since last call */
let managerApiOk = false;
/** Ollama host reachable (from /api/status) */
let ollamaHostOk = false;

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
    updateDiskWidget(s);
    updateChatSendEnabled();
  } catch (e) {
    managerApiOk = false;
    ollamaHostOk = false;
    $("status-pill").textContent = t("status.unreachable");
    $("status-pill").className = "pill pill-bad";
    updateDiskWidget(null);
    updateChatSendEnabled();
  }
}

function updateDiskWidget(status) {
  const wrap = $("disk-widget");
  const fill = $("disk-widget-fill");
  const text = $("disk-widget-text");
  if (!wrap || !fill || !text) return;

  const total = Number(status?.disk_total_bytes) || 0;
  const free = Number(status?.disk_free_bytes) || 0;
  if (total <= 0) {
    wrap.hidden = true;
    return;
  }

  const clampedFree = Math.max(0, Math.min(free, total));
  const used = total - clampedFree;
  const usedPct = Math.max(0, Math.min(100, (used / total) * 100));
  const freePct = Math.max(0, Math.min(100, (clampedFree / total) * 100));

  fill.style.width = `${usedPct.toFixed(1)}%`;
  fill.classList.toggle("warn", freePct <= 25 && freePct > 10);
  fill.classList.toggle("bad", freePct <= 10);

  text.textContent = t("status.disk_free_short", {
    free: fmtBytes(clampedFree),
    total: fmtBytes(total),
  });
  wrap.title = t("status.disk_free_title", {
    free: fmtBytes(clampedFree),
    total: fmtBytes(total),
    pct: Math.round(freePct),
  });
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
    renderTable();
    syncChatModelOptions();
    updateChatCapabilityUI();
    updateChatContextMeter();
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
  if (!models.length) {
    tbody.innerHTML = `<tr class="empty"><td colspan="9">${escapeHtml(t("state.empty_models"))}</td></tr>`;
    return;
  }
  const sorted = applySort(models);
  const dotLoadedTxt = t("detail.dot_loaded");
  const dotNotLoadedTxt = t("detail.dot_not_loaded");
  const deleteTitle = t("detail.delete_title");
  const infoTitle = t("detail.info_btn");
  const renderCapabilities = (caps) => (caps || [])
    .map((c) => `<span class="pill">${escapeHtml(c)}</span>`)
    .join("");
  tbody.innerHTML = sorted.map((m) => {
    const capsHtml = renderCapabilities(m.capabilities);
    return `
    <tr class="row${m.name === activeName ? " active" : ""}" data-name="${escapeHtml(m.name)}">
      <td class="col-state"><span class="state-dot${m.loaded ? " loaded" : ""}" title="${m.loaded ? dotLoadedTxt : dotNotLoadedTxt}"></span></td>
      <td class="cell-name">
        <div class="model-name-wrap">
          <div class="model-name-block">
            <div class="model-name">${escapeHtml(m.name)}</div>
            ${capsHtml ? `<div class="cap-list model-cap-list">${capsHtml}</div>` : ""}
          </div>
          <button type="button" class="btn-icon info-btn" data-name="${escapeHtml(m.name)}" title="${escapeHtml(infoTitle)}" aria-label="${escapeHtml(infoTitle)}"><span class="info-glyph" aria-hidden="true">i</span></button>
        </div>
      </td>
      <td>${escapeHtml(m.family || "—")}</td>
      <td class="cell-params">${escapeHtml(m.parameter_size || "—")}</td>
      <td class="cell-quant">${escapeHtml(m.quantization || "—")}</td>
      <td class="cell-ctx">${fmtCtx(m.context_length)}</td>
      <td class="cell-size">${fmtBytes(m.size)}</td>
      <td class="cell-modified">${fmtDate(m.modified_at)}</td>
      <td class="col-actions">
        <button class="btn-icon delete-btn" title="${escapeHtml(deleteTitle)}" data-name="${escapeHtml(m.name)}">×</button>
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
async function openDetail(name) {
  activeName = name;
  const panel = $("detail-panel");
  panel.hidden = false;
  $("detail-name").textContent = name;
  $("detail-body").innerHTML = `<div class="muted">${escapeHtml(t("state.loading"))}</div>`;
  document.querySelectorAll("tbody tr.row").forEach((tr) => {
    tr.classList.toggle("active", tr.dataset.name === name);
  });
  try {
    const d = await api("/api/models/" + encodeURIComponent(name));
    renderDetail(d);
  } catch (e) {
    $("detail-body").innerHTML = `<div class="muted">${escapeHtml(t("state.error_prefix") + e.message)}</div>`;
  }
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

  $("detail-body").innerHTML = `<div class="detail-grid">${grid}</div>${capsBlock}${repairBlock}${paramsBlock}${tmplBlock}`;
  bindRepairEntry(d);
}

$("detail-close").addEventListener("click", () => {
  $("detail-panel").hidden = true;
  activeName = null;
  document.querySelectorAll("tbody tr.row.active").forEach((tr) => tr.classList.remove("active"));
});

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
    const hint = detected.has(cap) ? `<span>${escapeHtml(t("repair.detected"))}</span>` : "";
    return `<label class="repair-check">
      <input type="checkbox" name="repair-cap" value="${escapeHtml(cap)}">
      <span>${escapeHtml(label)}</span>
      ${hint}
    </label>`;
  }).join("");
  const target = fixedModelName(d.name);
  const template = repairDefaultTemplate(d);
  return `<div class="repair-card">
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
          <option value="generic"${template === "generic" ? " selected" : ""}>ChatML genérico</option>
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
    if (!window.confirm(msg)) return;
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
  const capabilities = Array.from(document.querySelectorAll("input[name='repair-cap']:checked")).map((el) => el.value);
  const modelfile = $("repair-preview")?.value || "";
  return {
    model: d.name,
    capabilities,
    template_preset: $("repair-template")?.value || "generic",
    context_preset: $("repair-context")?.value || "safe",
    temperature_preset: $("repair-temperature")?.value || "keep",
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
  const sorted = applySort(models);
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
  const caps = modelCaps(model);
  const canVision = caps.has("vision");
  const canAudio = caps.has("audio");
  const canThinkToggle = caps.has("thinking");
  const canTools = caps.has("tools");
  $("chat-image-btn").hidden = !canVision;
  $("chat-audio-btn").hidden = !canAudio;
  $("chat-record-btn").hidden = !canAudio;
  $("chat-think-wrap").hidden = !canThinkToggle;
  $("chat-web-tools-wrap").hidden = !canTools;
  if (!canAudio && chatMediaRecorder) {
    stopAudioRecording(true);
  }
  const webW = $("chat-web-tools");
  if (webW) webW.checked = !!canTools;

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
  const selected = modelByName($("chat-model").value);
  const maxCtx = Number(selected?.context_length) || 0;
  if (!maxCtx) {
    $("chat-context-meter").textContent = "—";
    return;
  }
  const used = Math.max(0, Number(chatLastUsedTokens) || 0);
  const pct = Math.min(999, Math.round((used / maxCtx) * 100));
  $("chat-context-meter").textContent = `${fmtCtx(used)} / ${fmtCtx(maxCtx)} (${pct}%)`;
}

function showModelsView() {
  $("chat-view")?.classList.remove("chat-options-open");
  stopSpeechPlayback();
  currentView = "models";
  $("models-view").hidden = false;
  $("chat-view").hidden = true;
  $("chat-btn")?.classList.remove("active");
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
  currentView = "chat";
  $("chat-view")?.classList.remove("chat-options-open");
  $("models-view").hidden = true;
  $("chat-view").hidden = false;
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

function showChatViewWithModel(name) {
  showChatView();
  $("chat-view")?.classList.remove("chat-options-open");
  if (!name) return;
  const sel = $("chat-model");
  const exists = Array.from(sel?.options || []).some((o) => o.value === name);
  if (!exists) return;
  sel.value = name;
  updateChatCapabilityUI();
  updateChatContextMeter();
  void applyChatDefaultsForModel(name, true);
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
  });
}

function flushChatRender() {
  if (chatRenderRaf != null) {
    cancelAnimationFrame(chatRenderRaf);
    chatRenderRaf = null;
  }
  renderChatMessages();
  scrollChatToBottom();
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
  return html;
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
    if (m.role === "assistant" && m.streaming) {
      meta.push(formatMetaElapsed(Math.max(0, Number(m.elapsedMs) || 0)));
      if (m.tps != null && m.tps > 0 && Number.isFinite(m.tps)) {
        meta.push(t("chat.meta_tps", { rate: m.tps.toFixed(2) }));
      }
      meta.push(t("chat.streaming"));
    } else if (m.role === "assistant" && !m.streaming) {
      if (m.elapsedMs > 0) meta.push(formatMetaElapsed(m.elapsedMs));
      if (m.tokens > 0) meta.push(t("chat.meta_tokens", { n: m.tokens }));
      if (m.tps != null && m.tps > 0 && Number.isFinite(m.tps)) {
        meta.push(t("chat.meta_tps", { rate: m.tps.toFixed(2) }));
      }
    }
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

    const bodyHTML = m.role === "assistant"
      ? renderMarkdownSafe(m.content || "")
      : `<p>${escapeHtml(m.content || "")}</p>`;
    const roleLabel = m.role === "user" ? t("chat.role_user") : t("chat.role_assistant");
    const modelLabel = m.role === "assistant" && m.model
      ? `<span class="chat-model-used mono">${escapeHtml(m.model)}</span>`
      : "";
    const hideActionsWhileStreaming = m.role === "assistant" && m.streaming;
    const ttsPlaying = m.id === speakingMsgId;
    const ttsLabel = ttsPlaying ? t("chat.tts_stop") : t("chat.tts_play");
    const ttsBtn = hideActionsWhileStreaming ? "" : `<button type="button" class="btn-icon chat-tts-btn${ttsPlaying ? " active" : ""}" data-msg-id="${escapeHtml(m.id)}" title="${escapeHtml(ttsLabel)}" aria-label="${escapeHtml(ttsLabel)}">
<svg class="chat-tts-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
<path d="M11 5L6 9H3v6h3l5 4V5z"/>
<path d="M15.5 9.5a4 4 0 0 1 0 5"/>
<path d="M18.5 7a8 8 0 0 1 0 10"/>
</svg></button>`;
    const copyLabel = m.role === "user" ? t("chat.copy_user") : t("chat.copy_assistant");
    const copyBtn = hideActionsWhileStreaming ? "" : `<button type="button" class="btn-icon chat-copy-btn" data-msg-id="${escapeHtml(m.id)}" title="${escapeHtml(copyLabel)}" aria-label="${escapeHtml(copyLabel)}">
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
    const footBlock = footActions
      ? `<div class="chat-msg-foot">
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
    scheduleRenderChatMessages();
  }, 250);
}

function toBase64(file) {
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
  const canVision = caps.has("vision");
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

function recordingMimeType() {
  if (typeof MediaRecorder === "undefined" || !MediaRecorder.isTypeSupported) return "";
  const choices = [
    "audio/webm;codecs=opus",
    "audio/webm",
    "audio/ogg;codecs=opus",
    "audio/mp4",
  ];
  for (const m of choices) {
    if (MediaRecorder.isTypeSupported(m)) return m;
  }
  return "";
}

function recordingExt(mime) {
  const m = String(mime || "").toLowerCase();
  if (m.includes("ogg")) return "ogg";
  if (m.includes("mp4")) return "m4a";
  return "webm";
}

function setRecordButtonState(isRecording) {
  const btn = $("chat-record-btn");
  if (!btn) return;
  btn.classList.toggle("is-recording", !!isRecording);
  const key = isRecording ? "chat.record_audio_stop" : "chat.record_audio_start";
  const label = t(key);
  btn.title = label;
  btn.setAttribute("aria-label", label);
}

function releaseAudioRecorder() {
  if (chatRecorderStream) {
    for (const tr of chatRecorderStream.getTracks()) {
      try { tr.stop(); } catch {}
    }
  }
  chatRecorderStream = null;
  chatMediaRecorder = null;
  chatRecorderChunks = [];
  setRecordButtonState(false);
}

async function startAudioRecording() {
  if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") {
    toast(t("chat.record_audio_unsupported"), "error");
    return;
  }
  if (chatMediaRecorder) return;
  const caps = modelCaps($("chat-model").value);
  if (!caps.has("audio")) {
    toast(t("chat.attach_not_supported"), "error");
    return;
  }
  try {
    chatRecorderStream = await navigator.mediaDevices.getUserMedia({ audio: true });
    const mime = recordingMimeType();
    chatMediaRecorder = mime
      ? new MediaRecorder(chatRecorderStream, { mimeType: mime })
      : new MediaRecorder(chatRecorderStream);
    chatRecorderChunks = [];
    chatMediaRecorder.ondataavailable = (ev) => {
      if (ev.data && ev.data.size > 0) chatRecorderChunks.push(ev.data);
    };
    chatMediaRecorder.onerror = () => {
      toast(t("chat.record_audio_error"), "error");
      releaseAudioRecorder();
    };
    chatMediaRecorder.onstop = async () => {
      const mimeType = chatMediaRecorder?.mimeType || mime || "audio/webm";
      const chunks = chatRecorderChunks.slice();
      releaseAudioRecorder();
      if (!chunks.length) return;
      const blob = new Blob(chunks, { type: mimeType });
      if (!blob.size) return;
      const ext = recordingExt(mimeType);
      const file = new File([blob], `recording-${Date.now()}.${ext}`, { type: mimeType });
      await addFiles([file]);
    };
    chatMediaRecorder.start();
    setRecordButtonState(true);
  } catch (err) {
    toast(t("toast.error", { msg: err?.message || t("chat.record_audio_error") }), "error");
    releaseAudioRecorder();
  }
}

function stopAudioRecording(silent = false) {
  if (!chatMediaRecorder) return;
  const rec = chatMediaRecorder;
  if (silent) {
    rec.ondataavailable = null;
    rec.onstop = null;
    rec.onerror = null;
    releaseAudioRecorder();
    return;
  }
  try {
    if (rec.state !== "inactive") rec.stop();
  } catch {
    releaseAudioRecorder();
  }
}

function buildOutboundMessages() {
  const out = [];
  const systemPrompt = $("chat-system").value.trim();
  if (systemPrompt) out.push({ role: "system", content: systemPrompt });
  for (const m of chatMessages) {
    if (m.role !== "user" && m.role !== "assistant") continue;
    if (m.role === "assistant" && (m.streaming || !String(m.content || "").trim())) continue;
    const payload = { role: m.role, content: m.content || "" };
    if (m.role === "user" && m.attachments?.length) {
      const imgs = m.attachments.filter((a) => a.kind === "image").map((a) => a.data);
      const auds = m.attachments.filter((a) => a.kind === "audio").map((a) => a.data);
      const txts = m.attachments.filter((a) => a.kind === "text" && String(a.text || "").trim());
      if (imgs.length) payload.images = imgs;
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
  const lang = detectSpeechLang(text);
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
  if (bar) bar.hidden = !chatStreamLock;
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
  const canThinkToggle = caps.has("thinking");
  const noThink = canThinkToggle ? $("chat-no-think").checked : false;
  const canTools = caps.has("tools");
  const webToolsOn = canTools && $("chat-web-tools").checked;
  const payload = {
    model: modelName,
    think: canThinkToggle ? !noThink : undefined,
    options: {
      temperature: readOptionNumber("chat-temperature", CHAT_OPTION_FALLBACKS.temperature),
      top_k: Math.round(readOptionNumber("chat-top-k", CHAT_OPTION_FALLBACKS.top_k)),
      top_p: readOptionNumber("chat-top-p", CHAT_OPTION_FALLBACKS.top_p),
    },
    messages: buildOutboundMessages(),
  };
  if (webToolsOn) payload.web_tools = true;

  chatAbortController = new AbortController();
  chatStreamLock = true;
  updateStreamBar();
  const turnStartedAt = Date.now();
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
          assistantMsg.completionTokens = chunkEval;
        } else if (contentDelta) {
          // Fallback estimate while streaming when provider omits eval_count in chunks.
          assistantMsg.completionTokens += Math.max(1, Math.round(contentDelta.length / 4));
        }
        if (assistantMsg.elapsedMs > 0 && assistantMsg.completionTokens > 0) {
          assistantMsg.tps = assistantMsg.completionTokens / (assistantMsg.elapsedMs / 1000);
        }
        if (parts.inThink && !assistantMsg.thinkStartedAt) {
          assistantMsg.thinkStartedAt = Date.now();
          startThinkTicker(assistantMsg);
        }
        if (!parts.inThink && assistantMsg.thinkStartedAt) {
          assistantMsg.thinkMs = Date.now() - assistantMsg.thinkStartedAt;
          stopThinkTicker();
        }
        assistantMsg._accRaw = assistantRaw;
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
        chatLastUsedTokens = assistantMsg.promptTokens || assistantMsg.tokens;
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
      if (!assistantMsg.content) {
        assistantMsg.content = t("chat.error_reply", { msg: e.message || "failed" });
      }
      toast(t("toast.error", { msg: e.message || "chat failed" }), "error");
    }
  } finally {
    chatAbortController = null;
    stopThinkTicker();
    chatStreamLock = false;
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
  window.addEventListener("pagehide", stopSpeechPlayback);
  window.addEventListener("beforeunload", stopSpeechPlayback);
  window.addEventListener("popstate", stopSpeechPlayback);
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
  $("chat-model").addEventListener("change", () => {
    updateChatCapabilityUI();
    updateChatContextMeter();
    void applyChatDefaultsForModel($("chat-model").value, true);
  });
  $("chat-send-btn").addEventListener("click", sendChatMessage);
  ($("chat-scroll-shell") || $("chat-messages")).addEventListener("click", async (e) => {
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
  $("chat-input").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      if ($("chat-send-btn")?.disabled) return;
      sendChatMessage();
    }
  });
  $("chat-input").addEventListener("paste", async (e) => {
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
  $("chat-image-btn").addEventListener("click", () => $("chat-image-input").click());
  $("chat-audio-btn").addEventListener("click", () => $("chat-audio-input").click());
  $("chat-text-btn").addEventListener("click", () => $("chat-text-input").click());
  $("chat-record-btn").addEventListener("click", async () => {
    if (chatMediaRecorder) {
      stopAudioRecording();
    } else {
      await startAudioRecording();
    }
  });
  $("chat-image-input").addEventListener("change", async () => {
    await addFiles(Array.from($("chat-image-input").files || []));
    $("chat-image-input").value = "";
  });
  $("chat-audio-input").addEventListener("change", async () => {
    await addFiles(Array.from($("chat-audio-input").files || []));
    $("chat-audio-input").value = "";
  });
  $("chat-text-input").addEventListener("change", async () => {
    await addFiles(Array.from($("chat-text-input").files || []));
    $("chat-text-input").value = "";
  });

  const dropHost = $("chat-view");
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
}

// ---------- delete ----------
let pendingDelete = null;
function confirmDelete(name) {
  pendingDelete = name;
  $("confirm-title").textContent = t("detail.delete_title");
  // Substitute {name} ourselves so we can wrap it in a mono span.
  const text = t("confirm.delete_text", { name: "{__NAME__}" });
  const safe = escapeHtml(text).replace("{__NAME__}", `<span class="mono">${escapeHtml(name)}</span>`);
  $("confirm-text").innerHTML = safe;
  $("confirm-modal").hidden = false;
}
$("confirm-cancel").addEventListener("click", () => { $("confirm-modal").hidden = true; pendingDelete = null; });
$("confirm-ok").addEventListener("click", async () => {
  const name = pendingDelete;
  $("confirm-modal").hidden = true;
  pendingDelete = null;
  if (!name) return;
  try {
    await api("/api/models/" + encodeURIComponent(name), { method: "DELETE" });
    toast(t("toast.deleted", { name }), "success");
    if (activeName === name) { $("detail-panel").hidden = true; activeName = null; }
    refreshModels();
  } catch (e) {
    toast(t("toast.delete_error", { msg: e.message }), "error");
  }
});

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
      onJobsChanged();
    } catch {}
  });

  jobsStream.addEventListener("update", (ev) => {
    try {
      const data = JSON.parse(ev.data);
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
}

function jobsByStatus() {
  const buckets = { active: [], queued: [], finished: [] };
  for (const j of jobs.values()) {
    if (j.status === "running") buckets.active.push(j);
    else if (j.status === "queued") buckets.queued.push(j);
    else buckets.finished.push(j);
  }
  // Active and queued keep their natural (creation) order; finished shows
  // most recent first.
  const byCreated = (a, b) => new Date(a.created_at) - new Date(b.created_at);
  buckets.active.sort(byCreated);
  buckets.queued.sort(byCreated);
  buckets.finished.sort((a, b) => new Date(b.finished_at || b.created_at) - new Date(a.finished_at || a.created_at));
  return buckets;
}

function updateDownloadsBadge() {
  let activeCount = 0;
  for (const j of jobs.values()) {
    if (j.status === "running" || j.status === "queued") activeCount++;
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
  $("dl-count-finished").textContent = String(buckets.finished.length);
  $("dl-list-active").innerHTML = buckets.active.map(jobCardHTML).join("") || emptyRow();
  $("dl-list-queued").innerHTML = buckets.queued.map(jobCardHTML).join("") || emptyRow();
  $("dl-list-finished").innerHTML = buckets.finished.map(jobCardHTML).join("") || emptyRow();
  const hasAny = jobs.size > 0;
  $("dl-empty").hidden = hasAny;
  $("dl-total-badge").hidden = !hasAny;
  if (hasAny) {
    $("dl-total-badge").textContent = t("downloads.jobs_count", { n: jobs.size });
  }
  $("dl-clear-btn").disabled = buckets.finished.length === 0;

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
    ? `${fmtBytes(j.completed || 0)} / ${fmtBytes(j.total)}`
    : "";
  const showFinishedAt = (j.status === "done" || j.status === "error") && !!j.finished_at;
  const finishedLine = showFinishedAt ? fmtRelativeTime(j.finished_at) : "";
  const finishedTitle = showFinishedAt ? fmtDateTimeFull(j.finished_at) : "";
  const finishedHTML = finishedLine
    ? `<span class="dl-finished muted" title="${escapeHtml(finishedTitle)}">${escapeHtml(finishedLine)}</span>`
    : "";
  const statusText = jobStatusLabel(j);
  const showBar = j.status === "running" || (j.status === "done") || (j.total > 0);
  const progress = showBar
    ? `<div class="dl-progress"><div class="dl-progress-bar dl-progress-${j.status}" style="width:${pct.toFixed(1)}%"></div></div>`
    : "";
  const pctText = j.status === "running" || j.status === "done"
    ? `<span class="dl-pct mono">${pct.toFixed(1)}%</span>`
    : "";

  let actionBtn = "";
  if (j.status === "running" || j.status === "queued") {
    actionBtn = `<button class="btn-icon" data-action="cancel" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.cancel"))}">×</button>`;
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
        ${pctText}
        <span class="dl-bytes muted">${escapeHtml(sizeLine)}</span>
        ${finishedHTML}
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

$("downloads-btn").addEventListener("click", openDownloads);
$("downloads-close").addEventListener("click", closeDownloads);
$("downloads-x").addEventListener("click", closeDownloads);

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
  try {
    const res = await api(`/api/download-history/${encodeURIComponent(name)}`);
    previous = res && res.exists ? res.history : null;
  } catch {
    // history endpoint is best-effort for UX warning
  }
  let confirmMsg = "";
  if (installedNow || previous?.last_done_at) {
    confirmMsg = t("downloads.reenqueue_done_confirm", { name });
  } else if (previous?.last_error_at || (previous?.error_count || 0) > 0) {
    confirmMsg = t("downloads.reenqueue_error_confirm", { name });
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

// ---------- topbar buttons ----------
async function logoutAndRedirect() {
  await fetch("/logout", { method: "POST", credentials: "same-origin" });
  window.location.href = "/login";
}

$("refresh-btn").addEventListener("click", () => { refreshStatus(); refreshModels(); });
$("settings-logout-btn").addEventListener("click", logoutAndRedirect);

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
$("settings-close").addEventListener("click", () => { $("settings-modal").hidden = true; });
$("settings-x").addEventListener("click", () => { $("settings-modal").hidden = true; });

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

// ---------- init ----------
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
setInterval(refreshStatus, STATUS_REFRESH_MS);
setInterval(refreshLoadedState, 1000);
setInterval(() => {
  const modal = $("downloads-modal");
  if (modal && !modal.hidden) renderDownloads();
}, 60_000);
