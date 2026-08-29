package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gense/ollama-manager/internal/config"
	"github.com/gense/ollama-manager/internal/ollama"
)

func newOpenCodeTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := config.Defaults()
	cfg.OllamaURL = "http://localhost:11434"
	cfg.SessionSecret = "test"

	tags := `{"models":[
		{"name":"tag-a","model":"tag-a","size":1000},
		{"name":"tag-b","model":"tag-b","size":2000},
		{"name":"tag-c","model":"tag-c","size":3000}
	]}`
	tagSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			_, _ = io.WriteString(w, tags)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(tagSrv.Close)

	s := &Server{
		cfg:    cfg,
		ollama: ollama.New(tagSrv.URL),
	}

	configDir := t.TempDir()
	ocPath := filepath.Join(configDir, "opencode.json")
	t.Setenv("OPENCODE_CONFIG", ocPath)
	return s, ocPath
}

func (s *Server) doOpenCode(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = strings.NewReader(string(buf))
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func decodeOpenCodeState(t *testing.T, w *httptest.ResponseRecorder) opencodeStateView {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		State opencodeStateView `json:"state"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		var view opencodeStateView
		if err2 := json.Unmarshal(w.Body.Bytes(), &view); err2 == nil {
			return view
		}
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	return payload.State
}

func TestOpenCodeEndToEnd(t *testing.T) {
	s, ocPath := newOpenCodeTestServer(t)

	// 1. GET before anything exists.
	w := s.doOpenCode(t, http.MethodGet, "/api/opencode", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", w.Code, w.Body.String())
	}
	var initial opencodeStateView
	if err := json.Unmarshal(w.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Exists {
		t.Fatal("expected exists=false before file creation")
	}
	if !initial.Remote {
		t.Fatal("expected remote=true for httptest client (non-loopback)")
	}
	if initial.Provider != nil {
		t.Fatalf("expected no provider, got %+v", initial.Provider)
	}
	if initial.DefaultBaseURL != "http://localhost:11434/v1" {
		t.Fatalf("default base url = %q", initial.DefaultBaseURL)
	}
	if len(initial.Models) != 3 {
		t.Fatalf("models count = %d; want 3", len(initial.Models))
	}
	for _, m := range initial.Models {
		if m.Enabled {
			t.Fatalf("model %s should start disabled", m.Name)
		}
	}

	// 2. Create the provider.
	w = s.doOpenCode(t, http.MethodPost, "/api/opencode/provider", map[string]any{})
	state := decodeOpenCodeState(t, w)
	if state.Provider == nil {
		t.Fatal("provider missing after create")
	}
	if state.Provider.Key != "ollama-local" {
		t.Fatalf("provider key = %q", state.Provider.Key)
	}
	if _, err := os.Stat(ocPath); err != nil {
		t.Fatalf("config file not written: %v", err)
	}

	// 3. Save an exact set of models.
	w = s.doOpenCode(t, http.MethodPost, "/api/opencode/models", map[string]any{
		"enabled": []string{"tag-a", "tag-b"},
	})
	state = decodeOpenCodeState(t, w)
	enabled := map[string]bool{}
	for _, m := range state.Models {
		enabled[m.Name] = m.Enabled
	}
	if !enabled["tag-a"] || !enabled["tag-b"] || enabled["tag-c"] {
		t.Fatalf("enabled state wrong: %+v", enabled)
	}

	// 4. The file on disk contains exactly the enabled set.
	data, err := os.ReadFile(ocPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	models, _ := raw["provider"].(map[string]any)["ollama-local"].(map[string]any)["models"].(map[string]any)
	if len(models) != 2 {
		t.Fatalf("models on disk = %d; want 2", len(models))
	}
	if _, ok := models["tag-c"]; ok {
		t.Fatal("tag-c should have been removed from disk")
	}
	if _, ok := models["tag-a"]; !ok {
		t.Fatal("tag-a missing on disk")
	}

	// 5. Custom display names and limits are persisted with the selection.
	w = s.doOpenCode(t, http.MethodPost, "/api/opencode/models", map[string]any{
		"enabled": []string{"tag-a"},
		"names":   map[string]string{"tag-a": "Custom Name A"},
		"limits": map[string]any{
			"tag-a": map[string]any{
				"context": 65536,
				"output":  16384,
			},
		},
	})
	state = decodeOpenCodeState(t, w)
	for _, m := range state.Models {
		if m.Name == "tag-a" && m.DisplayName != "Custom Name A" {
			t.Fatalf("custom name not applied: %+v", m)
		}
	}
	data, err = os.ReadFile(ocPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"name": "Custom Name A"`) {
		t.Fatalf("custom name missing on disk:\n%s", data)
	}
	if !strings.Contains(string(data), `"context": 65536`) || !strings.Contains(string(data), `"output": 16384`) {
		t.Fatalf("limits missing on disk:\n%s", data)
	}
	if strings.Contains(string(data), `"tag-b"`) || strings.Contains(string(data), `"tag-c"`) {
		t.Fatalf("disabled models left on disk:\n%s", data)
	}

	// 6. Saving again with an empty selection clears the map.
	w = s.doOpenCode(t, http.MethodPost, "/api/opencode/models", map[string]any{"enabled": []string{}})
	state = decodeOpenCodeState(t, w)
	for _, m := range state.Models {
		if m.Enabled {
			t.Fatalf("model %s should be disabled after clearing", m.Name)
		}
	}
}

