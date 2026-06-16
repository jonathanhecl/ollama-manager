# Test Panel — Technical Documentation

A dedicated desktop-only panel for creating, editing, organizing, and grouping test prompts (test batteries) used to evaluate Ollama models. Standard tests are **management-only** (no execution yet), but **multi-turn agent tests** can be run in sandboxed sessions with file tools and human feedback.

---

## 1. Feature Overview

The test panel lets users:
- Create **tests** — prompt templates that will later be sent to models.
- Organize tests into **groups** — e.g. "Tools", "Vision", "Math", "Multi-Turn Agent".
- Activate or **suspend** individual tests — suspended tests are excluded from future battery runs.
- Edit every aspect of a test: prompt, system prompt, evaluation type, evaluation config, required model capabilities, and ordering.
- Run **multi-turn agent tests** — sandboxed sessions with file tools, turn history, and human-in-the-loop feedback.

The panel is available only on desktop (`window.innerWidth > 900`) via a 🧪 button in the topbar.

---

## 2. Data Model (`tests.json`)

Stored next to `config.json` (same directory). Atomic write pattern: write to `tests.json.tmp`, then `os.Rename()`.

### Test

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | auto | Random hex ID (20 chars) |
| `name` | string | yes | Human-readable test name |
| `description` | string | no | Short explanation |
| `group_id` | string | no | References a `Group.id`; empty = unassigned |
| `active` | bool | yes | `false` skips this test in battery runs |
| `order` | int | yes | Manual sort order within the group |
| `prompt` | string | yes | The user prompt sent to the model |
| `system_prompt` | string | no | Optional system instruction |
| `evaluation_type` | string | yes | One of: `exact_match`, `contains`, `regex`, `json_schema`, `human_review`, `agent` |
| `evaluation_config` | JSON object | no | Shape depends on `evaluation_type` (see §4) |
| `required_caps` | []string | no | Model capabilities required to run this test (e.g. `["tools"]`, `["vision"]`). These are the same capability strings used elsewhere in the app (`vision`, `tools`, `image`, etc.) |
| `created_at` | ISO 8601 | auto | UTC timestamp |
| `updated_at` | ISO 8601 | auto | UTC timestamp |

### Group

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | auto | Random hex ID |
| `name` | string | yes | Group name |
| `description` | string | no | Short explanation |
| `required_caps` | []string | no | Capabilities a model must have to be eligible for this group |
| `order` | int | yes | Manual sort order |

### On-disk format

```json
{
  "groups": [
    { "id": "...", "name": "Tools", "description": "", "required_caps": ["tools"], "order": 0 }
  ],
  "tests": [
    {
      "id": "...",
      "name": "Web search via tool",
      "group_id": "...",
      "active": true,
      "order": 0,
      "prompt": "Search for the current weather in Buenos Aires using web_search.",
      "system_prompt": "",
      "evaluation_type": "contains",
      "evaluation_config": { "expected": "Buenos Aires" },
      "required_caps": ["tools"],
      "created_at": "2026-01-15T10:00:00Z",
      "updated_at": "2026-01-15T10:00:00Z"
    }
  ]
}
```

---

## 3. API Reference

All routes require authentication (same cookie-based session as the rest of the app).

| Method | Path | Body | Response |
|--------|------|------|----------|
| GET | `/api/tests` | — | `{ groups: [...], tests: [...] }` |
| POST | `/api/tests` | `Test` (without `id`) | Created `Test` |
| PUT | `/api/tests/{id}` | Partial `Test` fields | Updated `Test` |
| DELETE | `/api/tests/{id}` | — | `{ ok: true }` |
| POST | `/api/tests/reorder` | `{ updates: { id: order, ... } }` | `{ ok: true }` |
| POST | `/api/test-groups` | `Group` (without `id`) | Created `Group` |
| PUT | `/api/test-groups/{id}` | Partial `Group` fields | Updated `Group` |
| DELETE | `/api/test-groups/{id}` | — | `{ ok: true }` (tests in group become unassigned) |
| GET | `/api/tests/agent/sessions` | — | `{ sessions: [...] }` |
| POST | `/api/tests/agent/sessions` | `{ test_id, model_id?, config? }` | Created `AgentSession` |
| GET | `/api/tests/agent/sessions/{id}` | — | `AgentSession` |
| POST | `/api/tests/agent/sessions/{id}/message` | `{ role, content }` | Updated `AgentSession` |
| POST | `/api/tests/agent/sessions/{id}/tool` | `{ call }` | `ToolResult` |
| POST | `/api/tests/agent/sessions/{id}/reset` | — | Reset `AgentSession` |
| DELETE | `/api/tests/agent/sessions/{id}` | — | Deleted `AgentSession` |
| GET | `/api/tests/agent/sessions/{id}/files` | — | `[{ name, path, is_dir }]` |

---

## 4. Evaluation Types

Each test declares how its result should be checked once execution is implemented. The `evaluation_config` shape depends on the type.

| Type | Config shape | Description |
|------|-------------|-------------|
| `exact_match` | `{ "expected": "string" }` | Response must match exactly |
| `contains` | `{ "expected": "string" }` | Response must contain the substring |
| `regex` | `{ "pattern": "regex" }` | Response must match the pattern |
| `json_schema` | `{ "required_keys": ["a", "b"] }` | Response must be valid JSON containing the listed keys |
| `human_review` | `{}` (or omitted) | No automatic check; a human scores the result later |
| `agent` | `{ max_turns, initial_files, tools, human_review }` | Multi-turn agent test with sandboxed file tools and human-in-the-loop feedback |

