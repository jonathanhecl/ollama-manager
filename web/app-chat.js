"use strict";

// ---------- chat ----------
function nanoid() {
  return Math.random().toString(36).slice(2, 10);
}

function modelByName(name) {
  return models.find((m) => m.name === name) || null;
}

function modelCaps(name) {
  const m = modelByName(name);
  const out = new Set();
  for (const c of (m?.capabilities || [])) out.add(String(c).toLowerCase());
  return out;
}

function isImageGenerationOnlyCaps(caps) {
  return caps.has("image") && !caps.has("vision") && !caps.has("completion");
}

function formatCapabilityLabel(raw) {
  const k = String(raw || "").toLowerCase().trim();
  if (!k) return "";
  const i18nKey = `chat.cap.${k.replace(/[^a-z0-9]+/g, "_")}`;
  const tr = t(i18nKey);
  if (tr !== i18nKey) return tr;
  return k.charAt(0).toUpperCase() + k.slice(1);
}

const CAPABILITY_ORDER = ["completion", "tools", "thinking", "vision", "audio"];

function capabilityOrderKey(cap) {
  const k = String(cap || "").toLowerCase().trim();
  const idx = CAPABILITY_ORDER.indexOf(k);
  return idx === -1 ? CAPABILITY_ORDER.length : idx;
}

function renderCapabilityPills(caps) {
  return (caps || [])
    .slice()
    .sort((a, b) => capabilityOrderKey(a) - capabilityOrderKey(b))
    .map((c) => {
      const raw = String(c);
      const slug = raw.toLowerCase().replace(/[^a-z0-9]+/g, "-");
      const label = formatCapabilityLabel(raw);
      return `<span class="pill chat-cap-pill" data-cap="${escapeHtml(slug)}">${escapeHtml(label)}</span>`;
    })
    .join("");
}

function updateMarquee(trackEl, textEl) {
  if (!trackEl || !textEl) return;
  textEl.classList.remove("is-bouncing");
  textEl.style.removeProperty("--marquee-dist");
  textEl.style.removeProperty("--marquee-dur");
  textEl.style.transform = "";

  requestAnimationFrame(() => {
    if (!trackEl || !textEl) return;
    const trackWidth = trackEl.clientWidth;
    const textWidth = textEl.scrollWidth;
    const overflow = textWidth - trackWidth;

    if (overflow > 2) {
      const dist = overflow + 8;
      const duration = Math.max(4, Math.min(16, dist / 20));
      textEl.style.setProperty("--marquee-dist", `-${dist}px`);
      textEl.style.setProperty("--marquee-dur", `${duration}s`);
      textEl.classList.add("is-bouncing");
    } else {
      textEl.classList.remove("is-bouncing");
    }
  });
}

function updateChatModelDisplay() {
  const sel = $("chat-model");
  const display = $("chat-model-display");
  const track = $("chat-model-marquee-track");
  if (sel && display) {
    display.textContent = sel.selectedOptions?.[0]?.textContent || sel.value || "";
    display.title = sel.value || "";
    updateMarquee(track, display);
  }
  const nameVal = $("chat-model-name-value");
  const nameTrack = $("chat-model-name-track");
  if (nameVal && nameTrack) {
    updateMarquee(nameTrack, nameVal);
  }
}

function syncChatModelOptions() {
  const sel = $("chat-model");
  if (!sel) return;
  const previous = sel.value;
  // Filter out archived models so they do not clutter chat view select dropdown
  const activeModels = models.filter(m => !m.archived);
  const sorted = applySort(activeModels);
  sel.innerHTML = sorted.map((m) => `<option value="${escapeHtml(m.name)}">${escapeHtml(m.name)}</option>`).join("");
  if (!sorted.length) return;
  if (previous && sorted.some((m) => m.name === previous)) {
    sel.value = previous;
  } else if (activeName && sorted.some((m) => m.name === activeName)) {
    sel.value = activeName;
  } else {
    const loaded = sorted.find((m) => m.loaded);
    sel.value = (loaded || sorted[0]).name;
  }
  updateChatModelLoadDot();
  updateChatModelDisplay();
}

