package server

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOllamaGateway serves tags, canned NDJSON chat chunks, a canned show
// response, and records the last /api/chat request body for assertions.
type fakeOllamaGateway struct {
	srv         *httptest.Server
	tags        []string
	chatChunks  []string
	showBody    string
	lastChatRaw string
}

func newFakeOllamaGateway(t *testing.T, tags []string, chunks []string) *fakeOllamaGateway {
	t.Helper()
	f := &fakeOllamaGateway{tags: tags, chatChunks: chunks}
	f.showBody = `{"modelfile":"FROM x","parameters":"","template":"{{ .Prompt }}","details":{"format":"gguf","family":"test"},"model_info":{},"capabilities":["completion"]}`
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			models := make([]any, 0, len(f.tags))
			for _, n := range f.tags {
				models = append(models, map[string]any{"name": n, "model": n, "size": 100, "digest": "d-" + n})
			}
			writeJSON(w, http.StatusOK, map[string]any{"models": models})
		case "/api/chat":
			raw, _ := io.ReadAll(r.Body)
			f.lastChatRaw = string(raw)
			w.Header().Set("Content-Type", "application/x-ndjson")
			for _, c := range f.chatChunks {
				_, _ = w.Write([]byte(c + "\n"))
			}
		case "/api/show":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(f.showBody))
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func gatewayOllamaDo(t *testing.T, srv *Server, method, target, body, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	rr := httptest.NewRecorder()
	srv.gatewayRoutes().ServeHTTP(rr, req)
	return rr
}

