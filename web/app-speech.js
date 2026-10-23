"use strict";

// ---------- speech (text-to-speech) ----------
// Ollama synthesizes audio at POST /v1/audio/speech with a model whose
// capabilities include "speech". The chat can hand every message you send to
// that model and show the result in a player right under the bubble, so what
// you wrote can be listened to without leaving the conversation.

const SPEECH_MODEL_KEY = "ollama_speech_model";
const SPEECH_AUTO_KEY = "ollama_speech_auto";
const SPEECH_INPUT_LIMIT = 4096;

function speechModels() {
  const list = typeof models !== "undefined" && Array.isArray(models) ? models : [];
  return list.filter((m) => !m.archived && (m.capabilities || []).some((c) => String(c).toLowerCase() === "speech"));
}

function selectedSpeechModel() {
  const available = speechModels();
  const sel = $("chat-speech-model");
  const picked = (sel && sel.value) || localStorage.getItem(SPEECH_MODEL_KEY) || "";
  if (picked && available.some((m) => m.name === picked)) return picked;
  return available[0]?.name || "";
}

function speechAutoEnabled() {
  const el = $("chat-speech-auto");
  return Boolean(el && el.checked);
}

// updateSpeechUI fills the voice-model selector from the installed models and
// hides the whole block when nothing can speak. The preference lives in
// localStorage so it survives a reload along with the rest of the chat options.
function updateSpeechUI() {
  const available = speechModels();
  const wrap = $("chat-speech-wrap");
  const modelField = $("chat-speech-model-field");
  const sel = $("chat-speech-model");
  const auto = $("chat-speech-auto");
  if (!wrap || !sel || !auto) return;

  wrap.hidden = available.length === 0;
  const previous = sel.value || localStorage.getItem(SPEECH_MODEL_KEY) || "";
  sel.innerHTML = available
    .map((m) => `<option value="${escapeHtml(m.name)}">${escapeHtml(m.name)}</option>`)
    .join("");
  if (previous && available.some((m) => m.name === previous)) {
    sel.value = previous;
  }
  auto.checked = localStorage.getItem(SPEECH_AUTO_KEY) === "1";
  if (modelField) modelField.hidden = available.length === 0 || !auto.checked;
}

function bindSpeechUI() {
  const auto = $("chat-speech-auto");
  const sel = $("chat-speech-model");
  updateSpeechUI();
  if (auto) {
    auto.addEventListener("change", () => {
      localStorage.setItem(SPEECH_AUTO_KEY, auto.checked ? "1" : "0");
      const modelField = $("chat-speech-model-field");
      if (modelField) modelField.hidden = !auto.checked;
    });
  }
  if (sel) {
    sel.addEventListener("change", () => {
      localStorage.setItem(SPEECH_MODEL_KEY, sel.value);
    });
  }
}

// generateSpeechURL asks the backend for the audio of text and returns a blob
// URL the player can use. The text is capped to the engine's input limit, cut
// by code point so a surrogate pair is never split.
async function generateSpeechURL(text, model) {
  const chars = Array.from(String(text || "").trim());
  const input = chars.slice(0, SPEECH_INPUT_LIMIT).join("");
  if (!input) return "";
  const blob = await api("/api/speech", { method: "POST", body: { model, input } }, "blob");
  return URL.createObjectURL(blob);
}

// attachSpeechToMessage generates the audio for a user message in the
// background. The bubble repaints with a pending line and then with the player.
function attachSpeechToMessage(msg, text) {
  if (!msg || msg.role !== "user" || msg._speech) return;
  if (!speechAutoEnabled()) return;
  const model = selectedSpeechModel();
  const input = String(text || "").trim();
  if (!model || !input) return;

  msg._speech = { status: "pending" };
  renderChatMessages();
  generateSpeechURL(input, model)
    .then((url) => {
      msg._speech = { status: "ready", url };
      renderChatMessages();
    })
    .catch((err) => {
      msg._speech = { status: "error" };
      renderChatMessages();
      toast(`${t("chat.speech_failed")}: ${err.message}`, "error");
    });
}

// attachSpeechToLastSentMessage finds the bubble the send path just pushed. The
// composer was cleared before the call, so the text is what identifies it.
function attachSpeechToLastSentMessage(text) {
  if (!speechAutoEnabled()) return;
  for (let i = chatMessages.length - 1; i >= 0; i--) {
    const m = chatMessages[i];
    if (m.role !== "user") continue;
    if ((m.content || "") !== text) continue;
    attachSpeechToMessage(m, text);
    return;
  }
}

function renderMessageSpeechHTML(m) {
  const sp = m && m._speech;
  if (!sp) return "";
  if (sp.status === "pending") {
    return `<div class="chat-speech-block chat-speech-pending" aria-live="polite">${escapeHtml(t("chat.speech_pending"))}</div>`;
  }
  if (sp.status === "error") {
    return `<div class="chat-speech-block chat-speech-error">${escapeHtml(t("chat.speech_failed"))}</div>`;
  }
  return `<div class="chat-file-item chat-file-item-audio chat-speech-block">
    <audio class="chat-audio-player" controls preload="metadata" src="${escapeHtml(sp.url)}" aria-label="${escapeHtml(t("chat.speech_player"))}"></audio>
    <span class="chat-file-name mono">${escapeHtml(t("chat.speech_player"))}</span>
  </div>`;
}

document.addEventListener("DOMContentLoaded", bindSpeechUI);
