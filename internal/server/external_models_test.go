package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gense/ollama-manager/internal/ollama"
)

func TestExternalModelsStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "external_models.json")

	store := newExternalModelsStore(path)
	if err := store.Load(); err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}

	if store.IsExternal("my-model") {
		t.Errorf("expected false for nonexistent model")
	}

	err := store.Register("my-model", "http://localhost:8000/v1", "secret-key", []string{"completion", "vision"})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	if !store.IsExternal("my-model") {
		t.Errorf("expected true for registered model")
	}
	if !store.IsExternal("my-model:latest") {
		t.Errorf("expected true for registered model with :latest suffix")
	}

	rec, ok := store.Get("my-model")
	if !ok || rec.URL != "http://localhost:8000/v1" || rec.APIKey != "secret-key" {
		t.Errorf("get returned invalid record: %+v", rec)
	}

	// Test reload from disk
	store2 := newExternalModelsStore(path)
	if err := store2.Load(); err != nil {
		t.Fatalf("load store2 failed: %v", err)
	}
	if !store2.IsExternal("my-model") {
		t.Errorf("store2 did not retain registered model")
	}

	// Test unregister
	if err := store.Unregister("my-model"); err != nil {
		t.Fatalf("unregister failed: %v", err)
	}
	if store.IsExternal("my-model") {
		t.Errorf("model still present after unregister")
	}
}

func TestProbeExternalModel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		var req openAIChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		// If vision probe
		isVision := false
		for _, m := range req.Messages {
			if parts, ok := m.Content.([]any); ok {
				for _, p := range parts {
					if pMap, ok2 := p.(map[string]any); ok2 && pMap["type"] == "image_url" {
						isVision = true
					}
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if isVision {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"I see a tiny dot"}}]}`))
			return
		}

		// Standard probe response with thinking
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"pong","reasoning_content":"thought..."}}]}`))
	}))
	defer ts.Close()

	ctx := context.Background()
	res, err := ProbeExternalModel(ctx, ts.URL, "test-key", "test-model")
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}

	if !res.Connected {
		t.Errorf("expected connected=true")
	}
	if !res.Thinking {
		t.Errorf("expected thinking=true")
	}
	if !res.Vision {
		t.Errorf("expected vision=true")
	}
}

func TestChatExternalStreaming(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		// Chunk 1: Thinking delta
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"thinking step 1\"}}]}\n\n")
		flusher.Flush()

		// Chunk 2: Content delta
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello from external!\"}}]}\n\n")
		flusher.Flush()

		// Chunk 3: Done
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer ts.Close()

	srv := &Server{
		externalModels: newExternalModelsStore(""),
	}
	_ = srv.externalModels.Register("ext-model", ts.URL, "api-key", []string{"completion", "thinking"})

	var chunks []ollama.ChatChunk
	err := srv.chatWithModel(context.Background(), ollama.ChatRequest{
		Model: "ext-model",
		Messages: []ollama.ChatMessage{
			{Role: "user", Content: "Hello"},
		},
	}, func(c ollama.ChatChunk) error {
		chunks = append(chunks, c)
		return nil
	})

	if err != nil {
		t.Fatalf("chatWithModel error: %v", err)
	}

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	if chunks[0].Message.Thinking != "thinking step 1" {
		t.Errorf("chunk 0 thinking mismatch: got %q", chunks[0].Message.Thinking)
	}
	if chunks[1].Message.Content != "Hello from external!" {
		t.Errorf("chunk 1 content mismatch: got %q", chunks[1].Message.Content)
	}
	if !chunks[len(chunks)-1].Done {
		t.Errorf("last chunk expected done=true")
	}
}

func TestChatExternalThinkingLevels(t *testing.T) {
	var receivedReq openAIChatRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedReq)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer ts.Close()

	srv := &Server{
		externalModels: newExternalModelsStore(""),
	}
	_ = srv.externalModels.Register("ext-thinking-model", ts.URL, "api-key", []string{"completion", "thinking"})

	cases := []struct {
		thinkLevel   ollama.ThinkLevel
		wantEnabled  *bool
		wantReason   string
		wantKwargsOn bool
	}{
		{
			thinkLevel:   "off",
			wantEnabled:  func() *bool { b := false; return &b }(),
			wantReason:   "none",
			wantKwargsOn: false,
		},
		{
			thinkLevel:   "low",
			wantEnabled:  func() *bool { b := true; return &b }(),
			wantReason:   "low",
			wantKwargsOn: true,
		},
		{
			thinkLevel:   "medium",
			wantEnabled:  func() *bool { b := true; return &b }(),
			wantReason:   "medium",
			wantKwargsOn: true,
		},
		{
			thinkLevel:   "high",
			wantEnabled:  func() *bool { b := true; return &b }(),
			wantReason:   "high",
			wantKwargsOn: true,
		},
		{
			thinkLevel:   "max",
			wantEnabled:  func() *bool { b := true; return &b }(),
			wantReason:   "high",
			wantKwargsOn: true,
		},
	}

	for _, tc := range cases {
		lvl := tc.thinkLevel
		err := srv.chatWithModel(context.Background(), ollama.ChatRequest{
			Model: "ext-thinking-model",
			Think: &lvl,
			Messages: []ollama.ChatMessage{
				{Role: "user", Content: "Hi"},
			},
		}, func(c ollama.ChatChunk) error { return nil })

		if err != nil {
			t.Fatalf("level %s failed: %v", tc.thinkLevel, err)
		}

		if receivedReq.EnableThinking == nil || *receivedReq.EnableThinking != *tc.wantEnabled {
			t.Errorf("level %s: enable_thinking = %v, want %v", tc.thinkLevel, receivedReq.EnableThinking, *tc.wantEnabled)
		}
		if receivedReq.ReasoningEffort != tc.wantReason {
			t.Errorf("level %s: reasoning_effort = %q, want %q", tc.thinkLevel, receivedReq.ReasoningEffort, tc.wantReason)
		}
		if receivedReq.ChatTemplateKwargs == nil {
			t.Errorf("level %s: missing chat_template_kwargs", tc.thinkLevel)
		} else if receivedReq.ChatTemplateKwargs["enable_thinking"] != tc.wantKwargsOn {
			t.Errorf("level %s: chat_template_kwargs.enable_thinking = %v, want %v", tc.thinkLevel, receivedReq.ChatTemplateKwargs["enable_thinking"], tc.wantKwargsOn)
		}
	}
}
