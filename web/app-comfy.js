"use strict";

// ---------- ComfyUI integration ----------
//
// Three surfaces live here: the chat toggle and workflow selector, the settings
// card that registers workflows, and the modal used to edit one of them.
//
// The chat side never talks to ComfyUI directly. It only tells the server which
// workflow to prefer; the tool schema, the queue call and the polling all happen
// server-side, because the manager has to keep the stored copy of every result.

// Whether the server has any workflow we could send. Cached because the chat
// options panel is re-rendered on every model switch and the endpoint is a
// network round trip.
let comfyWorkflowSummary = null;
let comfyWorkflowSummarySeq = 0;
let comfyStatusCache = null;
// The workflow the chat should use, held outside the <select> so it survives the
// window between restoring a session and fetching the workflow list.
let comfyChatWorkflowId = "";

function comfyApi(path, options) {
  return api("/api/comfyui" + path, options);
}

async function fetchComfyWorkflowSummary(force) {
  if (!force && comfyWorkflowSummary) return comfyWorkflowSummary;
  const seq = ++comfyWorkflowSummarySeq;
  try {
    const data = await comfyApi("/workflows/summary");
    if (seq !== comfyWorkflowSummarySeq) return comfyWorkflowSummary;
    comfyWorkflowSummary = {
      workflows: Array.isArray(data && data.workflows) ? data.workflows : [],
      configured: !!(data && data.configured),
      url: (data && data.url) || "",
      error: (data && data.error) || "",
    };
  } catch (e) {
    if (seq !== comfyWorkflowSummarySeq) return comfyWorkflowSummary;
    comfyWorkflowSummary = null;
  }
  return comfyWorkflowSummary;
}

function invalidateComfyCaches() {
  comfyWorkflowSummary = null;
  comfyStatusCache = null;
}

// ---------- chat options panel ----------

function updateComfyChatUI() {
  const wrap = $("chat-comfy-wrap");
  const selWrap = $("chat-comfy-workflow-wrap");
  const sel = $("chat-comfy-workflow");
  if (!wrap) return;

  const model = $("chat-model")?.value || "";
  const caps = modelCaps(model);
  const canTools = caps.has("tools");
  // Image-generation-only models have no tool loop at all, so offering ComfyUI
  // there would be a switch that does nothing.
  const isImageModel = isImageGenerationOnlyCaps(caps);

  const summary = comfyWorkflowSummary;
  const enabled = summary ? summary.workflows.filter((w) => w.enabled) : [];
  // No server URL or no enabled workflow means the toggle would be a lie.
  const usable = canTools && !isImageModel &&
    !!(summary && summary.configured) && enabled.length > 0;

  wrap.hidden = !canTools || isImageModel;
  if (selWrap) selWrap.hidden = !usable || enabled.length < 2;
  if (!sel || enabled.length < 2) return;

  // Rebuild only when the option set actually changed, so choosing a workflow
  // does not reset the select on every unrelated repaint.
  const wanted = enabled.map((w) => String(w.id));
  const have = Array.from(sel.options).map((o) => o.value).filter(Boolean);
  if (have.join(",") !== wanted.join(",")) {
    sel.innerHTML = "";
    const auto = document.createElement("option");
    auto.value = "";
    auto.textContent = t("chat.comfy_workflow_auto") || "Let the model choose";
    sel.appendChild(auto);
    enabled.forEach((w) => {
      const opt = document.createElement("option");
      opt.value = String(w.id);
      opt.textContent = w.name;
      // Tag the exotic kinds so "Portrait" and "Portrait (video)" do not read
      // as duplicates in the list.
      if (w.output_kind && w.output_kind !== "image" && w.output_kind !== "any") {
        opt.textContent += ` (${w.output_kind})`;
      }
      sel.appendChild(opt);
    });
  }
  // comfyChatWorkflowId is the source of truth, not sel.value: a session can be
  // restored before the workflow list has been fetched, and reading the select
  // back in that window would silently drop the stored selection.
  if (wanted.indexOf(comfyChatWorkflowId) >= 0) {
    sel.value = comfyChatWorkflowId;
  } else {
    comfyChatWorkflowId = "";
    sel.value = "";
  }
}

function comfyChatWorkflowValue() {
  const sel = $("chat-comfy-workflow");
  return comfyChatWorkflowId || (sel ? sel.value : "") || "";
}

