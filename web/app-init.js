"use strict";

// ---------- init ----------
// Tests and Analytics enabled across all device sizes
$("tests-btn") && ($("tests-btn").hidden = false);
$("analytics-btn") && ($("analytics-btn").hidden = false);

// Restore active chat session if page was refreshed or reloaded during chat
if (typeof restoreActiveChatSession === "function" && restoreActiveChatSession()) {
  if (typeof showChatView === "function") {
    showChatView();
  }
}