function updateChatCapabilityUI() {
  const model = $("chat-model").value;
  if ($("chat-model-name-value")) {
    $("chat-model-name-value").textContent = model;
    $("chat-model-name-value").title = model;
  }
  updateChatModelDisplay();
  const caps = modelCaps(model);
  const isImageModel = isImageGenerationOnlyCaps(caps);
  const canVision = caps.has("vision");
  const canAudio = caps.has("audio");
  const canThink = caps.has("thinking");
  const canTools = caps.has("tools");
  $("chat-image-btn").hidden = !canVision;
  $("chat-audio-btn").hidden = !canAudio;
  $("chat-record-btn").hidden = !canAudio;
  $("chat-think-wrap").hidden = !canThink;
  $("chat-web-tools-wrap").hidden = !canTools;
  $("chat-artifacts-wrap").hidden = !canTools;

  const artBtn = $("chat-model-artifacts-btn");
  if (artBtn) artBtn.hidden = true;

  void refreshModelArtifactCount();

  const imgOpts = $("chat-image-options-wrap");
  if (imgOpts) imgOpts.hidden = !isImageModel;
  const sysField = $("chat-system-field");
  if (sysField) sysField.hidden = isImageModel;
  const tempField = $("chat-temperature-field");
  if (tempField) tempField.hidden = isImageModel;
  const topKField = $("chat-top-k-field");
  if (topKField) topKField.hidden = isImageModel;
  const topPField = $("chat-top-p-field");
  if (topPField) topPField.hidden = isImageModel;
  const numCtxField = $("chat-num-ctx-field");
  if (numCtxField) numCtxField.hidden = isImageModel;

  const inputEl = $("chat-input");
  if (inputEl && !chatIsRecording) {
    inputEl.placeholder = isImageModel
      ? (t("chat.image_input_placeholder") || "Describe the image you want to generate…")
      : (t("chat.input_placeholder") || "Write your message…");
  }

  if (!canAudio && chatIsRecording) {
    stopAudioRecording(true);
  }


  const m = modelByName(model);

  // Update Model Performance Stats (Record Tok/s and Min Cold Load)
  const toksValEl = $("chat-model-stat-toks-val");
  const toksCard = $("chat-model-stat-toks");
  const loadValEl = $("chat-model-stat-load-val");
  const loadCard = $("chat-model-stat-load");

  if (toksValEl && toksCard) {
    const tps = Number(m?.record_tokens_per_sec) || 0;
    if (tps > 0) {
      const col = (typeof getToksRecordColor === "function") ? getToksRecordColor(tps) : "";
      const colStyle = col ? `style="color:${col}"` : "";
      toksValEl.innerHTML = `<span ${colStyle}>${tps.toFixed(1)}</span> <span class="stat-unit">tok/s</span>`;
      toksCard.title = m.record_tokens_per_sec_at
        ? t("detail.record_at", { date: fmtDateTimeFull(m.record_tokens_per_sec_at) })
        : t("detail.record_tokens");
    } else {
      toksValEl.innerHTML = `<span style="color:var(--muted)">—</span>`;
      toksCard.title = t("detail.record_tokens");
    }
  }

  if (loadValEl && loadCard) {
    const isExt = !!(m && m.is_external);
    const coldMs = Number(m?.min_cold_load_ms) || 0;
    if (!isExt && coldMs > 0) {
      loadValEl.textContent = fmtColdLoad(coldMs);
      loadCard.title = m.min_cold_load_at
        ? t("detail.min_load_at", { date: fmtDateTimeFull(m.min_cold_load_at) })
        : t("detail.min_cold_load");
    } else {
      loadValEl.innerHTML = `<span style="color:var(--muted)">—</span>`;
      loadCard.title = isExt ? t("detail.external_model") : t("detail.min_cold_load");
    }
  }

  const capsHtml = renderCapabilityPills(m?.capabilities);
  const capBlock = $("chat-cap-block");
  const capHost = $("chat-cap-flags");
  if (capBlock && capHost) {
    if (!capsHtml) {
      capBlock.hidden = true;
      capHost.innerHTML = "";
    } else {
      capBlock.hidden = false;
      capHost.innerHTML = capsHtml;
    }
  }
  updateChatModelLoadDot();
  if (typeof adjustChatSystemPromptHeight === "function") {
    adjustChatSystemPromptHeight();
  }
}

