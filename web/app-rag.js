"use strict";

const RAG_MAX_FILE_BYTES = 8 * 1024 * 1024;
const RAG_MAX_CONTENT_CHARS = 32000;
const RAG_MAX_ALIASES = 20;
const RAG_MAX_ALIAS_CHARS = 64;
const RAG_TRIM_WS = "\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000";
const ragTrimRe = new RegExp(`^[${RAG_TRIM_WS}]+|[${RAG_TRIM_WS}]+$`, "g");

function ragContentLength(value) {
  return Array.from(String(value || "").replace(ragTrimRe, "")).length;
}

function ragParseAliases(text) {
  const seen = new Set();
  const out = [];
  let tooLong = false;
  for (const part of String(text || "").split(/[,\r\n]+/)) {
    const a = part.replace(ragTrimRe, "");
    if (!a) continue;
    if (Array.from(a).length > RAG_MAX_ALIAS_CHARS) tooLong = true;
    const key = a.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(a);
  }
  let error = "";
  if (out.length > RAG_MAX_ALIASES) error = "rag.aliases_too_many";
  else if (tooLong) error = "rag.aliases_too_long";
  return { aliases: out, error };
}

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
  entries: [],
  entrySeq: 0,
  activeEntry: 0,
  chatListLoaded: false,
  chatImporting: false,
  pickerSelected: new Set(),
  externalFile: null,
  externalName: "",
  externalLoading: false,
  formGen: 0,
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
  if (p === "/rags/external") return "external";
  if (p.startsWith("/rags/") && p.length > 6) {
    let filename = decodeURIComponent(p.substring(6));
    if (filename.endsWith("/edit")) filename = filename.slice(0, -5);
    return { view: "edit", filename };
  }
  return "list";
}

function ragIsFormView(sub) {
  return sub === "create" || sub === "external" || (sub && sub.view === "edit");
}

function ragMediaPending() {
  return ragState.entries.some((e) => e.mediaPending);
}

function ragContentPending() {
  return ragState.entries.some((e) => e.contentPending);
}

function ragBusy() {
  return ragState.creating || ragMediaPending() || ragContentPending() || ragState.externalLoading;
}

function ragEntryError(entry) {
  const len = ragContentLength(entry.content);
  if (len > RAG_MAX_CONTENT_CHARS) {
    return {
      key: "rag.content_too_long",
      params: {
        max: RAG_MAX_CONTENT_CHARS.toLocaleString(),
        excess: (len - RAG_MAX_CONTENT_CHARS).toLocaleString(),
      },
    };
  }
  const parsed = ragParseAliases(entry.aliasesText);
  if (parsed.error) return { key: parsed.error, params: {} };
  return null;
}

function ragFormInvalid() {
  return ragState.entries.some((e) => ragEntryError(e) !== null);
}

function ragSyncSaveAvailability() {
  const disabled = ragBusy() || ragFormInvalid();
  for (const id of ["rag-create-btn", "rag-download-edited-btn"]) {
    const btn = document.getElementById(id);
    if (btn) btn.disabled = disabled;
  }
}

function ragUpdateEntryInvalidNav(entry) {
  const navList = document.getElementById("rag-entry-list");
  const navBtn = navList ? Array.from(navList.querySelectorAll(".rag-entry-nav"))
    .find((b) => b.dataset.entryKey === String(entry.key)) : null;
  if (!navBtn) return;
  const err = ragEntryError(entry);
  navBtn.classList.toggle("over-limit", !!err);
  const label = err ? ragEntryTitle(entry, ragState.entries.indexOf(entry)) + " — " + t(err.key, err.params) : "";
  navBtn.title = label;
  navBtn.setAttribute("aria-label", label);
}

