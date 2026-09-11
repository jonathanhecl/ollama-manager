package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gense/ollama-manager/internal/runner"
	"github.com/gense/ollama-manager/internal/tests"
)

func seedHistoryRuns(t *testing.T, srv *Server) {
	t.Helper()
	if _, err := srv.testsStore.CreateGroup(tests.Group{ID: "g1", Name: "G One"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.testsStore.CreateGroup(tests.Group{ID: "g2", Name: "G Two"}); err != nil {
		t.Fatal(err)
	}
	t1, err := srv.testsStore.CreateTest(tests.Test{Name: "T1", Prompt: "p", GroupID: "g1", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := srv.testsStore.CreateTest(tests.Test{Name: "T2", Prompt: "p", GroupID: "g2", Active: true})
	if err != nil {
		t.Fatal(err)
	}

	passed := true
	base := time.Now().UTC().Add(-3 * time.Hour)
	runs := []runner.BatteryRun{
		{ID: "r1", GroupID: "g1", GroupName: "G One", Timestamp: base, Models: []string{"model-a"},
			Results: []runner.TestResult{{TestID: t1.ID, TestName: t1.Name, Model: "model-a", Passed: &passed, MaxPoints: 1, Points: 1, ResponseTimeMs: 100}}},
		{ID: "r2", GroupID: "g2", GroupName: "G Two", Timestamp: base.Add(time.Hour), Models: []string{"model-b"},
			Results: []runner.TestResult{{TestID: t2.ID, TestName: t2.Name, Model: "model-b", Passed: &passed, MaxPoints: 1, Points: 1, ResponseTimeMs: 200}}},
		{ID: "r3", GroupID: "g1", GroupName: "G One", Timestamp: base.Add(2 * time.Hour), Models: []string{"model-a", "model-b"},
			Results: []runner.TestResult{
				{TestID: t1.ID, TestName: t1.Name, Model: "model-a", Passed: &passed, MaxPoints: 1, Points: 1, ResponseTimeMs: 300},
				{TestID: t2.ID, TestName: t2.Name, Model: "model-b", Passed: &passed, MaxPoints: 1, Points: 1, ResponseTimeMs: 400},
			}},
	}
	for i := range runs {
		if err := srv.runnerStore.SaveRun(&runs[i]); err != nil {
			t.Fatal(err)
		}
	}
}

type runsResponse struct {
	Runs    []map[string]any `json:"runs"`
	Total   int              `json:"total"`
	HasMore bool             `json:"has_more"`
	Summary map[string]any   `json:"model_summary"`
}

func getRuns(t *testing.T, srv *Server, query string) runsResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/runner/runs?"+query, nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/runner/runs?%s returned %d", query, rr.Code)
	}
	var out runsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestListRunsPaginationAndFilters(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)
	seedHistoryRuns(t, srv)

	// Newest first, paginated.
	page1 := getRuns(t, srv, "limit=2&offset=0")
	if page1.Total != 3 || len(page1.Runs) != 2 || !page1.HasMore {
		t.Fatalf("page1 mismatch: total=%d len=%d hasMore=%v", page1.Total, len(page1.Runs), page1.HasMore)
	}
	if page1.Runs[0]["id"] != "r3" {
		t.Fatalf("expected newest run r3 first, got %v", page1.Runs[0]["id"])
	}
	page2 := getRuns(t, srv, "limit=2&offset=2")
	if len(page2.Runs) != 1 || page2.HasMore {
		t.Fatalf("page2 mismatch: len=%d hasMore=%v", len(page2.Runs), page2.HasMore)
	}
	if page2.Runs[0]["id"] != "r1" {
		t.Fatalf("expected oldest run r1 last, got %v", page2.Runs[0]["id"])
	}

	// Model filter.
	byModel := getRuns(t, srv, "model=model-b")
	if byModel.Total != 2 {
		t.Fatalf("expected 2 runs for model-b, got %d", byModel.Total)
	}
	if byModel.Summary == nil || byModel.Summary["total"].(float64) != 2 {
		t.Fatalf("expected model summary total 2, got %+v", byModel.Summary)
	}

	// Category filter (by group id).
	byCat := getRuns(t, srv, "category=g1")
	if byCat.Total != 2 {
		t.Fatalf("expected 2 runs for category g1, got %d", byCat.Total)
	}

	// Free-text query matches model or category names.
	byQuery := getRuns(t, srv, "q=Model-A")
	if byQuery.Total != 2 {
		t.Fatalf("expected 2 runs for q=Model-A, got %d", byQuery.Total)
	}
	byCatName := getRuns(t, srv, "q=G%20Two")
	if byCatName.Total != 1 {
		t.Fatalf("expected 1 run for q=G Two, got %d", byCatName.Total)
	}
}
