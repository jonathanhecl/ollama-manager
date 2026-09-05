"use strict";

// ---- SVG helpers ----

function svgAttrs(el, attrs) {
  for (const k in attrs) el.setAttribute(k, attrs[k]);
  return el;
}

function svgEl(tag, attrs, text) {
  const el = document.createElementNS("http://www.w3.org/2000/svg", tag);
  if (attrs) svgAttrs(el, attrs);
  if (text != null) el.textContent = text;
  return el;
}

function fmtAxis(v, kind) {
  if (kind === "bytes") return fmtBytes(v);
  if (kind === "tokens") return fmtCompactTokens(v);
  if (kind === "ms") return v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${v}ms`;
  if (kind === "params") return v >= 1e9 ? `${(v / 1e9).toFixed(1)}B` : v >= 1e6 ? `${(v / 1e6).toFixed(0)}M` : `${v}`;
  if (kind === "gb") return `${v.toFixed(1)} GB`;
  if (kind === "paramsB") return `${v.toFixed(1)}B`;
  if (kind === "tps") return `${v.toFixed(0)} tok/s`;
  if (kind === "num") return `${Math.round(v)}`;
  return `${Math.round(v)}`;
}

function fmtCompactTokens(n) {
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(0)}k`;
  return `${Math.round(n)}`;
}

function shortModelLabel(name, max = 22) {
  const short = String(name || "").split("/").pop();
  return short.length > max ? `${short.slice(0, max - 1)}…` : short;
}

function niceDomain(values) {
  let min = Infinity, max = -Infinity;
  values.forEach((v) => {
    if (Number.isFinite(v) && v > 0) {
      if (v < min) min = v;
      if (v > max) max = v;
    }
  });
  if (!Number.isFinite(min) || !Number.isFinite(max)) return { min: 0, max: 1 };
  if (min === max) { max = min * 1.2 || 1; }
  return { min: 0, max: max * 1.15 };
}

// Fit smooth power regression: y = a * x^b (ln(y) = ln(a) + b * ln(x)) with linear fallback
function fitProgressionCurve(points) {
  const valid = points.filter(p => Number.isFinite(p.x) && p.x > 0 && Number.isFinite(p.y) && p.y > 0);
  if (valid.length < 2) return null;
  let sumLx = 0, sumLy = 0, sumLxLy = 0, sumLx2 = 0;
  const n = valid.length;
  for (const p of valid) {
    const lx = Math.log(p.x);
    const ly = Math.log(p.y);
    sumLx += lx;
    sumLy += ly;
    sumLxLy += lx * ly;
    sumLx2 += lx * lx;
  }
  const denom = n * sumLx2 - sumLx * sumLx;
  if (Math.abs(denom) > 1e-9) {
    const b = (n * sumLxLy - sumLx * sumLy) / denom;
    const lna = (sumLy - b * sumLx) / n;
    const a = Math.exp(lna);
    if (Number.isFinite(a) && Number.isFinite(b)) {
      return (x) => Math.max(0.01, a * Math.pow(x, b));
    }
  }
  // Linear regression fallback
  let sumX = 0, sumY = 0, sumXY = 0, sumX2 = 0;
  for (const p of valid) {
    sumX += p.x;
    sumY += p.y;
    sumXY += p.x * p.y;
    sumX2 += p.x * p.x;
  }
  const lDenom = n * sumX2 - sumX * sumX;
  if (Math.abs(lDenom) > 1e-9) {
    const m = (n * sumXY - sumX * sumY) / lDenom;
    const c = (sumY - m * sumX) / n;
    return (x) => Math.max(0.01, m * x + c);
  }
  return null;
}

// Calculate Pareto Frontier for (Params, Speed) where high speed at any size is optimal
function calculateParetoFrontier(points) {
  const sorted = [...points].sort((a, b) => a.x - b.x);
  const frontier = [];
  let maxSpeedSoFar = -Infinity;
  const rev = [...sorted].reverse();
  for (const p of rev) {
    if (p.y >= maxSpeedSoFar) {
      frontier.unshift(p);
      maxSpeedSoFar = p.y;
    }
  }
  return frontier;
}

// Floating Glassmorphism Tooltip
function showAnalyticsTooltip(e, p) {
  const tip = $("analytics-tooltip");
  if (!tip) return;
  const isGhost = p.ghost;
  const quantBadge = p.quant ? `<span class="badge badge-quant">${escapeHtml(p.quant)}</span>` : "";
  const moeBadge = p.isMOE ? `<span class="badge badge-moe">MoE</span>` : "";
  const loadedBadge = p.loaded
    ? `<span class="badge badge-loaded-memory">● ${escapeHtml(t("detail.dot_loaded") || "Loaded")}</span>`
    : "";
  const ghostBadge = isGhost
    ? `<span class="badge badge-muted">${escapeHtml(t("analytics.source_ghost"))}</span>`
    : `<span class="badge badge-installed">${escapeHtml(t("analytics.source_installed"))}</span>`;

  const tpsStr = p.tps > 0 ? `${p.tps.toFixed(1)} tok/s` : "—";
  const sizeStr = p.sizeBytes ? fmtBytes(p.sizeBytes) : "—";
  const effStr = p.efficiencyTokPerGB > 0 ? `${p.efficiencyTokPerGB.toFixed(1)} tok/s/GB` : "—";
  const loadStr = p.coldLoadMs > 0 ? `${(p.coldLoadMs / 1000).toFixed(1)}s (${Math.round(p.loadThroughputMBs)} MB/s)` : "—";
  const ctxStr = p.contextLength > 0 ? fmtCompactTokens(p.contextLength) : "—";

  tip.innerHTML = `
    <div class="analytics-tip-head">
      <div class="analytics-tip-title">${escapeHtml(p.name)}</div>
      <div class="analytics-tip-badges">${loadedBadge}${ghostBadge}${quantBadge}${moeBadge}</div>
    </div>
    <div class="analytics-tip-grid">
      <div class="analytics-tip-item"><span class="analytics-tip-lbl">⚡ ${escapeHtml(t("analytics.tps"))}:</span> <strong class="analytics-tip-val highlight-cyan">${tpsStr}</strong></div>
      <div class="analytics-tip-item"><span class="analytics-tip-lbl">🧠 ${escapeHtml(t("analytics.params"))}:</span> <strong class="analytics-tip-val">${escapeHtml(p.paramsLabel)}</strong></div>
      <div class="analytics-tip-item"><span class="analytics-tip-lbl">💾 ${escapeHtml(t("analytics.size"))}:</span> <strong class="analytics-tip-val">${sizeStr}</strong></div>
      <div class="analytics-tip-item"><span class="analytics-tip-lbl">🚀 ${escapeHtml(t("analytics.efficiency"))}:</span> <strong class="analytics-tip-val highlight-emerald">${effStr}</strong></div>
      <div class="analytics-tip-item"><span class="analytics-tip-lbl">⏱️ ${escapeHtml(t("analytics.coldload"))}:</span> <strong class="analytics-tip-val">${loadStr}</strong></div>
      <div class="analytics-tip-item"><span class="analytics-tip-lbl">📖 ${escapeHtml(t("analytics.context"))}:</span> <strong class="analytics-tip-val highlight-purple">${ctxStr}</strong></div>
    </div>
    ${p.architecture || p.family ? `<div class="analytics-tip-sub muted">Arch: ${escapeHtml(p.architecture || p.family)} · Quant: ${escapeHtml(p.quant || "—")}</div>` : ""}
  `;
  tip.hidden = false;
  positionAnalyticsTooltip(e);
}

function positionAnalyticsTooltip(e) {
  const tip = $("analytics-tooltip");
  if (!tip || tip.hidden) return;
  const tipRect = tip.getBoundingClientRect();
  const pad = 14;
  let x = e.clientX + pad;
  let y = e.clientY + pad;
  if (x + tipRect.width > window.innerWidth - pad) {
    x = e.clientX - tipRect.width - pad;
  }
  if (y + tipRect.height > window.innerHeight - pad) {
    y = e.clientY - tipRect.height - pad;
  }
  tip.style.left = `${Math.max(pad, x)}px`;
  tip.style.top = `${Math.max(pad, y)}px`;
}

function hideAnalyticsTooltip() {
  const tip = $("analytics-tooltip");
  if (tip) tip.hidden = true;
}

// KPI Summary Cards
function renderAnalyticsKPIs(data) {
  const container = $("analytics-kpis");
  if (!container) return;
  if (!data || !data.length) {
    container.innerHTML = "";
    return;
  }
  const points = data.map(analyticsPoint);
  const totalModels = points.length;
  const installedCount = points.filter((p) => !p.ghost).length;

  // Peak speed
  const withTps = points.filter((p) => p.tps > 0).sort((a, b) => b.tps - a.tps);
  const topTps = withTps[0];

  // Top efficiency
  const withEff = points.filter((p) => p.efficiencyTokPerGB > 0).sort((a, b) => b.efficiencyTokPerGB - a.efficiencyTokPerGB);
  const topEff = withEff[0];

  // Average speed
  const validTps = points.filter((p) => p.tps > 0);
  const avgTps = validTps.length ? validTps.reduce((acc, p) => acc + p.tps, 0) / validTps.length : 0;

  container.innerHTML = `
    <div class="analytics-kpi-card">
      <div class="analytics-kpi-icon">📊</div>
      <div class="analytics-kpi-body">
        <span class="analytics-kpi-label">${escapeHtml(t("analytics.kpi_models"))}</span>
        <strong class="analytics-kpi-val">${totalModels}</strong>
        <span class="analytics-kpi-sub muted">${installedCount} ${escapeHtml(t("analytics.source_installed").toLowerCase())}</span>
      </div>
    </div>
    <div class="analytics-kpi-card">
      <div class="analytics-kpi-icon highlight-cyan-icon">⚡</div>
      <div class="analytics-kpi-body">
        <span class="analytics-kpi-label">${escapeHtml(t("analytics.kpi_peak_tps"))}</span>
        <strong class="analytics-kpi-val highlight-cyan">${topTps ? topTps.tps.toFixed(1) + " <small>tok/s</small>" : "—"}</strong>
        <span class="analytics-kpi-sub muted" title="${topTps ? escapeHtml(topTps.name) : ""}">${topTps ? escapeHtml(shortModelLabel(topTps.name, 16)) : "—"}</span>
      </div>
    </div>
    <div class="analytics-kpi-card">
      <div class="analytics-kpi-icon highlight-emerald-icon">🚀</div>
      <div class="analytics-kpi-body">
        <span class="analytics-kpi-label">${escapeHtml(t("analytics.kpi_best_efficiency"))}</span>
        <strong class="analytics-kpi-val highlight-emerald">${topEff ? topEff.efficiencyTokPerGB.toFixed(1) + " <small>tok/s/GB</small>" : "—"}</strong>
        <span class="analytics-kpi-sub muted" title="${topEff ? escapeHtml(topEff.name) : ""}">${topEff ? escapeHtml(shortModelLabel(topEff.name, 16)) : "—"}</span>
      </div>
    </div>
    <div class="analytics-kpi-card">
      <div class="analytics-kpi-icon highlight-purple-icon">📈</div>
      <div class="analytics-kpi-body">
        <span class="analytics-kpi-label">${escapeHtml(t("analytics.kpi_avg_tps"))}</span>
        <strong class="analytics-kpi-val highlight-purple">${avgTps > 0 ? avgTps.toFixed(1) + " <small>tok/s</small>" : "—"}</strong>
        <span class="analytics-kpi-sub muted">${validTps.length} ${escapeHtml(t("analytics.meta_model").toLowerCase())}</span>
      </div>
    </div>
  `;
}

// Modern Scatter Plot with Trendline, Pareto Frontier and Radial Glow
function renderModernScatter(container, data, opts) {
  const W = 720, H = 380, PAD = { l: 96, r: 24, t: 26, b: 50 };
  const iw = W - PAD.l - PAD.r;
  const ih = H - PAD.t - PAD.b;

  const points = data
    .map(analyticsPoint)
    .map((p) => ({ p, x: opts.xValue(p), y: opts.yValue(p) }))
    .filter((pt) => Number.isFinite(pt.x) && pt.x > 0 && Number.isFinite(pt.y) && pt.y > 0)
    .sort((a, b) => a.x - b.x);

  if (!points.length) {
    container.innerHTML = `<div class="analytics-empty muted">${escapeHtml(t("analytics.no_data"))}</div>`;
    return;
  }

  const xDom = niceDomain(points.map((pt) => pt.x));
  const yDom = niceDomain(points.map((pt) => pt.y));
  const sx = (v) => PAD.l + ((v - xDom.min) / (xDom.max - xDom.min)) * iw;
  const sy = opts.invertY
    ? (v) => PAD.t + ((v - yDom.min) / (yDom.max - yDom.min)) * ih
    : (v) => PAD.t + ih - ((v - yDom.min) / (yDom.max - yDom.min)) * ih;

  const svg = svgEl("svg", { viewBox: `0 0 ${W} ${H}`, class: "analytics-svg modern-scatter", role: "img" });

  const defs = svgEl("defs");
  defs.innerHTML = `
    <linearGradient id="grad-trend-${opts.theme || "blue"}" x1="0%" y1="0%" x2="100%" y2="0%">
      <stop offset="0%" stop-color="#38bdf8" stop-opacity="0.85"/>
      <stop offset="100%" stop-color="#a855f7" stop-opacity="0.95"/>
    </linearGradient>
    <linearGradient id="grad-pareto" x1="0%" y1="0%" x2="100%" y2="0%">
      <stop offset="0%" stop-color="#fbbf24" stop-opacity="0.95"/>
      <stop offset="100%" stop-color="#f59e0b" stop-opacity="1"/>
    </linearGradient>
    <filter id="glow-loaded" x="-50%" y="-50%" width="200%" height="200%">
      <feGaussianBlur stdDeviation="3" result="coloredBlur"/>
      <feMerge>
        <feMergeNode in="coloredBlur"/>
        <feMergeNode in="coloredBlur"/>
        <feMergeNode in="SourceGraphic"/>
      </feMerge>
    </filter>
    <radialGradient id="grad-glow-loaded" cx="50%" cy="50%" r="50%">
      <stop offset="0%" stop-color="#10b981" stop-opacity="0.95"/>
      <stop offset="50%" stop-color="#34d399" stop-opacity="0.45"/>
      <stop offset="100%" stop-color="#10b981" stop-opacity="0"/>
    </radialGradient>
  `;
  svg.appendChild(defs);

  // Background Grid Lines + Y Labels
  const yTicks = 5;
  for (let i = 0; i <= yTicks; i++) {
    const v = yDom.min + ((yDom.max - yDom.min) * i) / yTicks;
    const y = sy(v);
    svg.appendChild(svgEl("line", { x1: PAD.l, y1: y, x2: W - PAD.r, y2: y, class: "analytics-grid-line" }));
    svg.appendChild(svgEl("text", { x: PAD.l - 12, y: y + 4, "text-anchor": "end", class: "analytics-tick" }, fmtAxis(v, opts.yKind)));
  }

  // X Labels + Grid Lines
  const xTicks = 6;
  for (let i = 0; i <= xTicks; i++) {
    const v = xDom.min + ((xDom.max - xDom.min) * i) / xTicks;
    const x = sx(v);
    svg.appendChild(svgEl("line", { x1: x, y1: PAD.t, x2: x, y2: H - PAD.b, class: "analytics-grid-line-v" }));
    svg.appendChild(svgEl("text", { x, y: H - PAD.b + 20, "text-anchor": "middle", class: "analytics-tick" }, fmtAxis(v, opts.xKind)));
  }

  // Axes
  const yCenter = (PAD.t + H - PAD.b) / 2;
  svg.appendChild(svgEl("line", { x1: PAD.l, y1: PAD.t, x2: PAD.l, y2: H - PAD.b, class: "analytics-axis" }));
  svg.appendChild(svgEl("line", { x1: PAD.l, y1: H - PAD.b, x2: W - PAD.r, y2: H - PAD.b, class: "analytics-axis" }));
  svg.appendChild(svgEl("text", { x: PAD.l + iw / 2, y: H - 8, "text-anchor": "middle", class: "analytics-axislabel" }, opts.xLabel));
  svg.appendChild(svgEl("text", { x: 16, y: yCenter, "text-anchor": "middle", class: "analytics-axislabel analytics-axislabel--y", transform: `rotate(-90 16 ${yCenter})` }, opts.yLabel));

  // Render Progression / Trendline Curve
  if (opts.showTrendline && points.length >= 2) {
    const curveFn = fitProgressionCurve(points.map((pt) => ({ x: pt.x, y: pt.y })));
    if (curveFn) {
      const segments = 40;
      const pathPoints = [];
      const minX = Math.max(xDom.min, points[0].x * 0.9);
      const maxX = Math.min(xDom.max, points[points.length - 1].x * 1.1);
      for (let s = 0; s <= segments; s++) {
        const vx = minX + ((maxX - minX) * s) / segments;
        const vy = curveFn(vx);
        if (Number.isFinite(vy) && vy >= yDom.min && vy <= yDom.max * 1.25) {
          pathPoints.push(`${pathPoints.length === 0 ? "M" : "L"} ${sx(vx).toFixed(1)} ${sy(vy).toFixed(1)}`);
        }
      }
      if (pathPoints.length > 2) {
        svg.appendChild(svgEl("path", {
          d: pathPoints.join(" "),
          class: "analytics-trendline-path",
          stroke: `url(#grad-trend-${opts.theme || "blue"})`,
        }));
      }
    }
  }

  // Render Pareto Frontier Line
  if (opts.showPareto && points.length >= 2) {
    const frontier = calculateParetoFrontier(points);
    if (frontier.length >= 2) {
      const paretoPath = frontier.map((pt, idx) => `${idx === 0 ? "M" : "L"} ${sx(pt.x).toFixed(1)} ${sy(pt.y).toFixed(1)}`).join(" ");
      svg.appendChild(svgEl("path", {
        d: paretoPath,
        class: "analytics-pareto-path",
        stroke: "url(#grad-pareto)",
      }));
      frontier.forEach((pt) => {
        svg.appendChild(svgEl("circle", {
          cx: sx(pt.x), cy: sy(pt.y), r: 7.5,
          class: "analytics-pareto-marker",
        }));
      });
    }
  }

  // Sort so loaded models are rendered last (drawn on top)
  const sortedPoints = [...points].sort((a, b) => {
    if (!!a.p.loaded === !!b.p.loaded) return a.x - b.x;
    return a.p.loaded ? 1 : -1;
  });

  // Calculate label configs (prioritizing loaded models so their labels are never skipped)
  const placedLabels = [];
  const priorityPoints = [...sortedPoints].sort((a, b) => (b.p.loaded ? 1 : 0) - (a.p.loaded ? 1 : 0));
  priorityPoints.forEach((pt) => {
    const cx = sx(pt.x), cy = sy(pt.y);
    const isLoaded = !!pt.p.loaded;
    const minDistanceX = isLoaded ? 80 : 120;
    const minDistanceY = isLoaded ? 14 : 20;
    const tooClose = placedLabels.some((q) => Math.abs(q.x - cx) < minDistanceX && Math.abs(q.y - cy) < minDistanceY);
    if (!tooClose || isLoaded) {
      const label = shortModelLabel(pt.p.name, 18);
      const onRightEdge = cx > W - 130;
      pt.labelConfig = {
        x: onRightEdge ? cx - 12 : cx + 12,
        y: cy - 6,
        anchor: onRightEdge ? "end" : "start",
        label: isLoaded ? `● ${label}` : label,
        isLoaded,
      };
      placedLabels.push({ x: cx, y: cy });
    }
  });

  // Render Interactive Dots
  sortedPoints.forEach((pt) => {
    const cx = sx(pt.x), cy = sy(pt.y);
    const isLoaded = !!pt.p.loaded;
    const color = isLoaded ? "#10b981" : modelFamilyColor(pt.p.family, pt.p.ghost);
    const g = svgEl("g", { class: `analytics-pt ${pt.p.ghost ? "is-ghost" : ""} ${isLoaded ? "is-loaded" : ""}` });

    if (isLoaded) {
      // Radiant glowing emerald styling for loaded models
      g.appendChild(svgEl("circle", { cx, cy, r: 16, class: "analytics-dot-glow-loaded" }));
      g.appendChild(svgEl("circle", { cx, cy, r: 9.5, class: "analytics-dot-halo-loaded" }));
      g.appendChild(svgEl("circle", { cx, cy, r: 6.5, class: "analytics-dot-core-loaded" }));
    } else {
      g.appendChild(svgEl("circle", { cx, cy, r: 8, fill: color, class: "analytics-dot-halo" }));
      g.appendChild(svgEl("circle", { cx, cy, r: 5.5, fill: color, class: "analytics-dot" }));
    }

    if (pt.labelConfig) {
      g.appendChild(svgEl("text", {
        x: pt.labelConfig.x,
        y: pt.labelConfig.y,
        "text-anchor": pt.labelConfig.anchor,
        class: `analytics-ptlabel ${isLoaded ? "is-loaded" : ""}`,
      }, pt.labelConfig.label));
    }

    // Attach listeners with touch / mobile support
    g.addEventListener("pointerenter", (e) => showAnalyticsTooltip(e, pt.p));
    g.addEventListener("pointermove", (e) => positionAnalyticsTooltip(e));
    g.addEventListener("pointerleave", () => hideAnalyticsTooltip());
    g.addEventListener("click", (e) => {
      e.stopPropagation();
      showAnalyticsTooltip(e, pt.p);
    });

    svg.appendChild(g);
  });

  container.innerHTML = "";
  container.appendChild(svg);
}

// Modern High-Fidelity Bar Chart (Top 10 with collapsible remainder)
function renderModernBars(container, data, opts) {
  const items = data
    .map(analyticsPoint)
    .filter((p) => {
      const v = opts.value(p);
      return Number.isFinite(v) && v > 0;
    })
    .sort((a, b) => opts.value(b) - opts.value(a));

  if (!items.length) {
    container.innerHTML = `<div class="analytics-empty muted">${escapeHtml(t("analytics.no_data"))}</div>`;
    return;
  }

  const maxVal = Math.max(...items.map((p) => opts.value(p))) || 1;
  const gradientClass = opts.gradientClass || "grad-cyan";

  const renderRow = (p, i) => {
    const v = opts.value(p);
    const pct = Math.min(100, Math.max(3, (v / maxVal) * 100));
    const valText = opts.formatVal ? opts.formatVal(v, p) : `${v.toFixed(1)} ${opts.unit || ""}`;
    const subText = opts.formatSub ? opts.formatSub(p) : "";
    const quantBadge = p.quant ? `<span class="badge badge-quant-pill">${escapeHtml(p.quant)}</span>` : "";
    const ghostClass = p.ghost ? "is-ghost" : "";
    const loadedBadge = p.loaded ? `<span class="badge badge-loaded-memory" title="${escapeHtml(t("detail.dot_loaded") || "Loaded in memory")}">●</span>` : "";

    return `
      <div class="analytics-bar-row ${ghostClass} ${p.loaded ? "is-loaded" : ""}" data-model-idx="${i}">
        <div class="analytics-bar-head">
          <div class="analytics-bar-title-group">
            <span class="analytics-bar-rank">#${i + 1}</span>
            <span class="analytics-bar-name" title="${escapeHtml(p.name)}">${escapeHtml(shortModelLabel(p.name, 28))}</span>
            ${quantBadge}
            ${loadedBadge}
          </div>
          <div class="analytics-bar-vals">
            <strong class="analytics-bar-primary">${escapeHtml(valText)}</strong>
            ${subText ? `<span class="analytics-bar-secondary muted">${escapeHtml(subText)}</span>` : ""}
          </div>
        </div>
        <div class="analytics-bar-track">
          <div class="analytics-bar-fill ${gradientClass}" style="width: ${pct}%"></div>
        </div>
      </div>
    `;
  };

  const TOP_N = 10;
  const topItems = items.slice(0, TOP_N);
  const restItems = items.slice(TOP_N);

  const topRowsHtml = topItems.map((p, i) => renderRow(p, i)).join("");

  let collapsibleHtml = "";
  if (restItems.length > 0) {
    const restRowsHtml = restItems.map((p, i) => renderRow(p, TOP_N + i)).join("");
    const moreLabel = t("analytics.show_more", { count: restItems.length }) || `Show remaining ${restItems.length} models`;
    const lessLabel = t("analytics.show_less") || "Show top 10 only";
    collapsibleHtml = `
      <details class="analytics-bars-details">
        <summary class="analytics-bars-summary" data-more="${escapeHtml(moreLabel)}" data-less="${escapeHtml(lessLabel)}">
          <span class="analytics-bars-summary-icon">▼</span>
          <span class="analytics-bars-summary-text">${escapeHtml(moreLabel)}</span>
        </summary>
        <div class="analytics-bars-rest">
          ${restRowsHtml}
        </div>
      </details>
    `;
  }

  container.innerHTML = `<div class="analytics-bars-container">${topRowsHtml}${collapsibleHtml}</div>`;

  const details = container.querySelector(".analytics-bars-details");
  if (details) {
    const summary = details.querySelector(".analytics-bars-summary");
    const textEl = summary?.querySelector(".analytics-bars-summary-text");
    details.addEventListener("toggle", () => {
      if (textEl && summary) {
        textEl.textContent = details.open ? summary.dataset.less : summary.dataset.more;
      }
    });
  }

  const rows = container.querySelectorAll(".analytics-bar-row");
  rows.forEach((row) => {
    const idx = Number(row.dataset.modelIdx);
    const p = items[idx];
    if (!p) return;
    row.addEventListener("pointerenter", (e) => showAnalyticsTooltip(e, p));
    row.addEventListener("pointermove", (e) => positionAnalyticsTooltip(e));
    row.addEventListener("pointerleave", () => hideAnalyticsTooltip());
    row.addEventListener("click", (e) => {
      e.stopPropagation();
      showAnalyticsTooltip(e, p);
    });
  });
}

// Global click to dismiss tooltip on touch/mobile
if (typeof document !== "undefined") {
  document.addEventListener("click", (e) => {
    if (!e.target.closest(".analytics-pt") && !e.target.closest(".analytics-bar-row")) {
      hideAnalyticsTooltip();
    }
  });
}

