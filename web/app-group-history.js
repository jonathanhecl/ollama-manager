"use strict";

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
    const [data, testsData] = await Promise.all([
      api("/api/runner/group-history/" + encodeURIComponent(groupId)),
      api("/api/tests").catch(() => null)
    ]);
    const summary = data.summary || [];
    if (summary.length === 0) {
      body.innerHTML = `<div class="battery-empty">${t("battery.no_history")}</div>`;
      return;
    }
    const allTests = (testsData && testsData.tests) || [];
    const groupActiveTests = allTests.filter((tst) => tst.group_id === groupId && tst.active);
    const activeTotal = groupActiveTests.length;
    let activeMaxPoints = 0;
    let activeCases = 0;
    for (const tst of groupActiveTests) {
      const cCount = (tst.cases && tst.cases.length > 0) ? tst.cases.length : 1;
      activeCases += cCount;
      activeMaxPoints += (cCount + 1);
    }

    let rows = "";
    for (const s of summary) {
      // Last-run %: use only the newest run's own denominator so tests
      // added afterwards don't drag the score down.
      const hasLast = (s.last_run_max_points || 0) > 0 || (s.last_run_total_tests || 0) > 0;
      const pts = hasLast ? (s.last_run_points || 0) : (s.score_points != null ? s.score_points : (s.passed * 2));
      const passedCases = hasLast ? (s.last_run_passed_cases || 0) : (s.passed_cases != null ? s.passed_cases : s.passed);
      const totalCases = hasLast ? (s.last_run_total_cases || 0) : (s.total_cases != null ? s.total_cases : s.total_tests);
      const runTotal = hasLast ? (s.last_run_total_tests || 0) : (s.total_tests || 0);
      const runMax = hasLast ? (s.last_run_max_points || 0) : (s.max_points || (s.total_tests ? s.total_tests * 2 : 0));
      const scorePct = hasLast
        ? ((s.last_run_score != null ? s.last_run_score : (runMax > 0 ? (pts / runMax) * 100 : 0)))
        : (() => { const denom = Math.max(activeMaxPoints, s.max_points || (s.total_tests ? s.total_tests * 2 : 0)); return denom > 0 ? Math.min(100.0, Math.max(0.0, (pts / denom) * 100)) : 0; })();
      const denomShown = hasLast ? runMax : Math.max(activeMaxPoints, s.max_points || (s.total_tests ? s.total_tests * 2 : 0));
      const unrun = hasLast ? 0 : Math.max(0, activeTotal - (s.total_tests || 0));
      const unrunBadge = unrun > 0 ? `<span class="badge badge-na" style="margin-left:4px;font-size:10px;" title="${unrun} tests added since this model ran">+${unrun} ${t("battery.pending_tests") || "unrun"}</span>` : "";
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
      const passCount = hasLast ? (s.last_run_passed || 0) : s.passed;
      const failCount = hasLast ? (s.last_run_failed || 0) : s.failed;
      const humanCount = hasLast ? (s.last_run_human_review || 0) : s.human_review;
      const errCount = hasLast ? (s.last_run_errors || 0) : s.errors;
      const testsCell = hasLast ? `${runTotal}` : (activeTotal > 0 ? `${s.total_tests} / ${activeTotal}${unrunBadge}` : s.total_tests);
      const casesCell = hasLast ? `${passedCases} / ${totalCases}` : `${passedCases} / ${activeCases || totalCases}`;
      rows += `
        <tr>
          <td class="cell-model">${escapeHtml(s.model)}</td>
          <td class="cell-time">
            ${testsCell}
            <div class="muted" style="font-size:11px">${casesCell} ${t("battery.cases") || "cases"}</div>
          </td>
          <td>
            <span class="badge badge-pass" title="${passTooltip}">${passCount}</span>
            <span class="badge badge-fail" title="${failTooltip}">${failCount}</span>
            ${humanCount > 0 ? `<span class="badge badge-human" title="${humanTooltip}">${humanCount}</span>` : ""}
            ${errCount > 0 ? `<span class="badge badge-na" title="${errorTooltip || t("battery.error_count")}">${errCount}</span>` : ""}
            <span class="mono" style="font-weight:700; font-size:12px; margin-left:6px; color:var(--primary);">${scorePct.toFixed(1)}%</span>
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

// ---------- leaderboard modal (aggregated across all groups) ----------

// Quick model filter for the leaderboard (page + modal). Persisted so it
// survives re-renders; actual row hiding is instant DOM filtering (no rebuild).
let _lbModelFilter = "";
try { _lbModelFilter = localStorage.getItem("leaderboard_model_filter") || ""; } catch (_) {}

// Sort mode: "coverage" (default) ranks models with more evaluated
// categories first so a 100% in a single category doesn't beat a solid
// model evaluated everywhere; "overall" restores the classic score sort.
let _lbSortMode = "coverage";
try {
  const _sm = localStorage.getItem("leaderboard_sort");
  if (_sm === "overall" || _sm === "coverage") _lbSortMode = _sm;
} catch (_) {}

async function refreshVisibleLeaderboards() {
  const jobs = [];
  if ($("leaderboard-modal") && !$("leaderboard-modal").hidden) jobs.push(renderLeaderboardModal());
  if ($("battery-leaderboard-view") && !$("battery-leaderboard-view").hidden) jobs.push(renderLeaderboardPage());
  if (jobs.length) await Promise.all(jobs);
}

function setLbSortMode(mode) {
  if (mode !== "overall" && mode !== "coverage") return;
  _lbSortMode = mode;
  try { localStorage.setItem("leaderboard_sort", mode); } catch (_) {}
  void refreshVisibleLeaderboards();
}

function lbFilterText(key, fallback, vars) {
  try {
    const s = t(key, vars);
    if (s && s !== key) return s;
  } catch (_) {}
  let out = fallback;
  if (vars) for (const k of Object.keys(vars)) out = out.replace("{" + k + "}", vars[k]);
  return out;
}

function refreshAllLbFilters(newVal, activeInput) {
  _lbModelFilter = newVal || "";
  try {
    if (_lbModelFilter) localStorage.setItem("leaderboard_model_filter", _lbModelFilter);
    else localStorage.removeItem("leaderboard_model_filter");
  } catch (_) {}
  document.querySelectorAll("#battery-leaderboard-page-body, #leaderboard-modal-body").forEach((root) => {
    applyLbModelFilter(root, activeInput);
  });
}

function applyLbModelFilter(root, activeInput) {
  if (!root) return;
  const q = (_lbModelFilter || "").trim().toLowerCase();
  const input = root.querySelector(".lb-filter-input");
  if (input && input !== activeInput && input.value !== (_lbModelFilter || "")) {
    input.value = _lbModelFilter || "";
  }
  let shown = 0;
  let total = 0;
  root.querySelectorAll(".battery-lb-table tbody tr").forEach((row) => {
    total++;
    const name = row.getAttribute("data-lb-model")
      || row.querySelector(".lb-model-name")?.getAttribute("title")
      || row.textContent || "";
    const match = !q || name.toLowerCase().includes(q);
    row.style.display = match ? "" : "none";
    if (match) shown++;
  });
  const countEl = root.querySelector(".lb-filter-count");
  if (countEl) {
    countEl.textContent = lbFilterText("battery.leaderboard_filter_count", "{shown} / {total}", { shown, total });
    countEl.style.display = total > 0 ? "" : "none";
  }
  const clearBtn = root.querySelector(".lb-filter-clear");
  if (clearBtn) clearBtn.hidden = !(_lbModelFilter || "");
  const emptyEl = root.querySelector(".lb-filter-empty");
  if (emptyEl) emptyEl.hidden = !(total > 0 && shown === 0);
  root.querySelectorAll(".lb-ready-section").forEach((sec) => filterReadyCards(sec));
}

function filterReadyCards(section) {
  if (!section) return;
  const activeTab = section.dataset.activeTab || "pending";
  const q = (_lbModelFilter || "").trim().toLowerCase();
  let shown = 0;
  const cards = section.querySelectorAll(".lb-ready-card");
  cards.forEach((card) => {
    const status = card.dataset.lbReadyStatus;
    const name = (card.dataset.lbModel || "").toLowerCase();
    const matchesSearch = !q || name.includes(q);
    let matchesTab = true;
    if (activeTab === "pending") matchesTab = (status === "pending");
    else if (activeTab === "evaluated") matchesTab = (status === "complete" || status === "partial");
    const visible = matchesSearch && matchesTab;
    card.style.display = visible ? "" : "none";
    if (visible) shown++;
  });
  const emptyEl = section.querySelector(".lb-ready-empty");
  if (emptyEl) emptyEl.style.display = (cards.length > 0 && shown === 0) ? "block" : "none";
}

// Delegated: survives table rebuilds (innerHTML) in both page and modal.
document.addEventListener("input", (e) => {
  const inp = e.target?.closest?.(".lb-filter-input");
  if (!inp) return;
  refreshAllLbFilters(inp.value, inp);
  // Keep the other container in sync without moving the caret here.
  const root = inp.closest("#battery-leaderboard-page-body, #leaderboard-modal-body");
  if (root) applyLbModelFilter(root, inp);
});

document.addEventListener("click", (e) => {
  const clr = e.target?.closest?.(".lb-filter-clear");
  if (!clr) return;
  refreshAllLbFilters("", null);
  const root = clr.closest("#battery-leaderboard-page-body, #leaderboard-modal-body");
  const inp = root?.querySelector(".lb-filter-input") || document.querySelector(".lb-filter-input");
  if (inp) inp.focus();
});

document.addEventListener("click", (e) => {
  const sortBtn = e.target?.closest?.(".lb-sort-btn");
  if (!sortBtn) return;
  setLbSortMode(_lbSortMode === "coverage" ? "overall" : "coverage");
});

// "/" focuses the filter when the leaderboard page is visible; Esc clears it.
document.addEventListener("keydown", (e) => {
  if (e.key === "/" && typeof currentView !== "undefined" && currentView === "leaderboard") {
    const tag = e.target?.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || e.target?.isContentEditable) return;
    const inp = document.querySelector("#battery-leaderboard-page-body .lb-filter-input");
    if (inp) { e.preventDefault(); inp.focus(); }
  } else if (e.key === "Escape" && e.target?.closest?.(".lb-filter-input")) {
    if (e.target.value) { e.target.value = ""; refreshAllLbFilters("", null); }
    else e.target.blur();
  }
});

function openLeaderboardModal() {
  $("leaderboard-modal").hidden = false;
  void renderLeaderboardModal();
}

function closeLeaderboardModal() {
  $("leaderboard-modal").hidden = true;
}

function buildReadySectionHtml(modelsData, lbRows = [], cols = []) {
  const allInstalled = (modelsData && Array.isArray(modelsData.models)) ? modelsData.models : [];
  const readyModels = allInstalled.filter((m) => {
    if (m.archived || m.disabled || m.is_ghost) return false;
    const tps = Number(m.record_tokens_per_sec) || 0;
    return tps > 0;
  });
  if (readyModels.length === 0) return "";

  readyModels.sort((a, b) => (Number(b.record_tokens_per_sec) || 0) - (Number(a.record_tokens_per_sec) || 0));

  const rowMap = new Map((lbRows || []).map((r) => [r.model, r]));

  let pendingCount = 0;
  let evaluatedCount = 0;

  const cardsData = readyModels.map((m) => {
    const row = rowMap.get(m.name);
    const evaluated = row ? (row.evaluated || 0) : 0;
    let compatible = 0;
    if (row && typeof row.compatible === "number") {
      compatible = row.compatible;
    } else {
      const caps = new Set((m.capabilities || []).map((c) => String(c).toLowerCase()));
      compatible = (cols || []).filter((col) => {
        if ((col.activeTotal || 0) <= 0) return false;
        const req = col.requiredCaps || [];
        return !req.some((cap) => !caps.has(cap));
      }).length;
    }

    let status = "pending";
    if (evaluated > 0 && compatible > 0 && evaluated >= compatible) {
      status = "complete";
      evaluatedCount++;
    } else if (evaluated > 0) {
      status = "partial";
      evaluatedCount++;
    } else {
      status = "pending";
      pendingCount++;
    }
    return { model: m, evaluated, compatible, status };
  });

  const allCount = readyModels.length;
  const activeTab = pendingCount > 0 ? "pending" : "all";
  const q = (_lbModelFilter || "").trim().toLowerCase();

  let cardsHtml = "";
  let shownCards = 0;

  for (const { model: m, evaluated, compatible, status } of cardsData) {
    const tps = Number(m.record_tokens_per_sec) || 0;
    const rc = (typeof getToksRecordColor === "function") ? getToksRecordColor(tps) : "";

    let modelName = m.name;
    let modelDisplay = escapeHtml(modelName);
    if (modelName.startsWith("hf.co/")) {
      modelDisplay = `<span style="opacity:0.45;font-weight:normal;">hf.co/</span>${escapeHtml(modelName.slice(6))}`;
    }

    const pills = (typeof renderCapabilityPills === "function") ? renderCapabilityPills(m.capabilities) : "";

    const specs = [];
    if (m.details && m.details.parameter_size) {
      specs.push(`<span>${escapeHtml(m.details.parameter_size)}</span>`);
    }
    if (m.size) {
      specs.push(`<span>${fmtBytes(m.size)}</span>`);
    }
    if (m.context_length) {
      specs.push(`<span>${fmtCtx(m.context_length)} ctx</span>`);
    }
    if (m.record_cold_load_ms > 0) {
      specs.push(`<span title="${escapeHtml(t("models.cold_load_time") || "Cold load")}: ${fmtColdLoad(m.record_cold_load_ms)}">❄️ ${fmtColdLoad(m.record_cold_load_ms)}</span>`);
    }

    let statusBadge = "";
    let actionLabel = "";
    if (status === "complete") {
      statusBadge = `<span class="pill lb-ready-badge-complete">✅ ${escapeHtml(t("battery.lb_ready_status_complete", { done: evaluated, total: compatible }))}</span>`;
      actionLabel = `🧪 ${escapeHtml(t("battery.lb_ready_rerun_btn") || t("battery.lb_ready_run_btn"))}`;
    } else if (status === "partial") {
      statusBadge = `<span class="pill lb-ready-badge-partial">📊 ${escapeHtml(t("battery.lb_ready_status_partial", { done: evaluated, total: compatible }))}</span>`;
      actionLabel = `🧪 ${escapeHtml(t("battery.lb_ready_continue_btn"))}`;
    } else {
      statusBadge = `<span class="pill lb-ready-badge-pending">⏳ ${escapeHtml(t("battery.lb_ready_status_untested"))}</span>`;
      actionLabel = `🧪 ${escapeHtml(t("battery.lb_ready_run_btn"))}`;
    }

    const matchesSearch = !q || m.name.toLowerCase().includes(q);
    let matchesTab = true;
    if (activeTab === "pending") matchesTab = (status === "pending");
    else if (activeTab === "evaluated") matchesTab = (status === "complete" || status === "partial");

    const isVisible = matchesSearch && matchesTab;
    if (isVisible) shownCards++;

    cardsHtml += `
      <div class="lb-ready-card" data-lb-model="${escapeHtml(m.name)}" data-lb-ready-status="${status}"${isVisible ? "" : ` style="display:none;"`}>
        <div class="lb-ready-card-header">
          <div class="lb-ready-card-name-box">
            <strong class="lb-ready-card-name mono" title="${escapeHtml(m.name)}">${modelDisplay}</strong>
          </div>
          <div class="lb-ready-card-speed">
            <span class="pill" title="${tps.toFixed(1)} tok/s"${rc ? ` style="color:${rc};"` : ""}><strong>${tps.toFixed(1)}</strong> <span class="speed-unit">tok/s</span></span>
          </div>
        </div>
        <div class="lb-ready-card-meta">
          ${pills ? `<div class="lb-ready-card-caps">${pills}</div>` : ""}
          ${specs.length > 0 ? `<div class="lb-ready-card-specs muted mono">${specs.join("")}</div>` : ""}
        </div>
        <div class="lb-ready-card-footer">
          <div class="lb-ready-card-status">${statusBadge}</div>
          <div class="lb-ready-card-actions">
            <button type="button" class="btn btn-sm btn-primary" data-lb-bench-model="${escapeHtml(m.name)}" title="${escapeHtml(t("battery.lb_ready_run_btn"))}">${actionLabel}</button>
            <button type="button" class="btn btn-sm ghost" data-lb-chat="${escapeHtml(m.name)}" title="${escapeHtml(t("battery.lb_chat"))}">💬</button>
          </div>
        </div>
      </div>
    `;
  }

  const titleTxt = escapeHtml(t("battery.lb_ready_section_title"));
  const descTxt = escapeHtml(t("battery.lb_ready_section_desc"));
  const tabPendingTxt = escapeHtml(t("battery.lb_ready_tab_pending"));
  const tabAllTxt = escapeHtml(t("battery.lb_ready_tab_all"));
  const tabEvalTxt = escapeHtml(t("battery.lb_ready_tab_evaluated"));
  const emptyTxt = escapeHtml(t("battery.lb_ready_no_match"));

  return `
    <section class="lb-ready-section" data-active-tab="${activeTab}">
      <div class="lb-ready-section-head">
        <div class="lb-ready-title-area">
          <div class="lb-ready-heading">
            <span class="lb-ready-icon" aria-hidden="true">⚡</span>
            <h3 class="lb-ready-title">${titleTxt}</h3>
          </div>
          <p class="lb-ready-desc">${descTxt}</p>
        </div>
        <div class="lb-ready-tabs" role="tablist">
          <button type="button" class="lb-ready-tab${activeTab === "pending" ? " active" : ""}" data-lb-ready-tab="pending" role="tab" aria-selected="${activeTab === "pending"}">${tabPendingTxt} <span class="lb-tab-badge">${pendingCount}</span></button>
          <button type="button" class="lb-ready-tab${activeTab === "all" ? " active" : ""}" data-lb-ready-tab="all" role="tab" aria-selected="${activeTab === "all"}">${tabAllTxt} <span class="lb-tab-badge">${allCount}</span></button>
          <button type="button" class="lb-ready-tab${activeTab === "evaluated" ? " active" : ""}" data-lb-ready-tab="evaluated" role="tab" aria-selected="${activeTab === "evaluated"}">${tabEvalTxt} <span class="lb-tab-badge">${evaluatedCount}</span></button>
        </div>
      </div>
      <div class="lb-ready-grid">
        ${cardsHtml}
      </div>
      <div class="lb-ready-empty muted" style="${shownCards === 0 ? "display:block;" : "display:none;"}">${emptyTxt}</div>
    </section>
  `;
}

async function buildLeaderboardTableHtml() {
  const testsData = await api("/api/tests").catch(() => null);
  if (testsData && testsData.groups) {
    testsGroups = testsData.groups;
  }
  const allTests = (testsData && testsData.tests) || [];
  const activeCountByGroup = new Map();
  const activeMaxPointsByGroup = new Map();
  const activeCasesByGroup = new Map();
  for (const tst of allTests) {
    if (tst.active) {
      activeCountByGroup.set(tst.group_id, (activeCountByGroup.get(tst.group_id) || 0) + 1);
      const cCount = (tst.cases && tst.cases.length > 0) ? tst.cases.length : 1;
      activeCasesByGroup.set(tst.group_id, (activeCasesByGroup.get(tst.group_id) || 0) + cCount);
      activeMaxPointsByGroup.set(tst.group_id, (activeMaxPointsByGroup.get(tst.group_id) || 0) + (cCount + 1));
    }
  }

  // Load custom category order from localStorage or config
  let customOrder = null;
  try {
    const rawLocal = localStorage.getItem("leaderboard_group_order");
    if (rawLocal) customOrder = JSON.parse(rawLocal);
  } catch (_) {}
  if (!customOrder || !Array.isArray(customOrder) || customOrder.length === 0) {
    const cfg = await api("/api/config").catch(() => null);
    if (cfg && Array.isArray(cfg.leaderboard_group_order) && cfg.leaderboard_group_order.length > 0) {
      customOrder = cfg.leaderboard_group_order;
      try { localStorage.setItem("leaderboard_group_order", JSON.stringify(customOrder)); } catch (_) {}
    }
  }
  const orderMap = new Map();
  if (Array.isArray(customOrder)) {
    customOrder.forEach((id, idx) => orderMap.set(id, idx));
  }

  const groups = (testsGroups || []).slice().sort((a, b) => {
    if (orderMap.size > 0) {
      const idxA = orderMap.has(a.id) ? orderMap.get(a.id) : Number.MAX_SAFE_INTEGER;
      const idxB = orderMap.has(b.id) ? orderMap.get(b.id) : Number.MAX_SAFE_INTEGER;
      if (idxA !== idxB) return idxA - idxB;
    }
    const oa = typeof a.order === "number" ? a.order : Number.MAX_SAFE_INTEGER;
    const ob = typeof b.order === "number" ? b.order : Number.MAX_SAFE_INTEGER;
    if (oa !== ob) return oa - ob;
    return String(a.name || a.id).localeCompare(String(b.name || b.id));
  });

  const summaries = await Promise.all(
    groups.map((g) =>
      api("/api/runner/group-history/" + encodeURIComponent(g.id))
        .then((d) => d.summary || [])
        .catch(() => [])
    )
  );
  // Model metadata for the second line of the model cell (capabilities,
  // context, disk size, record speed). Best-effort: rows still render if
  // the model was deleted or the call fails.
  const modelsData = await api("/api/models").catch(() => null);
  const modelInfo = new Map(
    ((modelsData && modelsData.models) || []).map((m) => [m.name, m])
  );

  // Keep ALL groups as columns (even if no models evaluated yet).
  const cols = [];
  groups.forEach((g, i) => {
    const activeTotal = activeCountByGroup.get(g.id) || 0;
    const activeMaxPoints = activeMaxPointsByGroup.get(g.id) || 0;
    const activeCases = activeCasesByGroup.get(g.id) || 0;
    const requiredCaps = (g.required_caps || []).map((c) => String(c).toLowerCase());
    cols.push({ id: g.id, name: g.name || g.id, summary: summaries[i] || [], activeTotal, activeMaxPoints, activeCases, requiredCaps });
  });

  // Per model per group: last-run % (own denominator, no active penalty).
  const modelSet = new Set();
  const scores = {};
  for (const col of cols) {
    for (const s of col.summary) {
      modelSet.add(s.model);
      const hasLast = (s.last_run_max_points || 0) > 0 || (s.last_run_total_tests || 0) > 0;
      let passed, tested, passedCases, totalCases, pts, maxPts, score, unrun;
      if (hasLast) {
        passed = s.last_run_passed || 0;
        tested = s.last_run_total_tests || 0;
        passedCases = s.last_run_passed_cases || 0;
        totalCases = s.last_run_total_cases || 0;
        pts = s.last_run_points || 0;
        maxPts = s.last_run_max_points || 0;
        score = s.last_run_score != null ? s.last_run_score : (maxPts > 0 ? Math.min(100, Math.max(0, (pts / maxPts) * 100)) : null);
        unrun = 0;
      } else {
        passed = s.passed || 0;
        tested = s.total_tests || 0;
        passedCases = s.passed_cases != null ? s.passed_cases : passed;
        totalCases = s.total_cases != null ? s.total_cases : tested;
        pts = s.score_points != null ? s.score_points : (passed * 2);
        const activeTotal = Math.max(col.activeTotal, tested);
        const activeCases = Math.max(col.activeCases, totalCases);
        const activeMaxPoints = Math.max(col.activeMaxPoints, s.max_points || (activeTotal * 2));
        unrun = Math.max(0, col.activeTotal - tested);
        score = activeMaxPoints > 0 ? Math.min(100.0, Math.max(0.0, (pts / activeMaxPoints) * 100)) : (activeTotal > 0 ? (passed / activeTotal) * 100 : null);
        (scores[s.model] ||= {})[col.id] = {
          passed,
          tested,
          passedCases,
          totalCases,
          pts,
          activeTotal,
          activeCases,
          activeMaxPoints,
          unrun,
          score,
        };
        continue;
      }
      (scores[s.model] ||= {})[col.id] = {
        passed,
        tested,
        passedCases,
        totalCases,
        pts,
        activeTotal: tested,
        activeCases: totalCases,
        activeMaxPoints: maxPts,
        unrun,
        score,
      };
    }
  }

  if (cols.length === 0 || modelSet.size === 0) {
    const readySectionHtml = buildReadySectionHtml(modelsData, [], cols);
    return `<div class="battery-empty">${t("battery.no_history")}</div>${readySectionHtml}`;
  }

  const lbRows = Array.from(modelSet).map((m) => {
    let totalPassed = 0;
    let totalActive = 0;
    let totalPassedCases = 0;
    let totalActiveCases = 0;
    let totalPoints = 0;
    let totalMaxPoints = 0;
    for (const col of cols) {
      const c = scores[m]?.[col.id];
      if (c && c.activeMaxPoints > 0) {
        totalPassed += c.passed;
        totalActive += c.activeTotal;
        totalPassedCases += c.passedCases;
        totalActiveCases += c.activeCases;
        totalPoints += c.pts;
        totalMaxPoints += c.activeMaxPoints;
      }
    }
    const overall = totalMaxPoints > 0 ? Math.min(100.0, Math.max(0.0, (totalPoints / totalMaxPoints) * 100)) : null;
    return {
      model: m,
      passed: totalPassed,
      total: totalActive,
      passedCases: totalPassedCases,
      totalCases: totalActiveCases,
      points: totalPoints,
      maxPoints: totalMaxPoints,
      overall,
    };
  });
  // Sort happens below once coverage per model is known (needs lbLacksCaps).

  const colRange = {};
  for (const col of cols) {
    const vals = lbRows
      .map((row) => {
        const c = scores[row.model]?.[col.id];
        return c && typeof c.score === "number" ? c.score : null;
      })
      .filter((v) => v != null);
    colRange[col.id] = {
      min: vals.length ? Math.min(...vals) : 0,
      max: vals.length ? Math.max(...vals) : 0,
      count: vals.length,
    };
  }
  const overallVals = lbRows.map((r) => r.overall).filter((v) => v != null);
  const overallRange = {
    min: overallVals.length ? Math.min(...overallVals) : 0,
    max: overallVals.length ? Math.max(...overallVals) : 0,
    count: overallVals.length,
  };

  let headerCols = `<th class="cell-lb-overall-head">${t("battery.leaderboard_overall")}</th>`;
  for (const col of cols) {
    const capSet = new Set(col.requiredCaps || []);
    const capBadges = `${capSet.has("vision") ? " 👁️" : ""}${capSet.has("audio") ? " 🔊" : ""}`;
    const capSuffix = (col.requiredCaps && col.requiredCaps.length > 0)
      ? ` (${t("tests.required_caps")}: ${col.requiredCaps.join(", ")})`
      : "";
    headerCols += `<th class="cell-lb-group-head" data-lb-run data-lb-run-group="${escapeHtml(col.id)}" data-lb-run-model="" title="${escapeHtml(col.name)}${escapeHtml(capSuffix)} · ${escapeHtml(t("battery.lb_run_hint"))}">${escapeHtml(col.name)}${capBadges}</th>`;
  }

  const runHint = t("battery.lb_run_hint");
  // Short labels reused as card row labels on mobile (see CSS attr(data-lb-col)).
  const overallColLabel = escapeHtml(t("battery.leaderboard_overall"));
  // Model capabilities (lowercased) for the ✕-vs-— decision below.
  const lbModelCaps = new Map();
  for (const [name, info] of modelInfo) {
    lbModelCaps.set(name, new Set((info?.capabilities || []).map((c) => String(c).toLowerCase())));
  }
  const lbLacksCaps = (model, col) => {
    const req = col.requiredCaps || [];
    if (req.length === 0) return [];
    const caps = lbModelCaps.get(model) || new Set();
    return req.filter((c) => !caps.has(c));
  };
  // Coverage per model: evaluated categories over compatible ones (only
  // groups with active tests count). Uninstalled models keep their cached
  // results but their caps are unknown, so they count as compatible —
  // same rule the cells use (— instead of ✕). A 0.0 score still counts
  // as evaluated; only missing (—/✕) doesn't.
  const lbActiveCols = cols.filter((col) => (col.activeTotal || 0) > 0);
  for (const row of lbRows) {
    const installed = modelInfo.has(row.model);
    let compatible = 0;
    let evaluated = 0;
    for (const col of lbActiveCols) {
      const lacking = installed ? lbLacksCaps(row.model, col) : [];
      if (lacking.length > 0) continue;
      compatible++;
      const c = scores[row.model]?.[col.id];
      if (c && c.score != null) evaluated++;
    }
    row.compatible = compatible;
    row.evaluated = evaluated;
    row.coverage = compatible > 0 ? evaluated / compatible : 0;
  }
  if (_lbSortMode === "overall") {
    lbRows.sort((a, b) => (b.overall ?? -1) - (a.overall ?? -1) || (b.points ?? 0) - (a.points ?? 0) || b.total - a.total);
  } else {
    // Default: most evaluated categories first, then coverage ratio (a
    // model compatible with fewer categories isn't penalized), then score.
    lbRows.sort((a, b) => (b.evaluated ?? 0) - (a.evaluated ?? 0)
      || (b.coverage ?? 0) - (a.coverage ?? 0)
      || (b.overall ?? -1) - (a.overall ?? -1)
      || (b.points ?? 0) - (a.points ?? 0)
      || b.total - a.total);
  }
  let bodyRows = "";
  // Missing categories per model: no result (—/✕) or 0.0 score, limited to
  // groups with active tests AND compatible with the model (incompatible
  // ones show ✕ and are skipped by the runner, so bench-missing excludes
  // them). Cached globally so the row-action button can open the battery
  // modal with exactly those categories preselected.
  const missingByModel = {};
  for (const row of lbRows) {
    missingByModel[row.model] = cols
      .filter((col) => {
        if ((col.activeTotal || 0) <= 0) return false;
        if (lbLacksCaps(row.model, col).length > 0) return false;
        const c = scores[row.model]?.[col.id];
        return !c || c.score == null || c.score <= 0.0001;
      })
      .map((col) => col.id);
  }
  window._lbMissingByModel = missingByModel;
  const lbFilterQ = (_lbModelFilter || "").trim().toLowerCase();
  lbRows.forEach((row, idx) => {
    let cells = "";
    const installed = modelInfo.has(row.model);
    // Uninstalled models keep their results visible but cannot be run.
    const runModelAttr = installed ? `data-lb-run data-lb-run-model="${escapeHtml(row.model)}"` : "";
    const hintSuffix = installed ? ` · ${escapeHtml(runHint)}` : "";
    if (row.overall == null) {
      cells += `<td class="cell-lb-score cell-lb-overall cell-lb-empty" data-lb-col="${overallColLabel}" ${runModelAttr} data-lb-run-group=""${installed ? ` title="${escapeHtml(runHint)}"` : ""}><span class="muted">—</span></td>`;
    } else {
      const overallScoreFormatted = row.overall.toFixed(1);
      const overallTooltip = `${row.passed}/${row.total} tests (${row.passedCases}/${row.totalCases} ${t("battery.cases") || "cases"}, ${row.points.toFixed(1)}/${row.maxPoints.toFixed(1)} ${t("battery.points") || "pts"}) · ${overallScoreFormatted}%${hintSuffix}`;
      cells += `<td class="cell-lb-score cell-lb-overall mono" data-lb-col="${overallColLabel}" ${runModelAttr} data-lb-run-group="" style="${batteryLbHeatStyle(row.overall, overallRange, true)}" title="${escapeHtml(overallTooltip)}">${overallScoreFormatted}</td>`;
    }
    for (const col of cols) {
      const c = scores[row.model]?.[col.id];
      const colLabel = escapeHtml(col.name);
      if (!c || c.score == null) {
        const lacking = installed ? lbLacksCaps(row.model, col) : [];
        if (lacking.length > 0) {
          const missTip = `${t("battery.lb_missing_cap", { caps: lacking.join(", ") })}${hintSuffix}`;
          cells += `<td class="cell-lb-score cell-lb-empty" data-lb-col="${colLabel}" data-lb-incompatible="${escapeHtml(row.model)}" data-lb-incompatible-caps="${escapeHtml(lacking.join(", "))}" title="${escapeHtml(missTip)}"><span class="muted">✕</span></td>`;
          continue;
        }
        cells += `<td class="cell-lb-score cell-lb-empty" data-lb-col="${colLabel}" ${runModelAttr} data-lb-run-group="${escapeHtml(col.id)}"${installed ? ` title="${escapeHtml(runHint)}"` : ""}><span class="muted">—</span></td>`;
        continue;
      }
      const score = c.score;
      const scoreFormatted = score.toFixed(1);
      const pendingText = c.unrun > 0 ? ` (${c.unrun} ${t("battery.pending_tests") || "unrun"})` : "";
      const scoreTooltip = `${c.passed}/${c.activeTotal} tests (${c.passedCases}/${c.activeCases} ${t("battery.cases") || "cases"}, ${c.pts.toFixed(1)}/${c.activeMaxPoints.toFixed(1)} ${t("battery.points") || "pts"})${pendingText} · ${scoreFormatted}%${hintSuffix}`;
      cells += `<td class="cell-lb-score mono" data-lb-col="${colLabel}" ${runModelAttr} data-lb-run-group="${escapeHtml(col.id)}" style="${batteryLbHeatStyle(score, colRange[col.id], false)}" title="${escapeHtml(scoreTooltip)}">${scoreFormatted}</td>`;
    }

    let modelName = row.model;
    let modelDisplay = escapeHtml(modelName);
    if (modelName.startsWith("hf.co/")) {
      modelDisplay = `<span style="opacity:0.45;font-weight:normal;">hf.co/</span>${escapeHtml(modelName.slice(6))}`;
    }

    const info = modelInfo.get(row.model);
    const lbCovTitle = escapeHtml(lbFilterText("battery.leaderboard_coverage_title", "Categories evaluated: {done}/{total}", { done: row.evaluated || 0, total: row.compatible || 0 }));
    const lbCovHtml = `<span class="lb-coverage" title="${lbCovTitle}">📊 ${row.evaluated || 0}/${row.compatible || 0}</span>`;
    let metaHtml = "";
    if (!installed) {
      metaHtml = `<div class="lb-model-meta mono">${lbCovHtml}<span class="lb-meta-sep">·</span><span class="pill lb-uninstalled-tag">${escapeHtml(t("battery.lb_not_installed_tag"))}</span></div>`;
    } else if (info) {
      const pills = (typeof renderCapabilityPills === "function") ? renderCapabilityPills(info.capabilities) : "";
      const parts = [lbCovHtml];
      if (Number(info.context_length) > 0) {
        parts.push(`<span title="${escapeHtml(t("detail.context"))}">ctx ${escapeHtml(fmtCtx(Number(info.context_length)))}</span>`);
      }
      if (Number(info.size) > 0) {
        parts.push(`<span title="${escapeHtml(t("col.size"))}">${escapeHtml(fmtBytes(Number(info.size)))}</span>`);
      }
      const rec = Number(info.record_tokens_per_sec) || 0;
      if (rec > 0) {
        const rc = (typeof getToksRecordColor === "function") ? getToksRecordColor(rec) : "";
        parts.push(`<span title="${rec.toFixed(1)} tok/s"${rc ? ` style="color:${rc};"` : ""}>${rec.toFixed(1)} <span class="speed-unit">tok/s</span></span>`);
      }
      metaHtml = `<div class="lb-model-meta muted mono">${pills}${pills ? `<span class="lb-meta-sep">·</span>` : ""}${parts.join(`<span class="lb-meta-sep">·</span>`)}</div>`;
    } else {
      metaHtml = `<div class="lb-model-meta muted mono">${lbCovHtml}</div>`;
    }

    bodyRows += `
      <tr class="${idx === 0 ? "lb-row-first" : ""}${installed ? "" : " lb-row-uninstalled"}" data-lb-model="${escapeHtml(row.model)}"${installed ? "" : ` data-lb-uninstalled="1"`}${lbFilterQ && !row.model.toLowerCase().includes(lbFilterQ) ? ` style="display:none"` : ""}>
        <td class="cell-lb-model">
          <div class="lb-model-top"><span class="lb-rank">${idx + 1}</span><strong class="lb-model-name mono" title="${escapeHtml(row.model)}">${modelDisplay}</strong>
            <span class="lb-row-actions">${installed ? `<button type="button" class="ghost lb-row-btn" data-lb-chat="${escapeHtml(row.model)}" title="${escapeHtml(t("battery.lb_chat"))}">💬</button>` : ""}${installed && (missingByModel[row.model] || []).length > 0 ? `<button type="button" class="ghost lb-row-btn" data-lb-bench-missing="${escapeHtml(row.model)}" title="${escapeHtml(t("battery.lb_bench_missing"))}">🧪</button>` : ""}${row.total > 0 ? `<button type="button" class="ghost lb-row-btn danger-text" data-lb-reset-model="${escapeHtml(row.model)}" title="${escapeHtml(t("battery.lb_reset_model"))}">🧹</button>` : ""}</span>
          </div>
          ${metaHtml}
        </td>
        ${cells}
      </tr>
    `;
  });

  const lbTotal = lbRows.length;
  const lbShown = lbFilterQ ? lbRows.filter((r) => r.model.toLowerCase().includes(lbFilterQ)).length : lbTotal;
  const lbFilterVal = escapeHtml(_lbModelFilter || "");
  const lbFilterPlaceholder = escapeHtml(lbFilterText("battery.leaderboard_filter_placeholder", "Filter models… ( / )"));
  const lbFilterCount = escapeHtml(lbFilterText("battery.leaderboard_filter_count", "{shown} / {total}", { shown: lbShown, total: lbTotal }));
  const lbFilterClearTitle = escapeHtml(lbFilterText("battery.leaderboard_filter_clear", "Clear"));
  const lbFilterEmptyTxt = escapeHtml(lbFilterText("battery.leaderboard_filter_empty", "No models match this filter."));
  const lbIsCoverage = _lbSortMode !== "overall";
  const lbSortLabel = escapeHtml(lbIsCoverage
    ? lbFilterText("battery.leaderboard_sort_coverage", "📊 Coverage")
    : lbFilterText("battery.leaderboard_sort_overall", "🏆 Overall"));
  const lbSortTitle = escapeHtml(lbFilterText("battery.leaderboard_sort_title", "Toggle leaderboard sort: coverage first vs overall score"));
  const lbFilterBar = `
    <div class="lb-filter-bar">
      <span class="lb-filter-icon" aria-hidden="true">🔍</span>
      <input type="search" class="lb-filter-input" placeholder="${lbFilterPlaceholder}" value="${lbFilterVal}" autocomplete="off" spellcheck="false" aria-label="${lbFilterPlaceholder}">
      <button type="button" class="ghost lb-filter-clear" title="${lbFilterClearTitle}"${_lbModelFilter ? "" : " hidden"}>✕</button>
      <button type="button" class="ghost lb-sort-btn${lbIsCoverage ? " active" : ""}" title="${lbSortTitle}">${lbSortLabel}</button>
      <span class="lb-filter-count muted mono"${lbTotal > 0 ? "" : ` style="display:none"`}>${lbFilterCount}</span>
    </div>
  `;

  return `
    ${lbFilterBar}
    <div class="battery-table-wrap battery-lb-wrap">
      <table class="battery-table battery-lb-table">
        <thead>
          <tr>
            <th class="cell-lb-model-head">${t("chat.model")}</th>
            ${headerCols}
          </tr>
        </thead>
        <tbody>${bodyRows}</tbody>
      </table>
    </div>
    <div class="lb-filter-empty muted"${lbTotal > 0 && lbShown === 0 ? "" : " hidden"}>${lbFilterEmptyTxt}</div>
    ${buildReadySectionHtml(modelsData, lbRows, cols)}
  `;
}

async function renderLeaderboardModal() {
  const body = $("leaderboard-modal-body");
  if (!body) return;
  body.innerHTML = `<div class="muted" style="padding:16px;">${t("status.loading")}</div>`;
  try {
    const html = await buildLeaderboardTableHtml();
    body.innerHTML = html;
  } catch (err) {
    body.innerHTML = `<div class="muted" style="padding:16px;">${escapeHtml(err.message)}</div>`;
  }
}

async function renderLeaderboardPage() {
  const body = $("battery-leaderboard-page-body");
  if (!body) return;
  body.innerHTML = `<div class="muted" style="padding:32px;text-align:center;">${t("status.loading")}</div>`;
  try {
    const html = await buildLeaderboardTableHtml();
    body.innerHTML = html;
  } catch (err) {
    body.innerHTML = `<div class="muted" style="padding:32px;">${escapeHtml(err.message)}</div>`;
  }
}

function showLeaderboardView() {
  hideAllMainViews();
  currentView = "leaderboard";
  $("battery-leaderboard-view").hidden = false;
  if (window.location.pathname !== "/leaderboard") {
    history.pushState(null, "", "/leaderboard");
  }
  void renderLeaderboardPage();
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

$("leaderboard-modal")?.addEventListener("click", (e) => {
  if (e.target === $("leaderboard-modal")) closeLeaderboardModal();
});
$("leaderboard-modal-close")?.addEventListener("click", closeLeaderboardModal);
$("leaderboard-modal-done")?.addEventListener("click", closeLeaderboardModal);
$("leaderboard-modal-expand")?.addEventListener("click", () => {
  closeLeaderboardModal();
  showLeaderboardView();
});
$("leaderboard-modal-open-page")?.addEventListener("click", () => {
  closeLeaderboardModal();
  showLeaderboardView();
});
$("battery-leaderboard-back")?.addEventListener("click", () => {
  showTestsView();
});
$("battery-leaderboard-refresh")?.addEventListener("click", () => {
  void renderLeaderboardPage();
});

// Category reorder modal for Leaderboard
async function openLeaderboardOrderModal() {
  const modal = $("leaderboard-order-modal");
  const listEl = $("leaderboard-order-list");
  if (!modal || !listEl) return;

  if (!testsGroups || testsGroups.length === 0) {
    const testsData = await api("/api/tests").catch(() => null);
    if (testsData && testsData.groups) {
      testsGroups = testsData.groups;
    }
  }

  let customOrder = null;
  try {
    const rawLocal = localStorage.getItem("leaderboard_group_order");
    if (rawLocal) customOrder = JSON.parse(rawLocal);
  } catch (_) {}
  if (!customOrder || !Array.isArray(customOrder) || customOrder.length === 0) {
    const cfg = await api("/api/config").catch(() => null);
    if (cfg && Array.isArray(cfg.leaderboard_group_order) && cfg.leaderboard_group_order.length > 0) {
      customOrder = cfg.leaderboard_group_order;
      try { localStorage.setItem("leaderboard_group_order", JSON.stringify(customOrder)); } catch (_) {}
    }
  }

  const orderMap = new Map();
  if (Array.isArray(customOrder)) {
    customOrder.forEach((id, idx) => orderMap.set(id, idx));
  }

  const sortedGroups = (testsGroups || []).slice().sort((a, b) => {
    if (orderMap.size > 0) {
      const idxA = orderMap.has(a.id) ? orderMap.get(a.id) : Number.MAX_SAFE_INTEGER;
      const idxB = orderMap.has(b.id) ? orderMap.get(b.id) : Number.MAX_SAFE_INTEGER;
      if (idxA !== idxB) return idxA - idxB;
    }
    const oa = typeof a.order === "number" ? a.order : Number.MAX_SAFE_INTEGER;
    const ob = typeof b.order === "number" ? b.order : Number.MAX_SAFE_INTEGER;
    if (oa !== ob) return oa - ob;
    return String(a.name || a.id).localeCompare(String(b.name || b.id));
  });

  renderLeaderboardOrderItems(sortedGroups);
  modal.hidden = false;
}

function renderLeaderboardOrderItems(groups) {
  const listEl = $("leaderboard-order-list");
  if (!listEl) return;

  const lockedTxt = t("battery.reorder_overall_locked") || "Always first";
  let html = `
    <div class="lb-order-item lb-order-fixed">
      <span class="lb-order-badge">1</span>
      <span class="lb-order-name mono" style="font-weight:700; color:var(--accent);">🔒 ${t("battery.leaderboard_overall") || "Overall"}</span>
      <span class="muted" style="font-size:11px; margin-left:auto;">(${escapeHtml(lockedTxt)})</span>
    </div>
  `;

  groups.forEach((g, idx) => {
    const isFirst = idx === 0;
    const isLast = idx === groups.length - 1;
    html += `
      <div class="lb-order-item" data-group-id="${escapeHtml(g.id)}">
        <span class="lb-order-badge">${idx + 2}</span>
        <span class="lb-order-name mono">${escapeHtml(g.name || g.id)}</span>
        <div class="lb-order-arrows">
          <button type="button" class="btn-icon lb-order-btn-up" title="Move Up" ${isFirst ? "disabled" : ""}>▲</button>
          <button type="button" class="btn-icon lb-order-btn-down" title="Move Down" ${isLast ? "disabled" : ""}>▼</button>
        </div>
      </div>
    `;
  });

  listEl.innerHTML = html;
}

function closeLeaderboardOrderModal() {
  const modal = $("leaderboard-order-modal");
  if (modal) modal.hidden = true;
}

function updateLeaderboardOrderBadgesAndButtons() {
  const listEl = $("leaderboard-order-list");
  if (!listEl) return;
  const items = Array.from(listEl.querySelectorAll(".lb-order-item[data-group-id]"));
  items.forEach((item, idx) => {
    const badge = item.querySelector(".lb-order-badge");
    if (badge) badge.textContent = String(idx + 2);
    const upBtn = item.querySelector(".lb-order-btn-up");
    if (upBtn) upBtn.disabled = idx === 0;
    const downBtn = item.querySelector(".lb-order-btn-down");
    if (downBtn) downBtn.disabled = idx === items.length - 1;
  });
}

$("leaderboard-order-modal")?.addEventListener("click", (e) => {
  if (e.target === $("leaderboard-order-modal")) closeLeaderboardOrderModal();
});
$("leaderboard-order-modal-close")?.addEventListener("click", closeLeaderboardOrderModal);
$("leaderboard-order-cancel")?.addEventListener("click", closeLeaderboardOrderModal);

$("leaderboard-order-reset")?.addEventListener("click", () => {
  const defaultSorted = (testsGroups || []).slice().sort((a, b) => {
    const oa = typeof a.order === "number" ? a.order : Number.MAX_SAFE_INTEGER;
    const ob = typeof b.order === "number" ? b.order : Number.MAX_SAFE_INTEGER;
    if (oa !== ob) return oa - ob;
    return String(a.name || a.id).localeCompare(String(b.name || b.id));
  });
  renderLeaderboardOrderItems(defaultSorted);
});

$("leaderboard-order-save")?.addEventListener("click", async () => {
  const listEl = $("leaderboard-order-list");
  if (!listEl) return;
  const items = Array.from(listEl.querySelectorAll(".lb-order-item[data-group-id]"));
  const newOrder = items.map((el) => el.dataset.groupId);
  try {
    localStorage.setItem("leaderboard_group_order", JSON.stringify(newOrder));
    await api("/api/config", {
      method: "PATCH",
      body: JSON.stringify({ leaderboard_group_order: newOrder })
    }).catch(console.error);
    toast(t("toast.categories_order_saved") || "Category order saved", "success");
    closeLeaderboardOrderModal();
    if ($("leaderboard-modal") && !$("leaderboard-modal").hidden) {
      await renderLeaderboardModal();
    }
    if ($("battery-leaderboard-view") && !$("battery-leaderboard-view").hidden) {
      await renderLeaderboardPage();
    }
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
});

$("leaderboard-order-list")?.addEventListener("click", (e) => {
  const upBtn = e.target.closest(".lb-order-btn-up");
  if (upBtn) {
    const item = upBtn.closest(".lb-order-item[data-group-id]");
    if (item && item.previousElementSibling && item.previousElementSibling.dataset.groupId) {
      item.parentNode.insertBefore(item, item.previousElementSibling);
      updateLeaderboardOrderBadgesAndButtons();
    }
    return;
  }
  const downBtn = e.target.closest(".lb-order-btn-down");
  if (downBtn) {
    const item = downBtn.closest(".lb-order-item[data-group-id]");
    if (item && item.nextElementSibling) {
      item.parentNode.insertBefore(item.nextElementSibling, item);
      updateLeaderboardOrderBadgesAndButtons();
    }
    return;
  }
});

$("battery-leaderboard-order-btn")?.addEventListener("click", () => {
  void openLeaderboardOrderModal();
});
$("leaderboard-modal-order-btn")?.addEventListener("click", () => {
  void openLeaderboardOrderModal();
});

// Reset all battery results for one model (the model itself is kept).
async function resetLeaderboardModelHistory(name) {
  if (!name) return;
  const ok = await askConfirm({
    title: t("battery.lb_reset_model_title"),
    text: t("battery.lb_reset_model_text", { name }),
    okText: t("action.delete"),
    okClass: "danger",
  });
  if (!ok.ok) return;
  try {
    await api("/api/runner/model-history/" + encodeURIComponent(name), { method: "DELETE" });
    toast(t("toast.model_history_deleted", { name }), "info");
    if ($("leaderboard-modal") && !$("leaderboard-modal").hidden) {
      await renderLeaderboardModal();
    }
    if ($("battery-leaderboard-view") && !$("battery-leaderboard-view").hidden) {
      await renderLeaderboardPage();
    }
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

// Click a leaderboard cell to run it: a score/empty cell opens the battery
// modal with that category + model preselected (overall column = all
// categories for the model, category header = that category). Delegated so
// it survives table rebuilds and works in the modal too.
document.addEventListener("click", (e) => {
  // Row actions: chat with the model, or clear its test results.
  const chatBtn = e.target?.closest?.("[data-lb-chat]");
  if (chatBtn) {
    const name = chatBtn.dataset.lbChat;
    if ($("leaderboard-modal") && !$("leaderboard-modal").hidden) {
      closeLeaderboardModal();
    }
    if (typeof showChatViewWithModel === "function") {
      void showChatViewWithModel(name);
    }
    return;
  }
  const resetBtn = e.target?.closest?.("[data-lb-reset-model]");
  if (resetBtn) {
    void resetLeaderboardModelHistory(resetBtn.dataset.lbResetModel);
    return;
  }
  // Bench pending categories: opens the battery modal with only the
  // runnable categories this model is missing (— or 0.0) preselected, and
  // only this model selected on the next step (via initialModel).
  const benchBtn = e.target?.closest?.("[data-lb-bench-missing]");
  if (benchBtn) {
    const name = benchBtn.dataset.lbBenchMissing;
    const missing = (window._lbMissingByModel && window._lbMissingByModel[name]) || [];
    if (!missing || missing.length === 0) {
      toast(t("battery.lb_no_missing"), "info");
      return;
    }
    if ($("leaderboard-modal") && !$("leaderboard-modal").hidden) {
      closeLeaderboardModal();
    }
    void openBatteryModal({ groupIds: missing, initialModel: name });
    return;
  }
  // Ready models tab switching
  const readyTab = e.target?.closest?.("[data-lb-ready-tab]");
  if (readyTab) {
    const sec = readyTab.closest(".lb-ready-section");
    if (sec) {
      const tabName = readyTab.dataset.lbReadyTab;
      sec.dataset.activeTab = tabName;
      sec.querySelectorAll(".lb-ready-tab").forEach((btn) => {
        const isActive = btn === readyTab;
        btn.classList.toggle("active", isActive);
        btn.setAttribute("aria-selected", isActive ? "true" : "false");
      });
      filterReadyCards(sec);
    }
    return;
  }
  // Ready models bench action: launches runner modal preselected with this model
  const benchModelBtn = e.target?.closest?.("[data-lb-bench-model]");
  if (benchModelBtn) {
    const name = benchModelBtn.dataset.lbBenchModel;
    const missing = (window._lbMissingByModel && window._lbMissingByModel[name]) || [];
    if ($("leaderboard-modal") && !$("leaderboard-modal").hidden) {
      closeLeaderboardModal();
    }
    if (missing && missing.length > 0) {
      void openBatteryModal({ groupIds: missing, initialModel: name });
    } else {
      void openBatteryModal({ initialModel: name });
    }
    return;
  }
  // Incompatible (✕) cells: the model lacks a capability the category
  // requires, so running it would skip every test. Explain instead.
  const incompatCell = e.target?.closest?.("[data-lb-incompatible]");
  if (incompatCell) {
    toast(t("battery.lb_missing_cap", { caps: incompatCell.dataset.lbIncompatibleCaps || "?" }), "warn");
    return;
  }
  // Uninstalled models: results stay visible but cannot be run.
  const lockedCell = e.target?.closest?.("tr[data-lb-uninstalled] td.cell-lb-score");
  if (lockedCell) {
    const row = lockedCell.closest("tr[data-lb-uninstalled]");
    toast(t("battery.lb_not_installed", { name: row?.dataset?.lbModel || "?" }), "warn");
    return;
  }
  const cell = e.target?.closest?.("[data-lb-run]");
  if (!cell) return;
  const groupId = cell.dataset.lbRunGroup || null;
  const model = cell.dataset.lbRunModel || null;
  if (!groupId && !model) return;
  if ($("leaderboard-modal") && !$("leaderboard-modal").hidden) {
    closeLeaderboardModal();
  }
  void openBatteryModal({ groupId: groupId || "all", initialModel: model || undefined });
});

$("human-review-modal")?.addEventListener("click", (e) => {
  if (e.target === $("human-review-modal")) closeHumanReviewModal();
});
$("human-review-modal-close")?.addEventListener("click", closeHumanReviewModal);
$("human-review-modal-done")?.addEventListener("click", closeHumanReviewModal);

$("response-view-modal")?.addEventListener("click", (e) => {
  if (e.target === $("response-view-modal")) closeResponseViewModal();
});
$("response-view-modal-close")?.addEventListener("click", closeResponseViewModal);

// NOTE: tests-group-history-btn / tests-run-battery-btn are wired once in
// renderTestsList (app-svg.js) with a dataset.wired guard — do not add
// duplicate listeners here.
$("battery-modal")?.addEventListener("click", (e) => {
  if (e.target === $("battery-modal")) closeBatteryModal();
});
$("battery-modal-close")?.addEventListener("click", closeBatteryModal);
$("battery-modal-cancel")?.addEventListener("click", closeBatteryModal);
$("battery-modal-confirm")?.addEventListener("click", () => {
  batteryModalConfirm();
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
$("battery-review-back")?.addEventListener("click", () => {
  showTestsView();
});
$("battery-review-later")?.addEventListener("click", () => {
  if (!blindReviewRunId) {
    showTestsView();
    return;
  }
  // Ratings so far are already saved server-side; force a refetch so the
  // results reflect the recomputed scores.
  currentBatteryRun = null;
  showBatteryResultsView(blindReviewRunId);
});
// Blind triage shortcuts: ← fail, → pass. Ratings can be changed later in
// the results view, so a stray keypress is harmless.
document.addEventListener("keydown", (e) => {
  if (typeof currentView === "undefined" || currentView !== "battery-review") return;
  if ($("battery-review-view")?.hidden) return;
  const tag = e.target?.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
  if (e.key === "ArrowLeft") {
    e.preventDefault();
    void rateBlindReview(false);
  } else if (e.key === "ArrowRight") {
    e.preventDefault();
    void rateBlindReview(true);
  }
});
$("battery-progress-abort")?.addEventListener("click", () => {
  openBatteryAbortModal();
});
$("battery-abort-keep")?.addEventListener("click", () => {
  closeBatteryAbortModal();
});
$("battery-abort-discard")?.addEventListener("click", () => {
  void abortBatteryRun("discard");
});
$("battery-abort-save")?.addEventListener("click", () => {
  void abortBatteryRun("save-completed");
});
$("battery-abort-modal")?.addEventListener("click", (e) => {
  if (e.target === $("battery-abort-modal")) closeBatteryAbortModal();
});
$("battery-progress-skip-model")?.addEventListener("click", () => {
  void skipModelBatteryTest();
});
$("battery-progress-retry")?.addEventListener("click", () => {
  void retryCurrentBatteryTest();
});
$("battery-progress-skip")?.addEventListener("click", () => {
  void skipCurrentBatteryTest();
});

document.addEventListener("visibilitychange", () => {
  if (!document.hidden && currentView === "battery-progress") {
    const saved = localStorage.getItem(BATTERY_KEY);
    if (saved) {
      try {
        const data = JSON.parse(saved);
        if (data.runID) {
          stopBatteryPolling();
          batteryActiveRunID = data.runID;
          pollBatteryProgress(data.runID, data.modelIDs || []);
        }
      } catch { }
    }
  }
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

window.I18n.setLang(window.I18n.getLang()); // applied immediately; refreshStatus may overwrite.
bindModelsSearchEvents();
refreshStatus();
refreshModels().then(() => handleRouting());
connectJobsStream();
bindChatEvents();
bindAnalyticsFilters();
bindAnalyticsMetaSearch();
updateStreamBar();
syncChatModelOptions();
updateChatCapabilityUI();
updateChatContextMeter();
updateChatSendEnabled();
setInterval(refreshStatus, STATUS_REFRESH_MS);
setInterval(() => {
  const modal = $("downloads-modal");
  if (modal && !modal.hidden) renderDownloads();
}, 60_000);
