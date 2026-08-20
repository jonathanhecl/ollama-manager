"use strict";

// ---------- detail ----------
function openDetail(name) {
  activeName = name;
  const panel = $("detail-panel");
  panel.hidden = false;
  $("detail-name").textContent = name;
  if ($("detail-delete")) {
    $("detail-delete").hidden = false;
    $("detail-delete").dataset.name = name;
  }
  if ($("detail-chat")) {
    $("detail-chat").hidden = false;
    $("detail-chat").dataset.name = name;
  }
  if ($("detail-archive")) {
    $("detail-archive").hidden = false;
    $("detail-archive").dataset.name = name;
    const m = models.find(x => x.name === name);
    const isArchived = !!(m && m.archived);
    $("detail-archive").textContent = isArchived ? "📥" : "📦";
    $("detail-archive").title = isArchived ? t("detail.unarchive_title") : t("detail.archive_title");
  }
  $("detail-body").innerHTML = `<div class="muted">${escapeHtml(t("state.loading"))}</div>`;
  document.querySelectorAll("tbody tr.row").forEach((tr) => {
    tr.classList.toggle("active", tr.dataset.name === name);
  });
  api("/api/models/" + encodeURIComponent(name)).then(renderDetail).catch((e) => {
    $("detail-body").innerHTML = `<div class="muted">${escapeHtml(t("state.error_prefix") + e.message)}</div>`;
  });
}

function modelHomepageUrl(name) {
  if (!name) return "";
  let base = name.split(":")[0];
  if (base.startsWith("hf.co/")) {
    const repo = base.slice("hf.co/".length);
    return repo ? "https://huggingface.co/" + repo : "";
  }
  if (base.includes("/")) {
    return "https://ollama.com/" + base;
  }
  return base ? "https://ollama.com/library/" + base : "";
}

function renderDetail(d) {
  const m = models.find((x) => x.name === d.name) || {};
  const stateText = m.loaded
    ? t("detail.loaded_vram", { size: fmtBytes(m.size_vram) })
    : t("detail.not_loaded");
  const lastUsedVal = (d.last_used_at || m.last_used_at)
    ? fmtDateTimeFull(d.last_used_at || m.last_used_at)
    : "—";
  const recordToksVal = (d.record_tokens_per_sec || m.record_tokens_per_sec) > 0
    ? `${(d.record_tokens_per_sec || m.record_tokens_per_sec).toFixed(1)} tok/s${(d.record_tokens_per_sec_at || m.record_tokens_per_sec_at) ? ` (${fmtDate(d.record_tokens_per_sec_at || m.record_tokens_per_sec_at)})` : ""}`
    : "—";
  const minColdLoadVal = (d.min_cold_load_ms || m.min_cold_load_ms) > 0
    ? `${fmtColdLoad(d.min_cold_load_ms || m.min_cold_load_ms)}${(d.min_cold_load_at || m.min_cold_load_at) ? ` (${fmtDate(d.min_cold_load_at || m.min_cold_load_at)})` : ""}`
    : "—";
  const siteUrl = modelHomepageUrl(d.name);
  const hostLabel = siteUrl ? (siteUrl.startsWith("https://huggingface.co") ? "Hugging Face" : "Ollama") : "";
  const rows = [
    [t("detail.family"), d.details?.family || "—", false],
    [t("detail.architecture"), d.architecture || "—", false],
    [t("detail.params"), d.details?.parameter_size || (d.parameter_count ? `${(d.parameter_count / 1e9).toFixed(2)}B` : "—"), false],
    [t("detail.quant"), d.details?.quantization_level || "—", false],
    [t("detail.format"), d.details?.format || "—", false],
    [t("detail.context"), fmtCtx(d.context_length), false],
    [t("detail.size"), fmtBytes(m.size), false],
    [t("detail.record_tokens"), recordToksVal, false],
    [t("detail.min_cold_load"), minColdLoadVal, false],
    [t("detail.last_used"), lastUsedVal, false],
    [t("detail.state"), stateText, true],
    [t("detail.modified"), new Date(d.modified_at).toLocaleString(), false],
    [t("detail.digest"), `<span class="mono">${escapeHtml((m.digest || "").slice(0, 16))}…</span>`, false],
  ];
  if (siteUrl) {
    rows.push([t("detail.site"), `<a class="detail-site-link" href="${siteUrl}" target="_blank" rel="noopener noreferrer">${escapeHtml(hostLabel)}</a>`, false]);
  }
  if (d.artifact_count > 0) {
    const artSize = d.artifact_bytes ? ` · ${fmtBytes(d.artifact_bytes)}` : "";
    rows.push([t("detail.artifacts"), `${d.artifact_count}${artSize}`, false]);
  }
  const grid = rows.map(([k, v, isState]) =>
    `<div class="k">${escapeHtml(k)}</div><div class="v"${isState ? " id=\"detail-state-value\"" : ""}>${isState ? escapeHtml(v) : v}</div>`).join("");

  const caps = renderCapabilityPills(d.capabilities);
  const capsBlock = caps ? `<div class="detail-section"><h3>${escapeHtml(t("detail.capabilities"))}</h3><div class="cap-list">${caps}</div></div>` : "";

  const paramsBlock = d.parameters ? detailCodeSection(t("detail.parameters_section"), d.parameters) : "";
  const tmplBlock = d.template ? detailCodeSection(t("detail.template"), d.template) : "";
  const systemBlock = detailCodeSection(t("detail.system"), d.system || "", true);
  const repairBlock = renderRepairEntry(d);

  const updateBlock = `<div class="detail-section detail-update-section">
    <button type="button" class="ghost detail-update-btn" id="detail-update-btn" data-name="${escapeHtml(d.name)}">⟳ ${escapeHtml(t("detail.update_btn"))}</button>
  </div>`;

  $("detail-body").innerHTML = `<div class="detail-grid">${grid}</div>${updateBlock}${capsBlock}${repairBlock}${paramsBlock}${tmplBlock}${systemBlock}`;
  bindRepairEntry(d);
  bindUpdateButton();
}

