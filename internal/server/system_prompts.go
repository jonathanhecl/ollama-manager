package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	Filename  string `json:"filename,omitempty"`
}

type legacySystemPromptsFile struct {
	Prompts []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Prompt    string `json:"prompt"`
		CreatedAt int64  `json:"created_at"`
		UpdatedAt int64  `json:"updated_at"`
	} `json:"prompts"`
}

type systemPromptsStore struct {
	dir        string
	legacyPath string
	mu         sync.RWMutex
}

func newSystemPromptsStore(dir string, legacyPath string) *systemPromptsStore {
	return &systemPromptsStore{
		dir:        dir,
		legacyPath: legacyPath,
	}
}

func sanitizePromptFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Untitled Prompt"
	}
	illegal := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*", "\x00"}
	for _, char := range illegal {
		name = strings.ReplaceAll(name, char, "-")
	}
	name = strings.TrimSpace(name)
	name = strings.Trim(name, ".")
	if name == "" {
		name = "prompt"
	}
	return name
}

func isPromptFile(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".txt", ".md", ".markdown", ".prompt", ".yaml", ".yml", ".json", ".text":
		return true
	default:
		return false
	}
}

func (s *systemPromptsStore) Load() error {
	if s.dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}

	// Migrate from legacy system_prompts.json if present and dir is empty
	if s.legacyPath != "" {
		if data, err := os.ReadFile(s.legacyPath); err == nil {
			entries, _ := os.ReadDir(s.dir)
			hasPrompts := false
			for _, e := range entries {
				if !e.IsDir() && isPromptFile(e.Name()) {
					hasPrompts = true
					break
				}
			}
			if !hasPrompts {
				var legacy legacySystemPromptsFile
				if err := json.Unmarshal(data, &legacy); err == nil && len(legacy.Prompts) > 0 {
					for _, p := range legacy.Prompts {
						baseName := sanitizePromptFilename(p.Title)
						fn := baseName + ".md"
						target := filepath.Join(s.dir, fn)
						count := 1
						for {
							if _, statErr := os.Stat(target); errors.Is(statErr, os.ErrNotExist) {
								break
							}
							fn = fmt.Sprintf("%s (%d).md", baseName, count)
							target = filepath.Join(s.dir, fn)
							count++
						}
						_ = os.WriteFile(target, []byte(p.Prompt), 0o644)
					}
					_ = os.Rename(s.legacyPath, s.legacyPath+".migrated")
				}
			}
		}
	}

	return nil
}

func (s *systemPromptsStore) safePath(id string) (string, error) {
	cleanID := filepath.Base(id)
	if cleanID == "." || cleanID == ".." || cleanID == "/" || cleanID == "\\" || cleanID == "" {
		return "", errors.New("invalid prompt id")
	}
	return filepath.Join(s.dir, cleanID), nil
}

func (s *systemPromptsStore) List() []SystemPrompt {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return []SystemPrompt{}
	}

	prompts := make([]SystemPrompt, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isPromptFile(entry.Name()) {
			continue
		}
		filePath := filepath.Join(s.dir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		info, err := entry.Info()
		modTime := time.Now().Unix()
		if err == nil {
			modTime = info.ModTime().Unix()
		}

		title := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		prompts = append(prompts, SystemPrompt{
			ID:        entry.Name(),
			Title:     title,
			Prompt:    string(data),
			CreatedAt: modTime,
			UpdatedAt: modTime,
			Filename:  entry.Name(),
		})
	}

	// Sort alphabetically by title
	sort.Slice(prompts, func(i, j int) bool {
		return strings.ToLower(prompts[i].Title) < strings.ToLower(prompts[j].Title)
	})

	return prompts
}

