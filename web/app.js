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
let jobs = new Map();   // id -> job
let jobsStream = null;  // EventSource for /api/jobs/events
let jobsBackoffMs = 1000;

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

// ---------- downloads queue ----------

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
}

function emptyRow() {
  return `<div class="dl-empty-row muted">${escapeHtml(t("downloads.section_empty"))}</div>`;
}

function jobCardHTML(j) {
  const pct = Math.max(0, Math.min(100, j.percent || 0));
  const sizeLine = j.total > 0
    ? `${fmtBytes(j.completed || 0)} / ${fmtBytes(j.total)}`
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

  return `
    <div class="dl-item dl-${j.status}" data-id="${escapeHtml(j.id)}">
      <div class="dl-row1">
        <span class="dl-name mono">${escapeHtml(j.name)}</span>
        <span class="dl-status dl-status-${j.status}">${escapeHtml(statusText)}</span>
        <span class="dl-actions">${actionBtn}</span>
      </div>
      ${progress}
      <div class="dl-row2">
        ${pctText}
        <span class="dl-bytes muted">${escapeHtml(sizeLine)}</span>
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
  const name = input.value.trim();
  if (!name) return;
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
connectJobsStream();
setInterval(refreshStatus, 15000);
