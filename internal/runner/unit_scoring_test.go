package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
	"github.com/gense/ollama-manager/internal/tests"
)

// runScoredTest executes one test against a mock chat backend whose reply is
// derived from the last user message, and returns the recorded result.
func runScoredTest(t *testing.T, testDef tests.Test, reply func(lastUser string) string) TestResult {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var req ollama.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastUser := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				lastUser = req.Messages[i].Content
				break
			}
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":       map[string]any{"role": "assistant", "content": reply(lastUser)},
			"done":          true,
			"eval_count":    5,
			"eval_duration": 1000000000,
		})
	}))
	defer srv.Close()

	c := NewClient(ollama.New(srv.URL))
	done := make(chan *BatteryRun, 1)
	c.ExecuteBatteryAsync(
		context.Background(),
		tests.Group{ID: "g1", Name: "g1"},
		[]tests.Test{testDef},
		[]string{"model-x"},
		map[string][]string{"model-x": nil},
		SysInfo{},
		func(run *BatteryRun) { done <- run },
	)
	select {
	case run := <-done:
		if run == nil || len(run.Results) != 1 {
			t.Fatalf("expected 1 result, got %+v", run)
		}
		return run.Results[0]
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for the test to finish")
		return TestResult{}
	}
}

// TestUnitScorePartialCaseSteps is the core regression: a case with two steps
// where only the first passes must earn partial credit (1/2) instead of
// collapsing the whole case to 0%.
func TestUnitScorePartialCaseSteps(t *testing.T) {
	testDef := tests.Test{
		ID: "t-partial-case", Name: "partial case", GroupID: "g1", Active: true,
		Cases: []tests.TestCase{{
			Name: "op",
			Steps: []tests.CaseStep{
				{Name: "s1", Prompt: "step-one", Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "GOOD1"}},
				{Name: "s2", Prompt: "step-two", Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "GOOD2"}},
			},
		}},
	}
	res := runScoredTest(t, testDef, func(last string) string {
		if last == "step-one" {
			return "GOOD1"
		}
		return "BAD"
	})

	if len(res.SubResults) != 2 {
		t.Fatalf("expected 2 sub-results, got %+v", res.SubResults)
	}
	if res.CasesTotal != 1 || res.CasesPassed != 0 {
		t.Errorf("case-level counts should stay all-or-nothing: total=%d passed=%d", res.CasesTotal, res.CasesPassed)
	}
	if res.Points != 1 || res.MaxPoints != 2 || res.Score != 50 {
		t.Fatalf("expected unit score 1/2 = 50%%, got points=%v max=%v score=%v", res.Points, res.MaxPoints, res.Score)
	}
	if res.Passed == nil || *res.Passed {
		t.Errorf("test must not be marked passed, got %v", res.Passed)
	}
}

// TestUnitScoreTopLevelSteps verifies a mis-scored step in a top-level step
// chain is counted individually rather than making the whole test 0%.
func TestUnitScoreTopLevelSteps(t *testing.T) {
	testDef := tests.Test{
		ID: "t-steps", Name: "steps", GroupID: "g1", Active: true,
		Steps: []tests.Step{
			{Name: "a", Prompt: "sa", Evaluation: &tests.Evaluation{Type: "contains", Expected: "A"}},
			{Name: "b", Prompt: "sb", Evaluation: &tests.Evaluation{Type: "contains", Expected: "B"}},
			{Name: "c", Prompt: "sc", Evaluation: &tests.Evaluation{Type: "contains", Expected: "C"}},
		},
	}
	res := runScoredTest(t, testDef, func(last string) string {
		switch last {
		case "sa":
			return "A present"
		case "sc":
			return "C present"
		default:
			return "nothing"
		}
	})

	if res.Points != 2 || res.MaxPoints != 3 {
		t.Fatalf("expected 2/3 units, got points=%v max=%v", res.Points, res.MaxPoints)
	}
	if res.Score < 66.6 || res.Score > 66.7 {
		t.Fatalf("expected ~66.7%%, got %v", res.Score)
	}
}

// TestUnitScoreMultiCaseWeighting verifies each scored turn across cases counts
// the same: 2 units passed out of 3 gives 2/3, not case-only 1/2.
func TestUnitScoreMultiCaseWeighting(t *testing.T) {
	testDef := tests.Test{
		ID: "t-multicase-units", Name: "multicase units", GroupID: "g1", Active: true,
		Cases: []tests.TestCase{
			{
				Name: "case-1",
				Steps: []tests.CaseStep{
					{Name: "c1s1", Prompt: "c1-s1", Evaluation: &tests.Evaluation{Type: "contains", Expected: "OK"}},
					{Name: "c1s2", Prompt: "c1-s2", Evaluation: &tests.Evaluation{Type: "contains", Expected: "OK"}},
				},
			},
			{
				Name:       "case-2",
				Prompt:     "c2",
				Evaluation: &tests.Evaluation{Type: "contains", Expected: "OK"},
			},
		},
	}
	res := runScoredTest(t, testDef, func(last string) string {
		if last == "c2" {
			return "nope"
		}
		return "OK"
	})

	if res.Points != 2 || res.MaxPoints != 3 {
		t.Fatalf("expected 2/3 units, got points=%v max=%v", res.Points, res.MaxPoints)
	}
	if res.CasesTotal != 2 || res.CasesPassed != 1 {
		t.Errorf("expected 1/2 cases, got total=%d passed=%d", res.CasesTotal, res.CasesPassed)
	}
}
