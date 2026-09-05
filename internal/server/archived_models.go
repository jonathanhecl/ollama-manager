package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

type archivedModelsFile struct {
	Models []string `json:"models"`
}

type archivedModelsStore struct {
	path   string
	mu     sync.RWMutex
	models map[string]bool
}

func newArchivedModelsStore(path string) *archivedModelsStore {
	return &archivedModelsStore{
		path:   path,
		models: make(map[string]bool),
	}
}

func (s *archivedModelsStore) Load() error {
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
	var file archivedModelsFile
	if err := json.Unmarshal(data, &file); err != nil {
		if q := quarantineCorrupt(s.path); q != "" {
			return fmt.Errorf("archived_models: corrupt %s quarantined to %s: %w", s.path, q, err)
		}
		return fmt.Errorf("archived_models: corrupt %s (quarantine failed): %w", s.path, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models = make(map[string]bool)
	for _, m := range file.Models {
		if m != "" {
			s.models[m] = true
		}
	}
	return nil
}

func (s *archivedModelsStore) IsArchived(name string) bool {
	if name == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.models[name]
}

func (s *archivedModelsStore) Archive(name string) error {
	if name == "" {
		return nil
	}
	s.mu.Lock()
	s.models[name] = true
	s.mu.Unlock()
	return s.save()
}

func (s *archivedModelsStore) Unarchive(name string) error {
	if name == "" {
		return nil
	}
	s.mu.Lock()
	delete(s.models, name)
	s.mu.Unlock()
	return s.save()
}

func (s *archivedModelsStore) save() error {
	if s.path == "" {
		return nil
	}
	// Full write lock for the whole snapshot+write: concurrent saves share
	// the same ".tmp" file, so they must serialize (last writer wins, but
	// the file on disk is always complete and valid).
	s.mu.Lock()
	defer s.mu.Unlock()
	file := archivedModelsFile{
		Models: make([]string, 0, len(s.models)),
	}
	for m := range s.models {
		file.Models = append(file.Models, m)
	}

	return writeJSONFileAtomic(s.path, file, 0o600)
}
