# ollama-manager

Tiny Go web server to manage the [Ollama](https://ollama.com) models installed on a machine.

- List models: name, family, parameters, quantization, size, context, install date, loaded state.
- Install models from the official registry (`POST /api/pull`) with a live progress bar.
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
| GET | `/api/status` | Manager status and Ollama reachability |
| GET | `/api/models` | Combined list of models + loaded state + context_length |
| GET | `/api/models/{name}` | Details: context, capabilities, template, modelinfo |
| DELETE | `/api/models/{name}` | Uninstall model |
| POST | `/api/pull` | Install model. Body `{"name":"llama3:8b"}`. Responds with an SSE progress stream |
| GET | `/api/config` | Read config (without `password_hash`) |
| PATCH | `/api/config` | Update `language`, `port`, `expose_network`, `ollama_url`. Returns `needs_restart` |
| POST | `/api/config/password` | Body `{"password":"x"}` sets the password; `{"password":""}` clears it |

### Install SSE stream

Events emitted by `POST /api/pull`:

- `event: start` — `{name}`
- `event: progress` — `{status, digest, total, completed, percent}`
- `event: done` — `{name}`
- `event: error` — `{error}`

## Layout

```
.
├── main.go                  # CLI + server bootstrap
├── config.example.json
├── internal/
│   ├── config/              # loads/saves config.json
│   ├── ollama/              # Ollama HTTP client
│   └── server/              # router, auth, handlers, SSE
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
