package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// SystemPrompt represents a stored system prompt template.
type SystemPrompt struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Prompt    string `json:"prompt"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type systemPromptsFile struct {
	Prompts []SystemPrompt `json:"prompts"`
}

type systemPromptsStore struct {
	path    string
	mu      sync.RWMutex
	prompts []SystemPrompt
}

func newSystemPromptsStore(path string) *systemPromptsStore {
	return &systemPromptsStore{
		path:    path,
		prompts: make([]SystemPrompt, 0),
	}
}

func (s *systemPromptsStore) Load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file systemPromptsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if file.Prompts == nil {
		s.prompts = make([]SystemPrompt, 0)
	} else {
		s.prompts = file.Prompts
	}
	return nil
}

func (s *systemPromptsStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(systemPromptsFile{Prompts: s.prompts}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *systemPromptsStore) List() []SystemPrompt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SystemPrompt, len(s.prompts))
	copy(out, s.prompts)
	return out
}

func (s *systemPromptsStore) Get(id string) (SystemPrompt, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.prompts {
		if p.ID == id {
			return p, true
		}
	}
	return SystemPrompt{}, false
}

func generatePromptID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))[:16]
	}
	return hex.EncodeToString(b)
}

func (s *systemPromptsStore) Create(title, prompt string) (SystemPrompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	item := SystemPrompt{
		ID:        generatePromptID(),
		Title:     strings.TrimSpace(title),
		Prompt:    strings.TrimSpace(prompt),
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.prompts = append([]SystemPrompt{item}, s.prompts...)
	if err := s.saveLocked(); err != nil {
		return SystemPrompt{}, err
	}
	return item, nil
}

func (s *systemPromptsStore) Update(id, title, prompt string) (SystemPrompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.prompts {
		if p.ID == id {
			p.Title = strings.TrimSpace(title)
			p.Prompt = strings.TrimSpace(prompt)
			p.UpdatedAt = time.Now().Unix()
			s.prompts[i] = p
			if err := s.saveLocked(); err != nil {
				return SystemPrompt{}, err
			}
			return p, nil
		}
	}
	return SystemPrompt{}, errors.New("prompt not found")
}

func (s *systemPromptsStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.prompts {
		if p.ID == id {
			s.prompts = append(s.prompts[:i], s.prompts[i+1:]...)
			return s.saveLocked()
		}
	}
	return errors.New("prompt not found")
}

// HTTP Handlers

func (s *Server) handleListSystemPrompts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	prompts := s.systemPrompts.List()
	writeJSON(w, http.StatusOK, map[string]any{"prompts": prompts})
}

func (s *Server) handleCreateSystemPrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Title  string `json:"title"`
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Title) == "" && strings.TrimSpace(req.Prompt) == "" {
		http.Error(w, "title or prompt is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		req.Title = "Untitled Prompt"
	}
	item, err := s.systemPrompts.Create(req.Title, req.Prompt)
	if err != nil {
		http.Error(w, "could not save prompt: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleSystemPromptByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		p, ok := s.systemPrompts.Get(id)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, p)

	case http.MethodPut, http.MethodPatch:
		var req struct {
			Title  string `json:"title"`
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Title) == "" && strings.TrimSpace(req.Prompt) == "" {
			http.Error(w, "title or prompt is required", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Title) == "" {
			req.Title = "Untitled Prompt"
		}
		item, err := s.systemPrompts.Update(id, req.Title, req.Prompt)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, item)

	case http.MethodDelete:
		if err := s.systemPrompts.Delete(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