func (s *systemPromptsStore) Get(id string) (SystemPrompt, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	targetPath, err := s.safePath(id)
	if err != nil {
		return SystemPrompt{}, false
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		// Fallback: search by title / filename without extension
		entries, readErr := os.ReadDir(s.dir)
		if readErr == nil {
			cleanID := strings.TrimSuffix(filepath.Base(id), filepath.Ext(id))
			for _, entry := range entries {
				if entry.IsDir() || !isPromptFile(entry.Name()) {
					continue
				}
				entryTitle := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
				if strings.EqualFold(entryTitle, cleanID) || strings.EqualFold(entry.Name(), id) {
					fallbackPath := filepath.Join(s.dir, entry.Name())
					fbData, fbErr := os.ReadFile(fallbackPath)
					if fbErr == nil {
						info, _ := entry.Info()
						modTime := time.Now().Unix()
						if info != nil {
							modTime = info.ModTime().Unix()
						}
						return SystemPrompt{
							ID:        entry.Name(),
							Title:     entryTitle,
							Prompt:    string(fbData),
							CreatedAt: modTime,
							UpdatedAt: modTime,
							Filename:  entry.Name(),
						}, true
					}
				}
			}
		}
		return SystemPrompt{}, false
	}

	info, _ := os.Stat(targetPath)
	modTime := time.Now().Unix()
	if info != nil {
		modTime = info.ModTime().Unix()
	}

	filename := filepath.Base(targetPath)
	title := strings.TrimSuffix(filename, filepath.Ext(filename))

	return SystemPrompt{
		ID:        filename,
		Title:     title,
		Prompt:    string(data),
		CreatedAt: modTime,
		UpdatedAt: modTime,
		Filename:  filename,
	}, true
}

func (s *systemPromptsStore) Create(title, prompt string) (SystemPrompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return SystemPrompt{}, err
	}

	baseName := sanitizePromptFilename(title)
	filename := baseName + ".md"
	target := filepath.Join(s.dir, filename)

	count := 1
	for {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		filename = fmt.Sprintf("%s (%d).md", baseName, count)
		target = filepath.Join(s.dir, filename)
		count++
	}

	if err := os.WriteFile(target, []byte(prompt), 0o644); err != nil {
		return SystemPrompt{}, err
	}

	now := time.Now().Unix()
	return SystemPrompt{
		ID:        filename,
		Title:     strings.TrimSuffix(filename, filepath.Ext(filename)),
		Prompt:    prompt,
		CreatedAt: now,
		UpdatedAt: now,
		Filename:  filename,
	}, nil
}

func (s *systemPromptsStore) Update(id, title, prompt string) (SystemPrompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldPath, err := s.safePath(id)
	if err != nil {
		return SystemPrompt{}, err
	}

	if _, err := os.Stat(oldPath); errors.Is(err, os.ErrNotExist) {
		return SystemPrompt{}, errors.New("prompt not found")
	}

	oldExt := filepath.Ext(oldPath)
	if oldExt == "" {
		oldExt = ".md"
	}

	newBase := sanitizePromptFilename(title)
	newFilename := newBase + oldExt
	newPath := filepath.Join(s.dir, newFilename)

	if !strings.EqualFold(filepath.Base(oldPath), newFilename) {
		count := 1
		for {
			if _, statErr := os.Stat(newPath); errors.Is(statErr, os.ErrNotExist) {
				break
			}
			newFilename = fmt.Sprintf("%s (%d)%s", newBase, count, oldExt)
			newPath = filepath.Join(s.dir, newFilename)
			count++
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			return SystemPrompt{}, err
		}
	} else {
		newPath = oldPath
		newFilename = filepath.Base(oldPath)
	}

	if err := os.WriteFile(newPath, []byte(prompt), 0o644); err != nil {
		return SystemPrompt{}, err
	}

	now := time.Now().Unix()
	return SystemPrompt{
		ID:        newFilename,
		Title:     strings.TrimSuffix(newFilename, filepath.Ext(newFilename)),
		Prompt:    prompt,
		CreatedAt: now,
		UpdatedAt: now,
		Filename:  newFilename,
	}, nil
}

func (s *systemPromptsStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	targetPath, err := s.safePath(id)
	if err != nil {
		return err
	}

	if err := os.Remove(targetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("prompt not found")
		}
		return err
	}
	return nil
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
