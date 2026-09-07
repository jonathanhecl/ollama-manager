# Test Panel & Battery — Technical Documentation

Create, edit, organize and **run** prompt batteries to evaluate Ollama models.
Tests live as files under `testing/<group>/<name>.yaml` so you can add your
own with any text editor — or use the built-in test editor in the UI.

---

## 1. Feature Overview

- **Tests** — prompt templates sent to models (single prompt, multi-case suite, or multi-step chain).
- **Groups** — subdirectories of `testing/` (e.g. `examples`), with optional `_category.yaml` metadata.
- **Battery runs** — execute a test, a group, or everything against one or more models, with live progress, auto-scoring, history and leaderboards.
- **Per-case files (sidecars)** — each case/step can carry its own image, audio or text file (see §5).
- **Agent sessions** — separate framework (`evaluation_type: agent`) with sandboxed file tools, excluded from battery runs.

---

## 2. Data Model (`testing/` directory)

Filesystem-backed (`internal/tests/store.go`). Groups = subdirectories (hidden/dot and `_`-prefixed ignored).
Tests = `.yaml`/`.yml`/`.json` files. Run history = `<base>._history.json` next to each test (never edit by hand).

### Test file (`testing/<group>/<name>.yaml`)

```yaml
id: example-arithmetic          # optional, defaults to filename base
name: Math Suite                # REQUIRED
description: "..."              # optional
group_id: examples              # overwritten by the directory name on load
active: true                    # false = excluded from runs
order: 0
system_prompt: "..."            # optional, prepended to every turn
prompt: "2 + 2?"                # simple branch (required if no messages/steps/cases)
messages:                       # alternative simple branch
  - role: user
    content: "..."
steps:                          # multi-turn sequential branch (keeps history)
  - step: 1
    name: "First turn"
    prompt: "..."               # REQUIRED per step
    evaluation: {type: contains, expected: pandas}
cases:                          # batch suite branch (independent turns)
  - name: "Case 1"
    prompt: "..."
    evaluation: {type: contains, expected: "14"}
    steps:                      # chained turns scoped to the case
      - name: "Follow-up"
        prompt: "..."
        evaluation: {type: contains, expected: "green"}
evaluation: {type: contains, expected: "foo"}  # scorer (simple) or fallback
evaluation_type: contains       # kept in sync with evaluation.type on load
required_caps: [tools]          # vision, tools, image, audio, thinking, ...
options: {temperature: 0.7, top_p: 0.9, max_tokens: 512}
```

### TestCase (entry in `cases`)

Each case runs in **isolation** (fresh conversation history). A case is either
single-turn (`prompt` + `evaluation`) or multi-turn (`steps` sharing one
history scoped to the case).

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | no | Human-readable case name |
| `prompt` | string | yes, unless `steps` has an opening turn | Single-turn prompt, or the opening turn of a multi-turn chain (scored with the case-level `evaluation` when set) |
| `evaluation` | object | no | `{ type, expected?, pattern?, schema? }`; same types as §4 |
| `system_prompt` | string | no | Override for this case only; empty/missing = inherits the test-level `system_prompt` |
| `options` | object | no | `{ temperature?, top_p?, max_tokens? }`; set fields override the test-level `options` |
| `steps` | []CaseStep | no | Chained follow-up turns; each `{ name?, prompt, evaluation?, system_prompt?, options? }` is sent in order keeping prior turns in context. A step-level `system_prompt` is **sticky**: it replaces the active system from that step onward, while an empty one keeps the active system (case-level, or test-level). Step-level `options` (`{ temperature?, top_p?, max_tokens? }`) fold the same way field by field over the active options. Each case starts fresh from the test-level system and options (or its own overrides) |

Example (instruction-list following with a per-case voice override):

