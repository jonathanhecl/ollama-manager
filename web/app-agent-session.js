"use strict";

// ---------- agent session ----------

async function showAgentSessionView(testId) {
  currentAgentTestId = testId;
  hideAllMainViews();
  currentView = "agent-session";
  $("agent-session-view").hidden = false;
  if (window.location.pathname !== "/tests/agent/" + testId) {
    history.pushState(null, "", "/tests/agent/" + testId);
  }

  // Look up the test to display title.
  const test = tests.find((t) => t.id === testId);
  $("agent-session-title").textContent = test ? test.name : "Agent Test";

  // Try to find an existing session for this test, or create one.
  try {
    const sessions = await api("/api/tests/agent/sessions");
    const existing = sessions.find((s) => s.test_id === testId);
    if (existing) {
      currentAgentSession = existing;
      renderAgentSession();
      return;
    }
  } catch {
    // ignore, will create below
  }

  try {
    const created = await api("/api/tests/agent/sessions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ test_id: testId, model_id: null }),
    });
    currentAgentSession = created;
    renderAgentSession();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function refreshAgentSession() {
  if (!currentAgentSession) return;
  try {
    const s = await api("/api/tests/agent/sessions/" + encodeURIComponent(currentAgentSession.id));
    currentAgentSession = s;
    renderAgentSession();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

function renderAgentSession() {
  const s = currentAgentSession;
  if (!s) return;

  $("agent-meta-max-turns").textContent = String(s.max_turns || "—");
  $("agent-meta-current-turn").textContent = String(s.turns?.length || 0);
  $("agent-meta-model").textContent = s.model_id || t("tests.agent_no_model");

  const statusBadge = $("agent-session-status");
  if (s.completed) {
    statusBadge.textContent = t("tests.agent_completed");
    statusBadge.className = "agent-status-badge completed";
    $("agent-feedback-area").hidden = true;
  } else {
    statusBadge.textContent = t("tests.agent_in_progress");
    statusBadge.className = "agent-status-badge in-progress";
    $("agent-feedback-area").hidden = false;
  }

  renderAgentTurns(s.turns || []);
  renderAgentSandbox(s.id);
}

function renderAgentTurns(turns) {
  const container = $("agent-turns-timeline");
  if (!container) return;
  if (!turns.length) {
    container.innerHTML = `<div class="muted">${escapeHtml(t("tests.agent_no_turns"))}</div>`;
    return;
  }
  container.innerHTML = turns.map((turn, idx) => {
    const roleClass = turn.role === "tool" ? "agent-turn-tool" : turn.role === "system" ? "agent-turn-system" : "agent-turn-user";
    const content = escapeHtml(turn.content || "");
    const toolCall = turn.tool_call ? `<pre class="agent-turn-tool-call">${escapeHtml(JSON.stringify(turn.tool_call, null, 2))}</pre>` : "";
    return `<div class="agent-turn ${roleClass}">
      <div class="agent-turn-header">#${idx + 1} — ${escapeHtml(turn.role)}</div>
      <div class="agent-turn-body">${content || ""}</div>
      ${toolCall}
      ${turn.tool_result ? `<pre class="agent-turn-tool-result">${escapeHtml(JSON.stringify(turn.tool_result, null, 2))}</pre>` : ""}
    </div>`;
  }).join("");
  container.scrollTop = container.scrollHeight;
}

async function renderAgentSandbox(sessionId) {
  const container = $("agent-sandbox-tree");
  if (!container) return;
  try {
    const files = await api("/api/tests/agent/sessions/" + encodeURIComponent(sessionId) + "/files");
    if (!files || !files.length) {
      container.innerHTML = `<div class="muted">${escapeHtml(t("tests.agent_empty_sandbox"))}</div>`;
      return;
    }
    container.innerHTML = files.map((f) => {
      const icon = f.is_dir ? "📁" : "📄";
      return `<div class="agent-sandbox-item" data-path="${escapeHtml(f.path)}">
        <span>${icon} ${escapeHtml(f.name)}</span>
      </div>`;
    }).join("");
  } catch {
    container.innerHTML = `<div class="muted">${escapeHtml(t("tests.agent_sandbox_error"))}</div>`;
  }
}

async function submitAgentFeedback() {
  if (!currentAgentSession) return;
  const input = $("agent-feedback-input");
  const content = input.value.trim();
  if (!content) return;
  try {
    await api("/api/tests/agent/sessions/" + encodeURIComponent(currentAgentSession.id) + "/message", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ role: "user", content }),
    });
    input.value = "";
    await refreshAgentSession();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function resetAgentSession() {
  if (!currentAgentSession) return;
  try {
    await api("/api/tests/agent/sessions/" + encodeURIComponent(currentAgentSession.id) + "/reset", { method: "POST" });
    await refreshAgentSession();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

async function deleteAgentSession() {
  if (!currentAgentSession) return;
  const ok = await askConfirm({
    title: t("tests.agent_delete_session"),
    text: t("tests.agent_delete_confirm"),
    okText: t("action.delete"),
    okClass: "danger",
  });
  if (!ok.ok) return;
  try {
    await api("/api/tests/agent/sessions/" + encodeURIComponent(currentAgentSession.id), { method: "DELETE" });
    currentAgentSession = null;
    showTestsView();
  } catch (err) {
    toast(t("toast.error", { msg: err.message }), "error");
  }
}

