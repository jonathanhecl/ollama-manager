package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gense/ollama-manager/internal/runner"
	"github.com/gense/ollama-manager/internal/tests"
)

// ---------- battery runner ----------

func (s *Server) handleBatteryRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GroupID  string   `json:"group_id"`
		GroupIDs []string `json:"group_ids"`
		TestID   string   `json:"test_id"`
		ModelIDs []string `json:"model_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid body"))
		return
	}
	if len(body.ModelIDs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("at least one model is required"))
		return
	}

	if activeProg, ok := s.runner.GetActiveProgress(); ok {
		if activeProg.WaitingReview {
			writeError(w, http.StatusConflict, errors.New("a battery run is awaiting human review: "+activeProg.RunID))
		} else {
			writeError(w, http.StatusConflict, errors.New("a battery run is already in progress: "+activeProg.RunID))
		}
		return
	}
	if s.runnerStore != nil {
		if run, _, ok := s.runnerStore.GetLatestRunPendingReview(); ok {
			writeError(w, http.StatusConflict, errors.New("a battery run is awaiting human review: "+run.ID))
			return
		}
	}

	var group tests.Group
	var testsList []tests.Test

	if body.TestID != "" {
		test, ok := s.testsStore.GetTest(body.TestID)
		if !ok {
			writeError(w, http.StatusNotFound, errors.New("test not found"))
			return
		}
		if g, ok := s.testsStore.GetGroup(test.GroupID); ok {
			group = g
		} else {
			group = tests.Group{ID: test.GroupID, Name: test.Name}
		}
		testsList = []tests.Test{test}
	} else if len(body.GroupIDs) > 0 {
		// Multi-category run: tests from any of the given groups.
		allowed := make(map[string]bool, len(body.GroupIDs))
		var resolved []tests.Group
		for _, gid := range body.GroupIDs {
			if allowed[gid] {
				continue
			}
			g, ok := s.testsStore.GetGroup(gid)
			if !ok {
				writeError(w, http.StatusNotFound, errors.New("group not found: "+gid))
				return
			}
			allowed[gid] = true
			resolved = append(resolved, g)
		}
		_, allTests := s.testsStore.List()
		for _, t := range allTests {
			if allowed[t.GroupID] {
				testsList = append(testsList, t)
			}
		}
		if len(resolved) == 1 {
			group = resolved[0]
		} else {
			names := make([]string, 0, len(resolved))
			for _, g := range resolved {
				if g.Name != "" {
					names = append(names, g.Name)
				} else {
					names = append(names, g.ID)
				}
			}
			group = tests.Group{ID: "all", Name: strings.Join(names, ", ")}
		}
	} else if body.GroupID == "" || body.GroupID == "all" {
		group = tests.Group{ID: "all", Name: "All Tests"}
		_, allTests := s.testsStore.List()
		testsList = allTests
	} else {
		g, ok := s.testsStore.GetGroup(body.GroupID)
		if !ok {
			writeError(w, http.StatusNotFound, errors.New("group not found"))
			return
		}
		group = g
		_, allTests := s.testsStore.List()
		for _, t := range allTests {
			if t.GroupID == body.GroupID {
				testsList = append(testsList, t)
			}
		}
	}

	// Fail fast on unknown evaluation types (they would score silent false).
	if err := runner.ValidateTestsForBattery(testsList); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Effective caps: union of test-level and category-level required_caps,
	// so a category marked vision/audio skips models lacking it even when
	// individual tests don't declare it.
	_, allGroups := s.testsStore.List()
	groupCaps := make(map[string][]string, len(allGroups))
	for _, g := range allGroups {
		if len(g.RequiredCaps) > 0 {
			groupCaps[g.ID] = g.RequiredCaps
		}
	}
	if len(groupCaps) > 0 {
		for i, tst := range testsList {
			gc := groupCaps[tst.GroupID]
			if len(gc) == 0 {
				continue
			}
			seen := make(map[string]bool, len(tst.RequiredCaps)+len(gc))
			merged := make([]string, 0, len(tst.RequiredCaps)+len(gc))
			for _, c := range tst.RequiredCaps {
				k := strings.ToLower(strings.TrimSpace(c))
				if k == "" || seen[k] {
					continue
				}
				seen[k] = true
				merged = append(merged, c)
			}
			for _, c := range gc {
				k := strings.ToLower(strings.TrimSpace(c))
				if k == "" || seen[k] {
					continue
				}
				seen[k] = true
				merged = append(merged, strings.ToLower(c))
			}
			testsList[i].RequiredCaps = merged
		}
	}

	// Fetch capabilities for selected models.
	ctx := r.Context()
	models, err := s.ollama.List(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	modelMeta := s.fetchModelMeta(ctx, models)
	modelCaps := make(map[string][]string, len(body.ModelIDs))
	for _, m := range models {
		for _, sel := range body.ModelIDs {
			if m.Name == sel {
				modelCaps[sel] = modelMeta[m.Digest].Capabilities
				break
			}
		}
	}

	// Detect system info for this run.
	sysInfo := runner.DetectSysInfo()

	// Snapshot the per-stage auto-skip limits so this run enforces the
	// testing settings active at start time.
	s.cfgMu.RLock()
	testingLimits := s.cfg.Testing
	s.cfgMu.RUnlock()
	s.runner.SetStageLimits(runner.StageLimits{
		MaxTokens:  testingLimits.MaxStageTokens,
		MaxSeconds: testingLimits.MaxStageSeconds,
		Mode:       testingLimits.Mode,
	})

	// Use background context so async execution survives HTTP request completion.
	bgCtx := context.Background()
	runID := s.runner.ExecuteBatteryAsync(bgCtx, group, testsList, body.ModelIDs, modelCaps, sysInfo, func(run *runner.BatteryRun) {
		_ = s.runnerStore.SaveRun(run)
		if run != nil {
			for _, testRes := range run.Results {
				if testRes.Model != "" {
					if testRes.TokensPerSec > 0 {
						s.recordModelTPS(testRes.Model, testRes.TokensPerSec, run.Timestamp)
					} else {
						s.recordModelTPS(testRes.Model, 0, run.Timestamp)
					}
					for _, sub := range testRes.SubResults {
						if sub.TokensPerSec > 0 {
							s.recordModelTPS(testRes.Model, sub.TokensPerSec, run.Timestamp)
						}
					}
				}
			}
		}
		hasPendingReviews := false
		if run != nil {
			for _, r := range run.Results {
				if r.Passed == nil && r.Error == "" {
					hasPendingReviews = true
					break
				}
			}
		}
		if !hasPendingReviews && run != nil {
			s.runner.ClearProgress(run.ID)
		}
	})
	writeJSON(w, http.StatusOK, map[string]string{"run_id": runID})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	runs := s.runnerStore.GetRuns()
	// Return lightweight view.
	type lightRun struct {
		ID         string   `json:"id"`
		Timestamp  string   `json:"timestamp"`
		GroupName  string   `json:"group_name"`
		Models     []string `json:"models"`
		TestIDs    []string `json:"test_ids"`
		TestCount  int      `json:"test_count"`
		PassCount  int      `json:"pass_count"`
		FailCount  int      `json:"fail_count"`
		TotalCount int      `json:"total_count"`
	}
	out := make([]lightRun, 0, len(runs))
	for _, run := range runs {
		lr := lightRun{
			ID:        run.ID,
			Timestamp: run.Timestamp.Format("2006-01-02T15:04:05Z"),
			GroupName: run.GroupName,
			Models:    run.Models,
		}
		tids := make(map[string]bool)
		for _, res := range run.Results {
			if res.TestID != "" {
				tids[res.TestID] = true
			}
			lr.TotalCount++
			if res.Passed != nil {
				if *res.Passed {
					lr.PassCount++
				} else {
					lr.FailCount++
				}
			} else {
				lr.TestCount++ // human_review or skipped
			}
		}
		lr.TestIDs = make([]string, 0, len(tids))
		for tid := range tids {
			lr.TestIDs = append(lr.TestIDs, tid)
		}
		out = append(out, lr)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing run id"))
		return
	}
	run, ok := s.runnerStore.GetRun(id)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("run not found"))
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleRateRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing run id"))
		return
	}
	var body struct {
		TestID string `json:"test_id"`
		Model  string `json:"model"`
		Rating string `json:"rating"` // "bad", "regular", "good"
		Passed *bool  `json:"passed,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid body"))
		return
	}
	if body.TestID == "" || body.Model == "" {
		writeError(w, http.StatusBadRequest, errors.New("test_id and model are required"))
		return
	}
	if body.Passed != nil {
		if err := s.runnerStore.UpdateResultPassed(id, body.TestID, body.Model, *body.Passed); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if s.runner != nil {
			s.runner.RateReviewResult(id, body.TestID, body.Model, *body.Passed)
		}
	} else if body.Rating != "" {
		if err := s.runnerStore.UpdateHumanRating(id, body.TestID, body.Model, body.Rating); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if s.runner != nil {
			s.runner.RateReviewResult(id, body.TestID, body.Model, body.Rating == "good")
		}
	} else {
		writeError(w, http.StatusBadRequest, errors.New("rating or passed is required"))
		return
	}
	if s.runnerStore != nil {
		if run, ok := s.runnerStore.GetRun(id); ok {
			pending := 0
			for _, res := range run.Results {
				if res.Passed == nil && res.Error == "" {
					pending++
				}
			}
			if pending == 0 && s.runner != nil {
				s.runner.ClearProgress(id)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing run id"))
		return
	}
	if err := s.runnerStore.DeleteRun(id); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeleteModelHistory removes all battery results for one model across
// every run. The model name travels in a wildcard segment because names may
// contain slashes (e.g. hf.co/org/model:tag).
func (s *Server) handleDeleteModelHistory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	if err := s.runnerStore.DeleteModelHistory(name); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCancelBatteryRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing run id"))
		return
	}
	cancelled := s.runner.CancelRun(id)
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": cancelled})
}

