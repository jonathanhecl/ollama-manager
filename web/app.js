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
const fmtCtx = (n) => {
  if (!n) return "—";
  if (n >= 1024) return `${(n / 1024).toFixed(0)}K`;
  return String(n);
};
const escapeHtml = (s) => String(s ?? "")
  .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
  .replace(/"/g, "&quot;").replace(/'/g, "&#39;");

function toast(msg, kind = "") {
  const div = document.createElement("div");
  div.className = "toast-item " + kind;
  div.textContent = msg;
  $("toast").appendChild(div);
  setTimeout(() => { div.style.opacity = "0"; div.style.transition = "opacity .3s"; }, 3500);
  setTimeout(() => div.remove(), 4000);
}

// ---------- state ----------
let models = [];
let activeName = null;
let pullSource = null;
let pullController = null; // AbortController for fetch-based SSE if needed

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
    $("logout-btn").hidden = !s.has_password;
  } catch (e) {
    $("status-pill").textContent = t("status.unreachable");
    $("status-pill").className = "pill pill-bad";
  }
}

// ---------- list ----------
async function refreshModels() {
  try {
    const data = await api("/api/models");
    models = data.models || [];
    renderTable();
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
    $("models-tbody").innerHTML = `<tr class="empty"><td colspan="9">${escapeHtml(t("state.error_prefix") + e.message)}</td></tr>`;
  }
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
  tbody.innerHTML = sorted.map((m) => `
    <tr class="row${m.name === activeName ? " active" : ""}" data-name="${escapeHtml(m.name)}">
      <td class="col-state"><span class="state-dot${m.loaded ? " loaded" : ""}" title="${m.loaded ? dotLoadedTxt : dotNotLoadedTxt}"></span></td>
      <td class="cell-name">${escapeHtml(m.name)}</td>
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
  `).join("");

  tbody.querySelectorAll("tr.row").forEach((tr) => {
    tr.addEventListener("click", (e) => {
      if (e.target.closest(".delete-btn")) return;
      openDetail(tr.dataset.name);
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
    [t("detail.family"), d.details?.family || "—"],
    [t("detail.architecture"), d.architecture || "—"],
    [t("detail.params"), d.details?.parameter_size || (d.parameter_count ? `${(d.parameter_count / 1e9).toFixed(2)}B` : "—")],
    [t("detail.quant"), d.details?.quantization_level || "—"],
    [t("detail.format"), d.details?.format || "—"],
    [t("detail.context"), fmtCtx(d.context_length)],
    [t("detail.size"), fmtBytes(m.size)],
    [t("detail.state"), stateText],
    [t("detail.modified"), new Date(d.modified_at).toLocaleString()],
    [t("detail.digest"), `<span class="mono">${escapeHtml((m.digest || "").slice(0, 16))}…</span>`],
  ];
  const grid = rows.map(([k, v]) => `<div class="k">${escapeHtml(k)}</div><div class="v">${v}</div>`).join("");

  const caps = (d.capabilities || []).map((c) => `<span class="pill">${escapeHtml(c)}</span>`).join("");
  const capsBlock = caps ? `<div class="detail-section"><h3>${escapeHtml(t("detail.capabilities"))}</h3><div class="cap-list">${caps}</div></div>` : "";

  const paramsBlock = d.parameters ? `<div class="detail-section"><h3>${escapeHtml(t("detail.parameters_section"))}</h3><pre>${escapeHtml(d.parameters)}</pre></div>` : "";
  const tmplBlock = d.template ? `<div class="detail-section"><h3>${escapeHtml(t("detail.template"))}</h3><pre>${escapeHtml(d.template)}</pre></div>` : "";

  $("detail-body").innerHTML = `<div class="detail-grid">${grid}</div>${capsBlock}${paramsBlock}${tmplBlock}`;
}

$("detail-close").addEventListener("click", () => {
  $("detail-panel").hidden = true;
  activeName = null;
  document.querySelectorAll("tbody tr.row.active").forEach((tr) => tr.classList.remove("active"));
});

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

// ---------- install (SSE via fetch) ----------
$("install-form").addEventListener("submit", (e) => {
  e.preventDefault();
  const name = $("install-name").value.trim();
  if (!name) return;
  startPull(name);
});

async function startPull(name) {
  $("install-modal-name").textContent = name;
  $("install-bar").style.width = "0%";
  $("install-percent").textContent = "0%";
  $("install-bytes").textContent = "";
  $("install-log").textContent = "";
  $("install-cancel").hidden = false;
  $("install-close").hidden = true;
  $("install-modal").hidden = false;

  pullController = new AbortController();
  let lastStatus = "";

  try {
    const res = await fetch("/api/pull", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
      signal: pullController.signal,
    });
    if (res.status === 401) { window.location.href = "/login"; return; }
    if (!res.ok) throw new Error(`HTTP ${res.status}`);

    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buf = "";

    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });

      let idx;
      while ((idx = buf.indexOf("\n\n")) !== -1) {
        const raw = buf.slice(0, idx);
        buf = buf.slice(idx + 2);
        const ev = parseSSE(raw);
        if (!ev) continue;

        if (ev.event === "progress") {
          const p = ev.data;
          if (p.percent != null) {
            const pct = Math.max(0, Math.min(100, p.percent));
            $("install-bar").style.width = pct.toFixed(1) + "%";
            $("install-percent").textContent = pct.toFixed(1) + "%";
          }
          if (p.total) {
            $("install-bytes").textContent = `${fmtBytes(p.completed || 0)} / ${fmtBytes(p.total)}`;
          } else {
            $("install-bytes").textContent = "";
          }
          if (p.status && p.status !== lastStatus) {
            appendLog(p.status);
            lastStatus = p.status;
          }
        } else if (ev.event === "done") {
          appendLog(t("install.installed"));
          $("install-bar").style.width = "100%";
          $("install-percent").textContent = "100%";
          finishPull(true, name);
        } else if (ev.event === "error") {
          appendLog("✗ " + (ev.data?.error || "error"));
          finishPull(false, name);
          throw new Error(ev.data?.error || "pull failed");
        } else if (ev.event === "start") {
          appendLog(t("install.pulling", { name: ev.data?.name }));
        }
      }
    }
  } catch (e) {
    if (e.name !== "AbortError") {
      appendLog("✗ " + e.message);
      toast(t("toast.error", { msg: e.message }), "error");
    } else {
      appendLog(t("install.cancelled"));
    }
    finishPull(false, name);
  }
}

