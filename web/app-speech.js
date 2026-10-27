"use strict";

// ---------- speech (text-to-speech) ----------
// Ollama synthesizes audio at POST /v1/audio/speech with a model whose
// capabilities include "speech". A speech-only model answers with audio, so the
// player lives in the assistant bubble; for a chat model the "Speech" switch
// voices the model's final answer in that same bubble when the turn ends.
//
// Voices come from /api/show (the same list `ollama show` prints). Ollama omits
// the field when the checkpoint only has its own voice, which the API calls
// "default", so the server always adds it first.

const SPEECH_MODEL_KEY = "ollama_speech_model";
const SPEECH_AUTO_KEY = "ollama_speech_auto";
const SPEECH_VOICE_KEY = "ollama_speech_voice:";
const SPEECH_INPUT_LIMIT = 4096;

// speechVoiceCache maps a model name to the voices the engine reports for it.
// The list only changes when the model is reinstalled, so it is never
// invalidated during a session.
const speechVoiceCache = new Map();

// speechVoiceReported counts the voices the engine listed for each model.
// Ollama only reports them from 0.40.11 on, so the number separates "the
// checkpoint has a single voice" (1) from "the engine cannot name them" (0).
const speechVoiceReported = new Map();

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

// effectiveSpeechModel is the model that actually synthesizes: the chat model
// itself when it only speaks, otherwise the one picked in the Speech block.
function effectiveSpeechModel() {
  const chatModel = $("chat-model")?.value || "";
  if (isSpeechOnlyModel(chatModel)) return chatModel;
  return selectedSpeechModel();
}

function speechAutoEnabled() {
  const el = $("chat-speech-auto");
  return Boolean(el && el.checked);
}

// isSpeechOnlyModel reports a model that synthesizes audio and cannot answer
// chat: picking one turns the chat into a voice composer instead of a
// conversation.
function isSpeechOnlyModel(name) {
  if (!name || typeof models === "undefined" || !Array.isArray(models)) return false;
  const m = models.find((x) => x && x.name === name);
  if (!m) return false;
  const caps = new Set((m.capabilities || []).map((c) => String(c).toLowerCase()));
  return caps.has("speech") && !caps.has("completion");
}

function selectedSpeechVoice(model) {
  const name = model || effectiveSpeechModel();
  if (!name) return "";
  let stored = "";
  try {
    stored = localStorage.getItem(SPEECH_VOICE_KEY + name) || "";
  } catch (_) { }
  const cached = speechVoiceCache.get(name);
  if (stored && cached && cached.length) {
    // A reinstalled model can drop the voice the user picked. Fall back to the
    // first one the engine reports instead of failing every request with an
    // unknown voice.
    const match = cached.find((v) => v.toLowerCase() === stored.toLowerCase());
    return match || cached[0];
  }
  if (cached && cached.length) return cached[0];
  return stored || "default";
}

// loadSpeechVoices asks the backend for the voices of one model. The backend
// reads them from Ollama's /api/show, where they are part of the model itself.
// It also reports how many of them the engine listed: Ollama leaves the field
// out before 0.40.11, so an empty list cannot be told apart from a checkpoint
// that only ships its own voice.
async function loadSpeechVoices(model) {
  if (!model) return [];
  if (speechVoiceCache.has(model)) return speechVoiceCache.get(model);
  const data = await api(`/api/speech/voices?model=${encodeURIComponent(model)}`);
  const list = Array.isArray(data?.voices) ? data.voices.map((v) => String(v)).filter(Boolean) : [];
  const voices = list.length ? list : ["default"];
  speechVoiceReported.set(model, Number(data?.reported) || 0);
  speechVoiceCache.set(model, voices);
  return voices;
}

function speechVoiceFieldVisible() {
  const wrap = $("chat-speech-wrap");
  if (!wrap || wrap.hidden) return false;
  return speechAutoEnabled() || isSpeechOnlyModel($("chat-model")?.value);
}

