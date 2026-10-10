package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func revealReq(t *testing.T, srv *Server, body string, headers map[string]string) (int, map[string]any, http.Header) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/secrets/reveal", strings.NewReader(body))
	if _, ok := headers["Content-Type"]; !ok {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out, rr.Header()
}

func TestSecretRevealHFToken(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)
	srv.cfg.HFToken = "hf_synthetic_secret"

	code, out, hdr := revealReq(t, srv, `{"kind":"hf_token"}`, nil)
	if code != http.StatusOK || out["value"] != "hf_synthetic_secret" {
		t.Fatalf("reveal = %d %v", code, out)
	}
	if hdr.Get("Cache-Control") != "no-store" || hdr.Get("Pragma") != "no-cache" {
		t.Fatalf("cache headers = %v", hdr)
	}
}

func TestSecretRevealMissingHFToken(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	code, out, _ := revealReq(t, srv, `{"kind":"hf_token"}`, nil)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d %v", code, out)
	}
}

func TestSecretRevealRequiresAuth(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)
	srv.cfg.PasswordHash = "$2a$10$0123456789abcdef0123456789abcdef0123456789abcdef"
	srv.cfg.HFToken = "hf_synthetic_secret"

	code, out, _ := revealReq(t, srv, `{"kind":"hf_token"}`, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d %v", code, out)
	}
	if strings.Contains(out["error"].(string), "hf_synthetic") {
		t.Fatal("error leaked secret")
	}
}

func TestSecretRevealExternalKey(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)
	if _, err := srv.externalModels.Upsert("", "ext-a", "http://a.example/v1", "sk_synthetic_key", nil, false, ""); err != nil {
		t.Fatal(err)
	}

	code, out, _ := revealReq(t, srv, `{"kind":"external_api_key","id":"ext-a"}`, nil)
	if code != http.StatusOK || out["value"] != "sk_synthetic_key" {
		t.Fatalf("reveal = %d %v", code, out)
	}

	code, out, _ = revealReq(t, srv, `{"kind":"external_api_key","id":"nope"}`, nil)
	if code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d %v", code, out)
	}
	code, out, _ = revealReq(t, srv, `{"kind":"external_api_key"}`, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("missing id status = %d %v", code, out)
	}
	code, out, _ = revealReq(t, srv, `{"kind":"bogus"}`, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("bad kind status = %d %v", code, out)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/external-models", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if strings.Contains(rr.Body.String(), "sk_synthetic_key") {
		t.Fatal("external-models GET leaked raw key")
	}
}

func TestSecretRevealSameOriginGuards(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)
	srv.cfg.HFToken = "hf_synthetic_secret"

	code, _, _ := revealReq(t, srv, `{"kind":"hf_token"}`, map[string]string{"Content-Type": "text/plain"})
	if code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain status = %d", code)
	}
	code, _, _ = revealReq(t, srv, `{"kind":"hf_token"}`, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if code != http.StatusForbidden {
		t.Fatalf("cross-site status = %d", code)
	}
	code, _, _ = revealReq(t, srv, `{"kind":"hf_token"}`, map[string]string{"Sec-Fetch-Site": "same-site"})
	if code != http.StatusForbidden {
		t.Fatalf("same-site status = %d", code)
	}
	code, _, _ = revealReq(t, srv, `{"kind":"hf_token"}`, map[string]string{"Origin": "https://evil.example.com"})
	if code != http.StatusForbidden {
		t.Fatalf("foreign Origin status = %d", code)
	}
	code, _, _ = revealReq(t, srv, `{"kind":"hf_token"}`, map[string]string{"Origin": "https://u:p@example.com"})
	if code != http.StatusForbidden {
		t.Fatalf("userinfo Origin status = %d", code)
	}
	code, out, _ := revealReq(t, srv, `{"kind":"hf_token"}`, map[string]string{"Origin": "http://example.com", "Sec-Fetch-Site": "same-origin"})
	if code != http.StatusOK || out["value"] != "hf_synthetic_secret" {
		t.Fatalf("same-origin reveal = %d %v", code, out)
	}
}
