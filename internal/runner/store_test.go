package runner

import (
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

	histFile := filepath.Join(dir, "core", "_history.json")
	if _, err := os.Stat(histFile); err != nil {
		t.Fatalf("expected _history.json to exist in core folder: %v", err)
	}

	if err := store.DeleteTestHistory("t1"); err != nil {
		t.Fatalf("DeleteTestHistory: %v", err)
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
	if len(store.runs) != 0 {
		t.Fatalf("runs = %d, want 0 after removing last result", len(store.runs))
	}
}
