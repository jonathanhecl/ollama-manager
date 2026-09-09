package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
)

// ---------- Gateway: OpenAI-compatible endpoints ----------
// Served on the gateway's own listener (see plan step 4); the routes below
// are also directly testable via gatewayRoutes().

// gatewayRoutes builds the OpenAI+Ollama compatible mux behind the Bearer
// middleware.
func (s *Server) gatewayRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/models", s.requireGatewayKey(http.HandlerFunc(s.handleGatewayModels)))
	mux.Handle("POST /v1/chat/completions", s.requireGatewayKey(http.HandlerFunc(s.handleGatewayChatCompletions)))
	s.gatewayOllamaRoutes(mux)
	return mux
}

// requireGatewayKey enforces Bearer auth when the gateway requires it.
// Errors follow the OpenAI {"error": {...}} shape, not writeError.
func (s *Server) requireGatewayKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.cfgMu.RLock()
		requireAuth := s.cfg.Gateway.RequireAuth
		s.cfgMu.RUnlock()
		if !requireAuth {
			next.ServeHTTP(w, r)
			return
		}
		token := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
			token = strings.TrimSpace(h[len("Bearer "):])
		}
		if token == "" || s.gatewayKeys == nil || !s.gatewayKeys.Verify(token) {
			writeGatewayError(w, http.StatusUnauthorized, "Invalid API key", "invalid_request_error", "invalid_api_key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeGatewayError(w http.ResponseWriter, status int, message, errType, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	})
}

// gatewayExposedModels resolves the model names visible through the gateway:
// local Ollama models (minus archived) plus enabled external models, filtered
// by the Gateway.Models allowlist when non-empty. Sorted for determinism.
func (s *Server) gatewayExposedModels(ctx context.Context) ([]string, error) {
	return s.gatewayExposedModelsWithLister(ctx, nil)
}

// gatewayListerOverride lets tests stub the Ollama model list.
type gatewayListerOverride func(ctx context.Context) ([]ollama.Model, error)

func (s *Server) gatewayExposedModelsWithLister(ctx context.Context, lister gatewayListerOverride) ([]string, error) {
	set := make(map[string]bool)
	if lister == nil {
		models, err := s.ollama.List(ctx)
		if err != nil {
			// Ollama down: externals (if any) are still servable.
			log.Printf("gateway: ollama list failed: %v", err)
		} else {
			for _, m := range models {
				name := strings.TrimSpace(m.Name)
				if name == "" || s.archived.IsArchived(name) {
					continue
				}
				set[name] = true
			}
		}
	} else {
		models, err := lister(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range models {
			name := strings.TrimSpace(m.Name)
			if name == "" || s.archived.IsArchived(name) {
				continue
			}
			set[name] = true
		}
	}
	if s.externalModels != nil {
		for name, rec := range s.externalModels.All() {
			if rec.Disabled || strings.TrimSpace(name) == "" {
				continue
			}
			set[name] = true
		}
	}

	s.cfgMu.RLock()
	allow := append([]string(nil), s.cfg.Gateway.Models...)
	s.cfgMu.RUnlock()
	if len(allow) > 0 {
		allowed := make(map[string]bool, len(allow))
		for _, n := range allow {
			allowed[strings.TrimSpace(n)] = true
		}
		for name := range set {
			if !allowed[name] {
				delete(set, name)
			}
		}
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// gatewayResolveModel maps a requested name to an exposed model, tolerating
// a missing/redundant ":latest" suffix like the main UI does.
func gatewayResolveModel(requested string, exposed []string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return ""
	}
	for _, n := range exposed {
		if n == requested {
			return n
		}
	}
	base := strings.TrimSuffix(requested, ":latest")
	for _, n := range exposed {
		if strings.TrimSuffix(n, ":latest") == base {
			return n
		}
	}
	return ""
}

func (s *Server) handleGatewayModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.gatewayExposedModels(r.Context())
	if err != nil {
		writeGatewayError(w, http.StatusBadGateway, err.Error(), "server_error", "upstream_unavailable")
		return
	}
	now := time.Now().Unix()
	data := make([]any, 0, len(models))
	for _, name := range models {
		data = append(data, map[string]any{
			"id":       name,
			"object":   "model",
			"created":  now,
			"owned_by": "ollama-manager",
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
	})
}

// ---------- Inbound translation: OpenAI -> ollama.ChatRequest ----------

type gatewayInboundToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments"`
	} `json:"function"`
}

type gatewayInboundMessage struct {
	Role       string                   `json:"role"`
	Content    any                      `json:"content"`
	Name       string                   `json:"name"`
	ToolCalls  []gatewayInboundToolCall `json:"tool_calls"`
	ToolCallID string                   `json:"tool_call_id"`
}

type gatewayChatRequest struct {
	Model               string                  `json:"model"`
	Messages            []gatewayInboundMessage `json:"messages"`
	Stream              *bool                   `json:"stream"`
	Temperature         *float64                `json:"temperature"`
	TopP                *float64                `json:"top_p"`
	TopK                *int                    `json:"top_k"`
	MaxTokens           *int                    `json:"max_tokens"`
	MaxCompletionTokens *int                    `json:"max_completion_tokens"`
	Stop                any                     `json:"stop"`
	Seed                *int                    `json:"seed"`
	PresencePenalty     *float64                `json:"presence_penalty"`
	FrequencyPenalty    *float64                `json:"frequency_penalty"`
	ReasoningEffort     string                  `json:"reasoning_effort"`
	Tools               any                     `json:"tools"`
}

// gatewayMessageContent splits inbound content (string | parts) into plain
// text plus base64 images (data-URI prefix stripped; remote URLs pass through).
func gatewayMessageContent(content any) (string, []string) {
	if content == nil {
		return "", nil
	}
	if str, ok := content.(string); ok {
		return str, nil
	}
	parts, ok := content.([]any)
	if !ok {
		return "", nil
	}
	var texts []string
	var images []string
	for _, p := range parts {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		switch typ {
		case "text":
			if t, _ := m["text"].(string); t != "" {
				texts = append(texts, t)
			}
		case "image_url":
			raw := m["image_url"]
			var urlStr string
			switch v := raw.(type) {
			case string:
				urlStr = v
			case map[string]any:
				urlStr, _ = v["url"].(string)
			}
			urlStr = strings.TrimSpace(urlStr)
			if urlStr == "" {
				continue
			}
			// data:[mime];base64,<payload> -> raw payload (both the local
			// Ollama path and chatExternal accept plain base64).
			if idx := strings.Index(urlStr, ";base64,"); strings.HasPrefix(urlStr, "data:") && idx >= 0 {
				urlStr = urlStr[idx+len(";base64,"):]
			}
			images = append(images, urlStr)
		case "input_audio":
			// OpenAI realtime-style audio part {input_audio:{data,format}}.
			if obj, ok := m["input_audio"].(map[string]any); ok {
				if d, _ := obj["data"].(string); d != "" {
					images = append(images, d)
				}
			}
		}
	}
	return strings.Join(texts, "\n"), images
}

func gatewayToolArgsString(v any) string {
	if v == nil {
		return ""
	}
	if str, ok := v.(string); ok {
		return str
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}

// gatewayToChatRequest validates the inbound request and translates it.
func gatewayToChatRequest(in gatewayChatRequest) (ollama.ChatRequest, error) {
	model := strings.TrimSpace(in.Model)
	if model == "" {
		return ollama.ChatRequest{}, fmt.Errorf("missing 'model'")
	}
	if len(in.Messages) == 0 {
		return ollama.ChatRequest{}, fmt.Errorf("missing 'messages'")
	}
	msgs := make([]ollama.ChatMessage, 0, len(in.Messages))
	for _, m := range in.Messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role == "" {
			role = "user"
		}
		if role == "developer" {
			role = "system"
		}
		text, images := gatewayMessageContent(m.Content)
		out := ollama.ChatMessage{Role: role, Content: text, Images: images}
		if role == "tool" {
			out.ToolName = m.ToolCallID
			if out.ToolName == "" {
				out.ToolName = m.Name
			}
		} else if len(m.ToolCalls) > 0 {
			for idx, tc := range m.ToolCalls {
				var call ollama.ToolCall
				call.Type = tc.Type
				if call.Type == "" {
					call.Type = "function"
				}
				call.Function.Index = idx
				call.Function.Name = tc.Function.Name
				call.Function.Arguments = json.RawMessage(gatewayToolArgsString(tc.Function.Arguments))
				out.ToolCalls = append(out.ToolCalls, call)
			}
		}
		msgs = append(msgs, out)
	}
	opts := make(map[string]any)
	if in.Temperature != nil {
		opts["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		opts["top_p"] = *in.TopP
	}
	if in.TopK != nil {
		opts["top_k"] = *in.TopK
	}
	if n := in.MaxTokens; n != nil {
		opts["num_predict"] = *n
	} else if n := in.MaxCompletionTokens; n != nil {
		opts["num_predict"] = *n
	}
	if in.Stop != nil {
		opts["stop"] = in.Stop
	}
	if in.Seed != nil {
		opts["seed"] = *in.Seed
	}
	if in.PresencePenalty != nil {
		opts["presence_penalty"] = *in.PresencePenalty
	}
	if in.FrequencyPenalty != nil {
		opts["frequency_penalty"] = *in.FrequencyPenalty
	}
	var req ollama.ChatRequest
	req.Model = model
	req.Messages = msgs
	req.Stream = true // always stream internally; aggregated when stream=false
	if len(opts) > 0 {
		req.Options = opts
	}
	if in.Tools != nil {
		req.Tools = in.Tools
	}
	if lvl := strings.ToLower(strings.TrimSpace(in.ReasoningEffort)); lvl != "" {
		think := ollama.ThinkLevel(lvl)
		req.Think = &think
	}
	return req, nil
}

// ---------- Outbound: ollama chunks -> OpenAI completion ----------

func newGatewayCompletionID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	return "chatcmpl-" + hex.EncodeToString(buf)
}

// gatewayToolAcc tracks one tool call across streamed chunks, emitting only
// the not-yet-sent argument suffix (upstreams differ: local Ollama sends
// fragments, the external path re-sends accumulated arguments).
type gatewayToolAcc struct {
	id       string
	index    int
	name     string
	sentArgs string
	fullArgs string
	sawName  bool
}

type gatewayStreamState struct {
	id        string
	model     string
	created   int64
	sentRole  bool
	toolIDs   map[int]string
	toolAcc   map[int]*gatewayToolAcc
	toolOrder []int
	promptTok int
	complTok  int
	done      bool
}

func newGatewayStreamState(model string) *gatewayStreamState {
	return &gatewayStreamState{
		id:      newGatewayCompletionID(),
		model:   model,
		created: time.Now().Unix(),
		toolIDs: make(map[int]string),
		toolAcc: make(map[int]*gatewayToolAcc),
	}
}

func (st *gatewayStreamState) toolID(index int) string {
	if id, ok := st.toolIDs[index]; ok {
		return id
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		id := fmt.Sprintf("call_%s_%d", st.id, index)
		st.toolIDs[index] = id
		return id
	}
	id := "call_" + hex.EncodeToString(buf)
	st.toolIDs[index] = id
	return id
}

// deltaToolCalls converts one chunk's tool calls into incremental OpenAI
// deltas, returning (deltas, hasToolCalls).
func (st *gatewayStreamState) deltaToolCalls(calls []ollama.ToolCall) []any {
	var out []any
	for _, tc := range calls {
		idx := tc.Function.Index
		acc, ok := st.toolAcc[idx]
		if !ok {
			acc = &gatewayToolAcc{index: idx, id: st.toolID(idx)}
			st.toolAcc[idx] = acc
			st.toolOrder = append(st.toolOrder, idx)
		}
		current := string(tc.Function.Arguments)
		var newArgs string
		if strings.HasPrefix(current, acc.sentArgs) {
			newArgs = current[len(acc.sentArgs):]
		} else if current != "" {
			// Non-monotonic upstream: treat as a fresh snapshot.
			acc.sentArgs = ""
			acc.fullArgs = ""
			newArgs = current
		}
		fn := map[string]any{}
		if tc.Function.Name != "" && !acc.sawName {
			fn["name"] = tc.Function.Name
			acc.name = tc.Function.Name
			acc.sawName = true
		}
		if newArgs != "" {
			fn["arguments"] = newArgs
			acc.sentArgs += newArgs
			acc.fullArgs += newArgs
		}
		if len(fn) == 0 {
			continue
		}
		out = append(out, map[string]any{
			"index":    idx,
			"id":       acc.id,
			"type":     "function",
			"function": fn,
		})
	}
	return out
}

func (st *gatewayStreamState) hasToolCalls() bool {
	return len(st.toolOrder) > 0
}

// sseChunk serializes one OpenAI streaming chunk (delta may be empty for the
// final chunk carrying finish_reason/usage).
func (st *gatewayStreamState) sseChunk(delta map[string]any, finishReason *string, usage bool) string {
	choice := map[string]any{"index": 0, "delta": delta, "finish_reason": finishReason}
	payload := map[string]any{
		"id":      st.id,
		"object":  "chat.completion.chunk",
		"created": st.created,
		"model":   st.model,
		"choices": []any{choice},
	}
	if usage {
		payload["usage"] = map[string]any{
			"prompt_tokens":     st.promptTok,
			"completion_tokens": st.complTok,
			"total_tokens":      st.promptTok + st.complTok,
		}
	}
	raw, _ := json.Marshal(payload)
	return "data: " + string(raw) + "\n\n"
}

// sseError emits an OpenAI-shaped stream error (no [DONE] follows, per spec).
func (st *gatewayStreamState) sseError(message string) string {
	raw, _ := json.Marshal(map[string]any{"error": map[string]any{
		"message": message,
		"type":    "server_error",
		"code":    "upstream_error",
	}})
	return "data: " + string(raw) + "\n\n"
}

func gatewayFinishReason(doneReason string, hasTools bool) string {
	if hasTools {
		return "tool_calls"
	}
	if doneReason == "length" {
		return "length"
	}
	return "stop"
}

func (s *Server) handleGatewayChatCompletions(w http.ResponseWriter, r *http.Request) {
	var in gatewayChatRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeGatewayError(w, http.StatusBadRequest, "Invalid JSON body", "invalid_request_error", "invalid_body")
		return
	}
	stream := true
	if in.Stream != nil {
		stream = *in.Stream
	}
	req, err := gatewayToChatRequest(in)
	if err != nil {
		writeGatewayError(w, http.StatusBadRequest, err.Error(), "invalid_request_error", "invalid_request")
		return
	}
	exposed, err := s.gatewayExposedModels(r.Context())
	if err != nil {
		writeGatewayError(w, http.StatusBadGateway, err.Error(), "server_error", "upstream_unavailable")
		return
	}
	resolved := gatewayResolveModel(req.Model, exposed)
	if resolved == "" {
		writeGatewayError(w, http.StatusNotFound, fmt.Sprintf("Model %q is not exposed by this gateway", in.Model), "invalid_request_error", "model_not_found")
		return
	}
	req.Model = resolved

	if stream {
		s.serveGatewayStream(w, r, req)
	} else {
		s.serveGatewayOnce(w, r, req)
	}
}

