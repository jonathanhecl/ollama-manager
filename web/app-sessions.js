// Persistent chat sessions.
//
// A quick chat lives and dies with the browser tab: the model streams straight
// into the open page and closing the tab kills the run. A session is different.
// It is stored on the server, it keeps running after the tab is closed, and it
// can be closed, reopened, continued or deleted later. The UI surface is the
// "+ Session" button in the options panel, one row per session under it, and a
// badge next to every model in the list so a background run is visible from
// anywhere.
//
// Badges follow the same three states everywhere:
//   running / queued -> red, glowing (the model is working right now)
//   idle + unseen    -> white (it finished and you have not read it yet)
//   idle + seen      -> nothing (concluded)

// chatSessions maps session id -> summary, as returned by GET /api/chat/sessions.
let chatSessions = new Map();
// chatSessionId is the session currently loaded into the chat view, or null
// while the user is on the plain quick chat.
let chatSessionId = null;
// chatSessionFeed follows every session state change (badge feed).
let chatSessionFeed = null;
// chatSessionStream follows the live stream of the open session only.
let chatSessionStream = null;
// chatSessionRun holds the per-turn state applyChatStreamEvent needs.
let chatSessionRun = null;
// chatSessionRunPending is set between posting a message and the turn actually
// starting. While it is true a status update saying "idle" is stale, so it must
// not settle the run: the turn is about to begin.
let chatSessionRunPending = false;
// chatSessionFeedRetry guards the EventSource against a tight reconnect loop.
let chatSessionFeedRetry = 0;
// chatSessionsEnabled mirrors the server setting. It starts true so the panel is
// usable before the first /api/chat/sessions round trip answers.
let chatSessionsEnabled = true;

// ---------- badges ----------

// sessionBadgeState reports whether a model has work in flight or an unread
// reply waiting, which is all the model list needs to draw its dots.
function sessionBadgeState(modelName) {
  if (!modelName || !chatSessions.size) return null;
  let running = 0;
  let unseen = 0;
  for (const s of chatSessions.values()) {
    if (s.model !== modelName) continue;
    if (s.status === "running" || s.status === "queued") running += 1;
    else if (s.unseen) unseen += 1;
  }
  if (!running && !unseen) return null;
  return { running, unseen };
}

// sessionBadgeHtml renders the dots shown inside a session row or a model row.
function sessionBadgeHtml(modelName) {
  const st = sessionBadgeState(modelName);
  if (!st) return "";
  let out = "";
  if (st.unseen > 0) {
    out += `<span class="session-badge session-badge-unseen" title="${escapeHtml(t("chat.session_badge_unseen"))}"></span>`;
  }
  if (st.running > 0) {
    out += `<span class="session-badge session-badge-running" title="${escapeHtml(t("chat.session_badge_running"))}"></span>`;
  }
  return out;
}

// renderSessionBadges patches the dots already in the DOM. renderTable draws
// them too, so a full re-render stays correct; this keeps them live in between.
function renderSessionBadges() {
  const tbody = $("models-tbody");
  if (!tbody) return;
  for (const tr of tbody.querySelectorAll("tr[data-name]")) {
    const track = tr.querySelector(".model-name-track");
    if (!track) continue;
    const html = sessionBadgeHtml(tr.dataset.name);
    let slot = track.querySelector(".session-badges");
    if (!html) {
      if (slot) slot.remove();
      continue;
    }
    if (!slot) {
      slot = document.createElement("span");
      slot.className = "session-badges";
      track.appendChild(slot);
    }
    if (slot.innerHTML !== html) slot.innerHTML = html;
  }
}

// ---------- session list ----------

function sessionRowStatusText(sum) {
  if (sum.status === "running") return t("chat.session_status_running");
  if (sum.status === "queued") return t("chat.session_status_queued");
  if (sum.status === "error") return t("chat.session_status_error");
  if (sum.status === "cancelled") return t("chat.session_status_cancelled");
  return fmtRelativeTime(sum.last_active_at);
}

