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

func assistantTextForUI(msg ollama.ChatMessage) string {
	c := msg.Content
	if msg.Thinking != "" && !strings.Contains(c, "<think>") {
		c = "<think>\n" + msg.Thinking + "\n</think>\n" + c
	}
	return c
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

// runWebToolAgentLoop calls Ollama in non-streaming mode repeatedly until the model
// returns a final assistant message without tool_calls, then streams that text as SSE
// (same event shape as handleChat) + done metrics.
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

	var last *ollama.ChatOnceResponse

	for round := 0; round < maxWebAgentRounds; round++ {
		if ctx.Err() != nil {
			return
		}
		req := ollama.ChatRequest{
			Model:    body.Model,
			Messages: msgs,
			Stream:   false,
			Think:    body.Think,
			Options:  body.Options,
			Tools:    tools,
		}
		res, err := s.ollama.ChatOnce(ctx, req)
		if err != nil {
			send("error", map[string]any{"error": err.Error()})
			return
		}
		last = res
		assistant := res.Message
		msgs = append(msgs, assistant)

		if len(assistant.ToolCalls) == 0 {
			text := assistantTextForUI(assistant)
			if text == "" {
				text = " "
			}
			send("chunk", map[string]any{
				"message": map[string]any{
					"content": text,
				},
			})
			total := last.PromptEvalCount + last.EvalCount
			send("done", map[string]any{
				"elapsed_ms":         time.Since(startedAt).Milliseconds(),
				"prompt_tokens":      last.PromptEvalCount,
				"completion_tokens":  last.EvalCount,
				"total_tokens":       total,
				"prompt_duration_ns": last.PromptEvalDuration,
				"eval_duration_ns":   last.EvalDuration,
				"total_duration_ns":  last.TotalDuration,
			})
			return
		}

		for _, tc := range assistant.ToolCalls {
			n := tc.Function.Name
			if n == "" {
				continue
			}
			send("tool", map[string]any{"name": n})
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
		}
	}
	send("error", map[string]any{"error": "web tools: too many tool rounds, try a narrower question"})
}
