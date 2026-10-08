"use strict";

const RAG_MAX_FILE_BYTES = 8 * 1024 * 1024;

const ragState = {
  list: [],
  warnings: [],
  loadError: "",
  loading: false,
  creating: false,
  models: [],
  modelsWarnings: [],
  modelsLoading: false,
  modelsError: "",
  modelsGen: 0,
  settingsGen: 0,
  defaultEmbedding: "",
  editingFilename: "",
  createPreferredModel: "",
  settingsLoading: false,
  settingsDirty: false,
  settingsMissingModel: "",
  detail: null,
  detailFilename: "",
  detailError: "",
  entries: [],
  entrySeq: 0,
  activeEntry: 0,
};

function ragEl(tag, cls, text) {
  const el = document.createElement(tag);
  if (cls) el.className = cls;
  if (text !== undefined && text !== null) el.textContent = text;
  return el;
}

function ragFmtDate(unix) {
  if (!unix) return "";
  try {
    return new Date(unix * 1000).toLocaleString();
  } catch {
    return String(unix);
  }
}

function ragFmtBytes(n) {
  if (!n) return "";
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KiB";
  return (n / 1024 / 1024).toFixed(2) + " MiB";
}

function ragModelCaps(name) {
  const m = (ragState.models || []).find((x) => x.name === name);
  if (!m || !m.capabilities) return new Set();
  return new Set(String(m.capabilities).split(",").map((s) => s.trim()).filter(Boolean));
}

function ragMediaAllowed(mediaType, caps) {
  if (mediaType === "image") return caps.has("vision");
  if (mediaType === "audio") return caps.has("audio");
  return false;
}

function ragSubview() {
  const p = window.location.pathname;
  if (p === "/rags/new") return "create";
  if (p.startsWith("/rags/") && p.length > 6) {
    const rest = decodeURIComponent(p.substring(6));
    if (rest.endsWith("/edit")) return { view: "edit", filename: rest.slice(0, -5) };
    return { view: "detail", filename: rest };
  }
  return "list";
}

function ragIsFormView(sub) {
  return sub === "create" || (sub && sub.view === "edit");
}

function ragMediaPending() {
  return ragState.entries.some((e) => e.mediaPending);
}

function ragBusy() {
  return ragState.creating || ragMediaPending();
}

function ragSetCreateDisabled(disabled) {
  const box = document.getElementById("rag-create-box");
  if (!box) return;
  box.querySelectorAll("input, select, textarea, button").forEach((el) => {
    el.disabled = disabled;
  });
  const newBtn = document.getElementById("rags-new-btn");
  if (newBtn) newBtn.disabled = disabled;
  const reloadBtn = document.getElementById("rags-reload-btn");
  if (reloadBtn) reloadBtn.disabled = disabled;
}

async function ragLoadModels(source) {
  const genKey = source === "settings" ? "settingsGen" : "modelsGen";
  const gen = ++ragState[genKey];
  ragState.modelsLoading = true;
  ragState.modelsError = "";
  try {
    const res = await api("/api/rags/models");
    if (ragState[genKey] !== gen) return null;
    ragState.models = res.models || [];
    ragState.modelsWarnings = res.warnings || [];
    ragState.defaultEmbedding = res.default_embedding || "";
    ragState.modelsError = "";
    return res;
  } catch (e) {
    if (ragState[genKey] !== gen) return null;
    ragState.models = [];
    ragState.modelsWarnings = [];
    ragState.modelsError = String(e && e.message ? e.message : e);
    return { models: [], warnings: [], error: ragState.modelsError };
  } finally {
    if (ragState[genKey] === gen) ragState.modelsLoading = false;
  }
}

async function ragRefreshList() {
  ragState.loading = true;
  ragRenderList();
  try {
    const res = await api("/api/rags");
    ragState.list = res.rags || [];
    ragState.warnings = res.warnings || [];
    ragState.loadError = "";
  } catch (e) {
    ragState.loadError = String(e && e.message ? e.message : e);
  } finally {
    ragState.loading = false;
  }
  ragRenderList();
}

function ragNav(path) {
  if (window.location.pathname !== path) history.pushState(null, "", path);
  showRagsView();
}

async function showRagsView() {
  ragCloseMediaPreview();
  currentView = "rags";
  hideAllMainViews();
  document.getElementById("rags-btn")?.classList.add("active");
  const view = document.getElementById("rags-view");
  if (view) view.hidden = false;
  const sub = ragSubview();
  const createBox = document.getElementById("rag-create-box");
  const detailBox = document.getElementById("rags-detail");
  const listWrap = document.getElementById("rags-list-wrap");
  const headActions = document.querySelector("#rags-view .tests-head-actions");
  const isForm = ragIsFormView(sub);
  if (createBox) createBox.hidden = !isForm;
  if (detailBox) detailBox.hidden = !(sub && sub.view === "detail");
  if (listWrap) listWrap.hidden = sub !== "list";
  if (headActions) headActions.style.display = sub === "list" ? "" : "none";
  if (sub === "list") {
    void ragRefreshList();
  } else if (sub === "create") {
    ragStartCreate();
  } else if (sub && sub.view === "edit") {
    void ragStartEdit(sub.filename);
  } else if (sub && sub.view === "detail") {
    void ragLoadDetail(sub.filename);
  }
}