function setComfyChatWorkflowValue(id) {
  comfyChatWorkflowId = id || "";
  const sel = $("chat-comfy-workflow");
  if (sel) sel.value = comfyChatWorkflowId;
}

async function refreshComfyChatUI(force) {
  const summary = await fetchComfyWorkflowSummary(force);
  updateComfyChatUI();
  return summary;
}

function onComfyToggleChanged() {
  // Turning the toggle on with no workflow selected is legal: the model then
  // picks among the enabled ones. But if nothing is registered at all the user
  // needs to be told why nothing happens.
  const on = !!$("chat-comfy")?.checked;
  const summary = comfyWorkflowSummary;
  const enabled = summary ? summary.workflows.filter((w) => w.enabled) : [];
  if (on && enabled.length === 0) {
    const msg = summary && summary.configured
      ? (t("chat.comfy_no_workflows") || "No ComfyUI workflow is enabled yet. Register one in Settings → ComfyUI.")
      : (t("chat.comfy_not_configured") || "ComfyUI is not configured yet. Add its server URL in Settings → ComfyUI.");
    toast(msg, "warn");
  }
  updateComfyChatUI();
  if (typeof saveChatOptionsForCurrentModel === "function") saveChatOptionsForCurrentModel();
}

function bindComfyChatEvents() {
  const cb = $("chat-comfy");
  if (cb) cb.addEventListener("change", onComfyToggleChanged);
  const sel = $("chat-comfy-workflow");
  if (sel) {
    sel.addEventListener("change", () => {
      setComfyChatWorkflowValue(sel.value);
      if (typeof saveChatOptionsForCurrentModel === "function") saveChatOptionsForCurrentModel();
    });
  }
}

// ---------- settings: connection ----------

function loadComfySettings(cfg) {
  const c = (cfg && cfg.comfyui) || {};
  const url = $("comfy-url");
  if (url) url.value = c.url || "";
  const cid = $("comfy-client-id");
  if (cid) cid.value = c.client_id || "";
  const to = $("comfy-timeout");
  if (to) to.value = c.timeout_seconds ? String(c.timeout_seconds) : "";
  invalidateComfyCaches();
  updateComfyChatUI();
  void loadComfyWorkflows();
}

function comfySettingsPayload() {
  const url = ($("comfy-url")?.value || "").trim();
  const payload = { url };
  const cid = ($("comfy-client-id")?.value || "").trim();
  payload.client_id = cid;
  const to = Math.round(Number($("comfy-timeout")?.value || 0));
  payload.timeout_seconds = to > 0 ? Math.min(3600, Math.max(10, to)) : 0;
  return payload;
}

async function testComfyConnection() {
  const btn = $("comfy-test-btn");
  const out = $("comfy-status");
  // Apply the fields the user is looking at before checking, so the test cannot
  // report on a URL they just corrected and did not save.
  try {
    await api("/api/config", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ comfyui: comfySettingsPayload() }),
    });
  } catch (e) {
    if (out) out.textContent = e && e.message ? e.message : String(e);
    return null;
  }
  invalidateComfyCaches();
  if (btn) btn.disabled = true;
  if (out) out.textContent = t("state.loading") || "Loading…";
  let data = null;
  try {
    data = await comfyApi("/status");
    comfyStatusCache = data;
  } catch (e) {
    if (out) out.textContent = e && e.message ? e.message : String(e);
  } finally {
    if (btn) btn.disabled = false;
  }
  renderComfyStatus(data);
  void loadComfyWorkflows();
  return data;
}

function renderComfyStatus(data) {
  const out = $("comfy-status");
  const warn = $("comfy-storage-warning");
  if (!out) return;
  if (!data) {
    out.textContent = t("settings.comfyui_unreachable") || "Could not reach ComfyUI.";
    if (warn) warn.hidden = true;
    return;
  }
  if (!data.reachable) {
    out.textContent = `${data.error || "unreachable"} · ${data.url || ""}`;
    if (warn) warn.hidden = true;
    return;
  }
  const bits = [];
  if (data.comfyui_version) bits.push(`ComfyUI ${data.comfyui_version}`);
  if (data.python_version) bits.push(`Python ${data.python_version}`);
  bits.push(`${t("settings.comfyui_queue_running")}: ${data.queue_running ?? 0}`);
  bits.push(`${t("settings.comfyui_queue_pending")}: ${data.queue_pending ?? 0}`);
  if (Array.isArray(data.devices) && data.devices.length) {
    bits.push(data.devices.join(", "));
  }
  out.textContent = bits.join(" · ");
  // Storing results is the part that silently fails when the data directory is
  // read-only, and the failure only surfaces later as a missing image.
  if (warn) {
    if (data.media_writable === false) {
      warn.textContent = t("settings.comfyui_storage_warning") || "The manager cannot write to its media folder, so generated files will not be kept.";
      warn.hidden = false;
    } else {
      warn.hidden = true;
    }
  }
}

