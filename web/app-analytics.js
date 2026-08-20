"use strict";

// ---------- analytics ----------

const ANALYTICS_FILTER_KEY = "ollamaMgr.analyticsFilters";
let analyticsAllData = [];
let analyticsFilters = { source: "all", paramsMin: "", paramsMax: "", tpsMin: "", family: "all", type: "all" };
try {
  const saved = JSON.parse(localStorage.getItem(ANALYTICS_FILTER_KEY) || "null");
  if (saved) analyticsFilters = Object.assign(analyticsFilters, saved);
} catch { }

async function renderAnalytics() {
  const countEl = $("analytics-count");
  const charts = ["tps", "size", "coldload", "tokens", "calls"].map((k) => $(`analytics-chart-${k}`));
  if (charts.every((c) => !c)) return;
  try {
    const data = await api("/api/models");
    // Archived models remain available in their archive view and history, but
    // must not influence analytics or comparison filters.
    const installed = (data.models || []).filter((m) => !m.archived);
    analyticsAllData = installed.concat(data.ghost_models || []);
    if (countEl) {
      const ghostCount = (data.ghost_models || []).length;
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
  const num = { paramsMin: "analytics-filter-params-min", paramsMax: "analytics-filter-params-max", tpsMin: "analytics-filter-tps-min" };
  for (const k in num) {
    const el = $(num[k]);
    if (el) el.value = analyticsFilters[k];
  }
}

function analyticsFilterMatches(p) {
  const f = analyticsFilters;
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

function renderAnalyticsFiltered() {
  const filtered = analyticsAllData.filter((m) => analyticsFilterMatches(analyticsPoint(m)));
  renderTpsVsParams(filtered);
  renderSizeVsParams(filtered);
  renderColdLoad(filtered);
  renderTokensBar(filtered);
  renderCallsBar(filtered);
  renderMetaTable(filtered);
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
  bind("analytics-filter-source", "source");
  bind("analytics-filter-family", "family");
  bind("analytics-filter-type", "type");
  bind("analytics-filter-params-min", "paramsMin");
  bind("analytics-filter-params-max", "paramsMax");
  bind("analytics-filter-tps-min", "tpsMin");
  const reset = $("analytics-filter-reset");
  if (reset) {
    reset.addEventListener("click", () => {
      analyticsFilters = { source: "all", paramsMin: "", paramsMax: "", tpsMin: "", family: "all", type: "all" };
      persistAnalyticsFilters();
      syncAnalyticsFilterControls();
      renderAnalyticsFiltered();
    });
  }
}

function persistAnalyticsFilters() {
  try { localStorage.setItem(ANALYTICS_FILTER_KEY, JSON.stringify(analyticsFilters)); } catch { }
}

// Combine metadata for a model: prefer the live modelView fields, fall back to
// usage-record metadata carried by ghosts. Returns a plain object.
function analyticsPoint(m) {
  const parameterCount = Number(m.parameter_count) || 0;
  const paramsRaw = m.parameter_size || (parameterCount ? formatExactParams(parameterCount) : "");
  return {
    name: m.name,
    ghost: !!m.is_ghost,
    // Some Ollama MLX entries omit parameter_size, while model_info still
    // provides general.parameter_count. Use that exact value as the fallback.
    params: parseParamSize(paramsRaw) || parameterCount,
    paramsLabel: paramsRaw || "—",
    sizeBytes: Number(m.size) || 0,
    quant: m.quantization || "",
    family: m.family || "",
    tps: Number(m.record_tokens_per_sec) || 0,
    tpsAt: m.record_tokens_per_sec_at || null,
    coldLoadMs: Number(m.min_cold_load_ms) || 0,
    totalTokens: Number(m.total_tokens) || 0,
    totalCalls: Number(m.total_calls) || 0,
    parameterCount,
    architecture: m.architecture || "",
    fileType: Number(m.file_type) || 0,
    sizeLabel: m.size_label || "",
    isMOE: !!m.is_moe,
  };
}

const ANALYTICS_COLORS = [
  "#4f8cff", "#e07b39", "#2ecc71", "#a855f7", "#f5c542",
  "#ef5b7d", "#22b8cf", "#8f8fff", "#f48024", "#5fbf6a",
];

function analyticsColor(i) {
  return ANALYTICS_COLORS[i % ANALYTICS_COLORS.length];
}

