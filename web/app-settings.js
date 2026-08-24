"use strict";

// ---------- settings ----------
let currentConfig = null;

async function showSettingsView() {
  hideAllMainViews();
  if (typeof stopSpeechPlayback === "function") {
    stopSpeechPlayback();
  }
  currentView = "settings";

  document.querySelectorAll(".topbar-actions button").forEach((b) => b.classList.remove("active"));
  $("settings-btn")?.classList.add("active");

  const view = $("settings-view");
  if (view) view.hidden = false;

  if (!window.location.pathname.startsWith("/settings")) {
    history.pushState(null, "", "/settings");
  }

  try {
    currentConfig = await api("/api/config");
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
    return;
  }
  const effectiveLang = currentConfig.language || (window.I18n ? window.I18n.getLang() : "en");
  $("set-language").value = effectiveLang;
  renderSettingsTranslations(effectiveLang);
  $("set-port").value = currentConfig.port;
  $("set-expose").checked = !!currentConfig.expose_network;
  $("set-password").value = "";

  const globalDefaults = getGlobalChatDefaults();
  if ($("set-default-system")) $("set-default-system").value = globalDefaults.system || "";
  if ($("set-default-temp")) $("set-default-temp").value = String(globalDefaults.temperature != null && globalDefaults.temperature !== "" ? globalDefaults.temperature : "0.7");
  if ($("set-default-top-k")) $("set-default-top-k").value = String(globalDefaults.top_k != null && globalDefaults.top_k !== "" ? globalDefaults.top_k : "40");
  if ($("set-default-top-p")) $("set-default-top-p").value = String(globalDefaults.top_p != null && globalDefaults.top_p !== "" ? globalDefaults.top_p : "0.9");
  if ($("set-default-num-ctx")) $("set-default-num-ctx").value = String(normalizeNumCtxPct(globalDefaults.num_ctx ?? 100));
  if ($("set-default-think-level")) $("set-default-think-level").value = globalDefaults.think_level || "auto";
  if ($("set-default-web-tools")) $("set-default-web-tools").checked = !!globalDefaults.web_tools;
  if ($("set-default-artifacts")) $("set-default-artifacts").checked = !!globalDefaults.artifacts;

  const buildEl = $("settings-build-info");
  if (buildEl) {
    buildEl.textContent = currentConfig.version ? `v${currentConfig.version}` : "";
    buildEl.title = currentConfig.version || "";
  }
  bindExternalModelsEvents();
  bindSystemPromptsEvents();
  bindSettingsNavEvents();
  bindDefaultSystemPromptFileEvents();
  bindSystemPromptsModalEvents();
}

function openSettings() {
  return showSettingsView();
}

function loadFileIntoDefaultSystemPrompt(file) {
  if (!file) return;
  const reader = new FileReader();
  reader.onload = (e) => {
    const text = e.target?.result;
    if (typeof text !== "string") return;
    const sysEl = $("set-default-system");
    if (!sysEl) return;
    sysEl.value = text;
    toast(t("chat.system_file_loaded") || "System prompt loaded from file", "success");
  };
  reader.onerror = () => {
    toast(t("chat.system_file_read_error") || "Could not read file", "error");
  };
  reader.readAsText(file);
}

function bindDefaultSystemPromptFileEvents() {
  const sysFileBtn = $("set-default-system-file-btn");
  const sysFileInput = $("set-default-system-file-input");
  if (sysFileBtn && sysFileInput && !sysFileBtn._bound) {
    sysFileBtn._bound = true;
    sysFileBtn.addEventListener("click", (e) => {
      e.preventDefault();
      sysFileInput.click();
    });
    sysFileInput.addEventListener("change", () => {
      const file = sysFileInput.files?.[0];
      if (file) {
        loadFileIntoDefaultSystemPrompt(file);
      }
      sysFileInput.value = "";
    });
  }

  const sysField = $("set-default-system-field");
  if (sysField && !sysField._boundDnd) {
    sysField._boundDnd = true;
    let sysDndDepth = 0;
    sysField.addEventListener("dragenter", (e) => {
      if (e.dataTransfer?.types?.includes("Files")) {
        e.preventDefault();
        e.stopPropagation();
        sysDndDepth += 1;
        sysField.classList.add("drag-over");
      }
    });
    sysField.addEventListener("dragover", (e) => {
      if (e.dataTransfer?.types?.includes("Files")) {
        e.preventDefault();
        e.stopPropagation();
        e.dataTransfer.dropEffect = "copy";
      }
    });
    sysField.addEventListener("dragleave", (e) => {
      e.preventDefault();
      e.stopPropagation();
      sysDndDepth = Math.max(0, sysDndDepth - 1);
      if (sysDndDepth === 0) {
        sysField.classList.remove("drag-over");
      }
    });
    sysField.addEventListener("drop", (e) => {
      e.preventDefault();
      e.stopPropagation();
      sysDndDepth = 0;
      sysField.classList.remove("drag-over");
      const files = Array.from(e.dataTransfer?.files || []);
      if (files.length > 0) {
        loadFileIntoDefaultSystemPrompt(files[0]);
      }
    });
  }
}

