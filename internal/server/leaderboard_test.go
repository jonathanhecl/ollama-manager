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

// TestCachedLeaderboardData verifies that hot read paths reuse an in-memory
// copy while _leaderboard.json is unchanged, and refresh it after a rebuild.
func TestCachedLeaderboardData(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	if _, err := srv.testsStore.CreateGroup(tests.Group{ID: "coding", Name: "Coding", Order: 1}); err != nil {
		t.Fatal(err)
	}
	tst, err := srv.testsStore.CreateTest(tests.Test{
		Name:    "Cached",
		Prompt:  "hi",
		GroupID: "coding",
		Active:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	passed := true
	if err := srv.runnerStore.SaveRun(&runner.BatteryRun{
		ID:        "run-cache",
		GroupID:   "coding",
		Timestamp: time.Now().UTC(),
		Results: []runner.TestResult{
			{TestID: tst.ID, TestName: tst.Name, Model: "m1", Passed: &passed, MaxPoints: 1, Points: 1},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// First call builds (file missing) and caches.
	d1, err := srv.cachedLeaderboardData()
	if err != nil {
		t.Fatalf("cachedLeaderboardData: %v", err)
	}
	if len(d1.Models) != 1 || d1.Models[0].Model != "m1" {
		t.Fatalf("unexpected first leaderboard: %+v", d1.Models)
	}
	if _, err := os.Stat(srv.LeaderboardPath()); err != nil {
		t.Fatalf("expected cache file to be created: %v", err)
	}

	// Second call must be served from memory (same pointer).
	d2, err := srv.cachedLeaderboardData()
	if err != nil {
		t.Fatalf("cachedLeaderboardData (2): %v", err)
	}
	if d1 != d2 {
		t.Fatalf("expected cached pointer reuse, got a rebuilt copy")
	}

	// A regeneration must invalidate the in-memory copy.
	if _, err := srv.RegenerateLeaderboardCache(); err != nil {
		t.Fatalf("RegenerateLeaderboardCache: %v", err)
	}
	d3, err := srv.cachedLeaderboardData()
	if err != nil {
		t.Fatalf("cachedLeaderboardData (3): %v", err)
	}
	if d3 == d1 {
		t.Fatalf("expected refreshed data after regeneration")
	}
}

func TestTestsListCacheAndETag(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	// Seed test group
	if _, err := srv.testsStore.CreateGroup(tests.Group{ID: "g_cache", Name: "G Cache"}); err != nil {
		t.Fatal(err)
	}

	// First request: should return 200 with an ETag header
	req1 := httptest.NewRequest(http.MethodGet, "/api/tests", nil)
	rr1 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("first GET /api/tests returned %d", rr1.Code)
	}
	etag := rr1.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("expected ETag header on /api/tests, got empty")
	}

	// Second request with If-None-Match: should return 304 Not Modified
	req2 := httptest.NewRequest(http.MethodGet, "/api/tests", nil)
	req2.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusNotModified {
		t.Fatalf("expected 304 Not Modified, got %d", rr2.Code)
	}

	// Mutate: create a new test (invalidates cache)
	if _, err := srv.testsStore.CreateTest(tests.Test{
		Name:    "New Test",
		Prompt:  "hello",
		GroupID: "g_cache",
		Active:  true,
	}); err != nil {
		t.Fatal(err)
	}
	srv.invalidateTestsCache()

	// Third request with old ETag: should now return 200 with new ETag
	req3 := httptest.NewRequest(http.MethodGet, "/api/tests", nil)
	req3.Header.Set("If-None-Match", etag)
	rr3 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr3, req3)

	if rr3.Code != http.StatusOK {
		t.Fatalf("expected 200 OK after mutation, got %d", rr3.Code)
	}
	newETag := rr3.Header().Get("ETag")
	if newETag == etag {
		t.Errorf("expected new ETag after mutation, but got same: %s", newETag)
	}
}
