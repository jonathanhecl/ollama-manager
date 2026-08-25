# ollama-manager

Tiny, fast, and feature-packed Go web server to manage, benchmark, and interact with [Ollama](https://ollama.com) models and external LLMs on your local machine or LAN.

![ollama-manager Showcase](image.png)

## Key Features

- **Hugging Face Model Explorer & Downloader**:
  - **Live GGUF Search**: search and explore models on Hugging Face directly within the app, filtering by name, architecture, quantization, and parameter size.
  - **Hardware Fit Estimator**: calculates required VRAM and system RAM for every quantization variant based on your machine's hardware, with clear fit indicators before downloading.
  - **Vision Projector (`mmproj`) Detection**: automatically discovers compatible multimodal vision projector files for vision-enabled architectures.
  - **Single-Click Queueing**: download models and projectors directly into the background FIFO download manager.

- **Model Management & Repair**:
  - **Comprehensive Catalog**: real-time list of installed models with family, parameter count, quantization, model size, context length, last used timestamp, record tok/s, and loaded status (VRAM/RAM).
  - **Model Repair & Vision Projector Attach**: inspect, test, and patch community models with missing tool templates, thinking tags, or invalid Markdown stop tokens. Attach `mmproj` vision projectors to add vision capabilities with **proactive disk space verification** before downloading.
  - **Memory & VRAM Management**: unload individual models or unload all active models from VRAM/RAM with one click.
  - **Model Archiving & Uninstall History**: archive rarely used models or review uninstalled models with optional deletion reason notes.
  - **Full Model Inspector**: view raw Modelfiles, Jinja/Go templates, licenses, parameters, and architecture details.

- **External Models Integration (OpenAI-Compatible)**:
  - Connect remote LLM providers (OpenAI, DeepSeek, Groq, OpenRouter, vLLM, LM Studio, etc.) alongside your local Ollama models.
  - Configure custom base URLs, API keys, model identifiers, and custom capability tags (`tools`, `thinking`, `vision`).
  - Use external models across the entire application: chat, web tools, live artifacts, and benchmarks.

- **Analytics & Performance Dashboard**:
  - **Speed & Cold Load Telemetry**: track record tokens/second (tok/s) and minimum cold load times for every model.
  - **Historical Usage & Token Counting**: monitor prompt tokens, completion tokens, execution times, and session counts.
  - **Interactive Performance Charts**: visualize model speed comparisons, memory footprints, and efficiency metrics.

- **System Prompts Library**:
  - Built-in and user-managed prompt library to store, edit, categorize, and search curated system prompts.
  - Instant one-click application to any model or chat session.

- **Rich Web Chat Interface**:
  - **Real-Time Streaming & Telemetry**: Server-Sent Events (SSE) streaming with live tokens/second (tok/s), cold load time, and response generation metrics.
  - **Thinking Traces & Granular Reasoning Control**: collapsible and chronologically structured `<think>` reasoning traces with granular thinking levels (`auto`, `off`, `low`, `medium`, `high`, `max`).
  - **Per-Model Customizations**: customize system prompts, temperature, top-k, top-p, context length, and thinking levels with automatic persistence in `localStorage`.
  - **Multimodal Support**: image generation, vision inputs, audio attachments, and browser microphone voice recording.
  - **Session Management & Message Editing**: edit previous turns, cancel queued requests to recover inputs, and reset conversations with confirmation.

- **Web Tools & Live Artifacts Agent**:
  - **Web Search & Fetch**: server-side bounded web agent using DuckDuckGo (`web_search`) and direct HTTP (`web_fetch`) with a structured timeline.
  - **Artifacts Workspace Sandbox**: interactive workspace where models build, test, and iterate on full-stack web applications and code (`create_artifact`, `write_file`, `replace_in_file`, `read_file`, `list_dir`, `exec`) with real-time live preview.
  - **Autonomous Visual Inspection (`take_artifact_screenshot`)**: vision-capable models capture and visually inspect rendered previews directly from the browser to fix layouts and styling.
  - **Direct UI Interaction (`eval_artifact_js`)**: models execute JavaScript inside the sandbox to click elements, fill forms, and test event flows.
  - **Console Diagnostics (`get_artifact_console`)**: automatic capture of sandboxed iframe runtime logs (`console.log`, `console.warn`, `console.error`) for autonomous debugging.
  - **Artifact Project History**: browse past generated artifact workspaces by model and date, reload previous projects, or delete them via UI/API.

- **OpenCode Integration** (⚙ Settings → OpenCode):
  - Manage which installed models appear in your local [OpenCode](https://opencode.ai) configuration (`~/.config/opencode/opencode.json`).
  - Detect or create local Ollama providers, toggle models with custom display names, and export provider blocks for remote machines.

- **Persistent Download Queue**:
  - FIFO download queue with real-time progress streams, pause/resume queue, pause/resume individual jobs, cancel/retry controls, speed telemetry, and persistence across server restarts via `jobs.json`.

- **Tests & Benchmark Suite**:
  - Built-in benchmark suite to evaluate and compare models against standardized prompts for speed, context processing, and accuracy.

- **Security & Portability**:
  - Single binary, zero external dependencies, minimalist dark UI, and bilingual interface (English / Spanish).
  - Optional bcrypt password protection and HMAC-signed session cookies.

---

## Requirements

- **Go 1.25 or later** (for building from source).
- **Ollama** running locally or remotely (defaults to `http://localhost:11434`).

---

## Build

```bash
# Standard build (embeds VCS metadata automatically)
go build -o ollama-manager .

# Embed compilation date/time manually
go build -ldflags "-X 'main.buildTime=$(date +'%F %T')'" -o ollama-manager .
```

### Cross-Compilation

```bash
GOOS=linux   GOARCH=amd64 go build -ldflags "-X 'main.buildTime=$(date +'%F %T')'" -o dist/ollama-manager-linux .
GOOS=darwin  GOARCH=arm64 go build -ldflags "-s -w -X 'main.buildTime=$(date +'%F %T')'" -o dist/ollama-manager-macos .
GOOS=windows GOARCH=amd64 go build -ldflags "-X 'main.buildTime=$(date +'%F %T')'" -o dist/ollama-manager.exe .
```

### Build & Release Scripts

- **macOS (Bash / PowerShell)**:
  ```bash
  ./build-mac.sh              # Builds native macOS binary (arm64/amd64)
  ./build-mac.sh -a arm64 -o dist/ollama-manager-macos
  ```
  ```powershell
  ./build-mac.ps1
  ```
- **Build all platforms**:
  ```bash
  ./build-all.sh v1.0.5        # Bash (macOS / Linux)
  ./build-all.ps1 -Version v1.0.5  # PowerShell (Windows)
  ```
- **Release to GitHub**:
  ```powershell
  # Interactive mode with automatic version tag detection & suggestion
  ./release.ps1
  
  # Or explicit version
  ./release.ps1 v1.0.5
  ```

---

## Usage

```bash
./ollama-manager                      # uses ./config.json
./ollama-manager -config /path/cfg.json
./ollama-manager set-password <pwd>   # hashes and stores password
./ollama-manager clear-password       # removes password
./ollama-manager version
```

On first launch, `config.json` is generated automatically:

```json
{
  "port": 7860,
  "expose_network": false,
  "password_hash": "",
  "session_secret": "<auto>",
  "ollama_url": "http://localhost:11434",
  "language": "en",
  "chat_defaults": {
    "system_prompt": "",
    "temperature": 0.7,
    "top_k": 40,
    "top_p": 0.9,
    "think_level": "auto",
    "web_tools": false,
    "artifacts": false
  }
}
```

- `port`: HTTP port for the manager.
- `expose_network`: `false` binds only to `127.0.0.1`. Set to `true` to listen on `0.0.0.0` for LAN access.
- `password_hash`: bcrypt hash; empty means no authentication is required.
- `session_secret`: HMAC secret key used to sign session cookies.
- `ollama_url`: URL of the Ollama server.
- `language`: UI language (`en` or `es`).
- `chat_defaults`: global fallback chat parameters. `think_level` accepts `auto`, `off`, `low`, `medium`, `high`, or `max`.

### 💡 Recommended Workflow: Always-On Background Daemon

> [!TIP]
> **Run alongside Ollama at system startup**  
> `ollama-manager` is designed to run continuously as a lightweight background daemon (just like Ollama itself). Keeping it running persistently ensures your web interface, background download queue, performance analytics, and OpenCode sync are always instantly available from any browser on your machine or LAN (`http://localhost:7860`).
>
> **Quick autostart setup:**
> - **Windows**: Press `Win + R`, type `shell:startup`, and place a shortcut to `ollama-manager.exe` inside (or configure a task in Windows Task Scheduler).
> - **macOS**: Go to **System Settings → General → Login Items** and add `ollama-manager`, or create a `launchd` service in `~/Library/LaunchAgents/`.
> - **Linux**: Set up a `systemd` user service (`~/.config/systemd/user/ollama-manager.service`) and run `systemctl --user enable --now ollama-manager`.

---

## HTTP API Reference

| Method | Path | Description |
| :--- | :--- | :--- |
| `GET` | `/` | Main single-page web UI |
| `GET`/`POST` | `/login`, `/logout` | Password authentication |
| `GET` | `/api/status` | System metrics, CPU/RAM/VRAM, disk usage, and Ollama connection status |
| `GET` | `/api/models` | List installed models with loaded status, metadata, and capabilities |
| `GET` | `/api/running` | Models currently loaded in VRAM/RAM (`/api/ps`) |
| `POST` | `/api/running/unload-all` | Unload all models currently residing in VRAM/RAM |
| `GET` | `/api/models/{name}` | Detailed model info, modelfile, parameters, license, and template |
| `POST` | `/api/models/unload` | Unload a specific model from VRAM/RAM |
| `POST` | `/api/models/archive` | Archive a model to hide it from standard listings |
| `POST` | `/api/models/unarchive` | Unarchive a model |
| `DELETE` | `/api/models/{name}` | Uninstall model (with optional reason tracking) and clean up associated artifacts |
| `POST` | `/api/chat` | SSE chat stream (`chunk`, `tool`, `done`, `error`) supporting Web Tools & Artifacts |
| `POST` | `/api/embed` | Generate text embeddings |
| `GET` | `/api/hf/search` | Search Hugging Face GGUF models and calculate hardware fit |
| `GET` | `/api/hf/model/{author}/{repo}` | Fetch detailed GGUF model files and projector compatibility from Hugging Face |
| `GET` | `/api/usage` | Model usage statistics, record tok/s, minimum cold load times, and token totals |
| `GET`/`POST` | `/api/prompts` | Manage and search curated system prompts library |
| `DELETE` | `/api/prompts/{id}` | Delete a system prompt |
| `GET`/`POST` | `/api/external-models` | List and configure external OpenAI-compatible models |
| `DELETE` | `/api/external-models/{id}` | Remove an external model configuration |
| `POST` | `/api/model-repair/preview` | Preview patched Modelfile (tools, templates, stops, projector disk check) |
| `POST` | `/api/model-repair/apply` | Apply repair, download mmproj projector, and build `:fixed` model |
| `GET` | `/api/artifacts/{digest}/{timestamp}/{path}` | Serve sandboxed artifact files for live preview |
| `DELETE` | `/api/artifacts/{digest}/{timestamp}` | Delete a specific generated artifact workspace |
| `POST` | `/api/artifacts/console` | Relay runtime console logs from artifact preview iframe to the agent |
| `POST` | `/api/artifacts/screenshot` | Submit browser preview screenshot for vision agent inspection |
| `POST` | `/api/artifacts/eval` | Submit browser preview JavaScript evaluation results for the agent |
| `GET` | `/api/models/{name}/artifacts` | List artifact projects created by a model |
| `GET` | `/api/tests` | List benchmark test cases and prompt suites |
| `POST` | `/api/tests/run` | Execute model benchmarks and record performance metrics |
| `POST` | `/api/pull` | Enqueue a model download (`{"name":"llama3:8b"}`) |
| `GET` | `/api/jobs` | List download queue jobs |
| `GET` | `/api/jobs/events` | SSE stream for real-time download queue updates |
| `GET` | `/api/download-history/{name}` | Download history and statistics for a model |
| `POST` | `/api/jobs/pause`, `/api/jobs/resume` | Pause or resume download queue processing |
| `POST` | `/api/jobs/{id}/cancel` | Cancel a queued or active download |
| `POST` | `/api/jobs/{id}/pause`, `/api/jobs/{id}/resume` | Pause or resume an individual download job |
| `DELETE` | `/api/jobs/{id}` | Delete a finished/cancelled job from history |
| `POST` | `/api/jobs/clear` | Clear all terminal download jobs |
| `GET` | `/api/config` | Read current configuration |
| `PATCH` | `/api/config` | Update settings (port, language, expose, chat defaults) |
| `POST` | `/api/config/password` | Set or clear password protection |
| `GET` | `/api/opencode` | OpenCode integration state (config path, local provider, per-model visibility) |
| `POST` | `/api/opencode/provider` | Create local Ollama provider in OpenCode config |
| `POST` | `/api/opencode/models` | Set which models are exposed in the local OpenCode provider |

---

## Project Structure

```
.
├── main.go                     # CLI entrypoint and server bootstrap
├── config.example.json         # Reference configuration template
├── jobs.json                   # Runtime download queue persistence (git-ignored)
├── internal/
│   ├── agent/                  # Artifact agent and sandboxed execution tools
│   ├── config/                 # Configuration file loading and persistence
│   ├── diskusage/              # Cross-platform disk space detection
│   ├── jobs/                   # FIFO download queue worker and SSE dispatcher
│   ├── ollama/                 # Ollama API client (chat, pull, tags, ps, create)
│   ├── opencode/               # OpenCode configuration parser and sync
│   ├── runner/                 # Benchmark test execution engine
│   ├── server/                 # HTTP router, middleware, handlers, and SSE streams
│   ├── sysmetrics/             # Live CPU, RAM, and VRAM telemetry
│   └── tests/                  # Test suite storage, seeding, and management
└── web/                        # Embedded frontend UI (HTML, CSS, JS, I18n)
```

---

## Frequently Asked Questions (FAQ)

### 🎙️ Why is the microphone / voice recording not working over my local network (LAN)?

Modern web browsers restrict media recording APIs (`navigator.mediaDevices.getUserMedia` and Web Speech) to **Secure Contexts** (`https://` or `localhost` / `127.0.0.1`). When accessing `ollama-manager` over an unencrypted local IP address (such as `http://192.168.1.50:7860`), Chromium-based browsers block microphone access by default.

To enable microphone access over HTTP on your local network without setting up SSL certificates:

1. Navigate to the insecure origin flag in your Chromium-based browser:
   - **Google Chrome**: `chrome://flags/#unsafely-treat-insecure-origin-as-secure`
   - **Brave**: `brave://flags/#unsafely-treat-insecure-origin-as-secure`
   - **Microsoft Edge**: `edge://flags/#unsafely-treat-insecure-origin-as-secure`
   - **Opera**: `opera://flags/#unsafely-treat-insecure-origin-as-secure`
2. Set the dropdown to **Enabled**.
3. In the text area below the flag, enter your server's origin and port (e.g. `http://192.168.1.50:7860`). If using multiple endpoints, separate them with commas.
4. Click **Relaunch** at the bottom of the browser window.

---

## License

[MIT](LICENSE)
