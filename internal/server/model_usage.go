package server

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

// ModelUsageRecord stores persistent telemetry and usage stats for an Ollama model.
type ModelUsageRecord struct {
	LastUsedAt           *time.Time `json:"last_used_at,omitempty"`
	RecordTokensPerSec   float64    `json:"record_tokens_per_sec,omitempty"`
	RecordTokensPerSecAt *time.Time `json:"record_tokens_per_sec_at,omitempty"`
	MinColdLoadMs        int64      `json:"min_cold_load_ms,omitempty"`
	MinColdLoadAt        *time.Time `json:"min_cold_load_at,omitempty"`
	TotalTokens          int64      `json:"total_tokens,omitempty"`
	TotalCalls           int64      `json:"total_calls,omitempty"`
	// Metadata captured the last time the model was seen installed or used.
	// Kept so deleted ("ghost") models retain enough context for analytics.
	ParameterSize string `json:"parameter_size,omitempty"`
	Size          int64  `json:"size,omitempty"`
	Quantization  string `json:"quantization,omitempty"`
	Family        string `json:"family,omitempty"`
	// Additional metadata sourced from the GGUF model_info block.
	ParameterCount int64  `json:"parameter_count,omitempty"`
	Architecture   string `json:"architecture,omitempty"`
	FileType       int64  `json:"file_type,omitempty"`
	SizeLabel      string `json:"size_label,omitempty"`
	IsMOE          bool   `json:"is_moe,omitempty"`
}

// modelUsageMeta carries the persistent metadata fields stored per model.
type modelUsageMeta struct {
	ParameterSize  string
	Size           int64
	Quantization   string
	Family         string
	ParameterCount int64
	Architecture   string
	FileType       int64
	SizeLabel      string
	IsMOE          bool
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

func (s *modelUsageStore) All() map[string]ModelUsageRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]ModelUsageRecord, len(s.models))
	for k, v := range s.models {
		res[k] = v
	}
	return res
}

func mergeBaseUsage(target, base ModelUsageRecord) ModelUsageRecord {
	out := base
	if target.RecordTokensPerSec > out.RecordTokensPerSec {
		out.RecordTokensPerSec = target.RecordTokensPerSec
		out.RecordTokensPerSecAt = target.RecordTokensPerSecAt
	}
	if target.MinColdLoadMs > 0 && (out.MinColdLoadMs == 0 || target.MinColdLoadMs < out.MinColdLoadMs) {
		out.MinColdLoadMs = target.MinColdLoadMs
		out.MinColdLoadAt = target.MinColdLoadAt
	}
	if target.LastUsedAt != nil {
		out.LastUsedAt = target.LastUsedAt
	}
	if target.TotalCalls > out.TotalCalls {
		out.TotalCalls = target.TotalCalls
		out.TotalTokens = target.TotalTokens
	}
	if out.ParameterSize == "" && target.ParameterSize != "" {
		out.ParameterSize = target.ParameterSize
	}
	if out.Size == 0 && target.Size != 0 {
		out.Size = target.Size
	}
	if out.Quantization == "" && target.Quantization != "" {
		out.Quantization = target.Quantization
	}
	if out.Family == "" && target.Family != "" {
		out.Family = target.Family
	}
	if out.ParameterCount == 0 && target.ParameterCount != 0 {
		out.ParameterCount = target.ParameterCount
	}
	if out.Architecture == "" && target.Architecture != "" {
		out.Architecture = target.Architecture
	}
	if out.FileType == 0 && target.FileType != 0 {
		out.FileType = target.FileType
	}
	if out.SizeLabel == "" && target.SizeLabel != "" {
		out.SizeLabel = target.SizeLabel
	}
	if target.IsMOE {
		out.IsMOE = true
	}
	return out
}

