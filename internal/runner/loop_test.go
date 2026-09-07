package runner

import (
	"context"
	"errors"
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

func TestSkipCaseCancellation(t *testing.T) {
	c := NewClient(nil)
	parentCtx, parentCancel := context.WithCancelCause(context.Background())
	defer parentCancel(nil)

	runID := "run-multi-case"

	// Case 1 starts
	case1Ctx, case1Cancel := context.WithCancelCause(parentCtx)
	c.setTestCancel(runID, case1Cancel)

	// User clicks Skip Test during Case 1
	if !c.SkipCurrentTest(runID) {
		t.Fatalf("expected SkipCurrentTest to succeed for case 1")
	}

	// Verify Case 1 was cancelled with errManualSkip
	if case1Ctx.Err() == nil {
		t.Fatalf("expected case1Ctx to be cancelled")
	}
	if !errors.Is(context.Cause(case1Ctx), errManualSkip) {
		t.Fatalf("expected cause to be errManualSkip, got %v", context.Cause(case1Ctx))
	}

	// Verify parentCtx was NOT cancelled
	if parentCtx.Err() != nil {
		t.Fatalf("expected parentCtx to remain active, but got %v", parentCtx.Err())
	}

	// Case 1 finishes, Case 2 starts
	case1Cancel(nil)
	case2Ctx, case2Cancel := context.WithCancelCause(parentCtx)
	c.setTestCancel(runID, case2Cancel)
	defer case2Cancel(nil)

	// Verify Case 2 is completely clean and active
	if case2Ctx.Err() != nil {
		t.Fatalf("expected case2Ctx to be active and not cancelled, got %v", case2Ctx.Err())
	}
}