function bindSettingsNavEvents() {
  const navItems = document.querySelectorAll(".settings-nav-item");
  navItems.forEach((btn) => {
    if (btn._bound) return;
    btn._bound = true;
    btn.addEventListener("click", () => {
      const sectionId = btn.dataset.section;
      if (!sectionId) return;
      showSettingsSection(sectionId, true);
    });
  });
}

function showSettingsSection(sectionId, updateUrl = true) {
  const navItems = document.querySelectorAll(".settings-nav-item");
  navItems.forEach((b) => {
    b.classList.toggle("active", b.dataset.section === sectionId);
  });
  const targetSec = $(sectionId);
  if (targetSec) {
    if (targetSec.tagName === "DETAILS") {
      targetSec.open = true;
    }
    targetSec.scrollIntoView({ behavior: "smooth", block: "start" });
  }
  if (updateUrl) {
    let subRoute = "/settings";
    if (sectionId === "sec-general") subRoute = "/settings/general";
    else if (sectionId === "sec-chat-defaults") subRoute = "/settings/chat-defaults";
    else if (sectionId === "sec-prompts") subRoute = "/settings/prompts";
    else if (sectionId === "sec-network") subRoute = "/settings/network";
    else if (sectionId === "sec-ext-models") subRoute = "/settings/external";
    else if (sectionId === "sec-archived") subRoute = "/settings/archived";
    else if (sectionId === "sec-opencode") subRoute = "/settings/opencode";
    if (window.location.pathname !== subRoute) {
      history.pushState(null, "", subRoute);
    }
  }
}

// ---------- System Prompts Library ----------
let systemPromptsList = [];

async function loadSystemPrompts(lang = null) {
  const targetLang = lang || currentConfig?.language || (window.I18n ? window.I18n.getLang() : "en");
  const listEl = $("prompts-list");
  const badge = $("prompts-badge");
  const navBadge = $("prompts-nav-badge");
  if (!listEl) return;
  try {
    const data = await api("/api/system-prompts");
    systemPromptsList = data.prompts || [];
    if (badge) badge.textContent = String(systemPromptsList.length);
    if (navBadge) navBadge.textContent = String(systemPromptsList.length);
    const filterText = ($("prompt-search-input")?.value || "").trim().toLowerCase();
    renderPromptsList(filterText, targetLang);
  } catch (e) {
    if (listEl) listEl.innerHTML = `<div class="muted small">${escapeHtml(e.message)}</div>`;
  }
}