function renderTpsVsParams(all) {
  const el = $("analytics-chart-tps");
  if (!el) return;
  renderModernScatter(el, all, {
    xLabel: t("analytics.tps_x"), yLabel: t("analytics.tps_y"),
    xKind: "paramsB", yKind: "tps",
    xValue: (p) => p.paramsB,
    yValue: (p) => p.tps,
    showTrendline: true,
    showPareto: true,
    theme: "speed",
  });
}

function renderSizeVsParams(all) {
  const el = $("analytics-chart-size");
  if (!el) return;
  renderModernScatter(el, all, {
    xLabel: t("analytics.size_x"), yLabel: t("analytics.size_y"),
    xKind: "paramsB", yKind: "gb",
    xValue: (p) => p.paramsB,
    yValue: (p) => p.sizeGB,
    invertY: true,
    showTrendline: true,
    showPareto: false,
    theme: "size",
  });
}

function renderEfficiency(all) {
  const el = $("analytics-chart-efficiency");
  if (!el) return;
  renderModernBars(el, all, {
    label: t("analytics.efficiency"),
    value: (p) => p.efficiencyTokPerGB,
    formatVal: (v) => `${v.toFixed(1)} tok/s/GB`,
    formatSub: (p) => p.tps > 0 && p.sizeBytes > 0 ? `${p.tps.toFixed(1)} tok/s · ${fmtBytes(p.sizeBytes)}` : "",
    gradientClass: "grad-emerald",
  });
}

function renderColdLoad(all) {
  const el = $("analytics-chart-coldload");
  if (!el) return;
  renderModernBars(el, all, {
    label: t("analytics.coldload"),
    value: (p) => p.loadThroughputMBs,
    formatVal: (v) => `${Math.round(v)} MB/s`,
    formatSub: (p) => p.coldLoadMs > 0 ? `${(p.coldLoadMs / 1000).toFixed(1)}s load · ${fmtBytes(p.sizeBytes)}` : "",
    gradientClass: "grad-cyan",
  });
}

let analyticsMetaSearch = "";

function renderMetaTable(all) {
  const q = analyticsMetaSearch.toLowerCase();
  const rows = all.filter((m) => {
    if (!q) return true;
    const p = analyticsPoint(m);
    return p.name.toLowerCase().includes(q) || p.family.toLowerCase().includes(q) || p.architecture.toLowerCase().includes(q);
  });
  rows.sort((a, b) => analyticsPoint(b).parameterCount - analyticsPoint(a).parameterCount);
  renderMetaTableBody($("analytics-meta-tbody-installed"), rows.filter((m) => !analyticsPoint(m).ghost), false, q);
  renderMetaTableBody($("analytics-meta-tbody-uninstalled"), rows.filter((m) => analyticsPoint(m).ghost), true, q);
}

function renderMetaTableBody(tbody, rows, uninstalled, query) {
  if (!tbody) return;
  if (!rows.length) {
    tbody.innerHTML = `<tr class="empty"><td colspan="${uninstalled ? 9 : 8}" class="muted">${escapeHtml(query ? t("analytics.no_data") : t("analytics.no_models"))}</td></tr>`;
    return;
  }
  tbody.innerHTML = rows.map((m) => {
    const p = analyticsPoint(m);
    const moeBadge = p.isMOE ? `<span class="badge badge-moe">MoE</span>` : "";
    const remove = uninstalled
      ? `<button type="button" class="ghost analytics-remove-ghost" data-name="${escapeHtml(p.name)}" title="${escapeHtml(t("analytics.remove_ghost"))}">🗑</button>`
      : "";
    return `<tr class="analytics-meta-row${uninstalled ? " ghost" : ""}" data-name="${escapeHtml(p.name)}">
      <td class="analytics-meta-name" title="${escapeHtml(t("analytics.usage_view_details"))}">${escapeHtml(p.name)}</td>
      <td>${escapeHtml(p.paramsLabel)}</td>
      <td class="mono">${p.parameterCount ? formatExactParams(p.parameterCount) : "—"}</td>
      <td>${escapeHtml(p.architecture || "—")} ${moeBadge}</td>
      <td class="mono">${p.fileType ? p.fileType : "—"}</td>
      <td>${escapeHtml(p.quant || "—")}</td>
      <td>${escapeHtml(p.family || "—")}</td>
      <td class="mono">${p.sizeBytes ? fmtBytes(p.sizeBytes) : "—"}</td>
      ${uninstalled ? `<td>${remove}</td>` : ""}
    </tr>`;
  }).join("");
}

function formatExactParams(n) {
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  return `${n}`;
}

let modelUsageModalBound = false;
function bindAnalyticsMetaSearch() {
  const input = $("analytics-meta-search");
  if (!input) return;
  input.addEventListener("input", () => {
    analyticsMetaSearch = input.value.trim();
    renderMetaTable(analyticsAllData.filter((m) => analyticsFilterMatches(analyticsPoint(m))));
  });
  document.querySelectorAll("#analytics-meta-tbody-installed, #analytics-meta-tbody-uninstalled").forEach((tbody) => {
    tbody.addEventListener("click", (e) => {
      const btn = e.target.closest(".analytics-remove-ghost");
      if (btn) {
        e.stopPropagation();
        const name = btn.getAttribute("data-name");
        if (name) void removeGhost(name);
        return;
      }
      const row = e.target.closest(".analytics-meta-row");
      if (row) {
        const name = row.getAttribute("data-name");
        if (name) void openModelUsageModal(name);
      }
    });
  });

  if (!modelUsageModalBound) {
    modelUsageModalBound = true;
    $("model-usage-modal-close")?.addEventListener("click", () => {
      $("model-usage-modal").hidden = true;
    });
    $("model-usage-modal-done")?.addEventListener("click", () => {
      $("model-usage-modal").hidden = true;
    });
    $("model-usage-modal")?.addEventListener("click", (e) => {
      if (e.target === $("model-usage-modal")) {
        $("model-usage-modal").hidden = true;
      }
    });
  }
}

async function openModelUsageModal(name) {
  if (!name) return;
  const modal = $("model-usage-modal");
  const body = $("model-usage-modal-body");
  if (!modal || !body) return;

  modal.hidden = false;
  body.innerHTML = `<div class="muted" style="padding: 24px 0; text-align: center;">${escapeHtml(t("state.loading"))}</div>`;

  const m = (analyticsAllData || []).find((x) => x.name === name) || {};
  const installedModel = (typeof models !== "undefined" && Array.isArray(models))
    ? models.find((x) => x.name === name)
    : null;

  const chatBtn = $("model-usage-modal-chat-btn");
  const detailBtn = $("model-usage-modal-detail-btn");
  if (chatBtn) {
    if (installedModel && !installedModel.archived) {
      chatBtn.hidden = false;
      chatBtn.onclick = () => {
        modal.hidden = true;
        if (typeof openChat === "function") openChat(name);
      };
    } else {
      chatBtn.hidden = true;
    }
  }
  if (detailBtn) {
    if (installedModel) {
      detailBtn.hidden = false;
      detailBtn.onclick = () => {
        modal.hidden = true;
        if (typeof showModelsView === "function") showModelsView();
        if (typeof openDetail === "function") openDetail(name);
      };
    } else {
      detailBtn.hidden = true;
    }
  }

  try {
    const [usageRes, detailRes] = await Promise.allSettled([
      api("/api/usage/" + encodeURIComponent(name)),
      api("/api/models/" + encodeURIComponent(name)),
    ]);
    const usagePayload = usageRes.status === "fulfilled" ? usageRes.value : null;
    const detailData = detailRes.status === "fulfilled" ? detailRes.value : null;

    renderModelUsageModalContent(
      m,
      usagePayload?.usage || m,
      usagePayload?.uninstall,
      installedModel,
      detailData
    );
  } catch (err) {
    renderModelUsageModalContent(m, m, null, installedModel, null);
  }
}

function renderUsageCodeBlock(title, content) {
  const val = String(content || "");
  if (!val.trim()) return "";
  return `
    <div class="model-usage-section" data-copy-target>
      <div class="model-usage-json-header">
        <h4 class="model-usage-section-title">${escapeHtml(title)}</h4>
        <button type="button" class="ghost model-usage-copy-section-btn" style="padding: 3px 8px; font-size: 11px;">📋 ${escapeHtml(t("detail.copy"))}</button>
      </div>
      <pre class="model-usage-json-box" style="color: var(--text);">${escapeHtml(val)}</pre>
    </div>
  `;
}

function renderModelUsageModalContent(m, u, uninst, installedModel, detail) {
  const body = $("model-usage-modal-body");
  if (!body) return;

  const rawRecord = Object.assign({}, m, u || {});
  const p = analyticsPoint(rawRecord);
  const name = p.name || m?.name || u?.name || detail?.name || "—";
  const isGhost = p.ghost;

  const statusBadge = isGhost
    ? `<span class="badge badge-muted">${escapeHtml(t("analytics.usage_status_ghost"))}</span>`
    : `<span class="badge badge-good">${escapeHtml(t("analytics.usage_status_installed"))}</span>`;
  const loadedBadge = installedModel?.loaded
    ? `<span class="badge badge-good">${escapeHtml(t("analytics.usage_status_loaded"))} (${fmtBytes(installedModel.size_vram)})</span>`
    : "";
  const archivedBadge = installedModel?.archived
    ? `<span class="badge badge-warn">${escapeHtml(t("analytics.usage_status_archived"))}</span>`
    : "";
  const moeBadge = p.isMOE ? `<span class="badge badge-moe">MoE</span>` : "";

  // Capabilities
  const rawCaps = (detail?.capabilities && detail.capabilities.length)
    ? detail.capabilities
    : (m?.capabilities || []);
  const capsPills = typeof renderCapabilityPills === "function" ? renderCapabilityPills(rawCaps) : "";

  // Site link
  const siteUrl = typeof modelHomepageUrl === "function" ? modelHomepageUrl(name) : "";
  const hostLabel = siteUrl ? (siteUrl.startsWith("https://huggingface.co") ? "Hugging Face" : "Ollama") : "";

  // KPI calculations
  const tpsVal = p.tps > 0 ? `${p.tps.toFixed(1)} tok/s` : "—";
  const tpsDate = (u?.record_tokens_per_sec_at || m?.record_tokens_per_sec_at)
    ? fmtDateTimeFull(u?.record_tokens_per_sec_at || m?.record_tokens_per_sec_at)
    : (p.tps > 0 ? "Benchmark recorded" : "—");

  const coldLoadVal = p.coldLoadMs > 0 ? `${(p.coldLoadMs / 1000).toFixed(2)}s` : "—";
  const coldLoadSub = p.coldLoadMs > 0
    ? `${p.coldLoadMs.toLocaleString()} ms · ${(u?.min_cold_load_at || m?.min_cold_load_at) ? fmtDate(u?.min_cold_load_at || m?.min_cold_load_at) : ""}`
    : "—";

  const throughputVal = p.loadThroughputMBs > 0 ? `${p.loadThroughputMBs.toFixed(1)} MB/s` : "—";
  const throughputSub = (p.coldLoadMs > 0 && p.sizeBytes > 0)
    ? `${fmtBytes(p.sizeBytes)} / ${(p.coldLoadMs / 1000).toFixed(1)}s`
    : "—";

  const effVal = p.efficiencyTokPerGB > 0 ? `${p.efficiencyTokPerGB.toFixed(1)} tok/s/GB` : "—";
  const effSub = p.sizeGB > 0 ? `${p.sizeGB.toFixed(1)} GB model size` : "—";

  const lastUsedVal = (u?.last_used_at || m?.last_used_at)
    ? fmtDateTimeFull(u?.last_used_at || m?.last_used_at)
    : "—";
  const lastUsedSub = (u?.last_used_at || m?.last_used_at)
    ? fmtRelativeTime(u?.last_used_at || m?.last_used_at)
    : "—";

  const totalCallsVal = (p.totalCalls || 0).toLocaleString();
  const totalTokensVal = (p.totalTokens || 0).toLocaleString();
  const sizeVal = p.sizeBytes > 0 ? fmtBytes(p.sizeBytes) : "—";
  const sizeSub = p.sizeBytes > 0 ? `${p.sizeBytes.toLocaleString()} bytes` : "—";

  // Families
  const families = (detail?.details?.families && detail.details.families.length)
    ? detail.details.families.join(", ")
    : (m?.families && m.families.length ? m.families.join(", ") : p.family);

  const format = detail?.details?.format || m?.format || "gguf";
  const parentModel = detail?.details?.parent_model || "";
  const digest = m?.digest || detail?.digest || "";
  const artifactCount = detail?.artifact_count || 0;
  const artifactBytes = detail?.artifact_bytes || 0;
  const license = detail?.license || "";
  const system = detail?.system || "";
  const parameters = detail?.parameters || "";
  const template = detail?.template || "";
  const modelfile = detail?.modelfile || "";
  const modelInfo = detail?.model_info || null;

  // Technical specifications table rows
  const specRows = [
    [t("detail.family"), families || p.family || "—"],
    [t("detail.architecture"), p.architecture || detail?.architecture || "—"],
    [t("detail.params"), p.paramsLabel || detail?.details?.parameter_size || (p.parameterCount ? `${(p.parameterCount / 1e9).toFixed(2)}B` : "—")],
    [t("analytics.usage_param_count"), p.parameterCount ? `${p.parameterCount.toLocaleString()} (${formatExactParams(p.parameterCount)})` : "—"],
    [t("analytics.usage_size_label"), p.sizeLabel || "—"],
    [t("detail.quant"), p.quant || detail?.details?.quantization_level || "—"],
    [t("detail.format"), format || "—"],
    [t("analytics.usage_file_type"), p.fileType ? String(p.fileType) : "—"],
    [t("detail.context"), (p.contextLength || detail?.context_length) ? `${fmtCtx(p.contextLength || detail?.context_length)} (${(p.contextLength || detail?.context_length).toLocaleString()} tokens)` : "—"],
    [t("analytics.usage_is_moe"), p.isMOE ? "Yes (Mixture of Experts)" : "No"],
    [t("detail.size"), p.sizeBytes > 0 ? `${fmtBytes(p.sizeBytes)} (${p.sizeBytes.toLocaleString()} bytes)` : "—"],
    [t("analytics.usage_status"), `${isGhost ? t("analytics.usage_status_ghost") : t("analytics.usage_status_installed")}${installedModel?.loaded ? ` (${t("analytics.usage_status_loaded")})` : ""}${installedModel?.archived ? ` (${t("analytics.usage_status_archived")})` : ""}`],
  ];

  if (parentModel) {
    specRows.push(["Parent Model", parentModel]);
  }
  if (digest) {
    specRows.push([t("detail.digest"), `<span class="mono" title="${escapeHtml(digest)}">${escapeHtml(digest.slice(0, 16))}…</span>`]);
  }
  if (artifactCount > 0) {
    const artSize = artifactBytes ? ` · ${fmtBytes(artifactBytes)}` : "";
    specRows.push([t("detail.artifacts"), `${artifactCount}${artSize}`]);
  }
  if (siteUrl) {
    specRows.push([t("detail.site"), `<a class="detail-site-link" href="${siteUrl}" target="_blank" rel="noopener noreferrer">${escapeHtml(hostLabel)} ↗</a>`]);
  }
  if (license) {
    specRows.push(["License", license]);
  }
  if (m?.modified_at || u?.modified_at || detail?.modified_at) {
    specRows.push([t("detail.modified"), fmtDateTimeFull(m?.modified_at || u?.modified_at || detail?.modified_at)]);
  }
  if (uninst && typeof uninst === "object") {
    if (uninst.reason) specRows.push([t("analytics.usage_uninstall_reason"), uninst.reason]);
    if (uninst.last_uninstall_at) specRows.push([t("analytics.usage_uninstall_at"), fmtDateTimeFull(uninst.last_uninstall_at)]);
  }

  // Model Info section (GGUF tensors and architectural hyperparameters)
  let modelInfoBlock = "";
  if (modelInfo && typeof modelInfo === "object" && Object.keys(modelInfo).length) {
    const sortedKeys = Object.keys(modelInfo).sort();
    const infoRows = sortedKeys.map((k) => {
      const val = typeof modelInfo[k] === "object" ? JSON.stringify(modelInfo[k]) : String(modelInfo[k]);
      return `<tr><td class="mono" style="font-size: 11px;">${escapeHtml(k)}</td><td class="mono" style="font-size: 11px; word-break: break-all;">${escapeHtml(val)}</td></tr>`;
    }).join("");
    modelInfoBlock = `
      <div class="model-usage-section">
        <h4 class="model-usage-section-title">GGUF Model Info & Tensors (${sortedKeys.length})</h4>
        <div style="max-height: 240px; overflow-y: auto; border: 1px solid var(--border); border-radius: var(--radius);">
          <table class="model-usage-specs-table" style="margin: 0; border: none;">
            <tbody>${infoRows}</tbody>
          </table>
        </div>
      </div>
    `;
  }

  const capsSection = capsPills ? `
    <div class="model-usage-section">
      <h4 class="model-usage-section-title">${escapeHtml(t("detail.capabilities"))}</h4>
      <div class="cap-list">${capsPills}</div>
    </div>
  ` : "";

  const systemBlock = system ? renderUsageCodeBlock(t("detail.system"), system) : "";
  const paramsBlock = parameters ? renderUsageCodeBlock(t("detail.parameters_section"), parameters) : "";
  const tmplBlock = template ? renderUsageCodeBlock(t("detail.template"), template) : "";
  const modelfileBlock = modelfile ? renderUsageCodeBlock("Modelfile", modelfile) : "";

  body.innerHTML = `
    <div class="model-usage-hero">
      <div class="model-usage-hero-top">
        <div class="model-usage-name-wrap">
          <span class="model-usage-name mono">${escapeHtml(name)}</span>
          <button type="button" class="ghost model-usage-copy-btn" id="model-usage-copy-name-btn" title="Copy name">📋</button>
        </div>
        <div class="model-usage-badges">
          ${statusBadge}
          ${loadedBadge}
          ${archivedBadge}
          ${moeBadge}
        </div>
      </div>
    </div>

    <div class="model-usage-section">
      <h4 class="model-usage-section-title">${escapeHtml(t("analytics.usage_metrics_title"))}</h4>
      <div class="model-usage-grid">
        <div class="model-usage-metric-card">
          <div class="model-usage-metric-label"><span class="model-usage-metric-card-icon">⚡</span>${escapeHtml(t("analytics.usage_stat_speed"))}</div>
          <div class="model-usage-metric-val mono">${escapeHtml(tpsVal)}</div>
          <div class="model-usage-metric-sub" title="${escapeHtml(tpsDate)}">${escapeHtml(tpsDate)}</div>
        </div>

        <div class="model-usage-metric-card">
          <div class="model-usage-metric-label"><span class="model-usage-metric-card-icon">⏱️</span>${escapeHtml(t("analytics.usage_stat_coldload"))}</div>
          <div class="model-usage-metric-val mono">${escapeHtml(coldLoadVal)}</div>
          <div class="model-usage-metric-sub">${escapeHtml(coldLoadSub)}</div>
        </div>

        <div class="model-usage-metric-card">
          <div class="model-usage-metric-label"><span class="model-usage-metric-card-icon">🚀</span>${escapeHtml(t("analytics.usage_stat_throughput"))}</div>
          <div class="model-usage-metric-val mono">${escapeHtml(throughputVal)}</div>
          <div class="model-usage-metric-sub">${escapeHtml(throughputSub)}</div>
        </div>

        <div class="model-usage-metric-card">
          <div class="model-usage-metric-label"><span class="model-usage-metric-card-icon">📈</span>${escapeHtml(t("analytics.usage_stat_efficiency"))}</div>
          <div class="model-usage-metric-val mono">${escapeHtml(effVal)}</div>
          <div class="model-usage-metric-sub">${escapeHtml(effSub)}</div>
        </div>

        <div class="model-usage-metric-card">
          <div class="model-usage-metric-label"><span class="model-usage-metric-card-icon">💬</span>${escapeHtml(t("analytics.usage_stat_calls"))}</div>
          <div class="model-usage-metric-val mono">${escapeHtml(totalCallsVal)}</div>
          <div class="model-usage-metric-sub">${escapeHtml(t("downloads.reenqueue_stat_total_calls"))}</div>
        </div>

        <div class="model-usage-metric-card">
          <div class="model-usage-metric-label"><span class="model-usage-metric-card-icon">🔤</span>${escapeHtml(t("analytics.usage_stat_tokens"))}</div>
          <div class="model-usage-metric-val mono">${escapeHtml(totalTokensVal)}</div>
          <div class="model-usage-metric-sub">Lifetime generated tokens</div>
        </div>

        <div class="model-usage-metric-card">
          <div class="model-usage-metric-label"><span class="model-usage-metric-card-icon">🕒</span>${escapeHtml(t("analytics.usage_stat_last_used"))}</div>
          <div class="model-usage-metric-val" style="font-size: 13px;">${escapeHtml(lastUsedVal)}</div>
          <div class="model-usage-metric-sub">${escapeHtml(lastUsedSub)}</div>
        </div>

        <div class="model-usage-metric-card">
          <div class="model-usage-metric-label"><span class="model-usage-metric-card-icon">📦</span>${escapeHtml(t("analytics.usage_stat_size"))}</div>
          <div class="model-usage-metric-val mono">${escapeHtml(sizeVal)}</div>
          <div class="model-usage-metric-sub">${escapeHtml(sizeSub)}</div>
        </div>
      </div>
    </div>

    ${capsSection}

    <div class="model-usage-section">
      <h4 class="model-usage-section-title">${escapeHtml(t("analytics.usage_specs_title"))}</h4>
      <table class="model-usage-specs-table">
        <tbody>
          ${specRows.map(([k, v]) => `<tr><td>${escapeHtml(k)}</td><td>${v.startsWith("<") ? v : `<span class="mono">${escapeHtml(v)}</span>`}</td></tr>`).join("")}
        </tbody>
      </table>
    </div>

    ${systemBlock}
    ${paramsBlock}
    ${tmplBlock}
    ${modelfileBlock}
    ${modelInfoBlock}
  `;

  $("model-usage-copy-name-btn")?.addEventListener("click", () => {
    navigator.clipboard?.writeText(name);
    toast(name, "info");
  });

  body.querySelectorAll(".model-usage-copy-section-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const pre = btn.closest("[data-copy-target]")?.querySelector("pre");
      if (pre) {
        navigator.clipboard?.writeText(pre.textContent);
        toast(t("chat.copied") || "Copied", "success");
      }
    });
  });
}

