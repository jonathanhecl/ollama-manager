package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gense/ollama-manager/internal/tests"
)

var errMissingParam = errors.New("missing id parameter")

// ---------- tests ----------

func (s *Server) invalidateTestsCache() {
	s.testsCacheMu.Lock()
	s.testsCacheJSON = nil
	s.testsCacheETag = fmt.Sprintf("\"%d\"", time.Now().UnixNano())
	s.testsCacheMu.Unlock()
}

func (s *Server) handleTestsList(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("refresh") == "true" || r.URL.Query().Get("refresh") == "1" {
		_ = s.testsStore.Load()
		s.invalidateTestsCache()
	}

	s.testsCacheMu.RLock()
	cachedJSON := s.testsCacheJSON
	etag := s.testsCacheETag
	s.testsCacheMu.RUnlock()

	clientETag := r.Header.Get("If-None-Match")
	if etag != "" && clientETag == etag && cachedJSON != nil {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if cachedJSON == nil {
		s.testsCacheMu.Lock()
		if s.testsCacheJSON == nil {
			groups, tests := s.testsStore.List()
			raw, err := json.Marshal(map[string]any{
				"groups": groups,
				"tests":  tests,
			})
			if err != nil {
				s.testsCacheMu.Unlock()
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			s.testsCacheJSON = raw
			if s.testsCacheETag == "" {
				s.testsCacheETag = fmt.Sprintf("\"%d\"", time.Now().UnixNano())
			}
		}
		cachedJSON = s.testsCacheJSON
		etag = s.testsCacheETag
		s.testsCacheMu.Unlock()
	}

	if clientETag == etag && clientETag != "" {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(cachedJSON)
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
	s.invalidateTestsCache()
	go s.RegenerateLeaderboardCache()
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
	s.invalidateTestsCache()
	go s.RegenerateLeaderboardCache()
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
	s.invalidateTestsCache()
	go s.RegenerateLeaderboardCache()
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
	s.invalidateTestsCache()
	go s.RegenerateLeaderboardCache()
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
	s.invalidateTestsCache()
	go s.RegenerateLeaderboardCache()
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
	s.invalidateTestsCache()
	go s.RegenerateLeaderboardCache()
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
	s.invalidateTestsCache()
	go s.RegenerateLeaderboardCache()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- test sidecars ----------

// handleTestSidecarUpload stores a <base>-<index>.<ext> sidecar file for one
// case/step of a test. Body: {index (1-based), filename, data (base64)}.
func (s *Server) handleTestSidecarUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errMissingParam)
		return
	}
	var body struct {
		Index    int    `json:"index"`
		Filename string `json:"filename"`
		Data     string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("data is not valid base64"))
		return
	}
	att, err := s.testsStore.SaveSidecar(id, body.Index, body.Filename, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.invalidateTestsCache()
	writeJSON(w, http.StatusOK, att)
}

// handleTestSidecarDelete removes the sidecar file bound to one case/step.
func (s *Server) handleTestSidecarDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errMissingParam)
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid sidecar index"))
		return
	}
	if err := s.testsStore.DeleteSidecar(id, index); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.invalidateTestsCache()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
