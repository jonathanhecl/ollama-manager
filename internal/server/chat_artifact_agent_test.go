package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
)

func TestArtifactReplaceInFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "artifact_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "app.js")
	initialContent := "console.log('Hello World');\nfunction test() { return 123; }"
	if err := os.WriteFile(filePath, []byte(initialContent), 0o644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	s := &Server{}
	ctx := context.Background()

	// 1. Test successful replacement
	args, _ := json.Marshal(map[string]string{
		"path":       "app.js",
		"old_string": "return 123;",
		"new_string": "return 456;",
	})
	res, err := s.runArtifactTool(ctx, tempDir, "replace_in_file", args)
	if err != nil {
		t.Fatalf("unexpected error running replace_in_file: %v", err)
	}
	if !strings.Contains(res, "replaced text in app.js") {
		t.Errorf("expected success message, got: %s", res)
	}

	updated, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read updated file: %v", err)
	}
	if !strings.Contains(string(updated), "return 456;") {
		t.Errorf("expected updated content to contain 'return 456;', got: %s", string(updated))
	}

	// 2. Test old_string not found
	argsNotFound, _ := json.Marshal(map[string]string{
		"path":       "app.js",
		"old_string": "non_existent_text",
		"new_string": "new",
	})
	resNotFound, err := s.runArtifactTool(ctx, tempDir, "replace_in_file", argsNotFound)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resNotFound, "Error: old_string not found") {
		t.Errorf("expected not found error, got: %s", resNotFound)
	}
}

func TestHandleArtifactFilesSPAFallback(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "artifact_spa_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ts := "2026-08-03-test"
	artifactDir := filepath.Join("artifacts", ts)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("failed to create artifact dir: %v", err)
	}
	defer os.RemoveAll("artifacts")

	indexPath := filepath.Join(artifactDir, "index.html")
	indexHtml := "<html><head></head><body><h1>SPA Index</h1></body></html>"
	if err := os.WriteFile(indexPath, []byte(indexHtml), 0o644); err != nil {
		t.Fatalf("failed to write index.html: %v", err)
	}

	s := &Server{}
	req := httptest.NewRequest("GET", "/api/artifacts/"+ts+"/dashboard/user/profile", nil)
	req.SetPathValue("rest", ts+"/dashboard/user/profile")

	rr := httptest.NewRecorder()
	s.handleArtifactFiles(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK for SPA fallback, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "SPA Index") {
		t.Errorf("expected body to contain 'SPA Index', got: %s", body)
	}
	if !strings.Contains(body, "artifact-console") {
		t.Errorf("expected body to contain injected console script, got: %s", body)
	}
}

func TestHandleArtifactFilesMissingIndexPage(t *testing.T) {
	ts := "2026-08-04-noindex"
	artifactDir := filepath.Join("artifacts", ts)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("failed to create artifact dir: %v", err)
	}
	defer os.RemoveAll("artifacts")

	// Write an auxiliary file like app.js, but no index.html
	_ = os.WriteFile(filepath.Join(artifactDir, "app.js"), []byte("console.log('hi');"), 0o644)

	s := &Server{}
	req := httptest.NewRequest("GET", "/api/artifacts/"+ts+"/", nil)
	req.SetPathValue("rest", ts+"/")

	rr := httptest.NewRecorder()
	s.handleArtifactFiles(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK for missing index page, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "index.html no encontrado") {
		t.Errorf("expected body to contain 'index.html no encontrado', got: %s", body)
	}
	if !strings.Contains(body, "write_file(path=\"index.html\"") {
		t.Errorf("expected body to contain write_file hint, got: %s", body)
	}
	if !strings.Contains(body, "app.js") {
		t.Errorf("expected body to list existing app.js, got: %s", body)
	}
}

