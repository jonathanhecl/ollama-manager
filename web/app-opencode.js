"use strict";

// ---------- OpenCode integration ----------
let openCodeState = null;

// Auto-name style for models not indexed (custom-named) in OpenCode:
// "plain" → "name model" · "tps" → "name model (45tok/s)".
let openCodeNameStylePref = localStorage.getItem("ollama_opencode_name_style") === "tps" ? "tps" : "plain";

async function refreshOpenCodeUI() {
  try {
    openCodeState = await api("/api/opencode");
  } catch (e) {
    openCodeState = null;
    renderOpenCodeError(e.message);
    return;
  }
  renderOpenCodeView();
}

function renderOpenCodeError(msg) {
  const badge = $("opencode-badge");
  const status = $("opencode-status");
  if (badge) {
    badge.textContent = t("settings.opencode_not_configured");
    badge.className = "badge badge-bad";
  }
  if (status) status.textContent = msg;
  const noProvider = $("opencode-noprovider");
  const createBtn = $("opencode-create-btn");
  const saveBtn = $("opencode-save-btn");
  if (noProvider) noProvider.hidden = true;
  if (createBtn) createBtn.hidden = true;
  if (saveBtn) saveBtn.disabled = true;
  const box = $("opencode-models");
  if (box) box.innerHTML = `<div class="muted">${escapeHtml(msg)}</div>`;
}

function renderOpenCodeView() {
  const st = openCodeState;
  if (!st) return;
  const badge = $("opencode-badge");
  const status = $("opencode-status");
  const noProvider = $("opencode-noprovider");
  const createBtn = $("opencode-create-btn");
  const saveBtn = $("opencode-save-btn");
  if (st.provider) {
    if (badge) {
      badge.textContent = t("settings.opencode_configured");
      badge.className = "badge badge-good";
    }
    if (status) status.textContent = `${st.config_path} · ${st.provider.key} → ${st.provider.base_url}`;
  } else {
    if (badge) {
      badge.textContent = t("settings.opencode_not_configured");
      badge.className = "badge badge-warn";
    }
    if (status) {
      status.textContent = st.exists
        ? t("settings.opencode_no_provider_status")
        : t("settings.opencode_no_file_status");
    }
  }
  if (noProvider) noProvider.hidden = !!st.provider;
  if (createBtn) createBtn.hidden = !!st.provider;
  if (saveBtn) saveBtn.disabled = !st.provider;
  const remoteWarn = $("opencode-remote-warn");
  if (remoteWarn) remoteWarn.hidden = !st.remote;
  syncOpenCodeStyleUI();
  renderOpenCodeModels(st);
  renderOpenCodePreview();
}

function showOpenCodeView() {
  if (typeof showSettingsView === "function") {
    showSettingsView().then(() => {
      if (typeof showSettingsSection === "function") {
        showSettingsSection("sec-opencode", true);
      }
    });
  }
}

function formatCtxTokens(tokens) {
  if (!tokens || tokens <= 0) return "";
  if (tokens >= 1048576) return `${(tokens / 1048576).toFixed(1)}M`;
  if (tokens >= 1024) return `${Math.round(tokens / 1024)}k`;
  return `${tokens}`;
}

function openCodeDefaultOutputLimit(ctx) {
  if (!ctx || ctx <= 0) return 4096;
  if (ctx >= 65536) return 16384;
  if (ctx >= 32768) return 8192;
  if (ctx >= 16384) return 4096;
  return Math.max(1024, Math.floor(ctx / 2));
}

function syncOpenCodeStyleUI() {
  const container = $("opencode-style-segmented");
  if (container) {
    container.querySelectorAll(".opencode-style-btn").forEach((btn) => {
      btn.classList.toggle("active", btn.dataset.style === openCodeNameStylePref);
    });
  }
  const sel = $("opencode-name-style");
  if (sel) sel.value = openCodeNameStylePref;
}

