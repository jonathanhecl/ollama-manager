"use strict";

// ---------- settings ----------
let currentConfig = null;

async function openSettings() {
  try {
    currentConfig = await api("/api/config");
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
    return;
  }
  $("set-language").value = currentConfig.language;
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

  updatePasswordSection();
  updateExposeWarning();
  updateBindPreview();
  $("settings-modal").hidden = false;
}

function updatePasswordSection() {
  if (!currentConfig) return;
  const badge = $("pwd-badge");
  if (currentConfig.has_password) {
    badge.textContent = t("settings.pwd_set");
    badge.className = "badge badge-good";
  } else {
    badge.textContent = t("settings.pwd_unset");
    badge.className = "badge badge-muted";
  }
  $("pwd-clear-btn").hidden = !currentConfig.has_password;
  $("settings-logout-btn").hidden = !currentConfig.has_password;
}

function updateBindPreview() {
  if (!currentConfig) return;
  const badge = $("bind-preview");
  const expose = $("set-expose").checked;
  if (expose) {
    badge.textContent = t("settings.bind_lan");
    badge.className = "badge badge-warn";
  } else {
    badge.textContent = t("settings.bind_local");
    badge.className = "badge badge-muted";
  }
}

function updateExposeWarning() {
  if (!currentConfig) return;
  const wantExpose = $("set-expose").checked;
  $("expose-warning").hidden = !(wantExpose && !currentConfig.has_password);
  updateBindPreview();
}

