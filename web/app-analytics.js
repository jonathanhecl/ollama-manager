"use strict";

// ---------- analytics ----------

const ANALYTICS_FILTER_KEY = "ollamaMgr.analyticsFilters";
let analyticsAllData = [];
let analyticsFilters = { name: "", source: "all", paramsMin: "", paramsMax: "", tpsMin: "", family: "all", type: "all" };
try {
  const saved = JSON.parse(localStorage.getItem(ANALYTICS_FILTER_KEY) || "null");
  if (saved) analyticsFilters = Object.assign(analyticsFilters, saved);
} catch { }

async function renderAnalytics() {
  const countEl = $("analytics-count");
  const charts = ["tps", "size", "efficiency", "coldload"].map((k) => $(`analytics-chart-${k}`));
  if (charts.every((c) => !c)) return;
  try {
    const data = await api("/api/models");
    // Archived and custom models must not influence analytics or comparison filters.
    // Custom models have their stats attributed directly to their base models.
    const installed = (data.models || []).filter((m) => !m.archived && !m.is_custom);
    const ghosts = (data.ghost_models || []).filter((m) => !m.is_custom);
    analyticsAllData = installed.concat(ghosts);
    if (countEl) {
      const ghostCount = ghosts.length;
      countEl.textContent = t("analytics.count", { models: analyticsAllData.length, ghosts: ghostCount });
    }
    populateAnalyticsFamilyFilter(analyticsAllData);
    syncAnalyticsFilterControls();
    renderAnalyticsFiltered();
  } catch (e) {
    if (countEl) countEl.textContent = "";
    charts.forEach((c) => {
      if (c) c.innerHTML = `<div class="analytics-empty muted">${escapeHtml(t("toast.error", { msg: e.message }))}</div>`;
    });
  }
}

function populateAnalyticsFamilyFilter(all) {
  const sel = $("analytics-filter-family");
  if (!sel) return;
  const fams = [...new Set(all.map((m) => analyticsPoint(m).family).filter(Boolean))].sort();
  const current = analyticsFilters.family;
  sel.innerHTML = `<option value="all">${escapeHtml(t("analytics.family_all"))}</option>`;
  fams.forEach((f) => {
    const o = document.createElement("option");
    o.value = f;
    o.textContent = f;
    if (f === current) o.selected = true;
    sel.appendChild(o);
  });
  if (!fams.includes(current) && current !== "all") {
    analyticsFilters.family = "all";
    sel.value = "all";
  }
}

function syncAnalyticsFilterControls() {
  const map = { source: "analytics-filter-source", family: "analytics-filter-family", type: "analytics-filter-type" };
  for (const k in map) {
    const el = $(map[k]);
    if (el) el.value = analyticsFilters[k];
  }
  const text = { name: "analytics-filter-name" };
  for (const k in text) {
    const el = $(text[k]);
    if (el) el.value = analyticsFilters[k] || "";
  }
  const num = { paramsMin: "analytics-filter-params-min", paramsMax: "analytics-filter-params-max", tpsMin: "analytics-filter-tps-min" };
  for (const k in num) {
    const el = $(num[k]);
    if (el) el.value = analyticsFilters[k];
  }
}

function analyticsFilterMatches(p) {
  const f = analyticsFilters;
  if (f.name && !p.name.toLowerCase().includes(f.name.trim().toLowerCase())) return false;
  if (f.source === "installed" && p.ghost) return false;
  if (f.source === "ghost" && !p.ghost) return false;
  if (f.family !== "all" && p.family !== f.family) return false;
  if (f.type === "dense" && p.isMOE) return false;
  if (f.type === "moe" && !p.isMOE) return false;
  if (f.paramsMin !== "" && p.params > 0 && p.params < Number(f.paramsMin) * 1e9) return false;
  if (f.paramsMax !== "" && p.params > 0 && p.params > Number(f.paramsMax) * 1e9) return false;
  if (f.tpsMin !== "" && p.tps > 0 && p.tps < Number(f.tpsMin)) return false;
  return true;
}