function ragSetCreateDisabled(disabled) {
  const box = document.getElementById("rag-create-box");
  if (!box) return;
  box.querySelectorAll("input, select, textarea, button").forEach((el) => {
    el.disabled = disabled;
  });
  for (const id of ["rags-new-btn", "rags-reload-btn", "rag-create-btn", "rag-create-cancel-btn", "rags-open-file-btn", "rag-download-edited-btn"]) {
    const btn = document.getElementById(id);
    if (btn) btn.disabled = disabled;
  }
  if (!disabled) ragSyncSaveAvailability();
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
  if (typeof chatRagRenderSelection === "function") chatRagRenderSelection();
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
  let sub = ragSubview();
  if (sub === "external" && !ragState.externalFile) {
    history.replaceState(null, "", "/rags");
    sub = "list";
  }
  const createBox = document.getElementById("rag-create-box");
  const listWrap = document.getElementById("rags-list-wrap");
  const isForm = ragIsFormView(sub);
  if (createBox) createBox.hidden = !isForm;
  if (listWrap) listWrap.hidden = sub !== "list";
  for (const id of ["rags-reload-btn", "rags-new-btn", "rags-open-file-btn"]) {
    const el = document.getElementById(id);
    if (el) el.hidden = isForm;
  }
  for (const id of ["rag-create-cancel-btn", "rag-create-btn"]) {
    const el = document.getElementById(id);
    if (el) el.hidden = !isForm;
  }
  const dlBtn = document.getElementById("rag-download-edited-btn");
  if (dlBtn) dlBtn.hidden = !(sub === "external" && ragState.externalFile);
  const extHint = document.getElementById("rag-external-hint");
  if (extHint) extHint.hidden = !(sub === "external" && ragState.externalFile);
  if (sub === "list") {
    ++ragState.formGen;
    if (ragState.externalFile || ragState.externalLoading) {
      ragResetExternal();
      ragState.entries = [];
      ragState.activeEntry = 0;
      ragState.editingFilename = "";
      ragState.createPreferredModel = ragConfiguredDefault();
      const nameIn = document.getElementById("rag-new-name");
      const descIn = document.getElementById("rag-new-desc");
      if (nameIn) nameIn.value = "";
      if (descIn) descIn.value = "";
      const meta = document.getElementById("rag-edit-meta");
      if (meta) {
        meta.textContent = "";
        meta.hidden = true;
      }
    }
    void ragRefreshList();
  } else if (sub === "create") {
    ragStartCreate();
  } else if (sub === "external") {
    ragStartExternal();
  } else if (sub && sub.view === "edit") {
    void ragStartEdit(sub.filename);
  }
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
  const exportTitle = t("rag.export_title");
  const deleteTitle = t("rag.delete_title");
  const editTitle = t("rag.edit_title");
  for (const r of ragState.list) {
    const filename = String(r.filename || "");
    const name = String(r.name || filename);
    const model = String(r.embedding_model || "—");
    const desc = String(r.description || "").trim();
    const created = Number(r.created_at) || 0;
    const updated = Number(r.updated_at) || created;
    const createdISO = created ? new Date(created * 1000).toISOString() : "";
    const updatedISO = updated ? new Date(updated * 1000).toISOString() : "";
    const tr = ragEl("tr", "row rag-row");
    tr.dataset.filename = filename;
    tr.title = editTitle;
    tr.innerHTML = `
      <td class="col-state"><span class="state-dot loaded" title="${escapeHtml(t("rag.ready"))}"></span></td>
      <td class="cell-name rag-name-cell">
        <div class="model-name-wrap">
          <div class="model-name-block">
            <div class="model-name model-name-track"><span class="model-name-text"><span class="model-name-base">${escapeHtml(name)}</span></span></div>
            ${desc ? `<div class="rag-desc-line">${escapeHtml(desc)}</div>` : ""}
          </div>
        </div>
      </td>
      <td><span class="badge badge-muted rag-model-pill" title="${escapeHtml(model)}">${escapeHtml(model)}</span></td>
      <td class="cell-size">${r.dimensions || "—"}</td>
      <td class="cell-size">${r.entries || 0}</td>
      <td class="cell-size rag-size-cell" title="${escapeHtml(filename)}">${fmtBytes(Number(r.size_bytes) || 0)}</td>
      <td class="cell-modified"><div class="cell-dates"><div class="date-primary" title="${escapeHtml(ragFmtDate(created))}">${escapeHtml(created ? fmtDate(createdISO) : "—")}</div></div></td>
      <td class="cell-modified"><div class="cell-dates"><div class="date-primary" title="${escapeHtml(ragFmtDate(updated))}">${escapeHtml(updated ? fmtDate(updatedISO) : "—")}</div></div></td>
      <td class="rag-actions-cell">
        <div class="rag-row-actions">
          <a class="btn-icon rag-export-btn" href="/api/rags/${encodeURIComponent(filename)}/download" download="${escapeHtml(filename)}" title="${escapeHtml(exportTitle)}" aria-label="${escapeHtml(exportTitle)}">⬇</a>
          <button type="button" class="btn-icon delete-btn" title="${escapeHtml(deleteTitle)}" aria-label="${escapeHtml(deleteTitle)}">×</button>
        </div>
      </td>`;
    tr.addEventListener("click", (ev) => {
      if (ev.target.closest(".rag-row-actions")) return;
      ragNav("/rags/" + encodeURIComponent(filename) + "/edit");
    });
    const exportLink = tr.querySelector(".rag-export-btn");
    exportLink.addEventListener("click", (ev) => ev.stopPropagation());
    const deleteBtn = tr.querySelector(".delete-btn");
    deleteBtn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      void ragDelete(r);
    });
    tbody.appendChild(tr);
  }
}

async function ragDelete(r) {
  const filename = String(r.filename || "");
  const name = String(r.name || filename);
  if (!filename) return;
  const { ok } = await askConfirm({
    title: t("rag.delete_title"),
    text: t("rag.delete_confirm", { name }),
    okText: t("action.delete"),
    okClass: "danger",
    mono: name,
  });
  if (!ok) return;
  try {
    await api("/api/rags/" + encodeURIComponent(filename), { method: "DELETE" });
    toast(t("rag.deleted", { name }), "success");
    if (chatRagPaths.includes(filename)) {
      chatRagPaths = chatRagPaths.filter((p) => p !== filename);
      chatRagCommit();
    }
    await ragRefreshList();
  } catch (e) {
    toast(String(e && e.message ? e.message : e), "error");
  }
}

function ragNewEntry() {
  const key = ++ragState.entrySeq;
  ragState.activeEntry = key;
  return { key, id: 0, term: "", content: "", inputMode: "combined", aliasesText: "", createdAt: 0, updatedAt: 0, media: {}, mediaPending: false, mediaError: "" };
}

function ragConfiguredDefault() {
  return (currentConfig && currentConfig.rag && currentConfig.rag.default_embedding) || ragState.defaultEmbedding || "";
}

function ragGoToRequiredSettings() {
  history.pushState(null, "", "/settings/rag");
  void showSettingsView();
}

async function ragEnsureDefaultModel() {
  const gen = ragState.formGen;
  const path = location.pathname;
  const res = await ragLoadModels();
  if (!res || gen !== ragState.formGen || location.pathname !== path) return;
  if (!ragState.editingFilename && !ragState.externalFile && !ragState.defaultEmbedding) {
    toast(t("rag.default_required"), "error");
    ragGoToRequiredSettings();
    return;
  }
  if (!ragState.createPreferredModel) {
    ragState.createPreferredModel = ragState.editingFilename || ragState.externalFile ? "" : ragState.defaultEmbedding;
  }
  ragRebuildModelSelect();
  ragRenderEntries();
}

function ragResetExternal() {
  ragState.externalFile = null;
  ragState.externalName = "";
  ragState.externalLoading = false;
}

function ragStartCreate() {
  ++ragState.formGen;
  const wasEditing = !!(ragState.editingFilename || ragState.externalFile || ragState.externalLoading);
  ragResetExternal();
  ragState.editingFilename = "";
  ragState.createPreferredModel = ragConfiguredDefault();
  const meta = document.getElementById("rag-edit-meta");
  if (meta) {
    meta.textContent = "";
    meta.hidden = true;
  }
  if (wasEditing) {
    ragState.entries = [];
    const nameIn = document.getElementById("rag-new-name");
    const descIn = document.getElementById("rag-new-desc");
    if (nameIn) nameIn.value = "";
    if (descIn) descIn.value = "";
  }
  if (!ragState.entries.length) ragState.entries = [ragNewEntry()];
  if (!ragState.entries.some((e) => e.key === ragState.activeEntry)) {
    ragState.activeEntry = ragState.entries[0].key;
  }
  ragRebuildModelSelect();
  ragRenderEntries();
  const btn = document.getElementById("rag-create-btn");
  if (btn) {
    btn.dataset.i18n = "rag.create_btn";
    btn.textContent = t("rag.create_btn");
  }
  void ragEnsureDefaultModel();
}

