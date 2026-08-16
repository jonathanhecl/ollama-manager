package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if a.FileCount != 1 {
		t.Errorf("file_count = %d, want 1", a.FileCount)
	}
	if a.Size <= 0 {
		t.Errorf("size = %d, want > 0", a.Size)
	}
}
