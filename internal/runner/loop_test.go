package runner

import (
	"strings"
	"testing"
)

func TestDetectRepetitionLoop(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantLoop    bool
		wantPattern string
	}{
		{
			name:     "empty string",
			input:    "",
			wantLoop: false,
		},
		{
			name:     "short string",
			input:    "Hello world!",
			wantLoop: false,
		},
		{
			name:     "normal code snippet",
			input:    "function total(arr) {\n  let s = 0;\n  for (let i = 0; i < arr.length; i++) {\n    s += arr[i];\n  }\n  return s;\n}",
			wantLoop: false,
		},
		{
			name:     "markdown divider should not trigger",
			input:    "Result:\n------------------------------------------------------------\nDone",
			wantLoop: false,
		},
		{
			name:     "dots should not trigger",
			input:    "Processing..................................................",
			wantLoop: false,
		},
		{
			name:        "ealealeal loop (from screenshot)",
			input:       "Here is the function: " + strings.Repeat("eal", 15),
			wantLoop:    true,
			wantPattern: "eal",
		},
		{
			name:        "repeated 2-char token loop",
			input:       "Start: " + strings.Repeat("ab", 20),
			wantLoop:    true,
			wantPattern: "ab",
		},
		{
			name:        "repeated sentence loop",
			input:       "Intro. " + strings.Repeat("I do not know the answer. ", 6),
			wantLoop:    true,
			wantPattern: "I do not know the answer. ",
		},
		{
			name:        "single alphanumeric char repeated 45 times",
			input:       "value: " + strings.Repeat("a", 45),
			wantLoop:    true,
			wantPattern: "a",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotLoop, gotPattern := detectRepetitionLoop(tc.input)
			if gotLoop != tc.wantLoop {
				t.Errorf("detectRepetitionLoop() gotLoop = %v, want %v (pattern: %q)", gotLoop, tc.wantLoop, gotPattern)
			}
			if tc.wantLoop && gotPattern != tc.wantPattern {
				t.Errorf("detectRepetitionLoop() gotPattern = %q, want %q", gotPattern, tc.wantPattern)
			}
		})
	}
}

func TestSkipCurrentTest(t *testing.T) {
	c := NewClient(nil)

	// No active test
	if c.SkipCurrentTest("non-existent") {
		t.Errorf("expected SkipCurrentTest to return false for non-existent run")
	}

	// Setup active test cancel
	runID := "run-123"
	called := false
	c.setTestCancel(runID, func(cause error) {
		called = true
		if cause != errManualSkip {
			t.Errorf("expected cause to be errManualSkip, got %v", cause)
		}
	})

	if !c.SkipCurrentTest(runID) {
		t.Errorf("expected SkipCurrentTest to return true")
	}
	if !called {
		t.Errorf("expected cancel func to be called")
	}

	c.clearTestCancel(runID)
	if c.SkipCurrentTest(runID) {
		t.Errorf("expected SkipCurrentTest to return false after clear")
	}
}

