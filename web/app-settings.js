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
  if ($("set-default-system")) {
    $("set-default-system").value = globalDefaults.system || "";
    updateSettingsDefaultSystemTokens();
  }
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

  const mobileBackBtn = $("settings-mobile-back-btn");
  if (mobileBackBtn && !mobileBackBtn._bound) {
    mobileBackBtn._bound = true;
    mobileBackBtn.addEventListener("click", showSettingsMobileMenu);
  }

  const path = window.location.pathname;
  let targetSecId = "sec-general";
  if (path === "/settings/chat-defaults") targetSecId = "sec-chat-defaults";
  else if (path === "/settings/prompts") targetSecId = "sec-prompts";
  else if (path === "/settings/network") targetSecId = "sec-network";
  else if (path === "/settings/external") targetSecId = "sec-ext-models";
  else if (path === "/settings/archived" || path === "/archived") targetSecId = "sec-archived";
  else if (path === "/settings/opencode" || path === "/opencode") targetSecId = "sec-opencode";
  else if (path === "/settings/general") targetSecId = "sec-general";
  else if (path === "/settings" || path === "/settings/") {
    if (window.innerWidth <= 900) {
      showSettingsMobileMenu();
      targetSecId = null;
    }
  }

  if (targetSecId) {
    showSettingsSection(targetSecId, false);
  }
}

function openSettings() {
  return showSettingsView();
}

function updateSettingsDefaultSystemTokens() {
  const sys = $("set-default-system");
  const badge = $("set-default-system-tokens");
  if (!badge) return;
  const val = (sys?.value || "").trim();
  const count = typeof estimateTokens === "function" ? estimateTokens(val) : 0;
  if (count > 0) {
    badge.textContent = `~${count.toLocaleString()} tok`;
    badge.classList.add("has-tokens");
    badge.title = `${count.toLocaleString()} approximate tokens`;
  } else {
    badge.textContent = "0 tok";
    badge.classList.remove("has-tokens");
    badge.title = "Approximate tokens";
  }
}
window.updateSettingsDefaultSystemTokens = updateSettingsDefaultSystemTokens;

function updatePromptEditorTokens() {
  const sys = $("prompt-edit-content");
  const badge = $("prompt-edit-tokens");
  if (!badge) return;
  const val = (sys?.value || "").trim();
  const count = typeof estimateTokens === "function" ? estimateTokens(val) : 0;
  if (count > 0) {
    badge.textContent = `~${count.toLocaleString()} tok`;
    badge.classList.add("has-tokens");
    badge.title = `${count.toLocaleString()} approximate tokens`;
  } else {
    badge.textContent = "0 tok";
    badge.classList.remove("has-tokens");
    badge.title = "Approximate tokens";
  }
}
window.updatePromptEditorTokens = updatePromptEditorTokens;

function loadFileIntoDefaultSystemPrompt(file) {
  if (!file) return;
  const reader = new FileReader();
  reader.onload = (e) => {
    const text = e.target?.result;
    if (typeof text !== "string") return;
    const sysEl = $("set-default-system");
    if (!sysEl) return;
    sysEl.value = text;
    updateSettingsDefaultSystemTokens();
    toast(t("chat.system_file_loaded") || "System prompt loaded from file", "success");
  };
  reader.onerror = () => {
    toast(t("chat.system_file_read_error") || "Could not read file", "error");
  };
  reader.readAsText(file);
}