function renderPromptsList(filterText = "", lang = null) {
  const targetLang = lang || currentConfig?.language || (window.I18n ? window.I18n.getLang() : "en");
  const listEl = $("prompts-list");
  if (!listEl) return;
  const filtered = systemPromptsList.filter((p) => {
    if (!filterText) return true;
    return (p.title && p.title.toLowerCase().includes(filterText)) ||
           (p.prompt && p.prompt.toLowerCase().includes(filterText));
  });

  if (!filtered.length) {
    listEl.innerHTML = `<div class="muted small">${escapeHtml(t("settings.prompts_none", null, targetLang))}</div>`;
    return;
  }

  listEl.innerHTML = filtered.map((p) => {
    const dateStr = p.updated_at ? new Date(p.updated_at * 1000).toLocaleDateString() : "";
    return `
      <div class="prompt-card" data-id="${escapeHtml(p.id)}">
        <div class="prompt-card-head">
          <div class="prompt-card-title">${escapeHtml(p.title || "Untitled")}</div>
          <div class="prompt-card-date">${escapeHtml(dateStr)}</div>
        </div>
        <div class="prompt-card-body">${escapeHtml(p.prompt || "")}</div>
        <div class="prompt-card-actions">
          <button type="button" class="ghost prompt-copy-btn" data-id="${escapeHtml(p.id)}" data-i18n="settings.prompt_copy_btn">${escapeHtml(t("settings.prompt_copy_btn", null, targetLang))}</button>
          <button type="button" class="ghost prompt-default-btn" data-id="${escapeHtml(p.id)}" data-i18n="settings.prompt_use_default_btn">${escapeHtml(t("settings.prompt_use_default_btn", null, targetLang))}</button>
          <button type="button" class="ghost prompt-edit-btn" data-id="${escapeHtml(p.id)}" data-i18n="settings.prompt_edit_btn">${escapeHtml(t("settings.prompt_edit_btn", null, targetLang))}</button>
          <button type="button" class="btn-icon danger-text prompt-del-btn" data-id="${escapeHtml(p.id)}" title="${escapeHtml(t("settings.prompt_delete_btn", null, targetLang))}">×</button>
        </div>
      </div>
    `;
  }).join("");

  // Bind actions
  listEl.querySelectorAll(".prompt-copy-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = btn.dataset.id;
      const item = systemPromptsList.find((p) => p.id === id);
      if (item && item.prompt) {
        navigator.clipboard.writeText(item.prompt).then(() => {
          toast(t("chat.copied", null, targetLang) || "Copied to clipboard", "success");
        }).catch(() => {
          toast(t("chat.copy_failed", null, targetLang) || "Could not copy", "error");
        });
      }
    });
  });

  listEl.querySelectorAll(".prompt-default-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = btn.dataset.id;
      const item = systemPromptsList.find((p) => p.id === id);
      if (item && item.prompt) {
        const sysEl = $("set-default-system");
        if (sysEl) {
          sysEl.value = item.prompt;
          toast(t("settings.prompt_use_default_success", null, targetLang), "success");
          showSettingsSection("sec-chat-defaults", false);
        }
      }
    });
  });

  listEl.querySelectorAll(".prompt-edit-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = btn.dataset.id;
      const item = systemPromptsList.find((p) => p.id === id);
      if (item) {
        $("prompt-edit-id").value = item.id;
        $("prompt-edit-title").value = item.title;
        $("prompt-edit-content").value = item.prompt;
        $("prompt-editor-box").hidden = false;
        $("prompt-edit-title").focus();
      }
    });
  });

  listEl.querySelectorAll(".prompt-del-btn").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.id;
      const item = systemPromptsList.find((p) => p.id === id);
      if (!item) return;
      const conf = await askConfirm({
        title: t("settings.prompt_delete_btn", null, targetLang),
        text: t("settings.prompt_delete_confirm", { title: item.title || "Untitled" }, targetLang),
        okText: t("action.delete", null, targetLang),
        okClass: "danger",
        mono: item.title,
      });
      if (conf.ok) {
        try {
          await api("/api/system-prompts/" + encodeURIComponent(id), { method: "DELETE" });
          toast(t("settings.prompt_deleted", null, targetLang), "success");
          loadSystemPrompts(targetLang);
        } catch (err) {
          toast(t("toast.error", { msg: err.message }, targetLang), "error");
        }
      }
    });
  });
}

function loadFileIntoPromptEditor(file) {
  if (!file) return;
  const reader = new FileReader();
  reader.onload = (e) => {
    const text = e.target?.result;
    if (typeof text !== "string") return;
    const contentEl = $("prompt-edit-content");
    if (contentEl) contentEl.value = text;
    const titleEl = $("prompt-edit-title");
    if (titleEl && !titleEl.value.trim()) {
      const fileName = file.name.replace(/\.[^/.]+$/, "");
      titleEl.value = fileName;
    }
    toast(t("chat.system_file_loaded") || "System prompt loaded from file", "success");
  };
  reader.onerror = () => {
    toast(t("chat.system_file_read_error") || "Could not read file", "error");
  };
  reader.readAsText(file);
}

