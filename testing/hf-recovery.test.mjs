import assert from "node:assert/strict";
import { test } from "node:test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import vm from "node:vm";

const web = join(dirname(fileURLToPath(import.meta.url)), "..", "web");
const downloadsSrc = readFileSync(join(web, "app-downloads.js"), "utf8");

function extractFunction(src, name) {
  const start = src.indexOf(`function ${name}(`);
  assert.notEqual(start, -1, `${name} not found`);
  const braceStart = src.indexOf("{", start);
  let depth = 0;
  for (let i = braceStart; i < src.length; i++) {
    const ch = src[i];
    if (ch === "{") depth++;
    else if (ch === "}") {
      depth--;
      if (depth === 0) return src.slice(start, i + 1);
    }
  }
  throw new Error(`unbalanced braces in ${name}`);
}
const extract = (src, name) => {
  const i = src.indexOf(`async function ${name}(`);
  if (i >= 0) {
    const s = extractFunction(src.slice(i).replace("async function", "function"), name);
    return `async function ${name}` + s.slice(`function ${name}`.length);
  }
  return extractFunction(src, name);
};

class FakeEl {
  constructor(id) {
    this.id = id;
    this._html = "";
    this.textContent = "";
    this.disabled = false;
    this.hidden = true;
    this.listeners = {};
  }
  get innerHTML() { return this._html; }
  set innerHTML(v) { this._html = String(v); }
  addEventListener(ev, fn) { (this.listeners[ev] ||= []).push(fn); }
  dispatch(ev, e) { for (const fn of this.listeners[ev] || []) fn(e); }
}

function makeSandbox() {
  const els = new Map();
  const $ = (id) => {
    if (!els.has(id)) els.set(id, new FakeEl(id));
    return els.get(id);
  };
  const apiCalls = [];
  const toasts = [];
  const job = { id: "j1", name: "huggingface.co/owner/repo:Q4_K_M", status: "error" };
  const preview = {
    repo: "owner/repo",
    revision: "0123456789abcdef0123456789abcdef01234567",
    selected_filename: "model-Q4_K_M.gguf",
    has_token: true,
    options: [
      {
        filename: "model-Q4_K_M.gguf",
        quant: "Q4_K_M",
        total_bytes: 100,
        files: [
          { filename: "model-Q4_K_M.gguf", size: 100, digest: "aa", exists: true },
        ],
      },
      {
        filename: "model-Q8_0.gguf",
        quant: "Q8_0",
        total_bytes: 200,
        files: [
          { filename: "model-Q8_0.gguf", size: 200, digest: "bb", exists: false },
        ],
      },
    ],
    projectors: [
      { filename: "mmproj-f16.gguf", size: 50, digest: "cc", exists: false },
    ],
  };
  const sandbox = {
    console,
    document: { addEventListener: () => {}, createElement: () => new FakeEl("tmp") },
    window: {},
    $,
    jobs: new Map([[job.id, job]]),
    jobsStream: null,
    hfRecoveryState: null,
    apiCalls,
    toasts,
    api: async (path, opts) => {
      apiCalls.push({ path, opts });
      if (opts && opts.method === "POST") {
        if (sandbox.__postFails) throw new Error("boom");
        return { job_id: job.id, status: "queued", name: job.name };
      }
      if (sandbox.__getFails) throw new Error("preview exploded");
      return preview;
    },
    toast: (msg, kind) => toasts.push({ msg, kind }),
    refreshJobs: async () => {},
    t: (k, p) => (p ? `${k}:${JSON.stringify(p)}` : k),
    escapeHtml: (s) => String(s),
    fmtBytes: (n) => `${n}B`,
    fmtDateTimeFull: () => "",
    fmtRelativeTime: () => "",
    fmtSpeed: () => "",
    fmtETA: () => "",
    modelHomepageUrl: () => "",
    hfAuthHint: () => "",
    dlSizeLine: () => "",
    dlSpeedText: () => "",
    dlEtaText: () => "",
    dlFinishedText: () => "",
    jobStatusLabel: (j) => j.status,
    setTimeout,
  };
  vm.createContext(sandbox);
  return { sandbox, els, apiCalls, toasts, preview, job };
}

const FNS = [
  "hfJobRecoverable",
  "jobCardHTML",
  "closeHFRecoveryModal",
  "openHFRecoveryModal",
  "hfRecoveryTotals",
  "hfRecoveryFileRow",
  "renderHFRecoveryBody",
  "updateHFRecoverySummary",
  "submitHFRecovery",
];

function loadFns(sandbox) {
  for (const name of FNS) {
    vm.runInContext(extract(downloadsSrc, name), sandbox, { filename: `app-downloads.js:${name}` });
  }
}