async function ragLoadDetail(filename) {
  ragState.detail = null;
  ragState.detailFilename = filename;
  ragState.detailError = "";
  ragRenderDetail();
  try {
    ragState.detail = await api("/api/rags/" + encodeURIComponent(filename));
    ragState.detailError = "";
  } catch (e) {
    ragState.detailError = String(e && e.message ? e.message : e);
  }
  ragRenderDetail();
}

function ragRenderList() {
  const warn = document.getElementById("rags-warnings");
  if (warn) {
    warn.innerHTML = "";
    warn.hidden = !ragState.warnings.length;
    for (const w of ragState.warnings) warn.appendChild(ragEl("div", "", String(w)));
  }
  const tbody = document.getElementById("rags-tbody");
  const empty = document.getElementById("rags-empty");
  const emptyTitle = document.getElementById("rags-empty-title");
  const emptyHint = document.getElementById("rags-empty-hint");
  const table = document.getElementById("rags-table");
  if (!tbody || !empty || !table) return;
  tbody.innerHTML = "";
  if (ragState.loadError) {
    table.closest(".table-wrap").hidden = true;
    empty.hidden = false;
    if (emptyTitle) emptyTitle.textContent = ragState.loadError;
    if (emptyHint) emptyHint.textContent = "";
    return;
  }
  if (!ragState.list.length) {
    table.closest(".table-wrap").hidden = true;
    empty.hidden = false;
    if (emptyTitle) emptyTitle.textContent = ragState.loading ? t("rag.loading_models") : t("rag.empty");
    if (emptyHint) {
      emptyHint.innerHTML = "";
      if (!ragState.loading) {
        emptyHint.appendChild(document.createTextNode(t("rag.empty_hint") + " "));
        const link = ragEl("a", "rag-empty-link", t("rag.empty_settings_link"));
        link.href = "/settings/rag";
        link.addEventListener("click", (ev) => {
          ev.preventDefault();
          history.pushState(null, "", "/settings/rag");
          handleRouting();
        });
        emptyHint.appendChild(link);
      }
    }
    return;
  }
  table.closest(".table-wrap").hidden = false;
  empty.hidden = true;
  for (const r of ragState.list) {
    const tr = ragEl("tr", "rag-row");
    tr.addEventListener("click", () => ragNav("/rags/" + encodeURIComponent(r.filename)));
    for (const v of [r.name, r.embedding_model || "", String(r.dimensions || ""), String(r.entries || 0), ragFmtDate(r.created_at), r.filename]) {
      tr.appendChild(ragEl("td", "", v));
    }
    tbody.appendChild(tr);
  }
}

function ragRenderDetail() {
  const title = document.getElementById("rag-detail-title");
  const meta = document.getElementById("rag-detail-meta");
  const box = document.getElementById("rag-detail-entries");
  if (!title || !meta || !box) return;
  title.textContent = ragState.detailFilename;
  meta.innerHTML = "";
  box.innerHTML = "";
  if (ragState.detailError) {
    meta.appendChild(ragEl("div", "form-hint", ragState.detailError));
    return;
  }
  const d = ragState.detail;
  if (!d || d.filename !== ragState.detailFilename) {
    meta.appendChild(ragEl("div", "form-hint", t("rag.loading_models")));
    return;
  }
  const m = d.meta || {};
  const ro = ragEl("span", "badge badge-muted", t("rag.read_only"));
  meta.appendChild(ro);
  meta.appendChild(document.createTextNode(
    " " + (m.name || "") + " · " + (m.embedding_model || "") + " · " +
    (m.dimensions || "") + " dims · " + ragFmtDate(m.created_at)));
  if (m.description) meta.appendChild(ragEl("div", "", m.description));
  const entries = d.entries || [];
  if (!entries.length) {
    box.appendChild(ragEl("div", "muted", t("rag.detail_empty")));
    return;
  }
  for (const e of entries) {
    const item = ragEl("div", "rag-entry-card");
    const head = ragEl("div", "rag-entry-head");
    head.appendChild(ragEl("div", "rag-entry-term", e.term));
    item.appendChild(head);
    if (e.content) item.appendChild(ragEl("div", "rag-entry-content", e.content));
    const media = e.media || [];
    if (media.length || e.input_mode === "media") {
      const mediaBox = ragEl("div", "rag-entry-media");
      if (e.input_mode === "media") mediaBox.appendChild(ragEl("span", "rag-mode-pill", t("rag.input_media")));
      for (const m of media) {
        const mediaURL = "/api/rags/" + encodeURIComponent(ragState.detailFilename) +
          "/media/" + encodeURIComponent(String(e.id || 0)) + "/" + encodeURIComponent(m.type);
        const tile = ragEl("div", "rag-media-slot rag-detail-media", "");
        tile.appendChild(ragEl("div", "rag-media-slot-title", (m.type === "audio" ? "🔊 " : "🖼️ ") + (m.name || m.type)));
        const preview = ragEl(m.type === "audio" ? "div" : "button", "rag-media-preview", "");
        if (m.type === "image") {
          preview.type = "button";
          const img = ragEl("img", "rag-media-thumb");
          img.src = mediaURL;
          img.alt = m.name || m.type;
          preview.appendChild(img);
        } else {
          preview.setAttribute("role", "button");
          preview.tabIndex = 0;
          preview.addEventListener("keydown", (ev) => {
            if (ev.key === "Enter" || ev.key === " ") {
              ev.preventDefault();
              ragShowMediaPreview(m, m.type, mediaURL);
            }
          });
          const audio = ragEl("audio", "rag-media-audio");
          audio.controls = true;
          audio.preload = "metadata";
          audio.src = mediaURL;
          audio.addEventListener("click", (ev) => ev.stopPropagation());
          audio.addEventListener("keydown", (ev) => ev.stopPropagation());
          preview.appendChild(audio);
          preview.appendChild(ragEl("span", "rag-media-open", t("rag.preview")));
        }
        preview.addEventListener("click", () => ragShowMediaPreview(m, m.type, mediaURL));
        tile.appendChild(preview);
        tile.appendChild(ragEl("div", "rag-media-meta",
          (m.mime || m.type) + " · " + ragFmtBytes(m.size || 0)));
        mediaBox.appendChild(tile);
      }
      item.appendChild(mediaBox);
    }
    box.appendChild(item);
  }
}