function bindSystemPromptsEvents() {
  const newBtn = $("prompt-new-btn");
  const editorBox = $("prompt-editor-box");
  const cancelBtn = $("prompt-cancel-btn");
  const saveBtn = $("prompt-save-btn");
  const setDefaultBtn = $("prompt-set-default-btn");
  const searchInput = $("prompt-search-input");
  const fileBtn = $("prompt-edit-file-btn");
  const fileInput = $("prompt-edit-file-input");
  const dropZone = $("prompt-edit-dropzone");

  if (newBtn && !newBtn._bound) {
    newBtn._bound = true;
    newBtn.addEventListener("click", () => {
      $("prompt-edit-id").value = "";
      $("prompt-edit-title").value = "";
      $("prompt-edit-content").value = "";
      editorBox.hidden = false;
      $("prompt-edit-title").focus();
    });
  }

  if (cancelBtn && !cancelBtn._bound) {
    cancelBtn._bound = true;
    cancelBtn.addEventListener("click", () => {
      editorBox.hidden = true;
    });
  }

  if (saveBtn && !saveBtn._bound) {
    saveBtn._bound = true;
    saveBtn.addEventListener("click", async () => {
      const id = $("prompt-edit-id").value.trim();
      const title = $("prompt-edit-title").value.trim();
      const prompt = $("prompt-edit-content").value.trim();
      if (!title && !prompt) {
        toast(t("settings.prompt_title_label") + " required", "error");
        return;
      }
      try {
        if (id) {
          await api("/api/system-prompts/" + encodeURIComponent(id), {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ title, prompt }),
          });
        } else {
          await api("/api/system-prompts", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ title, prompt }),
          });
        }
        toast(t("settings.prompt_saved"), "success");
        editorBox.hidden = true;
        loadSystemPrompts();
      } catch (err) {
        toast(t("toast.error", { msg: err.message }), "error");
      }
    });
  }

  if (setDefaultBtn && !setDefaultBtn._bound) {
    setDefaultBtn._bound = true;
    setDefaultBtn.addEventListener("click", () => {
      const prompt = $("prompt-edit-content").value.trim();
      if (prompt) {
        const sysEl = $("set-default-system");
        if (sysEl) {
          sysEl.value = prompt;
          toast(t("settings.prompt_use_default_success"), "success");
          showSettingsSection("sec-chat-defaults", false);
        }
      }
    });
  }

  if (searchInput && !searchInput._bound) {
    searchInput._bound = true;
    searchInput.addEventListener("input", () => {
      renderPromptsList(searchInput.value.trim().toLowerCase());
    });
  }

  if (fileBtn && fileInput && !fileBtn._bound) {
    fileBtn._bound = true;
    fileBtn.addEventListener("click", (e) => {
      e.preventDefault();
      fileInput.click();
    });
    fileInput.addEventListener("change", () => {
      const file = fileInput.files?.[0];
      if (file) {
        loadFileIntoPromptEditor(file);
      }
      fileInput.value = "";
    });
  }

  if (dropZone && !dropZone._boundDnd) {
    dropZone._boundDnd = true;
    let dndDepth = 0;
    dropZone.addEventListener("dragenter", (e) => {
      if (e.dataTransfer?.types?.includes("Files")) {
        e.preventDefault();
        e.stopPropagation();
        dndDepth += 1;
        dropZone.classList.add("drag-over");
      }
    });
    dropZone.addEventListener("dragover", (e) => {
      if (e.dataTransfer?.types?.includes("Files")) {
        e.preventDefault();
        e.stopPropagation();
        e.dataTransfer.dropEffect = "copy";
      }
    });
    dropZone.addEventListener("dragleave", (e) => {
      e.preventDefault();
      e.stopPropagation();
      dndDepth = Math.max(0, dndDepth - 1);
      if (dndDepth === 0) {
        dropZone.classList.remove("drag-over");
      }
    });
    dropZone.addEventListener("drop", (e) => {
      e.preventDefault();
      e.stopPropagation();
      dndDepth = 0;
      dropZone.classList.remove("drag-over");
      const files = Array.from(e.dataTransfer?.files || []);
      if (files.length > 0) {        loadFileIntoPromptEditor(files[0]);
      }
    });
  }
}

