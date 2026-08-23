"use strict";

// ---------- status ----------
async function refreshStatus() {
  try {
    const s = await api("/api/status");
    lastSystemStatus = s;
    managerApiOk = true;
    ollamaHostOk = !!s.ollama_reachable;
    if (s.language && s.language !== window.I18n.getLang()) {
      window.I18n.setLang(s.language);
      renderTable();
      if (typeof currentView !== "undefined" && currentView === "analytics" && typeof renderAnalytics === "function") renderAnalytics();
      if (typeof currentView !== "undefined" && currentView === "downloads" && typeof renderDownloads === "function") renderDownloads();
      if (typeof activeName !== "undefined" && activeName && typeof openDetail === "function") openDetail(activeName);
    }
    const pill = $("status-pill");
    if (s.ollama_reachable) {
      pill.textContent = t("status.online");
      pill.className = "pill pill-good";
    } else {
      pill.textContent = t("status.offline");
      pill.className = "pill pill-bad";
    }
    $("settings-logout-btn").hidden = !s.has_password;
    updateSystemWidgets(s);
    updateChatSendEnabled();
    if (Array.isArray(s.running)) {
      runningModels = s.running;
      applyRunning(s.running);
      updateLoadedDotsOnly();
      updateChatModelLoadDot();
      patchDetailLoadedState();
      if ($("running-modal") && !$("running-modal").hidden) {
        renderRunningModalList();
      }
    }
  } catch (e) {
    lastSystemStatus = null;
    managerApiOk = false;
    ollamaHostOk = false;
    $("status-pill").textContent = t("status.unreachable");
    $("status-pill").className = "pill pill-bad";
    updateSystemWidgets(null);
    updateChatSendEnabled();
  }
}

function updateSystemWidgets(status) {
  const compact = window.matchMedia("(max-width: 900px)").matches;
  const hasSeparateVram = Number(status?.vram_total) > 0;
  const metricsStrip = document.querySelector(".metrics-strip");
  if (metricsStrip) {
    metricsStrip.classList.toggle("has-vram", hasSeparateVram);
  }
  const cpuPct = Number(status?.cpu_used_pct);
  const cpuModel = status?.cpu_model ? String(status.cpu_model).trim() : "";
  const cpuLabelEl = $("cpu-widget-label");
  if (cpuLabelEl) {
    if (cpuModel) {
      cpuLabelEl.textContent = cpuModel;
      cpuLabelEl.title = cpuModel;
    } else {
      cpuLabelEl.textContent = t("status.cpu");
      cpuLabelEl.removeAttribute("title");
    }
  }

  const roundedPct = Math.round(cpuPct || 0);
  const cpuTitle = cpuModel
    ? `${cpuModel} • ${t("status.cpu_title", { pct: roundedPct })}`
    : t("status.cpu_title", { pct: roundedPct });

  updateMetricWidget({
    wrapId: "cpu-widget",
    fillId: "cpu-widget-fill",
    textId: "cpu-widget-text",
    pct: cpuPct,
    text: t("status.cpu_short", { pct: roundedPct }),
    title: cpuTitle,
    warn: true,
  });

  updateMemoryWidget(status, compact);
  updateVramWidget(status, compact);

  updateDiskWidget(status, compact);
}

function installedModelsBytes() {
  return models.reduce((acc, m) => {
    const size = Number(m?.size);
    if (!Number.isFinite(size) || size <= 0) return acc;
    return acc + size;
  }, 0);
}

function loadedModelsTotalEstimateBytes() {
  return models.reduce((acc, m) => {
    if (!m || !m.loaded) return acc;
    const size = Number(m?.size);
    const total = Number.isFinite(size) && size > 0 ? size : 0;
    return acc + total;
  }, 0);
}

