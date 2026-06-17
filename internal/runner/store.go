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
// It also sets Passed based on the rating: good = true, bad/regular = false.
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
				passed := rating == "good"
				res.Passed = &passed
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

// GroupModelSummary aggregates all results for a single model within a group.
type GroupModelSummary struct {
	Model            string    `json:"model"`
	TotalTests       int       `json:"total_tests"`
	Passed           int       `json:"passed"`
	Failed           int       `json:"failed"`
	HumanReview      int       `json:"human_review"`
	Errors           int       `json:"errors"`
	PassedTests      []string  `json:"passed_tests,omitempty"`
	FailedTests      []string  `json:"failed_tests,omitempty"`
	HumanReviewTests []string  `json:"human_review_tests,omitempty"`
	ErrorTests       []string  `json:"error_tests,omitempty"`
	AvgResponseMs    int64     `json:"avg_response_ms"`
	AvgTokensPerSec  float64   `json:"avg_tokens_per_sec,omitempty"`
	LastRunAt        time.Time `json:"last_run_at"`
	SysInfo          SysInfo   `json:"sys_info,omitempty"`
}

// GetGroupHistory returns per-model summaries for all runs of a given group.
func (s *ResultStore) GetGroupHistory(groupID string) []GroupModelSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	type acc struct {
		count            int
		passed           int
		failed           int
		human            int
		errors           int
		passedTests      []string
		failedTests      []string
		humanReviewTests []string
		errorTests       []string
		respSum          int64
		tokCount         int
		tokSum           float64
		lastRun          time.Time
		sysInfo          SysInfo
	}
	m := make(map[string]*acc)
	for _, run := range s.runs {
		if run.GroupID != groupID {
			continue
		}
		for _, res := range run.Results {
			a, ok := m[res.Model]
			if !ok {
				a = &acc{}
				m[res.Model] = a
			}
			a.count++
			a.respSum += res.ResponseTimeMs
			if res.TokensPerSec > 0 {
				a.tokCount++
				a.tokSum += res.TokensPerSec
			}
			if res.Error != "" {
				a.errors++
				a.errorTests = append(a.errorTests, res.TestName)
			} else if res.Passed == nil {
				a.human++
				a.humanReviewTests = append(a.humanReviewTests, res.TestName)
			} else if *res.Passed {
				a.passed++
				a.passedTests = append(a.passedTests, res.TestName)
			} else {
				a.failed++
				a.failedTests = append(a.failedTests, res.TestName)
			}
			if run.Timestamp.After(a.lastRun) {
				a.lastRun = run.Timestamp
				a.sysInfo = run.SysInfo
			}
		}
	}
	out := make([]GroupModelSummary, 0, len(m))
	for model, a := range m {
		summary := GroupModelSummary{
			Model:            model,
			TotalTests:       a.count,
			Passed:           a.passed,
			Failed:           a.failed,
			HumanReview:      a.human,
			Errors:           a.errors,
			PassedTests:      a.passedTests,
			FailedTests:      a.failedTests,
			HumanReviewTests: a.humanReviewTests,
			ErrorTests:       a.errorTests,
			LastRunAt:        a.lastRun,
			SysInfo:          a.sysInfo,
		}
		if a.count > 0 {
			summary.AvgResponseMs = a.respSum / int64(a.count)
		}
		if a.tokCount > 0 {
			summary.AvgTokensPerSec = a.tokSum / float64(a.tokCount)
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Model < out[j].Model
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
