package server

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

// minRecordTPS is the floor applied to a model's record tokens-per-second. A
// model that completes (or is cancelled) below this rate is indexed at 0.1
// tok/s so it is recorded as functional but extremely slow.
const minRecordTPS = 0.1

// cancelRecordThreshold is the rate above which a cancelled streaming response
// is not recorded at all: at that speed the model is usable enough that the
// response must complete normally to count.
const cancelRecordThreshold = 1.0

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
	ContextLength  int64  `json:"context_length,omitempty"`
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
	ContextLength  int64
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
		if out.LastUsedAt == nil || target.LastUsedAt.After(*out.LastUsedAt) {
			out.LastUsedAt = target.LastUsedAt
		}
	}
	if target.TotalCalls > out.TotalCalls {
		out.TotalCalls = target.TotalCalls
		out.TotalTokens = target.TotalTokens
	}
	if target.ParameterSize != "" {
		out.ParameterSize = target.ParameterSize
	}
	if target.Size != 0 {
		out.Size = target.Size
	}
	if target.Quantization != "" {
		out.Quantization = target.Quantization
	}
	if target.Family != "" {
		out.Family = target.Family
	}
	if target.ParameterCount != 0 {
		out.ParameterCount = target.ParameterCount
	}
	if target.Architecture != "" {
		out.Architecture = target.Architecture
	}
	if target.FileType != 0 {
		out.FileType = target.FileType
	}
	if target.SizeLabel != "" {
		out.SizeLabel = target.SizeLabel
	}
	if target.ContextLength != 0 {
		out.ContextLength = target.ContextLength
	}
	if target.IsMOE || base.IsMOE {
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
	// Fallback for :latest aliases
	if strings.HasSuffix(name, ":latest") {
		trimmed := strings.TrimSuffix(name, ":latest")
		if baseRec, baseOk := s.models[trimmed]; baseOk && (baseRec.TotalCalls > 0 || baseRec.RecordTokensPerSec > 0 || baseRec.LastUsedAt != nil || baseRec.MinColdLoadMs > 0) {
			return mergeBaseUsage(rec, baseRec), true
		}
	} else if !strings.Contains(name, ":") {
		if baseRec, baseOk := s.models[name+":latest"]; baseOk && (baseRec.TotalCalls > 0 || baseRec.RecordTokensPerSec > 0 || baseRec.LastUsedAt != nil || baseRec.MinColdLoadMs > 0) {
			return mergeBaseUsage(rec, baseRec), true
		}
	}
	// Fallback for :fixed models inheriting from their base model (exact name or :latest alias)
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
	src, okSrc := s.models[srcName]
	if !okSrc {
		if strings.HasSuffix(srcName, ":latest") {
			src, okSrc = s.models[strings.TrimSuffix(srcName, ":latest")]
		} else {
			src, okSrc = s.models[srcName+":latest"]
		}
	}
	target, okTarget := s.models[targetName]
	if !okTarget {
		if strings.HasSuffix(targetName, ":latest") {
			target, okTarget = s.models[strings.TrimSuffix(targetName, ":latest")]
		} else {
			target, okTarget = s.models[targetName+":latest"]
		}
	}
	if !okSrc && !okTarget {
		s.mu.Unlock()
		return nil
	}
	s.models[targetName] = mergeBaseUsage(target, src)
	s.models[srcName] = mergeBaseUsage(src, target)
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
		if tps < minRecordTPS {
			tps = minRecordTPS
		}
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
	if meta.ContextLength > 0 && rec.ContextLength != meta.ContextLength {
		rec.ContextLength = meta.ContextLength
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

// ResetAnalytics clears runtime performance metrics and token counts (tokens/sec,
// cold load times, call counts, token counters, last used timestamps) for a model,
// preserving static metadata (parameter count, architecture, quantization, etc.).
func (s *modelUsageStore) ResetAnalytics(name string) error {
	if name == "" {
		return nil
	}
	s.mu.Lock()
	resetRecord := func(k string) bool {
		rec, ok := s.models[k]
		if !ok {
			return false
		}
		rec.RecordTokensPerSec = 0
		rec.RecordTokensPerSecAt = nil
		rec.MinColdLoadMs = 0
		rec.MinColdLoadAt = nil
		rec.TotalTokens = 0
		rec.TotalCalls = 0
		rec.LastUsedAt = nil
		s.models[k] = rec
		return true
	}

	found := resetRecord(name)
	if strings.HasSuffix(name, ":latest") {
		if resetRecord(strings.TrimSuffix(name, ":latest")) {
			found = true
		}
	} else if !strings.Contains(name, ":") {
		if resetRecord(name + ":latest") {
			found = true
		}
	}
	s.mu.Unlock()

	if !found {
		return nil
	}
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
