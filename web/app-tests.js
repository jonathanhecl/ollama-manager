"use strict";

// ---------- tests views ----------

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

