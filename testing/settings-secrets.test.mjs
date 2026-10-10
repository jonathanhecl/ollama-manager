import assert from "node:assert/strict";
import { test } from "node:test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import vm from "node:vm";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const web = join(root, "web");
const settingsSrc = readFileSync(join(web, "app-settings.js"), "utf8");
const indexSrc = readFileSync(join(web, "index.html"), "utf8");
const i18nSrc = readFileSync(join(web, "i18n.js"), "utf8");

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
    this.value = "";
    this.placeholder = "";
    this.disabled = false;
    this.hidden = true;
    this.type = "text";
    this.title = "";
    this.checked = false;
    this.dataset = {};
    this.listeners = {};
    this.attrs = {};
    this.children = {};
  }
  get innerHTML() { return this._html; }
  set innerHTML(v) { this._html = String(v); }
  addEventListener(ev, fn) { (this.listeners[ev] ||= []).push(fn); }
  dispatch(ev, e) { for (const fn of this.listeners[ev] || []) fn(e || {}); }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k]; }
  removeAttribute(k) { delete this.attrs[k]; }
  querySelector(sel) {
    if (!this.children[sel]) this.children[sel] = new FakeEl(sel);
    return this.children[sel];
  }
  querySelectorAll() { return []; }
  scrollIntoView() {}
  focus() {}
  select() {}
}

function makeSandbox() {
  const els = new Map();
  const $ = (id) => {
    if (!els.has(id)) els.set(id, new FakeEl(id));
    return els.get(id);
  };
  const eyeButtons = new Map();
  for (const inputId of ["set-hf-token", "hf-ollama-key", "ext-model-apikey"]) {
    const btn = new FakeEl(`${inputId}-eye`);
    btn.dataset.secretEye = inputId;
    eyeButtons.set(inputId, btn);
  }
  const apiCalls = [];
  const toasts = [];
  const sandbox = {
    console,
    window: {},
    $,
    apiCalls,
    toasts,
    eyeButtons,
    settingsSecretState: undefined,
    editingExtModel: null,
    lastTestedExtModel: null,
    lastTestedCapabilities: null,
    currentConfig: null,
    navigator: {},
    api: (path, opts) => {
      apiCalls.push({ path, opts });
      if (sandbox.__deferReveal) {
        return new Promise((res, rej) => (sandbox.__pendingReveals ||= []).push({ res, rej }));
      }
      if (sandbox.__revealFails) return Promise.reject(new Error("reveal exploded"));
      if (path === "/api/settings/secrets/reveal") return Promise.resolve({ value: "synthetic_saved_secret" });
      return Promise.resolve({});
    },
    toast: (msg, kind) => toasts.push({ msg, kind }),
    t: (k, p) => (p ? `${k}:${JSON.stringify(p)}` : k),
    escapeHtml: (s) => String(s),
    sortCapabilityList: (c) => c,
    formatCapabilityLabel: (c) => c,
    loadExternalModels: () => {},
    refreshModels: () => {},
    setTimeout,
    Promise,
  };
  const eyeById = (inputId) => eyeButtons.get(inputId);
  sandbox.document = {
    addEventListener: () => {},
    querySelector: (sel) => {
      const m = /\[data-secret-eye="([^"]+)"\]/.exec(sel);
      return m ? eyeById(m[1]) : null;
    },
    querySelectorAll: (sel) => {
      if (sel === "[data-secret-eye]") return [...eyeButtons.values()];
      if (sel === ".settings-nav-item" || sel === ".settings-section-card") return [];
      return [];
    },
    visibilityState: "visible",
  };
  vm.createContext(sandbox);
  return { sandbox, els, apiCalls, toasts, eyeButtons };
}

const PRELUDE = `
  const settingsSecretState = new Map();
  var editingExtModel = null;
  var lastTestedExtModel = null;
  var lastTestedCapabilities = null;
  var currentConfig = null;
`;
const FNS = [
  "settingsSecretRec",
  "setSettingsSecretVisible",
  "getSettingsSecretDraft",
  "resetSettingsSecretVisibility",
  "toggleSettingsSecret",
  "bindSettingsSecretEvents",
  "bindHuggingFaceEvents",
  "updateHFTokenBadge",
  "syncHFTokenInput",
  "editExternalModel",
  "cancelEditExternalModel",
  "testExternalModel",
  "addExternalModel",
];

