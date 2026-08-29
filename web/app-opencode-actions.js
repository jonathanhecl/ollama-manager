"use strict";

// ---------- OpenCode actions ----------
$("settings-opencode-btn")?.addEventListener("click", () => {
  showOpenCodeView();
});

$("opencode-back-btn")?.addEventListener("click", () => {
  showSettingsView();
});

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
    const tags = openCodeEnabledTags();
    const names = openCodeNamesMap();
    const limits = {};
    const byTag = {};
    for (const m of (openCodeState && openCodeState.models) || []) byTag[m.name] = m;
    for (const tag of tags) {
      const m = byTag[tag];
      if (m && m.context_length > 0) {
        limits[tag] = {
          context: m.context_length,
          output: openCodeDefaultOutputLimit(m.context_length),
        };
      }
    }
    const res = await api("/api/opencode/models", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        enabled: tags,
        names: names,
        limits: limits,
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

$("opencode-style-segmented")?.addEventListener("click", (e) => {
  const btn = e.target.closest(".opencode-style-btn");
  if (!btn) return;
  const style = btn.dataset.style;
  if (style !== "plain" && style !== "tps") return;
  openCodeNameStylePref = style;
  localStorage.setItem("ollama_opencode_name_style", openCodeNameStylePref);
  syncOpenCodeStyleUI();
  applyOpenCodeNameStyle();
});

if ($("opencode-name-style")) {
  $("opencode-name-style").value = openCodeNameStylePref;
  $("opencode-name-style").addEventListener("change", () => {
    openCodeNameStylePref = $("opencode-name-style").value === "tps" ? "tps" : "plain";
    localStorage.setItem("ollama_opencode_name_style", openCodeNameStylePref);
    syncOpenCodeStyleUI();
    applyOpenCodeNameStyle();
  });
}
syncOpenCodeStyleUI();

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

