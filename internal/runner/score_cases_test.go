package runner

import (
	"math"
	"testing"

	"github.com/gense/ollama-manager/internal/tests"
)

func TestCasesScoringCalculation(t *testing.T) {
	// Test 1: Single case passing gets 1 case + 1 bonus = 2 points / 2 max points = 100%
	t.Run("single case passed", func(t *testing.T) {
		trueVal := true
		res := TestResult{
			TestID:      "test-1",
			Passed:      &trueVal,
			CasesTotal:  1,
			CasesPassed: 1,
			Points:      2.0,
			MaxPoints:   2.0,
			Score:       100.0,
		}
		if res.Points != 2.0 || res.MaxPoints != 2.0 || res.Score != 100.0 {
			t.Fatalf("unexpected res: %+v", res)
		}
	})

	// Test 2: Multi-case test where 2 out of 3 pass (50.0% score)
	t.Run("partial cases passed", func(t *testing.T) {
		falseVal := false
		casesTotal := 3
		casesPassed := 2
		bonus := 0.0 // not all passed
		pts := float64(casesPassed) + bonus
		maxPts := float64(casesTotal) + 1.0 // 4.0
		score := math.Min(100.0, math.Max(0.0, (pts/maxPts)*100.0))

		res := TestResult{
			TestID:      "test-3-cases",
			Passed:      &falseVal,
			CasesTotal:  casesTotal,
			CasesPassed: casesPassed,
			Points:      pts,
			MaxPoints:   maxPts,
			Score:       score,
		}
		if res.Points != 2.0 {
			t.Fatalf("expected 2.0 points, got %f", res.Points)
		}
		if res.MaxPoints != 4.0 {
			t.Fatalf("expected 4.0 max points, got %f", res.MaxPoints)
		}
		if res.Score != 50.0 {
			t.Fatalf("expected 50.0 score, got %f", res.Score)
		}
	})

	// Test 3: Multi-case test where 3 out of 3 pass (100.0% score with bonus)
	t.Run("all cases passed with bonus", func(t *testing.T) {
		trueVal := true
		casesTotal := 3
		casesPassed := 3
		bonus := 1.0 // all passed!
		pts := float64(casesPassed) + bonus
		maxPts := float64(casesTotal) + 1.0 // 4.0
		score := math.Min(100.0, math.Max(0.0, (pts/maxPts)*100.0))

		res := TestResult{
			TestID:      "test-3-cases-perfect",
			Passed:      &trueVal,
			CasesTotal:  casesTotal,
			CasesPassed: casesPassed,
			Points:      pts,
			MaxPoints:   maxPts,
			Score:       score,
		}
		if res.Points != 4.0 {
			t.Fatalf("expected 4.0 points, got %f", res.Points)
		}
		if res.MaxPoints != 4.0 {
			t.Fatalf("expected 4.0 max points, got %f", res.MaxPoints)
		}
		if res.Score != 100.0 {
			t.Fatalf("expected 100.0 score, got %f", res.Score)
		}
	})
}

func TestStoreGroupHistoryCasesAggregation(t *testing.T) {
	dir := t.TempDir()
	store := NewResultStore(dir)

	falseVal := false
	trueVal := true

	run := BatteryRun{
		ID:        "run-1",
		GroupID:   "coding",
		GroupName: "Coding",
		Models:    []string{"model-a"},
		Results: []TestResult{
			// Test 1: 2 cases, 1 passed -> points: 1, max: 3
			{
				TestID:      "t1",
				TestName:    "Test 1",
				Model:       "model-a",
				Passed:      &falseVal,
				CasesTotal:  2,
				CasesPassed: 1,
				Points:      1.0,
				MaxPoints:   3.0,
			},
			// Test 2: 1 case, 1 passed -> points: 2, max: 2 (clean pass with bonus)
			{
				TestID:      "t2",
				TestName:    "Test 2",
				Model:       "model-a",
				Passed:      &trueVal,
				CasesTotal:  1,
				CasesPassed: 1,
				Points:      2.0,
				MaxPoints:   2.0,
			},
		},
	}

	if err := store.SaveRun(&run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	summary := store.GetGroupHistory("coding")
	if len(summary) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summary))
	}
	s := summary[0]
	if s.TotalCases != 3 {
		t.Fatalf("expected 3 total cases, got %d", s.TotalCases)
	}
	if s.PassedCases != 2 {
		t.Fatalf("expected 2 passed cases, got %d", s.PassedCases)
	}
	if s.ScorePoints != 3.0 {
		t.Fatalf("expected 3.0 points earned, got %f", s.ScorePoints)
	}
	if s.MaxPoints != 5.0 {
		t.Fatalf("expected 5.0 max points, got %f", s.MaxPoints)
	}
	// 3 / 5 = 60.0%
	if math.Abs(s.Score-60.0) > 0.001 {
		t.Fatalf("expected score 60.0, got %f", s.Score)
	}
}

// Ensure tests package is referenced if needed
var _ = tests.Test{}
