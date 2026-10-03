import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import vm from "node:vm";

const web = join(dirname(fileURLToPath(import.meta.url)), "..", "web");

const els = new Map();
const fakeEl = (id) => {
  if (!els.has(id)) {
    els.set(id, {
      id,
      value: "",
      innerHTML: "",
      textContent: "",
      title: "",
      hidden: false,
      selectedOptions: [],
      addEventListener() {},
      focus() {},
      select() {},
      scrollIntoView() {},
      querySelectorAll: () => [],
      classList: { toggle() {}, add() {}, remove() {} },
      dataset: {},
    });
  }
  return els.get(id);
};

const sandbox = {
  console,
  URL,
  requestAnimationFrame: (fn) => fn(),
  I18n: { t: (k) => (k === "models.external_badge" ? "EXT" : k), getLang: () => "en" },
  addEventListener() {},
  document: {
    getElementById: (id) => (els.has(id) ? els.get(id) : null),
    querySelectorAll: () => [],
    documentElement: {},
    readyState: "complete",
    addEventListener() {},
  },
};
sandbox.window = sandbox;
sandbox.globalThis = sandbox;
vm.createContext(sandbox);

vm.runInContext(readFileSync(join(web, "app-helpers.js"), "utf8"), sandbox, { filename: "app-helpers.js" });

const { externalProviderLabel, externalCleanEndpoint } = sandbox;

assert.equal(externalProviderLabel(""), "EXT");
assert.equal(externalProviderLabel(null), "EXT");
assert.equal(externalProviderLabel("   "), "EXT");
assert.equal(externalProviderLabel("system"), "EXT");
assert.equal(externalProviderLabel(" SYSTEM "), "EXT");
assert.equal(externalProviderLabel("omlx"), "oMLX");
assert.equal(externalProviderLabel("OMLX"), "oMLX");
assert.equal(externalProviderLabel("vLLM"), "vLLM");
assert.equal(externalProviderLabel("  LM Studio "), "LM Studio");

assert.equal(externalCleanEndpoint("http://host:1234/v1"), "http://host:1234");
assert.equal(externalCleanEndpoint("http://host:1234/v1/chat/completions"), "http://host:1234");
assert.equal(externalCleanEndpoint("http://u:p@host:1234/v1/"), "http://host:1234");

const chatSrc = readFileSync(join(web, "app-chat.js"), "utf8");
Object.assign(sandbox, {
  models: [
    { name: "llama3:latest", remote_name: "", is_external: false },
    { name: "dup", remote_name: "dup", is_external: true, provider: "oMLX", url: "http://a:1/v1" },
    { name: "dup@ext-abcdef012345", remote_name: "dup", is_external: true, provider: "vLLM", url: "http://b:2/v1" },
    { name: "dup@ext-ffffeeeedddd", remote_name: "dup", is_external: true, provider: "vLLM", url: "http://b2:9/v1" },
    { name: "solo", remote_name: "solo", is_external: true, provider: "", url: "http://c:3/v1" },
  ],
  activeName: "",
  applySort: (a) => a,
  updateChatModelLoadDot() {},
});
const sel = fakeEl("chat-model");
vm.runInContext(chatSrc, sandbox, { filename: "app-chat.js" });

sandbox.syncChatModelOptions();
const opts = [...sel.innerHTML.matchAll(/<option value="([^"]*)">([^<]*)<\/option>/g)].map((m) => ({ value: m[1], text: m[2] }));
const byValue = Object.fromEntries(opts.map((o) => [o.value, o.text]));

assert.ok(byValue["dup"], "dup ID option missing");
assert.ok(byValue["dup@ext-abcdef012345"], "hashed ID option missing");
assert.match(byValue["dup"], /dup .*oMLX.*a:1/s);
assert.match(byValue["dup@ext-abcdef012345"], /dup .*vLLM.*b:2/s);
assert.match(byValue["dup@ext-ffffeeeedddd"], /dup .*vLLM.*b2:9/s);
assert.notEqual(byValue["dup@ext-abcdef012345"], byValue["dup@ext-ffffeeeedddd"]);
assert.equal(byValue["solo"], "solo");
assert.equal(byValue["llama3:latest"], "llama3:latest");