test("hfJobRecoverable only for failed HF jobs", () => {
  const { sandbox } = makeSandbox();
  loadFns(sandbox);
  const f = (j) => vm.runInContext(`hfJobRecoverable(${JSON.stringify(j)})`, sandbox);
  assert.equal(f({ status: "error", name: "huggingface.co/o/r:Q4_K_M" }), true);
  assert.equal(f({ status: "error", name: "hf.co/o/r" }), true);
  assert.equal(f({ status: "error", name: "llama3:8b" }), false);
  assert.equal(f({ status: "cancelled", name: "huggingface.co/o/r" }), false);
  assert.equal(f({ status: "done", name: "huggingface.co/o/r" }), false);
  assert.equal(f({ status: "running", name: "huggingface.co/o/r" }), false);
});

test("job card shows recover button only on failed HF jobs", () => {
  const { sandbox } = makeSandbox();
  loadFns(sandbox);
  const card = (j) => vm.runInContext(`jobCardHTML(${JSON.stringify(j)})`, sandbox);
  assert.match(card({ id: "a", status: "error", name: "huggingface.co/o/r:Q4_K_M" }), /data-action="hf-recover"/);
  assert.doesNotMatch(card({ id: "b", status: "error", name: "llama3:8b" }), /hf-recover/);
  assert.doesNotMatch(card({ id: "c", status: "done", name: "huggingface.co/o/r" }), /hf-recover/);
  assert.doesNotMatch(card({ id: "d", status: "cancelled", name: "huggingface.co/o/r" }), /hf-recover/);
  // Regular retry is still present.
  assert.match(card({ id: "e", status: "error", name: "huggingface.co/o/r" }), /data-action="retry"/);
});

test("preview does not post until confirm; confirm sends id + selection", async () => {
  const { sandbox, els, apiCalls, preview } = makeSandbox();
  loadFns(sandbox);
  await vm.runInContext(`openHFRecoveryModal("j1")`, sandbox);
  assert.equal(els.get("hf-recovery-modal").hidden, false);
  // Exactly one call: the GET preview.
  assert.equal(apiCalls.length, 1);
  assert.equal(apiCalls[0].path, "/api/jobs/j1/hf-recovery");
  assert.equal(apiCalls[0].opts?.method, undefined);
  // Auto-selected option enables the confirm button.
  assert.equal(els.get("hf-recovery-confirm").disabled, false);

  vm.runInContext(`hfRecoveryState.projector = "mmproj-f16.gguf"; updateHFRecoverySummary();`, sandbox);
  const summary = els.get("hf-rec-summary").textContent;
  // Q4_K_M (100B reused) + projector (50B to download) = 150B total.
  assert.match(summary, /100B/);
  assert.match(summary, /50B/);
  assert.match(summary, /150B/);
  await vm.runInContext(`submitHFRecovery()`, sandbox);
  assert.equal(apiCalls.length, 2);
  const post = apiCalls[1];
  assert.equal(post.opts.method, "POST");
  const sent = JSON.parse(post.opts.body);
  assert.equal(sent.revision, preview.revision);
  assert.equal(sent.filename, "model-Q4_K_M.gguf");
  assert.equal(sent.projector, "mmproj-f16.gguf");
  assert.equal(els.get("hf-recovery-modal").hidden, true);
});

test("ambiguous preview keeps confirm disabled until a selection", async () => {
  const { sandbox, els, preview } = makeSandbox();
  preview.selected_filename = "";
  loadFns(sandbox);
  await vm.runInContext(`openHFRecoveryModal("j1")`, sandbox);
  assert.equal(els.get("hf-recovery-confirm").disabled, true);
  vm.runInContext(`hfRecoveryState.selected = "model-Q8_0.gguf"; updateHFRecoverySummary();`, sandbox);
  assert.equal(els.get("hf-recovery-confirm").disabled, false);
  const summary = els.get("hf-rec-summary").textContent;
  assert.match(summary, /200B/);
});

test("preview error shows message and never posts", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  sandbox.__getFails = true;
  loadFns(sandbox);
  await vm.runInContext(`openHFRecoveryModal("j1")`, sandbox);
  assert.equal(els.get("hf-recovery-confirm").disabled, true);
  assert.match(els.get("hf-recovery-body").innerHTML, /hf_recover_error/);
  assert.equal(apiCalls.length, 1);
});

test("failed POST keeps modal open, toasts and re-enables controls", async () => {
  const { sandbox, els, apiCalls, toasts } = makeSandbox();
  sandbox.__postFails = true;
  loadFns(sandbox);
  await vm.runInContext(`openHFRecoveryModal("j1")`, sandbox);
  await vm.runInContext(`submitHFRecovery()`, sandbox);
  assert.equal(apiCalls.length, 2);
  assert.equal(els.get("hf-recovery-modal").hidden, false);
  assert.equal(els.get("hf-recovery-confirm").disabled, false);
  assert.equal(toasts.length, 1);
  assert.equal(toasts[0].kind, "error");
});