function loadFns(sandbox) {
  vm.runInContext(PRELUDE, sandbox);
  for (const name of FNS) {
    vm.runInContext(extract(settingsSrc, name), sandbox, { filename: `app-settings.js:${name}` });
  }
  vm.runInContext(`bindSettingsSecretEvents()`, sandbox);
}

test("markup: all three secrets are native password inputs with accessible SVG eye toggles", () => {
  const hf = /id="set-hf-token"[^>]*type="password"/.exec(indexSrc);
  assert.ok(hf, "hf token input not password");
  const key = /id="hf-ollama-key"[^>]*type="password"/.exec(indexSrc);
  assert.ok(key, "public key input not password");
  const ext = /id="ext-model-apikey"[^>]*type="password"/.exec(indexSrc);
  assert.ok(ext, "external api key input not password");
  const extTag = indexSrc.match(/<input[^>]*id="ext-model-apikey"[^>]*>/)[0];
  assert.equal(extTag.includes("input-masked"), false, "input-masked still present");
  assert.equal((indexSrc.match(/data-secret-eye=/g) || []).length, 3);
  assert.match(indexSrc, /data-secret-eye="set-hf-token"[^>]*aria-controls="set-hf-token"[^>]*aria-pressed="false"/);
  assert.match(indexSrc, /aria-hidden="true"/);
  assert.ok(indexSrc.includes("icon-eye") && indexSrc.includes("icon-eye-off"));
  assert.ok(i18nSrc.includes('"******** (saved'), "hf saved placeholder must use literal ********");
  assert.ok(i18nSrc.includes("settings.secret_show") && i18nSrc.includes("settings.secret_hide"));
});

test("typed HF value toggles locally with no API and keeps the draft", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  loadFns(sandbox);
  const input = sandbox.$("set-hf-token");
  input.type = "password";
  input.value = "typed_hf_secret";
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(input.type, "text");
  assert.equal(input.value, "typed_hf_secret");
  assert.equal(apiCalls.length, 0);
  const btn = sandbox.eyeButtons.get("set-hf-token");
  assert.equal(btn.getAttribute("aria-pressed"), "true");
  assert.equal(btn.getAttribute("data-i18n"), "settings.secret_hide");
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(input.type, "password");
  assert.equal(input.value, "typed_hf_secret");
  assert.equal(btn.getAttribute("data-i18n"), "settings.secret_show");
  assert.equal(vm.runInContext(`getSettingsSecretDraft("set-hf-token")`, sandbox), "typed_hf_secret");
});

test("saved HF token reveals on demand and never enters draft or config", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  loadFns(sandbox);
  sandbox.currentConfig = { has_hf_token: true };
  const input = sandbox.$("set-hf-token");
  input.type = "password";
  input.value = "";
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(apiCalls.length, 1);
  assert.equal(apiCalls[0].path, "/api/settings/secrets/reveal");
  assert.deepEqual(JSON.parse(apiCalls[0].opts.body), { kind: "hf_token" });
  assert.equal(input.type, "text");
  assert.equal(input.value, "synthetic_saved_secret");
  assert.equal(vm.runInContext(`getSettingsSecretDraft("set-hf-token")`, sandbox), "");
  assert.equal(JSON.stringify(sandbox.currentConfig).includes("synthetic_saved_secret"), false);
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(input.type, "password");
  assert.equal(input.value, "");
  input.value = "typed_now";
  input.dispatch("input");
  assert.equal(vm.runInContext(`getSettingsSecretDraft("set-hf-token")`, sandbox), "typed_now");
});

test("empty field without a stored secret makes no request and stays hidden", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  loadFns(sandbox);
  sandbox.currentConfig = { has_hf_token: false };
  const input = sandbox.$("set-hf-token");
  input.type = "password";
  input.value = "";
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(apiCalls.length, 0);
  assert.equal(input.type, "password");
});

test("external key reveals for the edited model only", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  loadFns(sandbox);
  const input = sandbox.$("ext-model-apikey");
  input.type = "password";
  input.value = "";
  vm.runInContext(`editingExtModel = { id: "m1", name: "m1", api_key: "••••••••" }`, sandbox);
  await vm.runInContext(`toggleSettingsSecret("ext-model-apikey")`, sandbox);
  assert.equal(apiCalls.length, 1);
  assert.deepEqual(JSON.parse(apiCalls[0].opts.body), { kind: "external_api_key", id: "m1" });
  assert.equal(input.value, "synthetic_saved_secret");
  assert.equal(input.type, "text");
  assert.equal(vm.runInContext(`getSettingsSecretDraft("ext-model-apikey")`, sandbox), "");

  vm.runInContext(`editingExtModel = null`, sandbox);
  vm.runInContext(`resetSettingsSecretVisibility("ext-model-apikey")`, sandbox);
  assert.equal(input.type, "password");
  assert.equal(input.value, "");
  await vm.runInContext(`toggleSettingsSecret("ext-model-apikey")`, sandbox);
  assert.equal(apiCalls.length, 1, "clone/no-edit must never reveal");
});

