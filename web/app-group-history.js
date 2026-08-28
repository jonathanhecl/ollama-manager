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

$("response-view-modal")?.addEventListener("click", (e) => {
  if (e.target === $("response-view-modal")) closeResponseViewModal();
});
$("response-view-modal-close")?.addEventListener("click", closeResponseViewModal);

$("tests-group-history-btn")?.addEventListener("click", () => {
  showBatteryHistoryView();
});
$("tests-run-battery-btn")?.addEventListener("click", () => {
  openBatteryModal({ groupId: selectedGroupId });
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
$("battery-progress-cancel")?.addEventListener("click", () => {
  void cancelBatteryRun();
});

document.addEventListener("visibilitychange", () => {
  if (!document.hidden && currentView === "battery-progress") {
    const saved = localStorage.getItem(BATTERY_KEY);
    if (saved) {
      try {
        const data = JSON.parse(saved);
        if (data.runID) {
          if (batteryPollTimer) {
            clearTimeout(batteryPollTimer);
            batteryPollTimer = null;
          }
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
