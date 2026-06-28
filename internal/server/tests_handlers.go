package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gense/ollama-manager/internal/tests"
)

var errMissingParam = errors.New("missing id parameter")

// ---------- tests ----------

func (s *Server) handleTestsList(w http.ResponseWriter, r *http.Request) {
	groups, tests := s.testsStore.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"groups": groups,
		"tests":  tests,
	})
}

func (s *Server) handleTestsCreate(w http.ResponseWriter, r *http.Request) {
	var in tests.Test
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.testsStore.CreateTest(in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTestsUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errMissingParam)
		return
	}
	var in tests.Test
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.testsStore.UpdateTest(id, in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTestsDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errMissingParam)
		return
	}
	if err := s.runnerStore.DeleteTestHistory(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	result, err := s.testsStore.DeleteTest(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"reseeded": result.Reseeded,
	})
}

func (s *Server) handleTestsReorder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Updates map[string]int `json:"updates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.testsStore.ReorderTest(body.Updates); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- test groups ----------

func (s *Server) handleTestGroupsCreate(w http.ResponseWriter, r *http.Request) {
	var in tests.Group
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.testsStore.CreateGroup(in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTestGroupsUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errMissingParam)
		return
	}
	var in tests.Group
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.testsStore.UpdateGroup(id, in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTestGroupsDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errMissingParam)
		return
	}
	if err := s.testsStore.DeleteGroup(id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