// renderSpeechVoiceOptions paints the voice selector for the given model. A
// single voice (the checkpoint's own) stays visible but disabled, and the hint
// under it says why: a selector with one option and no explanation reads like a
// bug, when in fact most checkpoints ship exactly one voice.
function renderSpeechVoiceOptions(model) {
  const field = $("chat-speech-voice-field");
  const sel = $("chat-speech-voice");
  const hint = $("chat-speech-voice-hint");
  if (!field || !sel) return;
  const voices = speechVoiceCache.get(model) || [];
  if (!model || !voices.length) {
    field.hidden = true;
    sel.innerHTML = "";
    if (hint) {
      hint.textContent = "";
      hint.hidden = true;
    }
    return;
  }
  const current = selectedSpeechVoice(model);
  sel.innerHTML = voices
    .map((v) => `<option value="${escapeHtml(v)}">${escapeHtml(v)}</option>`)
    .join("");
  sel.value = voices.includes(current) ? current : voices[0];
  sel.disabled = voices.length <= 1;
  if (hint) {
    let text = "";
    if (voices.length <= 1) {
      // Ollama leaves "voices" out of /api/show before 0.40.11, so an empty
      // list means the engine cannot name them instead of "one voice only".
      text = (speechVoiceReported.get(model) || 0) > 0
        ? t("chat.speech_voice_single")
        : t("chat.speech_voice_unknown");
    }
    hint.textContent = text;
    hint.hidden = !text || !speechVoiceFieldVisible();
  }
  field.hidden = !speechVoiceFieldVisible();
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

  const voiceModel = effectiveSpeechModel();
  renderSpeechVoiceOptions(voiceModel);
  if (voiceModel && !speechVoiceCache.has(voiceModel)) {
    loadSpeechVoices(voiceModel)
      .then(() => {
        if (effectiveSpeechModel() === voiceModel) renderSpeechVoiceOptions(voiceModel);
      })
      .catch(() => { });
  }
}

function bindSpeechUI() {
  const auto = $("chat-speech-auto");
  const sel = $("chat-speech-model");
  const voice = $("chat-speech-voice");
  updateSpeechUI();
  if (auto) {
    auto.addEventListener("change", () => {
      localStorage.setItem(SPEECH_AUTO_KEY, auto.checked ? "1" : "0");
      updateSpeechUI();
    });
  }
  if (sel) {
    sel.addEventListener("change", () => {
      localStorage.setItem(SPEECH_MODEL_KEY, sel.value);
      updateSpeechUI();
    });
  }
  if (voice) {
    voice.addEventListener("change", () => {
      const model = effectiveSpeechModel();
      if (!model) return;
      try { localStorage.setItem(SPEECH_VOICE_KEY + model, voice.value); } catch (_) { }
    });
  }
}

// generateSpeechURL asks the backend for the audio of text and returns a blob
// URL the player can use. The text is capped to the engine's input limit, cut
// by code point so a surrogate pair is never split.
async function generateSpeechURL(text, model, voice) {
  const chars = Array.from(String(text || "").trim());
  const input = chars.slice(0, SPEECH_INPUT_LIMIT).join("");
  if (!input) return "";
  const blob = await api("/api/speech", { method: "POST", body: { model, input, voice: voice || "" } }, "blob");
  return URL.createObjectURL(blob);
}

// startSpeechSynthesis paints the pending state in the bubble and swaps in the
// player once the audio arrives. It always overwrites whatever the bubble had,
// which is what lets a regenerate reuse the same message instead of pushing a
// new one.
function startSpeechSynthesis(msg, input, model, voice, opts = {}) {
  const startedAt = Date.now();
  const prior = msg._speech && msg._speech.status === "ready" ? { ...msg._speech } : null;
  // While a take is being regenerated the previous player stays on screen.
  msg._speech = prior
    ? { status: "pending", model, voice, url: prior.url }
    : { status: "pending", model, voice };
  renderChatMessages();
  generateSpeechURL(input, model, voice)
    .then((url) => {
      if (prior && prior.url && prior.url !== url) {
        try { URL.revokeObjectURL(prior.url); } catch (_) { }
      }
      msg._speech = { status: "ready", url, model, voice };
      // A chat answer keeps the numbers of its own turn; only a speech-only
      // reply, whose whole answer is the audio, is timed by the synthesis.
      if (msg.role === "assistant" && !String(msg.content || "").trim()) {
        msg.elapsedMs = Date.now() - startedAt;
      }
      renderChatMessages();
      if (opts.autoplay) playMessageAudio(msg.id);
      // The server records the call against the model, so refresh the stats
      // (record speed, cold load) the panel shows.
      if (typeof refreshModels === "function") refreshModels().catch(() => { });
    })
    .catch((err) => {
      // A regenerate that fails keeps the audio it already had: losing a
      // working player because a second voice failed would be worse than the
      // error message.
      msg._speech = prior || { status: "error", model, voice };
      renderChatMessages();
      toast(`${t("chat.speech_failed")}: ${err.message}`, "error");
    });
}

// playMessageAudio starts the player that the last render put in the bubble.
// Browsers may refuse an autoplay that has no recent gesture on the page; that
// is not an error, the player is still there for the user to press.
function playMessageAudio(msgId) {
  const article = document.querySelector(`article.chat-msg[data-id="${CSS.escape(String(msgId))}"]`);
  const audio = article && article.querySelector("audio.chat-audio-player");
  if (!audio) return;
  const started = audio.play();
  if (started && typeof started.catch === "function") started.catch(() => { });
}

// speakIntoMessage generates the audio for one message in the background. The
// bubble repaints with a pending line and then with the player. It is used both
// for a speech-only model's reply and for voicing a chat model's answer.
function speakIntoMessage(msg, text, opts = {}) {
  if (!msg || msg._speech) return false;
  const model = opts.model || effectiveSpeechModel();
  const input = String(text || "").trim();
  if (!model || !input) return false;
  startSpeechSynthesis(msg, input, model, opts.voice || selectedSpeechVoice(model), { autoplay: Boolean(opts.autoplay) });
  return true;
}

