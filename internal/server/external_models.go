package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
)

// ExternalModelRecord stores metadata and configuration for a remote/external
// model served via an OpenAI-compatible API (e.g., oMLX, vLLM, LM Studio).
type ExternalModelRecord struct {
	Name         string    `json:"name"`
	URL          string    `json:"url"`
	APIKey       string    `json:"api_key,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type externalModelsFile struct {
	Models map[string]ExternalModelRecord `json:"models"`
}

type externalModelsStore struct {
	path   string
	mu     sync.RWMutex
	models map[string]ExternalModelRecord
}

func newExternalModelsStore(path string) *externalModelsStore {
	return &externalModelsStore{
		path:   path,
		models: make(map[string]ExternalModelRecord),
	}
}

func (s *externalModelsStore) Load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file externalModelsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if file.Models != nil {
		s.models = file.Models
	} else {
		s.models = make(map[string]ExternalModelRecord)
	}
	return nil
}

func (s *externalModelsStore) IsExternal(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.models[name]; ok {
		return true
	}
	if strings.HasSuffix(name, ":latest") {
		if _, ok := s.models[strings.TrimSuffix(name, ":latest")]; ok {
			return true
		}
	} else {
		if _, ok := s.models[name+":latest"]; ok {
			return true
		}
	}
	return false
}

func (s *externalModelsStore) Get(name string) (ExternalModelRecord, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ExternalModelRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if rec, ok := s.models[name]; ok {
		return rec, true
	}
	if strings.HasSuffix(name, ":latest") {
		if rec, ok := s.models[strings.TrimSuffix(name, ":latest")]; ok {
			return rec, true
		}
	} else {
		if rec, ok := s.models[name+":latest"]; ok {
			return rec, true
		}
	}
	return ExternalModelRecord{}, false
}

func (s *externalModelsStore) Register(name, targetURL, apiKey string, capabilities []string) error {
	name = strings.TrimSpace(name)
	targetURL = strings.TrimSpace(targetURL)
	apiKey = strings.TrimSpace(apiKey)
	if name == "" {
		return errors.New("missing model name")
	}
	if targetURL == "" {
		return errors.New("missing model endpoint URL")
	}

	if len(capabilities) == 0 {
		capabilities = []string{"completion", "tools", "thinking", "vision"}
	}

	s.mu.Lock()
	rec := s.models[name]
	rec.Name = name
	rec.URL = targetURL
	rec.APIKey = apiKey
	rec.Capabilities = capabilities
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	s.models[name] = rec
	s.mu.Unlock()

	return s.save()
}

func (s *externalModelsStore) Unregister(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	s.mu.Lock()
	existed := false
	if _, ok := s.models[name]; ok {
		delete(s.models, name)
		existed = true
	}
	if strings.HasSuffix(name, ":latest") {
		trimmed := strings.TrimSuffix(name, ":latest")
		if _, ok := s.models[trimmed]; ok {
			delete(s.models, trimmed)
			existed = true
		}
	} else {
		withLatest := name + ":latest"
		if _, ok := s.models[withLatest]; ok {
			delete(s.models, withLatest)
			existed = true
		}
	}
	s.mu.Unlock()

	if !existed {
		return nil
	}
	return s.save()
}

func (s *externalModelsStore) All() map[string]ExternalModelRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]ExternalModelRecord, len(s.models))
	for k, v := range s.models {
		res[k] = v
	}
	return res
}

func (s *externalModelsStore) save() error {
	if s.path == "" {
		return nil
	}
	s.mu.RLock()
	file := externalModelsFile{
		Models: make(map[string]ExternalModelRecord, len(s.models)),
	}
	for k, v := range s.models {
		file.Models[k] = v
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ---------- OpenAI API Protocol Types ----------

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Stream      bool            `json:"stream"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Tools       any             `json:"tools,omitempty"`
}

type openAIMessage struct {
	Role             string           `json:"role"`
	Content          any              `json:"content"` // string or []openAIContentPart
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
}

type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

