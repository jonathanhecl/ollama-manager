// Package runner implements the test battery execution engine.
package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// persistFile is the on-disk format for tests-history.json.
type persistFile struct {
	Runs []BatteryRun `json:"runs"`
}

// ResultStore persists battery runs to disk atomically.
type ResultStore struct {
	mu   sync.Mutex
	path string
	runs []BatteryRun
}

// NewResultStore creates a store backed by path (e.g. /data/test_results.json).
func NewResultStore(path string) *ResultStore {
	return &ResultStore{path: path}
}

// Load reads existing runs from disk.
func (s *ResultStore) Load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", s.path, err)
	}
	var pf persistFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}
	s.runs = pf.Runs
	return nil
}

// SaveRun appends a run and persists atomically.
func (s *ResultStore) SaveRun(run *BatteryRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, *run)
	return s.saveLocked()
}

// GetRuns returns all runs sorted newest first.
func (s *ResultStore) GetRuns() []BatteryRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BatteryRun, len(s.runs))
	copy(out, s.runs)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	return out
}

// GetRun returns a single run by ID.
func (s *ResultStore) GetRun(id string) (BatteryRun, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if r.ID == id {
			return r, true
		}
	}
	return BatteryRun{}, false
}

// UpdateHumanRating updates the human rating for a specific test result within a run.
func (s *ResultStore) UpdateHumanRating(runID, testID, model, rating string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.runs {
		if s.runs[i].ID != runID {
			continue
		}
		for j := range s.runs[i].Results {
			res := &s.runs[i].Results[j]
			if res.TestID == testID && res.Model == model {
				res.HumanRating = rating
				return s.saveLocked()
			}
		}
	}
	return errors.New("result not found")
}

// TestHistoryItem is a single result for a test across all runs.
type TestHistoryItem struct {
	RunID          string    `json:"run_id"`
	Timestamp      time.Time `json:"timestamp"`
	GroupName      string    `json:"group_name"`
	Model          string    `json:"model"`
	Passed         *bool     `json:"passed,omitempty"`
	ResponseTimeMs int64     `json:"response_time_ms"`
	TokensPerSec   float64   `json:"tokens_per_sec,omitempty"`
	ReasoningUsed  bool      `json:"reasoning_used"`
	HumanRating    string    `json:"human_rating,omitempty"`
	ModelResponse  string    `json:"model_response,omitempty"`
	Error          string    `json:"error,omitempty"`
	SysInfo        SysInfo   `json:"sys_info,omitempty"`
}

// GetTestHistory returns all historical results for a specific test, newest first.
func (s *ResultStore) GetTestHistory(testID string) []TestHistoryItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []TestHistoryItem
	for _, run := range s.runs {
		for _, res := range run.Results {
			if res.TestID == testID {
				out = append(out, TestHistoryItem{
					RunID:          run.ID,
					Timestamp:      run.Timestamp,
					GroupName:      run.GroupName,
					Model:          res.Model,
					Passed:         res.Passed,
					ResponseTimeMs: res.ResponseTimeMs,
					TokensPerSec:   res.TokensPerSec,
					ReasoningUsed:  res.ReasoningUsed,
					HumanRating:    res.HumanRating,
					ModelResponse:  res.ModelResponse,
					Error:          res.Error,
					SysInfo:        run.SysInfo,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	return out
}

// DeleteRun removes a run by ID.
func (s *ResultStore) DeleteRun(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.runs {
		if r.ID == id {
			s.runs = append(s.runs[:i], s.runs[i+1:]...)
			return s.saveLocked()
		}
	}
	return errors.New("run not found")
}

func (s *ResultStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(persistFile{Runs: s.runs}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// DataDir returns the directory portion of the store path.
func (s *ResultStore) DataDir() string {
	return filepath.Dir(s.path)
}
