# ollama-manager

Tiny Go web server to manage the [Ollama](https://ollama.com) models installed on a machine.

- List models: name, family, parameters, quantization, size, context, install date, loaded state.
- Live system meters in the top bar: CPU load, memory usage (including RAM used by loaded models), and disk free/used.
- **Chat** in the browser: talk to a selected model with streaming (SSE), optional *thinking* traces, stop/regenerate, and an optional **web tools** mode (`web_search` + `web_fetch` executed on the server) with a **timeline** UI (think → tool → think → answer).
- **Download queue**: enqueue multiple installs; the manager runs one at a time, persists the queue to `jobs.json`, and resumes after restart (partial layers are kept by Ollama).
- Cancel/remove/retry individual jobs and clear finished history from the UI.
- Uninstall models.
- View full details: capabilities, template, parameters, model info.
- Minimalist dark UI, no frameworks. Bilingual (English / Spanish).
- Single binary, cross-platform (Windows, macOS, Linux).
- Optional password auth (bcrypt + HMAC-signed session cookie).
- Configurable via `config.json` or from a **Settings** panel inside the UI: port, LAN exposure, password, language.
- Sortable by any column; preference persisted in `localStorage`.

## Requirements

- Go 1.25 or later (build only).
- Ollama running on the same machine (defaults to `http://localhost:11434`).

## Build

```bash
go build -o ollama-manager .
# Windows
go build -o ollama-manager.exe .
```

Cross-compile (from any OS):

```bash
GOOS=linux   GOARCH=amd64 go build -o dist/ollama-manager-linux .
GOOS=darwin  GOARCH=arm64 go build -o dist/ollama-manager-macos .
GOOS=windows GOARCH=amd64 go build -o dist/ollama-manager.exe .
```

```powershell
$env:CGO_ENABLED = "0"; $env:GOOS = "darwin"; $env:GOARCH = "arm64"; go build -trimpath -ldflags="-s -w" -o ollama-manager .
```

## Usage

```bash
./ollama-manager                      # uses ./config.json
./ollama-manager -config /path/cfg.json
./ollama-manager set-password <pwd>   # hashes and stores password
./ollama-manager clear-password       # removes password
./ollama-manager version
```

On first launch it creates `config.json` with sensible defaults:

```json
{
  "port": 7860,
  "expose_network": false,
  "password_hash": "",
  "session_secret": "<auto>",
  "ollama_url": "http://localhost:11434",
  "language": "en"
}
```

- `port`: HTTP port for the manager.
- `expose_network`: `false` binds only to `127.0.0.1` (local access only).
  Set it to `true` to listen on `0.0.0.0` and reach it from another PC on your LAN.
- `password_hash`: bcrypt; empty = no login. Use `set-password` or the Settings panel to enable it.
- `session_secret`: HMAC key used to sign cookies (auto-generated).
- `ollama_url`: where Ollama is running.
- `language`: UI language (`en` or `es`). Switchable from the Settings panel.

Almost everything can also be changed from the **⚙ Settings** button in the UI
without touching the file. Changes to `port` and `expose_network` require
restarting the process to take effect (the UI warns you).

### Exposing to the network

```bash
# 1) Edit config.json and set "expose_network": true
# 2) Set a password
./ollama-manager set-password "myStrongPass"
# 3) Start the server
./ollama-manager
```

> If you enable `expose_network` without a password, the manager prints a
> warning and anyone on your LAN can delete/install models. Set a password.

### HTTP endpoints

| Method | Path | Description |
| --- | --- | --- |
| GET | `/` | Main UI |
| GET/POST | `/login`, `/logout` | Password login (when enabled) |
| GET | `/api/status` | Manager status, Ollama reachability, and live CPU/RAM/disk metrics |
| GET | `/api/models` | Combined list of models + loaded state + context_length |
| GET | `/api/running` | Light view of Ollama [`/api/ps`](https://github.com/ollama/ollama/blob/main/docs/api.md#list-running-models) (what is currently loaded) |
| GET | `/api/models/{name}` | Details: context, capabilities, template, modelinfo |
| DELETE | `/api/models/{name}` | Uninstall model |
| POST | `/api/chat` | Chat with the model. **Server-Sent Events** stream: `chunk` (content/thinking), optional `tool` (web agent), `done` (usage), `error`. Set `web_tools: true` in the JSON body to enable `web_search` / `web_fetch` for models that support tools. |
| POST | `/api/pull` | Enqueue a download. Body `{"name":"llama3:8b"}`. Returns `{job_id, status, name}`. Progress is served via `/api/jobs/events` |
| GET | `/api/jobs` | List all jobs (queued/running/done/error/cancelled) in insertion order |
| GET | `/api/jobs/events` | SSE stream: one `snapshot` event with the current list, then `update`/`remove` events for every change |
| POST | `/api/jobs/{id}/cancel` | Cancel a queued or running job |
| DELETE | `/api/jobs/{id}` | Remove a terminal job from history |
| POST | `/api/jobs/clear` | Remove all terminal (finished/error/cancelled) jobs |
| GET | `/api/config` | Read config (without `password_hash`) |
| PATCH | `/api/config` | Update `language`, `port`, `expose_network`, `ollama_url`. Returns `needs_restart` |
| POST | `/api/config/password` | Body `{"password":"x"}` sets the password; `{"password":""}` clears it |

### Download queue

- Only **one** job runs at a time. Extra pulls go into a FIFO queue.
- Jobs are persisted to `jobs.json` next to `config.json`. On startup any job
  that was marked `running` is demoted back to `queued` and the worker picks it
  up again. Ollama keeps completed layers, so the download effectively resumes
  from where it left off.
- Cancelling a running job aborts the HTTP stream to Ollama and starts the next
  queued job. Cancelled jobs stay in the list (state `cancelled`) so you can
  retry or remove them.

### Job events stream

Events emitted by `GET /api/jobs/events`:

- `event: snapshot` — `{jobs: [...]}` on connect
- `event: update` — `{job: {id, name, status, percent, completed, total, status_text, digest, error, ...}}`
- `event: remove` — `{id}`

### Chat stream (`POST /api/chat`)

The response is `text/event-stream` (not JSON). Send a JSON body with at least `model` and `messages` (Ollama chat format). Optional: `think`, `options` (temperature, top_k, top_p), and `web_tools: true` to run the on-server web agent (bounded rounds, tool results fed back to the model).

## Layout

```
.
├── main.go                  # CLI + server bootstrap
├── config.example.json
├── jobs.json                # runtime: persisted download queue (ignored by git)
├── internal/
│   ├── config/              # loads/saves config.json
│   ├── jobs/                # download queue manager (FIFO, SSE fan-out)
│   ├── ollama/              # Ollama HTTP client (incl. chat + tools)
│   └── server/              # router, auth, handlers, chat SSE, web tools agent
└── web/                     # embedded HTML/CSS/JS (go:embed)
```

## Development

```bash
go vet ./...
go build ./...
go run . -config dev.json
```

The frontend is embedded with `//go:embed all:web` in `main.go`, so any change
under `web/*` requires recompiling.

## License

MIT — see [LICENSE](LICENSE).
