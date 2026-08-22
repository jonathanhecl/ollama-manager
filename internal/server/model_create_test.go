package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gense/ollama-manager/internal/ollama"
)

func TestCreateModelHandlerValidation(t *testing.T) {
	srv := newTestServer(t, "")

	// Missing model name
	req := httptest.NewRequest(http.MethodPost, "/api/models/create", strings.NewReader(`{"modelfile":"FROM llama3.1"}`))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	// Invalid body
	req = httptest.NewRequest(http.MethodPost, "/api/models/create", strings.NewReader(`invalid json`))
	rec = httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateModelHandlerSuccessStream(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/create" {
			var cr ollama.CreateRequest
			_ = json.NewDecoder(r.Body).Decode(&cr)
			if cr.Model != "my-custom-model" {
				http.Error(w, "bad model name", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"status":"reading model metadata"}`+"\n")
			_, _ = io.WriteString(w, `{"status":"creating system layer"}`+"\n")
			_, _ = io.WriteString(w, `{"status":"writing manifest"}`+"\n")
			_, _ = io.WriteString(w, `{"status":"success"}`+"\n")
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	srv := newTestServer(t, mockServer.URL)

	payload := map[string]any{
		"name":      "my-custom-model",
		"from":      "llama3.1:8b",
		"system":    "You are a helpful assistant",
		"modelfile": "FROM llama3.1:8b\nSYSTEM You are a helpful assistant\nPARAMETER temperature 0.7",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/models/create", bytes.NewReader(body))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	bodyStr := rec.Body.String()
	if !strings.Contains(bodyStr, "event: progress") {
		t.Errorf("expected progress events in SSE response, got: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "event: done") {
		t.Errorf("expected done event in SSE response, got: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"success":true`) {
		t.Errorf("expected success in done payload, got: %s", bodyStr)
	}
}
