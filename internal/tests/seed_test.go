package tests

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsSeedTestID(t *testing.T) {
	if !IsSeedTestID("example-arithmetic") {
		t.Fatal("expected example-arithmetic to be a seed test")
	}
	if IsSeedTestID("non-existent-test-id") {
		t.Fatal("expected random id not to be seed test")
	}
}

func TestPopulateSeedCreatesExamples(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)

	if err := store.PopulateSeed(); err != nil {
		t.Fatalf("PopulateSeed: %v", err)
	}

	groups, testsList := store.List()
	if len(groups) != 1 {
		t.Fatalf("expected 1 group (examples), got %d", len(groups))
	}
	if len(testsList) != 3 {
		t.Fatalf("expected 3 example tests, got %d", len(testsList))
	}

	// Verify file structure on disk
	catFile := filepath.Join(dir, "examples", "_category.yaml")
	if _, err := os.Stat(catFile); err != nil {
		t.Fatalf("expected _category.yaml in examples folder: %v", err)
	}

	arithFile := filepath.Join(dir, "examples", "arithmetic.yaml")
	if _, err := os.Stat(arithFile); err != nil {
		t.Fatalf("expected arithmetic.yaml in examples folder: %v", err)
	}

	// Test reload from disk: backfill adds the newer seeds (3 + 8).
	storeReloaded := New(dir)
	if err := storeReloaded.Load(); err != nil {
		t.Fatalf("Load reloaded: %v", err)
	}
	g2, t2 := storeReloaded.List()
	if len(g2) != 1 || len(t2) != 11 {
		t.Fatalf("expected 1 group and 11 tests on reload (3 seeds + 8 backfilled), got %d groups and %d tests", len(g2), len(t2))
	}
	for _, id := range backfillSeedIDs {
		if _, ok := storeReloaded.GetTest(id); !ok {
			t.Fatalf("expected backfilled seed %s", id)
		}
	}
	// Vision seed must carry its sidecar fixtures.
	vision, ok := storeReloaded.GetTest("example-vision-cases")
	if !ok {
		t.Fatal("expected backfilled example-vision-cases")
	}
	images, texts := 0, 0
	for _, c := range vision.Cases {
		for _, a := range c.Attachments {
			if a.Kind == "image" {
				images++
			}
			if a.Kind == "text" {
				texts++
			}
		}
	}
	if images != 2 || texts != 1 {
		t.Fatalf("expected 2 image + 1 text sidecars on vision seed, got %d images %d texts", images, texts)
	}

	// Verify multi-case and multi-step parsing
	foundCases := false
	foundSteps := false
	foundCaseOverride := false
	foundCaseSteps := false
	foundStepOptions := false
	for _, tst := range t2 {
		if len(tst.Cases) > 0 {
			foundCases = true
		}
		if len(tst.Steps) > 0 {
			foundSteps = true
		}
		for _, c := range tst.Cases {
			if c.SystemPrompt != "" {
				foundCaseOverride = true
			}
			if len(c.Steps) > 0 {
				foundCaseSteps = true
			}
			for _, s := range c.Steps {
				if s.Options != nil && s.Options.Temperature != nil {
					foundStepOptions = true
				}
			}
		}
	}
	if !foundCases {
		t.Fatal("expected test with Cases")
	}
	if !foundSteps {
		t.Fatal("expected test with Steps")
	}
	if !foundCaseOverride {
		t.Fatal("expected case with system_prompt override")
	}
	if !foundCaseSteps {
		t.Fatal("expected case with chained steps")
	}
	if !foundStepOptions {
		t.Fatal("expected chained step with options override")
	}
}

func TestMergeOptionsAndEffectiveSystemPrompt(t *testing.T) {
	baseTemp, overTemp := 0.2, 0.9
	baseMax := 100
	base := &TestOptions{Temperature: &baseTemp, MaxTokens: &baseMax}
	over := &TestOptions{Temperature: &overTemp}

	merged := MergeOptions(base, over)
	if merged.Temperature == nil || *merged.Temperature != overTemp {
		t.Fatalf("expected override temperature %v, got %+v", overTemp, merged)
	}
	if merged.MaxTokens == nil || *merged.MaxTokens != baseMax {
		t.Fatalf("expected inherited max_tokens %v, got %+v", baseMax, merged)
	}
	if MergeOptions(base, nil) != base {
		t.Fatal("expected nil override to return base unchanged")
	}

	if got := EffectiveSystemPrompt("global", ""); got != "global" {
		t.Fatalf("expected global fallback, got %q", got)
	}
	if got := EffectiveSystemPrompt("global", "custom"); got != "custom" {
		t.Fatalf("expected custom override, got %q", got)
	}
}

func TestCreateTestRejectsEmptyCase(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	_ = store.PopulateSeed()

	_, err := store.CreateTest(Test{
		Name:    "Bad Suite",
		GroupID: "custom",
		Active:  true,
		Cases:   []TestCase{{Name: "empty case"}},
	})
	if err == nil {
		t.Fatal("expected error for case without prompt or steps")
	}

	_, err = store.CreateTest(Test{
		Name:    "Bad Chain",
		GroupID: "custom",
		Active:  true,
		Cases: []TestCase{{
			Name:   "chain",
			Prompt: "setup",
			Steps:  []CaseStep{{Name: "empty step"}},
		}},
	})
	if err == nil {
		t.Fatal("expected error for step without prompt")
	}
}

func TestCreateUpdateDeleteTest(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	_ = store.PopulateSeed()

	created, err := store.CreateTest(Test{
		Name:           "Custom Logic Test",
		GroupID:        "custom",
		Active:         true,
		Prompt:         "Solve this puzzle",
		EvaluationType: "contains",
		Evaluation: &Evaluation{
			Type:     "contains",
			Expected: "solved",
		},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}

	if created.GroupID != "custom" {
		t.Fatalf("expected group custom, got %s", created.GroupID)
	}

	// Verify file on disk in custom directory
	createdPath := filepath.Join(dir, "custom", created.Filename)
	if _, err := os.Stat(createdPath); err != nil {
		t.Fatalf("expected file on disk %s: %v", createdPath, err)
	}

	// Update test
	updated, err := store.UpdateTest(created.ID, Test{
		Name:           "Updated Logic Test",
		GroupID:        "custom",
		Active:         false,
		Prompt:         "Solve this puzzle updated",
		EvaluationType: "exact_match",
	})
	if err != nil {
		t.Fatalf("UpdateTest: %v", err)
	}
	if updated.Active {
		t.Fatal("expected Active to be false after update")
	}

	// Delete test
	res, err := store.DeleteTest(created.ID)
	if err != nil {
		t.Fatalf("DeleteTest: %v", err)
	}
	if res.Reseeded {
		t.Fatal("user test should not reseed")
	}

	if _, ok := store.GetTest(created.ID); ok {
		t.Fatal("deleted test still found in store")
	}
	if _, err := os.Stat(createdPath); !errorsIsNotExist(err) {
		t.Fatalf("expected file to be removed from disk, stat err: %v", err)
	}
}

func errorsIsNotExist(err error) bool {
	return os.IsNotExist(err)
}
