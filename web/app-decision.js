"use strict";

// ---------- decision (System One) playground ----------
// Decision models use a different endpoint (/api/decision -> Ollama /v1/systemone)
// and a JSON-in/JSON-out contract instead of chat messages. This module renders a
// dedicated input builder and a custom result view, replacing the chat composer
// when a decision-only model is selected.

let decisionStateMode = "text";
let decisionImages = [];
let decisionResultMode = "cards";
let decisionLastResult = null;
let decisionLastQuestions = {};

function isDecisionOnlyModel(modelName) {
  const caps = modelCaps(modelName);
  return caps.has("decision") && !caps.has("completion");
}

function updateDecisionChatUI() {
  const sel = $("chat-model");
  const model = sel ? sel.value : "";
  const isDecision = isDecisionOnlyModel(model);
  const view = $("chat-view");
  if (view) view.classList.toggle("chat-decision-mode", isDecision);
  const panel = $("chat-decision-panel");
  if (panel) panel.hidden = !isDecision;

  const imgWrap = $("decision-image-wrap");
  if (imgWrap) imgWrap.hidden = !modelCaps(model).has("vision");
}

// ---------- input builder ----------

function decisionTypeLabel(type) {
  const key = `chat.decision.type_${type}`;
  const tr = t(key);
  return tr === key ? type : tr;
}

function decisionCriteriaHtml(type) {
  if (type === "choice") {
    return `
      <div class="decision-crit-list" data-crit="choice"></div>
      <button type="button" class="ghost decision-crit-add">${escapeHtml(t("chat.decision.add_option"))}</button>`;
  }
  if (type === "score") {
    return `
      <div class="decision-crit-list" data-crit="score"></div>
      <button type="button" class="ghost decision-crit-add">${escapeHtml(t("chat.decision.add_level"))}</button>`;
  }
  // noul
  return `
    <div class="decision-noul-row">
      <input class="decision-noul-false" autocomplete="off" placeholder="${escapeHtml(t("chat.decision.noul_false"))}">
      <input class="decision-noul-true" autocomplete="off" placeholder="${escapeHtml(t("chat.decision.noul_true"))}">
    </div>`;
}

function decisionChoiceRowHtml(key, desc) {
  return `
    <div class="decision-crit-row">
      <input class="decision-crit-key" autocomplete="off" placeholder="${escapeHtml(t("chat.decision.key"))}" value="${escapeHtml(key || "")}">
      <input class="decision-crit-desc" autocomplete="off" placeholder="${escapeHtml(t("chat.decision.description"))}" value="${escapeHtml(desc || "")}">
      <button type="button" class="ghost decision-crit-remove" aria-label="remove">×</button>
    </div>`;
}

function decisionScoreRowHtml(desc) {
  return `
    <div class="decision-crit-row">
      <span class="decision-crit-index mono"></span>
      <input class="decision-crit-desc" autocomplete="off" placeholder="${escapeHtml(t("chat.decision.level"))}" value="${escapeHtml(desc || "")}">
      <button type="button" class="ghost decision-crit-remove" aria-label="remove">×</button>
    </div>`;
}

function decisionReindexScore(list) {
  list.querySelectorAll(".decision-crit-row").forEach((row, i) => {
    const idx = row.querySelector(".decision-crit-index");
    if (idx) idx.textContent = String(i + 1);
  });
}

function decisionFillCriteria(q, type) {
  const host = q.querySelector(".decision-q-criteria");
  host.innerHTML = decisionCriteriaHtml(type);
  const list = host.querySelector(".decision-crit-list");
  if (type === "choice") {
    list.insertAdjacentHTML("beforeend", decisionChoiceRowHtml("", ""));
    list.insertAdjacentHTML("beforeend", decisionChoiceRowHtml("", ""));
  } else if (type === "score") {
    list.insertAdjacentHTML("beforeend", decisionScoreRowHtml(""));
    list.insertAdjacentHTML("beforeend", decisionScoreRowHtml(""));
    decisionReindexScore(list);
  }
}

