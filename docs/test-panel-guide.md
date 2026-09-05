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
evaluation: {type: contains, expected: "foo"}  # scorer (simple) or fallback
evaluation_type: contains       # kept in sync with evaluation.type on load
required_caps: [tools]          # vision, tools, image, audio, thinking, ...
options: {temperature: 0.7, top_p: 0.9, max_tokens: 512}
```

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
| `example-instructions` | `instructions_regex.yaml` | cases EN/ES | `regex` |
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

| Type | Relevant fields | Description |
|------|----------------|-------------|
| `exact_match` | `expected` | Trimmed response equals expected |
| `contains` | `expected` | Case-insensitive substring (markdown/LaTeX normalized) |
| `contains_list` | `expected: [a, b]` | Any of the strings matches |
| `regex` | `pattern` | Go regexp match (invalid pattern = fail) |
| `json_schema` | `schema` | Only `{type: array, minItems, maxItems, items.type}` or `{type: object, required[]}` |
| `human_review` | — | No auto-check; rate with Pass/Fail in results (pending banner shows unrated) |
| `agent` | — | Excluded from battery; runs via agent sessions (§6) |

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
