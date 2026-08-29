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
