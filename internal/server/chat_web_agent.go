package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gense/ollama-manager/internal/ollama"
)

const maxWebAgentRounds = 24
const maxToolResultRunes = 12000

// webToolDefinitions returns web_search + web_fetch (same names as Ollama examples; executed locally on this server).
func webToolDefinitions() []any {
	return []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "web_search",
				"description": "Search the public web for up-to-date information (news, weather, facts, documentation). Use when the user needs current data not in the model.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"query"},
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Search query in natural language",
						},
						"max_results": map[string]any{
							"type":        "integer",
							"description": "Number of search results to return (1–10, default 5)",
						},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "web_fetch",
				"description": "Fetch and read the main text content of a public web page by URL (http or https).",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"url"},
					"properties": map[string]any{
						"url": map[string]any{
							"type":        "string",
							"description": "Full URL to fetch",
						},
					},
				},
			},
		},
	}
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "\n…(truncated)"
}

func parseToolArgs(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil && m != nil {
		return m
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil && str != "" {
		_ = json.Unmarshal([]byte(str), &m)
	}
	return m
}

func numFromAny(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	default:
		return 0
	}
}

func toolStartPayload(name string, args json.RawMessage) map[string]any {
	p := map[string]any{"phase": "start", "name": name}
	m := parseToolArgs(args)
	switch name {
	case "web_search":
		if q, _ := m["query"].(string); strings.TrimSpace(q) != "" {
			p["query"] = q
		}
		if mr := int(numFromAny(m["max_results"])); mr > 0 {
			p["max_results"] = mr
		}
	case "web_fetch":
		if u, _ := m["url"].(string); strings.TrimSpace(u) != "" {
			p["url"] = u
		}
	}
	return p
}

func (s *Server) runWebTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	m := parseToolArgs(args)
	switch name {
	case "web_search":
		q, _ := m["query"].(string)
		if strings.TrimSpace(q) == "" {
			return "Error: missing query for web_search", nil
		}
		mr := int(numFromAny(m["max_results"]))
		if mr == 0 {
			mr = 5
		}
		return s.localWebSearch(ctx, q, mr)
	case "web_fetch":
		u, _ := m["url"].(string)
		if strings.TrimSpace(u) == "" {
			return "Error: missing url for web_fetch", nil
		}
		return s.localWebFetch(ctx, u)
	default:
		return fmt.Sprintf("Error: tool %q is not implemented on this server", name), nil
	}
}

// runWebToolAgentLoop calls Ollama with stream:true (same as handleChat) for each
// agent step so the browser gets real token/chunk streaming. When the model
// returns tool calls, the stream has finished; we execute tools and go to the
// next round. Final answer streams live; we do not buffer the full reply to fake deltas.
func (s *Server) runWebToolAgentLoop(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, body chatRequestBody) {
	send := func(event string, payload any) {
		buf, _ := json.Marshal(payload)
		if event != "" {
			fmt.Fprintf(w, "event: %s\n", event)
		}
		fmt.Fprintf(w, "data: %s\n\n", buf)
		flusher.Flush()
	}

	startedAt := time.Now()
	tools := webToolDefinitions()

	msgs := make([]ollama.ChatMessage, len(body.Messages))
	copy(msgs, body.Messages)

	accComp := 0
	var accEvalNS int64

	for round := 0; round < maxWebAgentRounds; round++ {
		if ctx.Err() != nil {
			return
		}
		req := ollama.ChatRequest{
			Model:    body.Model,
			Messages: msgs,
			Stream:   true,
			Think:    body.Think,
			Options:  body.Options,
			Tools:    tools,
		}
		var last ollama.ChatChunk
		// Streaming sends deltas; the final {done:true} line often has an empty or partial
		// message, while tool_calls appeared on an earlier line. We must merge every chunk
		// to decide the real assistant turn (same as the client-side += on content).
		var acc ollama.ChatMessage
		acc.Role = "assistant"
		err := s.ollama.Chat(ctx, req, func(ev ollama.ChatChunk) error {
			last = ev
			m := ev.Message
			if m.Thinking != "" {
				acc.Thinking += m.Thinking
			}
			if m.Content != "" {
				acc.Content += m.Content
			}
			if len(m.ToolCalls) > 0 {
				acc.ToolCalls = m.ToolCalls
			}
			send("chunk", ev)
			return nil
		})
		if err != nil {
			send("error", map[string]any{"error": err.Error()})
			return
		}
		if acc.Role == "" {
			acc.Role = "assistant"
		}

		assistant := acc
		// Rare: tool_calls only on the last object; keep if merge missed them.
		if len(assistant.ToolCalls) == 0 && len(last.Message.ToolCalls) > 0 {
			assistant.ToolCalls = last.Message.ToolCalls
		}
		msgs = append(msgs, assistant)
		accComp += last.EvalCount
		accEvalNS += last.EvalDuration

		if len(assistant.ToolCalls) == 0 {
			send("done", map[string]any{
				"elapsed_ms":         time.Since(startedAt).Milliseconds(),
				"prompt_tokens":      last.PromptEvalCount,
				"completion_tokens":  accComp,
				"total_tokens":       last.PromptEvalCount + accComp,
				"prompt_duration_ns": last.PromptEvalDuration,
				"eval_duration_ns":   accEvalNS,
				"total_duration_ns":  last.TotalDuration,
			})
			return
		}

		for _, tc := range assistant.ToolCalls {
			n := tc.Function.Name
			if n == "" {
				continue
			}
			send("tool", toolStartPayload(n, tc.Function.Arguments))
			out, err := s.runWebTool(ctx, n, tc.Function.Arguments)
			if err != nil {
				out = "Error: " + err.Error()
			}
			out = truncateRunes(out, maxToolResultRunes)
			msgs = append(msgs, ollama.ChatMessage{
				Role:     "tool",
				ToolName: n,
				Content:  out,
			})
			done := map[string]any{"phase": "done", "name": n, "ok": err == nil}
			if err != nil {
				done["error"] = err.Error()
			} else if out != "" {
				done["result_preview"] = truncateRunes(out, 320)
				done["result_runes"] = utf8.RuneCountInString(out)
			}
			send("tool", done)
		}
	}
	send("error", map[string]any{"error": "web tools: too many tool rounds, try a narrower question"})
}
