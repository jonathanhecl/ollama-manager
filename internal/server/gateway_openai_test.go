package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gense/ollama-manager/internal/ollama"
	"github.com/gense/ollama-manager/internal/tests"
)

// fakeOllamaChat serves canned /api/tags models and NDJSON /api/chat chunks.
func fakeOllamaChat(t *testing.T, tagNames []string, chunks []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			models := make([]any, 0, len(tagNames))
			for _, n := range tagNames {
				models = append(models, map[string]any{"name": n})
			}
			writeJSON(w, http.StatusOK, map[string]any{"models": models})
		case "/api/chat":
			w.Header().Set("Content-Type", "application/x-ndjson")
			for _, c := range chunks {
				_, _ = w.Write([]byte(c + "\n"))
			}
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
}

func gatewayDo(t *testing.T, srv *Server, method, target, body, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	rr := httptest.NewRecorder()
	srv.gatewayRoutes().ServeHTTP(rr, req)
	return rr
}

func TestGatewayModelsListsExposed(t *testing.T) {
	ollamaSrv := fakeOllamaChat(t, []string{"local-a", "local-b"}, nil)
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	stub := func(ctx context.Context) ([]ollama.Model, error) {
		return []ollama.Model{{Name: "local-a"}, {Name: "local-b"}}, nil
	}
	got, err := srv.gatewayExposedModelsWithLister(context.Background(), stub)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "local-a" || got[1] != "local-b" {
		t.Fatalf("exposed = %v", got)
	}

	// Disabled externals are hidden; enabled ones listed.
	if err := srv.externalModels.Register("ext-on", "http://x/v1", "", nil, false); err != nil {
		t.Fatal(err)
	}
	if err := srv.externalModels.Register("ext-off", "http://x/v1", "", nil, true); err != nil {
		t.Fatal(err)
	}
	got, _ = srv.gatewayExposedModelsWithLister(context.Background(), stub)
	for _, want := range []string{"local-a", "local-b", "ext-on"} {
		found := false
		for _, n := range got {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("exposed = %v, want %q", got, want)
		}
	}
	for _, n := range got {
		if n == "ext-off" {
			t.Fatalf("disabled external listed: %v", got)
		}
	}

	// Allowlist filters.
	srv.cfg.Gateway.Models = []string{"ext-on"}
	got, _ = srv.gatewayExposedModelsWithLister(context.Background(), stub)
	if len(got) != 1 || got[0] != "ext-on" {
		t.Fatalf("allowlisted = %v", got)
	}
	srv.cfg.Gateway.Models = nil

	// :latest tolerance.
	if r := gatewayResolveModel("local-a:latest", []string{"local-a"}); r != "local-a" {
		t.Fatalf("resolve local-a:latest = %q", r)
	}
	if r := gatewayResolveModel("nope", []string{"local-a"}); r != "" {
		t.Fatalf("resolve nope = %q", r)
	}

	// HTTP shape.
	rr := gatewayDo(t, srv, http.MethodGet, "/v1/models", "", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["object"] != "list" {
		t.Fatalf("object = %v", out["object"])
	}
	data, _ := out["data"].([]any)
	if len(data) != 3 {
		t.Fatalf("data has %d entries: %v", len(data), out)
	}
	first := data[0].(map[string]any)
	if first["object"] != "model" || first["owned_by"] != "ollama-manager" || first["id"] == "" {
		t.Fatalf("bad model entry: %v", first)
	}
}

func TestGatewayAuth(t *testing.T) {
	ollamaSrv := fakeOllamaChat(t, nil, nil)
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)
	srv.cfg.Gateway.RequireAuth = true

	rr := gatewayDo(t, srv, http.MethodGet, "/v1/models", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no key status = %d, want 401", rr.Code)
	}
	var errBody map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &errBody)
	if _, ok := errBody["error"].(map[string]any); !ok {
		t.Fatalf("error body not OpenAI-shaped: %v", errBody)
	}

	rr = gatewayDo(t, srv, http.MethodGet, "/v1/models", "", "wrong")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key status = %d, want 401", rr.Code)
	}

	rec, secret, err := srv.gatewayKeys.Create("t")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID == "" {
		t.Fatalf("no id issued")
	}
	rr = gatewayDo(t, srv, http.MethodGet, "/v1/models", "", secret)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid key status = %d, want 200", rr.Code)
	}

	srv.cfg.Gateway.RequireAuth = false
	rr = gatewayDo(t, srv, http.MethodGet, "/v1/models", "", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("auth disabled status = %d, want 200", rr.Code)
	}
}

