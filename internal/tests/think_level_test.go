package tests

import (
	"testing"
)

func TestIsValidThinkLevel(t *testing.T) {
	valid := []string{"", "auto", "off", "low", "medium", "high", "max", "HIGH", " Off "}
	for _, v := range valid {
		if !IsValidThinkLevel(v) {
			t.Errorf("IsValidThinkLevel(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"bogus", "ultra", "0", "none"} {
		if IsValidThinkLevel(v) {
			t.Errorf("IsValidThinkLevel(%q) = true, want false", v)
		}
	}
}

func TestMergeOptionsThinkLevel(t *testing.T) {
	base := &TestOptions{ThinkLevel: "low"}
	if got := MergeOptions(base, nil); got != base {
		t.Fatalf("nil override should return base unchanged")
	}
	override := &TestOptions{ThinkLevel: "high"}
	if got := MergeOptions(base, override); got.ThinkLevel != "high" {
		t.Errorf("override think_level not applied, got %q", got.ThinkLevel)
	}
	empty := &TestOptions{}
	if got := MergeOptions(base, empty); got.ThinkLevel != "low" {
		t.Errorf("empty override should keep base think_level, got %q", got.ThinkLevel)
	}
}

func TestCreateTestRejectsInvalidThinkLevel(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.CreateTest(Test{
		Name:   "bad think",
		Prompt: "hi",
		Cases: []TestCase{{
			Prompt:  "hi",
			Options: &TestOptions{ThinkLevel: "ultra"},
		}},
	})
	if err == nil {
		t.Fatal("expected error for invalid case think_level")
	}
}

func TestCreateTestKeepsValidThinkLevel(t *testing.T) {
	s := New(t.TempDir())
	out, err := s.CreateTest(Test{
		Name:   "good think",
		Prompt: "hi",
		Cases: []TestCase{{
			Prompt:  "hi",
			Options: &TestOptions{ThinkLevel: "low"},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if out.Cases[0].Options == nil || out.Cases[0].Options.ThinkLevel != "low" {
		t.Fatalf("think_level not persisted: %+v", out.Cases[0].Options)
	}
	// Reload from disk to confirm YAML round-trip.
	s2 := New(s.Dir())
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := s2.GetTest(out.ID)
	if !ok {
		t.Fatal("created test not found after reload")
	}
	if got.Cases[0].Options == nil || got.Cases[0].Options.ThinkLevel != "low" {
		t.Fatalf("think_level lost in YAML round-trip: %+v", got.Cases[0].Options)
	}
}
