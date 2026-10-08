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
  settingsLoading: false,
  settingsDirty: false,
  settingsMissingModel: "",
  detail: null,
  detailFilename: "",
  detailError: "",
  entries: [],
  entrySeq: 0,
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

function ragMediaAccept(caps) {
  const parts = [];
  if (caps.has("vision")) parts.push("image/*");
  if (caps.has("audio")) parts.push("audio/*");
  return parts.join(",");
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
    return { view: "detail", filename: decodeURIComponent(p.substring(6)) };
  }
  return "list";
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
  if (createBox) createBox.hidden = sub !== "create";
  if (detailBox) detailBox.hidden = !(sub && sub.view === "detail");
  if (listWrap) listWrap.hidden = sub !== "list";
  if (headActions) headActions.style.display = sub === "list" ? "" : "none";
  if (sub === "list") {
    void ragRefreshList();
  } else if (sub === "create") {
    ragStartCreate();
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
    if (e.media_type && e.media_type !== "text") {
      item.appendChild(ragEl("div", "rag-entry-media muted small",
        (e.media_name || e.media_type) + " · " + (e.media_mime || e.media_type) + " · " + ragFmtBytes(e.media_size || 0)));
    }
    box.appendChild(item);
  }
}

function ragNewEntry() {
  return { key: ++ragState.entrySeq, term: "", content: "", media: null, mediaPending: false, mediaError: "" };
}

function ragStartCreate() {
  if (!ragState.entries.length) ragState.entries = [ragNewEntry()];
  const sel = document.getElementById("rag-new-model");
  const def = (currentConfig && currentConfig.rag && currentConfig.rag.default_embedding) || "";
  if (sel && !sel.value && def) {
    const o = ragEl("option", "", def);
    o.value = def;
    sel.appendChild(o);
    sel.value = def;
  }
  void ragLoadModels().then(() => ragRebuildModelSelect());
  ragRebuildModelSelect();
  ragRenderEntries();
}

function ragRebuildModelSelect() {
  const sel = document.getElementById("rag-new-model");
  if (!sel) return;
  const wanted = sel.value;
  sel.innerHTML = "";
  const none = ragEl("option", "", ragState.modelsLoading ? t("rag.loading_models") : t("rag.no_default"));
  none.value = "";
  sel.appendChild(none);
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
  sel.value = wanted;
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
    const def = (currentConfig && currentConfig.rag && currentConfig.rag.default_embedding) || "";
    if (def) hint.appendChild(ragEl("div", "form-hint", t("rag.default_model_hint", { model: def })));
  }
}

function ragCreateCaps() {
  const sel = document.getElementById("rag-new-model");
  return ragModelCaps(sel ? sel.value : "");
}

function ragRenderEntries() {
  const host = document.getElementById("rag-entries");
  if (!host) return;
  host.innerHTML = "";
  const caps = ragCreateCaps();
  for (const entry of ragState.entries) {
    const card = ragEl("div", "rag-entry-card");
    const top = ragEl("div", "rag-entry-head");
    const term = ragEl("input", "rag-entry-term");
    term.type = "text";
    term.placeholder = t("rag.term_placeholder");
    term.maxLength = 256;
    term.value = entry.term;
    term.addEventListener("input", () => {
      entry.term = term.value;
    });
    top.appendChild(term);
    const rm = ragEl("button", "ghost small rag-entry-remove", "");
    rm.type = "button";
    rm.innerHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M18 6L6 18M6 6l12 12"/></svg>';
    rm.title = t("rag.remove_entry");
    rm.addEventListener("click", () => {
      if (ragBusy()) return;
      ragState.entries = ragState.entries.filter((x) => x !== entry);
      ragRenderEntries();
    });
    top.appendChild(rm);
    card.appendChild(top);

    const content = ragEl("textarea", "rag-entry-content");
    content.placeholder = t("rag.content_placeholder");
    content.rows = 3;
    content.value = entry.content;
    content.addEventListener("input", () => {
      entry.content = content.value;
    });
    card.appendChild(content);

    const mediaRow = ragEl("div", "rag-entry-media");
    const fileIn = ragEl("input", "");
    fileIn.type = "file";
    fileIn.style.display = "none";
    fileIn.accept = ragMediaAccept(caps);
    fileIn.addEventListener("change", () => {
      const f = fileIn.files && fileIn.files[0];
      fileIn.value = "";
      ragOnFileChosen(entry, f);
    });
    card.appendChild(fileIn);

    if (entry.media) {
      const chip = ragEl("span", "muted small",
        entry.media.name + " · " + entry.media.type + " · " + ragFmtBytes(entry.media.size) + " ");
      const clear = ragEl("button", "ghost small", t("rag.remove_attachment"));
      clear.type = "button";
      clear.addEventListener("click", () => {
        if (ragBusy()) return;
        entry.media = null;
        ragRenderEntries();
      });
      chip.appendChild(clear);
      mediaRow.appendChild(chip);
      if (!ragMediaAllowed(entry.media.type, caps)) {
        mediaRow.appendChild(ragEl("span", "form-hint", t("rag.media_unsupported")));
      }
    } else {
      const attach = ragEl("button", "ghost small", t("rag.attach"));
      attach.type = "button";
      attach.title = t("rag.attach_title");
      if (!ragMediaAccept(caps)) attach.disabled = true;
      attach.addEventListener("click", () => {
        const a = ragMediaAccept(ragCreateCaps());
        if (!a) {
          entry.mediaError = t("rag.no_media_cap");
          ragRenderEntries();
          return;
        }
        fileIn.accept = a;
        fileIn.click();
      });
      mediaRow.appendChild(attach);
    }
    if (entry.mediaPending) mediaRow.appendChild(ragEl("span", "muted small", t("rag.file_pending")));
    if (entry.mediaError) mediaRow.appendChild(ragEl("span", "form-hint", entry.mediaError));
    card.appendChild(mediaRow);
    host.appendChild(card);
  }
  const mediaHint = ragEl("p", "form-hint", t("rag.media_hint"));
  host.appendChild(mediaHint);
}

