package tests

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceMigration(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Dir(filepath.Dir(wd)) // from internal/tests -> root
	testingDir := filepath.Join(root, "testing")

	store := New(testingDir)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.PopulateSeed(); err != nil {
		t.Fatalf("PopulateSeed: %v", err)
	}

	groups, tests := store.List()
	t.Logf("Loaded %d groups and %d tests in testing directory", len(groups), len(tests))
}
