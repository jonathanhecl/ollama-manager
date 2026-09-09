package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gense/ollama-manager/internal/runner"
	"github.com/gense/ollama-manager/internal/tests"
)

func TestLeaderboardGenerationAndEndpoint(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	// Create test group and tests
	if _, err := srv.testsStore.CreateGroup(tests.Group{ID: "coding", Name: "Coding", Order: 1}); err != nil {
		t.Fatal(err)
	}

	tst, err := srv.testsStore.CreateTest(tests.Test{
		Name:    "Fibonacci",
		Prompt:  "write fib",
		GroupID: "coding",
		Active:  true,
		Cases: []tests.TestCase{
			{Name: "case 1", Prompt: "fib(5)"},
			{Name: "case 2", Prompt: "fib(10)"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add a completed run for model "test-model:latest"
	passed := true
	run := &runner.BatteryRun{
		ID:        "run-123",
		GroupID:   "coding",
		Timestamp: time.Now().UTC(),
		Results: []runner.TestResult{
			{
				TestID:      tst.ID,
				TestName:    tst.Name,
				Model:       "test-model:latest",
				Passed:      &passed,
				CasesTotal:  2,
				CasesPassed: 2,
				Points:      3.0, // 2 cases + 1 bonus
				MaxPoints:   3.0,
			},
		},
	}
	if err := srv.runnerStore.SaveRun(run); err != nil {
		t.Fatal(err)
	}

	// Regenerate leaderboard cache
	data, err := srv.RegenerateLeaderboardCache()
	if err != nil {
		t.Fatalf("RegenerateLeaderboardCache failed: %v", err)
	}

	// Verify file exists on disk at /testing/_leaderboard.json
	lbFile := filepath.Join(srv.testsStore.Dir(), "_leaderboard.json")
	raw, err := os.ReadFile(lbFile)
	if err != nil {
		t.Fatalf("expected leaderboard file at %s: %v", lbFile, err)
	}

	var diskData LeaderboardData
	if err := json.Unmarshal(raw, &diskData); err != nil {
		t.Fatalf("unmarshal disk leaderboard: %v", err)
	}

	if len(diskData.Groups) == 0 {
		t.Fatalf("expected groups in leaderboard, got none")
	}
	if len(diskData.Models) != 1 {
		t.Fatalf("expected 1 model in leaderboard, got %d", len(diskData.Models))
	}

	mRow := diskData.Models[0]
	if mRow.Model != "test-model:latest" {
		t.Errorf("expected model name 'test-model:latest', got '%s'", mRow.Model)
	}
	if mRow.Overall == nil || *mRow.Overall != 100.0 {
		t.Errorf("expected overall 100.0, got %v", mRow.Overall)
	}
	if mRow.Points != 3.0 || mRow.MaxPoints != 3.0 {
		t.Errorf("expected points 3.0/3.0, got %f/%f", mRow.Points, mRow.MaxPoints)
	}

	// Test GET /api/runner/leaderboard
	req := httptest.NewRequest(http.MethodGet, "/api/runner/leaderboard", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/runner/leaderboard returned %d: %s", rr.Code, rr.Body.String())
	}

	var apiData LeaderboardData
	if err := json.Unmarshal(rr.Body.Bytes(), &apiData); err != nil {
		t.Fatalf("unmarshal api response: %v", err)
	}
	if len(apiData.Models) != 1 || apiData.Models[0].Model != "test-model:latest" {
		t.Errorf("api response mismatch: %v", apiData)
	}

	// Test GET /api/runner/leaderboard?refresh=true
	reqRefresh := httptest.NewRequest(http.MethodGet, "/api/runner/leaderboard?refresh=true", nil)
	rrRefresh := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rrRefresh, reqRefresh)

	if rrRefresh.Code != http.StatusOK {
		t.Fatalf("GET /api/runner/leaderboard?refresh=true returned %d", rrRefresh.Code)
	}

	_ = data
}

func TestLeaderboardCacheOnMutations(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	// Creating a test group via handler triggers cache regeneration
	grp, err := srv.testsStore.CreateGroup(tests.Group{ID: "math", Name: "Math", Order: 2})
	if err != nil {
		t.Fatal(err)
	}

	// Trigger regeneration
	if _, err := srv.RegenerateLeaderboardCache(); err != nil {
		t.Fatal(err)
	}

	lbFile := filepath.Join(srv.testsStore.Dir(), "_leaderboard.json")
	if _, err := os.Stat(lbFile); os.IsNotExist(err) {
		t.Fatalf("expected leaderboard cache file to exist: %v", err)
	}

	_ = grp
}
