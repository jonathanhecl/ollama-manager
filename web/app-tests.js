"use strict";

// ---------- tests views ----------

function hideAllMainViews() {
  if (typeof currentView !== "undefined" && currentView === "chat") {
    if (typeof resetChatState === "function") {
      resetChatState();
    }
  }
  $("models-view").hidden = true;
  $("chat-view").hidden = true;
  $("tests-view").hidden = true;
  $("test-editor-view").hidden = true;
  $("agent-session-view").hidden = true;
  $("battery-progress-view").hidden = true;
  $("battery-results-view").hidden = true;
  $("battery-history-view").hidden = true;
  $("opencode-view").hidden = true;
  $("analytics-view").hidden = true;
  $("modelfile-view") && ($("modelfile-view").hidden = true);
  $("detail-panel").hidden = true;
}

function showOpenCodeView() {
  hideAllMainViews();
  currentView = "opencode";
  $("opencode-view").hidden = false;
  refreshOpenCodeUI();
}

function showAnalyticsView() {
  hideAllMainViews();
  stopSpeechPlayback();
  currentView = "analytics";
  $("analytics-view").hidden = false;
  if (!window.location.pathname.startsWith("/analytics")) {
    history.pushState(null, "", "/analytics");
  }
  renderAnalytics();
}

