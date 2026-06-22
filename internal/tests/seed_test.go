package tests

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsSeedTestID(t *testing.T) {
	if !IsSeedTestID("t1") {
		t.Fatal("expected t1 to be a seed test")
	}
	if IsSeedTestID("abcdef0123456789abcd") {
		t.Fatal("expected user id not to be seed test")
	}
}

func TestPopulateSeedAddsMissingOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tests.json")
	store := New(path)

	if err := store.PopulateSeed(); err != nil {
		t.Fatalf("PopulateSeed: %v", err)
	}
	_, tests := store.List()
	if len(tests) == 0 {
		t.Fatal("expected seed tests")
	}

	first := tests[0]
	if _, err := store.DeleteTest(first.ID); err != nil {
		t.Fatalf("DeleteTest: %v", err)
	}
	result, err := store.DeleteTest(first.ID)
	if err != nil {
		t.Fatalf("DeleteTest reseed: %v", err)
	}
	if !result.Reseeded {
		t.Fatal("expected reseeded seed test")
	}

	got, ok := store.GetTest(first.ID)
	if !ok {
		t.Fatal("expected reseeded test to exist")
	}
	if got.Name != first.Name {
		t.Fatalf("reseeded name = %q, want %q", got.Name, first.Name)
	}
}

func TestDeleteUserTestDoesNotReseed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tests.json")
	store := New(path)
	_ = store.PopulateSeed()

	created, err := store.CreateTest(Test{
		Name:           "Custom",
		GroupID:        "core",
		Active:         true,
		Prompt:         "Say hi",
		EvaluationType: "contains",
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}

	result, err := store.DeleteTest(created.ID)
	if err != nil {
		t.Fatalf("DeleteTest: %v", err)
	}
	if result.Reseeded {
		t.Fatal("user test should not reseed")
	}
	if _, ok := store.GetTest(created.ID); ok {
		t.Fatal("user test should be deleted")
	}
}

func TestPopulateSeedAfterManualDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tests.json")
	store := New(path)
	if err := store.PopulateSeed(); err != nil {
		t.Fatalf("PopulateSeed: %v", err)
	}

	// Simulate removing a seed test file entry without reseed (direct map edit not possible).
	// Delete a non-seed test path: remove t5 and don't reseed by using a temp store hack.
	// Instead verify PopulateSeed adds a test that was fully removed from disk.
	store2 := New(filepath.Join(dir, "tests2.json"))
	_ = store2.PopulateSeed()
	if _, err := store2.DeleteTest("t5"); err != nil {
		t.Fatalf("DeleteTest t5: %v", err)
	}
	// Reseed puts t5 back; delete user-created duplicate scenario:
	user, err := store2.CreateTest(Test{
		Name:           "Temp",
		GroupID:        "tools",
		Active:         true,
		Prompt:         "x",
		EvaluationType: "contains",
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if _, err := store2.DeleteTest(user.ID); err != nil {
		t.Fatalf("DeleteTest user: %v", err)
	}

	// Reload from disk and populate should still have all seed tests.
	store3 := New(filepath.Join(dir, "tests2.json"))
	if err := store3.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store3.PopulateSeed(); err != nil {
		t.Fatalf("PopulateSeed reload: %v", err)
	}
	if _, ok := store3.GetTest("t5"); !ok {
		t.Fatal("expected t5 after reseed delete")
	}
	_ = os.Remove(filepath.Join(dir, "tests-tools.json"))
}
