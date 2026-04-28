package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gense/ollama-manager/internal/ollama"
)

const maxWebAgentRounds = 24
const maxToolResultRunes = 12000

// webToolDefinitions returns Ollama Cloud web_search + web_fetch schemas (same names as ollama-python).
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

// OllamaCloudKeyConfigured reports whether OLLAMA_API_KEY is set (for web search/fetch tools).
func OllamaCloudKeyConfigured() bool {
	return strings.TrimSpace(os.Getenv("OLLAMA_API_KEY")) != ""
}

func (s *Server) ollamaCloudWebSearch(ctx context.Context, apiKey, query string, maxResults int) (string, error) {
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}
	body, _ := json.Marshal(map[string]any{
		"query":         query,
		"max_results":   maxResults,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://ollama.com/api/web_search", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("web_search: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("web_search decode: %w", err)
	}
	if len(out.Results) == 0 {
		return "No search results.", nil
	}
	var sb strings.Builder
	for i, r := range out.Results {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(&sb, "%d. %s\n%s\n%s", i+1, r.Title, r.URL, strings.TrimSpace(r.Content))
	}
	return sb.String(), nil
}

func (s *Server) ollamaCloudWebFetch(ctx context.Context, apiKey, pageURL string) (string, error) {
	u := strings.TrimSpace(pageURL)
	if u == "" {
		return "", fmt.Errorf("empty url")
	}
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("only http/https URLs are allowed")
	}
	body, _ := json.Marshal(map[string]any{"url": u})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://ollama.com/api/web_fetch", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("web_fetch: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Title   string   `json:"title"`
		Content string   `json:"content"`
		Links   []string `json:"links"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("web_fetch decode: %w", err)
	}
	var sb strings.Builder
	if out.Title != "" {
		fmt.Fprintf(&sb, "Title: %s\n\n", out.Title)
	}
	sb.WriteString(strings.TrimSpace(out.Content))
	if len(out.Links) > 0 {
		sb.WriteString("\n\nLinks on page:\n")
		for i, l := range out.Links {
			if i >= 15 {
				break
			}
			sb.WriteString("- " + l + "\n")
		}
	}
	return sb.String(), nil
}

func (s *Server) runWebTool(ctx context.Context, apiKey, name string, args json.RawMessage) (string, error) {
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("set OLLAMA_API_KEY (ollama.com/settings/keys) to use web search and web fetch")
	}
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
		return s.ollamaCloudWebSearch(ctx, apiKey, q, mr)
	case "web_fetch":
		u, _ := m["url"].(string)
		if strings.TrimSpace(u) == "" {
			return "Error: missing url for web_fetch", nil
		}
		return s.ollamaCloudWebFetch(ctx, apiKey, u)
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
	apiKey := strings.TrimSpace(os.Getenv("OLLAMA_API_KEY"))
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
			out, err := s.runWebTool(ctx, apiKey, n, tc.Function.Arguments)
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
