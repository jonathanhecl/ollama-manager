package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteTestHistory(t *testing.T) {
	dir := t.TempDir()
	store := NewResultStore(dir)

	passed := true
	store.runs = []BatteryRun{
		{
			ID:        "run-1",
			Timestamp: time.Now().UTC(),
			GroupID:   "core",
			GroupName: "Core",
			Results: []TestResult{
				{TestID: "t1", TestName: "A", Model: "m1", Passed: &passed},
				{TestID: "t2", TestName: "B", Model: "m1", Passed: &passed},
			},
		},
		{
			ID:        "run-2",
			Timestamp: time.Now().UTC(),
			GroupID:   "core",
			GroupName: "Core",
			Results: []TestResult{
				{TestID: "t1", TestName: "A", Model: "m2", Passed: &passed},
			},
		},
	}

	if err := store.saveGroupLocked("core"); err != nil {
		t.Fatalf("saveGroupLocked: %v", err)
	}

	histFile1 := filepath.Join(dir, "core", "t1._history.json")
	if _, err := os.Stat(histFile1); err != nil {
		t.Fatalf("expected t1._history.json to exist in core folder: %v", err)
	}
	histFile2 := filepath.Join(dir, "core", "t2._history.json")
	if _, err := os.Stat(histFile2); err != nil {
		t.Fatalf("expected t2._history.json to exist in core folder: %v", err)
	}

	if err := store.DeleteTestHistory("t1"); err != nil {
		t.Fatalf("DeleteTestHistory: %v", err)
	}
	if _, err := os.Stat(histFile1); !os.IsNotExist(err) {
		t.Fatalf("expected t1._history.json to be deleted after DeleteTestHistory")
	}
	if _, err := os.Stat(histFile2); err != nil {
		t.Fatalf("expected t2._history.json to remain: %v", err)
	}

	if len(store.runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(store.runs))
	}
	if len(store.runs[0].Results) != 1 || store.runs[0].Results[0].TestID != "t2" {
		t.Fatalf("unexpected remaining results: %+v", store.runs[0].Results)
	}

	if err := store.DeleteTestHistory("t2"); err != nil {
		t.Fatalf("DeleteTestHistory t2: %v", err)
	}
	if _, err := os.Stat(histFile2); !os.IsNotExist(err) {
		t.Fatalf("expected t2._history.json to be deleted after DeleteTestHistory")
	}
	if len(store.runs) != 0 {
		t.Fatalf("runs = %d, want 0 after removing last result", len(store.runs))
	}
}

func TestDeleteModelHistory(t *testing.T) {
	dir := t.TempDir()
	store := NewResultStore(dir)

	passed := true
	store.runs = []BatteryRun{
		{
			ID:        "run-1",
			Timestamp: time.Now().UTC(),
			GroupID:   "core",
			GroupName: "Core",
			Models:    []string{"m1", "m2"},
			Results: []TestResult{
				{TestID: "t1", TestName: "A", Model: "m1", Passed: &passed},
				{TestID: "t2", TestName: "B", Model: "m1", Passed: &passed},
				{TestID: "t1", TestName: "A", Model: "m2", Passed: &passed},
			},
		},
		{
			ID:        "run-2",
			Timestamp: time.Now().UTC(),
			GroupID:   "core",
			GroupName: "Core",
			Models:    []string{"m1"},
			Results: []TestResult{
				{TestID: "t1", TestName: "A", Model: "m1", Passed: &passed},
			},
		},
	}

	if err := store.saveGroupLocked("core"); err != nil {
		t.Fatalf("saveGroupLocked: %v", err)
	}

	if err := store.DeleteModelHistory("m1"); err != nil {
		t.Fatalf("DeleteModelHistory: %v", err)
	}

	// Only m2 results survive; run-2 (m1 only) is dropped entirely.
	if len(store.runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(store.runs))
	}
	if len(store.runs[0].Results) != 1 || store.runs[0].Results[0].Model != "m2" {
		t.Fatalf("unexpected remaining results: %+v", store.runs[0].Results)
	}

	// t2 had only m1 results: its history file must be gone.
	if _, err := os.Stat(filepath.Join(dir, "core", "t2._history.json")); !os.IsNotExist(err) {
		t.Fatalf("expected t2._history.json to be deleted after DeleteModelHistory")
	}
	// t1 still has the m2 result: its file must remain with only m2 inside.
	data, err := os.ReadFile(filepath.Join(dir, "core", "t1._history.json"))
	if err != nil {
		t.Fatalf("expected t1._history.json to remain: %v", err)
	}
	var pf persistFile
	if err := json.Unmarshal(data, &pf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, r := range pf.Runs {
		for _, res := range r.Results {
			if res.Model == "m1" {
				t.Fatalf("m1 result leaked into t1 history: %+v", res)
			}
		}
	}

	// Deleting a model with no results is a no-op.
	if err := store.DeleteModelHistory("m1"); err != nil {
		t.Fatalf("DeleteModelHistory (absent model): %v", err)
	}
	if len(store.runs) != 1 {
		t.Fatalf("runs = %d, want 1 after no-op delete", len(store.runs))
	}
	if err := store.DeleteModelHistory(""); err != nil {
		t.Fatalf("DeleteModelHistory (empty model): %v", err)
	}
}

func TestPerExerciseHistorySaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	catDir := filepath.Join(dir, "examples")
	if err := os.MkdirAll(catDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create exercise test files
	arithmeticYaml := `id: example-arithmetic
name: Math Suite
group_id: examples
`
	weatherYaml := `id: example-weather
name: Weather Test
group_id: examples
`
	_ = os.WriteFile(filepath.Join(catDir, "arithmetic.yaml"), []byte(arithmeticYaml), 0o644)
	_ = os.WriteFile(filepath.Join(catDir, "weather.yaml"), []byte(weatherYaml), 0o644)

	store := NewResultStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	passed := true
	run := &BatteryRun{
		ID:        "run-100",
		Timestamp: time.Now().UTC(),
		GroupID:   "examples",
		GroupName: "Examples",
		Models:    []string{"model-a"},
		Results: []TestResult{
			{TestID: "example-arithmetic", TestName: "Math Suite", Model: "model-a", Passed: &passed},
			{TestID: "example-weather", TestName: "Weather Test", Model: "model-a", Passed: &passed},
		},
	}

	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	// Verify separate history files were created
	arithHist := filepath.Join(catDir, "arithmetic._history.json")
	if _, err := os.Stat(arithHist); err != nil {
		t.Fatalf("expected arithmetic._history.json: %v", err)
	}
	weatherHist := filepath.Join(catDir, "weather._history.json")
	if _, err := os.Stat(weatherHist); err != nil {
		t.Fatalf("expected weather._history.json: %v", err)
	}

	// Verify content of arithmetic history only contains arithmetic results
	data, err := os.ReadFile(arithHist)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var pf persistFile
	if err := json.Unmarshal(data, &pf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pf.Runs) != 1 || len(pf.Runs[0].Results) != 1 || pf.Runs[0].Results[0].TestID != "example-arithmetic" {
		t.Fatalf("unexpected arithmetic history content: %+v", pf)
	}

	// Verify new store loads and merges results
	store2 := NewResultStore(dir)
	if err := store2.Load(); err != nil {
		t.Fatalf("store2 Load: %v", err)
	}
	runs2 := store2.GetRuns()
	if len(runs2) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs2))
	}
	if len(runs2[0].Results) != 2 {
		t.Fatalf("expected 2 merged results, got %d", len(runs2[0].Results))
	}
}