function ragApplyDetail(detail, displayFilename) {
  const meta = document.getElementById("rag-edit-meta");
  const nameIn = document.getElementById("rag-new-name");
  const descIn = document.getElementById("rag-new-desc");
  if (nameIn) nameIn.value = detail.meta && detail.meta.name || "";
  if (descIn) descIn.value = detail.meta && detail.meta.description || "";
  if (meta) {
    const created = Number(detail.meta && detail.meta.created_at) || 0;
    const updated = Number(detail.meta && detail.meta.updated_at) || created;
    meta.textContent = [
      displayFilename,
      fmtBytes(Number(detail.size_bytes) || 0),
      t("rag.meta_created", { date: created ? ragFmtDate(created) : "—" }),
      t("rag.meta_updated", { date: updated ? ragFmtDate(updated) : "—" }),
    ].join(" · ");
    meta.hidden = false;
  }
  ragState.createPreferredModel = detail.meta && detail.meta.embedding_model || "";
  ragState.entries = (detail.entries || []).map((e) => {
    const entry = {
      key: ++ragState.entrySeq,
      id: Number(e.id) || 0,
      term: e.term || "",
      content: e.content || "",
      inputMode: e.input_mode || "combined",
      aliasesText: (e.aliases || []).join(", "),
      createdAt: Number(e.created_at) || 0,
      updatedAt: Number(e.updated_at) || 0,
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
        previewBase64: m.base64 || "",
      };
    }
    return entry;
  });
  ragState.activeEntry = ragState.entries[0] ? ragState.entries[0].key : 0;
}

async function ragStartEdit(filename) {
  const gen = ++ragState.formGen;
  const originPath = window.location.pathname;
  ragResetExternal();
  const btn = document.getElementById("rag-create-btn");
  if (btn) {
    btn.dataset.i18n = "rag.save_btn";
    btn.textContent = t("rag.save_btn");
  }
  ragState.editingFilename = filename;
  ragState.createPreferredModel = "";
  ragState.entries = [];
  ragState.activeEntry = 0;
  ragSetCreateError(t("rag.loading"));
  ragSetCreateDisabled(true);
  const meta = document.getElementById("rag-edit-meta");
  if (meta) {
    meta.textContent = "";
    meta.hidden = true;
  }
  try {
    const detail = await api("/api/rags/" + encodeURIComponent(filename));
    if (gen !== ragState.formGen || window.location.pathname !== originPath) return;
    ragApplyDetail(detail, filename);
    ragSetCreateError("");
    await ragLoadModels();
    if (gen !== ragState.formGen || window.location.pathname !== originPath) return;
    ragRebuildModelSelect();
    ragRenderEntries();
  } catch (e) {
    if (gen === ragState.formGen && window.location.pathname === originPath) {
      ragSetCreateError(String(e && e.message ? e.message : e));
    }
  } finally {
    if (gen === ragState.formGen) ragSetCreateDisabled(false);
  }
}

function ragStartExternal() {
  ragState.editingFilename = "";
  const btn = document.getElementById("rag-create-btn");
  if (btn) {
    btn.dataset.i18n = "rag.save_local";
    btn.textContent = t("rag.save_local");
  }
  ragRebuildModelSelect();
  ragRenderEntries();
}

