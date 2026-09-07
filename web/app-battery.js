"use strict";

// ---------- battery runner ----------
let currentRunTarget = null; // { type: 'single' | 'group' | 'all', testId?: string, groupId?: string, name?: string }
let currentHistoryFilterTestId = null;
let currentHistoryFilterModel = null;

const BATTERY_SORT_KEY = "om_battery_sort";
let batterySort = { col: "name", dir: "asc" };
try {
  const saved = JSON.parse(localStorage.getItem(BATTERY_SORT_KEY) || "null");
  if (saved && saved.col && (saved.dir === "asc" || saved.dir === "desc")) {
    batterySort = saved;
  }
} catch { }

function updateBatterySortUI() {
  document.querySelectorAll(".battery-sort-btn").forEach((btn) => {
    const col = btn.dataset.batterySort;
    const arrow = btn.querySelector(".sort-arrow");
    const isActive = batterySort.col === col;
    btn.classList.toggle("active", isActive);
    if (arrow) {
      arrow.textContent = isActive ? (batterySort.dir === "asc" ? "▲" : "▼") : "";
    }
  });
}

function wireBatterySortButtons() {
  const container = document.querySelector(".battery-modal-sort-actions");
  if (!container || container.dataset.wired) return;
  container.dataset.wired = "1";

  container.querySelectorAll(".battery-sort-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const col = btn.dataset.batterySort;
      if (batterySort.col === col) {
        batterySort.dir = batterySort.dir === "asc" ? "desc" : "asc";
      } else {
        batterySort.col = col;
        batterySort.dir = col === "name" ? "asc" : "desc";
      }
      try {
        localStorage.setItem(BATTERY_SORT_KEY, JSON.stringify(batterySort));
      } catch { }
      updateBatterySortUI();
      renderBatteryModalModels();
    });
  });
}

async function openBatteryModal(options = {}) {
  batterySelectedModels.clear();
  const defaultModel = options.initialModel || (typeof selectedTestModel !== "undefined" ? selectedTestModel : "");
  if (defaultModel) {
    batterySelectedModels.add(defaultModel);
  }
  $("battery-modal").hidden = false;

  if (options.testId) {
    const test = tests.find((t) => t.id === options.testId);
    currentRunTarget = { type: "single", testId: options.testId, groupId: test?.group_id, name: test?.name || options.testId };
    const titleEl = $("battery-modal-title");
    if (titleEl) titleEl.textContent = t("battery.run_single", { name: test?.name || options.testId });
  } else if (options.groupId && options.groupId !== "" && options.groupId !== "all") {
    const group = testsGroups.find((g) => g.id === options.groupId);
    currentRunTarget = { type: "group", groupId: options.groupId, name: group?.name || options.groupId };
    const titleEl = $("battery-modal-title");
    if (titleEl) titleEl.textContent = t("battery.run_group", { name: group?.name || options.groupId });
  } else {
    currentRunTarget = { type: "all", groupId: "all", name: t("battery.all_tests") };
    const titleEl = $("battery-modal-title");
    if (titleEl) titleEl.textContent = t("battery.run_all");
  }

  // Pre-fetch models and usage if needed
  if (typeof models === "undefined" || models.length === 0) {
    try { await refreshModels(); } catch { }
  }

  wireBatterySortButtons();
  updateBatterySortUI();
  renderBatteryModalModels();
  updateBatteryModalSelectionUI();

  // Wire toolbar quick buttons if not wired
  const selectAllBtn = $("battery-modal-select-all");
  if (selectAllBtn && !selectAllBtn.dataset.wired) {
    selectAllBtn.dataset.wired = "1";
    selectAllBtn.addEventListener("click", () => {
      const cbs = $("battery-modal-models")?.querySelectorAll('input[type="checkbox"]:not(:disabled)');
      if (cbs) {
        cbs.forEach((cb) => {
          cb.checked = true;
          batterySelectedModels.add(cb.value);
          cb.closest(".battery-model-item")?.classList.add("selected");
        });
        updateBatteryModalSelectionUI();
      }
    });
  }

  const clearBtn = $("battery-modal-clear");
  if (clearBtn && !clearBtn.dataset.wired) {
    clearBtn.dataset.wired = "1";
    clearBtn.addEventListener("click", () => {
      const cbs = $("battery-modal-models")?.querySelectorAll('input[type="checkbox"]');
      if (cbs) {
        cbs.forEach((cb) => {
          cb.checked = false;
          cb.closest(".battery-model-item")?.classList.remove("selected");
        });
        batterySelectedModels.clear();
        updateBatteryModalSelectionUI();
      }
    });
  }

  // Load system info preview.
  const sysEl = $("battery-modal-sysinfo");
  if (sysEl) {
    sysEl.textContent = t("status.loading");
    try {
      const info = await api("/api/runner/sys-info");
      const parts = [];
      if (info.os) parts.push(`${t("battery.sys_os")}: ${info.os}`);
      if (info.cpu_model) parts.push(`${t("battery.sys_cpu")}: ${info.cpu_model}`);
      if (info.gpu_model) parts.push(`${t("battery.sys_gpu")}: ${info.gpu_model}`);
      if (info.ram_gb) parts.push(`${t("battery.sys_ram")}: ${info.ram_gb} GB`);
      if (info.vram_gb) parts.push(`${t("battery.sys_vram")}: ${info.vram_gb} GB`);
      sysEl.textContent = parts.length ? parts.join(" | ") : "";
      sysEl.hidden = !parts.length;
    } catch (e) {
      sysEl.textContent = "";
      sysEl.hidden = true;
    }
  }
}

function updateBatteryModalSelectionUI() {
  const countEl = $("battery-modal-count");
  if (countEl) {
    countEl.textContent = batterySelectedModels.size > 0 ? t("battery.models_count", { count: batterySelectedModels.size }) : "";
  }
}

function closeBatteryModal() {
  $("battery-modal").hidden = true;
}

function renderBatteryModalModels() {
  const container = $("battery-modal-models");
  if (!container) return;

  const activeModels = (typeof models !== "undefined" ? models : []).filter((m) => !m.archived);

  // Determine required caps based on currentRunTarget
  let requiredCaps = new Set();
  if (currentRunTarget?.type === "single") {
    const test = tests.find((t) => t.id === currentRunTarget.testId);
    requiredCaps = new Set(test?.required_caps || []);
  } else if (currentRunTarget?.type === "group") {
    const groupTests = tests.filter((t) => t.group_id === currentRunTarget.groupId && t.active && t.evaluation_type !== "agent");
    for (const t of groupTests) {
      for (const c of t.required_caps || []) requiredCaps.add(c);
    }
  }

  const items = activeModels
    .filter((m) => (m.capabilities || []).includes("completion"))
    .map((m) => {
      const caps = m.capabilities || [];
      const hasAnyRequired = requiredCaps.size === 0 || [...requiredCaps].some((c) => caps.includes(c));
      const missing = [];
      for (const c of requiredCaps) {
        if (!caps.includes(c)) missing.push(c);
      }
      const disabled = !hasAnyRequired;
      const title = disabled ? t("battery.model_unsupported_caps") + ": " + missing.join(", ") : "";
      return { m, disabled, title };
    });

  if (items.length === 0) {
    container.innerHTML = `<div class="muted">${t("state.empty_models")}</div>`;
    updateBatteryModalSelectionUI();
    return;
  }

  items.sort((a, b) => {
    if (a.disabled !== b.disabled) {
      return a.disabled ? 1 : -1;
    }
    const dirMul = batterySort.dir === "asc" ? 1 : -1;
    if (batterySort.col === "tps") {
      const tpsA = Number(a.m.record_tokens_per_sec) || 0;
      const tpsB = Number(b.m.record_tokens_per_sec) || 0;
      if (tpsA !== tpsB) {
        return dirMul * (tpsA - tpsB);
      }
    } else if (batterySort.col === "size") {
      const sizeA = Number(a.m.size) || 0;
      const sizeB = Number(b.m.size) || 0;
      if (sizeA !== sizeB) {
        return dirMul * (sizeA - sizeB);
      }
    }
    const nameA = (a.m.name || "").toLowerCase();
    const nameB = (b.m.name || "").toLowerCase();
    const nameCmp = nameA.localeCompare(nameB);
    return batterySort.col === "name" ? dirMul * nameCmp : nameCmp;
  });

  container.innerHTML = items.map(({ m, disabled, title }) => {
    const capsHtml = (m.capabilities || [])
      .map((c) => `<span class="pill" data-cap="${escapeHtml(c)}">${escapeHtml(c)}</span>`)
      .join("");
    const tps = Number(m.record_tokens_per_sec) || 0;
    const tokColor = (typeof getToksRecordColor === "function" && tps > 0) ? getToksRecordColor(tps) : "";
    const colorStyle = tokColor ? ` style="color: ${tokColor};"` : "";
    const sizeText = (typeof fmtBytes === "function" && m.size && m.size > 0) ? fmtBytes(m.size) : "";
    const paramsText = m.parameter_size || "";
    const quantText = (m.quantization && m.quantization !== "unknown") ? m.quantization : "";
    const coldLoadMs = m.min_cold_load_ms || 0;
    const coldLoadHtml = (typeof fmtColdLoad === "function" && coldLoadMs > 0)
      ? `<span class="battery-model-coldload muted mono" title="${escapeHtml(t("col.min_load"))}">⏱️ ${fmtColdLoad(coldLoadMs)}</span>`
      : "";
    const isChecked = batterySelectedModels.has(m.name);

    return `
      <label class="battery-model-item ${isChecked ? "selected" : ""} ${disabled ? "disabled" : ""}" title="${escapeHtml(title)}">
        <input type="checkbox" value="${escapeHtml(m.name)}" ${isChecked ? "checked" : ""} ${disabled ? "disabled" : ""} />
        <div class="battery-model-main">
          <div class="battery-model-name">${escapeHtml(m.name)}</div>
          ${capsHtml ? `<div class="battery-model-caps cap-list model-cap-list">${capsHtml}</div>` : ""}
        </div>
        <div class="battery-model-right-cols">
          <div class="battery-model-tps-wrap">
            <div class="battery-model-tps-box">
              ${tps > 0
                ? `<span class="cell-record-tok battery-model-tps" title="${tps.toFixed(1)} tok/s"><span class="record-num"${colorStyle}>${tps.toFixed(1)}</span> <span class="unit">tok/s</span></span>`
                : `<span class="cell-record-tok battery-model-tps muted"><span class="record-num">—</span> <span class="unit">tok/s</span></span>`
              }
            </div>
            ${coldLoadHtml ? `<div class="battery-model-coldload-box">${coldLoadHtml}</div>` : ""}
          </div>
          <div class="battery-model-specs mono muted">
            ${sizeText ? `<span class="battery-model-size" title="${escapeHtml(t("col.size"))}">${escapeHtml(sizeText)}</span>` : ""}
            ${paramsText ? `<span class="battery-model-param">${escapeHtml(paramsText)}</span>` : ""}
            ${quantText ? `<span class="battery-model-quant">${escapeHtml(quantText)}</span>` : ""}
          </div>
        </div>
      </label>
    `;
  }).join("");

  container.querySelectorAll('input[type="checkbox"]').forEach((cb) => {
    cb.addEventListener("change", () => {
      const parentLabel = cb.closest(".battery-model-item");
      if (cb.checked) {
        batterySelectedModels.add(cb.value);
        parentLabel?.classList.add("selected");
      } else {
        batterySelectedModels.delete(cb.value);
        parentLabel?.classList.remove("selected");
      }
      updateBatteryModalSelectionUI();
    });
  });

  updateBatteryModalSelectionUI();
}

let batteryPollTimer = null;
let batteryActiveRunID = null; // run currently followed by the progress view

// Central stop: clears any pending poll and forgets the followed run, so an
// orphan poll can never hijack the view or toast after cancel/navigation.
function stopBatteryPolling() {
  if (batteryPollTimer) {
    clearTimeout(batteryPollTimer);
    batteryPollTimer = null;
  }
  if (batteryElapsedInterval) {
    clearInterval(batteryElapsedInterval);
    batteryElapsedInterval = null;
  }
  batteryActiveRunID = null;
  batteryActiveTurnKey = "";
  batteryTurnStartTime = 0;
  batteryThinkingStartTime = 0;
  batteryResponseStartTime = 0;
  updateBatteryCurrentTurnTimer();
}
let batteryCompletedTests = [];
let batteryLastTestSnapshot = null;
let batteryTimelineTotal = 0;
let batteryTimelineCompleted = []; // {index, name, model, testId}
let batteryTimelineCurrent = null; // {index, name, model, isThinking}
let batteryTimelineQueue = []; // {index, testId, testName, model}
let batteryTimelineScrollKey = "";
let batteryProgressModelIDs = [];
let batteryLiveResults = [];
let batteryStartTime = 0;
let batteryElapsedInterval = null;
let batteryActiveTab = "models";
let batteryActiveTurnKey = "";
let batteryTurnStartTime = 0;
let batteryThinkingStartTime = 0;
let batteryResponseStartTime = 0;
const testHistoryResponses = new Map(); // respKey -> full response string