async function removeGhost(name) {
  const res = await askConfirm({
    title: t("analytics.remove_ghost_title"),
    text: t("analytics.remove_ghost_confirm", { name: "{__NAME__}" }).replace("{__NAME__}", name),
    okText: t("analytics.remove_ghost"),
    okClass: "danger",
    mono: name,
  });
  if (!res || !res.ok) return;
  try {
    const res = await api("/api/models/ghost/remove", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    toast(t("analytics.remove_ghost_done", { name }), "success");
    analyticsAllData = analyticsAllData.filter((m) => m.name !== name);
    renderAnalyticsFiltered();
    void refreshModels();
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
}

function showTestsView(preserveGroup = false) {
  hideAllMainViews();
  stopSpeechPlayback();
  currentView = "tests";
  $("tests-btn")?.classList.add("active");
  $("tests-view").hidden = false;
  setupGroupModals();
  const targetPath = (preserveGroup && selectedGroupId) ? "/tests/group/" + encodeURIComponent(selectedGroupId) : "/tests";
  if (window.location.pathname !== targetPath && (!preserveGroup || !window.location.pathname.startsWith("/tests/group/"))) {
    history.pushState(null, "", targetPath);
  }
  void refreshTests();
}

let currentEditorCases = [];

function newEditorCase(name) {
  return {
    name: name || "",
    prompt: "",
    type: "contains",
    expected: "",
    pattern: "",
    system_prompt: "",
    temperature: "",
    top_p: "",
    max_tokens: "",
    steps: [],
  };
}

function newEditorStep(name) {
  return { name: name || "", prompt: "", type: "contains", expected: "", pattern: "", system_prompt: "", temperature: "" };
}

function evalOptionsHtml(selectedType) {
  return `
    <option value="contains" ${selectedType === "contains" ? "selected" : ""}>${t("tests.eval_contains")}</option>
    <option value="exact_match" ${selectedType === "exact_match" ? "selected" : ""}>${t("tests.eval_exact_match")}</option>
    <option value="regex" ${selectedType === "regex" ? "selected" : ""}>${t("tests.eval_regex")}</option>
    <option value="human_review" ${selectedType === "human_review" ? "selected" : ""}>${t("tests.eval_human_review")}</option>
  `;
}

function renderEditorCasesList() {
  const container = $("te-cases-list");
  const countBadge = $("te-cases-count-badge");
  if (!container) return;
  if (countBadge) countBadge.textContent = String(currentEditorCases.length);

  container.innerHTML = currentEditorCases.map((c, idx) => {
    const isRegex = c.type === "regex";
    const isHuman = c.type === "human_review";
    const isFirst = idx === 0;
    const isLast = idx === currentEditorCases.length - 1;
    const deleteBtn = currentEditorCases.length > 1
      ? `<button type="button" class="btn-icon te-case-delete" data-idx="${idx}" title="${t("tests.delete_case")}">🗑️</button>`
      : "";
    const steps = Array.isArray(c.steps) ? c.steps : [];
    const stepsHtml = steps.map((s, sidx) => {
      const sIsRegex = s.type === "regex";
      const sIsHuman = s.type === "human_review";
      return `
        <div class="te-case-step" data-step-idx="${sidx}">
          <div class="te-case-step-header">
            <span class="te-case-step-badge">${t("tests.step_num", { n: sidx + 1 })}</span>
            <input type="text" class="te-case-step-name" value="${escapeHtml(s.name || "")}" placeholder="${t("tests.step_name_placeholder")}" autocomplete="off">
            <button type="button" class="btn-icon te-case-step-delete" data-case-idx="${idx}" data-step-idx="${sidx}" title="${t("tests.delete_step")}">×</button>
          </div>
          <div class="te-case-step-grid">
            <textarea class="te-case-step-prompt" rows="2" placeholder="${t("tests.step_prompt_placeholder")}" autocomplete="off">${escapeHtml(s.prompt || "")}</textarea>
            <div class="te-case-step-eval">
              <select class="te-case-step-eval-type" autocomplete="off">${evalOptionsHtml(s.type || "contains")}</select>
              <input type="text" class="te-case-step-expected" value="${escapeHtml(sIsRegex ? (s.pattern || "") : (s.expected || ""))}" placeholder="${sIsRegex ? "^[A-Z]+$" : t("tests.case_expected_placeholder")}" autocomplete="off" ${sIsHuman ? "hidden" : ""}>
            </div>
          </div>
          <div class="te-case-step-extra">
            <input type="text" class="te-case-step-system" value="${escapeHtml(s.system_prompt || "")}" placeholder="${t("tests.step_system_placeholder")}" title="${t("tests.step_system_placeholder")}" autocomplete="off">
            <input type="number" class="te-case-step-temperature" min="0" max="2" step="0.05" value="${escapeHtml(s.temperature ?? "")}" placeholder="${t("tests.inherit_placeholder")}" title="${t("tests.step_temp_hint")}" autocomplete="off">
          </div>
        </div>
      `;
    }).join("");

    return `
      <div class="te-case-card" data-idx="${idx}">
        <div class="te-case-card-header">
          <div class="te-case-reorder-btns">
            <button type="button" class="btn-icon te-case-move-up" data-idx="${idx}" title="${t("tests.move_up")}" ${isFirst ? "disabled" : ""}>▲</button>
            <button type="button" class="btn-icon te-case-move-down" data-idx="${idx}" title="${t("tests.move_down")}" ${isLast ? "disabled" : ""}>▼</button>
          </div>
          <span class="te-case-card-badge">${t("tests.case_num", { n: idx + 1 })}</span>
          <input type="text" class="te-case-name" value="${escapeHtml(c.name || "")}" placeholder="${t("tests.case_name_placeholder")}" autocomplete="off">
          ${deleteBtn}
        </div>
        <div class="te-case-card-body">
          <div class="te-case-content-grid">
            <div class="field te-case-prompt-field">
              <label class="te-case-label">${t("tests.case_prompt")}</label>
              <textarea class="te-case-prompt" rows="3" placeholder="${t("tests.case_prompt_placeholder")}" autocomplete="off">${escapeHtml(c.prompt || "")}</textarea>
            </div>
            <div class="te-case-eval-col">
              <div class="field te-case-eval-type-field">
                <label class="te-case-label">${t("tests.case_eval_type")}</label>
                <select class="te-case-eval-type" autocomplete="off">${evalOptionsHtml(c.type || "contains")}</select>
              </div>
              <div class="field te-case-expected-field" ${isHuman ? "hidden" : ""}>
                <label class="te-case-label">${isRegex ? t("tests.eval_pattern") : t("tests.case_expected")}</label>
                <input type="text" class="te-case-expected" value="${escapeHtml(isRegex ? (c.pattern || "") : (c.expected || ""))}" placeholder="${isRegex ? "^[A-Z]+$" : t("tests.case_expected_placeholder")}" autocomplete="off">
              </div>
            </div>
          </div>
          <details class="te-case-advanced">
            <summary>${t("tests.case_advanced")}</summary>
            <div class="field te-case-system-field">
              <label class="te-case-label">${t("tests.case_system_override")}</label>
              <textarea class="te-case-system" rows="2" placeholder="${t("tests.case_system_placeholder")}" autocomplete="off">${escapeHtml(c.system_prompt || "")}</textarea>
            </div>
            <div class="te-case-options-row">
              <div class="field">
                <label class="te-case-label">${t("tests.case_temp")}</label>
                <input type="number" class="te-case-temperature" min="0" max="2" step="0.05" value="${escapeHtml(c.temperature ?? "")}" placeholder="${t("tests.inherit_placeholder")}" autocomplete="off">
              </div>
              <div class="field">
                <label class="te-case-label">${t("tests.case_topp")}</label>
                <input type="number" class="te-case-top-p" min="0" max="1" step="0.05" value="${escapeHtml(c.top_p ?? "")}" placeholder="${t("tests.inherit_placeholder")}" autocomplete="off">
              </div>
              <div class="field">
                <label class="te-case-label">${t("tests.case_maxtokens")}</label>
                <input type="number" class="te-case-max-tokens" min="1" step="1" value="${escapeHtml(c.max_tokens ?? "")}" placeholder="${t("tests.inherit_placeholder")}" autocomplete="off">
              </div>
            </div>
          </details>
          <div class="te-case-steps-block">
            <div class="te-case-steps-header">
              <div>
                <span class="te-case-label">${t("tests.case_steps_title")}</span>
                <small class="muted te-case-steps-hint">${t("tests.case_steps_hint")}</small>
              </div>
              <button type="button" class="ghost te-case-add-step-btn" data-case-idx="${idx}">${t("tests.add_step")}</button>
            </div>
            <div class="te-case-steps-list">${stepsHtml}</div>
          </div>
        </div>
      </div>
    `;
  }).join("");

  container.querySelectorAll(".te-case-move-up").forEach((btn) => {
    btn.addEventListener("click", () => {
      const idx = Number(btn.dataset.idx);
      if (idx <= 0) return;
      syncEditorCasesFromDOM();
      const temp = currentEditorCases[idx];
      currentEditorCases[idx] = currentEditorCases[idx - 1];
      currentEditorCases[idx - 1] = temp;
      renderEditorCasesList();
    });
  });

  container.querySelectorAll(".te-case-move-down").forEach((btn) => {
    btn.addEventListener("click", () => {
      const idx = Number(btn.dataset.idx);
      if (idx >= currentEditorCases.length - 1) return;
      syncEditorCasesFromDOM();
      const temp = currentEditorCases[idx];
      currentEditorCases[idx] = currentEditorCases[idx + 1];
      currentEditorCases[idx + 1] = temp;
      renderEditorCasesList();
    });
  });

  container.querySelectorAll(".te-case-delete").forEach((btn) => {
    btn.addEventListener("click", () => {
      const idx = Number(btn.dataset.idx);
      syncEditorCasesFromDOM();
      currentEditorCases.splice(idx, 1);
      renderEditorCasesList();
    });
  });

  container.querySelectorAll(".te-case-eval-type").forEach((sel) => {
    sel.addEventListener("change", () => {
      const card = sel.closest(".te-case-card");
      const expectedField = card?.querySelector(":scope > .te-case-card-body > .te-case-content-grid .te-case-expected-field");
      const label = expectedField?.querySelector("label");
      const input = expectedField?.querySelector("input");
      const type = sel.value;
      if (expectedField) {
        expectedField.hidden = type === "human_review";
        if (label) label.textContent = type === "regex" ? t("tests.eval_pattern") : t("tests.case_expected");
        if (input) input.placeholder = type === "regex" ? "^[A-Z]+$" : t("tests.case_expected_placeholder");
      }
    });
  });

  container.querySelectorAll(".te-case-step-eval-type").forEach((sel) => {
    sel.addEventListener("change", () => {
      const evalBox = sel.closest(".te-case-step-eval");
      const input = evalBox?.querySelector(".te-case-step-expected");
      const type = sel.value;
      if (input) {
        input.hidden = type === "human_review";
        input.placeholder = type === "regex" ? "^[A-Z]+$" : t("tests.case_expected_placeholder");
      }
    });
  });

  container.querySelectorAll(".te-case-add-step-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const caseIdx = Number(btn.dataset.caseIdx);
      syncEditorCasesFromDOM();
      const target = currentEditorCases[caseIdx];
      if (!target) return;
      if (!Array.isArray(target.steps)) target.steps = [];
      target.steps.push(newEditorStep(`Step ${target.steps.length + 1}`));
      renderEditorCasesList();
    });
  });

  container.querySelectorAll(".te-case-step-delete").forEach((btn) => {
    btn.addEventListener("click", () => {
      const caseIdx = Number(btn.dataset.caseIdx);
      const stepIdx = Number(btn.dataset.stepIdx);
      syncEditorCasesFromDOM();
      currentEditorCases[caseIdx]?.steps?.splice(stepIdx, 1);
      renderEditorCasesList();
    });
  });

  const addCaseBtn = $("te-add-case-btn");
  if (addCaseBtn && !addCaseBtn.dataset.wired) {
    addCaseBtn.dataset.wired = "1";
    addCaseBtn.addEventListener("click", () => {
      syncEditorCasesFromDOM();
      currentEditorCases.push(newEditorCase(`Case ${currentEditorCases.length + 1}`));
      renderEditorCasesList();
      const lastCard = $("te-cases-list")?.lastElementChild;
      lastCard?.scrollIntoView({ behavior: "smooth", block: "nearest" });
      lastCard?.querySelector(".te-case-prompt")?.focus();
    });
  }
}

function syncEditorCasesFromDOM() {
  const container = $("te-cases-list");
  if (!container) return;
  const cards = container.querySelectorAll(".te-case-card");
  if (cards.length === 0) return;
  currentEditorCases = Array.from(cards).map((card, idx) => {
    const name = card.querySelector(".te-case-name")?.value.trim() || `Case ${idx + 1}`;
    const prompt = card.querySelector(".te-case-prompt")?.value.trim() || "";
    const type = card.querySelector(".te-case-eval-type")?.value || "contains";
    const val = card.querySelector(".te-case-expected")?.value.trim() || "";
    const systemPrompt = card.querySelector(".te-case-system")?.value || "";
    const temperature = card.querySelector(".te-case-temperature")?.value.trim() || "";
    const topP = card.querySelector(".te-case-top-p")?.value.trim() || "";
    const maxTokens = card.querySelector(".te-case-max-tokens")?.value.trim() || "";
    const stepEls = card.querySelectorAll(".te-case-step");
    const steps = Array.from(stepEls).map((sel2, sidx) => {
      const sName = sel2.querySelector(".te-case-step-name")?.value.trim() || `Step ${sidx + 1}`;
      const sPrompt = sel2.querySelector(".te-case-step-prompt")?.value.trim() || "";
      const sType = sel2.querySelector(".te-case-step-eval-type")?.value || "contains";
      const sVal = sel2.querySelector(".te-case-step-expected")?.value.trim() || "";
      const sSys = sel2.querySelector(".te-case-step-system")?.value || "";
      const sTemp = sel2.querySelector(".te-case-step-temperature")?.value.trim() || "";
      return {
        name: sName,
        prompt: sPrompt,
        type: sType,
        expected: sType !== "regex" ? sVal : "",
        pattern: sType === "regex" ? sVal : "",
        system_prompt: sSys,
        temperature: sTemp,
      };
    });
    return {
      name,
      prompt,
      type,
      expected: type !== "regex" ? val : "",
      pattern: type === "regex" ? val : "",
      system_prompt: systemPrompt,
      temperature,
      top_p: topP,
      max_tokens: maxTokens,
      steps,
    };
  });
}

async function showTestEditorView(id) {
  hideAllMainViews();
  currentView = "test-editor";
  $("tests-btn")?.classList.add("active");
  $("test-editor-view").hidden = false;
  currentTestId = id;
  if (id) {
    let test = tests.find((x) => x.id === id);
    if (!test && tests.length === 0) {
      await refreshTests();
      test = tests.find((x) => x.id === id);
    }
    populateTestEditorGroupSelect();
    if (test) {
      $("test-editor-title").textContent = t("tests.edit_test");
      $("te-name").value = test.name || "";
      $("te-description").value = test.description || "";
      $("te-group").value = test.group_id || "";
      $("te-active").checked = !!test.active;
      $("te-system").value = test.system_prompt || "";
      $("te-required-caps").value = (test.required_caps || []).join(", ");
      $("te-order").value = String(test.order || 0);

      const toEditorStep = (s, i) => ({
        name: s.name || `Step ${i + 1}`,
        prompt: s.prompt || "",
        type: s.evaluation?.type || test.evaluation_type || "contains",
        expected: s.evaluation?.expected != null ? String(s.evaluation.expected) : "",
        pattern: s.evaluation?.pattern || "",
        system_prompt: s.system_prompt || "",
        temperature: s.options?.temperature ?? "",
      });
      const toEditorCase = (c, i) => ({
        name: c.name || `Case ${i + 1}`,
        prompt: c.prompt || "",
        type: c.evaluation?.type || test.evaluation_type || "contains",
        expected: c.evaluation?.expected != null ? String(c.evaluation.expected) : (test.evaluation_config?.expected != null ? String(test.evaluation_config.expected) : ""),
        pattern: c.evaluation?.pattern || test.evaluation_config?.pattern || "",
        system_prompt: c.system_prompt || "",
        temperature: c.options?.temperature ?? "",
        top_p: c.options?.top_p ?? "",
        max_tokens: c.options?.max_tokens ?? "",
        steps: Array.isArray(c.steps) ? c.steps.map(toEditorStep) : [],
      });
      // Load cases
      if (Array.isArray(test.cases) && test.cases.length > 0) {
        currentEditorCases = test.cases.map(toEditorCase);
      } else {
        currentEditorCases = [toEditorCase({
          name: "Case 1",
          prompt: test.prompt || "",
          evaluation: test.evaluation_type || test.evaluation ? { type: test.evaluation_type, expected: test.evaluation_config?.expected, pattern: test.evaluation_config?.pattern } : undefined,
        }, 0)];
      }
      $("te-temperature").value = test.options?.temperature ?? "";
      $("te-top-p").value = test.options?.top_p ?? "";
      $("te-max-tokens").value = test.options?.max_tokens ?? "";
      renderEditorCasesList();

      testEditorAttachments = (test.attachments || []).map((a) => ({ ...a }));
      renderTestEditorAttachments();
      $("test-editor-delete").hidden = false;
      if (window.location.pathname !== "/tests/edit/" + id) {
        history.pushState(null, "", "/tests/edit/" + id);
      }
      return;
    }
  }
  populateTestEditorGroupSelect();
  $("test-editor-title").textContent = t("tests.new_test");
  $("te-name").value = "";
  $("te-description").value = "";
  $("te-group").value = selectedGroupId || "";
  $("te-active").checked = true;
  $("te-system").value = "";
  $("te-temperature").value = "";
  $("te-top-p").value = "";
  $("te-max-tokens").value = "";
  $("te-required-caps").value = "";
  $("te-order").value = "0";
  currentEditorCases = [newEditorCase("Case 1")];
  renderEditorCasesList();
  testEditorAttachments = [];
  renderTestEditorAttachments();
  $("test-editor-delete").hidden = true;
  if (window.location.pathname !== "/tests/new") {
    history.pushState(null, "", "/tests/new");
  }
}

let selectedTestModel = "";
let testRunsCache = [];

async function refreshTests() {
  try {
    const [data, runsData] = await Promise.all([
      api("/api/tests"),
      api("/api/runner/runs").catch(() => ({ runs: [] })),
    ]);
    testsGroups = data.groups || [];
    tests = data.tests || [];
    testRunsCache = runsData.runs || [];
    renderTestsSidebar();
    renderTestsModelSelect();
    renderTestsList();
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
}

function renderTestsModelSelect() {
  const select = $("tests-model-filter");
  if (!select) return;
  const activeModels = (typeof models !== "undefined" ? models : []).filter((m) => !m.archived);
  let optionsHtml = `<option value="">${escapeHtml(t("tests.all_models"))}</option>`;
  for (const m of activeModels) {
    const isSel = m.name === selectedTestModel;
    optionsHtml += `<option value="${escapeHtml(m.name)}" ${isSel ? "selected" : ""}>${escapeHtml(m.name)}</option>`;
  }
  select.innerHTML = optionsHtml;

  if (!select.dataset.wired) {
    select.dataset.wired = "1";
    select.addEventListener("change", () => {
      selectedTestModel = select.value || "";
      renderTestsList();
    });
  }
}

function isSeedTestId(id) {
  return /^t\d+$/.test(String(id || ""));
}

async function deleteTestById(id) {
  const ok = await askConfirm({
    title: t("tests.delete_title"),
    text: isSeedTestId(id) ? t("tests.delete_seed_text") : t("tests.delete_text"),
    okText: t("action.delete"),
    okClass: "danger",
  });
  if (!ok.ok) return false;
  const data = await api("/api/tests/" + encodeURIComponent(id), { method: "DELETE" });
  if (data.reseeded) {
    toast(t("tests.reseeded_toast"), "success");
  }
  await refreshTests();
  return true;
}

function renderTestsSidebar() {
  const container = $("tests-groups-list");
  if (!container) return;
  const allBtnClass = selectedGroupId === "" ? "tests-group-item active" : "tests-group-item";
  let html = `<div class="${allBtnClass}" data-group-id="">
    <span class="tests-group-name">${escapeHtml(t("tests.all_tests"))}</span>
    <span class="tests-group-count">${tests.length}</span>
  </div>`;
  for (const g of testsGroups) {
    const cls = selectedGroupId === g.id ? "tests-group-item active" : "tests-group-item";
    const count = tests.filter((t) => t.group_id === g.id).length;
    html += `<div class="${cls}" data-group-id="${escapeHtml(g.id)}">
      <span class="tests-group-name">${escapeHtml(g.name)}</span>
      <span class="tests-group-actions">
        <button type="button" class="btn-icon te-group-settings" data-group-id="${escapeHtml(g.id)}" data-group-name="${escapeHtml(g.name)}" title="${t("tests.group_settings")}">⚙️</button>
      </span>
      <span class="tests-group-count">${count}</span>
    </div>`;
  }
  container.innerHTML = html;
  container.querySelectorAll(".tests-group-item").forEach((el) => {
    el.addEventListener("click", (e) => {
      if (e.target.closest(".te-group-settings")) return;
      selectedGroupId = el.dataset.groupId;
      const newPath = selectedGroupId ? "/tests/group/" + encodeURIComponent(selectedGroupId) : "/tests";
      if (window.location.pathname !== newPath) {
        history.pushState(null, "", newPath);
      }
      renderTestsSidebar();
      renderTestsList();
    });
  });
  container.querySelectorAll(".te-group-settings").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      openManageGroupModal(btn.dataset.groupId, btn.dataset.groupName);
    });
  });
}

function renderTestsList() {
  const list = $("tests-list");
  const empty = $("tests-empty");
  const title = $("tests-group-title");
  if (!list || !empty || !title) return;

  let filtered = selectedGroupId !== "" ? tests.filter((t) => t.group_id === selectedGroupId) : [...tests];
  filtered.sort((a, b) => (a.order || 0) - (b.order || 0));
  if (selectedGroupId !== "") {
    const g = testsGroups.find((x) => x.id === selectedGroupId);
    title.textContent = g ? g.name : t("tests.all_tests");
  } else {
    title.textContent = t("tests.all_tests");
  }

  const runBtn = $("tests-run-battery-btn");
  if (runBtn) {
    const hasActiveNonAgent = (selectedGroupId === "" ? tests : filtered).some((t) => t.active && t.evaluation_type !== "agent");
    runBtn.hidden = !hasActiveNonAgent;
    if (!runBtn.dataset.wired) {
      runBtn.dataset.wired = "1";
      runBtn.addEventListener("click", () => {
        openBatteryModal({ groupId: selectedGroupId || "all", initialModel: selectedTestModel || undefined });
      });
    }
  }
  const groupHistBtn = $("tests-group-history-btn");
  if (groupHistBtn) {
    groupHistBtn.hidden = false;
    if (!groupHistBtn.dataset.wired) {
      groupHistBtn.dataset.wired = "1";
      groupHistBtn.addEventListener("click", () => {
        showBatteryHistoryView(null, selectedTestModel || null);
      });
    }
  }
  const lbBtn = $("tests-leaderboard-btn");
  if (lbBtn) {
    lbBtn.hidden = false;
    if (!lbBtn.dataset.wired) {
      lbBtn.dataset.wired = "1";
      lbBtn.addEventListener("click", () => {
        openLeaderboardModal();
      });
    }
  }

  if (!filtered.length) {
    list.innerHTML = "";
    empty.hidden = false;
    return;
  }
  empty.hidden = true;

  list.innerHTML = filtered.map((test, idx) => {
    const isFirst = idx === 0;
    const isLast = idx === filtered.length - 1;
    const activeClass = test.active ? "pill-good" : "pill-muted";
    const activeLabel = test.active ? t("tests.status_active") : t("tests.status_suspended");
    let evalLabel = "";
    if (test.cases && test.cases.length > 1) {
      evalLabel = t("tests.cases_count", { n: test.cases.length });
    } else if (test.evaluation_type) {
      const tr = t("tests.eval_" + test.evaluation_type);
      evalLabel = (tr && tr !== "tests.eval_" + test.evaluation_type) ? tr : test.evaluation_type;
    } else if (test.cases && test.cases.length === 1 && test.cases[0]?.evaluation_type) {
      const tr = t("tests.eval_" + test.cases[0].evaluation_type);
      evalLabel = (tr && tr !== "tests.eval_" + test.cases[0].evaluation_type) ? tr : test.cases[0].evaluation_type;
    }
    const caps = (test.required_caps || []).map((c) => `<span class="pill">${escapeHtml(c)}</span>`).join("");

    let modelResultBadge = "";
    if (selectedTestModel) {
      let foundRes = null;
      for (const run of testRunsCache) {
        const r = (run.results || []).find((x) => x.test_id === test.id && x.model === selectedTestModel);
        if (r) {
          foundRes = r;
          break;
        }
      }
      if (foundRes) {
        if (foundRes.passed === true) {
          const tpsText = foundRes.tokens_per_sec > 0 ? ` · ⚡ ${foundRes.tokens_per_sec.toFixed(1)} tok/s` : "";
          modelResultBadge = `<span class="pill pill-good pill-model-result" title="${escapeHtml(selectedTestModel)}">✔ OK (${fmtDuration(foundRes.response_time_ms)}${tpsText})</span>`;
        } else if (foundRes.passed === false) {
          modelResultBadge = `<span class="pill pill-bad pill-model-result" title="${escapeHtml(selectedTestModel)}">✖ FAIL (${fmtDuration(foundRes.response_time_ms)})</span>`;
        } else {
          modelResultBadge = `<span class="pill pill-human pill-model-result" title="${escapeHtml(selectedTestModel)}">👁️ ${t("battery.human_review")}</span>`;
        }
      } else {
        modelResultBadge = `<span class="pill pill-muted pill-model-result" title="${escapeHtml(selectedTestModel)}">— ${t("tests.model_not_tested")}</span>`;
      }
    }

    return `
      <div class="tests-item" data-id="${escapeHtml(test.id)}">
        <div class="tests-item-reorder">
          <button type="button" class="btn-icon tests-item-move-up" data-id="${escapeHtml(test.id)}" title="${t("tests.move_up")}" ${isFirst ? "disabled" : ""}>▲</button>
          <button type="button" class="btn-icon tests-item-move-down" data-id="${escapeHtml(test.id)}" title="${t("tests.move_down")}" ${isLast ? "disabled" : ""}>▼</button>
        </div>
        <div class="tests-item-main">
          <div class="tests-item-name">${escapeHtml(test.name)}</div>
          <div class="tests-item-meta">
            <span class="pill ${activeClass}">${escapeHtml(activeLabel)}</span>
            ${evalLabel ? `<span class="pill">${escapeHtml(evalLabel)}</span>` : ""}
            ${caps}
            ${modelResultBadge}
          </div>
          ${test.description ? `<div class="tests-item-desc muted">${escapeHtml(test.description)}</div>` : ""}
        </div>
        <div class="tests-item-actions">
          <button class="primary tests-item-run" data-id="${escapeHtml(test.id)}" title="${t("tests.run")}">▶ ${t("tests.run")}</button>
          <button class="ghost tests-item-history" data-id="${escapeHtml(test.id)}">${t("tests.history_short")}</button>
          <button class="ghost tests-item-edit" data-i18n="action.edit">Edit</button>
          <button class="ghost tests-item-toggle" data-id="${escapeHtml(test.id)}">${test.active ? t("tests.suspend") : t("tests.activate")}</button>
          <button class="ghost danger-text tests-item-delete" data-id="${escapeHtml(test.id)}">×</button>
        </div>
      </div>
    `;
  }).join("");

  list.querySelectorAll(".tests-item-move-up").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      void moveTestOrder(btn.dataset.id, "up");
    });
  });
  list.querySelectorAll(".tests-item-move-down").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      void moveTestOrder(btn.dataset.id, "down");
    });
  });

  list.querySelectorAll(".tests-item").forEach((el) => {
    el.addEventListener("click", (e) => {
      if (e.target.closest("button")) return;
      void showTestEditorView(el.dataset.id);
    });
  });
  list.querySelectorAll(".tests-item-run").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const id = btn.dataset.id;
      if (id) {
        const test = tests.find((t) => t.id === id);
        if (test?.evaluation_type === "agent") {
          showAgentSessionView(id);
        } else {
          openBatteryModal({ testId: id, initialModel: selectedTestModel || undefined });
        }
      }
    });
  });
  list.querySelectorAll(".tests-item-edit").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const id = btn.closest(".tests-item")?.dataset?.id;
      if (id) void showTestEditorView(id);
    });
  });
  list.querySelectorAll(".tests-item-history").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const id = btn.dataset.id;
      if (id) {
        if (typeof openTestHistoryModal === "function") {
          openTestHistoryModal(id);
        } else {
          showBatteryHistoryView(id, selectedTestModel || null);
        }
      }
    });
  });
  list.querySelectorAll(".tests-item-toggle").forEach((btn) => {
    btn.addEventListener("click", async (e) => {
      e.stopPropagation();
      const id = btn.dataset.id;
      const test = tests.find((t) => t.id === id);
      if (!test) return;
      try {
        await api("/api/tests/" + encodeURIComponent(id), {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ ...test, active: !test.active }),
        });
        await refreshTests();
      } catch (err) {
        toast(t("toast.error", { msg: err.message }), "error");
      }
    });
  });
  list.querySelectorAll(".tests-item-delete").forEach((btn) => {
    btn.addEventListener("click", async (e) => {
      e.stopPropagation();
      const id = btn.dataset.id;
      try {
        await deleteTestById(id);
      } catch (err) {
        toast(t("toast.error", { msg: err.message }), "error");
      }
    });
  });
}