// ---------- Archived Models in Settings -----------
async function loadArchivedModelsInSettings(lang = null) {
  const targetLang = lang || currentConfig?.language || (window.I18n ? window.I18n.getLang() : "en");
  const listEl = $("settings-archived-list");
  const badge = $("archived-badge");
  const navBadge = $("archived-nav-badge");
  if (!listEl) return;
  try {
    const data = await api("/api/models");
    const allModels = data.models || [];
    const archived = allModels.filter((m) => !!m.archived);
    if (badge) badge.textContent = String(archived.length);
    if (navBadge) navBadge.textContent = String(archived.length);
    if (!archived.length) {
      listEl.innerHTML = `<div class="muted small">${escapeHtml(t("settings.archived_none", null, targetLang))}</div>`;
      return;
    }

    listEl.innerHTML = archived.map((m) => {
      const sizeStr = typeof formatBytes === "function" ? formatBytes(m.size) : `${(m.size / (1024 * 1024 * 1024)).toFixed(1)} GB`;
      const fam = m.details?.family || "";
      const quant = m.details?.quantization_level || "";
      const meta = [sizeStr, fam, quant].filter(Boolean).join(" · ");
      return `
        <div class="archived-model-card" data-name="${escapeHtml(m.name)}">
          <div class="archived-model-info">
            <div class="archived-model-name">${escapeHtml(m.name)}</div>
            <div class="archived-model-meta">${escapeHtml(meta)}</div>
          </div>
          <div class="archived-model-actions">
            <button type="button" class="ghost settings-unarchive-btn" data-name="${escapeHtml(m.name)}" data-i18n="settings.unarchive_btn">${escapeHtml(t("settings.unarchive_btn", null, targetLang))}</button>
            <button type="button" class="btn-icon danger-text settings-archive-del-btn" data-name="${escapeHtml(m.name)}" title="${escapeHtml(t("detail.delete_title", null, targetLang))}">×</button>
          </div>
        </div>
      `;
    }).join("");

    listEl.querySelectorAll(".settings-unarchive-btn").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const name = btn.dataset.name;
        if (!name) return;
        try {
          await api("/api/models/unarchive", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ name }),
          });
          toast(t("toast.unarchived", { name }, targetLang), "success");
          loadArchivedModelsInSettings(targetLang);
          refreshModels();
        } catch (err) {
          toast(t("toast.error", { msg: err.message }, targetLang), "error");
        }
      });
    });

    listEl.querySelectorAll(".settings-archive-del-btn").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const name = btn.dataset.name;
        if (!name) return;
        if (typeof confirmDelete === "function") {
          confirmDelete(name);
        }
      });
    });
  } catch (e) {
    if (listEl) listEl.innerHTML = `<div class="muted small">${escapeHtml(e.message)}</div>`;
  }
}

// ---------- External Models ----------
let lastTestedExtModel = null;
let lastTestedCapabilities = null;

async function loadExternalModels(lang = null) {
  const targetLang = lang || currentConfig?.language || (window.I18n ? window.I18n.getLang() : "en");
  const listEl = $("ext-models-list");
  const badge = $("ext-models-badge");
  const navBadge = $("ext-models-nav-badge");
  if (!listEl) return;
  try {
    const data = await api("/api/external-models");
    const list = data.models || [];
    if (badge) badge.textContent = String(list.length);
    if (navBadge) navBadge.textContent = String(list.length);
    if (!list.length) {
      listEl.innerHTML = `<div class="muted small">${escapeHtml(t("settings.ext_models_none", null, targetLang))}</div>`;
      return;
    }
    listEl.innerHTML = list.map((m) => {
      const caps = (m.capabilities || ["completion", "tools", "thinking", "vision"])
        .map((c) => `<span class="ext-cap-pill active">${escapeHtml(c)}</span>`)
        .join("");
      return `
        <div class="ext-model-card" data-name="${escapeHtml(m.name)}">
          <div class="ext-model-card-info">
            <div class="ext-model-card-name">
              ${escapeHtml(m.name)}
              <span class="model-external-tag">${escapeHtml(t("models.external_badge", null, targetLang))}</span>
            </div>
            <div class="ext-model-card-url" title="${escapeHtml(m.url)}">${escapeHtml(m.url)}</div>
            <div class="ext-caps-pills" style="margin-top:2px;">${caps}</div>
          </div>
          <button type="button" class="btn-icon danger-text ext-model-del-btn" data-name="${escapeHtml(m.name)}" title="${escapeHtml(t("detail.delete_external_title", null, targetLang))}">×</button>
        </div>
      `;
    }).join("");

    listEl.querySelectorAll(".ext-model-del-btn").forEach((btn) => {
      btn.addEventListener("click", async (e) => {
        e.stopPropagation();
        const name = btn.dataset.name;
        if (!name) return;
        const conf = await askConfirm({
          title: t("detail.delete_external_title", null, targetLang),
          text: t("settings.ext_model_remove_confirm", { name }, targetLang),
          okText: t("action.delete", null, targetLang),
          okClass: "danger",
          mono: name,
        });
        if (conf.ok) {
          try {
            await api("/api/external-models/" + encodeURIComponent(name), { method: "DELETE" });
            toast(t("settings.ext_model_removed", { name }, targetLang), "success");
            loadExternalModels(targetLang);
            refreshModels();
          } catch (err) {
            toast(t("toast.error", { msg: err.message }, targetLang), "error");
          }
        }
      });
    });
  } catch (e) {
    if (listEl) listEl.innerHTML = `<div class="muted small">${escapeHtml(e.message)}</div>`;
  }
}

