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
  bindSettingsNavEvents();
  bindDefaultSystemPromptFileEvents();
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
      navItems.forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      const sectionId = btn.dataset.section;
      if (!sectionId) return;
      const targetSec = $(sectionId);
      if (targetSec) {
        if (targetSec.tagName === "DETAILS") {
          targetSec.open = true;
        }
        targetSec.scrollIntoView({ behavior: "smooth", block: "start" });
      }
    });
  });
}

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
          title: t("detail.delete_external_title"),
          text: t("settings.ext_model_remove_confirm", { name }),
          okText: t("action.delete"),
          okClass: "danger",
          mono: name,
        });
        if (conf.ok) {
          try {
            await api("/api/external-models/" + encodeURIComponent(name), { method: "DELETE" });
            toast(t("settings.ext_model_removed", { name }), "success");
            loadExternalModels();
            refreshModels();
          } catch (err) {
            toast(t("toast.error", { msg: err.message }), "error");
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
  const resultEl = $("ext-test-result");
  const testBtn = $("ext-model-test-btn");

  const name = nameInput ? nameInput.value.trim() : "";
  const url = urlInput ? urlInput.value.trim() : "";
  const apiKey = keyInput ? keyInput.value.trim() : "";

  if (!name || !url) {
    toast(t("settings.ext_model_name") + " & " + t("settings.ext_model_url") + " required", "error");
    return;
  }

  if (testBtn) testBtn.disabled = true;
  if (resultEl) {
    resultEl.hidden = false;
    resultEl.className = "ext-test-result";
    resultEl.innerHTML = `<div class="muted">${escapeHtml(t("settings.ext_test_testing"))}</div>`;
  }

  try {
    const res = await api("/api/external-models/test", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, url, api_key: apiKey }),
    });

    if (!res.ok) {
      if (resultEl) {
        resultEl.className = "ext-test-result error";
        resultEl.innerHTML = `<div>${escapeHtml(t("settings.ext_test_failed", { error: res.error || "unknown" }))}</div>`;
      }
      lastTestedExtModel = null;
      lastTestedCapabilities = null;
      return;
    }

    lastTestedExtModel = name;
    lastTestedCapabilities = res.capabilities || ["completion", "tools", "thinking", "vision"];

    const visionPill = `<span class="ext-cap-pill ${res.vision ? 'active' : 'inactive'}" data-cap="vision" title="Click to toggle">👁️ ${escapeHtml(t("settings.ext_cap_vision"))}</span>`;
    const thinkPill = `<span class="ext-cap-pill ${res.thinking ? 'active' : 'inactive'}" data-cap="thinking" title="Click to toggle">🧠 ${escapeHtml(t("settings.ext_cap_thinking"))}</span>`;
    const toolsPill = `<span class="ext-cap-pill ${res.tools ? 'active' : 'inactive'}" data-cap="tools" title="Click to toggle">🛠️ ${escapeHtml(t("settings.ext_cap_tools"))}</span>`;

    if (resultEl) {
      resultEl.className = "ext-test-result success";
      resultEl.innerHTML = `
        <div class="ext-test-header">
          <span>✓ ${escapeHtml(t("settings.ext_test_success", { latency: res.latency_ms || 0 }))}</span>
        </div>
        <div class="ext-caps-pills">
          ${visionPill}
          ${thinkPill}
          ${toolsPill}
        </div>
      `;

      resultEl.querySelectorAll(".ext-cap-pill").forEach((p) => {
        p.addEventListener("click", () => {
          p.classList.toggle("active");
          p.classList.toggle("inactive");
        });
      });
    }
  } catch (e) {
    if (resultEl) {
      resultEl.className = "ext-test-result error";
      resultEl.innerHTML = `<div>${escapeHtml(t("settings.ext_test_failed", { error: e.message }))}</div>`;
    }
    lastTestedExtModel = null;
    lastTestedCapabilities = null;
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

