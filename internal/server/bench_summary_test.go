package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gense/ollama-manager/internal/runner"
	"github.com/gense/ollama-manager/internal/tests"
)

func fptr(v float64) *float64 { return &v }

func TestSummarizeBenchComplete(t *testing.T) {
	data := &LeaderboardData{
		Groups: []LeaderboardGroupCol{
			{ID: "g1", Name: "G1", ActiveTotal: 2, ActiveMaxPoints: 4},
			{ID: "g2", Name: "G2", ActiveTotal: 1, ActiveMaxPoints: 2},
		},
		Models: []LeaderboardModelRow{
			{
				Model:   "m",
				Overall: fptr(80),
				Scores: map[string]LeaderboardScore{
					"g1": {Score: fptr(80)},
					"g2": {Score: fptr(80)},
				},
			},
		},
	}
	out := summarizeBench(data, map[string][]string{"m": {"completion"}}, nil)
	sum, ok := out["m"]
	if !ok || !sum.Tested || !sum.Complete {
		t.Fatalf("expected tested+complete, got %+v ok=%v", sum, ok)
	}
	if len(sum.Missing) != 0 {
		t.Fatalf("expected no missing, got %v", sum.Missing)
	}
	if *sum.Overall != 80 {
		t.Fatalf("expected overall 80, got %v", *sum.Overall)
	}
}

func TestSummarizeBenchPartialAndZeroCountsAsMissing(t *testing.T) {
	data := &LeaderboardData{
		Groups: []LeaderboardGroupCol{
			{ID: "g1", Name: "G1", ActiveTotal: 1, ActiveMaxPoints: 2},
			{ID: "g2", Name: "G2", ActiveTotal: 1, ActiveMaxPoints: 2},
		},
		Models: []LeaderboardModelRow{
			{
				Model:   "m",
				Overall: fptr(25),
				Scores: map[string]LeaderboardScore{
					"g1": {Score: fptr(50)},
					"g2": {Score: fptr(0)},
				},
			},
		},
	}
	out := summarizeBench(data, map[string][]string{"m": {"completion"}}, nil)
	sum := out["m"]
	if !sum.Tested || sum.Complete {
		t.Fatalf("expected tested+incomplete, got %+v", sum)
	}
	if len(sum.Missing) != 1 || sum.Missing[0] != "g2" {
		t.Fatalf("expected missing=[g2], got %v", sum.Missing)
	}
}

func TestSummarizeBenchIncompatibleExcluded(t *testing.T) {
	data := &LeaderboardData{
		Groups: []LeaderboardGroupCol{
			{ID: "g1", Name: "G1", ActiveTotal: 1, ActiveMaxPoints: 2},
			{ID: "vision", Name: "V", RequiredCaps: []string{"vision"}, ActiveTotal: 1, ActiveMaxPoints: 2},
		},
		Models: []LeaderboardModelRow{
			{
				Model:   "m",
				Overall: fptr(100),
				Scores: map[string]LeaderboardScore{
					"g1": {Score: fptr(100)},
				},
			},
		},
	}
	out := summarizeBench(data, map[string][]string{"m": {"completion"}}, nil)
	sum := out["m"]
	if !sum.Complete {
		t.Fatalf("expected complete (vision incompatible), got %+v", sum)
	}
	if len(sum.Possible) != 1 || sum.Possible[0] != "g1" {
		t.Fatalf("expected possible=[g1], got %v", sum.Possible)
	}
}

func TestSummarizeBenchGhostAlwaysGrey(t *testing.T) {
	data := &LeaderboardData{
		Groups: []LeaderboardGroupCol{
			{ID: "g1", Name: "G1", ActiveTotal: 1, ActiveMaxPoints: 2},
		},
		Models: []LeaderboardModelRow{
			{Model: "ghost", Overall: fptr(60), Scores: map[string]LeaderboardScore{"g1": {Score: fptr(60)}}},
		},
	}
	out := summarizeBench(data, nil, map[string]bool{"ghost": true})
	sum := out["ghost"]
	if !sum.Tested || sum.Complete || len(sum.Missing) != 0 || *sum.Overall != 60 {
		t.Fatalf("expected tested grey-only ghost, got %+v", sum)
	}
}

func TestSummarizeBenchNoActiveGroups(t *testing.T) {
	data := &LeaderboardData{Groups: []LeaderboardGroupCol{{ID: "g1"}}}
	if out := summarizeBench(data, nil, nil); len(out) != 0 {
		t.Fatalf("expected empty, got %v", out)
	}
	if out := summarizeBench(nil, nil, nil); len(out) != 0 {
		t.Fatalf("expected empty for nil data, got %v", out)
	}
}

func TestModelsEndpointIncludesBenchSummary(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	if _, err := srv.testsStore.CreateGroup(tests.Group{ID: "g1", Name: "G One"}); err != nil {
		t.Fatal(err)
	}
	t1, err := srv.testsStore.CreateTest(tests.Test{Name: "T1", Prompt: "hi", GroupID: "g1", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	passed := true
	if err := srv.runnerStore.SaveRun(&runner.BatteryRun{
		ID:        "run-bench-sum",
		GroupID:   "g1",
		Timestamp: time.Now().UTC(),
		Models:    []string{"ext-m"},
		Results: []runner.TestResult{{
			TestID: t1.ID, TestName: t1.Name, Model: "ext-m", Passed: &passed,
			CasesTotal: 1, CasesPassed: 1, Points: 2.0, MaxPoints: 2.0,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.externalModels.Register("ext-m", "http://localhost:9/v1", "", []string{"completion"}, false); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/models returned %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Models []modelView `json:"models"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var found *modelView
	for i := range body.Models {
		if body.Models[i].Name == "ext-m" {
			found = &body.Models[i]
		}
	}
	if found == nil {
		t.Fatalf("external model missing from /api/models: %+v", body.Models)
	}
	// The seed data ships extra groups (e.g. "examples"), so ext-m is
	// tested in g1 but incomplete overall.
	if !found.BenchTested || found.BenchComplete || found.BenchOverall == nil || *found.BenchOverall != 100 {
		t.Fatalf("unexpected bench summary: %+v", *found)
	}
	contains := func(list []string, id string) bool {
		for _, v := range list {
			if v == id {
				return true
			}
		}
		return false
	}
	if !contains(found.BenchPossible, "g1") || contains(found.BenchMissing, "g1") {
		t.Fatalf("expected g1 possible and not missing: %+v", *found)
	}
	if len(found.BenchMissing) == 0 {
		t.Fatalf("expected seed groups to be missing: %+v", *found)
	}
}