function renderOpenCodeModels(st) {
  const box = $("opencode-models");
  if (!st.models || st.models.length === 0) {
    box.innerHTML = `<div class="muted">${escapeHtml(t("settings.opencode_empty"))}</div>`;
    return;
  }
  box.innerHTML = "";
  const list = document.createElement("div");
  list.className = "opencode-models-list";

  for (const m of st.models) {
    const row = document.createElement("div");
    row.className = "opencode-model-row" + (m.enabled ? " selected" : "") + (st.provider ? "" : " disabled");

    const checkWrap = document.createElement("label");
    checkWrap.className = "opencode-model-check-wrap";
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.className = "opencode-model-checkbox";
    cb.checked = m.enabled;
    cb.disabled = !st.provider;
    cb.dataset.tag = m.name;
    checkWrap.appendChild(cb);

    const body = document.createElement("div");
    body.className = "opencode-model-body";

    // Top: Tag + Badges (Vision, Context, TPS, Size)
    const top = document.createElement("div");
    top.className = "opencode-model-top";

    const tagWrap = document.createElement("div");
    tagWrap.className = "opencode-model-tag-wrap";
    const tag = document.createElement("span");
    tag.className = "opencode-model-tag mono";
    tag.title = m.name;
    tag.textContent = m.name;
    tagWrap.appendChild(tag);
    top.appendChild(tagWrap);

    const badges = document.createElement("div");
    badges.className = "opencode-model-badges";

    if (m.has_vision) {
      const visBadge = document.createElement("span");
      visBadge.className = "opencode-badge opencode-badge-vision";
      visBadge.title = t("chat.cap.vision") || "Vision";
      visBadge.innerHTML = `<span class="opencode-badge-icon">👁️</span> <span class="opencode-badge-text">Vision</span>`;
      badges.appendChild(visBadge);
    }

    if (m.context_length > 0) {
      const ctxBadge = document.createElement("span");
      ctxBadge.className = "opencode-badge opencode-badge-ctx mono";
      ctxBadge.title = `Context limit: ${m.context_length.toLocaleString()} tokens`;
      ctxBadge.textContent = `${formatCtxTokens(m.context_length)} ctx`;
      badges.appendChild(ctxBadge);
    }

    if (m.record_tps > 0) {
      const tpsBadge = document.createElement("span");
      tpsBadge.className = "opencode-badge opencode-badge-tps mono";
      tpsBadge.title = `Record speed: ${m.record_tps.toFixed(1)} tok/s`;
      tpsBadge.textContent = `⚡ ${Math.round(m.record_tps)} tok/s`;
      badges.appendChild(tpsBadge);
    }

    if (m.size > 0) {
      const sizeBadge = document.createElement("span");
      sizeBadge.className = "opencode-badge opencode-badge-size mono muted";
      sizeBadge.textContent = typeof formatBytes === "function" ? formatBytes(m.size) : `${Math.round(m.size / 1e9 * 10) / 10} GB`;
      badges.appendChild(sizeBadge);
    }

    top.appendChild(badges);
    body.appendChild(top);

    // Bottom: Custom Name input placed right under the model name (full-width)
    const bot = document.createElement("div");
    bot.className = "opencode-model-bottom";

    const auto = openCodeAutoName(m);
    const name = document.createElement("input");
    name.type = "text";
    name.className = "opencode-model-name";
    name.value = m.custom_name ? m.display_name : auto;
    name.dataset.auto = auto;
    name.disabled = !st.provider;
    name.dataset.tag = m.name;
    name.title = m.name;
    name.placeholder = auto || m.name;
    name.autocomplete = "off";
    name.spellcheck = false;

    bot.appendChild(name);
    body.appendChild(bot);

    // Clicking on the model tag or header toggles the checkbox
    tagWrap.addEventListener("click", () => {
      if (!st.provider) return;
      cb.checked = !cb.checked;
      row.classList.toggle("selected", cb.checked);
      cb.dispatchEvent(new Event("change", { bubbles: true }));
    });
    cb.addEventListener("change", () => {
      row.classList.toggle("selected", cb.checked);
    });

    row.appendChild(checkWrap);
    row.appendChild(body);
    list.appendChild(row);
  }
  box.appendChild(list);
}

function openCodeEnabledTags() {
  const tags = [];
  const boxes = $("opencode-models").querySelectorAll('input[type="checkbox"]:checked');
  for (const cb of boxes) tags.push(cb.dataset.tag);
  return tags;
}

function openCodeNamesMap() {
  const names = {};
  const inputs = $("opencode-models").querySelectorAll(".opencode-model-name");
  for (const inp of inputs) {
    const v = inp.value.trim();
    // Untouched rows keep the auto-generated name: don't index them so the
    // style selector keeps working on them.
    if (!v || v === inp.dataset.auto) continue;
    names[inp.dataset.tag] = v;
  }
  return names;
}

function openCodeShortName(tag) {
  const i = tag.lastIndexOf("/");
  return i >= 0 ? tag.slice(i + 1) : tag;
}