async function moveTestOrder(testId, direction) {
  let list = selectedGroupId !== "" ? tests.filter((t) => t.group_id === selectedGroupId) : [...tests];
  list.sort((a, b) => (a.order || 0) - (b.order || 0));
  const idx = list.findIndex((t) => t.id === testId);
  if (idx === -1) return;
  const targetIdx = direction === "up" ? idx - 1 : idx + 1;
  if (targetIdx < 0 || targetIdx >= list.length) return;

  list.forEach((t, i) => { t.order = i; });
  const currentTest = list[idx];
  const targetTest = list[targetIdx];

  const tmpOrder = currentTest.order;
  currentTest.order = targetTest.order;
  targetTest.order = tmpOrder;

  renderTestsList();

  try {
    await Promise.all([
      api("/api/tests/" + encodeURIComponent(currentTest.id), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(currentTest),
      }),
      api("/api/tests/" + encodeURIComponent(targetTest.id), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(targetTest),
      }),
    ]);
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
    await refreshTests();
  }
}

function getAutoCapsFromAttachments() {
  const caps = new Set();
  for (const a of testEditorAttachments) {
    if (a.kind === "image") caps.add("vision");
    if (a.kind === "audio") caps.add("audio");
  }
  return Array.from(caps);
}

function updateTestEditorAutoCaps() {
  const el = $("te-auto-caps");
  if (!el) return;
  const auto = getAutoCapsFromAttachments();
  const userRaw = $("te-required-caps")?.value || "";
  const user = userRaw.split(",").map((s) => s.trim()).filter(Boolean);
  const all = new Set([...user, ...auto]);
  if (!all.size) {
    el.innerHTML = "";
    return;
  }
  const pills = Array.from(all).map((c) => {
    const isAuto = auto.includes(c) && !user.includes(c);
    return `<span class="pill${isAuto ? " te-auto-pill" : ""}" title="${isAuto ? "Auto-detected from attachments" : "Manually set"}">${escapeHtml(c)}</span>`;
  }).join("");
  el.innerHTML = `<div class="te-auto-caps-label">Effective capabilities:</div><div class="te-auto-caps-pills">${pills}</div>`;
}

function renderTestEditorAttachments() {
  const list = $("te-attach-list");
  if (!list) return;
  if (!testEditorAttachments.length) {
    list.innerHTML = "";
    updateTestEditorAutoCaps();
    return;
  }
  list.innerHTML = testEditorAttachments.map((a) => {
    if (a.kind === "image") {
      const src = `data:${a.mime};base64,${a.data}`;
      return `<div class="te-attach-item" data-id="${escapeHtml(a.id)}">
        <img src="${src}" alt="" class="te-attach-thumb">
        <span class="te-attach-name mono">${escapeHtml(a.name)}</span>
        <button type="button" class="btn-icon te-attach-remove" data-id="${escapeHtml(a.id)}" title="Remove">×</button>
      </div>`;
    }
    if (a.kind === "audio") {
      const src = `data:${a.mime};base64,${a.data}`;
      return `<div class="te-attach-item te-attach-item-audio" data-id="${escapeHtml(a.id)}">
        <span class="te-attach-name mono">${escapeHtml(a.name)}</span>
        <audio controls preload="metadata" src="${src}" class="te-attach-audio"></audio>
        <button type="button" class="btn-icon te-attach-remove" data-id="${escapeHtml(a.id)}" title="Remove">×</button>
      </div>`;
    }
    return "";
  }).join("");
  list.querySelectorAll(".te-attach-remove").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      e.preventDefault();
      testEditorAttachments = testEditorAttachments.filter((x) => x.id !== btn.dataset.id);
      renderTestEditorAttachments();
    });
  });
  updateTestEditorAutoCaps();
}

async function handleTestEditorFileInput(files, kind) {
  for (const file of files) {
    const data = await toBase64(file);
    testEditorAttachments.push({
      id: nanoid(),
      kind,
      name: file.name,
      mime: file.type || (kind === "image" ? "image/jpeg" : "audio/webm"),
      data,
    });
  }
  renderTestEditorAttachments();
}

function populateTestEditorGroupSelect() {
  const sel = $("te-group");
  if (!sel) return;
  sel.innerHTML = `<option value="">${escapeHtml(t("tests.no_group"))}</option>` +
    testsGroups.map((g) => `<option value="${escapeHtml(g.id)}">${escapeHtml(g.name)}</option>`).join("");
}