function renderSessionList() {
  const list = $("chat-sessions-list");
  if (!list) return;
  const sessions = [...chatSessions.values()];

  let html = `<button type="button" class="chat-session-row chat-session-row-quick${chatSessionId ? "" : " active"}" data-session-quick="1">
    <span class="chat-session-row-badges"></span>
    <span class="chat-session-row-main">
      <span class="chat-session-row-title">${escapeHtml(t("chat.session_quick"))}</span>
      <span class="chat-session-row-meta">${escapeHtml(t("chat.session_quick_hint"))}</span>
    </span>
  </button>`;

  for (const s of sessions) {
    const active = s.id === chatSessionId;
    // The server only derives a title once the session has more than one turn, so
    // an unnamed row is a brand-new session. Showing a placeholder would fill the
    // panel with rows that all read the same; the relative time is the only line
    // that tells them apart until there is a name worth showing.
    const title = s.title || "";
    const canStop = s.status === "running" || s.status === "queued";
    // A session whose model is gone can never run again. The summary has no way
    // to know that, so the row checks the live model list and says so instead of
    // silently failing on the next message.
    const modelMissing = !!s.model && !modelByName(s.model);
    const notes = `${s.error ? ` · ${escapeHtml(s.error)}` : ""}${s.dropped_messages ? ` · ${escapeHtml(t("chat.session_trimmed", { n: s.dropped_messages }))}` : ""}${modelMissing ? ` · ${escapeHtml(t("chat.session_model_missing"))}` : ""}`;
    const meta = `${escapeHtml(sessionRowStatusText(s))}${notes}`;
    // With no title the row is brand new, so the relative time takes the place of
    // the name and the second line is dropped unless there is something to warn
    // about. That keeps the row a single readable line instead of two lines of
    // which the first says nothing.
    const head = title
      ? `<span class="chat-session-row-title" data-session-title="${escapeHtml(s.id)}" title="${escapeHtml(t("chat.session_rename_hint"))}">${escapeHtml(title)}</span><span class="chat-session-row-meta">${meta}</span>`
      : `<span class="chat-session-row-title chat-session-row-title-new" data-session-title="${escapeHtml(s.id)}" title="${escapeHtml(t("chat.session_rename_hint"))}">${meta}</span>`;
    html += `<div class="chat-session-row${active ? " active" : ""}${title ? "" : " chat-session-row-new"}" data-session-id="${escapeHtml(s.id)}"${title ? ` title="${escapeHtml(title)}"` : ""}>
      <span class="chat-session-row-badges">${sessionBadgeHtml(s.model)}</span>
      <span class="chat-session-row-main" data-session-open="${escapeHtml(s.id)}">
        ${head}
      <span class="chat-session-row-actions">
        ${canStop ? `<button type="button" class="chat-session-row-btn" data-session-stop="${escapeHtml(s.id)}" title="${escapeHtml(t("chat.session_stop"))}" aria-label="${escapeHtml(t("chat.session_stop"))}">■</button>` : ""}
        <button type="button" class="chat-session-row-btn" data-session-del="${escapeHtml(s.id)}" title="${escapeHtml(t("chat.session_delete"))}" aria-label="${escapeHtml(t("chat.session_delete"))}">×</button>
      </span>
    </div>`;
  }

  list.innerHTML = html;

  const count = $("chat-sessions-count");
  if (count) {
    count.textContent = sessions.length ? String(sessions.length) : "";
  }
  // The header button and the in-list one are the same action; showing both
  // would just be two ways to delete everything on the same screen.
  const clearBtn = $("chat-sessions-clear-all");
  if (clearBtn) {
    // One session is removed with its own ×, so the nuke button only shows up
    // once there is more than one thing to lose.
    clearBtn.hidden = !chatSessionsEnabled || sessions.length < 2;
  }
}

function mergeSessionSummary(sum) {
  if (!sum || !sum.id) return;
  if (sum.status === "error" && !sum.messages) {
    chatSessions.delete(sum.id);
  } else {
    chatSessions.set(sum.id, sum);
  }
  if (sum.id === chatSessionId) {
    syncSessionRunWithStatus(sum);
  }
}

