package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gense/ollama-manager/internal/agent"
)

// AgentTestSessionRequest starts a new agent-test session.
type AgentTestSessionRequest struct {
	ModelID string            `json:"model_id"`
	TestID  string            `json:"test_id"`
	Config  agent.AgentConfig `json:"config"`
}

// AgentTestMessageRequest appends a human feedback turn.
type AgentTestMessageRequest struct {
	Content string `json:"content"`
}

// AgentTestToolRequest manually executes a tool call.
type AgentTestToolRequest struct {
	Call agent.ToolCall `json:"call"`
}

func (s *Server) handleAgentSessionsCreate(w http.ResponseWriter, r *http.Request) {
	var req AgentTestSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.TestID == "" {
		writeError(w, http.StatusBadRequest, errors.New("test_id is required"))
		return
	}

	sess, err := s.agentStore.Create(req.ModelID, req.TestID, req.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleAgentSessionsList(w http.ResponseWriter, r *http.Request) {
	list := s.agentStore.List()
	writeJSON(w, http.StatusOK, map[string]any{"sessions": list})
}

func (s *Server) handleAgentSessionGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing session id"))
		return
	}
	sess, ok := s.agentStore.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}

	type response struct {
		agent.Session
		Files []agent.FileNode `json:"files"`
	}
	resp := response{Session: sess}
	if sb := s.agentStore.Sandbox(id); sb != nil {
		files, _ := sb.ListFiles()
		resp.Files = files
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAgentSessionMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing session id"))
		return
	}
	var req AgentTestMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	sess, ok := s.agentStore.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}

	turn := agent.Turn{
		Number:    sess.CurrentTurn + 1,
		Role:      agent.RoleUser,
		Content:   req.Content,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.agentStore.AddTurn(id, turn); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.agentStore.UpdateStatus(id, agent.StatusRunning)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAgentSessionTool(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing session id"))
		return
	}
	var req AgentTestToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	sb := s.agentStore.Sandbox(id)
	if sb == nil {
		writeError(w, http.StatusNotFound, errors.New("sandbox not found"))
		return
	}

	reg := agent.DefaultRegistry()
	result := reg.Run(sb, req.Call)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAgentSessionReset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing session id"))
		return
	}
	var req struct {
		Config agent.AgentConfig `json:"config"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := s.agentStore.Reset(id, req.Config); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAgentSessionDestroy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing session id"))
		return
	}
	if err := s.agentStore.Destroy(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAgentSessionFiles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing session id"))
		return
	}
	sb := s.agentStore.Sandbox(id)
	if sb == nil {
		writeError(w, http.StatusNotFound, errors.New("sandbox not found"))
		return
	}

	path := r.URL.Query().Get("path")
	if path != "" {
		content, err := sb.ReadFile(path)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":    path,
			"content": content,
		})
		return
	}

	files, err := sb.ListFiles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}