---

## 5. Frontend Architecture

### Views

The SPA has three top-level `<main>` elements for tests (all `hidden` by default):

- `<main id="tests-view">` — group sidebar + test list
- `<main id="test-editor-view">` — full-page create/edit form
- `<main id="agent-session-view">` — multi-turn agent session UI (timeline, sandbox explorer, feedback)

### Routing

`handleRouting()` in `app.js` handles:
- `/tests` → `showTestsView()`
- `/tests/new` → `showTestEditorView(null)`
- `/tests/edit/{id}` → `showTestEditorView(id)`
- `/tests/agent/{test_id}` → `showAgentSessionView(test_id)`

`handleIndex()` in Go also serves `index.html` for these paths so refresh/direct-link works.

### State variables

```js
let testsGroups = [];      // all groups
let tests = [];            // all tests
let selectedGroupId = "";  // "" means "All Tests"
let currentTestId = null;  // null = new, string = editing
let currentAgentSession = null; // active agent session object
```

### Key render functions

- `renderTestsSidebar()` — renders the left group list
- `renderTestsList()` — renders tests for the selected group
- `showTestEditorView(id)` — populates the editor form
- `saveTestEditor()` — validates JSON, builds payload, calls POST or PUT
- `createNewGroup()` — simple `prompt()` → POST `/api/test-groups`

---

## 6. Multi-Turn Agent Test Framework

Agent tests evaluate a model's ability to act as an iterative agent using sandboxed file tools. Each agent test spawns a **session** with an isolated filesystem (sandbox) where the model can read, write, list directories, and execute commands across multiple turns. Human feedback is injected between turns.

### Architecture

- **`internal/agent/sandbox.go`** — safe filesystem operations under `sandboxes/{modelID}/{sessionID}/`
- **`internal/agent/tools.go`** — built-in tools: `read_file`, `write_file`, `list_dir`, `exec`
- **`internal/agent/session.go`** — `SessionStore` with in-memory sessions backed by `agent_sessions.json`
- **`internal/server/agent_handlers.go`** — HTTP handlers under `/api/tests/agent/`

### Session lifecycle

1. User clicks **Run** on an agent test in the test list.
2. Frontend calls `POST /api/tests/agent/sessions` with `test_id`. A new sandbox directory is created and initial files (if defined in `evaluation_config`) are written.
3. The session starts with a system turn containing the test's `system_prompt`.
4. On each turn the model may emit tool calls. The frontend (or a future runner) sends them to `POST /api/tests/agent/sessions/{id}/tool` and receives tool results, which are stored as new turns.
5. When the model stops emitting tool calls, the session waits for human feedback via `POST /api/tests/agent/sessions/{id}/message` with `role: "user"`.
6. The session can be **reset** (`POST .../reset`) to clear turns and re-seed initial files, or **deleted** (`DELETE .../{id}`) to remove the session and its sandbox.

### Evaluation config shape (`agent`)

```json
{
  "max_turns": 15,
  "initial_files": [
    { "path": "index.html", "content": "<!DOCTYPE html>..." },
    { "path": "style.css", "content": "body { ... }" }
  ],
  "tools": ["read_file", "write_file", "list_dir", "exec"],
  "human_review": true
}
```

| Field | Type | Description |
|-------|------|-------------|
| `max_turns` | int | Hard limit on total turns before the session is marked completed |
| `initial_files` | `[{path, content}]` | Files pre-created in the sandbox when the session starts |
| `tools` | `[]string` | Subset of built-in tools available to the model for this test |
| `human_review` | bool | If true, the session pauses between turns for human feedback |

### Seed tests

The app ships with three seeded agent tests in the "Multi-Turn Agent" group:

1. **Animated Solar System** — build an orbiting solar system with HTML/CSS/JS.
2. **Shoe Store Website** — build a responsive single-page store with cart.
3. **To-Do App** — build a localStorage-persisted to-do list in one HTML file.

All three are evaluated by `human_review` and require the `tools` capability.

---

## 7. Required Capabilities (`required_caps`)

Tests and groups both carry a `required_caps` array. These use the same capability strings the app already uses:

- `vision` — model accepts images
- `tools` — model supports function calling / tool use
- `image` — model generates images
- `audio` — model accepts audio input
- `thinking` — model emits reasoning traces
- `completion` / `embedding` — standard text capabilities

When a battery runs (future feature), the runner will skip tests whose `required_caps` are not satisfied by the selected model.

---

## 8. How to Extend

### Adding a new evaluation type

1. Add the option to the `<select id="te-eval-type">` in `web/index.html`.
2. Add i18n keys for the label in `web/i18n.js` (both `en` and `es`).
3. Update this document's §4 table.
4. When execution is built, implement the scorer in the runner.

### Adding new fields to a test

1. Add the field to the `Test` struct in `internal/tests/store.go`.
2. Add a form field to the editor in `web/index.html`.
3. Wire it in `showTestEditorView()` and `saveTestEditor()` in `web/app.js`.
4. No migration needed — the JSON store simply ignores unknown fields on load.

### Wiring test execution (future)

1. Add a "Run Battery" button to the tests view.
2. Create a new runner package (e.g. `internal/runner`) that:
   - Takes a model name and a list of test IDs.
   - Sends each `prompt` (+ `system_prompt`) to Ollama.
   - Applies the `evaluation_type` / `evaluation_config` scorer.
   - Stores results in a new JSON file (e.g. `test_results.json`).
3. Add a results/history view for human-review tests.

---

## 9. I18n Keys Reference

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
