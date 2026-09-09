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

func TestSetStageLimits(t *testing.T) {
	c := NewClient(nil)
	c.SetStageLimits(StageLimits{MaxTokens: 50000, MaxSeconds: 600})
	got := c.getStageLimits()
	if got.MaxTokens != 50000 || got.MaxSeconds != 600 {
		t.Fatalf("getStageLimits = %+v, want {50000 600}", got)
	}
}
