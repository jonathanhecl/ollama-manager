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
      const pts = s.score_points != null ? s.score_points : (s.passed * 2);
      const passedCases = s.passed_cases != null ? s.passed_cases : s.passed;
      const totalCases = s.total_cases != null ? s.total_cases : s.total_tests;
      const denom = Math.max(activeMaxPoints, s.max_points || (s.total_tests ? s.total_tests * 2 : 0));
      const scorePct = denom > 0 ? Math.min(100.0, Math.max(0.0, (pts / denom) * 100)) : 0;
      const unrun = Math.max(0, activeTotal - (s.total_tests || 0));
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
      rows += `
        <tr>
          <td class="cell-model">${escapeHtml(s.model)}</td>
          <td class="cell-time">
            ${activeTotal > 0 ? `${s.total_tests} / ${activeTotal}${unrunBadge}` : s.total_tests}
            <div class="muted" style="font-size:11px">${passedCases} / ${activeCases || totalCases} ${t("battery.cases") || "cases"}</div>
          </td>
          <td>
            <span class="badge badge-pass" title="${passTooltip}">${s.passed}</span>
            <span class="badge badge-fail" title="${failTooltip}">${s.failed}</span>
            ${s.human_review > 0 ? `<span class="badge badge-human" title="${humanTooltip}">${s.human_review}</span>` : ""}
            ${s.errors > 0 ? `<span class="badge badge-na" title="${errorTooltip || t("battery.error_count")}">${s.errors}</span>` : ""}
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

function openLeaderboardModal() {
  $("leaderboard-modal").hidden = false;
  void renderLeaderboardModal();
}

function closeLeaderboardModal() {
  $("leaderboard-modal").hidden = true;
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

  // Per model per group: { passed, tested, passedCases, totalCases, pts, activeTotal, activeCases, activeMaxPoints, unrun, score }.
  const modelSet = new Set();
  const scores = {};
  for (const col of cols) {
    for (const s of col.summary) {
      modelSet.add(s.model);
      const passed = s.passed || 0;
      const tested = s.total_tests || 0;
      const passedCases = s.passed_cases != null ? s.passed_cases : passed;
      const totalCases = s.total_cases != null ? s.total_cases : tested;
      const pts = s.score_points != null ? s.score_points : (passed * 2);
      const activeTotal = Math.max(col.activeTotal, tested);
      const activeCases = Math.max(col.activeCases, totalCases);
      const activeMaxPoints = Math.max(col.activeMaxPoints, s.max_points || (activeTotal * 2));
      const unrun = Math.max(0, col.activeTotal - tested);
      const score = activeMaxPoints > 0 ? Math.min(100.0, Math.max(0.0, (pts / activeMaxPoints) * 100)) : (activeTotal > 0 ? (passed / activeTotal) * 100 : null);
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
    }
  }

  if (cols.length === 0 || modelSet.size === 0) {
    return `<div class="battery-empty">${t("battery.no_history")}</div>`;
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
  lbRows.sort((a, b) => (b.overall ?? -1) - (a.overall ?? -1) || (b.points ?? 0) - (a.points ?? 0) || b.total - a.total);

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
  lbRows.forEach((row, idx) => {
    let cells = "";
    const installed = modelInfo.has(row.model);
    // Uninstalled models keep their results visible but cannot be run.
    const runModelAttr = installed ? `data-lb-run data-lb-run-model="${escapeHtml(row.model)}"` : "";
    const hintSuffix = installed ? ` · ${escapeHtml(runHint)}` : "";
    if (row.overall == null) {
      cells += `<td class="cell-lb-score cell-lb-overall cell-lb-empty" ${runModelAttr} data-lb-run-group=""${installed ? ` title="${escapeHtml(runHint)}"` : ""}><span class="muted">—</span></td>`;
    } else {
      const overallScoreFormatted = row.overall.toFixed(1);
      const overallTooltip = `${row.passed}/${row.total} tests (${row.passedCases}/${row.totalCases} ${t("battery.cases") || "cases"}, ${row.points.toFixed(1)}/${row.maxPoints.toFixed(1)} ${t("battery.points") || "pts"}) · ${overallScoreFormatted}%${hintSuffix}`;
      cells += `<td class="cell-lb-score cell-lb-overall mono" ${runModelAttr} data-lb-run-group="" style="${batteryLbHeatStyle(row.overall, overallRange, true)}" title="${escapeHtml(overallTooltip)}">${overallScoreFormatted}</td>`;
    }
    for (const col of cols) {
      const c = scores[row.model]?.[col.id];
      if (!c || c.score == null) {
        const lacking = installed ? lbLacksCaps(row.model, col) : [];
        if (lacking.length > 0) {
          const missTip = `${t("battery.lb_missing_cap", { caps: lacking.join(", ") })}${hintSuffix}`;
          cells += `<td class="cell-lb-score cell-lb-empty" data-lb-incompatible="${escapeHtml(row.model)}" data-lb-incompatible-caps="${escapeHtml(lacking.join(", "))}" title="${escapeHtml(missTip)}"><span class="muted">✕</span></td>`;
          continue;
        }
        cells += `<td class="cell-lb-score cell-lb-empty" ${runModelAttr} data-lb-run-group="${escapeHtml(col.id)}"${installed ? ` title="${escapeHtml(runHint)}"` : ""}><span class="muted">—</span></td>`;
        continue;
      }
      const score = c.score;
      const scoreFormatted = score.toFixed(1);
      const pendingText = c.unrun > 0 ? ` (${c.unrun} ${t("battery.pending_tests") || "unrun"})` : "";
      const scoreTooltip = `${c.passed}/${c.activeTotal} tests (${c.passedCases}/${c.activeCases} ${t("battery.cases") || "cases"}, ${c.pts.toFixed(1)}/${c.activeMaxPoints.toFixed(1)} ${t("battery.points") || "pts"})${pendingText} · ${scoreFormatted}%${hintSuffix}`;
      cells += `<td class="cell-lb-score mono" ${runModelAttr} data-lb-run-group="${escapeHtml(col.id)}" style="${batteryLbHeatStyle(score, colRange[col.id], false)}" title="${escapeHtml(scoreTooltip)}">${scoreFormatted}</td>`;
    }

    let modelName = row.model;
    let modelDisplay = escapeHtml(modelName);
    if (modelName.startsWith("hf.co/")) {
      modelDisplay = `<span style="opacity:0.45;font-weight:normal;">hf.co/</span>${escapeHtml(modelName.slice(6))}`;
    }

    const info = modelInfo.get(row.model);
    let metaHtml = "";
    if (!installed) {
      metaHtml = `<div class="lb-model-meta mono"><span class="pill lb-uninstalled-tag">${escapeHtml(t("battery.lb_not_installed_tag"))}</span></div>`;
    } else if (info) {
      const pills = (typeof renderCapabilityPills === "function") ? renderCapabilityPills(info.capabilities) : "";
      const parts = [];
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
      if (pills || parts.length > 0) {
        metaHtml = `<div class="lb-model-meta muted mono">${pills}${(pills && parts.length > 0) ? `<span class="lb-meta-sep">·</span>` : ""}${parts.join(`<span class="lb-meta-sep">·</span>`)}</div>`;
      }
    }

    bodyRows += `
      <tr class="${idx === 0 ? "lb-row-first" : ""}${installed ? "" : " lb-row-uninstalled"}"${installed ? "" : ` data-lb-uninstalled="1" data-lb-model="${escapeHtml(row.model)}"`}>
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

  return `
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
