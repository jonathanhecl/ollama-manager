package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

	err := store.Register("my-model", "http://localhost:8000/v1", "secret-key", []string{"completion", "vision"}, false)
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
	if !ok || rec.URL != "http://localhost:8000/v1" || rec.APIKey != "secret-key" || rec.Disabled {
		t.Errorf("get returned invalid record: %+v", rec)
	}

	// Test toggle disabled / paused
	disabled, err := store.ToggleDisabled("my-model")
	if err != nil || !disabled {
		t.Errorf("expected disabled=true, got disabled=%v, err=%v", disabled, err)
	}
	rec, _ = store.Get("my-model")
	if !rec.Disabled {
		t.Errorf("record should be disabled")
	}
	disabled, err = store.ToggleDisabled("my-model")
	if err != nil || disabled {
		t.Errorf("expected disabled=false after 2nd toggle, got disabled=%v, err=%v", disabled, err)
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
	_ = srv.externalModels.Register("ext-model", ts.URL, "api-key", []string{"completion", "thinking"}, false)

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
	_ = srv.externalModels.Register("ext-thinking-model", ts.URL, "api-key", []string{"completion", "thinking"}, false)

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

func TestExternalSameNameCoexistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "external_models.json")
	store := newExternalModelsStore(path)

	r1, err := store.Upsert("", "shared-model", "http://a.example/v1", "key-a", nil, false, "oMLX")
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	r2, err := store.Upsert("", "shared-model", "http://b.example/v1", "key-b", nil, false, "vLLM")
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if r1.ID == r2.ID {
		t.Fatalf("expected distinct IDs, both got %q", r1.ID)
	}
	if r1.ID != "shared-model" {
		t.Fatalf("first entry should keep raw name ID, got %q", r1.ID)
	}
	if !strings.HasPrefix(r2.ID, "shared-model@ext-") {
		t.Fatalf("second entry expected hashed ID, got %q", r2.ID)
	}
	all := store.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}

	store2 := newExternalModelsStore(path)
	if err := store2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got1, ok := store2.Get(r1.ID)
	if !ok || got1.ID != r1.ID || got1.Name != "shared-model" || got1.Provider != "oMLX" || got1.APIKey != "key-a" {
		t.Fatalf("reloaded entry 1 mismatch: %+v", got1)
	}
	got2, ok := store2.Get(r2.ID)
	if !ok || got2.ID != r2.ID || got2.Provider != "vLLM" || got2.APIKey != "key-b" {
		t.Fatalf("reloaded entry 2 mismatch: %+v", got2)
	}
}

func TestExternalPairUpdateSameNormalizedURL(t *testing.T) {
	store := newExternalModelsStore("")
	r1, err := store.Upsert("", "m", "http://a.example/v1/", "k1", nil, false, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Trailing slash difference: same normalized endpoint -> same entry updated.
	r2, err := store.Upsert("", "m", "http://a.example/v1", "k2", nil, false, "")
	if err != nil {
		t.Fatalf("upsert same pair: %v", err)
	}
	if r2.ID != r1.ID {
		t.Fatalf("expected same ID %q, got %q", r1.ID, r2.ID)
	}
	if len(store.All()) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(store.All()))
	}
	rec, _ := store.Get(r1.ID)
	if rec.APIKey != "k2" {
		t.Fatalf("expected updated key k2, got %q", rec.APIKey)
	}
}

func TestExternalCredentialIsolation(t *testing.T) {
	store := newExternalModelsStore("")
	a, _ := store.Upsert("", "m", "http://a.example/v1", "key-a", nil, false, "")
	b, _ := store.Upsert("", "m", "http://b.example/v1", "key-b", nil, false, "")
	// Blank key on B's edit must inherit from B only, never from A.
	if _, err := store.Upsert(b.ID, "m-renamed", "http://b.example/v1", "", nil, false, ""); err != nil {
		t.Fatalf("edit B: %v", err)
	}
	recB, _ := store.Get(b.ID)
	if recB.APIKey != "key-b" {
		t.Fatalf("B key corrupted: %q", recB.APIKey)
	}
	recA, _ := store.Get(a.ID)
	if recA.APIKey != "key-a" || recA.Name != "m" {
		t.Fatalf("A must be untouched: %+v", recA)
	}
	// Masked placeholder on a brand-new entry is not a real key.
	c, err := store.Upsert("", "new", "http://c.example/v1", maskedAPIKey, nil, false, "")
	if err != nil {
		t.Fatalf("upsert new: %v", err)
	}
	if c.APIKey != "" {
		t.Fatalf("masked key must not persist as a real key, got %q", c.APIKey)
	}
}

