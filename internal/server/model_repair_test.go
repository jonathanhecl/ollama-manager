package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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

func TestBuildModelRepairPreviewLFM2WithToolsUsesModernTemplate(t *testing.T) {
	originalModelfile := `FROM hf.co/LiquidAI/LFM2.5-8B-A1B-GGUF:Q4_K_M
TEMPLATE """{{ if .System }}<|startoftext|><|im_start|>system
{{ .System }}
{{ end }}{{ if .Prompt }}<|im_start|>user
{{ .Prompt }}
{{ end }}<|im_start|>assistant
{{ .Response }}
"""
PARAMETER stop <|startoftext|>
PARAMETER stop <|im_start|>
PARAMETER stop  ""
`
	show := &ollama.ShowResponse{
		Capabilities: []string{"completion"},
		Template:     `{{ if .System }}<|startoftext|><|im_start|>system\n{{ .System }}\n{{ end }}{{ if .Prompt }}<|im_start|>user\n{{ .Prompt }}\n{{ end }}<|im_start|>assistant\n{{ .Response }}\n`,
		Modelfile:    originalModelfile,
		ModelInfo: map[string]json.RawMessage{
			"general.architecture": json.RawMessage(`"lfm2moe"`),
		},
	}
	preview, err := buildModelRepairPreview("lfm2.5:latest", show, modelRepairRequest{
		Capabilities:      []string{"tools", "thinking"},
		TemplatePreset:    "keep",
		ContextPreset:     "safe",
		TemperaturePreset: "keep",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.TargetName != "lfm2.5:fixed" {
		t.Fatalf("target = %q", preview.TargetName)
	}
	// When tools are requested, use the official approach: TEMPLATE {{ .Prompt }} + RENDERER + PARSER
	if !strings.Contains(preview.Modelfile, "PARSER lfm2-thinking") {
		t.Fatalf("missing PARSER lfm2-thinking:\n%s", preview.Modelfile)
	}
	if !strings.Contains(preview.Modelfile, "RENDERER lfm2-thinking") {
		t.Fatalf("missing RENDERER lfm2-thinking:\n%s", preview.Modelfile)
	}
	if strings.Contains(preview.Modelfile, "PARSER lfm2\n") {
		t.Fatalf("should use lfm2-thinking when thinking capability is requested:\n%s", preview.Modelfile)
	}
	// Must use TEMPLATE {{ .Prompt }} like the official model
	if !strings.Contains(preview.Modelfile, "TEMPLATE {{ .Prompt }}") {
		t.Fatalf("expected TEMPLATE {{ .Prompt }} like official model:\n%s", preview.Modelfile)
	}
	// num_ctx should be injected because contextPreset is safe
	if !strings.Contains(preview.Modelfile, "PARAMETER num_ctx 2048") {
		t.Fatalf("missing num_ctx override:\n%s", preview.Modelfile)
	}
}

func TestBuildModelRepairPreviewLFM2WithoutToolsPreservesOriginal(t *testing.T) {
	originalModelfile := `FROM lfm2.5:latest
TEMPLATE """{{ .Prompt }}"""
`
	show := &ollama.ShowResponse{
		Capabilities: []string{"completion"},
		Template:     `{{ .Prompt }}`,
		Modelfile:    originalModelfile,
		ModelInfo: map[string]json.RawMessage{
			"general.architecture": json.RawMessage(`"lfm2"`),
		},
	}
	preview, err := buildModelRepairPreview("lfm2.5:latest", show, modelRepairRequest{
		Capabilities:   []string{"thinking"},
		TemplatePreset: "keep",
		ContextPreset:  "keep",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Modelfile, "PARSER lfm2-thinking") {
		t.Fatalf("expected PARSER lfm2-thinking:\n%s", preview.Modelfile)
	}
	// Without tools, original template must be preserved
	if !strings.Contains(preview.Modelfile, "{{ .Prompt }}") {
		t.Fatalf("original template not preserved when tools not requested:\n%s", preview.Modelfile)
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
	if preview.Template != "" {
		t.Fatalf("template = %q", preview.Template)
	}
}

func TestRepairApplyCreatesFixedModel(t *testing.T) {
	var created struct {
		Model      string            `json:"model"`
		From       string            `json:"from"`
		Files      map[string]string `json:"files"`
		System     string            `json:"system"`
		Template   string            `json:"template"`
		Parameters map[string]any    `json:"parameters"`
		Renderer   string            `json:"renderer"`
		Parser     string            `json:"parser"`
		Modelfile  string            `json:"modelfile"`
		Stream     bool              `json:"stream"`
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
	for _, want := range []string{
		"FROM qwen3:latest",
		"edited {{ range .Tools }}template",
		"PARAMETER num_ctx 4096",
		"PARAMETER temperature 0.2",
	} {
		if !strings.Contains(created.Modelfile, want) {
			t.Fatalf("created Modelfile missing %q:\n%s", want, created.Modelfile)
		}
	}
	var out map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["replaced"] != true {
		t.Fatalf("replaced = %#v", out["replaced"])
	}
}

func TestRepairApplyBlobFromUsesFilesCreateRequest(t *testing.T) {
	var created struct {
		Model string            `json:"model"`
		From  string            `json:"from"`
		Files map[string]string `json:"files"`
	}
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			writeJSON(w, http.StatusOK, map[string]any{
				"modelfile": "FROM C:\\\\Ollama\\\\blobs\\\\sha256-abc123\nFROM C:\\\\Ollama\\\\blobs\\\\sha256-projector\n",
				"model_info": map[string]any{
					"general.architecture": "gemma4",
				},
			})
		case "/api/tags":
			writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{{"name": "gemma4:latest"}}})
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
	body := bytes.NewBufferString(`{"model":"gemma4:latest","capabilities":["completion"],"template_preset":"gemma4","context_preset":"keep","temperature_preset":"keep","fix_load":true,"modelfile":"FROM C:\\Ollama\\blobs\\sha256-abc123\n\nTEMPLATE \"\"\"{{ .Prompt }}\"\"\"\n\nRENDERER gemma4\nPARSER gemma4\n","confirm":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/model-repair/apply", body)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if created.From != "" {
		t.Fatalf("created from = %q", created.From)
	}
	if got := created.Files["model.gguf"]; got != "sha256:abc123" {
		t.Fatalf("created files = %#v", created.Files)
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

	// Query for a DIFFERENT quant of the same repo (e.g. qwen3:7b-instruct-q4_0)
	hReq2 := httptest.NewRequest(http.MethodGet, "/api/download-history/"+url.PathEscape("qwen3:7b-instruct-q4_0"), nil)
	hRec2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(hRec2, hReq2)
	if hRec2.Code != http.StatusOK {
		t.Fatalf("history status = %d, body = %s", hRec2.Code, hRec2.Body.String())
	}
	var out2 map[string]any
	if err := json.NewDecoder(hRec2.Body).Decode(&out2); err != nil {
		t.Fatal(err)
	}
	if out2["repo_base"] != "qwen3" {
		t.Fatalf("expected repo_base qwen3, got %v", out2["repo_base"])
	}
	related, ok := out2["related_models"].([]any)
	if !ok || len(related) == 0 {
		t.Fatalf("expected related_models to contain previously uninstalled qwen3:latest, got: %#v", out2)
	}
}

func TestBuildModelRepairPreviewFiltersMarkdownStops(t *testing.T) {
	show := &ollama.ShowResponse{
		Capabilities: []string{"completion"},
		Modelfile: `FROM hf.co/bartowski/Laguna-XS-2.1-GGUF:Q2_K_L
TEMPLATE "{{ if .Prompt }}<user>{{ .Prompt }}</user>\n{{ end }}<assistant></think>{{ .Response }}</assistant>\n"
PARAMETER stop <system>
PARAMETER stop <user>
PARAMETER stop <assistant>
PARAMETER stop ###
`,
		ModelInfo: map[string]json.RawMessage{
			"general.architecture": json.RawMessage(`"laguna"`),
		},
	}
	preview, err := buildModelRepairPreview("laguna:latest", show, modelRepairRequest{
		Capabilities:      []string{"completion"},
		TemplatePreset:    "keep",
		ContextPreset:     "safe",
		TemperaturePreset: "keep",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview.Modelfile, `PARAMETER stop "###"`) {
		t.Fatalf("Modelfile should not re-declare the Markdown stop:\n%s", preview.Modelfile)
	}
	for _, want := range []string{
		`PARAMETER stop "<system>"`,
		`PARAMETER stop "<user>"`,
		`PARAMETER stop "<assistant>"`,
	} {
		if !strings.Contains(preview.Modelfile, want) {
			t.Fatalf("Modelfile missing %q:\n%s", want, preview.Modelfile)
		}
	}
	stops, ok := preview.Parameters["stop"].([]string)
	if !ok || strings.Join(stops, ",") != "<system>,<user>,<assistant>" {
		t.Fatalf("stops = %#v", preview.Parameters["stop"])
	}
	foundWarning := false
	for _, w := range preview.Warnings {
		if strings.Contains(w, "Markdown punctuation") && strings.Contains(w, "###") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("missing Markdown stop warning: %#v", preview.Warnings)
	}
}

func TestBuildModelRepairPreviewKeepsTextualStops(t *testing.T) {
	show := &ollama.ShowResponse{
		Capabilities: []string{"completion"},
		Modelfile: `FROM some-model:latest
PARAMETER stop "### Response:"
PARAMETER stop ###
`,
	}
	preview, err := buildModelRepairPreview("some-model:latest", show, modelRepairRequest{
		Capabilities:      []string{"completion"},
		TemplatePreset:    "keep",
		ContextPreset:     "safe",
		TemperaturePreset: "keep",
	})
	if err != nil {
		t.Fatal(err)
	}
	stops, _ := preview.Parameters["stop"].([]string)
	if strings.Join(stops, ",") != "### Response:" {
		t.Fatalf("stops = %#v", preview.Parameters["stop"])
	}
	if strings.Contains(preview.Modelfile, `PARAMETER stop "###"`) {
		t.Fatalf("Modelfile should not contain bare ### stop:\n%s", preview.Modelfile)
	}
	if !strings.Contains(preview.Modelfile, `PARAMETER stop "### Response:"`) {
		t.Fatalf("Modelfile should keep textual stop:\n%s", preview.Modelfile)
	}
}

func TestBuildModelRepairPreviewPresetStopsReplaceBaseMarkdownStops(t *testing.T) {
	show := &ollama.ShowResponse{
		Capabilities: []string{"completion"},
		Modelfile: `FROM qwen3:latest
PARAMETER stop ###
PARAMETER stop ---
`,
		ModelInfo: map[string]json.RawMessage{
			"general.architecture": json.RawMessage(`"qwen3"`),
		},
	}
	preview, err := buildModelRepairPreview("qwen3:latest", show, modelRepairRequest{
		Capabilities:      []string{"completion"},
		TemplatePreset:    "qwen35",
		ContextPreset:     "safe",
		TemperaturePreset: "keep",
	})
	if err != nil {
		t.Fatal(err)
	}
	stops, _ := preview.Parameters["stop"].([]string)
	if strings.Join(stops, ",") != "<|im_end|>" {
		t.Fatalf("stops = %#v", preview.Parameters["stop"])
	}
	if strings.Contains(preview.Modelfile, `PARAMETER stop "###"`) || strings.Contains(preview.Modelfile, `PARAMETER stop "---"`) {
		t.Fatalf("Modelfile should not re-declare base Markdown stops:\n%s", preview.Modelfile)
	}
	foundWarning := false
	for _, w := range preview.Warnings {
		if strings.Contains(w, "Markdown punctuation") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("missing Markdown stop warning: %#v", preview.Warnings)
	}
}

func TestIsMarkdownPunctuationStop(t *testing.T) {
	for s, want := range map[string]bool{
		"###":           true,
		"---":           true,
		"***":           true,
		"___":           true,
		"#-":            true,
		"":              false,
		"### Response:": false,
		"<user>":        false,
		"<|im_end|>":    false,
		"-- text":       false,
	} {
		if got := isMarkdownPunctuationStop(s); got != want {
			t.Fatalf("isMarkdownPunctuationStop(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestBuildModelRepairPreviewCustomStops(t *testing.T) {
	show := &ollama.ShowResponse{
		Capabilities: []string{"completion"},
		Modelfile:    "FROM base:latest\nPARAMETER stop \"<|bad|>\"\n",
	}
	preview, err := buildModelRepairPreview("base:latest", show, modelRepairRequest{
		Capabilities:   []string{"completion"},
		TemplatePreset: "keep",
		Stops:          []string{"<|im_end|>", "<|eot|>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`PARAMETER stop "<|im_end|>"`, `PARAMETER stop "<|eot|>"`} {
		if !strings.Contains(preview.Modelfile, want) {
			t.Fatalf("missing %q:\n%s", want, preview.Modelfile)
		}
	}
	if strings.Contains(preview.Modelfile, `"<|bad|>"`) {
		t.Fatalf("base stop should be replaced by custom stops:\n%s", preview.Modelfile)
	}
	stops, ok := preview.Parameters["stop"].([]string)
	if !ok || strings.Join(stops, ",") != "<|im_end|>,<|eot|>" {
		t.Fatalf("stops = %#v", preview.Parameters["stop"])
	}
	if strings.Join(preview.BaseStops, ",") != "<|bad|>" {
		t.Fatalf("base stops = %#v", preview.BaseStops)
	}
}

func TestBuildModelRepairPreviewContextDefaultsToKeep(t *testing.T) {
	show := &ollama.ShowResponse{Capabilities: []string{"completion"}}
	preview, err := buildModelRepairPreview("base:latest", show, modelRepairRequest{
		Capabilities: []string{"completion"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview.Modelfile, "PARAMETER num_ctx") {
		t.Fatalf("expected no num_ctx when context preset is omitted, got:\n%s", preview.Modelfile)
	}
}

func TestResolveProjectorURL(t *testing.T) {
	cases := map[string]string{
		"https://huggingface.co/u/r/resolve/main/mm.gguf": "https://huggingface.co/u/r/resolve/main/mm.gguf",
		"https://huggingface.co/u/r/blob/main/mm.gguf":    "https://huggingface.co/u/r/resolve/main/mm.gguf",
		"hf.co/u/r/mm.gguf":                               "https://huggingface.co/u/r/resolve/main/mm.gguf",
		"u/r/mm.gguf":                                     "https://huggingface.co/u/r/resolve/main/mm.gguf",
	}
	for in, want := range cases {
		got, err := resolveProjectorURL(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
	if _, err := resolveProjectorURL("https://evil.com/u/r/resolve/main/mm.gguf"); err == nil {
		t.Fatal("expected non-HuggingFace host to be rejected")
	}
	if _, err := resolveProjectorURL("just-one-token"); err == nil {
		t.Fatal("expected malformed shorthand to be rejected")
	}
}

func TestRepairApplyWithProjectorUsesFiles(t *testing.T) {
	mmprojBytes := []byte("fake mmproj gguf data")
	mmprojHex := hex.EncodeToString(func() []byte { s := sha256.Sum256(mmprojBytes); return s[:] }())

	prev := projectorHTTPClient
	projectorHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(mmprojBytes)),
			Header:     http.Header{},
		}, nil
	})}
	defer func() { projectorHTTPClient = prev }()

	var created struct {
		Model string            `json:"model"`
		From  string            `json:"from"`
		Files map[string]string `json:"files"`
	}
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/show":
			writeJSON(w, http.StatusOK, map[string]any{
				"modelfile":    "FROM /x/blobs/sha256-abc123\n",
				"capabilities": []string{"completion"},
				"model_info":   map[string]any{"general.architecture": "qwen3"},
			})
		case r.URL.Path == "/api/tags":
			writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{{"name": "qwen3:latest"}}})
		case r.URL.Path == "/api/create":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
		case strings.HasPrefix(r.URL.Path, "/api/blobs/"):
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaSrv.Close()

	srv := newTestServer(t, ollamaSrv.URL)
	body := bytes.NewBufferString(`{"model":"qwen3:latest","capabilities":["completion"],"template_preset":"keep","context_preset":"keep","temperature_preset":"keep","projector":"https://huggingface.co/u/r/resolve/main/mmproj.gguf","modelfile":"FROM /x/blobs/sha256-abc123\n","confirm":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/model-repair/apply", body)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if created.From != "" {
		t.Fatalf("created from = %q, want empty", created.From)
	}
	if got := created.Files["model.gguf"]; got != "sha256:abc123" {
		t.Fatalf("files[model.gguf] = %q", got)
	}
	if got := created.Files["mmproj.gguf"]; got != "sha256:"+mmprojHex {
		t.Fatalf("files[mmproj.gguf] = %q, want %q", got, "sha256:"+mmprojHex)
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