async function testExternalModel() {
  const nameInput = $("ext-model-name");
  const urlInput = $("ext-model-url");
  const keyInput = $("ext-model-apikey");
  const testBtn = $("ext-model-test-btn");
  const resultEl = $("ext-test-result");

  const name = nameInput ? nameInput.value.trim() : "";
  const url = urlInput ? urlInput.value.trim() : "";
  const apiKey = keyInput ? keyInput.value.trim() : "";

  if (!url) {
    toast(t("settings.ext_model_url") + " required", "error");
    return;
  }

  if (testBtn) testBtn.disabled = true;
  if (resultEl) {
    resultEl.hidden = false;
    resultEl.className = "ext-test-result ext-test-running";
    resultEl.textContent = t("settings.ext_test_testing");
  }

  try {
    const res = await api("/api/external-models/test", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, url, api_key: apiKey }),
    });

    lastTestedExtModel = name;
    lastTestedCapabilities = res.capabilities || [];

    if (resultEl) {
      resultEl.className = "ext-test-result ext-test-success";
      const capsHtml = (res.capabilities || [])
        .map((c) => `<span class="ext-cap-pill active" data-cap="${escapeHtml(c)}">${escapeHtml(c)}</span>`)
        .join("");
      resultEl.innerHTML = `
        <div class="ext-test-head">
          <span class="ext-test-badge good">✓</span>
          <span>${escapeHtml(t("settings.ext_test_success", { latency: res.latency_ms || 0 }))}</span>
        </div>
        ${capsHtml ? `<div class="ext-test-caps"><span class="muted small">${escapeHtml(t("detail.capabilities"))}:</span> ${capsHtml}</div>` : ""}
      `;
    }
  } catch (e) {
    lastTestedExtModel = null;
    lastTestedCapabilities = null;
    if (resultEl) {
      resultEl.className = "ext-test-result ext-test-error";
      resultEl.textContent = t("settings.ext_test_failed", { error: e.message });
    }
  } finally {
    if (testBtn) testBtn.disabled = false;
  }
}

async function addExternalModel() {
  const nameInput = $("ext-model-name");
  const urlInput = $("ext-model-url");
  const keyInput = $("ext-model-apikey");
  const resultEl = $("ext-test-result");

  const name = nameInput ? nameInput.value.trim() : "";
  const url = urlInput ? urlInput.value.trim() : "";
  const apiKey = keyInput ? keyInput.value.trim() : "";

  if (!name || !url) {
    toast(t("settings.ext_model_name") + " & " + t("settings.ext_model_url") + " required", "error");
    return;
  }

  let caps = ["completion"];
  if (resultEl && !resultEl.hidden) {
    resultEl.querySelectorAll(".ext-cap-pill.active").forEach((p) => {
      if (p.dataset.cap && !caps.includes(p.dataset.cap)) {
        caps.push(p.dataset.cap);
      }
    });
  } else if (lastTestedExtModel === name && Array.isArray(lastTestedCapabilities) && lastTestedCapabilities.length > 0) {
    caps = lastTestedCapabilities;
  } else {
    caps = ["completion", "tools", "thinking", "vision"];
  }

  try {
    await api("/api/external-models", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, url, api_key: apiKey, capabilities: caps }),
    });

    toast(t("settings.ext_model_added", { name }), "success");
    if (nameInput) nameInput.value = "";
    if (urlInput) urlInput.value = "";
    if (keyInput) keyInput.value = "";
    if (resultEl) resultEl.hidden = true;
    lastTestedExtModel = null;
    lastTestedCapabilities = null;

    loadExternalModels();
    refreshModels();
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
}