func TestExternalToggleDeleteIndependent(t *testing.T) {
	store := newExternalModelsStore("")
	a, _ := store.Upsert("", "m", "http://a.example/v1", "ka", nil, false, "")
	b, _ := store.Upsert("", "m", "http://b.example/v1", "kb", nil, false, "")
	if _, err := store.ToggleDisabled(a.ID); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	recA, _ := store.Get(a.ID)
	recB, _ := store.Get(b.ID)
	if !recA.Disabled || recB.Disabled {
		t.Fatalf("toggle hit wrong entry: A=%+v B=%+v", recA, recB)
	}
	if err := store.Unregister(a.ID); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if store.IsExternal(a.ID) {
		t.Fatalf("A should be gone")
	}
	if !store.IsExternal(b.ID) {
		t.Fatalf("B must remain")
	}
}

func TestExternalEditPreservesIDAndConflicts(t *testing.T) {
	store := newExternalModelsStore("")
	a, _ := store.Upsert("", "m", "http://a.example/v1", "ka", nil, false, "")
	b, _ := store.Upsert("", "m", "http://b.example/v1", "kb", nil, false, "")
	createdA := a.CreatedAt

	// Rename + endpoint change on B keeps its ID.
	b2, err := store.Upsert(b.ID, "other-name", "http://c.example/v1", "", nil, false, "")
	if err != nil {
		t.Fatalf("edit B: %v", err)
	}
	if b2.ID != b.ID {
		t.Fatalf("ID must be preserved, got %q want %q", b2.ID, b.ID)
	}
	if b2.APIKey != "kb" {
		t.Fatalf("key must be inherited from target entry, got %q", b2.APIKey)
	}

	// Edit B into A's pair -> conflict, no mutation.
	if _, err := store.Upsert(b.ID, "m", "http://a.example/v1", "", nil, false, ""); !errors.Is(err, errExternalPairConflict) {
		t.Fatalf("expected pair conflict, got %v", err)
	}
	if len(store.All()) != 2 {
		t.Fatalf("conflict must not remove entries, got %d", len(store.All()))
	}
	if _, err := store.Upsert("no-such-id", "x", "http://x/v1", "", nil, false, ""); !errors.Is(err, errExternalNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	recA, _ := store.Get(a.ID)
	if recA.CreatedAt != createdA {
		t.Fatalf("CreatedAt changed: %v -> %v", createdA, recA.CreatedAt)
	}
}

func TestExternalLegacyLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "external_models.json")
	legacy := `{"models":{"old-model":{"name":"old-model","url":"http://x/v1","api_key":"k","created_at":"2024-01-01T00:00:00Z"}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newExternalModelsStore(path)
	if err := store.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	rec, ok := store.Get("old-model")
	if !ok || rec.ID != "old-model" {
		t.Fatalf("legacy ID must come from map key: %+v", rec)
	}
}

func openAIModelsServer(t *testing.T, ownedBy any, extra map[string]any, auth string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth != "" && r.Header.Get("Authorization") != "Bearer "+auth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			entry := map[string]any{"id": "m"}
			if ownedBy != nil {
				entry["owned_by"] = ownedBy
			}
			for k, v := range extra {
				entry[k] = v
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{entry}})
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
}

func TestDetectExternalProvider(t *testing.T) {
	ctx := context.Background()
	endpointOf := func(ts *httptest.Server) string {
		return normalizeOpenAIEndpoint(ts.URL)
	}

	ts := openAIModelsServer(t, "omlx", nil, "secret")
	defer ts.Close()
	if got := detectExternalProvider(ctx, endpointOf(ts), "secret", "m"); got != "oMLX" {
		t.Fatalf("omlx canonicalization failed: %q", got)
	}
	if got := detectExternalProvider(ctx, endpointOf(ts), "wrong-key", "m"); got != "" {
		t.Fatalf("auth failure must yield empty, got %q", got)
	}

	for _, ownedBy := range []any{"system", " SYSTEM ", "", nil, 42} {
		ts := openAIModelsServer(t, ownedBy, nil, "")
		if got := detectExternalProvider(ctx, endpointOf(ts), "", "m"); got != "" {
			t.Fatalf("owned_by=%v must yield empty provider, got %q", ownedBy, got)
		}
		ts.Close()
	}

	ts = openAIModelsServer(t, "vLLM", nil, "")
	defer ts.Close()
	if got := detectExternalProvider(ctx, endpointOf(ts), "", "m"); got != "vLLM" {
		t.Fatalf("provider name must be preserved, got %q", got)
	}
	if got := detectExternalProvider(ctx, endpointOf(ts), "", "different-model"); got != "" {
		t.Fatalf("mismatched model id must yield empty, got %q", got)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{not json"))
	}))
	defer bad.Close()
	if got := detectExternalProvider(ctx, endpointOf(bad), "", "m"); got != "" {
		t.Fatalf("malformed JSON must yield empty, got %q", got)
	}

	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/leak", http.StatusFound)
	}))
	defer redir.Close()
	if got := detectExternalProvider(ctx, endpointOf(redir), "secret", "m"); got != "" {
		t.Fatalf("redirects must not be followed, got %q", got)
	}
}

func TestHandleTestExternalModelUsesStoredKey(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer ts.Close()

	srv := &Server{externalModels: newExternalModelsStore("")}
	rec, _ := srv.externalModels.Upsert("", "m", ts.URL, "real-key", nil, false, "")

	body := fmt.Sprintf(`{"id":%q,"name":"m","url":%q,"api_key":""}`, rec.ID, ts.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/external-models/test", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleTestExternalModel(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if gotAuth != "Bearer real-key" {
		t.Fatalf("probe must use stored key, got %q", gotAuth)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["ok"] != true {
		t.Fatalf("probe failed: %v", resp)
	}
}

func TestExternalHandlersProviderAndKeySafety(t *testing.T) {
	ollamaSrv := fakeOllamaChat(t, nil, nil)
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m", "owned_by": "omlx"}}})
			return
		}
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer ext.Close()

	body := fmt.Sprintf(`{"name":"m","url":%q,"api_key":"sk-live"}`, ext.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/external-models", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleCreateExternalModel(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status %d: %s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	newID, _ := created["id"].(string)
	if newID != "m" {
		t.Fatalf("first entry ID should be raw name, got %q", newID)
	}

	// Second entry with same remote name, different endpoint -> distinct ID.
	body = `{"name":"m","url":"http://other.invalid/v1","api_key":"k2"}`
	req = httptest.NewRequest(http.MethodPost, "/api/external-models", strings.NewReader(body))
	rr = httptest.NewRecorder()
	srv.handleCreateExternalModel(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create 2 status %d: %s", rr.Code, rr.Body.String())
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	id2, _ := created["id"].(string)
	if id2 == "" || id2 == newID {
		t.Fatalf("second entry needs distinct ID, got %q", id2)
	}
	if rn, _ := created["name"].(string); rn != "m" {
		t.Fatalf("remote name must stay m, got %q", rn)
	}

	// Unknown edit ID -> 404.
	body = `{"id":"ghost","name":"m","url":"http://other.invalid/v1"}`
	req = httptest.NewRequest(http.MethodPost, "/api/external-models", strings.NewReader(body))
	rr = httptest.NewRecorder()
	srv.handleCreateExternalModel(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}

	// Edit second entry into first entry's pair -> 409, nothing mutated.
	body = fmt.Sprintf(`{"id":%q,"name":"m","url":%q}`, id2, ext.URL)
	req = httptest.NewRequest(http.MethodPost, "/api/external-models", strings.NewReader(body))
	rr = httptest.NewRecorder()
	srv.handleCreateExternalModel(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(srv.externalModels.All()) != 2 {
		t.Fatalf("conflict must not mutate store")
	}

	// List: provider present, API key masked.
	req = httptest.NewRequest(http.MethodGet, "/api/external-models", nil)
	rr = httptest.NewRecorder()
	srv.handleListExternalModels(rr, req)
	var list struct {
		Models []ExternalModelRecord `json:"models"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &list)
	var first *ExternalModelRecord
	for i := range list.Models {
		if list.Models[i].APIKey == "sk-live" {
			t.Fatalf("API key leaked in list response")
		}
		if list.Models[i].ID == newID {
			first = &list.Models[i]
		}
	}
	if first == nil || first.Provider != "oMLX" {
		t.Fatalf("provider must be exposed in list: %+v", list.Models)
	}
	if first.Name != "m" || first.ID != newID {
		t.Fatalf("list must expose id+remote name: %+v", first)
	}
}

