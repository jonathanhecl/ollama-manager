"use strict";

// ---------- state ----------
let models = [];
let ghostModels = [];
let showGhostModels = localStorage.getItem("ollama_show_ghost_models") === "true";
let activeName = null;
let jobs = new Map();   // id -> job
let jobsStream = null;  // EventSource for /api/jobs/events
let jobsBackoffMs = 1000;
let jobsHydrated = false; // true once the first jobs snapshot has arrived
let queuePaused = false;
let currentView = "models";
let showArchivedOnly = false;
let modelSearchQuery = "";
let chatMessages = [];
let chatAttachments = [];
let chatStreamLock = false;
let chatRenderRaf = null;
let chatAbortController = null;
let chatArtifactVisibleBeforeOptions = false;
let chatThinkTicker = null;
let chatStreamTicker = null;
let chatLastUsedTokens = 0;
let chatDndDepth = 0;
let chatPendingQueue = [];
let chatIsRecording = false;
let chatRecorderStream = null;
let chatAudioContext = null;
let chatAudioSource = null;
let chatAudioProcessor = null;
let chatAudioBuffers = [];
let chatAudioSampleRate = 0;
let speakingMsgId = "";
let activeStreamMessage = null;
let activeArtifactTimestamp = null;
let activeArtifactName = null;
let activeArtifactUrl = null;
let chatEditingMessageId = "";
let chatEditingDraft = "";
const CHAT_OPTION_FALLBACKS = {
  system: "",
  temperature: 0.7,
  top_k: 40,
  top_p: 0.9,
  num_ctx: 100,
  think_level: "auto",
  web_tools: false,
  artifacts: false,
  image_width: 512,
  image_height: 512,
  image_steps: 4,
  image_seed: 0,
};
const STATUS_REFRESH_MS = 1000;
const chatModelDefaultsCache = new Map();
let chatDefaultsReqSeq = 0;
let lastChatDefaultsModel = "";
/** /api/status succeeded since last call */
let managerApiOk = false;
/** Ollama host reachable (from /api/status) */
let ollamaHostOk = false;
let lastSystemStatus = null;
let runningModels = [];
let runningRefreshTimer = null;

// Tests panel state.
let testsGroups = [];
let tests = [];
let selectedGroupId = "";
let currentTestId = null; // null for new, id for edit
let testEditorAttachments = []; // {id, kind, name, mime, data}

// Battery runner state.
let batterySelectedModels = new Set();
let currentBatteryRun = null;
const BATTERY_KEY = "ollamaMgr.battery";
let batteryPollRetryCount = 0;

// Agent session state.
let currentAgentSession = null; // session object from API
let currentAgentTestId = null;

// Sorting: persisted across reloads.
const SORT_KEY = "ollamaMgr.sort";
const MOBILE_BREAK = 640;
let sort = { col: "modified_at", dir: "desc" };
try {
  const saved = JSON.parse(localStorage.getItem(SORT_KEY) || "null");
  if (saved && saved.col) sort = saved;
} catch { }
if (!localStorage.getItem(SORT_KEY) && window.innerWidth <= MOBILE_BREAK) {
  sort = { col: "record_tokens_per_sec", dir: "desc" };
}

