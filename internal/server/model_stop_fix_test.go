package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gense/ollama-manager/internal/ollama"
)

// stopFixFake mimics the parts of the Ollama API the stop-fix engine uses:
// /api/show, /api/chat (with stop simulation), /api/create and /api/generate
// (unload). Creating the model rewrites the stop list it reports and applies,
// so the verification probe after an apply sees the rebuilt model.
type stopFixFake struct {
	t               *testing.T
	full            string
	template        string
	base            string
	mu              sync.Mutex
	failAfterCreate bool
	stops           []string
	called          [][]string
	createdAt       []ollama.CreateRequest
}

const stopFixFakeTemplate = `{{- range .Messages }}<|im_start|>{{ .Role }}
{{ .Content }}<|im_end|>
{{ end -}}<|im_start|>assistant
`

func newStopFixFake(t *testing.T, full string, stops []string) *stopFixFake {
	t.Helper()
	return &stopFixFake{
		t:        t,
		full:     full,
		stops:    append([]string(nil), stops...),
		template: stopFixFakeTemplate,
		base: `FROM /var/lib/ollama/models/blobs/sha256-aaaabbbb
TEMPLATE """` + stopFixFakeTemplate + `"""
RENDERER qwen3.5
PARSER qwen3.5
PARAMETER temperature 0.7
PARAMETER top_p 0.95
LICENSE """MIT"""
`,
	}
}