function updateMemoryWidget(status, compact) {
  const wrap = $("memory-widget");
  const modelsFill = $("memory-widget-fill-models");
  const otherFill = $("memory-widget-fill");
  const textNode = $("memory-widget-text");
  if (!wrap || !modelsFill || !otherFill || !textNode) return;

  const memoryTotal = Number(status?.memory_total) || 0;
  const memoryUsedRaw = Number(status?.memory_used) || 0;
  const memoryPct = Number(status?.memory_used_pct);
  if (memoryTotal <= 0 || !Number.isFinite(memoryPct)) {
    wrap.hidden = true;
    return;
  }

  const memoryUsed = Math.max(0, Math.min(memoryUsedRaw, memoryTotal));
  const hasServerLoadedTotal = !!(status && Object.prototype.hasOwnProperty.call(status, "models_loaded_bytes"));
  // Systems with a separate VRAM pool (Windows/Linux with a GPU) load models
  // into VRAM, so the RAM widget shows no models segment there. On unified
  // memory (macOS) models live in RAM.
  const hasSeparateVram = Number(status?.vram_total) > 0;
  const loadedModelsApprox = hasServerLoadedTotal
    ? (hasSeparateVram ? 0 : (Number(status?.models_loaded_bytes) || 0))
    : loadedModelsTotalEstimateBytes();
  const modelUsed = Math.min(Math.max(0, loadedModelsApprox), memoryUsed);
  const otherUsed = Math.max(0, memoryUsed - modelUsed);

  const modelsPct = (modelUsed / memoryTotal) * 100;
  const otherPct = (otherUsed / memoryTotal) * 100;
  const freePct = ((memoryTotal - memoryUsed) / memoryTotal) * 100;

  modelsFill.style.width = `${Math.max(0, Math.min(100, modelsPct)).toFixed(1)}%`;
  otherFill.style.width = `${Math.max(0, Math.min(100, otherPct)).toFixed(1)}%`;
  textNode.textContent = compact
    ? t("status.percent_short", { pct: Math.round(memoryPct) })
    : t("status.memory_short", { used: fmtBytes(memoryUsed), total: fmtBytes(memoryTotal) });
  wrap.title = t("status.memory_breakdown_title", {
    models: fmtBytes(modelUsed),
    other: fmtBytes(otherUsed),
    free: fmtBytes(Math.max(0, memoryTotal - memoryUsed)),
    total: fmtBytes(memoryTotal),
    pct: Math.round(freePct),
  });
  wrap.hidden = false;
}

function updateVramWidget(status, compact) {
  const wrap = $("vram-widget");
  const modelsFill = $("vram-widget-fill-models");
  const otherFill = $("vram-widget-fill");
  const textNode = $("vram-widget-text");
  if (!wrap || !modelsFill || !otherFill || !textNode) return;

  const vramTotal = Number(status?.vram_total) || 0;
  const vramUsedRaw = Number(status?.vram_used) || 0;
  const vramPct = Number(status?.vram_used_pct);
  if (vramTotal <= 0 || !Number.isFinite(vramPct)) {
    wrap.hidden = true;
    return;
  }

  const vramUsed = Math.max(0, Math.min(vramUsedRaw, vramTotal));
  const modelUsed = Math.min(Math.max(0, Number(status?.models_vram_loaded_bytes) || 0), vramUsed);
  const otherUsed = Math.max(0, vramUsed - modelUsed);

  const modelsPct = (modelUsed / vramTotal) * 100;
  const otherPct = (otherUsed / vramTotal) * 100;

  modelsFill.style.width = `${Math.max(0, Math.min(100, modelsPct)).toFixed(1)}%`;
  otherFill.style.width = `${Math.max(0, Math.min(100, otherPct)).toFixed(1)}%`;
  textNode.textContent = compact
    ? t("status.percent_short", { pct: Math.round(vramPct) })
    : t("status.vram_short", { used: fmtBytes(vramUsed), total: fmtBytes(vramTotal) });
  wrap.title = t("status.vram_breakdown_title", {
    models: fmtBytes(modelUsed),
    other: fmtBytes(otherUsed),
    free: fmtBytes(Math.max(0, vramTotal - vramUsed)),
    total: fmtBytes(vramTotal),
    pct: Math.round(vramPct),
  });
  wrap.hidden = false;
}

