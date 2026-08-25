"use strict";

// ---------- HuggingFace Model Explorer Section ----------

let hfModels = [];
let hfActiveModel = null;
let hfReadmeCache = new Map();
let hfSearchDebounce = null;
let hfCurrentFilter = "all"; // "all" | "ollama" | "vision"
let hfCurrentTimeFilter = "all"; // "all" | "week" | "month" | "year"
let hfCurrentSort = "trending"; // "trending" | "downloads" | "likes" | "recent"
let hfActiveTab = "quants"; // "quants" | "readme"
let hfPage = 0;
let hfNextCursor = "";
let hfHasMore = false;
let hfLoadingMore = false;

function showHFView() {
  if (typeof hideAllMainViews === "function") {
    hideAllMainViews();
  } else {
    $("models-view") && ($("models-view").hidden = true);
    $("chat-view") && ($("chat-view").hidden = true);
    $("tests-view") && ($("tests-view").hidden = true);
    $("analytics-view") && ($("analytics-view").hidden = true);
    $("settings-view") && ($("settings-view").hidden = true);
    $("modelfile-view") && ($("modelfile-view").hidden = true);
  }

  if (typeof closeDownloads === "function") {
    closeDownloads();
  }

  currentView = "hf";
  const hfView = $("hf-view");
  if (hfView) hfView.hidden = false;

  $("chat-btn")?.classList.remove("active");
  $("settings-btn")?.classList.remove("active");
  $("modelfile-btn")?.classList.remove("active");
  $("hf-topbar-btn")?.classList.add("active");

  if (window.location.pathname !== "/hf" && window.location.pathname !== "/huggingface") {
    history.pushState(null, "", "/hf");
  }

  updateHFMemoryBanner();
  const input = $("hf-search-input");
  if (input && !input.value.trim() && hfModels.length === 0) {
    doHFSearch("");
  } else {
    renderHFModelsList();
  }
  setTimeout(() => input?.focus(), 50);
}

function updateHFMemoryBanner() {
  const banner = $("hf-memory-banner");
  if (!banner) return;
  const vramBytes = Number(lastSystemStatus?.vram_total || 0);
  const ramBytes = Number(lastSystemStatus?.memory_total || 0);
  const vramText = vramBytes > 0 ? fmtBytes(vramBytes) : "—";
  const ramText = ramBytes > 0 ? fmtBytes(ramBytes) : "—";
  banner.textContent = t("hf.sys_mem_info", { vram: vramText, ram: ramText });
}

/**
 * Calculates memory fit based on GGUF size, optional vision projector, and current system specs.
 * Returns: { level: 'optimal'|'partial'|'exceeds', label: string, desc: string, totalBytes: number }
 */
function computeMemoryFit(modelSizeBytes, visionSizeBytes = 0) {
  const vramTotal = Number(lastSystemStatus?.vram_total || 0);
  const ramTotal = Number(lastSystemStatus?.memory_total || 0);
  const totalModelBytes = (modelSizeBytes || 0) + (visionSizeBytes || 0);

  // Approximate KV cache & context overhead (~1GB)
  const ctxOverhead = 1024 * 1024 * 1024;
  const neededBytes = totalModelBytes + ctxOverhead;

  if (vramTotal > 0 && neededBytes <= vramTotal) {
    return {
      level: "optimal",
      badgeClass: "hf-fit-optimal",
      label: t("hf.fit_optimal"),
      desc: t("hf.fit_optimal_desc"),
      totalBytes: totalModelBytes,
    };
  }

  const combinedMemory = vramTotal + ramTotal;
  if (combinedMemory > 0 && neededBytes <= combinedMemory) {
    return {
      level: "partial",
      badgeClass: "hf-fit-partial",
      label: t("hf.fit_partial"),
      desc: t("hf.fit_partial_desc"),
      totalBytes: totalModelBytes,
    };
  }

  return {
    level: "exceeds",
    badgeClass: "hf-fit-exceeds",
    label: t("hf.fit_exceeds"),
    desc: t("hf.fit_exceeds_desc"),
    totalBytes: totalModelBytes,
  };
}

let hfAutoFetchDepth = 0;

function getVisibleHFCount() {
  const now = Date.now();
  const ONE_DAY = 24 * 60 * 60 * 1000;
  return hfModels.filter((m) => {
    if (hfCurrentFilter === "ollama" && !m.has_ollama) return false;
    if (hfCurrentFilter === "vision" && !m.has_vision) return false;

    if (hfCurrentTimeFilter !== "all" && m.last_modified) {
      const modTime = new Date(m.last_modified).getTime();
      if (!isNaN(modTime)) {
        const diffDays = (now - modTime) / ONE_DAY;
        if (hfCurrentTimeFilter === "week" && diffDays > 7) return false;
        if (hfCurrentTimeFilter === "month" && diffDays > 30) return false;
        if (hfCurrentTimeFilter === "6months" && diffDays > 180) return false;
        if (hfCurrentTimeFilter === "year" && diffDays > 365) return false;
      }
    }
    return true;
  }).length;
}

