# Ollama Model Capability Detection & Repair Guide

> **Scope:** How Ollama detects model capabilities (tools, thinking, vision, audio, embedding), how to fix models that only show "completion", and the known model families that require special handling.
>
> **Last updated:** 2026-05-28

---

## Table of Contents

1. [How Ollama Detects Capabilities](#how-ollama-detects-capabilities)
2. [How to Make Ollama Recognize Hidden Capabilities](#how-to-make-ollama-recognize-hidden-capabilities)
3. [Known Model Families](#known-model-families)
   - [LFM2 / LFM2MoE (Liquid AI)](#lfm2--lfm2moe-liquid-ai)
   - [Qwen 3.5](#qwen-35)
   - [Gemma 4 / 3 / 2](#gemma-4--3--2)
4. [General Repair Strategy](#general-repair-strategy)
5. [Implementation in ollama-manager](#implementation-in-ollama-manager)
6. [References](#references)

---

## How Ollama Detects Capabilities

Ollama evaluates **three independent signals** when determining which capabilities a model supports. All three are checked in `server/images.go` inside the `Capabilities()` method.

### Signal 1: GGUF Metadata

The GGUF file contains metadata keys that Ollama reads directly:

| Metadata Key | Capability |
|-------------|-----------|
| `vision.block_count` > 0 | `vision` |
| `audio.block_count` > 0 | `audio` |
| `pooling_type` == "mean" or "cls" | `embedding` |
| `general.architecture` | Used for parser auto-detection (indirect) |

If these keys are missing or set to zero, Ollama will not detect the corresponding capability, even if the model physically supports it.

### Signal 2: Template Variables

Ollama parses the Modelfile `TEMPLATE` as a Go text/template and scans for variable references:

| Template references | Capability added |
|--------------------|-----------------|
| `.Tools` | `tools` |
| `.ToolCalls` | `tools` (indirect) |
| `.Thinking` | `thinking` |
| `.Messages` + `.Tools` | `tools` (modern chat format) |

Example: a Llama 3 template uses `{{- range .Messages }}` which is why Llama 3 models show `tools` when the underlying architecture supports it.

### Signal 3: Built-in Parser (PARSER / RENDERER)

The `PARSER` and `RENDERER` directives in a Modelfile map to Go structs in `model/parsers/`. Each parser explicitly declares support:

```go
// From model/parsers/lfm2.go
type LFM2Parser struct {
    hasToolSupport     bool
    hasThinkingSupport bool
}

func (p *LFM2Parser) HasToolSupport() bool     { return p.hasToolSupport }
func (p *LFM2Parser) HasThinkingSupport() bool { return p.hasThinkingSupport }
```

When `PARSER lfm2-thinking` is set, Ollama's `Capabilities()` function checks:

```go
// From server/images.go (simplified)
if builtinParser != nil && builtinParser.HasToolSupport() {
    caps = append(caps, "tools")
}
if builtinParser != nil && builtinParser.HasThinkingSupport() {
    caps = append(caps, "thinking")
}
```

**This is the most reliable signal** because it does not depend on the GGUF author adding metadata flags or the template referencing modern variables.

---

## How to Make Ollama Recognize Hidden Capabilities

There are three ways to make Ollama recognize capabilities it would otherwise miss. They can be used independently or combined.

### Method A: Add a Built-in Parser (Most Reliable)

If Ollama ships with a parser for your model family, simply add it to the Modelfile:

    PARSER lfm2-thinking
    PARSER qwen3.5
    PARSER gemma4

**How it works:** The parser's Go implementation declares `HasToolSupport()` and/or `HasThinkingSupport()`. Ollama's `Capabilities()` function checks these booleans and appends the capability strings unconditionally.

**Pros:**
- Works even if the GGUF has no metadata flags
- Works even if the template is legacy (`.System` / `.Prompt`)
- Preserves the model's native token format
- Single-line change

**Cons:**
- Only works if Ollama actually ships a parser for that architecture
- Some parsers expect a specific output format from the model

**When to use:** Any model where Ollama has a built-in parser but the Modelfile author did not include the `PARSER` directive. This is the case for most Hugging Face GGUFs that were not originally created for Ollama.

### Method B: Rewrite the Template

Replace the legacy template with a modern one that references `.Tools` and `.Thinking`:

    TEMPLATE """{{- if .System }}<|im_start|>system
    {{ .System }}
    {{- end }}{{- range .Messages }}<|im_start|>{{ .Role }}
    {{ .Content }}
    {{- end }}<|im_start|>assistant
    """

If this template also contains `.Tools` references inside conditionals, Ollama will detect `tools`.

**How it works:** Ollama parses the template AST and looks for field access nodes named `Tools`, `ToolCalls`, or `Thinking`. If found, the corresponding capability is added.

**Pros:**
- Enables automatic tool injection by Ollama's `/api/chat` endpoint
- Works for any model architecture (no parser needed)
- Standard approach used by official Ollama models

**Cons:**
- Requires the model to actually understand the new template format
- May break the model's native tool/thinking format if it differs
- Very easy to get stop parameters wrong (model stops after 1 token)
- Loses any special token characters that were in the original template

**When to use:** When no built-in parser exists, or when you want full Ollama API integration (automatic tool injection, thinking toggle via `think` option).

### Method C: Inject Metadata into the GGUF (Advanced)

If you control the GGUF creation process, add metadata keys before quantization:

- `vision.block_count = 12`
- `audio.block_count = 4`
- `pooling_type = "mean"`

**How it works:** Ollama reads these keys at load time. `vision.block_count` > 0 adds `vision`; `audio.block_count` > 0 adds `audio`; `pooling_type` adds `embedding`.

**Pros:**
- The "correct" way from a metadata perspective
- Works for all consumers of the GGUF, not just Ollama

**Cons:**
- Requires access to the original weights and quantization pipeline
- Not practical for end users who only have the GGUF file

**When to use:** When you are the model publisher creating the GGUF from scratch.

---

## Known Model Families

| Family | Architectures | Parser | Adds tools | Adds thinking | Repair Strategy |
|--------|--------------|--------|-----------|--------------|-----------------|
| **LFM2 / LFM2MoE** | `lfm2`, `lfm2moe` | `lfm2-thinking` | Yes | Yes | **Preserve exact Modelfile** - template/stops contain invisible special tokens |
| **LFM2 / LFM2MoE** | `lfm2`, `lfm2moe` | `lfm2` | Yes | No | **Preserve exact Modelfile** |
| **Qwen 3.5** | `qwen3.5`, `qwen35`, `qwen35moe` | `qwen3.5` | Yes | Yes | Replace template with `.Messages` + `.Tools` / `.Thinking` |
| **Gemma 4** | `gemma4` | `gemma4` | Yes | Yes | Replace template; renderer handles stops internally |
| **Gemma 3** | `gemma3` | `gemma3` | Yes | No | Replace template with Gemma chat format |
| **Gemma 2** | `gemma2` | `gemma2` | No | No | Replace template; limited tool support |
| **Gemma** | `gemma` | `gemma` | No | No | Replace template; basic chat support |

### LFM2 / LFM2MoE (Liquid AI)

**Models:** `hf.co/LiquidAI/LFM2.5-8B-A1B-GGUF`, `lfm2.5-thinking`, LFM2MoE variants
**Architecture:** `lfm2`, `lfm2moe`
**Capabilities:** `completion`, `tools`, `thinking` (with parser)

**Native formats:**
- Thinking: `<thinking>` ... `</thinking>`
- Tool calls: `<|tool_call_start|>[function_name(arg=value)]`

**Why it fails:** The GGUF has no tool/thinking metadata, and the default template uses legacy variables (`.System` / `.Prompt` / `.Response`) instead of `.Messages` / `.Tools`. Ollama therefore reports only `completion`.

**How we fixed it:**
1. Run `ollama show` on the base model to extract the **exact** Modelfile
2. Programmatically prepend `PARSER lfm2-thinking` (or `lfm2` if thinking is not desired)
3. **Do NOT regenerate the template or stop parameters** — they contain special invisible token characters
4. Create a new model from the modified Modelfile

**Critical warning:** The third `PARAMETER stop` in the LFM2.5 Modelfile is NOT an empty string or a space. It is a special token character sequence. If you recreate this Modelfile manually in a text editor, you will almost certainly get the wrong character sequence, and the model will stop after generating the first token.

**Behavior after fix:**
- `thinking`: Works perfectly. Ollama automatically strips thinking tags from `content` and populates the `thinking` field in the API response.
- `tools`: Ollama detects the capability, but the legacy template cannot render `.Tools`, so tool definitions are not automatically injected. The model still generates native tool calls when tools are passed manually in the system prompt.

### Qwen 3.5

**Models:** `qwen3.5`, `qwen3.5moe`, Qwen3 variants
**Architecture:** `qwen3.5`, `qwen35`, `qwen35moe`
**Capabilities:** `completion`, `tools`, `thinking` (with parser)

**Why it fails:** Same pattern — GGUF lacks metadata, template uses legacy variables.

**How we fix it:**
1. Replace the template with the Qwen3.5 chat format using `.Messages`, `.Tools`, `.ToolCalls`, and `.Thinking`
2. Add `PARSER qwen3.5` and `RENDERER qwen3.5`
3. Set stop parameter to `\u003c\u002f\u005c\u007c\u007c\u0069\u006d\u005f\u0065\u006e\u0064\u007c\u003e` (im_end)

**Behavior after fix:**
- Full Ollama API integration: tools are automatically injected into the prompt
- Thinking is toggled via the `think` option in the API request

### Gemma 4 / 3 / 2

**Models:** `gemma4`, `gemma3`, `gemma2`, `gemma` variants from Hugging Face or Google
**Architecture:** `gemma4`, `gemma3`, `gemma2`, `gemma`
**Capabilities:** Varies by version (see table above)

**Why it fails:** Gemma models from Hugging Face often have incomplete metadata. Gemma 4 requires the `gemma4` renderer for multimodal and tool support.

**How we fix it:**
1. For Gemma 4: use `RENDERER gemma4` + `PARSER gemma4`; the renderer handles stops internally
2. For Gemma 3: replace template with Gemma 3 chat format, add `PARSER gemma3`
3. For Gemma 2 / Gemma: replace template with basic Gemma chat format, add `PARSER gemma2` / `PARSER gemma`

**Behavior after fix:**
- Gemma 4: full tool and thinking support via Ollama API
- Gemma 3: tool support, no thinking
- Gemma 2 / Gemma: basic chat only

---

## General Repair Strategy

When you encounter a model that only shows `completion`, follow this decision tree:

1. **Check architecture:** Run `ollama show <model>` and look for `general.architecture` in `model_info`
2. **Check if Ollama has a parser:** Look up the architecture in the Known Model Families table above
3. **If a parser exists:** Use Method A (add `PARSER`) — this is the safest and most reliable approach
4. **If no parser exists:** Use Method B (rewrite template) — but test thoroughly for regressions
5. **If you are the publisher:** Use Method C (fix GGUF metadata) — the most correct long-term solution

**Golden rule for parser-based fixes:**
- For families like LFM2 where the template contains invisible/special token characters: **always preserve the exact original Modelfile and only inject the PARSER directive**
- For families like Qwen 3.5 where the template is standard: **replace the template with the modern format** to get full API integration

---

## Implementation in ollama-manager

The ollama-manager web app already implements automatic parser detection in `internal/server/model_repair.go`.

### What happens when you click "Repair" on an LFM2 model:

1. Backend detects `general.architecture` contains `lfm2` or `lfm2moe`
2. Shows warning: *"LFM2 models use special token characters in their template/stop parameters. The repair will preserve the original Modelfile exactly."*
3. Extracts the original Modelfile via `ollama show`
4. Strips any existing `PARSER` / `RENDERER` directives from the original
5. Injects `PARSER lfm2-thinking` (or `PARSER lfm2`) right after the `FROM` line
6. Adds `SYSTEM`, `PARAMETER num_ctx`, `PARAMETER temperature` based on user-selected presets
7. Creates the `:fixed` model with the modified Modelfile

### Key code paths:

- `isLFM2Arch(arch string) bool` — detects LFM2 architectures
- `buildLFM2RepairPreview(...)` — preserves exact Modelfile + injects parser
- `rendererFromArch(arch string) string` — maps architecture to parser name
- `repairRenderer(preset, arch)` — same mapping for template preset mode

### For other families:

- Qwen 3.5: `repairTemplate("qwen35", tools, thinking)` generates the modern Qwen template
- Gemma 4: `repairRenderer("gemma4", "gemma4")` returns `"gemma4"`, which triggers both `RENDERER` and `PARSER`

---

## References

- Ollama source: `model/parsers/lfm2.go` — `LFM2Parser` implementation
- Ollama source: `model/parsers/parsers.go` — parser registration and `ParserForName`
- Ollama source: `server/images.go` — `Capabilities()` detection logic
- Ollama source: `parser/parser.go` — `PARSER` / `RENDERER` Modelfile directives
- Ollama source: `template/template.go` — template AST scanning for `.Tools` / `.Thinking`
- LFM2.5 docs: https://huggingface.co/LiquidAI/LFM2.5-8B-A1B

