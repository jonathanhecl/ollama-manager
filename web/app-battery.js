"use strict";

// ---------- battery runner ----------
let currentRunTarget = null; // { type: 'single' | 'group' | 'multi' | 'all', testId?, groupId?, groupIds?, name? }
let currentHistoryFilterTestId = null;
let currentHistoryFilterModel = null;
let currentHistoryFilterCategory = null;

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

let batterySelectedGroups = new Set(); // group ids picked in the categories step
let batteryModalStep = "models"; // "groups" | "models"
let batteryModalSingleTestId = null;
let batteryCoverageByModel = new Map(); // model name -> Set of group ids with history
let batteryCoverageLoaded = false;
let batteryCoverageSeq = 0;

function batteryModalAllGroupIds() {
  return (Array.isArray(testsGroups) ? testsGroups : []).map((g) => g.id);
}

function batteryModalGroupById(id) {
  return (Array.isArray(testsGroups) ? testsGroups : []).find((g) => g.id === id) || null;
}

function batteryModalRunnableTests(groupIds) {
  const ids = groupIds == null ? null : groupIds;
  return (Array.isArray(tests) ? tests : []).filter(
    (x) => (ids === null || ids.includes(x.group_id)) && x.active && x.evaluation_type !== "agent"
  );
}

// Tests targeted by the current modal selection (single test, groups or all).
function batteryModalTargetTests() {
  if (currentRunTarget?.type === "single" && currentRunTarget.testId) {
    const one = (Array.isArray(tests) ? tests : []).find((x) => x.id === currentRunTarget.testId);
    return one ? [one] : [];
  }
  if (currentRunTarget?.type === "multi" && Array.isArray(currentRunTarget.groupIds)) {
    return batteryModalRunnableTests(currentRunTarget.groupIds);
  }
  if (currentRunTarget?.type === "group" && currentRunTarget.groupId) {
    return batteryModalRunnableTests([currentRunTarget.groupId]);
  }
  return batteryModalRunnableTests(null);
}

function batteryModalRequiredCaps() {
  const caps = new Set();
  const groupCapsById = new Map(
    (Array.isArray(testsGroups) ? testsGroups : []).map((g) => [g.id, g.required_caps || []])
  );
  for (const x of batteryModalTargetTests()) {
    for (const c of x.required_caps || []) caps.add(c);
    for (const c of groupCapsById.get(x.group_id) || []) caps.add(String(c).toLowerCase());
  }
  return caps;
}

// Effective caps for one test: union of its own required_caps and its
// category-level required_caps.
function batteryTestEffectiveCaps(test) {
  const out = new Set();
  for (const c of test?.required_caps || []) out.add(String(c).toLowerCase());
  const g = batteryModalGroupById(test?.group_id);
  for (const c of g?.required_caps || []) out.add(String(c).toLowerCase());
  return out;
}

// Group ids the coverage badges refer to (the current modal target).
function batteryCoverageTargetIds() {
  if (currentRunTarget?.type === "single") {
    return currentRunTarget.groupId ? [currentRunTarget.groupId] : batteryModalAllGroupIds();
  }
  if (currentRunTarget?.type === "multi" && Array.isArray(currentRunTarget.groupIds)) {
    return [...currentRunTarget.groupIds];
  }
  if (currentRunTarget?.type === "group" && currentRunTarget.groupId) {
    return [currentRunTarget.groupId];
  }
  return batteryModalAllGroupIds();
}

function batteryCoverageInfo(modelName, targetIds) {
  if (!targetIds || targetIds.length === 0) return { text: "", cls: "muted", title: "" };
  if (!batteryCoverageLoaded) {
    return { text: "…", cls: "muted", title: t("battery.coverage_loading") };
  }
  const cov = batteryCoverageByModel.get(modelName) || new Set();
  const nameOf = (id) => batteryModalGroupById(id)?.name || id;
  const done = targetIds.filter((id) => cov.has(id));
  const pending = targetIds.filter((id) => !cov.has(id));
  if (targetIds.length === 1) {
    if (done.length > 0) {
      return { text: `✓ ${t("battery.coverage_done")}`, cls: "text-good", title: `${t("battery.coverage_evaluated")}: ${nameOf(targetIds[0])}` };
    }
    return { text: `○ ${t("battery.coverage_new")}`, cls: "muted", title: `${t("battery.coverage_pending")}: ${nameOf(targetIds[0])}` };
  }
  const title = `${t("battery.coverage_evaluated")}: ${done.map(nameOf).join(", ") || "—"} · ${t("battery.coverage_pending")}: ${pending.map(nameOf).join(", ") || "—"}`;
  if (pending.length === 0) return { text: `✓ ${done.length}/${targetIds.length}`, cls: "text-good", title };
  if (done.length === 0) return { text: `○ 0/${targetIds.length}`, cls: "muted", title };
  return { text: `◐ ${done.length}/${targetIds.length}`, cls: "", warn: true, title };
}

// In-place badge refresh: never rebuilds rows (checkbox state and scroll
// position are preserved).
function paintBatteryCoverage() {
  const container = $("battery-modal-models");
  if (!container) return;
  const targetIds = batteryCoverageTargetIds();
  container.querySelectorAll(".battery-model-item").forEach((label) => {
    const cb = label.querySelector('input[type="checkbox"]');
    const el = label.querySelector("[data-coverage]");
    if (!cb || !el) return;
    const info = batteryCoverageInfo(cb.value, targetIds);
    el.textContent = info.text;
    el.title = info.title || "";
    el.classList.toggle("text-good", info.cls === "text-good");
    el.classList.toggle("muted", info.cls === "muted");
    el.style.color = info.warn ? "var(--warn)" : "";
  });
}

// Loads per-model evaluation history for the targeted categories so the
// models step shows what was already evaluated (and what is still pending).
// Derives coverage from the cached leaderboard payload (one request) instead
// of scanning every category's full history server-side.
async function refreshBatteryCoverage() {
  const token = ++batteryCoverageSeq;
  const targetIds = new Set(batteryCoverageTargetIds());
  batteryCoverageByModel = new Map();
  batteryCoverageLoaded = false;
  paintBatteryCoverage();

  let lb = null;
  try {
    lb = await api("/api/runner/leaderboard");
  } catch {
    lb = null;
  }
  if (token !== batteryCoverageSeq) return; // superseded (Back / reopen / new target)

  const map = new Map();
  const rows = (lb && Array.isArray(lb.models)) ? lb.models : [];
  for (const m of rows) {
    if (!m || !m.model) continue;
    const scores = m.scores || {};
    let set = null;
    for (const gid of Object.keys(scores)) {
      if (targetIds.size > 0 && !targetIds.has(gid)) continue;
      const sc = scores[gid];
      if (sc && (sc.tested || 0) > 0) {
        if (!set) set = new Set();
        set.add(gid);
      }
    }
    if (set) map.set(m.model, set);
  }
  batteryCoverageByModel = map;
  batteryCoverageLoaded = true;
  paintBatteryCoverage();
  applyBatteryModelsFilter();
}

async function openBatteryModal(options = {}) {
  if (window._activeBatteryRun && window._activeBatteryRun.runID) {
    const msg = window._activeBatteryRun.waitingReview ? t("battery.human_review_block_toast") : t("battery.already_running_toast");
    toast(msg, "warn");
    return;
  }
  try {
    const check = await api("/api/runner/active");
    if (check && check.active) {
      if (typeof updateBatteryGlobalStatus === "function") {
        updateBatteryGlobalStatus({ battery_active: true, battery_run_id: check.run_id, ...(check.progress || {}) });
      }
      const msg = check.waiting_review ? t("battery.human_review_block_toast") : t("battery.already_running_toast");
      toast(msg, "warn");
      return;
    }
  } catch { }

  batterySelectedModels.clear();
  const defaultModel = options.initialModel || (typeof selectedTestModel !== "undefined" ? selectedTestModel : "");
  if (defaultModel) {
    batterySelectedModels.add(defaultModel);
  }
  $("battery-modal").hidden = false;
  // Lock background scroll so touch drags over the model list don't move
  // the page behind (mobile). Restored in closeBatteryModal.
  document.body.style.overflow = "hidden";

  wireBatteryModalStepButtons();

  // Data for the categories step. Note the leaderboard page loads group
  // names without the test list, so both must be checked (otherwise every
  // category shows "0 tests" and Continue is blocked).
  if (!options.testId && (!Array.isArray(testsGroups) || testsGroups.length === 0 || !Array.isArray(tests) || tests.length === 0)) {
    try { await refreshTests(); } catch { }
  }
  // Pre-fetch models and usage if needed
  if (typeof models === "undefined" || models.length === 0) {
    try { await refreshModels(); } catch { }
  }

  wireBatterySortButtons();
  updateBatterySortUI();

  if (options.testId) {
    // Single test: skip the categories step and go straight to models.
    batteryModalSingleTestId = options.testId;
    let test = (Array.isArray(tests) ? tests : []).find((t) => t.id === options.testId);
    if (!test) {
      try { await refreshTests(); } catch { }
      test = (Array.isArray(tests) ? tests : []).find((t) => t.id === options.testId);
    }
    currentRunTarget = { type: "single", testId: options.testId, groupId: test?.group_id, name: test?.name || options.testId };
    const titleEl = $("battery-modal-title");
    if (titleEl) titleEl.textContent = t("battery.run_single", { name: test?.name || options.testId });
    batteryModalShowStep("models");
    renderBatteryModalModels();
    void refreshBatteryCoverage();
  } else {
    // Group/all runs: pick categories first so several can run together.
    batteryModalSingleTestId = null;
    batterySelectedGroups.clear();
    const allIds = batteryModalAllGroupIds();
    let pre;
    if (Array.isArray(options.groupIds) && options.groupIds.length > 0) {
      pre = options.groupIds.filter((id) => allIds.includes(id));
      if (pre.length === 0) pre = allIds;
    } else {
      pre = options.groupId && options.groupId !== "" && options.groupId !== "all" ? [options.groupId] : allIds;
    }
    for (const id of pre) {
      if (allIds.includes(id)) batterySelectedGroups.add(id);
    }
    if (batterySelectedGroups.size === 0) {
      for (const id of allIds) batterySelectedGroups.add(id);
    }
    batteryModalShowStep("groups");
    renderBatteryModalGroups();
  }
  updateBatteryModalSelectionUI();

  batteryModelsOnlyPending = false;
  updateBatteryPendingToggleUI();

  const filterInput = $("battery-models-filter");
  if (filterInput) filterInput.value = "";
  wireBatteryModelsFilter();

  // Wire toolbar quick buttons if not wired
  const selectAllBtn = $("battery-modal-select-all");
  if (selectAllBtn && !selectAllBtn.dataset.wired) {
    selectAllBtn.dataset.wired = "1";
    selectAllBtn.addEventListener("click", () => {
      const cbs = $("battery-modal-models")?.querySelectorAll('input[type="checkbox"]:not(:disabled)');
      if (cbs) {
        cbs.forEach((cb) => {
          const item = cb.closest(".battery-model-item");
          if (item && item.hidden) return;
          cb.checked = true;
          batterySelectedModels.add(cb.value);
          item?.classList.add("selected");
        });
        updateBatteryModalSelectionUI();
      }
    });
  }

  const clearBtn = $("battery-modal-clear");
  if (clearBtn && !clearBtn.dataset.wired) {
    clearBtn.dataset.wired = "1";
    clearBtn.addEventListener("click", () => {
      const filterInput = $("battery-models-filter");
      const q = (filterInput?.value || "").trim();
      const hasFilter = Boolean(q) || batteryModelsOnlyPending;
      const cbs = $("battery-modal-models")?.querySelectorAll('input[type="checkbox"]');
      if (cbs) {
        cbs.forEach((cb) => {
          const item = cb.closest(".battery-model-item");
          if (hasFilter && item && item.hidden) return;
          cb.checked = false;
          batterySelectedModels.delete(cb.value);
          item?.classList.remove("selected");
        });
        if (!hasFilter) batterySelectedModels.clear();
        updateBatteryModalSelectionUI();
      }
    });
  }
}

