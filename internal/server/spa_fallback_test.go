package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsSPAClientPath(t *testing.T) {
	spa := []string{
		"/", "/chat", "/chat/", "/chat/abc",
		"/tests", "/tests/", "/tests/new",
		"/tests/edit/1", "/tests/group/g", "/tests/agent/s",
		"/tests/battery/progress/r", "/tests/battery/results/r",
		"/tests/battery/history", "/tests/history/t1",
		"/leaderboard", "/leaderboard/", "/tests/leaderboard", "/tests/battery/leaderboard",
		"/analytics", "/analytics/",
		"/settings", "/settings/", "/settings/general",
		"/opencode", "/opencode/", "/archived", "/archived/",
		"/modelfile", "/modelfile/x",
		"/hf", "/hf/", "/huggingface", "/huggingface/",
	}
	for _, p := range spa {
		if !isSPAClientPath(p) {
			t.Errorf("isSPAClientPath(%q) = false, want true", p)
		}
	}
	notSPA := []string{
		"/api/tests", "/static/app.js", "/login", "/favicon.ico",
		"/leaderboardx", "/analyticss", "/nope",
	}
	for _, p := range notSPA {
		if isSPAClientPath(p) {
			t.Errorf("isSPAClientPath(%q) = true, want false", p)
		}
	}
}

func TestHandleIndexLeaderboardRefresh(t *testing.T) {
	srv := newTestServer(t, "http://127.0.0.1:1")

	req := httptest.NewRequest(http.MethodGet, "/leaderboard", nil)
	rr := httptest.NewRecorder()
	srv.handleIndex(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /leaderboard status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("GET /leaderboard Content-Type = %q", ct)
	}
	if rr.Body.Len() == 0 {
		t.Fatalf("GET /leaderboard returned empty body")
	}

	req404 := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rr404 := httptest.NewRecorder()
	srv.handleIndex(rr404, req404)
	if rr404.Code != http.StatusNotFound {
		t.Fatalf("GET /nope status = %d, want 404", rr404.Code)
	}
}