function formatTimeDisplay(totalSeconds) {
  if (isNaN(totalSeconds) || totalSeconds < 0) return "00:00";
  const s = Math.floor(totalSeconds);
  const hrs = Math.floor(s / 3600);
  const mins = Math.floor((s % 3600) / 60);
  const secs = s % 60;
  if (hrs > 0) {
    return `${String(hrs).padStart(2, "0")}:${String(mins).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
  }
  return `${String(mins).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
}

function updateBatteryCurrentTurnTimer() {
  const elStreamTurn = $("battery-stream-turn-timer");
  const elStreamTime = $("battery-stream-turn-time");
  const elHeadTurn = $("battery-head-turn-timer");
  const elHeadTime = $("battery-head-turn-time");
  const elThinkingTimer = $("battery-stream-thinking-timer");
  const elResponseTimer = $("battery-stream-response-timer");

  if (!batteryTurnStartTime || !batteryActiveTurnKey) {
    if (elStreamTurn) elStreamTurn.style.display = "none";
    if (elHeadTurn) elHeadTurn.hidden = true;
    if (elThinkingTimer) elThinkingTimer.hidden = true;
    if (elResponseTimer) elResponseTimer.hidden = true;
    return;
  }

  const turnSec = Math.floor((Date.now() - batteryTurnStartTime) / 1000);
  const timeStr = formatTimeDisplay(turnSec);

  if (elStreamTurn) {
    elStreamTurn.style.display = "inline-flex";
    if (elStreamTime) elStreamTime.textContent = timeStr;

    elStreamTurn.classList.remove("timer-warn", "timer-danger");
    if (turnSec >= 60) {
      elStreamTurn.classList.add("timer-danger");
      elStreamTurn.title = `${t("battery.timer_long_desc") || "Posible bloqueo o bucle (>60s). Puedes usar Skip Test para continuar."} (${timeStr})`;
    } else if (turnSec >= 30) {
      elStreamTurn.classList.add("timer-warn");
      elStreamTurn.title = `${t("battery.timer_warn_desc") || "Generación tomando más de 30 segundos"} (${timeStr})`;
    } else {
      elStreamTurn.title = `${t("battery.timer_turn_desc") || "Tiempo de respuesta en curso"} (${timeStr})`;
    }
  }

  if (elHeadTurn) {
    elHeadTurn.hidden = false;
    if (elHeadTime) elHeadTime.textContent = timeStr;
    elHeadTurn.classList.remove("timer-warn", "timer-danger");
    if (turnSec >= 60) {
      elHeadTurn.classList.add("timer-danger");
    } else if (turnSec >= 30) {
      elHeadTurn.classList.add("timer-warn");
    }
  }

  if (elThinkingTimer) {
    if (batteryThinkingStartTime) {
      const thinkingSec = Math.floor((Date.now() - batteryThinkingStartTime) / 1000);
      elThinkingTimer.hidden = false;
      elThinkingTimer.textContent = `⏱️ ${formatTimeDisplay(thinkingSec)}`;
    } else {
      elThinkingTimer.hidden = true;
    }
  }

  if (elResponseTimer) {
    if (batteryResponseStartTime) {
      const respSec = Math.floor((Date.now() - batteryResponseStartTime) / 1000);
      elResponseTimer.hidden = false;
      elResponseTimer.textContent = `⏱️ ${formatTimeDisplay(respSec)}`;
    } else {
      elResponseTimer.hidden = true;
    }
  }
}

function updateBatteryElapsedDisplay() {
  if (!batteryStartTime) return;
  const elapsedSec = Math.floor((Date.now() - batteryStartTime) / 1000);
  const elElapsed = $("battery-timer-elapsed");
  if (elElapsed) elElapsed.textContent = formatTimeDisplay(elapsedSec);

  // Dynamic empirical ETA calculation
  const elEta = $("battery-timer-eta");
  if (elEta) {
    const completedCount = batteryLiveResults.length;
    if (completedCount >= 1 && batteryTimelineTotal > completedCount && elapsedSec > 2) {
      const avgSecPerTest = elapsedSec / completedCount;
      const remainingTests = batteryTimelineTotal - completedCount;
      const estSec = Math.round(remainingTests * avgSecPerTest);
      elEta.textContent = "~" + formatTimeDisplay(estSec);
    } else if (completedCount >= batteryTimelineTotal && batteryTimelineTotal > 0) {
      elEta.textContent = "00:00";
    } else {
      elEta.textContent = t("battery.kpi_eta_calc");
    }
  }

  updateBatteryCurrentTurnTimer();
}

function computeBatteryStats(modelIDs, results, currentModel, currentTestIdx, totalTests) {
  const modelMap = new Map();
  const queue = batteryTimelineQueue || [];

  for (const m of modelIDs) {
    const expected = queue.filter((q) => q.model === m).length || 0;
    const modelResults = results.filter((r) => r.model === m);
    const completed = modelResults.length;
    const passed = modelResults.filter((r) => r.passed === true).length;
    const failed = modelResults.filter((r) => r.passed === false).length;
    const totalExpected = expected > 0 ? expected : completed;
    // Pass rate relative to total expected tests (done + pending) so it builds up progressively and never moves downward
    const passRate = totalExpected > 0 ? (passed / totalExpected) * 100 : 0;
    const completedPassRate = completed > 0 ? (passed / completed) * 100 : 0;
    const totalSpeed = modelResults.reduce((sum, r) => sum + (r.tokens_per_sec || 0), 0);
    const avgSpeed = completed > 0 ? totalSpeed / completed : 0;
    const totalDuration = modelResults.reduce((sum, r) => sum + (r.response_time_ms || 0), 0);
    const avgLatency = completed > 0 ? (totalDuration / completed) / 1000 : 0;
    const isCurrent = m === currentModel;
    const isDone = completed > 0 && (expected === 0 || completed >= expected);

    modelMap.set(m, {
      model: m,
      expected: expected || completed,
      completed,
      passed,
      failed,
      passRate,
      completedPassRate,
      avgSpeed,
      avgLatency,
      isCurrent,
      isDone,
    });
  }

  // Next model in queue
  let nextModel = "";
  if (currentModel) {
    const curIdx = modelIDs.indexOf(currentModel);
    for (let i = curIdx + 1; i < modelIDs.length; i++) {
      const cand = modelIDs[i];
      const st = modelMap.get(cand);
      if (!st || !st.isDone) {
        nextModel = cand;
        break;
      }
    }
  } else if (modelIDs.length > 0) {
    nextModel = modelIDs[0];
  }

  // Global aggregates
  const totalCompleted = results.length;
  const totalPassed = results.filter((r) => r.passed === true).length;
  const totalFailed = results.filter((r) => r.passed === false).length;
  const globalPassRate = totalCompleted > 0 ? (totalPassed / totalCompleted) * 100 : 0;
  const totalSpeedAll = results.reduce((sum, r) => sum + (r.tokens_per_sec || 0), 0);
  const globalAvgSpeed = totalCompleted > 0 ? totalSpeedAll / totalCompleted : 0;
  const totalDurationAll = results.reduce((sum, r) => sum + (r.response_time_ms || 0), 0);
  const globalAvgLatency = totalCompleted > 0 ? (totalDurationAll / totalCompleted) / 1000 : 0;
  const totalTokens = results.reduce((sum, r) => sum + (r.total_tokens || 0), 0);

  return {
    modelMap,
    nextModel,
    totalCompleted,
    totalPassed,
    totalFailed,
    globalPassRate,
    globalAvgSpeed,
    globalAvgLatency,
    totalTokens,
  };
}

function renderBatteryKPIs(p, stats) {
  const currentModel = p.model || "";
  const total = p.total_tests || batteryTimelineTotal || 0;
  const idx = p.test_index || 0;
  const done = p.done || false;

  // Active Model Card
  const elModelName = $("battery-kpi-model-name");
  const elModelStatus = $("battery-kpi-model-status");
  const elModelSub = $("battery-kpi-model-sub");
  const elModelPct = $("battery-kpi-model-pct");
  const elModelBar = $("battery-kpi-model-bar");
  const elNextName = $("battery-kpi-next-name");

  if (elModelName) {
    elModelName.textContent = currentModel || (done ? t("battery.status_done") : "--");
    elModelName.title = currentModel || "";
  }
  if (elModelStatus) {
    if (done) {
      elModelStatus.className = "badge badge-pass";
      elModelStatus.textContent = t("battery.status_done");
    } else if (p.is_thinking) {
      elModelStatus.className = "badge badge-warn pulse";
      elModelStatus.innerHTML = `🧠 ${t("battery.status_thinking")}`;
    } else if (p.partial_response) {
      elModelStatus.className = "badge badge-pass pulse";
      elModelStatus.innerHTML = `⚡ ${t("battery.status_generating")}`;
    } else {
      elModelStatus.className = "badge badge-primary pulse";
      elModelStatus.innerHTML = `⏳ ${t("battery.status_evaluating")}`;
    }
  }

  const curStats = stats.modelMap.get(currentModel);
  if (curStats) {
    const curModelRunningIdx = Math.min(curStats.expected, curStats.completed + (done ? 0 : 1));
    if (elModelSub) {
      elModelSub.textContent = t("battery.kpi_model_tests", { current: String(curModelRunningIdx), total: String(curStats.expected) });
    }
    const modelPct = curStats.expected > 0 ? Math.round((curStats.completed / curStats.expected) * 100) : 0;
    if (elModelPct) elModelPct.textContent = `${modelPct}%`;
    if (elModelBar) elModelBar.style.width = `${modelPct}%`;
  } else {
    if (elModelSub) elModelSub.textContent = "--";
    if (elModelPct) elModelPct.textContent = "0%";
    if (elModelBar) elModelBar.style.width = "0%";
  }

  if (elNextName) {
    elNextName.textContent = stats.nextModel || t("battery.kpi_no_next");
    elNextName.title = stats.nextModel || "";
  }

  // Global Progress Card
  const elGlobalCount = $("battery-kpi-global-count");
  const elGlobalPct = $("battery-kpi-global-pct");
  const elRemaining = $("battery-kpi-remaining-text");
  const elFill = $("battery-progress-fill");

  const displayIdx = done ? total : Math.max(0, idx - 1);
  const globalPct = total > 0 ? (done ? 100 : Math.max(0, Math.min(100, Math.round((displayIdx / total) * 100)))) : 0;
  const remainingCount = Math.max(0, total - displayIdx);

  if (elGlobalCount) elGlobalCount.textContent = `${displayIdx} / ${total}`;
  if (elGlobalPct) elGlobalPct.textContent = `${globalPct}%`;
  if (elRemaining) elRemaining.textContent = t("battery.kpi_remaining", { count: String(remainingCount) });
  if (elFill) elFill.style.width = `${globalPct}%`;

  // Global Pass Rate Card
  const elDonutVal = $("battery-kpi-donut-val");
  const elDonutPct = $("battery-kpi-pass-pct-inner");
  const elPassCount = $("battery-kpi-pass-count-text");
  const elFailCount = $("battery-kpi-fail-count-text");

  const passPctRound = Math.round(stats.globalPassRate);
  if (elDonutVal) {
    elDonutVal.setAttribute("stroke-dasharray", `${passPctRound}, 100`);
    if (passPctRound < 50 && stats.totalCompleted > 0) {
      elDonutVal.style.stroke = "var(--danger)";
    } else if (passPctRound < 75 && stats.totalCompleted > 0) {
      elDonutVal.style.stroke = "var(--warn)";
    } else {
      elDonutVal.style.stroke = "var(--good)";
    }
  }
  if (elDonutPct) elDonutPct.textContent = `${passPctRound}%`;
  if (elPassCount) elPassCount.textContent = `✔ ${stats.totalPassed} ${t("battery.pass")}`;
  if (elFailCount) elFailCount.textContent = `✖ ${stats.totalFailed} ${t("battery.fail")}`;

  // Performance Card
  const elSpeed = $("battery-kpi-avg-speed");
  const elLatency = $("battery-kpi-avg-latency");
  const elTokens = $("battery-kpi-tokens-val");

  if (elSpeed) {
    elSpeed.innerHTML = stats.globalAvgSpeed > 0 ? `${stats.globalAvgSpeed.toFixed(1)} <span class="kpi-unit">tok/s</span>` : `-- <span class="kpi-unit">tok/s</span>`;
  }
  if (elLatency) {
    elLatency.textContent = stats.globalAvgLatency > 0 ? `${stats.globalAvgLatency.toFixed(1)}s ${t("battery.response_time").toLowerCase()}` : `-- s`;
  }
  if (elTokens) {
    elTokens.textContent = `${stats.totalTokens.toLocaleString()} tokens`;
  }

  requestAnimationFrame(() => {
    const view = $("battery-progress-view");
    if (view) setupMarquees(view);
  });
}

function setupMarquees(container = document) {
  if (!container) return;
  const wrappers = container.querySelectorAll(".marquee-wrapper");
  wrappers.forEach((wrap) => {
    const content = wrap.querySelector(".marquee-content");
    if (!content) return;
    wrap.classList.remove("is-overflowing");
    const diff = content.scrollWidth - wrap.clientWidth;
    if (diff > 4) {
      wrap.classList.add("is-overflowing");
      const duration = Math.max(5, Math.min(18, Math.round(diff / 18)));
      wrap.style.setProperty("--marquee-end", `-${diff + 10}px`);
      wrap.style.setProperty("--marquee-dur", `${duration}s`);
    } else {
      wrap.classList.remove("is-overflowing");
      wrap.style.removeProperty("--marquee-end");
      wrap.style.removeProperty("--marquee-dur");
    }
  });
}

function renderBatteryLeaderboard(modelIDs, modelMap, currentModel) {
  const container = $("battery-leaderboard-container");
  if (!container) return;
  if (!modelIDs || !modelIDs.length) {
    container.innerHTML = `<div class="muted">${escapeHtml(t("battery.starting"))}</div>`;
    return;
  }

  let html = `
    <table class="battery-leaderboard-table">
      <thead>
        <tr>
          <th>${escapeHtml(t("battery.col_model"))}</th>
          <th>${escapeHtml(t("battery.col_tests"))}</th>
          <th>${escapeHtml(t("battery.col_ratio"))}</th>
          <th>${escapeHtml(t("battery.col_pass_pct"))}</th>
          <th>${escapeHtml(t("battery.col_speed"))}</th>
          <th>${escapeHtml(t("battery.col_status"))}</th>
        </tr>
      </thead>
      <tbody>
  `;

  for (const m of modelIDs) {
    const st = modelMap.get(m) || { expected: 0, completed: 0, passed: 0, failed: 0, passRate: 0, completedPassRate: 0, avgSpeed: 0, isCurrent: m === currentModel, isDone: false };
    const totalExp = st.expected > 0 ? st.expected : st.completed;
    const passPct = Math.round(st.passRate);
    const passBarWidth = totalExp > 0 ? (st.passed / totalExp) * 100 : 0;
    const failBarWidth = totalExp > 0 ? (st.failed / totalExp) * 100 : 0;
    const pendingCount = Math.max(0, totalExp - st.completed);

    let statusBadge = `<span class="badge badge-muted">⏳ ${escapeHtml(t("battery.status_pending"))}</span>`;
    if (st.isCurrent) {
      statusBadge = `<span class="badge badge-primary pulse">⚡ ${escapeHtml(t("battery.status_running"))}</span>`;
    } else if (st.isDone) {
      statusBadge = `<span class="badge badge-pass">✔ ${escapeHtml(t("battery.status_done"))}</span>`;
    }

    const rowClass = st.isCurrent ? "battery-leaderboard-row active-model-row" : "battery-leaderboard-row";
    const passPctColor = passPct >= 75 ? "var(--good)" : (passPct >= 50 ? "var(--warn)" : "var(--danger)");
    const ratioTooltip = `${st.passed} ${t("battery.pass")} · ${st.failed} ${t("battery.fail")}${pendingCount > 0 ? ` · ${pendingCount} ${t("battery.status_pending")}` : ""}`;
    const pctTooltip = st.completed > 0 ? `${st.passed}/${totalExp} (${passPct}%)` : "";

    html += `
      <tr class="${rowClass}">
        <td>
          <div class="leaderboard-model-cell">
            <div class="marquee-wrapper leaderboard-marquee">
              <span class="marquee-content leaderboard-model-name mono" title="${escapeHtml(m)}">${escapeHtml(m)}</span>
            </div>
          </div>
        </td>
        <td class="mono">${st.completed} / ${st.expected}</td>
        <td>
          <div class="leaderboard-ratio-bar" title="${escapeHtml(ratioTooltip)}">
            <div class="ratio-bar-pass" style="width: ${passBarWidth}%"></div>
            <div class="ratio-bar-fail" style="width: ${failBarWidth}%"></div>
          </div>
        </td>
        <td class="mono font-bold" style="color:${st.completed > 0 ? passPctColor : 'var(--muted)'};" title="${escapeHtml(pctTooltip)}">
          ${st.completed > 0 ? passPct + "%" : "--"}
        </td>
        <td class="mono muted">
          ${st.avgSpeed > 0 ? st.avgSpeed.toFixed(1) + " tok/s" : "--"}
        </td>
        <td>${statusBadge}</td>
      </tr>
    `;
  }

  html += `</tbody></table>`;
  container.innerHTML = html;
  requestAnimationFrame(() => setupMarquees(container));
}

function renderBatteryAnalyticsCharts(modelIDs, modelMap) {
  const containers = [
    $("battery-leaderboard-charts"),
    $("battery-analytics-charts-container"),
  ].filter(Boolean);

  if (!containers.length) return;

  if (!modelIDs || !modelIDs.length) {
    const emptyHtml = `<div class="muted" style="padding: 24px; text-align: center;">${escapeHtml(t("battery.charts_no_data"))}</div>`;
    containers.forEach((c) => (c.innerHTML = emptyHtml));
    return;
  }

  // Find models with stats
  const allModelStats = modelIDs.map((m) => {
    return modelMap.get(m) || { model: m, expected: 0, completed: 0, passed: 0, failed: 0, passRate: 0, avgSpeed: 0 };
  });

  const maxSpeed = Math.max(...allModelStats.map((s) => s.avgSpeed || 0), 20);
  const rowHeight = 34;
  const chartHeight = Math.max(60, allModelStats.length * rowHeight + 16);

  // SVG for Pass Rates
  let passBars = "";
  allModelStats.forEach((st, idx) => {
    const y = idx * rowHeight + 8;
    const isTested = st.completed > 0;
    const totalExp = st.expected > 0 ? st.expected : st.completed;
    const barWidth = isTested ? Math.max(3, Math.round(st.passRate * 2.8)) : 0;
    const passColor = st.passRate >= 75 ? "#10b981" : (st.passRate >= 50 ? "#f59e0b" : "#ef4444");
    const shortName = st.model.length > 22 ? st.model.slice(0, 20) + "…" : st.model;
    const valText = isTested ? `${Math.round(st.passRate)}% (${st.passed}/${totalExp})` : `-- (0/${totalExp || 0})`;

    passBars += `
      <g class="chart-row">
        <text x="8" y="${y + 13}" class="chart-label" font-family="monospace">${escapeHtml(shortName)}</text>
        <rect x="175" y="${y}" width="280" height="16" class="chart-bar-bg" rx="3" />
        ${isTested ? `<rect x="175" y="${y}" width="${barWidth}" height="16" fill="${passColor}" rx="3" />` : ""}
        <text x="${175 + barWidth + 8}" y="${y + 13}" class="chart-val">${escapeHtml(valText)}</text>
      </g>
    `;
  });

  // SVG for Speeds
  let speedBars = "";
  allModelStats.forEach((st, idx) => {
    const y = idx * rowHeight + 8;
    const isTested = st.completed > 0;
    const speedRatio = (maxSpeed > 0 && isTested) ? (st.avgSpeed / maxSpeed) : 0;
    const barWidth = isTested ? Math.max(3, Math.round(speedRatio * 280)) : 0;
    const shortName = st.model.length > 22 ? st.model.slice(0, 20) + "…" : st.model;
    const valText = isTested ? `${st.avgSpeed.toFixed(1)} tok/s` : `--`;

    speedBars += `
      <g class="chart-row">
        <text x="8" y="${y + 13}" class="chart-label" font-family="monospace">${escapeHtml(shortName)}</text>
        <rect x="175" y="${y}" width="280" height="16" class="chart-bar-bg" rx="3" />
        ${isTested ? `<rect x="175" y="${y}" width="${barWidth}" height="16" fill="#38bdf8" rx="3" />` : ""}
        <text x="${175 + barWidth + 8}" y="${y + 13}" class="chart-val">${escapeHtml(valText)}</text>
      </g>
    `;
  });

  const fullChartsHtml = `
    <div class="analytics-card-section">
      <div class="analytics-section-title">
        <span>🎯</span> ${escapeHtml(t("battery.charts_overall"))}
      </div>
      <svg class="analytics-svg-chart" viewBox="0 0 540 ${chartHeight}" height="${chartHeight}">
        ${passBars}
      </svg>
    </div>

    <div class="analytics-card-section">
      <div class="analytics-section-title">
        <span>⚡</span> ${escapeHtml(t("battery.charts_speed"))}
      </div>
      <svg class="analytics-svg-chart" viewBox="0 0 540 ${chartHeight}" height="${chartHeight}">
        ${speedBars}
      </svg>
    </div>
  `;

  containers.forEach((c) => {
    c.innerHTML = fullChartsHtml;
  });
}

function initBatteryProgressControls() {
  const tabs = [
    { btn: "battery-tab-btn-models", content: "battery-tab-content-models", id: "models" },
    { btn: "battery-tab-btn-charts", content: "battery-tab-content-charts", id: "charts" },
    { btn: "battery-tab-btn-queue", content: "battery-tab-content-queue", id: "queue" },
  ];

  tabs.forEach((tab) => {
    const btn = $(tab.btn);
    if (!btn || btn.dataset.bound) return;
    btn.dataset.bound = "1";
    btn.addEventListener("click", () => {
      batteryActiveTab = tab.id;
      tabs.forEach((t) => {
        const b = $(t.btn);
        const c = $(t.content);
        if (b) b.classList.toggle("active", t.id === tab.id);
        if (c) c.hidden = (t.id !== tab.id);
      });
    });
  });

  const copyBtn = $("battery-copy-prompt-btn");
  if (copyBtn && !copyBtn.dataset.bound) {
    copyBtn.dataset.bound = "1";
    copyBtn.addEventListener("click", async () => {
      const promptEl = $("battery-stream-prompt");
      if (!promptEl) return;
      const text = promptEl.textContent || "";
      if (!text) return;
      try {
        await navigator.clipboard.writeText(text);
        const orig = copyBtn.textContent;
        copyBtn.textContent = t("battery.copied");
        setTimeout(() => {
          copyBtn.textContent = orig;
        }, 1500);
      } catch {
        toast(t("toast.copy_error") || "Failed to copy", "warn");
      }
    });
  }
}

if (typeof document !== "undefined") {
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", initBatteryProgressControls);
  } else {
    initBatteryProgressControls();
  }
}

function renderBatteryProgressModels(modelIDs, currentModel, isThinking) {
  // Kept for backward compatibility
  const container = $("battery-progress-models");
  if (!container) return;
  container.innerHTML = "";
}

function getTestCategoryName(testId, fallbackGroupId = "") {
  let gid = fallbackGroupId;
  const tObj = (typeof tests !== "undefined" && Array.isArray(tests)) ? tests.find((t) => t.id === testId) : null;
  if (tObj && tObj.group_id) {
    gid = tObj.group_id;
  }
  if (!gid) return "";
  const gObj = (typeof testsGroups !== "undefined" && Array.isArray(testsGroups)) ? testsGroups.find((g) => g.id === gid) : null;
  if (gObj && gObj.name) return gObj.name;
  return gid.charAt(0).toUpperCase() + gid.slice(1);
}

function buildBatteryTimelineQueue(groupId, modelIDs) {
  const activeTests = tests
    .filter((t) => (groupId === "all" || !groupId || t.group_id === groupId) && t.active && t.evaluation_type !== "agent")
    .sort((a, b) => (a.order || 0) - (b.order || 0));
  const queue = [];
  let idx = 0;
  for (const model of modelIDs) {
    const caps = modelCaps(model);
    for (const test of activeTests) {
      const required = (test.required_caps || []).map((c) => String(c).toLowerCase());
      if (required.every((c) => caps.has(c))) {
        idx++;
        queue.push({ index: idx, testId: test.id, testName: test.name, model, groupId: test.group_id });
      }
    }
  }
  return queue;
}

function showBatteryProgressView(modelIDs, runID, groupId) {
  batteryCompletedTests = [];
  batteryLastTestSnapshot = null;
  batteryPollRetryCount = 0;
  batteryTimelineTotal = 0;
  batteryTimelineCompleted = [];
  batteryTimelineCurrent = null;
  batteryTimelineQueue = buildBatteryTimelineQueue(groupId, modelIDs);
  batteryTimelineScrollKey = "";
  batteryLiveResults = [];
  batteryStartTime = Date.now();

  if (!tests || tests.length === 0 || !testsGroups || testsGroups.length === 0) {
    void api("/api/tests").then((data) => {
      if (data) {
        if (data.tests) tests = data.tests;
        if (data.groups) testsGroups = data.groups;
        batteryTimelineQueue = buildBatteryTimelineQueue(groupId, modelIDs);
        renderBatteryTimeline(batteryLiveResults);
      }
    }).catch(() => {});
  }

  if (batteryElapsedInterval) {
    clearInterval(batteryElapsedInterval);
    batteryElapsedInterval = null;
  }
  batteryElapsedInterval = setInterval(updateBatteryElapsedDisplay, 1000);
  updateBatteryElapsedDisplay();

  const groupBadge = $("battery-progress-group-badge");
  if (groupBadge) {
    if (groupId && groupId !== "all") {
      const g = (typeof groups !== "undefined" && Array.isArray(groups)) ? groups.find((grp) => grp.id === groupId) : null;
      groupBadge.hidden = false;
      groupBadge.textContent = g ? g.name : groupId;
    } else {
      groupBadge.hidden = true;
    }
  }

  initBatteryProgressControls();

  const completedEl = $("battery-completed-tests");
  const headingEl = $("battery-completed-tests-heading");
  if (completedEl) { completedEl.innerHTML = ""; completedEl.hidden = true; }
  if (headingEl) headingEl.hidden = true;

  // Reset UI elements
  const fill = $("battery-progress-fill");
  const count = $("battery-progress-count");
  const timeline = $("battery-timeline");
  if (fill) fill.style.width = "0%";
  if (count) count.textContent = "0 / 0";
  if (timeline) timeline.innerHTML = `<div class="battery-timeline-empty">${escapeHtml(t("battery.starting"))}</div>`;

  hideAllMainViews();
  currentView = "battery-progress";
  $("battery-progress-view").hidden = false;

  batteryProgressModelIDs = modelIDs;
  const initialStats = computeBatteryStats(modelIDs, [], modelIDs[0] || "", 0, batteryTimelineQueue.length);
  renderBatteryKPIs({ model: modelIDs[0] || "", total_tests: batteryTimelineQueue.length, test_index: 1 }, initialStats);
  renderBatteryLeaderboard(modelIDs, initialStats.modelMap, modelIDs[0] || "");
  renderBatteryAnalyticsCharts(modelIDs, initialStats.modelMap);

  localStorage.setItem(BATTERY_KEY, JSON.stringify({ runID, modelIDs, groupId }));
  batteryActiveRunID = runID;
  const progressPath = "/tests/battery/progress/" + runID;
  if (window.location.pathname !== progressPath) {
    history.pushState(null, "", progressPath);
  }
  void pollBatteryProgress(runID, modelIDs);
}

function renderBatteryTimeline(liveResults = []) {
  const container = $("battery-timeline");
  if (!container) return;
  if (batteryTimelineTotal === 0) {
    container.innerHTML = `<div class="battery-timeline-empty">${escapeHtml(t("battery.starting"))}</div>`;
    return;
  }

  let html = "";
  // Completed items
  for (const item of batteryTimelineCompleted) {
    const res = (liveResults || []).find((r) => r.test_id === item.testId && r.model === item.model);
    let dotIcon = "&#10003;";
    let itemClass = "battery-timeline-item completed";
    let metaDetails = "";

    if (res) {
      if (res.passed === true) {
        dotIcon = "&#10003;";
        itemClass = "battery-timeline-item completed";
      } else if (res.passed === false) {
        dotIcon = "&#10005;";
        itemClass = "battery-timeline-item failed";
      }
      const dur = res.response_time_ms > 0 ? (res.response_time_ms / 1000).toFixed(1) + "s" : "";
      const spd = res.tokens_per_sec > 0 ? res.tokens_per_sec.toFixed(1) + " tok/s" : "";
      if (dur || spd) {
        metaDetails = ` &middot; ${dur}${spd ? " (" + spd + ")" : ""}`;
      }
    }

    const catName = getTestCategoryName(item.testId, item.groupId);
    const catBadge = catName ? `<span class="battery-timeline-category-tag">📁 ${escapeHtml(catName)}</span>` : "";

    html += `
      <div class="${itemClass}">
        <div class="battery-timeline-left">
          <div class="battery-timeline-dot">${dotIcon}</div>
          <div class="battery-timeline-line"></div>
        </div>
        <div class="battery-timeline-body">
          <div class="battery-timeline-name">${catBadge}${escapeHtml(item.name || "Test")}</div>
          <div class="battery-timeline-meta">${escapeHtml(item.model || "")}${metaDetails}</div>
        </div>
      </div>
    `;
  }
  // Current item
  if (batteryTimelineCurrent) {
    const caseBadge = (batteryTimelineCurrent.totalCases > 1 && batteryTimelineCurrent.caseName)
      ? `<div class="battery-timeline-case-badge">⚡ ${escapeHtml(batteryTimelineCurrent.caseName)} (${batteryTimelineCurrent.caseIndex}/${batteryTimelineCurrent.totalCases})</div>`
      : "";
    const activeCatName = getTestCategoryName(batteryTimelineCurrent.testId, batteryTimelineCurrent.groupId);
    const activeCatBadge = activeCatName ? `<span class="battery-timeline-category-tag">📁 ${escapeHtml(activeCatName)}</span>` : "";

    html += `
      <div class="battery-timeline-item active">
        <div class="battery-timeline-left">
          <div class="battery-timeline-dot pulse"></div>
          <div class="battery-timeline-line"></div>
        </div>
        <div class="battery-timeline-body">
          <div class="battery-timeline-name">${activeCatBadge}${escapeHtml(batteryTimelineCurrent.name || "Test")}</div>
          ${caseBadge}
          <div class="battery-timeline-meta">
            ${escapeHtml(batteryTimelineCurrent.model || "")}
            ${batteryTimelineCurrent.isThinking ? " &middot; " + escapeHtml(t("battery.status_thinking")) : ""}
          </div>
        </div>
      </div>
    `;
  }
  // Pending items (fill up to total)
  const shown = batteryTimelineCompleted.length + (batteryTimelineCurrent ? 1 : 0);
  const pending = Math.max(0, batteryTimelineTotal - shown);
  for (let i = 0; i < pending; i++) {
    const shownIdx = shown + i + 1;
    const queueItem = batteryTimelineQueue.find((q) => q.index === shownIdx);
    const isLast = i === pending - 1;
    const pendingCatName = queueItem ? getTestCategoryName(queueItem.testId, queueItem.groupId) : "";
    const pendingCatBadge = pendingCatName ? `<span class="battery-timeline-category-tag">📁 ${escapeHtml(pendingCatName)}</span>` : "";
    html += `
      <div class="battery-timeline-item pending">
        <div class="battery-timeline-left">
          <div class="battery-timeline-dot"></div>
          ${isLast ? "" : "<div class=\"battery-timeline-line\"></div>"}
        </div>
        <div class="battery-timeline-body">
          <div class="battery-timeline-name">${pendingCatBadge}${escapeHtml(queueItem ? queueItem.testName : t("battery.status_pending"))}</div>
          ${queueItem ? `<div class="battery-timeline-meta">${escapeHtml(queueItem.model)}</div>` : ""}
        </div>
      </div>
    `;
  }
  container.innerHTML = html;
  requestAnimationFrame(() => scrollBatteryTimelineToActive());
}

function scrollBatteryTimelineToActive() {
  const container = $("battery-timeline");
  if (!container) return;
  const active = container.querySelector(".battery-timeline-item.active");
  if (!active) return;

  const key = batteryTimelineCurrent
    ? `${batteryTimelineCurrent.testId || ""}:${batteryTimelineCurrent.index || 0}`
    : "";
  const taskChanged = key !== batteryTimelineScrollKey;
  if (key) batteryTimelineScrollKey = key;

  active.scrollIntoView({
    behavior: taskChanged ? "smooth" : "auto",
    block: "center",
    inline: "nearest",
  });
}

function updateBatteryProgressUI(p, liveResults = []) {
  const total = p.total_tests || 0;
  const idx = p.test_index || 0;
  const done = p.done || false;

  // Update bar
  const fill = $("battery-progress-fill");
  const count = $("battery-progress-count");
  if (fill && total > 0) {
    const pct = done ? 100 : Math.max(0, Math.min(100, Math.round(((idx - 1) / total) * 100)));
    fill.style.width = pct + "%";
  }
  if (count && total > 0) {
    const displayIdx = done ? total : Math.max(0, idx - 1);
    count.textContent = `${displayIdx} / ${total}`;
  }

  // Update timeline state
  if (total > 0) batteryTimelineTotal = total;

  // Archive previous current into completed when test changes
  if (batteryTimelineCurrent && batteryTimelineCurrent.testId && p.test_id && batteryTimelineCurrent.testId !== p.test_id) {
    batteryTimelineCompleted.push({
      index: batteryTimelineCurrent.index,
      name: batteryTimelineCurrent.name,
      model: batteryTimelineCurrent.model,
      testId: batteryTimelineCurrent.testId,
      groupId: batteryTimelineCurrent.groupId,
    });
  }

  // Set current
  if (p.test_id && !done) {
    batteryTimelineCurrent = {
      index: idx,
      testId: p.test_id,
      name: p.test_name || "",
      model: p.model || "",
      isThinking: p.is_thinking || false,
      caseName: p.case_name || "",
      caseIndex: p.case_index || 0,
      totalCases: p.total_cases || 0,
      groupId: p.group_id || "",
    };
  } else if (done) {
    // Archive final current
    if (batteryTimelineCurrent) {
      batteryTimelineCompleted.push({
        index: batteryTimelineCurrent.index,
        name: batteryTimelineCurrent.name,
        model: batteryTimelineCurrent.model,
        testId: batteryTimelineCurrent.testId,
        groupId: batteryTimelineCurrent.groupId,
      });
    }
    batteryTimelineCurrent = null;
  }

  renderBatteryTimeline(liveResults);
}

function renderBatteryCompletedTests() {
  const container = $("battery-completed-tests");
  const heading = $("battery-completed-tests-heading");
  if (!container) return;
  if (!batteryCompletedTests.length) {
    container.hidden = true;
    if (heading) heading.hidden = true;
    return;
  }
  container.hidden = false;
  if (heading) heading.hidden = false;
  container.innerHTML = batteryCompletedTests.map((item, idx) => {
    const thinkBlock = item.thinking
      ? `<div class="battery-completed-label">${escapeHtml(t("battery.stream_thinking"))}</div><div class="battery-completed-block">${escapeHtml(item.thinking)}</div>`
      : "";
    const responseText = item.response ? escapeHtml(item.response) : `<em class="muted">${escapeHtml(t("battery.no_response"))}</em>`;
    const respBlock = `<div class="battery-completed-label">${escapeHtml(t("battery.stream_response"))}</div><div class="battery-completed-block">${responseText}</div>`;
    return `<details class="battery-completed-item" ${idx === batteryCompletedTests.length - 1 ? "open" : ""}>
      <summary><span>${escapeHtml(item.name)}</span><span class="battery-completed-meta">${escapeHtml(item.model)}</span></summary>
      <div class="battery-completed-body">
        <div class="battery-completed-label">${escapeHtml(t("battery.prompt"))}</div>
        <div class="battery-completed-block">${escapeHtml(item.prompt || "")}</div>
        ${thinkBlock}
        ${respBlock}
      </div>
    </details>`;
  }).join("");
}

async function pollBatteryProgress(runID, modelIDs) {
  // Stale poll (cancelled or user navigated away): stop silently.
  if (runID !== batteryActiveRunID) return;
  if (batteryPollTimer) {
    clearTimeout(batteryPollTimer);
    batteryPollTimer = null;
  }
  try {
    const p = await api("/api/runner/runs/" + encodeURIComponent(runID) + "/progress");
    // Update live results and models from server if provided
    if (p.results && Array.isArray(p.results)) {
      batteryLiveResults = p.results;
    }
    if (p.models && Array.isArray(p.models) && p.models.length > 0) {
      batteryProgressModelIDs = p.models;
    }

    // Detect test change: archive previous snapshot.
    if (batteryLastTestSnapshot && batteryLastTestSnapshot.testId && p.test_id && batteryLastTestSnapshot.testId !== p.test_id) {
      batteryCompletedTests.push(batteryLastTestSnapshot);
      renderBatteryCompletedTests();
    }
    // Update timeline, bar, and count.
    updateBatteryProgressUI(p, batteryLiveResults);

    // Compute live stats and render KPIs, Leaderboard and Analytics charts
    const currentModel = p.model || "";
    const stats = computeBatteryStats(batteryProgressModelIDs, batteryLiveResults, currentModel, p.test_index, p.total_tests);
    renderBatteryKPIs(p, stats);
    renderBatteryLeaderboard(batteryProgressModelIDs, stats.modelMap, currentModel);
    renderBatteryAnalyticsCharts(batteryProgressModelIDs, stats.modelMap);
    updateBatteryElapsedDisplay();

    // Update streaming panel.
    const streamPanel = $("battery-stream-panel");
    const currentTest = tests.find((t) => t.id === p.test_id);
    if (streamPanel && p.test_name && !p.done) {
      streamPanel.hidden = false;

      // Track active turn for the live duration counter
      const turnKey = `${p.model || ""}::${p.test_id || ""}::${p.case_index || 0}::${p.case_name || ""}`;
      if (turnKey !== batteryActiveTurnKey) {
        batteryActiveTurnKey = turnKey;
        batteryTurnStartTime = Date.now();
        batteryThinkingStartTime = p.is_thinking ? Date.now() : 0;
        batteryResponseStartTime = p.partial_response ? Date.now() : 0;
      } else {
        if (p.is_thinking && !batteryThinkingStartTime) {
          batteryThinkingStartTime = Date.now();
        }
        if (p.partial_response && !batteryResponseStartTime) {
          batteryResponseStartTime = Date.now();
        }
      }
      updateBatteryCurrentTurnTimer();

      // Topbar elements
      const testTitleEl = $("battery-stream-test-title");
      const categoryBadgeEl = $("battery-stream-category-badge");
      const casePillEl = $("battery-stream-case-pill");
      const statusBadgeEl = $("battery-stream-status-badge");

      const catName = getTestCategoryName(p.test_id, p.group_id || (currentTest ? currentTest.group_id : ""));
      if (categoryBadgeEl) {
        if (catName) {
          categoryBadgeEl.hidden = false;
          categoryBadgeEl.innerHTML = `📁 ${escapeHtml(catName)}`;
          categoryBadgeEl.title = `Category: ${catName}`;
        } else {
          categoryBadgeEl.hidden = true;
        }
      }
      if (testTitleEl) testTitleEl.textContent = p.test_name;
      if (casePillEl) {
        if (p.total_cases > 1) {
          casePillEl.hidden = false;
          casePillEl.textContent = `${p.case_index || 1}/${p.total_cases} ${p.case_name ? "· " + p.case_name : ""}`;
        } else {
          casePillEl.hidden = true;
        }
      }

      if (statusBadgeEl) {
        if (p.is_thinking) {
          statusBadgeEl.innerHTML = `<span class="badge badge-warn pulse">🧠 ${t("battery.status_thinking")}</span>`;
        } else if (p.partial_response) {
          statusBadgeEl.innerHTML = `<span class="badge badge-pass pulse">⚡ ${t("battery.status_generating")}</span>`;
        } else {
          statusBadgeEl.innerHTML = `<span class="badge badge-primary pulse">⏳ ${t("battery.status_evaluating")}</span>`;
        }
      }

      // Cases progress tracker
      const casesContainer = $("battery-stream-cases-container");
      const casesCountEl = $("battery-stream-cases-count");
      const casesListEl = $("battery-stream-cases-list");

      if (p.total_cases > 1) {
        if (casesContainer) casesContainer.hidden = false;
        if (casesCountEl) casesCountEl.textContent = `${p.case_index || 1} / ${p.total_cases}`;
        if (casesListEl) {
          let casesHtml = "";
          // Precompute unit labels from currentTest if available
          const unitLabels = [];
          if (currentTest && currentTest.cases && Array.isArray(currentTest.cases)) {
            for (let ci = 0; ci < currentTest.cases.length; ci++) {
              const tc = currentTest.cases[ci];
              const cName = tc.name || `Case ${ci + 1}`;
              if (!tc.steps || tc.steps.length === 0) {
                unitLabels.push(cName);
              } else {
                if (tc.prompt) {
                  unitLabels.push(`${cName} › context`);
                }
                for (let si = 0; si < tc.steps.length; si++) {
                  const st = tc.steps[si];
                  const sName = st.name || `Step ${si + 1}`;
                  unitLabels.push(`${cName} › ${sName}`);
                }
              }
            }
          }

          const formatTurnLabel = (fullName) => {
            if (!fullName) return "Case";
            if (fullName.includes(" › ")) {
              const parts = fullName.split(" › ");
              return `<span class="muted">${escapeHtml(parts[0])}</span> <span class="case-chip-sep">›</span> <strong>${escapeHtml(parts.slice(1).join(" › "))}</strong>`;
            }
            return escapeHtml(fullName);
          };

          // Completed cases
          const completed = p.completed_cases || [];
          completed.forEach((c) => {
            const isPass = c.passed === true;
            const isFail = c.passed === false;
            const icon = isPass ? "✔" : (isFail ? "✖" : "•");
            const badgeCls = isPass ? "badge-pass" : (isFail ? "badge-fail" : "badge-human");
            const tps = c.tokens_per_sec > 0 ? `${c.tokens_per_sec.toFixed(1)} tok/s` : "";
            const time = c.response_time_ms > 0 ? fmtDuration(c.response_time_ms) : "";
            const chipTitle = c.name || "Case";
            casesHtml += `
              <div class="battery-stream-case-chip done" title="${escapeHtml(chipTitle)}">
                <span class="badge ${badgeCls}">${icon}</span>
                <span class="case-chip-name">${formatTurnLabel(chipTitle)}</span>
                <span class="case-chip-meta mono muted">${time}${tps ? " · " + tps : ""}</span>
              </div>
            `;
          });
          // Active case
          if (p.case_name || p.case_index) {
            const activeTitle = p.case_name || ("Case " + p.case_index);
            casesHtml += `
              <div class="battery-stream-case-chip active pulse" title="${escapeHtml(activeTitle)}">
                <span class="badge badge-primary">⚡</span>
                <span class="case-chip-name">${formatTurnLabel(activeTitle)}</span>
                <span class="case-chip-meta mono" style="color:var(--accent);">${t("battery.status_evaluating")}</span>
              </div>
            `;
          }
          // Remaining cases
          const currentIdx = p.case_index || (completed.length + 1);
          for (let rem = currentIdx + 1; rem <= p.total_cases; rem++) {
            const remTitle = unitLabels[rem - 1] || `Case ${rem}`;
            casesHtml += `
              <div class="battery-stream-case-chip pending" title="${escapeHtml(remTitle)}">
                <span class="badge badge-muted">⏳</span>
                <span class="case-chip-name muted">${formatTurnLabel(remTitle)}</span>
                <span class="case-chip-meta mono muted">${t("battery.status_pending")}</span>
              </div>
            `;
          }
          casesListEl.innerHTML = casesHtml;
        }
      } else if (casesContainer) {
        casesContainer.hidden = true;
      }

      // Prompt block
      const promptName = $("battery-stream-prompt-name");
      const promptBlock = $("battery-stream-prompt");
      if (promptName) {
        const catPrefix = catName ? `[${catName}] ` : "";
        promptName.textContent = p.case_name ? `${catPrefix}${p.test_name} — ${p.case_name}` : `${catPrefix}${p.test_name}`;
      }
      if (promptBlock) {
        const promptText = p.active_prompt || (currentTest ? currentTest.prompt : "") || "";
        promptBlock.textContent = promptText;
      }

      // Thinking block
      const thinkingWrap = $("battery-stream-thinking-wrap");
      const thinkingBlock = $("battery-stream-thinking");
      if (thinkingWrap && thinkingBlock) {
        thinkingBlock.textContent = p.partial_thinking || "";
        thinkingWrap.hidden = !p.partial_thinking;
        if (p.partial_thinking) {
          thinkingBlock.scrollTo({ top: thinkingBlock.scrollHeight, behavior: "smooth" });
        }
      }

      // Response block
      const responseBlock = $("battery-stream-response");
      if (responseBlock) {
        if (p.partial_response) {
          responseBlock.innerHTML = escapeHtml(p.partial_response) + `<span class="streaming-cursor">▌</span>`;
          responseBlock.scrollTo({ top: responseBlock.scrollHeight, behavior: "smooth" });
        } else if (p.is_thinking) {
          responseBlock.innerHTML = `<em class="muted pulse">🧠 ${t("battery.status_thinking")}…</em>`;
        } else {
          responseBlock.innerHTML = `<em class="muted pulse">⏳ ${t("battery.status_evaluating")}…</em>`;
        }
      }
    } else if (streamPanel) {
      streamPanel.hidden = true;
    }
    // Save snapshot for the current test.
    batteryLastTestSnapshot = {
      testId: p.test_id,
      name: p.test_name || "",
      model: p.model || "",
      prompt: currentTest ? (currentTest.prompt || "") : "",
      thinking: p.partial_thinking || "",
      response: p.partial_response || "",
    };

    // Update model cards (show current + next 2).
    renderBatteryProgressModels(modelIDs, p.model || "", p.is_thinking || false);
    if (p.done) {
      // Archive final snapshot before finishing.
      if (batteryLastTestSnapshot) {
        batteryCompletedTests.push(batteryLastTestSnapshot);
        renderBatteryCompletedTests();
      }
      localStorage.removeItem(BATTERY_KEY);
      // The user may have navigated away while the run finished: only take
      // over the view when this run is still the followed one.
      if (runID !== batteryActiveRunID) return;
      stopBatteryPolling();
      // Let the user read the last response for a moment.
      await new Promise((r) => setTimeout(r, 1500));
      // Fetch full run and show results.
      try {
        const run = await api("/api/runner/runs/" + encodeURIComponent(runID));
        currentBatteryRun = run;
        hideAllMainViews();
        currentView = "battery-results";
        $("battery-results-view").hidden = false;
        history.pushState(null, "", "/tests/battery/results/" + run.id);
        renderBatteryResults(run);
      } catch (err) {
        toast(t("toast.error", { msg: err.message }), "error");
        showTestsView();
      }
      return;
    }
    // The user may have navigated away while the request was in flight:
    // do not reschedule a poll nobody follows anymore.
    if (runID !== batteryActiveRunID) return;
    batteryPollTimer = setTimeout(() => pollBatteryProgress(runID, modelIDs), 2000);
  } catch (err) {
    if (runID !== batteryActiveRunID) return;
    batteryPollRetryCount++;
    if (batteryPollRetryCount < 3) {
      batteryPollTimer = setTimeout(() => pollBatteryProgress(runID, modelIDs), 2000);
      return;
    }
    localStorage.removeItem(BATTERY_KEY);
    toast(t("toast.error", { msg: err.message }), "error");
    showTestsView();
  }
}

async function cancelBatteryRun() {
  stopBatteryPolling();
  const saved = localStorage.getItem(BATTERY_KEY);
  if (!saved) return;
  let runID = "";
  try {
    const data = JSON.parse(saved);
    runID = data.runID || "";
  } catch { }
  if (!runID) return;
  try {
    await api("/api/runner/runs/" + encodeURIComponent(runID) + "/cancel", { method: "POST" });
    localStorage.removeItem(BATTERY_KEY);
    showTestsView();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function skipCurrentBatteryTest() {
  const saved = localStorage.getItem(BATTERY_KEY);
  if (!saved) return;
  let runID = "";
  try {
    const data = JSON.parse(saved);
    runID = data.runID || "";
  } catch { }
  if (!runID) return;
  const skipBtn = $("battery-progress-skip");
  if (skipBtn) skipBtn.disabled = true;
  try {
    const res = await api("/api/runner/runs/" + encodeURIComponent(runID) + "/skip", { method: "POST" });
    if (res?.skipped) {
      toast(t("toast.test_skipped") || "Current test skipped", "info");
      batteryActiveTurnKey = "";
      batteryTurnStartTime = 0;
      batteryThinkingStartTime = 0;
      batteryResponseStartTime = 0;
      updateBatteryCurrentTurnTimer();
      if (batteryActiveRunID === runID) {
        void pollBatteryProgress(runID, []);
      }
    }
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  } finally {
    if (skipBtn) {
      setTimeout(() => {
        if (skipBtn) skipBtn.disabled = false;
      }, 1000);
    }
  }
}

async function confirmBatteryRun() {
  if (batterySelectedModels.size === 0) {
    toast(t("battery.select_models"), "warn");
    return;
  }
  closeBatteryModal();
  const modelIDs = Array.from(batterySelectedModels);
  const payload = { model_ids: modelIDs };
  if (currentRunTarget?.type === "single" && currentRunTarget.testId) {
    payload.test_id = currentRunTarget.testId;
  } else if (currentRunTarget?.type === "group" && currentRunTarget.groupId) {
    payload.group_id = currentRunTarget.groupId;
  } else if (selectedGroupId && selectedGroupId !== "") {
    payload.group_id = selectedGroupId;
  } else {
    payload.group_id = "all";
  }

  // Warn when image sidecars meet models without vision (they would fail).
  try {
    let targetTests = [];
    if (payload.test_id) {
      const one = tests.find((x) => x.id === payload.test_id);
      if (one) targetTests = [one];
    } else {
      const gid = payload.group_id || "all";
      targetTests = tests.filter((x) => (gid === "all" || x.group_id === gid) && x.active && x.evaluation_type !== "agent");
    }
    const hasImages = (x) => [
      ...((x.cases || []).flatMap((c) => c.attachments || [])),
      ...((x.steps || []).flatMap((s) => s.attachments || [])),
      ...(x.sidecars || []),
    ].some((a) => a.kind === "image");
    const withImages = targetTests.filter(hasImages).map((x) => x.name);
    const noVision = modelIDs.filter((m) => !modelCaps(m).has("vision"));
    if (withImages.length > 0 && noVision.length > 0) {
      toast(t("battery.no_vision_warn", { models: noVision.join(", "), tests: withImages.slice(0, 3).join(", ") }), "warn");
    }
  } catch { /* caps lookup is best-effort */ }

  try {
    const data = await api("/api/runner/battery", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const runID = data.run_id;
    if (!runID) {
      toast(t("toast.error", { msg: "No run_id returned" }), "error");
      return;
    }
    showBatteryProgressView(modelIDs, runID, payload.group_id || "all");
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
    showTestsView();
  }
}

function showBatteryResultsView(runId) {
  hideAllMainViews();
  currentView = "battery-results";
  $("battery-results-view").hidden = false;
  void (async () => {
    try {
      if (tests.length === 0) {
        try {
          const data = await api("/api/tests");
          testsGroups = data.groups || [];
          tests = data.tests || [];
        } catch {
          // ignore; renderBatteryResults will treat all tests as non-human-review
        }
      }
      if (currentBatteryRun && currentBatteryRun.id === runId) {
        renderBatteryResults(currentBatteryRun);
      } else {
        const run = await api("/api/runner/runs/" + encodeURIComponent(runId));
        currentBatteryRun = run;
        renderBatteryResults(run);
      }
    } catch (err) {
      toast(t("toast.error", { msg: err.message }), "error");
      showTestsView();
    }
  })();
}

function showBatteryHistoryView(filterTestId = null, filterModel = null) {
  hideAllMainViews();
  currentView = "battery-history";
  currentHistoryFilterTestId = filterTestId || null;
  currentHistoryFilterModel = filterModel || null;
  $("battery-history-view").hidden = false;
  let path = "/tests/battery/history";
  if (filterTestId) {
    path = "/tests/history/" + encodeURIComponent(filterTestId);
  }
  if (filterModel) {
    path += (path.includes("?") ? "&" : "?") + "model=" + encodeURIComponent(filterModel);
  }
  if (window.location.pathname + window.location.search !== path) {
    history.pushState(null, "", path);
  }
  void renderBatteryHistory();
}

let batteryResultsViewMode = "matrix";

// Fractional score for leaderboard: sub-cases count partially, errors and
// pending human reviews are not countable.
function batteryResultScore(r) {
  if (r.error) return null;
  if (r.sub_results && r.sub_results.length > 0) {
    let earned = 0;
    let total = 0;
    for (const s of r.sub_results) {
      if (s.passed === true) {
        earned++;
        total++;
      } else if (s.passed === false) {
        total++;
      }
    }
    return total > 0 ? { earned, total } : null;
  }
  if (r.passed === true) return { earned: 1, total: 1 };
  if (r.passed === false) return { earned: 0, total: 1 };
  return null;
}

// Column-relative heatmap background for leaderboard cells.
function batteryLbHeatStyle(v, range, strong) {
  if (v == null) return "";
  const rel = range.max > range.min ? (v - range.min) / (range.max - range.min) : 0.5;
  const base = strong ? 16 : 4;
  const span = strong ? 44 : 34;
  return `background: color-mix(in srgb, var(--accent) ${(base + rel * span).toFixed(0)}%, transparent);`;
}

// Short display name for a model ("owner/name" -> "name").
function batteryShortModel(m) {
  return escapeHtml(m).replace(/^[^/]+\//, "");
}

// One horizontal bar row: label + track/fill + value.
function batteryBarRow(label, title, pct, text, fillCls) {
  const w = pct == null ? 0 : Math.max(0, Math.min(100, pct));
  return `<div class="battery-chart-row">
    <span class="battery-chart-label" title="${title}">${label}</span>
    <div class="battery-chart-track"><div class="battery-chart-fill ${fillCls || ""}" style="width:${w.toFixed(1)}%"></div></div>
    <span class="battery-chart-val mono">${text}</span>
  </div>`;
}

// Charts view HTML: overall pass bars, per-category grouped bars, speed bars.
// Pure CSS, no dependencies. lbRows must be pre-sorted (best first).
function batteryChartsHtml(run, lbRows, groupIdsPresent, groupName, scores) {
  const scored = (lbRows || []).filter((r) => r.overall != null);
  const withTps = (lbRows || []).filter((r) => r.avgTps > 0);
  if (!scored.length && !withTps.length) {
    return `<div class="battery-empty"><div>${escapeHtml(t("battery.charts_no_data"))}</div></div>`;
  }

  let overallHtml = "";
  if (scored.length) {
    overallHtml = `<section class="battery-chart-section">
      <h4>${escapeHtml(t("battery.charts_overall"))}</h4>
      ${scored.map((row, idx) => batteryBarRow(
        `${idx + 1}. ${batteryShortModel(row.model)}`,
        `${row.model} — ${row.earned}/${row.total}`,
        row.overall,
        `${row.overall.toFixed(1)}%`,
        idx === 0 ? "battery-chart-fill-first" : ""
      )).join("")}
    </section>`;
  }

  let groupsHtml = "";
  const groupSections = (groupIdsPresent || []).map((gid) => {
    const rows = (lbRows || []).map((row) => {
      const c = (scores[row.model] || {})[gid];
      if (!c || c.total === 0) return null;
      const pct = (c.earned / c.total) * 100;
      return { row, pct, text: `${pct.toFixed(0)}%`, title: `${row.model} — ${c.earned}/${c.total}` };
    }).filter(Boolean);
    if (!rows.length) return "";
    return `<div class="battery-chart-group">
      <h5>${escapeHtml(groupName(gid))}</h5>
      ${rows.map((r) => batteryBarRow(
        batteryShortModel(r.row.model), r.title, r.pct, r.text, ""
      )).join("")}
    </div>`;
  }).join("");
  if (groupSections) {
    groupsHtml = `<section class="battery-chart-section">
      <h4>${escapeHtml(t("battery.charts_by_group"))}</h4>
      <div class="battery-chart-groups">${groupSections}</div>
    </section>`;
  }

  let speedHtml = "";
  if (withTps.length) {
    const maxTps = Math.max(...withTps.map((r) => r.avgTps));
    const bySpeed = [...withTps].sort((a, b) => b.avgTps - a.avgTps);
    speedHtml = `<section class="battery-chart-section">
      <h4>${escapeHtml(t("battery.charts_speed"))}</h4>
      ${bySpeed.map((row, idx) => batteryBarRow(
        `${idx + 1}. ${batteryShortModel(row.model)}`,
        `${row.model}`,
        maxTps > 0 ? (row.avgTps / maxTps) * 100 : 0,
        `⚡ ${row.avgTps.toFixed(1)} tok/s`,
        "battery-chart-fill-speed"
      )).join("")}
    </section>`;
  }

  return `<div class="battery-charts">${overallHtml}${groupsHtml}${speedHtml}</div>`;
}

function renderBatteryResults(run) {
  if (!run) return;
  const title = $("battery-results-title");
  if (title) title.textContent = t("battery.results") + " — " + escapeHtml(run.group_name);

  const body = $("battery-results-body");
  if (!body) return;

  // Build per-model stats.
  const modelStats = {};
  for (const m of run.models) {
    modelStats[m] = { pass: 0, fail: 0, human: 0, total: 0, timeSum: 0, reasoning: 0, tpsSum: 0, tpsCount: 0 };
  }
  for (const r of run.results) {
    const s = modelStats[r.model];
    if (!s) continue;
    s.total++;
    s.timeSum += r.response_time_ms;
    if (r.tokens_per_sec > 0) {
      s.tpsSum += r.tokens_per_sec;
      s.tpsCount++;
    }
    if (r.reasoning_used) s.reasoning++;
    if (r.passed === true) s.pass++;
    else if (r.passed === false) s.fail++;
    else s.human++;
  }

  // Summary cards.
  let summaryHtml = `<div class="battery-summary">`;
  for (const m of run.models) {
    const s = modelStats[m];
    const avgMs = s.total > 0 ? Math.round(s.timeSum / s.total) : 0;
    const avgTps = s.tpsCount > 0 ? (s.tpsSum / s.tpsCount).toFixed(1) : null;
    const avgTpsColor = avgTps ? (typeof getToksRecordColor === "function" ? getToksRecordColor(Number(avgTps)) : "") : "";
    const pct = Math.round((s.pass / (s.total || 1)) * 100);
    const okClass = s.pass === s.total && s.total > 0 ? "pill-good" : (s.pass > 0 ? "pill-warn" : "pill-bad");

    summaryHtml += `
      <div class="battery-summary-card">
        <h4>${escapeHtml(m)}</h4>
        <div class="battery-summary-card-body">
          <div class="big">${s.pass} / ${s.total} <span class="pill ${okClass}" style="font-size:12px;margin-left:6px;">${pct}%</span></div>
          <div class="battery-summary-metrics">
            <span class="battery-summary-time mono">⏱️ ${fmtDuration(avgMs)}</span>
            ${avgTps ? `<span class="battery-summary-tps mono" style="color: ${avgTpsColor}">⚡ <strong>${avgTps}</strong> <span class="unit">tok/s</span></span>` : ""}
            ${s.reasoning > 0 ? `<span class="battery-summary-reasoning">🧠 ${s.reasoning}</span>` : ""}
          </div>
        </div>
      </div>
    `;
  }
  summaryHtml += `</div>`;

  // Pending human-review banner: unrated results are easy to miss.
  const pendingReview = run.results.filter((r) => r.passed == null && !r.error);
  if (pendingReview.length > 0) {
    summaryHtml += `<div class="battery-pending-review">⏳ <strong>${escapeHtml(t("battery.pending_review", { n: pendingReview.length }))}</strong> <span class="muted">${escapeHtml(t("battery.pending_review_hint"))}</span></div>`;
  }

  // Detect podium leaders if multiple models
  let podiumHtml = "";
  if (run.models && run.models.length > 1) {
    let bestWinner = null;
    let fastest = null;
    let mostAccurate = null;

    for (const m of run.models) {
      const s = modelStats[m];
      if (!s || s.total === 0) continue;
      const pct = (s.pass / s.total) * 100;
      const avgTps = s.tpsCount > 0 ? (s.tpsSum / s.tpsCount) : 0;
      const avgMs = s.timeSum / s.total;

      if (!fastest || avgTps > fastest.avgTps) {
        fastest = { model: m, avgTps, pct };
      }
      if (!mostAccurate || pct > mostAccurate.pct) {
        mostAccurate = { model: m, pct, avgTps };
      }
      if (!bestWinner) {
        bestWinner = { model: m, pct, avgTps, avgMs };
      } else if (pct > bestWinner.pct) {
        bestWinner = { model: m, pct, avgTps, avgMs };
      } else if (pct === bestWinner.pct && avgTps > bestWinner.avgTps) {
        bestWinner = { model: m, pct, avgTps, avgMs };
      } else if (pct === bestWinner.pct && avgTps === bestWinner.avgTps && avgMs < bestWinner.avgMs) {
        bestWinner = { model: m, pct, avgTps, avgMs };
      }
    }

    if (bestWinner) {
      const wShort = escapeHtml(bestWinner.model).replace(/^[^/]+\//, "");
      const fShort = fastest ? escapeHtml(fastest.model).replace(/^[^/]+\//, "") : "";
      const aShort = mostAccurate ? escapeHtml(mostAccurate.model).replace(/^[^/]+\//, "") : "";

      podiumHtml = `
        <div class="battery-podium-strip">
          <div class="battery-podium-card podium-winner">
            <div class="podium-icon">🏆</div>
            <div class="podium-info">
              <span class="podium-label">${t("battery.podium_winner")}</span>
              <strong class="podium-model" title="${escapeHtml(bestWinner.model)}">${wShort}</strong>
              <span class="podium-sub">${Math.round(bestWinner.pct)}% · ${bestWinner.avgTps.toFixed(1)} tok/s</span>
            </div>
          </div>
          ${fastest && fastest.model !== bestWinner.model ? `
            <div class="battery-podium-card podium-fastest">
              <div class="podium-icon">⚡</div>
              <div class="podium-info">
                <span class="podium-label">${t("battery.podium_fastest")}</span>
                <strong class="podium-model" title="${escapeHtml(fastest.model)}">${fShort}</strong>
                <span class="podium-sub" style="color:var(--accent);">${fastest.avgTps.toFixed(1)} tok/s</span>
              </div>
            </div>
          ` : ""}
          ${mostAccurate && mostAccurate.model !== bestWinner.model ? `
            <div class="battery-podium-card podium-accurate">
              <div class="podium-icon">🎯</div>
              <div class="podium-info">
                <span class="podium-label">${t("battery.podium_accurate")}</span>
                <strong class="podium-model" title="${escapeHtml(mostAccurate.model)}">${aShort}</strong>
                <span class="podium-sub">${Math.round(mostAccurate.pct)}%</span>
              </div>
            </div>
          ` : ""}
        </div>
      `;
    }
  }

  // Group results by test_id.
  const byTest = {};
  for (const r of run.results) {
    if (!byTest[r.test_id]) byTest[r.test_id] = [];
    byTest[r.test_id].push(r);
  }
  const testIds = Object.keys(byTest);

  const isMultiModel = run.models && run.models.length > 1;
  const isMatrix = isMultiModel && batteryResultsViewMode === "matrix";
  const isLeaderboard = isMultiModel && batteryResultsViewMode === "leaderboard";
  const isCharts = isMultiModel && batteryResultsViewMode === "charts";
  const isDetailed = !isMatrix && !isLeaderboard && !isCharts;

  const viewToggleHtml = isMultiModel ? `
    <div class="battery-view-toolbar">
      <div class="battery-view-toggle">
        <button type="button" class="battery-toggle-btn ${isMatrix ? "active" : ""}" id="btn-view-matrix">
          📊 ${t("battery.view_matrix")}
        </button>
        <button type="button" class="battery-toggle-btn ${isLeaderboard ? "active" : ""}" id="btn-view-leaderboard">
          🏆 ${t("battery.view_leaderboard")}
        </button>
        <button type="button" class="battery-toggle-btn ${isCharts ? "active" : ""}" id="btn-view-charts">
          📈 ${t("battery.view_charts")}
        </button>
        <button type="button" class="battery-toggle-btn ${isDetailed ? "active" : ""}" id="btn-view-detailed">
          📋 ${t("battery.view_detailed")}
        </button>
      </div>
    </div>
  ` : "";

  // 1. Matrix Side-by-Side View
  let matrixTableHtml = "";
  if (isMatrix) {
    let headerCols = `<th class="cell-matrix-test-head">${t("battery.matrix_test")}</th>`;
    for (const m of run.models) {
      const shortM = escapeHtml(m).replace(/^[^/]+\//, "");
      headerCols += `<th class="cell-matrix-model-col" title="${escapeHtml(m)}">${shortM}</th>`;
    }

    let matrixRows = "";
    for (const tid of testIds) {
      const results = byTest[tid];
      const test = tests.find((t) => t.id === tid);
      const testName = results[0]?.test_name || tid;
      const evalLabel = test?.evaluation_type ? `<span class="battery-matrix-eval-tag">${escapeHtml(t("tests.eval_" + test.evaluation_type) || test.evaluation_type)}</span>` : "";
      const promptBtn = `<button type="button" class="battery-prompt-link battery-matrix-prompt-link" data-test-id="${escapeHtml(tid)}">${t("battery.prompt")}</button>`;

      let modelCells = "";
      for (const m of run.models) {
        const r = results.find((x) => x.model === m);
        if (!r) {
          modelCells += `<td class="cell-matrix-result cell-matrix-empty"><span class="muted">—</span></td>`;
          continue;
        }

        let badge = "";
        let scorePill = "";
        if (r.error) {
          badge = `<span class="badge badge-na" title="${escapeHtml(r.error)}">${t("battery.error")}</span>`;
        } else if (r.sub_results && r.sub_results.length > 0) {
          const pCount = r.sub_results.filter((s) => s.passed === true).length;
          const tCount = r.sub_results.length;
          const pillCls = pCount === tCount ? "pill-good" : (pCount > 0 ? "pill-warn" : "pill-bad");
          scorePill = `<span class="pill ${pillCls}">✔ ${pCount}/${tCount}</span>`;
        } else if (r.passed === true) {
          badge = `<span class="badge badge-pass">${t("battery.pass")}</span>`;
        } else if (r.passed === false) {
          badge = `<span class="badge badge-fail">${t("battery.fail")}</span>`;
        } else {
          badge = `<span class="badge badge-human">${t("battery.human_review")}</span>`;
        }

        const tpsStr = r.tokens_per_sec > 0 ? `${r.tokens_per_sec.toFixed(1)} tok/s` : "—";
        const tpsColor = (typeof getToksRecordColor === "function" && r.tokens_per_sec > 0) ? getToksRecordColor(r.tokens_per_sec) : "";
        const timeStr = r.response_time_ms > 0 ? fmtDuration(r.response_time_ms) : "";
        const reasoning = r.reasoning_used ? "🧠" : "";

        modelCells += `
          <td class="cell-matrix-result">
            <div class="matrix-cell-content">
              <div class="matrix-cell-top">
                ${scorePill || badge}
                <button type="button" class="ghost battery-matrix-resp-btn" data-test-id="${escapeHtml(tid)}" data-model="${escapeHtml(m)}" title="${t("chat.response")}">↗</button>
              </div>
              <div class="matrix-cell-metrics mono">
                <span class="matrix-cell-time muted">⏱️ ${timeStr} ${reasoning}</span>
                <span class="matrix-cell-tps" style="color:${tpsColor};">⚡ ${escapeHtml(tpsStr)}</span>
              </div>
            </div>
          </td>
        `;
      }

      matrixRows += `
        <tr>
          <td class="cell-matrix-test-info">
            <div class="matrix-test-title"><strong>${escapeHtml(testName)}</strong></div>
            <div class="matrix-test-meta">${evalLabel} ${promptBtn}</div>
          </td>
          ${modelCells}
        </tr>
      `;
    }

    // Summary footer row
    let footerCells = `<td class="cell-matrix-test-info"><strong>${t("battery.matrix_summary")}</strong></td>`;
    for (const m of run.models) {
      const s = modelStats[m];
      const avgMs = s && s.total > 0 ? Math.round(s.timeSum / s.total) : 0;
      const avgTps = s && s.tpsCount > 0 ? (s.tpsSum / s.tpsCount).toFixed(1) : "—";
      const avgTpsColor = avgTps !== "—" ? (typeof getToksRecordColor === "function" ? getToksRecordColor(Number(avgTps)) : "") : "";
      const pct = s && s.total > 0 ? Math.round((s.pass / s.total) * 100) : 0;
      const pillCls = s && s.pass === s.total && s.total > 0 ? "pill-good" : (s && s.pass > 0 ? "pill-warn" : "pill-bad");

      footerCells += `
        <td class="cell-matrix-footer">
          <div class="matrix-footer-score">
            <span class="pill ${pillCls}">${s ? s.pass : 0}/${s ? s.total : 0} (${pct}%)</span>
          </div>
          <div class="matrix-footer-metrics mono">
            <span class="muted">⏱️ ${fmtDuration(avgMs)}</span>
            <span style="color:${avgTpsColor}; font-weight:600;">⚡ ${avgTps} tok/s</span>
          </div>
        </td>
      `;
    }

    matrixTableHtml = `
      <div class="battery-table-wrap battery-matrix-wrap">
        <table class="battery-table battery-matrix-table">
          <thead>
            <tr>${headerCols}</tr>
          </thead>
          <tbody>${matrixRows}</tbody>
          <tfoot>
            <tr class="matrix-footer-row">${footerCells}</tr>
          </tfoot>
        </table>
      </div>
    `;
  }

  // Shared leaderboard/charts data: groups present, per-model per-group
  // scores, and overall ranking.
  const groupIdsPresent = [];
  for (const tid of testIds) {
    const test = tests.find((x) => x.id === tid);
    const gid = test?.group_id || "";
    if (!groupIdsPresent.includes(gid)) groupIdsPresent.push(gid);
  }
  groupIdsPresent.sort((a, b) => {
    const ga = testsGroups.find((g) => g.id === a);
    const gb = testsGroups.find((g) => g.id === b);
    const oa = ga && typeof ga.order === "number" ? ga.order : Number.MAX_SAFE_INTEGER;
    const ob = gb && typeof gb.order === "number" ? gb.order : Number.MAX_SAFE_INTEGER;
    if (oa !== ob) return oa - ob;
    return String(ga?.name || a).localeCompare(String(gb?.name || b));
  });
  const groupName = (gid) => {
    if (!gid) return t("battery.leaderboard_uncategorized");
    const g = testsGroups.find((x) => x.id === gid);
    return g?.name || gid;
  };

  // Scores per model per group.
  const scores = {};
  for (const m of run.models) scores[m] = {};
  for (const r of run.results) {
    const sc = batteryResultScore(r);
    if (!sc || !scores[r.model]) continue;
    const test = tests.find((x) => x.id === r.test_id);
    const gid = test?.group_id || "";
    const cell = (scores[r.model][gid] ||= { earned: 0, total: 0 });
    cell.earned += sc.earned;
    cell.total += sc.total;
  }

  // Overall per model and ranking.
  const lbRows = run.models.map((m) => {
    let earned = 0;
    let total = 0;
    for (const gid of groupIdsPresent) {
      const c = scores[m][gid];
      if (c) {
        earned += c.earned;
        total += c.total;
      }
    }
    const s = modelStats[m];
    const avgTps = s && s.tpsCount > 0 ? s.tpsSum / s.tpsCount : 0;
    return { model: m, earned, total, overall: total > 0 ? (earned / total) * 100 : null, avgTps };
  });
  lbRows.sort((a, b) => (b.overall ?? -1) - (a.overall ?? -1) || b.avgTps - a.avgTps);

  // 2. Leaderboard View (models as rows, categories as columns, heatmap scores)
  let leaderboardTableHtml = "";
  if (isLeaderboard) {
    // Column ranges for the heatmap (column-relative, like public leaderboards).
    const colRange = {};
    for (const gid of groupIdsPresent) {
      const vals = lbRows
        .map((row) => {
          const c = scores[row.model][gid];
          return c && c.total > 0 ? (c.earned / c.total) * 100 : null;
        })
        .filter((v) => v != null);
      colRange[gid] = {
        min: vals.length ? Math.min(...vals) : 0,
        max: vals.length ? Math.max(...vals) : 0,
      };
    }
    const overallVals = lbRows.map((r) => r.overall).filter((v) => v != null);
    const overallRange = {
      min: overallVals.length ? Math.min(...overallVals) : 0,
      max: overallVals.length ? Math.max(...overallVals) : 0,
    };
    const heatStyle = batteryLbHeatStyle;
    let lbHeaderCols = `<th class="cell-lb-overall-head">${t("battery.leaderboard_overall")}</th>`;
    for (const gid of groupIdsPresent) {
      lbHeaderCols += `<th class="cell-lb-group-head" title="${escapeHtml(groupName(gid))}">${escapeHtml(groupName(gid))}</th>`;
    }

    let lbBodyRows = "";
    lbRows.forEach((row, idx) => {
      let cells = "";
      if (row.overall == null) {
        cells += `<td class="cell-lb-score cell-lb-overall cell-lb-empty"><span class="muted">—</span></td>`;
      } else {
        cells += `<td class="cell-lb-score cell-lb-overall mono" style="${heatStyle(row.overall, overallRange, true)}" title="${row.earned}/${row.total}">${row.overall.toFixed(1)}</td>`;
      }
      for (const gid of groupIdsPresent) {
        const c = scores[row.model][gid];
        if (!c || c.total === 0) {
          cells += `<td class="cell-lb-score cell-lb-empty"><span class="muted">—</span></td>`;
          continue;
        }
        const pct = (c.earned / c.total) * 100;
        cells += `<td class="cell-lb-score mono" style="${heatStyle(pct, colRange[gid], false)}" title="${c.earned}/${c.total}">${pct.toFixed(1)}</td>`;
      }
      lbBodyRows += `
        <tr class="${idx === 0 ? "lb-row-first" : ""}">
          <td class="cell-lb-model">
            <span class="lb-rank">${idx + 1}</span>
            <strong class="lb-model-name" title="${escapeHtml(row.model)}">${escapeHtml(row.model).replace(/^[^/]+\//, "")}</strong>
          </td>
          ${cells}
        </tr>
      `;
    });

    leaderboardTableHtml = `
      <div class="battery-table-wrap battery-lb-wrap">
        <table class="battery-table battery-lb-table">
          <thead>
            <tr>
              <th class="cell-lb-model-head">${t("chat.model")}</th>
              ${lbHeaderCols}
            </tr>
          </thead>
          <tbody>${lbBodyRows}</tbody>
        </table>
      </div>
    `;
  }

  // 2b. Charts View (pure-CSS bars: overall, by category, speed).
  let chartsHtml = "";
  if (isCharts) {
    chartsHtml = batteryChartsHtml(run, lbRows, groupIdsPresent, groupName, scores);
  }

  // 3. Detailed Table View
  let detailedTableHtml = "";
  if (isDetailed) {
    let rowsHtml = "";
    for (const tid of testIds) {
      const results = byTest[tid];
      const test = tests.find((t) => t.id === tid);
      const isHumanReview = test?.evaluation_type === "human_review";
      const testName = results[0]?.test_name || tid;
      const evalLabel = test?.evaluation_type ? `<div class="battery-eval-label">${escapeHtml(t("tests.eval_" + test.evaluation_type) || test.evaluation_type)}</div>` : "";
      const promptBtn = `<div class="battery-prompt-link-wrap"><button type="button" class="battery-prompt-link" data-test-id="${escapeHtml(tid)}">${t("battery.prompt")}</button></div>`;
      const humanReviewLabel = isHumanReview
        ? `<div class="battery-human-review-label">${t("battery.human_review")}</div>`
        : "";

      for (let i = 0; i < results.length; i++) {
        const r = results[i];
        let resultCell = "";
        if (isHumanReview) {
          resultCell = `
            <div class="battery-pass-fail" data-test-id="${escapeHtml(r.test_id)}" data-model="${escapeHtml(r.model)}">
              <button type="button" data-passed="true" class="${r.passed === true ? "active" : ""}">${t("battery.pass")}</button>
              <button type="button" data-passed="false" class="${r.passed === false ? "active" : ""}">${t("battery.fail")}</button>
            </div>
          `;
        } else {
          const errLower = (r.error || "").toLowerCase();
          const isLoop = errLower.includes("loop") || errLower.includes("bucle");
          const isSkip = errLower.includes("skip") || errLower.includes("salteo");
          const hasRealResponse = (r.tokens_per_sec || 0) > 0 && (r.model_response || "").trim().length > 0;
          if (r.passed === false) {
            if (isLoop) {
              const reasonText = t("battery.loop_detected") || "Repetition loop";
              resultCell = `<span class="badge badge-fail" title="${escapeHtml(r.error || reasonText)}">✖ ${t("battery.fail")} (${escapeHtml(reasonText)})</span>`;
            } else if (isSkip) {
              const reasonText = t("battery.skipped_manually") || "Manually skipped";
              resultCell = `<span class="badge badge-fail" title="${escapeHtml(r.error || reasonText)}">✖ ${t("battery.fail")} (${escapeHtml(reasonText)})</span>`;
            } else {
              resultCell = `<span class="badge badge-fail" ${r.error ? `title="${escapeHtml(r.error)}"` : ""}>${t("battery.fail")}</span>`;
            }
          } else if (r.error) {
            resultCell = `<span class="badge badge-na" title="${escapeHtml(r.error)}">${t("battery.error")}</span>`;
          } else if (!hasRealResponse && r.passed === false) {
            resultCell = `<span class="badge badge-na" title="${escapeHtml(r.model_response || t("battery.no_response"))}">${t("battery.error")}</span>`;
          } else if (r.passed === true) {
            resultCell = `<span class="badge badge-pass">${t("battery.pass")}</span>`;
          } else {
            resultCell = `<span class="badge badge-human">${t("battery.human_review")}</span>`;
          }
        }

        const reasoningIcon = r.reasoning_used ? "🧠" : "";
        const tokColor = (typeof getToksRecordColor === "function" && r.tokens_per_sec > 0) ? getToksRecordColor(r.tokens_per_sec) : "";
        const resp = r.model_response || "";
        const respId = `br-${run.id}-${r.test_id}-${escapeHtml(r.model)}`;
        const respShort = escapeHtml(resp.slice(0, 200));
        const respRest = escapeHtml(resp.slice(200));

        let responseCellHtml = "";
        if (r.sub_results && r.sub_results.length > 0) {
          responseCellHtml = `
            <div class="battery-subresults-list">
              ${r.sub_results.map((sub, sidx) => {
                const isPass = sub.passed === true;
                const isFail = sub.passed === false;
                const badgeClass = isPass ? "badge-pass" : (isFail ? "badge-fail" : "badge-human");
                const statusIcon = isPass ? "✔" : (isFail ? "✖" : "•");
                const name = sub.name || `Case #${sub.index + 1 || sidx + 1}`;
                const subErrLower = (sub.error || "").toLowerCase();
                const isSubLoop = subErrLower.includes("loop") || subErrLower.includes("bucle");
                const isSubSkip = subErrLower.includes("skip") || subErrLower.includes("salteo");
                let subPillHtml = "";
                if (isSubLoop) {
                  subPillHtml = `<span class="badge badge-fail" style="font-size:10px; padding:1px 5px; margin-left:6px;" title="${escapeHtml(sub.error)}">🔁 ${escapeHtml(t("battery.loop_detected") || "Loop")}</span>`;
                } else if (isSubSkip) {
                  subPillHtml = `<span class="badge badge-warn" style="font-size:10px; padding:1px 5px; margin-left:6px;" title="${escapeHtml(sub.error)}">⏭️ ${escapeHtml(t("battery.skipped_manually") || "Skipped")}</span>`;
                }
                const tpsColor = (typeof getToksRecordColor === "function" && sub.tokens_per_sec > 0) ? getToksRecordColor(sub.tokens_per_sec) : "";
                const timeStr = sub.response_time_ms > 0 ? fmtDuration(sub.response_time_ms) : "";
                const tpsStr = sub.tokens_per_sec > 0 ? `${sub.tokens_per_sec.toFixed(1)} tok/s` : "";

                return `
                  <div class="battery-subresult-row">
                    <div class="battery-subresult-left">
                      <span class="badge ${badgeClass} battery-subresult-pill">${statusIcon}</span>
                      <span class="battery-subresult-title">${escapeHtml(name)}${subPillHtml}</span>
                    </div>
                    <div class="battery-subresult-right">
                      ${timeStr ? `<span class="battery-subresult-time mono muted">⏱️ ${timeStr}</span>` : ""}
                      ${tpsStr ? `<span class="battery-subresult-tps mono" style="color:${tpsColor}">⚡ ${tpsStr}</span>` : ""}
                      <button type="button" class="ghost battery-subresult-btn" data-test-id="${escapeHtml(r.test_id)}" data-model="${escapeHtml(r.model)}" data-sub-idx="${sidx}" title="${t("chat.response")}">
                        ${t("action.view") || "View"} ↗
                      </button>
                    </div>
                  </div>
                `;
              }).join("")}
            </div>
          `;
        } else {
          responseCellHtml = `
            <div class="battery-single-response">
              <div class="resp-text-wrap">
                <span class="resp-short">${respShort}${resp.length > 200 ? `<button type="button" class="resp-toggle" data-target="${respId}">…</button>` : ""}</span>
                ${resp.length > 200 ? `<span class="resp-rest" id="${respId}" hidden>${respRest}</span>` : ""}
              </div>
              ${resp.length > 0 ? `
                <button type="button" class="ghost battery-single-raw-btn" data-test-id="${escapeHtml(r.test_id)}" data-model="${escapeHtml(r.model)}">
                  ${t("action.view") || "View"} ↗
                </button>
              ` : ""}
            </div>
          `;
        }

        rowsHtml += `
          <tr>
            ${i === 0 ? `<td class="cell-test" rowspan="${results.length}"><strong>${escapeHtml(testName)}</strong>${evalLabel}${humanReviewLabel}${promptBtn}</td>` : ""}
            <td class="cell-model">${escapeHtml(r.model)}</td>
            <td>${resultCell}</td>
            <td class="cell-time">
              <div class="battery-res-time mono">⏱️ ${fmtDuration(r.response_time_ms)} ${reasoningIcon}</div>
              ${r.tokens_per_sec > 0
                ? `<div class="battery-res-tps mono" style="color: ${tokColor}">⚡ <strong>${r.tokens_per_sec.toFixed(1)}</strong> <span class="unit">tok/s</span></div>`
                : `<div class="battery-res-tps mono muted">— <span class="unit">tok/s</span></div>`
              }
            </td>
            <td class="cell-response">
              ${responseCellHtml}
            </td>
          </tr>
        `;
      }
    }

    detailedTableHtml = `
      <div class="battery-table-wrap">
        <table class="battery-table">
          <thead>
            <tr>
              <th>${t("tests.name")}</th>
              <th>${t("chat.model")}</th>
              <th>${t("battery.results")}</th>
              <th>${t("battery.response_time")}</th>
              <th>${t("chat.response")}</th>
            </tr>
          </thead>
          <tbody>${rowsHtml}</tbody>
        </table>
      </div>
    `;
  }

  body.innerHTML = podiumHtml + summaryHtml + viewToggleHtml + matrixTableHtml + leaderboardTableHtml + chartsHtml + detailedTableHtml;

  const btnMatrix = body.querySelector("#btn-view-matrix");
  if (btnMatrix) {
    btnMatrix.addEventListener("click", () => {
      batteryResultsViewMode = "matrix";
      renderBatteryResults(run);
    });
  }
  const btnLeaderboard = body.querySelector("#btn-view-leaderboard");
  if (btnLeaderboard) {
    btnLeaderboard.addEventListener("click", () => {
      batteryResultsViewMode = "leaderboard";
      renderBatteryResults(run);
    });
  }
  const btnCharts = body.querySelector("#btn-view-charts");
  if (btnCharts) {
    btnCharts.addEventListener("click", () => {
      batteryResultsViewMode = "charts";
      renderBatteryResults(run);
    });
  }
  const btnDetailed = body.querySelector("#btn-view-detailed");
  if (btnDetailed) {
    btnDetailed.addEventListener("click", () => {
      batteryResultsViewMode = "detailed";
      renderBatteryResults(run);
    });
  }

  body.querySelectorAll(".battery-matrix-resp-btn").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const testId = btn.dataset.testId;
      const model = btn.dataset.model;
      const res = run.results.find((x) => x.test_id === testId && x.model === model);
      const titleEl = $("response-view-modal-title");
      if (titleEl) titleEl.textContent = `${res?.test_name || testId} (${model})`;
      openResponseViewModal(model, res?.model_response || res?.error || t("battery.no_response"));
    });
  });

  body.querySelectorAll(".resp-toggle").forEach((btn) => {
    btn.addEventListener("click", () => {
      const target = document.getElementById(btn.dataset.target);
      if (!target) return;
      target.hidden = !target.hidden;
      btn.textContent = target.hidden ? "…" : "▲";
    });
  });

  body.querySelectorAll(".battery-subresult-btn").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const testId = btn.dataset.testId;
      const model = btn.dataset.model;
      const sidx = Number(btn.dataset.subIdx);
      const res = run.results.find((x) => x.test_id === testId && x.model === model);
      const sub = res?.sub_results?.[sidx];
      const caseName = sub?.name || `Case #${sidx + 1}`;
      const titleEl = $("response-view-modal-title");
      if (titleEl) titleEl.textContent = `${res?.test_name || testId} — ${caseName}`;
      let detail = "";
      if (sub?.options?.temperature != null) detail += `${t("battery.temperature_label")}: ${sub.options.temperature}\n\n`;
      if (sub?.system_prompt) detail += `${t("battery.system_label")}\n${sub.system_prompt}\n\n`;
      if (sub?.prompt) detail += `${t("battery.user_label")}\n${sub.prompt}\n\n`;
      detail += `${t("battery.assistant_label")}\n${sub?.model_response || sub?.error || t("battery.no_response")}`;
      if (sub?.error) {
        const subErrLower = sub.error.toLowerCase();
        const tag = subErrLower.includes("loop") ? (t("battery.loop_detected") || "Repetition loop detected") : (subErrLower.includes("skip") ? (t("battery.skipped_manually") || "Manually skipped") : sub.error);
        detail += `\n\n[${tag}]`;
      }
      openResponseViewModal(model, detail);
    });
  });

  body.querySelectorAll(".battery-single-raw-btn").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const testId = btn.dataset.testId;
      const model = btn.dataset.model;
      const res = run.results.find((x) => x.test_id === testId && x.model === model);
      const titleEl = $("response-view-modal-title");
      if (titleEl) titleEl.textContent = `${res?.test_name || testId} (${model})`;
      let detail = res?.model_response || res?.error || t("battery.no_response");
      if (res?.error && res?.model_response) {
        const errLower = res.error.toLowerCase();
        const tag = errLower.includes("loop") ? (t("battery.loop_detected") || "Repetition loop detected") : (errLower.includes("skip") ? (t("battery.skipped_manually") || "Manually skipped") : res.error);
        detail += `\n\n[${tag}]`;
      }
      openResponseViewModal(model, detail);
    });
  });

  body.querySelectorAll(".battery-pass-fail button").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const wrap = btn.closest(".battery-pass-fail");
      const testId = wrap.dataset.testId;
      const model = wrap.dataset.model;
      const passed = btn.dataset.passed === "true";
      try {
        await submitTestResult(run, testId, model, passed);
      } catch (err) {
        toast(t("toast.error", { msg: err.message }), "error");
      }
    });
  });

  body.querySelectorAll(".battery-prompt-link").forEach((btn) => {
    btn.addEventListener("click", () => {
      const testId = btn.dataset.testId;
      const firstResult = run.results.find((r) => r.test_id === testId);
      if (firstResult) {
        openHumanReviewModal(run, testId, firstResult.model);
      }
    });
  });
}