// serveGatewayStream proxies chunks as OpenAI SSE.
func (s *Server) serveGatewayStream(w http.ResponseWriter, r *http.Request, req ollama.ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeGatewayError(w, http.StatusInternalServerError, "Streaming not supported", "server_error", "no_flusher")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	st := newGatewayStreamState(req.Model)
	ctx := r.Context()
	failed := false
	err := s.chatWithModel(ctx, req, func(chunk ollama.ChatChunk) error {
		if chunk.Error != "" {
			failed = true
			_, _ = fmt.Fprint(w, st.sseError(chunk.Error))
			flusher.Flush()
			return fmt.Errorf("%s", chunk.Error)
		}
		if chunk.PromptEvalCount > 0 {
			st.promptTok = chunk.PromptEvalCount
		}
		if chunk.EvalCount > 0 {
			st.complTok = chunk.EvalCount
		}
		if chunk.Done {
			return nil
		}
		delta := make(map[string]any)
		if !st.sentRole {
			delta["role"] = "assistant"
			st.sentRole = true
		}
		if chunk.Message.Content != "" {
			delta["content"] = chunk.Message.Content
		}
		if chunk.Message.Thinking != "" {
			delta["reasoning_content"] = chunk.Message.Thinking
		}
		if tc := st.deltaToolCalls(chunk.Message.ToolCalls); len(tc) > 0 {
			delta["tool_calls"] = tc
		}
		if len(delta) == 0 {
			return nil
		}
		_, _ = fmt.Fprint(w, st.sseChunk(delta, nil, false))
		flusher.Flush()
		// Abort early if the client went away.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	})
	if err != nil && !failed {
		_, _ = fmt.Fprint(w, st.sseError(err.Error()))
		flusher.Flush()
		return
	}
	if failed {
		return
	}
	finish := gatewayFinishReason("", st.hasToolCalls())
	_, _ = fmt.Fprint(w, st.sseChunk(map[string]any{}, &finish, true))
	flusher.Flush()
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// serveGatewayOnce aggregates the stream into one OpenAI completion object.
func (s *Server) serveGatewayOnce(w http.ResponseWriter, r *http.Request, req ollama.ChatRequest) {
	st := newGatewayStreamState(req.Model)
	var content, thinking strings.Builder
	doneReason := ""
	failed := ""
	err := s.chatWithModel(r.Context(), req, func(chunk ollama.ChatChunk) error {
		if chunk.Error != "" {
			failed = chunk.Error
			return fmt.Errorf("%s", chunk.Error)
		}
		if chunk.PromptEvalCount > 0 {
			st.promptTok = chunk.PromptEvalCount
		}
		if chunk.EvalCount > 0 {
			st.complTok = chunk.EvalCount
		}
		if chunk.Done {
			if chunk.DoneReason != "" {
				doneReason = chunk.DoneReason
			}
			return nil
		}
		content.WriteString(chunk.Message.Content)
		thinking.WriteString(chunk.Message.Thinking)
		st.deltaToolCalls(chunk.Message.ToolCalls)
		return nil
	})
	if err != nil {
		msg := err.Error()
		if failed != "" {
			msg = failed
		}
		writeGatewayError(w, http.StatusBadGateway, msg, "server_error", "upstream_error")
		return
	}
	toolCalls := make([]any, 0)
	for _, idx := range st.toolOrder {
		acc := st.toolAcc[idx]
		toolCalls = append(toolCalls, map[string]any{
			"id":    acc.id,
			"type":  "function",
			"index": idx,
			"function": map[string]any{
				"name":      acc.name,
				"arguments": acc.fullArgs,
			},
		})
	}
	finish := gatewayFinishReason(doneReason, len(toolCalls) > 0)
	message := map[string]any{"role": "assistant", "content": content.String()}
	if thinking.Len() > 0 {
		message["reasoning_content"] = thinking.String()
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      st.id,
		"object":  "chat.completion",
		"created": st.created,
		"model":   req.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": finish,
		}},
		"usage": map[string]any{
			"prompt_tokens":     st.promptTok,
			"completion_tokens": st.complTok,
			"total_tokens":      st.promptTok + st.complTok,
		},
	})
}