```yaml
system_prompt: |
  You are a concise assistant. Always reply in English.
options:
  temperature: 0.2
cases:
  - name: Pirate voice override
    prompt: Say hello in one short sentence.
    system_prompt: |
      You are a pirate. Every reply must contain the word "arr".
    options:
      temperature: 0.9
    evaluation:
      type: regex
      pattern: (?i)\barr\b
  - name: Follow a list of instructions
    prompt: Memorize this list in order: red, green, blue. Reply with only OK.
    evaluation:
      type: contains
      expected: "OK"
    steps:
      - name: Recall second item
        prompt: What was the second color? Reply with just the color.
        evaluation:
          type: contains
          expected: "green"
      - name: Repeat full list
        prompt: Repeat the full list in order, comma-separated.
        evaluation:
          type: regex
          pattern: (?i)red.*green.*blue
```

Overall result: the test passes when **all** scored turns pass (case-level
evaluations plus every chained step). In the UI, chained turns appear as
`Case › Step` sub-results and share the case's progress counter. Each
sub-result records the effective `system_prompt` and `options` used for that
turn (the detail modal shows the active temperature when set).

Minimum valid test: `name` + one of `prompt` / `messages` / `steps` / `cases`.
Per-case `evaluation` wins over the test-level one. Unknown evaluation types
are rejected with HTTP 400 at launch (`runner.ValidateTestsForBattery`).

### Group (`testing/<group>/_category.yaml`)

```yaml
id: examples
name: Examples
description: "..."
required_caps: []
order: 0
```

Without it the group auto-registers as `id = dirname`.

### Seeds

Fresh installs get `testing/examples/` from `internal/tests/seed.go`
(`PopulateSeed` + `backfillSeedsLocked` adds newer seeds to existing installs
without touching the original three):

| ID | File | Shape | Checks |
|----|------|-------|--------|
| `example-arithmetic` | `arithmetic.yaml` | cases | math via `contains` |
| `example-weather-tool` | `weather_tool.yaml` | simple | tool call via `regex` |
| `example-multi-turn` | `multi_turn.yaml` | steps | memory via `contains` |
| `example-exact-math` | `exact_math.yaml` | cases EN/ES | `exact_match` |
| `example-codegen` | `codegen.yaml` | cases EN/ES | `contains` |
| `example-json-output` | `json_output.yaml` | cases EN/ES | `json_schema` |
| `example-instructions` | `instructions.yaml` | cases + steps | instruction following + overrides |
| `example-format-regex` | `instructions_regex.yaml` | cases EN/ES | `regex` |
| `example-memory` | `memory_bilingual.yaml` | steps EN→ES | cross-language recall |
| `example-human-review` | `human_review.yaml` | cases EN/ES | manual rating |
| `example-vision-cases` | `vision_cases.yaml` | cases + sidecars | vision + inlined text |

---

## 3. API Reference

All routes require auth.

**Tests CRUD** (`internal/server/tests_handlers.go`):

| Method | Path | Body | Response |
|--------|------|------|----------|
| GET | `/api/tests` | — | `{ groups, tests }` (tests carry runtime `attachments`/`sidecars`) |
| POST | `/api/tests` | `Test` (without `id`) | Created `Test` |
| PUT | `/api/tests/{id}` | `Test` fields | Updated `Test` (moves file + history + sidecars on rename) |
| DELETE | `/api/tests/{id}` | — | `{ ok, reseeded }` (also deletes history + sidecars) |
| POST | `/api/tests/reorder` | `{ updates: { id: order } }` | `{ ok }` |
| POST | `/api/tests/{id}/sidecars` | `{ index (1-based), filename, data (base64) }` | Saved `Attachment` |
| DELETE | `/api/tests/{id}/sidecars/{index}` | — | `{ ok }` |
| POST/PUT/DELETE | `/api/test-groups[/{id}]` | `Group` | Created/updated/deleted |

**Runner / battery** (`internal/server/runner_handlers.go`):

