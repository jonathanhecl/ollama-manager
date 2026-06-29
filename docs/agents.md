# Agent Chat Timeline Ordering

## Principle

The chat must **always** represent the faithful chronological order of actions the agent performed. Every element — thinking, tool usage, speaking — must appear below the previous one, in the exact order it happened.

## Timeline Architecture

Every assistant message uses a **timeline** — an ordered array of segments that represents the sequence of actions:

```
think → tool (using) → tool (used) → speak → tool (using) → tool (used) → think → speak → ...
```

### Segment Types

| Type | Description |
|------|-------------|
| `think` | The agent was thinking (inside `<think>` tags). Rendered as a collapsible `<details>` block. |
| `md` | The agent produced visible content (markdown). Rendered as a styled markdown block. |
| `tool` | The agent used a tool. Rendered as a tool log entry with status indicator. |

### Tool States

Tools have two visual states that transition in order:

1. **Using** (`generating` / `running`) — the tool is being called or executing. Shows a pulse animation (`◌` or `✎`).
2. **Used** (`ok` / `error`) — the tool finished. Shows a checkmark (`✓`) or cross (`✗`) with optional result preview.

The transition happens when the backend sends a `done` event for the tool, changing the entry's status from `running` to `ok` or `error`.

## How the Timeline is Built

### During Streaming (`chunk` events)

Content accumulates in `assistantRaw`. Think blocks (`<think>...</think>`) and answer text are tracked via `splitThink()`. The raw text is NOT yet in the timeline — it's rendered as "tail" content (live preview after the last timeline segment).

### When a Tool Event Arrives (`tool` event)

1. **`generating`** — Model is generating tool call arguments. Before adding the tool to the timeline, `flushSegmentToTimeline()` is called to push any accumulated think/md content to the timeline first. Then the tool entry is added.
2. **`start`** — Tool execution begins. If upgrading from `generating`, the existing timeline entry is updated in place. If no prior entry exists, content is flushed first, then a new entry is added.
3. **`done`** — Tool finished. The existing timeline entry's status is updated to `ok` or `error`.

### On Completion (`done` event)

`flushSegmentToTimeline()` is called one final time to push any remaining think/md content to the timeline. This ensures the full response is captured in order.

### Key Rule: Flush Before Tool

Before adding any tool entry to the timeline, `flushSegmentToTimeline()` is called. This ensures that content produced before the tool call (think blocks, markdown) appears **above** the tool entry in the timeline, preserving chronological order.

## Rendering

### Timeline Mode (always used for text models)

```html
<div class="chat-timeline">
  <details class="chat-think">...</details>           <!-- think segment -->
  <div class="chat-md chat-timeline-md">...</div>      <!-- md segment -->
  <div class="chat-tool-log chat-tool-log--tl">...</div> <!-- tool segment -->
  <div class="chat-md chat-timeline-md">...</div>      <!-- md segment -->
</div>
```

### Simple Mode (fallback for image models)

Image models bypass the timeline and render directly: think block + content.

## Stale Generating Entries

If the response ends with only `generating` entries (no real tool execution completed), they are cleaned up and removed from the timeline.