function ragNewEntry() {
  const key = ++ragState.entrySeq;
  ragState.activeEntry = key;
  return { key, term: "", content: "", inputMode: "combined", media: {}, mediaPending: false, mediaError: "" };
}

function ragConfiguredDefault() {
  return (currentConfig && currentConfig.rag && currentConfig.rag.default_embedding) || ragState.defaultEmbedding || "";
}

function ragGoToRequiredSettings() {
  history.pushState(null, "", "/settings/rag");
  void showSettingsView();
}

async function ragEnsureDefaultModel() {
  const res = await ragLoadModels();
  if (!res) return;
  if (!ragState.editingFilename && !ragState.defaultEmbedding) {
    toast(t("rag.default_required"), "error");
    ragGoToRequiredSettings();
    return;
  }
  if (!ragState.createPreferredModel) {
    ragState.createPreferredModel = ragState.editingFilename ? "" : ragState.defaultEmbedding;
  }
  ragRebuildModelSelect();
  ragRenderEntries();
}

function ragStartCreate() {
  const wasEditing = !!ragState.editingFilename;
  ragState.editingFilename = "";
  ragState.createPreferredModel = ragConfiguredDefault();
  if (wasEditing) ragState.entries = [];
  if (!ragState.entries.length) ragState.entries = [ragNewEntry()];
  if (!ragState.entries.some((e) => e.key === ragState.activeEntry)) {
    ragState.activeEntry = ragState.entries[0].key;
  }
  ragRebuildModelSelect();
  ragRenderEntries();
  const btn = document.getElementById("rag-create-btn");
  if (btn) btn.textContent = t("rag.create_btn");
  void ragEnsureDefaultModel();
}

async function ragStartEdit(filename) {
  const btn = document.getElementById("rag-create-btn");
  if (btn) btn.textContent = t("rag.save_btn");
  ragState.editingFilename = filename;
  ragState.createPreferredModel = "";
  ragState.entries = [];
  ragState.activeEntry = 0;
  ragSetCreateError(t("rag.loading"));
  ragSetCreateDisabled(true);
  try {
    const detail = await api("/api/rags/" + encodeURIComponent(filename));
    const nameIn = document.getElementById("rag-new-name");
    const descIn = document.getElementById("rag-new-desc");
    if (nameIn) nameIn.value = detail.meta && detail.meta.name || "";
    if (descIn) descIn.value = detail.meta && detail.meta.description || "";
    ragState.createPreferredModel = detail.meta && detail.meta.embedding_model || "";
    ragState.entries = (detail.entries || []).map((e) => {
      const entry = {
        key: ++ragState.entrySeq,
        term: e.term || "",
        content: e.content || "",
        inputMode: e.input_mode || "combined",
        media: {},
        mediaPending: false,
        mediaError: "",
      };
      for (const m of e.media || []) {
        entry.media[m.type] = {
          type: m.type,
          name: m.name || m.type,
          mime: m.mime || "",
          size: m.size || 0,
          existing: true,
          entryID: e.id,
        };
      }
      return entry;
    });
    ragState.activeEntry = ragState.entries[0] ? ragState.entries[0].key : 0;
    ragSetCreateError("");
    await ragLoadModels();
    ragRebuildModelSelect();
    ragRenderEntries();
  } catch (e) {
    ragSetCreateError(String(e && e.message ? e.message : e));
  } finally {
    ragSetCreateDisabled(false);
  }
}