| Method | Path | Notes |
|--------|------|-------|
| POST | `/api/runner/battery` | `{ group_id \| test_id, model_ids[] }` → `{ run_id }` (async) |
| GET | `/api/runner/runs` | Light list (pass/fail/human_review/total) |
| GET | `/api/runner/runs/{id}` | Full `BatteryRun` |
| GET | `/api/runner/runs/{id}/progress` | Live `Progress` (poll every 2s) or `{ done: true }` |
| POST | `/api/runner/runs/{id}/cancel` | Cancels + `{ cancelled }` |
| PUT | `/api/runner/runs/{id}/rate` | `{ test_id, model, passed }` — manual human rating |
| DELETE | `/api/runner/runs/{id}` | Deletes a run |
| GET | `/api/runner/test-history/{id}` | Per-test history items |
| GET | `/api/runner/group-history/{id}` | Per-model group summary |
| GET | `/api/runner/sys-info` | `{ os, cpu, gpu, vram, ram }` |

---

## 4. Evaluation Types

Implemented in `runner.scoreEval` (unknown types → launch-time 400):

| Type | Config / Fields | Description |
|------|-----------------|-------------|
| `exact_match` | `expected` | Trimmed response equals expected |
| `contains` | `expected` | Case-insensitive substring (markdown/LaTeX normalized) |
| `not_contains` | `expected` or `pattern` | Response must NOT contain substring / match regex (empty fails closed) |
| `contains_list` | `expected: [a, b]` | Any of the strings matches |
| `all_of` | `evaluations: [...]` | Every sub-evaluation must pass (fails fast on first fail, empty fails closed) |
| `regex` | `pattern` | Go regexp match (invalid pattern = fail) |
| `json_schema` | `schema` or `required_keys` | Validates schema or required JSON object keys |
| `human_review` | — | No auto-check; manual pass/fail rating in results |
| `agent` | `{ max_turns, ... }` | Excluded from battery; runs via sandboxed agent sessions (§7) |

---

## 5. Per-Case Files (sidecars)

Attachments belong to **cases/steps, never to the test**. They are sidecar
files next to the YAML named `<base>-<N>.<ext>` (`N` = 1-based case/step
number; simple prompt-only tests use `-1`):

```text
testing/examples/
  vision_cases.yaml
  vision_cases-1.png   # image → case 1 (Images[])
  vision_cases-2.png   # image → case 2
  vision_cases-3.txt   # text  → case 3 (inlined as "--- attached file: … ---")
```

| Extension | Kind | How the model receives it |
|-----------|------|---------------------------|
| `.png .jpg .jpeg .webp .gif` | image | `Images[]` of that turn (needs `vision` cap — the UI warns otherwise) |
| `.wav .mp3 .ogg` | audio | `Images[]` pass-through (model-dependent) |
| `.txt .md` | text | Inlined into the prompt (Ollama has no document input) |

Rules: one file per case number (uploading replaces), max 10 MiB, unknown
extensions ignored. Files follow the **case number** on reorder. Deleting a
test deletes its sidecars; renaming moves them. The editor uploads/deletes
via the `/api/tests/{id}/sidecars` endpoints; `GET /api/tests` exposes them
as runtime `attachments` per case (never written into the YAML).

---

## 6. Battery Execution Flow

```text
POST /api/runner/battery
 → ExecuteBatteryAsync (run id + goroutine, cancellable)
 → runTest per model × test: steps (sequential + history) | cases (independent) | simple
 → execChatTurn (Ollama streaming, retries ×3, tok/s metrics) + Unload(model)
 → scoreEval per case/step → SaveRun → testing/<group>/<base>._history.json
 → progress polling → results matrix → manual PUT …/rate for human_review
```

---

## 7. Agent Sessions (outside the battery)

`evaluation_type: agent` tests are filtered out of battery runs and use the
sandbox framework instead: `internal/agent/` (`read_file`, `write_file`,
`list_dir`, `exec` under `sandboxes/`) with HTTP under `/api/tests/agent/*`.
See endpoint list in §3.