// syncSessionRunWithStatus keeps the local "turn in flight" flag honest, so the
// elapsed timer and the send button behave while a detached run is going on.
function syncSessionRunWithStatus(sum) {
  const busy = sum.status === "running" || sum.status === "queued";
  if (busy) {
    if (!chatSessionRun) {
      const msg = ensureSessionLiveMessage();
      if (msg) {
        chatSessionRun = { raw: msg.raw || "", turnStartedAt: msg.streamStartedAt || Date.now(), modelName: sum.model || "" };
        activeStreamMessage = msg;
        chatStreamLock = true;
        startStreamTicker(msg, chatSessionRun.turnStartedAt);
        updateStreamBar();
      }
    }
    return;
  }
  if (chatSessionRunPending) return;
  if (chatSessionRun) settleSessionRun();
}

function settleSessionRun() {
  chatSessionRunPending = false;
  chatSessionRun = null;
  const msg = sessionLiveMessage();
  if (msg) {
    msg.streaming = false;
    msg.inThink = false;
    if (msg.thinkStartedAt && !msg.thinkMs) {
      msg.thinkMs = Date.now() - msg.thinkStartedAt;
    }
    if (!msg.elapsedMs && chatSessionRun === null) {
      msg.elapsedMs = Math.max(0, Date.now() - (msg.streamStartedAt || Date.now()));
    }
  }
  stopThinkTicker();
  stopStreamTicker();
  chatStreamLock = false;
  activeStreamMessage = null;
  updateStreamBar();
  updateChatSendEnabled();
  flushChatRender();
  void refreshModelArtifactCount();
  refreshModels().catch(() => {});
}

// ---------- loading ----------

async function loadChatSessions() {
  // null means "we could not ask", which must not be confused with "off": a
  // network blip should not hide the whole feature.
  let enabled = null;
  try {
    const data = await api("/api/chat/sessions");
    // The server is the authority on whether the feature is on: settings can
    // turn it off at any moment, and a page left open has to notice. When it is
    // off the whole block is hidden and the chat stays the plain, request-bound
    // quick chat.
    enabled = data?.enabled !== false;
    chatSessions = new Map((data?.sessions || []).map((s) => [s.id, s]));
  } catch {
    chatSessions = new Map();
  }
  if (enabled !== null) chatSessionsEnabled = enabled;
  renderSessionList();
  renderSessionBadges();
  applyChatSessionsAvailability();
}

// applyChatSessionsAvailability hides the "+ Session" button and the session
// list when the feature is disabled in settings. A session that is already open
// is left alone: turning the switch off cancels the running turn server-side, so
// the page still needs to follow it to the end and let the user read the result.
function applyChatSessionsAvailability() {
  const block = $("chat-sessions-block");
  if (block) block.hidden = !chatSessionsEnabled;
  const btn = $("chat-session-new-btn");
  if (btn) btn.hidden = !chatSessionsEnabled;
  if (chatSessionsEnabled || chatSessionId) return;
  // Nothing to show while the feature is off: drop the badges too, otherwise the
  // model list keeps hinting at sessions the user can no longer open.
  chatSessions = new Map();
  renderSessionBadges();
}

function sessionMessageToChatMessage(m) {
  const out = {
    id: nanoid(),
    role: m.role,
    content: m.content || "",
    model: m.model || "",
    raw: m.raw || "",
    attachments: (m.attachments || []).map((a) => ({
      kind: a.kind,
      name: a.name || "",
      mimeType: a.mime_type || "",
      text: a.text || "",
      data: a.data || "",
    })),
    toolLog: (m.tool_log || []).map((e) => ({ ...e })),
  };
  if (m.artifact_ts) out.artifactTimestamp = m.artifact_ts;
  if (m.artifact_name) out.artifactName = m.artifact_name;
  if (m.artifact_url) out.artifactUrl = m.artifact_url;
  if (m.artifact_desc) out.artifactDescription = m.artifact_desc;
  if (m.artifact_generating) out.artifactGenerating = true;
  if (m.elapsed_ms) out.elapsedMs = m.elapsed_ms;
  // The server measured the thinking time, so a restored transcript shows the
  // same duration the live turn did instead of an empty "thinking (0ms)".
  if (m.think_ms) out.thinkMs = m.think_ms;
  if (m.prompt_tokens) out.promptTokens = m.prompt_tokens;
  if (m.completion_tokens) out.completionTokens = m.completion_tokens;
  if (m.eval_duration_ns) out.evalDurationNs = m.eval_duration_ns;
  if (m.done_reason) out.doneReason = m.done_reason;
  if (m.created_at) out.createdAt = m.created_at;
  if (m.error) {
    out.isError = true;
    out.error = m.error;
  }

  if (m.role === "assistant") {
    // Reproduce what a live message looks like, so the renderer cannot tell the
    // difference between a turn that streamed here and one restored from disk.
    const parts = splitThink(out.raw || "");
    out.thinkContent = m.think || parts.think || "";
    out.content = m.content || parts.answer || "";
    out.inThink = false;
    out.thinkOpen = false;
    out.streaming = !!m.pending;
    out.hasDebug = !m.pending;
    out.streamStartedAt = m.stream_started_at || 0;
  }
  return out;
}