func TestHandleArtifactFilesNestedDigestLayout(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "artifact_nested_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	digest := "9b60184f8688036b4813f05ed0debae7ba9f3a94f44bd26fddafc6116967bef6"
	date := "2026-08-15_20-15-00"
	artifactDir := filepath.Join("artifacts", digest, date)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("failed to create artifact dir: %v", err)
	}
	defer os.RemoveAll("artifacts")

	indexPath := filepath.Join(artifactDir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html><body>Nested Artifact</body></html>"), 0o644); err != nil {
		t.Fatalf("failed to write index.html: %v", err)
	}
	subFile := filepath.Join(artifactDir, "app.js")
	if err := os.WriteFile(subFile, []byte("console.log('hi');"), 0o644); err != nil {
		t.Fatalf("failed to write app.js: %v", err)
	}

	s := &Server{}
	path := digest + "/" + date

	// Root of the nested artifact serves index.html via SPA fallback.
	req := httptest.NewRequest("GET", "/api/artifacts/"+path+"/", nil)
	req.SetPathValue("rest", path+"/")
	rr := httptest.NewRecorder()
	s.handleArtifactFiles(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("nested root: expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Nested Artifact") {
		t.Errorf("nested root: expected index.html content, got: %s", rr.Body.String())
	}

	// A real file inside the nested artifact is served directly.
	req2 := httptest.NewRequest("GET", "/api/artifacts/"+path+"/app.js", nil)
	req2.SetPathValue("rest", path+"/app.js")
	rr2 := httptest.NewRecorder()
	s.handleArtifactFiles(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("nested file: expected 200, got %d", rr2.Code)
	}
	if !strings.Contains(rr2.Body.String(), "console.log('hi')") {
		t.Errorf("nested file: expected app.js content, got: %s", rr2.Body.String())
	}

	// SPA fallback for a client-side route inside the nested artifact.
	req3 := httptest.NewRequest("GET", "/api/artifacts/"+path+"/dashboard", nil)
	req3.SetPathValue("rest", path+"/dashboard")
	rr3 := httptest.NewRecorder()
	s.handleArtifactFiles(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("nested route: expected 200, got %d", rr3.Code)
	}
	if !strings.Contains(rr3.Body.String(), "Nested Artifact") {
		t.Errorf("nested route: expected index.html content, got: %s", rr3.Body.String())
	}

	// Path traversal is still blocked.
	req4 := httptest.NewRequest("GET", "/api/artifacts/"+path+"/../../secret.txt", nil)
	req4.SetPathValue("rest", path+"/../../secret.txt")
	rr4 := httptest.NewRecorder()
	s.handleArtifactFiles(rr4, req4)
	if rr4.Code == http.StatusOK {
		t.Errorf("traversal: expected non-200, got %d", rr4.Code)
	}
}