function decisionQuestionRowHtml() {
  return `
    <div class="decision-q">
      <div class="decision-q-head">
        <input class="decision-q-name" autocomplete="off" placeholder="${escapeHtml(t("chat.decision.name"))}">
        <select class="decision-q-type" autocomplete="off">
          <option value="choice">${escapeHtml(decisionTypeLabel("choice"))}</option>
          <option value="noul">${escapeHtml(decisionTypeLabel("noul"))}</option>
          <option value="score">${escapeHtml(decisionTypeLabel("score"))}</option>
        </select>
        <button type="button" class="ghost decision-q-remove" aria-label="remove">×</button>
      </div>
      <textarea class="decision-q-instructions" rows="2" autocomplete="off" placeholder="${escapeHtml(t("chat.decision.instructions"))}"></textarea>
      <div class="decision-q-criteria"></div>
    </div>`;
}

function decisionAddQuestion() {
  const host = $("decision-questions");
  if (!host) return;
  host.insertAdjacentHTML("beforeend", decisionQuestionRowHtml());
  const q = host.lastElementChild;
  decisionFillCriteria(q, "choice");
}

function decisionRenderImages() {
  const list = $("decision-image-list");
  if (!list) return;
  if (!decisionImages.length) {
    list.innerHTML = "";
    return;
  }
  list.innerHTML = decisionImages
    .map((img, i) => `
      <span class="decision-image-chip">
        <img src="${img.url || ""}" alt="">
        <button type="button" class="ghost decision-image-remove" data-idx="${i}" aria-label="remove">×</button>
      </span>`)
    .join("");
}

// ---------- validation + collection ----------

function collectDecisionQuestions() {
  const rows = document.querySelectorAll("#decision-questions .decision-q");
  if (!rows.length) throw new Error(t("chat.decision.err_no_questions"));
  const questions = {};
  for (const q of rows) {
    const name = q.querySelector(".decision-q-name").value.trim();
    const type = q.querySelector(".decision-q-type").value;
    const instructions = q.querySelector(".decision-q-instructions").value.trim();
    if (!name) throw new Error(t("chat.decision.err_name"));
    if (questions[name]) throw new Error(t("chat.decision.err_dup_name", { name }));
    if (!instructions) throw new Error(t("chat.decision.err_instructions", { name }));

    const item = { type, instructions };
    if (type === "choice") {
      const criteria = {};
      q.querySelectorAll(".decision-crit-list .decision-crit-row").forEach((row) => {
        const k = row.querySelector(".decision-crit-key").value.trim();
        const d = row.querySelector(".decision-crit-desc").value.trim();
        if (k) criteria[k] = d || k;
      });
      if (Object.keys(criteria).length < 2) throw new Error(t("chat.decision.err_choice_criteria", { name }));
      item.criteria = criteria;
    } else if (type === "score") {
      const criteria = [];
      q.querySelectorAll(".decision-crit-list .decision-crit-row").forEach((row) => {
        criteria.push(row.querySelector(".decision-crit-desc").value.trim());
      });
      if (criteria.length < 2 || criteria.some((c) => !c)) throw new Error(t("chat.decision.err_score_criteria", { name }));
      item.criteria = criteria;
    } else {
      const f = q.querySelector(".decision-noul-false").value.trim();
      const tr = q.querySelector(".decision-noul-true").value.trim();
      if (f || tr) item.criteria = { false: f, true: tr };
    }
    questions[name] = item;
  }
  return questions;
}

function decisionBuildState() {
  const raw = ($("decision-state")?.value || "").trim();
  if (!raw) throw new Error(t("chat.decision.err_state"));
  if (decisionStateMode === "json") {
    try {
      return JSON.parse(raw);
    } catch {
      throw new Error(t("chat.decision.err_json"));
    }
  }
  return raw;
}

