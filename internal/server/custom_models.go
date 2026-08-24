package server

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

// CustomModelRecord stores persistent metadata about a locally created or repaired model.
type CustomModelRecord struct {
	BaseModel string    `json:"base_model,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type customModelsFile struct {
	Models map[string]CustomModelRecord `json:"models"`
}

type customModelsStore struct {
	path   string
	mu     sync.RWMutex
	models map[string]CustomModelRecord
}

func newCustomModelsStore(path string) *customModelsStore {
	return &customModelsStore{
		path:   path,
		models: make(map[string]CustomModelRecord),
	}
}

func (s *customModelsStore) Load() error {
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
	var file customModelsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cleaned := false
	s.models = make(map[string]CustomModelRecord)
	if file.Models != nil {
		for k, v := range file.Models {
			// If an entry was mistakenly registered with a local path/blob as base_model, purge it.
			if v.BaseModel != "" && isLocalFilePathOrDigest(v.BaseModel) {
				cleaned = true
				continue
			}
			s.models[k] = v
		}
	}
	if cleaned {
		// Save sanitized file to disk
		cleanFile := customModelsFile{
			Models: make(map[string]CustomModelRecord, len(s.models)),
		}
		for k, v := range s.models {
			cleanFile.Models[k] = v
		}
		if data, err := json.MarshalIndent(cleanFile, "", "  "); err == nil {
			data = append(data, '\n')
			tmp := s.path + ".tmp"
			if err := os.WriteFile(tmp, data, 0o600); err == nil {
				_ = os.Rename(tmp, s.path)
			}
		}
	}
	return nil
}

func (s *customModelsStore) IsCustom(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if rec, ok := s.models[name]; ok {
		if rec.BaseModel == "" || !isLocalFilePathOrDigest(rec.BaseModel) {
			return true
		}
	}
	if strings.HasSuffix(name, ":latest") {
		if rec, ok := s.models[strings.TrimSuffix(name, ":latest")]; ok {
			if rec.BaseModel == "" || !isLocalFilePathOrDigest(rec.BaseModel) {
				return true
			}
		}
	} else {
		if rec, ok := s.models[name+":latest"]; ok {
			if rec.BaseModel == "" || !isLocalFilePathOrDigest(rec.BaseModel) {
				return true
			}
		}
	}
	return isFixedModelName(name)
}

func (s *customModelsStore) GetBase(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if rec, ok := s.models[name]; ok && rec.BaseModel != "" && !isLocalFilePathOrDigest(rec.BaseModel) {
		return rec.BaseModel
	}
	if strings.HasSuffix(name, ":latest") {
		if rec, ok := s.models[strings.TrimSuffix(name, ":latest")]; ok && rec.BaseModel != "" && !isLocalFilePathOrDigest(rec.BaseModel) {
			return rec.BaseModel
		}
	} else {
		if rec, ok := s.models[name+":latest"]; ok && rec.BaseModel != "" && !isLocalFilePathOrDigest(rec.BaseModel) {
			return rec.BaseModel
		}
	}
	if isFixedModelName(name) {
		return fixedBaseName(name)
	}
	return ""
}

func (s *customModelsStore) Register(name, baseModel string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	baseModel = strings.TrimSpace(baseModel)
	if isLocalFilePathOrDigest(baseModel) {
		baseModel = ""
	}
	s.mu.Lock()
	rec := s.models[name]
	rec.BaseModel = baseModel
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	s.models[name] = rec
	s.mu.Unlock()

	return s.save()
}

func (s *customModelsStore) Unregister(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	s.mu.Lock()
	existed := false
	if _, ok := s.models[name]; ok {
		delete(s.models, name)
		existed = true
	}
	if strings.HasSuffix(name, ":latest") {
		trimmed := strings.TrimSuffix(name, ":latest")
		if _, ok := s.models[trimmed]; ok {
			delete(s.models, trimmed)
			existed = true
		}
	} else {
		withLatest := name + ":latest"
		if _, ok := s.models[withLatest]; ok {
			delete(s.models, withLatest)
			existed = true
		}
	}
	s.mu.Unlock()

	if !existed {
		return nil
	}
	return s.save()
}

func (s *customModelsStore) All() map[string]CustomModelRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]CustomModelRecord, len(s.models))
	for k, v := range s.models {
		res[k] = v
	}
	return res
}

func (s *customModelsStore) save() error {
	if s.path == "" {
		return nil
	}
	s.mu.RLock()
	file := customModelsFile{
		Models: make(map[string]CustomModelRecord, len(s.models)),
	}
	for k, v := range s.models {
		file.Models[k] = v
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