function ragOnFileChosen(entry, file) {
  if (!file) return;
  const caps = ragCreateCaps();
  const isImage = file.type.startsWith("image/");
  const isAudio = file.type.startsWith("audio/");
  const mediaType = isImage ? "image" : isAudio ? "audio" : "";
  if (!mediaType || !ragMediaAllowed(mediaType, caps)) {
    entry.mediaError = t("rag.media_bad_type");
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
    entry.media = {
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
    if (!e.term.trim()) err = t("rag.term_required");
    else if (e.media && !ragMediaAllowed(e.media.type, caps)) err = t("rag.media_unsupported");
    else if (!e.media && !e.content.trim()) err = t("rag.content_required");
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
    entries: ragState.entries.map((e) => {
      const row = { term: e.term, content: e.content };
      if (e.media) {
        row.media_type = e.media.type;
        row.media_name = e.media.name;
        row.media_mime = e.media.mime;
        row.media_base64 = e.media.base64;
      }
      return row;
    }),
  };
  ragState.creating = true;
  ragSetCreateDisabled(true);
  const btn = document.getElementById("rag-create-btn");
  if (btn) btn.textContent = t("rag.generating");
  try {
    await api("/api/rags", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    ragState.entries = [];
    const nameIn = document.getElementById("rag-new-name");
    const descIn = document.getElementById("rag-new-desc");
    if (nameIn) nameIn.value = "";
    if (descIn) descIn.value = "";
    toast(t("rag.created", { name }), "success");
    ragState.creating = false;
    if (btn) btn.textContent = t("rag.create_btn");
    ragNav("/rags");
    return;
  } catch (e) {
    ragSetCreateError(String(e && e.message ? e.message : e));
  } finally {
    ragState.creating = false;
    if (btn) btn.textContent = t("rag.create_btn");
    if (ragSubview() === "create") ragSetCreateDisabled(ragMediaPending());
  }
}

function ragSettingsPayload() {
  const body = {};
  if (!ragState.settingsLoading) {
    const sel = document.getElementById("set-rag-embedding");
    if (sel) body.default_embedding = sel.value;
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
  const none = ragEl("option", "", t("rag.loading_models"));
  none.value = "";
  sel.appendChild(none);
  if (saved) {
    const o = ragEl("option", "", saved);
    o.value = saved;
    sel.appendChild(o);
  }
  sel.value = saved;

  ragState.settingsLoading = true;
  ragLoadModels("settings").then((res) => {
    if (!res) return;
    ragState.settingsLoading = false;
    const current = sel.value;
    sel.innerHTML = "";
    const none2 = ragEl("option", "", t("rag.no_default"));
    none2.value = "";
    sel.appendChild(none2);
    for (const m of ragState.models) {
      const o = ragEl("option", "", m.name);
      o.value = m.name;
      sel.appendChild(o);
    }
    const wanted = ragState.settingsDirty ? current : ((currentConfig.rag && currentConfig.rag.default_embedding) || "");
    ragState.settingsMissingModel = "";
    if (wanted && !ragState.models.some((m) => m.name === wanted)) {
      ragState.settingsMissingModel = wanted;
      const o = ragEl("option", "", wanted + " (" + t("rag.model_missing") + ")");
      o.value = wanted;
      sel.appendChild(o);
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
  ragState.entries = [];
  ragSetCreateError("");
  const nameIn = document.getElementById("rag-new-name");
  const descIn = document.getElementById("rag-new-desc");
  const modelSel = document.getElementById("rag-new-model");
  if (nameIn) nameIn.value = "";
  if (descIn) descIn.value = "";
  if (modelSel) modelSel.value = "";
  ragNav("/rags");
});
document.getElementById("rag-add-entry-btn")?.addEventListener("click", () => {
  if (ragBusy()) return;
  ragState.entries.push(ragNewEntry());
  ragRenderEntries();
});
document.getElementById("rag-create-btn")?.addEventListener("click", () => void ragSubmitCreate());
document.getElementById("rag-detail-back-btn")?.addEventListener("click", () => ragNav("/rags"));
document.getElementById("rag-new-model")?.addEventListener("change", () => ragRenderEntries());
