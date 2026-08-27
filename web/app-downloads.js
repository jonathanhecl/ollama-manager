"use strict";

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
      jobsHydrated = true;
      queuePaused = !!data.queue_paused;
      onJobsChanged();
    } catch { }
  });

  jobsStream.addEventListener("update", (ev) => {
    try {
      const data = JSON.parse(ev.data);
      jobsHydrated = true;
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
        refreshModels().then(() => {
          // Re-render HF cards after models are updated so the card shows
          // the correct installed (green) / previously had (orange) color.
          if (typeof renderHFModelsList === "function") {
            try { renderHFModelsList(); } catch {}
          }
          if (typeof hfActiveModel !== "undefined" && hfActiveModel && typeof renderHFQuantsTable === "function" && !$("hf-detail-modal")?.hidden) {
            try { renderHFQuantsTable(hfActiveModel); } catch {}
          }
        });
      }
    } catch { }
  });

  jobsStream.addEventListener("remove", (ev) => {
    try {
      const data = JSON.parse(ev.data);
      jobsHydrated = true;
      if ("queue_paused" in data) queuePaused = !!data.queue_paused;
      if (!data.id) return;
      jobs.delete(data.id);
      onJobsChanged();
    } catch { }
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

// renderTable is cheap enough to run on its own, but the jobs SSE stream can
// emit ~4 events/second during a download. Throttle the table refresh so the
// main thread stays responsive (drops clicks/jank elsewhere in the UI).
let renderTableThrottled = false;
let renderTablePending = false;
function throttleRenderTable() {
  renderTablePending = true;
  if (renderTableThrottled) return;
  renderTableThrottled = true;
  renderTablePending = false;
  renderTable();
  setTimeout(() => {
    renderTableThrottled = false;
    if (renderTablePending) {
      renderTablePending = false;
      renderTable();
    }
  }, 300);
}

let hfRenderThrottled = false;
let hfRenderPending = false;
function throttleHFRender() {
  if (hfRenderThrottled) { hfRenderPending = true; return; }
  hfRenderThrottled = true;
  hfRenderPending = false;
  try {
    if (typeof renderHFModelsList === "function") renderHFModelsList();
    if (typeof hfActiveModel !== "undefined" && hfActiveModel && typeof renderHFQuantsTable === "function" && !$("hf-detail-modal")?.hidden) {
      renderHFQuantsTable(hfActiveModel);
    }
  } catch {}
  setTimeout(() => {
    hfRenderThrottled = false;
    if (hfRenderPending) {
      hfRenderPending = false;
      throttleHFRender();
    }
  }, 1500);
}

function onJobsChanged() {
  updateDownloadsBadge();
  if (!$("downloads-modal").hidden) renderDownloads();
  throttleRenderTable(); // Update main model list to show/hide pending downloads
  throttleHFRender();
}

// Re-fetch the authoritative job list so the jobs Map reflects the server's
// current queue order (e.g. after "move to front"). Map insertion order is
// preserved, so this also reorders the queue UI to match the server.
async function refreshJobs() {
  const data = await api("/api/jobs");
  jobs = new Map((data.jobs || []).map((j) => [j.id, j]));
  onJobsChanged();
}

function jobsByStatus() {
  const buckets = { active: [], queued: [], paused: [], finished: [] };
  for (const j of jobs.values()) {
    if (j.status === "running") buckets.active.push(j);
    else if (j.status === "queued") buckets.queued.push(j);
    else if (j.status === "paused") buckets.paused.push(j);
    else buckets.finished.push(j);
  }
  // Active, queued and paused keep the server's authoritative queue order
  // (the jobs Map is inserted in the order returned by /api/jobs, which is the
  // manager's m.order — the same order "move to front" promotes into). We must
  // NOT re-sort by created_at here, or promotion would be undone. Finished
  // shows most recent first.
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
  $("dl-clear-btn").disabled = !jobsHydrated || buckets.finished.length === 0;

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

  // Wire per-card buttons via delegation on the stable modal container in
  // bindDownloadsEvents (see below). Per-card listeners are NOT attached here
  // because while a download is active the SSE stream re-renders the list
  // every few hundred ms, destroying the buttons before a click lands.
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

  const siteUrl = typeof modelHomepageUrl === "function" ? modelHomepageUrl(j.name) : "";
  const siteBtn = siteUrl
    ? `<a class="btn-icon dl-site-btn" href="${siteUrl}" target="_blank" rel="noopener noreferrer" title="${escapeHtml(t("detail.site"))}" aria-label="${escapeHtml(t("detail.site"))}"><span class="ghost-site-glyph" aria-hidden="true">↗</span></a>`
    : "";

  let actionBtn = "";
  if (j.status === "running" || j.status === "queued") {
    const promoteBtn = j.status === "queued"
      ? `<button class="btn-icon" data-action="promote" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.promote"))}">↑</button>`
      : "";
    actionBtn = `
      ${promoteBtn}
      ${siteBtn}
      <button class="ghost dl-pause" data-action="pause" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.pause"))}">⏸</button>
      <button class="btn-icon" data-action="cancel" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.cancel"))}">×</button>`;
  } else if (j.status === "paused") {
    actionBtn = `
      ${siteBtn}
      <button class="ghost dl-resume" data-action="resume" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.resume"))}">▶</button>
      <button class="btn-icon" data-action="cancel" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.cancel"))}">×</button>`;
  } else if (j.status === "error" || j.status === "cancelled") {
    actionBtn = `
      ${siteBtn}
      <button class="ghost dl-retry" data-action="retry" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.retry"))}">↻</button>
      <button class="btn-icon" data-action="remove" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.remove"))}">×</button>`;
  } else {
    // done
    actionBtn = `
      ${siteBtn}
      <button class="btn-icon" data-action="remove" data-id="${escapeHtml(j.id)}" title="${escapeHtml(t("downloads.remove"))}">×</button>`;
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
  if (currentView === "settings") {
    showModelsView();
    history.pushState(null, "", "/");
  }
}

$("downloads-btn").addEventListener("click", openDownloads);
$("downloads-close").addEventListener("click", closeDownloads);
$("downloads-x").addEventListener("click", closeDownloads);
$("downloads-modal").addEventListener("click", (e) => {
  if (e.target === $("downloads-modal")) closeDownloads();
});

// Per-card actions are delegated to the stable modal container (attached once,
// never re-rendered). While a download is active the SSE stream re-renders the
// list every few hundred ms, so listeners bound to individual cards/buttons
// would be destroyed before a click lands; delegation survives that.
$("downloads-modal").addEventListener("click", async (e) => {
  const btn = e.target.closest("[data-action]");
  if (btn) {
    e.stopPropagation();
    const id = btn.dataset.id;
    const action = btn.dataset.action;
    if (!id || !action) return;
    try {
      if (action === "cancel") {
        await api(`/api/jobs/${encodeURIComponent(id)}/cancel`, { method: "POST" });
      } else if (action === "remove") {
        await api(`/api/jobs/${encodeURIComponent(id)}`, { method: "DELETE" });
      } else if (action === "pause") {
        await api(`/api/jobs/${encodeURIComponent(id)}/pause`, { method: "POST" });
      } else if (action === "resume") {
        await api(`/api/jobs/${encodeURIComponent(id)}/resume`, { method: "POST" });
        await refreshJobs();
      } else if (action === "promote") {
        await api(`/api/jobs/${encodeURIComponent(id)}/promote`, { method: "POST" });
        await refreshJobs();
      } else if (action === "retry") {
        const j = jobs.get(id);
        if (!j) return;
        await api("/api/pull", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ name: j.name }),
        });
      }
    } catch (err) {
      toast(t("toast.error", { msg: err.message }), "error");
    }
    return;
  }
  if (e.target.closest(".dl-site-btn")) {
    return;
  }
  // Click on a finished download card opens chat with that model (only if done).
  // Clicking on running, queued, paused or failed downloads does not trigger any action.
  const card = e.target.closest(".dl-item.dl-done");
  if (!card) return;
  const id = card.dataset.id;
  if (!id) return;
  const j = jobs.get(id);
  if (!j || j.status !== "done" || !j.name) return;
  await refreshModels();
  if (!modelByName(j.name)) return;
  closeDownloads();
  showChatViewWithModel(j.name);
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
    await refreshJobs();
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

async function promptDownloadModel(rawName) {
  const name = normalizePullInput(rawName);
  if (!name) return;

  let historyData = null;
  try {
    historyData = await api(`/api/download-history/${encodeURIComponent(name)}`);
  } catch {
    // history endpoint is best-effort for UX warning
  }

  const related = Array.isArray(historyData?.related_models) ? historyData.related_models : [];
  const repoBase = historyData?.repo_base || name.split(":")[0];

  if (related.length > 0) {
    const cardsHtml = related.map((m) => {
      let badgeClass = "dl-badge-tested";
      let badgeText = t("downloads.history_variant_tested");

      if (m.is_installed) {
        badgeClass = "dl-badge-installed";
        badgeText = t("downloads.history_variant_installed");
      } else if (m.uninstall?.reason) {
        badgeClass = "dl-badge-uninstalled";
        badgeText = t("downloads.history_variant_uninstalled");
      } else if (m.history?.error_count > 0 || m.history?.last_error) {
        badgeClass = "dl-badge-error";
        badgeText = t("downloads.history_variant_error");
      }

      let reasonHtml = "";
      if (m.uninstall?.reason) {
        const rText = uninstallReasonToText(m.uninstall.reason);
        const atText = m.uninstall.at ? ` · <span class="mono">${fmtDate(m.uninstall.at)}</span>` : "";
        reasonHtml = `<div class="dl-hist-reason">🗑️ ${escapeHtml(t("downloads.history_delete_reason", { reason: rText }))}${atText}</div>`;
      }

      const statsParts = [];
      if (m.usage) {
        if (m.usage.record_tokens_per_sec > 0) {
          statsParts.push(`⚡ ${m.usage.record_tokens_per_sec.toFixed(1)} tok/s`);
        }
        if (m.usage.min_cold_load_ms > 0) {
          statsParts.push(`⏱️ ${fmtColdLoad(m.usage.min_cold_load_ms)}`);
        }
        if (m.usage.last_used_at) {
          statsParts.push(`📅 ${fmtDate(m.usage.last_used_at)}`);
        }
        if (m.usage.total_calls > 0) {
          statsParts.push(`💬 ${m.usage.total_calls} calls`);
        }
      }
      const statsHtml = statsParts.length > 0
        ? `<div class="dl-hist-stats mono">${statsParts.map((s) => `<span>${escapeHtml(s)}</span>`).join("")}</div>`
        : "";

      let errHtml = "";
      if (m.history?.last_error && !m.is_installed) {
        errHtml = `<div class="dl-hist-error">⚠️ ${escapeHtml(m.history.last_error)}</div>`;
      }

      return `
        <div class="dl-history-alert-card">
          <div class="dl-hist-head">
            <span class="dl-hist-name mono">${escapeHtml(m.name)}</span>
            <span class="dl-hist-badge ${badgeClass}">${escapeHtml(badgeText)}</span>
          </div>
          ${reasonHtml}
          ${statsHtml}
          ${errHtml}
        </div>
      `;
    }).join("");

    const intro = t("downloads.history_alert_intro", { name: escapeHtml(name), repo: escapeHtml(repoBase) });
    const modalHtml = `<div>${intro}</div><div class="dl-history-alert-wrap">${cardsHtml}</div>`;

    const { ok } = await askConfirm({
      title: t("downloads.history_alert_title"),
      html: modalHtml,
      okText: t("downloads.history_download_anyway"),
      okClass: "primary",
    });
    if (!ok) return;
  }

  try {
    await api("/api/pull", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    const input = $("dl-add-input");
    if (input) input.value = "";
    toast(t("downloads.enqueued", { name }), "success");
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

$("dl-add-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const input = $("dl-add-input");
  if (!input) return;
  await promptDownloadModel(input.value);
});

$("dl-clear-btn").addEventListener("click", async () => {
  const btn = $("dl-clear-btn");
  if (btn.disabled) return;
  btn.disabled = true;
  const originalLabel = btn.textContent;
  btn.textContent = t("downloads.clearing");
  try {
    const res = await api("/api/jobs/clear", { method: "POST" });
    // Optimistically drop terminal jobs locally so the UI updates instantly,
    // without waiting for the SSE "remove" events.
    for (const [id, j] of jobs) {
      if (isTerminal(j.status)) jobs.delete(id);
    }
    onJobsChanged();
    const removed = (res && typeof res.removed === "number") ? res.removed : 0;
    if (removed > 0) toast(t("downloads.cleared", { n: removed }), "success");
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  } finally {
    btn.textContent = originalLabel;
    renderDownloads(); // recompute the correct disabled state (no finished left)
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
$("vram-widget")?.addEventListener("click", () => {
  openRunningModal();
});
$("vram-widget")?.addEventListener("keydown", (e) => {
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
  const btn = $("running-unload-all");
  if (btn?.disabled) return;
  const { ok } = await askConfirm({
    title: t("running.unload_all"),
    text: t("running.unload_all_confirm"),
    okText: t("running.unload_all"),
    okClass: "danger",
  });
  if (!ok) return;
  if (btn) btn.disabled = true;
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
  } finally {
    if (btn) btn.disabled = false;
  }
  await refreshRunningModalList({ silent: true });
  refreshLoadedState();
  refreshStatus();
});