func (f *stopFixFake) parametersLocked() string {
	var lines []string
	for _, stop := range f.stops {
		lines = append(lines, "stop                           "+jsonString(stop))
	}
	lines = append(lines, "temperature                    0.7", "top_p                          0.95")
	return strings.Join(lines, "\n")
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (f *stopFixFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/show":
		f.mu.Lock()
		params := f.parametersLocked()
		modelfile := rewriteModelfileStops(f.base, f.stops)
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"modelfile":    modelfile,
			"template":     f.template,
			"parameters":   params,
			"license":      "MIT",
			"capabilities": []string{"completion", "tools"},
			"details":      map[string]any{"family": "qwen3"},
		})
	case "/api/chat":
		var body struct {
			Options map[string]any `json:"options"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			f.t.Fatalf("decode chat body: %v", err)
		}
		f.mu.Lock()
		stops := append([]string(nil), f.stops...)
		f.mu.Unlock()
		if v, ok := body.Options["stop"]; ok {
			stops = nil
			if list, ok := v.([]any); ok {
				for _, item := range list {
					if s, ok := item.(string); ok {
						stops = append(stops, s)
					}
				}
			}
		}
		f.mu.Lock()
		f.called = append(f.called, stops)
		f.mu.Unlock()
		f.mu.Lock()
		broken := f.failAfterCreate && len(f.createdAt) > 0
		f.mu.Unlock()
		content := simulateStops(f.full, stops)
		if broken {
			content = ""
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"model":       "test-model",
			"done":        true,
			"done_reason": "stop",
			"eval_count":  len(strings.Fields(content)),
			"message":     map[string]any{"role": "assistant", "content": content},
		})
	case "/api/create":
		var req ollama.CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			f.t.Fatalf("decode create body: %v", err)
		}
		f.mu.Lock()
		f.createdAt = append(f.createdAt, req)
		switch v := req.Parameters["stop"].(type) {
		case []any:
			f.stops = nil
			for _, item := range v {
				f.stops = append(f.stops, item.(string))
			}
		case []string:
			f.stops = append([]string(nil), v...)
		}
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	case "/api/generate":
		writeJSON(w, http.StatusOK, map[string]any{"done": true, "done_reason": "unload"})
	default:
		http.NotFound(w, r)
	}
}

func simulateStops(text string, stops []string) string {
	cut := -1
	for _, stop := range stops {
		if idx := strings.Index(text, stop); idx >= 0 && (cut == -1 || idx < cut) {
			cut = idx
		}
	}
	if cut < 0 {
		return text
	}
	return strings.TrimSpace(text[:cut])
}

func newStopFixTestServer(t *testing.T, fake *stopFixFake, backupRoot string) *Server {
	t.Helper()
	ts := httptest.NewServer(fake)
	t.Cleanup(ts.Close)
	return &Server{ollama: ollama.New(ts.URL), stopFixBackupRoot: backupRoot}
}

func TestStopFixPreviewReproducesBlankReplyAndProposesFix(t *testing.T) {
	fake := newStopFixFake(t, "Hello! How can I help you today?<|im_end|><|im_start|>user\nhi", []string{"Hello", "<|im_start|>user"})
	srv := newStopFixTestServer(t, fake, t.TempDir())

	preview, _, err := srv.buildStopFixPreview(t.Context(), "test-model", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Diagnosis != stopFixDiagnosisStops || !preview.NeedsFix {
		t.Fatalf("diagnosis = %q needs_fix=%v, want stops/true", preview.Diagnosis, preview.NeedsFix)
	}
	if !preview.CurrentProbe.Empty {
		t.Fatalf("current probe should reproduce the blank reply, got %q", preview.CurrentProbe.Content)
	}
	if preview.NoStopProbe.Empty {
		t.Fatal("no-stop probe should answer")
	}
	if !slices.Contains(preview.ProposedStops, "<|im_end|>") {
		t.Fatalf("proposed stops %v should contain <|im_end|>", preview.ProposedStops)
	}
	if slices.Contains(preview.ProposedStops, "Hello") {
		t.Fatalf("proposed stops %v must drop the stop that blanks the reply", preview.ProposedStops)
	}
	if !slices.Contains(preview.RemovedStops, "Hello") {
		t.Fatalf("removed stops %v should list Hello", preview.RemovedStops)
	}
	if strings.Contains(preview.Modelfile, `PARAMETER stop "Hello"`) {
		t.Fatalf("preview Modelfile still declares the broken stop:\n%s", preview.Modelfile)
	}
	var hello *stopFixCandidate
	for i := range preview.Candidates {
		if preview.Candidates[i].Stop == "Hello" {
			hello = &preview.Candidates[i]
		}
	}
	if hello == nil || hello.Verdict != stopFixVerdictCuts {
		t.Fatalf("candidate Hello = %+v, want verdict cuts", hello)
	}
	if hello.Source != "current" {
		t.Fatalf("candidate source = %q", hello.Source)
	}
}

func TestStopFixPreviewHealthyModelDoesNotProbeCandidates(t *testing.T) {
	fake := newStopFixFake(t, "Sure, I can help with that.", []string{"Hello"})
	srv := newStopFixTestServer(t, fake, t.TempDir())

	preview, _, err := srv.buildStopFixPreview(t.Context(), "test-model", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Diagnosis != stopFixDiagnosisOK || preview.NeedsFix {
		t.Fatalf("diagnosis = %q needs_fix=%v, want ok/false", preview.Diagnosis, preview.NeedsFix)
	}
	if len(fake.called) != 1 {
		t.Fatalf("healthy model should run one probe, ran %d", len(fake.called))
	}
}

func TestStopFixPreviewReportsTemplateProblem(t *testing.T) {
	fake := newStopFixFake(t, "", nil)
	srv := newStopFixTestServer(t, fake, t.TempDir())

	preview, _, err := srv.buildStopFixPreview(t.Context(), "test-model", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Diagnosis != stopFixDiagnosisTemplate || preview.NeedsFix {
		t.Fatalf("diagnosis = %q needs_fix=%v, want template/false", preview.Diagnosis, preview.NeedsFix)
	}
	if len(preview.Warnings) == 0 {
		t.Fatal("template diagnosis should carry a warning")
	}
}

func TestStopFixPreviewClearsStopsWhenOnlyTheyBreakTheReply(t *testing.T) {
	fake := newStopFixFake(t, "Hi there, how are you?", []string{"Hi"})
	srv := newStopFixTestServer(t, fake, t.TempDir())

	preview, _, err := srv.buildStopFixPreview(t.Context(), "test-model", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.NeedsFix || preview.Diagnosis != stopFixDiagnosisStops {
		t.Fatalf("want a stop fix, got %+v", preview)
	}
	if len(preview.ProposedStops) != 0 {
		t.Fatalf("proposed stops = %v, want none: the model ends on its own token", preview.ProposedStops)
	}
	if !slices.Equal(preview.RemovedStops, []string{"Hi"}) {
		t.Fatalf("removed = %v", preview.RemovedStops)
	}
}

func TestStopFixPreviewUnsupportedWithoutBlob(t *testing.T) {
	fake := newStopFixFake(t, "hello", nil)
	fake.base = `FROM cloud-model:latest
TEMPLATE """{{ .Prompt }}"""
`
	srv := newStopFixTestServer(t, fake, t.TempDir())

	preview, _, err := srv.buildStopFixPreview(t.Context(), "test-model", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Diagnosis != stopFixDiagnosisUnsupported || preview.Supported {
		t.Fatalf("diagnosis = %q supported=%v", preview.Diagnosis, preview.Supported)
	}
	if len(fake.called) != 0 {
		t.Fatal("unsupported model should not be probed")
	}
}

func TestClassifyStopCandidate(t *testing.T) {
	base := stopFixProbeResult{Content: "Hello! How can I help you today?<|im_end|>"}
	cases := []struct {
		name   string
		probe  stopFixProbeResult
		expect string
	}{
		{"blank", stopFixProbeResult{Content: ""}, stopFixVerdictCuts},
		{"whitespace", stopFixProbeResult{Content: "  \n"}, stopFixVerdictCuts},
		{"full answer", stopFixProbeResult{Content: "Hello! How can I help you today?"}, stopFixVerdictSafe},
		{"cut short", stopFixProbeResult{Content: "Hello! "}, stopFixVerdictPartial},
		{"changed answer", stopFixProbeResult{Content: "Sure, here is"}, stopFixVerdictCuts},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict, _ := classifyStopCandidate(base, tc.probe)
			if verdict != tc.expect {
				t.Fatalf("verdict = %q, want %q", verdict, tc.expect)
			}
		})
	}
}

func TestExtractStopCandidates(t *testing.T) {
	got := extractStopCandidates("Hi<|im_end|><|im_start|>user\nmore</s> and [INST] <turn|>")
	for _, want := range []string{"<|im_end|>", "<|im_start|>", "</s>", "[INST]", "<turn|>"} {
		if !slices.Contains(got, want) {
			t.Fatalf("candidates %v missing %q", got, want)
		}
	}
}

func TestRewriteModelfileStopsKeepsEveryOtherDirective(t *testing.T) {
	modelfile := `FROM /blobs/sha256-aaa
TEMPLATE """{{ .Prompt }}"""
RENDERER qwen3.5
PARSER qwen3.5
PARAMETER stop "broken"
PARAMETER temperature 0.7
SYSTEM """be nice"""
`
	got := rewriteModelfileStops(modelfile, []string{"<|im_end|>"})
	if strings.Contains(got, `"broken"`) {
		t.Fatalf("old stop kept:\n%s", got)
	}
	for _, want := range []string{`PARAMETER stop "<|im_end|>"`, "TEMPLATE", "RENDERER qwen3.5", "PARSER qwen3.5", "PARAMETER temperature 0.7", "SYSTEM"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rewritten Modelfile missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "FROM /blobs/sha256-aaa\n\nPARAMETER stop") {
		t.Fatalf("stop line should follow FROM:\n%s", got)
	}

	cleared := rewriteModelfileStops(modelfile, nil)
	if strings.Contains(cleared, "PARAMETER stop") {
		t.Fatalf("clearing stops left a stop line:\n%s", cleared)
	}
}

func TestBuildStopFixCreateRequestPreservesModelConfig(t *testing.T) {
	fake := newStopFixFake(t, "Hello", []string{"Hello"})
	show := &ollama.ShowResponse{
		Modelfile:  rewriteModelfileStops(fake.base, fake.stops),
		Template:   fake.template,
		Parameters: fake.parametersLocked(),
		License:    "MIT",
	}
	req, err := buildStopFixCreateRequest("test-model", show, []string{"<|im_end|>"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "test-model" || req.Files["model.gguf"] != "sha256:aaaabbbb" {
		t.Fatalf("unexpected create request: %+v", req)
	}
	if req.Renderer != "qwen3.5" || req.Parser != "qwen3.5" {
		t.Fatalf("renderer/parser lost: %q/%q", req.Renderer, req.Parser)
	}
	if req.Template != fake.template {
		t.Fatal("template not preserved")
	}
	if req.License != "MIT" {
		t.Fatalf("license = %v", req.License)
	}
	stops, _ := req.Parameters["stop"].([]string)
	if !slices.Equal(stops, []string{"<|im_end|>"}) {
		t.Fatalf("parameters stop = %#v", req.Parameters["stop"])
	}
	if req.Parameters["temperature"] != 0.7 {
		t.Fatalf("temperature lost: %#v", req.Parameters["temperature"])
	}

	show.Modelfile = "FROM /blobs/sha256-1111\nTEMPLATE \"\"\"x\"\"\"\nPARAMETER stop \"a\"\n" +
		"FROM /blobs/sha256-2222\n"
	show.ProjectorInfo = map[string]json.RawMessage{"projector_type": json.RawMessage(`"mmproj"`)}
	req, err = buildStopFixCreateRequest("vision-model", show, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Files["mmproj.gguf"] != "sha256:2222" {
		t.Fatalf("projector blob missing: %+v", req.Files)
	}
	if stops, ok := req.Parameters["stop"].([]string); !ok || len(stops) != 0 {
		t.Fatalf("cleared stops should be an explicit empty list, got %#v", req.Parameters["stop"])
	}

	show.Modelfile = "FROM /blobs/sha256-1111\nFROM /blobs/sha256-2222\nFROM /blobs/sha256-3333\n"
	if _, err := buildStopFixCreateRequest("split", show, nil); err == nil {
		t.Fatal("split GGUF should be rejected")
	}

	// Two blobs without a projector are two shards of one split GGUF.
	show.ProjectorInfo = nil
	show.Modelfile = "FROM /blobs/sha256-1111\nFROM /blobs/sha256-2222\n"
	if _, err := buildStopFixCreateRequest("two-shards", show, nil); err == nil {
		t.Fatal("split GGUF without projector should be rejected")
	}
}

func TestApplyStopFixRebuildsAndVerifies(t *testing.T) {
	fake := newStopFixFake(t, "Hello! How can I help you today?<|im_end|><|im_start|>user\nhi", []string{"Hello"})
	srv := newStopFixTestServer(t, fake, t.TempDir())

	preview, _, err := srv.buildStopFixPreview(t.Context(), "test-model", "hi")
	if err != nil {
		t.Fatal(err)
	}
	result, err := srv.applyStopFix(t.Context(), stopFixRequest{
		Model:   "test-model",
		Prompt:  "hi",
		Stops:   &preview.ProposedStops,
		Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.createdAt) != 1 {
		t.Fatalf("create calls = %d", len(fake.createdAt))
	}
	if result.Verify.Empty {
		t.Fatalf("verify probe still blank: %+v", result.Verify)
	}
	if !strings.Contains(result.Verify.Content, "Hello!") {
		t.Fatalf("verify content = %q", result.Verify.Content)
	}
	if result.BackupPath == "" {
		t.Fatal("missing backup path")
	}
	last := fake.called[len(fake.called)-1]
	if !slices.Contains(last, "<|im_end|>") {
		t.Fatalf("verification used stops %v", last)
	}
}

func TestApplyStopFixRestoresTheModelWhenVerificationFails(t *testing.T) {
	fake := newStopFixFake(t, "Hello! How can I help you today?", []string{"Hello"})
	fake.failAfterCreate = true
	srv := newStopFixTestServer(t, fake, t.TempDir())

	preview, _, err := srv.buildStopFixPreview(t.Context(), "test-model", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.NeedsFix {
		t.Fatalf("fixture should need a fix: %+v", preview)
	}
	_, err = srv.applyStopFix(t.Context(), stopFixRequest{
		Model:   "test-model",
		Prompt:  "hi",
		Stops:   &preview.ProposedStops,
		Confirm: true,
	})
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("err = %v, want a restore report", err)
	}
	if len(fake.createdAt) != 2 {
		t.Fatalf("create calls = %d, want the fix plus the restore", len(fake.createdAt))
	}
	var restored []string
	if list, ok := fake.createdAt[1].Parameters["stop"].([]any); ok {
		for _, item := range list {
			restored = append(restored, item.(string))
		}
	}
	if !slices.Equal(restored, []string{"Hello"}) {
		t.Fatalf("restore stops = %#v", fake.createdAt[1].Parameters["stop"])
	}
}

func TestApplyStopFixRequiresConfirmation(t *testing.T) {
	fake := newStopFixFake(t, "Hello", nil)
	srv := newStopFixTestServer(t, fake, t.TempDir())
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"model":"test-model","stops":[]}`)
	srv.handleStopFixApply(rr, httptest.NewRequest(http.MethodPost, "/api/model-stop-fix/apply", body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}