func TestGatewayChatNonStream(t *testing.T) {
	chunks := []string{
		`{"model":"local-a","message":{"role":"assistant","content":"Hel"},"done":false}`,
		`{"model":"local-a","message":{"role":"assistant","content":"lo"},"done":false}`,
		`{"model":"local-a","message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":10,"eval_count":5}`,
	}
	ollamaSrv := fakeOllamaChat(t, []string{"local-a"}, chunks)
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	// The fake /api/tags is empty; expose local-a via the allowlist so the
	// model check passes and chat goes to the fake Ollama server.
	srv.cfg.Gateway.Models = []string{"local-a"}

	rr := gatewayDo(t, srv, http.MethodPost, "/v1/chat/completions", `{"model":"local-a","messages":[{"role":"user","content":"hi"}],"stream":false}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["object"] != "chat.completion" {
		t.Fatalf("object = %v", out["object"])
	}
	choices := out["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "Hello" {
		t.Fatalf("content = %q", msg["content"])
	}
	if choices[0].(map[string]any)["finish_reason"] != "stop" {
		t.Fatalf("finish_reason = %v", choices[0])
	}
	usage := out["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(10) || usage["completion_tokens"] != float64(5) || usage["total_tokens"] != float64(15) {
		t.Fatalf("usage = %v", usage)
	}
}

func TestGatewayChatStream(t *testing.T) {
	chunks := []string{
		`{"model":"local-a","message":{"role":"assistant","content":"Hel"},"done":false}`,
		`{"model":"local-a","message":{"role":"assistant","content":"lo"},"done":false}`,
		`{"model":"local-a","message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":3,"eval_count":2}`,
	}
	ollamaSrv := fakeOllamaChat(t, []string{"local-a"}, chunks)
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)
	srv.cfg.Gateway.Models = []string{"local-a"}

	rr := gatewayDo(t, srv, http.MethodPost, "/v1/chat/completions", `{"model":"local-a","messages":[{"role":"user","content":"hi"}],"stream":true}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	var deltas []string
	var finish string
	var sawRole, sawUsage, sawDone bool
	sc := bufio.NewScanner(strings.NewReader(rr.Body.String()))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if line == "data: [DONE]" {
			sawDone = true
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			t.Fatalf("bad SSE line: %q", line)
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatal(err)
		}
		if ev["object"] != "chat.completion.chunk" {
			t.Fatalf("object = %v", ev["object"])
		}
		choice := ev["choices"].([]any)[0].(map[string]any)
		delta := choice["delta"].(map[string]any)
		if r, _ := delta["role"].(string); r == "assistant" {
			sawRole = true
		}
		if c, _ := delta["content"].(string); c != "" {
			deltas = append(deltas, c)
		}
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			finish = fr
		}
		if u, ok := ev["usage"].(map[string]any); ok && u != nil {
			sawUsage = true
			if u["prompt_tokens"] != float64(3) || u["completion_tokens"] != float64(2) {
				t.Fatalf("usage = %v", u)
			}
		}
	}
	if strings.Join(deltas, "") != "Hello" {
		t.Fatalf("deltas = %q", deltas)
	}
	if !sawRole || finish != "stop" || !sawUsage || !sawDone {
		t.Fatalf("role=%v finish=%q usage=%v done=%v", sawRole, finish, sawUsage, sawDone)
	}
}

func TestGatewayChatErrors(t *testing.T) {
	ollamaSrv := fakeOllamaChat(t, []string{"local-a"}, nil)
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)
	srv.cfg.Gateway.Models = []string{"local-a"}

	rr := gatewayDo(t, srv, http.MethodPost, "/v1/chat/completions", `{"messages":[]}`, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing model status = %d, want 400", rr.Code)
	}
	rr = gatewayDo(t, srv, http.MethodPost, "/v1/chat/completions", `{"model":"nope","messages":[{"role":"user","content":"hi"}]}`, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown model status = %d, want 404", rr.Code)
	}
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["error"].(map[string]any)["code"] != "model_not_found" {
		t.Fatalf("body = %v", out)
	}
	rr = gatewayDo(t, srv, http.MethodPost, "/v1/chat/completions", `not json`, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad json status = %d, want 400", rr.Code)
	}
}

