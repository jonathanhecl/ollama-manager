"use strict";

// ---------- OpenCode integration ----------
let openCodeState = null;

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
  if (!$("opencode-view") || $("opencode-view").hidden) return;
  const badge = $("opencode-badge");
  const status = $("opencode-status");
  const noProvider = $("opencode-noprovider");
  const createBtn = $("opencode-create-btn");
  const saveBtn = $("opencode-save-btn");
  if (st.provider) {
    badge.textContent = t("settings.opencode_configured");
    badge.className = "badge badge-good";
    status.textContent = `${st.config_path} · ${st.provider.key} → ${st.provider.base_url}`;
  } else {
    badge.textContent = t("settings.opencode_not_configured");
    badge.className = "badge badge-warn";
    status.textContent = st.exists
      ? t("settings.opencode_no_provider_status")
      : t("settings.opencode_no_file_status");
  }
  noProvider.hidden = !!st.provider;
  createBtn.hidden = !!st.provider;
  saveBtn.disabled = !st.provider;
  const remoteWarn = $("opencode-remote-warn");
  if (remoteWarn) remoteWarn.hidden = !st.remote;
  renderOpenCodeModels(st);
  renderOpenCodePreview();
}

function renderOpenCodeModels(st) {
  const box = $("opencode-models");
  if (!st.models || st.models.length === 0) {
    box.innerHTML = `<div class="muted">${escapeHtml(t("settings.opencode_empty"))}</div>`;
    return;
  }
  box.innerHTML = "";
  const list = document.createElement("div");
  for (const m of st.models) {
    const row = document.createElement("label");
    row.className = "opencode-model-row" + (st.provider ? "" : " disabled");
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.checked = m.enabled;
    cb.disabled = !st.provider;
    cb.dataset.tag = m.name;
    const name = document.createElement("input");
    name.type = "text";
    name.className = "opencode-model-name";
    name.value = m.display_name;
    name.disabled = !st.provider;
    name.dataset.tag = m.name;
    name.title = m.name;
    name.placeholder = m.name;
    name.autocomplete = "off";
    name.spellcheck = false;
    const tag = document.createElement("span");
    tag.className = "opencode-model-tag mono muted";
    tag.textContent = m.name;
    row.appendChild(cb);
    row.appendChild(name);
    row.appendChild(tag);
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
  for (const inp of inputs) names[inp.dataset.tag] = inp.value.trim();
  return names;
}

function openCodeShortName(tag) {
  const i = tag.lastIndexOf("/");
  return i >= 0 ? tag.slice(i + 1) : tag;
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
  const models = {};
  for (const tag of tags) models[tag] = { name: (names[tag] || openCodeShortName(tag)) };
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

$("settings-btn").addEventListener("click", openSettings);
$("settings-close").addEventListener("click", closeSettings);
$("settings-x").addEventListener("click", closeSettings);
$("settings-modal").addEventListener("click", (e) => {
  if (e.target === $("settings-modal")) closeSettings();
});

// Live language switch on dropdown change.
$("set-language").addEventListener("change", () => {
  const lang = $("set-language").value;
  window.I18n.setLang(lang);
  // Re-render dynamic UI to pick up the new language.
  refreshStatus();
  renderTable();
  if (activeName) openDetail(activeName);
  updateChatContextMeter();
  renderAttachments();
  renderChatMessages();
  renderChatQueue();
  updateStreamBar();
  updateChatCapabilityUI();
  updateChatSendEnabled();
  updatePasswordSection();
  refreshOpenCodeUI();
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
    $("settings-modal").hidden = true;
    refreshStatus();
    renderTable();
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