func TestChatExternalDuplicateRouting(t *testing.T) {
	makeExt := func(tag string, gotModel *string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req openAIChatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			*gotModel = req.Model
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", tag)
			fmt.Fprintf(w, "data: [DONE]\n\n")
		}))
	}
	var modelA, modelB string
	srvA := makeExt("resp-A", &modelA)
	defer srvA.Close()
	srvB := makeExt("resp-B", &modelB)
	defer srvB.Close()

	srv := &Server{externalModels: newExternalModelsStore("")}
	a, _ := srv.externalModels.Upsert("", "dup", srvA.URL, "", nil, false, "")
	b, _ := srv.externalModels.Upsert("", "dup", srvB.URL, "", nil, false, "")
	if a.ID == b.ID {
		t.Fatalf("duplicates need distinct IDs")
	}

	chat := func(id string) (string, string) {
		var content, lastModel string
		err := srv.chatWithModel(context.Background(), ollama.ChatRequest{
			Model:    id,
			Messages: []ollama.ChatMessage{{Role: "user", Content: "hi"}},
		}, func(c ollama.ChatChunk) error {
			content += c.Message.Content
			lastModel = c.Model
			return nil
		})
		if err != nil {
			t.Fatalf("chat %s: %v", id, err)
		}
		return content, lastModel
	}

	contentA, routeA := chat(a.ID)
	contentB, routeB := chat(b.ID)
	if modelA != "dup" || modelB != "dup" {
		t.Fatalf("upstream must receive the remote model name: A=%q B=%q", modelA, modelB)
	}
	if contentA != "resp-A" || contentB != "resp-B" {
		t.Fatalf("routing mixed up: A=%q B=%q", contentA, contentB)
	}
	if routeA != a.ID || routeB != b.ID {
		t.Fatalf("stream chunks must carry routing ID: %q %q", routeA, routeB)
	}
}