// ---------- settings: workflow list ----------

let comfyWorkflowsCache = [];

async function loadComfyWorkflows(force) {
  const list = $("comfy-workflows-list");
  if (!list) return comfyWorkflowsCache;
  if (force || !comfyWorkflowsCache.length) {
    try {
      const data = await comfyApi("/workflows");
      comfyWorkflowsCache = Array.isArray(data && data.workflows) ? data.workflows : [];
    } catch (e) {
      comfyWorkflowsCache = [];
      list.innerHTML = `<div class="muted small">${escapeHtml(e && e.message ? e.message : String(e))}</div>`;
      return comfyWorkflowsCache;
    }
  }
  renderComfyWorkflowList(comfyWorkflowsCache);
  return comfyWorkflowsCache;
}

function renderComfyWorkflowList(workflows) {
  const list = $("comfy-workflows-list");
  if (!list) return;
  const count = $("comfy-workflows-count");
  const badge = $("comfy-nav-badge");
  const badge2 = $("comfy-nav-badge-2");
  if (count) count.textContent = String(workflows.length);
  if (badge) badge.textContent = String(workflows.length);
  if (badge2) badge2.textContent = String(workflows.length);

  if (!workflows.length) {
    list.innerHTML = `<div class="muted small">${escapeHtml(t("settings.comfyui_workflows_none") || "No workflows registered yet.")}</div>`;
    return;
  }
  list.innerHTML = workflows.map((wf) => comfyWorkflowRowHtml(wf)).join("");
}

function comfyWorkflowRowHtml(wf) {
  const id = escapeHtml(wf.id);
  const name = escapeHtml(wf.name);
  const kind = wf.output_kind && wf.output_kind !== "any" ? escapeHtml(wf.output_kind) : "";
  const desc = wf.description
    ? `<div class="comfy-wf-desc">${escapeHtml(wf.description)}</div>`
    : "";
  const params = Array.isArray(wf.bindings) && wf.bindings.length
    ? wf.bindings.map((b) => escapeHtml(b.param)).join(", ")
    : (t("settings.comfyui_wf_no_params") || "no editable parameters");
  return `<div class="comfy-wf-row${wf.enabled ? "" : " comfy-wf-row--off"}" data-comfy-id="${id}">
    <label class="switch comfy-wf-switch">
      <input type="checkbox" class="comfy-wf-enabled" ${wf.enabled ? "checked" : ""} autocomplete="off">
      <span class="switch-slider"></span>
    </label>
    <div class="comfy-wf-main">
      <div class="comfy-wf-head">
        <span class="comfy-wf-name">${name}</span>
        ${kind ? `<span class="comfy-wf-kind">${kind}</span>` : ""}
        <span class="muted small">${escapeHtml(String(wf.node_count ?? (wf.nodes || []).length))} nodes</span>
      </div>
      ${desc}
      <div class="comfy-wf-params mono muted small">${params}</div>
    </div>
    <div class="comfy-wf-actions">
      <button type="button" class="ghost small comfy-wf-edit">${escapeHtml(t("settings.comfyui_wf_edit") || "Edit")}</button>
      <button type="button" class="ghost small comfy-wf-test">${escapeHtml(t("settings.comfyui_wf_test") || "Test")}</button>
      <button type="button" class="danger small comfy-wf-delete">${escapeHtml(t("action.delete") || "Delete")}</button>
    </div>
  </div>`;
}

// ---------- workflow editor modal ----------

let comfyWorkflowModalState = null;
// Fingerprint of the graph the current bindings were detected from, so a paste
// that replaces the graph can re-detect instead of saving bindings that point at
// node ids the new graph does not have.
let comfyDetectedFrom = "";
let comfyBindingsDirty = false;
let comfyDetectTimer = null;