async function saveTestEditor() {
  syncEditorCasesFromDOM();
  if (currentEditorCases.length === 0) {
    currentEditorCases = [newEditorCase("Case 1")];
  }

  const buildEval = (type, expected, pattern) => {
    const evaluation = { type: type || "contains" };
    if (type === "regex") {
      if (pattern || expected) evaluation.pattern = pattern || expected;
    } else if (type !== "human_review" && expected) {
      evaluation.expected = expected;
    }
    return evaluation;
  };
  const parseNum = (v) => {
    if (v === "" || v === null || v === undefined) return undefined;
    const n = Number(v);
    return Number.isFinite(n) ? n : undefined;
  };

  const cases = currentEditorCases.map((c, i) => {
    const item = {
      name: c.name || `Case ${i + 1}`,
      prompt: c.prompt || "",
      evaluation: buildEval(c.type, c.expected, c.pattern),
    };
    if (c.system_prompt && c.system_prompt.trim() !== "") {
      item.system_prompt = c.system_prompt;
    }
    const temp = parseNum(c.temperature);
    const topP = parseNum(c.top_p);
    const maxTokens = parseNum(c.max_tokens);
    if (temp !== undefined || topP !== undefined || maxTokens !== undefined) {
      item.options = {};
      if (temp !== undefined) item.options.temperature = temp;
      if (topP !== undefined) item.options.top_p = topP;
      if (maxTokens !== undefined) item.options.max_tokens = Math.round(maxTokens);
    }
    if (Array.isArray(c.steps) && c.steps.length > 0) {
      item.steps = c.steps.map((s, j) => {
        const stepItem = {
          name: s.name || `Step ${j + 1}`,
          prompt: s.prompt || "",
          evaluation: buildEval(s.type, s.expected, s.pattern),
        };
        if (s.system_prompt && s.system_prompt.trim() !== "") {
          stepItem.system_prompt = s.system_prompt;
        }
        const sTemp = parseNum(s.temperature);
        if (sTemp !== undefined) {
          stepItem.options = { temperature: sTemp };
        }
        return stepItem;
      });
    }
    return item;
  });

  const firstCase = cases[0] || { prompt: "", evaluation: { type: "contains" } };
  const firstPrompt = firstCase.prompt || firstCase.steps?.[0]?.prompt || "";
  const firstEval = firstCase.evaluation?.type === "human_review" && firstCase.steps?.[0]?.evaluation
    ? firstCase.steps[0].evaluation
    : firstCase.evaluation;
  const autoCaps = getAutoCapsFromAttachments();
  const userCaps = $("te-required-caps").value.split(",").map((s) => s.trim()).filter(Boolean);
  const gTemp = parseNum($("te-temperature")?.value);
  const gTopP = parseNum($("te-top-p")?.value);
  const gMaxTokens = parseNum($("te-max-tokens")?.value);
  const payload = {
    name: $("te-name").value.trim(),
    description: $("te-description").value.trim(),
    group_id: $("te-group").value,
    active: $("te-active").checked,
    system_prompt: $("te-system").value,
    prompt: firstPrompt,
    cases: cases,
    evaluation_type: (firstEval || {}).type || "contains",
    evaluation_config: firstEval?.expected ? { expected: firstEval.expected } : (firstEval?.pattern ? { pattern: firstEval.pattern } : null),
    required_caps: Array.from(new Set([...userCaps, ...autoCaps])),
    attachments: testEditorAttachments.map((a) => ({ id: a.id, kind: a.kind, name: a.name, mime: a.mime, data: a.data })),
    order: Number($("te-order").value) || 0,
  };
  if (gTemp !== undefined || gTopP !== undefined || gMaxTokens !== undefined) {
    payload.options = {};
    if (gTemp !== undefined) payload.options.temperature = gTemp;
    if (gTopP !== undefined) payload.options.top_p = gTopP;
    if (gMaxTokens !== undefined) payload.options.max_tokens = Math.round(gMaxTokens);
  }
  try {
    if (currentTestId) {
      await api("/api/tests/" + encodeURIComponent(currentTestId), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
    } else {
      await api("/api/tests", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
    }
    await refreshTests();
    const groupId = payload.group_id;
    selectedGroupId = groupId || "";
    const newPath = selectedGroupId ? "/tests/group/" + encodeURIComponent(selectedGroupId) : "/tests";
    if (window.location.pathname !== newPath) {
      history.pushState(null, "", newPath);
    }
    showTestsView();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function deleteTestEditor() {
  if (!currentTestId) return;
  try {
    const deleted = await deleteTestById(currentTestId);
    if (deleted) showTestsView();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

function openNewGroupModal() {
  setupGroupModals();
  const modal = $("new-group-modal");
  const input = $("new-group-name-input");
  if (!modal || !input) return;
  input.value = "";
  modal.hidden = false;
  setTimeout(() => input.focus(), 50);
}

function closeNewGroupModal() {
  const modal = $("new-group-modal");
  if (modal) modal.hidden = true;
}

async function submitNewGroup() {
  const input = $("new-group-name-input");
  const name = input?.value?.trim();
  if (!name) {
    input?.focus();
    return;
  }
  try {
    const res = await api("/api/test-groups", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, order: testsGroups.length }),
    });
    closeNewGroupModal();
    if (res && res.id) {
      selectedGroupId = res.id;
    }
    await refreshTests();
    toast(t("toast.saved"), "success");
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

function openManageGroupModal(id, name) {
  setupGroupModals();
  const modal = $("manage-group-modal");
  const idInput = $("manage-group-id-input");
  const nameInput = $("manage-group-name-input");
  if (!modal || !idInput || !nameInput) return;
  idInput.value = id;
  nameInput.value = name || "";
  modal.hidden = false;
  setTimeout(() => nameInput.focus(), 50);
}

function closeManageGroupModal() {
  const modal = $("manage-group-modal");
  if (modal) modal.hidden = true;
}

async function submitSaveManageGroup() {
  const id = $("manage-group-id-input")?.value;
  const input = $("manage-group-name-input");
  const name = input?.value?.trim();
  if (!id || !name) {
    input?.focus();
    return;
  }
  try {
    await api("/api/test-groups/" + encodeURIComponent(id), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    closeManageGroupModal();
    await refreshTests();
    toast(t("toast.saved"), "success");
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function submitDeleteManageGroup() {
  const id = $("manage-group-id-input")?.value;
  if (!id) return;
  const group = testsGroups.find((g) => g.id === id);
  const name = group ? group.name : id;
  const { ok } = await askConfirm({
    title: t("tests.delete_group_btn"),
    text: `¿Eliminar grupo "${name}"? Los tests de este grupo no se borrarán, quedarán sin grupo.`,
    okText: t("action.delete"),
    okClass: "danger",
  });
  if (!ok) return;
  try {
    await api("/api/test-groups/" + encodeURIComponent(id), { method: "DELETE" });
    closeManageGroupModal();
    if (selectedGroupId === id) {
      selectedGroupId = "";
      if (window.location.pathname !== "/tests") {
        history.pushState(null, "", "/tests");
      }
    }
    await refreshTests();
    toast(t("toast.deleted"), "success");
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function createNewGroup() {
  openNewGroupModal();
}

function setupGroupModals() {
  if (setupGroupModals.wired) return;
  setupGroupModals.wired = true;
  $("new-group-modal-close")?.addEventListener("click", closeNewGroupModal);
  $("new-group-cancel")?.addEventListener("click", closeNewGroupModal);
  $("new-group-submit")?.addEventListener("click", submitNewGroup);
  $("new-group-name-input")?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void submitNewGroup();
    } else if (e.key === "Escape") {
      closeNewGroupModal();
    }
  });

  $("manage-group-modal-close")?.addEventListener("click", closeManageGroupModal);
  $("manage-group-cancel")?.addEventListener("click", closeManageGroupModal);
  $("manage-group-save")?.addEventListener("click", submitSaveManageGroup);
  $("manage-group-delete-btn")?.addEventListener("click", submitDeleteManageGroup);
  $("manage-group-name-input")?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void submitSaveManageGroup();
    } else if (e.key === "Escape") {
      closeManageGroupModal();
    }
  });
}

async function showChatViewWithModel(name) {
  showChatView();
  if (!$("chat-view") || $("chat-view").hidden) return;
  $("chat-view")?.classList.remove("chat-options-open");
  if (!name) return;

  if (typeof refreshModels === "function" && (!modelByName(name) || !modelByName(name)?.capabilities?.length)) {
    try {
      await refreshModels();
    } catch (_) {}
  } else if (typeof syncChatModelOptions === "function") {
    syncChatModelOptions();
  }

  const sel = $("chat-model");
  if (sel) {
    const exists = Array.from(sel.options || []).some((o) => o.value === name);
    if (!exists) {
      const opt = document.createElement("option");
      opt.value = name;
      opt.textContent = name;
      sel.appendChild(opt);
    }
    sel.value = name;
    updateChatModelLoadDot();
    updateChatCapabilityUI();
    updateChatContextMeter();
    void applyChatDefaultsForModel(name, true);
  }

  const model = modelByName(name);
  if (model) {
    const urlKey = getModelUrlKey(model);
    const newPath = "/chat/" + urlKey;
    if (window.location.pathname !== newPath) {
      history.pushState(null, "", newPath);
    }
  }
}

async function handleRouting() {
  const path = window.location.pathname;
  if (path.startsWith("/chat/")) {
    const key = path.substring(6);
    const model = findModelByUrlKey(key);
    if (model) {
      showChatViewWithModel(model.name);
    } else {
      showModelsView();
    }
  } else if (path === "/chat" || path === "/chat/") {
    showChatView();
  } else if (path === "/tests" || path === "/tests/") {
    const urlParams = new URLSearchParams(window.location.search);
    if (urlParams.has("model")) selectedTestModel = urlParams.get("model") || "";
    showTestsView();
  } else if (path.startsWith("/tests/group/")) {
    selectedGroupId = path.substring(13);
    const urlParams = new URLSearchParams(window.location.search);
    if (urlParams.has("model")) selectedTestModel = urlParams.get("model") || "";
    showTestsView(true);
  } else if (path === "/tests/new") {
    await showTestEditorView(null);
  } else if (path.startsWith("/tests/edit/")) {
    const id = path.substring(12);
    await showTestEditorView(id);
  } else if (path.startsWith("/tests/agent/")) {
    const id = path.substring(13);
    showAgentSessionView(id);
  } else if (path.startsWith("/tests/battery/progress/")) {
    const id = path.substring(24);
    const saved = localStorage.getItem(BATTERY_KEY);
    let modelIDs = [];
    let groupId = "";
    if (saved) {
      try {
        const data = JSON.parse(saved);
        if (data.runID === id) {
          modelIDs = data.modelIDs || [];
          groupId = data.groupId || "";
        }
      } catch { }
    }
    showBatteryProgressView(modelIDs, id, groupId);
  } else if (path.startsWith("/tests/battery/results/")) {
    const id = path.substring(23);
    void showBatteryResultsView(id);
  } else if (path === "/tests/battery/history" || path.startsWith("/tests/history/")) {
    const filterTestId = path.startsWith("/tests/history/") ? decodeURIComponent(path.substring(15)) : null;
    const urlParams = new URLSearchParams(window.location.search);
    const filterModel = urlParams.get("model") || null;
    showBatteryHistoryView(filterTestId, filterModel);
    if (filterTestId && typeof openTestHistoryModal === "function") {
      openTestHistoryModal(filterTestId);
    }
  } else if (path === "/analytics" || path === "/analytics/") {
    showAnalyticsView();
  } else if (path === "/settings" || path.startsWith("/settings/")) {
    if (typeof showSettingsView === "function") {
      await showSettingsView();
      if (typeof showSettingsSection === "function") {
        if (path === "/settings/general") showSettingsSection("sec-general", false);
        else if (path === "/settings/chat-defaults") showSettingsSection("sec-chat-defaults", false);
        else if (path === "/settings/prompts") showSettingsSection("sec-prompts", false);
        else if (path === "/settings/network") showSettingsSection("sec-network", false);
        else if (path === "/settings/external") showSettingsSection("sec-ext-models", false);
        else if (path === "/settings/archived") showSettingsSection("sec-archived", false);
        else if (path === "/settings/opencode") showSettingsSection("sec-opencode", false);
        else if ((path === "/settings" || path === "/settings/") && window.innerWidth <= 900 && typeof showSettingsMobileMenu === "function") {
          showSettingsMobileMenu();
        }
      }
    }
  } else if (path === "/opencode" || path === "/opencode/") {
    if (typeof showSettingsView === "function") {
      await showSettingsView();
      if (typeof showSettingsSection === "function") {
        showSettingsSection("sec-opencode", true);
      }
    }
  } else if (path === "/archived" || path === "/archived/") {
    if (typeof showSettingsView === "function") {
      await showSettingsView();
      if (typeof showSettingsSection === "function") {
        showSettingsSection("sec-archived", true);
      }
    }
  } else if (path === "/modelfile" || path === "/modelfile/") {
    if (typeof showModelfileView === "function") {
      showModelfileView();
    }
  } else if (path === "/hf" || path === "/hf/" || path === "/huggingface" || path === "/huggingface/") {
    if (typeof showHFView === "function") {
      showHFView();
    }
  } else if (path === "/") {
    if (currentView !== "models") {
      showModelsView();
    }
  }
}

function isGFMTableRow(s) {
  s = s.trim();
  return s.length > 2 && s.startsWith("|") && s.endsWith("|");
}

function isGFMTableSeparator(s) {
  s = s.trim();
  if (!s.includes("|") || !/[-:]{2,}/.test(s)) return false;
  const parts = s.split("|").map((p) => p.trim()).filter((p) => p.length > 0);
  if (parts.length < 1) return false;
  return parts.every((p) => /^:?-{2,}:?$/.test(p));
}

function gfmTableCell(s) {
  if (!s) return "";
  let t = escapeHtml(s);
  t = t.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  t = t.replace(/`([^`]+)`/g, "<code>$1</code>");
  return t;
}

function gfmTableBlockToHTML(rows) {
  if (rows.length < 2) return "";
  const parseRow = (line) => {
    const t = line.trim();
    const inner = t.startsWith("|") ? t.slice(1) : t;
    const core = inner.endsWith("|") ? inner.slice(0, -1) : inner;
    return core.split("|").map((c) => c.trim());
  };
  const header = parseRow(rows[0]);
  const body = rows.slice(2).map(parseRow);
  const n = Math.max(
    header.length,
    body.reduce((m, r) => Math.max(m, r.length), 0),
  );
  const pad = (r) => {
    const x = r.slice();
    while (x.length < n) x.push("");
    return x;
  };
  const th = pad(header);
  const br = body.map(pad);
  let h = "<div class=\"chat-md-table-wrap\"><table class=\"chat-md-table\"><thead><tr>";
  th.forEach((c) => {
    h += `<th>${gfmTableCell(c)}</th>`;
  });
  h += "</tr></thead><tbody>";
  br.forEach((row) => {
    h += "<tr>";
    row.forEach((c) => {
      h += `<td>${gfmTableCell(c)}</td>`;
    });
    h += "</tr>";
  });
  h += "</tbody></table></div>";
  return h;
}

/** After fenced code: replace GFM tables with placeholders. */
function extractGFMTables(text, outTables) {
  const lines = text.split("\n");
  const out = [];
  let i = 0;
  while (i < lines.length) {
    if (
      i + 1 < lines.length
      && isGFMTableRow(lines[i])
      && isGFMTableSeparator(lines[i + 1])
    ) {
      const block = [lines[i], lines[i + 1]];
      i += 2;
      while (i < lines.length && isGFMTableRow(lines[i]) && !isGFMTableSeparator(lines[i])) {
        block.push(lines[i]);
        i += 1;
      }
      const idx = outTables.length;
      outTables.push(gfmTableBlockToHTML(block));
      out.push(`@@GFMTABLE_${idx}@@`);
      continue;
    }
    out.push(lines[i]);
    i += 1;
  }
  return out.join("\n");
}

function addFastTapListener(el, handler) {
  if (!el) return;
  let lastTriggerTime = 0;
  const run = (e) => {
    const now = Date.now();
    if (now - lastTriggerTime < 300) return;
    lastTriggerTime = now;
    handler(e);
  };
  el.addEventListener("pointerdown", (e) => {
    if (e.pointerType === "mouse" && e.button !== 0) return;
    if (e.pointerType === "touch" || e.pointerType === "pen") {
      run(e);
    }
  }, { passive: true });
  el.addEventListener("click", (e) => {
    run(e);
  });
}

function scheduleRenderChatMessages() {
  if (chatRenderRaf != null) return;
  chatRenderRaf = requestAnimationFrame(() => {
    chatRenderRaf = null;
    renderChatMessages();
    scrollChatToBottom();
    scrollActiveBlocks();
  });
}

function flushChatRender() {
  if (chatRenderRaf != null) {
    cancelAnimationFrame(chatRenderRaf);
    chatRenderRaf = null;
  }
  renderChatMessages();
  scrollChatToBottom();
  scrollActiveBlocks();
}

function renderMarkdownSafe(input) {
  const text = String(input || "").replace(/\r\n/g, "\n");
  const codeBlocks = [];
  let work = text.replace(/```([\w-]+)?\n([\s\S]*?)```/g, (_m, _lang, code) => {
    const key = `@@CODEBLOCK_${codeBlocks.length}@@`;
    const langLabel = _lang ? escapeHtml(_lang) : "";
    codeBlocks.push(`<div class="chat-code-wrap"><div class="chat-code-header"><span class="chat-code-lang">${langLabel}</span><button type="button" class="chat-code-copy-btn" data-code="${escapeHtml(code)}" title="${escapeHtml(t("chat.copy_code"))}">${escapeHtml(t("chat.copy_code"))}</button></div><pre class="chat-code"><code>${escapeHtml(code)}</code></pre></div>`);
    return key;
  });
  const tableBlocks = [];
  work = extractGFMTables(work, tableBlocks);

  const mathBlocks = [];
  work = work.replace(/\$\$([\s\S]*?)\$\$/g, (_m, math) => {
    const key = `@@MATHDISP_${mathBlocks.length}@@`;
    mathBlocks.push({ type: "display", math: math.trim() });
    return key;
  });
  work = work.replace(/\$([^$\n]+?)\$/g, (_m, math) => {
    const key = `@@MATHINLINE_${mathBlocks.length}@@`;
    mathBlocks.push({ type: "inline", math: math.trim() });
    return key;
  });

  let html = escapeHtml(work);
  html = html.replace(/`([^`\n]+)`/g, "<code>$1</code>");
  html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  html = html.replace(/\*([^*\n]+)\*/g, "<em>$1</em>");
  html = html.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, `<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>`);
  html = html.replace(/^###\s+(.+)$/gm, "<h4>$1</h4>");
  html = html.replace(/^##\s+(.+)$/gm, "<h3>$1</h3>");
  html = html.replace(/^#\s+(.+)$/gm, "<h2>$1</h2>");

  const lines = html.split("\n");
  const out = [];
  let inList = false;
  let quoteLines = [];

  function flushQuote() {
    if (!quoteLines.length) return;
    const cleanText = quoteLines.map((l) => String(l || "")
      .replace(/<[^>]+>/g, "")
      .replace(/&quot;/g, '"')
      .replace(/&#39;/g, "'")
      .replace(/&lt;/g, "<")
      .replace(/&gt;/g, ">")
      .replace(/&amp;/g, "&")
    ).join("\n").trim();
    const innerHtml = quoteLines.map((l) => l.trim()).join("<br>");
    out.push(`<div class="chat-quote-wrap"><div class="chat-quote-header"><span class="chat-quote-badge"><svg class="chat-quote-icon" viewBox="0 0 24 24" width="13" height="13" fill="currentColor" aria-hidden="true"><path d="M4.583 17.321C3.553 16.227 3 15 3 13.011c0-3.5 2.457-6.637 6.03-8.188l.893 1.378c-3.335 1.804-3.987 4.145-4.247 5.621.537-.278 1.24-.375 1.929-.311 1.804.167 3.226 1.648 3.226 3.489a3.5 3.5 0 01-3.5 3.5c-1.073 0-2.099-.49-2.748-1.179zm10 0C13.553 16.227 13 15 13 13.011c0-3.5 2.457-6.637 6.03-8.188l.893 1.378c-3.335 1.804-3.987 4.145-4.247 5.621.537-.278 1.24-.375 1.929-.311 1.804.167 3.226 1.648 3.226 3.489a3.5 3.5 0 01-3.5 3.5c-1.073 0-2.099-.49-2.748-1.179z"/></svg><span>${escapeHtml(t("chat.quote"))}</span></span><button type="button" class="chat-quote-copy-btn" data-quote="${escapeHtml(cleanText)}" title="${escapeHtml(t("chat.copy_quote"))}"><svg class="chat-quote-copy-ic" viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg><span>${escapeHtml(t("chat.copy_quote"))}</span></button></div><blockquote class="chat-quote-content">${innerHtml}</blockquote></div>`);
    quoteLines = [];
  }

  for (const line of lines) {
    const trimmed = line.trim();
    if (/^&gt;\s?(.*)$/.test(trimmed)) {
      if (inList) {
        out.push("</ul>");
        inList = false;
      }
      const match = trimmed.match(/^&gt;\s?(.*)$/);
      quoteLines.push(match ? match[1] : "");
      continue;
    }
    if (quoteLines.length) {
      flushQuote();
    }
    if (/^[-*]\s+/.test(trimmed)) {
      if (!inList) {
        out.push("<ul>");
        inList = true;
      }
      out.push(`<li>${trimmed.replace(/^[-*]\s+/, "")}</li>`);
      continue;
    }
    if (inList) {
      out.push("</ul>");
      inList = false;
    }
    if (trimmed === "") {
      out.push("");
    } else if (/^([-*_]){3,}$/.test(trimmed)) {
      out.push(`<hr class="chat-hr" />`);
    } else if (/^@@GFMTABLE_(\d+)@@$/.test(trimmed)) {
      const m = trimmed.match(/^@@GFMTABLE_(\d+)@@$/);
      const tIdx = m ? Number(m[1]) : -1;
      if (tIdx >= 0 && tIdx < tableBlocks.length) {
        out.push(tableBlocks[tIdx]);
      } else {
        out.push("<p></p>");
      }
    } else if (/^@@CODEBLOCK_\d+@@$/.test(trimmed)) {
      out.push(trimmed);
    } else if (/^<h[234]>/.test(trimmed)) {
      out.push(trimmed);
    } else {
      out.push(`<p>${trimmed}</p>`);
    }
  }
  if (inList) out.push("</ul>");
  if (quoteLines.length) flushQuote();
  html = out.join("\n").replace(/\n{3,}/g, "\n\n");

  codeBlocks.forEach((block, i) => {
    html = html.replace(`@@CODEBLOCK_${i}@@`, block);
  });
  mathBlocks.forEach((block, i) => {
    if (block.type === "display") {
      html = html.replace(`@@MATHDISP_${i}@@`, `<span class="math-display">${escapeHtml(block.math)}</span>`);
    } else {
      html = html.replace(`@@MATHINLINE_${i}@@`, `<span class="math-inline">${escapeHtml(block.math)}</span>`);
    }
  });
  return html;
}

function renderChatMath(container) {
  if (typeof katex === "undefined") return;
  container.querySelectorAll(".math-inline").forEach((el) => {
    try {
      katex.render(el.textContent, el, { throwOnError: false, displayMode: false });
    } catch (e) { /* ignore */ }
  });
  container.querySelectorAll(".math-display").forEach((el) => {
    try {
      katex.render(el.textContent, el, { throwOnError: false, displayMode: true });
    } catch (e) { /* ignore */ }
  });
}

function splitThink(raw) {
  const text = String(raw || "");
  const open = text.indexOf("<think>");
  if (open === -1) {
    return { think: "", answer: text.replace(/<\/?think>/g, ""), inThink: false };
  }
  const close = text.indexOf("</think>", open + 7);
  if (close === -1) {
    return {
      think: text.slice(open + 7),
      answer: (text.slice(0, open)).replace(/<\/?think>/g, ""),
      inThink: true,
    };
  }
  const before = text.slice(0, open);
  const think = text.slice(open + 7, close);
  const after = text.slice(close + 8);
  const sub = splitThink(after);
  let cleanAnswer = (before).replace(/<\/?think>/g, "") + sub.answer;
  if (!before.trim()) {
    cleanAnswer = cleanAnswer.replace(/^[\r\n]+/, "");
  }
  return {
    think: think + (sub.think ? "\n" + sub.think : ""),
    answer: cleanAnswer,
    inThink: sub.inThink,
  };
}

function splitThinkSegment(seg, wasInThink) {
  const text = String(seg || "");

  if (wasInThink) {
    const close = text.indexOf("</think>");
    if (close === -1) {
      return {
        think: text,
        answer: "",
        inThink: true,
        closePrevious: false,
      };
    } else {
      const thinkPart = text.slice(0, close);
      const remaining = text.slice(close + 8);
      const sub = splitThinkSegment(remaining, false);
      let cleanAns = sub.answer.replace(/^[\r\n]+/, "");
      return {
        think: thinkPart + (sub.think ? "\n" + sub.think : ""),
        answer: cleanAns,
        inThink: sub.inThink,
        closePrevious: true,
      };
    }
  }

  const open = text.indexOf("<think>");
  if (open === -1) {
    return {
      think: "",
      answer: text.replace(/<\/?think>/g, ""),
      inThink: false,
      closePrevious: false,
    };
  }

  const before = text.slice(0, open);
  const close = text.indexOf("</think>", open + 7);
  if (close === -1) {
    return {
      think: text.slice(open + 7),
      answer: before.replace(/<\/?think>/g, ""),
      inThink: true,
      closePrevious: false,
    };
  } else {
    const think = text.slice(open + 7, close);
    const after = text.slice(close + 8);
    const sub = splitThinkSegment(after, false);
    let cleanAns = before.replace(/<\/?think>/g, "") + sub.answer;
    if (!before.trim()) {
      cleanAns = cleanAns.replace(/^[\r\n]+/, "");
    }
    return {
      think: think + (sub.think ? "\n" + sub.think : ""),
      answer: cleanAns,
      inThink: sub.inThink,
      closePrevious: false,
    };
  }
}

function thinkLabel(ms, streaming) {
  const dur = formatMetaElapsed(ms || 0);
  if (streaming) return t("chat.think_running", { t: dur });
  return t("chat.think_done", { t: dur });
}

function formatDoneReason(reason) {
  if (!reason) return "";
  const r = String(reason).toLowerCase().trim();
  if (r === "stop") return t("chat.done_reason.stop") || "stop";
  if (r === "length") return t("chat.done_reason.length") || "length limit";
  if (r === "abort" || r === "aborted" || r === "stopped") return t("chat.done_reason.aborted") || "stopped";
  if (r === "tool_limit" || r === "tools_limit") return t("chat.done_reason.tool_limit") || "tool limit";
  if (r === "error") return t("chat.done_reason.error") || "error";
  return r;
}

function assistantMetricParts(m, opts = {}) {
  if (!m || m.role !== "assistant") return [];
  const parts = [];
  const elapsed = Math.max(0, Number(m.elapsedMs) || 0);
  const tokens = Math.max(0, Math.round(Number(m.completionTokens || m.tokens || 0)));
  const tps = Number(m.tps);
  if (elapsed > 0 || opts.showZero) parts.push(formatMetaElapsed(elapsed));
  if (tokens > 0 || opts.showZero) parts.push(t("chat.meta_tokens", { n: tokens }));
  if ((Number.isFinite(tps) && tps > 0) || opts.showZero) {
    parts.push(t("chat.meta_tps", { rate: (Number.isFinite(tps) && tps > 0 ? tps : 0).toFixed(2) }));
  }
  if (!m.streaming && (m.doneReason || m.stopped)) {
    const reason = m.stopped ? "aborted" : m.doneReason;
    const label = formatDoneReason(reason);
    if (label) parts.push(label);
  }
  if (opts.streaming) parts.push(t("chat.streaming"));
  return parts;
}

function assistantMetricText(m, opts = {}) {
  return assistantMetricParts(m, opts).join(" · ");
}

function assistantMetricHTML(m, opts = {}) {
  if (!m || m.role !== "assistant") return "";
  const parts = [];
  const elapsed = Math.max(0, Number(m.elapsedMs) || 0);
  const tokens = Math.max(0, Math.round(Number(m.completionTokens || m.tokens || 0)));
  const tps = Number(m.tps);
  if (elapsed > 0 || opts.showZero) {
    const timeText = formatMetaElapsed(elapsed);
    const timeTitle = formatMetaElapsedSecondsTitle(elapsed);
    parts.push(`<span class="chat-meta-time" title="${escapeHtml(timeTitle)}">${escapeHtml(timeText)}</span>`);
  }
  if (tokens > 0 || opts.showZero) {
    parts.push(`<span>${escapeHtml(t("chat.meta_tokens", { n: tokens }))}</span>`);
  }
  if ((Number.isFinite(tps) && tps > 0) || opts.showZero) {
    parts.push(`<span>${escapeHtml(t("chat.meta_tps", { rate: (Number.isFinite(tps) && tps > 0 ? tps : 0).toFixed(2) }))}</span>`);
  }
  if (!m.streaming && (m.doneReason || m.stopped)) {
    const reason = m.stopped ? "aborted" : m.doneReason;
    const label = formatDoneReason(reason);
    if (label) parts.push(`<span>${escapeHtml(label)}</span>`);
  }
  if (opts.streaming) parts.push(`<span>${escapeHtml(t("chat.streaming"))}</span>`);
  return parts.join(" · ");
}

/**
 * Añade al timeline el texto desde segmentFlushIndex hasta ahora, como think (y opcional bloque md).
 * @param {object} assistantMsg
 * @param {string} assistantRaw
 * @param {boolean} isFinal - si true, es el ultimo flush antes de done
 */
function flushSegmentToTimeline(assistantMsg, assistantRaw, isFinal) {
  const start = Number(assistantMsg.segmentFlushIndex) || 0;
  if (assistantRaw.length <= start) return;
  const seg = assistantRaw.slice(start);
  assistantMsg.segmentFlushIndex = assistantRaw.length;
  if (!assistantMsg.timeline) assistantMsg.timeline = [];

  const wasInThink = assistantMsg._lastSegInThink || false;
  const parts = splitThinkSegment(seg, wasInThink);
  assistantMsg._lastSegInThink = parts.inThink;

  if (parts.closePrevious) {
    let lastThink = null;
    for (let i = assistantMsg.timeline.length - 1; i >= 0; i--) {
      if (assistantMsg.timeline[i].type === "think") {
        lastThink = assistantMsg.timeline[i];
        break;
      }
    }
    if (lastThink) {
      if (parts.think && parts.think.trim()) {
        lastThink.think = (lastThink.think + "\n" + parts.think).trim();
      }
    } else if (parts.think && parts.think.trim()) {
      assistantMsg.timeline.push({ type: "think", think: parts.think, segId: nanoid() });
    }
  } else {
    if (parts.think && parts.think.trim()) {
      assistantMsg.timeline.push({ type: "think", think: parts.think, segId: nanoid() });
    }
  }

  if (parts.answer && parts.answer.trim()) {
    assistantMsg.timeline.push({ type: "md", content: parts.answer });
  }
}

/** Long tool error bodies (e.g. raw HTML) go inside a &lt;details&gt; with a one-line peek. */
const TOOL_ERR_COLLAPSE_LEN = 360;

function toolErrOneLinePeek(s, max) {
  const t = String(s).replace(/\s+/g, " ").trim();
  if (t.length <= max) return t;
  return t.slice(0, max - 1) + "…";
}

function renderToolErrorBlock(err, toolIdx, msgId, open) {
  const text = String(err);
  if (text.length <= TOOL_ERR_COLLAPSE_LEN) {
    return `<div class="chat-tool-err mono">${escapeHtml(text)}</div>`;
  }
  const peek = toolErrOneLinePeek(text, 96);
  return `<details class="chat-tool-preview chat-tool-err-details" ${open ? "open" : ""} data-msg-id="${escapeHtml(msgId)}" data-tool-idx="${toolIdx}">
  <summary class="chat-tool-err-summary">
    <span class="chat-tool-err-peek mono">${escapeHtml(peek)}</span>
    <span class="chat-tool-err-expand muted">${escapeHtml(t("chat.tool.error_expand", { n: String(text.length) }))}</span>
  </summary>
  <pre class="chat-tool-err-body mono">${escapeHtml(text)}</pre>
</details>`;
}

function renderAssistantToolLogEntry(e, toolIdx, msgId) {
  const isSearch = e.name === "web_search";
  const isFetch = e.name === "web_fetch";
  const isWrite = e.name === "write_file";
  const isRead = e.name === "read_file";
  const isList = e.name === "list_dir";
  const isExec = e.name === "exec";
  const isCreateArt = e.name === "create_artifact";
  const isScreenshot = e.name === "take_artifact_screenshot";
  const isEval = e.name === "eval_artifact_js";
  const title = isSearch ? t("chat.tool.web_search")
    : isFetch ? t("chat.tool.web_fetch")
      : isWrite ? t("chat.tool.write_file")
        : isRead ? t("chat.tool.read_file")
          : isList ? t("chat.tool.list_dir")
            : isExec ? t("chat.tool.exec")
              : isCreateArt ? t("chat.tool.create_artifact")
                : isScreenshot ? t("chat.tool.take_artifact_screenshot")
                  : isEval ? t("chat.tool.eval_artifact_js")
                    : escapeHtml(e.name);
  let detailHtml = "";
  if (isSearch && e.query) {
    let d = escapeHtml(e.query);
    if (e.max_results) d += ` · ${escapeHtml(t("chat.tool.max_results", { n: e.max_results }))}`;
    detailHtml = `<div class="chat-tool-detail mono">${d}</div>`;
  } else if (isFetch && e.url) {
    const u = escapeHtml(e.url);
    detailHtml = `<div class="chat-tool-detail"><a href="${u}" target="_blank" rel="noopener noreferrer" class="chat-tool-link mono">${u}</a></div>`;
  } else if ((isWrite || isRead || isList) && e.path) {
    detailHtml = `<div class="chat-tool-detail mono">${escapeHtml(e.path)}</div>`;
  } else if (isExec && e.command) {
    detailHtml = `<div class="chat-tool-detail mono">${escapeHtml(e.command)}</div>`;
  } else if (isEval && e.code) {
    detailHtml = `<div class="chat-tool-detail mono">${escapeHtml(e.code)}</div>`;
  } else if (isCreateArt && e.artifact_name) {
    let d = escapeHtml(e.artifact_name);
    if (e.description) d += ` — <span class="muted">${escapeHtml(e.description)}</span>`;
    const msg = chatMessages.find((x) => x.id === msgId);
    const timestamp = (msg && msg.artifactTimestamp) || activeArtifactTimestamp;
    if (timestamp) {
      d += ` <span class="chat-tool-runes mono" style="margin-left: 8px;">[folder: ${timestamp}]</span>`;
    }
    detailHtml = `<div class="chat-tool-detail">${d}</div>`;
  }
  if (e.image) {
    const imgName = t("chat.tool.take_artifact_screenshot") || "Screenshot";
    detailHtml += `<div class="chat-tool-img-wrap">
      <button type="button" class="image-preview-open chat-tool-img-thumb" data-name="${escapeHtml(imgName)}" title="${escapeHtml(imgName)}">
        <img src="${e.image}" alt="${escapeHtml(imgName)}" onload="if (typeof scrollChatToBottom === 'function') scrollChatToBottom();" />
      </button>
    </div>`;
  }
  const st = e.status || "unknown";
  const icon = st === "generating" ? "✎" : st === "running" ? "◌" : st === "ok" ? "✓" : st === "error" ? "✗" : "·";
  const titleText = title;
  const labelSuffix = st === "generating" ? "…" : "";
  let tail = "";
  if (st === "error" && e.error) {
    tail += renderToolErrorBlock(e.error, toolIdx, msgId, !!e.open);
  }
  if (st === "ok" && (e.result_preview || e.result_runes)) {
    const metaBits = [];
    if (e.result_runes) metaBits.push(t("chat.tool.chars", { n: e.result_runes }));
    const meta = metaBits.length ? `<span class="chat-tool-runes mono">${escapeHtml(metaBits.join(" · "))}</span>` : "";
    const prev = e.result_preview
      ? `<details class="chat-tool-preview" ${e.open ? "open" : ""} data-msg-id="${escapeHtml(msgId)}" data-tool-idx="${toolIdx}"><summary>${escapeHtml(t("chat.tool.result_preview"))}</summary><pre>${escapeHtml(e.result_preview)}</pre></details>`
      : "";
    tail += `<div class="chat-tool-result-head">${meta}</div>${prev}`;
  }
  return `<div class="chat-tool-line chat-tool-line--${st}"><span class="chat-tool-ic" aria-hidden="true">${icon}</span><div class="chat-tool-main"><span class="chat-tool-name">${titleText}${labelSuffix}</span>${detailHtml}${tail}</div>${(st === "running" || st === "generating") ? "<span class=\"chat-tool-pulse\" aria-hidden=\"true\"></span>" : ""}</div>`;
}

function renderAssistantToolLog(m) {
  const entries = m.toolLog || [];
  if (!entries.length) return "";
  const lines = entries.map((e, idx) => renderAssistantToolLogEntry(e, idx, m.id));
  return `<div class="chat-tool-log" role="region" aria-label="${escapeHtml(t("chat.tool.region_label"))}">${lines.join("")}</div>`;
}

function renderAssistantTimeline(m) {
  const items = m.timeline || [];
  if (!items.length) return "";
  const segs = items.map((it) => {
    if (it.type === "think") {
      const o = it.thinkOpen !== false;
      return `<details class="chat-think" ${o ? "open" : ""} data-id="${escapeHtml(m.id)}" data-tl-seg="${escapeHtml(it.segId || "")}">
          <summary>${escapeHtml(t("chat.cap.thinking"))}</summary>
          <pre>${escapeHtml(it.think)}</pre>
        </details>`;
    }
    if (it.type === "md") {
      return `<div class="chat-md chat-timeline-md">${renderMarkdownSafe(it.content || "")}</div>`;
    }
    if (it.type === "tool" && it.entry) {
      const toolIdx = (m.toolLog || []).indexOf(it.entry);
      return `<div class="chat-tool-log chat-tool-log--tl" role="region" aria-label="${escapeHtml(t("chat.tool.region_label"))}">${renderAssistantToolLogEntry(it.entry, toolIdx, m.id)}</div>`;
    }
    return "";
  });
  return `<div class="chat-timeline">${segs.join("")}</div>`;
}

function buildAssistantDebugFooter(m) {
  if (m.role !== "assistant" || m.streaming || !m.hasDebug) return "";
  const used = Math.max(0, Number(m.promptTokens) || 0);
  if (!used) return "";
  const maxCtx = Math.max(0, Number(m.contextMax) || 0);
  let ctxLine;
  if (maxCtx > 0) {
    const pct = Math.min(999, Math.round((used / maxCtx) * 100));
    ctxLine = t("chat.debug_context", { used: String(used), max: fmtCtx(maxCtx), pct });
  } else {
    ctxLine = t("chat.debug_context_plain", { used: String(used), max: "—" });
  }
  return `<footer class="chat-debug mono">${escapeHtml(ctxLine)}</footer>`;
}

function bindMessageDetails(el, m) {
  if (!el) return;
  el.querySelectorAll("details.chat-think").forEach((dt) => {
    dt.addEventListener("toggle", () => {
      const msg = chatMessages.find((x) => x.id === dt.dataset.id);
      if (!msg) return;
      if (dt.dataset.tail === "1") {
        msg.tailThinkOpen = dt.open;
      } else if (dt.dataset.tlSeg && msg.timeline) {
        const item = msg.timeline.find((i) => i.segId === dt.dataset.tlSeg);
        if (item) item.thinkOpen = dt.open;
      } else {
        msg.thinkOpen = dt.open;
      }
    });
  });

  el.querySelectorAll("details.chat-tool-preview").forEach((dt) => {
    dt.addEventListener("toggle", () => {
      const msg = chatMessages.find((x) => x.id === dt.dataset.msgId);
      if (!msg) return;
      const toolIdx = parseInt(dt.dataset.toolIdx, 10);
      if (msg.toolLog && msg.toolLog[toolIdx]) {
        msg.toolLog[toolIdx].open = dt.open;
      }
    });
  });
}

function renderSingleChatMessageHTML(m, i, lastUserMsgIdx) {
  const meta = [];
  if (m.role === "assistant" && !m.streaming && m.stopped) {
    meta.push(t("chat.stopped_badge_short"));
  }

  const isEditingUser = m.role === "user" && m.id === chatEditingMessageId;
  const canEditUser = m.role === "user" && !chatStreamLock && i === lastUserMsgIdx;

  const files = isEditingUser ? "" : (m.attachments || []).map((a) => {
    if (a.kind === "image" && a.data) {
      const src = attachmentImageSrc(a);
      if (!src) {
        return `<span class="chat-file-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)}</span>`;
      }
      return `<div class="chat-file-item chat-file-item-image">
        <button type="button" class="image-preview-open chat-msg-file-thumb" data-name="${escapeHtml(a.name)}">
          <img src="${src}" alt="" />
        </button>
        <span class="chat-file-name mono">${escapeHtml(a.name)}</span>
      </div>`;
    }
    if (a.kind === "audio" && a.data) {
      const src = attachmentAudioSrc(a);
      if (!src) {
        return `<span class="chat-file-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)}</span>`;
      }
      return `<div class="chat-file-item chat-file-item-audio">
        <audio class="chat-audio-player" controls preload="metadata" src="${src}"></audio>
        <span class="chat-file-name mono">${escapeHtml(a.name)}</span>
      </div>`;
    }
    if (a.kind === "text") {
      const prev = attachmentTextPreview(a);
      return `<div class="chat-file-item chat-file-item-text">
        <div class="chat-text-snippet mono">${escapeHtml(prev || "text file")}</div>
        <span class="chat-file-name mono">${escapeHtml(a.name)}</span>
      </div>`;
    }
    return `<span class="chat-file-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)}</span>`;
  }).join("");

  const hasTl = m.role === "assistant" && m.timeline && m.timeline.length > 0;
  const acc = m._accRaw || "";
  const flushI = hasTl ? (Number(m.segmentFlushIndex) || 0) : 0;
  const tailStr = hasTl && m.streaming && acc && acc.length > flushI ? acc.slice(flushI) : "";
  const tailParts = tailStr ? splitThink(tailStr) : { think: "", inThink: false, answer: "" };
  const showTailThink = hasTl && (Boolean((tailParts.think || "").trim()) || (m.streaming && tailParts.inThink));
  const showTailMd = hasTl && m.streaming && Boolean((tailParts.answer || "").trim());

  const thinkSecTitle = formatMetaElapsedSecondsTitle(m.thinkMs || 0);
  const thinkBlock = hasTl || !m.thinkContent
    ? ""
    : `<details class="chat-think" ${m.thinkOpen ? "open" : ""} data-id="${escapeHtml(m.id)}">
        <summary title="${escapeHtml(thinkSecTitle)}">${escapeHtml(thinkLabel(m.thinkMs || 0, !!m.streaming && !!m.inThink))}</summary>
        <pre>${escapeHtml(m.thinkContent)}</pre>
      </details>`;

  const toolLogBlock = m.role === "assistant" && !hasTl && m.toolLog?.length
    ? renderAssistantToolLog(m)
    : "";
  const timelineBlock = m.role === "assistant" && hasTl
    ? renderAssistantTimeline(m)
    : "";
  const tailThinkBlock = showTailThink
    ? `<details class="chat-think" ${m.tailThinkOpen !== false ? "open" : ""} data-id="${escapeHtml(m.id)}" data-tail="1">
        <summary title="${escapeHtml(thinkSecTitle)}">${escapeHtml(thinkLabel(m.thinkMs || 0, !!m.streaming && tailParts.inThink))}</summary>
        <pre>${escapeHtml(tailParts.think || "")}</pre>
      </details>`
    : "";
  const tailMdBlock = showTailMd
    ? `<div class="chat-md chat-timeline-md">${renderMarkdownSafe(tailParts.answer || "")}</div>`
    : "";

  let bodyHTML = "";
  const isImageModel = m.role === "assistant" && m.model && modelCaps(m.model).has("image");
  if (m.role === "assistant") {
    if (isImageModel) {
      if (m.streaming) {
        let progressInfo = "";
        if (m.completedSteps != null && m.totalSteps) {
          const pct = Math.min(100, Math.round((m.completedSteps / m.totalSteps) * 100));
          progressInfo = `<div class="chat-image-progress-text">Step ${m.completedSteps}/${m.totalSteps} (${pct}%)</div>
          <div class="chat-image-progress-bar-wrap">
            <div class="chat-image-progress-bar" style="width: ${pct}%"></div>
          </div>`;
        }
        bodyHTML = `<div class="chat-image-generating-card">
          <div class="chat-image-generating">
            <span class="chat-tool-ic chat-tool-pulse"></span>
            <span>${escapeHtml(t("chat.generating_image"))}</span>
          </div>
          ${progressInfo}
        </div>`;
      } else {
        const cleanedContent = String(m.content || "").replace(/\s+/g, "");
        const isError = m.isError || String(m.content || "").startsWith("Error:") || String(m.content || "").startsWith("Error ");
        if (isError) {
          bodyHTML = renderMarkdownSafe(m.content || "");
        } else if (cleanedContent) {
          const imgSrc = `data:image/png;base64,${cleanedContent}`;
          const imgName = `${m.model.replace(/[^a-zA-Z0-9]/g, "_")}_${m.id}.png`;
          bodyHTML = `<div class="chat-generated-image-container">
            <button type="button" class="image-preview-open chat-generated-image-thumb" data-name="${escapeHtml(imgName)}">
              <img src="${imgSrc}" alt="${escapeHtml(imgName)}" class="chat-generated-image" />
            </button>
            <div class="chat-generated-image-actions">
              <a href="${imgSrc}" download="${escapeHtml(imgName)}" class="btn-icon download-generated-image-btn" title="${escapeHtml(t("chat.download_image"))}">
                <svg class="chat-download-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" width="18" height="18">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
                  <polyline points="7 10 12 15 17 10"/>
                  <line x1="12" y1="15" x2="12" y2="3"/>
                </svg>
              </a>
            </div>
          </div>`;
        } else {
          bodyHTML = `<p class="muted">${escapeHtml(t("state.error_prefix"))} Empty response</p>`;
        }
      }
    } else if (m.isError) {
      bodyHTML = `<div class="chat-msg-error"><span class="chat-msg-error-icon">⚠️</span><div class="chat-msg-error-text">${renderMarkdownSafe(m.content || "")}</div></div>`;
      if (m.isOom && m.suggestedPct > 0) {
        const curTokens = m.effectiveCtx > 0 ? ` (${fmtCtx(m.effectiveCtx)})` : "";
        const sugTokens = m.suggestedCtx > 0 ? ` (${fmtCtx(m.suggestedCtx)})` : "";
        bodyHTML += `<div class="chat-oom-hint">
          <p class="chat-oom-hint-text">${escapeHtml(t("chat.oom_reduce_hint", {
            pct: m.effectivePct,
            tokens: curTokens,
            suggestedPct: m.suggestedPct,
            suggestedTokens: sugTokens,
          }))}</p>
          <button type="button" class="chat-oom-retry-btn" data-msg-id="${escapeHtml(m.id)}" data-suggested-pct="${m.suggestedPct}">${escapeHtml(t("chat.oom_retry", { pct: m.suggestedPct }))}</button>
        </div>`;
      }
    } else {
      bodyHTML = renderMarkdownSafe(m.content || "");
    }
  }
  if (isEditingUser) {
    bodyHTML = `<div class="chat-edit-box" data-msg-id="${escapeHtml(m.id)}">
  <div class="chat-edit-attachments" id="chat-edit-attachments">
    ${renderEditAttachmentsHTML(chatEditingAttachments)}
  </div>
  <textarea class="chat-edit-textarea" data-msg-id="${escapeHtml(m.id)}" placeholder="${escapeHtml(t("chat.input_placeholder") || "Write your message…")}">${escapeHtml(chatEditingDraft)}</textarea>
  <div class="chat-edit-actions">
    <div class="chat-edit-upload-actions">
      <button type="button" class="btn-icon chat-edit-add-file-btn" data-msg-id="${escapeHtml(m.id)}" title="${escapeHtml(t("chat.add_image") || "Add file / image")}">
        <svg class="chat-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16">
          <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/>
        </svg>
      </button>
      <input type="file" class="chat-edit-add-file-input" hidden accept="image/*,audio/*,text/*,.txt,.md,.json,.js,.ts,.go,.py,.css,.html,.c,.cpp,.h,.rs,.yaml,.yml,.toml" multiple />
    </div>
    <div class="chat-edit-btn-group">
      <button type="button" class="chat-edit-save primary" data-msg-id="${escapeHtml(m.id)}">${escapeHtml(t("chat.edit_save") || "Save")}</button>
      <button type="button" class="chat-edit-cancel ghost">${escapeHtml(t("chat.edit_cancel") || "Cancel")}</button>
    </div>
  </div>
</div>`;
  } else if (m.role === "user") {
    bodyHTML = `<p>${escapeHtml(m.content || "")}</p>`;
  }
  const roleLabel = m.role === "user" ? t("chat.role_user") : t("chat.role_assistant");
  const modelLabel = m.role === "assistant" && m.model
    ? `<span class="chat-model-used mono">${escapeHtml(m.model)}</span>`
    : "";
  const hideActions = (m.role === "assistant" && m.streaming) || (m.role === "assistant" && isImageModel) || isEditingUser;
  const ttsPlaying = m.id === speakingMsgId;
  const ttsLabel = ttsPlaying ? t("chat.tts_stop") : t("chat.tts_play");
  const ttsBtn = hideActions ? "" : `<button type="button" class="btn-icon chat-tts-btn${ttsPlaying ? " active" : ""}" data-msg-id="${escapeHtml(m.id)}" title="${escapeHtml(ttsLabel)}" aria-label="${escapeHtml(ttsLabel)}">
<svg class="chat-tts-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
<path d="M11 5L6 9H3v6h3l5 4V5z"/>
<path d="M15.5 9.5a4 4 0 0 1 0 5"/>
<path d="M18.5 7a8 8 0 0 1 0 10"/>
</svg></button>`;
  const copyLabel = m.role === "user" ? t("chat.copy_user") : t("chat.copy_assistant");
  const copyBtn = hideActions ? "" : `<button type="button" class="btn-icon chat-copy-btn" data-msg-id="${escapeHtml(m.id)}" title="${escapeHtml(copyLabel)}" aria-label="${escapeHtml(copyLabel)}">
<svg class="chat-copy-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
<rect x="9" y="9" width="11" height="11" rx="2"/>
<path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
</svg></button>`;
  const quoteLabel = t("chat.quote");
  const quoteBtn = hideActions ? "" : `<button type="button" class="btn-icon chat-quote-btn" data-msg-id="${escapeHtml(m.id)}" title="${escapeHtml(quoteLabel)}" aria-label="${escapeHtml(quoteLabel)}">
<svg class="chat-quote-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  <path d="M3 21c3 0 7-1 7-8V5c0-1.25-.75-2-2-2H4c-1.25 0-2 .75-2 2v6c0 1.25.75 2 2 2 1.5 0 2 .5 2 2 0 2-2 3-5 4"/>
  <path d="M15 21c3 0 7-1 7-8V5c0-1.25-.75-2-2-2h-4c-1.25 0-2 .75-2 2v6c0 1.25.75 2 2 2 1.5 0 2 .5 2 2 0 2-2 3-5 4"/>
</svg></button>`;
  const editBtn = !hideActions && canEditUser
    ? `<button type="button" class="btn-icon chat-edit-btn" data-msg-id="${escapeHtml(m.id)}" title="${escapeHtml(t("chat.edit_title"))}" aria-label="${escapeHtml(t("chat.edit"))}">
<svg class="chat-edit-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  <path d="M12 20h9"/>
  <path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/>
</svg></button>`
    : "";
  const isLast = i === chatMessages.length - 1;
  const canRegen = m.role === "assistant" && isLast && !m.streaming;
  const regenBtn = canRegen
    ? `<button type="button" class="btn-icon chat-regenerate-btn" data-msg-id="${escapeHtml(m.id)}" title="${escapeHtml(t("chat.regenerate_title"))}" aria-label="${escapeHtml(t("chat.regenerate"))}">
<svg class="chat-regenerate-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
<path d="M21 12a9 9 0 1 1-3-6.7"/><path d="M21 3v7h-7"/>
</svg></button>`
    : "";

  const footActions = `${editBtn}${regenBtn}${ttsBtn}${quoteBtn}${copyBtn}`;
  const finalMetricsHTML = m.role === "assistant" && !m.streaming ? assistantMetricHTML(m) : "";
  const finalMetricsTitle = m.role === "assistant" && !m.streaming && m.elapsedMs ? formatMetaElapsedSecondsTitle(m.elapsedMs) : "";
  const footBlock = footActions || finalMetricsHTML
    ? `<div class="chat-msg-foot">
        ${finalMetricsHTML ? `<span class="chat-msg-final-meta mono"${finalMetricsTitle ? ` title="${escapeHtml(finalMetricsTitle)}"` : ""}>${finalMetricsHTML}</span>` : ""}
        <div class="chat-msg-foot-actions">
          ${footActions}
        </div>
      </div>`
    : "";
  const artifactBadge = m.role === "assistant" && (m.artifactUrl || m.artifactGenerating)
    ? `<div class="chat-artifact-badge">
         <span class="chat-artifact-badge-icon">✦</span>
         <span class="chat-artifact-badge-name">${escapeHtml(m.artifactName || "Artifact")}</span>
         ${m.artifactUrl ? `<button type="button" class="btn-icon chat-artifact-open-btn" data-artifact-url="${escapeHtml(m.artifactUrl)}" data-artifact-name="${escapeHtml(m.artifactName || "Artifact")}" title="${escapeHtml(t("chat.artifact.open"))}" aria-label="${escapeHtml(t("chat.artifact.open"))}">↗</button>` : ""}
       </div>`
    : "";
  const streamCls = m.role === "assistant" && m.streaming ? " chat-streaming" : "";
  let contentBlock;
  if (hasTl) {
    // Timeline mode: all think/md/tool segments are in the timeline in order.
    // Add tail (streaming think + md) after the timeline.
    contentBlock = `${timelineBlock}${tailThinkBlock}${tailMdBlock}`;
  } else {
    // Simple mode: no timeline, render think + toolLog + body in order.
    contentBlock = `${thinkBlock}${toolLogBlock}<div class="chat-md">${bodyHTML || "<p></p>"}</div>`;
  }
  return `
    <article class="chat-msg ${m.role === "user" ? "chat-user" : "chat-assistant"}${streamCls}" data-id="${escapeHtml(m.id)}">
      <header class="chat-msg-head">
        <div class="chat-msg-head-main">
          <span class="chat-role">${escapeHtml(roleLabel)}</span>
          ${modelLabel}
        </div>
      </header>
      ${meta.length ? `<div class="chat-msg-meta-line"><span class="chat-meta mono">${escapeHtml(meta.join(" · "))}</span></div>` : ""}
      ${files ? `<div class="chat-file-list">${files}</div>` : ""}
      ${contentBlock}
      ${artifactBadge}
      ${footBlock}
    </article>
  `;
}

function renderChatMessages() {
  const host = $("chat-messages");
  if (!chatMessages.length) {
    host.innerHTML = `<div class="chat-empty muted">${escapeHtml(t("chat.empty"))}</div>`;
    return;
  }

  const emptyEl = host.querySelector(".chat-empty");
  if (emptyEl) emptyEl.remove();

  let lastUserMsgIdx = -1;
  for (let k = chatMessages.length - 1; k >= 0; k--) {
    if (chatMessages[k].role === "user") {
      lastUserMsgIdx = k;
      break;
    }
  }

  const existingElements = new Map();
  Array.from(host.querySelectorAll(":scope > article.chat-msg")).forEach((el) => {
    if (el.dataset.id) {
      existingElements.set(el.dataset.id, el);
    }
  });

  const currentIds = new Set(chatMessages.map((m) => m.id));

  // Remove elements no longer in chatMessages
  existingElements.forEach((el, id) => {
    if (!currentIds.has(id)) {
      el.remove();
      existingElements.delete(id);
    }
  });

  const tempContainer = document.createElement("div");

  chatMessages.forEach((m, i) => {
    const newHtml = renderSingleChatMessageHTML(m, i, lastUserMsgIdx).trim();
    const existingEl = existingElements.get(m.id);

    if (!existingEl) {
      tempContainer.innerHTML = newHtml;
      const newEl = tempContainer.firstElementChild;
      if (newEl) {
        newEl.dataset.renderedHtml = newHtml;
        const nextSibling = host.children[i] || null;
        host.insertBefore(newEl, nextSibling);
        existingElements.set(m.id, newEl);
        bindMessageDetails(newEl, m);
        renderChatMath(newEl);
      }
    } else {
      const isStreaming = Boolean(m.role === "assistant" && m.streaming);
      const htmlChanged = existingEl.dataset.renderedHtml !== newHtml;

      if (isStreaming || htmlChanged) {
        tempContainer.innerHTML = newHtml;
        const newEl = tempContainer.firstElementChild;
        if (newEl) {
          newEl.dataset.renderedHtml = newHtml;
          host.replaceChild(newEl, existingEl);
          existingElements.set(m.id, newEl);
          bindMessageDetails(newEl, m);
          renderChatMath(newEl);
        }
      }

      if (host.children[i] !== existingElements.get(m.id)) {
        host.insertBefore(existingElements.get(m.id), host.children[i] || null);
      }
    }
  });

  if (typeof saveActiveChatSession === "function") {
    saveActiveChatSession();
  }
}

function renderAttachments() {
  const box = $("chat-attachments");
  if (!chatAttachments.length) {
    box.hidden = true;
    box.innerHTML = "";
    if (typeof saveActiveChatSession === "function") {
      saveActiveChatSession();
    }
    return;
  }
  box.hidden = false;
  box.innerHTML = chatAttachments.map((a) => {
    if (a.kind === "image") {
      const src = attachmentImageSrc(a);
      if (!src) {
        return `<span class="chat-attach-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)} <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button></span>`;
      }
      return `<div class="chat-attach-card">
        <button type="button" class="image-preview-open chat-attach-thumb" data-name="${escapeHtml(a.name)}" title="${escapeHtml(t("chat.image_preview_title"))}">
          <img src="${src}" alt="" />
        </button>
        <div class="chat-attach-foot">
          <span class="chat-attach-name mono" title="${escapeHtml(a.name)}">${escapeHtml(a.name)}</span>
          <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button>
        </div>
      </div>`;
    }
    if (a.kind === "audio" && a.data) {
      const src = attachmentAudioSrc(a);
      if (!src) {
        return `<span class="chat-attach-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)} <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button></span>`;
      }
      return `<div class="chat-attach-card chat-attach-card-audio">
        <audio class="chat-audio-player" controls preload="metadata" src="${src}"></audio>
        <div class="chat-attach-foot">
          <span class="chat-attach-name mono" title="${escapeHtml(a.name)}">${escapeHtml(a.name)}</span>
          <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button>
        </div>
      </div>`;
    }
    if (a.kind === "text") {
      const prev = attachmentTextPreview(a);
      return `<div class="chat-attach-card chat-attach-card-text">
        <div class="chat-text-snippet mono">${escapeHtml(prev || "text file")}</div>
        <div class="chat-attach-foot">
          <span class="chat-attach-name mono" title="${escapeHtml(a.name)}">${escapeHtml(a.name)}</span>
          <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button>
        </div>
      </div>`;
    }
    return `<span class="chat-attach-pill">${escapeHtml(a.kind)} · ${escapeHtml(a.name)} <button type="button" class="btn-icon chat-attach-x" data-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment"))}">×</button></span>`;
  }).join("");
  box.querySelectorAll(".chat-attach-x").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      e.preventDefault();
      chatAttachments = chatAttachments.filter((x) => x.id !== btn.dataset.id);
      renderAttachments();
    });
  });
}

function renderChatQueue() {
  const host = $("chat-queue");
  if (!host) return;
  if (!chatPendingQueue.length) {
    host.hidden = true;
    host.innerHTML = "";
    return;
  }
  host.hidden = false;
  const n = chatPendingQueue.length;
  const rows = chatPendingQueue.map((q, i) => {
    const hasText = String(q.text || "").trim().length > 0;
    const preview = hasText ? String(q.text).trim() : t("chat.queue_attachments_only");
    const short = preview.length > 100 ? `${preview.slice(0, 100)}…` : preview;
    const nAtt = (q.attachments || []).length;
    const fileLine = nAtt
      ? `<div class="chat-queue-files mono">${escapeHtml(t("chat.queue_files", { n: nAtt }))}</div>`
      : "";
    return `
      <div class="chat-queue-item" data-id="${escapeHtml(q.id)}">
        <div class="chat-queue-row1">
          <span class="chat-queue-n mono">${i + 1}</span>
          <p class="chat-queue-preview">${escapeHtml(short)}</p>
          <div class="chat-queue-item-actions">
            <button type="button" class="btn-icon chat-queue-send-now" data-id="${escapeHtml(q.id)}" title="${escapeHtml(t("chat.queue_send_now"))}" aria-label="${escapeHtml(t("chat.queue_send_now"))}">
              <span aria-hidden="true">⚡</span>
            </button>
            <button type="button" class="btn-icon chat-queue-x" data-id="${escapeHtml(q.id)}" title="${escapeHtml(t("chat.queue_remove"))}">×</button>
          </div>
        </div>
        ${fileLine}
      </div>`;
  }).join("");
  host.innerHTML = `
    <details class="chat-queue-details" open>
      <summary class="chat-queue-summary">
        <span class="chat-queue-chev" aria-hidden="true">▾</span>
        <span>${escapeHtml(t("chat.queue_title"))}</span>
        <span class="chat-queue-count mono">${n}</span>
      </summary>
      <div class="chat-queue-list">${rows}</div>
    </details>`;
  host.querySelectorAll(".chat-queue-send-now").forEach((btn) => {
    addFastTapListener(btn, (e) => {
      e?.stopPropagation?.();
      const id = btn.dataset.id;
      const idx = chatPendingQueue.findIndex((q) => q.id === id);
      if (idx < 0) return;
      const [item] = chatPendingQueue.splice(idx, 1);
      chatPendingQueue.unshift(item);
      renderChatQueue();
      if (chatStreamLock) {
        stopChatGeneration();
      } else {
        const next = chatPendingQueue.shift();
        renderChatQueue();
        void runOneChatTurn(next.text, next.attachments);
      }
    });
  });
  host.querySelectorAll(".chat-queue-x").forEach((btn) => {
    addFastTapListener(btn, (e) => {
      e?.stopPropagation?.();
      const item = chatPendingQueue.find((q) => q.id === btn.dataset.id);
      if (item) {
        const inputEl = $("chat-input");
        if (inputEl) {
          const currentVal = inputEl.value;
          if (!currentVal.trim()) {
            inputEl.value = item.text || "";
          } else if (item.text) {
            inputEl.value = currentVal + "\n" + item.text;
          }
          inputEl.focus();
        }
        if (item.attachments && item.attachments.length) {
          chatAttachments = [...chatAttachments, ...item.attachments];
          renderAttachments();
        }
      }
      chatPendingQueue = chatPendingQueue.filter((q) => q.id !== btn.dataset.id);
      renderChatQueue();
    });
  });
}

function stopThinkTicker() {
  if (!chatThinkTicker) return;
  clearInterval(chatThinkTicker);
  chatThinkTicker = null;
}

function startThinkTicker(msg) {
  stopThinkTicker();
  chatThinkTicker = setInterval(() => {
    if (!msg || !msg.inThink || !msg.thinkStartedAt) return;
    msg.thinkMs = Date.now() - msg.thinkStartedAt;

    document.querySelectorAll(`details.chat-think[data-id="${msg.id}"]`).forEach((details) => {
      const summary = details.querySelector("summary");
      if (summary) {
        const isTail = details.dataset.tail === "1";
        const tailStr = msg._accRaw || "";
        const tailParts = splitThink(tailStr);
        const inThink = isTail ? tailParts.inThink : msg.inThink;
        summary.textContent = thinkLabel(msg.thinkMs, inThink);
        summary.title = formatMetaElapsedSecondsTitle(msg.thinkMs || 0);
      }
    });
  }, 250);
}

function stopStreamTicker() {
  if (!chatStreamTicker) return;
  clearInterval(chatStreamTicker);
  chatStreamTicker = null;
}

function startStreamTicker(msg, turnStartedAt) {
  stopStreamTicker();
  msg.turnStartedAt = turnStartedAt;
  msg.elapsedMs = 0;
  msg.completionTokens = 0;
  msg.tokens = 0;
  msg.tps = null;
  msg._firstTokenAt = null;
  msg._chunkCount = 0;
  msg._charCount = 0;

  chatStreamTicker = setInterval(() => {
    if (!msg) return;
    // Total processed time: complete time from beginning to now
    msg.elapsedMs = Math.max(0, Date.now() - turnStartedAt);
    updateLiveAssistantTPS(msg);
    updateStreamBar();
  }, 100);
}

function updateLiveAssistantTPS(msg) {
  if (!msg || !msg._firstTokenAt) {
    msg.tps = null;
    return;
  }
  const genDurationMs = Date.now() - msg._firstTokenAt;
  const tokens = Number(msg.completionTokens || msg.tokens) || 0;
  if (tokens > 0 && genDurationMs >= 150) {
    msg.tps = tokens / (genDurationMs / 1000);
  }
}

function resizeImageToBase64(file, maxDim = 1024) {
  return new Promise((resolve, reject) => {
    const img = new Image();
    const url = URL.createObjectURL(file);
    img.onload = () => {
      URL.revokeObjectURL(url);
      let width = img.width;
      let height = img.height;
      if (width > maxDim || height > maxDim) {
        if (width > height) {
          height = Math.round((height * maxDim) / width);
          width = maxDim;
        } else {
          width = Math.round((width * maxDim) / height);
          height = maxDim;
        }
      }
      const canvas = document.createElement("canvas");
      canvas.width = width;
      canvas.height = height;
      const ctx = canvas.getContext("2d");
      if (!ctx) {
        reject(new Error("could not get canvas 2d context"));
        return;
      }
      ctx.drawImage(img, 0, 0, width, height);
      const mime = file.type || "image/jpeg";
      const dataUrl = canvas.toDataURL(mime);
      resolve(dataUrl.includes(",") ? dataUrl.split(",")[1] : dataUrl);
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error("failed to load image for resizing"));
    };
    img.src = url;
  });
}

function toBase64(file) {
  if (file.type && file.type.startsWith("image/")) {
    return resizeImageToBase64(file, 1024);
  }
  return new Promise((resolve, reject) => {
    const fr = new FileReader();
    fr.onload = () => {
      const res = String(fr.result || "");
      resolve(res.includes(",") ? res.split(",")[1] : res);
    };
    fr.onerror = () => reject(new Error("read failed"));
    fr.readAsDataURL(file);
  });
}


async function appendFilesToEditingAttachments(files) {
  const selectedModel = $("chat-model")?.value || "";
  const caps = modelCaps(selectedModel);
  const canVision = caps.has("vision");
  const canAudio = caps.has("audio");
  const accepted = [];
  for (const file of files) {
    const type = String(file.type || "");
    if (type.startsWith("image/")) accepted.push({ file, kind: "image" });
    else if (type.startsWith("audio/") && canAudio) accepted.push({ file, kind: "audio" });
    else if (isTextAttachmentFile(file)) accepted.push({ file, kind: "text" });
  }
  if (!accepted.length) {
    toast(t("chat.attach_not_supported") || "File type not supported", "error");
    return;
  }
  for (const item of accepted) {
    if (item.file.size > 20 * 1024 * 1024) {
      toast(t("chat.file_too_large", { name: item.file.name }), "error");
      continue;
    }
    if (item.kind === "text") {
      const text = await item.file.text();
      chatEditingAttachments.push({
        id: nanoid(),
        kind: item.kind,
        name: item.file.name,
        mime: item.file.type || "text/plain",
        text,
      });
    } else {
      const data = await toBase64(item.file);
      chatEditingAttachments.push({
        id: nanoid(),
        kind: item.kind,
        name: item.file.name,
        mime: item.file.type,
        data,
      });
    }
  }
}

async function replaceEditingAttachment(attId, file) {
  const idx = chatEditingAttachments.findIndex((x) => x.id === attId);
  if (idx < 0) return;
  if (file.size > 20 * 1024 * 1024) {
    toast(t("chat.file_too_large", { name: file.name }), "error");
    return;
  }
  const type = String(file.type || "");
  let kind = "image";
  if (type.startsWith("audio/")) kind = "audio";
  else if (isTextAttachmentFile(file)) kind = "text";
  else if (type.startsWith("image/")) kind = "image";

  if (kind === "text") {
    const text = await file.text();
    chatEditingAttachments[idx] = {
      id: attId,
      kind,
      name: file.name,
      mime: file.type || "text/plain",
      text,
    };
  } else {
    const data = await toBase64(file);
    chatEditingAttachments[idx] = {
      id: attId,
      kind,
      name: file.name,
      mime: file.type,
      data,
    };
  }
}

function renderEditAttachmentsHTML(attachments) {
  if (!attachments || !attachments.length) return "";
  return attachments.map((a) => {
    let previewHTML = "";
    if (a.kind === "image" && a.data) {
      const src = attachmentImageSrc(a);
      previewHTML = `<div class="chat-edit-attach-thumb-wrap">
        <img src="${src}" alt="${escapeHtml(a.name)}" class="chat-edit-attach-thumb" />
      </div>`;
    } else if (a.kind === "audio" && a.data) {
      const src = attachmentAudioSrc(a);
      previewHTML = `<div class="chat-edit-attach-thumb-wrap chat-edit-attach-audio-wrap">
        <audio class="chat-audio-player" controls preload="metadata" src="${src}"></audio>
      </div>`;
    } else {
      const prev = attachmentTextPreview(a);
      previewHTML = `<div class="chat-edit-attach-thumb-wrap chat-edit-attach-text-wrap">
        <div class="chat-text-snippet mono">${escapeHtml(prev || "text")}</div>
      </div>`;
    }

    return `<div class="chat-edit-attach-item" data-att-id="${escapeHtml(a.id)}">
      ${previewHTML}
      <div class="chat-edit-attach-meta">
        <span class="chat-edit-attach-name mono" title="${escapeHtml(a.name)}">${escapeHtml(a.name)}</span>
        <div class="chat-edit-attach-btns">
          <button type="button" class="btn-icon chat-edit-replace-btn" data-att-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.replace_attachment") || "Replace")}" aria-label="Replace">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="14" height="14" aria-hidden="true">
              <path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/>
              <path d="M3 3v5h5"/>
              <path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16"/>
              <path d="M16 21h5v-5"/>
            </svg>
          </button>
          <button type="button" class="btn-icon chat-edit-delete-btn" data-att-id="${escapeHtml(a.id)}" title="${escapeHtml(t("chat.remove_attachment") || "Delete")}" aria-label="Delete">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="14" height="14" aria-hidden="true">
              <polyline points="3 6 5 6 21 6"/>
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
            </svg>
          </button>
          <input type="file" class="chat-edit-replace-file-input" data-att-id="${escapeHtml(a.id)}" hidden accept="image/*,audio/*,text/*,.txt,.md,.json,.js,.ts,.go,.py,.css,.html,.c,.cpp,.h,.rs,.yaml,.yml,.toml" />
        </div>
      </div>
    </div>`;
  }).join("");
}

async function addFiles(files) {
  const selectedModel = $("chat-model").value;
  const caps = modelCaps(selectedModel);
  const canVision = caps.has("vision");
  const canAudio = caps.has("audio");
  const accepted = [];
  for (const file of files) {
    const type = String(file.type || "");
    if (type.startsWith("image/") && canVision) accepted.push({ file, kind: "image" });
    if (type.startsWith("audio/") && canAudio) accepted.push({ file, kind: "audio" });
    if (isTextAttachmentFile(file)) accepted.push({ file, kind: "text" });
  }
  if (!accepted.length) {
    toast(t("chat.attach_not_supported"), "error");
    return;
  }

  for (const item of accepted) {
    if (item.file.size > 20 * 1024 * 1024) {
      toast(t("chat.file_too_large", { name: item.file.name }), "error");
      continue;
    }
    if (item.kind === "text") {
      const text = await item.file.text();
      chatAttachments.push({
        id: nanoid(),
        kind: item.kind,
        name: item.file.name,
        mime: item.file.type || "text/plain",
        text,
      });
    } else {
      const data = await toBase64(item.file);
      chatAttachments.push({
        id: nanoid(),
        kind: item.kind,
        name: item.file.name,
        mime: item.file.type,
        data,
      });
    }
  }
  renderAttachments();
}

function setRecordButtonState(isRecording) {
  const btn = $("chat-record-btn");
  if (!btn) return;
  btn.classList.toggle("is-recording", !!isRecording);
  const key = isRecording ? "chat.record_audio_stop" : "chat.record_audio_start";
  const label = t(key);
  btn.title = label;
  btn.setAttribute("aria-label", label);

  const inputEl = $("chat-input");
  if (inputEl) {
    if (isRecording) {
      inputEl.placeholder = t("chat.recording_placeholder") || "Recording audio... Speak now...";
      inputEl.classList.add("recording-active");
    } else {
      inputEl.placeholder = t("chat.input_placeholder") || "Write your message...";
      inputEl.classList.remove("recording-active");
    }
  }
}

function releaseAudioRecorder() {
  if (chatAudioProcessor) {
    try { chatAudioProcessor.disconnect(); } catch { }
    chatAudioProcessor.onaudioprocess = null;
    chatAudioProcessor = null;
  }
  if (chatAudioSource) {
    try { chatAudioSource.disconnect(); } catch { }
    chatAudioSource = null;
  }
  if (chatAudioContext) {
    try { chatAudioContext.close(); } catch { }
    chatAudioContext = null;
  }
  if (chatRecorderStream) {
    for (const tr of chatRecorderStream.getTracks()) {
      try { tr.stop(); } catch { }
    }
    chatRecorderStream = null;
  }
  chatAudioBuffers = [];
  chatAudioSampleRate = 0;
  chatIsRecording = false;
  setRecordButtonState(false);
}

async function startAudioRecording() {
  if (!navigator.mediaDevices?.getUserMedia) {
    toast(t("chat.record_audio_unsupported"), "error");
    return;
  }
  const caps = modelCaps($("chat-model").value);
  if (!caps.has("audio")) {
    toast(t("chat.attach_not_supported"), "error");
    return;
  }
  if (chatIsRecording) return;
  try {
    chatRecorderStream = await navigator.mediaDevices.getUserMedia({ audio: true });
    const AudioContextClass = window.AudioContext || window.webkitAudioContext;
    chatAudioContext = new AudioContextClass();
    if (chatAudioContext.state === "suspended") {
      await chatAudioContext.resume();
    }
    chatAudioSampleRate = chatAudioContext.sampleRate;
    chatAudioBuffers = [];
    chatAudioSource = chatAudioContext.createMediaStreamSource(chatRecorderStream);
    chatAudioProcessor = chatAudioContext.createScriptProcessor(4096, 1, 1);
    chatAudioProcessor.onaudioprocess = (event) => {
      chatAudioBuffers.push(new Float32Array(event.inputBuffer.getChannelData(0)));
    };
    chatAudioSource.connect(chatAudioProcessor);
    chatAudioProcessor.connect(chatAudioContext.destination);
    chatIsRecording = true;
    setRecordButtonState(true);
  } catch (err) {
    toast(t("toast.error", { msg: err?.message || t("chat.record_audio_error") }), "error");
    releaseAudioRecorder();
  }
}

function stopAudioRecording(silent = false) {
  if (!chatIsRecording) return;
  chatIsRecording = false;
  setRecordButtonState(false);

  if (silent) {
    if (chatAudioProcessor) {
      chatAudioProcessor.onaudioprocess = null;
    }
    releaseAudioRecorder();
    return;
  }

  const buffers = chatAudioBuffers.slice();
  const sampleRate = chatAudioSampleRate;
  releaseAudioRecorder();

  if (!buffers.length || !sampleRate) return;
  const blob = createWavBlob(buffers, sampleRate);
  if (blob.size === 0) return;

  const file = new File([blob], `recording-${Date.now()}.wav`, { type: "audio/wav" });
  addFiles([file]);
}

function createWavBlob(buffers, sampleRate) {
  if (!buffers.length || !sampleRate) return new Blob([], { type: "audio/wav" });
  const samples = mergeAudioBuffers(buffers);
  const buffer = new ArrayBuffer(44 + samples.length * 2);
  const view = new DataView(buffer);
  writeAscii(view, 0, "RIFF");
  view.setUint32(4, 36 + samples.length * 2, true);
  writeAscii(view, 8, "WAVE");
  writeAscii(view, 12, "fmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  writeAscii(view, 36, "data");
  view.setUint32(40, samples.length * 2, true);
  writePcm16(view, 44, samples);
  return new Blob([view], { type: "audio/wav" });
}

function mergeAudioBuffers(buffers) {
  const length = buffers.reduce((sum, buffer) => sum + buffer.length, 0);
  const merged = new Float32Array(length);
  let offset = 0;
  for (const buffer of buffers) {
    merged.set(buffer, offset);
    offset += buffer.length;
  }
  return merged;
}

function writePcm16(view, offset, samples) {
  for (let i = 0; i < samples.length; i++) {
    const sample = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(offset + i * 2, sample < 0 ? sample * 0x8000 : sample * 0x7fff, true);
  }
}

function writeAscii(view, offset, value) {
  for (let i = 0; i < value.length; i++) {
    view.setUint8(offset + i, value.charCodeAt(i));
  }
}

function buildOutboundMessages() {
  const out = [];
  const selectedModel = $("chat-model").value;
  const selectedCaps = selectedModel ? modelCaps(selectedModel) : new Set();
  const isImageModel = isImageGenerationOnlyCaps(selectedCaps);
  const systemPrompt = isImageModel ? "" : $("chat-system").value.trim();
  if (systemPrompt) out.push({ role: "system", content: systemPrompt });
  for (const m of chatMessages) {
    if (m.role !== "user" && m.role !== "assistant") continue;
    if (m.role === "assistant") {
      if (m.streaming) continue;
      const text = String(m.content || "").trim();
      if (!text) {
        if (m.artifactUrl || m.artifactName || (m.toolLog && m.toolLog.length)) {
          out.push({ role: "assistant", content: `I have updated the artifact ${m.artifactName || "project"}.` });
        }
        continue;
      }
      out.push({ role: "assistant", content: text });
      continue;
    }
    const payload = { role: m.role, content: m.content || "" };
    if (m.role === "user" && m.attachments?.length) {
      const imgs = m.attachments.filter((a) => a.kind === "image").map((a) => a.data);
      const auds = m.attachments.filter((a) => a.kind === "audio").map((a) => a.data);
      const txts = m.attachments.filter((a) => a.kind === "text" && String(a.text || "").trim());
      const media = [...imgs, ...auds];
      if (media.length) payload.images = media;
      if (auds.length) payload.audios = auds;
      if (txts.length) {
        const blocks = txts.map((a) =>
          `--- ${a.name || "text"} ---\n${String(a.text || "").trim()}`).join("\n\n");
        const prefix = t("chat.attached_text_files");
        const extra = `${prefix}\n\n${blocks}`;
        payload.content = payload.content
          ? `${payload.content}\n\n${extra}`
          : extra;
      }
    }
    out.push(payload);
  }
  return out;
}

function readOptionNumber(id, fallback) {
  const n = Number($(id).value);
  return Number.isFinite(n) ? n : fallback;
}

function parseModelChatOptionsText(text) {
  const out = {};
  const src = String(text || "");
  if (!src.trim()) return out;
  for (const rawLine of src.split(/\r?\n/)) {
    let line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    line = line.replace(/^parameter\s+/i, "");
    const parts = line.split(/\s+/);
    if (parts.length < 2) continue;
    const key = parts[0].toLowerCase();
    const val = Number(parts[1]);
    if (!Number.isFinite(val)) continue;
    if (key === "temperature") out.temperature = val;
    else if (key === "top_k" || key === "top-k" || key === "topk") out.top_k = val;
    else if (key === "top_p" || key === "top-p" || key === "topp") out.top_p = val;
  }
  return out;
}

function extractModelChatDefaults(detail) {
  const fromParams = parseModelChatOptionsText(detail?.parameters);
  const fromModelfile = parseModelChatOptionsText(detail?.modelfile);
  const out = { ...fromParams, ...fromModelfile };
  if (detail?.system) {
    out.system = detail.system;
  }
  return out;
}

const GLOBAL_CHAT_DEFAULTS_KEY = "ollama_manager_global_chat_defaults";
const MODEL_CHAT_OPTIONS_KEY = "ollama_manager_model_chat_options";

function getGlobalChatDefaults() {
  const serverDefaults = currentConfig?.chat_defaults;
  const fallback = {
    system: serverDefaults?.system_prompt ?? "",
    temperature: serverDefaults?.temperature ?? 0.7,
    top_k: serverDefaults?.top_k ?? 40,
    top_p: serverDefaults?.top_p ?? 0.9,
    num_ctx: serverDefaults?.num_ctx ?? 100,
    think_level: serverDefaults?.think_level ?? "auto",
    web_tools: serverDefaults?.web_tools ?? false,
    artifacts: serverDefaults?.artifacts ?? false,
  };
  try {
    const raw = localStorage.getItem(GLOBAL_CHAT_DEFAULTS_KEY);
    if (raw) {
      const parsed = JSON.parse(raw);
      const merged = { ...fallback, ...parsed };
      // Migrate the legacy no_think flag into think_level.
      if (parsed.no_think !== undefined && merged.think_level === "auto") {
        merged.think_level = parsed.no_think ? "off" : "auto";
      }
      return merged;
    }
  } catch (e) {
    console.error("Error reading global chat defaults", e);
  }
  return fallback;
}

function saveGlobalChatDefaults(defaults) {
  try {
    localStorage.setItem(GLOBAL_CHAT_DEFAULTS_KEY, JSON.stringify(defaults));
  } catch (e) {
    console.error("Error saving global chat defaults", e);
  }
}

function getAllModelChatOptions() {
  try {
    const raw = localStorage.getItem(MODEL_CHAT_OPTIONS_KEY);
    if (raw) return JSON.parse(raw) || {};
  } catch (e) {
    console.error("Error reading model chat options", e);
  }
  return {};
}

function getEffectiveChatDefaults(modelName) {
  const modelfileDefaults = (modelName && chatModelDefaultsCache.get(modelName)) || {};
  const globalDefaults = getGlobalChatDefaults();
  const effectiveDefaults = {
    ...CHAT_OPTION_FALLBACKS,
    ...modelfileDefaults,
    ...globalDefaults,
  };

  if (globalDefaults.system) {
    effectiveDefaults.system = globalDefaults.system;
  } else if (modelfileDefaults.system) {
    effectiveDefaults.system = modelfileDefaults.system;
  } else {
    effectiveDefaults.system = "";
  }

  return effectiveDefaults;
}

function getCurrentChatOptions() {
  return {
    system: $("chat-system")?.value ?? "",
    temperature: $("chat-temperature")?.value ?? "0.7",
    top_k: $("chat-top-k")?.value ?? "40",
    top_p: $("chat-top-p")?.value ?? "0.9",
    num_ctx: $("chat-num-ctx")?.value ?? "100",
    think_level: $("chat-think-level")?.value ?? "auto",
    web_tools: $("chat-web-tools")?.checked ?? false,
    artifacts: $("chat-artifacts")?.checked ?? false,
    image_width: $("chat-image-width")?.value ?? "512",
    image_height: $("chat-image-height")?.value ?? "512",
    image_steps: $("chat-image-steps")?.value ?? "4",
    image_seed: $("chat-image-seed")?.value ?? "0",
  };
}

function areChatOptionsDefault(modelName, opts) {
  if (!modelName) return true;
  const defaults = getEffectiveChatDefaults(modelName);
  const current = opts || getCurrentChatOptions();

  const caps = typeof modelCaps === "function" ? modelCaps(modelName) : new Set();
  const isImageModel = typeof isImageGenerationOnlyCaps === "function" && isImageGenerationOnlyCaps(caps);

  if (isImageModel) {
    const curW = Number(current.image_width);
    const curH = Number(current.image_height);
    const curSteps = Number(current.image_steps);
    const curSeed = Number(current.image_seed);

    const defW = Number(defaults.image_width ?? 512);
    const defH = Number(defaults.image_height ?? 512);
    const defSteps = Number(defaults.image_steps ?? 4);
    const defSeed = Number(defaults.image_seed ?? 0);

    return curW === defW && curH === defH && curSteps === defSteps && curSeed === defSeed;
  }

  const curSys = String(current.system ?? "");
  const defSys = String(defaults.system ?? "");
  if (curSys !== defSys && (curSys.trim() !== "" || defSys.trim() !== "")) {
    return false;
  }

  const curTemp = Number(current.temperature);
  const defTemp = Number(defaults.temperature);
  if (Number.isFinite(curTemp) && Number.isFinite(defTemp)) {
    if (Math.abs(curTemp - defTemp) > 0.0001) return false;
  } else if (curTemp !== defTemp) {
    return false;
  }

  const curTopK = Math.round(Number(current.top_k));
  const defTopK = Math.round(Number(defaults.top_k));
  if (Number.isFinite(curTopK) && Number.isFinite(defTopK)) {
    if (curTopK !== defTopK) return false;
  } else if (curTopK !== defTopK) {
    return false;
  }

  const curTopP = Number(current.top_p);
  const defTopP = Number(defaults.top_p);
  if (Number.isFinite(curTopP) && Number.isFinite(defTopP)) {
    if (Math.abs(curTopP - defTopP) > 0.0001) return false;
  } else if (curTopP !== defTopP) {
    return false;
  }

  const curCtx = typeof normalizeNumCtxPct === "function"
    ? normalizeNumCtxPct(Number(current.num_ctx))
    : Number(current.num_ctx);
  const defCtx = typeof normalizeNumCtxPct === "function"
    ? normalizeNumCtxPct(Number(defaults.num_ctx))
    : Number(defaults.num_ctx);
  if (curCtx !== defCtx) return false;

  if (caps.has("thinking")) {
    const curThink = String(current.think_level || "auto");
    const defThink = String(defaults.think_level || "auto");
    if (curThink !== defThink) return false;
  }

  if (caps.has("tools")) {
    if (Boolean(current.web_tools) !== Boolean(defaults.web_tools)) return false;
    if (Boolean(current.artifacts) !== Boolean(defaults.artifacts)) return false;
  }

  return true;
}

function getModelChatOptions(modelName) {
  if (!modelName) return null;
  const all = getAllModelChatOptions();
  const saved = all[modelName];
  if (!saved) return null;
  if (areChatOptionsDefault(modelName, saved)) {
    delete all[modelName];
    try {
      localStorage.setItem(MODEL_CHAT_OPTIONS_KEY, JSON.stringify(all));
    } catch (e) {}
    return null;
  }
  return saved;
}

function updateChatCustomOptionsBadge() {
  const modelName = $("chat-model")?.value;
  const resetBtn = $("chat-options-reset-btn");
  if (!resetBtn) return;
  if (!modelName) {
    resetBtn.hidden = true;
    return;
  }
  const hasCustom = !areChatOptionsDefault(modelName);
  resetBtn.hidden = !hasCustom;
}

function saveChatOptionsForCurrentModel() {
  const modelName = $("chat-model")?.value;
  if (!modelName) return;

  const currentOpts = getCurrentChatOptions();
  const all = getAllModelChatOptions();

  if (areChatOptionsDefault(modelName, currentOpts)) {
    if (all[modelName]) {
      delete all[modelName];
      try {
        localStorage.setItem(MODEL_CHAT_OPTIONS_KEY, JSON.stringify(all));
      } catch (e) {
        console.error("Error saving model chat options", e);
      }
    }
  } else {
    all[modelName] = currentOpts;
    try {
      localStorage.setItem(MODEL_CHAT_OPTIONS_KEY, JSON.stringify(all));
    } catch (e) {
      console.error("Error saving model chat options", e);
    }
  }
  updateChatCustomOptionsBadge();
}

function resetModelChatOptionsToDefaults() {
  const modelName = $("chat-model")?.value;
  if (!modelName) return;

  const all = getAllModelChatOptions();
  if (all[modelName]) {
    delete all[modelName];
    try {
      localStorage.setItem(MODEL_CHAT_OPTIONS_KEY, JSON.stringify(all));
    } catch (e) {
      console.error("Error resetting model chat options", e);
    }
  }

  void applyChatDefaultsForModel(modelName, true);
  updateChatCustomOptionsBadge();
  toast(t("chat.reset_to_defaults_done"), "success");
}

function resetAllModelChatOptionsToDefaults() {
  try {
    localStorage.removeItem(MODEL_CHAT_OPTIONS_KEY);
  } catch (e) {
    console.error("Error clearing all model chat options", e);
  }

  const activeChatModel = $("chat-model")?.value;
  if (activeChatModel) {
    void applyChatDefaultsForModel(activeChatModel, true);
  }
  updateChatCustomOptionsBadge();
}

function setChatOptionsValues(opts) {
  if (!opts) return;
  if (opts.system !== undefined && $("chat-system")) {
    $("chat-system").value = opts.system || "";
  }
  if (opts.temperature != null && $("chat-temperature")) {
    $("chat-temperature").value = String(opts.temperature);
  }
  if (opts.top_k != null && $("chat-top-k")) {
    $("chat-top-k").value = String(Math.round(Number(opts.top_k)));
  }
  if (opts.top_p != null && $("chat-top-p")) {
    $("chat-top-p").value = String(opts.top_p);
  }
  if (opts.num_ctx != null && $("chat-num-ctx")) {
    const pct = Number(opts.num_ctx);
    $("chat-num-ctx").value = String(normalizeNumCtxPct(pct));
  }
  if (opts.think_level !== undefined && $("chat-think-level")) {
    $("chat-think-level").value = opts.think_level;
  } else if (opts.no_think !== undefined && $("chat-think-level")) {
    $("chat-think-level").value = opts.no_think ? "off" : "auto";
  }
  if (opts.web_tools !== undefined && $("chat-web-tools")) {
    $("chat-web-tools").checked = !!opts.web_tools;
  }
  if (opts.artifacts !== undefined && $("chat-artifacts")) {
    $("chat-artifacts").checked = !!opts.artifacts;
  }
  if (opts.image_width !== undefined && $("chat-image-width")) {
    $("chat-image-width").value = opts.image_width;
  }
  if (opts.image_height !== undefined && $("chat-image-height")) {
    $("chat-image-height").value = opts.image_height;
  }
  if (opts.image_steps !== undefined && $("chat-image-steps")) {
    $("chat-image-steps").value = opts.image_steps;
  }
  if (opts.image_seed !== undefined && $("chat-image-seed")) {
    $("chat-image-seed").value = opts.image_seed;
  }
  if (typeof adjustChatSystemPromptHeight === "function") {
    adjustChatSystemPromptHeight();
  }
  if (typeof updateChatSystemTokens === "function") {
    updateChatSystemTokens();
  }
}

async function applyChatDefaultsForModel(name, force = false) {
  const model = String(name || "").trim();
  if (!model) return;
  if (!force && lastChatDefaultsModel === model) return;

  const reqSeq = ++chatDefaultsReqSeq;
  let modelfileDefaults = chatModelDefaultsCache.get(model);
  if (!modelfileDefaults) {
    try {
      const detail = await api(`/api/models/${encodeURIComponent(model)}`);
      modelfileDefaults = extractModelChatDefaults(detail);
      chatModelDefaultsCache.set(model, modelfileDefaults);
    } catch {
      modelfileDefaults = {};
    }
  }

  if (reqSeq !== chatDefaultsReqSeq) return;
  if ($("chat-model").value !== model) return;

  lastChatDefaultsModel = model;

  // Check if this model has custom saved options in localStorage:
  const modelSavedOpts = getModelChatOptions(model);
  if (modelSavedOpts) {
    setChatOptionsValues(modelSavedOpts);
    updateChatCustomOptionsBadge();
    return;
  }

  // Model does not have custom options -> use effective defaults
  const effectiveDefaults = getEffectiveChatDefaults(model);
  setChatOptionsValues(effectiveDefaults);
  updateChatCustomOptionsBadge();
}

function isEmbeddingOnlyModel(modelName) {
  const caps = modelCaps(modelName);
  return caps.has("embedding") && !caps.has("completion");
}

function buildEmbeddingInputText() {
  const outbound = buildOutboundMessages();
  for (let i = outbound.length - 1; i >= 0; i -= 1) {
    const m = outbound[i];
    if (m.role === "user" && String(m.content || "").trim()) {
      return String(m.content).trim();
    }
  }
  return "";
}

function formatEmbeddingResult(vec) {
  const dims = Array.isArray(vec) ? vec.length : 0;
  const preview = (Array.isArray(vec) ? vec : [])
    .slice(0, 24)
    .map((n) => Number(n).toFixed(6))
    .join(", ");
  return t("chat.embed_result", {
    dims,
    preview: `[${preview}${dims > 24 ? ", ..." : ""}]`,
  });
}

function stopSpeechPlayback() {
  if (!window.speechSynthesis) return;
  try { window.speechSynthesis.cancel(); } catch { }
  speakingMsgId = "";
}

function speakMessage(msg) {
  if (!window.speechSynthesis || typeof SpeechSynthesisUtterance === "undefined") {
    toast(t("chat.tts_unsupported"), "error");
    return;
  }
  const text = textForSpeech(msg?.content || "");
  if (!text) return;
  const isSame = speakingMsgId && speakingMsgId === msg.id;
  if (isSame && window.speechSynthesis.speaking) {
    stopSpeechPlayback();
    renderChatMessages();
    return;
  }
  stopSpeechPlayback();

  const u = new SpeechSynthesisUtterance(text);
  const lang = speechLangFromUi();
  const voice = findBestVoice(lang);
  u.lang = voice?.lang || lang;
  if (voice) u.voice = voice;
  u.rate = 1;
  u.pitch = 1;
  speakingMsgId = msg.id;
  u.onend = () => {
    speakingMsgId = "";
    if (currentView === "chat") renderChatMessages();
  };
  u.onerror = () => {
    speakingMsgId = "";
    if (currentView === "chat") renderChatMessages();
  };
  window.speechSynthesis.speak(u);
  renderChatMessages();
}

async function readSSEStream(response, onEvent) {
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    while (true) {
      const splitAt = buf.indexOf("\n\n");
      if (splitAt < 0) break;
      const block = buf.slice(0, splitAt);
      buf = buf.slice(splitAt + 2);
      let event = "message";
      const dataLines = [];
      for (const line of block.split("\n")) {
        if (!line || line.startsWith(":")) continue;
        if (line.startsWith("event:")) event = line.slice(6).trim();
        if (line.startsWith("data:")) dataLines.push(line.slice(5).trimStart());
      }
      if (!dataLines.length) continue;
      let data = {};
      try {
        data = JSON.parse(dataLines.join("\n"));
      } catch {
        data = { raw: dataLines.join("\n") };
      }
      await onEvent(event, data);
    }
  }
}

function isAbortError(e) {
  if (!e) return false;
  if (chatAbortController && chatAbortController.signal && chatAbortController.signal.aborted) {
    return true;
  }
  if (e.name === "AbortError") return true;
  if (e.code === 20) return true; // legacy DOMException
  const msg = String(e.message || "").toLowerCase().trim();
  return (
    msg === "aborted" ||
    msg === "the user aborted a request." ||
    msg === "the operation was aborted" ||
    msg === "the operation was aborted." ||
    msg === "operation aborted" ||
    msg === "user aborted" ||
    msg === "user aborted a request" ||
    msg === "aborterror"
  );
}

function isOomError(e) {
  const msg = String(e && (e.message || e) || "").toLowerCase();
  return (
    msg.includes("out of memory") ||
    msg.includes("ran out of memory") ||
    msg.includes("sin memoria") ||
    msg.includes("falta de memoria") ||
    msg.includes("memory error") ||
    msg.includes("vram") ||
    msg.includes("cuda error") ||
    msg.includes("compute error") ||
    msg.includes("llama-server chat error") ||
    msg.includes("metal: command buffer") ||
    msg.includes("allocation failed") ||
    msg.includes("failed to allocate")
  );
}

const NUM_CTX_PRESETS = [10, 25, 50, 75, 100];

/** Clamp a value to a valid context percentage (100/75/50/25/10), defaulting to 100. */
function normalizeNumCtxPct(p) {
  const n = Math.round(Number(p));
  return NUM_CTX_PRESETS.includes(n) ? n : 100;
}

/** Token budget for a context percentage. 100% (or unknown model) returns 0 = use model default. */
function numCtxTokensForPct(pct, modelName) {
  const p = normalizeNumCtxPct(pct);
  if (p >= 100) return 0;
  const modelMax = Number(modelByName(modelName)?.context_length) || 0;
  const base = modelMax > 0 ? modelMax : 32768;
  return Math.max(256, Math.round((base * p) / 100));
}

/** Suggest the next lower context percentage to retry after an out-of-memory error. */
function suggestLowerPct(currentPct) {
  const order = [100, 75, 50, 25, 10];
  for (let i = 0; i < order.length; i++) {
    if (order[i] < currentPct) return order[i];
  }
  return 0; // already at the minimum
}

function updateStreamBar() {
  const bar = $("chat-stream-bar");
  const btn = $("chat-stop-btn");
  const label = bar?.querySelector(".chat-stream-label");
  if (bar) bar.hidden = !chatStreamLock;
  if (label) {
    const metrics = activeStreamMessage ? assistantMetricText(activeStreamMessage, { showZero: true }) : "";
    label.textContent = metrics ? `${t("chat.generating")} ${metrics}` : t("chat.generating");
    if (activeStreamMessage && activeStreamMessage.elapsedMs != null) {
      const timeTitle = formatMetaElapsedSecondsTitle(activeStreamMessage.elapsedMs || 0);
      label.title = timeTitle;
      if (bar) bar.title = timeTitle;
    } else {
      label.title = "";
      if (bar) bar.title = "";
    }
  }
  if (btn) {
    btn.disabled = !chatStreamLock;
    btn.title = t("chat.stop_hint");
  }
  const sendBtn = $("chat-send-btn");
  if (sendBtn) {
    sendBtn.textContent = chatStreamLock ? t("chat.queue_send") : t("chat.send");
    sendBtn.title = chatStreamLock ? t("chat.queue_send") : t("chat.send");
  }
}

function stopChatGeneration() {
  if (!chatStreamLock || !chatAbortController) return;
  try { chatAbortController.abort(); } catch (_) { }
}

function newAssistantMessage() {
  return {
    id: nanoid(),
    role: "assistant",
    model: "",
    content: "",
    thinkContent: "",
    thinkOpen: true,
    inThink: false,
    thinkMs: 0,
    thinkStartedAt: 0,
    streaming: true,
    elapsedMs: 0,
    tokens: 0,
    hasDebug: false,
    promptTokens: 0,
    completionTokens: 0,
    evalDurationNs: 0,
    contextMax: 0,
    tps: null,
    lastChunkEvalCount: null,
    streamStartedAt: 0,
    toolLog: [],
    thinkBlockStarted: false,
    thinkBlockClosed: false,
    timeline: [],
    segmentFlushIndex: 0,
    tailThinkOpen: true,
    _lastSegInThink: false,
  };
}

/** Keep the chat pane pinned to the latest content (streaming + layout). */
let chatUserScrolledUp = false;
function scrollChatToBottom(force = false) {
  const host = $("chat-scroll-shell") || $("chat-messages");
  if (!host) return;
  
  if (force) {
    chatUserScrolledUp = false;
    host.scrollTop = host.scrollHeight;
    return;
  }

  // If the user has explicitly scrolled up to read previous messages, do NOT auto-scroll down
  if (chatUserScrolledUp) return;

  host.scrollTop = host.scrollHeight;
}

/** Scroll thinking blocks of the currently streaming message to bottom without jumping. */
function scrollActiveBlocks() {
  const streamingMsg = document.querySelector("article.chat-msg.chat-streaming");
  if (!streamingMsg) return;

  streamingMsg.querySelectorAll("details.chat-think").forEach((details) => {
    if (!details.open) details.open = true;
    const pre = details.querySelector("pre");
    if (pre && !chatUserScrolledUp) {
      pre.scrollTop = pre.scrollHeight;
    }
  });
}

async function runChatRequest(assistantMsg) {
  const modelName = $("chat-model").value;
  assistantMsg.model = modelName;
  if (isEmbeddingOnlyModel(modelName)) {
    const input = buildEmbeddingInputText();
    if (!input) {
      throw new Error(t("chat.embed_empty_input"));
    }
    const started = Date.now();
    const data = await api("/api/embed", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ model: modelName, input }),
    });
    assistantMsg.streaming = false;
    assistantMsg.elapsedMs = Date.now() - started;
    assistantMsg.hasDebug = false;
    assistantMsg.content = formatEmbeddingResult(data.embedding || []);
    flushChatRender();
    return;
  }

  const caps = modelCaps(modelName);
  const isImageModel = isImageGenerationOnlyCaps(caps);
  const canThink = caps.has("thinking");
  const canTools = caps.has("tools");
  const webToolsOn = !isImageModel && canTools && $("chat-web-tools").checked;
  const artifactsOn = !isImageModel && canTools && $("chat-artifacts").checked;

  const options = {};
  const imageParams = {};
  if (isImageModel) {
    imageParams.width = Math.min(1024, Math.max(128, Math.round(readOptionNumber("chat-image-width", 512))));
    imageParams.height = Math.min(1024, Math.max(128, Math.round(readOptionNumber("chat-image-height", 512))));
    imageParams.steps = Math.max(1, Math.round(readOptionNumber("chat-image-steps", 4)));
    const seedVal = Math.round(readOptionNumber("chat-image-seed", 0));
    if (seedVal > 0) {
      imageParams.seed = seedVal;
    }
  } else {
    options.temperature = readOptionNumber("chat-temperature", CHAT_OPTION_FALLBACKS.temperature);
    options.top_k = Math.round(readOptionNumber("chat-top-k", CHAT_OPTION_FALLBACKS.top_k));
    options.top_p = readOptionNumber("chat-top-p", CHAT_OPTION_FALLBACKS.top_p);
    const numCtx = numCtxTokensForPct(readOptionNumber("chat-num-ctx", CHAT_OPTION_FALLBACKS.num_ctx), modelName);
    if (numCtx > 0) options.num_ctx = numCtx;
  }

  const thinkLevel = canThink ? ($("chat-think-level")?.value || "auto") : "auto";
  let think;
  if (canThink && thinkLevel !== "auto") {
    think = thinkLevel === "off" ? false : thinkLevel;
  }

  const payload = {
    model: modelName,
    think,
    options,
    messages: buildOutboundMessages(),
    ...imageParams,
  };
  if (webToolsOn) payload.web_tools = true;
  if (artifactsOn) {
    payload.artifacts = true;
    if (activeArtifactTimestamp) {
      payload.artifact_dir = activeArtifactTimestamp;
    } else {
      // Find the most recent artifact in chat history to iterate on it.
      for (let i = chatMessages.length - 1; i >= 0; i--) {
        const msg = chatMessages[i];
        if (msg.artifactTimestamp) {
          payload.artifact_dir = msg.artifactTimestamp;
          break;
        }
        if (msg.artifactUrl) {
          const match = String(msg.artifactUrl).match(/\/api\/artifacts\/(.+)\//);
          if (match) {
            payload.artifact_dir = match[1];
            break;
          }
        }
      }
    }
  }

  chatAbortController = new AbortController();
  chatStreamLock = true;
  activeStreamMessage = assistantMsg;
  updateStreamBar();
  const turnStartedAt = Date.now();
  assistantMsg.streamStartedAt = turnStartedAt;
  startStreamTicker(assistantMsg, turnStartedAt);
  let assistantRaw = "";
  try {
    const res = await fetch("/api/chat", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal: chatAbortController.signal,
    });
    if (!res.ok) {
      let msg = res.statusText;
      try {
        const j = await res.json();
        if (j.error) msg = j.error;
      } catch { }
      throw new Error(msg || "chat failed");
    }
    await readSSEStream(res, async (event, data) => {
      // Debug: log every SSE event with full data
      if (event === "chunk") {
        const thinkDelta = data?.message?.thinking || "";
        const contentDelta = data?.message?.content || "";
        const toolCalls = data?.message?.tool_calls;
        if (thinkDelta || contentDelta || toolCalls) {
          // console.log("[chat] chunk", {
          //   thinking: thinkDelta ? thinkDelta.slice(0, 200) : "",
          //   content: contentDelta ? contentDelta.slice(0, 200) : "",
          //   tool_calls: toolCalls,
          //   done: data?.done,
          //   eval_count: data?.eval_count,
          // });
        }
      } else {
        console.log(`[chat] event:${event}`, data);
      }
      if (event === "artifact") {
        if (data?.timestamp) {
          assistantMsg.artifactTimestamp = data.timestamp;
          activeArtifactTimestamp = data.timestamp;
        }
        if (data?.name) {
          assistantMsg.artifactName = data.name;
          activeArtifactName = data.name;
        }
        if (data?.url) {
          assistantMsg.artifactUrl = data.url;
          activeArtifactUrl = data.url;
        }
        if (data?.generating) {
          // create_artifact was called — show loading screen, don't load URL yet.
          assistantMsg.artifactUrl = data?.url || "";
          assistantMsg.artifactName = data?.name || "Artifact";
          assistantMsg.artifactDescription = data?.description || "";
          assistantMsg.artifactGenerating = true;
          activeArtifactName = assistantMsg.artifactName;
          activeArtifactUrl = assistantMsg.artifactUrl;
          showArtifactPanel(assistantMsg.artifactUrl, assistantMsg.artifactName, true);
          updateArtifactResourceBtn();
          scheduleRenderChatMessages();
        } else if (data?.loaded) {
          // index.html was written — transition from loading screen to live preview.
          assistantMsg.artifactGenerating = false;
          const url = data?.url || assistantMsg.artifactUrl || "";
          activeArtifactUrl = url;
          const panel = $("chat-artifact-panel");
          const frame = $("chat-artifact-frame");
          if (panel) panel.hidden = false;
          if (frame && url) {
            frame.removeAttribute("srcdoc");
            frame.src = url;
            const match = String(url).match(/\/api\/artifacts\/(.+)\//);
            if (match) {
              activeArtifactTimestamp = match[1];
            }
          }
          updateArtifactResourceBtn();
          // Close options panel so artifact is visible on mobile
          $("chat-view")?.classList.remove("chat-options-open");
          syncChatPanels($("chat-view"));
          scheduleRenderChatMessages();
        } else if (data?.reload) {
          // Reload the iframe if the artifact panel is already visible.
          const panel = $("chat-artifact-panel");
          const frame = $("chat-artifact-frame");
          if (panel && !panel.hidden && frame && frame.src) {
            frame.removeAttribute("srcdoc");
            // Bypass browser cache and trigger immediate reload
            const u = new URL(frame.src, window.location.href);
            u.searchParams.set("_t", Date.now());
            frame.src = u.toString();
          }
        } else {
          assistantMsg.artifactUrl = data?.url || "";
          assistantMsg.artifactName = data?.name || "Artifact";
          assistantMsg.artifactDescription = data?.description || "";
          activeArtifactName = assistantMsg.artifactName;
          activeArtifactUrl = assistantMsg.artifactUrl;
          showArtifactPanel(assistantMsg.artifactUrl, assistantMsg.artifactName);
          updateArtifactResourceBtn();
          scheduleRenderChatMessages();
        }
      } else if (event === "artifact_screenshot_request") {
        void handleArtifactScreenshotRequest(data);
      } else if (event === "artifact_eval_request") {
        void handleArtifactEvalRequest(data);
      } else if (event === "tool") {
        if (!assistantMsg.toolLog) assistantMsg.toolLog = [];
        if (data?.phase === "generating") {
          // Model is generating tool call arguments — show early feedback.
          let existing = assistantMsg.toolLog.find(
            (e) => e.name === data.name && e.status === "generating"
          );
          if (!existing) {
            flushSegmentToTimeline(assistantMsg, assistantRaw, false);
            existing = {
              name: data.name,
              path: data.path,
              command: data.command,
              code: data.code,
              artifact_name: data.artifact_name,
              status: "generating",
            };
            assistantMsg.toolLog.push(existing);
            if (!assistantMsg.timeline) assistantMsg.timeline = [];
            assistantMsg.timeline.push({ type: "tool", entry: existing });
          } else {
            // Update existing entry with any newly received fields.
            if (data.path) existing.path = data.path;
            if (data.command) existing.command = data.command;
            if (data.code) existing.code = data.code;
            if (data.artifact_name) existing.artifact_name = data.artifact_name;
          }

          if (data.name === "create_artifact") {
            assistantMsg.artifactGenerating = true;
            if (data.artifact_name) {
              assistantMsg.artifactName = data.artifact_name;
            }
            showArtifactPanel(null, assistantMsg.artifactName || "Artifact", true);
          }
        } else if (data?.phase === "start") {
          if (!assistantMsg._toolActiveStart) {
            assistantMsg._toolActiveStart = Date.now();
          }
          // Upgrade 'generating' entries to 'running', or add new if none.
          let upgraded = false;
          for (let i = assistantMsg.toolLog.length - 1; i >= 0; i -= 1) {
            const e = assistantMsg.toolLog[i];
            if (e.name === data.name && e.status === "generating") {
              e.status = "running";
              e.query = data.query;
              e.url = data.url;
              e.max_results = data.max_results;
              e.description = data.description;
              if (data.path) e.path = data.path;
              if (data.command) e.command = data.command;
              if (data.code) e.code = data.code;
              if (data.artifact_name) e.artifact_name = data.artifact_name;
              upgraded = true;
              break;
            }
          }
          if (!upgraded) {
            flushSegmentToTimeline(assistantMsg, assistantRaw, false);
            const entry = {
              name: data.name,
              query: data.query,
              url: data.url,
              max_results: data.max_results,
              path: data.path,
              command: data.command,
              code: data.code,
              artifact_name: data.artifact_name,
              description: data.description,
              status: "running",
            };
            assistantMsg.toolLog.push(entry);
            if (!assistantMsg.timeline) assistantMsg.timeline = [];
            assistantMsg.timeline.push({ type: "tool", entry });
          }

          if (data.name === "create_artifact") {
            assistantMsg.artifactGenerating = true;
            if (data.artifact_name) {
              assistantMsg.artifactName = data.artifact_name;
            }
            showArtifactPanel(null, assistantMsg.artifactName || "Artifact", true);
          }
        } else if (data?.phase === "done") {
          if (assistantMsg._toolActiveStart) {
            const duration = Date.now() - assistantMsg._toolActiveStart;
            assistantMsg._toolTotalTimeMs = (assistantMsg._toolTotalTimeMs || 0) + duration;
            assistantMsg._toolActiveStart = null;
          }
          for (let i = assistantMsg.toolLog.length - 1; i >= 0; i -= 1) {
            const e = assistantMsg.toolLog[i];
            if (e.name === data.name && (e.status === "running" || e.status === "generating")) {
              e.status = data.ok ? "ok" : "error";
              e.error = data.error || "";
              e.result_preview = data.result_preview || "";
              e.result_runes = data.result_runes;
              if (data.image) e.image = data.image;
              break;
            }
          }
        }
        assistantMsg._accRaw = assistantRaw;
        scheduleRenderChatMessages();
      } else if (event === "chunk") {
        const thinkDelta = data?.message?.thinking || "";
        const contentDelta = data?.message?.content || "";
        if (thinkDelta || contentDelta) {
          assistantMsg._lastChunkAt = Date.now();
          if (!assistantMsg._firstTokenAt) {
            assistantMsg._firstTokenAt = Date.now();
          }
          const chunkChars = (thinkDelta ? thinkDelta.length : 0) + (contentDelta ? contentDelta.length : 0);
          assistantMsg._charCount = (assistantMsg._charCount || 0) + chunkChars;
          assistantMsg._chunkCount = (assistantMsg._chunkCount || 0) + 1;

          const tokenEst = Math.max(assistantMsg._chunkCount, Math.round(assistantMsg._charCount / 3.8));
          assistantMsg.completionTokens = tokenEst;
          assistantMsg.tokens = tokenEst;
        }
        if (data?.completed != null) {
          assistantMsg.completedSteps = data.completed;
        }
        if (data?.total != null) {
          assistantMsg.totalSteps = data.total;
        }
        if (thinkDelta) {
          if (!assistantMsg.thinkBlockStarted) {
            assistantRaw += "<think>\n";
            assistantMsg.thinkBlockStarted = true;
          } else if (assistantMsg.thinkBlockClosed) {
            assistantRaw += "\n<think>\n";
            assistantMsg.thinkBlockClosed = false;
          }
          assistantRaw += thinkDelta;
        }
        if (contentDelta) {
          if (assistantMsg.thinkBlockStarted && !assistantMsg.thinkBlockClosed) {
            assistantRaw += "\n</think>\n";
            assistantMsg.thinkBlockClosed = true;
          }
          assistantRaw += contentDelta;
        }
        const parts = splitThink(assistantRaw);
        assistantMsg.thinkContent = parts.think;
        assistantMsg.content = parts.answer;
        assistantMsg.inThink = parts.inThink;
        assistantMsg.elapsedMs = Date.now() - turnStartedAt;
        const chunkEval = Number(data?.eval_count);
        if (Number.isFinite(chunkEval) && chunkEval > (assistantMsg.completionTokens || 0)) {
          assistantMsg.completionTokens = chunkEval;
          assistantMsg.tokens = chunkEval;
        }
        updateLiveAssistantTPS(assistantMsg);
        if (parts.inThink && !assistantMsg.thinkStartedAt) {
          assistantMsg.thinkStartedAt = Date.now();
          startThinkTicker(assistantMsg);
        }
        if (!parts.inThink && assistantMsg.thinkStartedAt) {
          assistantMsg.thinkMs = Date.now() - assistantMsg.thinkStartedAt;
          stopThinkTicker();
        }
        assistantMsg._accRaw = assistantRaw;
        updateStreamBar();
        scheduleRenderChatMessages();
      } else if (event === "error") {
        throw new Error(data?.error || "stream error");
      } else if (event === "done") {
        stopStreamTicker();
        console.log("[chat] done event", {
          toolLog: assistantMsg.toolLog || [],
          artifactUrl: assistantMsg.artifactUrl || null,
          artifactGenerating: assistantMsg.artifactGenerating || false,
          contentPreview: (assistantMsg.content || "").slice(0, 300),
          thinkPreview: (assistantMsg.thinkContent || "").slice(0, 300),
          tokens: data?.total_tokens,
          elapsed_ms: data?.elapsed_ms,
        });
        if (assistantMsg.thinkBlockStarted && !assistantMsg.thinkBlockClosed) {
          assistantRaw += "\n</think>\n";
          assistantMsg.thinkBlockClosed = true;
        }
        const p2 = splitThink(assistantRaw);
        assistantMsg.thinkContent = p2.think;
        assistantMsg.content = p2.answer;
        assistantMsg.inThink = p2.inThink;
        // Always flush remaining content to timeline for faithful chronological order.
        // Skip image models — their content is base64 image data, not text.
        const isImgModel = assistantMsg.model && modelCaps(assistantMsg.model).has("image");
        if (!isImgModel) {
          flushSegmentToTimeline(assistantMsg, assistantRaw, true);
        }
        if (assistantMsg.toolLog && assistantMsg.toolLog.length > 0) {
          // If the response ended without any real tool execution (only
          // generating placeholders), remove the stale entries.
          const hasRealTool = assistantMsg.toolLog.some(
            (e) => e.status !== "generating"
          );
          if (!hasRealTool) {
            assistantMsg.toolLog = [];
          }
          if (assistantMsg.timeline) {
            assistantMsg.timeline = assistantMsg.timeline.filter(
              (it) => it.type !== "tool" || (it.entry && it.entry.status !== "generating")
            );
          }
        }
        assistantMsg._accRaw = "";
        assistantMsg.streaming = false;
        assistantMsg.inThink = false;
        assistantMsg.elapsedMs = Number(data.elapsed_ms) || (Date.now() - turnStartedAt);
        assistantMsg.promptTokens = Number(data.prompt_tokens) || 0;
        assistantMsg.completionTokens = Number(data.completion_tokens) || (assistantMsg.completionTokens || 0);
        assistantMsg.tokens = Number(data.total_tokens) || (assistantMsg.promptTokens + assistantMsg.completionTokens);
        assistantMsg.evalDurationNs = Number(data.eval_duration_ns) || 0;
        assistantMsg.doneReason = data?.done_reason || assistantMsg.doneReason || "stop";
        const mdl = modelByName(assistantMsg.model || modelName);
        assistantMsg.contextMax = Number(mdl?.context_length) || 0;
        const evNs = assistantMsg.evalDurationNs;
        const comp = assistantMsg.completionTokens;
        if (evNs > 0 && comp > 0) {
          assistantMsg.tps = comp / (evNs / 1e9);
        } else if (assistantMsg._firstTokenAt && comp > 0) {
          const genSec = (Date.now() - assistantMsg._firstTokenAt) / 1000;
          assistantMsg.tps = genSec > 0.1 ? (comp / genSec) : (comp / Math.max(0.001, assistantMsg.elapsedMs / 1000));
        } else if (comp > 0 && assistantMsg.elapsedMs > 0) {
          assistantMsg.tps = comp / (assistantMsg.elapsedMs / 1000);
        } else {
          assistantMsg.tps = null;
        }
        assistantMsg.hasDebug = true;
        chatLastUsedTokens = assistantMsg.tokens || (assistantMsg.promptTokens + assistantMsg.completionTokens);
        updateChatContextMeter();
        flushChatRender();
        refreshModels().catch(() => {});
      }
    });
    assistantMsg.streaming = false;
    if (assistantMsg.thinkStartedAt && assistantMsg.thinkMs === 0) {
      assistantMsg.thinkMs = Date.now() - assistantMsg.thinkStartedAt;
    }
    assistantMsg.elapsedMs = assistantMsg.elapsedMs || (Date.now() - turnStartedAt);
  } catch (e) {
    assistantMsg.streaming = false;
    assistantMsg.inThink = false;
    if (isAbortError(e)) {
      assistantMsg.stopped = true;
      assistantMsg.doneReason = "aborted";
      const hasAnswer = String(assistantMsg.content || "").trim().length > 0;
      const hasThink = String(assistantMsg.thinkContent || "").trim().length > 0;
      if (!hasAnswer && !hasThink) {
        assistantMsg.content = t("chat.stopped_empty");
      }
    } else {
      let errMsg = e.message || "failed";
      if (errMsg.includes("mlx runner failed") || errMsg.includes("failed to initialize MLX") || errMsg.includes("failed to load MLX")) {
        errMsg = t("chat.error_mlx_unsupported");
      } else if (
        errMsg.includes("Compute error") ||
        errMsg.includes("llama-server chat error") ||
        errMsg.includes("out of memory") ||
        errMsg.includes("CUDA error") ||
        errMsg.includes("metal: command buffer")
      ) {
        errMsg = t("chat.error_compute_oom");
      }
      if (isOomError(errMsg)) {
        assistantMsg.isOom = true;
        const curPct = normalizeNumCtxPct(Number($("chat-num-ctx")?.value) || 100);
        const modelMax = Number(modelByName(assistantMsg.model)?.context_length) || 0;
        const base = modelMax > 0 ? modelMax : 32768;
        const effective = curPct >= 100 ? (modelMax || 0) : Math.round((base * curPct) / 100);
        const suggestedPct = suggestLowerPct(curPct);
        const suggested = suggestedPct >= 100 ? (modelMax || 0) : Math.round((base * suggestedPct) / 100);
        assistantMsg.effectiveCtx = effective;
        assistantMsg.effectivePct = curPct;
        assistantMsg.suggestedPct = suggestedPct;
        assistantMsg.suggestedCtx = suggested;
      }
      assistantMsg.isError = true;
      const isImg = assistantMsg.model && modelCaps(assistantMsg.model).has("image");
      if (!assistantMsg.content || isImg) {
        assistantMsg.content = t("chat.error_reply", { msg: errMsg });
      }
      toast(t("toast.error", { msg: errMsg }), "error");
    }
  } finally {
    chatAbortController = null;
    stopThinkTicker();
    stopStreamTicker();
    chatStreamLock = false;
    activeStreamMessage = null;
    updateStreamBar();
    flushChatRender();
    void refreshModelArtifactCount();
    if (chatPendingQueue.length > 0) {
      const next = chatPendingQueue.shift();
      renderChatQueue();
      setTimeout(() => { runOneChatTurn(next.text, next.attachments); }, 0);
    } else {
      renderChatQueue();
    }
  }
}