function ragRebuildModelSelect() {
  const sel = document.getElementById("rag-new-model");
  if (!sel) return;
  const wanted = ragState.createPreferredModel || sel.value || ragState.defaultEmbedding;
  sel.innerHTML = "";
  if (ragState.modelsLoading && !ragState.models.length) {
    const loading = ragEl("option", "", t("rag.loading_models"));
    loading.value = "";
    loading.disabled = true;
    sel.appendChild(loading);
  }
  for (const m of ragState.models) {
    const o = ragEl("option", "", m.name);
    o.value = m.name;
    sel.appendChild(o);
  }
  if (wanted && !ragState.models.some((m) => m.name === wanted)) {
    const o = ragEl("option", "", wanted + " (" + t("rag.model_missing") + ")");
    o.value = wanted;
    sel.appendChild(o);
  }
  if (wanted) sel.value = wanted;
  else if (ragState.models.length) sel.value = ragState.models[0].name;
  const hint = document.getElementById("rag-new-model-hint");
  if (hint) {
    hint.innerHTML = "";
    if (ragState.modelsError) {
      hint.appendChild(ragEl("div", "form-hint", t("rag.models_load_failed") + ": " + ragState.modelsError));
    }
    for (const w of ragState.modelsWarnings) hint.appendChild(ragEl("div", "form-hint", String(w)));
    if (!ragState.modelsLoading && !ragState.modelsError && !ragState.models.length) {
      hint.appendChild(ragEl("div", "form-hint", t("rag.no_embedding_models")));
    }
    if (!ragState.editingFilename && ragState.defaultEmbedding) {
      hint.appendChild(ragEl("div", "form-hint", t("rag.default_model_hint", { model: ragState.defaultEmbedding })));
    }
  }
}

function ragCreateCaps() {
  const sel = document.getElementById("rag-new-model");
  return ragModelCaps(sel ? sel.value : "");
}

function ragEntryMediaList(entry) {
  return ["image", "audio"].map((type) => entry.media[type]).filter(Boolean);
}

function ragEntryTitle(entry, idx) {
  return entry.term.trim() || (entry.media.image && entry.media.image.name) ||
    (entry.media.audio && entry.media.audio.name) || t("rag.entry_untitled", { index: idx + 1 });
}

function ragUpdateEntryNav(entry) {
  const btn = document.querySelector(`.rag-entry-nav[data-entry-key="${entry.key}"] .rag-entry-nav-title`);
  if (!btn) return;
  const idx = ragState.entries.indexOf(entry);
  btn.textContent = ragEntryTitle(entry, idx);
}

function ragSetActiveEntry(key) {
  ragState.activeEntry = key;
  ragRenderEntries();
}

function ragMediaDataURL(media) {
  if (media.base64) {
    return "data:" + (media.mime || "application/octet-stream") + ";base64," + media.base64;
  }
  if (media.existing && media.entryID && ragState.editingFilename) {
    return "/api/rags/" + encodeURIComponent(ragState.editingFilename) +
      "/media/" + encodeURIComponent(String(media.entryID)) + "/" + encodeURIComponent(media.type);
  }
  return "";
}

function ragShowMediaPreview(media, mediaType, src) {
  const modal = document.getElementById("rag-media-modal");
  const body = document.getElementById("rag-media-modal-body");
  const caption = document.getElementById("rag-media-modal-caption");
  if (!media || !modal || !body || !caption) return;
  body.innerHTML = "";
  caption.textContent = (media.name || media.type) + " · " + (media.mime || media.type) + " · " + ragFmtBytes(media.size || 0);
  if (mediaType === "image") {
    const img = ragEl("img", "rag-media-modal-img");
    img.src = src;
    img.alt = media.name || mediaType;
    body.appendChild(img);
  } else {
    const audio = ragEl("audio", "rag-media-modal-audio");
    audio.controls = true;
    audio.autoplay = true;
    audio.src = src;
    body.appendChild(audio);
  }
  modal.hidden = false;
}

function ragCloseMediaPreview() {
  const modal = document.getElementById("rag-media-modal");
  const body = document.getElementById("rag-media-modal-body");
  if (body) body.innerHTML = "";
  if (modal) modal.hidden = true;
}