type openAIImageURL struct {
	URL string `json:"url"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type openAIToolCall struct {
	Index    int                `json:"index,omitempty"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIChatCompletionChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string           `json:"role"`
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content"`
			Reasoning        string           `json:"reasoning"`
			ToolCalls        []openAIToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openAIChatCompletionResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Role             string           `json:"role"`
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content"`
			ToolCalls        []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

func normalizeOpenAIEndpoint(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	rawURL = strings.TrimRight(rawURL, "/")
	if strings.HasSuffix(rawURL, "/chat/completions") {
		return rawURL
	}
	if strings.HasSuffix(rawURL, "/v1") {
		return rawURL + "/chat/completions"
	}
	if strings.Contains(rawURL, "/v1/") {
		return rawURL + "/chat/completions"
	}
	return rawURL + "/v1/chat/completions"
}

// ---------- Capability Probing ----------

// ExternalModelProbeResult details the test and detected capabilities.
type ExternalModelProbeResult struct {
	Connected    bool     `json:"connected"`
	Vision       bool     `json:"vision"`
	Tools        bool     `json:"tools"`
	Thinking     bool     `json:"thinking"`
	Capabilities []string `json:"capabilities"`
	Model        string   `json:"model"`
	LatencyMs    int64    `json:"latency_ms"`
	Message      string   `json:"message,omitempty"`
}

// ProbeExternalModel runs lightweight test probes against an OpenAI-compatible endpoint.
func ProbeExternalModel(ctx context.Context, targetURL, apiKey, modelName string) (*ExternalModelProbeResult, error) {
	targetURL = strings.TrimSpace(targetURL)
	apiKey = strings.TrimSpace(apiKey)
	modelName = strings.TrimSpace(modelName)
	if targetURL == "" {
		return nil, errors.New("URL de endpoint requerida")
	}
	if modelName == "" {
		return nil, errors.New("Nombre del modelo requerido")
	}

	endpoint := normalizeOpenAIEndpoint(targetURL)
	httpClient := &http.Client{Timeout: 12 * time.Second}

	start := time.Now()

	// 1. Basic completion probe
	baseReq := openAIChatRequest{
		Model: modelName,
		Messages: []openAIMessage{
			{Role: "user", Content: "ping"},
		},
		Stream: false,
		MaxTokens: func() *int {
			v := 4
			return &v
		}(),
	}

	bodyBytes, err := json.Marshal(baseReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		// Fallback: If normalized /v1/chat/completions failed, try without /v1/ prefix
		if strings.HasSuffix(endpoint, "/v1/chat/completions") {
			altEndpoint := strings.TrimSuffix(endpoint, "/v1/chat/completions") + "/chat/completions"
			req2, err2 := http.NewRequestWithContext(ctx, http.MethodPost, altEndpoint, bytes.NewReader(bodyBytes))
			if err2 == nil {
				req2.Header.Set("Content-Type", "application/json")
				if apiKey != "" {
					req2.Header.Set("Authorization", "Bearer "+apiKey)
				}
				if resp2, err3 := httpClient.Do(req2); err3 == nil {
					resp = resp2
					endpoint = altEndpoint
					err = nil
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("no se pudo conectar con %s: %w", targetURL, err)
		}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errObj openAIChatCompletionResponse
		_ = json.Unmarshal(respBody, &errObj)
		if errObj.Error != nil && errObj.Error.Message != "" {
			return nil, fmt.Errorf("error del servidor (%d): %s", resp.StatusCode, errObj.Error.Message)
		}
		return nil, fmt.Errorf("error del servidor (código HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var parsedResp openAIChatCompletionResponse
	_ = json.Unmarshal(respBody, &parsedResp)

	latency := time.Since(start).Milliseconds()
	caps := []string{"completion"}
	connected := true
	thinking := false

	// Check if reasoning was returned
	if len(parsedResp.Choices) > 0 {
		msg := parsedResp.Choices[0].Message
		if msg.ReasoningContent != "" || strings.Contains(msg.Content, "<think>") {
			thinking = true
			caps = append(caps, "thinking")
		}
	}
	lowerName := strings.ToLower(modelName)
	if !thinking && (strings.Contains(lowerName, "r1") || strings.Contains(lowerName, "thinking") || strings.Contains(lowerName, "deepseek-r1")) {
		thinking = true
		caps = append(caps, "thinking")
	}

	// 2. Vision probe (1x1 transparent PNG)
	vision := false
	const tinyPngBase64 = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAA="
	visionReq := openAIChatRequest{
		Model: modelName,
		Messages: []openAIMessage{
			{
				Role: "user",
				Content: []openAIContentPart{
					{Type: "text", Text: "describe"},
					{Type: "image_url", ImageURL: &openAIImageURL{URL: tinyPngBase64}},
				},
			},
		},
		Stream: false,
		MaxTokens: func() *int {
			v := 2
			return &v
		}(),
	}
	if vBytes, vErr := json.Marshal(visionReq); vErr == nil {
		if vHttpReq, vHttpErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(vBytes)); vHttpErr == nil {
			vHttpReq.Header.Set("Content-Type", "application/json")
			if apiKey != "" {
				vHttpReq.Header.Set("Authorization", "Bearer "+apiKey)
			}
			vClient := &http.Client{Timeout: 6 * time.Second}
			if vResp, vDoErr := vClient.Do(vHttpReq); vDoErr == nil {
				defer vResp.Body.Close()
				if vResp.StatusCode == http.StatusOK {
					vision = true
					caps = append(caps, "vision")
				}
			}
		}
	}
	if !vision && (strings.Contains(lowerName, "vision") || strings.Contains(lowerName, "vl") || strings.Contains(lowerName, "omni") || strings.Contains(lowerName, "gpt-4o")) {
		vision = true
		caps = append(caps, "vision")
	}

	// 3. Tools probe
	tools := false
	toolsReq := openAIChatRequest{
		Model: modelName,
		Messages: []openAIMessage{
			{Role: "user", Content: "use tool"},
		},
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name:        "probe_test",
					Description: "A test function",
					Parameters:  map[string]any{"type": "object", "properties": map[string]any{"arg": map[string]any{"type": "string"}}},
				},
			},
		},
		Stream: false,
		MaxTokens: func() *int {
			v := 2
			return &v
		}(),
	}
	if tBytes, tErr := json.Marshal(toolsReq); tErr == nil {
		if tHttpReq, tHttpErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(tBytes)); tHttpErr == nil {
			tHttpReq.Header.Set("Content-Type", "application/json")
			if apiKey != "" {
				tHttpReq.Header.Set("Authorization", "Bearer "+apiKey)
			}
			tClient := &http.Client{Timeout: 6 * time.Second}
			if tResp, tDoErr := tClient.Do(tHttpReq); tDoErr == nil {
				defer tResp.Body.Close()
				if tResp.StatusCode == http.StatusOK {
					tools = true
					caps = append(caps, "tools")
				}
			}
		}
	}
	// Fallback for tools: default include tools unless explicitly rejected
	if !tools {
		tools = true
		caps = append(caps, "tools")
	}

	return &ExternalModelProbeResult{
		Connected:    connected,
		Vision:       vision,
		Tools:        tools,
		Thinking:     thinking,
		Capabilities: caps,
		Model:        modelName,
		LatencyMs:    latency,
	}, nil
}