async function runOneChatTurn(text, attachments) {
  chatEditingMessageId = "";
  chatEditingDraft = "";
  const userMsg = {
    id: nanoid(),
    role: "user",
    content: text,
    attachments: (attachments || []).map((a) => ({ ...a })),
  };
  chatMessages.push(userMsg);
  const assistantMsg = newAssistantMessage();
  assistantMsg.model = $("chat-model").value;
  chatMessages.push(assistantMsg);
  renderChatMessages();
  scrollChatToBottom(true);
  await runChatRequest(assistantMsg);
}

async function regenerateLastAssistantMessage(clickedId) {
  if (chatStreamLock) {
    toast(t("chat.regenerate_busy"), "error");
    return;
  }
  if (!chatMessages.length) return;
  const last = chatMessages[chatMessages.length - 1];
  if (last.id !== clickedId || last.role !== "assistant" || last.streaming) return;
  if (chatMessages.length < 2) return;
  const prev = chatMessages[chatMessages.length - 2];
  if (!prev || prev.role !== "user") return;
  if ($("chat-send-btn")?.disabled) {
    const why = $("chat-send-btn")?.title || t("status.unreachable");
    toast(why, "error");
    return;
  }
  if (!models.length) {
    toast(t("chat.no_models"), "error");
    return;
  }
  chatMessages.pop();
  const assistantMsg = newAssistantMessage();
  assistantMsg.model = $("chat-model").value;
  chatMessages.push(assistantMsg);
  renderChatMessages();
  scrollChatToBottom(true);
  await runChatRequest(assistantMsg);
}

