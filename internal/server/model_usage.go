package server

import (
	"bytes"
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

type modelUsageStore struct {
	path         string
	mu           sync.RWMutex
	currentID    string
	currentSpecs DeviceSpecs
	entries      []*DeviceUsageEntry
}

func newModelUsageStore(path string) *modelUsageStore {
	specs, id := DetectCurrentDeviceSpecs()
	entry := &DeviceUsageEntry{
		ID:     id,
		Specs:  specs,
		Models: make(map[string]ModelUsageRecord),
	}
	return &modelUsageStore{
		path:         path,
		currentID:    id,
		currentSpecs: specs,
		entries:      []*DeviceUsageEntry{entry},
	}
}

// SetCurrentSpecs allows overriding the active device specifications and ID (useful in tests).
func (s *modelUsageStore) SetCurrentSpecs(specs DeviceSpecs, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentSpecs = specs
	s.currentID = id
	cur := s.findOrCreateCurrentLocked()
	cur.ID = id
	cur.Specs = specs
}

func (s *modelUsageStore) CurrentDeviceID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentID
}

func (s *modelUsageStore) CurrentSpecs() DeviceSpecs {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentSpecs
}

func (s *modelUsageStore) findOrCreateCurrentLocked() *DeviceUsageEntry {
	for _, e := range s.entries {
		if e.ID == s.currentID {
			return e
		}
	}
	// Fallback match by hardware
	for _, e := range s.entries {
		if e.Specs.CPU == s.currentSpecs.CPU && e.Specs.RAM == s.currentSpecs.RAM && e.Specs.GPU == s.currentSpecs.GPU {
			e.ID = s.currentID
			e.Specs.OS = s.currentSpecs.OS
			return e
		}
	}
	newEntry := &DeviceUsageEntry{
		ID:     s.currentID,
		Specs:  s.currentSpecs,
		Models: make(map[string]ModelUsageRecord),
	}
	s.entries = append(s.entries, newEntry)
	return newEntry
}

func (s *modelUsageStore) currentModelsLocked() map[string]ModelUsageRecord {
	cur := s.findOrCreateCurrentLocked()
	if cur.Models == nil {
		cur.Models = make(map[string]ModelUsageRecord)
	}
	return cur.Models
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
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var loadedEntries []*DeviceUsageEntry
	migratedFromLegacy := false

	if trimmed[0] == '[' {
		// New array format: [ {"specs": ..., "models": ...}, ... ]
		if err := json.Unmarshal(trimmed, &loadedEntries); err != nil {
			return err
		}
	} else if trimmed[0] == '{' {
		// Legacy format: {"models": {...}} or wrapper {"devices": [...]}
		var wrapper struct {
			Devices []*DeviceUsageEntry         `json:"devices"`
			Models  map[string]ModelUsageRecord `json:"models"`
		}
		if err := json.Unmarshal(trimmed, &wrapper); err != nil {
			return err
		}
		if len(wrapper.Devices) > 0 {
			loadedEntries = wrapper.Devices
		} else if wrapper.Models != nil {
			// Legacy migration: preserve all existing model telemetry under the current machine's specs.
			migratedFromLegacy = true
			legacyEntry := &DeviceUsageEntry{
				ID:     s.currentID,
				Specs:  s.currentSpecs,
				Models: wrapper.Models,
			}
			loadedEntries = []*DeviceUsageEntry{legacyEntry}
		}
	}

	if len(loadedEntries) > 0 {
		s.entries = loadedEntries
	}

	// Ensure current device exists and reflects any minor OS updates
	cur := s.findOrCreateCurrentLocked()
	if cur.Specs.OS == "" || cur.Specs.OS != s.currentSpecs.OS {
		cur.Specs.OS = s.currentSpecs.OS
	}
	if cur.Specs.CPU == "" {
		cur.Specs.CPU = s.currentSpecs.CPU
	}
	if cur.Specs.RAM == "" {
		cur.Specs.RAM = s.currentSpecs.RAM
	}
	if cur.Models == nil {
		cur.Models = make(map[string]ModelUsageRecord)
	}

	if migratedFromLegacy {
		return s.saveLocked()
	}
	return nil
}

func (s *modelUsageStore) All() map[string]ModelUsageRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	models := s.currentModelsLocked()
	res := make(map[string]ModelUsageRecord, len(models))
	for k, v := range models {
		res[k] = v
	}
	return res
}

func (s *modelUsageStore) Devices() []DeviceUsageSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DeviceUsageSummary, 0, len(s.entries))
	for _, e := range s.entries {
		isCur := e.ID == s.currentID
		out = append(out, DeviceUsageSummary{
			ID:          e.ID,
			Name:        DeviceDisplayName(e.Specs, isCur),
			Specs:       e.Specs,
			IsCurrent:   isCur,
			ModelsCount: len(e.Models),
		})
	}
	return out
}

