package runner

import (
	"testing"
)

func rulesClient(speeds map[string]float64) *Client {
	c := NewClient(nil)
	c.SetSpeedFunc(func(name string) (float64, bool) {
		v, ok := speeds[name]
		return v, ok
	})
	return c
}

func TestEffectiveStageCutLegacyWithoutRules(t *testing.T) {
	c := rulesClient(map[string]float64{"m": 150})
	l := StageLimits{MaxTokens: 10000, MaxSeconds: 300, Mode: "all"}
	toks, secs, mode := c.effectiveStageCut("m", l)
	if toks != 10000 || secs != 300 || mode != "all" {
		t.Fatalf("legacy fallback = (%d,%d,%q), want (10000,300,all)", toks, secs, mode)
	}
}

func TestEffectiveStageCutFirstMatchWins(t *testing.T) {
	c := rulesClient(map[string]float64{"m": 150})
	l := StageLimits{Rules: []StageSkipRule{
		{MinTPS: 100, MaxTPS: 200, MaxSeconds: 120, Mode: "any"},
		{MinTPS: 0, MaxTPS: 0, MaxSeconds: 600, Mode: "any"},
	}}
	toks, secs, mode := c.effectiveStageCut("m", l)
	if toks != 0 || secs != 120 || mode != "any" {
		t.Fatalf("first match = (%d,%d,%q), want (0,120,any)", toks, secs, mode)
	}
}

func TestEffectiveStageCutBoundaries(t *testing.T) {
	l := StageLimits{Rules: []StageSkipRule{
		{MinTPS: 100, MaxTPS: 200, MaxSeconds: 120},
	}}
	// Lower bound inclusive.
	c := rulesClient(map[string]float64{"m": 100})
	if _, secs, _ := c.effectiveStageCut("m", l); secs != 120 {
		t.Fatalf("min bound should match, got secs=%d", secs)
	}
	// Upper bound exclusive.
	c = rulesClient(map[string]float64{"m": 200})
	if toks, secs, _ := c.effectiveStageCut("m", l); toks != 0 || secs != 0 {
		t.Fatalf("max bound should not match, got (%d,%d)", toks, secs)
	}
	// Unbounded max matches fast models.
	l2 := StageLimits{Rules: []StageSkipRule{{MinTPS: 200, MaxSeconds: 60}}}
	c = rulesClient(map[string]float64{"m": 940})
	if _, secs, _ := c.effectiveStageCut("m", l2); secs != 60 {
		t.Fatalf("unbounded max should match fast model, got secs=%d", secs)
	}
}

func TestEffectiveStageCutUnknownSpeed(t *testing.T) {
	l := StageLimits{Rules: []StageSkipRule{
		{MinTPS: 100, MaxTPS: 200, MaxSeconds: 120},
		{MinTPS: 0, MaxTPS: 50, MaxSeconds: 300},
	}}
	c := rulesClient(nil) // no recorded speed
	toks, secs, _ := c.effectiveStageCut("new-model", l)
	if toks != 0 || secs != 300 {
		t.Fatalf("unknown speed should match rule starting at 0, got (%d,%d)", toks, secs)
	}
}

func TestEffectiveStageCutNoMatchDisables(t *testing.T) {
	c := rulesClient(map[string]float64{"m": 500})
	l := StageLimits{
		MaxTokens:  10000,
		MaxSeconds: 300,
		Mode:       "all",
		Rules:      []StageSkipRule{{MinTPS: 0, MaxTPS: 100, MaxSeconds: 60}},
	}
	toks, secs, mode := c.effectiveStageCut("m", l)
	if toks != 10000 || secs != 300 || mode != "all" {
		t.Fatalf("no matching rule should fall back to simple limits, got (%d,%d,%q)", toks, secs, mode)
	}
}

func TestEffectiveStageCutRuleAllMode(t *testing.T) {
	c := rulesClient(map[string]float64{"m": 75})
	l := StageLimits{Rules: []StageSkipRule{
		{MinTPS: 50, MaxTPS: 100, MaxTokens: 3000, MaxSeconds: 180, Mode: "all"},
	}}
	toks, secs, mode := c.effectiveStageCut("m", l)
	if toks != 3000 || secs != 180 || mode != "all" {
		t.Fatalf("rule cut = (%d,%d,%q), want (3000,180,all)", toks, secs, mode)
	}
	// Tokens alone must not trip in all mode.
	if fire, _ := evalStageSkip("response", 3001*4, 1000, toks, secs, mode); fire {
		t.Fatalf("all mode should not fire on tokens alone")
	}
	// Both together trip.
	if fire, _ := evalStageSkip("response", 3001*4, 181*1000, toks, secs, mode); !fire {
		t.Fatalf("all mode should fire when both trip")
	}
}

func TestEffectiveStageCutNoSpeedFunc(t *testing.T) {
	c := NewClient(nil) // no speed func: unknown speed
	l := StageLimits{Rules: []StageSkipRule{
		{MinTPS: 0, MaxSeconds: 60},
	}}
	if _, secs, _ := c.effectiveStageCut("m", l); secs != 60 {
		t.Fatalf("missing speed func should behave as unknown speed, got secs=%d", secs)
	}
}
