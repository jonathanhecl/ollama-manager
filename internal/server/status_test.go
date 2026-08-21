package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gense/ollama-manager/internal/config"
	"github.com/gense/ollama-manager/internal/ollama"
)

func TestHandleStatus(t *testing.T) {
	cfg := config.Defaults()
	client := ollama.New("http://localhost:11434")
	srv := &Server{
		cfg:    cfg,
		ollama: client,
	}

	req := httptest.NewRequest("GET", "/api/status", nil)
	rr := httptest.NewRecorder()

	srv.handleStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var data map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if _, ok := data["cpu_model"]; !ok {
		t.Errorf("expected cpu_model in status response, got %v", data)
	}

	for _, key := range []string{"vram_total", "vram_used", "vram_used_pct", "memory_total"} {
		if _, ok := data[key]; !ok {
			t.Errorf("expected %s in status response, got %v", key, data)
		}
	}

	t.Logf("cpu_model in /api/status: %v", data["cpu_model"])
}
