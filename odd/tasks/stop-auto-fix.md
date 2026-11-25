# Stop auto-fix — detect and repair broken `stop` parameters on imported models

Branch: `feat/stop-auto-fix` · Delivery: single PR (under 400 authored lines) · Runner: `go test ./internal/server`

## Specs

- S1. A chat reply that came back empty with `done_reason = "stop"` shows a "Fix stops" action on that message. Clicking it opens the stop-fix modal for the model of that turn.
- S2. The same action is available from the model detail "Repair" section for any local model.
- S3. The diagnosis is empirical: it re-runs the same prompt with the model's baked stop parameters untouched (`current` probe), then again with the request option `stop: []` (`no-stop` probe). Ollama merges request options over the Modelfile, so `stop: []` removes every baked stop for that request (verified against ollama 0.40.18: baked `stop "Hello"` -> blank reply, 1 token, `done_reason=stop`; `stop: []` -> full answer).
- S4. When `current` is blank and `no-stop` answers, the fix must keep exactly the stops that do not cut the answer: each candidate stop is probed alone and kept only if the reply is a prefix of the no-stop reply and at least 60% of its length. Candidates that return a blank reply, or cut it short, are dropped.
- S5. Candidates come from the no-stop reply, the model's current stops, and the model template. When the no-stop probe ends with `done_reason=stop` and no candidate qualifies, the fix is "remove every stop" (the model ends on its own EOS token).
- S6. Never touches TEMPLATE, SYSTEM, RENDERER, PARSER, LICENSE or any other parameter. Apply copies the model to `<model>:stopfix-src`, rebuilds the model in place with `POST /api/create` `from` that snapshot and the original parameters minus `stop`, and deletes the snapshot afterwards, after writing the previous Modelfile to `<config dir>/stop-fix-backups/`.
- S7. After apply the endpoint verifies with a probe using the model's own stops and returns the observed reply; the modal then offers to regenerate the failed turn.
- S8. If the no-stop reply is also empty, the diagnosis reports a template/renderer problem instead of inventing a stop, and apply stays disabled.
- S9. External (API) models and embedding models are never offered the action; a split GGUF (more than two blobs) is rejected with an explicit error instead of a partial rebuild.

## Tasks

- T1 — S1-S2: chat foot action + repair entry (web/app-svg.js, web/app-agent_mode.js, web/app-detail.js). done
- T2 — S3-S5, S8: `internal/server/model_stop_fix.go` engine + `/api/model-stop-fix/preview`. done
- T3 — S6-S7, S9: `/api/model-stop-fix/apply` (backup, in-place create, verify). done
- T4 — S1-S3: modal UI `web/app-stopfix.js`, index.html markup, i18n (en/es), CSS. done
- T5 — S3-S6: Go tests with a fake Ollama (candidate extraction, classification, modelfile rewrite, preview/apply flow). done

## Log

- L1 (user): "cada tanto, no siempre, tengo problemas con algunos modelos descargados de huggingface ... que responden en blanco ... ya muchas veces encontramos que esto era debido a parametros de corte invalidos ... me gustaria, desde el chat tener un boton, para si recibo una respuesta con un stop sin respuesta, poder depurar el modelo, y corregir detectando automaticamente cual es el stop correcto; editando el modelofile hasta encontrar el indicado, y reemplazar el modelfile del modelo descargado por el modelfile que si tiene el stop correcto ... esta opcion tambien debería ser accesible desde repair options. Auto corregir el auto stop. este error solo sucede cuando vienen de huggingface o externos a ollama library."
- L2 (evidence, 2026-11-25): ollama 0.40.18 ignores `CreateRequest.Modelfile`; structured `parameters` are the only way to persist stops. A derived model with `PARAMETER stop "Hello"` answers `hi` with `''` / `done_reason=stop` / 1 token; the same request with `options.stop=[]` returns the full answer, and `stop: ["<|im_end|>"]` does too. Empty array removes an inherited stop on create.
- L3 (implementation): the engine is `internal/server/model_stop_fix.go` (`buildStopFixPreview`, `probeStops`, `classifyStopCandidate`, `rewriteModelfileStops`, `buildStopFixCreateRequest`, `applyStopFix`). A healthy model stops after one probe; a broken one runs 2 + N candidate probes. A candidate is only *safe* when its reply is a prefix of the no-stop reply and at least 60% of its length. Because 0.40 ignores `CreateRequest.Modelfile`, the rebuild is done with structured fields (Files with model.gguf/mmproj.gguf, Template, System, Renderer, Parser, Parameters minus `stop`, License); `stop: []` persists an empty list and removes inherited stops.
- L4 (E2E, probe model built from another model): baked `stop "Hello"` -> preview reports `diagnosis=stops`, current probe blank, no-stop probe full, candidate `Hello` verdict `cuts`, proposed `[]`; apply rebuilt in place, kept template/renderer/parser/temperature/vision projector, verified reply `Hello! How can I help you today?`, backup written; a second preview then reported `diagnosis=ok`, `needs_fix=false`. Temporary model and test server were removed afterwards.
- L5 (tests): `go test ./internal/server/` passes except `TestMultiDeviceModelUsage` and `TestHandleListModelsDeviceFilter`, which expect 2 GPU devices and fail identically on the base commit (environment-dependent, unrelated).
- L6 (user report, 2026-11-25): the first apply on a real Hugging Face import failed with `template error: template: :9: function "raise_exception" not defined`. That model stores its original Jinja chat template, which Ollama accepts at chat time but cannot re-parse during create. Rebuilding from `files` + that template always fails, so the rebuild now inherits the manifest (`from` a `:stopfix-src` copy) and only overrides parameters; the template is never re-parsed and stays identical. Verified end to end on a model with a 3.8 KB Jinja template: preview detected a blanking stop, apply succeeded, the template length was unchanged, the reply came back, and the snapshot was deleted.
