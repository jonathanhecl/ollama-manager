// Package runner implements the test battery execution engine and results persistence.
package runner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// persistFile is the on-disk format for _history.json.
type persistFile struct {
	Runs []BatteryRun `json:"runs"`
}

// ResultStore persists battery runs to disk within each category folder alongside the test files.
type ResultStore struct {
	mu   sync.Mutex
	dir  string // root testing directory (e.g. /path/to/testing)
	runs []BatteryRun
}

// NewResultStore creates a store backed by the testing directory.
func NewResultStore(pathOrDir string) *ResultStore {
	dir := pathOrDir
	if strings.HasSuffix(strings.ToLower(dir), ".json") || strings.HasSuffix(strings.ToLower(dir), ".yaml") || strings.HasSuffix(strings.ToLower(dir), ".yml") {
		dir = filepath.Dir(dir)
	}
	return &ResultStore{dir: dir}
}

// Load reads existing runs from all category folders in the testing directory.
func (s *ResultStore) Load() error {
	if s.dir == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.runs = make([]BatteryRun, 0)
	runMap := make(map[string]BatteryRun)

	// Check legacy single-file history for migration
	legacyPaths := []string{
		filepath.Join(s.dir, "_history_all.json"),
		filepath.Join(s.dir, ".history.json"),
		filepath.Join(s.dir, "tests-history.json"),
		filepath.Join(filepath.Dir(s.dir), "tests-history.json"),
	}
	for _, lp := range legacyPaths {
		if data, err := os.ReadFile(lp); err == nil {
			var pf persistFile
			if err := json.Unmarshal(data, &pf); err == nil {
				for _, r := range pf.Runs {
					if r.ID != "" {
						runMap[r.ID] = r
					}
				}
			}
		}
	}

	// Scan category subdirectories in testing dir
	entries, err := os.ReadDir(s.dir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			catDir := filepath.Join(s.dir, entry.Name())
			histPaths := []string{
				filepath.Join(catDir, "_history.json"),
				filepath.Join(catDir, ".history.json"),
				filepath.Join(catDir, "history.json"),
			}
			for _, hp := range histPaths {
				data, err := os.ReadFile(hp)
				if err != nil {
					continue
				}
				var pf persistFile
				if err := json.Unmarshal(data, &pf); err == nil {
					for _, r := range pf.Runs {
						if r.ID != "" {
							runMap[r.ID] = r
						}
					}
				}
			}
		}
	}

	for _, r := range runMap {
		s.runs = append(s.runs, r)
	}

	// Persist per-group history files if migrated from legacy
	groups := make(map[string]struct{})
	for _, r := range s.runs {
		groups[r.GroupID] = struct{}{}
	}
	for gid := range groups {
		_ = s.saveGroupLocked(gid)
	}

	return nil
}

// SaveRun appends a run and persists to its category's _history.json.
func (s *ResultStore) SaveRun(run *BatteryRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, *run)
	return s.saveGroupLocked(run.GroupID)
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
				passed := rating == "good"
				res.Passed = &passed
				return s.saveGroupLocked(s.runs[i].GroupID)
			}
		}
	}
	return errors.New("result not found")
}

// UpdateResultPassed updates only the Passed flag for a specific test result.
func (s *ResultStore) UpdateResultPassed(runID, testID, model string, passed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.runs {
		if s.runs[i].ID != runID {
			continue
		}
		for j := range s.runs[i].Results {
			res := &s.runs[i].Results[j]
			if res.TestID == testID && res.Model == model {
				res.Passed = &passed
				return s.saveGroupLocked(s.runs[i].GroupID)
			}
		}
	}
	return errors.New("result not found")
}

// TestHistoryItem is a single result for a test across all runs.
type TestHistoryItem struct {
	RunID          string      `json:"run_id"`
	Timestamp      time.Time   `json:"timestamp"`
	GroupName      string      `json:"group_name"`
	Model          string      `json:"model"`
	Passed         *bool       `json:"passed,omitempty"`
	ResponseTimeMs int64       `json:"response_time_ms"`
	TokensPerSec   float64     `json:"tokens_per_sec,omitempty"`
	PromptTokens   int         `json:"prompt_tokens,omitempty"`
	EvalTokens     int         `json:"eval_tokens,omitempty"`
	TotalTokens    int         `json:"total_tokens,omitempty"`
	ReasoningUsed  bool        `json:"reasoning_used"`
	HumanRating    string      `json:"human_rating,omitempty"`
	ModelResponse  string      `json:"model_response,omitempty"`
	Error          string      `json:"error,omitempty"`
	SubResults     []SubResult `json:"sub_results,omitempty"`
	SysInfo        SysInfo     `json:"sys_info,omitempty"`
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
					PromptTokens:   res.PromptTokens,
					EvalTokens:     res.EvalTokens,
					TotalTokens:    res.TotalTokens,
					ReasoningUsed:  res.ReasoningUsed,
					HumanRating:    res.HumanRating,
					ModelResponse:  res.ModelResponse,
					Error:          res.Error,
					SubResults:     res.SubResults,
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

// DeleteTestHistory removes all battery results for a test across every run.
func (s *ResultStore) DeleteTestHistory(testID string) error {
	if testID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	affectedGroups := make(map[string]struct{})
	kept := make([]BatteryRun, 0, len(s.runs))
	for _, run := range s.runs {
		filtered := run.Results[:0]
		for _, res := range run.Results {
			if res.TestID == testID {
				affectedGroups[run.GroupID] = struct{}{}
				continue
			}
			filtered = append(filtered, res)
		}
		if len(filtered) == 0 {
			affectedGroups[run.GroupID] = struct{}{}
			continue
		}
		if len(filtered) != len(run.Results) {
			run.Results = filtered
		}
		kept = append(kept, run)
	}

	if len(affectedGroups) == 0 {
		return nil
	}
	s.runs = kept
	for gid := range affectedGroups {
		_ = s.saveGroupLocked(gid)
	}
	return nil
}

// DeleteRun removes a run by ID.
func (s *ResultStore) DeleteRun(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.runs {
		if r.ID == id {
			gid := r.GroupID
			s.runs = append(s.runs[:i], s.runs[i+1:]...)
			return s.saveGroupLocked(gid)
		}
	}
	return errors.New("run not found")
}

func (s *ResultStore) saveGroupLocked(groupID string) error {
	if s.dir == "" {
		return nil
	}
	if groupID == "" {
		groupID = "examples"
	}

	var target string
	if groupID == "all" {
		target = filepath.Join(s.dir, "_history_all.json")
	} else {
		catDir := filepath.Join(s.dir, groupID)
		_ = os.MkdirAll(catDir, 0o755)
		target = filepath.Join(catDir, "_history.json")
	}

	var groupRuns []BatteryRun
	for _, r := range s.runs {
		if r.GroupID == groupID {
			groupRuns = append(groupRuns, r)
		}
	}

	if len(groupRuns) == 0 {
		_ = os.Remove(target)
		return nil
	}

	data, err := json.MarshalIndent(persistFile{Runs: groupRuns}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// DataDir returns the root testing directory path.
func (s *ResultStore) DataDir() string {
	return s.dir
}