test("editExternalModel sets masked placeholder without prefetching", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  loadFns(sandbox);
  await vm.runInContext(`editExternalModel({ id: "m1", name: "m1", url: "http://x/v1", api_key: "••••••••", capabilities: ["tools"] })`, sandbox);
  const input = sandbox.$("ext-model-apikey");
  assert.equal(input.value, "");
  assert.match(input.placeholder, /^\*\*\*\*\*\*\*\*/);
  assert.equal(apiCalls.length, 0);
});

test("stale reveal result is ignored after user types or resets", async () => {
  const { sandbox, els } = makeSandbox();
  loadFns(sandbox);
  sandbox.currentConfig = { has_hf_token: true };
  sandbox.__deferReveal = true;
  const input = sandbox.$("set-hf-token");
  input.type = "password";
  input.value = "";
  const p = vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  input.value = "user_typing";
  input.dispatch("input");
  sandbox.__pendingReveals[0].res({ value: "synthetic_saved_secret" });
  await p;
  assert.equal(input.value, "user_typing");
  assert.equal(input.type, "password");

  input.value = "";
  input.type = "password";
  input.dispatch("input");
  input.value = "";
  const p2 = vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  vm.runInContext(`resetSettingsSecretVisibility("set-hf-token")`, sandbox);
  sandbox.__pendingReveals[1].res({ value: "synthetic_saved_secret" });
  await p2;
  assert.equal(input.value, "");
  assert.equal(input.type, "password");
});

test("reveal failure stays hidden, re-enables the eye and toasts", async () => {
  const { sandbox, els, toasts } = makeSandbox();
  loadFns(sandbox);
  sandbox.currentConfig = { has_hf_token: true };
  sandbox.__revealFails = true;
  const input = sandbox.$("set-hf-token");
  input.type = "password";
  input.value = "";
  const btn = sandbox.eyeButtons.get("set-hf-token");
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(input.type, "password");
  assert.equal(input.value, "");
  assert.equal(btn.disabled, false);
  assert.equal(toasts.length, 1);
  assert.equal(toasts[0].kind, "error");
});

test("busy eye does not fire a duplicate reveal", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  loadFns(sandbox);
  sandbox.currentConfig = { has_hf_token: true };
  sandbox.__deferReveal = true;
  const input = sandbox.$("set-hf-token");
  input.type = "password";
  const p = vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(apiCalls.length, 1);
  sandbox.__pendingReveals[0].res({ value: "synthetic_saved_secret" });
  await p;
});

test("public key eye is purely local and Copy still reads the same value", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  loadFns(sandbox);
  const input = sandbox.$("hf-ollama-key");
  input.type = "password";
  input.value = "ssh-ed25519 AAAAsynthetic";
  await vm.runInContext(`toggleSettingsSecret("hf-ollama-key")`, sandbox);
  assert.equal(apiCalls.length, 0);
  assert.equal(input.type, "text");
  assert.equal(input.value, "ssh-ed25519 AAAAsynthetic");
  await vm.runInContext(`toggleSettingsSecret("hf-ollama-key")`, sandbox);
  assert.equal(input.type, "password");
  assert.equal(input.value, "ssh-ed25519 AAAAsynthetic");
});

test("external add/test send empty api_key for a revealed saved key until user types", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  loadFns(sandbox);
  vm.runInContext(`editingExtModel = { id: "m1", name: "m1", api_key: "••••••••" }`, sandbox);
  const name = sandbox.$("ext-model-name"); name.value = "m1";
  const url = sandbox.$("ext-model-url"); url.value = "http://x/v1";
  const key = sandbox.$("ext-model-apikey"); key.type = "password"; key.value = "";
  await vm.runInContext(`toggleSettingsSecret("ext-model-apikey")`, sandbox);
  assert.equal(key.value, "synthetic_saved_secret");
  await vm.runInContext(`addExternalModel()`, sandbox);
  const addPost = apiCalls.find((c) => c.path === "/api/external-models");
  assert.ok(addPost, "no external-models POST");
  assert.equal(JSON.parse(addPost.opts.body).api_key, "");

  name.value = "m1";
  url.value = "http://x/v1";
  key.value = "typed_new_key";
  key.dispatch("input");
  await vm.runInContext(`testExternalModel()`, sandbox);
  const testPost = apiCalls.find((c) => c.path === "/api/external-models/test");
  assert.equal(JSON.parse(testPost.opts.body).api_key, "typed_new_key");
});