async function doHFSearch(query, append = false) {
  const listEl = $("hf-models-list");
  const loadingEl = $("hf-loading");
  const emptyEl = $("hf-empty");
  const errorEl = $("hf-error");
  const loadMoreWrap = $("hf-load-more-wrap");

  if (!append) {
    hfNextCursor = "";
    hfAutoFetchDepth = 0;
    if (loadingEl) loadingEl.hidden = false;
    if (emptyEl) emptyEl.hidden = true;
    if (errorEl) errorEl.hidden = true;
    if (loadMoreWrap) loadMoreWrap.hidden = true;
    if (listEl) listEl.innerHTML = "";
  }

  try {
    const params = new URLSearchParams();
    if (query && query.trim()) params.set("q", query.trim());
    params.set("sort", hfCurrentSort);
    params.set("filter", hfCurrentFilter);
    params.set("limit", "30");
    if (append && hfNextCursor) {
      params.set("cursor", hfNextCursor);
    }

    const data = await api(`/api/hf/search?${params.toString()}`);
    const incoming = Array.isArray(data?.models) ? data.models : [];
    hfNextCursor = data?.next_cursor || "";
    hfHasMore = incoming.length >= 30 && !!hfNextCursor;

    if (append) {
      const existing = new Set(hfModels.map((m) => m.id));
      const newItems = incoming.filter((m) => !existing.has(m.id));
      hfModels.push(...newItems);
    } else {
      hfModels = incoming;
    }

    if (loadingEl) loadingEl.hidden = true;
    renderHFModelsList();

    // If after date/type filtering we have very few visible results (< 12)
    // and there are more pages available, automatically fetch the next batch (up to 3 continuous pulls)
    const visibleCount = getVisibleHFCount();
    if (visibleCount < 12 && hfHasMore && hfNextCursor && (hfAutoFetchDepth || 0) < 3) {
      hfAutoFetchDepth = (hfAutoFetchDepth || 0) + 1;
      await doHFSearch(query, true);
    } else {
      hfAutoFetchDepth = 0;
    }
  } catch (err) {
    if (loadingEl) loadingEl.hidden = true;
    if (errorEl) {
      errorEl.textContent = `${t("hf.error")} (${err.message})`;
      errorEl.hidden = false;
    }
  }
}

function renderHFModelsList() {
  const listEl = $("hf-models-list");
  const emptyEl = $("hf-empty");
  const loadMoreWrap = $("hf-load-more-wrap");
  if (!listEl) return;

  const now = Date.now();
  const ONE_DAY = 24 * 60 * 60 * 1000;

  const filtered = hfModels.filter((m) => {
    if (hfCurrentFilter === "ollama" && !m.has_ollama) return false;
    if (hfCurrentFilter === "vision" && !m.has_vision) return false;

    if (hfCurrentTimeFilter !== "all" && m.last_modified) {
      const modTime = new Date(m.last_modified).getTime();
      if (!isNaN(modTime)) {
        const diffDays = (now - modTime) / ONE_DAY;
        if (hfCurrentTimeFilter === "week" && diffDays > 7) return false;
        if (hfCurrentTimeFilter === "month" && diffDays > 30) return false;
        if (hfCurrentTimeFilter === "6months" && diffDays > 180) return false;
        if (hfCurrentTimeFilter === "year" && diffDays > 365) return false;
      }
    }
    return true;
  });

  if (filtered.length === 0) {
    listEl.innerHTML = "";
    if (emptyEl) emptyEl.hidden = false;
    if (loadMoreWrap) loadMoreWrap.hidden = true;
    return;
  }
  if (emptyEl) emptyEl.hidden = true;

  listEl.innerHTML = filtered.map((m) => hfModelCardHTML(m)).join("");
  if (loadMoreWrap) {
    loadMoreWrap.hidden = !hfHasMore;
  }
}

