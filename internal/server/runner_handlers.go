package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gense/ollama-manager/internal/runner"
)

// ---------- battery runner ----------

func (s *Server) handleBatteryRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GroupID  string   `json:"group_id"`
		ModelIDs []string `json:"model_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid body"))
		return
	}
	if body.GroupID == "" {
		writeError(w, http.StatusBadRequest, errors.New("group_id is required"))
		return
	}
	if len(body.ModelIDs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("at least one model is required"))
		return
	}

	group, ok := s.testsStore.GetGroup(body.GroupID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("group not found"))
		return
	}

	_, testsList := s.testsStore.List()

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

	// Use background context so async execution survives HTTP request completion.
	bgCtx := context.Background()
	runID := s.runner.ExecuteBatteryAsync(bgCtx, group, testsList, body.ModelIDs, modelCaps, sysInfo, func(run *runner.BatteryRun) {
		_ = s.runnerStore.SaveRun(run)
		s.runner.ClearProgress(run.ID)
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
		for _, res := range run.Results {
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
	} else if body.Rating != "" {
		if err := s.runnerStore.UpdateHumanRating(id, body.TestID, body.Model, body.Rating); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
	} else {
		writeError(w, http.StatusBadRequest, errors.New("rating or passed is required"))
		return
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

func (s *Server) handleCancelBatteryRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing run id"))
		return
	}
	cancelled := s.runner.CancelRun(id)
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": cancelled})
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