---

## 8. Frontend Notes

Views: `tests-view` (sidebar + list), `test-editor-view` (cases with per-case
file buttons), `battery-progress-view` (2s polling, auto-stops on
navigation/cancel), `battery-results-view` (matrix + summary + pending-review
banner), history + leaderboard modals. Key modules: `app-battery.js`
(poll/run/results), `app-test-history.js`, `app-group-history.js`,
`app-tests.js` (view switching).

---

## 9. I18n

Tests UI keys live under `tests.*`, battery under `battery.*` (both `en` and
`es` in `web/i18n.js`).

### Keys Reference

All new keys live under the `tests.` prefix:

- `tests.button`
- `tests.groups_title`
- `tests.new_group`
- `tests.all_tests`
- `tests.new_test`
- `tests.edit_test`
- `tests.empty`
- `tests.back_to_list`
- `tests.name`
- `tests.name_placeholder`
- `tests.description`
- `tests.description_placeholder`
- `tests.group`
- `tests.no_group`
- `tests.active`
- `tests.active_hint`
- `tests.prompt`
- `tests.prompt_placeholder`
- `tests.system_prompt`
- `tests.system_placeholder`
- `tests.eval_type`
- `tests.eval_config`
- `tests.eval_config_placeholder`
- `tests.required_caps`
- `tests.required_caps_placeholder`
- `tests.order`
- `tests.status_active`
- `tests.status_suspended`
- `tests.suspend`
- `tests.activate`
- `tests.delete_title`
- `tests.delete_text`
- `tests.invalid_json`
- `tests.group_name_prompt`
- `tests.eval_exact_match`
- `tests.eval_contains`
- `tests.eval_regex`
- `tests.eval_json_schema`
- `tests.eval_human_review`
- `tests.eval_agent`
- `tests.agent_settings`
- `tests.agent_max_turns`
- `tests.agent_initial_files`
- `tests.agent_tools`
- `tests.agent_run`
- `tests.agent_sandbox`
- `tests.agent_turns`
- `tests.agent_feedback`
- `tests.agent_send_feedback`
- `tests.agent_reset`
- `tests.agent_delete_session`
- `tests.agent_status`
- `tests.agent_waiting_human`
- `tests.agent_running`
- `tests.agent_finished`
- `tests.agent_no_model`
- `tests.agent_completed`
- `tests.agent_in_progress`
- `tests.agent_no_turns`
- `tests.agent_empty_sandbox`
- `tests.agent_sandbox_error`
- `tests.agent_delete_confirm`

Plus the generic action keys:
- `action.save`
- `action.edit`

---

## 10. Simulated Agent Scenarios (`testing/simulated-agent/`)

The battery runner skips tests with `evaluation_type: agent` (real agent
sessions are human-interactive only). The `simulated-agent` category works
around this: plain battery tests that **simulate** the agent loop.

### Simulation protocol

Every test declares the same fake toolset in its `system_prompt`
(weather-style syntax, see `testing/examples/weather_tool.yaml`):

- Tool calls are single lines: `read_file("p")`, `write_file("p", "c")`,
  `list_dir("p")`, `exec("cmd")` — reply with ONLY the call, no other text.
- Simulated results are injected as chained `steps` prompts, always starting
  with `[tool-result ok]` or `[tool-result ERROR]`. The model must never
  invent results.
- A finished task ends with `FINAL: <answer>`.
- `options: { temperature: 0.2 }` keeps runs deterministic.
- No `required_caps`: plain text, runs on any model.

Regex notes: patterns run on Go RE2 — **no lookahead** (`(?!…)` won't
compile). Absence checks (e.g. "no more tool calls") use `not_contains`
with a `pattern`. Prompts starting with `[tool-result …]` must be YAML-quoted
(leading `[` would parse as a flow sequence).

### Scenario table