function ragRenderMediaSlot(entry, mediaType, caps, fileIn) {
  const media = entry.media[mediaType];
  const box = ragEl("div", "rag-media-slot");
  const icon = mediaType === "audio" ? "🔊" : "🖼️";
  const label = t(mediaType === "audio" ? "rag.media_audio" : "rag.media_image");
  box.appendChild(ragEl("div", "rag-media-slot-title", icon + " " + label));
  if (!media) {
    const attach = ragEl("button", "ghost small rag-media-add", t(mediaType === "audio" ? "rag.attach_audio" : "rag.attach_image"));
    attach.type = "button";
    attach.disabled = !ragMediaAllowed(mediaType, caps);
    attach.title = t("rag.attach_title");
    attach.addEventListener("click", () => {
      if (!ragMediaAllowed(mediaType, ragCreateCaps())) {
        entry.mediaError = t("rag.no_media_cap");
        ragRenderEntries();
        return;
      }
      fileIn.dataset.mediaType = mediaType;
      fileIn.accept = mediaType + "/*";
      fileIn.click();
    });
    box.appendChild(attach);
    return box;
  }

  const preview = ragEl(mediaType === "audio" ? "div" : "button", "rag-media-preview", "");
  if (mediaType === "image") {
    preview.type = "button";
  } else {
    preview.setAttribute("role", "button");
    preview.tabIndex = 0;
    preview.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" || ev.key === " ") {
        ev.preventDefault();
        ragShowMediaPreview(media, mediaType, ragMediaDataURL(media));
      }
    });
  }
  preview.title = t("rag.preview");
  preview.addEventListener("click", () => ragShowMediaPreview(media, mediaType, ragMediaDataURL(media)));
  if (mediaType === "image") {
    const img = ragEl("img", "rag-media-thumb");
    img.src = ragMediaDataURL(media);
    img.alt = media.name || mediaType;
    preview.appendChild(img);
  } else {
    const audio = ragEl("audio", "rag-media-audio");
    audio.controls = true;
    audio.preload = "metadata";
    audio.src = ragMediaDataURL(media);
    audio.addEventListener("click", (ev) => ev.stopPropagation());
    audio.addEventListener("keydown", (ev) => ev.stopPropagation());
    preview.appendChild(audio);
    preview.appendChild(ragEl("span", "rag-media-open", t("rag.preview")));
  }
  box.appendChild(preview);

  const meta = ragEl("div", "rag-media-meta", media.name || media.type);
  meta.title = (media.name || "") + " · " + (media.mime || "") + " · " + ragFmtBytes(media.size || 0);
  box.appendChild(meta);
  const actions = ragEl("div", "rag-media-actions");
  const replace = ragEl("button", "ghost small", t("rag.replace"));
  replace.type = "button";
  replace.addEventListener("click", () => {
    if (ragBusy()) return;
    fileIn.dataset.mediaType = mediaType;
    fileIn.accept = mediaType + "/*";
    fileIn.click();
  });
  const remove = ragEl("button", "ghost small", t("rag.remove_attachment"));
  remove.type = "button";
  remove.addEventListener("click", () => {
    if (ragBusy()) return;
    delete entry.media[mediaType];
    if (entry.inputMode === "media" && !ragEntryMediaList(entry).length) entry.inputMode = "combined";
    ragRenderEntries();
  });
  actions.appendChild(replace);
  actions.appendChild(remove);
  box.appendChild(actions);
  if (!ragMediaAllowed(mediaType, caps)) box.appendChild(ragEl("div", "form-hint", t("rag.media_unsupported")));
  return box;
}