function updateChatContextMeter() {
  const meter = $("chat-context-meter");
  const selectedName = $("chat-model")?.value || "";
  const selected = modelByName(selectedName);
  const maxCtx = Number(selected?.context_length) || 0;
  const ring = $("chat-context-ring");
  if (!maxCtx) {
    if (meter) meter.textContent = "—";
    if (ring) {
      ring.style.setProperty("--ctx-pct", "0%");
      ring.title = "";
    }
    return;
  }
  const used = Math.max(0, Number(chatLastUsedTokens) || 0);
  const pct = Math.min(999, Math.round((used / maxCtx) * 100));
  const ringPct = `${Math.max(0, Math.min(100, (used / maxCtx) * 100))}%`;
  if (meter) meter.textContent = `${fmtCtx(used)} / ${fmtCtx(maxCtx)} (${pct}%)`;
  if (ring) {
    ring.style.setProperty("--ctx-pct", ringPct);
    ring.title = `${fmtCtx(used)} / ${fmtCtx(maxCtx)} (${pct}%)`;
  }
}

function showModelsView() {
  const chatView = $("chat-view");
  const modelsView = $("models-view");
  chatView?.classList.remove("chat-options-open");
  syncChatPanels(chatView);
  stopSpeechPlayback();
  if (typeof resetChatState === "function") {
    resetChatState();
  }
  currentView = "models";
  if (modelsView) modelsView.hidden = false;
  if (chatView) chatView.hidden = true;
  $("tests-view") && ($("tests-view").hidden = true);
  $("test-editor-view") && ($("test-editor-view").hidden = true);
  $("opencode-view") && ($("opencode-view").hidden = true);
  $("analytics-view") && ($("analytics-view").hidden = true);
  $("settings-view") && ($("settings-view").hidden = true);
  $("modelfile-view") && ($("modelfile-view").hidden = true);
  $("hf-view") && ($("hf-view").hidden = true);
  $("chat-btn")?.classList.remove("active");
  $("settings-btn")?.classList.remove("active");
  $("hf-topbar-btn")?.classList.remove("active");
  if (window.location.pathname !== "/") {
    history.pushState(null, "", "/");
  }
  renderTable();
  void refreshModels();
}

function resetChatState() {
  stopAudioRecording(true);
  stopSpeechPlayback();
  if (chatAbortController) {
    try { chatAbortController.abort(); } catch (_) { }
    chatAbortController = null;
  }
  chatStreamLock = false;
  chatMessages = [];
  chatAttachments = [];
  chatPendingQueue = [];
  chatLastUsedTokens = 0;
  chatEditingMessageId = "";
  chatEditingDraft = "";
  chatEditingAttachments = [];
  chatDndDepth = 0;
  stopThinkTicker();
  updateStreamBar();
  closeImagePreview();
  hideArtifactPanel();
  activeArtifactTimestamp = null;
  activeArtifactName = null;
  activeArtifactUrl = null;
  updateArtifactResourceBtn();
  updateChatContextMeter();
  if ($("chat-dropzone")) $("chat-dropzone").hidden = true;
  if ($("chat-attachments")) {
    $("chat-attachments").hidden = true;
    $("chat-attachments").innerHTML = "";
  }
  renderChatQueue();
  if ($("chat-messages")) $("chat-messages").innerHTML = `<div class="chat-empty muted">${escapeHtml(t("chat.empty"))}</div>`;
  if ($("chat-input")) $("chat-input").value = "";
  if (typeof updateChatSendEnabled === "function") {
    updateChatSendEnabled();
  }
  try {
    sessionStorage.removeItem("ollama_manager_active_chat_session");
  } catch (_) { }
}

const CHAT_SESSION_STORAGE_KEY = "ollama_manager_active_chat_session";

function saveActiveChatSession() {
  try {
    if (typeof chatMessages === "undefined") return;
    const model = $("chat-model")?.value || "";
    const input = $("chat-input")?.value || "";
    const hasData = (chatMessages && chatMessages.length > 0) || (input && input.trim()) || (chatAttachments && chatAttachments.length > 0);
    if (!hasData) {
      sessionStorage.removeItem(CHAT_SESSION_STORAGE_KEY);
      return;
    }
    const sessionData = {
      model,
      messages: chatMessages,
      input,
      attachments: chatAttachments,
      activeArtifactTimestamp,
      activeArtifactName,
      activeArtifactUrl,
      chatLastUsedTokens,
      savedAt: Date.now(),
    };
    sessionStorage.setItem(CHAT_SESSION_STORAGE_KEY, JSON.stringify(sessionData));
  } catch (_) { }
}