func TestLegacyHistoryMigration(t *testing.T) {
	dir := t.TempDir()
	catDir := filepath.Join(dir, "examples")
	if err := os.MkdirAll(catDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	arithmeticYaml := `id: example-arithmetic
name: Math Suite
group_id: examples
`
	_ = os.WriteFile(filepath.Join(catDir, "arithmetic.yaml"), []byte(arithmeticYaml), 0o644)

	passed := true
	legacyPF := persistFile{
		Runs: []BatteryRun{
			{
				ID:        "run-legacy-1",
				Timestamp: time.Now().UTC(),
				GroupID:   "examples",
				GroupName: "Examples",
				Models:    []string{"model-1"},
				Results: []TestResult{
					{TestID: "example-arithmetic", TestName: "Math Suite", Model: "model-1", Passed: &passed},
				},
			},
		},
	}
	legacyData, _ := json.MarshalIndent(legacyPF, "", "  ")
	legacyFile := filepath.Join(catDir, "_history.json")
	if err := os.WriteFile(legacyFile, legacyData, 0o644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	store := NewResultStore(dir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Check legacy file was deleted
	if _, err := os.Stat(legacyFile); !os.IsNotExist(err) {
		t.Fatalf("expected legacy _history.json to be deleted after migration")
	}

	// Check arithmetic._history.json was created
	newHist := filepath.Join(catDir, "arithmetic._history.json")
	if _, err := os.Stat(newHist); err != nil {
		t.Fatalf("expected arithmetic._history.json to exist after migration: %v", err)
	}

	// Verify loaded runs
	runs := store.GetRuns()
	if len(runs) != 1 || runs[0].ID != "run-legacy-1" {
		t.Fatalf("unexpected runs loaded: %+v", runs)
	}
}

func TestGetGroupHistoryCrossCategoryAndAll(t *testing.T) {
	store := NewResultStore("")
	store.exerciseMap = map[string]exerciseLocation{
		"code-1":   {GroupID: "coding", Base: "code-1"},
		"reason-1": {GroupID: "reasoning", Base: "reason-1"},
	}

	passed := true
	failed := false

	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)

	// An "all" run with tests across coding and reasoning
	run1 := BatteryRun{
		ID:        "run-all",
		Timestamp: t1,
		GroupID:   "all",
		GroupName: "All Tests",
		Models:    []string{"model-alpha"},
		Results: []TestResult{
			{TestID: "code-1", TestName: "Coding Test 1", Model: "model-alpha", Passed: &failed, ResponseTimeMs: 100},
			{TestID: "reason-1", TestName: "Reasoning Test 1", Model: "model-alpha", Passed: &passed, ResponseTimeMs: 200},
		},
	}

	// Later run updates code-1 to pass
	run2 := BatteryRun{
		ID:        "run-coding",
		Timestamp: t2,
		GroupID:   "coding",
		GroupName: "Coding",
		Models:    []string{"model-alpha"},
		Results: []TestResult{
			{TestID: "code-1", TestName: "Coding Test 1", Model: "model-alpha", Passed: &passed, ResponseTimeMs: 80},
		},
	}

	store.runs = []BatteryRun{run1, run2}

	// Coding should resolve code-1 from both runs, newest first (run2 passed)
	codingSummary := store.GetGroupHistory("coding")
	if len(codingSummary) != 1 {
		t.Fatalf("expected 1 model in coding summary, got %d", len(codingSummary))
	}
	if codingSummary[0].Passed != 1 || codingSummary[0].Failed != 0 || codingSummary[0].TotalTests != 1 {
		t.Fatalf("unexpected coding stats: %+v", codingSummary[0])
	}
	if codingSummary[0].AvgResponseMs != 80 {
		t.Fatalf("expected AvgResponseMs 80 from newest run, got %d", codingSummary[0].AvgResponseMs)
	}

	// Reasoning should resolve reason-1 from run1 (passed)
	reasonSummary := store.GetGroupHistory("reasoning")
	if len(reasonSummary) != 1 {
		t.Fatalf("expected 1 model in reasoning summary, got %d", len(reasonSummary))
	}
	if reasonSummary[0].Passed != 1 || reasonSummary[0].TotalTests != 1 {
		t.Fatalf("unexpected reasoning stats: %+v", reasonSummary[0])
	}

	// "all" should aggregate both code-1 (passed from run2) and reason-1 (passed from run1)
	allSummary := store.GetGroupHistory("all")
	if len(allSummary) != 1 {
		t.Fatalf("expected 1 model in all summary, got %d", len(allSummary))
	}
	if allSummary[0].TotalTests != 2 || allSummary[0].Passed != 2 {
		t.Fatalf("unexpected all stats: %+v", allSummary[0])
	}
}