// regenerateSpeechReply re-synthesizes the answer of a speech-only turn with
// the voice selected now. The text to speak is the message the user sent,
// because a speech-only reply carries its answer as audio and has no body.
function regenerateSpeechReply(msg) {
  if (!msg || msg.role !== "assistant") return false;
  if (msg._speech && msg._speech.status === "pending") return false;
  const idx = chatMessages.indexOf(msg);
  if (idx < 0) return false;
  let text = "";
  for (let i = idx - 1; i >= 0; i--) {
    const prev = chatMessages[i];
    if (prev.role === "user" && String(prev.content || "").trim()) {
      text = String(prev.content);
      break;
    }
  }
  const input = text.trim();
  const model = (msg._speech && msg._speech.model) || effectiveSpeechModel();
  if (!model || !input) return false;
  startSpeechSynthesis(msg, input, model, selectedSpeechVoice(model));
  return true;
}

// attachSpeechToAssistantReply voices the model's answer when the Speech switch
// is on. It runs as the turn ends, because only then the answer is complete.
// msg.content holds the final answer alone: the thinking and the tool log live
// in their own fields, so they are never read aloud.
function attachSpeechToAssistantReply(msg) {
  if (!msg || msg.role !== "assistant" || msg._speech) return false;
  if (msg.isError || msg.stopped || msg.doneReason === "aborted") return false;
  if (!speechAutoEnabled()) return false;
  // A speech-only model already answered with its own audio.
  if (isSpeechOnlyModel(msg.model)) return false;
  // An image model's "answer" is a base64 picture, not text.
  if (typeof isImageGenerationOnlyCaps === "function" && isImageGenerationOnlyCaps(modelCaps(msg.model))) return false;
  // textForSpeech drops code blocks and markdown noise: reading symbols aloud
  // helps nobody.
  const text = textForSpeech(msg.content || "");
  if (!text) return false;
  return speakIntoMessage(msg, text, { model: effectiveSpeechModel(), autoplay: true });
}

// sendSpeechOnlyTurn posts a message to a model that only speaks. There is no
// chat completion to run: the model's answer is the audio itself, so the player
// goes in an assistant bubble and "send" means "speak this".
function sendSpeechOnlyTurn(text, attachments, model) {
  const userMsg = {
    id: nanoid(),
    role: "user",
    content: text,
    attachments: (attachments || []).map((a) => ({ ...a })),
  };
  chatMessages.push(userMsg);

  const replyMsg = typeof newAssistantMessage === "function"
    ? newAssistantMessage()
    : { id: nanoid(), role: "assistant", content: "", model: "" };
  replyMsg.model = model || effectiveSpeechModel();
  replyMsg.streaming = false;
  chatMessages.push(replyMsg);

  renderChatMessages();
  if (typeof scrollChatToBottom === "function") scrollChatToBottom(true);
  speakIntoMessage(replyMsg, text, { model: replyMsg.model });
  if (model) {
    try { localStorage.setItem(SPEECH_MODEL_KEY, model); } catch (_) { }
  }
}

function renderMessageSpeechHTML(m) {
  const sp = m && m._speech;
  if (!sp) return "";
  // A regenerate keeps the player of the previous take visible while the new
  // audio is generated, so the bubble never goes blank on a second try.
  if (sp.status === "pending" && sp.url) {
    return `<div class="chat-file-item chat-file-item-audio chat-speech-block">
    <audio class="chat-audio-player" controls preload="metadata" src="${escapeHtml(sp.url)}" aria-label="${escapeHtml(t("chat.speech_player"))}"></audio>
    <div class="chat-speech-pending" aria-live="polite">${escapeHtml(t("chat.speech_pending"))}</div>
  </div>`;
  }
  if (sp.status === "pending") {
    return `<div class="chat-speech-block chat-speech-pending" aria-live="polite">${escapeHtml(t("chat.speech_pending"))}</div>`;
  }
  if (sp.status === "error") {
    return `<div class="chat-speech-block chat-speech-error">${escapeHtml(t("chat.speech_failed"))}</div>`;
  }
  const voice = sp.voice && sp.voice !== "default" ? ` · ${sp.voice}` : "";
  return `<div class="chat-file-item chat-file-item-audio chat-speech-block">
    <audio class="chat-audio-player" controls preload="metadata" src="${escapeHtml(sp.url)}" aria-label="${escapeHtml(t("chat.speech_player"))}"></audio>
    <span class="chat-file-name mono">${escapeHtml(t("chat.speech_player"))}${escapeHtml(voice)}</span>
  </div>`;
}

document.addEventListener("DOMContentLoaded", bindSpeechUI);