async function submitTestResult(run, testId, model, passed) {
  await api("/api/runner/runs/" + encodeURIComponent(run.id) + "/rate", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ test_id: testId, model, passed }),
  });
  const result = run.results.find((r) => r.test_id === testId && r.model === model);
  if (result) {
    result.passed = passed;
  }
  renderBatteryResults(run);
  toast(t("battery.review_saved"), "success");
}

function openHumanReviewModal(run, testId, model) {
  const test = tests.find((t) => t.id === testId);
  if (!test) return;

  const titleEl = $("human-review-modal-title");
  if (titleEl) titleEl.textContent = t("battery.prompt") + " — " + escapeHtml(test.name);

  // Prompt
  const promptEl = $("human-review-prompt");
  if (promptEl) promptEl.textContent = test.prompt || "";

  // System prompt
  const sysEl = $("human-review-system");
  if (sysEl) {
    sysEl.textContent = test.system_prompt || "";
    sysEl.parentElement.hidden = !test.system_prompt;
    if (sysEl.previousElementSibling) sysEl.previousElementSibling.hidden = !test.system_prompt;
  }

  // Evaluation
  const evalTypeEl = $("human-review-eval-type");
  const evalConfigEl = $("human-review-eval-config");
  if (evalTypeEl) {
    const evalName = t("tests.eval_" + test.evaluation_type) || test.evaluation_type || "";
    evalTypeEl.textContent = evalName;
  }
  if (evalConfigEl) {
    let cfgText = "";
    let cfgObj = test.evaluation_config;
    if (cfgObj) {
      if (typeof cfgObj === "string") {
        try { cfgObj = JSON.parse(cfgObj); } catch { cfgObj = null; }
      }
      if (cfgObj && typeof cfgObj === "object") {
        if (cfgObj.expected !== undefined) cfgText = "Expected: " + String(cfgObj.expected);
        else if (cfgObj.pattern !== undefined) cfgText = "Pattern: " + String(cfgObj.pattern);
        else if (cfgObj.schema !== undefined) cfgText = "Schema: " + JSON.stringify(cfgObj.schema, null, 2);
        else cfgText = JSON.stringify(cfgObj, null, 2);
      } else if (cfgObj) {
        cfgText = String(cfgObj);
      }
    }
    evalConfigEl.textContent = cfgText;
    evalConfigEl.parentElement.hidden = !cfgText;
  }

  // Attachments: union of per-case / per-step sidecars (+ legacy simple).
  const attachEl = $("human-review-attachments");
  if (attachEl) {
    const allAtts = [
      ...((test.cases || []).flatMap((c) => c.attachments || [])),
      ...((test.steps || []).flatMap((s) => s.attachments || [])),
      ...(test.sidecars || []),
    ];
    const attHtml = allAtts.map((att) => {
      if (att.kind === "image") {
        const src = `data:${att.mime || "image/jpeg"};base64,${att.data}`;
        return `<div class="hr-attach-item"><img src="${src}" alt="${escapeHtml(att.name || "")}" class="hr-attach-img" /><span class="hr-attach-name">${escapeHtml(att.name || "")}</span></div>`;
      }
      if (att.kind === "audio") {
        const src = `data:${att.mime || "audio/webm"};base64,${att.data}`;
        return `<div class="hr-attach-item"><audio controls src="${src}" class="hr-attach-audio"></audio><span class="hr-attach-name">${escapeHtml(att.name || "")}</span></div>`;
      }
      return `<div class="hr-attach-item"><span class="pill">txt</span><span class="hr-attach-name">${escapeHtml(att.name || "")}</span></div>`;
    }).join("");
    attachEl.innerHTML = attHtml || `<div class="muted">${t("battery.no_attachments")}</div>`;
  }

  $("human-review-modal").hidden = false;
}

