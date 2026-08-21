"use strict";

// ---------- OpenCode actions ----------
$("settings-opencode-btn").addEventListener("click", () => {
  closeSettings();
  showOpenCodeView();
});

$("opencode-back-btn").addEventListener("click", showModelsView);

$("opencode-create-btn").addEventListener("click", async () => {
  try {
    await api("/api/opencode/provider", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({}),
    });
    toast(t("settings.opencode_created"), "success");
    await refreshOpenCodeUI();
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
});

$("opencode-save-btn").addEventListener("click", async () => {
  try {
    const res = await api("/api/opencode/models", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        enabled: openCodeEnabledTags(),
        names: openCodeNamesMap(),
      }),
    });
    openCodeState = res.state;
    toast(t("settings.opencode_saved"), "success");
    renderOpenCodeView();
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
});

$("opencode-models").addEventListener("change", renderOpenCodePreview);
$("opencode-models").addEventListener("input", renderOpenCodePreview);

$("opencode-name-style").value = openCodeNameStylePref;
$("opencode-name-style").addEventListener("change", () => {
  openCodeNameStylePref = $("opencode-name-style").value === "tps" ? "tps" : "plain";
  localStorage.setItem("ollama_opencode_name_style", openCodeNameStylePref);
  applyOpenCodeNameStyle();
});

$("opencode-copy-all-btn").addEventListener("click", () => {
  const ex = buildOpenCodeExport();
  if (ex.count === 0) {
    toast(t("settings.opencode_empty_selection"), "error");
    return;
  }
  copyOpenCodeText(ex.provider);
});

$("opencode-copy-models-btn").addEventListener("click", () => {
  const ex = buildOpenCodeExport();
  if (ex.count === 0) {
    toast(t("settings.opencode_empty_selection"), "error");
    return;
  }
  copyOpenCodeText(ex.models);
});

$("opencode-cmd").textContent = openCodeFolderCommand();
$("opencode-cmd-copy-btn").addEventListener("click", async () => {
  const ok = await copyTextToClipboard(openCodeFolderCommand());
  toast(ok ? t("settings.opencode_cmd_copied") : t("settings.opencode_copy_manual"), ok ? "success" : "");
});

