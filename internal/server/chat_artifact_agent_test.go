package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