func (s *Server) handleRetryBatteryTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing run id"))
		return
	}
	retried := s.runner.RetryCurrentTest(id)
	writeJSON(w, http.StatusOK, map[string]any{"retried": retried})
}

func (s *Server) handleSkipBatteryTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing run id"))
		return
	}
	skipped := s.runner.SkipCurrentTest(id)
	writeJSON(w, http.StatusOK, map[string]any{"skipped": skipped})
}

func (s *Server) handleSkipBatteryModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing run id"))
		return
	}
	skipped := s.runner.SkipCurrentModel(id)
	writeJSON(w, http.StatusOK, map[string]any{"skipped": skipped})
}

func (s *Server) handleAbortBatteryRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing run id"))
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Mode != "save-completed" {
		body.Mode = "discard"
	}
	aborted := s.runner.AbortRun(id, body.Mode)
	writeJSON(w, http.StatusOK, map[string]any{"aborted": aborted, "mode": body.Mode})
}

func (s *Server) handleBatteryProgress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing run id"))
		return
	}
	p, ok := s.runner.GetProgress(id)
	if !ok {
		// If no active progress, maybe it's already done — return done flag.
		writeJSON(w, http.StatusOK, runner.Progress{RunID: id, Done: true})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleActiveBatteryRun(w http.ResponseWriter, r *http.Request) {
	p, ok := s.runner.GetActiveProgress()
	if !ok && s.runnerStore != nil {
		if run, pending, hasPending := s.runnerStore.GetLatestRunPendingReview(); hasPending {
			p = runner.Progress{
				RunID:          run.ID,
				GroupName:      run.GroupName,
				GroupID:        run.GroupID,
				Models:         run.Models,
				TotalTests:     len(run.Results),
				TestIndex:      len(run.Results),
				WaitingReview:  true,
				PendingReviews: pending,
				Results:        run.Results,
			}
			ok = true
		}
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"active": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"active":          true,
		"run_id":          p.RunID,
		"waiting_review":  p.WaitingReview,
		"pending_reviews": p.PendingReviews,
		"progress":        p,
	})
}

func (s *Server) handleGetTestHistory(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")
	if testID == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing test id"))
		return
	}
	history := s.runnerStore.GetTestHistory(testID)
	writeJSON(w, http.StatusOK, map[string]any{"history": history})
}

func (s *Server) handleGetGroupHistory(w http.ResponseWriter, r *http.Request) {
	groupID := r.PathValue("id")
	if groupID == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing group id"))
		return
	}
	summary := s.runnerStore.GetGroupHistory(groupID)
	writeJSON(w, http.StatusOK, map[string]any{"summary": summary})
}

func (s *Server) handleSysInfo(w http.ResponseWriter, r *http.Request) {
	info := runner.DetectSysInfo()
	writeJSON(w, http.StatusOK, info)
}

// Ensure runner types are used.
var _ = runner.BatteryRun{}
