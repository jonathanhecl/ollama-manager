# Test Panel — Technical Documentation

A dedicated desktop-only panel for creating, editing, organizing, and grouping test prompts (test batteries) used to evaluate Ollama models. This iteration is **management-only**: no prompt execution, scoring, or human-review workflows are implemented yet.

---

## 1. Feature Overview

The test panel lets users:
- Create **tests** — prompt templates that will later be sent to models.
- Organize tests into **groups** — e.g. "Tools", "Vision", "Math".
- Activate or **suspend** individual tests — suspended tests are excluded from future battery runs.
- Edit every aspect of a test: prompt, system prompt, evaluation type, evaluation config, required model capabilities, and ordering.

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
| `evaluation_type` | string | yes | One of: `exact_match`, `contains`, `regex`, `json_schema`, `human_review` |
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

---

## 5. Frontend Architecture

### Views

The SPA has two new top-level `<main>` elements (all `hidden` by default):

- `<main id="tests-view">` — group sidebar + test list
- `<main id="test-editor-view">` — full-page create/edit form

### Routing

`handleRouting()` in `app.js` handles:
- `/tests` → `showTestsView()`
- `/tests/new` → `showTestEditorView(null)`
- `/tests/edit/{id}` → `showTestEditorView(id)`

`handleIndex()` in Go also serves `index.html` for these paths so refresh/direct-link works.

### State variables

```js
let testsGroups = [];      // all groups
let tests = [];            // all tests
let selectedGroupId = "";  // "" means "All Tests"
let currentTestId = null;  // null = new, string = editing
```

### Key render functions

- `renderTestsSidebar()` — renders the left group list
- `renderTestsList()` — renders tests for the selected group
- `showTestEditorView(id)` — populates the editor form
- `saveTestEditor()` — validates JSON, builds payload, calls POST or PUT
- `createNewGroup()` — simple `prompt()` → POST `/api/test-groups`

---

## 6. Required Capabilities (`required_caps`)

Tests and groups both carry a `required_caps` array. These use the same capability strings the app already uses:

- `vision` — model accepts images
- `tools` — model supports function calling / tool use
- `image` — model generates images
- `audio` — model accepts audio input
- `thinking` — model emits reasoning traces
- `completion` / `embedding` — standard text capabilities

When a battery runs (future feature), the runner will skip tests whose `required_caps` are not satisfied by the selected model.

---

## 7. How to Extend

### Adding a new evaluation type

1. Add the option to the `<select id="te-eval-type">` in `web/index.html`.
2. Add i18n keys for the label in `web/i18n.js` (both `en` and `es`).
3. Update this document’s §4 table.
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

## 8. I18n Keys Reference

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

Plus the generic action keys:
- `action.save`
- `action.edit`