function detailCodeSection(title, text, alwaysShow) {
  const val = String(text ?? "");
  if (!alwaysShow && !val) return "";
  const emptyCls = val ? "" : " empty";
  return `<div class="detail-section${emptyCls}" data-copy-target>
    <div class="detail-section-head">
      <h3>${escapeHtml(title)}</h3>
      <button type="button" class="detail-copy-btn" title="${escapeHtml(t("detail.copy"))}" aria-label="${escapeHtml(t("detail.copy"))}">${escapeHtml(t("detail.copy"))}</button>
    </div>
    <pre>${escapeHtml(val)}</pre>
  </div>`;
}

$("detail-body").addEventListener("click", async (e) => {
  const btn = e.target.closest(".detail-copy-btn");
  if (!btn) return;
  const section = btn.closest("[data-copy-target]");
  if (!section) return;
  const pre = section.querySelector("pre");
  if (!pre) return;
  const ok = await copyTextToClipboard(pre.textContent);
  toast(ok ? t("chat.copied") : t("chat.copy_failed"), ok ? "success" : "error");
});

function bindUpdateButton() {
  const btn = $("detail-update-btn");
  if (!btn) return;
  btn.addEventListener("click", async () => {
    const name = btn.dataset.name;
    if (!name) return;
    btn.disabled = true;
    try {
      await api("/api/pull", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      toast(t("detail.update_enqueued", { name }), "success");
      openDownloads();
    } catch (err) {
      toast(t("toast.error", { msg: err.message }), "error");
    } finally {
      btn.disabled = false;
    }
  });
}

$("detail-close").addEventListener("click", () => {
  $("detail-panel").hidden = true;
  if ($("detail-delete")) {
    $("detail-delete").hidden = true;
    $("detail-delete").dataset.name = "";
  }
  if ($("detail-archive")) {
    $("detail-archive").hidden = true;
    $("detail-archive").dataset.name = "";
  }
  if ($("detail-chat")) {
    $("detail-chat").hidden = true;
    $("detail-chat").dataset.name = "";
  }
  activeName = null;
  document.querySelectorAll("tbody tr.row.active").forEach((tr) => tr.classList.remove("active"));
});

$("detail-chat")?.addEventListener("click", (e) => {
  const name = e.currentTarget?.dataset?.name || activeName;
  if (!name) return;
  $("detail-panel").hidden = true;
  if ($("detail-delete")) {
    $("detail-delete").hidden = true;
    $("detail-delete").dataset.name = "";
  }
  if ($("detail-archive")) {
    $("detail-archive").hidden = true;
    $("detail-archive").dataset.name = "";
  }
  if ($("detail-chat")) {
    $("detail-chat").hidden = true;
    $("detail-chat").dataset.name = "";
  }
  activeName = null;
  document.querySelectorAll("tbody tr.row.active").forEach((tr) => tr.classList.remove("active"));
  showChatViewWithModel(name);
});

$("detail-archive")?.addEventListener("click", (e) => {
  const name = e.currentTarget?.dataset?.name || activeName;
  if (!name) return;
  const m = models.find(x => x.name === name);
  if (m) {
    toggleArchived(name, !m.archived);
  }
});

$("detail-delete")?.addEventListener("click", (e) => {
  const name = e.currentTarget?.dataset?.name || activeName;
  if (!name) return;
  confirmDelete(name);
});

async function toggleArchived(name, toArchive) {
  try {
    const endpoint = toArchive ? "/api/models/archive" : "/api/models/unarchive";
    await api(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    toast(toArchive ? t("toast.archived", { name }) : t("toast.unarchived", { name }), "success");
    await refreshModels();
    if (activeName === name) {
      openDetail(name);
    }
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

const REPAIR_CAPS = ["completion", "tools", "thinking", "vision", "audio", "embedding"];

function isFixedModelName(name) {
  return String(name || "").trim().endsWith(":fixed");
}

function fixedBaseName(name) {
  return isFixedModelName(name) ? String(name).trim().slice(0, -":fixed".length) : String(name || "").trim();
}

function fixedModelName(name) {
  const s = String(name || "").trim();
  if (!s) return "";
  const slash = s.lastIndexOf("/");
  const colon = s.lastIndexOf(":");
  if (colon > slash) return `${s.slice(0, colon)}:fixed`;
  return `${s}:fixed`;
}

function repairDefaultTemplate(d) {
  if (String(d?.template || "").trim()) return "keep";
  const arch = String(d?.architecture || d?.details?.family || "").toLowerCase();
  const name = String(d?.name || "").toLowerCase();
  if (arch.includes("glimmer") || arch.includes("muse") || name.includes("glimmer") || name.includes("muse")) return "muse_glimmer";
  if (arch.includes("qwen")) return "qwen35";
  if (arch.includes("llama")) return "llama3";
  if (arch.includes("gemma4") || arch.includes("gemma-4")) return "gemma4";
  if (arch.includes("gemma")) return "gemma";
  return "generic";
}

function extractStopTokens(modelfile) {
  const out = [];
  for (const line of String(modelfile || "").split("\n")) {
    const t = line.trim();
    if (!t || t.startsWith("#")) continue;
    const m = t.match(/^PARAMETER\s+stop\s+(.+)$/i);
    if (!m) continue;
    let v = m[1].trim();
    if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) {
      try { v = JSON.parse(v); } catch { v = v.slice(1, -1); }
    }
    out.push(v);
  }
  return out;
}

function renderRepairEntry(d) {
  if (isFixedModelName(d.name)) {
    const base = fixedBaseName(d.name);
    return `<div class="detail-section repair-entry">
      <h3>${escapeHtml(t("repair.title"))}</h3>
      <div class="repair-note">${escapeHtml(t("repair.fixed_note", { base }))}</div>
      <button type="button" class="ghost repair-open-base" data-base="${escapeHtml(base)}">${escapeHtml(t("repair.open_base"))}</button>
    </div>`;
  }
  const target = fixedModelName(d.name);
  return `<div class="detail-section repair-entry">
    <h3>${escapeHtml(t("repair.title"))}</h3>
    <div class="repair-note">${escapeHtml(t("repair.entry_note", { name: target }))}</div>
    <button type="button" class="ghost repair-open-modal">${escapeHtml(t("repair.options_btn"))}</button>
  </div>`;
}

function renderRepairModalContent(d) {
  if (isFixedModelName(d.name)) {
    const base = fixedBaseName(d.name);
    return `<div class="repair-card">
      <div class="repair-note">${escapeHtml(t("repair.fixed_note", { base }))}</div>
      <button type="button" class="ghost repair-open-base" data-base="${escapeHtml(base)}">${escapeHtml(t("repair.open_base"))}</button>
    </div>`;
  }

  const detected = new Set((d.capabilities || []).map((c) => String(c).toLowerCase()));
  const detectedHtml = (d.capabilities || []).length
    ? `<div class="cap-list repair-detected">${(d.capabilities || []).map((c) => `<span class="pill">${escapeHtml(c)}</span>`).join("")}</div>`
    : `<div class="muted">${escapeHtml(t("repair.detected_none"))}</div>`;
  const capsHtml = REPAIR_CAPS.map((cap) => {
    const label = t(`chat.cap.${cap}`);
    const isDetected = detected.has(cap);
    const hint = isDetected ? `<span>${escapeHtml(t("repair.detected"))}</span>` : "";
    return `<label class="repair-check">
      <input type="checkbox" name="repair-cap" value="${escapeHtml(cap)}"${isDetected ? " checked disabled" : ""}>
      <span>${escapeHtml(label)}</span>
      ${hint}
    </label>`;
  }).join("");
  const target = fixedModelName(d.name);
  const template = repairDefaultTemplate(d);
  const baseStops = extractStopTokens(d.modelfile);
  const stopsValue = escapeHtml(baseStops.join("\n"));
  return `<div class="repair-card">
    <label class="repair-check repair-fix-load">
      <input type="checkbox" id="repair-fix-load">
      <span>${escapeHtml(t("repair.fix_load"))}</span>
    </label>
    <div class="repair-warning">${escapeHtml(t("repair.warning"))}</div>
    <div class="repair-subtitle">${escapeHtml(t("repair.detected_caps"))}</div>
    ${detectedHtml}
    <div class="repair-subtitle">${escapeHtml(t("repair.flags"))}</div>
    <div class="repair-caps">${capsHtml}</div>
    <div class="repair-form-grid">
      <label>
        <span>${escapeHtml(t("repair.template"))}</span>
        <select id="repair-template">
          <option value="keep"${template === "keep" ? " selected" : ""}>${escapeHtml(t("repair.template_keep"))}</option>
          <option value="qwen35"${template === "qwen35" ? " selected" : ""}>Qwen 3 / 3.5</option>
          <option value="llama3"${template === "llama3" ? " selected" : ""}>Llama 3</option>
          <option value="gemma"${template === "gemma" ? " selected" : ""}>Gemma</option>
          <option value="gemma4"${template === "gemma4" ? " selected" : ""}>Gemma 4</option>
          <option value="gemma2_unsloth"${template === "gemma2_unsloth" ? " selected" : ""}>Gemma 2 / 4 (Unsloth)</option>
          <option value="muse_glimmer"${template === "muse_glimmer" ? " selected" : ""}>Muse Glimmer</option>
          <option value="hf_generic"${template === "hf_generic" ? " selected" : ""}>HuggingFace / GGUF</option>
          <option value="generic"${template === "generic" ? " selected" : ""}>ChatML</option>
        </select>
      </label>
      <label>
        <span>${escapeHtml(t("repair.context"))}</span>
        <select id="repair-context">
          <option value="keep" selected>${escapeHtml(t("repair.keep"))}</option>
          <option value="safe">${escapeHtml(t("repair.context_safe"))}</option>
          <option value="thinking">${escapeHtml(t("repair.context_thinking"))}</option>
        </select>
      </label>
      <label>
        <span>${escapeHtml(t("repair.temperature"))}</span>
        <select id="repair-temperature">
          <option value="keep">${escapeHtml(t("repair.keep"))}</option>
          <option value="tools">${escapeHtml(t("repair.temp_tools"))}</option>
          <option value="low">${escapeHtml(t("repair.temp_low"))}</option>
        </select>
      </label>
    </div>
    <div class="repair-subtitle">${escapeHtml(t("repair.stops"))}</div>
    <div class="repair-stops-row">
      <select id="repair-stops-mode">
        <option value="auto" selected>${escapeHtml(t("repair.stops_auto"))}</option>
        <option value="custom">${escapeHtml(t("repair.stops_custom"))}</option>
      </select>
    </div>
    <textarea id="repair-stops" class="repair-stops" spellcheck="false" placeholder="${escapeHtml(t("repair.stops_placeholder"))}" disabled>${stopsValue}</textarea>
    <div class="repair-stops-warn">${escapeHtml(t("repair.stops_warning"))}</div>
    <label class="repair-projector-field">
      <span>${escapeHtml(t("repair.projector"))}</span>
      <input id="repair-projector" type="text" placeholder="${escapeHtml(t("repair.projector_placeholder"))}" autocomplete="off" spellcheck="false">
    </label>
    <div class="repair-target">${escapeHtml(t("repair.target", { name: target }))}</div>
    <label class="repair-confirm">
      <input id="repair-confirm" type="checkbox">
      <span>${escapeHtml(t("repair.confirm"))}</span>
    </label>
    <div class="repair-actions">
      <button type="button" class="ghost" id="repair-preview-btn">${escapeHtml(t("repair.preview"))}</button>
      <button type="button" class="primary" id="repair-apply-btn" disabled>${escapeHtml(t("repair.apply"))}</button>
    </div>
    <div id="repair-status" class="muted repair-status"></div>
    <div id="repair-warnings" class="repair-warnings" hidden></div>
    <textarea id="repair-preview" class="repair-preview" spellcheck="false" hidden></textarea>
  </div>`;
}

function bindRepairEntry(d) {
  const openBase = document.querySelector(".repair-open-base");
  if (openBase) {
    openBase.addEventListener("click", () => openDetail(openBase.dataset.base));
  }
  const openModal = document.querySelector(".repair-open-modal");
  if (openModal) {
    openModal.addEventListener("click", () => openRepairModal(d));
  }
}

function openRepairModal(d) {
  $("repair-modal-title").textContent = `${t("repair.title")} · ${d.name}`;
  $("repair-modal-body").innerHTML = renderRepairModalContent(d);
  $("repair-modal").hidden = false;
  bindRepairControls(d);
}

function closeRepairModal() {
  const modal = $("repair-modal");
  if (!modal) return;
  modal.hidden = true;
  $("repair-modal-body").innerHTML = "";
}

function bindRepairControls(d) {
  const root = $("repair-modal-body");
  const openBase = root?.querySelector(".repair-open-base");
  if (openBase) {
    openBase.addEventListener("click", () => {
      closeRepairModal();
      openDetail(openBase.dataset.base);
    });
    return;
  }

  const previewBtn = $("repair-preview-btn");
  const applyBtn = $("repair-apply-btn");
  const confirm = $("repair-confirm");
  if (!previewBtn || !applyBtn || !confirm) return;

  let hasPreview = false;
  const updateApply = () => {
    const modelfile = $("repair-preview")?.value?.trim() || "";
    applyBtn.disabled = !(hasPreview && confirm.checked && modelfile);
  };
  const resetPreview = () => {
    hasPreview = false;
    const pre = $("repair-preview");
    if (pre) {
      pre.hidden = true;
      pre.value = "";
    }
    const warnings = $("repair-warnings");
    if (warnings) {
      warnings.hidden = true;
      warnings.innerHTML = "";
    }
    $("repair-status").textContent = "";
    updateApply();
  };
  confirm.addEventListener("change", updateApply);
  $("repair-preview")?.addEventListener("input", updateApply);
  root.querySelectorAll("input[name='repair-cap'], select").forEach((el) => {
    el.addEventListener("change", resetPreview);
  });
  $("repair-fix-load")?.addEventListener("change", resetPreview);

  const stopsMode = $("repair-stops-mode");
  const stopsArea = $("repair-stops");
  const syncStopsArea = () => {
    if (!stopsArea || !stopsMode) return;
    stopsArea.disabled = stopsMode.value !== "custom";
  };
  stopsMode?.addEventListener("change", syncStopsArea);
  syncStopsArea();
  stopsArea?.addEventListener("input", resetPreview);

  const projInput = $("repair-projector");
  const visionCapChk = root?.querySelector("input[name='repair-cap'][value='vision']");
  const syncVisionCap = () => {
    if (!projInput || !visionCapChk) return;
    const val = projInput.value.trim();
    if (val) {
      visionCapChk.checked = true;
    }
  };
  projInput?.addEventListener("input", () => {
    syncVisionCap();
    resetPreview();
  });
  projInput?.addEventListener("change", syncVisionCap);
  projInput?.addEventListener("paste", () => setTimeout(syncVisionCap, 10));

  previewBtn.addEventListener("click", async () => {
    try {
      previewBtn.disabled = true;
      $("repair-status").textContent = t("repair.previewing");
      const out = await api("/api/model-repair/preview", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(collectRepairRequest(d, false)),
      });
      renderRepairPreview(out);
      hasPreview = true;
      $("repair-status").textContent = t("repair.preview_ready");
    } catch (e) {
      hasPreview = false;
      $("repair-status").textContent = t("state.error_prefix") + e.message;
    } finally {
      previewBtn.disabled = false;
      updateApply();
    }
  });

  applyBtn.addEventListener("click", async () => {
    if (!confirm.checked) return;
    const target = fixedModelName(d.name);
    const exists = models.some((m) => m.name === target || m.model === target);
    const msg = exists ? t("repair.replace_confirm", { name: target }) : t("repair.apply_confirm", { name: target });
    const { ok } = await askConfirm({
      title: t("repair.apply"),
      text: msg,
      okText: exists ? t("repair.replace") : t("repair.create"),
      okClass: "primary",
      mono: target,
    });
    if (!ok) return;

    const setRepairProgress = (pct, text) => {
      const clamped = Math.max(0, Math.min(100, pct || 0));
      $("repair-status").innerHTML = `<div class="repair-progress-wrap">
        <div class="repair-progress-track"><div class="repair-progress-fill" style="width: ${clamped.toFixed(1)}%"></div></div>
        <div class="repair-progress-text">${escapeHtml(text)}</div>
      </div>`;
    };

    try {
      applyBtn.disabled = true;
      previewBtn.disabled = true;
      setRepairProgress(0, t("repair.applying"));

      const res = await fetch("/api/model-repair/apply", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Accept": "text/event-stream",
        },
        body: JSON.stringify(collectRepairRequest(d, true)),
      });

      if (!res.ok) {
        const txt = await res.text();
        let errMsg = txt;
        try {
          const errObj = JSON.parse(txt);
          if (errObj.error) errMsg = errObj.error;
        } catch (_) {}
        throw new Error(errMsg || `HTTP ${res.status}`);
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      let finalResult = null;

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop();

        let currentEvent = "message";
        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed) continue;
          if (trimmed.startsWith("event:")) {
            currentEvent = trimmed.slice(6).trim();
            continue;
          }
          if (trimmed.startsWith("data:")) {
            const dataStr = trimmed.slice(5).trim();
            try {
              const data = JSON.parse(dataStr);
              if (currentEvent === "progress") {
                if (data.stage === "downloading_projector") {
                  const completed = data.completed || 0;
                  const total = data.total || 0;
                  const pct = data.percent || 0;
                  let text = t("repair.downloading_projector");
                  if (total > 0) {
                    text += ` ${formatBytes(completed)} / ${formatBytes(total)} (${pct.toFixed(1)}%)`;
                  } else if (completed > 0) {
                    text += ` ${formatBytes(completed)}`;
                  }
                  setRepairProgress(pct, text);
                } else if (data.stage === "creating_model") {
                  setRepairProgress(100, t("repair.creating_model"));
                }
              } else if (currentEvent === "done") {
                finalResult = data;
              } else if (currentEvent === "error") {
                throw new Error(data.error || "Repair failed");
              }
            } catch (err) {
              if (currentEvent === "error" || err.message !== "Unexpected end of JSON input") {
                throw err;
              }
            }
          }
        }
      }

      if (finalResult) {
        toast(t(finalResult.replaced ? "repair.replaced" : "repair.created", { name: finalResult.target_name }), "success");
        await refreshModels();
        closeRepairModal();
        openDetail(finalResult.target_name);
      } else {
        toast(t("repair.created", { name: target }), "success");
        await refreshModels();
        closeRepairModal();
        openDetail(target);
      }
    } catch (e) {
      toast(t("toast.error", { msg: e.message }), "error");
      $("repair-status").textContent = t("state.error_prefix") + e.message;
      updateApply();
    } finally {
      previewBtn.disabled = false;
    }
  });
}

