package server

import (
	"bytes"
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

func TestBatteryHumanReviewPendingState(t *testing.T) {
	fakeOllama := fakeOllamaForBattery()
	defer fakeOllama.Close()

	srv := newTestServer(t, fakeOllama.URL)
	seedTwoGroups(t, srv)

	reviewRunID := "test-run-human-pending"
	srv.runner.SetProgressForTest(runner.Progress{
		RunID:          reviewRunID,
		TotalTests:     2,
		TestIndex:      2,
		GroupID:        "g1",
		GroupName:      "G One",
		Models:         []string{"m1"},
		Done:           false,
		WaitingReview:  true,
		PendingReviews: 1,
		Results: []runner.TestResult{
			{
				TestID: "t1",
				Model:  "m1",
				Passed: nil,
			},
		},
	})

	// Also add to runnerStore so handleRateRun can update it
	_ = srv.runnerStore.SaveRun(&runner.BatteryRun{
		ID:        reviewRunID,
		GroupID:   "g1",
		GroupName: "G One",
		Models:    []string{"m1"},
		Results: []runner.TestResult{
			{
				TestID: "t1",
				Model:  "m1",
				Passed: nil,
			},
		},
	})

	// Status must report battery_active=true and battery_waiting_review=true
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rrStatus := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rrStatus, reqStatus)
	var statOut map[string]any
	_ = json.Unmarshal(rrStatus.Body.Bytes(), &statOut)
	if statOut["battery_active"] != true {
		t.Fatalf("expected battery_active=true during human review, got %v", statOut["battery_active"])
	}
	if statOut["battery_waiting_review"] != true {
		t.Fatalf("expected battery_waiting_review=true, got %v", statOut["battery_waiting_review"])
	}
	if statOut["battery_pending_reviews"] != float64(1) {
		t.Fatalf("expected 1 pending review, got %v", statOut["battery_pending_reviews"])
	}

	// Starting another run while review is pending must fail with 409
	code, _ := postBatteryRun(t, srv, map[string]any{
		"group_ids": []string{"g2"},
		"model_ids": []string{"m1"},
	})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict while awaiting human review, got %d", code)
	}

	// Rate the test as passed
	rateBody := map[string]any{
		"test_id": "t1",
		"model":   "m1",
		"passed":  true,
	}
	rateBytes, _ := json.Marshal(rateBody)
	reqRate := httptest.NewRequest(http.MethodPut, "/api/runner/runs/"+reviewRunID+"/rate", bytes.NewReader(rateBytes))
	rrRate := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rrRate, reqRate)
	if rrRate.Code != http.StatusOK {
		t.Fatalf("expected 200 on rating, got %d: %s", rrRate.Code, rrRate.Body.String())
	}

	// Status should now be battery_active=false because all reviews are finished
	rrStatus2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rrStatus2, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var statOut2 map[string]any
	_ = json.Unmarshal(rrStatus2.Body.Bytes(), &statOut2)
	if statOut2["battery_active"] != false {
		t.Fatalf("expected battery_active=false after finishing reviews, got %v", statOut2["battery_active"])
	}
}
