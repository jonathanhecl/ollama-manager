package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gense/ollama-manager/internal/config"
)

func patchConfig(t *testing.T, srv *Server, payload map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func getConfig(t *testing.T, srv *Server) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/config status = %d", rr.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGatewayConfigPatchAndGet(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	code, out := patchConfig(t, srv, map[string]any{
		"gateway": map[string]any{
			"enabled":        true,
			"port":           7861,
			"expose_network": false,
			"models":         []string{"m1", "m2"},
			"require_auth":   true,
		},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, out)
	}
	if out["needs_restart"] != true {
		t.Fatalf("needs_restart = %v, want true: %v", out["needs_restart"], out)
	}

	got := getConfig(t, srv)["gateway"].(map[string]any)
	if got["enabled"] != true || got["port"] != float64(7861) || got["require_auth"] != true {
		t.Fatalf("unexpected gateway in GET: %v", got)
	}
	models, _ := got["models"].([]any)
	if len(models) != 2 || models[0] != "m1" || models[1] != "m2" {
		t.Fatalf("unexpected gateway models: %v", got["models"])
	}

	// Persisted to disk: reload and compare.
	reloaded, err := config.Load(srv.cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Gateway.Enabled || reloaded.Gateway.Port != 7861 || !reloaded.Gateway.RequireAuth {
		t.Fatalf("gateway not persisted: %+v", reloaded.Gateway)
	}
	if len(reloaded.Gateway.Models) != 2 {
		t.Fatalf("gateway models not persisted: %+v", reloaded.Gateway.Models)
	}
	if addr := reloaded.Gateway.GatewayBindAddress(); addr != "127.0.0.1:7861" {
		t.Fatalf("bind address = %q, want 127.0.0.1:7861", addr)
	}
}

func TestGatewayConfigPortValidation(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	// Out of range.
	if code, _ := patchConfig(t, srv, map[string]any{"gateway": map[string]any{"port": 70000}}); code != http.StatusBadRequest {
		t.Fatalf("port 70000 status = %d, want 400", code)
	}
	// Clash with the main port (7860 default) while enabling.
	if code, _ := patchConfig(t, srv, map[string]any{"gateway": map[string]any{"enabled": true, "port": 7860}}); code != http.StatusBadRequest {
		t.Fatalf("clashing port status = %d, want 400", code)
	}
	// Main port moved onto the enabled gateway port.
	if code, _ := patchConfig(t, srv, map[string]any{"gateway": map[string]any{"enabled": true, "port": 7861}}); code != http.StatusOK {
		t.Fatalf("enable gateway status = %d", code)
	}
	if code, _ := patchConfig(t, srv, map[string]any{"port": 7861}); code != http.StatusBadRequest {
		t.Fatalf("main port clash status = %d, want 400", code)
	}
	// Failed validations must not mutate the in-memory config.
	if srv.cfg.Port != 7860 {
		t.Fatalf("main port mutated to %d after failed PATCH", srv.cfg.Port)
	}
}

func gatewayKeysCall(t *testing.T, srv *Server, method, target string, payload map[string]any) (int, map[string]any) {
	t.Helper()
	var body *bytes.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func TestGatewayKeysCRUD(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	if code, _ := gatewayKeysCall(t, srv, http.MethodPost, "/api/gateway/keys", map[string]any{"name": ""}); code != http.StatusBadRequest {
		t.Fatalf("empty name status = %d, want 400", code)
	}

	code, created := gatewayKeysCall(t, srv, http.MethodPost, "/api/gateway/keys", map[string]any{"name": "opencode"})
	if code != http.StatusOK {
		t.Fatalf("create status = %d, body = %v", code, created)
	}
	secret, _ := created["key"].(string)
	if len(secret) < 32 || secret[:5] != "omgr-" {
		t.Fatalf("plaintext key missing omgr- prefix: %v", created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id in %v", created)
	}

	// Plaintext must never leak through the list endpoint.
	_, listed := gatewayKeysCall(t, srv, http.MethodGet, "/api/gateway/keys", nil)
	keys, _ := listed["keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("keys = %v, want 1", listed["keys"])
	}
	entry := keys[0].(map[string]any)
	if _, ok := entry["key"]; ok {
		t.Fatalf("plaintext leaked in list: %v", entry)
	}
	if _, ok := entry["key_hash"]; ok {
		t.Fatalf("hash leaked in list: %v", entry)
	}
	if entry["id"] != id || entry["name"] != "opencode" {
		t.Fatalf("unexpected list entry: %v", entry)
	}

	if !srv.gatewayKeys.Verify(secret) {
		t.Fatalf("Verify(plaintext) = false")
	}
	if srv.gatewayKeys.Verify(secret + "x") {
		t.Fatalf("Verify(tampered) = true")
	}
	if srv.gatewayKeys.Verify("") {
		t.Fatalf("Verify(empty) = true")
	}

	// Persistence round-trip: hash (not secret) survives reload.
	fresh := newGatewayKeysStore(srv.gatewayKeys.path)
	if err := fresh.Load(); err != nil {
		t.Fatal(err)
	}
	if !fresh.Verify(secret) {
		t.Fatalf("reloaded store does not verify the secret")
	}

	if code, _ := gatewayKeysCall(t, srv, http.MethodDelete, "/api/gateway/keys/"+id, nil); code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", code)
	}
	if srv.gatewayKeys.Verify(secret) {
		t.Fatalf("revoked key still verifies")
	}
	if code, _ := gatewayKeysCall(t, srv, http.MethodDelete, "/api/gateway/keys/"+id, nil); code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", code)
	}
}
