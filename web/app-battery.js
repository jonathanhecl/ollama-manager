"use strict";

// ---------- battery runner ----------

async function openBatteryModal() {
  if (selectedGroupId === "") return;
  batterySelectedModels.clear();
  $("battery-modal").hidden = false;
  renderBatteryModalModels();
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

function closeBatteryModal() {
  $("battery-modal").hidden = true;
}

function renderBatteryModalModels() {
  const container = $("battery-modal-models");
  if (!container) return;

  // Reuse main list cache (capabilities + archived state); skip archived models.
  const activeModels = models.filter((m) => !m.archived);

  // Determine required caps for the selected group.
  const groupTests = tests.filter((t) => t.group_id === selectedGroupId && t.active && t.evaluation_type !== "agent");
  const requiredCaps = new Set();
  for (const t of groupTests) {
    for (const c of t.required_caps || []) requiredCaps.add(c);
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
    })
    .filter(({ disabled }) => !disabled);

  if (items.length === 0) {
    container.innerHTML = `<div class="muted">${t("state.empty_models")}</div>`;
    return;
  }

  container.innerHTML = items.map(({ m, disabled, title }) => {
    const capsHtml = (m.capabilities || []).map((c) => `<span class="pill">${escapeHtml(c)}</span>`).join("");
    return `
      <label class="battery-model-item" title="${escapeHtml(title)}">
        <input type="checkbox" value="${escapeHtml(m.name)}" ${disabled ? "disabled" : ""} />
        <span class="battery-model-name">${escapeHtml(m.name)}</span>
        <span class="battery-model-caps">${capsHtml}</span>
      </label>
    `;
  }).join("");

  container.querySelectorAll('input[type="checkbox"]').forEach((cb) => {
    cb.addEventListener("change", () => {
      if (cb.checked) batterySelectedModels.add(cb.value);
      else batterySelectedModels.delete(cb.value);
    });
  });
}

let batteryPollTimer = null;
let batteryCompletedTests = [];
let batteryLastTestSnapshot = null;
let batteryTimelineTotal = 0;
let batteryTimelineCompleted = []; // {index, name, model}
let batteryTimelineCurrent = null; // {index, name, model, isThinking}
let batteryTimelineQueue = []; // {index, testId, testName, model}
let batteryTimelineScrollKey = "";
let batteryProgressModelIDs = [];
const testHistoryResponses = new Map(); // respKey -> full response string

function renderBatteryProgressModels(modelIDs, currentModel, isThinking) {
  const container = $("battery-progress-models");
  if (!container) return;
  const currentIdx = currentModel ? modelIDs.indexOf(currentModel) : -1;
  const startIdx = Math.max(0, currentIdx);
  const visibleModels = [];
  for (let i = startIdx; i < Math.min(startIdx + 3, modelIDs.length); i++) {
    visibleModels.push(modelIDs[i]);
  }
  container.innerHTML = visibleModels.map((m) => {
    const modelIndex = modelIDs.indexOf(m);
    const isRunning = m === currentModel;
    const isDone = currentIdx !== -1 && modelIndex < currentIdx;
    let status = t("battery.status_pending");
    if (isRunning) {
      status = t(isThinking ? "battery.status_thinking" : "battery.status_running");
    } else if (isDone) {
      status = t("battery.status_done");
    }
    const cls = isRunning ? "battery-progress-model running" : (isDone ? "battery-progress-model done" : "battery-progress-model");
    return `
      <div class="${cls}" data-model="${escapeHtml(m)}">
        <span class="battery-progress-dot"></span>
        <span class="battery-progress-name">${escapeHtml(m)}</span>
        <span class="battery-progress-status">${escapeHtml(status)}</span>
      </div>
    `;
  }).join("");
}