test("model switch via cancel resets the ext eye and invalidates a pending reveal", async () => {
  const { sandbox, els } = makeSandbox();
  loadFns(sandbox);
  vm.runInContext(`editingExtModel = { id: "m1", name: "m1", api_key: "••••••••" }`, sandbox);
  sandbox.__deferReveal = true;
  const input = sandbox.$("ext-model-apikey");
  input.type = "password";
  input.value = "";
  const p = vm.runInContext(`toggleSettingsSecret("ext-model-apikey")`, sandbox);
  await vm.runInContext(`cancelEditExternalModel()`, sandbox);
  sandbox.__pendingReveals[0].res({ value: "synthetic_saved_secret" });
  await p;
  assert.equal(input.value, "");
  assert.equal(input.type, "password");
});

test("eye icons toggle via hidden attribute, not expando", async () => {
  const { sandbox, els } = makeSandbox();
  loadFns(sandbox);
  const input = sandbox.$("set-hf-token");
  input.type = "password";
  input.value = "typed";
  const btn = sandbox.eyeButtons.get("set-hf-token");
  const eye = btn.querySelector(".icon-eye");
  const off = btn.querySelector(".icon-eye-off");
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(eye.getAttribute("hidden"), "");
  assert.equal(off.getAttribute("hidden"), undefined);
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(eye.getAttribute("hidden"), undefined);
  assert.equal(off.getAttribute("hidden"), "");
});

test("reset during pending reveal lets a second request own the eye", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  loadFns(sandbox);
  sandbox.currentConfig = { has_hf_token: true };
  sandbox.__deferReveal = true;
  const input = sandbox.$("set-hf-token");
  input.type = "password";
  input.value = "";
  const btn = sandbox.eyeButtons.get("set-hf-token");
  const pA = vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  vm.runInContext(`resetSettingsSecretVisibility("set-hf-token")`, sandbox);
  assert.equal(btn.disabled, false);
  const pB = vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(apiCalls.filter((c) => c.path === "/api/settings/secrets/reveal").length, 2);
  assert.equal(btn.disabled, true);
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(apiCalls.filter((c) => c.path === "/api/settings/secrets/reveal").length, 2);
  sandbox.__pendingReveals[0].res({ value: "stale_secret_A" });
  await pA;
  assert.equal(input.value, "");
  assert.equal(btn.disabled, true);
  sandbox.__pendingReveals[1].res({ value: "fresh_secret_B" });
  await pB;
  assert.equal(input.value, "fresh_secret_B");
  assert.equal(input.type, "text");
  assert.equal(btn.disabled, false);
});

test("typing during a pending reveal unblocks and ignores the stale reply", async () => {
  const { sandbox, els, apiCalls, toasts } = makeSandbox();
  loadFns(sandbox);
  sandbox.currentConfig = { has_hf_token: true };
  sandbox.__deferReveal = true;
  const input = sandbox.$("set-hf-token");
  input.type = "password";
  input.value = "";
  const btn = sandbox.eyeButtons.get("set-hf-token");
  const p = vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  input.value = "my_typed";
  input.dispatch("input");
  assert.equal(btn.disabled, false);
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(input.type, "text");
  assert.equal(input.value, "my_typed");
  assert.equal(apiCalls.filter((c) => c.path === "/api/settings/secrets/reveal").length, 1);
  sandbox.__pendingReveals[0].rej(new Error("late failure"));
  await p;
  assert.equal(toasts.length, 0);
  assert.equal(input.value, "my_typed");
});

