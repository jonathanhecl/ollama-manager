# REST API Guide: Using Image Generation Models (Diffusion)

This guide explains how to programmatically interact with diffusion-based image generation models (such as Flux or Z-Image-Turbo) using the `ollama-manager` REST API.

---

## 1. Transparent `/api/chat` to `/api/generate` Redirection

Ollama's diffusion models (like `Flux`) do not support standard chat payloads (`/api/chat`) and will reject them with a `400 Bad Request` error. 

To resolve this while maintaining a uniform API footprint, **`ollama-manager` automatically redirects requests sent to `/api/chat` to Ollama's `/api/generate` endpoint** if it detects that the model has the `image` capability. 

### How it works:
1. When you request `/api/chat`, the server queries the model capabilities.
2. If the model has `image` capability, the server extracts the last user message text as the prompt and any attached image base64 elements.
3. The server queries Ollama's `/api/generate` endpoint using the extracted prompt and images.
4. The output NDJSON stream from `/api/generate` is translated back to standard SSE chat chunk payloads (`event: chunk`) on the fly, allowing clients to consume it without any modifications.

---

## 2. API Request Payload Structure

Send a `POST` request to `/api/chat`. By default, this endpoint streams SSE (Server-Sent Events) back to the client.

* **Method:** `POST`
* **Path:** `/api/chat`
* **Headers:** 
  * `Content-Type: application/json`
  * `Cookie: <session_token>` (if password authentication is enabled)

### Text-to-Image (Txt2Img) Request Payload

```json
{
  "model": "x/flux2-klein:4b",
  "messages": [
    {
      "role": "user",
      "content": "A high-tech cyberpunk cat wearing neon sunglasses, detailed digital art"
    }
  ],
  "options": {
    "width": 512,
    "height": 512,
    "steps": 4,
    "seed": 42
  }
}
```

### Image-to-Image (Img2Img) / Image Editing Request Payload
To modify or perform variations of an existing image, include a base64-encoded string representing the source image in the `"images"` array of the user message.

```json
{
  "model": "x/flux2-klein:4b",
  "messages": [
    {
      "role": "user",
      "content": "Transform this picture into a starry night van gogh oil painting style",
      "images": [
        "iVBORw0KGgoAAAANS... (base64_encoded_source_image_bytes)"
      ]
    }
  ],
  "options": {
    "width": 512,
    "height": 512,
    "steps": 8
  }
}
```

### Option Parameters
These parameters must be placed inside the `"options"` object:
* **`width`**: (Integer) Width of the output image in pixels. Resolution max is `1024`.
* **`height`**: (Integer) Height of the output image in pixels. Resolution max is `1024`.
* **`steps`**: (Integer) Inference steps. For accelerated/Turbo models, values between `4` and `8` are recommended. For standard models, use `20` to `30`.
* **`seed`**: (Integer) Random seed. If omitted or set to `0`, a random seed is selected for every new generation.

---

## 3. Streaming SSE Response Format

The server responds with a Server-Sent Events stream (`text/event-stream`). Events are newline-delimited (`\n\n`) and contain structured JSON payloads.

### SSE Events

#### A. Step Progress Event (`event: chunk`)
During image generation, the model reports its step progress periodically. These chunks contain `completed` and `total` step counts:

```http
event: chunk
data: {"model":"x/flux2-klein:4b","completed":1,"total":4,"done":false}

event: chunk
data: {"model":"x/flux2-klein:4b","completed":2,"total":4,"done":false}
```

* **`completed`**: The index of the generation step currently finished.
* **`total`**: The total configured inference steps (`steps`).

#### B. Final Content Event (`event: chunk`)
When the diffusion process completes, the final `chunk` event contains the generated image payload as a Base64-encoded PNG/JPEG string in the message content:

```http
event: chunk
data: {"model":"x/flux2-klein:4b","message":{"role":"assistant","content":"iVBORw0KGgoAAAANS... (large_base64_image_bytes)"},"done":false}
```

#### C. Completion Event (`event: done`)
Sent at the very end to provide execution metrics and token stats:

```http
event: done
data: {"elapsed_ms":12450,"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_duration_ns":0,"eval_duration_ns":0,"total_duration_ns":12450000000}
```

#### D. Error Event (`event: error`)
Sent if a failure occurs (e.g., model not found or out of VRAM):

```http
event: error
data: {"error":"ollama: model out of memory"}
```

---

## 4. Example: Querying and Saving via cURL

### Generating an Image
To request an image directly using `curl`, you can send the request and parse the output. Here is how to make the API call and output the resulting base64 response directly.

```bash
curl -s -X POST http://127.0.0.1:7860/api/chat \
  -H "Content-Type: application/json" \
  -d '{
    "model": "x/flux2-klein:4b",
    "messages": [{"role": "user", "content": "A mystical forest at sunrise"}],
    "options": {
      "width": 512,
      "height": 512,
      "steps": 4
    }
  }'
```

Because the output is streamed as SSE, parsing it requires extracting the `content` field from the final chunk of the SSE stream and decoding the base64 string to a file:

```javascript
// Example Node.js extraction snippet
const responseText = "..."; // SSE stream response accumulated
const chunkJson = JSON.parse(responseText.match(/event: chunk\ndata: (\{.*?\})/g).pop().replace("event: chunk\ndata: ", ""));
const base64Image = chunkJson.message.content; 
require('fs').writeFileSync('output.png', Buffer.from(base64Image, 'base64'));
```

---

## 5. OS & Hardware Compatibility (MLX Model Runners)

Many diffusion-based models (like experimental `Flux` or `Z-Image-Turbo` Ollama packages) utilize Apple's MLX machine learning framework as their runner engine. 

### Compatibility Warning:
* **MLX-based models run exclusively on Apple Silicon (macOS) devices.**
* They are **not compatible** with Windows or Linux operating systems due to the lack of native MLX runner dynamic libraries.

### Error Signature:
If a user attempts to run an MLX-based model on Windows or Linux, Ollama will return a `500 Internal Server Error` with the following structure:
```json
{
  "error": "mlx runner failed: Error: failed to initialize MLX: failed to load MLX dynamic library (searched: [...]) (exit: exit status 1)"
}
```

### How `ollama-manager` Handles It:
To improve user experience on unsupported platforms, `ollama-manager` intercepts this signature at both the backend and frontend layers:
1. **Error Translation**: The raw technical error is translated into a user-friendly, localized notice (English/Spanish).
2. **Graceful UI Fallback**: The chat interface displays the notice as a standard text message in the chat timeline rather than rendering a broken/failed image, and displays a toast notification with the details.