function ragRenderEntries() {
  const host = document.getElementById("rag-entries");
  const list = document.getElementById("rag-entry-list");
  const count = document.getElementById("rag-entry-count");
  if (!host || !list) return;
  list.innerHTML = "";
  host.innerHTML = "";
  if (count) count.textContent = "· " + ragState.entries.length;
  const caps = ragCreateCaps();

  ragState.entries.forEach((entry, idx) => {
    const btn = ragEl("button", "rag-entry-nav" + (entry.key === ragState.activeEntry ? " active" : ""), "");
    btn.type = "button";
    btn.dataset.entryKey = String(entry.key);
    btn.setAttribute("role", "option");
    btn.setAttribute("aria-selected", entry.key === ragState.activeEntry ? "true" : "false");
    const title = ragEl("span", "rag-entry-nav-title", ragEntryTitle(entry, idx));
    const meta = ragEl("span", "rag-entry-nav-meta", "");
    if (entry.media.image) meta.appendChild(document.createTextNode("🖼️"));
    if (entry.media.audio) meta.appendChild(document.createTextNode("🔊"));
    if (entry.inputMode === "media") meta.appendChild(ragEl("span", "rag-mode-pill", t("rag.input_media_short")));
    btn.appendChild(title);
    btn.appendChild(meta);
    btn.addEventListener("click", () => ragSetActiveEntry(entry.key));
    list.appendChild(btn);
  });

  const entry = ragState.entries.find((e) => e.key === ragState.activeEntry) || ragState.entries[0];
  if (!entry) {
    host.appendChild(ragEl("div", "muted", t("rag.entries_required")));
    return;
  }
  ragState.activeEntry = entry.key;

  const card = ragEl("div", "rag-entry-card");
  const top = ragEl("div", "rag-entry-head");
  const term = ragEl("input", "rag-entry-term");
  term.type = "text";
  term.placeholder = t("rag.term_placeholder");
  term.maxLength = 256;
  term.value = entry.term;
  term.addEventListener("input", () => {
    entry.term = term.value;
    ragUpdateEntryNav(entry);
  });
  top.appendChild(term);
  const rm = ragEl("button", "ghost small rag-entry-remove", "");
  rm.type = "button";
  rm.innerHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M18 6L6 18M6 6l12 12"/></svg>';
  rm.title = t("rag.remove_entry");
  rm.addEventListener("click", () => {
    if (ragBusy()) return;
    const idx = ragState.entries.indexOf(entry);
    ragState.entries = ragState.entries.filter((x) => x !== entry);
    if (entry.key === ragState.activeEntry) {
      const next = ragState.entries[Math.min(idx, ragState.entries.length - 1)];
      ragState.activeEntry = next ? next.key : 0;
    }
    ragRenderEntries();
  });
  top.appendChild(rm);
  card.appendChild(top);

  const fileIn = ragEl("input", "");
  fileIn.type = "file";
  fileIn.style.display = "none";
  fileIn.addEventListener("change", () => {
    const f = fileIn.files && fileIn.files[0];
    const mediaType = fileIn.dataset.mediaType || "";
    fileIn.value = "";
    ragOnFileChosen(entry, mediaType, f);
  });
  card.appendChild(fileIn);

  const grid = ragEl("div", "rag-entry-grid");
  const textBox = ragEl("div", "rag-entry-text");
  const modeField = ragEl("label", "rag-input-mode", "");
  modeField.appendChild(ragEl("span", "", t("rag.input_mode_label")));
  const modeSel = ragEl("select", "", "");
  const combined = ragEl("option", "", t("rag.input_combined"));
  combined.value = "combined";
  const mediaOnly = ragEl("option", "", t("rag.input_media"));
  mediaOnly.value = "media";
  mediaOnly.disabled = !ragEntryMediaList(entry).length;
  modeSel.appendChild(combined);
  modeSel.appendChild(mediaOnly);
  if (entry.inputMode === "media" && !ragEntryMediaList(entry).length) entry.inputMode = "combined";
  modeSel.value = entry.inputMode;
  modeSel.addEventListener("change", () => {
    entry.inputMode = modeSel.value;
    ragRenderEntries();
  });
  modeField.appendChild(modeSel);
  textBox.appendChild(modeField);
  textBox.appendChild(ragEl("div", "form-hint", t("rag.input_mode_hint")));

  const content = ragEl("textarea", "rag-entry-content");
  content.placeholder = t("rag.content_placeholder");
  content.rows = 6;
  content.value = entry.content;
  content.addEventListener("input", () => {
    entry.content = content.value;
  });
  textBox.appendChild(content);
  grid.appendChild(textBox);

  const mediaBox = ragEl("div", "rag-entry-media-grid");
  mediaBox.appendChild(ragRenderMediaSlot(entry, "image", caps, fileIn));
  mediaBox.appendChild(ragRenderMediaSlot(entry, "audio", caps, fileIn));
  grid.appendChild(mediaBox);
  card.appendChild(grid);

  if (entry.mediaPending) card.appendChild(ragEl("div", "rag-entry-media muted small", t("rag.file_pending")));
  if (entry.mediaError) card.appendChild(ragEl("div", "rag-entry-media form-hint", entry.mediaError));
  host.appendChild(card);
  host.appendChild(ragEl("p", "form-hint", t("rag.media_hint")));
}

function ragOnFileChosen(entry, mediaType, file) {
  if (!file) return;
  const caps = ragCreateCaps();
  if ((mediaType !== "image" && mediaType !== "audio") || !file.type.startsWith(mediaType + "/")) {
    entry.mediaError = t("rag.media_bad_type");
    ragRenderEntries();
    return;
  }
  if (!ragMediaAllowed(mediaType, caps)) {
    entry.mediaError = t("rag.media_unsupported");
    ragRenderEntries();
    return;
  }
  if (file.size > RAG_MAX_FILE_BYTES) {
    entry.mediaError = t("rag.media_too_big");
    ragRenderEntries();
    return;
  }
  entry.mediaPending = true;
  entry.mediaError = "";
  ragSetCreateDisabled(true);
  const reader = new FileReader();
  reader.onerror = () => {
    entry.mediaPending = false;
    entry.mediaError = t("rag.file_read_error");
    ragRenderEntries();
    ragSetCreateDisabled(ragState.creating || ragMediaPending());
  };
  reader.onload = () => {
    entry.mediaPending = false;
    const result = String(reader.result || "");
    const idx = result.indexOf(",");
    entry.media[mediaType] = {
      type: mediaType,
      name: file.name,
      mime: file.type,
      size: file.size,
      base64: idx >= 0 ? result.substring(idx + 1) : "",
    };
    ragRenderEntries();
    ragSetCreateDisabled(ragState.creating || ragMediaPending());
  };
  reader.readAsDataURL(file);
}

