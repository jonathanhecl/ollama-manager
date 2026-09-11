package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gense/ollama-manager/internal/config"
)

func TestHFTokenConfigPatchAndGet(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	if getConfig(t, srv)["has_hf_token"] != false {
		t.Fatalf("expected no token initially")
	}

	code, out := patchConfig(t, srv, map[string]any{"hf_token": "  hf_abc123  "})
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, out)
	}
	if out["has_hf_token"] != true {
		t.Fatalf("has_hf_token = %v, want true", out["has_hf_token"])
	}

	got := getConfig(t, srv)
	if got["has_hf_token"] != true {
		t.Fatalf("GET has_hf_token = %v, want true", got["has_hf_token"])
	}
	if _, leaked := got["hf_token"]; leaked {
		t.Fatalf("GET leaked the raw hf_token")
	}

	reloaded, err := config.Load(srv.cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.HFToken != "hf_abc123" {
		t.Fatalf("persisted token = %q, want trimmed value", reloaded.HFToken)
	}

	// Clearing with an empty string.
	code, out = patchConfig(t, srv, map[string]any{"hf_token": ""})
	if code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %v", code, out)
	}
	if out["has_hf_token"] != false {
		t.Fatalf("has_hf_token after clear = %v, want false", out["has_hf_token"])
	}
	reloaded, err = config.Load(srv.cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.HFToken != "" {
		t.Fatalf("token not cleared, got %q", reloaded.HFToken)
	}
}

func TestApplyHFAuthHeader(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	req, _ := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models", nil)
	srv.applyHFAuth(req)
	if h := req.Header.Get("Authorization"); h != "" {
		t.Fatalf("Authorization = %q, want empty without token", h)
	}

	srv.cfg.HFToken = "hf_secret"
	req, _ = http.NewRequest(http.MethodGet, "https://huggingface.co/api/models", nil)
	srv.applyHFAuth(req)
	if h := req.Header.Get("Authorization"); h != "Bearer hf_secret" {
		t.Fatalf("Authorization = %q, want Bearer token", h)
	}
}

func TestOllamaKeyHandler(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	home := t.TempDir()
	t.Setenv("HOME", home)

	// Missing key reports found=false with the attempted path.
	req := httptest.NewRequest(http.MethodGet, "/api/ollama/key", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, `"found":false`) || !strings.Contains(body, "id_ed25519.pub") {
		t.Fatalf("unexpected body without key: %s", body)
	}

	key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA test@example"
	dir := filepath.Join(home, ".ollama")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "id_ed25519.pub"), []byte(key+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/ollama/key", nil)
	rr = httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, `"found":true`) || !strings.Contains(body, key) {
		t.Fatalf("unexpected body with key: %s", body)
	}
}
