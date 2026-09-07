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
  if (!testsGroups || testsGroups.length === 0) {
    const data = await api("/api/tests");
    testsGroups = data.groups || [];
  }
  const groups = testsGroups.slice().sort((a, b) => {
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

  // Keep only groups that have data, as columns.
  const cols = [];
  groups.forEach((g, i) => {
    if (summaries[i].length > 0) cols.push({ id: g.id, name: g.name || g.id, summary: summaries[i] });
  });
  if (cols.length === 0) {
    return `<div class="battery-empty">${t("battery.no_history")}</div>`;
  }

  // Per model per group: { passed, total }.
  const modelSet = new Set();
  const scores = {};
  for (const col of cols) {
    for (const s of col.summary) {
      modelSet.add(s.model);
      (scores[s.model] ||= {})[col.id] = { passed: s.passed || 0, total: s.total_tests || 0 };
    }
  }

  const lbRows = Array.from(modelSet).map((m) => {
    let passed = 0;
    let total = 0;
    for (const col of cols) {
      const c = scores[m][col.id];
      if (c && c.total > 0) {
        passed += c.passed;
        total += c.total;
      }
    }
    return { model: m, passed, total, overall: total > 0 ? (passed / total) * 100 : null };
  });
  lbRows.sort((a, b) => (b.overall ?? -1) - (a.overall ?? -1) || b.total - a.total);

  const colRange = {};
  for (const col of cols) {
    const vals = lbRows
      .map((row) => {
        const c = scores[row.model][col.id];
        return c && c.total > 0 ? (c.passed / c.total) * 100 : null;
      })
      .filter((v) => v != null);
    colRange[col.id] = {
      min: vals.length ? Math.min(...vals) : 0,
      max: vals.length ? Math.max(...vals) : 0,
    };
  }
  const overallVals = lbRows.map((r) => r.overall).filter((v) => v != null);
  const overallRange = {
    min: overallVals.length ? Math.min(...overallVals) : 0,
    max: overallVals.length ? Math.max(...overallVals) : 0,
  };

  let headerCols = `<th class="cell-lb-overall-head">${t("battery.leaderboard_overall")}</th>`;
  for (const col of cols) {
    headerCols += `<th class="cell-lb-group-head" data-lb-run data-lb-run-group="${escapeHtml(col.id)}" data-lb-run-model="" title="${escapeHtml(col.name)} · ${escapeHtml(t("battery.lb_run_hint"))}">${escapeHtml(col.name)}</th>`;
  }

  const runHint = t("battery.lb_run_hint");
  let bodyRows = "";
  lbRows.forEach((row, idx) => {
    let cells = "";
    const installed = modelInfo.has(row.model);
    // Uninstalled models keep their results visible but cannot be run.
    const runModelAttr = installed ? `data-lb-run data-lb-run-model="${escapeHtml(row.model)}"` : "";
    const hintSuffix = installed ? ` · ${escapeHtml(runHint)}` : "";
    if (row.overall == null) {
      cells += `<td class="cell-lb-score cell-lb-overall cell-lb-empty" ${runModelAttr} data-lb-run-group=""${installed ? ` title="${escapeHtml(runHint)}"` : ""}><span class="muted">—</span></td>`;
    } else {
      cells += `<td class="cell-lb-score cell-lb-overall mono" ${runModelAttr} data-lb-run-group="" style="${batteryLbHeatStyle(row.overall, overallRange, true)}" title="${row.passed}/${row.total}${hintSuffix}">${row.overall.toFixed(1)}</td>`;
    }
    for (const col of cols) {
      const c = scores[row.model][col.id];
      if (!c || c.total === 0) {
        cells += `<td class="cell-lb-score cell-lb-empty" ${runModelAttr} data-lb-run-group="${escapeHtml(col.id)}"${installed ? ` title="${escapeHtml(runHint)}"` : ""}><span class="muted">—</span></td>`;
        continue;
      }
      const pct = (c.passed / c.total) * 100;
      cells += `<td class="cell-lb-score mono" ${runModelAttr} data-lb-run-group="${escapeHtml(col.id)}" style="${batteryLbHeatStyle(pct, colRange[col.id], false)}" title="${c.passed}/${c.total}${hintSuffix}">${pct.toFixed(1)}</td>`;
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
            <span class="lb-row-actions">${installed ? `<button type="button" class="ghost lb-row-btn" data-lb-chat="${escapeHtml(row.model)}" title="${escapeHtml(t("battery.lb_chat"))}">💬</button>` : ""}${row.total > 0 ? `<button type="button" class="ghost lb-row-btn danger-text" data-lb-reset-model="${escapeHtml(row.model)}" title="${escapeHtml(t("battery.lb_reset_model"))}">🧹</button>` : ""}</span>
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
