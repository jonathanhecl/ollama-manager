package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"github.com/gense/ollama-manager/internal/runner"
)

func TestBatteryConcurrencyAndActiveEndpoints(t *testing.T) {
	fakeOllama := fakeOllamaForBattery()
	defer fakeOllama.Close()

	srv := newTestServer(t, fakeOllama.URL)

	seedTwoGroups(t, srv)

	// Initially no active run
	req := httptest.NewRequest(http.MethodGet, "/api/runner/active", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var activeResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &activeResp)
	if activeResp["active"] != false {
		t.Fatalf("expected active=false, got %v", activeResp["active"])
	}

	// Simulate an active run in progress
	activeRunID := "test-active-run-99"
	srv.runner.SetProgressForTest(runner.Progress{
		RunID:      activeRunID,
		TotalTests: 10,
		TestIndex:  3,
		GroupID:    "g1",
		GroupName:  "G One",
		Models:     []string{"m1"},
		Done:       false,
	})

	// Test GET /api/runner/active returns active run
	reqActive := httptest.NewRequest(http.MethodGet, "/api/runner/active", nil)
	rrActive := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rrActive, reqActive)
	if rrActive.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrActive.Code)
	}
	var actOut map[string]any
	_ = json.Unmarshal(rrActive.Body.Bytes(), &actOut)
	if actOut["active"] != true {
		t.Fatalf("expected active=true, got %v", actOut["active"])
	}
	if actOut["run_id"] != activeRunID {
		t.Fatalf("expected run_id=%s, got %v", activeRunID, actOut["run_id"])
	}

	// Test GET /api/status includes battery_active
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rrStatus := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rrStatus, reqStatus)
	if rrStatus.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrStatus.Code)
	}
	var statOut map[string]any
	_ = json.Unmarshal(rrStatus.Body.Bytes(), &statOut)
	if statOut["battery_active"] != true {
		t.Fatalf("expected battery_active=true, got %v", statOut["battery_active"])
	}
	if statOut["battery_run_id"] != activeRunID {
		t.Fatalf("expected battery_run_id=%s, got %v", activeRunID, statOut["battery_run_id"])
	}

	// Attempting a new battery run while active must fail with 409 Conflict
	code2, out2 := postBatteryRun(t, srv, map[string]any{
		"group_ids": []string{"g2"},
		"model_ids": []string{"m1"},
	})
	if code2 != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d: %v", code2, out2)
	}

	// Clear active run
	srv.runner.ClearProgress(activeRunID)

	// After clearing, GET /api/runner/active returns active=false
	reqAfter := httptest.NewRequest(http.MethodGet, "/api/runner/active", nil)
	rrAfter := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rrAfter, reqAfter)
	var afterOut map[string]any
	_ = json.Unmarshal(rrAfter.Body.Bytes(), &afterOut)
	if afterOut["active"] != false {
		t.Fatalf("expected active=false after clear, got %v", afterOut["active"])
	}
}
