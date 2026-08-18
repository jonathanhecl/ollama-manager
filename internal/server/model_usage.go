package server

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// ModelUsageRecord stores persistent telemetry and usage stats for an Ollama model.
type ModelUsageRecord struct {
	LastUsedAt           *time.Time `json:"last_used_at,omitempty"`
	RecordTokensPerSec   float64    `json:"record_tokens_per_sec,omitempty"`
	RecordTokensPerSecAt *time.Time `json:"record_tokens_per_sec_at,omitempty"`
	TotalTokens          int64      `json:"total_tokens,omitempty"`
	TotalCalls           int64      `json:"total_calls,omitempty"`
}

type modelUsageFile struct {
	Models map[string]ModelUsageRecord `json:"models"`
}

type modelUsageStore struct {
	path   string
	mu     sync.RWMutex
	models map[string]ModelUsageRecord
}

func newModelUsageStore(path string) *modelUsageStore {
	return &modelUsageStore{
		path:   path,
		models: make(map[string]ModelUsageRecord),
	}
}

func (s *modelUsageStore) Load() error {
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
	var file modelUsageFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if file.Models != nil {
		s.models = file.Models
	} else {
		s.models = make(map[string]ModelUsageRecord)
	}
	return nil
}

func (s *modelUsageStore) Get(name string) (ModelUsageRecord, bool) {
	if name == "" {
		return ModelUsageRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.models[name]
	return rec, ok
}

func (s *modelUsageStore) Record(name string, evalCount int, evalDurationNs int64, promptEvalCount int, usedAt time.Time) error {
	if name == "" {
		return nil
	}
	if usedAt.IsZero() {
		usedAt = time.Now()
	}

	s.mu.Lock()
	rec := s.models[name]
	rec.LastUsedAt = &usedAt
	rec.TotalCalls++
	rec.TotalTokens += int64(evalCount + promptEvalCount)

	if evalCount > 0 && evalDurationNs > 0 {
		tps := float64(evalCount) / (float64(evalDurationNs) / 1e9)
		if tps > rec.RecordTokensPerSec {
			rec.RecordTokensPerSec = tps
			rec.RecordTokensPerSecAt = &usedAt
		}
	}
	s.models[name] = rec
	s.mu.Unlock()

	return s.save()
}

func (s *modelUsageStore) RecordTPS(name string, tps float64, usedAt time.Time) error {
	if name == "" {
		return nil
	}
	if usedAt.IsZero() {
		usedAt = time.Now()
	}

	s.mu.Lock()
	rec := s.models[name]
	rec.LastUsedAt = &usedAt
	rec.TotalCalls++
	if tps > rec.RecordTokensPerSec {
		rec.RecordTokensPerSec = tps
		rec.RecordTokensPerSecAt = &usedAt
	}
	s.models[name] = rec
	s.mu.Unlock()

	return s.save()
}

func (s *modelUsageStore) save() error {
	if s.path == "" {
		return nil
	}
	s.mu.RLock()
	file := modelUsageFile{
		Models: make(map[string]ModelUsageRecord, len(s.models)),
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