function closeHumanReviewModal() {
  $("human-review-modal").hidden = true;
}

function openResponseViewModal(model, response) {
  const modelEl = $("response-view-model");
  const contentEl = $("response-view-content");
  if (modelEl) modelEl.textContent = model || "—";
  if (contentEl) contentEl.textContent = response || "";
  $("response-view-modal").hidden = false;
}

function closeResponseViewModal() {
  $("response-view-modal").hidden = true;
}

async function renderBatteryHistory() {
  const body = $("battery-history-body");
  if (!body) return;
  body.innerHTML = `<div class="muted">${t("status.loading")}</div>`;
  try {
    const data = await api("/api/runner/runs");
    let allRuns = data.runs || [];

    // Setup header model dropdown
    const modelSel = $("battery-history-model-select");
    if (modelSel) {
      const activeModels = (typeof models !== "undefined" ? models : []).filter((m) => !m.archived);
      let opts = `<option value="">${escapeHtml(t("tests.all_models"))}</option>`;
      for (const m of activeModels) {
        const sel = m.name === currentHistoryFilterModel;
        opts += `<option value="${escapeHtml(m.name)}" ${sel ? "selected" : ""}>${escapeHtml(m.name)}</option>`;
      }
      modelSel.innerHTML = opts;
      if (!modelSel.dataset.wired) {
        modelSel.dataset.wired = "1";
        modelSel.addEventListener("change", () => {
          currentHistoryFilterModel = modelSel.value || null;
          let path = "/tests/battery/history";
          if (currentHistoryFilterTestId) path = "/tests/history/" + encodeURIComponent(currentHistoryFilterTestId);
          if (currentHistoryFilterModel) path += (path.includes("?") ? "&" : "?") + "model=" + encodeURIComponent(currentHistoryFilterModel);
          history.pushState(null, "", path);
          void renderBatteryHistory();
        });
      }
    }

    let runs = allRuns;
    if (currentHistoryFilterTestId) {
      runs = runs.filter((r) => (r.test_ids || []).includes(currentHistoryFilterTestId) || (r.results || []).some((res) => res.test_id === currentHistoryFilterTestId));
    }
    if (currentHistoryFilterModel) {
      runs = runs.filter((r) => (r.models || []).includes(currentHistoryFilterModel));
    }

    let bannerHtml = "";
    if (currentHistoryFilterTestId || currentHistoryFilterModel) {
      const test = currentHistoryFilterTestId ? tests.find((t) => t.id === currentHistoryFilterTestId) : null;
      const testName = test?.name || currentHistoryFilterTestId;
      const filterParts = [];
      if (testName) filterParts.push(t("battery.history_for", { name: testName }));
      if (currentHistoryFilterModel) filterParts.push(`🤖 ${currentHistoryFilterModel}`);
      const lbBtn = currentHistoryFilterTestId
        ? `<button type="button" class="primary battery-mini-btn" id="battery-history-open-leaderboard" style="margin-left:auto;margin-right:8px;">🏆 ${t("tests.leaderboard_title")}</button>`
        : "";
      bannerHtml = `
        <div class="battery-history-filter-banner">
          <span>${escapeHtml(filterParts.join(" · "))}</span>
          ${lbBtn}
          <button type="button" class="ghost battery-mini-btn" id="battery-history-clear-filter">✕ ${t("analytics.source_all")}</button>
        </div>
      `;
    }

    let modelSummaryHtml = "";
    if (currentHistoryFilterModel) {
      let modelTotalTests = 0;
      let modelPassTests = 0;
      let modelTimeSum = 0;
      let modelTpsSum = 0;
      let modelTpsCount = 0;

      for (const run of runs) {
        for (const res of run.results || []) {
          if (res.model === currentHistoryFilterModel) {
            modelTotalTests++;
            if (res.passed === true) modelPassTests++;
            modelTimeSum += res.response_time_ms || 0;
            if (res.tokens_per_sec > 0) {
              modelTpsSum += res.tokens_per_sec;
              modelTpsCount++;
            }
          }
        }
      }

      const avgMs = modelTotalTests > 0 ? Math.round(modelTimeSum / modelTotalTests) : 0;
      const avgTps = modelTpsCount > 0 ? (modelTpsSum / modelTpsCount).toFixed(1) : null;
      const avgTpsColor = avgTps ? (typeof getToksRecordColor === "function" ? getToksRecordColor(Number(avgTps)) : "") : "";
      const passPct = modelTotalTests > 0 ? Math.round((modelPassTests / modelTotalTests) * 100) : 0;
      const passClass = passPct === 100 ? "pill-good" : (passPct > 0 ? "pill-warn" : "pill-bad");

      modelSummaryHtml = `
        <div class="battery-history-model-summary">
          <div class="battery-history-model-summary-left">
            <h3>🤖 ${escapeHtml(currentHistoryFilterModel)}</h3>
            <p>${escapeHtml(t("battery.model_stats_title"))}</p>
          </div>
          <div class="battery-history-model-summary-stats">
            <div class="battery-history-model-stat-item">
              <span class="battery-history-model-stat-val pill ${passClass}" style="font-size:16px;">${modelPassTests} / ${modelTotalTests} (${passPct}%)</span>
              <span class="battery-history-model-stat-lbl">${escapeHtml(t("battery.model_overall_pass"))}</span>
            </div>
            <div class="battery-history-model-stat-item">
              <span class="battery-history-model-stat-val mono">⏱️ ${fmtDuration(avgMs)}</span>
              <span class="battery-history-model-stat-lbl">${escapeHtml(t("battery.response_time"))}</span>
            </div>
            ${avgTps ? `
              <div class="battery-history-model-stat-item">
                <span class="battery-history-model-stat-val mono" style="color:${avgTpsColor}">⚡ ${avgTps} tok/s</span>
                <span class="battery-history-model-stat-lbl">${escapeHtml(t("battery.avg_tok_sec"))}</span>
              </div>
            ` : ""}
          </div>
        </div>
      `;
    }

    if (runs.length === 0) {
      const emptyAction = currentHistoryFilterTestId
        ? `<div style="margin-top:12px;"><button type="button" class="primary" id="battery-empty-open-lb">🏆 ${t("tests.leaderboard_title")}</button></div>`
        : "";
      body.innerHTML = bannerHtml + modelSummaryHtml + `
        <div class="battery-empty">
          <div>${t("battery.no_history")}</div>
          ${emptyAction}
        </div>`;
      setupHistoryClearFilterListener();
      return;
    }

    // Trend chart: pass rate of recent runs, oldest → newest.
    let trendHtml = "";
    {
      const trendRuns = [...runs].reverse().slice(-15);
      const rows = trendRuns.map((run) => {
        const pass = run.pass_count || 0;
        const total = run.total_count || 0;
        if (!total) return null;
        const pct = (pass / total) * 100;
        const date = String(run.timestamp || "").slice(5, 16).replace("T", " ");
        const label = `${date} · ${run.group_name || ""}`;
        const title = `${run.id} — ${pass}/${total} — ${(run.models || []).join(", ")}`;
        return batteryBarRow(label, title, pct, `${pct.toFixed(0)}%`, "");
      }).filter(Boolean);
      if (rows.length > 1) {
        trendHtml = `<section class="battery-chart-section">
          <h4>📈 ${escapeHtml(t("battery.charts_trend"))}</h4>
          ${rows.join("")}
        </section>`;
      }
    }

    body.innerHTML = bannerHtml + modelSummaryHtml + trendHtml + `
      <div class="battery-history-list">
        ${runs.map((run) => {
          const date = fmtDateTimeFull(run.timestamp);
          const modelsBadges = (run.models || []).map((m) => {
            const isTarget = m === currentHistoryFilterModel;
            return `<span class="pill ${isTarget ? "pill-good" : ""}">${escapeHtml(m)}</span>`;
          }).join("");
          const passCount = run.pass_count || 0;
          const totalCount = run.total_count || 0;
          const passClass = passCount === totalCount && totalCount > 0 ? "pill-good" : (passCount > 0 ? "pill-warn" : "pill-bad");

          let scoreBadgeHtml = `<span class="pill ${passClass}">${passCount} / ${totalCount} OK</span>`;
          if (currentHistoryFilterModel) {
            const modelResults = (run.results || []).filter((r) => r.model === currentHistoryFilterModel);
            const mPass = modelResults.filter((r) => r.passed === true).length;
            const mTotal = modelResults.length;
            const mTpsList = modelResults.filter((r) => r.tokens_per_sec > 0).map((r) => r.tokens_per_sec);
            const mAvgTps = mTpsList.length > 0 ? (mTpsList.reduce((a, b) => a + b, 0) / mTpsList.length).toFixed(1) : null;
            scoreBadgeHtml = `<span class="pill ${mPass === mTotal && mTotal > 0 ? "pill-good" : (mPass > 0 ? "pill-warn" : "pill-bad")}">${mPass} / ${mTotal} OK ${mAvgTps ? `· ⚡ ${mAvgTps} tok/s` : ""}</span>`;
          }

          return `
            <div class="battery-history-card" data-run-id="${escapeHtml(run.id)}">
              <div class="battery-history-card-left">
                <div class="battery-history-card-title-row">
                  <span class="battery-history-group-name">${escapeHtml(run.group_name || t("battery.all_tests"))}</span>
                  ${scoreBadgeHtml}
                </div>
                <div class="battery-history-meta-row">
                  <span class="battery-history-date muted mono">${escapeHtml(date)}</span>
                  <div class="battery-history-models-wrap">${modelsBadges}</div>
                </div>
              </div>
              <div class="battery-history-card-actions">
                <button type="button" class="primary battery-history-view-btn" data-run-id="${escapeHtml(run.id)}">${t("battery.results")}</button>
                <button type="button" class="ghost danger-text battery-history-delete" data-run-id="${escapeHtml(run.id)}" title="${t("action.delete")}">🗑️</button>
              </div>
            </div>
          `;
        }).join("")}
      </div>
    `;

    setupHistoryClearFilterListener();

    body.querySelectorAll(".battery-history-view-btn").forEach((btn) => {
      btn.addEventListener("click", (e) => {
        e.stopPropagation();
        const id = btn.dataset.runId;
        history.pushState(null, "", "/tests/battery/results/" + id);
        showBatteryResultsView(id);
      });
    });

    body.querySelectorAll(".battery-history-card").forEach((card) => {
      card.addEventListener("click", (e) => {
        if (e.target.closest("button")) return;
        const id = card.dataset.runId;
        history.pushState(null, "", "/tests/battery/results/" + id);
        showBatteryResultsView(id);
      });
    });

    body.querySelectorAll(".battery-history-delete").forEach((btn) => {
      btn.addEventListener("click", async (e) => {
        e.stopPropagation();
        const id = btn.dataset.runId;
        const ok = await askConfirm({
          title: t("action.delete"),
          text: t("tests.delete_text"),
          okText: t("action.delete"),
          okClass: "danger",
        });
        if (!ok.ok) return;
        try {
          await api("/api/runner/runs/" + encodeURIComponent(id), { method: "DELETE" });
          await renderBatteryHistory();
        } catch (err) {
          toast(t("toast.error", { msg: err.message }), "error");
        }
      });
    });
  } catch (err) {
    body.innerHTML = `<div class="muted">${escapeHtml(err.message)}</div>`;
  }
}

function setupHistoryClearFilterListener() {
  const clearBtn = $("battery-history-clear-filter");
  if (clearBtn) {
    clearBtn.addEventListener("click", () => {
      currentHistoryFilterTestId = null;
      currentHistoryFilterModel = null;
      const modelSel = $("battery-history-model-select");
      if (modelSel) modelSel.value = "";
      history.pushState(null, "", "/tests/battery/history");
      void renderBatteryHistory();
    });
  }
  const lbBtn = $("battery-history-open-leaderboard");
  if (lbBtn && currentHistoryFilterTestId) {
    lbBtn.addEventListener("click", () => {
      openTestHistoryModal(currentHistoryFilterTestId);
    });
  }
  const emptyLbBtn = $("battery-empty-open-lb");
  if (emptyLbBtn && currentHistoryFilterTestId) {
    emptyLbBtn.addEventListener("click", () => {
      openTestHistoryModal(currentHistoryFilterTestId);
    });
  }
}