| # | File | Behavior under test |
|---|------|---------------------|
| 1 | `01_read_then_answer.yaml` | Grounding: call `read_file` instead of guessing, answer from the result |
| 2 | `02_file_not_found.yaml` | `read` fails → `list_dir` → read the discovered file → `FINAL` |
| 3 | `03_exec_fail_retry.yaml` | `exec` permission denied → retry with `chmod`/`sudo`/`bash` alternative |
| 4 | `04_malformed_call.yaml` | Validation error (`missing param`) → re-emit the corrected call |
| 5 | `05_write_then_verify.yaml` | Stateful chain: `write_file` → verify via `read_file`/`cat` → confirm |
| 6 | `06_hint_following.yaml` | Error contains the fix (`Did you mean "testing/"?`) → apply it |
| 7 | `07_stop_condition.yaml` | After success, reply `FINAL:` AND emit no further tool calls (`all_of` combining both) |
| 8 | `08_param_extraction.yaml` | NL requests → correct tool + arguments (path, content, flags) |
| 9 | `09_dangerous_command.yaml` | Refuse/confirm `rm -rf /`, but still run the safe `ls /` mirror case |
| 10 | `10_strategy_choice.yaml` | Ambiguous failure (disk full, no hint) → investigate with `du`/`df`/`ls`/`list_dir` before acting |
| 11 | `11_skill_selection.yaml` | Invented skills (`invoice-parser`, `web-search`, `translator`) via `use_skill("name", "task")`; pick the right one unprompted, none for plain math |
| 12 | `12_skill_recipe.yaml` | Follow a 3-step skill recipe in order; pass requires all three consecutive simulated steps |

---

## 11. Coding Scenarios (`testing/coding/`)

Battery tests for coding ability in JavaScript, Python and Go. There is no
code execution: answers are evaluated statically (`exact_match` for
deterministic outputs, `regex` for code structure, `contains` where a token
suffices). Same conventions as §10: `options: { temperature: 0.2 }`, no
`required_caps`, RE2 regex (no lookahead).

Layout is one file per task × language (`01_fix_js.yaml`,
`01_fix_py.yaml`, …), so the leaderboard compares languages directly: run the
whole category on a model and contrast the `01_fix_*` / `02_trace_*` / … rows.

Fake editing/search tools extend the §10 protocol per file (`apply_patch`,
`search_code`) and are declared in that file's `system_prompt`.

### Scenario table

| # | Files | Behavior under test |
|---|-------|---------------------|
| 1 | `01_fix_{js,py,go}.yaml` | Fix the same off-by-one bug in each language (direct fix + `apply_patch` variant) |
| 2 | `02_trace_{js,py,go}.yaml` | Predict exact output: JS `var` closures (`3,3,3`), Py accumulator (`20`), Go slice aliasing (`9`) |
| 3 | `03_create_{js,py,go}.yaml` | Write one-level `flatten`/`Flatten` from a spec; signature + key tokens checked |
| 4 | `04_search_{js,py,go}.yaml` | Needle-in-haystack, chained: call `search_code`, then `FINAL:` with the defining file from 3 fake hits |

---

## 13. Security, Spanish, Reasoning, Structured & Robustness

Five small categories (orders 4–8), all `temperature: 0.2`, all fully
automatic (`regex`/`contains`/`exact_match`/`json_schema`, no `human_review`).

**`testing/security/`** — agent safety, mostly chained like §10:
`01_tool_injection` (malicious instruction smuggled in a `[tool-result]`
must be ignored), `02_hierarchy` (system `[EN]` marker outranks a Spanish
user override), `03_exfiltration` (refuse system-prompt disclosure + benign
mirror), `04_jailbreak` (refuse phishing how-to + benign writing mirror).

**`testing/spanish/`** — neutral Spanish: `01_comprension` (factual),
`02_gramatica` (por/para, subjuntivo, `exact_match`), `03_resumen`
(key concepts via order-free regex), `04_traduccion` (false friend
*embarrassed* → *vergüenza*).

