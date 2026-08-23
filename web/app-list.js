"use strict";

// ---------- list ----------
async function refreshModels() {
  try {
    const data = await api("/api/models");
    models = data.models || [];
    ghostModels = data.ghost_models || [];
    updateSystemWidgets(lastSystemStatus);
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

function renderRunningModalList() {
  const list = $("running-list");
  const empty = $("running-empty");
  const badge = $("running-count-badge");
  const unloadAllBtn = $("running-unload-all");
  if (!list || !empty || !badge) return;

  const rows = [...(runningModels || [])]
    .sort((a, b) => (Number(b.size_vram) || 0) - (Number(a.size_vram) || 0));

  badge.textContent = String(rows.length);
  badge.hidden = rows.length === 0;
  empty.hidden = rows.length !== 0;
  if (unloadAllBtn) {
    unloadAllBtn.disabled = rows.length === 0;
  }

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
  renderRunningModalList();
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
    case "name": return (m.name || "").toLowerCase();
    case "family": return (m.family || "").toLowerCase();
    case "record_tokens_per_sec": return Number(m.record_tokens_per_sec) || 0;
    case "parameter_size": return parseParamSize(m.parameter_size) || Number(m.parameter_count) || 0;
    case "quantization": return ((m.family || "") + " " + (m.quantization || "")).toLowerCase();
    case "context_length": return Number(m.context_length) || 0;
    case "size": return Number(m.size) || 0;
    case "modified_at": return m.last_used_at ? new Date(m.last_used_at).getTime() || 0 : (new Date(m.modified_at).getTime() || 0);
    default: return "";
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

function modelParameterLabel(m) {
  if (m.parameter_size && m.parameter_size !== "—") return m.parameter_size;
  const exact = Number(m.parameter_count) || 0;
  return exact > 0 ? formatExactParams(exact) : "—";
}

function modelQuantLabel(m) {
  const q = (m.quantization && m.quantization !== "—") ? m.quantization : "—";
  if (m.family && m.family !== "—") return m.family + " · " + q;
  return q;
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
      const timeA = a.job?.created_at
        ? new Date(a.job.created_at).getTime()
        : (a.last_used_at ? new Date(a.last_used_at).getTime() : new Date(a.modified_at).getTime());
      const timeB = b.job?.created_at
        ? new Date(b.job.created_at).getTime()
        : (b.last_used_at ? new Date(b.last_used_at).getTime() : new Date(b.modified_at).getTime());
      return dir === "asc" ? (timeA - timeB) : (timeB - timeA);
    });
  }
  const mul = dir === "asc" ? 1 : -1;
  return [...arr].sort((a, b) => {
    const ka = sortKey(a, col);
    const kb = sortKey(b, col);
    let cmp;
    if (typeof ka === "string" || typeof kb === "string") {
      cmp = String(ka).localeCompare(String(kb));
    } else {
      cmp = ka - kb;
    }
    if (cmp === 0) {
      cmp = String(a.name || "").toLowerCase().localeCompare(String(b.name || "").toLowerCase());
    }
    return mul * cmp;
  });
}

function updateModelsCount(renderedCount, totalCount, query) {
  const countEl = $("models-count");
  if (!countEl) return;
  if (!totalCount && !renderedCount) {
    countEl.textContent = "";
    return;
  }
  countEl.classList.toggle("ghosts-active", showGhostModels && ghostModels.length > 0);
  countEl.title = t("models.toggle_ghosts_title");

  if (query) {
    countEl.textContent = t("models.count_filtered", { count: renderedCount, total: totalCount });
  } else if (showGhostModels && ghostModels.length > 0) {
    countEl.textContent = t("models.count_with_ghosts", { count: totalCount, ghosts: ghostModels.length });
  } else {
    countEl.textContent = t("models.count_total", { count: totalCount });
  }
}