function applySessionTranscript(detail) {
  chatMessages = (detail.messages || []).map(sessionMessageToChatMessage);

  // Re-apply the options exactly as they were when the last message was sent,
  // so a continued session keeps its system prompt, tools and artifacts.
  if (detail.settings) setChatOptionsValues(detail.settings);

  const modelSel = $("chat-model");
  if (modelSel && detail.model) {
    const wanted = detail.model;
    const has = [...modelSel.options].some((o) => o.value === wanted);
    if (has) {
      modelSel.value = wanted;
    } else if (wanted) {
      const opt = document.createElement("option");
      opt.value = wanted;
      opt.textContent = wanted;
      modelSel.appendChild(opt);
      modelSel.value = wanted;
    }
  }
  activeName = detail.model || null;

  chatLastUsedTokens = chatMessages.reduce((acc, m) => acc + (m.promptTokens || 0) + (m.completionTokens || 0), 0);

  // Point the artifact panel at whatever this session last produced.
  activeArtifactTimestamp = "";
  activeArtifactName = "";
  activeArtifactUrl = "";
  hideArtifactPanel();
  let artifactMsg = null;
  for (let i = chatMessages.length - 1; i >= 0; i -= 1) {
    if (chatMessages[i].artifactUrl) {
      artifactMsg = chatMessages[i];
      break;
    }
  }
  if (artifactMsg) {
    activeArtifactTimestamp = artifactMsg.artifactTimestamp || "";
    activeArtifactName = artifactMsg.artifactName || "";
    activeArtifactUrl = artifactMsg.artifactUrl || "";
    if (!artifactMsg.artifactGenerating) {
      showArtifactPanel(artifactMsg.artifactUrl, artifactMsg.artifactName);
    }
  }

  updateArtifactResourceBtn();
  updateChatCapabilityUI();
  updateChatContextMeter();
  updateChatSendEnabled();
  renderChatMessages();
  scrollChatToBottom(false);
}

// ---------- open / close ----------

async function openChatSession(id) {
  let detail;
  try {
    detail = await api(`/api/chat/sessions/${encodeURIComponent(id)}`);
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
    await loadChatSessions();
    return;
  }
  closeChatSessionStream();
  chatSessionId = id;
  chatSessionRunPending = false;
  // Detach whatever the previous session was streaming before the transcript is
  // replaced. chatStreamLock and activeStreamMessage are chat-wide globals: a run
  // that is still "in flight" here would keep the send button stuck on Queue and
  // keep appending to a transcript that is no longer on screen. The turn itself
  // is not cancelled - it keeps running on the server, which is the whole point
  // of a session, and its badge keeps showing that it is working.
  if (chatSessionRun) settleSessionRun();
  else {
    chatStreamLock = false;
    activeStreamMessage = null;
    updateStreamBar();
    updateChatSendEnabled();
  }
  mergeSessionSummary(detail);
  applySessionTranscript(detail);

  if (currentView !== "chat") showChatView();

  if (detail.status === "running" || detail.status === "queued") {
    syncSessionRunWithStatus({ status: detail.status, model: detail.model });
  }
  openChatSessionStream(id, detail.seq || 0);

  // Opening the session at its last message counts as having read it.
  if (detail.unseen) markChatSessionSeen(id);
  renderSessionList();
  renderSessionBadges();
  saveActiveChatSession();
}

async function closeChatSession() {
  const wasOpen = chatSessionId;
  closeChatSessionStream();
  chatSessionId = null;
  chatSessionRun = null;
  settleSessionRun();
  resetChatState();
  renderSessionList();
  renderSessionBadges();
  if (wasOpen) saveActiveChatSession();
}