function bindDefaultSystemPromptFileEvents() {
  const sysEl = $("set-default-system");
  if (sysEl && !sysEl._boundTokens) {
    sysEl._boundTokens = true;
    sysEl.addEventListener("input", updateSettingsDefaultSystemTokens);
    sysEl.addEventListener("change", updateSettingsDefaultSystemTokens);
  }

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

  const sysClearBtn = $("set-default-system-clear-btn");
  if (sysClearBtn && !sysClearBtn._bound) {
    sysClearBtn._bound = true;
    sysClearBtn.addEventListener("click", (e) => {
      e.preventDefault();
      const sys = $("set-default-system");
      if (sys) {
        sys.value = "";
        updateSettingsDefaultSystemTokens();
        sys.dispatchEvent(new Event("input", { bubbles: true }));
        sys.dispatchEvent(new Event("change", { bubbles: true }));
        sys.focus();
      }
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
  const settingsView = $("settings-view");
  if (settingsView) {
    settingsView.classList.add("settings-mobile-section-open");
  }

  const navItems = document.querySelectorAll(".settings-nav-item");
  navItems.forEach((b) => {
    b.classList.toggle("active", b.dataset.section === sectionId);
  });

  const allSections = document.querySelectorAll(".settings-section-card");
  allSections.forEach((sec) => {
    sec.hidden = true;
  });

  const targetSec = $(sectionId);
  if (targetSec) {
    targetSec.hidden = false;
    const mainEl = document.querySelector(".settings-main");
    if (mainEl) mainEl.scrollTop = 0;
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

function showSettingsMobileMenu() {
  const settingsView = $("settings-view");
  if (settingsView) {
    settingsView.classList.remove("settings-mobile-section-open");
  }
  if (window.location.pathname !== "/settings") {
    history.pushState(null, "", "/settings");
  }
}
window.showSettingsMobileMenu = showSettingsMobileMenu;

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
    const promptText = p.prompt || "";
    const tokenCount = typeof estimateTokens === "function" ? estimateTokens(promptText) : 0;
    const tokenStr = typeof fmtTokens === "function" ? fmtTokens(tokenCount) : `~${tokenCount} tok`;
    return `
      <div class="prompt-card" data-id="${escapeHtml(p.id)}">
        <!-- View Mode -->
        <div class="prompt-card-view">
          <div class="prompt-card-head">
            <div class="prompt-card-title-wrap">
              <div class="prompt-card-title">${escapeHtml(p.title || "Untitled")}</div>
            </div>
            <div class="prompt-card-meta">
              <span class="prompt-token-pill mono" title="Approximate tokens">${escapeHtml(tokenStr)}</span>
              <div class="prompt-card-date">${escapeHtml(dateStr)}</div>
            </div>
          </div>
          <div class="prompt-card-body">${escapeHtml(promptText)}</div>
          <div class="prompt-card-actions">
            <button type="button" class="ghost prompt-copy-btn" data-id="${escapeHtml(p.id)}" data-i18n="settings.prompt_copy_btn">${escapeHtml(t("settings.prompt_copy_btn", null, targetLang))}</button>
            <button type="button" class="ghost prompt-export-btn" data-id="${escapeHtml(p.id)}" data-i18n="settings.prompt_export_btn">${escapeHtml(t("settings.prompt_export_btn", null, targetLang))}</button>
            <button type="button" class="ghost prompt-edit-btn" data-id="${escapeHtml(p.id)}" data-i18n="settings.prompt_edit_btn">${escapeHtml(t("settings.prompt_edit_btn", null, targetLang))}</button>
            <button type="button" class="ghost danger-text prompt-del-btn" data-id="${escapeHtml(p.id)}" data-i18n="settings.prompt_delete_btn">${escapeHtml(t("settings.prompt_delete_btn", null, targetLang))}</button>
          </div>
        </div>

        <!-- Inline Edit Mode -->
        <div class="prompt-card-edit" hidden>
          <div class="field">
            <label class="small muted" data-i18n="settings.prompt_title_label">${escapeHtml(t("settings.prompt_title_label", null, targetLang))}</label>
            <input type="text" class="prompt-inline-title" value="${escapeHtml(p.title || "")}" autocomplete="off">
          </div>
          <div class="field field-vertical">
            <div class="chat-system-head">
              <div class="chat-system-label-wrap">
                <label class="small muted" data-i18n="settings.prompt_content_label">${escapeHtml(t("settings.prompt_content_label", null, targetLang))}</label>
                <span class="prompt-inline-tokens system-token-badge mono${tokenCount > 0 ? " has-tokens" : ""}" title="Approximate tokens">${escapeHtml(tokenStr)}</span>
              </div>
              <div class="chat-system-actions">
                <button type="button" class="ghost chat-system-file-btn prompt-inline-file-btn" title="${escapeHtml(t("settings.prompt_load_file", null, targetLang))}">
                  <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
                    <path d="M14 2v6h6"/>
                    <path d="M12 18v-6"/>
                    <path d="M9 15l3-3 3 3"/>
                  </svg>
                </button>
                <button type="button" class="ghost chat-system-file-btn prompt-inline-clear-btn" title="${escapeHtml(t("chat.clear_system_prompt", null, targetLang))}">
                  <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M3 6h18"/>
                    <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                  </svg>
                </button>
                <input type="file" class="prompt-inline-file-input" hidden accept=".txt,.md,.markdown,.json,.yaml,.yml,.prompt,.py,.js,.ts,.html,.css,*/*">
              </div>
            </div>
            <textarea class="prompt-inline-content mono" rows="6" autocomplete="off">${escapeHtml(p.prompt || "")}</textarea>
          </div>
          <div class="prompt-inline-actions">
            <button type="button" class="primary prompt-inline-save-btn" data-id="${escapeHtml(p.id)}" data-i18n="settings.prompt_save_btn">${escapeHtml(t("settings.prompt_save_btn", null, targetLang))}</button>
            <button type="button" class="ghost prompt-inline-cancel-btn" data-id="${escapeHtml(p.id)}" data-i18n="settings.prompt_cancel_btn">${escapeHtml(t("settings.prompt_cancel_btn", null, targetLang))}</button>
          </div>
        </div>
      </div>
    `;
  }).join("");

  // Bind copy
  listEl.querySelectorAll(".prompt-copy-btn").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.id;
      const item = systemPromptsList.find((p) => p.id === id);
      if (item && item.prompt) {
        try {
          if (navigator.clipboard && navigator.clipboard.writeText) {
            await navigator.clipboard.writeText(item.prompt);
          } else {
            const ta = document.createElement("textarea");
            ta.value = item.prompt;
            ta.style.position = "fixed";
            ta.style.opacity = "0";
            document.body.appendChild(ta);
            ta.select();
            document.execCommand("copy");
            document.body.removeChild(ta);
          }
          toast(t("chat.copied", null, targetLang) || "Copied to clipboard", "success");
        } catch {
          toast(t("chat.copy_failed", null, targetLang) || "Could not copy", "error");
        }
      }
    });
  });

  // Bind export
  listEl.querySelectorAll(".prompt-export-btn").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.id;
      const item = systemPromptsList.find((p) => p.id === id);
      if (!item) return;

      let defaultFn = item.filename || (item.title ? `${item.title}.md` : "prompt.md");
      if (!defaultFn.includes(".")) defaultFn += ".md";

      if (window.showSaveFilePicker) {
        try {
          const handle = await window.showSaveFilePicker({
            suggestedName: defaultFn,
            types: [
              {
                description: "Markdown Document (*.md)",
                accept: { "text/markdown": [".md", ".markdown"] }
              },
              {
                description: "Text Document (*.txt)",
                accept: { "text/plain": [".txt", ".prompt"] }
              },
              {
                description: "All Files (*.*)",
                accept: { "*/*": [] }
              }
            ]
          });
          const writable = await handle.createWritable();
          await writable.write(item.prompt || "");
          await writable.close();
          toast(t("settings.prompt_exported", null, targetLang) || "Prompt exported", "success");
          return;
        } catch (err) {
          if (err && err.name === "AbortError") {
            return; // User cancelled
          }
        }
      }

      // Fallback
      const askMsg = t("settings.prompt_export_ask_name", null, targetLang) || "Export file name:";
      const chosenName = window.prompt(askMsg, defaultFn);
      if (!chosenName) return;

      try {
        const blob = new Blob([item.prompt || ""], { type: "text/markdown;charset=utf-8" });
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = chosenName.trim();
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
        toast(t("settings.prompt_exported", null, targetLang) || "Prompt exported", "success");
      } catch (e) {
        toast(e.message, "error");
      }
    });
  });

  // Bind inline edit expand
  listEl.querySelectorAll(".prompt-edit-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const card = btn.closest(".prompt-card");
      if (!card) return;
      const viewEl = card.querySelector(".prompt-card-view");
      const editEl = card.querySelector(".prompt-card-edit");
      if (viewEl && editEl) {
        viewEl.hidden = true;
        editEl.hidden = false;
        const titleInput = editEl.querySelector(".prompt-inline-title");
        if (titleInput) titleInput.focus();
      }
    });
  });

  // Bind inline edit cancel
  listEl.querySelectorAll(".prompt-inline-cancel-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const card = btn.closest(".prompt-card");
      if (!card) return;
      const viewEl = card.querySelector(".prompt-card-view");
      const editEl = card.querySelector(".prompt-card-edit");
      if (viewEl && editEl) {
        editEl.hidden = true;
        viewEl.hidden = false;
      }
    });
  });

  // Bind inline edit save
  listEl.querySelectorAll(".prompt-inline-save-btn").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.id;
      const card = btn.closest(".prompt-card");
      if (!card) return;
      const titleInput = card.querySelector(".prompt-inline-title");
      const contentInput = card.querySelector(".prompt-inline-content");
      const title = (titleInput?.value || "").trim();
      const prompt = (contentInput?.value || "").trim();
      if (!title && !prompt) {
        toast(t("settings.prompt_title_label", null, targetLang) + " required", "error");
        return;
      }
      try {
        await api("/api/system-prompts/" + encodeURIComponent(id), {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ title, prompt }),
        });
        toast(t("settings.prompt_saved", null, targetLang), "success");
        loadSystemPrompts(targetLang);
      } catch (err) {
        toast(t("toast.error", { msg: err.message }, targetLang), "error");
      }
    });
  });

  // Bind inline content token updates
  listEl.querySelectorAll(".prompt-inline-content").forEach((ta) => {
    const parent = ta.closest(".prompt-card-edit");
    const badge = parent?.querySelector(".prompt-inline-tokens");
    const update = () => {
      if (!badge) return;
      const count = typeof estimateTokens === "function" ? estimateTokens(ta.value) : 0;
      if (count > 0) {
        badge.textContent = `~${count.toLocaleString()} tok`;
        badge.classList.add("has-tokens");
      } else {
        badge.textContent = "0 tok";
        badge.classList.remove("has-tokens");
      }
    };
    ta.addEventListener("input", update);
    ta.addEventListener("change", update);
  });

  // Bind inline file import
  listEl.querySelectorAll(".prompt-inline-file-btn").forEach((btn) => {
    const parent = btn.closest(".prompt-card-edit");
    const fileInput = parent?.querySelector(".prompt-inline-file-input");
    if (fileInput) {
      btn.addEventListener("click", () => fileInput.click());
      fileInput.addEventListener("change", () => {
        const file = fileInput.files?.[0];
        if (file) {
          const reader = new FileReader();
          reader.onload = (e) => {
            const text = e.target?.result;
            if (typeof text === "string") {
              const ta = parent.querySelector(".prompt-inline-content");
              if (ta) {
                ta.value = text;
                ta.dispatchEvent(new Event("input", { bubbles: true }));
              }
              const titleInp = parent.querySelector(".prompt-inline-title");
              if (titleInp && !titleInp.value.trim()) {
                titleInp.value = file.name.replace(/\.[^/.]+$/, "");
              }
              toast(t("chat.system_file_loaded", null, targetLang) || "System prompt loaded from file", "success");
            }
          };
          reader.onerror = () => {
            toast(t("chat.system_file_read_error", null, targetLang) || "Could not read file", "error");
          };
          reader.readAsText(file);
        }
        fileInput.value = "";
      });
    }
  });

  // Bind inline clear
  listEl.querySelectorAll(".prompt-inline-clear-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const parent = btn.closest(".prompt-card-edit");
      const ta = parent?.querySelector(".prompt-inline-content");
      if (ta) {
        ta.value = "";
        ta.dispatchEvent(new Event("input", { bubbles: true }));
        ta.focus();
      }
    });
  });

  // Bind delete with confirm modal
  listEl.querySelectorAll(".prompt-del-btn").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.id;
      const item = systemPromptsList.find((p) => p.id === id);
      if (!item) return;
      const conf = await askConfirm({
        title: t("settings.prompt_delete_btn", null, targetLang) || "Delete",
        text: t("settings.prompt_delete_confirm", { title: item.title || "Untitled" }, targetLang),
        okText: t("settings.prompt_delete_btn", null, targetLang) || "Delete",
        okClass: "danger",
        mono: item.title,
      });
      if (conf?.ok) {
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
    if (contentEl) {
      contentEl.value = text;
      updatePromptEditorTokens();
    }
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
  const fileBtn = $("prompt-edit-file-btn");
  const fileInput = $("prompt-edit-file-input");
  const dropZone = $("prompt-edit-dropzone");

  const editContentEl = $("prompt-edit-content");
  if (editContentEl && !editContentEl._boundTokens) {
    editContentEl._boundTokens = true;
    editContentEl.addEventListener("input", updatePromptEditorTokens);
    editContentEl.addEventListener("change", updatePromptEditorTokens);
  }

  if (newBtn && !newBtn._bound) {
    newBtn._bound = true;
    newBtn.addEventListener("click", () => {
      $("prompt-edit-id").value = "";
      $("prompt-edit-title").value = "";
      $("prompt-edit-content").value = "";
      updatePromptEditorTokens();
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

  const searchInput = $("prompt-search-input");
  const searchClear = $("prompt-search-clear");
  if (searchInput && !searchInput._bound) {
    searchInput._bound = true;
    const updateClear = () => {
      if (searchClear) searchClear.hidden = !searchInput.value;
    };
    searchInput.addEventListener("input", () => {
      updateClear();
      renderPromptsList(searchInput.value.trim().toLowerCase());
    });
    if (searchClear) {
      searchClear.addEventListener("click", () => {
        searchInput.value = "";
        updateClear();
        renderPromptsList("");
        searchInput.focus();
      });
    }
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

  const promptEditClearBtn = $("prompt-edit-clear-btn");
  if (promptEditClearBtn && !promptEditClearBtn._bound) {
    promptEditClearBtn._bound = true;
    promptEditClearBtn.addEventListener("click", (e) => {
      e.preventDefault();
      const el = $("prompt-edit-content");
      if (el) {
        el.value = "";
        updatePromptEditorTokens();
        el.dispatchEvent(new Event("input", { bubbles: true }));
        el.dispatchEvent(new Event("change", { bubbles: true }));
        el.focus();
      }
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
      if (files.length > 0) {
        loadFileIntoPromptEditor(files[0]);
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
  await loadAndRenderPromptsModal("", true);
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

async function loadAndRenderPromptsModal(filterQuery = "", forceFetch = false) {
  const listEl = $("prompts-modal-list");
  if (!listEl) return;
  const targetLang = currentConfig?.language || (window.I18n ? window.I18n.getLang() : "en");
  
  if (forceFetch || !systemPromptsList || systemPromptsList.length === 0) {
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
    const promptText = p.prompt || "";
    const tokenCount = typeof estimateTokens === "function" ? estimateTokens(promptText) : 0;
    const tokenStr = typeof fmtTokens === "function" ? fmtTokens(tokenCount) : `~${tokenCount} tok`;
    return `
      <div class="prompts-modal-item" data-id="${escapeHtml(p.id)}">
        <div class="prompts-modal-item-head">
          <div class="prompts-modal-item-title-wrap">
            <span class="prompts-modal-item-title">${escapeHtml(p.title || "Untitled Prompt")}</span>
            <span class="prompt-token-pill mono" title="Approximate tokens">${escapeHtml(tokenStr)}</span>
          </div>
          <button type="button" class="primary prompts-modal-item-btn" data-id="${escapeHtml(p.id)}">${escapeHtml(t("prompts.modal_use", null, targetLang))}</button>
        </div>
        <div class="prompts-modal-item-preview">${escapeHtml(promptText)}</div>
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
  
  const searchInput = $("prompts-modal-search-input");
  const searchClear = $("prompts-modal-search-clear");
  if (searchInput && !searchInput._bound) {
    searchInput._bound = true;
    const updateClear = () => {
      if (searchClear) searchClear.hidden = !searchInput.value;
    };
    searchInput.addEventListener("input", () => {
      updateClear();
      loadAndRenderPromptsModal(searchInput.value.trim());
    });
    if (searchClear) {
      searchClear.addEventListener("click", () => {
        searchInput.value = "";
        updateClear();
        loadAndRenderPromptsModal("");
        searchInput.focus();
      });
    }
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