function ragSetCreateError(msg) {
  const st = document.getElementById("rag-create-status");
  if (!st) return;
  st.textContent = msg || "";
  st.hidden = !msg;
}

async function ragSubmitCreate() {
  if (ragBusy()) {
    if (ragMediaPending()) ragSetCreateError(t("rag.file_pending"));
    return;
  }
  const model = document.getElementById("rag-new-model")?.value || "";
  const name = (document.getElementById("rag-new-name")?.value || "").trim();
  const description = (document.getElementById("rag-new-desc")?.value || "").trim();
  const caps = ragCreateCaps();
  let err = "";
  if (!name) err = t("rag.name_required");
  else if (!model) err = t("rag.no_model_selected");
  else if (!ragState.entries.length) err = t("rag.entries_required");
  for (const e of ragState.entries) {
    if (err) break;
    const media = ragEntryMediaList(e);
    if (!e.term.trim() && !media.length) err = t("rag.term_required");
    else if (media.some((m) => !ragMediaAllowed(m.type, caps))) err = t("rag.media_unsupported");
    else if (e.inputMode === "media" && !media.length) err = t("rag.media_required");
    else if (!media.length && !e.content.trim()) err = t("rag.content_required");
  }
  if (err) {
    ragSetCreateError(err);
    return;
  }
  ragSetCreateError("");
  const payload = {
    name,
    description,
    embedding_model: model,
    entries: ragState.entries.map((e) => ({
      term: e.term,
      content: e.content,
      input_mode: e.inputMode,
      media: ragEntryMediaList(e).map((m) => (m.existing ? {
        type: m.type,
        name: m.name,
        mime: m.mime,
        existing: true,
        entry_id: m.entryID,
      } : {
        type: m.type,
        name: m.name,
        mime: m.mime,
        base64: m.base64,
      })),
    })),
  };
  ragState.creating = true;
  ragSetCreateDisabled(true);
  const btn = document.getElementById("rag-create-btn");
  const editing = ragState.editingFilename;
  if (btn) btn.textContent = editing ? t("rag.saving") : t("rag.generating");
  try {
    const res = await api(editing ? "/api/rags/" + encodeURIComponent(editing) : "/api/rags", {
      method: editing ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    ragState.entries = [];
    ragState.activeEntry = 0;
    ragState.editingFilename = "";
    ragState.createPreferredModel = "";
    const nameIn = document.getElementById("rag-new-name");
    const descIn = document.getElementById("rag-new-desc");
    if (nameIn) nameIn.value = "";
    if (descIn) descIn.value = "";
    toast(t(editing ? "rag.updated" : "rag.created", { name }), "success");
    ragState.creating = false;
    if (btn) btn.textContent = t("rag.create_btn");
    const filename = res && res.rag && res.rag.filename;
    ragNav(editing && filename ? "/rags/" + encodeURIComponent(filename) : "/rags");
    return;
  } catch (e) {
    ragSetCreateError(String(e && e.message ? e.message : e));
  } finally {
    ragState.creating = false;
    if (btn) btn.textContent = editing ? t("rag.save_btn") : t("rag.create_btn");
    if (ragIsFormView(ragSubview())) ragSetCreateDisabled(ragMediaPending());
  }
}

function ragSettingsPayload() {
  const body = {};
  if (!ragState.settingsLoading) {
    const sel = document.getElementById("set-rag-embedding");
    if (sel && sel.value) body.default_embedding = sel.value;
  }
  const dir = document.getElementById("set-rag-dir");
  if (dir) body.directory = dir.value.trim();
  return body;
}

function applyRagConfigResponse(res) {
  if (res && res.rag_directory) {
    const hint = document.getElementById("set-rag-dir-hint");
    if (hint) hint.textContent = res.rag_directory;
  }
  if (res && res.rag && typeof res.rag.default_embedding === "string") {
    ragState.defaultEmbedding = res.rag.default_embedding;
    ragState.settingsMissingModel = "";
    ragState.settingsDirty = false;
  }
}

function ragSettingsInit() {
  const sel = document.getElementById("set-rag-embedding");
  if (!sel || !currentConfig) return;
  const saved = (currentConfig.rag && currentConfig.rag.default_embedding) || "";
  const dirIn = document.getElementById("set-rag-dir");
  if (dirIn) dirIn.value = (currentConfig.rag && currentConfig.rag.directory) || "";
  const resolved = document.getElementById("set-rag-dir-hint");
  if (resolved) resolved.textContent = currentConfig.rag_directory || "";

  ragState.settingsDirty = false;
  sel.onchange = () => {
    ragState.settingsDirty = true;
  };
  sel.innerHTML = "";
  if (saved) {
    const o = ragEl("option", "", saved);
    o.value = saved;
    sel.appendChild(o);
  } else {
    const loading = ragEl("option", "", t("rag.loading_models"));
    loading.value = "";
    loading.disabled = true;
    sel.appendChild(loading);
  }
  sel.value = saved;

  ragState.settingsLoading = true;
  ragLoadModels("settings").then((res) => {
    if (!res) return;
    ragState.settingsLoading = false;
    const current = sel.value;
    sel.innerHTML = "";
    for (const m of ragState.models) {
      const o = ragEl("option", "", m.name);
      o.value = m.name;
      sel.appendChild(o);
    }
    let wanted = ragState.settingsDirty ? current : ((currentConfig.rag && currentConfig.rag.default_embedding) || "");
    if (!wanted && ragState.models.length) wanted = ragState.models[0].name;
    ragState.settingsMissingModel = "";
    if (wanted && !ragState.models.some((m) => m.name === wanted)) {
      ragState.settingsMissingModel = wanted;
      const o = ragEl("option", "", wanted + " (" + t("rag.model_missing") + ")");
      o.value = wanted;
      sel.appendChild(o);
    }
    if (!wanted && !ragState.models.length) {
      const empty = ragEl("option", "", t("rag.no_embedding_models"));
      empty.value = "";
      empty.disabled = true;
      sel.appendChild(empty);
    }
    sel.value = wanted;
    const warn = document.getElementById("set-rag-embedding-warn");
    if (warn) {
      warn.hidden = false;
      warn.innerHTML = "";
      if (ragState.modelsError) {
        warn.appendChild(ragEl("div", "", t("rag.models_load_failed") + ": " + ragState.modelsError));
      }
      for (const w of ragState.modelsWarnings) warn.appendChild(ragEl("div", "", String(w)));
      if (!ragState.modelsError && !ragState.models.length) {
        warn.appendChild(ragEl("div", "", t("rag.no_embedding_models")));
      }
      if (ragState.settingsMissingModel) {
        warn.appendChild(ragEl("div", "", t("settings.rag_embedding_missing", { model: ragState.settingsMissingModel })));
      }
      if (!warn.children.length) warn.hidden = true;
    }
  });
}

document.getElementById("rags-btn")?.addEventListener("click", () => ragNav("/rags"));
document.getElementById("rags-back-btn")?.addEventListener("click", () => {
  if (ragBusy()) return;
  showModelsView();
  history.pushState(null, "", "/");
});
document.getElementById("rags-reload-btn")?.addEventListener("click", () => void ragRefreshList());
document.getElementById("rags-new-btn")?.addEventListener("click", () => ragNav("/rags/new"));
document.getElementById("rag-create-cancel-btn")?.addEventListener("click", () => {
  if (ragBusy()) return;
  const editing = ragState.editingFilename;
  ragState.entries = [];
  ragState.activeEntry = 0;
  ragState.editingFilename = "";
  ragState.createPreferredModel = "";
  ragSetCreateError("");
  const nameIn = document.getElementById("rag-new-name");
  const descIn = document.getElementById("rag-new-desc");
  const modelSel = document.getElementById("rag-new-model");
  if (nameIn) nameIn.value = "";
  if (descIn) descIn.value = "";
  if (modelSel) modelSel.value = "";
  ragNav(editing ? "/rags/" + encodeURIComponent(editing) : "/rags");
});
document.getElementById("rag-add-entry-btn")?.addEventListener("click", () => {
  if (ragBusy()) return;
  ragState.entries.push(ragNewEntry());
  ragRenderEntries();
});
document.getElementById("rag-create-btn")?.addEventListener("click", () => void ragSubmitCreate());
document.getElementById("rag-entry-list")?.addEventListener("keydown", (ev) => {
  if (ev.key !== "ArrowDown" && ev.key !== "ArrowUp") return;
  const idx = ragState.entries.findIndex((e) => e.key === ragState.activeEntry);
  const nextIdx = idx + (ev.key === "ArrowDown" ? 1 : -1);
  const next = ragState.entries[nextIdx];
  if (!next) return;
  ev.preventDefault();
  ragSetActiveEntry(next.key);
  requestAnimationFrame(() => {
    document.querySelector(`.rag-entry-nav[data-entry-key="${next.key}"]`)?.focus();
  });
});
document.getElementById("rag-detail-edit-btn")?.addEventListener("click", () => {
  if (ragState.detailFilename) ragNav("/rags/" + encodeURIComponent(ragState.detailFilename) + "/edit");
});
document.getElementById("rag-detail-back-btn")?.addEventListener("click", () => ragNav("/rags"));
document.getElementById("rag-new-model")?.addEventListener("change", (ev) => {
  ragState.createPreferredModel = ev.target.value;
  ragRenderEntries();
});
document.getElementById("rag-media-modal-close")?.addEventListener("click", ragCloseMediaPreview);
document.getElementById("rag-media-modal")?.addEventListener("click", (ev) => {
  if (ev.target && ev.target.id === "rag-media-modal") ragCloseMediaPreview();
});
document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape") ragCloseMediaPreview();
});