func TestOpenCodeSetModelsRequiresProvider(t *testing.T) {
	s, ocPath := newOpenCodeTestServer(t)
	// Seed a config file with no local provider.
	if err := os.WriteFile(ocPath, []byte(`{"provider":{"ollama-remote":{"options":{"baseURL":"http://192.168.0.121:11434/v1"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	w := s.doOpenCode(t, http.MethodPost, "/api/opencode/models", map[string]any{
		"enabled": []string{"tag-a"},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestOpenCodeDoesNotOverwriteLlamacppOnPort8080(t *testing.T) {
	s, ocPath := newOpenCodeTestServer(t)
	raw := `{
	"provider": {
		"llamacpp": {
			"npm": "@ai-sdk/openai-compatible",
			"name": "llama.cpp local",
			"options": {
				"baseURL": "http://127.0.0.1:8080/v1"
			},
			"models": {
				"llama3-8b": {
					"name": "Llama 3 8B"
				}
			}
		},
		"ollama": {
			"npm": "@ai-sdk/openai-compatible",
			"name": "Ollama",
			"options": {
				"baseURL": "http://localhost:11434/v1"
			},
			"models": {}
		}
	}
}`
	if err := os.WriteFile(ocPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. GET /api/opencode must detect ollama, NOT llamacpp.
	w := s.doOpenCode(t, http.MethodGet, "/api/opencode", nil)
	var state opencodeStateView
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Provider == nil {
		t.Fatal("expected provider to be detected")
	}
	if state.Provider.Key != "ollama" {
		t.Fatalf("detected provider key = %q; want %q (must NOT pick llamacpp)", state.Provider.Key, "ollama")
	}
	if state.Provider.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("detected baseURL = %q", state.Provider.BaseURL)
	}

	// 2. POST /api/opencode/models must update only the ollama provider.
	w = s.doOpenCode(t, http.MethodPost, "/api/opencode/models", map[string]any{
		"enabled": []string{"tag-a"},
		"names":   map[string]string{"tag-a": "Friendly Tag A"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}

	data, err := os.ReadFile(ocPath)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded map[string]any
	if err := json.Unmarshal(data, &reloaded); err != nil {
		t.Fatal(err)
	}
	providers, _ := reloaded["provider"].(map[string]any)

	// llamacpp must be completely untouched.
	llamacpp, ok := providers["llamacpp"].(map[string]any)
	if !ok {
		t.Fatal("llamacpp provider was deleted or missing")
	}
	llamaModels, _ := llamacpp["models"].(map[string]any)
	if _, exists := llamaModels["llama3-8b"]; !exists {
		t.Fatal("llama3-8b model was overwritten/removed from llamacpp")
	}
	if _, exists := llamaModels["tag-a"]; exists {
		t.Fatal("tag-a was erroneously added to llamacpp")
	}

	// ollama must contain tag-a.
	ollamaProv, ok := providers["ollama"].(map[string]any)
	if !ok {
		t.Fatal("ollama provider missing")
	}
	ollamaModels, _ := ollamaProv["models"].(map[string]any)
	if _, exists := ollamaModels["tag-a"]; !exists {
		t.Fatal("tag-a missing in ollama provider")
	}
}

func TestOpenCodeDoesNotTouchLlamacppWhenNoOllamaConfigured(t *testing.T) {
	s, ocPath := newOpenCodeTestServer(t)
	raw := `{
	"provider": {
		"llamacpp": {
			"options": {
				"baseURL": "http://127.0.0.1:8080/v1"
			},
			"models": {
				"my-model": {}
			}
		}
	}
}`
	if err := os.WriteFile(ocPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	// GET must report no local ollama provider.
	w := s.doOpenCode(t, http.MethodGet, "/api/opencode", nil)
	var state opencodeStateView
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Provider != nil {
		t.Fatalf("expected nil provider when only llamacpp:8080 exists, got %+v", state.Provider)
	}

	// POST /api/opencode/models must fail with 409 and NOT overwrite llamacpp.
	w = s.doOpenCode(t, http.MethodPost, "/api/opencode/models", map[string]any{
		"enabled": []string{"tag-a"},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}

	// llamacpp content preserved.
	after, err := os.ReadFile(ocPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "my-model") || strings.Contains(string(after), "tag-a") {
		t.Fatalf("llamacpp was modified on disk:\n%s", after)
	}
}

func TestIsLoopbackRequest(t *testing.T) {
	cases := []struct {
		remote string
		want   bool
	}{
		{"127.0.0.1:7860", true},
		{"[::1]:7860", true},
		{"localhost:7860", true},
		{"192.168.1.50:7860", false},
		{"10.0.0.7:4444", false},
		{"", true},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/opencode", nil)
		req.RemoteAddr = c.remote
		if got := isLoopbackRequest(req); got != c.want {
			t.Errorf("RemoteAddr=%q: got %v, want %v", c.remote, got, c.want)
		}
	}
}