function parseSSE(raw) {
  const lines = raw.split("\n");
  let event = "message";
  let dataStr = "";
  for (const line of lines) {
    if (line.startsWith("event:")) event = line.slice(6).trim();
    else if (line.startsWith("data:")) dataStr += line.slice(5).trim();
  }
  if (!dataStr) return null;
  try {
    return { event, data: JSON.parse(dataStr) };
  } catch {
    return { event, data: dataStr };
  }
}

function appendLog(msg) {
  const log = $("install-log");
  log.textContent += msg + "\n";
  log.scrollTop = log.scrollHeight;
}

function finishPull(success, name) {
  $("install-cancel").hidden = true;
  $("install-close").hidden = false;
  pullController = null;
  if (success) {
    $("install-name").value = "";
    refreshModels();
  }
}

$("install-cancel").addEventListener("click", () => {
  if (pullController) pullController.abort();
});
$("install-close").addEventListener("click", () => { $("install-modal").hidden = true; });

// ---------- topbar buttons ----------
$("refresh-btn").addEventListener("click", () => { refreshStatus(); refreshModels(); });
$("logout-btn").addEventListener("click", async () => {
  await fetch("/logout", { method: "POST", credentials: "same-origin" });
  window.location.href = "/login";
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
setInterval(refreshStatus, 15000);
