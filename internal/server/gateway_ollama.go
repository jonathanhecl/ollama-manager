package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
)

// ---------- Gateway: Ollama-native endpoints ----------

// gatewayOllamaRoutes registers the Ollama-compatible mux behind the Bearer
// middleware. Called from gatewayRoutes so both protocols share one listener.
func (s *Server) gatewayOllamaRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/tags", s.requireGatewayKey(http.HandlerFunc(s.handleGatewayTags)))
	mux.Handle("POST /api/chat", s.requireGatewayKey(http.HandlerFunc(s.handleGatewayOllamaChat)))
	mux.Handle("POST /api/generate", s.requireGatewayKey(http.HandlerFunc(s.handleGatewayOllamaGenerate)))
	mux.Handle("POST /api/show", s.requireGatewayKey(http.HandlerFunc(s.handleGatewayShow)))
}

// writeGatewayOllamaError uses Ollama's {"error": "msg"} shape.
func writeGatewayOllamaError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// gatewayExposedEntry is one model visible through the gateway.
type gatewayExposedEntry struct {
	Name     string
	Local    *ollama.Model
	External *ExternalModelRecord
}

// gatewayExposedEntries resolves full records: local Ollama models (minus
// archived) plus enabled externals, filtered by the allowlist when set.
func (s *Server) gatewayExposedEntries(ctx context.Context, lister gatewayListerOverride) ([]gatewayExposedEntry, error) {
	var locals []ollama.Model
	if lister == nil {
		models, err := s.ollama.List(ctx)
		if err != nil {
			// Ollama down: externals (if any) are still servable.
			log.Printf("gateway: ollama list failed: %v", err)
		} else {
			locals = models
		}
	} else {
		models, err := lister(ctx)
		if err != nil {
			return nil, err
		}
		locals = models
	}

	s.cfgMu.RLock()
	modelsConfigured := s.cfg.Gateway.Models != nil
	allow := append([]string(nil), s.cfg.Gateway.Models...)
	s.cfgMu.RUnlock()
	allowed := make(map[string]bool, len(allow))
	for _, n := range allow {
		allowed[strings.TrimSpace(n)] = true
	}
	keep := func(name string) bool {
		if !modelsConfigured {
			return true
		}
		return allowed[strings.TrimSpace(name)]
	}

	var out []gatewayExposedEntry
	for i := range locals {
		name := strings.TrimSpace(locals[i].Name)
		if name == "" || s.archived.IsArchived(name) || !keep(name) {
			continue
		}
		m := locals[i]
		out = append(out, gatewayExposedEntry{Name: name, Local: &m})
	}
	if s.externalModels != nil {
		for name, rec := range s.externalModels.All() {
			if rec.Disabled || strings.TrimSpace(name) == "" || !keep(name) {
				continue
			}
			r := rec
			out = append(out, gatewayExposedEntry{Name: name, External: &r})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Server) handleGatewayTags(w http.ResponseWriter, r *http.Request) {
	entries, err := s.gatewayExposedEntries(r.Context(), nil)
	if err != nil {
		writeGatewayOllamaError(w, http.StatusBadGateway, err.Error())
		return
	}
	models := make([]any, 0, len(entries))
	for _, e := range entries {
		if e.Local != nil {
			m := *e.Local
			models = append(models, map[string]any{
				"name":        m.Name,
				"model":       m.Model,
				"modified_at": m.ModifiedAt.UTC().Format(time.RFC3339),
				"size":        m.Size,
				"digest":      m.Digest,
				"details":     m.Details,
			})
			continue
		}
		models = append(models, map[string]any{
			"name":        e.Name,
			"model":       e.Name,
			"modified_at": e.External.CreatedAt.UTC().Format(time.RFC3339),
			"size":        0,
			"digest":      "",
			"details": map[string]any{
				"parent_model":       "",
				"format":             "external",
				"family":             "external",
				"families":           []string{"external"},
				"parameter_size":     "",
				"quantization_level": "",
			},
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
}

func (s *Server) handleGatewayShow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeGatewayOllamaError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Model)
	if name == "" {
		name = strings.TrimSpace(body.Name)
	}
	entries, err := s.gatewayExposedEntries(r.Context(), nil)
	if err != nil {
		writeGatewayOllamaError(w, http.StatusBadGateway, err.Error())
		return
	}
	names := make([]string, 0, len(entries))
	byName := make(map[string]gatewayExposedEntry, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
		byName[e.Name] = e
	}
	resolved := gatewayResolveModel(name, names)
	if resolved == "" {
		writeGatewayOllamaError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", name))
		return
	}
	entry := byName[resolved]
	if entry.Local != nil {
		info, err := s.ollama.Show(r.Context(), entry.Name)
		if err != nil {
			writeGatewayOllamaError(w, http.StatusBadGateway, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(info)
		return
	}
	caps := append([]string(nil), entry.External.Capabilities...)
	if len(caps) == 0 {
		caps = []string{"completion"}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"license":    "",
		"modelfile":  "# External model proxied by ollama-manager gateway",
		"parameters": "",
		"template":   "",
		"details": map[string]any{
			"parent_model":       "",
			"format":             "external",
			"family":             "external",
			"families":           []string{"external"},
			"parameter_size":     "",
			"quantization_level": "",
		},
		"model_info":   map[string]any{},
		"capabilities": caps,
	})
}

// gatewayChatResult aggregates one streamed chat run.
type gatewayChatResult struct {
	Content    string
	Thinking   string
	ToolCalls  []ollama.ToolCall // merged per index, full arguments
	DoneReason string
	PromptTok  int
	ComplTok   int
	Failed     string
}

// gatewayRunChat executes req (always streamed internally) and aggregates.
func (s *Server) gatewayRunChat(ctx context.Context, req ollama.ChatRequest) gatewayChatResult {
	st := newGatewayStreamState(req.Model)
	var content, thinking strings.Builder
	var res gatewayChatResult
	err := s.chatWithModel(ctx, req, func(chunk ollama.ChatChunk) error {
		if chunk.Error != "" {
			res.Failed = chunk.Error
			return fmt.Errorf("%s", chunk.Error)
		}
		if chunk.PromptEvalCount > 0 {
			res.PromptTok = chunk.PromptEvalCount
		}
		if chunk.EvalCount > 0 {
			res.ComplTok = chunk.EvalCount
		}
		if chunk.Done {
			if chunk.DoneReason != "" {
				res.DoneReason = chunk.DoneReason
			}
			return nil
		}
		content.WriteString(chunk.Message.Content)
		thinking.WriteString(chunk.Message.Thinking)
		st.deltaToolCalls(chunk.Message.ToolCalls)
		return nil
	})
	if err != nil && res.Failed == "" {
		res.Failed = err.Error()
	}
	res.Content = content.String()
	res.Thinking = thinking.String()
	for _, idx := range st.toolOrder {
		acc := st.toolAcc[idx]
		res.ToolCalls = append(res.ToolCalls, ollama.ToolCall{
			Type: "function",
			Function: struct {
				Index     int             `json:"index,omitempty"`
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}{
				Index:     idx,
				Name:      acc.name,
				Arguments: json.RawMessage(acc.fullArgs),
			},
		})
	}
	return res
}

// gatewayCheckModel resolves the requested model against the exposed set.
func (s *Server) gatewayCheckModel(ctx context.Context, requested string) (string, error) {
	entries, err := s.gatewayExposedEntries(ctx, nil)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	resolved := gatewayResolveModel(requested, names)
	if resolved == "" {
		return "", fmt.Errorf("model %q not found", strings.TrimSpace(requested))
	}
	return resolved, nil
}

// normalizeGatewayChatBody validates an Ollama /api/chat body: defaults the
// role. Tool-call arguments keep their raw JSON form.
func normalizeGatewayChatBody(body *ollama.ChatRequest) error {
	body.Model = strings.TrimSpace(body.Model)
	if body.Model == "" {
		return fmt.Errorf("missing 'model'")
	}
	if len(body.Messages) == 0 {
		return fmt.Errorf("missing 'messages'")
	}
	for i := range body.Messages {
		if strings.TrimSpace(body.Messages[i].Role) == "" {
			body.Messages[i].Role = "user"
		}
		for j := range body.Messages[i].ToolCalls {
			if body.Messages[i].ToolCalls[j].Type == "" {
				body.Messages[i].ToolCalls[j].Type = "function"
			}
		}
	}
	return nil
}

// gatewayArgsValue renders merged tool-call arguments for Ollama-shaped
// output: a JSON object when parseable, the raw string otherwise, and an
// empty object when absent (a bare json.RawMessage would break encoding).
func gatewayArgsValue(raw string) any {
	t := strings.TrimSpace(raw)
	if t == "" {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal([]byte(t), &v); err != nil {
		return t
	}
	return json.RawMessage(t)
}

func ollamaNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func (s *Server) handleGatewayOllamaChat(w http.ResponseWriter, r *http.Request) {
	// Decode into a shadow struct so absent "stream" defaults to true
	// (Ollama semantics), unlike encoding/json's false zero-value.
	var raw struct {
		Model    string               `json:"model"`
		Messages []ollama.ChatMessage `json:"messages"`
		Stream   *bool                `json:"stream"`
		Think    *ollama.ThinkLevel   `json:"think"`
		Options  map[string]any       `json:"options"`
		Tools    any                  `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeGatewayOllamaError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	stream := true
	if raw.Stream != nil {
		stream = *raw.Stream
	}
	body := ollama.ChatRequest{
		Model:    raw.Model,
		Messages: raw.Messages,
		Stream:   true, // always stream internally; aggregated when false
		Think:    raw.Think,
		Options:  raw.Options,
		Tools:    raw.Tools,
	}
	if err := normalizeGatewayChatBody(&body); err != nil {
		writeGatewayOllamaError(w, http.StatusBadRequest, err.Error())
		return
	}
	resolved, err := s.gatewayCheckModel(r.Context(), body.Model)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeGatewayOllamaError(w, http.StatusNotFound, err.Error())
		} else {
			writeGatewayOllamaError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	body.Model = resolved

	if stream {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		flush := func() {
			if flusher != nil {
				flusher.Flush()
			}
		}
		ctx := r.Context()
		st := newGatewayStreamState(resolved)
		err := s.chatWithModel(ctx, body, func(chunk ollama.ChatChunk) error {
			if chunk.Error != "" {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": chunk.Error})
				flush()
				return fmt.Errorf("%s", chunk.Error)
			}
			if chunk.Done {
				doneReason := chunk.DoneReason
				if doneReason == "" {
					doneReason = "stop"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"model":                resolved,
					"created_at":           ollamaNow(),
					"message":              map[string]any{"role": "assistant", "content": ""},
					"done_reason":          doneReason,
					"done":                 true,
					"prompt_eval_count":    chunk.PromptEvalCount,
					"eval_count":           chunk.EvalCount,
					"prompt_eval_duration": chunk.PromptEvalDuration,
					"eval_duration":        chunk.EvalDuration,
					"total_duration":       chunk.TotalDuration,
				})
				flush()
				return nil
			}
			msg := map[string]any{"role": "assistant", "content": chunk.Message.Content}
			if chunk.Message.Thinking != "" {
				msg["thinking"] = chunk.Message.Thinking
			}
			if len(chunk.Message.Images) > 0 {
				msg["images"] = chunk.Message.Images
			}
			if tc := st.deltaToolCalls(chunk.Message.ToolCalls); len(tc) > 0 {
				calls := make([]any, 0, len(tc))
				for _, d := range tc {
					m := d.(map[string]any)
					fn := m["function"].(map[string]any)
					entry := map[string]any{"type": "function", "function": map[string]any{}}
					if n, ok := fn["name"].(string); ok && n != "" {
						entry["function"].(map[string]any)["name"] = n
					}
					if a, ok := fn["arguments"].(string); ok && a != "" {
						entry["function"].(map[string]any)["arguments"] = a
					}
					calls = append(calls, entry)
				}
				msg["tool_calls"] = calls
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model":      resolved,
				"created_at": ollamaNow(),
				"message":    msg,
				"done":       false,
			})
			flush()
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return nil
			}
		})
		_ = err
		return
	}

	res := s.gatewayRunChat(r.Context(), body)
	if res.Failed != "" {
		writeGatewayOllamaError(w, http.StatusBadGateway, res.Failed)
		return
	}
	msg := map[string]any{"role": "assistant", "content": res.Content}
	if res.Thinking != "" {
		msg["thinking"] = res.Thinking
	}
	if len(res.ToolCalls) > 0 {
		calls := make([]any, 0, len(res.ToolCalls))
		for _, tc := range res.ToolCalls {
			calls = append(calls, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":      tc.Function.Name,
					"arguments": gatewayArgsValue(string(tc.Function.Arguments)),
				},
			})
		}
		msg["tool_calls"] = calls
	}
	doneReason := res.DoneReason
	if doneReason == "" {
		doneReason = "stop"
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"model":             resolved,
		"created_at":        ollamaNow(),
		"message":           msg,
		"done_reason":       doneReason,
		"done":              true,
		"prompt_eval_count": res.PromptTok,
		"eval_count":        res.ComplTok,
		"total_tokens":      res.PromptTok + res.ComplTok,
	})
}

func (s *Server) handleGatewayOllamaGenerate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model   string             `json:"model"`
		Prompt  string             `json:"prompt"`
		System  string             `json:"system"`
		Images  []string           `json:"images"`
		Options map[string]any     `json:"options"`
		Stream  *bool              `json:"stream"`
		Think   *ollama.ThinkLevel `json:"think"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeGatewayOllamaError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	stream := true
	if body.Stream != nil {
		stream = *body.Stream
	}
	if strings.TrimSpace(body.Model) == "" {
		writeGatewayOllamaError(w, http.StatusBadRequest, "missing 'model'")
		return
	}
	resolved, err := s.gatewayCheckModel(r.Context(), body.Model)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeGatewayOllamaError(w, http.StatusNotFound, err.Error())
		} else {
			writeGatewayOllamaError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	msgs := make([]ollama.ChatMessage, 0, 2)
	if strings.TrimSpace(body.System) != "" {
		msgs = append(msgs, ollama.ChatMessage{Role: "system", Content: body.System})
	}
	msgs = append(msgs, ollama.ChatMessage{Role: "user", Content: body.Prompt, Images: body.Images})
	req := ollama.ChatRequest{Model: resolved, Messages: msgs, Stream: true, Think: body.Think}
	if len(body.Options) > 0 {
		req.Options = body.Options
	}

	if stream {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		flush := func() {
			if flusher != nil {
				flusher.Flush()
			}
		}
		ctx := r.Context()
		err := s.chatWithModel(ctx, req, func(chunk ollama.ChatChunk) error {
			if chunk.Error != "" {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": chunk.Error})
				flush()
				return fmt.Errorf("%s", chunk.Error)
			}
			if chunk.Done {
				doneReason := chunk.DoneReason
				if doneReason == "" {
					doneReason = "stop"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"model":                resolved,
					"created_at":           ollamaNow(),
					"response":             "",
					"done_reason":          doneReason,
					"done":                 true,
					"prompt_eval_count":    chunk.PromptEvalCount,
					"eval_count":           chunk.EvalCount,
					"prompt_eval_duration": chunk.PromptEvalDuration,
					"eval_duration":        chunk.EvalDuration,
					"total_duration":       chunk.TotalDuration,
				})
				flush()
				return nil
			}
			ev := map[string]any{
				"model":      resolved,
				"created_at": ollamaNow(),
				"response":   chunk.Message.Content,
				"done":       false,
			}
			if chunk.Message.Thinking != "" {
				ev["thinking"] = chunk.Message.Thinking
			}
			_ = json.NewEncoder(w).Encode(ev)
			flush()
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return nil
			}
		})
		_ = err
		return
	}

	res := s.gatewayRunChat(r.Context(), req)
	if res.Failed != "" {
		writeGatewayOllamaError(w, http.StatusBadGateway, res.Failed)
		return
	}
	doneReason := res.DoneReason
	if doneReason == "" {
		doneReason = "stop"
	}
	ev := map[string]any{
		"model":             resolved,
		"created_at":        ollamaNow(),
		"response":          res.Content,
		"done_reason":       doneReason,
		"done":              true,
		"prompt_eval_count": res.PromptTok,
		"eval_count":        res.ComplTok,
	}
	if res.Thinking != "" {
		ev["thinking"] = res.Thinking
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ev)
}