async function reduceContextAndRetry(msgId, suggestedPct) {
  if (chatStreamLock) {
    toast(t("chat.regenerate_busy"), "error");
    return;
  }
  const pct = normalizeNumCtxPct(suggestedPct);
  if (pct <= 0) return;
  const numCtxInput = $("chat-num-ctx");
  if (numCtxInput) {
    numCtxInput.value = String(pct);
    saveChatOptionsForCurrentModel();
    toast(t("chat.oom_ctx_set", { pct }), "success");
  }
  await regenerateLastAssistantMessage(msgId);
}

async function editAndResendUserMessage(userId, newText) {
  if (chatStreamLock) {
    toast(t("chat.edit_busy"), "error");
    return;
  }
  const idx = chatMessages.findIndex((m) => m.id === userId && m.role === "user");
  if (idx < 0) return;

  let lastUserIdx = -1;
  for (let k = chatMessages.length - 1; k >= 0; k--) {
    if (chatMessages[k].role === "user") {
      lastUserIdx = k;
      break;
    }
  }
  if (idx !== lastUserIdx) return;

  if ($("chat-send-btn")?.disabled) {
    const why = $("chat-send-btn")?.title || t("status.unreachable");
    toast(why, "error");
    return;
  }
  if (!models.length) {
    toast(t("chat.no_models"), "error");
    return;
  }
  const trimmed = newText.trim();
  const atts = (chatEditingAttachments || []).slice();
  if (!trimmed && !atts.length) return;
  chatMessages[idx].content = trimmed;
  chatMessages[idx].attachments = atts;
  chatMessages = chatMessages.slice(0, idx + 1);
  const assistantMsg = newAssistantMessage();
  assistantMsg.model = $("chat-model").value;
  chatMessages.push(assistantMsg);
  chatEditingMessageId = "";
  chatEditingDraft = "";
  chatEditingAttachments = [];
  renderChatMessages();
  scrollChatToBottom(true);
  await runChatRequest(assistantMsg);
}

async function sendChatMessage(interruptNow = false) {
  if ($("chat-send-btn")?.disabled) return;
  const text = $("chat-input").value.trim();
  if (!text && chatAttachments.length === 0) return;
  if (!models.length) {
    toast(t("chat.no_models"), "error");
    return;
  }

  const snapText = text;
  const snapAtt = chatAttachments.map((a) => ({ ...a }));
  $("chat-input").value = "";
  chatAttachments = [];
  renderAttachments();

  if (chatStreamLock) {
    if (interruptNow) {
      chatPendingQueue.unshift({ id: nanoid(), text: snapText, attachments: snapAtt });
      renderChatQueue();
      stopChatGeneration();
      return;
    }
    chatPendingQueue.push({ id: nanoid(), text: snapText, attachments: snapAtt });
    renderChatQueue();
    return;
  }

  await runOneChatTurn(snapText, snapAtt);
}

let chatArtifactWidthSet = false;