// Best-effort assembly of the current form into the System One payload,
// used to seed the editable JSON view. Unlike collectDecisionQuestions it
// does not throw, so the JSON is always shown.
function decisionAssembleInput() {
  const model = $("chat-model")?.value || "";
  const state = $("decision-state")?.value || "";
  const questions = {};
  document.querySelectorAll("#decision-questions .decision-q").forEach((q) => {
    const name = q.querySelector(".decision-q-name").value.trim();
    if (!name) return;
    const type = q.querySelector(".decision-q-type").value;
    const instructions = q.querySelector(".decision-q-instructions").value.trim();
    const item = { type, instructions };
    if (type === "choice") {
      const criteria = {};
      q.querySelectorAll(".decision-crit-list .decision-crit-row").forEach((row) => {
        const k = row.querySelector(".decision-crit-key").value.trim();
        const d = row.querySelector(".decision-crit-desc").value.trim();
        if (k) criteria[k] = d || k;
      });
      item.criteria = criteria;
    } else if (type === "score") {
      const criteria = [];
      q.querySelectorAll(".decision-crit-list .decision-crit-row").forEach((row) => {
        criteria.push(row.querySelector(".decision-crit-desc").value.trim());
      });
      item.criteria = criteria;
    } else {
      const f = q.querySelector(".decision-noul-false").value.trim();
      const tr = q.querySelector(".decision-noul-true").value.trim();
      if (f || tr) item.criteria = { false: f, true: tr };
    }
    questions[name] = item;
  });
  const payload = { model, state, questions };
  if (decisionImages.length) payload.images = decisionImages.map((i) => i.data);
  return payload;
}

function decisionSyncJsonView() {
  const el = $("decision-json-input");
  if (el) el.value = JSON.stringify(decisionAssembleInput(), null, 2);
}

// ---------- execution ----------

async function decisionPost(payload) {
  const res = await fetch("/api/decision", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!res.ok) {
    let msg = res.statusText || "request failed";
    try {
      const j = await res.json();
      if (j && j.error) msg = j.error;
    } catch { /* keep statusText */ }
    const err = new Error(msg);
    err.status = res.status;
    throw err;
  }
  const data = await res.json();
  return {
    data,
    latencyMs: Number(res.headers.get("X-Ollama-Latency-Ms")) || 0,
    wasCold: res.headers.get("X-Ollama-Was-Cold") === "true",
  };
}

async function runDecision() {
  const model = $("chat-model")?.value || "";
  const btn = $("decision-run-btn");
  const status = $("decision-status");
  try {
    let payload;
    let questions;
    if (decisionStateMode === "json") {
      const raw = ($("decision-json-input")?.value || "").trim();
      if (!raw) throw new Error(t("chat.decision.err_state"));
      try {
        payload = JSON.parse(raw);
      } catch {
        throw new Error(t("chat.decision.err_json"));
      }
      if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
        throw new Error(t("chat.decision.err_json"));
      }
      if (!payload.model) payload.model = model;
      if (payload.state === undefined || payload.state === null || payload.state === "") {
        throw new Error(t("chat.decision.err_state"));
      }
      if (!payload.questions || !Object.keys(payload.questions).length) {
        throw new Error(t("chat.decision.err_no_questions"));
      }
      questions = payload.questions;
    } else {
      const state = decisionBuildState();
      questions = collectDecisionQuestions();
      payload = { model, state, questions };
      if (decisionImages.length) payload.images = decisionImages.map((i) => i.data);
    }

    if (btn) btn.disabled = true;
    if (status) status.textContent = t("chat.decision.running");

    const { data, latencyMs, wasCold } = await decisionPost(payload);
    renderDecisionResult(data, questions, { latencyMs, wasCold });
    if (status) status.textContent = "";
  } catch (e) {
    const msg = (e && e.message) || "failed";
    if (status) status.textContent = msg;
    toast(t("toast.error", { msg }), "error");
  } finally {
    if (btn) btn.disabled = false;
  }
}

// ---------- result rendering ----------

function decisionPct(n) {
  return `${Math.round((Number(n) || 0) * 100)}%`;
}

function decisionBar(label, p, active) {
  const pct = Math.max(0, Math.min(100, (Number(p) || 0) * 100));
  return `
    <div class="decision-bar${active ? " is-active" : ""}">
      <div class="decision-bar-fill" style="width:${pct.toFixed(2)}%"></div>
      <span class="decision-bar-label">${escapeHtml(label)}</span>
      <span class="decision-bar-value mono">${decisionPct(p)}</span>
    </div>`;
}

function decisionConfidence(c) {
  if (c === null || c === undefined) return "";
  return `<span class="decision-confidence">${escapeHtml(t("chat.decision.confidence"))} ${decisionPct(c)}</span>`;
}