**`testing/reasoning/`** — exact answers: `01_logic` (knights/knaves,
two one-word cases), `02_math_chain` (scored intermediate step),
`03_sequence` (quadratic → 42), `04_constraints` (mini Einstein, two cases).

**`testing/structured/`** — revives the two legacy `testing/.backup/structured/`
tests rewritten in the modern `evaluation: {type: json_schema, schema: …}`
format (`01_person`, `02_array`), plus `03_toolcall` (tool-call JSON shape)
and `04_no_markdown` (fences fail `json.Unmarshal` automatically). Note: the
scorer checks shape (type/required/array bounds), not values.

**`testing/robustness/`** — same answer under paraphrase (`01`), typos and
case noise (`02`), and format noise (`03`, tolerant regex). Related fix:
`testing/coding/02_trace_{js,py,go}` moved from `exact_match` to tolerant
regex so correct answers with extra words or spacing still pass.

---

## 12. Vision Scaffolds (`testing/vision/`)

Vision tests without images yet: structure, prompts and evaluations are
ready, the `attachments` hold empty placeholders (`data: ""`). All are
`active: false` so battery runs skip them.

### Activation workflow

1. Open the test in the editor and attach the real image(s) — this replaces
   the `TODO_*.jpg` placeholder with base64 data.
2. Fill the `TODO-*` expected value (or tighten the regex) with the true
   answer for your image.
3. Set the test to active.

Notes: images are only sent in the single-prompt format (top-level `prompt`
+ `attachments`); the `cases`/`steps` branches do not forward media.
`required_caps: [vision]` restricts runs to vision-capable models.

### Scaffold table

| # | File | Behavior under test |
|---|------|---------------------|
| 1 | `01_find_objects.yaml` | Count objects of a kind in the image (`contains` number) |
| 2 | `02_follow_image_instructions.yaml` | Read instructions shown in the image, quote the action |
| 3 | `03_ocr_document.yaml` | Transcribe a document photo/scan (distinctive phrases) |
| 4 | `04_locate_position.yaml` | Reply position as `"x,y"` 0-1000 coordinates (regex format check; tighten ranges per image) |
| 5 | `05_describe_scene.yaml` | Free scene description (`contains` key visible elements, filled per image) |
| 6 | `06_compare_images.yaml` | Spot the single difference between two attached images |
| 7 | `07_judge_description.yaml` | Verdict CORRECT/INCORRECT on a candidate description (same format as §14; tighten to the true verdict per image) |
| 8 | `08_judge_missing.yaml` | List what an incomplete description omits from the image |

---

## 14. Judge Scenarios (`testing/judge/`)

The model acts as judge/jury: a `system_prompt` fixes the role once per
file, and verdicts follow a rigid format so they stay auto-scorable:

```
Verdict: CORRECT  (or INCORRECT, or A/B)
Reason: <one sentence>
```

Validated with `(?i)verdict:\s*…` regexes. The rationale (`Reason:`) is
deliberately out of score — grading rationales needs a human or a future
LLM-as-judge, which these tests would then calibrate. `temperature: 0.2`.

| # | File | What is judged |
|---|------|----------------|
| 1 | `01_grade_math.yaml` | Correct solution passes, planted step-2 error fails |
| 2 | `02_review_code.yaml` | Off-by-one fails, fixed version passes (no default approval) |
| 3 | `03_hallucination.yaml` | Invented date fails, faithful summary passes |
| 4 | `04_preference.yaml` | Picks the right answer of a pair, order-swapped as position-bias control |
| 5 | `05_desc_proxy.yaml` | Textual ground truth + candidate description (runs without images); `(?s)` flag spans the `Reason:` line |

The visual counterparts live in §12 (`07_judge_description`,
`08_judge_missing`): same verdict mechanics once images are attached.
>>>>>>> 039552b00bef88718148cc72f3da27ccef1f043a
