package runner

import (
	"math"
	"testing"

	"github.com/gense/ollama-manager/internal/tests"
)

func TestCasesScoringCalculation(t *testing.T) {
	pass := true
	fail := false

	// Every scored sub-case (turn) is one point: partial passes earn
	// proportional credit and there is no perfect-run bonus.
	t.Run("partial sub-cases earn proportional credit", func(t *testing.T) {
		res := TestResult{
			TestID: "test-partial",
			Passed: &fail,
			SubResults: []SubResult{
				{Passed: &pass},
				{Passed: &fail},
				{Passed: &pass},
				{Passed: &fail},
			},
		}
		applyUnitScore(&res, 4)
		if res.Points != 2.0 || res.MaxPoints != 4.0 || res.Score != 50.0 {
			t.Fatalf("expected 2/4 = 50%%, got points=%v max=%v score=%v", res.Points, res.MaxPoints, res.Score)
		}
	})

	t.Run("single sub-case pass is 100%", func(t *testing.T) {
		res := TestResult{TestID: "t1", Passed: &pass, SubResults: []SubResult{{Passed: &pass}}}
		applyUnitScore(&res, 1)
		if res.Points != 1.0 || res.MaxPoints != 1.0 || res.Score != 100.0 {
			t.Fatalf("expected 1/1 = 100%%, got points=%v max=%v score=%v", res.Points, res.MaxPoints, res.Score)
		}
	})

	t.Run("planned denominator keeps aborted work from inflating", func(t *testing.T) {
		// Only 1 of 3 planned units ran (abort); the denominator stays 3.
		res := TestResult{TestID: "t-abort", Passed: &fail, SubResults: []SubResult{{Passed: &pass}}}
		applyUnitScore(&res, 3)
		if res.Points != 1.0 || res.MaxPoints != 3.0 || res.Score < 33.3 || res.Score > 33.4 {
			t.Fatalf("expected 1/3 ≈ 33%%, got points=%v max=%v score=%v", res.Points, res.MaxPoints, res.Score)
		}
	})

	t.Run("skipped and pending units count in denominator only", func(t *testing.T) {
		res := TestResult{
			TestID: "t-skips",
			Passed: &fail,
			SubResults: []SubResult{
				{Passed: &pass},
				{Passed: &fail, Error: "manually skipped"},
				{Passed: nil}, // awaiting human review
			},
		}
		applyUnitScore(&res, 3)
		if res.Points != 1.0 || res.MaxPoints != 3.0 {
			t.Fatalf("expected 1/3, got points=%v max=%v", res.Points, res.MaxPoints)
		}
	})

	t.Run("no sub-results falls back to the test verdict", func(t *testing.T) {
		res := TestResult{TestID: "plain", Passed: &pass}
		applyUnitScore(&res, 0)
		if res.Points != 1.0 || res.MaxPoints != 1.0 || res.Score != 100.0 {
			t.Fatalf("expected 1/1 = 100%%, got points=%v max=%v score=%v", res.Points, res.MaxPoints, res.Score)
		}
	})
}

func TestComputeUnitCount(t *testing.T) {
	cases := []struct {
		name string
		test tests.Test
		want int
	}{
		{"plain prompt", tests.Test{Prompt: "hi"}, 1},
		{"top-level steps", tests.Test{Steps: []tests.Step{{Prompt: "a"}, {Prompt: "b"}}}, 2},
		{
			"case prompt only",
			tests.Test{Cases: []tests.TestCase{{Prompt: "a"}}},
			1,
		},
		{
			"case with steps and prompt",
			tests.Test{Cases: []tests.TestCase{{Prompt: "ctx", Steps: []tests.CaseStep{{Prompt: "s1"}, {Prompt: "s2"}}}}},
			3,
		},
		{
			"case with steps and no prompt",
			tests.Test{Cases: []tests.TestCase{{Steps: []tests.CaseStep{{Prompt: "s1"}}}}},
			1,
		},
		{
			"mixed cases",
			tests.Test{Cases: []tests.TestCase{
				{Prompt: "a"},
				{Prompt: "ctx", Steps: []tests.CaseStep{{Prompt: "s1"}}},
			}},
			3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.test.ComputeUnitCount(); got != tc.want {
				t.Fatalf("ComputeUnitCount = %d, want %d", got, tc.want)
			}
		})
	}
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
			// Test 1: 2 cases (3 scored units), 1 unit passed -> 1/3 points
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
			// Test 2: 1 case / 1 unit, passed -> 1/1 points
			{
				TestID:      "t2",
				TestName:    "Test 2",
				Model:       "model-a",
				Passed:      &trueVal,
				CasesTotal:  1,
				CasesPassed: 1,
				Points:      1.0,
				MaxPoints:   1.0,
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
	if s.ScorePoints != 2.0 {
		t.Fatalf("expected 2.0 points earned, got %f", s.ScorePoints)
	}
	if s.MaxPoints != 4.0 {
		t.Fatalf("expected 4.0 max points, got %f", s.MaxPoints)
	}
	// 2 / 4 = 50.0%
	if math.Abs(s.Score-50.0) > 0.001 {
		t.Fatalf("expected score 50.0, got %f", s.Score)
	}
}