function openChatSessionStream(id, from) {
  closeChatSessionStream();
  const es = new EventSource(`/api/chat/sessions/${encodeURIComponent(id)}/events?from=${Number(from) || 0}`);
  chatSessionStream = es;
  for (const name of ["snapshot", "chunk", "tool", "artifact", "artifact_screenshot_request", "artifact_eval_request", "done", "error"]) {
    es.addEventListener(name, (ev) => handleChatSessionStreamEvent(name, ev));
  }
  es.onerror = () => {
    // The browser reconnects on its own; only bail out if the session is gone.
    if (chatSessionStream === es && es.readyState === EventSource.CLOSED) {
      closeChatSessionStream();
      void loadChatSessions();
    }
  };
}

function closeChatSessionStream() {
  if (chatSessionStream) {
    chatSessionStream.close();
    chatSessionStream = null;
  }
}

// sessionLiveMessage returns the assistant message a live event belongs to.
function sessionLiveMessage() {
  const last = chatMessages[chatMessages.length - 1];
  if (last && last.role === "assistant" && last.streaming) return last;
  return null;
}

// ensureSessionLiveMessage makes sure there is an assistant message to stream
// into, which matters when the page is opened while the model is already busy.
function ensureSessionLiveMessage() {
  let msg = sessionLiveMessage();
  if (msg) return msg;
  const sum = chatSessions.get(chatSessionId);
  if (!sum || (sum.status !== "running" && sum.status !== "queued")) return null;
  msg = newAssistantMessage();
  msg.model = sum.model || ($("chat-model")?.value || "");
  msg.streamStartedAt = sum.last_active_at ? new Date(sum.last_active_at).getTime() : Date.now();
  chatMessages.push(msg);
  renderChatMessages();
  scrollChatToBottom(false);
  return msg;
}

function handleChatSessionStreamEvent(name, ev) {
  let parsed = {};
  try {
    parsed = JSON.parse(ev.data || "{}");
  } catch {
    return;
  }
  if (name === "snapshot") {
    const sum = parsed.session;
    if (sum) {
      mergeSessionSummary(sum);
      renderSessionList();
      renderSessionBadges();
    }
    return;
  }
  const msg = ensureSessionLiveMessage();
  if (!msg) return;
  if (!chatSessionRun) {
    chatSessionRun = { raw: msg.raw || "", turnStartedAt: msg.streamStartedAt || Date.now(), modelName: msg.model || "" };
  }
  try {
    applyChatStreamEvent(msg, name, parsed.data, chatSessionRun);
  } catch (e) {
    msg.streaming = false;
    msg.isError = true;
    if (!String(msg.content || "").trim()) {
      msg.content = t("chat.error_reply", { msg: e.message });
    }
    void toast(t("toast.error", { msg: e.message }), "error");
  }
  if (name === "done" || name === "error") {
    msg.streaming = false;
    chatSessionRunPending = false;
    settleSessionRun();
    // The server flags every finished reply as unread, because a stream staying
    // open says nothing about whether anyone read it. Only this browser can say
    // it was on screen, and only while the chat is actually the visible view:
    // leaving to the models list keeps the event source alive, so a blind
    // "I received it" would swallow the badge the user is waiting for.
    if (chatSessionId && currentView === "chat") markChatSessionSeen(chatSessionId);
  } else {
    flushChatRender();
  }
}

// ---------- sending ----------

function chatAttachForWire(a) {
  const out = { kind: a.kind || "image" };
  if (a.name) out.name = a.name;
  if (a.mimeType) out.mime_type = a.mimeType;
  if (a.text) out.text = a.text;
  if (a.data) out.data = a.data;
  return out;
}