function comfyGraphFingerprint(graph) {
  try {
    // Key order is irrelevant to the graph's meaning, so sort before hashing.
    return JSON.stringify(graph, Object.keys(graph || {}).sort());
  } catch (e) {
    return "";
  }
}

function openComfyWorkflowModal(workflow) {
  const state = {
    id: workflow ? workflow.id : "",
    bindings: workflow && Array.isArray(workflow.bindings) ? workflow.bindings.slice() : [],
  };
  comfyWorkflowModalState = state;
  comfyBindingsDirty = false;

  $("comfy-wf-modal-title").textContent = workflow
    ? (t("settings.comfyui_modal_edit") || "Edit workflow")
    : (t("settings.comfyui_modal_new") || "Register workflow");
  $("comfy-wf-save-btn").textContent = workflow
    ? (t("settings.comfyui_wf_save_edit") || "Save changes")
    : (t("settings.comfyui_wf_save") || "Register");
  $("comfy-wf-name").value = workflow ? workflow.name || "" : "";
  $("comfy-wf-desc").value = workflow ? workflow.description || "" : "";
  $("comfy-wf-output-kind").value = workflow ? workflow.output_kind || "" : "";
  $("comfy-wf-bindings-raw").value = "";
  setComfyModalError("");
  renderComfyNodes(workflow ? workflow.nodes || [] : []);

  // Editing keeps the stored graph on the server: the textarea is only for a
  // fresh paste, and emptying it would otherwise look like "clear the graph".
  if (workflow) {
    try {
      $("comfy-wf-json").value = JSON.stringify(workflow.workflow || {}, null, 2);
      $("comfy-wf-json").dataset.mode = "existing";
    } catch (e) {
      $("comfy-wf-json").dataset.mode = "existing";
    }
    comfyDetectedFrom = comfyGraphFingerprint(workflow.workflow || {});
  } else {
    $("comfy-wf-json").value = "";
    $("comfy-wf-json").dataset.mode = "new";
    comfyDetectedFrom = "";
  }

  renderComfyBindingsEditor(state.bindings);
  $("comfy-workflow-modal").hidden = false;
  const nameEl = $("comfy-wf-name");
  if (nameEl) nameEl.focus();
}

function closeComfyWorkflowModal() {
  $("comfy-workflow-modal").hidden = true;
  comfyWorkflowModalState = null;
}

function setComfyModalError(msg) {
  const box = $("comfy-wf-error");
  if (!box) return;
  if (!msg) {
    box.hidden = true;
    box.textContent = "";
    return;
  }
  box.textContent = msg;
  box.hidden = false;
}

function renderComfyNodes(nodes) {
  const box = $("comfy-wf-nodes");
  if (!box) return;
  if (!nodes || !nodes.length) {
    box.textContent = "";
    return;
  }
  const parts = nodes.slice(0, 40).map((n) => {
    const id = escapeHtml(n.id || "");
    const ct = escapeHtml(n.class_type || "");
    const title = n.title ? ` “${escapeHtml(n.title)}”` : "";
    return `${id}: ${ct}${title}`;
  });
  const extra = nodes.length > parts.length ? ` … +${nodes.length - parts.length}` : "";
  box.textContent = `${t("settings.comfyui_wf_nodes") || "Nodes"}: ${parts.join(" · ")}${extra}`;
}

// parseComfyPastedGraph validates a paste locally so the user gets the format
// error without a round trip. The server repeats the check; this is only about
// the message being immediate and specific.
function parseComfyPastedGraph(text) {
  const raw = (text || "").trim();
  if (!raw) return { ok: false, error: t("settings.comfyui_wf_json_empty") || "Paste a workflow exported in API format." };
  let data;
  try {
    data = JSON.parse(raw);
  } catch (e) {
    return { ok: false, error: `${t("settings.comfyui_wf_json_invalid") || "Invalid JSON"}: ${e.message}` };
  }
  if (!data || typeof data !== "object" || Array.isArray(data)) {
    return { ok: false, error: t("settings.comfyui_wf_json_not_object") || "A workflow must be a JSON object keyed by node id." };
  }
  const uiKeys = ["nodes", "links", "last_node_id", "extra", "groups", "version"];
  const found = uiKeys.filter((k) => Object.prototype.hasOwnProperty.call(data, k));
  if (found.length) {
    return {
      ok: false,
      error: t("settings.comfyui_wf_json_ui_format")
        || "That is the visual editor export. In ComfyUI use Workflow → Export (API Format) instead.",
    };
  }
  // Envelopes are unwrapped here too, so the node count and bindings preview
  // work on the same shape the server will store.
  let graph = data;
  for (let depth = 0; depth < 4; depth++) {
    const inner = graph.prompt || graph.workflow || graph.graph;
    if (inner && typeof inner === "object" && !Array.isArray(inner)) {
      graph = inner;
      continue;
    }
    break;
  }
  const ids = Object.keys(graph);
  if (!ids.length) return { ok: false, error: t("settings.comfyui_wf_json_empty") || "No nodes found in that workflow." };
  const missing = ids.filter((id) => {
    const n = graph[id];
    return !n || typeof n !== "object" || typeof n.class_type !== "string";
  });
  if (missing.length === ids.length) {
    return {
      ok: false,
      error: t("settings.comfyui_wf_json_not_api")
        || "No node has a class_type. Export with Workflow → Export (API Format).",
    };
  }
  return { ok: true, graph, nodeCount: ids.length };
}