function renderDecisionAnswer(name, ans, qtype) {
  const type = (ans && ans.type) || qtype || "";
  let body = "";
  if (type === "choice") {
    const probs = ans.probabilities || {};
    const chosen = ans.choice || "";
    const keys = Object.keys(probs).sort((a, b) => (probs[b] || 0) - (probs[a] || 0));
    const rows = keys.length
      ? keys.map((k) => decisionBar(k, probs[k], k === chosen)).join("")
      : decisionBar(chosen, 1, true);
    body = `
      <div class="decision-answer-head">
        <span class="decision-choice-pill">${escapeHtml(chosen || "—")}</span>
        ${decisionConfidence(ans.confidence)}
      </div>
      <div class="decision-bars">${rows}</div>`;
  } else if (type === "noul") {
    const p = Number(ans.noul) || 0;
    body = `
      <div class="decision-answer-head">${decisionConfidence(ans.confidence)}</div>
      <div class="decision-bars">${decisionBar(t("chat.decision.noul_true"), p, p >= 0.5)}</div>`;
  } else if (type === "score") {
    const probs = ans.probabilities || {};
    const legend = ans.legend || {};
    const idxs = Object.keys(probs).length
      ? Object.keys(probs).sort((a, b) => Number(a) - Number(b))
      : Object.keys(legend).sort((a, b) => Number(a) - Number(b));
    const rows = idxs
      .map((i) => decisionBar(legend[i] || `#${i}`, probs[i], Number(i) === Math.round(Number(ans.score))))
      .join("");
    body = `
      <div class="decision-answer-head">
        <span class="decision-score-pill mono">${escapeHtml(t("chat.decision.score"))} ${Number(ans.score).toFixed(2)}</span>
        ${decisionConfidence(ans.confidence)}
      </div>
      <div class="decision-bars">${rows}</div>`;
  } else {
    body = `<pre class="decision-raw-pre">${escapeHtml(JSON.stringify(ans, null, 2))}</pre>`;
  }
  return `
    <div class="decision-answer">
      <div class="decision-answer-name mono">${escapeHtml(name)}</div>
      ${body}
    </div>`;
}

function decisionResultBodyHtml() {
  const data = decisionLastResult || {};
  if (decisionResultMode === "json") {
    return `<pre class="decision-raw-pre">${escapeHtml(JSON.stringify(data, null, 2))}</pre>`;
  }
  const answers = data.answers || {};
  const questions = decisionLastQuestions || {};
  const answerKeys = Object.keys(answers);
  const cards = answerKeys.length
    ? answerKeys.map((name) => renderDecisionAnswer(name, answers[name], questions[name]?.type)).join("")
    : `<div class="muted">${escapeHtml(t("chat.decision.no_answers"))}</div>`;
  return `<div class="decision-answers">${cards}</div>`;
}

function renderDecisionResultView() {
  const host = $("decision-result");
  if (!host) return;
  const body = host.querySelector(".decision-result-body");
  if (body) body.innerHTML = decisionResultBodyHtml();
  host.querySelectorAll(".decision-result-mode-btn").forEach((b) => {
    b.classList.toggle("active", b.dataset.mode === decisionResultMode);
  });
}

function renderDecisionResult(data, questions, meta) {
  const host = $("decision-result");
  if (!host) return;
  const info = meta || {};
  decisionLastResult = data || {};
  decisionLastQuestions = questions || {};
  host.hidden = false;
  const usage = data.usage || {};
  const latency = Number(info.latencyMs) || 0;

  const metaHtml = `
    <div class="decision-meta">
      <span class="decision-meta-item"><b class="mono">${latency}</b> ms</span>
      <span class="decision-meta-item">${escapeHtml(t("chat.decision.tokens_in"))} <b class="mono">${usage.input_tokens || 0}</b></span>
      <span class="decision-meta-item">${escapeHtml(t("chat.decision.tokens_out"))} <b class="mono">${usage.output_tokens || 0}</b></span>
      ${info.wasCold ? `<span class="decision-meta-item decision-cold">${escapeHtml(t("chat.decision.cold"))}</span>` : ""}
    </div>`;

  host.innerHTML = `
    <div class="decision-result-head">
      <span class="decision-result-title">${escapeHtml(t("chat.decision.result"))}</span>
      <div class="decision-result-modes">
        <button type="button" class="decision-result-mode-btn${decisionResultMode === "cards" ? " active" : ""}" data-mode="cards">${escapeHtml(t("chat.decision.view_cards"))}</button>
        <button type="button" class="decision-result-mode-btn${decisionResultMode === "json" ? " active" : ""}" data-mode="json">${escapeHtml(t("chat.decision.raw_json"))}</button>
      </div>
      ${metaHtml}
    </div>
    <div class="decision-result-body">${decisionResultBodyHtml()}</div>`;
}

