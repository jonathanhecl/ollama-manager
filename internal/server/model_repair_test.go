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

func TestParseRepairModelfileUsesEditedValues(t *testing.T) {
	modelfile := `FROM qwen3:latest

SYSTEM """custom system"""

TEMPLATE """custom template"""

PARAMETER num_ctx 4096
PARAMETER temperature 0.2
PARAMETER stop "<|im_end|>"
PARAMETER stop "<|custom|>"
`
	from, system, template, params, err := parseRepairModelfile(modelfile, "qwen3:latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	if from != "qwen3:latest" {
		t.Fatalf("from = %q", from)
	}
	if system != "custom system" {
		t.Fatalf("system = %q", system)
	}
	if template != "custom template" {
		t.Fatalf("template = %q", template)
	}
	if params["num_ctx"] != 4096 || params["temperature"] != 0.2 {
		t.Fatalf("params = %#v", params)
	}
	stops, ok := params["stop"].([]string)
	if !ok || strings.Join(stops, ",") != "<|im_end|>,<|custom|>" {
		t.Fatalf("stops = %#v", params["stop"])
	}
}

func TestParseRepairModelfileRejectsDifferentBase(t *testing.T) {
	_, _, _, _, err := parseRepairModelfile("FROM other:latest\n", "qwen3:latest", nil)
	if err == nil {
		t.Fatal("expected different FROM to be rejected")
	}
}

func TestBuildModelRepairPreviewKeepsExistingTemplateByDefault(t *testing.T) {
	show := &ollama.ShowResponse{
		Template: "{{ .Prompt }}",
	}
	preview, err := buildModelRepairPreview("base:latest", show, modelRepairRequest{
		Capabilities:  []string{"tools"},
		ContextPreset: "safe",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview.Modelfile, "TEMPLATE") {
		t.Fatalf("expected inherited template, got:\n%s", preview.Modelfile)
	}
	if !strings.Contains(preview.Modelfile, "SYSTEM") {
		t.Fatalf("expected system overlay, got:\n%s", preview.Modelfile)
	}
	if preview.Template != "" {
		t.Fatalf("template = %q", preview.Template)
	}
	if preview.System == "" {
		t.Fatal("expected system overlay")
	}
}

func TestRepairApplyCreatesFixedModel(t *testing.T) {
	var created struct {
		Model      string         `json:"model"`
		From       string         `json:"from"`
		System     string         `json:"system"`
		Template   string         `json:"template"`
		Parameters map[string]any `json:"parameters"`
		Modelfile  string         `json:"modelfile"`
		Stream     bool           `json:"stream"`
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
	body := bytes.NewBufferString(`{"model":"qwen3:latest","capabilities":["tools"],"template_preset":"qwen35","context_preset":"safe","temperature_preset":"tools","modelfile":"FROM qwen3:latest\n\nTEMPLATE \"\"\"edited {{ range .Tools }}template{{ end }}\"\"\"\n\nPARAMETER num_ctx 4096\nPARAMETER temperature 0.2\nPARAMETER stop \"<|edited|>\"\n","confirm":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/model-repair/apply", body)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if created.Model != "qwen3:fixed" {
		t.Fatalf("created model = %q", created.Model)
	}
	if created.From != "qwen3:latest" {
		t.Fatalf("created from = %q", created.From)
	}
	if !strings.Contains(created.Template, "edited {{ range .Tools }}template") {
		t.Fatalf("created template = %s", created.Template)
	}
	if got := created.Parameters["temperature"]; got != float64(0.2) {
		t.Fatalf("temperature = %#v", got)
	}
	if got := created.Parameters["num_ctx"]; got != float64(4096) {
		t.Fatalf("num_ctx = %#v", got)
	}
	if !strings.Contains(created.Modelfile, "edited") {
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

func TestDeleteModelRejectsInvalidReason(t *testing.T) {
	calledDelete := false
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/delete" {
			calledDelete = true
		}
		http.NotFound(w, r)
	}))
	defer ollamaSrv.Close()

	srv := newTestServer(t, ollamaSrv.URL)
	req := httptest.NewRequest(http.MethodDelete, "/api/models/"+url.PathEscape("qwen3:latest"), strings.NewReader(`{"reason":"invalid_reason"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if calledDelete {
		t.Fatalf("ollama delete should not be called on invalid reason")
	}
}

func TestDeleteModelStoresUninstallReason(t *testing.T) {
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/delete":
			writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
		case "/api/tags":
			writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaSrv.Close()

	srv := newTestServer(t, ollamaSrv.URL)
	req := httptest.NewRequest(http.MethodDelete, "/api/models/"+url.PathEscape("qwen3:latest"), strings.NewReader(`{"reason":"too_slow"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}

	hReq := httptest.NewRequest(http.MethodGet, "/api/download-history/"+url.PathEscape("qwen3:latest"), nil)
	hRec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(hRec, hReq)
	if hRec.Code != http.StatusOK {
		t.Fatalf("history status = %d, body = %s", hRec.Code, hRec.Body.String())
	}
	var out map[string]any
	if err := json.NewDecoder(hRec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	uninstall, ok := out["uninstall"].(map[string]any)
	if !ok {
		t.Fatalf("uninstall payload missing: %#v", out)
	}
	if uninstall["reason"] != "too_slow" {
		t.Fatalf("uninstall reason = %#v", uninstall["reason"])
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