// sendChatSessionMessage hands the turn to the server and returns immediately.
// Everything after this point arrives through the session event feed, so the
// page can be closed and reopened without losing the reply.
//
// opts.replaceLast asks the server to drop the reply that follows the last user
// turn, and opts.editLast to rewrite that user turn instead of appending a new
// one. Regenerate and edit-and-resend both need them, because a session's
// transcript is authoritative on the server rather than in chatMessages.
async function sendChatSessionMessage(text, attachments, opts = {}) {
  const id = chatSessionId;
  if (!id) return;
  const sum = chatSessions.get(id);
  // The panel is the source of truth for the config of this message, model
  // included: a session stores the configuration of its last input, so picking a
  // different model here carries over to the session and the next detached turn
  // runs on it.
  const sel = $("chat-model");
  const modelName = sel?.value || sum?.model || activeName || "";

  chatEditingMessageId = "";
  chatEditingDraft = "";

  if (opts.editLast) {
    // The caller already rewrote the user turn in place.
  } else {
    if (opts.replaceLast) {
      let keep = 0;
      for (let i = chatMessages.length - 1; i >= 0; i -= 1) {
        if (chatMessages[i].role === "user") {
          keep = i + 1;
          break;
        }
      }
      chatMessages.length = keep;
    }
    chatMessages.push({
      id: nanoid(),
      role: "user",
      content: text,
      attachments: (attachments || []).map((a) => ({ ...a })),
    });
  }

  const assistantMsg = newAssistantMessage();
  assistantMsg.model = modelName;
  assistantMsg.streamStartedAt = Date.now();
  chatMessages.push(assistantMsg);

  chatSessionRun = { raw: "", turnStartedAt: assistantMsg.streamStartedAt, modelName };
  chatSessionRunPending = true;
  chatStreamLock = true;
  activeStreamMessage = assistantMsg;
  updateStreamBar();
  updateChatSendEnabled();
  startStreamTicker(assistantMsg, chatSessionRun.turnStartedAt);
  renderChatMessages();
  scrollChatToBottom(true);

  const body = {
    content: text,
    attachments: (attachments || []).map(chatAttachForWire),
    settings: typeof getCurrentChatOptions === "function" ? getCurrentChatOptions() : undefined,
    // The model is part of the config of this message, so the session adopts it
    // too. That keeps a long run pinned to the model it was started with, and
    // still lets you switch models mid-session.
    model: modelName || "",
  };
  if (opts.replaceLast) body.replace_last = true;
  if (opts.editLast) body.edit_last = true;

  try {
    await api(`/api/chat/sessions/${encodeURIComponent(id)}/messages`, { method: "POST", body });
    saveActiveChatSession();
  } catch (e) {
    assistantMsg.streaming = false;
    assistantMsg.isError = true;
    assistantMsg.content = t("chat.error_reply", { msg: e.message });
    chatSessionRun = null;
    settleSessionRun();
    void toast(t("toast.error", { msg: e.message }), "error");
  }
}

// regenerateChatSessionReply asks the server to drop the current reply to
// userMsg and answer again. The server keeps the authoritative transcript, so
// the local copy is trimmed here only to keep the view honest.
async function regenerateChatSessionReply(userMsg) {
  if (!userMsg) return;
  await sendChatSessionMessage(userMsg.content, userMsg.attachments, { replaceLast: true, editLast: true });
}

// editChatSessionUserMessage rewrites the last user turn and resends it.
async function editChatSessionUserMessage(userMsg, text, attachments) {
  if (!userMsg) return;
  userMsg.content = text;
  userMsg.attachments = (attachments || []).map((a) => ({ ...a }));
  await sendChatSessionMessage(text, attachments, { replaceLast: true, editLast: true });
}

// ---------- session actions ----------

async function newChatSession() {
  if (!chatSessionsEnabled) {
    // The server refuses with 403 anyway, but the button should never be
    // clickable in the first place.
    toast(t("chat.sessions_disabled"), "error");
    return;
  }
  const modelName = $("chat-model")?.value || activeName || "";
  if (!modelName) {
    toast(t("chat.no_models"), "error");
    return;
  }
  let detail;
  try {
    detail = await api("/api/chat/sessions", {
      method: "POST",
      body: {
        model: modelName,
        settings: typeof getCurrentChatOptions === "function" ? getCurrentChatOptions() : undefined,
      },
    });
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
    return;
  }
  mergeSessionSummary(detail);
  await openChatSession(detail.id);
}

