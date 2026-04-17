# ollama-manager

Pequeño servidor web en Go para gestionar los modelos de [Ollama](https://ollama.com) que tenés instalados en una PC.

- Listar modelos: nombre, familia, parámetros, cuantización, tamaño, contexto, fecha de instalación, estado (cargado en memoria).
- Instalar modelos del registry oficial (`POST /api/pull`) con barra de progreso en vivo.
- Desinstalar modelos.
- Ver detalles completos: capacidades, template, parámetros, model info.
- UI minimalista en modo oscuro, sin frameworks.
- Binario único, multiplataforma (Windows, macOS, Linux).
- Auth opcional con contraseña (bcrypt + cookie de sesión firmada HMAC).
- Configuración por `config.json`: puerto, exposición a la red local, contraseña.

## Requisitos

- Go 1.25 o superior (solo para compilar).
- Ollama corriendo en la misma máquina (por defecto `http://localhost:11434`).

## Compilar

```bash
go build -o ollama-manager .
# Windows
go build -o ollama-manager.exe .
```

Cross-compile (desde cualquier OS):

```bash
GOOS=linux   GOARCH=amd64 go build -o dist/ollama-manager-linux .
GOOS=darwin  GOARCH=arm64 go build -o dist/ollama-manager-macos .
GOOS=windows GOARCH=amd64 go build -o dist/ollama-manager.exe .
```

## Uso

```bash
./ollama-manager                      # usa ./config.json
./ollama-manager -config /ruta/cfg.json
./ollama-manager set-password <pwd>   # hashea y guarda contraseña
./ollama-manager clear-password       # quita contraseña
./ollama-manager version
```

Al primer arranque crea `config.json` con valores por defecto:

```json
{
  "port": 7860,
  "expose_network": false,
  "password_hash": "",
  "session_secret": "<auto>",
  "ollama_url": "http://localhost:11434"
}
```

- `port`: puerto HTTP del manager.
- `expose_network`: `false` enlaza solo a `127.0.0.1` (acceso únicamente local).
  Si lo ponés en `true`, escucha en `0.0.0.0` y podés entrar desde otra PC de tu LAN.
- `password_hash`: bcrypt; vacío = sin login. Usá `set-password` para configurarla.
- `session_secret`: clave HMAC para firmar cookies (autogenerada).
- `ollama_url`: dónde corre Ollama.

### Exponer a la red

```bash
# 1) Editá config.json y poné "expose_network": true
# 2) Configurá una contraseña
./ollama-manager set-password "miClaveSegura"
# 3) Arrancá el servidor
./ollama-manager
```

> Si activás `expose_network` sin contraseña, el manager imprime una advertencia
> y cualquiera en tu LAN podrá borrar/instalar modelos. Configurá una contraseña.

### Endpoints HTTP

| Método | Ruta | Descripción |
| --- | --- | --- |
| GET | `/` | UI principal |
| GET/POST | `/login`, `/logout` | Login con contraseña (si está habilitada) |
| GET | `/api/status` | Estado del manager y reachability de Ollama |
| GET | `/api/models` | Lista combinada de modelos + estado loaded |
| GET | `/api/models/{name}` | Detalle: contexto, capacidades, template, modelinfo |
| DELETE | `/api/models/{name}` | Desinstalar modelo |
| POST | `/api/pull` | Instalar modelo. Body `{"name":"llama3:8b"}`. Responde con stream SSE de progreso |

### SSE de instalación

Eventos emitidos por `POST /api/pull`:

- `event: start` — `{name}`
- `event: progress` — `{status, digest, total, completed, percent}`
- `event: done` — `{name}`
- `event: error` — `{error}`

## Estructura

```
.
├── main.go                  # CLI + arranque del servidor
├── config.example.json
├── internal/
│   ├── config/              # carga/guarda config.json
│   ├── ollama/              # cliente HTTP de Ollama
│   └── server/              # router, auth, handlers, SSE
└── web/                     # HTML/CSS/JS embebido (go:embed)
```

## Desarrollo

```bash
go vet ./...
go build ./...
go run . -config dev.json
```

El frontend está embebido con `//go:embed all:web` en `main.go`, así que cualquier
cambio en `web/*` requiere recompilar.

## Licencia

MIT — ver [LICENSE](LICENSE).