func TestGatewayTags(t *testing.T) {
	f := newFakeOllamaGateway(t, []string{"local-a", "local-b"}, nil)
	srv := newTestServer(t, f.srv.URL)
	if err := srv.externalModels.Register("ext-on", "http://x/v1", "", nil, false); err != nil {
		t.Fatal(err)
	}
	if err := srv.externalModels.Register("ext-off", "http://x/v1", "", nil, true); err != nil {
		t.Fatal(err)
	}

	rr := gatewayOllamaDo(t, srv, http.MethodGet, "/api/tags", "", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	models, _ := out["models"].([]any)
	if len(models) != 3 {
		t.Fatalf("models = %v, want 3", out["models"])
	}
	byName := map[string]map[string]any{}
	for _, m := range models {
		mm := m.(map[string]any)
		byName[mm["name"].(string)] = mm
	}
	if byName["local-a"]["digest"] != "d-local-a" || byName["local-a"]["size"] != float64(100) {
		t.Fatalf("local entry = %v", byName["local-a"])
	}
	if _, ok := byName["ext-off"]; ok {
		t.Fatalf("disabled external listed: %v", models)
	}
	ext := byName["ext-on"]
	if ext["model"] != "ext-on" {
		t.Fatalf("external entry = %v", ext)
	}
	if det, _ := ext["details"].(map[string]any); det["format"] != "external" {
		t.Fatalf("external details = %v", det)
	}

	srv.cfg.Gateway.Models = []string{"local-b"}
	rr = gatewayOllamaDo(t, srv, http.MethodGet, "/api/tags", "", "")
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if models, _ := out["models"].([]any); len(models) != 1 {
		t.Fatalf("allowlisted tags = %v", out["models"])
	}
}

func TestGatewayTagsAuth(t *testing.T) {
	f := newFakeOllamaGateway(t, nil, nil)
	srv := newTestServer(t, f.srv.URL)
	srv.cfg.Gateway.RequireAuth = true
	rr := gatewayOllamaDo(t, srv, http.MethodGet, "/api/tags", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	var out map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if _, ok := out["error"]; !ok {
		t.Fatalf("body not Ollama-shaped: %v", out)
	}
}

func TestGatewayShow(t *testing.T) {
	f := newFakeOllamaGateway(t, []string{"local-a"}, nil)
	srv := newTestServer(t, f.srv.URL)
	if err := srv.externalModels.Register("ext-on", "http://x/v1", "", []string{"completion", "vision"}, false); err != nil {
		t.Fatal(err)
	}

	rr := gatewayOllamaDo(t, srv, http.MethodPost, "/api/show", `{"model":"local-a"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("local show status = %d", rr.Code)
	}
	var local map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &local)
	if local["modelfile"] != "FROM x" {
		t.Fatalf("local show = %v", local)
	}

	rr = gatewayOllamaDo(t, srv, http.MethodPost, "/api/show", `{"name":"ext-on"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("external show status = %d", rr.Code)
	}
	var ext map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &ext)
	if det, _ := ext["details"].(map[string]any); det["format"] != "external" {
		t.Fatalf("external show = %v", ext)
	}
	caps, _ := ext["capabilities"].([]any)
	if len(caps) != 2 {
		t.Fatalf("external capabilities = %v", ext["capabilities"])
	}

	rr = gatewayOllamaDo(t, srv, http.MethodPost, "/api/show", `{"model":"nope"}`, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown show status = %d, want 404", rr.Code)
	}
}

func TestGatewayOllamaChatStream(t *testing.T) {
	f := newFakeOllamaGateway(t, []string{"local-a"}, []string{
		`{"model":"local-a","message":{"role":"assistant","content":"Hel"},"done":false}`,
		`{"model":"local-a","message":{"role":"assistant","content":"lo"},"done":false}`,
		`{"model":"local-a","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":4,"eval_count":3}`,
	})
	srv := newTestServer(t, f.srv.URL)

	// Absent "stream" defaults to true (Ollama semantics).
	rr := gatewayOllamaDo(t, srv, http.MethodPost, "/api/chat", `{"model":"local-a","messages":[{"role":"user","content":"hi"}]}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var text strings.Builder
	var doneSeen bool
	var doneReason string
	sc := bufio.NewScanner(strings.NewReader(rr.Body.String()))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", line, err)
		}
		if ev["model"] != "local-a" {
			t.Fatalf("model = %v", ev["model"])
		}
		if done, _ := ev["done"].(bool); done {
			doneSeen = true
			doneReason, _ = ev["done_reason"].(string)
			if ev["prompt_eval_count"] != float64(4) || ev["eval_count"] != float64(3) {
				t.Fatalf("counts = %v", ev)
			}
			continue
		}
		msg := ev["message"].(map[string]any)
		if msg["role"] != "assistant" {
			t.Fatalf("role = %v", msg["role"])
		}
		text.WriteString(msg["content"].(string))
	}
	if text.String() != "Hello" {
		t.Fatalf("content = %q", text.String())
	}
	if !doneSeen || doneReason != "stop" {
		t.Fatalf("done=%v reason=%q", doneSeen, doneReason)
	}
}

func TestGatewayOllamaChatOnce(t *testing.T) {
	f := newFakeOllamaGateway(t, []string{"local-a"}, []string{
		`{"model":"local-a","message":{"role":"assistant","content":"A"},"done":false}`,
		`{"model":"local-a","message":{"role":"assistant","content":"B"},"done":false}`,
		`{"model":"local-a","message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":1,"eval_count":2}`,
	})
	srv := newTestServer(t, f.srv.URL)

	rr := gatewayOllamaDo(t, srv, http.MethodPost, "/api/chat", `{"model":"local-a","messages":[{"role":"user","content":"hi"}],"stream":false}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["done"] != true || out["message"].(map[string]any)["content"] != "AB" {
		t.Fatalf("body = %v", out)
	}
	if out["done_reason"] != "stop" {
		t.Fatalf("done_reason = %v", out["done_reason"])
	}
}

func TestGatewayOllamaChatErrors(t *testing.T) {
	f := newFakeOllamaGateway(t, []string{"local-a"}, nil)
	srv := newTestServer(t, f.srv.URL)

	rr := gatewayOllamaDo(t, srv, http.MethodPost, "/api/chat", `{"messages":[]}`, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing model status = %d, want 400", rr.Code)
	}
	rr = gatewayOllamaDo(t, srv, http.MethodPost, "/api/chat", `{"model":"nope","messages":[{"role":"user","content":"hi"}]}`, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown model status = %d, want 404", rr.Code)
	}
}

func TestGatewayOllamaChatTools(t *testing.T) {
	f := newFakeOllamaGateway(t, []string{"local-a"}, []string{
		`{"model":"local-a","message":{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"f","arguments":{"a":1}}}]},"done":false}`,
		`{"model":"local-a","message":{"role":"assistant","content":""},"done":true}`,
	})
	srv := newTestServer(t, f.srv.URL)

	rr := gatewayOllamaDo(t, srv, http.MethodPost, "/api/chat", `{"model":"local-a","messages":[{"role":"user","content":"hi"}],"stream":false,"tools":[{"type":"function"}]}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	msg := out["message"].(map[string]any)
	calls, _ := msg["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool_calls = %v", msg["tool_calls"])
	}
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "f" {
		t.Fatalf("function = %v", fn)
	}
	args, _ := fn["arguments"].(map[string]any)
	if args["a"] != float64(1) {
		t.Fatalf("arguments not preserved as object: %v", fn["arguments"])
	}
}

func TestGatewayGenerate(t *testing.T) {
	// Upstream is always chat-shaped: the gateway translates generate→chat.
	f := newFakeOllamaGateway(t, []string{"local-a"}, []string{
		`{"model":"local-a","message":{"role":"assistant","content":"Hel"},"done":false}`,
		`{"model":"local-a","message":{"role":"assistant","content":"lo"},"done":false}`,
		`{"model":"local-a","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":7,"eval_count":2}`,
	})
	srv := newTestServer(t, f.srv.URL)

	rr := gatewayOllamaDo(t, srv, http.MethodPost, "/api/generate", `{"model":"local-a","prompt":"hi","system":"be nice"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("stream status = %d", rr.Code)
	}
	var text strings.Builder
	var doneSeen bool
	sc := bufio.NewScanner(strings.NewReader(rr.Body.String()))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		if done, _ := ev["done"].(bool); done {
			doneSeen = true
			continue
		}
		text.WriteString(ev["response"].(string))
	}
	if text.String() != "Hello" || !doneSeen {
		t.Fatalf("text=%q done=%v", text.String(), doneSeen)
	}
	// prompt+system must have been translated into chat messages upstream.
	var upstream map[string]any
	if err := json.Unmarshal([]byte(f.lastChatRaw), &upstream); err != nil {
		t.Fatalf("upstream body not JSON: %v", err)
	}
	msgs, _ := upstream["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("upstream messages = %v", upstream["messages"])
	}
	if msgs[0].(map[string]any)["role"] != "system" || msgs[1].(map[string]any)["content"] != "hi" {
		t.Fatalf("upstream messages = %v", msgs)
	}

	rr = gatewayOllamaDo(t, srv, http.MethodPost, "/api/generate", `{"model":"local-a","prompt":"hi","stream":false}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("once status = %d", rr.Code)
	}
	var once map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &once)
	if once["response"] != "Hello" || once["done"] != true || once["done_reason"] != "stop" {
		t.Fatalf("once = %v", once)
	}

	rr = gatewayOllamaDo(t, srv, http.MethodPost, "/api/generate", `{"model":"nope","prompt":"hi"}`, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown model status = %d, want 404", rr.Code)
	}
}