function normalizeHFModelName(name) {
  if (!name) return "";
  return String(name).toLowerCase().replace(/^hf\.co\//, "").trim();
}

function getHFModelInstallStatus(repoId) {
  if (!repoId) return { installedCount: 0, ghostCount: 0 };
  const target = normalizeHFModelName(repoId);

  const installedMatches = (models || []).filter((m) => {
    const norm = normalizeHFModelName(m.name || "");
    return norm === target || norm.startsWith(target + ":") || norm.includes(target);
  });

  const installedNames = new Set(installedMatches.map((m) => normalizeHFModelName(m.name)));

  const ghostMatches = (ghostModels || []).filter((g) => {
    const norm = normalizeHFModelName(g.name || "");
    return (norm === target || norm.startsWith(target + ":") || norm.includes(target)) && !installedNames.has(norm);
  });

  return {
    installedCount: installedMatches.length,
    ghostCount: ghostMatches.length,
  };
}

function getHFQuantInstallStatus(pullName) {
  if (!pullName) return { isInstalled: false, wasInstalled: false };
  const target = normalizeHFModelName(pullName);

  const isInstalled = (models || []).some((m) => {
    const norm = normalizeHFModelName(m.name || "");
    return norm === target || norm.startsWith(target + ":");
  });

  const wasInstalled = !isInstalled && (ghostModels || []).some((g) => {
    const norm = normalizeHFModelName(g.name || "");
    return norm === target || norm.startsWith(target + ":");
  });

  return { isInstalled, wasInstalled };
}

function hfModelCardHTML(m) {
  const author = escapeHtml(m.author || m.id.split("/")[0] || "");
  const name = escapeHtml(m.name || m.id);
  const dlCount = Number(m.downloads || 0).toLocaleString();
  const likesCount = Number(m.likes || 0).toLocaleString();
  const updatedTime = m.last_modified ? fmtRelativeTime(m.last_modified) : "";

  const installStatus = getHFModelInstallStatus(m.id);
  let statusBadge = "";
  let cardClass = "hf-card";

  if (installStatus.installedCount > 0) {
    cardClass += " hf-card-installed";
    statusBadge = `<span class="badge badge-success" title="${escapeHtml(t("hf.card_installed_tooltip", { count: installStatus.installedCount }))}">💾 ${escapeHtml(t("hf.card_installed"))}</span>`;
  } else if (installStatus.ghostCount > 0) {
    cardClass += " hf-card-had";
    statusBadge = `<span class="badge badge-subtle hf-badge-history" title="${escapeHtml(t("hf.card_had_tooltip", { count: installStatus.ghostCount }))}">🕒 ${escapeHtml(t("hf.card_had"))}</span>`;
  }

  let tagsHTML = "";
  if (statusBadge) tagsHTML += statusBadge + " ";
  const qCountText = m.gguf_count > 0 ? t("hf.quants_tag", { n: m.gguf_count }) : t("hf.tag_gguf");
  tagsHTML += `<span class="badge badge-subtle">${escapeHtml(qCountText)}</span>`;
  if (m.has_ollama) {
    tagsHTML += ` <span class="badge badge-accent">${t("hf.tag_ollama")}</span>`;
  }
  if (m.has_vision) {
    tagsHTML += ` <span class="badge badge-vision">${t("hf.tag_vision")}</span>`;
  }

  const hfUrl = `https://huggingface.co/${m.id.split("/").map(encodeURIComponent).join("/")}`;

  return `
    <div class="${cardClass}" data-repo-id="${escapeHtml(m.id)}">
      <div class="hf-card-head">
        <div class="hf-card-title-wrap">
          <span class="hf-card-author">${author} /</span>
          <h4 class="hf-card-title">${name}</h4>
        </div>
        <a href="${hfUrl}" target="_blank" rel="noopener noreferrer" class="hf-ext-link" title="${escapeHtml(t("hf.view_on_hf"))}" onclick="event.stopPropagation();">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"></path>
            <polyline points="15 3 21 3 21 9"></polyline>
            <line x1="10" y1="14" x2="21" y2="3"></line>
          </svg>
        </a>
      </div>
      <div class="hf-card-tags">
        ${tagsHTML}
      </div>
      <div class="hf-card-stats">
        <span class="hf-stat" title="${dlCount} downloads">⬇️ ${dlCount}</span>
        <span class="hf-stat" title="${likesCount} likes">❤️ ${likesCount}</span>
        ${updatedTime ? `<span class="hf-stat muted">${escapeHtml(t("hf.updated", { time: updatedTime }))}</span>` : ""}
      </div>
    </div>
  `;
}

async function openHFModelDetail(repoId) {
  const modal = $("hf-detail-modal");
  if (!modal) return;

  $("hf-detail-name").textContent = repoId;
  $("hf-detail-loading").hidden = false;
  $("hf-detail-body").hidden = true;
  modal.hidden = false;
  hfActiveTab = "quants";
  switchHFDetailTab("quants");

  try {
    const data = await api(`/api/hf/model?id=${encodeURIComponent(repoId)}`);
    hfActiveModel = data;
    renderHFModelDetail(data);
    $("hf-detail-loading").hidden = true;
    $("hf-detail-body").hidden = false;
  } catch (err) {
    $("hf-detail-loading").hidden = true;
    toast(t("toast.error", { msg: err.message }), "error");
    closeHFModelDetail();
  }
}

function closeHFModelDetail() {
  const modal = $("hf-detail-modal");
  if (modal) modal.hidden = true;
  hfActiveModel = null;
}

function renderHFModelDetail(m) {
  $("hf-detail-author").textContent = m.author || m.id.split("/")[0] || "";
  $("hf-detail-name").textContent = m.name || m.id;
  $("hf-detail-downloads").textContent = `⬇️ ${Number(m.downloads || 0).toLocaleString()}`;
  $("hf-detail-likes").textContent = `❤️ ${Number(m.likes || 0).toLocaleString()}`;
  $("hf-detail-link").href = `https://huggingface.co/${m.id.split("/").map(encodeURIComponent).join("/")}`;

  // Vision Projector Notice
  const visionBox = $("hf-detail-vision-box");
  if (visionBox) {
    if (m.has_vision && Array.isArray(m.vision_files) && m.vision_files.length > 0) {
      const vFile = m.vision_files[0];
      visionBox.textContent = t("hf.vision_projector_note", {
        file: vFile.filename,
        size: fmtBytes(vFile.size_bytes),
      });
      visionBox.hidden = false;
    } else if (m.has_vision) {
      visionBox.textContent = t("hf.tag_vision") + ": Multimodal vision support.";
      visionBox.hidden = false;
    } else {
      visionBox.hidden = true;
    }
  }

  // Render Quants Table
  renderHFQuantsTable(m);
}

function renderHFQuantsTable(m) {
  const tbody = $("hf-quants-tbody");
  if (!tbody) return;

  const ggufFiles = Array.isArray(m.gguf_files) ? m.gguf_files : [];
  if (ggufFiles.length === 0) {
    tbody.innerHTML = `
      <tr>
        <td colspan="5" class="muted text-center" style="padding: 36px 20px;">
          <div style="font-size: 15px; font-weight: 600; margin-bottom: 6px; color: var(--text);">⚠️ ${escapeHtml(t("hf.no_gguf_files"))}</div>
          <div class="small muted" style="max-width: 480px; margin: 0 auto; line-height: 1.5;">${escapeHtml(t("hf.no_gguf_files_desc"))}</div>
        </td>
      </tr>
    `;
    return;
  }

  // Vision size to account for
  let visionSize = 0;
  if (Array.isArray(m.vision_files) && m.vision_files.length > 0) {
    visionSize = m.vision_files[0].size_bytes || 0;
  }

  // Filter out imatrix, mtp, draft, and auxiliary non-standalone files
  const validFiles = ggufFiles.filter((f) => {
    const fn = (f.filename || "").toLowerCase();
    const q = (f.quant || "").toUpperCase();
    if (q === "IMATRIX" || q === "AUXILIARY" || q === "MMPROJ") return false;
    if (fn.includes("imatrix") || fn.endsWith(".dat")) return false;
    if (fn.startsWith("mtp-") || fn.startsWith("mtp_") || fn.includes("-mtp-") || fn.includes("-mtp.") || fn.includes("_mtp_") || fn.includes("_mtp.")) return false;
    if (fn.startsWith("draft-") || fn.startsWith("draft_") || fn.includes("-draft-") || fn.includes("-draft.") || fn.includes("_draft_") || fn.includes("_draft.")) return false;
    return true;
  });

  // Sort files by size ascending
  const sortedFiles = [...validFiles].sort((a, b) => (a.size_bytes || 0) - (b.size_bytes || 0));

  // Determine recommended quant using priority list
  const quantPriority = [
    "Q4_K_M", "Q5_K_M", "Q4_K_S", "Q5_K_S", "IQ4_XS", "IQ4_NL",
    "Q4_0", "Q4_1", "Q3_K_M", "Q3_K_L", "Q3_K_S", "IQ3_M", "IQ3_S",
    "Q6_K", "Q8_0", "Q2_K"
  ];

  function pickBestQuant(files) {
    for (const q of quantPriority) {
      const match = files.find((f) => f.quant === q);
      if (match) return match;
    }
    return files.find((f) => f.quant !== "OTHER") || files[0] || null;
  }

  let recommendedFile = null;
  const optimalFiles = sortedFiles.filter((f) => computeMemoryFit(f.size_bytes, visionSize).level === "optimal");
  if (optimalFiles.length > 0) {
    recommendedFile = pickBestQuant(optimalFiles);
  } else {
    const sharedFiles = sortedFiles.filter((f) => computeMemoryFit(f.size_bytes, visionSize).level === "shared");
    if (sharedFiles.length > 0) {
      recommendedFile = pickBestQuant(sharedFiles);
    } else if (sortedFiles.length > 0) {
      recommendedFile = pickBestQuant(sortedFiles);
    }
  }

  tbody.innerHTML = sortedFiles.map((f) => {
    const fit = computeMemoryFit(f.size_bytes, visionSize);
    const isRec = recommendedFile && recommendedFile.filename === f.filename;
    const pullName = f.pull_name || f.pullName || (m.id ? `hf.co/${m.id}:${f.quant}` : "");
    
    const quantStatus = getHFQuantInstallStatus(pullName);
    const isInstalled = quantStatus.isInstalled;
    const wasInstalled = quantStatus.wasInstalled;
    
    let isQueued = false;
    let isDownloading = false;
    for (const j of jobs.values()) {
      if (j.name === pullName) {
        if (j.status === "running") isDownloading = true;
        else if (j.status === "queued") isQueued = true;
      }
    }

    let statusBtn = "";
    if (isInstalled) {
      statusBtn = `<span class="badge badge-success">💾 ${escapeHtml(t("hf.installed_badge"))}</span>`;
    } else if (isDownloading) {
      statusBtn = `<span class="badge badge-accent">${escapeHtml(t("hf.downloading_badge"))}</span>`;
    } else if (isQueued) {
      statusBtn = `<span class="badge badge-subtle">${escapeHtml(t("hf.queued_badge"))}</span>`;
    } else if (wasInstalled) {
      statusBtn = `
        <button type="button" class="secondary btn-sm hf-install-btn" data-pull-name="${escapeHtml(pullName)}" title="ollama pull ${escapeHtml(pullName)}">
          🔄 ${escapeHtml(t("hf.reinstall_btn"))}
        </button>
      `;
    } else {
      statusBtn = `
        <button type="button" class="primary btn-sm hf-install-btn" data-pull-name="${escapeHtml(pullName)}" title="ollama pull ${escapeHtml(pullName)}">
          ${escapeHtml(t("hf.install_btn"))}
        </button>
      `;
    }

    const recBadge = isRec ? `<span class="badge badge-rec" title="${escapeHtml(t("hf.recommended_quant"))}">★ ${escapeHtml(t("hf.recommended_quant"))}</span>` : "";
    let quantStatusBadge = "";
    if (isInstalled) {
      quantStatusBadge = ` <span class="badge badge-success hf-quant-state-badge" title="${escapeHtml(t("hf.quant_installed"))}">💾 ${escapeHtml(t("hf.card_installed"))}</span>`;
    } else if (wasInstalled) {
      quantStatusBadge = ` <span class="badge badge-subtle hf-badge-history hf-quant-state-badge" title="${escapeHtml(t("hf.quant_was_installed_desc"))}">🕒 ${escapeHtml(t("hf.quant_was_installed"))}</span>`;
    }

    return `
      <tr class="${isRec ? "hf-row-recommended" : ""}">
        <td class="mono font-semibold">
          ${escapeHtml(f.quant)}
          ${recBadge}
          ${quantStatusBadge}
        </td>
        <td class="muted small mono hf-cell-filename" title="${escapeHtml(f.filename)}">${escapeHtml(f.filename)}</td>
        <td class="mono">${fmtBytes(f.size_bytes)}</td>
        <td>
          <span class="badge ${fit.badgeClass}" title="${escapeHtml(fit.desc)}">${escapeHtml(fit.label)}</span>
        </td>
        <td class="text-right">
          ${statusBtn}
        </td>
      </tr>
    `;
  }).join("");
}

function switchHFDetailTab(tab) {
  hfActiveTab = tab;
  const quantsTab = $("hf-tab-quants");
  const readmeTab = $("hf-tab-readme");
  const quantsSection = $("hf-section-quants");
  const readmeSection = $("hf-section-readme");

  if (tab === "quants") {
    quantsTab?.classList.add("active");
    readmeTab?.classList.remove("active");
    if (quantsSection) quantsSection.hidden = false;
    if (readmeSection) readmeSection.hidden = true;
  } else {
    readmeTab?.classList.add("active");
    quantsTab?.classList.remove("active");
    if (readmeSection) readmeSection.hidden = false;
    if (quantsSection) quantsSection.hidden = true;
    if (hfActiveModel) loadHFReadme(hfActiveModel.id);
  }
}

async function loadHFReadme(repoId) {
  const container = $("hf-readme-content");
  if (!container) return;

  if (hfReadmeCache.has(repoId)) {
    container.innerHTML = hfReadmeCache.get(repoId);
    return;
  }

  container.innerHTML = `<div class="muted text-center" style="padding: 30px;">${escapeHtml(t("hf.readme_loading"))}</div>`;

  try {
    const data = await api(`/api/hf/readme?id=${encodeURIComponent(repoId)}`);
    const raw = data?.content || "";
    if (!raw.trim()) {
      container.innerHTML = `<div class="muted text-center" style="padding: 30px;">${escapeHtml(t("hf.readme_error"))}</div>`;
      return;
    }
    const formatted = formatHFMarkdown(raw);
    hfReadmeCache.set(repoId, formatted);
    container.innerHTML = formatted;
  } catch (err) {
    container.innerHTML = `<div class="muted text-center danger-text" style="padding: 30px;">${escapeHtml(t("hf.readme_error"))} (${escapeHtml(err.message)})</div>`;
  }
}

function sanitizeHFHtml(html) {
  if (!html) return "";
  // Strip dangerous tags: script, style, iframe, object, embed, form, input
  let clean = html.replace(/<\s*(script|style|iframe|object|embed|form|input)[^>]*>[\s\S]*?<\s*\/\s*\1\s*>/gi, "");
  clean = clean.replace(/<\s*(script|style|iframe|object|embed|form|input)[^>]*>/gi, "");

  // Strip event handlers (onload, onerror, onclick, etc.) and javascript: links
  clean = clean.replace(/\son[a-zA-Z]+\s*=\s*(['"][^'"]*['"]|[^\s>]+)/gi, "");
  clean = clean.replace(/href\s*=\s*['"]\s*javascript:[^'"]*['"]/gi, 'href="#"');
  clean = clean.replace(/src\s*=\s*['"]\s*javascript:[^'"]*['"]/gi, 'src=""');

  // Enforce target="_blank" and rel="noopener noreferrer" on all links
  clean = clean.replace(/<a\s+(?:[^>]*?\s+)?href="([^"]*)"([^>]*)>/gi, (_m, href, rest) => {
    return `<a href="${href}" target="_blank" rel="noopener noreferrer"${rest}>`;
  });

  return clean;
}

