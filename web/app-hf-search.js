"use strict";

// ---------- HuggingFace Model Explorer Section ----------

let hfModels = [];
let hfActiveModel = null;
let hfReadmeCache = new Map();
let hfSearchDebounce = null;
let hfCurrentFilter = "all"; // "all" | "ollama" | "vision"
let hfCurrentSort = "downloads"; // "downloads" | "likes" | "recent" | "trending"
let hfActiveTab = "quants"; // "quants" | "readme"

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

async function doHFSearch(query) {
  const listEl = $("hf-models-list");
  const loadingEl = $("hf-loading");
  const emptyEl = $("hf-empty");
  const errorEl = $("hf-error");

  if (loadingEl) loadingEl.hidden = false;
  if (emptyEl) emptyEl.hidden = true;
  if (errorEl) errorEl.hidden = true;
  if (listEl) listEl.innerHTML = "";

  try {
    const params = new URLSearchParams();
    if (query && query.trim()) params.set("q", query.trim());
    params.set("sort", hfCurrentSort);
    params.set("limit", "40");

    const data = await api(`/api/hf/search?${params.toString()}`);
    hfModels = Array.isArray(data?.models) ? data.models : [];
    if (loadingEl) loadingEl.hidden = true;
    renderHFModelsList();
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
  if (!listEl) return;

  const filtered = hfModels.filter((m) => {
    if (hfCurrentFilter === "ollama" && !m.has_ollama) return false;
    if (hfCurrentFilter === "vision" && !m.has_vision) return false;
    return true;
  });

  if (filtered.length === 0) {
    listEl.innerHTML = "";
    if (emptyEl) emptyEl.hidden = false;
    return;
  }
  if (emptyEl) emptyEl.hidden = true;

  listEl.innerHTML = filtered.map((m) => hfModelCardHTML(m)).join("");
}

function hfModelCardHTML(m) {
  const author = escapeHtml(m.author || m.id.split("/")[0] || "");
  const name = escapeHtml(m.name || m.id);
  const dlCount = Number(m.downloads || 0).toLocaleString();
  const likesCount = Number(m.likes || 0).toLocaleString();
  const updatedTime = m.last_modified ? fmtRelativeTime(m.last_modified) : "";

  let tagsHTML = `<span class="badge badge-subtle">${t("hf.tag_gguf")}</span>`;
  if (m.has_ollama) {
    tagsHTML += ` <span class="badge badge-accent">${t("hf.tag_ollama")}</span>`;
  }
  if (m.has_vision) {
    tagsHTML += ` <span class="badge badge-vision">${t("hf.tag_vision")}</span>`;
  }

  const hfUrl = `https://huggingface.co/${encodeURIComponent(m.id)}`;

  return `
    <div class="hf-card" data-repo-id="${escapeHtml(m.id)}">
      <div class="hf-card-head">
        <div class="hf-card-title-wrap">
          <span class="hf-card-author">${author} /</span>
          <h4 class="hf-card-title">${name}</h4>
        </div>
        <a href="${hfUrl}" target="_blank" rel="noopener noreferrer" class="btn-icon hf-ext-link" title="${escapeHtml(t("hf.view_on_hf"))}" onclick="event.stopPropagation();">↗</a>
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
  $("hf-detail-link").href = `https://huggingface.co/${encodeURIComponent(m.id)}`;

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
    tbody.innerHTML = `<tr><td colspan="5" class="muted text-center" style="padding: 24px;">${escapeHtml(t("hf.no_results"))}</td></tr>`;
    return;
  }

  // Vision size to account for
  let visionSize = 0;
  if (Array.isArray(m.vision_files) && m.vision_files.length > 0) {
    visionSize = m.vision_files[0].size_bytes || 0;
  }

  // Sort files by size ascending
  const sortedFiles = [...ggufFiles].sort((a, b) => (a.size_bytes || 0) - (b.size_bytes || 0));

  // Determine recommended quant
  let recommendedFile = sortedFiles.find((f) => f.quant === "Q4_K_M") || sortedFiles[0];
  const optimalFiles = sortedFiles.filter((f) => computeMemoryFit(f.size_bytes, visionSize).level === "optimal");
  if (optimalFiles.length > 0) {
    const pref = optimalFiles.find((f) => f.quant === "Q5_K_M") || optimalFiles.find((f) => f.quant === "Q4_K_M");
    if (pref) recommendedFile = pref;
    else recommendedFile = optimalFiles[optimalFiles.length - 1];
  }

  tbody.innerHTML = sortedFiles.map((f) => {
    const fit = computeMemoryFit(f.size_bytes, visionSize);
    const isRec = recommendedFile && recommendedFile.filename === f.filename;
    const isInstalled = models.some((local) => local.name === f.pullName || local.name.startsWith(f.pullName));
    
    let isQueued = false;
    let isDownloading = false;
    for (const j of jobs.values()) {
      if (j.name === f.pullName) {
        if (j.status === "running") isDownloading = true;
        else if (j.status === "queued") isQueued = true;
      }
    }

    let statusBtn = "";
    if (isInstalled) {
      statusBtn = `<span class="badge badge-success">${escapeHtml(t("hf.installed_badge"))}</span>`;
    } else if (isDownloading) {
      statusBtn = `<span class="badge badge-accent">${escapeHtml(t("hf.downloading_badge"))}</span>`;
    } else if (isQueued) {
      statusBtn = `<span class="badge badge-subtle">${escapeHtml(t("hf.queued_badge"))}</span>`;
    } else {
      statusBtn = `
        <button type="button" class="primary btn-sm hf-install-btn" data-pull-name="${escapeHtml(f.pullName)}" title="ollama pull ${escapeHtml(f.pullName)}">
          ${escapeHtml(t("hf.install_btn"))}
        </button>
      `;
    }

    const recBadge = isRec ? `<span class="badge badge-rec" title="${escapeHtml(t("hf.recommended_quant"))}">★ ${escapeHtml(t("hf.recommended_quant"))}</span>` : "";

    return `
      <tr class="${isRec ? "hf-row-recommended" : ""}">
        <td class="mono font-semibold">
          ${escapeHtml(f.quant)}
          ${recBadge}
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

function formatHFMarkdown(md) {
  if (!md) return "";
  let text = escapeHtml(md);

  // Fenced code blocks
  text = text.replace(/```([a-zA-Z0-9_-]*)\n([\s\S]*?)```/g, (_m, lang, code) => {
    return `<pre class="mono hf-code-block"><code class="language-${lang}">${code}</code></pre>`;
  });

  // Inline code
  text = text.replace(/`([^`]+)`/g, '<code class="mono">$1</code>');

  // Headers
  text = text.replace(/^### (.*$)/gim, "<h3>$1</h3>");
  text = text.replace(/^## (.*$)/gim, "<h2>$1</h2>");
  text = text.replace(/^# (.*$)/gim, "<h1>$1</h1>");

  // Bold & Italics
  text = text.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  text = text.replace(/\*([^*]+)\*/g, "<em>$1</em>");

  // Links
  text = text.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1 ↗</a>');

  // Paragraphs / linebreaks
  text = text.replace(/\n\n+/g, "</p><p>");
  return `<div class="hf-markdown-body"><p>${text}</p></div>`;
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

  // Filter chips
  document.querySelectorAll(".hf-filter-chip").forEach((chip) => {
    chip.addEventListener("click", () => {
      document.querySelectorAll(".hf-filter-chip").forEach((c) => c.classList.remove("active"));
      chip.classList.add("active");
      hfCurrentFilter = chip.dataset.filter || "all";
      renderHFModelsList();
    });
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
      btn.textContent = t("hf.install_btn");
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
