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
const formatBytes = fmtBytes;
const estimateTokens = (text) => {
  if (!text || !text.trim()) return 0;
  const matches = text.match(/[\w]+|[^\s\w]+/gu);
  if (!matches) return 0;
  let tokens = 0;
  for (let i = 0; i < matches.length; i++) {
    const len = matches[i].length;
    tokens += Math.max(1, Math.ceil(len / 3.8));
  }
  return tokens;
};
window.estimateTokens = estimateTokens;
const fmtTokens = (count) => {
  const n = typeof count === "number" ? count : estimateTokens(count);
  if (!n || n <= 0) return "0 tok";
  return `~${n.toLocaleString()} tok`;
};
window.fmtTokens = fmtTokens;
const fmtDate = (s) => {
  if (!s) return "—";
  const d = new Date(s);
  if (isNaN(d)) return "—";
  const now = new Date();
  const diffDays = Math.floor((now - d) / 86400000);
  const lang = window.I18n?.getLang?.() || "en";
  const locale = lang === "es" ? "es-AR" : "en-US";
  if (diffDays < 1) return d.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" });
  if (diffDays < 7) return getRelativeTimeFormatter().format(-diffDays, "day");
  return d.toLocaleDateString(locale);
};
const fmtColdLoad = (ms) => {
  if (!ms || ms <= 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
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

function formatMetaElapsedSecondsTitle(ms) {
  const n = Math.max(0, Number(ms) || 0);
  if (n < 1000) {
    return `${Math.round(n)}ms`;
  }
  const sec = n / 1000;
  if (sec < 10) {
    return `${sec.toFixed(1)}s`;
  }
  return `${Math.floor(sec)}s`;
}

function formatMetaElapsed(ms) {
  const n = Math.max(0, Number(ms) || 0);
  if (n < 1000) {
    return t("chat.meta_time_ms", { ms: Math.round(n) });
  }
  const sec = n / 1000;
  if (sec < 10) {
    return t("chat.meta_time_s_dec", { s: sec.toFixed(1) });
  }
  if (sec < 60) {
    return t("chat.meta_time", { s: Math.floor(sec) });
  }
  const totalSec = Math.floor(sec);
  const days = Math.floor(totalSec / 86400);
  const hours = Math.floor((totalSec % 86400) / 3600);
  const mins = Math.floor((totalSec % 3600) / 60);
  const secs = totalSec % 60;

  if (days > 0) {
    return `${days}d${hours}h${mins}m${secs}s`;
  }
  if (hours > 0) {
    return `${hours}h${mins}m${secs}s`;
  }
  return `${mins}m${secs}s`;
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

function updateMarquee(trackEl, textEl) {
  if (!trackEl || !textEl) return;
  textEl.classList.remove("is-bouncing");
  textEl.style.removeProperty("--marquee-dist");
  textEl.style.removeProperty("--marquee-dur");
  textEl.style.transform = "";

  requestAnimationFrame(() => {
    if (!trackEl || !textEl) return;
    const trackWidth = trackEl.clientWidth;
    const textWidth = textEl.scrollWidth;
    const overflow = textWidth - trackWidth;

    if (overflow > 2) {
      const dist = overflow + 8;
      const duration = Math.max(4, Math.min(16, dist / 20));
      textEl.style.setProperty("--marquee-dist", `-${dist}px`);
      textEl.style.setProperty("--marquee-dur", `${duration}s`);
      textEl.classList.add("is-bouncing");
    } else {
      textEl.classList.remove("is-bouncing");
    }
  });
}

function getModelUrlKey(model) {
  if (!model) return "";
  if (model.digest) return model.digest.replace(":", "-");
  return encodeURIComponent(model.name || "");
}

function findModelByUrlKey(key) {
  if (!key || typeof models === "undefined" || !Array.isArray(models)) return null;
  const decoded = decodeURIComponent(key);
  return models.find((m) => m.digest && m.digest.replace(":", "-") === key) ||
         models.find((m) => m.name === decoded || m.name === key) ||
         models.find((m) => m.name.toLowerCase() === decoded.toLowerCase() || m.name.toLowerCase() === key.toLowerCase()) ||
         null;
}