function bindExternalModelsEvents() {
  const testBtn = $("ext-model-test-btn");
  if (testBtn && !testBtn._bound) {
    testBtn._bound = true;
    testBtn.addEventListener("click", testExternalModel);
  }
  const addBtn = $("ext-model-add-btn");
  if (addBtn && !addBtn._bound) {
    addBtn._bound = true;
    addBtn.addEventListener("click", addExternalModel);
  }
}

function renderSettingsTranslations(lang = null) {
  const targetLang = lang || currentConfig?.language || (window.I18n ? window.I18n.getLang() : "en");
  const settingsView = $("settings-view");
  if (settingsView && typeof window.I18n?.applyTranslations === "function") {
    window.I18n.applyTranslations(settingsView, targetLang);
  }
  updatePasswordSection(targetLang);
  updateExposeWarning(targetLang);
  loadExternalModels(targetLang);
  loadSystemPrompts(targetLang);
  loadArchivedModelsInSettings(targetLang);
  if (typeof refreshOpenCodeUI === "function") {
    refreshOpenCodeUI();
  }
}

function updatePasswordSection(lang = null) {
  if (!currentConfig) return;
  const targetLang = lang || currentConfig.language || (window.I18n ? window.I18n.getLang() : "en");
  const badge = $("pwd-badge");
  if (currentConfig.has_password) {
    badge.textContent = t("settings.pwd_set", null, targetLang);
    badge.className = "badge badge-good";
  } else {
    badge.textContent = t("settings.pwd_unset", null, targetLang);
    badge.className = "badge badge-muted";
  }
  $("pwd-clear-btn").hidden = !currentConfig.has_password;
  $("settings-logout-btn").hidden = !currentConfig.has_password;
}

function updateBindPreview(lang = null) {
  if (!currentConfig) return;
  const targetLang = lang || currentConfig.language || (window.I18n ? window.I18n.getLang() : "en");
  const badge = $("bind-preview");
  const expose = $("set-expose").checked;
  if (expose) {
    badge.textContent = t("settings.bind_lan", null, targetLang);
    badge.className = "badge badge-warn";
  } else {
    badge.textContent = t("settings.bind_local", null, targetLang);
    badge.className = "badge badge-muted";
  }
}

function updateExposeWarning(lang = null) {
  if (!currentConfig) return;
  const wantExpose = $("set-expose").checked;
  $("expose-warning").hidden = !(wantExpose && !currentConfig.has_password);
  updateBindPreview(lang);
}

// ---------- System Prompts Library Modal ----------
let promptsModalTargetTextarea = null;
let promptsModalCallback = null;

async function openSystemPromptsPickerModal(targetTextarea = null, onSelect = null) {
  promptsModalTargetTextarea = targetTextarea;
  promptsModalCallback = onSelect;
  const modal = $("prompts-modal");
  if (!modal) return;
  const searchInput = $("prompts-modal-search-input");
  if (searchInput) searchInput.value = "";
  modal.hidden = false;
  await loadAndRenderPromptsModal();
  if (searchInput) searchInput.focus();
}
window.openSystemPromptsPickerModal = openSystemPromptsPickerModal;

function closeSystemPromptsPickerModal() {
  const modal = $("prompts-modal");
  if (modal) modal.hidden = true;
  promptsModalTargetTextarea = null;
  promptsModalCallback = null;
}
window.closeSystemPromptsPickerModal = closeSystemPromptsPickerModal;