test("HF Remove invalidates a pending reveal before the PATCH finishes", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  loadFns(sandbox);
  sandbox.currentConfig = { has_hf_token: true };
  vm.runInContext(`bindHuggingFaceEvents()`, sandbox);
  const input = sandbox.$("set-hf-token");
  input.type = "password";
  input.value = "";
  const clearBtn = sandbox.$("hf-token-clear-btn");
  sandbox.__deferReveal = true;
  const reveal = vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  const patches = [];
  const origApi = sandbox.api;
  sandbox.api = (path, opts) => {
    if (path === "/api/config") return new Promise((res) => patches.push(res));
    return origApi(path, opts);
  };
  clearBtn.dispatch("click");
  const btn = sandbox.eyeButtons.get("set-hf-token");
  assert.equal(input.type, "password");
  assert.equal(btn.disabled, false);
  sandbox.__pendingReveals[0].res({ value: "synthetic_saved_secret" });
  await reveal;
  assert.equal(input.value, "");
  assert.equal(input.type, "password");
  patches[0]({ has_hf_token: false });
});

test("settings save omits a revealed saved HF token but sends a typed draft", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  const opencodeSrc = readFileSync(join(web, "app-opencode.js"), "utf8");
  const marker = '$("settings-save").addEventListener("click", ';
  const mi = opencodeSrc.indexOf(marker);
  assert.notEqual(mi, -1);
  const arrow = opencodeSrc.indexOf("async () =>", mi);
  const bs = opencodeSrc.indexOf("{", arrow);
  let depth = 0, end = -1;
  for (let i = bs; i < opencodeSrc.length; i++) {
    if (opencodeSrc[i] === "{") depth++;
    else if (opencodeSrc[i] === "}") { depth--; if (!depth) { end = i; break; } }
  }
  const cbSrc = opencodeSrc.slice(arrow, end + 1);
  loadFns(sandbox);
  vm.runInContext(`var activeName = null;`, sandbox);
  Object.assign(sandbox, {
    normalizeNumCtxPct: (v) => v,
    saveGlobalChatDefaults: () => {},
    getModelChatOptions: () => null,
    applyChatDefaultsForModel: () => {},
    renderSettingsTranslations: () => {},
    refreshStatus: () => {},
    renderTable: () => {},
    openDetail: () => {},
    updateChatContextMeter: () => {},
    renderAttachments: () => {},
    renderChatMessages: () => {},
    renderChatQueue: () => {},
    updateStreamBar: () => {},
    updateChatCapabilityUI: () => {},
    updateChatSendEnabled: () => {},
    refreshOpenCodeUI: () => {},
    refreshGatewayStatusFromServer: () => Promise.resolve(),
    invalidateComfyCaches: () => {},
    refreshComfyChatUI: () => {},
    setTimeout: (fn) => {},
  });
  sandbox.window.I18n = { setLang: () => {} };
  sandbox.$("set-port").value = "11434";
  sandbox.$("set-language").value = "en";
  sandbox.currentConfig = { has_hf_token: true };
  vm.runInContext(`globalThis.__saveCb = ${cbSrc}`, sandbox);

  const input = sandbox.$("set-hf-token");
  input.type = "password";
  input.value = "";
  await vm.runInContext(`toggleSettingsSecret("set-hf-token")`, sandbox);
  assert.equal(input.value, "synthetic_saved_secret");
  await vm.runInContext(`__saveCb()`, sandbox);
  const patch = apiCalls.find((c) => c.path === "/api/config");
  assert.ok(patch);
  assert.equal(JSON.parse(patch.opts.body).hf_token, undefined);
  assert.equal(JSON.stringify(patch.opts.body).includes("synthetic_saved_secret"), false);

  input.value = "hf_newdraft";
  input.dispatch("input");
  await vm.runInContext(`__saveCb()`, sandbox);
  const patch2 = apiCalls.filter((c) => c.path === "/api/config")[1];
  assert.equal(JSON.parse(patch2.opts.body).hf_token, "hf_newdraft");
});

test("external test sends empty api_key while fetched saved key is shown", async () => {
  const { sandbox, els, apiCalls } = makeSandbox();
  loadFns(sandbox);
  vm.runInContext(`editingExtModel = { id: "m1", name: "m1", api_key: "••••••••" }`, sandbox);
  sandbox.$("ext-model-name").value = "m1";
  sandbox.$("ext-model-url").value = "http://x/v1";
  const key = sandbox.$("ext-model-apikey");
  key.type = "password";
  key.value = "";
  await vm.runInContext(`toggleSettingsSecret("ext-model-apikey")`, sandbox);
  assert.equal(key.value, "synthetic_saved_secret");
  await vm.runInContext(`testExternalModel()`, sandbox);
  const testPost = apiCalls.find((c) => c.path === "/api/external-models/test");
  assert.ok(testPost);
  assert.equal(JSON.parse(testPost.opts.body).api_key, "");
});