function updateDiskWidget(status, compact) {
  const wrap = $("disk-widget");
  const modelsFill = $("disk-widget-fill-models");
  const otherFill = $("disk-widget-fill");
  const textNode = $("disk-widget-text");
  if (!wrap || !modelsFill || !otherFill || !textNode) return;

  const diskTotal = Number(status?.disk_total_bytes) || 0;
  const diskFree = Number(status?.disk_free_bytes) || 0;
  if (diskTotal <= 0) {
    wrap.hidden = true;
    return;
  }

  const clampedFree = Math.max(0, Math.min(diskFree, diskTotal));
  const diskUsed = Math.max(0, diskTotal - clampedFree);
  const modelUsed = Math.min(Math.max(0, installedModelsBytes()), diskUsed);
  const otherUsed = Math.max(0, diskUsed - modelUsed);

  const modelsPct = (modelUsed / diskTotal) * 100;
  const otherPct = (otherUsed / diskTotal) * 100;
  const freePct = (clampedFree / diskTotal) * 100;
  modelsFill.style.width = `${Math.max(0, Math.min(100, modelsPct)).toFixed(1)}%`;
  otherFill.style.width = `${Math.max(0, Math.min(100, otherPct)).toFixed(1)}%`;

  textNode.textContent = compact
    ? fmtBytes(clampedFree)
    : t("status.disk_free_short", { free: fmtBytes(clampedFree), total: fmtBytes(diskTotal) });
  wrap.title = t("status.disk_breakdown_title", {
    models: fmtBytes(modelUsed),
    other: fmtBytes(otherUsed),
    free: fmtBytes(clampedFree),
    total: fmtBytes(diskTotal),
    pct: Math.round(freePct),
  });
  wrap.hidden = false;
}

function updateMetricWidget({ wrapId, fillId, textId, pct, text, title, warn = false, bad = false, hideWhenInvalid = true }) {
  const wrap = $(wrapId);
  const fill = $(fillId);
  const textNode = $(textId);
  if (!wrap || !fill || !textNode) return;

  if (!Number.isFinite(pct)) {
    if (hideWhenInvalid) {
      wrap.hidden = true;
      return;
    }
    fill.style.width = "0%";
    fill.classList.remove("warn", "bad");
    textNode.textContent = text || "—";
    wrap.title = title || "";
    wrap.hidden = false;
    return;
  }

  const clampedPct = Math.max(0, Math.min(100, pct));
  fill.style.width = `${clampedPct.toFixed(1)}%`;
  fill.classList.toggle("warn", !!warn && !bad);
  fill.classList.toggle("bad", !!bad);
  textNode.textContent = text || "—";
  wrap.title = title || "";
  wrap.hidden = false;
}

function updateChatSendEnabled() {
  const btn = $("chat-send-btn");
  let ok = managerApiOk && ollamaHostOk;
  if (btn) {
    if (!ok) {
      if (!managerApiOk) {
        btn.title = t("chat.send_disabled_manager");
      } else {
        btn.title = t("chat.send_disabled_ollama");
      }
    } else {
      btn.title = chatStreamLock ? t("chat.queue_send") : t("chat.send");
    }
    btn.textContent = chatStreamLock ? t("chat.queue_send") : t("chat.send");
    btn.disabled = !ok;
  }
}

async function copyTextToClipboard(text) {
  const s = String(text ?? "").replace(/^[\r\n]+/, "");
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(s);
      return true;
    }
  } catch {
    // fall back
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = s;
    ta.style.position = "fixed";
    ta.style.left = "-9999px";
    document.body.appendChild(ta);
    ta.select();
    const r = document.execCommand("copy");
    ta.remove();
    return r;
  } catch {
    return false;
  }
}