const apiCalls = [];
sandbox.api = async (path, opts) => {
  apiCalls.push({ path, opts });
  if (path === "/api/external-models") {
    return {
      models: [
        { id: "dup", name: "dup", url: "http://user:pass@a:1/v1", api_key: "••••••••", provider: "oMLX", capabilities: ["completion"], disabled: false },
        { id: "dup@ext-abcdef012345", name: "dup", url: "http://b:2/v1", provider: "vLLM", capabilities: ["completion"], disabled: true },
      ],
    };
  }
  throw new Error("unexpected api " + path);
};
sandbox.toast = () => {};
sandbox.t = (k) => k;
sandbox.currentConfig = { language: "en" };
sandbox.refreshModels = () => {};
sandbox.renderTableCalls = 0;
sandbox.renderTable = () => { sandbox.renderTableCalls++; };
let chatSyncCalls = 0;
const origSync = sandbox.syncChatModelOptions;
sandbox.syncChatModelOptions = () => { chatSyncCalls++; return origSync(); };

vm.runInContext(readFileSync(join(web, "app-settings.js"), "utf8"), sandbox, { filename: "app-settings.js" });

fakeEl("ext-models-list");
fakeEl("ext-models-badge");
fakeEl("ext-models-nav-badge");

const entryA = sandbox.models.find((m) => m.name === "dup");
const entryB = sandbox.models.find((m) => m.name === "dup@ext-abcdef012345");
entryA.provider = "";
entryB.provider = "";

await sandbox.loadExternalModels("en");

const listHtml = els.get("ext-models-list").innerHTML;
assert.match(listHtml, /data-name="dup"/, "card must key by raw-name ID");
assert.match(listHtml, /data-name="dup@ext-abcdef012345"/, "card must key by hashed ID");
assert.match(listHtml, />oMLX</, "provider badge must render oMLX");
assert.match(listHtml, />vLLM</, "provider badge must render vLLM");

assert.equal(entryA.provider, "oMLX");
assert.equal(entryB.provider, "vLLM");
assert.equal(entryA.url, "http://a:1/v1", "reconciliation must not overwrite sanitized url with raw credential url");
assert.ok(sandbox.renderTableCalls >= 1, "provider update must re-render table");
assert.ok(chatSyncCalls >= 1, "provider update must re-sync chat options");

const nameInput = fakeEl("ext-model-name");
const urlInput = fakeEl("ext-model-url");
const keyInput = fakeEl("ext-model-apikey");
fakeEl("ext-model-cancel-btn");
fakeEl("ext-model-add-btn");
fakeEl("ext-test-result");
nameInput.value = "";
urlInput.value = "";
keyInput.value = "";

sandbox.editExternalModel({ id: "dup@ext-abcdef012345", name: "dup", url: "http://b:2/v1", api_key: "••••••••", capabilities: ["completion"], disabled: true });
apiCalls.length = 0;
await sandbox.addExternalModel();
const saveCall = apiCalls.find((c) => c.path === "/api/external-models" && c.opts?.method === "POST");
assert.ok(saveCall, "edit must POST /api/external-models");
const saveBody = JSON.parse(saveCall.opts.body);
assert.equal(saveBody.id, "dup@ext-abcdef012345");
assert.equal(saveBody.name, "dup");
assert.equal(saveBody.old_name, undefined);

sandbox.cancelEditExternalModel();
nameInput.value = "dup-clone";
urlInput.value = "http://c:9/v1";
keyInput.value = "";
apiCalls.length = 0;
await sandbox.addExternalModel();
const cloneCall = apiCalls.find((c) => c.path === "/api/external-models" && c.opts?.method === "POST");
const cloneBody = JSON.parse(cloneCall.opts.body);
assert.equal(cloneBody.id, "", "clone/add must not send an id");

console.log("external-provider JS tests: all assertions passed");