func (s *modelUsageStore) Get(name string) (ModelUsageRecord, bool) {
	if name == "" {
		return ModelUsageRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.models[name]
	if ok && (rec.TotalCalls > 0 || rec.RecordTokensPerSec > 0 || rec.LastUsedAt != nil || rec.MinColdLoadMs > 0) {
		return rec, true
	}
	// Fallback for :fixed models inheriting from their base model
	if isFixedModelName(name) {
		base := fixedBaseName(name)
		if baseRec, baseOk := s.models[base]; baseOk && (baseRec.TotalCalls > 0 || baseRec.RecordTokensPerSec > 0 || baseRec.LastUsedAt != nil || baseRec.MinColdLoadMs > 0) {
			return mergeBaseUsage(rec, baseRec), true
		}
		if baseRec, baseOk := s.models[base+":latest"]; baseOk && (baseRec.TotalCalls > 0 || baseRec.RecordTokensPerSec > 0 || baseRec.LastUsedAt != nil || baseRec.MinColdLoadMs > 0) {
			return mergeBaseUsage(rec, baseRec), true
		}
	}
	return rec, ok
}

func (s *modelUsageStore) InheritUsage(srcName, targetName string) error {
	if srcName == "" || targetName == "" || srcName == targetName {
		return nil
	}
	s.mu.Lock()
	src, ok := s.models[srcName]
	if !ok {
		if strings.HasSuffix(srcName, ":latest") {
			src, ok = s.models[strings.TrimSuffix(srcName, ":latest")]
		} else {
			src, ok = s.models[srcName+":latest"]
		}
	}
	if !ok {
		s.mu.Unlock()
		return nil
	}
	target := s.models[targetName]
	s.models[targetName] = mergeBaseUsage(target, src)
	s.mu.Unlock()

	return s.save()
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

func (s *modelUsageStore) RecordColdLoad(name string, durationMs int64, at time.Time) error {
	if name == "" || durationMs <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}

	s.mu.Lock()
	rec := s.models[name]
	if rec.MinColdLoadMs == 0 || durationMs < rec.MinColdLoadMs {
		rec.MinColdLoadMs = durationMs
		rec.MinColdLoadAt = &at
	}
	s.models[name] = rec
	s.mu.Unlock()

	return s.save()
}

// SetMeta records model metadata (parameter size, disk size, quantization,
// family, exact parameter count, architecture, GGUF file type and size label)
// keyed by model name. It only persists when a value actually changes, so it is
// safe to call on every model listing. Empty/zero values never overwrite a
// previously known value.
func (s *modelUsageStore) SetMeta(name string, meta modelUsageMeta) error {
	if name == "" {
		return nil
	}
	s.mu.Lock()
	rec := s.models[name]
	changed := false
	if meta.ParameterSize != "" && rec.ParameterSize != meta.ParameterSize {
		rec.ParameterSize = meta.ParameterSize
		changed = true
	}
	if meta.Size > 0 && rec.Size != meta.Size {
		rec.Size = meta.Size
		changed = true
	}
	if meta.Quantization != "" && rec.Quantization != meta.Quantization {
		rec.Quantization = meta.Quantization
		changed = true
	}
	if meta.Family != "" && rec.Family != meta.Family {
		rec.Family = meta.Family
		changed = true
	}
	if meta.ParameterCount > 0 && rec.ParameterCount != meta.ParameterCount {
		rec.ParameterCount = meta.ParameterCount
		changed = true
	}
	if meta.Architecture != "" && rec.Architecture != meta.Architecture {
		rec.Architecture = meta.Architecture
		changed = true
	}
	if meta.FileType > 0 && rec.FileType != meta.FileType {
		rec.FileType = meta.FileType
		changed = true
	}
	if meta.SizeLabel != "" && rec.SizeLabel != meta.SizeLabel {
		rec.SizeLabel = meta.SizeLabel
		changed = true
	}
	if meta.IsMOE && !rec.IsMOE {
		rec.IsMOE = true
		changed = true
	}
	s.models[name] = rec
	s.mu.Unlock()

	if !changed {
		return nil
	}
	return s.save()
}

// Delete removes the persistent usage/metadata record for a model entirely.
// It does not touch the uninstall-history store, so the deletion reason remains
// available. Returns true if a record existed and was removed.
func (s *modelUsageStore) Delete(name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	s.mu.Lock()
	_, existed := s.models[name]
	if existed {
		delete(s.models, name)
	}
	s.mu.Unlock()
	if !existed {
		return false, nil
	}
	return true, s.save()
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