function renderComfyBindingsEditor(bindings) {
  const box = $("comfy-wf-bindings-list");
  if (!box) return;
  if (!bindings.length) {
    box.innerHTML = `<div class="muted small">${escapeHtml(t("settings.comfyui_wf_no_params") || "No editable parameters detected. Edit as JSON to add one.")}</div>`;
    return;
  }
  box.innerHTML = bindings.map((b, i) => comfyBindingRowHtml(b, i)).join("");
}

function comfyBindingRowHtml(b, idx) {
  const kind = escapeHtml(b.kind || "string");
  const options = Array.isArray(b.enum) && b.enum.length
    ? `<span class="comfy-binding-enum muted small">${escapeHtml(b.enum.slice(0, 6).join(", "))}</span>`
    : "";
  return `<div class="comfy-binding-row" data-comfy-binding-idx="${idx}">
    <label class="switch comfy-binding-switch">
      <input type="checkbox" class="comfy-binding-on" ${b.param ? "checked" : ""} autocomplete="off">
      <span class="switch-slider"></span>
    </label>
    <div class="comfy-binding-main">
      <input type="text" class="comfy-binding-param mono" value="${escapeHtml(b.param || "")}" placeholder="param" spellcheck="false">
      <div class="comfy-binding-target mono muted small">${escapeHtml(`${b.node_id || "?"} · ${b.class_type || ""} · ${b.input || ""}`)}</div>
      ${options}
    </div>
    <div class="comfy-binding-actions">
      <span class="badge badge-muted">${kind}</span>
      <button type="button" class="danger small comfy-binding-remove">${escapeHtml(t("settings.comfyui_binding_remove") || "Remove")}</button>
    </div>
  </div>`;
}

// collectComfyBindingsEditor reads the binding rows back into the wire shape.
// A row whose switch is off keeps its parameter name empty, which is how the
// server drops it while preserving the list for the other rows.
function collectComfyBindingsEditor() {
  const rows = Array.from(document.querySelectorAll("#comfy-wf-bindings-list .comfy-binding-row"));
  const out = [];
  for (const row of rows) {
    const idx = Number(row.dataset.comfyBindingIdx);
    const base = comfyWorkflowModalState && comfyWorkflowModalState.bindings[idx];
    const on = row.querySelector(".comfy-binding-on");
    const paramEl = row.querySelector(".comfy-binding-param");
    const param = paramEl ? paramEl.value.trim() : "";
    if (!on || !on.checked || !param) continue;
    out.push({
      param,
      label: (base && base.label) || "",
      node_id: (base && base.node_id) || "",
      class_type: (base && base.class_type) || "",
      input: (base && base.input) || "",
      kind: (base && base.kind) || "",
      enum: (base && base.enum) || undefined,
      title: (base && base.title) || "",
    });
  }
  return out;
}

function readComfyBindingsRaw() {
  const raw = ($("comfy-wf-bindings-raw")?.value || "").trim();
  if (!raw) return null;
  try {
    const data = JSON.parse(raw);
    if (!Array.isArray(data)) throw new Error("expected an array");
    comfyBindingsDirty = true;
    return data;
  } catch (e) {
    setComfyModalError(`${t("settings.comfyui_wf_bindings_invalid") || "Invalid bindings JSON"}: ${e.message}`);
    return undefined;
  }
}