// openCodeAutoName builds the display name for a model that has no custom
// name stored in OpenCode, following the selected auto-name style.
function openCodeAutoName(m) {
  const short = openCodeShortName(m.name);
  if (openCodeNameStylePref === "tps" && m.record_tps > 0) {
    return `${short} (${Math.round(m.record_tps)}tok/s)`;
  }
  return short;
}

// openCodeSortBySpeed sorts models in OpenCode by speed descending (fastest first),
// updating the UI and preview so saving preserves the sorted sequence.
function openCodeSortBySpeed() {
  const st = openCodeState;
  if (!st || !st.models || st.models.length === 0) return;

  const box = $("opencode-models");
  if (box) {
    for (const m of st.models) {
      const cb = box.querySelector(`.opencode-model-checkbox[data-tag="${CSS.escape(m.name)}"]`);
      if (cb) m.enabled = cb.checked;
      const inp = box.querySelector(`.opencode-model-name[data-tag="${CSS.escape(m.name)}"]`);
      if (inp) {
        const val = inp.value.trim();
        if (val && val !== inp.dataset.auto) {
          m.display_name = val;
          m.custom_name = true;
        } else if (val === inp.dataset.auto) {
          m.custom_name = false;
        }
      }
    }
  }

  st.models.sort((a, b) => {
    const tpsA = Number(a.record_tps) || 0;
    const tpsB = Number(b.record_tps) || 0;
    if (tpsA !== tpsB) {
      return tpsB - tpsA;
    }
    return a.name.localeCompare(b.name);
  });

  const sortBtn = $("opencode-sort-speed-btn");
  if (sortBtn) {
    sortBtn.classList.add("active");
  }

  renderOpenCodeModels(st);
  renderOpenCodePreview();
}

// applyOpenCodeNameStyle refreshes every untouched auto-named input after the
// style selector changes, preserving user-typed names.
function applyOpenCodeNameStyle() {
  syncOpenCodeStyleUI();
  const st = openCodeState;
  if (!st || !st.models) return;
  const box = $("opencode-models");
  for (const m of st.models) {
    if (m.custom_name) continue;
    const inp = box.querySelector(`.opencode-model-name[data-tag="${CSS.escape(m.name)}"]`);
    if (!inp || inp.value.trim() !== inp.dataset.auto) continue;
    inp.value = openCodeAutoName(m);
    inp.dataset.auto = inp.value;
  }
  renderOpenCodePreview();
}

function openCodeFolderCommand() {
  const ua = navigator.userAgent || "";
  if (/windows/i.test(ua)) return "explorer %USERPROFILE%\\.config\\opencode";
  if (/mac|darwin/i.test(ua) || /mac/i.test(navigator.platform || "")) return "open ~/.config/opencode";
  return "xdg-open ~/.config/opencode";
}

function buildOpenCodeExport() {
  const tags = openCodeEnabledTags();
  const names = openCodeNamesMap();
  const byTag = {};
  for (const m of (openCodeState && openCodeState.models) || []) byTag[m.name] = m;
  const models = {};
  for (const tag of tags) {
    const m = byTag[tag];
    const item = { name: names[tag] || (m ? openCodeAutoName(m) : openCodeShortName(tag)) };
    if (m && m.context_length > 0) {
      item.limit = {
        context: m.context_length,
        output: openCodeDefaultOutputLimit(m.context_length),
      };
    }
    models[tag] = item;
  }
  const provider = {
    ollama: {
      npm: "@ai-sdk/openai-compatible",
      name: "Ollama",
      options: { baseURL: "http://localhost:11434/v1" },
      models,
    },
  };
  return {
    count: tags.length,
    provider: JSON.stringify(provider, null, "\t"),
    models: JSON.stringify({ models }, null, "\t"),
  };
}

function renderOpenCodePreview() {
  const pre = $("opencode-preview");
  const count = $("opencode-preview-count");
  if (!pre) return;
  const ex = buildOpenCodeExport();
  pre.textContent = ex.provider;
  if (count) {
    count.textContent = ex.count > 0 ? String(ex.count) : "0";
  }
}

async function copyOpenCodeText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast(t("settings.opencode_copied"), "success");
  } catch (e) {
    const pre = $("opencode-preview");
    if (pre) {
      const range = document.createRange();
      range.selectNodeContents(pre);
      const sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
    }
    toast(t("settings.opencode_copy_manual"));
  }
}

$("settings-btn")?.addEventListener("click", showSettingsView);
$("settings-back-btn")?.addEventListener("click", () => {
  const savedLang = currentConfig?.language || (window.I18n ? window.I18n.getLang() : "en");
  if ($("set-language")) $("set-language").value = savedLang;
  if (typeof renderSettingsTranslations === "function") renderSettingsTranslations(savedLang);
  showModelsView();
  history.pushState(null, "", "/");
});

