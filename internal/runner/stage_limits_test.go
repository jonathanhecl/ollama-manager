package runner

import (
	"errors"
	"fmt"
	"testing"
)

func TestEstimateStageTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"ab", 1},
		{"12345678", 2},
		{"this is a short ocr text of twenty four chars", 11},
	}
	for _, c := range cases {
		if got := estimateStageTokens(c.in); got != c.want {
			t.Errorf("estimateStageTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestAutoSkipStageMatches(t *testing.T) {
	wrapped := fmt.Errorf("auto-skipped: thinking stage exceeded %d tokens (~%d): %w", 50000, 51234, errAutoSkipStage)
	if !errors.Is(wrapped, errAutoSkipStage) {
		t.Fatalf("wrapped auto-skip error should match errAutoSkipStage")
	}
	if !isLoopOrSkip(wrapped.Error()) {
		t.Fatalf("auto-skip error %q should be treated as loop-or-skip", wrapped.Error())
	}
	// Manual skip behavior must be unchanged.
	if !isLoopOrSkip("manually skipped") {
		t.Fatalf("manual skip should still be treated as loop-or-skip")
	}
}

func TestUpdateProgressStreamStageCounters(t *testing.T) {
	c := NewClient(nil)
	c.setProgress(Progress{RunID: "r"})
	c.updateProgressStream("r", true, "content", "reason", 1234, 5678, 400, 800)
	p, ok := c.GetProgress("r")
	if !ok {
		t.Fatal("progress missing")
	}
	if p.ThinkingMs != 1234 || p.ResponseMs != 5678 || p.ThinkingChars != 400 || p.ResponseChars != 800 {
		t.Fatalf("stage counters not stored: %+v", p)
	}
	if !p.IsThinking || p.PartialResponse != "content" || p.PartialThinking != "reason" {
		t.Fatalf("stream fields not stored: %+v", p)
	}
}

func TestSetStageLimits(t *testing.T) {
	c := NewClient(nil)
	c.SetStageLimits(StageLimits{MaxTokens: 50000, MaxSeconds: 600, Mode: "all"})
	got := c.getStageLimits()
	if got.MaxTokens != 50000 || got.MaxSeconds != 600 || got.Mode != "all" {
		t.Fatalf("getStageLimits = %+v, want {50000 600 all}", got)
	}
}

func TestStageSkipDecisionAny(t *testing.T) {
	l := StageLimits{MaxTokens: 10000, MaxSeconds: 300, Mode: "any"}
	// 10001 tokens, barely any time: fires (either is enough).
	if fire, reason := l.stageSkipDecision("thinking", 10001*4, 1000); !fire {
		t.Fatalf("any mode should fire on tokens alone")
	} else if reason == "" {
		t.Fatalf("firing decision should carry a reason")
	}
	// Way over time, few tokens: fires.
	if fire, _ := l.stageSkipDecision("response", 100, 301*1000); !fire {
		t.Fatalf("any mode should fire on time alone")
	}
	// Within both: no fire.
	if fire, _ := l.stageSkipDecision("thinking", 100, 1000); fire {
		t.Fatalf("any mode should not fire within limits")
	}
	// Disabled: never fires.
	if fire, _ := (StageLimits{}).stageSkipDecision("thinking", 1<<30, 1<<40); fire {
		t.Fatalf("disabled limits should never fire")
	}
}

func TestStageSkipDecisionAll(t *testing.T) {
	l := StageLimits{MaxTokens: 10000, MaxSeconds: 300, Mode: "all"}
	// Only tokens exceeded: no fire.
	if fire, _ := l.stageSkipDecision("thinking", 10001*4, 1000); fire {
		t.Fatalf("all mode should not fire on tokens alone")
	}
	// Only time exceeded: no fire.
	if fire, _ := l.stageSkipDecision("response", 100, 301*1000); fire {
		t.Fatalf("all mode should not fire on time alone")
	}
	// Both exceeded at once: fires.
	if fire, reason := l.stageSkipDecision("thinking", 10001*4, 301*1000); !fire {
		t.Fatalf("all mode should fire when both trip together")
	} else if reason == "" {
		t.Fatalf("firing decision should carry a reason")
	}
	// Single enabled condition degrades to any-like behavior.
	single := StageLimits{MaxSeconds: 300, Mode: "all"}
	if fire, _ := single.stageSkipDecision("response", 100, 301*1000); !fire {
		t.Fatalf("all mode with a single limit should fire on it")
	}
}

func TestHasOpenThinkTag(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"Hello world", false},
		{"<think>This is thinking", true},
		{"<think>This was thinking</think> This is response", false},
		{"<thinking>Long reasoning", true},
		{"<thinking>Long reasoning</thinking>", false},
		{"<think>\nMultiline\n</think>\nAnd <think>more thinking", true},
		{"<think>Nested</think> <stitching>stitching mode", true},
		{"<stitching>stitching</stitching>", false},
		{"<throat>throat</throat>", false},
		{"<THINKING>upper case", true},
		{"<think level=\"high\">with attrs", true},
	}
	for _, tc := range cases {
		got := hasOpenThinkTag(tc.input)
		if got != tc.want {
			t.Errorf("hasOpenThinkTag(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestSplitInlineReasoning(t *testing.T) {
	cases := []struct {
		name         string
		input        string
		wantThinking string
		wantAnswer   string
	}{
		{
			name:         "no tags",
			input:        "just an answer",
			wantThinking: "",
			wantAnswer:   "just an answer",
		},
		{
			name:         "think block then answer",
			input:        "<think>reasoning</think>\n\n{\"a\": 1}",
			wantThinking: "reasoning",
			wantAnswer:   `{"a": 1}`,
		},
		{
			name:         "thinking tag treated like think",
			input:        "<thinking>reasoning</thinking>\n\nfinal",
			wantThinking: "reasoning",
			wantAnswer:   "final",
		},
		{
			name:         "case-insensitive and attributes",
			input:        "<THINK level=\"high\">reasoning</THINK>\nfinal",
			wantThinking: "reasoning",
			wantAnswer:   "final",
		},
		{
			name:         "lone closing tag before answer",
			input:        "{\"a\": 1}\n</think>\n\n{\"a\": 1}",
			wantThinking: `{"a": 1}`,
			wantAnswer:   `{"a": 1}`,
		},
		{
			name:         "unclosed block runs to end",
			input:        "<think>still reasoning",
			wantThinking: "still reasoning",
			wantAnswer:   "",
		},
		{
			name:         "multiple blocks",
			input:        "<think>one</think>mid<thinking>two</thinking>end",
			wantThinking: "one\ntwo",
			wantAnswer:   "midend",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			thinking, answer := splitInlineReasoning(tc.input)
			if thinking != tc.wantThinking || answer != tc.wantAnswer {
				t.Fatalf("splitInlineReasoning(%q) = (%q, %q), want (%q, %q)",
					tc.input, thinking, answer, tc.wantThinking, tc.wantAnswer)
			}
		})
	}
}