func TestGatewayInboundTranslation(t *testing.T) {
	in := gatewayChatRequest{Model: "m"}
	in.Messages = []gatewayInboundMessage{
		{Role: "system", Content: "be nice"},
		{Role: "user", Content: []any{
			map[string]any{"type": "text", "text": "see this"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAA"}},
			map[string]any{"type": "image_url", "image_url": "http://x/y.png"},
		}},
		{Role: "assistant", Content: nil, ToolCalls: []gatewayInboundToolCall{
			{ID: "c1", Type: "function", Function: struct {
				Name      string `json:"name"`
				Arguments any    `json:"arguments"`
			}{Name: "f", Arguments: map[string]any{"a": 1}}},
		}},
		{Role: "tool", Content: "ok", ToolCallID: "c1"},
	}
	temp := 0.5
	in.Temperature = &temp
	in.Tools = []any{"x"}
	in.ReasoningEffort = "low"

	req, err := gatewayToChatRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 4 {
		t.Fatalf("messages = %d", len(req.Messages))
	}
	if req.Messages[1].Content != "see this" {
		t.Fatalf("text = %q", req.Messages[1].Content)
	}
	if len(req.Messages[1].Images) != 2 || req.Messages[1].Images[0] != "AAA" || req.Messages[1].Images[1] != "http://x/y.png" {
		t.Fatalf("images = %v", req.Messages[1].Images)
	}
	tc := req.Messages[2].ToolCalls
	if len(tc) != 1 || tc[0].Function.Name != "f" || string(tc[0].Function.Arguments) != `{"a":1}` {
		t.Fatalf("tool_calls = %+v", tc)
	}
	if req.Messages[3].Role != "tool" || req.Messages[3].ToolName != "c1" {
		t.Fatalf("tool msg = %+v", req.Messages[3])
	}
	if req.Options["temperature"] != 0.5 {
		t.Fatalf("options = %v", req.Options)
	}
	if req.Think == nil || *req.Think != "low" {
		t.Fatalf("think = %v", req.Think)
	}

	// Developer role folds to system; string tool args pass through.
	in2 := gatewayChatRequest{Model: "m", Messages: []gatewayInboundMessage{{Role: "developer", Content: "x"}}}
	req2, err := gatewayToChatRequest(in2)
	if err != nil {
		t.Fatal(err)
	}
	if req2.Messages[0].Role != "system" {
		t.Fatalf("developer mapped to %q", req2.Messages[0].Role)
	}
	if _, err := gatewayToChatRequest(gatewayChatRequest{}); err == nil {
		t.Fatalf("expected error for empty request")
	}
}

func TestGatewayToolDeltaAccumulation(t *testing.T) {
	mkcall := func(idx int, args string) ollama.ToolCall {
		var tc ollama.ToolCall
		tc.Type = "function"
		tc.Function.Index = idx
		tc.Function.Name = "get_weather"
		tc.Function.Arguments = json.RawMessage(args)
		return tc
	}

	// External-style: each chunk re-sends accumulated arguments; the
	// tracker must converge on the full value.
	st := newGatewayStreamState("m")
	st.deltaToolCalls([]ollama.ToolCall{mkcall(0, `{"ci`)})
	st.deltaToolCalls([]ollama.ToolCall{mkcall(0, `{"city":"A`)})
	st.deltaToolCalls([]ollama.ToolCall{mkcall(0, `{"city":"ABC"}`)})
	if got := st.toolAcc[0].fullArgs; got != `{"city":"ABC"}` {
		t.Fatalf("accumulated = %q", got)
	}

	// The emitted deltas must concatenate to exactly the full arguments.
	st2 := newGatewayStreamState("m")
	var pieces []string
	nameCount := 0
	for _, snap := range []string{`{"ci`, `{"city":"A`, `{"city":"ABC"}`} {
		for _, d := range st2.deltaToolCalls([]ollama.ToolCall{mkcall(0, snap)}) {
			fn := d.(map[string]any)["function"].(map[string]any)
			if n, ok := fn["name"].(string); ok && n == "get_weather" {
				nameCount++
			}
			if a, ok := fn["arguments"].(string); ok {
				pieces = append(pieces, a)
			}
		}
	}
	if strings.Join(pieces, "") != `{"city":"ABC"}` {
		t.Fatalf("delta pieces = %q", pieces)
	}
	if nameCount != 1 {
		t.Fatalf("name emitted %d times, want 1", nameCount)
	}

	// Local-style fragments concatenate as well.
	st3 := newGatewayStreamState("m")
	var frags []string
	for _, frag := range []string{`{"a"`, `:1}`} {
		for _, d := range st3.deltaToolCalls([]ollama.ToolCall{mkcall(1, frag)}) {
			if a, ok := d.(map[string]any)["function"].(map[string]any)["arguments"].(string); ok {
				frags = append(frags, a)
			}
		}
	}
	if strings.Join(frags, "") != `{"a":1}` {
		t.Fatalf("fragment pieces = %q", frags)
	}
}

func TestGatewayExposedWithTestsStore(t *testing.T) {
	ollamaSrv := fakeOllamaChat(t, nil, nil)
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)
	// Unused here besides ensuring the store exists; groups don't gate the gateway.
	if _, err := srv.testsStore.CreateGroup(tests.Group{ID: "g1", Name: "G"}); err != nil {
		t.Fatal(err)
	}
	rr := gatewayDo(t, srv, http.MethodGet, "/v1/models", "", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
}