func TestGatewayExposesDuplicateExternalIDs(t *testing.T) {
	ollamaSrv := fakeOllamaChat(t, nil, nil)
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	a, _ := srv.externalModels.Upsert("", "dup", "http://a.example/v1", "", nil, false, "")
	b, _ := srv.externalModels.Upsert("", "dup", "http://b.example/v1", "", nil, false, "")

	names, err := srv.gatewayExposedModels(context.Background())
	if err != nil {
		t.Fatalf("gatewayExposedModels: %v", err)
	}
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	if !set[a.ID] || !set[b.ID] {
		t.Fatalf("gateway must expose both routing IDs %q %q: %v", a.ID, b.ID, names)
	}

	entries, err := srv.gatewayExposedEntries(context.Background(), nil)
	if err != nil {
		t.Fatalf("gatewayExposedEntries: %v", err)
	}
	var ea, eb *gatewayExposedEntry
	for i := range entries {
		if entries[i].Name == a.ID {
			ea = &entries[i]
		}
		if entries[i].Name == b.ID {
			eb = &entries[i]
		}
	}
	if ea == nil || eb == nil || ea.External == nil || eb.External == nil {
		t.Fatalf("gateway entries must include both externals: %+v", entries)
	}
	if ea.External.Name != "dup" || eb.External.Name != "dup" {
		t.Fatalf("upstream remote name must stay dup: %+v %+v", ea.External, eb.External)
	}
}

func TestExternalModelArtifacts(t *testing.T) {
	srv := &Server{
		externalModels: newExternalModelsStore(""),
	}
	_ = srv.externalModels.Register("qwen38-27b-ablitEXT", "http://localhost:8000/v1", "key", []string{"completion", "tools"}, false)

	ctx := context.Background()
	digest := srv.artifactModelDigest(ctx, "qwen38-27b-ablitEXT")
	if digest != "qwen38-27b-ablitEXT" {
		t.Fatalf("expected digest 'qwen38-27b-ablitEXT', got %q", digest)
	}

	count, bytes := srv.artifactInfoForModel(ctx, "qwen38-27b-ablitEXT")
	if count != 0 || bytes != 0 {
		t.Errorf("expected 0 artifacts initially, got count=%d bytes=%d", count, bytes)
	}
}