function bindAnalyticsFilters() {
  const bind = (id, key) => {
    const el = $(id);
    if (!el) return;
    el.addEventListener("change", () => {
      analyticsFilters[key] = el.value;
      persistAnalyticsFilters();
      renderAnalyticsFiltered();
    });
    if (el.tagName === "INPUT") {
      el.addEventListener("input", () => {
        analyticsFilters[key] = el.value;
        persistAnalyticsFilters();
        renderAnalyticsFiltered();
      });
    }
  };
  bind("analytics-filter-name", "name");
  bind("analytics-filter-source", "source");
  bind("analytics-filter-family", "family");
  bind("analytics-filter-type", "type");
  bind("analytics-filter-params-min", "paramsMin");
  bind("analytics-filter-params-max", "paramsMax");
  bind("analytics-filter-tps-min", "tpsMin");
  const reset = $("analytics-filter-reset");
  if (reset) {
    reset.addEventListener("click", () => {
      analyticsFilters = { name: "", source: "all", paramsMin: "", paramsMax: "", tpsMin: "", family: "all", type: "all" };
      persistAnalyticsFilters();
      syncAnalyticsFilterControls();
      renderAnalyticsFiltered();
    });
  }
}

function persistAnalyticsFilters() {
  try { localStorage.setItem(ANALYTICS_FILTER_KEY, JSON.stringify(analyticsFilters)); } catch { }
}

function analyticsPoint(m) {
  const parameterCount = Number(m.parameter_count) || 0;
  const paramsRaw = m.parameter_size || (parameterCount ? formatExactParams(parameterCount) : "");
  const params = parseParamSize(paramsRaw) || parameterCount;
  const sizeBytes = Number(m.size) || 0;
  const tps = Number(m.record_tokens_per_sec) || 0;
  const coldLoadMs = Number(m.min_cold_load_ms) || 0;
  const totalTokens = Number(m.total_tokens) || 0;
  const totalCalls = Number(m.total_calls) || 0;

  // Derived metrics
  const sizeGB = sizeBytes > 0 ? sizeBytes / 1e9 : 0;
  const efficiencyTokPerGB = sizeGB > 0 && tps > 0 ? tps / sizeGB : 0;
  const loadThroughputMBs = coldLoadMs > 0 && sizeBytes > 0 ? (sizeBytes / 1e6) / (coldLoadMs / 1000) : 0;
  const paramsB = params > 0 ? params / 1e9 : 0;

  return {
    raw: m,
    name: m.name,
    ghost: !!m.is_ghost,
    params,
    paramsB,
    paramsLabel: paramsRaw || "—",
    sizeBytes,
    sizeGB,
    quant: m.quantization || "",
    family: m.family || "other",
    tps,
    tpsAt: m.record_tokens_per_sec_at || null,
    coldLoadMs,
    loadThroughputMBs,
    efficiencyTokPerGB,
    totalTokens,
    totalCalls,
    parameterCount,
    architecture: m.architecture || "",
    fileType: Number(m.file_type) || 0,
    sizeLabel: m.size_label || "",
    isMOE: !!m.is_moe,
    contextLength: Number(m.context_length) || 0,
  };
}

const FAMILY_PALETTE = {
  gemma: "#4f8cff",
  gemma2: "#4f8cff",
  gemma3: "#38bdf8",
  gemma4: "#06b6d4",
  qwen: "#a855f7",
  qwen2: "#a855f7",
  qwen25: "#c084fc",
  qwen35: "#e879f9",
  qwen3: "#d946ef",
  llama: "#2ecc71",
  llama2: "#2ecc71",
  llama3: "#10b981",
  llama31: "#059669",
  mistral: "#f59e0b",
  mixtral: "#d97706",
  phi: "#ec4899",
  phi3: "#f43f5e",
  deepseek: "#3b82f6",
  command: "#8b5cf6",
  other: "#94a3b8",
};

const ANALYTICS_COLORS = [
  "#38bdf8", "#a855f7", "#10b981", "#f59e0b", "#ec4899",
  "#06b6d4", "#6366f1", "#f97316", "#84cc16", "#14b8a6",
];

function analyticsColor(i) {
  return ANALYTICS_COLORS[i % ANALYTICS_COLORS.length];
}

function modelFamilyColor(family, isGhost) {
  if (isGhost) return "#94a3b8";
  if (!family) return "#38bdf8";
  const norm = family.toLowerCase().replace(/[^a-z0-9]/g, "");
  for (const k in FAMILY_PALETTE) {
    if (norm.startsWith(k) || norm.includes(k)) return FAMILY_PALETTE[k];
  }
  return "#38bdf8";
}

function renderAnalyticsFiltered() {
  const filtered = analyticsAllData.filter((m) => analyticsFilterMatches(analyticsPoint(m)));
  renderAnalyticsKPIs(filtered);
  renderTpsVsParams(filtered);
  renderSizeVsParams(filtered);
  renderEfficiency(filtered);
  renderColdLoad(filtered);
  renderMetaTable(filtered);
}

