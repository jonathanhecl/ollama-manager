"use strict";

// ---------- tests views ----------

function hideAllMainViews() {
  if (typeof currentView !== "undefined" && currentView === "chat") {
    if (typeof resetChatState === "function") {
      resetChatState();
    }
  }
  $("models-view") && ($("models-view").hidden = true);
  $("chat-view") && ($("chat-view").hidden = true);
  $("tests-view") && ($("tests-view").hidden = true);
  $("test-editor-view") && ($("test-editor-view").hidden = true);
  $("agent-session-view") && ($("agent-session-view").hidden = true);
  $("battery-progress-view") && ($("battery-progress-view").hidden = true);
  $("battery-results-view") && ($("battery-results-view").hidden = true);
  $("battery-history-view") && ($("battery-history-view").hidden = true);
  $("analytics-view") && ($("analytics-view").hidden = true);
  $("settings-view") && ($("settings-view").hidden = true);
  $("modelfile-view") && ($("modelfile-view").hidden = true);
  $("hf-view") && ($("hf-view").hidden = true);
  $("detail-panel") && ($("detail-panel").hidden = true);
  document.querySelectorAll(".topbar-actions button").forEach((b) => b.classList.remove("active"));
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

function showAnalyticsView() {
  hideAllMainViews();
  stopSpeechPlayback();
  currentView = "analytics";
  $("analytics-btn")?.classList.add("active");
  $("analytics-view").hidden = false;
  if (!window.location.pathname.startsWith("/analytics")) {
    history.pushState(null, "", "/analytics");
  }
  renderAnalytics();
}