function collectRepairRequest(d, confirmed) {
  const capabilities = Array.from(document.querySelectorAll("input[name='repair-cap']"))
    .filter((el) => el.checked)
    .map((el) => el.value);
  const modelfile = $("repair-preview")?.value || "";
  const stopsMode = $("repair-stops-mode")?.value || "auto";
  const stops = stopsMode === "custom"
    ? ($("repair-stops")?.value || "").split("\n").map((s) => s.replace(/\r$/, "")).filter((s) => s.length > 0)
    : null;
  return {
    model: d.name,
    capabilities,
    template_preset: $("repair-template")?.value || "generic",
    context_preset: $("repair-context")?.value || "keep",
    temperature_preset: $("repair-temperature")?.value || "keep",
    stops,
    projector: $("repair-projector")?.value?.trim() || "",
    fix_load: $("repair-fix-load")?.checked || false,
    modelfile: confirmed ? modelfile : "",
    confirm: !!confirmed,
  };
}

function renderRepairPreview(out) {
  const pre = $("repair-preview");
  pre.hidden = false;
  pre.value = out.modelfile || "";
  const warnings = $("repair-warnings");
  const list = out.warnings || [];
  warnings.hidden = !list.length;
  warnings.innerHTML = list.map((w) => `<div>${escapeHtml(w)}</div>`).join("");
}

$("repair-modal-x")?.addEventListener("click", closeRepairModal);
$("repair-modal-close")?.addEventListener("click", closeRepairModal);