function extractYAMLFrontmatter(md) {
  let text = String(md || "").replace(/\r\n/g, "\n");
  let metaHTML = "";

  // Check if starts with ---
  if (text.startsWith("---\n")) {
    const endIdx = text.indexOf("\n---\n", 4);
    if (endIdx !== -1) {
      const yamlContent = text.substring(4, endIdx);
      text = text.substring(endIdx + 5);

      const metaPills = [];
      const lines = yamlContent.split("\n");
      for (const line of lines) {
        const trimmed = line.trim();
        if (!trimmed || trimmed.startsWith("#")) continue;

        if (trimmed.startsWith("base_model:")) {
          const val = trimmed.replace("base_model:", "").replace(/[[\]'"]/g, "").trim();
          if (val) metaPills.push(`<span class="badge badge-subtle">🏷️ Base: <strong>${escapeHtml(val)}</strong></span>`);
        } else if (trimmed.startsWith("license:")) {
          const val = trimmed.replace("license:", "").replace(/['"]/g, "").trim();
          if (val) metaPills.push(`<span class="badge badge-subtle">⚖️ License: <strong>${escapeHtml(val)}</strong></span>`);
        } else if (trimmed.startsWith("pipeline_tag:")) {
          const val = trimmed.replace("pipeline_tag:", "").replace(/['"]/g, "").trim();
          if (val) metaPills.push(`<span class="badge badge-subtle">⚡ ${escapeHtml(val)}</span>`);
        }
      }

      if (metaPills.length > 0) {
        metaHTML = `<div class="hf-readme-meta-strip">${metaPills.join(" ")}</div>`;
      }
    }
  }

  return { cleanText: text, metaHTML };
}

function extractGFMTables(text, tables) {
  const lines = text.split("\n");
  const result = [];
  let tableLines = [];

  function flushTable() {
    if (tableLines.length >= 2) {
      const headerLine = tableLines[0];
      const alignLine = tableLines[1];
      const dataLines = tableLines.slice(2);

      const headers = headerLine.split("|").slice(1, -1).map((h) => h.trim());
      const aligns = alignLine.split("|").slice(1, -1).map((a) => {
        a = a.trim();
        if (a.startsWith(":") && a.endsWith(":")) return "center";
        if (a.endsWith(":")) return "right";
        return "left";
      });

      let html = '<div class="table-wrap"><table class="hf-quants-table"><thead><tr>';
      headers.forEach((h, i) => {
        const align = aligns[i] || "left";
        html += `<th style="text-align:${align}">${h}</th>`;
      });
      html += '</tr></thead><tbody>';

      dataLines.forEach((row) => {
        const cells = row.split("|").slice(1, -1).map((c) => c.trim());
        if (cells.length > 0) {
          html += '<tr>';
          headers.forEach((_, i) => {
            const align = aligns[i] || "left";
            const cell = cells[i] || "";
            html += `<td style="text-align:${align}">${cell}</td>`;
          });
          html += '</tr>';
        }
      });
      html += '</tbody></table></div>';

      const key = `@@TABLE_${tables.length}@@`;
      tables.push(html);
      result.push(key);
    } else {
      result.push(...tableLines);
    }
    tableLines = [];
  }

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim();
    if (line.startsWith("|") && line.endsWith("|")) {
      tableLines.push(line);
    } else {
      if (tableLines.length) flushTable();
      result.push(lines[i]);
    }
  }
  if (tableLines.length) flushTable();

  return result.join("\n");
}

function formatHFMarkdown(md) {
  if (!md) return "";

  // 1. Extract and format YAML frontmatter
  const { cleanText, metaHTML } = extractYAMLFrontmatter(md);
  let work = cleanText;

  // 2. Extract code blocks so markdown rules don't tamper with them
  const codeBlocks = [];
  work = work.replace(/```([a-zA-Z0-9_-]*)\n([\s\S]*?)```/g, (_m, lang, code) => {
    const key = `@@CODEBLOCK_${codeBlocks.length}@@`;
    const langLabel = lang ? escapeHtml(lang) : "code";
    const escapedCode = escapeHtml(code.trim());
    codeBlocks.push(`
      <div class="chat-code-wrap">
        <div class="chat-code-header">
          <span class="chat-code-lang">${langLabel}</span>
          <button type="button" class="chat-code-copy-btn" data-code="${escapedCode}" onclick="navigator.clipboard.writeText(this.dataset.code); this.textContent='✓ Copied'; setTimeout(() => this.textContent='Copy', 2000);">Copy</button>
        </div>
        <pre class="chat-code mono"><code>${escapedCode}</code></pre>
      </div>
    `);
    return key;
  });

  // 3. Extract GFM Tables
  const tableBlocks = [];
  work = extractGFMTables(work, tableBlocks);

  // 4. Markdown headers
  work = work.replace(/^######\s+(.+)$/gm, "<h6>$1</h6>");
  work = work.replace(/^#####\s+(.+)$/gm, "<h5>$1</h5>");
  work = work.replace(/^####\s+(.+)$/gm, "<h4>$1</h4>");
  work = work.replace(/^###\s+(.+)$/gm, "<h3>$1</h3>");
  work = work.replace(/^##\s+(.+)$/gm, "<h2>$1</h2>");
  work = work.replace(/^#\s+(.+)$/gm, "<h1>$1</h1>");

  // 5. Markdown images: ![alt](url)
  work = work.replace(/!\[([^\]]*)\]\((https?:\/\/[^\s)]+)\)/g, '<img src="$2" alt="$1" loading="lazy" class="hf-readme-img" />');

  // 6. Markdown links: [text](url)
  work = work.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1 ↗</a>');

  // 7. Bold & Italics
  work = work.replace(/\*\*\*([^*]+)\*\*\*/g, "<strong><em>$1</em></strong>");
  work = work.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  work = work.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  work = work.replace(/\*([^*\n]+)\*/g, "<em>$1</em>");
  work = work.replace(/_([^_\n]+)_/g, "<em>$1</em>");

  // 8. Strikethrough
  work = work.replace(/~~([^~]+)~~/g, "<del>$1</del>");

  // 9. Inline code: `code`
  work = work.replace(/`([^`\n]+)`/g, '<code class="mono">$1</code>');

  // 10. Horizontal rules
  work = work.replace(/^([-*_]){3,}$/gm, '<hr class="hf-readme-hr" />');

  // 11. Parse lists and blockquotes line by line
  const lines = work.split("\n");
  const out = [];
  let inUl = false;
  let inOl = false;
  let inQuote = false;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();

    // Bullet list
    if (/^[-*+]\s+(.*)$/.test(trimmed)) {
      if (inOl) { out.push("</ol>"); inOl = false; }
      if (!inUl) { out.push("<ul>"); inUl = true; }
      const item = trimmed.replace(/^[-*+]\s+/, "");
      out.push(`<li>${item}</li>`);
      continue;
    }

    // Numbered list
    if (/^\d+\.\s+(.*)$/.test(trimmed)) {
      if (inUl) { out.push("</ul>"); inUl = false; }
      if (!inOl) { out.push("<ol>"); inOl = true; }
      const item = trimmed.replace(/^\d+\.\s+/, "");
      out.push(`<li>${item}</li>`);
      continue;
    }

    // End lists if normal line
    if (inUl) { out.push("</ul>"); inUl = false; }
    if (inOl) { out.push("</ol>"); inOl = false; }

    // Blockquote
    if (/^>\s?(.*)$/.test(trimmed)) {
      if (!inQuote) { out.push("<blockquote>"); inQuote = true; }
      const quoteText = trimmed.replace(/^>\s?/, "");
      out.push(quoteText ? `<p>${quoteText}</p>` : "");
      continue;
    }
    if (inQuote) { out.push("</blockquote>"); inQuote = false; }

    // Table placeholder
    if (/^@@TABLE_\d+@@$/.test(trimmed)) {
      out.push(trimmed);
      continue;
    }

    // Codeblock placeholder
    if (/^@@CODEBLOCK_\d+@@$/.test(trimmed)) {
      out.push(trimmed);
      continue;
    }

    if (trimmed === "") {
      out.push("");
    } else {
      // Check if it's already an HTML block tag
      if (/^<(h[1-6]|div|p|table|blockquote|ul|ol|li|hr|details|summary|pre|img)/i.test(trimmed)) {
        out.push(trimmed);
      } else {
        out.push(`<p>${trimmed}</p>`);
      }
    }
  }

  if (inUl) out.push("</ul>");
  if (inOl) out.push("</ol>");
  if (inQuote) out.push("</blockquote>");

  let parsed = out.join("\n");

  // 12. Restore tables
  tableBlocks.forEach((tbl, idx) => {
    parsed = parsed.replace(`@@TABLE_${idx}@@`, tbl);
  });

  // 13. Restore code blocks
  codeBlocks.forEach((code, idx) => {
    parsed = parsed.replace(`@@CODEBLOCK_${idx}@@`, code);
  });

  // 14. Sanitize final HTML to prevent XSS while allowing clean rendering
  const sanitized = sanitizeHFHtml(parsed);

  return `
    <div class="hf-markdown-body">
      ${metaHTML}
      ${sanitized}
    </div>
  `;
}

// ---------- Event Listeners Bindings ----------

function bindHFExplorerEvents() {
  // Topbar button
  $("hf-topbar-btn")?.addEventListener("click", showHFView);

  // Open HF explorer from downloads modal button
  $("dl-open-hf-btn")?.addEventListener("click", () => {
    closeDownloads();
    showHFView();
  });

  // Back button in HF view -> back to models view
  $("hf-back-btn")?.addEventListener("click", () => {
    showModelsView();
    history.pushState(null, "", "/");
  });

  // Search input with debouncing
  $("hf-search-input")?.addEventListener("input", (e) => {
    const val = e.target.value;
    const clearBtn = $("hf-search-clear");
    if (clearBtn) clearBtn.hidden = !val;

    clearTimeout(hfSearchDebounce);
    hfSearchDebounce = setTimeout(() => {
      doHFSearch(val);
    }, 350);
  });

  $("hf-search-clear")?.addEventListener("click", () => {
    const input = $("hf-search-input");
    if (input) {
      input.value = "";
      $("hf-search-clear").hidden = true;
      doHFSearch("");
      input.focus();
    }
  });

  // Sort dropdown
  $("hf-sort-select")?.addEventListener("change", (e) => {
    hfCurrentSort = e.target.value;
    const query = $("hf-search-input")?.value || "";
    doHFSearch(query);
  });

  // Filter chips (Type)
  document.querySelectorAll(".hf-filter-chip").forEach((chip) => {
    chip.addEventListener("click", () => {
      document.querySelectorAll(".hf-filter-chip").forEach((c) => c.classList.remove("active"));
      chip.classList.add("active");
      hfCurrentFilter = chip.dataset.filter || "all";
      const q = $("hf-search-input")?.value || "";
      doHFSearch(q, false);
    });
  });

  // Filter chips (Time)
  document.querySelectorAll(".hf-time-chip").forEach((chip) => {
    chip.addEventListener("click", () => {
      document.querySelectorAll(".hf-time-chip").forEach((c) => c.classList.remove("active"));
      chip.classList.add("active");
      hfCurrentTimeFilter = chip.dataset.time || "all";
      renderHFModelsList();
    });
  });

  // Load more button
  $("hf-load-more-btn")?.addEventListener("click", async () => {
    if (hfLoadingMore || !hfNextCursor) return;
    hfLoadingMore = true;
    const btn = $("hf-load-more-btn");
    if (btn) {
      btn.disabled = true;
      btn.textContent = t("hf.loading_more");
    }
    const query = $("hf-search-input")?.value || "";
    await doHFSearch(query, true);
    hfLoadingMore = false;
    if (btn) {
      btn.disabled = false;
      btn.textContent = t("hf.load_more");
    }
  });

  // Card click -> open detail modal
  $("hf-models-list")?.addEventListener("click", (e) => {
    const card = e.target.closest(".hf-card");
    if (!card) return;
    const repoId = card.dataset.repoId;
    if (repoId) openHFModelDetail(repoId);
  });

  // Detail Modal tabs
  $("hf-tab-quants")?.addEventListener("click", () => switchHFDetailTab("quants"));
  $("hf-tab-readme")?.addEventListener("click", () => switchHFDetailTab("readme"));

  // Detail Modal close
  $("hf-detail-close")?.addEventListener("click", closeHFModelDetail);
  $("hf-detail-modal")?.addEventListener("click", (e) => {
    if (e.target === $("hf-detail-modal")) closeHFModelDetail();
  });

  // Install button inside Quants table
  $("hf-quants-tbody")?.addEventListener("click", async (e) => {
    const btn = e.target.closest(".hf-install-btn");
    if (!btn) return;
    const pullName = btn.dataset.pullName;
    if (!pullName) return;

    btn.disabled = true;
    const origText = btn.innerHTML;
    btn.textContent = "…";

    try {
      if (typeof promptDownloadModel === "function") {
        await promptDownloadModel(pullName);
      } else {
        await api("/api/pull", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ name: pullName }),
        });
        toast(t("downloads.enqueued", { name: pullName }), "success");
      }
      btn.textContent = t("hf.queued_badge");
      btn.className = "badge badge-subtle";
    } catch (err) {
      btn.disabled = false;
      btn.innerHTML = origText;
      toast(t("toast.error", { msg: err.message }), "error");
    }
  });
}

// Bind when DOM ready
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", bindHFExplorerEvents);
} else {
  bindHFExplorerEvents();
}