// Live language preview in Settings view on dropdown change.
$("set-language").addEventListener("change", () => {
  const lang = $("set-language").value;
  if (typeof renderSettingsTranslations === "function") {
    renderSettingsTranslations(lang);
  }
});

$("set-expose").addEventListener("change", updateExposeWarning);

$("settings-save").addEventListener("click", async () => {
  if (!currentConfig) return;
  const port = parseInt($("set-port").value, 10);
  if (!Number.isFinite(port) || port < 1 || port > 65535) {
    toast(t("toast.error", { msg: "port 1..65535" }), "error");
    return;
  }
  const chatDefaults = {
    system_prompt: $("set-default-system")?.value ?? "",
    temperature: parseFloat($("set-default-temp")?.value) || 0.7,
    top_k: parseInt($("set-default-top-k")?.value, 10) || 40,
    top_p: parseFloat($("set-default-top-p")?.value) || 0.9,
    num_ctx: normalizeNumCtxPct($("set-default-num-ctx")?.value ?? 100),
    think_level: $("set-default-think-level")?.value ?? "auto",
    web_tools: $("set-default-web-tools")?.checked ?? false,
    artifacts: $("set-default-artifacts")?.checked ?? false,
  };
  const body = {
    language: $("set-language").value,
    port,
    expose_network: $("set-expose").checked,
    chat_defaults: chatDefaults,
  };
  try {
    const res = await api("/api/config", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    currentConfig = { ...currentConfig, ...res };
    window.I18n.setLang(res.language);

    saveGlobalChatDefaults({
      system: chatDefaults.system_prompt,
      temperature: chatDefaults.temperature,
      top_k: chatDefaults.top_k,
      top_p: chatDefaults.top_p,
      num_ctx: chatDefaults.num_ctx,
      think_level: chatDefaults.think_level,
      web_tools: chatDefaults.web_tools,
      artifacts: chatDefaults.artifacts,
    });

    const activeChatModel = $("chat-model")?.value;
    if (activeChatModel && !getModelChatOptions(activeChatModel)) {
      void applyChatDefaultsForModel(activeChatModel, true);
    }

    toast(res.needs_restart ? t("settings.saved_restart") : t("settings.saved"), "success");
    renderSettingsTranslations(res.language);
    refreshStatus();
    renderTable();
    if (activeName) openDetail(activeName);
    if (typeof currentView !== "undefined" && currentView === "analytics" && typeof renderAnalytics === "function") renderAnalytics();
    if (typeof currentView !== "undefined" && currentView === "downloads" && typeof renderDownloads === "function") renderDownloads();
    if (typeof currentView !== "undefined" && currentView === "modelfile" && typeof refreshModelfileUI === "function") refreshModelfileUI();
    updateChatContextMeter();
    renderAttachments();
    renderChatMessages();
    renderChatQueue();
    updateStreamBar();
    updateChatCapabilityUI();
    updateChatSendEnabled();
    refreshOpenCodeUI();
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
});

$("pwd-save-btn").addEventListener("click", async () => {
  const pwd = $("set-password").value;
  if (pwd.length < 4) {
    toast(t("settings.pwd_too_short"), "error");
    return;
  }
  try {
    const res = await api("/api/config/password", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password: pwd }),
    });
    currentConfig.has_password = res.has_password;
    $("set-password").value = "";
    updatePasswordSection();
    updateExposeWarning();
    refreshStatus();
    toast(t("settings.pwd_saved"), "success");
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
});

$("pwd-clear-btn").addEventListener("click", async () => {
  try {
    const res = await api("/api/config/password", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password: "" }),
    });
    currentConfig.has_password = res.has_password;
    $("set-password").value = "";
    updatePasswordSection();
    updateExposeWarning();
    refreshStatus();
    toast(t("settings.pwd_cleared"), "success");
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
});

$("settings-archived-btn")?.addEventListener("click", () => {
  closeSettings();
  showArchivedOnly = true;
  $("archived-banner").hidden = false;
  renderTable();
});

$("btn-back-active")?.addEventListener("click", () => {
  showArchivedOnly = false;
  $("archived-banner").hidden = true;
  renderTable();
});

$("archived-back-btn")?.addEventListener("click", () => {
  showArchivedOnly = false;
  $("archived-banner").hidden = true;
  renderTable();
});