async function saveComfyWorkflow() {
  const state = comfyWorkflowModalState || { id: "", bindings: [] };
  const name = ($("comfy-wf-name")?.value || "").trim();
  const description = ($("comfy-wf-desc")?.value || "").trim();
  const outputKind = $("comfy-wf-output-kind")?.value || "";
  const jsonEl = $("comfy-wf-json");
  const isExisting = jsonEl && jsonEl.dataset.mode === "existing" && state.id;

  let workflow = null;
  const text = jsonEl ? jsonEl.value : "";
  if (!isExisting || text.trim()) {
    const parsed = parseComfyPastedGraph(text);
    if (!parsed.ok) {
      setComfyModalError(parsed.error);
      return;
    }
    workflow = parsed.graph;
  }
  // The graph in the textarea is the source of truth for the bindings. A graph
  // pasted while the modal was open may not have been detected yet if the user
  // hit Save quickly, and stale bindings would be rejected by the server with a
  // confusing "node not in this workflow" error.
  if (workflow) await syncComfyBindingsToGraph(false);

  const rawBindings = readComfyBindingsRaw();
  if (rawBindings === undefined) return;
  const bindings = rawBindings !== null ? rawBindings : collectComfyBindingsEditor();

  const btn = $("comfy-wf-save-btn");
  if (btn) btn.disabled = true;
  setComfyModalError("");
  try {
    const body = {
      name,
      description,
      output_kind: outputKind,
      workflow_id: state.id || "",
      bindings,
    };
    if (workflow) body.workflow = workflow;
    await comfyApi(isExisting ? `/workflows/${encodeURIComponent(state.id)}` : "/workflows", {
      method: isExisting ? "PATCH" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    closeComfyWorkflowModal();
    invalidateComfyCaches();
    comfyWorkflowsCache = [];
    await loadComfyWorkflows(true);
    await refreshComfyChatUI(true);
    toast(t("settings.comfyui_wf_saved") || "Workflow saved.", "success");
  } catch (e) {
    setComfyModalError(e && e.message ? e.message : String(e));
  } finally {
    if (btn) btn.disabled = false;
  }
}

function comfyNodeRefsFromGraph(graph) {
  return Object.keys(graph || {}).map((id) => {
    const n = graph[id] || {};
    return { id, class_type: n.class_type || "", title: n._meta && n._meta.title ? n._meta.title : "" };
  });
}

// requestComfyDetection asks the server what it would bind for a graph. The rules
// live server-side, so the Settings page has to ask rather than duplicate them in
// JavaScript, which would drift the moment a rule is added.
async function requestComfyDetection(graph) {
  const data = await comfyApi("/workflows/detect", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ workflow: graph }),
  });
  return {
    bindings: Array.isArray(data && data.bindings) ? data.bindings : [],
    nodes: Array.isArray(data && data.nodes) ? data.nodes : comfyNodeRefsFromGraph(graph),
    outputKind: (data && data.output_kind) || "",
  };
}

// applyComfyDetection installs a detection result in the modal and records the
// graph it came from.
function applyComfyDetection(state, result, graph) {
  state.bindings = result.bindings;
  comfyDetectedFrom = comfyGraphFingerprint(graph);
  renderComfyBindingsEditor(result.bindings);
  renderComfyNodes(result.nodes);
  // A pasted graph usually says what it produces better than the stored value.
  const kindSel = $("comfy-wf-output-kind");
  if (kindSel && (!kindSel.value || kindSel.value === "image") && result.outputKind) {
    kindSel.value = result.outputKind;
  }
}

// scheduleComfyAutoDetect re-detects after the paste settles. Autodetection is
// the point of the feature, so a fresh paste must not need an extra click.
function scheduleComfyAutoDetect() {
  if (comfyDetectTimer) clearTimeout(comfyDetectTimer);
  comfyDetectTimer = setTimeout(() => void syncComfyBindingsToGraph(false), 500);
}

