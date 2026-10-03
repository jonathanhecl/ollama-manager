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
};
sandbox.document = {
  getElementById: (id) => (els.has(id) ? els.get(id) : null),
  querySelectorAll: () => [],
  documentElement: {},
  readyState: "complete",
  addEventListener() {},
};
sandbox.window = {
  I18n: { t: (k) => (k === "models.external_badge" ? "EXT" : k), getLang: () => "en" },
  addEventListener() {},
};
sandbox.window.I18n.t = sandbox.window.I18n.t;
sandbox.globalThis = sandbox;
vm.createContext(sandbox);

const helpersSrc = readFileSync(join(web, "app-helpers.js"), "utf8");
vm.runInContext(helpersSrc, sandbox, { filename: "app-helpers.js" });

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

// --- chat select: option values are routing IDs, labels disambiguate ---
const sel = fakeEl("chat-model");
const chatSrc = readFileSync(join(web, "app-chat.js"), "utf8");
Object.assign(sandbox, {
  models: [
    { name: "llama3:latest", remote_name: "", is_external: false },
    { name: "dup", remote_name: "dup", is_external: true, provider: "oMLX", url: "http://a:1/v1" },
    { name: "dup@ext-abcdef012345", remote_name: "dup", is_external: true, provider: "vLLM", url: "http://b:2/v1" },
    { name: "solo", remote_name: "solo", is_external: true, provider: "", url: "http://c:3/v1" },
  ],
  activeName: "",
  applySort: (a) => a,
  updateChatModelLoadDot() {},
});
vm.runInContext(chatSrc, sandbox, { filename: "app-chat.js" });

sandbox.syncChatModelOptions();
const opts = [...sel.innerHTML.matchAll(/<option value="([^"]*)">([^<]*)<\/option>/g)].map((m) => ({ value: m[1], text: m[2] }));

const byValue = Object.fromEntries(opts.map((o) => [o.value, o.text]));
assert.ok(byValue["dup"], "dup ID option missing");
assert.ok(byValue["dup@ext-abcdef012345"], "hashed ID option missing");
assert.match(byValue["dup"], /dup .*oMLX.*a:1/s);
assert.match(byValue["dup@ext-abcdef012345"], /dup .*vLLM.*b:2/s);
assert.equal(byValue["solo"], "solo");
assert.equal(byValue["llama3:latest"], "llama3:latest");

console.log("external-provider JS tests: all assertions passed");
