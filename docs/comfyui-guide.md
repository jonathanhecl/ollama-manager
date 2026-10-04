# ComfyUI Integration

The chat can send jobs to a ComfyUI server you run yourself. The model calls a
tool, ComfyUI renders, and the result comes back into the transcript — the image
is attached to the message so the model can look at it, correct it, and try
again on the next turn.

## Layout

```
<dir of config.json>/
  comfyui/
    workflows/<id>.json     one file per registered workflow
    media/<session>/<file>  copies of everything the workflows produced
```

Both live next to `config.json` rather than inside the config directory itself,
because they are not configuration and they grow without bound. Media is swept
on startup against the live session list, so deleting a session deletes its
renders.

## Why the Model Only Gets Images

Vision models accept images; they cannot watch a video or hear audio. Sending
one would either fail or, worse, be silently dropped by the model and then
reported as if it had been seen. So `describeComfyRun` states explicitly that
video and audio were shown to the user but are not attached.

The consequence is a real limitation worth knowing: the model cannot critique a
video it just made. It can iterate on the images that came out of the same run.

Cross-turn iteration works by replay. `buildSessionMessages` walks the stored
transcript and, for the most recent `sessionComfyReplayRuns` tool entries
(`sessionComfyReplayImages` images total), re-attaches the images to the
assistant turn that announced them. Without this the model would be asked to
refine a picture it can no longer see, because tool results are not part of the
transcript that gets rebuilt. Budget is capped deliberately: every replayed
image costs vision tokens on every subsequent turn.

A model that cannot see images is not blocked from using ComfyUI — it just
cannot judge the output, so the system prompt tells it to describe its intent
and let the user react.

## Bindings Are the Only Writable Surface

A workflow is a graph of nodes with dozens of inputs. Exposing them all to a
model is how you get a workflow that renders noise. So each registered workflow
declares a set of **bindings**, and the tool schema for that workflow is
generated from them. Everything not bound is fixed.

```
ComfyBinding{Param, Label, NodeID, ClassType, Input, Kind, Enum, Title}
```

`Param` is the only name the model ever sees. `NodeID` + `Input` is where the
value actually goes. A call with a parameter that is not bound is **rejected**,
not ignored — a silent no-op would leave the model convinced it had changed the
seed.

Bindings are detected by `class_type` rather than by node position, because node
ids change on every re-export:

- `CLIPTextEncode` becomes `prompt` or `negative_prompt`, decided by whether the
  wired text reads as negative.
- A table of `class_type` + `input` pairs covers seed, steps, cfg, denoise,
  width, height, image, lora and prompt.
- Each detected value's JSON type in the graph gives the schema type, so a
  float64 `20` on `steps` stays an integer and `cfg` stays a number.

Detection is re-run when the graph is replaced, unless the user hand-edited the
bindings — overwriting someone's work silently would be worse than a stale
binding list, which at least fails loudly.

### Defaults

Each schema property carries a `default` taken from the value already stored in
the graph. A tool call with no arguments is therefore a re-run of the workflow as
it is, not a render from a blank slate.

## Format: API, Not UI

ComfyUI's visual editor exports a graph with `nodes`, `links`, `last_node_id`,
`extra`, `groups` and `version`. That is not an executable prompt and it is
rejected with a message pointing at **Workflow → Export (API)**. Envelopes
(`{"prompt": {...}}`) are unwrapped because people paste those by accident.

## Polling Instead of WebSocket

ComfyUI's `/ws` pushes progress events, but a long-lived socket per chat turn is
a lot of machinery for something `/history` answers. `WaitResult` polls
`/history/<prompt_id>` every 900ms until the entry reports `completed`.

Cancellation is cooperative: interrupting a turn sends `/interrupt`, and it only
does so if this client's own `prompt_id` is the one currently running — otherwise
one cancelled chat would kill another user's render.

## Media Classification

`classifyComfyMedia` sniffs content with `http.DetectContentType` before trusting
the extension, because ComfyUI returns a filename like `ComfyUI_00001_.webp`
that says very little.

Video and audio arrive in `outputs[*].gifs[]`, not `images[]`. Animated WebP is
detected by looking for the `ANIM` chunk inside the RIFF container and is
classified as video, since that is what it behaves like in a browser.

Images are copied byte for byte. Before being attached to a message they are
decoded, capped at 1024px on the long edge and re-encoded as JPEG q82 — vision
models pay per pixel and a 1024px JPEG is enough to judge composition and style.
Anything the standard library cannot decode, animated WebP being the case,
returns `ok = false` and is skipped rather than sent.

Video and audio are also copied byte for byte. Re-encoding a video would only
lose quality, and the original is still in ComfyUI.

Duration is **not** computed. It lives in the `moov` atom for MP4 and in EBML
headers for WebM, neither of which the standard library parses, so the caption
shows the file size instead.

## Progress Reporting

ComfyUI reports progress while it runs, which is worth surfacing: a 40-step SDXL
render is a long silence otherwise. That progress travels in `run_status`,
**not** `status`, because `status` is what drives the entry's
`generating`/`running`/`ok`/`error` state machine. See
[agents.md](agents.md#tool-states-are-not-progress-reports).

## Storage

`http.ServeFile` handles Range requests, which is what lets a `<video>` seek
without downloading the whole file. Generated files get an `immutable` cache
header because the name embeds a millisecond timestamp and a re-render never
overwrites an old URL the chat already stored.

A render that produces an image the model cannot see, or that produces nothing
at all, is still worth showing — hence `media` on the tool event carrying every
file, independent of what was attached to the model.

## What Is Not Implemented

- **img2img.** Uploading the chat's image back to ComfyUI via `/upload/image` to
  feed it into a `LoadImage` node is not wired up. The plumbing exists
  (`Client.UploadImage`, and `LoadImage` inputs are detected as bindings), but
  the tool never uploads anything.
- **Workflow editing.** Graphs can be replaced by pasting, but not patched
  node by node.
- **Multiple preview images per run.** Every output is collected and every image
  is shown, which on a batch of 8 means 8 thumbnails.