// renameChatSession replaces the derived title, which otherwise comes from the
// first user message and cannot be changed after the fact. Double-clicking the
// title in the row is the only entry point: renaming is rare enough that it
// should not cost a permanent button.
async function renameChatSession(id) {
  if (!id || !chatSessions.has(id)) return;
  const current = chatSessions.get(id);
  // window.prompt matches how the rest of the app asks for a single free-text
  // value, and an empty answer is its own cancel.
  const next = (window.prompt(t("chat.session_rename_title"), current.title || "") || "").trim();
  // Confirming without changing anything would burn a pointless request.
  if (!next || next === (current.title || "")) return;
  try {
    await api(`/api/chat/sessions/${encodeURIComponent(id)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ title: next }),
    });
    const sum = { ...current, title: next };
    chatSessions.set(id, sum);
    renderSessionList();
    toast(t("chat.session_renamed"), "success");
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
  }
}

async function deleteChatSession(id) {
  try {
    await api(`/api/chat/sessions/${encodeURIComponent(id)}`, { method: "DELETE" });
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
    return;
  }
  chatSessions.delete(id);
  if (chatSessionId === id) {
    chatSessionId = null;
    chatSessionRun = null;
    settleSessionRun();
    resetChatState();
    saveActiveChatSession();
  }
  renderSessionList();
  renderSessionBadges();
}

// clearAllChatSessions wipes every session of every model. It asks first,
// because there is no undo and no per-session backup.
async function clearAllChatSessions() {
  const n = chatSessions.size;
  if (!n) return;
  const { ok } = await askConfirm({
    title: t("chat.session_clear_all_title"),
    text: t("chat.session_clear_all_text", { n }),
    okText: t("chat.session_clear_all_ok"),
    okClass: "danger",
  });
  if (!ok) return;
  try {
    await api("/api/chat/sessions", { method: "DELETE" });
  } catch (e) {
    toast(t("toast.error", { msg: e.message }), "error");
    return;
  }
  chatSessions.clear();
  if (chatSessionId) {
    chatSessionId = null;
    chatSessionRun = null;
    chatSessionRunPending = false;
    settleSessionRun();
    closeChatSessionStream();
    resetChatState();
    saveActiveChatSession();
  }
  renderSessionList();
  renderSessionBadges();
  toast(t("chat.session_clear_all_done", { n }), "success");
}

async function cancelChatSession(id) {
  try {
    await api(`/api/chat/sessions/${encodeURIComponent(id)}/cancel`, { method: "POST" });
  } catch {
    // A session that finished on its own is not worth complaining about.
  }
  await loadChatSessions();
}

async function markChatSessionSeen(id) {
  const sum = chatSessions.get(id);
  if (sum) sum.unseen = false;
  renderSessionList();
  renderSessionBadges();
  try {
    await api(`/api/chat/sessions/${encodeURIComponent(id)}/seen`, { method: "POST" });
  } catch {
    // The badge clears again on the next reload if this failed.
  }
}

// ---------- badge feed ----------

function ensureChatSessionFeed() {
  if (chatSessionFeed || typeof EventSource === "undefined") return;
  const es = new EventSource("/api/chat/events");
  chatSessionFeed = es;
  es.addEventListener("snapshot", (ev) => {
    chatSessionFeedRetry = 0;
    try {
      const data = JSON.parse(ev.data || "{}");
      chatSessions = new Map((data.sessions || []).map((s) => [s.id, s]));
    } catch {
      return;
    }
    renderSessionList();
    renderSessionBadges();
  });
  es.addEventListener("session", (ev) => {
    chatSessionFeedRetry = 0;
    let data = null;
    try {
      data = JSON.parse(ev.data || "{}");
    } catch {
      return;
    }
    if (!data) return;
    if (data.kind === "remove") {
      const id = data.id || data.session?.id || "";
      chatSessions.delete(id);
      if (id && id === chatSessionId) {
        chatSessionId = null;
        chatSessionRun = null;
        settleSessionRun();
        resetChatState();
      }
    } else if (data.kind === "update" && data.session) {
      mergeSessionSummary(data.session);
    }
    renderSessionList();
    renderSessionBadges();
    if (currentView === "chat") flushChatRender();
  });
  es.onerror = () => {
    // Back off a little between reconnects so a server restart does not turn
    // into a tight retry loop, but never give up for good.
    chatSessionFeedRetry = Math.min(chatSessionFeedRetry + 1, 6);
    if (chatSessionFeedRetry >= 6) {
      chatSessionFeed.close();
      chatSessionFeed = null;
      setTimeout(ensureChatSessionFeed, 10000);
    }
  };
}

// ---------- cross-tab sync ----------

// The settings page can turn the feature off or wipe every session while this
// page is open. There is no server event for either, so the two sides talk over
// a BroadcastChannel, with a localStorage ping as the fallback for browsers
// without one. Either way the reaction is the same: re-read from the server.
const CHAT_SESSIONS_SYNC_KEY = "ollama_manager_chat_sessions_sync";

// broadcastChatSessionsSetting tells the other tabs that the settings or the
// stored sessions changed. It is a hint, never the source of truth: every tab
// re-fetches /api/chat/sessions when it hears it.
function broadcastChatSessionsSetting(enabled) {
  const payload = { at: Date.now(), enabled: !!enabled };
  try {
    if (typeof BroadcastChannel !== "undefined") {
      const ch = new BroadcastChannel(CHAT_SESSIONS_SYNC_KEY);
      ch.postMessage(payload);
      // The channel is only needed for this one message; closing it right away
      // keeps it from pinning a channel open for the life of the page.
      setTimeout(() => { try { ch.close(); } catch (_) { /* already closed */ } }, 0);
      return;
    }
  } catch (_) { /* fall through to localStorage */ }
  try { localStorage.setItem(CHAT_SESSIONS_SYNC_KEY, JSON.stringify(payload)); } catch (_) { /* private mode */ }
}

function onChatSessionsSyncMessage() {
  // Debounced: the settings page fires once per change, but a "clear all" plus
  // a toggle could land back to back.
  clearTimeout(onChatSessionsSyncMessage._timer);
  onChatSessionsSyncMessage._timer = setTimeout(() => {
    void loadChatSessions();
  }, 150);
}

if (typeof BroadcastChannel !== "undefined") {
  try {
    const ch = new BroadcastChannel(CHAT_SESSIONS_SYNC_KEY);
    ch.addEventListener("message", onChatSessionsSyncMessage);
  } catch (_) { /* localStorage below still covers it */ }
}

// The storage event only fires in *other* tabs, which is exactly what is wanted:
// the tab that made the change already updated itself.
if (typeof window !== "undefined") {
  window.addEventListener("storage", (ev) => {
    if (ev.key === CHAT_SESSIONS_SYNC_KEY) onChatSessionsSyncMessage();
  });
}

// ---------- wiring ----------

$("chat-session-new-btn")?.addEventListener("click", () => {
  void newChatSession();
});

$("chat-sessions-clear-all")?.addEventListener("click", () => {
  void clearAllChatSessions();
});

$("chat-sessions-list")?.addEventListener("dblclick", (ev) => {
  const title = ev.target.closest("[data-session-title]");
  if (!title) return;
  void renameChatSession(title.getAttribute("data-session-title"));
});

$("chat-sessions-list")?.addEventListener("click", (ev) => {
  const del = ev.target.closest("[data-session-del]");
  if (del) {
    ev.stopPropagation();
    void deleteChatSession(del.getAttribute("data-session-del"));
    return;
  }
  const stop = ev.target.closest("[data-session-stop]");
  if (stop) {
    ev.stopPropagation();
    void cancelChatSession(stop.getAttribute("data-session-stop"));
    return;
  }
  if (ev.target.closest("[data-session-quick]")) {
    void closeChatSession();
    return;
  }
  const open = ev.target.closest("[data-session-open]");
  if (open) {
    void openChatSession(open.getAttribute("data-session-open"));
  }
});

if (typeof document !== "undefined") {
  document.addEventListener("DOMContentLoaded", async () => {
    ensureChatSessionFeed();
    await loadChatSessions();
    // A reload while a session was open: pull the authoritative transcript
    // back from the server instead of the sessionStorage snapshot.
    const pending = pendingChatSessionOpenId;
    if (pending && chatSessions.has(pending)) {
      pendingChatSessionOpenId = null;
      await openChatSession(pending);
    } else if (pending) {
      pendingChatSessionOpenId = null;
      renderSessionList();
    }
  });
}