function buildBatteryTimelineQueue(groupId, modelIDs) {
  const activeTests = tests
    .filter((t) => t.group_id === groupId && t.active && t.evaluation_type !== "agent")
    .sort((a, b) => (a.order || 0) - (b.order || 0));
  const queue = [];
  let idx = 0;
  for (const model of modelIDs) {
    const caps = modelCaps(model);
    for (const test of activeTests) {
      const required = (test.required_caps || []).map((c) => String(c).toLowerCase());
      if (required.every((c) => caps.has(c))) {
        idx++;
        queue.push({ index: idx, testId: test.id, testName: test.name, model });
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
  batteryTimelineQueue = groupId ? buildBatteryTimelineQueue(groupId, modelIDs) : [];
  batteryTimelineScrollKey = "";

  const completedEl = $("battery-completed-tests");
  const headingEl = $("battery-completed-tests-heading");
  if (completedEl) { completedEl.innerHTML = ""; completedEl.hidden = true; }
  if (headingEl) headingEl.hidden = true;

  // Reset new UI elements
  const fill = $("battery-progress-fill");
  const count = $("battery-progress-count");
  const timeline = $("battery-timeline");
  const sub = $("battery-progress-sub");
  if (fill) fill.style.width = "0%";
  if (count) count.textContent = "0 / 0";
  if (timeline) timeline.innerHTML = `<div class="battery-timeline-empty">${escapeHtml(t("battery.starting"))}</div>`;
  if (sub) sub.textContent = t("battery.progress_sub", { count: String(modelIDs.length) });

  hideAllMainViews();
  currentView = "battery-progress";
  $("battery-progress-view").hidden = false;

  batteryProgressModelIDs = modelIDs;
  renderBatteryProgressModels(modelIDs, "", false);
  localStorage.setItem(BATTERY_KEY, JSON.stringify({ runID, modelIDs, groupId }));
  const progressPath = "/tests/battery/progress/" + runID;
  if (window.location.pathname !== progressPath) {
    history.pushState(null, "", progressPath);
  }
  void pollBatteryProgress(runID, modelIDs);
}

function renderBatteryTimeline() {
  const container = $("battery-timeline");
  if (!container) return;
  if (batteryTimelineTotal === 0) {
    container.innerHTML = `<div class="battery-timeline-empty">${escapeHtml(t("battery.starting"))}</div>`;
    return;
  }

  let html = "";
  // Completed items
  for (const item of batteryTimelineCompleted) {
    html += `
      <div class="battery-timeline-item completed">
        <div class="battery-timeline-left">
          <div class="battery-timeline-dot">&#10003;</div>
          <div class="battery-timeline-line"></div>
        </div>
        <div class="battery-timeline-body">
          <div class="battery-timeline-name">${escapeHtml(item.name || "Test")}</div>
          <div class="battery-timeline-meta">${escapeHtml(item.model || "")}</div>
        </div>
      </div>
    `;
  }
  // Current item
  if (batteryTimelineCurrent) {
    html += `
      <div class="battery-timeline-item active">
        <div class="battery-timeline-left">
          <div class="battery-timeline-dot pulse"></div>
          <div class="battery-timeline-line"></div>
        </div>
        <div class="battery-timeline-body">
          <div class="battery-timeline-name">${escapeHtml(batteryTimelineCurrent.name || "Test")}</div>
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
    html += `
      <div class="battery-timeline-item pending">
        <div class="battery-timeline-left">
          <div class="battery-timeline-dot"></div>
          ${isLast ? "" : "<div class=\"battery-timeline-line\"></div>"}
        </div>
        <div class="battery-timeline-body">
          <div class="battery-timeline-name">${escapeHtml(queueItem ? queueItem.testName : t("battery.status_pending"))}</div>
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

function updateBatteryProgressUI(p) {
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
    };
  } else if (done) {
    // Archive final current
    if (batteryTimelineCurrent) {
      batteryTimelineCompleted.push({
        index: batteryTimelineCurrent.index,
        name: batteryTimelineCurrent.name,
        model: batteryTimelineCurrent.model,
        testId: batteryTimelineCurrent.testId,
      });
    }
    batteryTimelineCurrent = null;
  }

  renderBatteryTimeline();
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
  if (batteryPollTimer) {
    clearTimeout(batteryPollTimer);
    batteryPollTimer = null;
  }
  try {
    const p = await api("/api/runner/runs/" + encodeURIComponent(runID) + "/progress");
    // Detect test change: archive previous snapshot.
    if (batteryLastTestSnapshot && batteryLastTestSnapshot.testId && p.test_id && batteryLastTestSnapshot.testId !== p.test_id) {
      batteryCompletedTests.push(batteryLastTestSnapshot);
      renderBatteryCompletedTests();
    }
    // Update timeline, bar, and count.
    updateBatteryProgressUI(p);

    // Update streaming panel.
    const streamPanel = $("battery-stream-panel");
    const currentTest = tests.find((t) => t.id === p.test_id);
    if (streamPanel && p.test_name && !p.done) {
      streamPanel.hidden = false;
      const promptName = $("battery-stream-prompt-name");
      const promptBlock = $("battery-stream-prompt");
      const thinkingBlock = $("battery-stream-thinking");
      const responseBlock = $("battery-stream-response");
      if (promptName) promptName.textContent = escapeHtml(p.test_name);
      if (promptBlock && currentTest) promptBlock.textContent = currentTest.prompt || "";
      if (thinkingBlock) {
        thinkingBlock.textContent = p.partial_thinking || "";
        thinkingBlock.parentElement.hidden = !p.partial_thinking;
        if (thinkingBlock.previousElementSibling) thinkingBlock.previousElementSibling.hidden = !p.partial_thinking;
        thinkingBlock.scrollTo({ top: thinkingBlock.scrollHeight, behavior: "smooth" });
      }
      if (responseBlock) {
        responseBlock.textContent = p.partial_response || "";
        responseBlock.hidden = !p.partial_response;
        if (responseBlock.previousElementSibling) responseBlock.previousElementSibling.hidden = !p.partial_response;
        if (p.partial_response) {
          responseBlock.scrollTo({ top: responseBlock.scrollHeight, behavior: "smooth" });
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
    batteryPollTimer = setTimeout(() => pollBatteryProgress(runID, modelIDs), 2000);
  } catch (err) {
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

async function confirmBatteryRun() {
  if (batterySelectedModels.size === 0) {
    toast(t("battery.select_models"), "warn");
    return;
  }
  closeBatteryModal();
  const modelIDs = Array.from(batterySelectedModels);
  try {
    const data = await api("/api/runner/battery", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ group_id: selectedGroupId, model_ids: modelIDs }),
    });
    const runID = data.run_id;
    if (!runID) {
      toast(t("toast.error", { msg: "No run_id returned" }), "error");
      return;
    }
    showBatteryProgressView(modelIDs, runID, selectedGroupId);
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

function showBatteryHistoryView() {
  hideAllMainViews();
  currentView = "battery-history";
  $("battery-history-view").hidden = false;
  if (window.location.pathname !== "/tests/battery/history") {
    history.pushState(null, "", "/tests/battery/history");
  }
  void renderBatteryHistory();
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
    modelStats[m] = { pass: 0, fail: 0, human: 0, total: 0, timeSum: 0, reasoning: 0 };
  }
  for (const r of run.results) {
    const s = modelStats[r.model];
    if (!s) continue;
    s.total++;
    s.timeSum += r.response_time_ms;
    if (r.reasoning_used) s.reasoning++;
    if (r.passed === true) s.pass++;
    else if (r.passed === false) s.fail++;
    else s.human++;
  }

  // Summary cards.
  let summaryHtml = `<div class="battery-summary">`;
  for (const m of run.models) {
    const s = modelStats[m];
    const avg = s.total > 0 ? Math.round(s.timeSum / s.total) : 0;
    summaryHtml += `
      <div class="battery-summary-card">
        <h4>${escapeHtml(m)}</h4>
        <div class="big">${s.pass} / ${s.total}</div>
        <div class="sub">${t("battery.response_time")}: ${avg}ms · ${t("battery.reasoning_used")}: ${s.reasoning}</div>
      </div>
    `;
  }
  summaryHtml += `</div>`;

  // Table.
  let rowsHtml = "";
  // Group results by test_id.
  const byTest = {};
  for (const r of run.results) {
    if (!byTest[r.test_id]) byTest[r.test_id] = [];
    byTest[r.test_id].push(r);
  }
  const testIds = Object.keys(byTest);

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
        const hasRealResponse = (r.tokens_per_sec || 0) > 0 && (r.model_response || "").trim().length > 0;
        if (r.error) {
          resultCell = `<span class="badge badge-na" title="${escapeHtml(r.error)}">${t("battery.error")}</span>`;
        } else if (!hasRealResponse && r.passed === false) {
          resultCell = `<span class="badge badge-na" title="${escapeHtml(r.model_response || t("battery.no_response"))}">${t("battery.error")}</span>`;
        } else if (r.passed === true) {
          resultCell = `<span class="badge badge-pass">${t("battery.pass")}</span>`;
        } else if (r.passed === false) {
          resultCell = `<span class="badge badge-fail">${t("battery.fail")}</span>`;
        } else {
          resultCell = `<span class="badge badge-human">${t("battery.human_review")}</span>`;
        }
      }

      const reasoningIcon = r.reasoning_used ? "🧠" : "";
      const tps = r.tokens_per_sec ? `${r.tokens_per_sec.toFixed(1)} tok/s` : "";
      const resp = r.model_response || "";
      const respId = `br-${run.id}-${r.test_id}-${escapeHtml(r.model)}`;
      const respShort = escapeHtml(resp.slice(0, 200));
      const respRest = escapeHtml(resp.slice(200));

      rowsHtml += `
        <tr>
          ${i === 0 ? `<td class="cell-test" rowspan="${results.length}"><strong>${escapeHtml(testName)}</strong>${evalLabel}${humanReviewLabel}${promptBtn}</td>` : ""}
          <td class="cell-model">${escapeHtml(r.model)}</td>
          <td>${resultCell}</td>
          <td class="cell-time">${fmtDuration(r.response_time_ms)} ${reasoningIcon}<br><span class="muted" style="font-size:11px">${escapeHtml(tps)}</span></td>
          <td class="cell-response">
            <span class="resp-short">${respShort}${resp.length > 200 ? `<button type="button" class="resp-toggle" data-target="${respId}">…</button>` : ""}</span>
            ${resp.length > 200 ? `<span class="resp-rest" id="${respId}" hidden>${respRest}</span>` : ""}
          </td>
        </tr>
      `;
    }
  }

  body.innerHTML = summaryHtml + `
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

  body.querySelectorAll(".resp-toggle").forEach((btn) => {
    btn.addEventListener("click", () => {
      const target = document.getElementById(btn.dataset.target);
      if (!target) return;
      target.hidden = !target.hidden;
      btn.textContent = target.hidden ? "…" : "▲";
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

  // Attachments
  const attachEl = $("human-review-attachments");
  if (attachEl) {
    const attHtml = (test.attachments || []).map((att) => {
      if (att.kind === "image") {
        const src = `data:${att.mime || "image/jpeg"};base64,${att.data}`;
        return `<div class="hr-attach-item"><img src="${src}" alt="${escapeHtml(att.name || "")}" class="hr-attach-img" /></div>`;
      }
      if (att.kind === "audio") {
        const src = `data:${att.mime || "audio/webm"};base64,${att.data}`;
        return `<div class="hr-attach-item"><audio controls src="${src}" class="hr-attach-audio"></audio><span class="hr-attach-name">${escapeHtml(att.name || "")}</span></div>`;
      }
      return "";
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
    const runs = data.runs || [];
    if (runs.length === 0) {
      body.innerHTML = `<div class="battery-empty">${t("battery.no_history")}</div>`;
      return;
    }
    body.innerHTML = runs.map((run) => {
      const date = fmtDateTimeFull(run.timestamp);
      return `
        <div class="battery-history-item" data-run-id="${escapeHtml(run.id)}">
          <span class="battery-history-date">${escapeHtml(date)}</span>
          <span class="battery-history-group">${escapeHtml(run.group_name)}</span>
          <span class="battery-history-models">${escapeHtml((run.models || []).join(", "))}</span>
          <span class="battery-history-counts">${run.pass_count || 0} / ${run.total_count || 0}</span>
          <button type="button" class="ghost danger-text battery-history-delete" data-run-id="${escapeHtml(run.id)}">×</button>
        </div>
      `;
    }).join("");

    body.querySelectorAll(".battery-history-item").forEach((el) => {
      el.addEventListener("click", (e) => {
        if (e.target.closest("button")) return;
        const id = el.dataset.runId;
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