async function loadBatteryModalSysinfo() {
  const sysEl = $("battery-modal-sysinfo");
  if (!sysEl) return;
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

function updateBatteryModalSelectionUI() {
  const countEl = $("battery-modal-count");
  if (countEl) {
    countEl.textContent = batterySelectedModels.size > 0 ? t("battery.models_count", { count: batterySelectedModels.size }) : "";
  }
  updateBatteryGroupsCountUI();
}

function updateBatteryGroupsCountUI() {
  const countEl = $("battery-modal-groups-count");
  if (countEl) {
    countEl.textContent = batterySelectedGroups.size > 0 ? t("battery.categories_count", { count: batterySelectedGroups.size }) : "";
  }
}

function batteryModalShowStep(step) {
  batteryModalStep = step;
  const isGroups = step === "groups";
  const groupsStep = $("battery-modal-groups-step");
  const modelsStep = $("battery-modal-models-step");
  const backBtn = $("battery-modal-back");
  const confirmBtn = $("battery-modal-confirm");
  const subtitleEl = $("battery-modal-subtitle");
  const titleEl = $("battery-modal-title");
  if (groupsStep) groupsStep.hidden = !isGroups;
  if (modelsStep) modelsStep.hidden = isGroups;
  if (backBtn) backBtn.hidden = isGroups || !!batteryModalSingleTestId;
  if (confirmBtn) confirmBtn.textContent = isGroups ? t("battery.continue") : t("battery.confirm");
  if (subtitleEl) {
    subtitleEl.textContent = isGroups ? t("battery.select_categories_hint") : t("battery.select_models_hint");
  }
  if (isGroups && titleEl) {
    titleEl.textContent = t("battery.select_categories");
  }
  if (isGroups) {
    updateBatteryGroupsCountUI();
  } else {
    updateBatteryModalSelectionUI();
    void loadBatteryModalSysinfo();
    setTimeout(() => { $("battery-models-filter")?.focus(); }, 50);
  }
}

function wireBatteryModalStepButtons() {
  const backBtn = $("battery-modal-back");
  if (backBtn && !backBtn.dataset.wired) {
    backBtn.dataset.wired = "1";
    backBtn.addEventListener("click", () => {
      batteryModalShowStep("groups");
      renderBatteryModalGroups();
    });
  }
  const selectAllBtn = $("battery-modal-groups-select-all");
  if (selectAllBtn && !selectAllBtn.dataset.wired) {
    selectAllBtn.dataset.wired = "1";
    selectAllBtn.addEventListener("click", () => {
      for (const id of batteryModalAllGroupIds()) batterySelectedGroups.add(id);
      renderBatteryModalGroups();
    });
  }
  const clearBtn = $("battery-modal-groups-clear");
  if (clearBtn && !clearBtn.dataset.wired) {
    clearBtn.dataset.wired = "1";
    clearBtn.addEventListener("click", () => {
      batterySelectedGroups.clear();
      renderBatteryModalGroups();
    });
  }
}

function renderBatteryModalGroups() {
  const container = $("battery-modal-groups-list");
  if (!container) return;
  const groups = Array.isArray(testsGroups) ? testsGroups : [];
  if (groups.length === 0) {
    container.innerHTML = `<div class="muted">${escapeHtml(t("battery.no_categories"))}</div>`;
    updateBatteryGroupsCountUI();
    return;
  }
  container.innerHTML = groups.map((g) => {
    const n = batteryModalRunnableTests([g.id]).length;
    const isChecked = batterySelectedGroups.has(g.id);
    const gcaps = new Set((g.required_caps || []).map((c) => String(c).toLowerCase()));
    const capBadges = `${gcaps.has("vision") ? `<span title="${escapeHtml(t("tests.group_required_vision"))}">👁️</span>` : ""}${gcaps.has("audio") ? `<span title="${escapeHtml(t("tests.group_required_audio"))}">🔊</span>` : ""}`;
    return `
      <label class="battery-model-item ${isChecked ? "selected" : ""}">
        <input type="checkbox" value="${escapeHtml(g.id)}" ${isChecked ? "checked" : ""} />
        <div class="battery-model-main">
          <div class="battery-model-name">${escapeHtml(g.name || g.id)}${capBadges ? `<span style="margin-left:6px;">${capBadges}</span>` : ""}</div>
        </div>
        <div class="battery-model-right-cols">
          <div class="battery-model-specs mono muted">
            <span>${escapeHtml(t("battery.group_tests_count", { count: n }))}</span>
          </div>
        </div>
      </label>
    `;
  }).join("");

  container.querySelectorAll('input[type="checkbox"]').forEach((cb) => {
    cb.addEventListener("change", () => {
      const parentLabel = cb.closest(".battery-model-item");
      if (cb.checked) {
        batterySelectedGroups.add(cb.value);
        parentLabel?.classList.add("selected");
      } else {
        batterySelectedGroups.delete(cb.value);
        parentLabel?.classList.remove("selected");
      }
      updateBatteryGroupsCountUI();
    });
  });

  updateBatteryGroupsCountUI();
}

function batteryModalGoModels() {
  if (batterySelectedGroups.size === 0) {
    toast(t("battery.select_categories_warn"), "warn");
    return;
  }
  const sel = batteryModalAllGroupIds().filter((id) => batterySelectedGroups.has(id));
  if (sel.length === 0) {
    toast(t("battery.select_categories_warn"), "warn");
    return;
  }
  if (batteryModalRunnableTests(sel).length === 0) {
    toast(t("battery.no_tests_in_groups"), "warn");
    return;
  }
  const titleEl = $("battery-modal-title");
  const allIds = batteryModalAllGroupIds();
  const isAll = sel.length === allIds.length && allIds.length > 0;
  if (isAll) {
    currentRunTarget = { type: "all", groupId: "all", name: t("battery.all_tests") };
    if (titleEl) titleEl.textContent = t("battery.run_all");
  } else if (sel.length === 1) {
    const g = batteryModalGroupById(sel[0]);
    currentRunTarget = { type: "group", groupId: sel[0], name: g?.name || sel[0] };
    if (titleEl) titleEl.textContent = t("battery.run_group", { name: g?.name || sel[0] });
  } else {
    currentRunTarget = { type: "multi", groupIds: sel, name: t("battery.run_n_categories", { count: sel.length }) };
    if (titleEl) titleEl.textContent = t("battery.run_n_categories", { count: sel.length });
  }
  batteryModalShowStep("models");
  renderBatteryModalModels();
  updateBatteryModalSelectionUI();
  void refreshBatteryCoverage();
}

function batteryModalConfirm() {
  if (batteryModalStep === "groups" && !batteryModalSingleTestId) {
    batteryModalGoModels();
    return;
  }
  void confirmBatteryRun();
}

function closeBatteryModal() {
  $("battery-modal").hidden = true;
  document.body.style.overflow = "";
}

function renderBatteryModalModels() {
  const container = $("battery-modal-models");
  if (!container) return;

  // Only show active models that have verified speed (record_tokens_per_sec > 0)
  const activeModels = (typeof models !== "undefined" ? models : []).filter((m) => {
    if (m.archived || m.disabled || m.is_ghost) return false;
    const tps = Number(m.record_tokens_per_sec) || 0;
    return tps > 0;
  });

  if (activeModels.length === 0) {
    container.innerHTML = `<div class="muted" style="text-align:center;padding:32px 16px;">${escapeHtml(t("battery.no_tested_models"))}</div>`;
    updateBatteryModalSelectionUI();
    return;
  }

  // Required caps across the targeted tests (union, so partially
  // compatible models stay selectable).
  const requiredCaps = batteryModalRequiredCaps();

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
    container.innerHTML = `<div class="muted" style="text-align:center;padding:32px 16px;">${escapeHtml(t("battery.no_tested_models"))}</div>`;
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
    let modelName = m.name;
    let modelDisplay = escapeHtml(modelName);
    if (modelName.startsWith("hf.co/")) {
      modelDisplay = `<span style="opacity:0.45;font-weight:normal;">hf.co/</span>${escapeHtml(modelName.slice(6))}`;
    }
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
      <label class="battery-model-item ${isChecked ? "selected" : ""} ${disabled ? "disabled" : ""}" data-name="${escapeHtml(m.name)}" title="${escapeHtml(title)}">
        <input type="checkbox" value="${escapeHtml(m.name)}" ${isChecked ? "checked" : ""} ${disabled ? "disabled" : ""} />
        <div class="battery-model-main">
          <div class="battery-model-name mono" title="${escapeHtml(m.name)}">${modelDisplay}</div>
          ${capsHtml ? `<div class="battery-model-caps cap-list model-cap-list">${capsHtml}</div>` : ""}
          <div class="battery-model-coverage mono muted" data-coverage>…</div>
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
  paintBatteryCoverage();
  applyBatteryModelsFilter();
}

let batteryModelsOnlyPending = false;

function updateBatteryPendingToggleUI() {
  const btn = $("battery-models-toggle-pending");
  if (!btn) return;
  btn.classList.toggle("active", Boolean(batteryModelsOnlyPending));
  btn.setAttribute("aria-pressed", batteryModelsOnlyPending ? "true" : "false");
}

function applyBatteryModelsFilter() {
  const filterInput = $("battery-models-filter");
  const q = (filterInput?.value || "").trim().toLowerCase();
  const items = document.querySelectorAll("#battery-modal-models .battery-model-item");
  const targetIds = batteryCoverageTargetIds();

  let visibleCount = 0;
  items.forEach((item) => {
    const name = item.dataset.name || "";
    const matchesSearch = !q || name.toLowerCase().includes(q);

    let matchesPending = true;
    if (batteryModelsOnlyPending && batteryCoverageLoaded) {
      const cov = batteryCoverageByModel.get(name) || new Set();
      const pending = targetIds.filter((id) => !cov.has(id));
      matchesPending = (pending.length > 0);
    }

    const visible = matchesSearch && matchesPending;
    item.hidden = !visible;
    if (visible) visibleCount++;
  });

  const noMatchEl = $("battery-models-no-match");
  if (noMatchEl) {
    if (batteryModelsOnlyPending && !q) {
      noMatchEl.textContent = t("battery.no_pending_models");
    } else {
      noMatchEl.textContent = t("battery.no_matching_models");
    }
    noMatchEl.style.display = (items.length > 0 && visibleCount === 0) ? "block" : "none";
  }
}

function wireBatteryModelsFilter() {
  const filterInput = $("battery-models-filter");
  if (filterInput && !filterInput.dataset.wired) {
    filterInput.dataset.wired = "1";
    filterInput.addEventListener("input", applyBatteryModelsFilter);
  }
  const toggleBtn = $("battery-models-toggle-pending");
  if (toggleBtn && !toggleBtn.dataset.wired) {
    toggleBtn.dataset.wired = "1";
    toggleBtn.addEventListener("click", () => {
      batteryModelsOnlyPending = !batteryModelsOnlyPending;
      updateBatteryPendingToggleUI();
      applyBatteryModelsFilter();
    });
  }
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
  batteryStageSnap = null;
  updateBatteryCurrentTurnTimer();
}
let batteryCompletedTests = [];
let batteryLastTestSnapshot = null;
let batteryTimelineTotal = 0;
let batteryTimelineCompleted = []; // {index, name, model, testId}
let batteryTimelineCurrent = null; // {index, name, model, isThinking}
let batteryTimelineQueue = []; // {index, testId, testName, model}
let batteryProgressModelIDs = [];
let batteryLiveResults = [];
let batteryStartTime = 0;
let batteryElapsedInterval = null;
let batteryActiveTab = "models";
let batteryActiveTurnKey = "";
let batteryTurnStartTime = 0;
// Latest per-stage snapshot from the backend progress poll. Times come from
// the server (exact per-chunk accounting), so headers stay correct even when
// a phase ends before the first poll sees it. Live phases are projected
// forward from `at` until the snapshot goes stale.
let batteryStageSnap = null;
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

// Elapsed ms for one live stage: backend snapshot plus projection while the
// phase is active and the snapshot is fresh (polls arrive ~every 2s).
function batteryStageElapsed(which) {
  const snap = batteryStageSnap;
  if (!snap) return 0;
  const base = which === "think" ? (snap.thinkMs || 0) : (snap.respMs || 0);
  const active = which === "think" ? snap.isThinking : snap.respActive;
  if (active && Date.now() - snap.at < 5000) {
    return base + (Date.now() - snap.at);
  }
  return base;
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
      elStreamTurn.title = `${t("battery.timer_long_desc") || "Posible bloqueo o bucle (>60s). Puedes usar Skip Case para continuar."} (${timeStr})`;
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

  const elThinkingLiveTag = $("battery-stream-thinking-live-tag");
  const elResponseLiveTag = $("battery-stream-live-tag");

  if (elThinkingTimer) {
    if (batteryStageSnap && batteryStageSnap.hasThink) {
      const thinkingSec = Math.floor(Math.max(0, batteryStageElapsed("think")) / 1000);
      elThinkingTimer.hidden = false;
      elThinkingTimer.textContent = `⏱️ ${formatTimeDisplay(thinkingSec)}`;
    } else {
      elThinkingTimer.hidden = true;
    }
  }

  if (elResponseTimer) {
    if (batteryStageSnap && (batteryStageSnap.respMs > 0 || batteryStageSnap.respChars > 0 || batteryStageSnap.respActive)) {
      const respSec = Math.floor(Math.max(0, batteryStageElapsed("resp")) / 1000);
      elResponseTimer.hidden = false;
      elResponseTimer.textContent = `⏱️ ${formatTimeDisplay(respSec)}`;
    } else if (batteryStageSnap && batteryStageSnap.isThinking) {
      elResponseTimer.hidden = false;
      elResponseTimer.textContent = `⏱️ 00:00`;
    } else {
      elResponseTimer.hidden = true;
    }
  }

  if (batteryStageSnap) {
    if (batteryStageSnap.isThinking) {
      if (elThinkingLiveTag) elThinkingLiveTag.hidden = false;
      if (elResponseLiveTag) elResponseLiveTag.hidden = true;
    } else if (batteryStageSnap.respActive) {
      if (elThinkingLiveTag) elThinkingLiveTag.hidden = true;
      if (elResponseLiveTag) elResponseLiveTag.hidden = false;
    } else {
      if (elThinkingLiveTag) elThinkingLiveTag.hidden = true;
      if (elResponseLiveTag) elResponseLiveTag.hidden = true;
    }
  } else {
    if (elThinkingLiveTag) elThinkingLiveTag.hidden = true;
    if (elResponseLiveTag) elResponseLiveTag.hidden = true;
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
    const modelQueue = queue.filter((q) => q.model === m);
    const queueExpected = modelQueue.length || 0;
    const queueExpectedUnits = modelQueue.reduce((sum, q) => sum + (q.units || 1), 0);
    const modelResults = results.filter((r) => r.model === m);
    const completed = modelResults.length;
    const passed = modelResults.filter((r) => r.passed === true).length;
    const failed = modelResults.filter((r) => r.passed === false).length;
    // Sub-case (unit) aggregates: partial passes earn proportional credit.
    let completedUnits = 0;
    let passedUnits = 0;
    let failedUnits = 0;
    for (const r of modelResults) {
      const u = batteryResultUnitStats(r);
      completedUnits += u.total;
      passedUnits += u.passed;
      if (r.passed === false) failedUnits += Math.max(0, u.total - u.passed);
    }
    // Expected work can never be less than what has already been completed.
    const expected = Math.max(queueExpected, completed);
    const totalExpected = expected > 0 ? expected : completed;
    const expectedUnits = Math.max(queueExpectedUnits, completedUnits);
    const totalExpectedUnits = expectedUnits > 0 ? expectedUnits : completedUnits;
    // Pass rate relative to expected units (done + pending) so it builds up
    // progressively; completed rate is the accuracy of what has run so far.
    const passRate = totalExpectedUnits > 0 ? Math.min(100, (passedUnits / totalExpectedUnits) * 100) : 0;
    const completedPassRate = completedUnits > 0 ? Math.min(100, (passedUnits / completedUnits) * 100) : 0;
    const totalSpeed = modelResults.reduce((sum, r) => sum + (r.tokens_per_sec || 0), 0);
    const avgSpeed = completed > 0 ? totalSpeed / completed : 0;
    const totalDuration = modelResults.reduce((sum, r) => sum + (r.response_time_ms || 0), 0);
    const avgLatency = completed > 0 ? (totalDuration / completed) / 1000 : 0;
    const isCurrent = m === currentModel;
    const isDone = completed > 0 && (expected === 0 || completed >= expected);

    modelMap.set(m, {
      model: m,
      expected: totalExpected,
      completed,
      passed,
      failed,
      expectedUnits: totalExpectedUnits,
      completedUnits,
      passedUnits,
      failedUnits,
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
  let totalCompletedUnits = 0;
  let totalPassedUnits = 0;
  for (const r of results) {
    const u = batteryResultUnitStats(r);
    totalCompletedUnits += u.total;
    totalPassedUnits += u.passed;
  }
  const totalExpectedUnits = queue.reduce((sum, q) => sum + (q.units || 1), 0) || totalCompletedUnits;
  const globalPassRate = totalCompletedUnits > 0 ? (totalPassedUnits / totalCompletedUnits) * 100 : 0;
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
    totalCompletedUnits,
    totalPassedUnits,
    totalExpectedUnits,
    globalPassRate,
    globalAvgSpeed,
    globalAvgLatency,
    totalTokens,
  };
}

function renderBatteryKPIs(p, stats) {
  const currentModel = p.model || "";
  const done = p.done || false;

  // Active Model Card
  const elModelName = $("battery-kpi-model-name");
  const elModelStatus = $("battery-kpi-model-status");
  const elModelSub = $("battery-kpi-model-sub");
  const elModelPct = $("battery-kpi-model-pct");
  const elModelBar = $("battery-kpi-model-bar");
  const elNextName = $("battery-kpi-next-name");

  // NOTE: marquee animation restarts whenever the text node is replaced, so
  // only touch textContent when the value actually changed. Otherwise the
  // ping-pong (which holds still for the first 18% of each cycle) would be
  // reset on every poll and the title would look frozen.
  if (elModelName) {
    const modelLabel = currentModel || (done ? t("battery.status_done") : "--");
    setTextIfChanged(elModelName, modelLabel);
    if (elModelName.title !== (currentModel || "")) elModelName.title = currentModel || "";
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
    const totalExpUnits = Math.max(curStats.expectedUnits, curStats.completedUnits);
    const curModelRunningIdx = Math.min(totalExpUnits, curStats.completedUnits + (done ? 0 : 1));
    if (elModelSub) {
      elModelSub.textContent = t("battery.kpi_model_tests", { current: String(curModelRunningIdx), total: String(totalExpUnits) });
    }
    const modelPct = totalExpUnits > 0 ? Math.min(100, Math.round((curStats.completedUnits / totalExpUnits) * 100)) : 0;
    if (elModelPct) elModelPct.textContent = `${modelPct}%`;
    if (elModelBar) elModelBar.style.width = `${modelPct}%`;
  } else {
    if (elModelSub) elModelSub.textContent = "--";
    if (elModelPct) elModelPct.textContent = "0%";
    if (elModelBar) elModelBar.style.width = "0%";
  }

  if (elNextName) {
    const nextLabel = stats.nextModel || t("battery.kpi_no_next");
    setTextIfChanged(elNextName, nextLabel);
    if (elNextName.title !== (stats.nextModel || "")) elNextName.title = stats.nextModel || "";
  }

  // Global Progress Card (sub-cases of the model currently running, so
  // partial work inside a multi-turn test is reflected as it happens).
  const elGlobalCount = $("battery-kpi-global-count");
  const elGlobalPct = $("battery-kpi-global-pct");
  const elRemaining = $("battery-kpi-remaining-text");
  const elFill = $("battery-progress-fill");

  const expectedUnits = curStats ? curStats.expectedUnits : 0;
  let displayUnits = curStats ? curStats.completedUnits : 0;
  if (!done && Array.isArray(p.completed_cases) && p.completed_cases.length > 0) {
    const alreadyRecorded = (batteryLiveResults || []).some(
      (r) => r.model === currentModel && r.test_id === p.test_id
    );
    if (!alreadyRecorded) displayUnits += p.completed_cases.length;
  }
  if (expectedUnits > 0) displayUnits = Math.min(displayUnits, expectedUnits);
  const globalPct = expectedUnits > 0
    ? Math.max(0, Math.min(100, Math.round((displayUnits / expectedUnits) * 100)))
    : 0;
  const remainingUnits = Math.max(0, expectedUnits - displayUnits);

  if (elGlobalCount) elGlobalCount.textContent = `${displayUnits} / ${expectedUnits}`;
  if (elGlobalPct) elGlobalPct.textContent = `${globalPct}%`;
  if (elRemaining) elRemaining.textContent = t("battery.kpi_remaining", { count: String(remainingUnits) });
  if (elFill) elFill.style.width = `${globalPct}%`;

  // Global Pass Rate Card
  const elDonutVal = $("battery-kpi-donut-val");
  const elDonutPct = $("battery-kpi-pass-pct-inner");
  const elPassCount = $("battery-kpi-pass-count-text");
  const elFailCount = $("battery-kpi-fail-count-text");

  const passPctRound = Math.round(stats.globalPassRate);
  const failedUnits = Math.max(0, (stats.totalCompletedUnits || 0) - (stats.totalPassedUnits || 0));
  if (elDonutVal) {
    elDonutVal.setAttribute("stroke-dasharray", `${passPctRound}, 100`);
    if (passPctRound < 50 && stats.totalCompletedUnits > 0) {
      elDonutVal.style.stroke = "var(--danger)";
    } else if (passPctRound < 75 && stats.totalCompletedUnits > 0) {
      elDonutVal.style.stroke = "var(--warn)";
    } else {
      elDonutVal.style.stroke = "var(--good)";
    }
  }
  if (elDonutPct) elDonutPct.textContent = `${passPctRound}%`;
  if (elPassCount) elPassCount.textContent = `✔ ${stats.totalPassedUnits} ${t("battery.pass")}`;
  if (elFailCount) elFailCount.textContent = `✖ ${failedUnits} ${t("battery.fail")}`;

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

  // Measure KPI marquee overflow. setupMarquees is a no-op for wrappers whose
  // content/width did not change, so running marquees keep playing.
  requestAnimationFrame(() => {
    const view = $("battery-progress-view");
    if (view && !view.hidden) setupMarquees(view);
  });

}

// Set textContent only when it changed: replacing the text node restarts any
// CSS animation running on the element (e.g. .marquee-content ping-pong).
function setTextIfChanged(el, val) {
  if (el && el.textContent !== val) el.textContent = val;
}

function setupMarquees(container = document) {
  if (!container) return;
  const wrappers = container.querySelectorAll(".marquee-wrapper");
  wrappers.forEach((wrap) => {
    const content = wrap.querySelector(".marquee-content");
    if (!content) return;
    if (wrap.clientWidth <= 0) return;
    const diff = content.scrollWidth - wrap.clientWidth;
    if (diff > 4) {
      const endVal = `-${diff + 8}px`;
      const durVal = `${Math.max(5, Math.min(22, Math.round(diff / 16)))}s`;
      if (wrap.style.getPropertyValue("--marquee-end") !== endVal) {
        wrap.style.setProperty("--marquee-end", endVal);
        wrap.style.setProperty("--marquee-dur", durVal);
      }
      if (!wrap.classList.contains("is-overflowing")) {
        wrap.classList.add("is-overflowing");
      }
    } else {
      wrap.classList.remove("is-overflowing");
      wrap.style.removeProperty("--marquee-end");
      wrap.style.removeProperty("--marquee-dur");
    }
  });
}

let _leaderboardRowModels = []; // model ids in the current DOM order (marquee nodes are preserved while this matches)
function batteryLbStatsFor(modelMap, m, currentModel) {
  return modelMap.get(m) || { expected: 0, completed: 0, passed: 0, failed: 0, expectedUnits: 0, completedUnits: 0, passedUnits: 0, failedUnits: 0, passRate: 0, completedPassRate: 0, avgSpeed: 0, isCurrent: m === currentModel, isDone: false };
}
// Signature of everything rendered OUTSIDE the model-name marquee node.
function batteryLbRowSig(st) {
  return `${st.expected}|${st.completed}|${st.passed}|${st.failed}|${st.expectedUnits}|${st.completedUnits}|${st.passedUnits}|${st.failedUnits}|${(st.avgSpeed || 0).toFixed(1)}|${st.isCurrent ? 1 : 0}|${st.isDone ? 1 : 0}`;
}
function batteryLbStatusKey(st) {
  if (st.isCurrent) return "running";
  if (st.isDone) return "done";
  return "pending";
}
function batteryLbStatusLabel(st) {
  if (st.isCurrent) return t("battery.status_running");
  if (st.isDone) return t("battery.status_done");
  return t("battery.status_pending");
}
function batteryLbStatusBadge(st) {
  // Legacy helper kept for compatibility: status is now encoded as row
  // background (see batteryLbStatusKey), not as a badge column.
  const key = batteryLbStatusKey(st);
  const label = escapeHtml(batteryLbStatusLabel(st));
  if (key === "running") {
    return `<span class="badge badge-primary pulse">⚡ ${label}</span>`;
  } else if (key === "done") {
    return `<span class="badge badge-pass">✔ ${label}</span>`;
  }
  return `<span class="badge badge-muted">⏳ ${label}</span>`;
}
function batteryLbRowClass(st) {
  const key = batteryLbStatusKey(st);
  const base = "battery-leaderboard-row status-" + key;
  return key === "running" ? base + " active-model-row" : base;
}
function renderBatteryLeaderboard(modelIDs, modelMap, currentModel) {
  const container = $("battery-leaderboard-container");
  if (!container) return;
  if (!modelIDs || !modelIDs.length) {
    _leaderboardRowModels = [];
    container.innerHTML = `<div class="muted">${escapeHtml(t("battery.starting"))}</div>`;
    return;
  }

  // Fast path: same model list/order as the DOM already shows. Update only the
  // stat cells in place and NEVER touch the .marquee-content nodes, so running
  // title animations survive every poll / completed test / case change.
  const sameOrder = _leaderboardRowModels.length === modelIDs.length &&
    _leaderboardRowModels.every((m, i) => m === modelIDs[i]);
  const tbody = container.querySelector("table.battery-leaderboard-table tbody");
  if (sameOrder && tbody && tbody.children.length === modelIDs.length) {
    updateBatteryLeaderboardRows(tbody, modelIDs, modelMap, currentModel);
    return;
  }

  let html = `
    <table class="battery-leaderboard-table">
      <thead>
        <tr>
          <th class="col-head-model">${escapeHtml(t("battery.col_model"))}</th>
          <th class="col-head-tests">${escapeHtml(t("battery.col_tests"))}</th>
          <th class="col-head-ratio">${escapeHtml(t("battery.col_ratio"))}</th>
          <th class="col-head-pass">${escapeHtml(t("battery.col_pass_pct"))}</th>
          <th class="col-head-speed">${escapeHtml(t("battery.col_speed"))}</th>
        </tr>
      </thead>
      <tbody>
  `;

  for (const m of modelIDs) {
    const st = batteryLbStatsFor(modelMap, m, currentModel);
    const totalExpUnits = st.expectedUnits > 0 ? st.expectedUnits : st.completedUnits;
    const passPct = Math.round(st.passRate);
    const passBarWidth = totalExpUnits > 0 ? (st.passedUnits / totalExpUnits) * 100 : 0;
    const failBarWidth = totalExpUnits > 0 ? (st.failedUnits / totalExpUnits) * 100 : 0;
    const pendingUnits = Math.max(0, totalExpUnits - st.completedUnits);

    const rowClass = batteryLbRowClass(st);
    const statusLabel = batteryLbStatusLabel(st);
    const passPctColor = passPct >= 75 ? "var(--good)" : (passPct >= 50 ? "var(--warn)" : "var(--danger)");
    const ratioTooltip = `${st.passedUnits} ${t("battery.pass")} · ${st.failedUnits} ${t("battery.fail")}${pendingUnits > 0 ? ` · ${pendingUnits} ${t("battery.status_pending")}` : ""}`;
    const pctTooltip = st.completedUnits > 0 ? `${st.passedUnits}/${totalExpUnits} (${passPct}%)` : "";

    let modelDisplay = escapeHtml(m);
    if (m.startsWith("hf.co/")) {
      modelDisplay = `<span class="model-ns-prefix">hf.co/</span>${escapeHtml(m.slice(6))}`;
    }

    html += `
      <tr class="${rowClass}" data-sig="${escapeHtml(batteryLbRowSig(st))}" title="${escapeHtml(m)} · ${escapeHtml(statusLabel)}">
        <td class="col-cell-model">
          <div class="leaderboard-model-cell">
            <div class="marquee-wrapper leaderboard-marquee">
              <span class="marquee-content leaderboard-model-name mono" title="${escapeHtml(m)}">${modelDisplay}</span>
            </div>
          </div>
        </td>
        <td class="col-cell-tests mono" title="${st.completedUnits} / ${totalExpUnits}">
          <span class="tests-val">${st.completedUnits}</span><span class="tests-sep">/</span><span class="tests-total">${totalExpUnits}</span>
        </td>
        <td class="col-cell-ratio">
          <div class="leaderboard-ratio-bar" title="${escapeHtml(ratioTooltip)}">
            <div class="ratio-bar-pass" style="width: ${passBarWidth}%"></div>
            <div class="ratio-bar-fail" style="width: ${failBarWidth}%"></div>
          </div>
        </td>
        <td class="col-cell-pass mono font-bold" style="color:${st.completedUnits > 0 ? passPctColor : 'var(--muted)'};" title="${escapeHtml(pctTooltip)}">
          ${st.completedUnits > 0 ? passPct + "%" : "—"}
        </td>
        <td class="col-cell-speed mono muted">
          ${st.avgSpeed > 0 ? `${st.avgSpeed.toFixed(1)} <span class="speed-unit">tok/s</span>` : "—"}
        </td>
      </tr>
    `;
  }

  html += `</tbody></table>`;
  container.innerHTML = html;
  _leaderboardRowModels = [...modelIDs];
  requestAnimationFrame(() => setupMarquees(container));
}

// In-place stat update for leaderboard rows. The model-name marquee nodes are
// never touched here, so their animations survive polls, completed tests and
// case changes. Only the numeric cells are rewritten, and only when
// their per-row signature changed. Status is encoded as row background.
function updateBatteryLeaderboardRows(tbody, modelIDs, modelMap, currentModel) {
  const rows = tbody.querySelectorAll("tr.battery-leaderboard-row");
  modelIDs.forEach((m, i) => {
    const row = rows[i];
    if (!row) return;
    const st = batteryLbStatsFor(modelMap, m, currentModel);
    const sig = batteryLbRowSig(st);
    if (row.dataset.sig === sig) return;
    row.dataset.sig = sig;
    const key = batteryLbStatusKey(st);
    row.classList.toggle("status-running", key === "running");
    row.classList.toggle("status-done", key === "done");
    row.classList.toggle("status-pending", key === "pending");
    row.classList.toggle("active-model-row", !!st.isCurrent);
    row.title = `${m} · ${batteryLbStatusLabel(st)}`;

    const totalExpUnits = st.expectedUnits > 0 ? st.expectedUnits : st.completedUnits;
    const passPct = Math.round(st.passRate);
    const pendingUnits = Math.max(0, totalExpUnits - st.completedUnits);

    const testsCell = row.querySelector(".col-cell-tests");
    if (testsCell) {
      testsCell.title = `${st.completedUnits} / ${totalExpUnits}`;
      setTextIfChanged(testsCell.querySelector(".tests-val"), String(st.completedUnits));
      setTextIfChanged(testsCell.querySelector(".tests-total"), String(totalExpUnits));
    }
    const ratioBar = row.querySelector(".leaderboard-ratio-bar");
    if (ratioBar) {
      ratioBar.title = `${st.passedUnits} ${t("battery.pass")} · ${st.failedUnits} ${t("battery.fail")}${pendingUnits > 0 ? ` · ${pendingUnits} ${t("battery.status_pending")}` : ""}`;
      const passBar = ratioBar.querySelector(".ratio-bar-pass");
      const failBar = ratioBar.querySelector(".ratio-bar-fail");
      if (passBar) passBar.style.width = `${totalExpUnits > 0 ? (st.passedUnits / totalExpUnits) * 100 : 0}%`;
      if (failBar) failBar.style.width = `${totalExpUnits > 0 ? (st.failedUnits / totalExpUnits) * 100 : 0}%`;
    }
    const passCell = row.querySelector(".col-cell-pass");
    if (passCell) {
      const passPctColor = passPct >= 75 ? "var(--good)" : (passPct >= 50 ? "var(--warn)" : "var(--danger)");
      passCell.style.color = st.completedUnits > 0 ? passPctColor : "var(--muted)";
      passCell.title = st.completedUnits > 0 ? `${st.passedUnits}/${totalExpUnits} (${passPct}%)` : "";
      setTextIfChanged(passCell, st.completedUnits > 0 ? passPct + "%" : "—");
    }
    const speedCell = row.querySelector(".col-cell-speed");
    if (speedCell) {
      const html = st.avgSpeed > 0 ? `${st.avgSpeed.toFixed(1)} <span class="speed-unit">tok/s</span>` : "—";
      if (speedCell.dataset.html !== html) {
        speedCell.dataset.html = html;
        speedCell.innerHTML = html;
      }
    }
  });
}

function renderBatteryAnalyticsCharts(modelIDs, modelMap) {
  const container = $("battery-analytics-charts-container");
  if (!container) return;

  if (!modelIDs || !modelIDs.length) {
    container.innerHTML = `<div class="muted" style="padding: 24px; text-align: center;">${escapeHtml(t("battery.charts_no_data"))}</div>`;
    return;
  }

  // Find models with stats
  const allModelStats = modelIDs.map((m) => {
    return modelMap.get(m) || { model: m, expected: 0, completed: 0, passed: 0, failed: 0, expectedUnits: 0, completedUnits: 0, passedUnits: 0, failedUnits: 0, passRate: 0, avgSpeed: 0 };
  });

  const maxSpeed = Math.max(...allModelStats.map((s) => s.avgSpeed || 0), 20);
  const rowHeight = 34;
  const chartHeight = Math.max(60, allModelStats.length * rowHeight + 16);

  // SVG for Pass Rates
  let passBars = "";
  allModelStats.forEach((st, idx) => {
    const y = idx * rowHeight + 8;
    const isTested = st.completedUnits > 0;
    const totalExpUnits = st.expectedUnits > 0 ? st.expectedUnits : st.completedUnits;
    const barWidth = isTested ? Math.max(3, Math.round(st.passRate * 2.8)) : 0;
    const passColor = st.passRate >= 75 ? "#10b981" : (st.passRate >= 50 ? "#f59e0b" : "#ef4444");
    const shortName = st.model.length > 22 ? st.model.slice(0, 20) + "…" : st.model;
    const valText = isTested ? `${Math.round(st.passRate)}% (${st.passedUnits}/${totalExpUnits})` : `-- (0/${totalExpUnits || 0})`;

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

  container.innerHTML = fullChartsHtml;
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
      // Wrappers measured while hidden have width 0 and skip the marquee
      // setup; re-measure now that the tab is visible so marquees start.
      // setupMarquees never touches up-to-date wrappers, so running
      // animations on other tabs keep playing.
      requestAnimationFrame(() => {
        const view = $("battery-progress-view");
        if (view && !view.hidden) setupMarquees(view);
      });
    });
  });

  const copyBtn = $("battery-copy-prompt-btn");
  if (copyBtn && !copyBtn.dataset.bound) {
    copyBtn.dataset.bound = "1";
    copyBtn.addEventListener("click", async (e) => {
      e.stopPropagation();
      e.preventDefault();
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

  const toggleDetailsBtn = $("battery-stream-toggle-details");
  if (toggleDetailsBtn && !toggleDetailsBtn.dataset.bound) {
    toggleDetailsBtn.dataset.bound = "1";
    toggleDetailsBtn.addEventListener("click", (e) => {
      e.stopPropagation();
      const promptD = $("battery-stream-prompt-details");
      const thinkD = $("battery-stream-thinking-details");
      const respD = $("battery-stream-response-details");
      const detailsList = [promptD, thinkD, respD].filter(Boolean);
      const anyOpen = detailsList.some((d) => d.open);
      detailsList.forEach((d) => {
        d.open = !anyOpen;
      });
      toggleDetailsBtn.classList.toggle("all-collapsed", anyOpen);
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

// Re-measure marquee overflow when layout can change underneath it (window
// resize, font load). setupMarquees is read-only for up-to-date wrappers, so
// running animations are not restarted.
if (typeof window !== "undefined" && !window.__batteryMarqueeResizeWired) {
  window.__batteryMarqueeResizeWired = true;
  let _marqueeResizeTimer = null;
  window.addEventListener("resize", () => {
    if (_marqueeResizeTimer) clearTimeout(_marqueeResizeTimer);
    _marqueeResizeTimer = setTimeout(() => {
      const view = (typeof $ === "function" && $("battery-progress-view")) || null;
      if (view && !view.hidden && typeof setupMarquees === "function") {
        setupMarquees(view);
      } else if (typeof setupMarquees === "function") {
        setupMarquees(document);
      }
    }, 250);
  });
  if (document.fonts && document.fonts.ready && typeof setupMarquees === "function") {
    document.fonts.ready.then(() => {
      const view = (typeof $ === "function" && $("battery-progress-view")) || null;
      if (view && !view.hidden) setupMarquees(view);
    }).catch(() => {});
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

// Scored sub-cases (units) a test contributes. Mirrors the Go side
// (tests.Test.ComputeUnitCount): every case-level prompt/step turn counts.
function batteryTestUnitCount(test) {
  if (!test) return 1;
  if (typeof test.unit_count === "number" && test.unit_count > 0) return test.unit_count;
  if (Array.isArray(test.cases) && test.cases.length > 0) {
    let total = 0;
    for (const c of test.cases) {
      const steps = Array.isArray(c.steps) ? c.steps : [];
      total += steps.length;
      if (steps.length === 0 || c.prompt) total++;
    }
    return total || 1;
  }
  if (Array.isArray(test.steps) && test.steps.length > 0) return test.steps.length;
  return 1;
}

// Passed/total sub-cases (units) recorded in a finished test result.
function batteryResultUnitStats(r) {
  if (!r) return { passed: 0, total: 0 };
  if (r.max_points != null && r.max_points > 0) {
    return { passed: r.points || 0, total: r.max_points };
  }
  if (Array.isArray(r.sub_results) && r.sub_results.length > 0) {
    let passed = 0;
    for (const s of r.sub_results) {
      if (s.passed === true && !s.error) passed++;
    }
    return { passed, total: r.sub_results.length };
  }
  if (r.passed === true) return { passed: 1, total: 1 };
  if (r.passed === false) return { passed: 0, total: 1 };
  return { passed: 0, total: 0 };
}

function buildBatteryTimelineQueue(groupFilter, modelIDs) {
  const ids = Array.isArray(groupFilter) ? groupFilter : (groupFilter === "all" || !groupFilter ? null : [groupFilter]);
  // Group by category (group order first, then test order within the group)
  // so runs execute — and save progress — one category at a time instead
  // of interleaved. Mirrors the backend ordering in handleBatteryRun.
  const groupOrder = new Map(
    (typeof testsGroups !== "undefined" && Array.isArray(testsGroups) ? testsGroups : []).map((g) => [
      g.id,
      typeof g.order === "number" ? g.order : Number.MAX_SAFE_INTEGER,
    ])
  );
  const activeTests = tests
    .filter((t) => (ids === null || ids.includes(t.group_id)) && t.active && t.evaluation_type !== "agent")
    .sort((a, b) => {
      const goa = groupOrder.has(a.group_id) ? groupOrder.get(a.group_id) : Number.MAX_SAFE_INTEGER;
      const gob = groupOrder.has(b.group_id) ? groupOrder.get(b.group_id) : Number.MAX_SAFE_INTEGER;
      if (goa !== gob) return goa - gob;
      if (a.group_id !== b.group_id) return String(a.group_id).localeCompare(String(b.group_id));
      if ((a.order || 0) !== (b.order || 0)) return (a.order || 0) - (b.order || 0);
      return String(a.name || a.id).localeCompare(String(b.name || b.id));
    });
  const queue = [];
  let idx = 0;
  for (const model of modelIDs) {
    const caps = modelCaps(model);
    for (const test of activeTests) {
      const required = [...batteryTestEffectiveCaps(test)];
      if (required.every((c) => caps.has(c))) {
        idx++;
        queue.push({ index: idx, testId: test.id, testName: test.name, model, groupId: test.group_id, units: batteryTestUnitCount(test) });
      }
    }
  }
  return queue;
}

function updateActiveBatteryBanner(activeRun) {
  const banner = $("tests-active-battery-banner");
  const runBtn = $("tests-run-battery-btn");

  if (!activeRun || !activeRun.runID) {
    if (banner) banner.hidden = true;
    if (runBtn) {
      runBtn.disabled = false;
      runBtn.classList.remove("disabled");
      runBtn.removeAttribute("title");
    }
    return;
  }

  if (runBtn) {
    runBtn.disabled = true;
    runBtn.classList.add("disabled");
    runBtn.setAttribute("title", t("battery.already_running_toast"));
  }

  if (!banner) return;

  if (typeof currentView !== "undefined" && currentView === "battery-progress") {
    banner.hidden = true;
    return;
  }

  banner.hidden = false;

  const badge = $("active-battery-badge");
  const meta = $("active-battery-meta");
  const viewBtn = $("active-battery-view-btn");
  const titleStrong = banner.querySelector("strong");

  if (activeRun.waitingReview) {
    if (titleStrong) titleStrong.textContent = t("battery.human_review_pending_title");
    if (badge) {
      badge.textContent = t("battery.human_review_pending_badge");
      badge.className = "pill pill-warning";
    }
    if (meta) {
      meta.textContent = t("battery.human_review_pending_meta", { count: activeRun.pendingReviews || "—" });
    }
    if (viewBtn) {
      viewBtn.textContent = t("battery.human_review_start_btn");
      viewBtn.onclick = (e) => {
        e.preventDefault();
        e.stopPropagation();
        showBlindReviewView(activeRun.runID);
      };
    }
    return;
  }

  if (titleStrong) titleStrong.textContent = t("battery.active_banner_title");
  if (badge) {
    badge.textContent = activeRun.groupName || activeRun.groupId || t("battery.running") || "Running";
    badge.className = "pill pill-warning";
  }

  if (meta) {
    const parts = [];
    if (activeRun.totalTests > 0) {
      const idx = activeRun.testIndex || 1;
      const pct = Math.round(((Math.max(1, idx) - 1) / activeRun.totalTests) * 100);
      parts.push(t("battery.active_banner_test_progress", {
        index: idx,
        total: activeRun.totalTests,
        pct: pct,
      }));
    }
    if (Array.isArray(activeRun.models) && activeRun.models.length > 0) {
      parts.push(t("battery.active_banner_models", {
        models: activeRun.models.join(", "),
      }));
    }
    if (activeRun.model) {
      parts.push(activeRun.model);
    }
    meta.textContent = parts.join(" · ");
  }

  if (viewBtn) {
    viewBtn.textContent = t("battery.active_banner_view");
    viewBtn.onclick = (e) => {
      e.preventDefault();
      e.stopPropagation();
      showBatteryProgressView(activeRun.models || [], activeRun.runID, activeRun.groupId || "all");
    };
  }
}

function showBatteryProgressView(modelIDs, runID, groupId) {
  batteryCompletedTests = [];
  batteryLastTestSnapshot = null;
  batteryPollRetryCount = 0;
  batteryTimelineTotal = 0;
  batteryTimelineCompleted = [];
  batteryTimelineCurrent = null;
  batteryLiveResults = [];
  _leaderboardRowModels = []; // force a full leaderboard build for the new run

  // Try to rehydrate start time from active run or localStorage so elapsed time does not reset on re-entering
  let knownStartTime = 0;
  if (window._activeBatteryRun && window._activeBatteryRun.runID === runID && window._activeBatteryRun.startedAtUnixMs) {
    knownStartTime = window._activeBatteryRun.startedAtUnixMs;
  }
  const savedInit = localStorage.getItem(BATTERY_KEY);
  if (!knownStartTime && savedInit) {
    try {
      const d = JSON.parse(savedInit);
      if (d.runID === runID && d.startTime) {
        knownStartTime = d.startTime;
      }
    } catch { }
  }
  if (batteryActiveRunID === runID && batteryStartTime > 0) {
    // Retain existing start time
  } else {
    batteryStartTime = knownStartTime || Date.now();
  }

  // Try to rehydrate modelIDs and groupId if not provided
  if (!Array.isArray(modelIDs) || modelIDs.length === 0) {
    if (window._activeBatteryRun && window._activeBatteryRun.runID === runID && Array.isArray(window._activeBatteryRun.models) && window._activeBatteryRun.models.length > 0) {
      modelIDs = window._activeBatteryRun.models;
      if (!groupId && window._activeBatteryRun.groupId) groupId = window._activeBatteryRun.groupId;
    } else {
      const saved = localStorage.getItem(BATTERY_KEY);
      if (saved) {
        try {
          const d = JSON.parse(saved);
          if (d.runID === runID && Array.isArray(d.modelIDs) && d.modelIDs.length > 0) {
            modelIDs = d.modelIDs;
            if (!groupId && d.groupId) groupId = d.groupId;
          }
        } catch { }
      }
    }
  }

  batteryProgressModelIDs = Array.isArray(modelIDs) ? modelIDs : [];
  batteryTimelineQueue = buildBatteryTimelineQueue(groupId, batteryProgressModelIDs);

  // A new run must never inherit stuck disabled controls from a previous
  // run (e.g. a Skip whose request never settled and left finally pending).
  for (const id of ["battery-progress-retry", "battery-progress-skip", "battery-progress-skip-model", "battery-progress-abort"]) {
    const btn = $(id);
    if (btn) btn.disabled = false;
  }

  const promptDetails = $("battery-stream-prompt-details");
  if (promptDetails && typeof window !== "undefined" && window.innerWidth <= 900) {
    promptDetails.open = false;
  }

  const loadTestsAndRefresh = () => {
    return api("/api/tests").then((data) => {
      if (data) {
        if (data.tests) tests = data.tests;
        if (data.groups) testsGroups = data.groups;
        if (currentView === "battery-progress" && batteryActiveRunID === runID) {
          batteryTimelineQueue = buildBatteryTimelineQueue(groupId, batteryProgressModelIDs);
          const qLen = batteryTimelineQueue.length || 0;
          if (qLen > 0 && batteryTimelineTotal === 0) {
            batteryTimelineTotal = qLen;
          }
          const curModel = (batteryTimelineCurrent && batteryTimelineCurrent.model) || batteryProgressModelIDs[0] || "";
          const stats = computeBatteryStats(batteryProgressModelIDs, batteryLiveResults, curModel, 0, batteryTimelineTotal || qLen);
          renderBatteryKPIs({ model: curModel, total_tests: batteryTimelineTotal || qLen, test_index: 1 }, stats);
          renderBatteryLeaderboard(batteryProgressModelIDs, stats.modelMap, curModel);
          renderBatteryAnalyticsCharts(batteryProgressModelIDs, stats.modelMap);
          renderBatteryTimeline(batteryLiveResults);
        }
      }
    }).catch(() => {});
  };

  if (!tests || tests.length === 0 || !testsGroups || testsGroups.length === 0) {
    void loadTestsAndRefresh();
  }

  if (batteryElapsedInterval) {
    clearInterval(batteryElapsedInterval);
    batteryElapsedInterval = null;
  }
  batteryElapsedInterval = setInterval(updateBatteryElapsedDisplay, 1000);
  updateBatteryElapsedDisplay();

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
  // Start at the top (header/KPIs): entering a battery must not inherit or
  // jump to a scrolled-down position.
  window.scrollTo(0, 0);

  const queueLen = (batteryTimelineQueue && batteryTimelineQueue.length) || 0;
  const initialStats = computeBatteryStats(batteryProgressModelIDs, [], batteryProgressModelIDs[0] || "", 0, queueLen);
  renderBatteryKPIs({ model: batteryProgressModelIDs[0] || "", total_tests: queueLen, test_index: 1 }, initialStats);
  renderBatteryLeaderboard(batteryProgressModelIDs, initialStats.modelMap, batteryProgressModelIDs[0] || "");
  renderBatteryAnalyticsCharts(batteryProgressModelIDs, initialStats.modelMap);

  localStorage.setItem(BATTERY_KEY, JSON.stringify({ runID, modelIDs: batteryProgressModelIDs, groupId, startTime: batteryStartTime }));
  batteryActiveRunID = runID;
  const progressPath = "/tests/battery/progress/" + runID;
  if (window.location.pathname !== progressPath) {
    history.pushState(null, "", progressPath);
  }
  void pollBatteryProgress(runID, batteryProgressModelIDs);
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
      const u = batteryResultUnitStats(res);
      const partial = res.passed === false && u.total > 1 && u.passed > 0;
      if (res.passed === true) {
        dotIcon = "&#10003;";
        itemClass = "battery-timeline-item completed";
      } else if (res.passed === false) {
        // A test with sub-cases where some passed but not all is "partial",
        // not a flat failure.
        dotIcon = partial ? "&#9680;" : "&#10005;";
        itemClass = partial ? "battery-timeline-item partial" : "battery-timeline-item failed";
      } else {
        dotIcon = "&#8943;";
        itemClass = "battery-timeline-item review";
      }
      const dur = res.response_time_ms > 0 ? (res.response_time_ms / 1000).toFixed(1) + "s" : "";
      const spd = res.tokens_per_sec > 0 ? res.tokens_per_sec.toFixed(1) + " tok/s" : "";
      if (dur || spd) {
        metaDetails = ` &middot; ${dur}${spd ? " (" + spd + ")" : ""}`;
      }
      if (u.total > 1) {
        metaDetails += ` &middot; ${u.passed}/${u.total} ${escapeHtml(t("battery.subcases_short"))}`;
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
    // Sync true start time from backend so elapsed time survives refresh & view navigation
    if (p.started_at_unix_ms && p.started_at_unix_ms > 0) {
      batteryStartTime = p.started_at_unix_ms;
      try {
        const saved = localStorage.getItem(BATTERY_KEY);
        if (saved) {
          const d = JSON.parse(saved);
          if (d.runID === runID) {
            d.startTime = p.started_at_unix_ms;
            localStorage.setItem(BATTERY_KEY, JSON.stringify(d));
          }
        }
      } catch { }
    }
    // Update live results and models from server if provided
    if (p.results && Array.isArray(p.results)) {
      batteryLiveResults = p.results;
    }
    let modelsUpdated = false;
    if (p.models && Array.isArray(p.models) && p.models.length > 0) {
      if (batteryProgressModelIDs.length === 0 || JSON.stringify(batteryProgressModelIDs) !== JSON.stringify(p.models)) {
        batteryProgressModelIDs = p.models;
        modelsUpdated = true;
      }
      try {
        localStorage.setItem(BATTERY_KEY, JSON.stringify({ runID, modelIDs: p.models, groupId: p.group_id || "" }));
      } catch { }
    }

    // Ensure tests & groups are loaded (e.g. if loaded directly from another tab/device)
    if (!tests || tests.length === 0 || !testsGroups || testsGroups.length === 0) {
      try {
        const tData = await api("/api/tests");
        if (tData) {
          if (tData.tests) tests = tData.tests;
          if (tData.groups) testsGroups = tData.groups;
          modelsUpdated = true;
        }
      } catch { }
    }

    // Rebuild timeline queue if it was empty or models/tests were updated
    if ((!batteryTimelineQueue || batteryTimelineQueue.length === 0 || modelsUpdated) && batteryProgressModelIDs.length > 0) {
      const effGroupId = p.group_id || "all";
      batteryTimelineQueue = buildBatteryTimelineQueue(effGroupId, batteryProgressModelIDs);
      batteryTimelineTotal = p.total_tests || batteryTimelineQueue.length;
    }

    // Reconstruct completed timeline items for prior tests if rehydrated
    if (batteryTimelineCompleted.length === 0 && p.test_index > 1 && batteryTimelineQueue && batteryTimelineQueue.length > 0) {
      for (let idx = 1; idx < p.test_index; idx++) {
        const q = batteryTimelineQueue.find((item) => item.index === idx);
        if (q) {
          batteryTimelineCompleted.push({
            index: q.index,
            name: q.name,
            model: q.model,
            testId: q.testId,
            groupId: q.groupId,
          });
        }
      }
    }

    // Detect test change: archive previous snapshot.
    if (batteryLastTestSnapshot && batteryLastTestSnapshot.testId && p.test_id && batteryLastTestSnapshot.testId !== p.test_id) {
      batteryCompletedTests.push(batteryLastTestSnapshot);
      renderBatteryCompletedTests();
    }
    // Update timeline, bar, and count.
    updateBatteryProgressUI(p, batteryLiveResults);

    // Compute live stats and render KPIs, Leaderboard and Analytics charts
    const currentModel = p.model || (batteryProgressModelIDs.length > 0 ? batteryProgressModelIDs[0] : "");
    const stats = computeBatteryStats(batteryProgressModelIDs, batteryLiveResults, currentModel, p.test_index, p.total_tests || batteryTimelineTotal);
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
      // Per-stage snapshot from the backend (exact per-chunk accounting).
      // hasThink covers both the dedicated thinking field and tag-embedded
      // thinking so the header shows even when thinking ended early.
      const hasThink = !!(
        p.partial_thinking ||
        p.thinking_ms > 0 ||
        p.is_thinking ||
        /<(think|thinking|stitching|throat)>[\s\S]*?<\/(think|thinking|stitching|throat)>/i.test(p.partial_response || "")
      );
      const isThinking = !!p.is_thinking;
      const respActive = !isThinking && (!!p.partial_response || (p.response_ms || 0) > 0);
      batteryStageSnap = {
        key: turnKey,
        thinkMs: p.thinking_ms || 0,
        respMs: p.response_ms || 0,
        thinkChars: p.thinking_chars || 0,
        respChars: p.response_chars || 0,
        at: Date.now(),
        isThinking,
        respActive,
        hasThink,
      };
      const turnElapsedServerMs = (p.thinking_ms || 0) + (p.response_ms || 0);
      const serverTurnStart = p.turn_started_at_unix_ms > 0
        ? p.turn_started_at_unix_ms
        : (turnElapsedServerMs > 0 ? (Date.now() - turnElapsedServerMs) : 0);

      if (turnKey !== batteryActiveTurnKey) {
        batteryActiveTurnKey = turnKey;
        batteryTurnStartTime = serverTurnStart || Date.now();
        const oldThinkTok = $("battery-stream-thinking-tokens");
        if (oldThinkTok) oldThinkTok.hidden = true;
        const oldRespTok = $("battery-stream-response-tokens");
        if (oldRespTok) oldRespTok.hidden = true;
      } else if (serverTurnStart > 0 && Math.abs(batteryTurnStartTime - serverTurnStart) > 3000) {
        // Sync drift if local timer was ahead/behind by more than 3 seconds
        batteryTurnStartTime = serverTurnStart;
      }
      updateBatteryCurrentTurnTimer();

      // Topbar elements
      const testTitleEl = $("battery-stream-test-title");
      const categoryBadgeEl = $("battery-stream-category-badge");
      const casePillEl = $("battery-stream-case-pill");
      const statusBadgeEl = $("battery-stream-status-badge");

      const catName = getTestCategoryName(p.test_id, p.category || (currentTest ? currentTest.group_id : ""));
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

      // Per-stage token counters from the backend char accumulators
      // (~4 chars/token), refreshed on every progress poll.
      const thinkingTokCount = batteryStageSnap.thinkChars > 0 ? Math.max(1, Math.floor(batteryStageSnap.thinkChars / 4)) : 0;
      const responseTokCount = batteryStageSnap.respChars > 0 ? Math.max(1, Math.floor(batteryStageSnap.respChars / 4)) : 0;

      // Thinking block
      const thinkingWrap = $("battery-stream-thinking-wrap");
      const thinkingBlock = $("battery-stream-thinking");
      if (thinkingWrap && thinkingBlock) {
        thinkingBlock.textContent = p.partial_thinking || "";
        thinkingWrap.hidden = !(p.partial_thinking || p.is_thinking || (batteryStageSnap && batteryStageSnap.hasThink));
        if (p.partial_thinking) {
          thinkingBlock.scrollTo({ top: thinkingBlock.scrollHeight, behavior: "smooth" });
        }
      }
      const thinkingTokEl = $("battery-stream-thinking-tokens");
      if (thinkingTokEl) {
        if (thinkingTokCount > 0 && batteryStageSnap.hasThink) {
          thinkingTokEl.hidden = false;
          thinkingTokEl.textContent = `~${thinkingTokCount.toLocaleString()} tok`;
          thinkingTokEl.title = t("battery.stream_thinking_tokens");
        } else {
          thinkingTokEl.hidden = true;
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
      const responseTokEl = $("battery-stream-response-tokens");
      if (responseTokEl) {
        if (responseTokCount > 0) {
          responseTokEl.hidden = false;
          responseTokEl.textContent = `~${responseTokCount.toLocaleString()} tok`;
          responseTokEl.title = t("battery.stream_response_tokens");
        } else {
          responseTokEl.hidden = true;
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
    renderBatteryProgressModels(batteryProgressModelIDs, p.model || "", p.is_thinking || false);

    if (p.waiting_review) {
      if (batteryLastTestSnapshot) {
        batteryCompletedTests.push(batteryLastTestSnapshot);
        renderBatteryCompletedTests();
      }
      if (runID !== batteryActiveRunID) return;
      stopBatteryPolling();
      await new Promise((r) => setTimeout(r, 1000));
      showBlindReviewView(runID);
      return;
    }

    if (p.done) {
      // Archive final snapshot before finishing.
      if (batteryLastTestSnapshot) {
        batteryCompletedTests.push(batteryLastTestSnapshot);
        renderBatteryCompletedTests();
      }
      // The user may have navigated away while the run finished: only take
      // over the view when this run is still the followed one.
      if (runID !== batteryActiveRunID) return;
      stopBatteryPolling();
      // Let the user read the last response for a moment.
      await new Promise((r) => setTimeout(r, 1500));
      // Fetch full run: pending human reviews go to the blind triage
      // first, everything else straight to the final results.
      try {
        const run = await api("/api/runner/runs/" + encodeURIComponent(runID));
        currentBatteryRun = run;
        if (blindPendingResults(run).length > 0) {
          openBlindReviewWithRun(run);
        } else {
          localStorage.removeItem(BATTERY_KEY);
          window._activeBatteryRun = null;
          if (typeof updateBatteryGlobalStatus === "function") {
            updateBatteryGlobalStatus(null);
          }
          hideAllMainViews();
          currentView = "battery-results";
          $("battery-results-view").hidden = false;
          history.pushState(null, "", "/tests/battery/results/" + run.id);
          renderBatteryResults(run);
        }
      } catch (err) {
        console.warn("Error fetching completed run:", err);
        if (batteryLiveResults && batteryLiveResults.length > 0) {
          showBatteryResultsView(runID);
        } else {
          toast(t("toast.error", { msg: err.message }), "error");
          showTestsView();
        }
      }
      return;
    }
    // The user may have navigated away while the request was in flight:
    // do not reschedule a poll nobody follows anymore.
    if (runID !== batteryActiveRunID) return;
    batteryPollTimer = setTimeout(() => pollBatteryProgress(runID, batteryProgressModelIDs), 2000);
  } catch (err) {
    if (runID !== batteryActiveRunID) return;
    batteryPollRetryCount++;
    if (batteryPollRetryCount <= 5) {
      batteryPollTimer = setTimeout(() => pollBatteryProgress(runID, batteryProgressModelIDs), 2500);
      return;
    }
    if (currentView !== "battery-progress") {
      localStorage.removeItem(BATTERY_KEY);
      window._activeBatteryRun = null;
      if (typeof updateBatteryGlobalStatus === "function") {
        updateBatteryGlobalStatus(null);
      }
    }
    toast(t("toast.error", { msg: err.message }), "error");
    batteryPollTimer = setTimeout(() => pollBatteryProgress(runID, batteryProgressModelIDs), 5000);
  }
}

function batteryRunIdFromStorage() {
  const saved = localStorage.getItem(BATTERY_KEY);
  if (!saved) return "";
  try {
    return JSON.parse(saved).runID || "";
  } catch {
    return "";
  }
}

function resetBatteryTurnTimers() {
  batteryActiveTurnKey = "";
  batteryTurnStartTime = 0;
  batteryStageSnap = null;
  updateBatteryCurrentTurnTimer();
}

function openBatteryAbortModal() {
  $("battery-abort-modal").hidden = false;
}

function closeBatteryAbortModal() {
  $("battery-abort-modal").hidden = true;
}

// Abort the run, choosing what happens to partial results:
// "discard" drops everything, "save-completed" keeps everything completed
// so far, including the partial current model (in-flight test is dropped).
async function abortBatteryRun(mode) {
  const runID = batteryRunIdFromStorage();
  if (!runID) {
    closeBatteryAbortModal();
    return;
  }
  closeBatteryAbortModal();
  let aborted = false;
  try {
    const res = await api("/api/runner/runs/" + encodeURIComponent(runID) + "/abort", {
      method: "POST",
      body: JSON.stringify({ mode }),
    });
    aborted = !!res?.aborted;
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
    return;
  }
  if (!aborted) {
    // Run already finished: let the regular poll loop show the results.
    return;
  }
  if (mode === "discard") {
    // The backend drops the partial run; wait until it is done, then make
    // sure nothing was persisted and leave the progress view.
    stopBatteryPolling();
    const deadline = Date.now() + 30000;
    while (Date.now() < deadline) {
      try {
        const p = await api("/api/runner/runs/" + encodeURIComponent(runID) + "/progress");
        if (p?.done) break;
      } catch {
        break;
      }
      await new Promise((r) => setTimeout(r, 1000));
    }
    try {
      await api("/api/runner/runs/" + encodeURIComponent(runID), { method: "DELETE" });
    } catch { }
    localStorage.removeItem(BATTERY_KEY);
    toast(t("toast.run_discarded") || "Run discarded", "info");
    showTestsView();
  } else {
    // Pruned results are saved by the backend; the regular poll loop picks
    // up Done and shows the results view.
    resetBatteryTurnTimers();
    toast(t("toast.run_abort_saving") || "Stopping… progress so far will be kept", "info");
    if (batteryActiveRunID === runID) {
      void pollBatteryProgress(runID, []);
    }
  }
}

async function skipModelBatteryTest() {
  const runID = batteryRunIdFromStorage();
  if (!runID) return;
  const skipBtn = $("battery-progress-skip-model");
  if (skipBtn) {
    skipBtn.disabled = true;
    // Re-enable on a timer, not in finally: if the request hangs (stuck
    // backend), the button must come back anyway.
    setTimeout(() => { if (skipBtn) skipBtn.disabled = false; }, 1000);
  }
  try {
    const res = await api("/api/runner/runs/" + encodeURIComponent(runID) + "/skip-model", { method: "POST" });
    if (res?.skipped) {
      toast(t("toast.model_skipped") || "Current model skipped", "info");
      // Drop the skipped model's future queue entries so the timeline and
      // live stats no longer expect them.
      const cur = batteryTimelineCurrent;
      if (cur && cur.model && Array.isArray(batteryTimelineQueue)) {
        batteryTimelineQueue = batteryTimelineQueue.filter(
          (q) => q.model !== cur.model || (q.index || 0) <= (cur.index || 0)
        );
      }
      resetBatteryTurnTimers();
      if (batteryActiveRunID === runID) {
        void pollBatteryProgress(runID, []);
      }
    } else {
      toast(t("toast.skip_no_turn") || "No active case to skip right now", "warn");
    }
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function retryCurrentBatteryTest() {
  const saved = localStorage.getItem(BATTERY_KEY);
  if (!saved) return;
  let runID = "";
  try {
    const data = JSON.parse(saved);
    runID = data.runID || "";
  } catch { }
  if (!runID) return;
  const retryBtn = $("battery-progress-retry");
  if (retryBtn) {
    retryBtn.disabled = true;
    setTimeout(() => { if (retryBtn) retryBtn.disabled = false; }, 1000);
  }
  try {
    const res = await api("/api/runner/runs/" + encodeURIComponent(runID) + "/retry", { method: "POST" });
    if (res?.retried) {
      toast(t("toast.test_retried") || "Retrying current case", "info");
      batteryActiveTurnKey = "";
      batteryTurnStartTime = 0;
      batteryStageSnap = null;
      updateBatteryCurrentTurnTimer();
      if (batteryActiveRunID === runID) {
        void pollBatteryProgress(runID, []);
      }
    } else {
      toast(t("toast.skip_no_turn") || "No active case to skip right now", "warn");
    }
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
  if (skipBtn) {
    skipBtn.disabled = true;
    setTimeout(() => { if (skipBtn) skipBtn.disabled = false; }, 1000);
  }
  try {
    const res = await api("/api/runner/runs/" + encodeURIComponent(runID) + "/skip", { method: "POST" });
    if (res?.skipped) {
      toast(t("toast.test_skipped") || "Current case skipped", "info");
      batteryActiveTurnKey = "";
      batteryTurnStartTime = 0;
      batteryStageSnap = null;
      updateBatteryCurrentTurnTimer();
      if (batteryActiveRunID === runID) {
        void pollBatteryProgress(runID, []);
      }
    } else {
      toast(t("toast.skip_no_turn") || "No active case to skip right now", "warn");
    }
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function confirmBatteryRun() {
  if (window._activeBatteryRun && window._activeBatteryRun.runID) {
    const msg = window._activeBatteryRun.waitingReview ? t("battery.human_review_block_toast") : t("battery.already_running_toast");
    toast(msg, "warn");
    return;
  }
  if (batterySelectedModels.size === 0) {
    toast(t("battery.select_models"), "warn");
    return;
  }
  closeBatteryModal();
  const modelIDs = Array.from(batterySelectedModels);
  const payload = { model_ids: modelIDs };
  let groupFilter = "all";
  if (currentRunTarget?.type === "single" && currentRunTarget.testId) {
    payload.test_id = currentRunTarget.testId;
  } else if (currentRunTarget?.type === "multi" && Array.isArray(currentRunTarget.groupIds) && currentRunTarget.groupIds.length > 0) {
    if (currentRunTarget.groupIds.length === 1) {
      payload.group_id = currentRunTarget.groupIds[0];
      groupFilter = currentRunTarget.groupIds[0];
    } else {
      payload.group_ids = [...currentRunTarget.groupIds];
      groupFilter = [...currentRunTarget.groupIds];
    }
  } else if (currentRunTarget?.type === "group" && currentRunTarget.groupId) {
    payload.group_id = currentRunTarget.groupId;
    groupFilter = currentRunTarget.groupId;
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
      targetTests = batteryModalTargetTests();
    }
    const hasImages = (x) => [
      ...((x.cases || []).flatMap((c) => c.attachments || [])),
      ...((x.steps || []).flatMap((s) => s.attachments || [])),
      ...(x.sidecars || []),
    ].some((a) => a.kind === "image") || batteryTestEffectiveCaps(x).has("vision");
    const withImages = targetTests.filter(hasImages).map((x) => x.name);
    const noVision = modelIDs.filter((m) => !modelCaps(m).has("vision"));
    if (withImages.length > 0 && noVision.length > 0) {
      toast(t("battery.no_vision_warn", { models: noVision.join(", "), tests: withImages.slice(0, 3).join(", ") }), "warn");
    }
  } catch { /* caps lookup is best-effort */ }

  let data;
  try {
    data = await api("/api/runner/battery", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
  } catch (err) {
    if (err.status === 409 || (err.message && err.message.includes("already in progress"))) {
      toast(t("battery.already_running_toast"), "warn");
    } else {
      toast(t("toast.error", { msg: err.message }), "error");
    }
    showTestsView();
    return;
  }

  const runID = data && data.run_id;
  if (!runID) {
    toast(t("toast.error", { msg: "No run_id returned" }), "error");
    showTestsView();
    return;
  }

  showBatteryProgressView(modelIDs, runID, groupFilter);
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

// ---------- Blind human-review triage ----------
// Results with no automatic verdict (passed == null, no error) are judged
// here one by one BEFORE the final results, without revealing the model
// (or the test) so ratings stay unbiased. The queue is shuffled on entry.

let blindReviewRunId = null;
let blindReviewQueue = [];
let blindReviewIndex = 0;
let blindReviewBusy = false;

function blindPendingResults(run) {
  if (!run || !Array.isArray(run.results)) return [];
  return run.results.filter((r) => {
    if (r.passed != null || r.error) return false;
    const subs = Array.isArray(r.sub_results) ? r.sub_results : [];
    if (subs.length > 0) {
      return subs.some((s) => (s.model_response || "").trim().length > 0);
    }
    return (r.model_response || "").trim().length > 0;
  });
}

function blindShuffle(arr) {
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
  return arr;
}

function openBlindReviewWithRun(run) {
  blindReviewRunId = run.id;
  blindReviewQueue = blindShuffle(blindPendingResults(run));
  blindReviewIndex = 0;
  blindReviewBusy = false;
  currentBatteryRun = run;
  if (blindReviewQueue.length === 0) {
    showBatteryResultsView(run.id);
    return;
  }
  hideAllMainViews();
  currentView = "battery-review";
  $("battery-review-view").hidden = false;
  if (window.location.pathname !== "/tests/battery/review/" + run.id) {
    history.pushState(null, "", "/tests/battery/review/" + run.id);
  }
  renderBlindReviewCard();
}

async function showBlindReviewView(runId) {
  blindReviewRunId = runId;
  blindReviewIndex = 0;
  blindReviewBusy = false;
  hideAllMainViews();
  currentView = "battery-review";
  $("battery-review-view").hidden = false;
  if (window.location.pathname !== "/tests/battery/review/" + runId) {
    history.pushState(null, "", "/tests/battery/review/" + runId);
  }
  const body = $("battery-review-body");
  if (body) body.innerHTML = `<div class="muted" style="padding:32px;text-align:center;">${t("status.loading")}</div>`;
  try {
    if (!Array.isArray(tests) || tests.length === 0) {
      try {
        const data = await api("/api/tests");
        testsGroups = data.groups || [];
        tests = data.tests || [];
      } catch { /* prompt lookup is best-effort */ }
    }
    const run = await api("/api/runner/runs/" + encodeURIComponent(runId));
    openBlindReviewWithRun(run);
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
    showTestsView();
  }
}

function blindReviewAttachmentsHtml(test) {
  if (!test) return "";
  const allAtts = [
    ...((test.cases || []).flatMap((c) => c.attachments || [])),
    ...((test.steps || []).flatMap((s) => s.attachments || [])),
    ...(test.sidecars || []),
  ];
  if (allAtts.length === 0) return "";
  return allAtts.map((att) => {
    if (att.kind === "image") {
      const src = `data:${att.mime || "image/jpeg"};base64,${att.data}`;
      return `<div class="br-attach-item"><img src="${src}" alt="" class="br-attach-img" loading="lazy" /></div>`;
    }
    if (att.kind === "audio") {
      const src = `data:${att.mime || "audio/webm"};base64,${att.data}`;
      return `<div class="br-attach-item"><audio controls src="${src}" class="br-attach-audio"></audio></div>`;
    }
    return `<div class="br-attach-item"><span class="pill">txt</span><span class="br-attach-name">${escapeHtml(att.name || "")}</span></div>`;
  }).join("");
}

// Split one pending result into blindable blocks: one per case/step when
// the result carries sub-results, otherwise a single prompt → response.
function blindReviewBlocks(result) {
  const test = (Array.isArray(tests) ? tests : []).find((x) => x.id === result.test_id) || null;
  const attachments = blindReviewAttachmentsHtml(test);
  const subs = Array.isArray(result.sub_results) ? result.sub_results : [];
  if (subs.length > 0) {
    return {
      prompt: "",
      attachments,
      items: subs.map((s) => ({
        prompt: s.prompt || "",
        thinking: s.thinking || "",
        response: s.model_response || "",
      })),
    };
  }
  let prompt = "";
  if (test) {
    if (test.prompt) {
      prompt = test.prompt;
    } else if (Array.isArray(test.messages) && test.messages.length > 0) {
      prompt = test.messages.map((m) => `${m.role || "user"}: ${m.content || ""}`).join("\n\n");
    }
  }
  return {
    prompt,
    attachments,
    items: [{ prompt: "", thinking: result.thinking || "", response: result.model_response || "" }],
  };
}

// Approximate token count for a visible text (~4 chars/token). The backend
// only reports turn-level totals, so per-section counts are estimates
// marked with ~, just as a size reference while reviewing.
function brApproxTokens(text) {
  const n = Math.round(String(text || "").length / 4);
  if (n < 1000) return `~${n} tok`;
  return `~${(n / 1000).toFixed(n < 10000 ? 1 : 0)}k tok`;
}

function brTokCount(text) {
  if (!text) return "";
  return `<span class="br-tokcount">${escapeHtml(brApproxTokens(text))}</span>`;
}

function renderBlindReviewCard() {
  const body = $("battery-review-body");
  if (!body) return;
  const total = blindReviewQueue.length;
  const item = blindReviewQueue[blindReviewIndex];
  if (!item) {
    void finishBlindReview();
    return;
  }
  const counterEl = $("battery-review-counter");
  if (counterEl) counterEl.textContent = `${blindReviewIndex + 1}/${total}`;
  const bar = $("battery-review-progress-bar");
  if (bar) bar.style.width = `${total > 0 ? (blindReviewIndex / total) * 100 : 0}%`;

  const blocks = blindReviewBlocks(item);
  const noThinking = `<div class="muted" style="font-size:12px;">${escapeHtml(t("battery.review_no_thinking"))}</div>`;
  let mainHtml = "";
  if (blocks.prompt) {
    mainHtml += `<div class="br-section"><div class="br-label">${escapeHtml(t("battery.prompt"))}</div><div class="br-block br-prompt">${escapeHtml(blocks.prompt)}</div></div>`;
  }
  if (blocks.attachments) {
    mainHtml += `<div class="br-section"><div class="br-label">${escapeHtml(t("battery.review_input"))}</div><div class="br-attachments">${blocks.attachments}</div></div>`;
  }
  blocks.items.forEach((it, i) => {
    if (it.prompt) {
      mainHtml += `<div class="br-section"><div class="br-label">${escapeHtml(t("battery.prompt"))}${blocks.items.length > 1 ? ` · ${i + 1}` : ""}</div><div class="br-block br-prompt">${escapeHtml(it.prompt)}</div></div>`;
    }
    mainHtml += `<div class="br-section"><div class="br-label"><span>🧠 ${escapeHtml(t("battery.review_thinking"))}</span>${brTokCount(it.thinking)}</div>${it.thinking ? `<div class="br-block br-thinking">${escapeHtml(it.thinking)}</div>` : noThinking}</div>`;
    mainHtml += `<div class="br-section"><div class="br-label"><span>💬 ${escapeHtml(t("battery.review_output"))}</span>${brTokCount(it.response)}</div><div class="br-block br-response">${escapeHtml(it.response) || `<span class="muted">${escapeHtml(t("battery.no_response"))}</span>`}</div></div>`;
  });

  body.innerHTML = `
    <div class="br-card">
      ${mainHtml}
      <div class="br-actions">
        <button type="button" class="ghost danger-text br-vote-btn" id="br-vote-fail">❌ ${escapeHtml(t("battery.review_fail"))} <kbd>←</kbd></button>
        <button type="button" class="primary br-vote-btn" id="br-vote-pass">✅ ${escapeHtml(t("battery.review_pass"))} <kbd>→</kbd></button>
      </div>
    </div>
  `;
  $("br-vote-fail")?.addEventListener("click", () => { void rateBlindReview(false); });
  $("br-vote-pass")?.addEventListener("click", () => { void rateBlindReview(true); });
}

async function rateBlindReview(passed) {
  if (blindReviewBusy) return;
  const item = blindReviewQueue[blindReviewIndex];
  if (!item || !blindReviewRunId) return;
  blindReviewBusy = true;
  const failBtn = $("br-vote-fail");
  const passBtn = $("br-vote-pass");
  if (failBtn) failBtn.disabled = true;
  if (passBtn) passBtn.disabled = true;
  try {
    await api("/api/runner/runs/" + encodeURIComponent(blindReviewRunId) + "/rate", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ test_id: item.test_id, model: item.model, passed }),
    });
    item.passed = passed;
    blindReviewIndex++;
    if (blindReviewIndex >= blindReviewQueue.length) {
      await finishBlindReview();
    } else {
      renderBlindReviewCard();
    }
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  } finally {
    blindReviewBusy = false;
  }
}

async function finishBlindReview() {
  const runId = blindReviewRunId;
  blindReviewRunId = null;
  blindReviewQueue = [];
  blindReviewIndex = 0;
  localStorage.removeItem(BATTERY_KEY);
  window._activeBatteryRun = null;
  if (typeof updateBatteryGlobalStatus === "function") {
    updateBatteryGlobalStatus(null);
  }
  // Refetch: Points/Score are recomputed server-side on rating.
  currentBatteryRun = null;
  toast(t("battery.review_done"), "success");
  showBatteryResultsView(runId);
}

function showBatteryHistoryView(filterTestId = null, filterModel = null, filterCategory = null) {
  hideAllMainViews();
  currentView = "battery-history";
  currentHistoryFilterTestId = filterTestId || null;
  currentHistoryFilterModel = filterModel || null;
  currentHistoryFilterCategory = filterCategory || null;
  batteryHistoryState = null;
  $("battery-history-view").hidden = false;
  let path = "/tests/battery/history";
  if (filterTestId) {
    path = "/tests/history/" + encodeURIComponent(filterTestId);
  }
  const qs = [];
  if (filterModel) qs.push("model=" + encodeURIComponent(filterModel));
  if (filterCategory) qs.push("category=" + encodeURIComponent(filterCategory));
  if (qs.length) path += (path.includes("?") ? "&" : "?") + qs.join("&");
  if (window.location.pathname + window.location.search !== path) {
    history.pushState(null, "", path);
  }
  void renderBatteryHistory();
}

let batteryResultsViewMode = "matrix";

// Fractional score for a test result: every scored sub-case is one point,
// so partial passes earn proportional credit. Errors and pending human
// reviews are not countable.
function batteryResultScore(r) {
  if (r.error) return null;
  if (r.max_points != null && r.max_points > 0) {
    return { earned: r.points || 0, total: r.max_points };
  }
  if (r.sub_results && r.sub_results.length > 0) {
    let earned = 0;
    let total = 0;
    for (const s of r.sub_results) {
      if (s.error) continue;
      if (s.passed === true) {
        earned++;
        total++;
      } else if (s.passed === false) {
        total++;
      }
    }
    if (total > 0) return { earned, total };
    return null;
  }
  if (r.passed === true) return { earned: 1, total: 1 };
  if (r.passed === false) return { earned: 0, total: 1 };
  return null;
}

// Column-relative heatmap background and text color for leaderboard cells (green to red palette).
function batteryLbHeatStyle(v, range, strong) {
  if (v == null) return "";
  let rel = 1.0;
  if (range && typeof range.max === "number" && typeof range.min === "number") {
    if (range.max > range.min) {
      rel = Math.max(0, Math.min(1, (v - range.min) / (range.max - range.min)));
    } else if (range.max === 0) {
      rel = 0.0;
    } else {
      rel = 1.0;
    }
  }
  const hue = Math.round(rel * 120); // 120 = green, 60 = yellow, 0 = red
  const base = strong ? 18 : 10;
  const span = strong ? 38 : 30;
  const bgPct = (base + rel * span).toFixed(0);
  const textL = (58 + rel * 18).toFixed(0);
  return `background: color-mix(in srgb, hsl(${hue}, 75%, 42%) ${bgPct}%, transparent); color: hsl(${hue}, 88%, ${textL}%); font-weight: ${rel >= 0.95 ? "700" : "600"};`;
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
    modelStats[m] = { pass: 0, fail: 0, human: 0, total: 0, passUnits: 0, failUnits: 0, totalUnits: 0, timeSum: 0, reasoning: 0, tpsSum: 0, tpsCount: 0 };
  }
  for (const r of run.results) {
    const s = modelStats[r.model];
    if (!s) continue;
    s.total++;
    const u = batteryResultUnitStats(r);
    s.totalUnits += u.total;
    s.passUnits += u.passed;
    s.failUnits += Math.max(0, u.total - u.passed);
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
    const pct = Math.round((s.passUnits / (s.totalUnits || 1)) * 100);
    const okClass = s.passUnits === s.totalUnits && s.totalUnits > 0 ? "pill-good" : (s.passUnits > 0 ? "pill-warn" : "pill-bad");

    summaryHtml += `
      <div class="battery-summary-card">
        <h4>${escapeHtml(m)}</h4>
        <div class="battery-summary-card-body">
          <div class="big">${s.passUnits} / ${s.totalUnits} <span class="pill ${okClass}" style="font-size:12px;margin-left:6px;">${pct}%</span></div>
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
    summaryHtml += `<div class="battery-pending-review">⏳ <strong>${escapeHtml(t("battery.pending_review", { n: pendingReview.length }))}</strong> <span class="muted">${escapeHtml(t("battery.pending_review_hint"))}</span> <button type="button" class="primary battery-mini-btn" id="battery-start-review-btn">🙈 ${escapeHtml(t("battery.start_review"))}</button></div>`;
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
      const pct = (s.passUnits / (s.totalUnits || 1)) * 100;
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
  // Optional categories still show as columns but are excluded from the run's
  // overall, mirroring the global leaderboard.
  const groupIsRequired = (gid) => {
    const g = testsGroups.find((x) => x.id === gid);
    return !g || g.required !== false;
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
      if (!groupIsRequired(gid)) continue;
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
    const lbOverallLabel = escapeHtml(t("battery.leaderboard_overall"));
    let lbHeaderCols = `<th class="cell-lb-overall-head">${t("battery.leaderboard_overall")}</th>`;
    for (const gid of groupIdsPresent) {
      const optBadge = groupIsRequired(gid) ? "" : ` <span class="pill" title="${escapeHtml(t("tests.group_optional_hint"))}">${escapeHtml(t("tests.group_optional"))}</span>`;
      lbHeaderCols += `<th class="cell-lb-group-head" title="${escapeHtml(groupName(gid))}">${escapeHtml(groupName(gid))}${optBadge}</th>`;
    }

    let lbBodyRows = "";
    lbRows.forEach((row, idx) => {
      let cells = "";
      if (row.overall == null) {
        cells += `<td class="cell-lb-score cell-lb-overall cell-lb-empty" data-lb-col="${lbOverallLabel}"><span class="muted">—</span></td>`;
      } else {
        cells += `<td class="cell-lb-score cell-lb-overall mono" data-lb-col="${lbOverallLabel}" style="${heatStyle(row.overall, overallRange, true)}" title="${row.earned.toFixed(1)}/${row.total.toFixed(1)} pts">${row.overall.toFixed(1)}</td>`;
      }
      for (const gid of groupIdsPresent) {
        const c = scores[row.model][gid];
        const lbColLabel = escapeHtml(groupName(gid));
        if (!c || c.total === 0) {
          cells += `<td class="cell-lb-score cell-lb-empty" data-lb-col="${lbColLabel}"><span class="muted">—</span></td>`;
          continue;
        }
        const pct = Math.min(100.0, Math.max(0.0, (c.earned / c.total) * 100));
        cells += `<td class="cell-lb-score mono" data-lb-col="${lbColLabel}" style="${heatStyle(pct, colRange[gid], false)}" title="${c.earned.toFixed(1)}/${c.total.toFixed(1)} pts">${pct.toFixed(1)}</td>`;
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
    // Group by model so the model name appears once per section instead of
    // once per test row. Within a model, tests keep the stored order.
    const modelOrder = (Array.isArray(run.models) && run.models.length > 0)
      ? run.models
      : [...new Set(run.results.map((r) => r.model))];

    const modelSections = modelOrder.map((model) => {
      const modelResults = run.results.filter((r) => r.model === model);
      if (modelResults.length === 0) return "";

      const total = modelResults.length;
      const passed = modelResults.filter((r) => r.passed === true).length;
      const pct = total > 0 ? Math.round((passed / total) * 100) : 0;
      const passClass = passed === total && total > 0 ? "pill-good" : (passed > 0 ? "pill-warn" : "pill-bad");
      const timeSum = modelResults.reduce((a, r) => a + (r.response_time_ms || 0), 0);
      const avgMs = total > 0 ? Math.round(timeSum / total) : 0;
      const tpsVals = modelResults.filter((r) => r.tokens_per_sec > 0).map((r) => r.tokens_per_sec);
      const avgTps = tpsVals.length > 0 ? tpsVals.reduce((a, b) => a + b, 0) / tpsVals.length : 0;
      const reasoningCount = modelResults.filter((r) => r.reasoning_used).length;

      const rows = testIds.map((tid) => {
        const r = modelResults.find((x) => x.test_id === tid);
        if (!r) return "";
        const test = tests.find((t) => t.id === tid);
        const isHumanReview = test?.evaluation_type === "human_review";
        const testName = r.test_name || tid;
        const evalLabel = test?.evaluation_type
          ? `<span class="battery-eval-label">${escapeHtml(t("tests.eval_" + test.evaluation_type) || test.evaluation_type)}</span>`
          : "";
        const humanReviewLabel = isHumanReview
          ? `<span class="battery-human-review-label">${t("battery.human_review")}</span>`
          : "";
        const promptBtn = `<button type="button" class="battery-prompt-link" data-test-id="${escapeHtml(tid)}">${t("battery.prompt")}</button>`;

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

        const reasoningIcon = r.reasoning_used
          ? `<span class="battery-detail-chip" title="${escapeHtml(t("battery.reasoning_used"))}">🧠</span>`
          : "";
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

        return `
          <div class="battery-detail-row">
            <div class="battery-detail-head">
              <div class="battery-detail-test">
                <span class="battery-detail-test-name">${escapeHtml(testName)}</span>
                ${evalLabel}
                ${humanReviewLabel}
                ${promptBtn}
              </div>
              <div class="battery-detail-result">
                ${resultCell}
                <span class="battery-detail-chip mono" title="${escapeHtml(t("battery.response_time"))}">⏱️ ${fmtDuration(r.response_time_ms)}</span>
                ${r.tokens_per_sec > 0
                  ? `<span class="battery-detail-chip mono" style="color:${tokColor}">⚡ ${r.tokens_per_sec.toFixed(1)} <span class="unit">tok/s</span></span>`
                  : `<span class="battery-detail-chip mono muted">— <span class="unit">tok/s</span></span>`
                }
                ${reasoningIcon}
              </div>
            </div>
            <div class="battery-detail-response">${responseCellHtml}</div>
          </div>
        `;
      }).join("");

      return `
        <section class="battery-model-group">
          <header class="battery-model-group-head">
            <div class="battery-model-group-name" title="${escapeHtml(model)}">${escapeHtml(model)}</div>
            <div class="battery-model-group-stats">
              <span class="pill ${passClass}">${passed} / ${total} (${pct}%)</span>
              <span class="mono muted">⏱️ ${fmtDuration(avgMs)}</span>
              ${avgTps > 0 ? `<span class="mono muted">⚡ ${avgTps.toFixed(1)} tok/s</span>` : ""}
              ${reasoningCount > 0 ? `<span class="mono muted">🧠 ${reasoningCount}</span>` : ""}
            </div>
          </header>
          <div class="battery-model-group-rows">${rows}</div>
        </section>
      `;
    }).join("");

    detailedTableHtml = modelOrder.length > 0
      ? `<div class="battery-detailed-groups">${modelSections}</div>`
      : "";
  }
  body.innerHTML = podiumHtml + summaryHtml + viewToggleHtml + matrixTableHtml + leaderboardTableHtml + chartsHtml + detailedTableHtml;

  const btnMatrix = body.querySelector("#btn-view-matrix");
  if (btnMatrix) {
    btnMatrix.addEventListener("click", () => {
      batteryResultsViewMode = "matrix";
      renderBatteryResults(run);
    });
  }
  const btnStartReview = body.querySelector("#battery-start-review-btn");
  if (btnStartReview) {
    btnStartReview.addEventListener("click", () => {
      openBlindReviewWithRun(run);
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

let batteryHistoryState = null;

function batteryHistoryResetState() {
  batteryHistoryState = {
    q: "",
    model: currentHistoryFilterModel || "",
    category: currentHistoryFilterCategory || "",
    testId: currentHistoryFilterTestId || null,
    offset: 0,
    limit: 25,
    total: 0,
    hasMore: false,
    runs: [],
    summary: null,
    loading: false,
  };
}

function batteryHistoryResetPage() {
  if (!batteryHistoryState) batteryHistoryResetState();
  batteryHistoryState.offset = 0;
  batteryHistoryState.total = 0;
  batteryHistoryState.hasMore = false;
  batteryHistoryState.runs = [];
  batteryHistoryState.summary = null;
}

function batteryHistoryQuery(state, offsetOverride) {
  const params = new URLSearchParams();
  params.set("offset", String(offsetOverride == null ? state.offset : offsetOverride));
  params.set("limit", String(state.limit));
  if (state.q) params.set("q", state.q);
  if (state.model) params.set("model", state.model);
  if (state.category) params.set("category", state.category);
  if (state.testId) params.set("test_id", state.testId);
  return params.toString();
}

function batteryHistorySyncUrl() {
  const state = batteryHistoryState;
  let path = "/tests/battery/history";
  if (state.testId) path = "/tests/history/" + encodeURIComponent(state.testId);
  const qs = [];
  if (state.model) qs.push("model=" + encodeURIComponent(state.model));
  if (state.category) qs.push("category=" + encodeURIComponent(state.category));
  if (qs.length) path += (path.includes("?") ? "&" : "?") + qs.join("&");
  if (window.location.pathname + window.location.search !== path) {
    history.pushState(null, "", path);
  }
}

function populateBatteryHistoryFilters() {
  const state = batteryHistoryState;

  const modelSel = $("battery-history-model-select");
  if (modelSel) {
    const names = new Set();
    for (const m of (Array.isArray(models) ? models : [])) {
      if (!m.archived) names.add(m.name);
    }
    for (const g of (typeof ghostModels !== "undefined" && Array.isArray(ghostModels) ? ghostModels : [])) {
      if (g && g.name) names.add(g.name);
    }
    if (state.model) names.add(state.model);
    let opts = `<option value="">${escapeHtml(t("tests.all_models"))}</option>`;
    for (const n of [...names].sort((a, b) => a.localeCompare(b))) {
      opts += `<option value="${escapeHtml(n)}"${n === state.model ? " selected" : ""}>${escapeHtml(n)}</option>`;
    }
    modelSel.innerHTML = opts;
  }

  const catSel = $("battery-history-category-select");
  if (catSel) {
    const groups = Array.isArray(testsGroups) ? testsGroups : [];
    let opts = `<option value="">${escapeHtml(t("battery.all_categories"))}</option>`;
    for (const g of groups) {
      opts += `<option value="${escapeHtml(g.id)}"${g.id === state.category ? " selected" : ""}>${escapeHtml(g.name || g.id)}</option>`;
    }
    catSel.innerHTML = opts;
  }

  const search = $("battery-history-search");
  if (search && search.value !== state.q) search.value = state.q;

  const clearBtn = $("battery-history-clear-filters");
  if (clearBtn) clearBtn.hidden = !(state.q || state.model || state.category || state.testId);
}

function batteryHistoryRefresh() {
  if (!batteryHistoryState) batteryHistoryResetState();
  batteryHistoryResetPage();
  void renderBatteryHistory();
}

function wireBatteryHistoryFilters() {
  const search = $("battery-history-search");
  if (search && !search.dataset.wired) {
    search.dataset.wired = "1";
    let timer = null;
    search.addEventListener("input", () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (!batteryHistoryState) batteryHistoryResetState();
        batteryHistoryState.q = search.value.trim();
        batteryHistoryRefresh();
      }, 250);
    });
  }
  const modelSel = $("battery-history-model-select");
  if (modelSel && !modelSel.dataset.wired) {
    modelSel.dataset.wired = "1";
    modelSel.addEventListener("change", () => {
      if (!batteryHistoryState) batteryHistoryResetState();
      batteryHistoryState.model = modelSel.value || "";
      currentHistoryFilterModel = batteryHistoryState.model || null;
      batteryHistorySyncUrl();
      batteryHistoryRefresh();
    });
  }
  const catSel = $("battery-history-category-select");
  if (catSel && !catSel.dataset.wired) {
    catSel.dataset.wired = "1";
    catSel.addEventListener("change", () => {
      if (!batteryHistoryState) batteryHistoryResetState();
      batteryHistoryState.category = catSel.value || "";
      currentHistoryFilterCategory = batteryHistoryState.category || null;
      batteryHistorySyncUrl();
      batteryHistoryRefresh();
    });
  }
  const clearBtn = $("battery-history-clear-filters");
  if (clearBtn && !clearBtn.dataset.wired) {
    clearBtn.dataset.wired = "1";
    clearBtn.addEventListener("click", () => {
      currentHistoryFilterTestId = null;
      currentHistoryFilterModel = null;
      currentHistoryFilterCategory = null;
      batteryHistoryResetState();
      batteryHistorySyncUrl();
      void renderBatteryHistory();
    });
  }
}

async function renderBatteryHistory() {
  const body = $("battery-history-body");
  if (!body) return;
  if (!batteryHistoryState) batteryHistoryResetState();
  const state = batteryHistoryState;

  // Deep-linked view: make sure category/model dropdowns have data.
  if ((!Array.isArray(testsGroups) || testsGroups.length === 0 || !Array.isArray(tests) || tests.length === 0) && typeof refreshTests === "function") {
    try { await refreshTests(); } catch (_) {}
  }
  if ((typeof models === "undefined" || !Array.isArray(models) || models.length === 0) && typeof refreshModels === "function") {
    try { await refreshModels(); } catch (_) {}
  }

  populateBatteryHistoryFilters();
  wireBatteryHistoryFilters();
  wireBatteryHistoryBody();

  body.innerHTML = `<div class="muted" style="padding:24px 0;">${t("status.loading")}</div>`;
  try {
    const data = await api("/api/runner/runs?" + batteryHistoryQuery(state, 0));
    state.runs = data.runs || [];
    state.total = typeof data.total === "number" ? data.total : state.runs.length;
    state.offset = state.runs.length;
    state.hasMore = !!data.has_more;
    state.summary = data.model_summary || null;
    renderBatteryHistoryBody();
  } catch (err) {
    body.innerHTML = `<div class="muted" style="padding:24px 0;">${escapeHtml(err.message)}</div>`;
  }
}

async function loadMoreBatteryHistory() {
  const state = batteryHistoryState;
  if (!state || state.loading || !state.hasMore) return;
  state.loading = true;
  const btn = $("battery-history-load-more");
  if (btn) {
    btn.disabled = true;
    btn.textContent = t("status.loading");
  }
  try {
    const data = await api("/api/runner/runs?" + batteryHistoryQuery(state, state.offset));
    const more = data.runs || [];
    state.runs = state.runs.concat(more);
    state.offset += more.length;
    state.hasMore = !!data.has_more;
    state.total = typeof data.total === "number" ? data.total : state.total;
    renderBatteryHistoryBody();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  } finally {
    state.loading = false;
  }
}

function batteryHistoryTrendHtml(runs) {
  const recent = runs.slice(0, 15).reverse();
  const rows = recent.map((run) => {
    const pass = run.pass_count || 0;
    const total = run.total_count || 0;
    if (!total) return null;
    const pct = (pass / total) * 100;
    const date = String(run.timestamp || "").slice(5, 16).replace("T", " ");
    const label = `${date} · ${run.group_name || ""}`;
    const title = `${run.id} — ${pass}/${total} — ${(run.models || []).join(", ")}`;
    return batteryBarRow(label, title, pct, `${pct.toFixed(0)}%`, "");
  }).filter(Boolean);
  if (rows.length < 2) return "";
  return `<section class="battery-chart-section">
    <h4>📈 ${escapeHtml(t("battery.charts_trend"))}</h4>
    ${rows.join("")}
  </section>`;
}

function batteryHistorySummaryHtml() {
  const s = batteryHistoryState?.summary;
  if (!s || !batteryHistoryState.model) return "";
  const total = s.total || 0;
  const pass = s.pass || 0;
  const pct = total > 0 ? Math.round((pass / total) * 100) : 0;
  const passClass = pct === 100 ? "pill-good" : (pct > 0 ? "pill-warn" : "pill-bad");
  const avgMs = s.avg_ms || 0;
  const avgTps = s.avg_tps || 0;
  const avgTpsColor = avgTps > 0 && typeof getToksRecordColor === "function" ? getToksRecordColor(avgTps) : "";
  return `
    <div class="battery-history-model-summary">
      <div class="battery-history-model-summary-left">
        <h3>🤖 ${escapeHtml(batteryHistoryState.model)}</h3>
        <p>${escapeHtml(t("battery.model_stats_title"))}</p>
      </div>
      <div class="battery-history-model-summary-stats">
        <div class="battery-history-model-stat-item">
          <span class="battery-history-model-stat-val pill ${passClass}" style="font-size:16px;">${pass} / ${total} (${pct}%)</span>
          <span class="battery-history-model-stat-lbl">${escapeHtml(t("battery.model_overall_pass"))}</span>
        </div>
        <div class="battery-history-model-stat-item">
          <span class="battery-history-model-stat-val mono">⏱️ ${fmtDuration(avgMs)}</span>
          <span class="battery-history-model-stat-lbl">${escapeHtml(t("battery.response_time"))}</span>
        </div>
        ${avgTps > 0 ? `
          <div class="battery-history-model-stat-item">
            <span class="battery-history-model-stat-val mono" style="color:${avgTpsColor}">⚡ ${avgTps.toFixed(1)} tok/s</span>
            <span class="battery-history-model-stat-lbl">${escapeHtml(t("battery.avg_tok_sec"))}</span>
          </div>
        ` : ""}
      </div>
    </div>`;
}

function batteryHistoryBannerHtml() {
  const state = batteryHistoryState;
  if (!state.testId) return "";
  const test = (Array.isArray(tests) ? tests : []).find((x) => x.id === state.testId);
  const testName = test?.name || state.testId;
  return `
    <div class="battery-history-filter-banner">
      <span>${escapeHtml(t("battery.history_for", { name: testName }))}</span>
      <button type="button" class="primary battery-mini-btn" id="battery-history-open-leaderboard" style="margin-left:auto;margin-right:8px;">🏆 ${escapeHtml(t("tests.leaderboard_title"))}</button>
    </div>`;
}

function batteryHistoryCardHtml(run) {
  const state = batteryHistoryState;
  const date = fmtDateTimeFull(run.timestamp);
  const modelsBadges = (run.models || []).map((m) =>
    `<span class="pill${m === state.model ? " pill-good" : ""}">${escapeHtml(m)}</span>`
  ).join("");

  let scoreHtml;
  if (state.model) {
    const st = (run.model_stats || {})[state.model] || { pass: 0, total: 0 };
    const cls = st.pass === st.total && st.total > 0 ? "pill-good" : (st.pass > 0 ? "pill-warn" : "pill-bad");
    const tps = st.avg_tps ? ` · ⚡ ${st.avg_tps.toFixed(1)} tok/s` : "";
    scoreHtml = `<span class="pill ${cls}">${st.pass} / ${st.total} OK${tps}</span>`;
  } else {
    const pass = run.pass_count || 0;
    const total = run.total_count || 0;
    const cls = pass === total && total > 0 ? "pill-good" : (pass > 0 ? "pill-warn" : "pill-bad");
    scoreHtml = `<span class="pill ${cls}">${pass} / ${total} OK</span>`;
  }

  return `
    <div class="battery-history-card" data-run-id="${escapeHtml(run.id)}">
      <div class="battery-history-card-left">
        <div class="battery-history-card-title-row">
          <span class="battery-history-group-name">${escapeHtml(run.group_name || t("battery.all_tests"))}</span>
          ${scoreHtml}
        </div>
        <div class="battery-history-meta-row">
          <span class="battery-history-date muted mono">${escapeHtml(date)}</span>
          <div class="battery-history-models-wrap">${modelsBadges}</div>
        </div>
      </div>
      <div class="battery-history-card-actions">
        <button type="button" class="primary battery-history-view-btn" data-run-id="${escapeHtml(run.id)}">${escapeHtml(t("battery.results"))}</button>
        <button type="button" class="ghost danger-text battery-history-delete" data-run-id="${escapeHtml(run.id)}" title="${escapeHtml(t("action.delete"))}">🗑️</button>
      </div>
    </div>`;
}

function renderBatteryHistoryBody() {
  const body = $("battery-history-body");
  if (!body) return;
  const state = batteryHistoryState;

  if (state.runs.length === 0) {
    body.innerHTML = batteryHistoryBannerHtml() + batteryHistorySummaryHtml() + `
      <div class="battery-empty">
        <div>${escapeHtml(t("battery.no_history"))}</div>
      </div>`;
    wireBatteryHistoryBody();
    return;
  }

  const shown = state.runs.length;
  const loadMoreHtml = state.hasMore
    ? `<div class="battery-history-load-more-wrap">
        <button type="button" class="ghost battery-mini-btn" id="battery-history-load-more">${escapeHtml(t("battery.load_more"))}</button>
      </div>`
    : "";

  body.innerHTML = batteryHistoryBannerHtml()
    + batteryHistorySummaryHtml()
    + batteryHistoryTrendHtml(state.runs)
    + `<div class="battery-history-count muted">${escapeHtml(t("battery.history_showing", { shown: String(shown), total: String(state.total) }))}</div>`
    + `<div class="battery-history-list">${state.runs.map(batteryHistoryCardHtml).join("")}</div>`
    + loadMoreHtml;

  wireBatteryHistoryBody();
}

function wireBatteryHistoryBody() {
  const body = $("battery-history-body");
  if (!body || body.dataset.wired) return;
  body.dataset.wired = "1";

  body.addEventListener("click", async (e) => {
    const loadMore = e.target.closest("#battery-history-load-more");
    if (loadMore) {
      e.stopPropagation();
      void loadMoreBatteryHistory();
      return;
    }
    const lbBtn = e.target.closest("#battery-history-open-leaderboard");
    if (lbBtn) {
      openTestHistoryModal(batteryHistoryState.testId);
      return;
    }
    const delBtn = e.target.closest(".battery-history-delete");
    if (delBtn) {
      e.stopPropagation();
      const id = delBtn.dataset.runId;
      const ok = await askConfirm({
        title: t("action.delete"),
        text: t("tests.delete_text"),
        okText: t("action.delete"),
        okClass: "danger",
      });
      if (!ok.ok) return;
      try {
        await api("/api/runner/runs/" + encodeURIComponent(id), { method: "DELETE" });
        batteryHistoryRefresh();
      } catch (err) {
        toast(t("toast.error", { msg: err.message }), "error");
      }
      return;
    }
    const viewBtn = e.target.closest(".battery-history-view-btn");
    const card = e.target.closest(".battery-history-card");
    if (viewBtn || card) {
      const id = (viewBtn || card).dataset.runId;
      if (!id) return;
      history.pushState(null, "", "/tests/battery/results/" + id);
      showBatteryResultsView(id);
    }
  });
}