func (s *modelUsageStore) GetDeviceModels(deviceID string) (map[string]ModelUsageRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if deviceID == "" || deviceID == "current" || deviceID == s.currentID {
		models := s.currentModelsLocked()
		res := make(map[string]ModelUsageRecord, len(models))
		for k, v := range models {
			res[k] = v
		}
		return res, true
	}
	if deviceID == "all" {
		merged := make(map[string]ModelUsageRecord)
		for _, e := range s.entries {
			for k, v := range e.Models {
				if existing, ok := merged[k]; ok {
					merged[k] = mergeBaseUsage(existing, v)
				} else {
					merged[k] = v
				}
			}
		}
		return merged, true
	}
	for _, e := range s.entries {
		if e.ID == deviceID {
			res := make(map[string]ModelUsageRecord, len(e.Models))
			for k, v := range e.Models {
				res[k] = v
			}
			return res, true
		}
	}
	return nil, false
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
	models := s.currentModelsLocked()
	rec, ok := models[name]
	if ok && (rec.TotalCalls > 0 || rec.RecordTokensPerSec > 0 || rec.LastUsedAt != nil || rec.MinColdLoadMs > 0) {
		return rec, true
	}
	// Fallback for :latest aliases
	if strings.HasSuffix(name, ":latest") {
		trimmed := strings.TrimSuffix(name, ":latest")
		if baseRec, baseOk := models[trimmed]; baseOk && (baseRec.TotalCalls > 0 || baseRec.RecordTokensPerSec > 0 || baseRec.LastUsedAt != nil || baseRec.MinColdLoadMs > 0) {
			return mergeBaseUsage(rec, baseRec), true
		}
	} else if !strings.Contains(name, ":") {
		if baseRec, baseOk := models[name+":latest"]; baseOk && (baseRec.TotalCalls > 0 || baseRec.RecordTokensPerSec > 0 || baseRec.LastUsedAt != nil || baseRec.MinColdLoadMs > 0) {
			return mergeBaseUsage(rec, baseRec), true
		}
	}
	// Fallback for :fixed models inheriting from their base model (exact name or :latest alias)
	if isFixedModelName(name) {
		base := fixedBaseName(name)
		if baseRec, baseOk := models[base]; baseOk && (baseRec.TotalCalls > 0 || baseRec.RecordTokensPerSec > 0 || baseRec.LastUsedAt != nil || baseRec.MinColdLoadMs > 0) {
			return mergeBaseUsage(rec, baseRec), true
		}
		if baseRec, baseOk := models[base+":latest"]; baseOk && (baseRec.TotalCalls > 0 || baseRec.RecordTokensPerSec > 0 || baseRec.LastUsedAt != nil || baseRec.MinColdLoadMs > 0) {
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
	models := s.currentModelsLocked()
	src, okSrc := models[srcName]
	if !okSrc {
		if strings.HasSuffix(srcName, ":latest") {
			src, okSrc = models[strings.TrimSuffix(srcName, ":latest")]
		} else {
			src, okSrc = models[srcName+":latest"]
		}
	}
	target, okTarget := models[targetName]
	if !okTarget {
		if strings.HasSuffix(targetName, ":latest") {
			target, okTarget = models[strings.TrimSuffix(targetName, ":latest")]
		} else {
			target, okTarget = models[targetName+":latest"]
		}
	}
	if !okSrc && !okTarget {
		s.mu.Unlock()
		return nil
	}
	models[targetName] = mergeBaseUsage(target, src)
	models[srcName] = mergeBaseUsage(src, target)
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
	models := s.currentModelsLocked()
	rec := models[name]
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
	models[name] = rec
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
	models := s.currentModelsLocked()
	rec := models[name]
	rec.LastUsedAt = &usedAt
	rec.TotalCalls++
	if tps > rec.RecordTokensPerSec {
		rec.RecordTokensPerSec = tps
		rec.RecordTokensPerSecAt = &usedAt
	}
	models[name] = rec
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
	models := s.currentModelsLocked()
	rec := models[name]
	if rec.MinColdLoadMs == 0 || durationMs < rec.MinColdLoadMs {
		rec.MinColdLoadMs = durationMs
		rec.MinColdLoadAt = &at
	}
	models[name] = rec
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
	models := s.currentModelsLocked()
	rec := models[name]
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
	models[name] = rec

	// Also propagate static model metadata across other device entries if model exists there
	if changed {
		for _, entry := range s.entries {
			if entry.ID == s.currentID || entry.Models == nil {
				continue
			}
			if otherRec, ok := entry.Models[name]; ok {
				if meta.ParameterSize != "" {
					otherRec.ParameterSize = meta.ParameterSize
				}
				if meta.Size > 0 {
					otherRec.Size = meta.Size
				}
				if meta.Quantization != "" {
					otherRec.Quantization = meta.Quantization
				}
				if meta.Family != "" {
					otherRec.Family = meta.Family
				}
				if meta.ParameterCount > 0 {
					otherRec.ParameterCount = meta.ParameterCount
				}
				if meta.Architecture != "" {
					otherRec.Architecture = meta.Architecture
				}
				if meta.FileType > 0 {
					otherRec.FileType = meta.FileType
				}
				if meta.SizeLabel != "" {
					otherRec.SizeLabel = meta.SizeLabel
				}
				if meta.ContextLength > 0 {
					otherRec.ContextLength = meta.ContextLength
				}
				if meta.IsMOE {
					otherRec.IsMOE = true
				}
				entry.Models[name] = otherRec
			}
		}
	}
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
	models := s.currentModelsLocked()
	_, existed := models[name]
	if existed {
		delete(models, name)
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
	models := s.currentModelsLocked()
	resetRecord := func(k string) bool {
		rec, ok := models[k]
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
		models[k] = rec
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveLocked()
}

func (s *modelUsageStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.entries, "", "  ")
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
