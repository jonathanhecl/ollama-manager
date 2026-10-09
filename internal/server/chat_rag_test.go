package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gense/ollama-manager/internal/ollama"
	ragpkg "github.com/gense/ollama-manager/internal/rag"
)

type fakeOllamaChatRAG struct {
	srv *httptest.Server

	mu         sync.Mutex
	embedVecs  map[string][]float64
	embedErrs  map[string]error
	embedBlock bool
	embedReqs  []map[string]any
	listErr    bool
	tags       []map[string]any
	chatReqs   []ollama.ChatRequest
	chatTool   bool
}

func newFakeOllamaChatRAG() *fakeOllamaChatRAG {
	f := &fakeOllamaChatRAG{
		embedVecs: map[string][]float64{},
		embedErrs: map[string]error{},
		tags: []map[string]any{
			{"name": "embed-model:latest", "digest": "sha256:embed1"},
			{"name": "embed-vision:latest", "digest": "sha256:embedv"},
			{"name": "chat-model:latest", "digest": "sha256:chat1"},
		},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			f.mu.Lock()
			listErr := f.listErr
			tags := f.tags
			f.mu.Unlock()
			if listErr {
				http.Error(w, "tags boom", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"models": tags})
		case "/api/ps":
			writeJSON(w, http.StatusOK, map[string]any{"models": []any{}})
		case "/api/show":
			var req struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			caps := map[string][]string{
				"embed-model:latest":  {"embedding"},
				"embed-vision:latest": {"embedding", "vision"},
				"chat-model:latest":   {"completion", "tools"},
			}
			c, ok := caps[req.Name]
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"capabilities": c,
				"details":      map[string]any{"format": "safetensors"},
				"model_info":   map[string]any{},
			})
		case "/api/embed":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			model, _ := req["model"].(string)
			f.mu.Lock()
			block := f.embedBlock
			f.mu.Unlock()
			if block {
				<-r.Context().Done()
				return
			}
			f.mu.Lock()
			f.embedReqs = append(f.embedReqs, req)
			vec := f.embedVecs[model]
			eErr := f.embedErrs[model]
			f.mu.Unlock()
			if eErr != nil {
				http.Error(w, eErr.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"embedding": vec})
		case "/api/chat":
			var req ollama.ChatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.mu.Lock()
			f.chatReqs = append(f.chatReqs, req)
			call := len(f.chatReqs)
			wantTool := f.chatTool && call == 1
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/x-ndjson")
			if wantTool {
				_, _ = w.Write([]byte(`{"model":"` + req.Model + `","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"bogus_tool","arguments":{}}}]},"done":true,"done_reason":"stop"}` + "\n"))
				return
			}
			_, _ = w.Write([]byte(`{"model":"` + req.Model + `","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop","eval_count":1,"prompt_eval_count":1}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	return f
}

func (f *fakeOllamaChatRAG) Close() { f.srv.Close() }

func (f *fakeOllamaChatRAG) embedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.embedReqs)
}

func (f *fakeOllamaChatRAG) lastChatRequest() ollama.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.chatReqs) == 0 {
		return ollama.ChatRequest{}
	}
	return f.chatReqs[len(f.chatReqs)-1]
}

func createChatRAGBase(t *testing.T, dir, name, model, digest string, dims int, entries []ragpkg.Entry) (ragpkg.Meta, string) {
	t.Helper()
	meta, filename, err := ragpkg.Create(context.Background(), dir, ragpkg.Meta{
		Name: name, EmbeddingModel: model, EmbeddingDigest: digest, Dimensions: dims,
	}, entries)
	if err != nil {
		t.Fatal(err)
	}
	return meta, filename
}

func postChat(t *testing.T, srv *Server, payload map[string]any) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

func TestChatRAGInjectsContext(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	srv.cfgMu.Lock()
	srv.cfg.RAG.DefaultEmbedding = ""
	srv.cfgMu.Unlock()
	dir0, defModel := srv.ragConfigSnapshot()
	if defModel != "" {
		t.Fatalf("default embedding should be blank, got %q", defModel)
	}
	_ = dir0
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}

	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
		{Term: "car", Content: "a car is a vehicle", Embedding: []float64{0, 1}},
	})

	code, sse := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "tell me about apples"}},
		"rag_enabled": true,
		"rag_paths":   []string{filename},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d, body = %s", code, sse)
	}
	req := fake.lastChatRequest()
	var user, sys string
	for _, m := range req.Messages {
		if m.Role == "user" {
			user = m.Content
		}
		if m.Role == "system" && sys == "" {
			sys = m.Content
		}
	}
	if !strings.Contains(user, "Retrieved RAG context (JSON):") {
		t.Fatalf("user message lacks injected context: %q", user)
	}
	if !strings.Contains(user, "apple") || !strings.Contains(user, `"filename":"`+filename+`"`) {
		t.Fatalf("injected context lacks entry or citation id: %q", user)
	}
	if !strings.Contains(sys, "without mentioning RAG, retrieval, reference context, source filenames, entry IDs, or citations unless the user explicitly asks for sources or provenance") {
		t.Fatalf("system instruction missing: %q", sys)
	}
	if strings.Contains(sys, "cite its source as [filename#entry_id]") {
		t.Fatalf("old citation instruction still present: %q", sys)
	}
	if !strings.Contains(sys, "Start with the answer itself.") ||
		!strings.Contains(sys, "'según la información disponible', 'según el contexto', or 'los datos indican'") {
		t.Fatalf("preamble ban missing: %q", sys)
	}
	if strings.Contains(user, "a car is a vehicle") {
		t.Fatalf("orthogonal entry should be below threshold: %q", user)
	}
	if !strings.Contains(sse, "rag_search") {
		t.Fatalf("stream lacks rag_search tool events: %s", sse)
	}
}

func TestChatRAGDisabledSkipsOllama(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})

	code, _ := postChat(t, srv, map[string]any{
		"model":    "chat-model:latest",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	if n := fake.embedCount(); n != 0 {
		t.Fatalf("embeds with rag disabled = %d", n)
	}
	_ = filename
}

func TestChatRAGBelowThresholdSkipsInjection(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{0, 1}
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
	})

	code, sse := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "unrelated question"}},
		"rag_enabled": true,
		"rag_paths":   []string{filename},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	req := fake.lastChatRequest()
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "Retrieved RAG context") {
			t.Fatalf("below-threshold match must not be injected: %q", m.Content)
		}
	}
	if !strings.Contains(sse, "no relevant results") {
		t.Fatalf("stream lacks the no-results preview: %s", sse)
	}
}

func TestChatRAGMissingModelWarnsAndContinues(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Ghost", "ghost-model:latest", "sha256:ghost", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})

	code, sse := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "hi"}},
		"rag_enabled": true,
		"rag_paths":   []string{filename},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d, body = %s", code, sse)
	}
	if !strings.Contains(sse, "event: warning") || !strings.Contains(sse, "rag_unavailable") {
		t.Fatalf("stream lacks warning event: %s", sse)
	}
	if !strings.Contains(sse, `"ok":false`) {
		t.Fatalf("stream lacks tool error: %s", sse)
	}
	if !strings.Contains(sse, `event: done`) || !strings.Contains(sse, `event: chunk`) {
		t.Fatalf("chat did not finish: %s", sse)
	}
	if n := fake.embedCount(); n != 0 {
		t.Fatalf("embed ran without an installed model: %d", n)
	}
}

func TestChatRAGSharedModelEmbedsOnce(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	dir := ragDirOf(t, srv)
	_, f1 := createChatRAGBase(t, dir, "One", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "a", Content: "first base", Embedding: []float64{1, 0}},
	})
	_, f2 := createChatRAGBase(t, dir, "Two", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "b", Content: "second base", Embedding: []float64{1, 0}},
	})

	code, _ := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "hi"}},
		"rag_enabled": true,
		"rag_paths":   []string{f1, f2},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	if n := fake.embedCount(); n != 1 {
		t.Fatalf("two bases sharing a model should embed once, got %d", n)
	}
}

func TestChatRAGIndependentModelsEmbedSeparately(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	fake.embedVecs["embed-vision:latest"] = []float64{1, 0}
	dir := ragDirOf(t, srv)
	_, f1 := createChatRAGBase(t, dir, "One", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "a", Content: "first base", Embedding: []float64{1, 0}},
	})
	_, f2 := createChatRAGBase(t, dir, "Two", "embed-vision:latest", "sha256:embedv", 2, []ragpkg.Entry{
		{Term: "b", Content: "second base", Embedding: []float64{1, 0}},
	})

	code, _ := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "hi"}},
		"rag_enabled": true,
		"rag_paths":   []string{f1, f2},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	if n := fake.embedCount(); n != 2 {
		t.Fatalf("two different models should embed independently, got %d", n)
	}
	var user string
	for _, m := range fake.lastChatRequest().Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	if !strings.Contains(user, "first base") || !strings.Contains(user, "second base") {
		t.Fatalf("each base must be matched against its own dimensions: %q", user)
	}
}

func TestSessionTurnRAGInjectsAndKeepsTranscript(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
	})

	st := srv.chatSessions
	sess := st.Create("chat-model:latest", SessionSettings{
		RAGEnabled: true, RAGPaths: []string{filename}, NumCtxPct: 100,
	})
	if !st.AppendUser(sess.ID, "tell me about apples", nil) {
		t.Fatal("AppendUser failed")
	}
	srv.runSessionTurn(context.Background(), sess.ID)

	got := st.Get(sess.ID)
	if got.Status != chatSessionIdle {
		t.Fatalf("session status = %q, want idle (err=%q)", got.Status, got.Error)
	}
	var userMsg, assistant *SessionMessage
	for i := range got.Messages {
		if got.Messages[i].Role == "user" {
			userMsg = &got.Messages[i]
		}
		if got.Messages[i].Role == "assistant" {
			assistant = &got.Messages[i]
		}
	}
	if userMsg == nil || strings.Contains(userMsg.Content, "Retrieved RAG context") {
		t.Fatalf("stored transcript must not carry injected context: %+v", userMsg)
	}
	if assistant == nil || assistant.Content != "ok" {
		t.Fatalf("assistant reply = %+v", assistant)
	}
	foundTool := false
	for _, e := range assistant.ToolLog {
		if e.Name == "rag_search" && e.Status == "ok" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatalf("tool log lacks a successful rag_search: %+v", assistant.ToolLog)
	}
	req := fake.lastChatRequest()
	var sent string
	for _, m := range req.Messages {
		if m.Role == "user" {
			sent = m.Content
		}
	}
	if !strings.Contains(sent, "Retrieved RAG context (JSON):") {
		t.Fatalf("model did not receive injected context: %q", sent)
	}
}

func TestSessionTurnRAGFailureStillAnswers(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	fake.embedErrs["embed-model:latest"] = context.DeadlineExceeded
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})

	st := srv.chatSessions
	sess := st.Create("chat-model:latest", SessionSettings{
		RAGEnabled: true, RAGPaths: []string{filename}, NumCtxPct: 100,
	})
	st.AppendUser(sess.ID, "hi", nil)
	srv.runSessionTurn(context.Background(), sess.ID)

	got := st.Get(sess.ID)
	if got.Status != chatSessionIdle {
		t.Fatalf("session status = %q, want idle (err=%q)", got.Status, got.Error)
	}
	var assistant *SessionMessage
	for i := range got.Messages {
		if got.Messages[i].Role == "assistant" {
			assistant = &got.Messages[i]
		}
	}
	if assistant == nil || assistant.Content != "ok" {
		t.Fatalf("assistant reply = %+v", assistant)
	}
	foundErr := false
	for _, e := range assistant.ToolLog {
		if e.Name == "rag_search" && e.Status == "error" {
			foundErr = true
		}
	}
	if !foundErr {
		t.Fatalf("tool log lacks the failed rag_search: %+v", assistant.ToolLog)
	}
	for _, m := range fake.lastChatRequest().Messages {
		if strings.Contains(m.Content, "Retrieved RAG context") {
			t.Fatalf("failed retrieval must not inject context: %q", m.Content)
		}
	}
}

func TestChatRAGMixedFailingAndValidBase(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	dir := ragDirOf(t, srv)
	_, bad := createChatRAGBase(t, dir, "Ghost", "ghost-model:latest", "sha256:ghost", 2, []ragpkg.Entry{
		{Term: "x", Content: "c", Embedding: []float64{1, 0}},
	})
	_, good := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
	})

	code, sse := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "apples"}},
		"rag_enabled": true,
		"rag_paths":   []string{bad, good},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	if !strings.Contains(sse, "rag_unavailable") {
		t.Fatalf("missing warning for the bad base: %s", sse)
	}
	var user string
	for _, m := range fake.lastChatRequest().Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	if !strings.Contains(user, "an apple is a fruit") {
		t.Fatalf("valid base context missing: %q", user)
	}
}

func TestChatRAGToolLoopSeesAugmentedMessages(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	fake.chatTool = true
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
	})
	webOn := true

	code, _ := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "apples"}},
		"web_tools":   webOn,
		"rag_enabled": true,
		"rag_paths":   []string{filename},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	fake.mu.Lock()
	first := fake.chatReqs[0]
	fake.mu.Unlock()
	var user string
	for _, m := range first.Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	if !strings.Contains(user, "Retrieved RAG context (JSON):") {
		t.Fatalf("tool loop did not receive augmented messages: %q", user)
	}
}

type recordSink struct {
	mu     sync.Mutex
	events []struct {
		event string
		raw   string
	}
}

func (k *recordSink) Send(event string, payload any) {
	raw, _ := json.Marshal(payload)
	k.mu.Lock()
	k.events = append(k.events, struct {
		event string
		raw   string
	}{event, string(raw)})
	k.mu.Unlock()
}

func (k *recordSink) all() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	var b strings.Builder
	for _, e := range k.events {
		b.WriteString("event: ")
		b.WriteString(e.event)
		b.WriteString("\ndata: ")
		b.WriteString(e.raw)
		b.WriteString("\n\n")
	}
	return b.String()
}

func TestChatRAGWarningCases(t *testing.T) {
	mkBase := func(t *testing.T, dir, name, model, digest string, dims int, vec []float64) string {
		t.Helper()
		_, f := createChatRAGBase(t, dir, name, model, digest, dims, []ragpkg.Entry{
			{Term: "apple", Content: "an apple is a fruit", Embedding: vec},
		})
		return f
	}
	cases := []struct {
		name    string
		setup   func(t *testing.T, fake *fakeOllamaChatRAG, dir string) []string
		wantEmb bool
	}{
		{"model not installed", func(t *testing.T, fake *fakeOllamaChatRAG, dir string) []string {
			return []string{mkBase(t, dir, "Ghost", "ghost-model:latest", "sha256:g", 2, []float64{1, 0})}
		}, false},
		{"digest mismatch", func(t *testing.T, fake *fakeOllamaChatRAG, dir string) []string {
			fake.embedVecs["embed-model:latest"] = []float64{1, 0}
			return []string{mkBase(t, dir, "Old", "embed-model:latest", "sha256:old", 2, []float64{1, 0})}
		}, false},
		{"digest absent", func(t *testing.T, fake *fakeOllamaChatRAG, dir string) []string {
			fake.embedVecs["embed-model:latest"] = []float64{1, 0}
			fake.tags = []map[string]any{
				{"name": "embed-model:latest"},
				{"name": "chat-model:latest", "digest": "sha256:chat1"},
			}
			return []string{mkBase(t, dir, "NoTag", "embed-model:latest", "sha256:embed1", 2, []float64{1, 0})}
		}, false},
		{"wrong dimensions", func(t *testing.T, fake *fakeOllamaChatRAG, dir string) []string {
			fake.embedVecs["embed-model:latest"] = []float64{1, 0}
			return []string{mkBase(t, dir, "Dims", "embed-model:latest", "sha256:embed1", 3, []float64{1, 0, 0})}
		}, true},
		{"empty embedding", func(t *testing.T, fake *fakeOllamaChatRAG, dir string) []string {
			return []string{mkBase(t, dir, "Empty", "embed-model:latest", "sha256:embed1", 2, []float64{1, 0})}
		}, true},
		{"zero embedding", func(t *testing.T, fake *fakeOllamaChatRAG, dir string) []string {
			fake.embedVecs["embed-model:latest"] = []float64{0, 0}
			return []string{mkBase(t, dir, "Zero", "embed-model:latest", "sha256:embed1", 2, []float64{1, 0})}
		}, true},
		{"embed failure", func(t *testing.T, fake *fakeOllamaChatRAG, dir string) []string {
			fake.embedErrs["embed-model:latest"] = errors.New("embed boom")
			return []string{mkBase(t, dir, "Fail", "embed-model:latest", "sha256:embed1", 2, []float64{1, 0})}
		}, true},
		{"corrupt base", func(t *testing.T, fake *fakeOllamaChatRAG, dir string) []string {
			fake.embedVecs["embed-model:latest"] = []float64{1, 0}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "corrupt.db"), []byte("not sqlite"), 0o644); err != nil {
				t.Fatal(err)
			}
			return []string{"corrupt.db"}
		}, false},
		{"missing base", func(t *testing.T, fake *fakeOllamaChatRAG, dir string) []string {
			fake.embedVecs["embed-model:latest"] = []float64{1, 0}
			return []string{"missing.db"}
		}, false},
		{"list failure", func(t *testing.T, fake *fakeOllamaChatRAG, dir string) []string {
			fake.listErr = true
			return []string{mkBase(t, dir, "Any", "embed-model:latest", "sha256:embed1", 2, []float64{1, 0})}
		}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeOllamaChatRAG()
			defer fake.Close()
			srv := newTestServer(t, fake.srv.URL)
			paths := tt.setup(t, fake, ragDirOf(t, srv))
			code, sse := postChat(t, srv, map[string]any{
				"model":       "chat-model:latest",
				"messages":    []map[string]any{{"role": "user", "content": "apples"}},
				"rag_enabled": true,
				"rag_paths":   paths,
			})
			if code != http.StatusOK {
				t.Fatalf("chat status = %d, body = %s", code, sse)
			}
			if !strings.Contains(sse, "event: warning") || !strings.Contains(sse, "rag_unavailable") {
				t.Fatalf("stream lacks warning: %s", sse)
			}
			if !strings.Contains(sse, "event: chunk") || !strings.Contains(sse, "event: done") {
				t.Fatalf("chat did not finish: %s", sse)
			}
			for _, m := range fake.lastChatRequest().Messages {
				if strings.Contains(m.Content, "Retrieved RAG context") {
					t.Fatalf("unavailable base must not inject context: %q", m.Content)
				}
			}
		})
	}
}

func TestChatRAGLatestTagAlias(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Alias", "embed-model", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
	})
	code, sse := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "apples"}},
		"rag_enabled": true,
		"rag_paths":   []string{filename},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	if strings.Contains(sse, "event: warning") {
		t.Fatalf(":latest alias resolution must not warn: %s", sse)
	}
	var user string
	for _, m := range fake.lastChatRequest().Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	if !strings.Contains(user, "an apple is a fruit") {
		t.Fatalf("context missing via :latest alias: %q", user)
	}
}

func TestChatRAGDimMismatchDoesNotPoisonSharedModel(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	dir := ragDirOf(t, srv)
	badMeta, bad := createChatRAGBase(t, dir, "Wide", "embed-model:latest", "sha256:embed1", 3, []ragpkg.Entry{
		{Term: "w", Content: "three dims", Embedding: []float64{1, 0, 0}},
	})
	_, good := createChatRAGBase(t, dir, "Narrow", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
	})
	if badMeta.Dimensions != 3 {
		t.Fatalf("first base dims = %d, want 3", badMeta.Dimensions)
	}
	code, sse := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "apples"}},
		"rag_enabled": true,
		"rag_paths":   []string{bad, good},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	if !strings.Contains(sse, "event: warning") {
		t.Fatalf("dimension-mismatched base must warn: %s", sse)
	}
	var user string
	for _, m := range fake.lastChatRequest().Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	if !strings.Contains(user, "an apple is a fruit") {
		t.Fatalf("valid same-model base lost context to the bad base: %q", user)
	}
	if n := fake.embedCount(); n != 1 {
		t.Fatalf("shared model should embed once, got %d", n)
	}
}

func TestChatRAGCancelDuringEmbedStaysSilent(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	fake.embedBlock = true

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	sink := &recordSink{}
	body := chatRequestBody{
		Model:      "chat-model:latest",
		Messages:   []ollama.ChatMessage{{Role: "user", Content: "apples"}},
		RAGEnabled: true,
		RAGPaths:   []string{filename},
	}
	out := srv.augmentChatWithRAG(ctx, sink, body)
	if strings.Contains(sink.all(), "warning") {
		t.Fatalf("caller cancellation must not warn: %s", sink.all())
	}
	for _, m := range out.Messages {
		if strings.Contains(m.Content, "Retrieved RAG context") {
			t.Fatalf("cancelled retrieval must not inject: %q", m.Content)
		}
	}
}

func TestAugmentPreservesSystemAndInput(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
	})
	body := chatRequestBody{
		Model: "chat-model:latest",
		Messages: []ollama.ChatMessage{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "apples"},
		},
		RAGEnabled: true,
		RAGPaths:   []string{filename},
	}
	out := srv.augmentChatWithRAG(context.Background(), &recordSink{}, body)
	if body.Messages[0].Content != "be terse" || body.Messages[1].Content != "apples" {
		t.Fatalf("input body mutated: %+v", body.Messages)
	}
	if !strings.HasPrefix(out.Messages[0].Content, "be terse") ||
		!strings.Contains(out.Messages[0].Content, "without mentioning RAG, retrieval, reference context, source filenames, entry IDs, or citations unless the user explicitly asks for sources or provenance") {
		t.Fatalf("system prompt not preserved/extended: %q", out.Messages[0].Content)
	}
	if strings.Contains(out.Messages[0].Content, "cite its source as [filename#entry_id]") {
		t.Fatalf("old citation instruction still present: %q", out.Messages[0].Content)
	}
	if !strings.Contains(out.Messages[0].Content, "Start with the answer itself.") ||
		!strings.Contains(out.Messages[0].Content, "'según la información disponible', 'según el contexto', or 'los datos indican'") {
		t.Fatalf("preamble ban missing: %q", out.Messages[0].Content)
	}
	if !strings.Contains(out.Messages[1].Content, "apples\n\nRetrieved RAG context (JSON):") {
		t.Fatalf("user message not augmented: %q", out.Messages[1].Content)
	}
}

func TestChatRAGRoundRobinNeverComparesAcrossModels(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	fake.embedVecs["embed-vision:latest"] = []float64{1, 0}
	dir := ragDirOf(t, srv)
	mk := func(name, model, digest string, vec []float64) string {
		entries := make([]ragpkg.Entry, 4)
		for i := range entries {
			entries[i] = ragpkg.Entry{Term: name + string(rune('a'+i)), Content: name + " entry", Embedding: vec}
		}
		_, f := createChatRAGBase(t, dir, name, model, digest, 2, entries)
		return f
	}
	fa := mk("Alpha", "embed-model:latest", "sha256:embed1", []float64{1, 0})
	fb := mk("Beta", "embed-vision:latest", "sha256:embedv", []float64{0.8, 0.6})
	code, _ := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "apples"}},
		"rag_enabled": true,
		"rag_paths":   []string{fa, fb},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	var user string
	for _, m := range fake.lastChatRequest().Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	idx := strings.Index(user, "Retrieved RAG context (JSON):\n")
	if idx < 0 {
		t.Fatalf("context missing: %q", user)
	}
	var items []struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(user[idx+29:])), &items); err != nil {
		t.Fatalf("context is not JSON: %v", err)
	}
	if len(items) != 6 {
		t.Fatalf("round robin should emit 6 items, got %d: %q", len(items), user)
	}
	for i, it := range items {
		want := fa
		if i%2 == 1 {
			want = fb
		}
		if it.Filename != want {
			t.Fatalf("item %d = %s, want %s (order must alternate by selection, not score)", i, it.Filename, want)
		}
	}
}

func TestChatRAGTruncationAndContextBudget(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	dir := ragDirOf(t, srv)
	mk := func(name string) string {
		entries := make([]ragpkg.Entry, 3)
		for i := range entries {
			entries[i] = ragpkg.Entry{
				Term:      strings.Repeat("t", 300),
				Content:   strings.Repeat("c", 3000),
				Embedding: []float64{1, 0},
			}
		}
		_, f := createChatRAGBase(t, dir, name, "embed-model:latest", "sha256:embed1", 2, entries)
		return f
	}
	fa, fb := mk("One"), mk("Two")
	code, _ := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "apples"}},
		"rag_enabled": true,
		"rag_paths":   []string{fa, fb},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	var user string
	for _, m := range fake.lastChatRequest().Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	idx := strings.Index(user, "Retrieved RAG context (JSON):\n")
	if idx < 0 {
		t.Fatalf("context missing: %q", user)
	}
	raw := strings.TrimSpace(user[idx+len("Retrieved RAG context (JSON):\n"):])
	if utf8.RuneCountInString(raw) > ragContextMaxRunes {
		t.Fatalf("serialized context is %d runes, budget %d", utf8.RuneCountInString(raw), ragContextMaxRunes)
	}
	var items []struct {
		Term    string `json:"term"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("context is not JSON: %v", err)
	}
	if len(items) == 0 || len(items) >= 6 {
		t.Fatalf("budget should drop at least one of 6 oversized items, got %d", len(items))
	}
	for _, it := range items {
		if utf8.RuneCountInString(it.Term) > ragFieldTermRunes || utf8.RuneCountInString(it.Content) > ragFieldContentRunes {
			t.Fatalf("field limits exceeded: term=%d content=%d", utf8.RuneCountInString(it.Term), utf8.RuneCountInString(it.Content))
		}
	}
}