async function loadAndRenderPromptsModal(filterQuery = "") {
  const listEl = $("prompts-modal-list");
  if (!listEl) return;
  const targetLang = currentConfig?.language || (window.I18n ? window.I18n.getLang() : "en");
  
  if (!systemPromptsList || systemPromptsList.length === 0) {
    try {
      const data = await api("/api/system-prompts");
      systemPromptsList = data.prompts || [];
    } catch {
      systemPromptsList = [];
    }
  }
  
  const q = (filterQuery || "").toLowerCase();
  const filtered = (systemPromptsList || []).filter(
    (p) => (p.title || "").toLowerCase().includes(q) || (p.prompt || "").toLowerCase().includes(q)
  );

  if (filtered.length === 0) {
    listEl.innerHTML = `<div class="muted small">${escapeHtml(t("prompts.modal_empty", null, targetLang))}</div>`;
    return;
  }

  listEl.innerHTML = filtered.map((p) => {
    return `
      <div class="prompts-modal-item" data-id="${escapeHtml(p.id)}">
        <div class="prompts-modal-item-head">
          <span class="prompts-modal-item-title">${escapeHtml(p.title || "Untitled Prompt")}</span>
          <button type="button" class="primary prompts-modal-item-btn" data-id="${escapeHtml(p.id)}">${escapeHtml(t("prompts.modal_use", null, targetLang))}</button>
        </div>
        <div class="prompts-modal-item-preview">${escapeHtml(p.prompt || "")}</div>
      </div>
    `;
  }).join("");

  listEl.querySelectorAll(".prompts-modal-item").forEach((itemEl) => {
    itemEl.addEventListener("click", (e) => {
      const id = itemEl.dataset.id;
      const promptObj = (systemPromptsList || []).find((p) => p.id === id);
      if (!promptObj) return;
      if (promptsModalTargetTextarea) {
        promptsModalTargetTextarea.value = promptObj.prompt || "";
        promptsModalTargetTextarea.dispatchEvent(new Event("input", { bubbles: true }));
        promptsModalTargetTextarea.dispatchEvent(new Event("change", { bubbles: true }));
      }
      if (typeof promptsModalCallback === "function") {
        promptsModalCallback(promptObj);
      }
      toast(t("prompts.modal_applied", null, targetLang), "success");
      closeSystemPromptsPickerModal();
    });
  });
}

function bindSystemPromptsModalEvents() {
  const closeBtn = $("prompts-modal-close");
  const modal = $("prompts-modal");
  const searchInput = $("prompts-modal-search-input");
  
  if (closeBtn && !closeBtn._bound) {
    closeBtn._bound = true;
    closeBtn.addEventListener("click", closeSystemPromptsPickerModal);
  }
  
  if (modal && !modal._boundBackdrop) {
    modal._boundBackdrop = true;
    modal.addEventListener("click", (e) => {
      if (e.target === modal) {
        closeSystemPromptsPickerModal();
      }
    });
  }
  
  if (searchInput && !searchInput._bound) {
    searchInput._bound = true;
    searchInput.addEventListener("input", () => {
      loadAndRenderPromptsModal(searchInput.value.trim());
    });
  }

  // Connect Settings default prompt library button:
  const settingsLibBtn = $("set-default-system-prompt-lib-btn");
  if (settingsLibBtn && !settingsLibBtn._bound) {
    settingsLibBtn._bound = true;
    settingsLibBtn.addEventListener("click", (e) => {
      e.preventDefault();
      openSystemPromptsPickerModal($("set-default-system"));
    });
  }

  // Connect Chat system prompt library button:
  const chatLibBtn = $("chat-system-prompt-lib-btn");
  if (chatLibBtn && !chatLibBtn._bound) {
    chatLibBtn._bound = true;
    chatLibBtn.addEventListener("click", (e) => {
      e.preventDefault();
      openSystemPromptsPickerModal($("chat-system"), () => {
        if (typeof adjustChatSystemPromptHeight === "function") {
          adjustChatSystemPromptHeight();
        }
        if (typeof saveChatOptionsForCurrentModel === "function") {
          saveChatOptionsForCurrentModel();
        }
      });
    });
  }
}

// Bind immediately on boot if elements exist
if (typeof document !== "undefined") {
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", bindSystemPromptsModalEvents);
  } else {
    bindSystemPromptsModalEvents();
  }
}