func TestDeleteModelRemovesArtifacts(t *testing.T) {
	digest := "dd3be4e31ad39f4762067b6ca2139b0977250d1413d2dcb65e4e21e52086bca7"
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/delete":
			writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
		case "/api/tags":
			writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{
				{"name": "qwen3:latest", "model": "qwen3:latest", "digest": "sha256:" + digest},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaSrv.Close()

	artifactDir := filepath.Join("artifacts", digest, "2026-08-15_20-15-00")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	defer os.RemoveAll("artifacts")
	if err := os.WriteFile(filepath.Join(artifactDir, "index.html"), []byte("<html>hi</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	srv := newTestServer(t, ollamaSrv.URL)
	req := httptest.NewRequest(http.MethodDelete, "/api/models/"+url.PathEscape("qwen3:latest"), nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		DeletedArtifacts int `json:"deleted_artifacts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.DeletedArtifacts != 1 {
		t.Fatalf("deleted_artifacts = %d, want 1", resp.DeletedArtifacts)
	}
	if _, err := os.Stat(artifactDir); !os.IsNotExist(err) {
		t.Fatalf("artifact folder should have been removed, stat err = %v", err)
	}
}

func TestModelDetailReportsArtifactCount(t *testing.T) {
	digest := "9b60184f8688036b4813f05ed0debae7ba9f3a94f44bd26fddafc6116967bef6"
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			writeJSON(w, http.StatusOK, map[string]any{"license": "MIT"})
		case "/api/tags":
			writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{
				{"name": "cap:latest", "model": "cap:latest", "digest": "sha256:" + digest},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaSrv.Close()

	artifactDir := filepath.Join("artifacts", digest, "2026-08-15_20-15-00")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	defer os.RemoveAll("artifacts")
	if err := os.WriteFile(filepath.Join(artifactDir, "index.html"), []byte("<html>hi</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	srv := newTestServer(t, ollamaSrv.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/models/"+url.PathEscape("cap:latest"), nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var detail struct {
		ArtifactCount int   `json:"artifact_count"`
		ArtifactBytes int64 `json:"artifact_bytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if detail.ArtifactCount != 1 {
		t.Fatalf("artifact_count = %d, want 1", detail.ArtifactCount)
	}
	if detail.ArtifactBytes <= 0 {
		t.Fatalf("artifact_bytes = %d, want > 0", detail.ArtifactBytes)
	}
}

func TestListModelArtifacts(t *testing.T) {
	digest := "9b60184f8688036b4813f05ed0debae7ba9f3a94f44bd26fddafc6116967bef6"
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{
				{"name": "cap:latest", "model": "cap:latest", "digest": "sha256:" + digest},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaSrv.Close()

	artifactDir := filepath.Join("artifacts", digest, "2026-08-15_20-15-00")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	defer os.RemoveAll("artifacts")
	if err := os.WriteFile(filepath.Join(artifactDir, "index.html"), []byte("<html>hi</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, ".artifact.json"), []byte(`{"name":"My Dashboard","description":"A sleek dashboard"}`), 0o644); err != nil {
		t.Fatalf("write .artifact.json: %v", err)
	}

	srv := newTestServer(t, ollamaSrv.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/models/"+url.PathEscape("cap:latest")+"/artifacts", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Artifacts []artifactEntry `json:"artifacts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(resp.Artifacts))
	}
	a := resp.Artifacts[0]
	if a.ID != digest+"/2026-08-15_20-15-00" {
		t.Errorf("artifact id = %q, want nested digest/date id", a.ID)
	}
	if a.Date != "2026-08-15_20-15-00" {
		t.Errorf("artifact date = %q", a.Date)
	}
	if a.Name != "My Dashboard" {
		t.Errorf("artifact name = %q, want %q", a.Name, "My Dashboard")
	}
	if a.Description != "A sleek dashboard" {
		t.Errorf("artifact description = %q, want %q", a.Description, "A sleek dashboard")
	}
	if a.FileCount != 2 {
		t.Errorf("file_count = %d, want 2", a.FileCount)
	}
	if a.Size <= 0 {
		t.Errorf("size = %d, want > 0", a.Size)
	}

	// Test deleting the artifact
	delReq := httptest.NewRequest(http.MethodDelete, "/api/artifacts/"+a.ID, nil)
	delRec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", delRec.Code, delRec.Body.String())
	}

	if _, err := os.Stat(artifactDir); !os.IsNotExist(err) {
		t.Errorf("expected artifact dir to be deleted, stat err = %v", err)
	}
}

func TestArtifactVisionToolDefinitions(t *testing.T) {
	toolsNoVision := artifactOperationalToolDefinitions(false)
	hasScreenshotTool := false
	for _, raw := range toolsNoVision {
		if m, ok := raw.(map[string]any); ok {
			if fn, ok := m["function"].(map[string]any); ok {
				if fn["name"] == "take_artifact_screenshot" {
					hasScreenshotTool = true
				}
			}
		}
	}
	if hasScreenshotTool {
		t.Errorf("expected no take_artifact_screenshot when hasVision=false")
	}

	toolsWithVision := artifactOperationalToolDefinitions(true)
	hasScreenshotToolVision := false
	for _, raw := range toolsWithVision {
		if m, ok := raw.(map[string]any); ok {
			if fn, ok := m["function"].(map[string]any); ok {
				if fn["name"] == "take_artifact_screenshot" {
					hasScreenshotToolVision = true
				}
			}
		}
	}
	if !hasScreenshotToolVision {
		t.Errorf("expected take_artifact_screenshot when hasVision=true")
	}
}

func TestArtifactScreenshotHandler(t *testing.T) {
	srv := newTestServer(t, "http://127.0.0.1:11434")
	reqID := "test-req-123"
	ch := make(chan artifactScreenshotResponse, 1)

	srv.artifactScreenshotMu.Lock()
	srv.artifactScreenshotCh[reqID] = ch
	srv.artifactScreenshotMu.Unlock()

	bodyBytes, _ := json.Marshal(map[string]string{
		"request_id": reqID,
		"image":      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/artifacts/screenshot", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	select {
	case res := <-ch:
		if res.Image == "" {
			t.Errorf("expected non-empty image")
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for screenshot channel response")
	}
}

// TestArtifactToolsHaveProperties guards against no-argument tools omitting
// the "properties" field. Ollama's api.ToolFunctionParameters serializes a nil
// Properties as `"properties": null`, which strict (cloud) tool-schema
// validators reject with "properties must be an object".
func TestArtifactToolsHaveProperties(t *testing.T) {
	for _, hasVision := range []bool{false, true} {
		for _, raw := range artifactOperationalToolDefinitions(hasVision) {
			m, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("tool definition is not a map: %T", raw)
			}
			fn, ok := m["function"].(map[string]any)
			if !ok {
				t.Fatalf("tool missing function map: %v", m)
			}
			params, ok := fn["parameters"].(map[string]any)
			if !ok {
				t.Fatalf("tool %v missing parameters map", fn["name"])
			}
			if props, ok := params["properties"].(map[string]any); !ok || props == nil {
				t.Errorf("tool %v must define a non-nil object properties field", fn["name"])
			}
		}
	}
}

func TestArtifactEvalToolDefinitions(t *testing.T) {
	tools := artifactOperationalToolDefinitions(false)
	hasEvalTool := false
	for _, raw := range tools {
		if m, ok := raw.(map[string]any); ok {
			if fn, ok := m["function"].(map[string]any); ok {
				if fn["name"] == "eval_artifact_js" {
					hasEvalTool = true
				}
			}
		}
	}
	if !hasEvalTool {
		t.Errorf("expected eval_artifact_js tool to be defined in operational tools")
	}
}

func TestArtifactEvalHandler(t *testing.T) {
	srv := newTestServer(t, "http://127.0.0.1:11434")
	reqID := "test-eval-req-456"
	ch := make(chan artifactEvalResponse, 1)

	srv.artifactEvalMu.Lock()
	srv.artifactEvalCh[reqID] = ch
	srv.artifactEvalMu.Unlock()

	bodyBytes, _ := json.Marshal(map[string]string{
		"request_id": reqID,
		"result":     "{\"clicked\":true,\"counter\":1}",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/artifacts/eval", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	select {
	case res := <-ch:
		if res.Result != "{\"clicked\":true,\"counter\":1}" {
			t.Errorf("expected expected result, got: %s", res.Result)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for eval channel response")
	}
}

func systemMessagesText(req ollama.ChatRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		if m.Role == "system" {
			b.WriteString(m.Content)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func webToolChatBody(web *bool, system string) chatRequestBody {
	msgs := []ollama.ChatMessage{{Role: "user", Content: "make me a page about the weather"}}
	if system != "" {
		msgs = append([]ollama.ChatMessage{{Role: "system", Content: system}}, msgs...)
	}
	return chatRequestBody{Model: "chat-model:latest", Messages: msgs, WebTools: web}
}

func TestWebToolInstructionArtifactLoop(t *testing.T) {
	yes := true
	nope := false
	for _, tc := range []struct {
		name        string
		web         *bool
		artifactDir string
		customSys   string
		wantInstr   bool
	}{
		{"enabled new artifact", &yes, "", "", true},
		{"enabled existing artifact", &yes, "existing-1", "Be terse.", true},
		{"disabled", &nope, "", "Be terse.", false},
		{"unset", nil, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeOllamaRAGAgent()
			defer fake.Close()
			srv := newTestServer(t, fake.srv.URL)
			t.Chdir(t.TempDir())
			fake.chatSteps = []ragAgentChatStep{{content: "done"}}

			if tc.artifactDir != "" {
				existingDir := filepath.Join("artifacts", tc.artifactDir)
				if err := os.MkdirAll(existingDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(existingDir, "index.html"), []byte("<html>old</html>"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			body := webToolChatBody(tc.web, tc.customSys)
			body.ArtifactDir = tc.artifactDir
			srv.runArtifactAgentLoop(context.Background(), &recordSink{}, body)

			reqs := fake.chatRequests()
			if len(reqs) == 0 {
				t.Fatal("no chat requests captured")
			}
			sys := systemMessagesText(reqs[0])
			if tc.wantInstr && !strings.Contains(sys, webToolSystemInstruction) {
				t.Errorf("system prompt missing web instruction: %q", sys)
			}
			if !tc.wantInstr && strings.Contains(sys, "WEB ACCESS:") {
				t.Errorf("web instruction must not be injected when WebTools is off: %q", sys)
			}
			if tc.customSys != "" && !strings.Contains(sys, tc.customSys) {
				t.Errorf("custom system text lost: %q", sys)
			}
		})
	}
}

func TestWebToolInstructionWebLoop(t *testing.T) {
	yes := true
	nope := false
	for _, tc := range []struct {
		name      string
		web       *bool
		customSys string
		wantInstr bool
	}{
		{"enabled with custom system", &yes, "Be terse.", true},
		{"enabled without system", &yes, "", true},
		{"disabled", &nope, "Be terse.", false},
		{"unset", nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeOllamaRAGAgent()
			defer fake.Close()
			srv := newTestServer(t, fake.srv.URL)
			fake.chatSteps = []ragAgentChatStep{{content: "done"}}

			srv.runWebToolAgentLoop(context.Background(), &recordSink{}, webToolChatBody(tc.web, tc.customSys))

			reqs := fake.chatRequests()
			if len(reqs) == 0 {
				t.Fatal("no chat requests captured")
			}
			sys := systemMessagesText(reqs[0])
			if tc.wantInstr && !strings.Contains(sys, webToolSystemInstruction) {
				t.Errorf("system prompt missing web instruction: %q", sys)
			}
			if !tc.wantInstr && strings.Contains(sys, "WEB ACCESS:") {
				t.Errorf("web instruction must not be injected when WebTools is off: %q", sys)
			}
			if tc.customSys != "" && !strings.Contains(sys, tc.customSys) {
				t.Errorf("custom system text lost: %q", sys)
			}
		})
	}
}

func fakeOnlyTransport(t *testing.T, fakeURL string) {
	t.Helper()
	orig := http.DefaultTransport
	fakeHost := ""
	if u, err := url.Parse(fakeURL); err == nil {
		fakeHost = u.Host
	}
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == fakeHost {
			return orig.RoundTrip(r)
		}
		return nil, fmt.Errorf("test transport refusing unexpected remote host %q", r.URL.Host)
	})
	t.Cleanup(func() { http.DefaultTransport = orig })
}

func TestArtifactLoopWebToolsDisabledDeniesForgedCall(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	t.Chdir(t.TempDir())
	fakeOnlyTransport(t, fake.srv.URL)
	fake.chatSteps = []ragAgentChatStep{
		{toolName: "web_search", toolArgs: map[string]any{"query": "weather Madrid"}},
		{content: "done"},
	}

	srv.runArtifactAgentLoop(context.Background(), &recordSink{}, webToolChatBody(nil, "Be terse."))

	reqs := fake.chatRequests()
	if len(reqs) != 2 {
		t.Fatalf("chat requests = %d, want 2", len(reqs))
	}
	names := ragAgentToolNames(reqs[0])
	if names["web_search"] || names["web_fetch"] {
		t.Errorf("web tool schemas must not be offered when WebTools is off: %v", names)
	}
	if strings.Contains(systemMessagesText(reqs[0]), "WEB ACCESS:") {
		t.Error("web instruction must not be injected when WebTools is off")
	}
	last := reqs[1].Messages[len(reqs[1].Messages)-1]
	if last.Role != "tool" || last.ToolName != "web_search" {
		t.Fatalf("last message = %+v, want a web_search tool result", last)
	}
	if !strings.Contains(last.Content, "web tools are not enabled") {
		t.Errorf("forged web call should be denied, got %q", last.Content)
	}
}

func stubbedWebTransport(t *testing.T, fakeURL, fetchURL string, weather *string) {
	t.Helper()
	orig := http.DefaultTransport
	fakeHost := ""
	if u, err := url.Parse(fakeURL); err == nil {
		fakeHost = u.Host
	}
	respond := func(r *http.Request, body, contentType string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{contentType}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	}
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Hostname() {
		case "api.duckduckgo.com":
			return respond(r, `{"AbstractText":"Current weather in Madrid: `+*weather+` and clear skies.","AbstractURL":"`+fetchURL+`","RelatedTopics":[],"Results":[]}`, "application/json")
		case "lite.duckduckgo.com":
			page := `<html><body><a class="result-link" href="//duckduckgo.com/l/?uddg=` +
				url.QueryEscape(fetchURL) + `">Madrid weather now</a></body></html>`
			return respond(r, page, "text/html")
		case "93.184.216.34":
			return respond(r, `<html><head><title>Madrid Weather</title></head><body>Madrid now: `+*weather+`, light breeze.</body></html>`, "text/html")
		}
		if r.URL.Host == fakeHost {
			return orig.RoundTrip(r)
		}
		return nil, fmt.Errorf("test transport refusing unexpected remote host %q", r.URL.Host)
	})
	t.Cleanup(func() { http.DefaultTransport = orig })
}

func toolLogNames(log []SessionToolEntry) []string {
	var out []string
	for _, e := range log {
		if e.Status == "ok" {
			out = append(out, e.Name)
		}
	}
	return out
}

var reFixtureTemp = regexp.MustCompile(`\d+ C`)

func lastFetchTemperature(req ollama.ChatRequest) (string, bool) {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role == "tool" && m.ToolName == "web_fetch" {
			if temp := reFixtureTemp.FindString(m.Content); temp != "" {
				return temp, true
			}
		}
	}
	return "", false
}

func lastToolContent(req ollama.ChatRequest, name string) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role == "tool" && m.ToolName == name {
			return m.Content
		}
	}
	return ""
}

func TestDetachedSessionWebResearchThenArtifact(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	t.Chdir(t.TempDir())

	const fetchURL = "http://93.184.216.34/weather"
	weather := "21 C"
	stubbedWebTransport(t, fake.srv.URL, fetchURL, &weather)

	var scriptErrs []string
	noteErr := func(format string, a ...any) {
		scriptErrs = append(scriptErrs, fmt.Sprintf(format, a...))
	}
	readErrs := func() []string {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return append([]string(nil), scriptErrs...)
	}

	tool := func(name string, args map[string]any) func(ollama.ChatRequest) ragAgentChatStep {
		return func(ollama.ChatRequest) ragAgentChatStep {
			return ragAgentChatStep{toolName: name, toolArgs: args}
		}
	}
	answer := func(text string) func(ollama.ChatRequest) ragAgentChatStep {
		return func(ollama.ChatRequest) ragAgentChatStep {
			return ragAgentChatStep{content: text}
		}
	}
	writeIndex := func(req ollama.ChatRequest) ragAgentChatStep {
		temp, ok := lastFetchTemperature(req)
		if !ok {
			noteErr("write_file step found no temperature in the preceding web_fetch result")
			temp = "MISSING"
		}
		return ragAgentChatStep{toolName: "write_file", toolArgs: map[string]any{
			"path":    "index.html",
			"content": "<html><body><h1>Madrid: " + temp + "</h1></body></html>",
		}}
	}
	setScript := func(script []func(ollama.ChatRequest) ragAgentChatStep) {
		idx := 0
		fake.mu.Lock()
		fake.stepFn = func(req ollama.ChatRequest) ragAgentChatStep {
			if idx >= len(script) {
				return ragAgentChatStep{content: "done"}
			}
			step := script[idx]
			idx++
			return step(req)
		}
		fake.mu.Unlock()
	}

	setScript([]func(ollama.ChatRequest) ragAgentChatStep{
		tool("web_search", map[string]any{"query": "weather Madrid"}),
		tool("web_fetch", map[string]any{"url": fetchURL}),
		tool("create_artifact", map[string]any{"name": "weather", "description": "Madrid weather page"}),
		writeIndex,
		answer("Here is your Madrid weather page."),
	})

	st := srv.chatSessions
	sess := st.Create("chat-model:latest", SessionSettings{WebTools: true, Artifacts: true})
	if !st.AppendUser(sess.ID, "build me a page with the current weather in Madrid", nil) {
		t.Fatal("AppendUser failed")
	}
	st.flush(sess.ID)

	st = newChatSessionStore(st.dir)
	st.Load()
	srv.chatSessions = st
	if st.Get(sess.ID) == nil {
		t.Fatal("session missing after first reload")
	}

	srv.runSessionTurn(context.Background(), sess.ID)

	got := st.Get(sess.ID)
	if got.Status != chatSessionIdle {
		t.Fatalf("session status = %q (err=%q), want idle", got.Status, got.Error)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(got.Messages))
	}
	asst := got.Messages[1]
	wantOrder := []string{"web_search", "web_fetch", "create_artifact", "write_file"}
	gotOrder := toolLogNames(asst.ToolLog)
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("tool order = %v, want %v", gotOrder, wantOrder)
	}
	for _, e := range asst.ToolLog {
		if e.Status == "error" {
			t.Errorf("tool %s failed: %q", e.Name, e.Error)
		}
	}
	if asst.ArtifactTS == "" || asst.ArtifactNm != "weather" {
		t.Fatalf("artifact fields not stored: ts=%q name=%q", asst.ArtifactTS, asst.ArtifactNm)
	}

	reqs := fake.chatRequests()
	if len(reqs) != 5 {
		t.Fatalf("chat requests = %d, want 5", len(reqs))
	}
	for i, req := range reqs {
		names := ragAgentToolNames(req)
		if !names["web_search"] || !names["web_fetch"] {
			t.Errorf("req %d: web schemas must be offered every round: %v", i, names)
		}
		if names["take_artifact_screenshot"] || names["eval_artifact_js"] {
			t.Errorf("req %d: browser tools must stay hidden for an unwatched session: %v", i, names)
		}
	}
	for i := 0; i < 3; i++ {
		if names := ragAgentToolNames(reqs[i]); names["write_file"] {
			t.Errorf("req %d: write_file must stay unavailable before create_artifact: %v", i, names)
		}
	}
	for i := 3; i < len(reqs); i++ {
		if names := ragAgentToolNames(reqs[i]); !names["write_file"] {
			t.Errorf("req %d: write_file must be available after create_artifact: %v", i, names)
		}
	}
	if searchResult := lastToolContent(reqs[1], "web_search"); !strings.Contains(searchResult, "21 C") {
		t.Errorf("web_search tool result missing fixture weather: %q", searchResult)
	}
	if fetchResult := lastToolContent(reqs[2], "web_fetch"); !strings.Contains(fetchResult, "21 C") {
		t.Errorf("web_fetch tool result missing fixture weather: %q", fetchResult)
	}

	indexPath := filepath.Join("artifacts", asst.ArtifactTS, "index.html")
	written, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("index.html not written: %v", err)
	}
	if !strings.Contains(string(written), "21 C") {
		t.Errorf("index.html does not carry the researched weather: %q", written)
	}

	st = newChatSessionStore(st.dir)
	st.Load()
	srv.chatSessions = st
	reloaded := st.Get(sess.ID)
	if reloaded == nil {
		t.Fatal("session missing after reload")
	}
	if !reloaded.Settings.WebTools || !reloaded.Settings.Artifacts {
		t.Errorf("toggles lost after reload: %+v", reloaded.Settings)
	}
	if len(reloaded.Messages) != 2 || reloaded.Messages[1].ArtifactTS != asst.ArtifactTS ||
		len(toolLogNames(reloaded.Messages[1].ToolLog)) != 4 {
		t.Fatalf("reloaded transcript lost the turn: %+v", reloaded.Messages)
	}

	weather = "22 C"
	setScript([]func(ollama.ChatRequest) ragAgentChatStep{
		tool("web_search", map[string]any{"query": "weather Madrid"}),
		tool("web_fetch", map[string]any{"url": fetchURL}),
		writeIndex,
		answer("Updated the page."),
	})

	if !st.AppendUser(sess.ID, "refresh it with the latest numbers", nil) {
		t.Fatal("AppendUser for second turn failed")
	}
	srv.runSessionTurn(context.Background(), sess.ID)

	got = st.Get(sess.ID)
	if got.Status != chatSessionIdle || len(got.Messages) != 4 {
		t.Fatalf("after second turn: status=%q messages=%d", got.Status, len(got.Messages))
	}
	asst2 := got.Messages[3]
	wantOrder2 := []string{"web_search", "web_fetch", "write_file"}
	if gotOrder2 := toolLogNames(asst2.ToolLog); strings.Join(gotOrder2, ",") != strings.Join(wantOrder2, ",") {
		t.Fatalf("second turn tool order = %v, want %v (no repeated create_artifact)", gotOrder2, wantOrder2)
	}
	if asst2.ArtifactTS != asst.ArtifactTS {
		t.Errorf("second turn moved to a different workspace: %q vs %q", asst2.ArtifactTS, asst.ArtifactTS)
	}
	reqs = fake.chatRequests()
	if len(reqs) != 9 {
		t.Fatalf("chat requests after second turn = %d, want 9", len(reqs))
	}
	for i := 5; i < len(reqs); i++ {
		names := ragAgentToolNames(reqs[i])
		if !names["web_search"] || !names["web_fetch"] || !names["write_file"] {
			t.Errorf("req %d: restored session must keep web + workspace tools: %v", i, names)
		}
		if names["create_artifact"] {
			t.Errorf("req %d: an active workspace must not offer create_artifact again: %v", i, names)
		}
		if names["take_artifact_screenshot"] || names["eval_artifact_js"] {
			t.Errorf("req %d: browser tools must stay hidden for an unwatched session: %v", i, names)
		}
	}
	if searchResult := lastToolContent(reqs[6], "web_search"); !strings.Contains(searchResult, "22 C") {
		t.Errorf("second turn web_search result missing updated fixture weather: %q", searchResult)
	}
	if fetchResult := lastToolContent(reqs[7], "web_fetch"); !strings.Contains(fetchResult, "22 C") {
		t.Errorf("second turn web_fetch result missing updated fixture weather: %q", fetchResult)
	}
	updated, err := os.ReadFile(indexPath)
	if err != nil || !strings.Contains(string(updated), "22 C") {
		t.Fatalf("second turn did not update the same workspace: %q %v", updated, err)
	}

	if errs := readErrs(); len(errs) > 0 {
		t.Fatalf("mock script errors: %v", errs)
	}
}