async function ragOpenExternalFile(file) {
  if (!file || ragBusy()) return;
  if (!ragSafeFilename(file.name)) {
    toast(t("rag.import_bad_file"), "error");
    return;
  }
  const gen = ++ragState.formGen;
  const originPath = window.location.pathname;
  ragState.externalLoading = true;
  ragSetCreateDisabled(true);
  try {
    const detail = await api("/api/rags/external/open?name=" + encodeURIComponent(file.name), {
      method: "POST",
      headers: { "Content-Type": "application/octet-stream" },
      body: file,
    });
    if (gen !== ragState.formGen || window.location.pathname !== originPath) return;
    ragState.externalFile = file;
    ragState.externalName = file.name;
    ragState.editingFilename = "";
    ragApplyDetail(detail, file.name);
    ragSetCreateError("");
    await ragLoadModels();
    if (gen !== ragState.formGen || window.location.pathname !== originPath) return;
    ragRebuildModelSelect();
    ragNav("/rags/external");
  } catch (e) {
    if (gen === ragState.formGen && window.location.pathname === originPath) {
      toast(String(e && e.message ? e.message : e), "error");
    }
  } finally {
    if (gen === ragState.formGen) {
      ragState.externalLoading = false;
      ragSetCreateDisabled(ragBusy());
    }
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
    if (!ragState.editingFilename && !ragState.externalFile && ragState.defaultEmbedding) {
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
  if (media.previewBase64) {
    return "data:" + (media.mime || "application/octet-stream") + ";base64," + media.previewBase64;
  }
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
    const entryErr = ragEntryError(entry);
    if (entryErr) {
      btn.classList.add("over-limit");
      btn.title = ragEntryTitle(entry, idx) + " — " + t(entryErr.key, entryErr.params);
      btn.setAttribute("aria-label", btn.title);
    }
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
  const titleField = ragEl("div", "rag-entry-title-field");
  const termLabel = ragEl("label", "", t("rag.term_label"));
  termLabel.setAttribute("for", "rag-term-" + entry.key);
  const term = ragEl("input", "rag-entry-term");
  term.id = "rag-term-" + entry.key;
  term.type = "text";
  term.placeholder = t("rag.term_placeholder");
  term.maxLength = 256;
  term.value = entry.term;
  term.setAttribute("aria-describedby", "rag-term-hint-" + entry.key);
  term.addEventListener("input", () => {
    entry.term = term.value;
    ragUpdateEntryNav(entry);
  });
  titleField.appendChild(termLabel);
  titleField.appendChild(term);
  top.appendChild(titleField);
  if (entry.updatedAt) {
    const updatedISO = new Date(entry.updatedAt * 1000).toISOString();
    const stamp = ragEl("span", "rag-entry-updated", t("rag.entry_updated", { date: fmtDate(updatedISO) }));
    stamp.title = [
      t("rag.entry_created", { date: ragFmtDate(entry.createdAt || entry.updatedAt) }),
      t("rag.entry_updated", { date: ragFmtDate(entry.updatedAt) }),
    ].join(" · ");
    top.appendChild(stamp);
  }
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
  const termHint = ragEl("div", "form-hint", t("rag.term_hint"));
  termHint.id = "rag-term-hint-" + entry.key;
  textBox.appendChild(termHint);
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

  const aliasId = "rag-aliases-" + entry.key;
  const aliasHead = ragEl("div", "chat-system-head rag-aliases-head");
  const aliasLabel = ragEl("label", "", t("rag.aliases_label"));
  aliasLabel.setAttribute("for", aliasId);
  aliasHead.appendChild(aliasLabel);
  const aliasCount = ragEl("span", "rag-content-count rag-alias-count", "");
  aliasCount.setAttribute("aria-live", "polite");
  aliasHead.appendChild(aliasCount);
  textBox.appendChild(aliasHead);
  const aliasTa = ragEl("textarea", "rag-entry-aliases");
  aliasTa.id = aliasId;
  aliasTa.rows = 2;
  aliasTa.placeholder = t("rag.aliases_placeholder");
  aliasTa.value = entry.aliasesText || "";
  aliasTa.setAttribute("aria-describedby", aliasId + "-hint " + aliasId + "-err");
  const aliasWarn = ragEl("div", "form-hint rag-content-warn", "");
  aliasWarn.id = aliasId + "-err";
  aliasWarn.hidden = true;
  const aliasHint = ragEl("div", "form-hint", "");
  aliasHint.id = aliasId + "-hint";
  const syncAliasState = () => {
    const parsed = ragParseAliases(aliasTa.value);
    aliasCount.textContent = t("rag.aliases_count", { count: parsed.aliases.length });
    const over = !!parsed.error;
    aliasCount.classList.toggle("rag-limit-over", over);
    aliasTa.classList.toggle("rag-limit-over", over);
    aliasTa.setAttribute("aria-invalid", over ? "true" : "false");
    aliasWarn.hidden = !over;
    aliasWarn.textContent = over ? t(parsed.error) : "";
    aliasHint.textContent = t("rag.aliases_hint") +
      (entry.inputMode === "media" ? " " + t("rag.aliases_media_hint") : "");
    ragUpdateEntryInvalidNav(entry);
    ragSyncSaveAvailability();
  };
  aliasTa.addEventListener("input", () => {
    entry.aliasesText = aliasTa.value;
    syncAliasState();
  });
  textBox.appendChild(aliasTa);
  textBox.appendChild(aliasWarn);
  textBox.appendChild(aliasHint);
  syncAliasState();

  const contentHead = ragEl("div", "chat-system-head rag-content-head");
  const contentLabel = ragEl("label", "", t("rag.content_label"));
  contentLabel.setAttribute("for", "rag-content-" + entry.key);
  contentHead.appendChild(contentLabel);
  const contentCount = ragEl("span", "rag-content-count", "");
  contentCount.setAttribute("aria-live", "polite");
  contentCount.title = t("rag.content_count_hint");
  contentHead.appendChild(contentCount);
  const contentBtn = ragEl("button", "ghost chat-system-file-btn rag-content-file-btn", "");
  contentBtn.type = "button";
  contentBtn.innerHTML = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/><path d="M12 18v-6"/><path d="M9 15l3-3 3 3"/></svg>';
  contentBtn.title = t("rag.load_content");
  contentBtn.setAttribute("aria-label", t("rag.load_content"));
  const contentFile = ragEl("input", "rag-content-file-input");
  contentFile.type = "file";
  contentFile.accept = ".txt,.md,.markdown,.json,.yaml,.yml,.prompt,.py,.js,.ts,.html,.css,*/*";
  contentFile.style.display = "none";
  contentFile.addEventListener("change", () => {
    const f = contentFile.files && contentFile.files[0];
    contentFile.value = "";
    ragLoadContentFile(entry, f);
  });
  contentBtn.addEventListener("click", (ev) => {
    ev.preventDefault();
    contentFile.click();
  });
  contentHead.appendChild(contentBtn);
  textBox.appendChild(contentHead);
  textBox.appendChild(contentFile);

  const content = ragEl("textarea", "rag-entry-content");
  content.id = "rag-content-" + entry.key;
  content.placeholder = t("rag.content_placeholder");
  content.rows = 14;
  content.value = entry.content;
  const contentWarn = ragEl("div", "form-hint rag-content-warn", "");
  contentWarn.hidden = true;
  const syncContentState = () => {
    const len = ragContentLength(content.value);
    const over = len > RAG_MAX_CONTENT_CHARS;
    const near = !over && len >= RAG_MAX_CONTENT_CHARS * 0.9;
    contentCount.textContent = t("rag.content_count", {
      count: len.toLocaleString(),
      max: RAG_MAX_CONTENT_CHARS.toLocaleString(),
    });
    contentCount.classList.toggle("rag-limit-warn", near);
    contentCount.classList.toggle("rag-limit-over", over);
    content.classList.toggle("rag-limit-warn", near);
    content.classList.toggle("rag-limit-over", over);
    content.setAttribute("aria-invalid", over ? "true" : "false");
    contentWarn.hidden = !over;
    contentWarn.textContent = over ? t("rag.content_too_long", {
      max: RAG_MAX_CONTENT_CHARS.toLocaleString(),
      excess: (len - RAG_MAX_CONTENT_CHARS).toLocaleString(),
    }) : "";
    ragUpdateEntryInvalidNav(entry);
    ragSyncSaveAvailability();
  };
  content.addEventListener("input", () => {
    entry.content = content.value;
    syncContentState();
  });
  syncContentState();
  content.addEventListener("dragenter", (e) => {
    if (e.dataTransfer?.types?.includes("Files")) {
      e.preventDefault();
      e.stopPropagation();
      content.classList.add("drag-over");
    }
  });
  content.addEventListener("dragover", (e) => {
    if (e.dataTransfer?.types?.includes("Files")) {
      e.preventDefault();
      e.stopPropagation();
      e.dataTransfer.dropEffect = "copy";
    }
  });
  content.addEventListener("dragleave", (e) => {
    if (e.dataTransfer?.types?.includes("Files")) {
      e.preventDefault();
      e.stopPropagation();
      content.classList.remove("drag-over");
    }
  });
  content.addEventListener("drop", (e) => {
    const files = Array.from(e.dataTransfer?.files || []);
    if (!files.length) return;
    e.preventDefault();
    e.stopPropagation();
    content.classList.remove("drag-over");
    ragLoadContentFile(entry, files[0]);
  });
  textBox.appendChild(content);
  textBox.appendChild(contentWarn);
  grid.appendChild(textBox);

  const mediaBox = ragEl("div", "rag-entry-media-grid");
  mediaBox.appendChild(ragRenderMediaSlot(entry, "image", caps, fileIn));
  mediaBox.appendChild(ragRenderMediaSlot(entry, "audio", caps, fileIn));
  grid.appendChild(mediaBox);
  card.appendChild(grid);

  if (entry.mediaPending || entry.contentPending) card.appendChild(ragEl("div", "rag-entry-media muted small", t("rag.file_pending")));
  if (entry.mediaError) card.appendChild(ragEl("div", "rag-entry-media form-hint", entry.mediaError));
  host.appendChild(card);
  host.appendChild(ragEl("p", "form-hint", t("rag.media_hint")));
  ragSyncSaveAvailability();
}

function ragLoadContentFile(entry, file) {
  if (!file || ragBusy()) return;
  entry.contentPending = true;
  ragSetCreateDisabled(true);
  const fail = () => {
    entry.contentPending = false;
    toast(t("rag.file_read_error"), "error");
    ragRenderEntries();
    ragSetCreateDisabled(ragBusy());
  };
  const reader = new FileReader();
  reader.onload = () => {
    entry.contentPending = false;
    if (typeof reader.result === "string" && ragState.entries.includes(entry)) {
      entry.content = reader.result;
    }
    ragRenderEntries();
    ragSetCreateDisabled(ragBusy());
  };
  reader.onerror = fail;
  reader.onabort = fail;
  try {
    reader.readAsText(file);
  } catch {
    fail();
  }
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
    ragSetCreateDisabled(ragBusy());
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
    ragSetCreateDisabled(ragBusy());
  };
  reader.readAsDataURL(file);
}

function ragSetCreateError(msg) {
  const st = document.getElementById("rag-create-status");
  if (!st) return;
  st.textContent = msg || "";
  st.hidden = !msg;
}

async function ragSubmitCreate(destination = "local") {
  if (ragBusy()) {
    if (ragMediaPending() || ragContentPending()) ragSetCreateError(t("rag.file_pending"));
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
  if (!err) {
    const badIdx = ragState.entries.findIndex((e) => ragEntryError(e) !== null);
    if (badIdx >= 0) {
      const bad = ragState.entries[badIdx];
      const badErr = ragEntryError(bad);
      if (badErr.key === "rag.content_too_long") {
        err = t("rag.content_too_long_entry", {
          entry: ragEntryTitle(bad, badIdx),
          max: RAG_MAX_CONTENT_CHARS.toLocaleString(),
        });
      } else {
        err = ragEntryTitle(bad, badIdx) + ": " + t(badErr.key, badErr.params);
      }
      ragSetActiveEntry(bad.key);
      const focusSel = badErr.key === "rag.aliases_too_many" || badErr.key === "rag.aliases_too_long"
        ? "textarea.rag-entry-aliases" : "textarea.rag-entry-content";
      document.querySelector(focusSel)?.focus();
    }
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
      id: e.id || 0,
      term: e.term,
      content: e.content,
      input_mode: e.inputMode,
      aliases: ragParseAliases(e.aliasesText).aliases,
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
  const external = !!ragState.externalFile;
  if (btn) btn.textContent = editing || external ? t("rag.saving") : t("rag.generating");
  try {
    if (external && destination === "download") {
      const fd = new FormData();
      fd.append("file", ragState.externalFile);
      fd.append("changes", JSON.stringify(payload));
      fd.append("destination", "download");
      const blob = await api("/api/rags/external/save", { method: "POST", body: fd }, "blob");
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = ragState.externalName || "rag.db";
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 0);
      toast(t("rag.downloaded"), "success");
      return;
    }
    let toastKey = editing ? "rag.updated" : "rag.created";
    let toastName = name;
    if (external) {
      const fd = new FormData();
      fd.append("file", ragState.externalFile);
      fd.append("changes", JSON.stringify(payload));
      fd.append("destination", "local");
      const res = await api("/api/rags/external/save", { method: "POST", body: fd });
      toastKey = "rag.saved_local";
      toastName = (res && res.rag && res.rag.name) || name;
    } else {
      await api(editing ? "/api/rags/" + encodeURIComponent(editing) : "/api/rags", {
        method: editing ? "PUT" : "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
    }
    ragState.entries = [];
    ragState.activeEntry = 0;
    ragState.editingFilename = "";
    ragState.createPreferredModel = "";
    ragResetExternal();
    const nameIn = document.getElementById("rag-new-name");
    const descIn = document.getElementById("rag-new-desc");
    if (nameIn) nameIn.value = "";
    if (descIn) descIn.value = "";
    toast(t(toastKey, { name: toastName }), "success");
    ragNav("/rags");
    return;
  } catch (e) {
    ragSetCreateError(String(e && e.message ? e.message : e));
  } finally {
    ragState.creating = false;
    if (btn) {
      const key = ragState.externalFile ? "rag.save_local" : ragState.editingFilename ? "rag.save_btn" : "rag.create_btn";
      btn.dataset.i18n = key;
      btn.textContent = t(key);
    }
    ragSetCreateDisabled(ragBusy());
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
    if (typeof updateChatRagAvailability === "function") updateChatRagAvailability();
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

// ---------- chat-side RAG selection ----------

const CHAT_RAG_MAX_SELECTED = 32;

function ragSafeFilename(name) {
  const s = String(name || "").trim();
  return !!s && s !== "." && s !== ".." &&
    !/[\\/]/.test(s) && !s.includes("\0") &&
    !s.startsWith(".") && !s.startsWith("~") &&
    /\.db$/i.test(s);
}

function normalizeChatRagPaths(list) {
  const out = [];
  const seen = new Set();
  for (const item of Array.isArray(list) ? list : []) {
    const name = String(item || "").trim();
    if (!ragSafeFilename(name) || seen.has(name)) continue;
    seen.add(name);
    out.push(name);
    if (out.length >= CHAT_RAG_MAX_SELECTED) break;
  }
  return out;
}

function normalizeChatRagEditable(list, paths) {
  return normalizeChatRagPaths(list).filter((p) => paths.includes(p));
}

function chatRagLoadLocal() {
  try {
    const parsed = JSON.parse(localStorage.getItem(CHAT_RAG_STATE_KEY) || "null");
    chatRagEnabled = !!(parsed && parsed.enabled);
    chatRagPaths = normalizeChatRagPaths(parsed && parsed.paths);
    chatRagEditable = normalizeChatRagEditable(parsed && parsed.editable, chatRagPaths);
  } catch {
    chatRagEnabled = false;
    chatRagPaths = [];
    chatRagEditable = [];
  }
  if (!chatRagEnabled) {
    chatRagPaths = [];
    chatRagEditable = [];
  }
}

function chatRagOptionPayload() {
  return {
    rag_enabled: !!chatRagEnabled,
    rag_paths: chatRagEnabled ? [...chatRagPaths] : [],
    rag_editable: chatRagEnabled ? [...chatRagEditable] : [],
  };
}

function chatRagPersistLocal() {
  try {
    localStorage.setItem(CHAT_RAG_STATE_KEY, JSON.stringify({
      enabled: !!chatRagEnabled,
      paths: chatRagEnabled ? [...chatRagPaths] : [],
      editable: chatRagEnabled ? [...chatRagEditable] : [],
    }));
  } catch { }
}

async function chatRagSyncSession() {
  if (typeof chatSessionId === "undefined" || !chatSessionId) return;
  try {
    await api(`/api/chat/sessions/${encodeURIComponent(chatSessionId)}/settings`, {
      method: "PATCH",
      body: chatRagOptionPayload(),
    });
  } catch (e) {
    toast(String(e && e.message ? e.message : e), "error");
  }
}

function chatRagInfo(filename) {
  return (ragState.list || []).find((r) => r.filename === filename) || null;
}

function updateChatRagAvailability() {
  const wrap = document.getElementById("chat-rag-wrap");
  const panel = document.getElementById("chat-rag-panel");
  if (!wrap || !panel) return;
  const model = document.getElementById("chat-model")?.value || "";
  const caps = typeof modelCaps === "function" ? modelCaps(model) : new Set();
  const imageOnly = typeof isImageGenerationOnlyCaps === "function" && isImageGenerationOnlyCaps(caps);
  wrap.hidden = imageOnly;
  panel.hidden = wrap.hidden || !chatRagEnabled;
  const actions = document.getElementById("chat-rag-actions");
  if (actions) actions.hidden = panel.hidden;
}

function chatRagRenderSelection() {
  const input = document.getElementById("chat-rag");
  const panel = document.getElementById("chat-rag-panel");
  const list = document.getElementById("chat-rag-list");
  if (input) input.checked = !!chatRagEnabled;
  if (!panel || !list) {
    updateChatRagAvailability();
    return;
  }
  panel.hidden = !chatRagEnabled;
  const actions = document.getElementById("chat-rag-actions");
  if (actions) actions.hidden = panel.hidden;
  list.innerHTML = "";
  if (chatRagEnabled && !chatRagPaths.length) {
    list.appendChild(ragEl("div", "chat-rag-empty muted", t("rag.chat_empty")));
  }
  for (const filename of chatRagPaths) {
    const info = chatRagInfo(filename);
    const row = ragEl("div", "chat-rag-item");
    const main = ragEl("div", "chat-rag-item-main");
    const title = ragEl("div", "chat-rag-item-title", info ? (info.name || filename) : filename);
    const description = (info && info.description || "").trim();
    row.title = [description, filename, info && info.embedding_model].filter(Boolean).join("\n");
    const entries = info ? t("rag.entries_count", { count: info.entries || 0 }) : "";
    const metaText = !info ? t("rag.chat_pending") : (description ? `${description} · ${entries}` : entries);
    main.appendChild(title);
    main.appendChild(ragEl("div", "chat-rag-item-meta", metaText));
    const remove = ragEl("button", "chat-rag-remove", "×");
    remove.type = "button";
    remove.dataset.filename = filename;
    remove.title = t("rag.chat_remove");
    remove.setAttribute("aria-label", t("rag.chat_remove"));
    const edit = ragEl("label", "chat-rag-edit");
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.className = "chat-rag-edit-input";
    cb.checked = chatRagEditable.includes(filename);
    cb.dataset.filename = filename;
    edit.title = t("rag.chat_editable");
    edit.setAttribute("aria-label", t("rag.chat_editable"));
    edit.appendChild(cb);
    edit.appendChild(ragEl("span", "chat-rag-edit-text", t("rag.chat_editable_short")));
    row.appendChild(main);
    row.appendChild(edit);
    row.appendChild(remove);
    list.appendChild(row);
  }
  const importBtn = document.getElementById("chat-rag-file-btn");
  if (importBtn) {
    importBtn.disabled = ragState.chatImporting;
    const label = ragState.chatImporting ? t("rag.importing") : t("rag.load_file");
    importBtn.title = label;
    importBtn.setAttribute("aria-label", label);
  }
  updateChatRagAvailability();
}

function chatRagCommit(syncSession = true, saveModelOptions = true) {
  if (!chatRagEnabled) chatRagPaths = [];
  chatRagPaths = normalizeChatRagPaths(chatRagPaths);
  chatRagEditable = chatRagEnabled ? normalizeChatRagEditable(chatRagEditable, chatRagPaths) : [];
  chatRagPersistLocal();
  chatRagRenderSelection();
  if (saveModelOptions && typeof saveChatOptionsForCurrentModel === "function") saveChatOptionsForCurrentModel();
  if (syncSession) void chatRagSyncSession();
  if (typeof adjustChatSystemPromptHeight === "function") adjustChatSystemPromptHeight();
}

function chatRagSetEnabled(enabled, saveModelOptions = true) {
  const next = !!enabled;
  chatRagEnabled = next;
  if (!next) {
    chatRagPaths = [];
    chatRagEditable = [];
  }
  chatRagCommit(true, saveModelOptions);
  if (next) {
    requestAnimationFrame(() => {
      const panel = document.getElementById("chat-rag-panel");
      const card = panel?.closest(".chat-side-card");
      if (card) card.scrollTop = card.scrollHeight;
      else panel?.scrollIntoView({ block: "end", inline: "nearest", behavior: "smooth" });
    });
    void chatRagValidateSelection(false);
  }
}

function chatRagApplyOptions(opts) {
  if (!opts) return;
  if (opts.rag_enabled !== undefined) {
    chatRagEnabled = !!opts.rag_enabled;
    if (opts.rag_paths === undefined) chatRagPaths = [];
  }
  if (opts.rag_paths !== undefined) chatRagPaths = normalizeChatRagPaths(opts.rag_paths);
  if (!chatRagEnabled) chatRagPaths = [];
  if (opts.rag_editable !== undefined) {
    chatRagEditable = normalizeChatRagEditable(opts.rag_editable, chatRagPaths);
  } else if (opts.rag_enabled !== undefined && opts.rag_paths === undefined) {
    chatRagEditable = [];
  }
  chatRagEditable = normalizeChatRagEditable(chatRagEditable, chatRagPaths);
  chatRagPersistLocal();
  chatRagRenderSelection();
  if (chatRagEnabled || opts.rag_paths !== undefined) void chatRagValidateSelection(true);
}

async function chatRagFetchList() {
  const res = await api("/api/rags");
  ragState.list = res.rags || [];
  ragState.warnings = res.warnings || [];
  if (Object.prototype.hasOwnProperty.call(res, "default_embedding")) {
    ragState.defaultEmbedding = res.default_embedding || "";
  }
  ragState.chatListLoaded = true;
  return ragState.list;
}

async function chatRagValidateSelection(syncSession) {
  try {
    const list = await chatRagFetchList();
    const valid = new Set(list.map((r) => r.filename));
    const kept = chatRagPaths.filter((p) => valid.has(p));
    const changed = kept.length !== chatRagPaths.length;
    chatRagPaths = kept;
    if (changed) chatRagCommit(syncSession);
    else chatRagRenderSelection();
  } catch {
    // Keep the saved names visible when the list cannot be checked; the next
    // successful refresh will drop missing files.
    chatRagRenderSelection();
  }
}

function chatRagAddPaths(paths) {
  if (!chatRagEnabled) chatRagEnabled = true;
  chatRagPaths = normalizeChatRagPaths([...chatRagPaths, ...paths]);
  chatRagCommit();
}

function chatRagRemovePath(filename) {
  chatRagPaths = chatRagPaths.filter((p) => p !== filename);
  chatRagCommit();
}

function chatRagSetEditable(filename, editable) {
  const name = String(filename || "").trim();
  if (!name || !chatRagPaths.includes(name)) return;
  if (editable) {
    if (!chatRagEditable.includes(name)) chatRagEditable.push(name);
  } else {
    chatRagEditable = chatRagEditable.filter((p) => p !== name);
  }
  chatRagCommit();
}

function chatRagRenderPicker(filter = "") {
  const listEl = document.getElementById("rag-picker-list");
  const addBtn = document.getElementById("rag-picker-add");
  const count = document.getElementById("rag-picker-count");
  if (!listEl) return;
  const q = String(filter || "").toLowerCase();
  const selectedNow = new Set(chatRagPaths);
  const filtered = (ragState.list || []).filter((r) => {
    const hay = `${r.filename || ""} ${r.name || ""} ${r.description || ""} ${r.embedding_model || ""}`.toLowerCase();
    return hay.includes(q);
  });
  listEl.innerHTML = "";
  if (!filtered.length) {
    listEl.appendChild(ragEl("div", "muted small", t("rag.picker_empty")));
  }
  for (const r of filtered) {
    const filename = String(r.filename || "");
    const already = selectedNow.has(filename);
    const item = ragEl("label", "prompts-modal-item rag-picker-item");
    const row = ragEl("div", "rag-picker-row");
    const check = ragEl("input", "rag-picker-check");
    check.type = "checkbox";
    check.dataset.filename = filename;
    check.checked = already || ragState.pickerSelected.has(filename);
    check.disabled = already;
    const body = ragEl("div", "rag-picker-body");
    const titleLine = ragEl("div", "rag-picker-title-line");
    titleLine.appendChild(ragEl("span", "rag-picker-title", r.name || filename));
    if (already) titleLine.appendChild(ragEl("span", "prompt-token-pill", t("rag.picker_added")));
    body.appendChild(titleLine);
    const desc = String(r.description || "").trim();
    if (desc) {
      body.appendChild(ragEl("div", "rag-picker-desc", desc));
    } else if (r.name && String(r.name).trim() && String(r.name) !== filename) {
      body.appendChild(ragEl("div", "rag-picker-filename mono muted", filename));
    }
    const meta = ragEl("div", "rag-picker-meta");
    const model = String(r.embedding_model || "—");
    const modelPill = ragEl("span", "badge badge-muted rag-model-pill mono", model);
    modelPill.title = model;
    meta.appendChild(modelPill);
    meta.appendChild(ragEl("span", "", `${r.dimensions || 0} dims`));
    meta.appendChild(ragEl("span", "rag-picker-entries", t("rag.picker_entries", { count: r.entries || 0 })));
    meta.appendChild(ragEl("span", "", ragFmtBytes(r.size_bytes) || "—"));
    body.appendChild(meta);
    row.appendChild(check);
    row.appendChild(body);
    const updated = Number(r.updated_at) || Number(r.created_at) || 0;
    const updatedSpan = ragEl("span", "rag-picker-date", t("rag.picker_updated", {
      date: updated ? fmtDate(new Date(updated * 1000).toISOString()) : "—",
    }));
    updatedSpan.title = ragFmtDate(updated);
    row.appendChild(updatedSpan);
    item.appendChild(row);
    listEl.appendChild(item);
  }
  if (count) {
    const n = ragState.pickerSelected.size;
    count.textContent = n ? t("rag.picker_selected", { count: n }) : "";
  }
  if (addBtn) addBtn.disabled = !ragState.pickerSelected.size;
}

async function openChatRagPicker() {
  const modal = document.getElementById("rag-picker-modal");
  if (!modal) return;
  ragState.pickerSelected = new Set();
  const search = document.getElementById("rag-picker-search");
  if (search) search.value = "";
  modal.hidden = false;
  chatRagRenderPicker();
  await chatRagValidateSelection(true);
  if (!ragState.chatListLoaded) {
    const listEl = document.getElementById("rag-picker-list");
    if (listEl) listEl.innerHTML = `<div class="muted small">${escapeHtml(t("rag.models_load_failed"))}</div>`;
  } else {
    chatRagRenderPicker();
  }
  if (search) search.focus();
}

function closeChatRagPicker() {
  const modal = document.getElementById("rag-picker-modal");
  if (modal) modal.hidden = true;
  ragState.pickerSelected = new Set();
}

async function chatRagImportFile(file) {
  if (!file) return;
  if (!ragSafeFilename(file.name)) {
    toast(t("rag.import_bad_file"), "error");
    return;
  }
  ragState.chatImporting = true;
  chatRagRenderSelection();
  try {
    const res = await api(`/api/rags/import?name=${encodeURIComponent(file.name)}`, {
      method: "POST",
      headers: {
        "Content-Type": "application/octet-stream",
        "X-RAG-Filename": file.name,
      },
      body: file,
    });
    const filename = res && res.rag && res.rag.filename;
    if (!ragSafeFilename(filename)) throw new Error(t("rag.import_bad_response"));
    try {
      await chatRagFetchList();
    } catch { }
    chatRagAddPaths([filename]);
    toast(t("rag.imported", { name: (res.rag && res.rag.name) || filename }), "success");
  } catch (e) {
    toast(String(e && e.message ? e.message : e), "error");
  } finally {
    ragState.chatImporting = false;
    chatRagRenderSelection();
  }
}

chatRagLoadLocal();
chatRagRenderSelection();
void chatRagValidateSelection(false);

document.getElementById("chat-rag")?.addEventListener("change", (ev) => {
  chatRagSetEnabled(ev.target.checked);
});
document.getElementById("chat-rag-picker-btn")?.addEventListener("click", () => {
  void openChatRagPicker();
});
document.getElementById("chat-rag-file-btn")?.addEventListener("click", () => {
  document.getElementById("chat-rag-file-input")?.click();
});
document.getElementById("chat-rag-file-input")?.addEventListener("change", async (ev) => {
  const file = ev.target.files && ev.target.files[0];
  ev.target.value = "";
  await chatRagImportFile(file);
});
document.getElementById("chat-rag-list")?.addEventListener("click", (ev) => {
  const btn = ev.target.closest(".chat-rag-remove");
  if (!btn) return;
  chatRagRemovePath(btn.dataset.filename || "");
});
document.getElementById("chat-rag-list")?.addEventListener("change", (ev) => {
  const check = ev.target.closest(".chat-rag-edit-input");
  if (!check) return;
  chatRagSetEditable(check.dataset.filename || "", check.checked);
});
document.getElementById("rag-picker-close")?.addEventListener("click", closeChatRagPicker);
document.getElementById("rag-picker-modal")?.addEventListener("click", (ev) => {
  if (ev.target && ev.target.id === "rag-picker-modal") closeChatRagPicker();
});
document.getElementById("rag-picker-search")?.addEventListener("input", (ev) => {
  const clear = document.getElementById("rag-picker-search-clear");
  if (clear) clear.hidden = !ev.target.value;
  chatRagRenderPicker(ev.target.value);
});
document.getElementById("rag-picker-search-clear")?.addEventListener("click", () => {
  const search = document.getElementById("rag-picker-search");
  if (search) search.value = "";
  document.getElementById("rag-picker-search-clear").hidden = true;
  chatRagRenderPicker();
  search?.focus();
});
document.getElementById("rag-picker-list")?.addEventListener("change", (ev) => {
  const check = ev.target.closest(".rag-picker-check");
  if (!check) return;
  const filename = check.dataset.filename || "";
  if (check.checked) ragState.pickerSelected.add(filename);
  else ragState.pickerSelected.delete(filename);
  chatRagRenderPicker(document.getElementById("rag-picker-search")?.value || "");
});
document.getElementById("rag-picker-add")?.addEventListener("click", () => {
  const paths = [...ragState.pickerSelected];
  if (!paths.length) return;
  chatRagAddPaths(paths);
  closeChatRagPicker();
});
document.addEventListener("keydown", (ev) => {
  if (ev.key !== "Escape") return;
  const modal = document.getElementById("rag-picker-modal");
  if (modal && !modal.hidden) closeChatRagPicker();
});

document.getElementById("rags-btn")?.addEventListener("click", () => ragNav("/rags"));
document.getElementById("rags-back-btn")?.addEventListener("click", () => {
  if (ragBusy()) return;
  showModelsView();
  history.pushState(null, "", "/");
});
document.getElementById("rags-reload-btn")?.addEventListener("click", () => void ragRefreshList());
document.getElementById("rags-new-btn")?.addEventListener("click", () => ragNav("/rags/new"));
document.getElementById("rags-open-file-btn")?.addEventListener("click", () => {
  if (ragBusy()) return;
  document.getElementById("rags-external-file-input")?.click();
});
document.getElementById("rags-external-file-input")?.addEventListener("change", (ev) => {
  const f = ev.target.files && ev.target.files[0];
  ev.target.value = "";
  void ragOpenExternalFile(f);
});
document.getElementById("rag-download-edited-btn")?.addEventListener("click", () => {
  void ragSubmitCreate("download");
});
document.getElementById("rag-create-cancel-btn")?.addEventListener("click", () => {
  if (ragBusy()) return;
  ++ragState.formGen;
  ragResetExternal();
  ragState.entries = [];
  ragState.activeEntry = 0;
  ragState.editingFilename = "";
  ragState.createPreferredModel = "";
  ragSetCreateError("");
  const nameIn = document.getElementById("rag-new-name");
  const descIn = document.getElementById("rag-new-desc");
  const modelSel = document.getElementById("rag-new-model");
  const meta = document.getElementById("rag-edit-meta");
  if (nameIn) nameIn.value = "";
  if (descIn) descIn.value = "";
  if (modelSel) modelSel.value = "";
  if (meta) {
    meta.textContent = "";
    meta.hidden = true;
  }
  ragNav("/rags");
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
