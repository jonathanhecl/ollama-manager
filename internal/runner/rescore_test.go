package runner

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRescoreResultFromSubResults(t *testing.T) {
	tru := true
	fal := false

	t.Run("old case+bonus scheme is normalized", func(t *testing.T) {
		res := TestResult{
			TestID:      "t1",
			Passed:      &fal,
			CasesTotal:  3,
			CasesPassed: 2,
			Points:      3.0, // 2 cases + 1 bonus under the old scheme
			MaxPoints:   4.0,
			SubResults: []SubResult{
				{Passed: &tru},
				{Passed: &tru},
				{Passed: &fal},
			},
		}
		if !rescoreResultFromSubResults(&res) {
			t.Fatal("expected rescore to report a change")
		}
		if res.Points != 2 || res.MaxPoints != 3 {
			t.Fatalf("expected 2/3, got %v/%v", res.Points, res.MaxPoints)
		}
		if math.Abs(res.Score-66.666) > 0.01 {
			t.Fatalf("expected ~66.67, got %v", res.Score)
		}
	})

	t.Run("manual pass keeps verdict and normalizes denominator", func(t *testing.T) {
		res := TestResult{
			TestID: "t2", Passed: &tru, ManualVerdict: true,
			Points: 2, MaxPoints: 2,
			SubResults: []SubResult{{Passed: &fal}, {Passed: &fal}},
		}
		if !rescoreResultFromSubResults(&res) {
			t.Fatal("expected rescore to report a change")
		}
		if res.Points != 2 || res.MaxPoints != 2 || res.Score != 100 {
			t.Fatalf("manual pass must stay 100%%, got %v/%v score=%v", res.Points, res.MaxPoints, res.Score)
		}
	})

	t.Run("manual fail keeps verdict", func(t *testing.T) {
		res := TestResult{
			TestID: "t3", Passed: &fal, ManualVerdict: true,
			Points: 0, MaxPoints: 2,
			SubResults: []SubResult{{Passed: &tru}, {Passed: &tru}},
		}
		rescoreResultFromSubResults(&res)
		if res.Points != 0 || res.MaxPoints != 2 || res.Score != 0 {
			t.Fatalf("manual fail must stay 0%%, got %v/%v score=%v", res.Points, res.MaxPoints, res.Score)
		}
	})

	t.Run("no sub-results leaves the result untouched", func(t *testing.T) {
		res := TestResult{TestID: "t4", Passed: &tru, Points: 1, MaxPoints: 1, Score: 100}
		if rescoreResultFromSubResults(&res) {
			t.Fatal("expected no change without sub-results")
		}
	})
}

// TestLoadRescoresLegacyRuns verifies that loading a history file saved under
// the old case+bonus scheme rewrites its scores from the recorded sub-results
// and persists the correction.
func TestLoadRescoresLegacyRuns(t *testing.T) {
	dir := t.TempDir()
	catDir := filepath.Join(dir, "security")
	if err := os.MkdirAll(catDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tru := true
	fal := false
	run := BatteryRun{
		ID:        "run-legacy",
		Timestamp: time.Now().UTC(),
		GroupID:   "security",
		GroupName: "Security",
		Models:    []string{"model-a"},
		Results: []TestResult{{
			TestID:      "sec",
			TestName:    "sec",
			Model:       "model-a",
			Passed:      &fal,
			CasesTotal:  3,
			CasesPassed: 2,
			Points:      3.0, // old scheme: 2 cases + 1 bonus
			MaxPoints:   4.0,
			Score:       75.0,
			SubResults: []SubResult{
				{Passed: &tru},
				{Passed: &tru},
				{Passed: &fal},
			},
		}},
	}
	data, err := json.MarshalIndent(persistFile{Runs: []BatteryRun{run}}, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	histPath := filepath.Join(catDir, "sec._history.json")
	if err := os.WriteFile(histPath, data, 0o644); err != nil {
		t.Fatalf("write history: %v", err)
	}

	store := NewResultStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	summary := store.GetGroupHistory("security")
	if len(summary) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summary))
	}
	s := summary[0]
	if s.ScorePoints != 2 || s.MaxPoints != 3 {
		t.Fatalf("expected rescored 2/3 points, got %v/%v", s.ScorePoints, s.MaxPoints)
	}
	if math.Abs(s.Score-66.666) > 0.01 {
		t.Fatalf("expected ~66.67, got %v", s.Score)
	}

	// The correction must be persisted back to disk.
	raw, err := os.ReadFile(histPath)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	var pf persistFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pf.Runs) != 1 || len(pf.Runs[0].Results) != 1 {
		t.Fatalf("unexpected persisted runs: %+v", pf.Runs)
	}
	got := pf.Runs[0].Results[0]
	if got.Points != 2 || got.MaxPoints != 3 {
		t.Fatalf("persisted score not normalized: %v/%v", got.Points, got.MaxPoints)
	}
}