function bindModelsSearchEvents() {
  const searchInput = $("models-search");
  const clearBtn = $("models-search-clear");
  const countEl = $("models-count");
  if (countEl && !countEl._ghostBound) {
    countEl._ghostBound = true;
    countEl.title = t("models.toggle_ghosts_title");
    countEl.addEventListener("click", () => {
      showGhostModels = !showGhostModels;
      localStorage.setItem("ollama_show_ghost_models", String(showGhostModels));
      renderTable();
    });
  }
  if (!searchInput) return;

  searchInput.addEventListener("input", () => {
    modelSearchQuery = searchInput.value;
    if (clearBtn) {
      clearBtn.hidden = !modelSearchQuery;
    }
    renderTable();
  });

  if (clearBtn) {
    clearBtn.addEventListener("click", () => {
      modelSearchQuery = "";
      searchInput.value = "";
      clearBtn.hidden = true;
      renderTable();
      searchInput.focus();
    });
  }

  searchInput.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      if (searchInput.value) {
        e.preventDefault();
        modelSearchQuery = "";
        searchInput.value = "";
        if (clearBtn) clearBtn.hidden = true;
        renderTable();
      } else {
        searchInput.blur();
      }
    }
  });
}

function renderTable() {
  updateSortIndicators();
  const tbody = $("models-tbody");
  if (!tbody) return;

  const q = (modelSearchQuery || "").trim().toLowerCase();

  // Filter models based on archived state
  let activeModels = models.filter(m => !!m.archived === showArchivedOnly).map(m => ({ ...m }));
  const totalCount = activeModels.length;

  if (showGhostModels && !showArchivedOnly) {
    activeModels = activeModels.concat(ghostModels.map(g => ({
      ...g,
      isGhost: true,
      capabilities: []
    })));
  }

  let filteredModels = activeModels;
  if (q) {
    filteredModels = activeModels.filter(m => {
      return (
        (m.name && m.name.toLowerCase().includes(q)) ||
        (m.family && m.family.toLowerCase().includes(q)) ||
        ((m.families || []).some(f => f && f.toLowerCase().includes(q))) ||
         modelParameterLabel(m).toLowerCase().includes(q) ||
         (m.parameter_count && String(m.parameter_count).includes(q)) ||
        (m.quantization && m.quantization.toLowerCase().includes(q)) ||
        ((m.capabilities || []).some(c => c && c.toLowerCase().includes(q))) ||
        (m.digest && m.digest.toLowerCase().includes(q))
      );
    });
  }

  // Attach active jobs to installed models so they participate in sorting and display.
  // Only queued and running jobs appear in the main list; paused ones stay in the downloads modal.
  const installedNames = new Set(models.map(m => m.name));
  let pendingModels = [];
  const runningJobByName = new Map();
  if (!showArchivedOnly) {
    for (const j of jobs.values()) {
      if (j.status === "running") runningJobByName.set(j.name, j);
      if (j.status === "running" || j.status === "queued") {
        const model = filteredModels.find(m => m.name === j.name);
        if (model) {
          model.job = j;
        } else if (!installedNames.has(j.name)) {
          pendingModels.push({
            name: j.name,
            isPending: true,
            job: j,
            family: "—",
             parameter_size: "—",
             parameter_count: 0,
            quantization: "—",
            context_length: 0,
            size: 0,
            modified_at: j.created_at,
            capabilities: []
          });
        }
      }
    }
    if (q) {
      pendingModels = pendingModels.filter(p => p.name.toLowerCase().includes(q));
    }
  }

  const allToRender = applySort([...filteredModels, ...pendingModels]);

  updateModelsCount(allToRender.length, totalCount, q);

  if (!allToRender.length) {
    tbody.innerHTML = "";
    if (q) {
      tbody.innerHTML = `<tr class="empty"><td colspan="9">${escapeHtml(t("state.no_search_results", { query: modelSearchQuery.trim() }))}</td></tr>`;
    } else if (showArchivedOnly) {
      tbody.innerHTML = `<tr class="empty"><td colspan="9">${escapeHtml(t("state.empty_archived"))}</td></tr>`;
    } else {
      tbody.innerHTML = `<tr class="empty"><td colspan="9">${escapeHtml(t("state.empty_models"))}</td></tr>`;
    }
    return;
  }

  // Calculate min and max record tokens per second across all models with data
  let minToks = Infinity;
  let maxToks = -Infinity;
  for (const m of activeModels) {
    const tps = Number(m.record_tokens_per_sec) || 0;
    if (tps > 0) {
      if (tps < minToks) minToks = tps;
      if (tps > maxToks) maxToks = tps;
    }
  }

  function getToksRecordColor(tps) {
    if (!tps || tps <= 0 || !isFinite(minToks) || !isFinite(maxToks)) return "";
    let ratio = 1.0;
    if (maxToks > minToks) {
      ratio = Math.max(0, Math.min(1, (tps - minToks) / (maxToks - minToks)));
    }
    // Hue from 0 (red) -> 60 (yellow) -> 120 (green)
    const hue = Math.round(ratio * 120);
    return `hsl(${hue}, 85%, 62%)`;
  }

  const dotLoadedTxt = t("detail.dot_loaded");
  const dotNotLoadedTxt = t("detail.dot_not_loaded");
  const deleteTitle = t("detail.delete_title");
  const infoTitle = t("detail.info_btn");
  const archiveTitle = t("detail.archive_title");
  const unarchiveTitle = t("detail.unarchive_title");
  function getRowInnerHtml(m, capsHtml, progressHtml) {
    const ghostTag = m.isGhost ? `<span class="model-ghost-tag">(${escapeHtml(t("models.ghost_badge"))})</span>` : "";
    const customTag = (!m.isGhost && m.is_custom)
      ? `<span class="model-custom-tag" title="${m.base_model ? escapeHtml(t("models.custom_based_on", { base: m.base_model })) : escapeHtml(t("models.custom_tooltip"))}">${escapeHtml(t("models.custom_badge"))}</span>`
      : "";
    const tokColor = getToksRecordColor(m.record_tokens_per_sec);
    const colorStyle = tokColor ? ` style="color: ${tokColor};"` : "";
    const recordTokHtml = (m.record_tokens_per_sec && m.record_tokens_per_sec > 0)
      ? `<span class="cell-record-tok" title="${m.record_tokens_per_sec_at ? escapeHtml(t("detail.record_at", { date: fmtDateTimeFull(m.record_tokens_per_sec_at) })) : ""}"><span class="record-num"${colorStyle}>${m.record_tokens_per_sec.toFixed(1)}</span> <span class="unit">tok/s</span></span>`
      : "—";
    const minLoadHtml = (m.min_cold_load_ms && m.min_cold_load_ms > 0)
      ? `<div class="cell-min-load mono" title="${m.min_cold_load_at ? escapeHtml(t("detail.min_load_at", { date: fmtDateTimeFull(m.min_cold_load_at) })) : ""}">${escapeHtml(t("col.min_load"))}: ${fmtColdLoad(m.min_cold_load_ms)}</div>`
      : "";
    
    const lastUsedDisplay = m.last_used_at ? fmtDate(m.last_used_at) : "—";
    const lastUsedTitle = m.last_used_at ? fmtDateTimeFull(m.last_used_at) : "";
    const modifiedDisplay = m.isGhost ? "—" : fmtDate(m.modified_at);
    const modifiedTitle = m.isGhost ? "" : fmtDateTimeFull(m.modified_at);

    const stateDotHtml = m.isGhost
      ? `<span class="state-dot ghost" title="${escapeHtml(t("models.ghost_badge"))}">👻</span>`
      : `<span class="state-dot${m.loaded ? " loaded" : ""}" title="${m.loaded ? dotLoadedTxt : dotNotLoadedTxt}"></span>`;

    const actionsHtml = m.isGhost
      ? `<button type="button" class="btn-icon reinstall-ghost-btn" title="${escapeHtml(t("models.reinstall_title"))}" data-name="${escapeHtml(m.name)}">📥</button>`
      : (!m.isPending ? `<button type="button" class="btn-icon delete-btn" title="${escapeHtml(deleteTitle)}" data-name="${escapeHtml(m.name)}">×</button>` : "");

    return `
      <td class="col-state">${stateDotHtml}</td>
      <td class="cell-name">
        <div class="model-name-wrap">
          <div class="model-name-block">
            <div class="model-name model-name-track"><span class="model-name-text">${escapeHtml(m.name)}</span>${customTag}${ghostTag}</div>
            ${progressHtml}
            ${capsHtml ? `<div class="cap-list model-cap-list">${capsHtml}</div>` : ""}
          </div>
          ${(!m.isPending && !m.isGhost) ? `<button type="button" class="btn-icon info-btn" data-name="${escapeHtml(m.name)}" title="${escapeHtml(infoTitle)}" aria-label="${escapeHtml(infoTitle)}"><span class="info-glyph" aria-hidden="true">i</span></button>` : ""}
          ${(!m.isPending && m.isGhost && modelHomepageUrl(m.name)) ? `<a class="btn-icon ghost-site-btn" href="${modelHomepageUrl(m.name)}" target="_blank" rel="noopener noreferrer" title="${escapeHtml(t("detail.site"))}" aria-label="${escapeHtml(t("detail.site"))}"><span class="ghost-site-glyph" aria-hidden="true">↗</span></a>` : ""}
        </div>
      </td>
      <td class="cell-record-tok-col">
        <div class="cell-record-wrap">
          <div class="cell-record-main">${m.isPending ? "—" : recordTokHtml}</div>
          ${m.isPending ? "" : minLoadHtml}
        </div>
      </td>
      <td class="cell-params">${escapeHtml(modelParameterLabel(m))}</td>
      <td class="cell-quant">${escapeHtml(modelQuantLabel(m))}</td>
      <td class="cell-ctx">${m.isPending ? "—" : (m.context_length > 0 ? fmtCtx(m.context_length) : "—")}</td>
      <td class="cell-size">${m.isPending ? "—" : (m.size > 0 ? fmtBytes(m.size) : "—")}</td>
      <td class="cell-modified">
        <div class="cell-dates">
          <div class="date-primary" title="${escapeHtml(lastUsedTitle)}">${m.isPending ? "—" : lastUsedDisplay}</div>
          <div class="date-secondary mono" title="${escapeHtml(modifiedTitle)}">${m.isPending ? "—" : modifiedDisplay}</div>
        </div>
      </td>
      <td class="col-actions">
        ${actionsHtml}
      </td>
    `;
  }

  // Get existing rows in the DOM to reconcile
  const existingRows = new Map();
  tbody.querySelectorAll("tr.row").forEach((tr) => {
    const name = tr.dataset.name;
    if (name) existingRows.set(name, tr);
  });

  // Remove empty row if we have actual models to render
  const emptyRow = tbody.querySelector("tr.empty");
  if (emptyRow) {
    emptyRow.remove();
  }

  allToRender.forEach((m, idx) => {
    const capsHtml = renderCapabilityPills(m.capabilities);
    const job = m.job || runningJobByName.get(m.name);
    const pct = job && job.status === "running" ? Math.max(0, Math.min(100, job.percent || 0)) : 0;
    const progressHtml = job && job.status === "running"
      ? `<div class="model-progress"><div class="model-progress-bar" style="width:${pct.toFixed(1)}%"></div></div>`
      : "";
    const isActive = (m.name === activeName);
    const capsStr = JSON.stringify(m.capabilities || []);
    const tokColor = getToksRecordColor(m.record_tokens_per_sec);

    let tr = existingRows.get(m.name);
    let needUpdate = false;

    if (tr) {
      // Check if structural fields changed
      if (
        tr._m_isPending !== !!m.isPending ||
        tr._m_isGhost !== !!m.isGhost ||
        tr._m_isCustom !== !!m.is_custom ||
        tr._m_caps !== capsStr ||
        tr._m_size !== m.size ||
        tr._m_modified !== m.modified_at ||
        tr._m_last_used !== m.last_used_at ||
        tr._m_record_tok !== m.record_tokens_per_sec ||
        tr._m_tok_color !== tokColor ||
        tr._m_min_cold_load !== m.min_cold_load_ms ||
        tr._m_ctx !== m.context_length ||
        tr._m_family !== m.family ||
        tr._m_param !== m.parameter_size ||
        tr._m_param_count !== m.parameter_count ||
        tr._m_quant !== modelQuantLabel(m)
      ) {
        needUpdate = true;
      }
    }

    if (!tr || needUpdate) {
      const newTr = document.createElement("tr");
      newTr.dataset.name = m.name;
      
      const rowClass = m.isPending ? "row pending" : m.isGhost ? `row ghost${isActive ? " active" : ""}` : `row${isActive ? " active" : ""}`;
      newTr.className = rowClass;
      if (m.isGhost) {
        newTr.title = t("models.reinstall_title");
      }
      if (m.isPending) {
        newTr.title = "Downloading...";
        newTr.style.pointerEvents = "none";
      }

      newTr.innerHTML = getRowInnerHtml(m, capsHtml, progressHtml);

      // Event listeners
      newTr.addEventListener("click", (e) => {
        if (e.target.closest(".info-btn")) return;
        if (e.target.closest(".delete-btn")) return;
        if (e.target.closest(".reinstall-ghost-btn")) return;
        if (e.target.closest(".ghost-site-btn")) return;
        if (m.isGhost) return;
        showChatViewWithModel(newTr.dataset.name);
      });

      const reinstallBtn = newTr.querySelector(".reinstall-ghost-btn");
      if (reinstallBtn) {
        reinstallBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          void promptDownloadModel(reinstallBtn.dataset.name);
        });
      }

      const infoBtn = newTr.querySelector(".info-btn");
      if (infoBtn) {
        infoBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          openDetail(infoBtn.dataset.name);
        });
      }

      const deleteBtn = newTr.querySelector(".delete-btn");
      if (deleteBtn) {
        deleteBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          confirmDelete(deleteBtn.dataset.name);
        });
      }

      // Save properties to track state
      newTr._m_isPending = !!m.isPending;
      newTr._m_isGhost = !!m.isGhost;
      newTr._m_isCustom = !!m.is_custom;
      newTr._m_loaded = !!m.loaded;
      newTr._m_active = isActive;
      newTr._m_pct = pct;
      newTr._m_caps = capsStr;
      newTr._m_size = m.size;
      newTr._m_modified = m.modified_at;
      newTr._m_last_used = m.last_used_at;
      newTr._m_record_tok = m.record_tokens_per_sec;
      newTr._m_tok_color = tokColor;
      newTr._m_min_cold_load = m.min_cold_load_ms;
      newTr._m_ctx = m.context_length;
      newTr._m_family = m.family;
      newTr._m_param = m.parameter_size;
      newTr._m_param_count = m.parameter_count;
      newTr._m_quant = modelQuantLabel(m);

      if (tr) {
        tr.replaceWith(newTr);
      }
      tr = newTr;
    } else {
      // Re-use and patch in-place
      // 1. Loaded status
      if (tr._m_loaded !== !!m.loaded) {
        tr._m_loaded = !!m.loaded;
        const dot = tr.querySelector(".state-dot");
        if (dot) {
          dot.className = `state-dot${m.loaded ? " loaded" : ""}`;
          dot.title = m.loaded ? dotLoadedTxt : dotNotLoadedTxt;
        }
      }

      // 2. Active class
      if (tr._m_active !== isActive) {
        tr._m_active = isActive;
        tr.classList.toggle("active", isActive);
      }

      // 3. Progress bar
      if (tr._m_pct !== pct) {
        tr._m_pct = pct;
        const nameBlock = tr.querySelector(".model-name-block");
        if (nameBlock) {
          let progressContainer = nameBlock.querySelector(".model-progress");
          if (pct > 0) {
            if (!progressContainer) {
              const tempDiv = document.createElement("div");
              tempDiv.innerHTML = `<div class="model-progress"><div class="model-progress-bar" style="width:${pct.toFixed(1)}%"></div></div>`;
              progressContainer = tempDiv.firstElementChild;
              const modelNameEl = nameBlock.querySelector(".model-name");
              if (modelNameEl) {
                modelNameEl.after(progressContainer);
              } else {
                nameBlock.prepend(progressContainer);
              }
            } else {
              const bar = progressContainer.querySelector(".model-progress-bar");
              if (bar) {
                bar.style.width = `${pct.toFixed(1)}%`;
              }
            }
          } else {
            if (progressContainer) {
              progressContainer.remove();
            }
          }
        }
      }
    }

    // Move to correct index position
    const currentChild = tbody.children[idx];
    if (currentChild !== tr) {
      tbody.insertBefore(tr, currentChild || null);
    }
  });

  // Remove any remaining rows
  while (tbody.children.length > allToRender.length) {
    tbody.removeChild(tbody.lastChild);
  }

  updateListMarquees();
}

function updateListMarquees() {
  if (typeof updateMarquee !== "function") return;
  const tracks = document.querySelectorAll("#models-table .model-name-track");
  tracks.forEach((track) => {
    const text = track.querySelector(".model-name-text");
    if (text) updateMarquee(track, text);
  });
}

window.addEventListener("resize", () => {
  if (typeof currentView !== "undefined" && currentView === "models") {
    updateListMarquees();
  }
});

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
      sort.dir = ["size", "context_length", "modified_at", "parameter_size", "record_tokens_per_sec"].includes(col) ? "desc" : "asc";
    }
    localStorage.setItem(SORT_KEY, JSON.stringify(sort));
    renderTable();
  });
});

