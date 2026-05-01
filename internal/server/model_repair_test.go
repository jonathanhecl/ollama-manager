package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gense/ollama-manager/internal/config"
	"github.com/gense/ollama-manager/internal/ollama"
)

func TestBuildModelRepairPreviewQwenToolsThinking(t *testing.T) {
	show := &ollama.ShowResponse{
		Capabilities: []string{"completion"},
		ModelInfo: map[string]json.RawMessage{
			"general.architecture": json.RawMessage(`"qwen3"`),
		},
	}
	preview, err := buildModelRepairPreview("qwen3:latest", show, modelRepairRequest{
		Capabilities:      []string{"tools", "thinking"},
		TemplatePreset:    "qwen35",
		ContextPreset:     "thinking",
		TemperaturePreset: "tools",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.TargetName != "qwen3:fixed" {
		t.Fatalf("target = %q", preview.TargetName)
	}
	for _, want := range []string{
		"FROM qwen3:latest",
		"{{ range .Tools }}",
		"{{ range .ToolCalls }}",
		"RENDERER qwen3.5",
		"PARSER qwen3.5",
		"PARAMETER num_ctx 16384",
		"PARAMETER temperature 0.0",
		`PARAMETER stop "<|im_end|>"`,
	} {
		if !strings.Contains(preview.Modelfile, want) {
			t.Fatalf("Modelfile missing %q:\n%s", want, preview.Modelfile)
		}
	}
}

func TestBuildModelRepairPreviewRejectsFixedSource(t *testing.T) {
	_, err := buildModelRepairPreview("qwen3:fixed", &ollama.ShowResponse{}, modelRepairRequest{})
	if err == nil {
		t.Fatal("expected fixed source to be rejected")
	}
}

func TestRepairApplyCreatesFixedModel(t *testing.T) {
	var created struct {
		Model     string `json:"model"`
		Modelfile string `json:"modelfile"`
		Stream    bool   `json:"stream"`
	}
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			writeJSON(w, http.StatusOK, map[string]any{
				"capabilities": []string{"completion"},
				"model_info": map[string]any{
					"general.architecture": "qwen3",
				},
			})
		case "/api/tags":
			writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{
				{"name": "qwen3:latest"},
				{"name": "qwen3:fixed"},
			}})
		case "/api/create":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaSrv.Close()

	srv := newTestServer(t, ollamaSrv.URL)
	body := bytes.NewBufferString(`{"model":"qwen3:latest","capabilities":["tools"],"template_preset":"qwen35","context_preset":"safe","temperature_preset":"tools","confirm":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/model-repair/apply", body)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if created.Model != "qwen3:fixed" {
		t.Fatalf("created model = %q", created.Model)
	}
	if !strings.Contains(created.Modelfile, "FROM qwen3:latest") {
		t.Fatalf("created Modelfile = %s", created.Modelfile)
	}
	var out map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["replaced"] != true {
		t.Fatalf("replaced = %#v", out["replaced"])
	}
}

func TestDeleteBaseAlsoDeletesFixed(t *testing.T) {
	var deleted []string
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/delete":
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			deleted = append(deleted, body.Name)
			writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
		case "/api/tags":
			writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{
				{"name": "qwen3:latest"},
				{"name": "qwen3:fixed"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaSrv.Close()

	srv := newTestServer(t, ollamaSrv.URL)
	req := httptest.NewRequest(http.MethodDelete, "/api/models/"+url.PathEscape("qwen3:latest"), nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Join(deleted, ",") != "qwen3:latest,qwen3:fixed" {
		t.Fatalf("deleted = %#v", deleted)
	}
}

func newTestServer(t *testing.T, ollamaURL string) *Server {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.OllamaURL = ollamaURL
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	webRoot := os.DirFS(filepath.Join(wd, "..", "..", "web"))
	srv, err := New(cfg, ollama.New(ollamaURL), webRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.jobs.Shutdown)
	return srv
}