// ---------- init ----------

function initDecisionPanel() {
  const panel = $("chat-decision-panel");
  if (!panel) return;

  // state mode toggle
  panel.querySelectorAll(".decision-mode-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      decisionStateMode = btn.dataset.mode === "json" ? "json" : "text";
      panel.querySelectorAll(".decision-mode-btn").forEach((b) => b.classList.toggle("active", b === btn));
      panel.classList.toggle("decision-json-mode", decisionStateMode === "json");
      if (decisionStateMode === "json") decisionSyncJsonView();
    });
  });

  // result view toggle (Cards / JSON)
  const resultHost = $("decision-result");
  if (resultHost) {
    resultHost.addEventListener("click", (e) => {
      const b = e.target.closest(".decision-result-mode-btn");
      if (!b) return;
      decisionResultMode = b.dataset.mode === "json" ? "json" : "cards";
      renderDecisionResultView();
    });
  }

  // images
  const imgBtn = $("decision-image-btn");
  const imgInput = $("decision-image-input");
  if (imgBtn && imgInput) {
    imgBtn.addEventListener("click", () => imgInput.click());
    imgInput.addEventListener("change", () => {
      const files = Array.from(imgInput.files || []);
      files.forEach((file) => {
        const reader = new FileReader();
        reader.onload = () => {
          const res = String(reader.result || "");
          const comma = res.indexOf(",");
          decisionImages.push({ name: file.name, url: res, data: comma >= 0 ? res.slice(comma + 1) : res });
          decisionRenderImages();
        };
        reader.readAsDataURL(file);
      });
      imgInput.value = "";
    });
  }

  const imgList = $("decision-image-list");
  if (imgList) {
    imgList.addEventListener("click", (e) => {
      const btn = e.target.closest(".decision-image-remove");
      if (!btn) return;
      decisionImages.splice(Number(btn.dataset.idx), 1);
      decisionRenderImages();
    });
  }

  // questions: add + delegated controls
  const addBtn = $("decision-add-question");
  if (addBtn) addBtn.addEventListener("click", decisionAddQuestion);

  const qHost = $("decision-questions");
  if (qHost) {
    qHost.addEventListener("change", (e) => {
      const sel = e.target.closest(".decision-q-type");
      if (!sel) return;
      decisionFillCriteria(sel.closest(".decision-q"), sel.value);
    });
    qHost.addEventListener("click", (e) => {
      const remove = e.target.closest(".decision-q-remove");
      if (remove) {
        const all = qHost.querySelectorAll(".decision-q");
        if (all.length > 1) remove.closest(".decision-q").remove();
        return;
      }
      const addCrit = e.target.closest(".decision-crit-add");
      if (addCrit) {
        const q = addCrit.closest(".decision-q");
        const list = q.querySelector(".decision-crit-list");
        const type = q.querySelector(".decision-q-type").value;
        if (type === "choice") list.insertAdjacentHTML("beforeend", decisionChoiceRowHtml("", ""));
        else {
          list.insertAdjacentHTML("beforeend", decisionScoreRowHtml(""));
          decisionReindexScore(list);
        }
        return;
      }
      const rmCrit = e.target.closest(".decision-crit-remove");
      if (rmCrit) {
        const list = rmCrit.closest(".decision-crit-list");
        rmCrit.closest(".decision-crit-row").remove();
        decisionReindexScore(list);
      }
    });
  }

  const runBtn = $("decision-run-btn");
  if (runBtn) runBtn.addEventListener("click", () => { void runDecision(); });

  decisionAddQuestion();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", initDecisionPanel);
} else {
  initDecisionPanel();
}