func TestChatRAGExternalAttachment(t *testing.T) {
	fake := newFakeOllamaChatRAG()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedVecs["embed-model:latest"] = []float64{1, 0}
	dir := ragDirOf(t, srv)

	seed := t.TempDir()
	_, seedFile := createChatRAGBase(t, seed, "Attach", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
		{Term: "car", Content: "a car is a vehicle", Embedding: []float64{0, 1}},
	})
	raw, err := os.ReadFile(filepath.Join(seed, seedFile))
	if err != nil {
		t.Fatal(err)
	}
	code, out := chatRAGUpload(t, srv, "attach.db", raw)
	if code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %v", code, out)
	}
	ref := out["rag"].(map[string]any)["filename"].(string)
	if got := countDBFiles(t, dir); len(got) != 0 {
		t.Fatalf("managed dir must stay empty: %v", got)
	}

	code, sse := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "tell me about apples"}},
		"rag_enabled": true,
		"rag_paths":   []string{ref},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d, body = %s", code, sse)
	}
	var user string
	for _, m := range fake.lastChatRequest().Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	if !strings.Contains(user, "an apple is a fruit") || !strings.Contains(user, `"filename":"`+ref+`"`) {
		t.Fatalf("external attachment context missing: %q", user)
	}
	if got := countDBFiles(t, dir); len(got) != 0 {
		t.Fatalf("retrieval leaked into managed dir: %v", got)
	}
}