// syncComfyBindingsToGraph re-detects when the textarea graph no longer matches
// the one the visible bindings were detected from. Hand-edited bindings win:
// re-detecting under the user would throw away their work silently.
async function syncComfyBindingsToGraph(force) {
  const state = comfyWorkflowModalState;
  if (!state) return false;
  const parsed = parseComfyPastedGraph($("comfy-wf-json")?.value || "");
  if (!parsed.ok) {
    setComfyModalError(parsed.error);
    return false;
  }
  const fingerprint = comfyGraphFingerprint(parsed.graph);
  if (!force && fingerprint === comfyDetectedFrom) return true;
  if (!force && comfyBindingsDirty) return true;
  try {
    const result = await requestComfyDetection(parsed.graph);
    applyComfyDetection(state, result, parsed.graph);
    comfyBindingsDirty = false;
    setComfyModalError("");
    return true;
  } catch (e) {
    setComfyModalError(e && e.message ? e.message : String(e));
    return false;
  }
}

// redetectComfyBindings is the "Detect again" button: it always overwrites, even
// bindings the user hand-edited, which is what makes it an escape hatch.
async function redetectComfyBindings() {
  const state = comfyWorkflowModalState;
  const parsed = parseComfyPastedGraph($("comfy-wf-json")?.value || "");
  if (!parsed.ok) {
    setComfyModalError(parsed.error);
    return;
  }
  try {
    const result = await requestComfyDetection(parsed.graph);
    applyComfyDetection(state, result, parsed.graph);
    comfyBindingsDirty = false;
    setComfyModalError("");
    toast(t("settings.comfyui_wf_detected") || "Detected parameters.", "success");
  } catch (e) {
    setComfyModalError(e && e.message ? e.message : String(e));
  }
}