// ---------- Streaming Chat Client ----------

func (s *Server) chatExternal(ctx context.Context, ext ExternalModelRecord, req ollama.ChatRequest, onChunk func(ollama.ChatChunk) error) error {
	endpoint := normalizeOpenAIEndpoint(ext.URL)

	// Translate messages
	var openAIMsgs []openAIMessage
	for _, m := range req.Messages {
		var content any = m.Content
		if len(m.Images) > 0 {
			var parts []openAIContentPart
			if m.Content != "" {
				parts = append(parts, openAIContentPart{Type: "text", Text: m.Content})
			}
			for _, img := range m.Images {
				dataURI := img
				if !strings.HasPrefix(dataURI, "data:") {
					dataURI = "data:image/png;base64," + img
				}
				parts = append(parts, openAIContentPart{
					Type:     "image_url",
					ImageURL: &openAIImageURL{URL: dataURI},
				})
			}
			content = parts
		}

		var toolCalls []openAIToolCall
		for idx, tc := range m.ToolCalls {
			toolCalls = append(toolCalls, openAIToolCall{
				Index: idx,
				ID:    tc.Function.Name + fmt.Sprintf("_%d", idx),
				Type:  "function",
				Function: openAIFunctionCall{
					Name:      tc.Function.Name,
					Arguments: string(tc.Function.Arguments),
				},
			})
		}

		openAIMsgs = append(openAIMsgs, openAIMessage{
			Role:             m.Role,
			Content:          content,
			ReasoningContent: m.Thinking,
			ToolCalls:        toolCalls,
		})
	}

	payload := openAIChatRequest{
		Model:    ext.Name,
		Messages: openAIMsgs,
		Stream:   true,
		Tools:    req.Tools,
	}

	if req.Options != nil {
		if v, ok := req.Options["temperature"].(float64); ok {
			payload.Temperature = &v
		}
		if v, ok := req.Options["top_p"].(float64); ok {
			payload.TopP = &v
		}
		if v, ok := req.Options["num_predict"].(float64); ok && v > 0 {
			vi := int(v)
			payload.MaxTokens = &vi
		} else if v, ok := req.Options["max_tokens"].(float64); ok && v > 0 {
			vi := int(v)
			payload.MaxTokens = &vi
		}
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if ext.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+ext.APIKey)
	}

	httpClient := &http.Client{}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		// Try without /v1/ fallback
		if strings.HasSuffix(endpoint, "/v1/chat/completions") {
			altEndpoint := strings.TrimSuffix(endpoint, "/v1/chat/completions") + "/chat/completions"
			req2, err2 := http.NewRequestWithContext(ctx, http.MethodPost, altEndpoint, bytes.NewReader(bodyBytes))
			if err2 == nil {
				req2.Header.Set("Content-Type", "application/json")
				req2.Header.Set("Accept", "text/event-stream")
				if ext.APIKey != "" {
					req2.Header.Set("Authorization", "Bearer "+ext.APIKey)
				}
				if resp2, err3 := httpClient.Do(req2); err3 == nil {
					resp = resp2
					err = nil
				}
			}
		}
		if err != nil {
			return fmt.Errorf("error al conectar con modelo externo (%s): %w", ext.URL, err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBytes, _ := io.ReadAll(resp.Body)
		var errObj openAIChatCompletionResponse
		_ = json.Unmarshal(errBytes, &errObj)
		if errObj.Error != nil && errObj.Error.Message != "" {
			return fmt.Errorf("error del modelo externo (%d): %s", resp.StatusCode, errObj.Error.Message)
		}
		return fmt.Errorf("error del modelo externo (HTTP %d): %s", resp.StatusCode, string(errBytes))
	}

	// Tool calls aggregation accumulator
	toolCallsAcc := make(map[int]*ollama.ToolCall)

	scanner := bufio.NewScanner(resp.Body)
	// Larger buffer for large completions or image responses
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var lastUsagePromptTokens, lastUsageCompletionTokens int

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk openAIChatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			lastUsagePromptTokens = chunk.Usage.PromptTokens
			lastUsageCompletionTokens = chunk.Usage.CompletionTokens
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		delta := choice.Delta

		// Parse reasoning delta
		thinkingDelta := delta.ReasoningContent
		if thinkingDelta == "" {
			thinkingDelta = delta.Reasoning
		}

		// Parse tool calls delta
		var emittedToolCalls []ollama.ToolCall
		for _, tc := range delta.ToolCalls {
			idx := tc.Index
			acc, exists := toolCallsAcc[idx]
			if !exists {
				acc = &ollama.ToolCall{
					Type: "function",
				}
				acc.Function.Name = tc.Function.Name
				acc.Function.Index = idx
				toolCallsAcc[idx] = acc
			}
			if tc.Function.Name != "" {
				acc.Function.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				acc.Function.Arguments = append(acc.Function.Arguments, []byte(tc.Function.Arguments)...)
			}
			emittedToolCalls = append(emittedToolCalls, *acc)
		}

		outChunk := ollama.ChatChunk{
			Model:     ext.Name,
			CreatedAt: time.Now().UTC(),
			Done:      false,
			Message: ollama.ChatMessage{
				Role:      "assistant",
				Content:   delta.Content,
				Thinking:  thinkingDelta,
				ToolCalls: emittedToolCalls,
			},
		}

		if choice.FinishReason != nil && *choice.FinishReason != "" {
			outChunk.DoneReason = *choice.FinishReason
		}

		if err := onChunk(outChunk); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("error leyendo stream: %w", err)
	}

	// Emit final done chunk
	finalChunk := ollama.ChatChunk{
		Model:           ext.Name,
		CreatedAt:       time.Now().UTC(),
		Done:            true,
		PromptEvalCount: lastUsagePromptTokens,
		EvalCount:       lastUsageCompletionTokens,
	}

	return onChunk(finalChunk)
}

// chatWithModel dispatches a chat request to either an external model or Ollama.
func (s *Server) chatWithModel(ctx context.Context, req ollama.ChatRequest, onChunk func(ollama.ChatChunk) error) error {
	if s.externalModels != nil && s.externalModels.IsExternal(req.Model) {
		rec, _ := s.externalModels.Get(req.Model)
		return s.chatExternal(ctx, rec, req, onChunk)
	}
	return s.ollama.Chat(ctx, req, onChunk)
}

// cleanURLDisplay removes credentials/secrets for safe UI display.
func cleanURLDisplay(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = nil
	return u.String()
}