function restoreActiveChatSession() {
  try {
    const raw = sessionStorage.getItem(CHAT_SESSION_STORAGE_KEY);
    if (!raw) return false;
    const data = JSON.parse(raw);
    if (!data || ((!data.messages || !data.messages.length) && !data.input && (!data.attachments || !data.attachments.length))) {
      sessionStorage.removeItem(CHAT_SESSION_STORAGE_KEY);
      return false;
    }
    if (data.model && $("chat-model")) {
      $("chat-model").value = data.model;
    }
    if (Array.isArray(data.messages) && data.messages.length) {
      chatMessages = data.messages;
    }
    if (data.input && $("chat-input")) {
      $("chat-input").value = data.input;
    }
    if (Array.isArray(data.attachments)) {
      chatAttachments = data.attachments;
    }
    if (data.activeArtifactTimestamp) activeArtifactTimestamp = data.activeArtifactTimestamp;
    if (data.activeArtifactName) activeArtifactName = data.activeArtifactName;
    if (data.activeArtifactUrl) activeArtifactUrl = data.activeArtifactUrl;
    if (data.chatLastUsedTokens) chatLastUsedTokens = data.chatLastUsedTokens;

    if (typeof renderChatMessages === "function") renderChatMessages();
    if (typeof renderAttachments === "function") renderAttachments();
    if (typeof updateArtifactResourceBtn === "function") updateArtifactResourceBtn();
    if (typeof updateChatContextMeter === "function") updateChatContextMeter();
    if (typeof updateChatSendEnabled === "function") updateChatSendEnabled();
    return true;
  } catch (_) {
    return false;
  }
}

function showChatView() {
  const chatView = $("chat-view");
  const modelsView = $("models-view");
  if (!chatView || !modelsView) {
    toast(t("toast.error", { msg: "chat UI is not available; refresh the page" }), "error");
    return;
  }
  if (currentView !== "chat" && !chatMessages.length && !$("chat-input")?.value?.trim()) {
    resetChatState();
  }
  currentView = "chat";
  chatView.classList.remove("chat-options-open");
  modelsView.hidden = true;
  $("tests-view") && ($("tests-view").hidden = true);
  $("test-editor-view") && ($("test-editor-view").hidden = true);
  $("opencode-view") && ($("opencode-view").hidden = true);
  $("analytics-view") && ($("analytics-view").hidden = true);
  $("settings-view") && ($("settings-view").hidden = true);
  $("modelfile-view") && ($("modelfile-view").hidden = true);
  $("hf-view") && ($("hf-view").hidden = true);
  $("settings-btn")?.classList.remove("active");
  $("hf-topbar-btn")?.classList.remove("active");
  chatView.hidden = false;
  syncChatPanels(chatView);
  $("chat-btn")?.classList.add("active");
  if ($("detail-panel") && !$("detail-panel").hidden) {
    $("detail-panel").hidden = true;
    activeName = null;
    document.querySelectorAll("tbody tr.row.active").forEach((tr) => tr.classList.remove("active"));
  }
  if (window.location.pathname.startsWith("/settings") || window.location.pathname.startsWith("/hf") || window.location.pathname.startsWith("/modelfile") || window.location.pathname.startsWith("/tests") || window.location.pathname.startsWith("/analytics")) {
    history.pushState(null, "", "/");
  }
  syncChatModelOptions();
  updateChatCapabilityUI();
  updateChatContextMeter();
  updateChatSendEnabled();
  void applyChatDefaultsForModel($("chat-model").value);
  setTimeout(() => $("chat-input")?.focus(), 20);
}

$("chat-input")?.addEventListener("input", () => {
  saveActiveChatSession();
});

if (typeof ResizeObserver !== "undefined") {
  const chatMarqueeRO = new ResizeObserver(() => {
    if (typeof updateChatModelDisplay === "function") {
      updateChatModelDisplay();
    }
  });
  const t1 = $("chat-model-marquee-track");
  if (t1) chatMarqueeRO.observe(t1);
  const t2 = $("chat-model-name-track");
  if (t2) chatMarqueeRO.observe(t2);
}
window.addEventListener("resize", () => {
  if (typeof currentView !== "undefined" && currentView === "chat" && typeof updateChatModelDisplay === "function") {
    updateChatModelDisplay();
  }
});