async function toggleComfyWorkflow(checkbox) {
  const row = checkbox.closest(".comfy-wf-row");
  const id = row && row.dataset.comfyId;
  if (!id) return;
  const want = checkbox.checked;
  try {
    await comfyApi(`/workflows/${encodeURIComponent(id)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled: want }),
    });
    invalidateComfyCaches();
    comfyWorkflowsCache = comfyWorkflowsCache.map((wf) => (wf.id === id ? Object.assign({}, wf, { enabled: want }) : wf));
    renderComfyWorkflowList(comfyWorkflowsCache);
    await refreshComfyChatUI(true);
  } catch (e) {
    checkbox.checked = !want;
    toast(e && e.message ? e.message : String(e), "error");
  }
}

async function editComfyWorkflow(id) {
  try {
    const data = await comfyApi(`/workflows/${encodeURIComponent(id)}`);
    const wf = data && data.workflow;
    if (wf) openComfyWorkflowModal(wf);
  } catch (e) {
    toast(e && e.message ? e.message : String(e), "error");
  }
}

async function deleteComfyWorkflow(id, name) {
  if (!window.confirm(t("settings.comfyui_wf_delete_confirm", { name }) || `Delete the workflow "${name}"?`)) return;
  try {
    await comfyApi(`/workflows/${encodeURIComponent(id)}`, { method: "DELETE" });
    invalidateComfyCaches();
    comfyWorkflowsCache = comfyWorkflowsCache.filter((wf) => wf.id !== id);
    renderComfyWorkflowList(comfyWorkflowsCache);
    await refreshComfyChatUI(true);
    toast(t("settings.comfyui_wf_deleted") || "Workflow deleted.", "success");
  } catch (e) {
    toast(e && e.message ? e.message : String(e), "error");
  }
}

// testComfyWorkflow runs a workflow once with its stored defaults, which is the
// fastest way to find out that the graph needs a model the server does not have.
async function testComfyWorkflow(id, button) {
  const row = button && button.closest(".comfy-wf-row");
  const label = row ? (row.querySelector(".comfy-wf-name") || {}).textContent : id;
  if (button) button.disabled = true;
  const prev = button ? button.textContent : "";
  if (button) button.textContent = t("settings.comfyui_wf_running") || "Running…";
  try {
    const data = await comfyApi("/run", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ workflow: id, params: {} }),
    });
    const media = (data && data.media) || [];
    const msg = t("settings.comfyui_wf_test_done", { name: label, n: media.length })
      || `"${label}" finished and produced ${media.length} file(s).`;
    toast(msg, "success");
  } catch (e) {
    toast(e && e.message ? e.message : String(e), "error");
  } finally {
    if (button) {
      button.disabled = false;
      button.textContent = prev;
    }
  }
}

function bindComfySettingsEvents() {
  const test = $("comfy-test-btn");
  if (test) test.addEventListener("click", () => void testComfyConnection());
  const refresh = $("comfy-refresh-btn");
  if (refresh) {
    refresh.addEventListener("click", () => {
      invalidateComfyCaches();
      void comfyApi("/status").then(renderComfyStatus).catch(() => renderComfyStatus(null));
      comfyWorkflowsCache = [];
      void loadComfyWorkflows(true);
    });
  }
  const newBtn = $("comfy-new-btn");
  if (newBtn) newBtn.addEventListener("click", () => openComfyWorkflowModal(null));

  const modal = $("comfy-workflow-modal");
  const x = $("comfy-wf-modal-x");
  if (x) x.addEventListener("click", closeComfyWorkflowModal);
  const cancel = $("comfy-wf-cancel-btn");
  if (cancel) cancel.addEventListener("click", closeComfyWorkflowModal);
  const save = $("comfy-wf-save-btn");
  if (save) save.addEventListener("click", () => void saveComfyWorkflow());
  const redetect = $("comfy-wf-redetect-btn");
  if (redetect) redetect.addEventListener("click", () => void redetectComfyBindings());
  const jsonEl = $("comfy-wf-json");
  if (jsonEl) {
    // Debounced input rather than a paste-only listener: dragging a file in, or
    // pasting a graph assembled in an editor, both arrive as input events too.
    jsonEl.addEventListener("input", scheduleComfyAutoDetect);
    jsonEl.addEventListener("blur", () => {
      if (comfyDetectTimer) clearTimeout(comfyDetectTimer);
    });
  }
  if (modal) {
    modal.addEventListener("click", (ev) => {
      if (ev.target === modal) closeComfyWorkflowModal();
    });
  }

  // One delegated listener covers rows rendered later by renderComfyWorkflowList.
  const list = $("comfy-workflows-list");
  if (list) {
    list.addEventListener("click", (ev) => {
      const target = ev.target;
      const row = target.closest(".comfy-wf-row");
      if (!row) return;
      const id = row.dataset.comfyId;
      const name = (row.querySelector(".comfy-wf-name") || {}).textContent || id;
      if (target.closest(".comfy-wf-enabled")) return;
      if (target.closest(".comfy-wf-edit")) void editComfyWorkflow(id);
      else if (target.closest(".comfy-wf-test")) void testComfyWorkflow(id, target.closest(".comfy-wf-test"));
      else if (target.closest(".comfy-wf-delete")) void deleteComfyWorkflow(id, name);
    });
    list.addEventListener("change", (ev) => {
      const cb = ev.target.closest(".comfy-wf-enabled");
      if (cb) void toggleComfyWorkflow(cb);
    });
  }

  const bindingsList = $("comfy-wf-bindings-list");
  if (bindingsList) {
    bindingsList.addEventListener("click", (ev) => {
      const btn = ev.target.closest(".comfy-binding-remove");
      if (!btn) return;
      const row = btn.closest(".comfy-binding-row");
      const idx = Number(row.dataset.comfyBindingIdx);
      if (comfyWorkflowModalState && comfyWorkflowModalState.bindings[idx]) {
        comfyWorkflowModalState.bindings[idx] = { ...comfyWorkflowModalState.bindings[idx], param: "" };
      }
      comfyBindingsDirty = true;
      row.querySelector(".comfy-binding-on").checked = false;
      const paramEl = row.querySelector(".comfy-binding-param");
      if (paramEl) {
        paramEl.value = "";
        paramEl.placeholder = t("settings.comfyui_binding_removed") || "removed";
        paramEl.disabled = true;
      }
      row.classList.add("comfy-binding-row--off");
    });
    // Renaming a parameter by hand means the auto-detected list is no longer what
    // the user wants, so a later paste must not overwrite it.
    bindingsList.addEventListener("input", (ev) => {
      if (ev.target.classList && ev.target.classList.contains("comfy-binding-param")) {
        comfyBindingsDirty = true;
      }
    });
    bindingsList.addEventListener("change", (ev) => {
      if (ev.target.classList && ev.target.classList.contains("comfy-binding-on")) {
        comfyBindingsDirty = true;
      }
    });
  }
}

// The chat toggle lives outside the settings view, so its listeners are bound on
// load rather than when the settings card opens. Escape closes the workflow
// editor the same way the other modals do.
document.addEventListener("DOMContentLoaded", () => {
  bindComfyChatEvents();
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    const modal = $("comfy-workflow-modal");
    if (modal && !modal.hidden) {
      e.preventDefault();
      e.stopPropagation();
      e.stopImmediatePropagation();
      closeComfyWorkflowModal();
    }
  });
});