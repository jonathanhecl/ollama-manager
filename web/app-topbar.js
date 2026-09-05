"use strict";

// ---------- topbar buttons ----------
async function logoutAndRedirect() {
  await fetch("/logout", { method: "POST", credentials: "same-origin" });
  window.location.href = "/login";
}

document.querySelector(".brand")?.addEventListener("click", () => {
  showModelsView();
  history.pushState(null, "", "/");
});
$("models-reload-btn")?.addEventListener("click", () => { refreshStatus(); refreshModels(); });
$("settings-logout-btn").addEventListener("click", logoutAndRedirect);

$("tests-btn")?.addEventListener("click", () => {
  showTestsView();
});
$("tests-back-btn")?.addEventListener("click", () => {
  showModelsView();
});
$("analytics-btn")?.addEventListener("click", () => {
  showAnalyticsView();
});
$("analytics-back-btn")?.addEventListener("click", () => {
  showModelsView();
});
$("tests-new-test-btn")?.addEventListener("click", () => {
  void showTestEditorView(null);
});
$("tests-new-group-btn")?.addEventListener("click", () => {
  createNewGroup();
});
$("test-editor-back")?.addEventListener("click", () => {
  showTestsView();
});
$("test-editor-cancel")?.addEventListener("click", () => {
  showTestsView();
});
$("test-editor-save")?.addEventListener("click", () => {
  void saveTestEditor();
});
$("test-editor-delete")?.addEventListener("click", () => {
  void deleteTestEditor();
});
$("te-eval-type")?.addEventListener("change", () => {
  updateAgentSettingsVisibility();
  updateEvalConfigVisibility();
});

function updateAgentSettingsVisibility() {
  const isAgent = $("te-eval-type")?.value === "agent";
  const panel = $("te-agent-settings");
  if (panel) panel.hidden = !isAgent;
}

function updateEvalConfigVisibility() {
  const type = $("te-eval-type")?.value;
  const expectedWrap = $("te-eval-expected-wrap");
  const patternWrap = $("te-eval-pattern-wrap");
  const configWrap = $("te-eval-config-wrap");
  if (!expectedWrap || !patternWrap || !configWrap) return;
  expectedWrap.hidden = type !== "exact_match" && type !== "contains";
  patternWrap.hidden = type !== "regex";
  configWrap.hidden = type !== "json_schema" && type !== "agent";
}

// Test editor per-case sidecar picker. The target case index is set by the
// card's "Add file" button (see app-svg.js); a single hidden input serves
// all cards.
$("te-case-file-input")?.addEventListener("change", (e) => {
  const files = e.target.files;
  if (files?.length) {
    void handleTestEditorCaseFile(Array.from(files).slice(0, 1));
  }
  e.target.value = "";
});
$("te-required-caps")?.addEventListener("input", () => {
  updateTestEditorAutoCaps();
});

// Disable autocomplete globally for all inputs, textareas, and selects.
document.querySelectorAll("input, textarea, select").forEach((el) => {
  if (!el.hasAttribute("autocomplete")) {
    el.setAttribute("autocomplete", "off");
  }
});

