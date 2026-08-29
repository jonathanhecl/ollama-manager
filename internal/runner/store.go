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

	"github.com/gense/ollama-manager/internal/tests"
	"gopkg.in/yaml.v3"
)

// persistFile is the on-disk format for <exercise>._history.json.
type persistFile struct {
	Runs []BatteryRun `json:"runs"`
}

type exerciseLocation struct {
	GroupID string
	Base    string
}

// ResultStore persists battery runs to disk within each category folder alongside each exercise test file (<exercise>._history.json).
type ResultStore struct {
	mu          sync.Mutex
	dir         string // root testing directory (e.g. /path/to/testing)
	runs        []BatteryRun
	testsStore  *tests.Store
	exerciseMap map[string]exerciseLocation // testID or base -> exerciseLocation
}

// NewResultStore creates a store backed by the testing directory.
func NewResultStore(pathOrDir string) *ResultStore {
	dir := pathOrDir
	if strings.HasSuffix(strings.ToLower(dir), ".json") || strings.HasSuffix(strings.ToLower(dir), ".yaml") || strings.HasSuffix(strings.ToLower(dir), ".yml") {
		dir = filepath.Dir(dir)
	}
	return &ResultStore{
		dir:         dir,
		exerciseMap: make(map[string]exerciseLocation),
	}
}

// SetTestsStore associates a tests.Store with the ResultStore for accurate test-to-file mapping.
func (s *ResultStore) SetTestsStore(ts *tests.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.testsStore = ts
}

func (s *ResultStore) scanTestFilesLocked() {
	if s.dir == "" {
		return
	}
	if s.exerciseMap == nil {
		s.exerciseMap = make(map[string]exerciseLocation)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		catName := entry.Name()
		if strings.HasPrefix(catName, ".") || strings.HasPrefix(catName, "_") {
			continue
		}
		catDir := filepath.Join(s.dir, catName)
		catEntries, err := os.ReadDir(catDir)
		if err != nil {
			continue
		}
		for _, ce := range catEntries {
			if ce.IsDir() {
				continue
			}
			name := ce.Name()
			lower := strings.ToLower(name)
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
				strings.HasSuffix(lower, "._history.json") ||
				strings.HasSuffix(lower, ".history.json") ||
				strings.HasSuffix(lower, "_history.json") {
				continue
			}
			isYaml := strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
			isJson := strings.HasSuffix(lower, ".json")
			if !isYaml && !isJson {
				continue
			}
			base := strings.TrimSuffix(name, filepath.Ext(name))
			if base == "" {
				continue
			}
			s.exerciseMap[base] = exerciseLocation{GroupID: catName, Base: base}

			// Read ID from file if present
			data, err := os.ReadFile(filepath.Join(catDir, name))
			if err == nil {
				var meta struct {
					ID string `json:"id" yaml:"id"`
				}
				if isYaml {
					_ = yaml.Unmarshal(data, &meta)
				} else {
					_ = json.Unmarshal(data, &meta)
				}
				if meta.ID != "" {
					s.exerciseMap[meta.ID] = exerciseLocation{GroupID: catName, Base: base}
				}
			}
		}
	}
}

func (s *ResultStore) resolveExerciseLocked(testID, fallbackGroup string) (string, string) {
	if s.testsStore != nil {
		if t, ok := s.testsStore.GetTest(testID); ok {
			gid := t.GroupID
			if gid == "" {
				gid = fallbackGroup
			}
			base := strings.TrimSuffix(t.Filename, filepath.Ext(t.Filename))
			if base != "" {
				if s.exerciseMap == nil {
					s.exerciseMap = make(map[string]exerciseLocation)
				}
				s.exerciseMap[testID] = exerciseLocation{GroupID: gid, Base: base}
				return gid, base
			}
		}
	}
	if s.exerciseMap != nil {
		if ex, ok := s.exerciseMap[testID]; ok && ex.Base != "" {
			gid := ex.GroupID
			if gid == "" {
				gid = fallbackGroup
			}
			return gid, ex.Base
		}
	}
	if s.dir != "" {
		s.scanTestFilesLocked()
		if ex, ok := s.exerciseMap[testID]; ok && ex.Base != "" {
			gid := ex.GroupID
			if gid == "" {
				gid = fallbackGroup
			}
			return gid, ex.Base
		}
	}
	gid := fallbackGroup
	if gid == "" {
		gid = "examples"
	}
	base := testID
	if base == "" {
		base = "exercise"
	}
	return gid, base
}

func mergeRunIntoMap(runMap map[string]BatteryRun, incoming BatteryRun) {
	existing, ok := runMap[incoming.ID]
	if !ok {
		runMap[incoming.ID] = incoming
		return
	}
	if incoming.Timestamp.After(existing.Timestamp) {
		existing.Timestamp = incoming.Timestamp
	}
	if incoming.GroupName != "" && existing.GroupName == "" {
		existing.GroupName = incoming.GroupName
	}
	if incoming.GroupID != "" && existing.GroupID == "" {
		existing.GroupID = incoming.GroupID
	}
	if len(existing.Models) == 0 && len(incoming.Models) > 0 {
		existing.Models = incoming.Models
	}
	if existing.SysInfo.OS == "" && incoming.SysInfo.OS != "" {
		existing.SysInfo = incoming.SysInfo
	}

	// Merge results without duplicate testID + model entries
	resMap := make(map[string]int)
	for idx, res := range existing.Results {
		key := res.TestID + "::" + res.Model
		resMap[key] = idx
	}
	for _, incRes := range incoming.Results {
		key := incRes.TestID + "::" + incRes.Model
		if idx, found := resMap[key]; found {
			if incRes.HumanRating != "" {
				existing.Results[idx].HumanRating = incRes.HumanRating
			}
			if incRes.Passed != nil {
				existing.Results[idx].Passed = incRes.Passed
			}
		} else {
			existing.Results = append(existing.Results, incRes)
			resMap[key] = len(existing.Results) - 1
		}
	}
	runMap[incoming.ID] = existing
}

// Load reads existing runs from all exercise history files (*._history.json) in category folders.
// Any legacy _history.json files are migrated to per-exercise history files.
func (s *ResultStore) Load() error {
	if s.dir == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.runs = make([]BatteryRun, 0)
	runMap := make(map[string]BatteryRun)

	// Pre-scan test files to know testID -> exercise mapping
	s.scanTestFilesLocked()

	// Check legacy single-file history for migration
	legacyRootPaths := []string{
		filepath.Join(s.dir, "_history_all.json"),
		filepath.Join(s.dir, ".history.json"),
		filepath.Join(s.dir, "tests-history.json"),
		filepath.Join(filepath.Dir(s.dir), "tests-history.json"),
	}
	var legacyRootToDelete []string
	for _, lp := range legacyRootPaths {
		if data, err := os.ReadFile(lp); err == nil {
			var pf persistFile
			if err := json.Unmarshal(data, &pf); err == nil {
				for _, r := range pf.Runs {
					if r.ID != "" {
						mergeRunIntoMap(runMap, r)
					}
				}
				legacyRootToDelete = append(legacyRootToDelete, lp)
			}
		}
	}

	// Scan category subdirectories in testing dir
	entries, err := os.ReadDir(s.dir)
	var legacyCategoryToDelete []string
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			catName := entry.Name()
			if strings.HasPrefix(catName, ".") || strings.HasPrefix(catName, "_") {
				continue
			}
			catDir := filepath.Join(s.dir, catName)
			catEntries, err := os.ReadDir(catDir)
			if err != nil {
				continue
			}

			for _, ce := range catEntries {
				if ce.IsDir() {
					continue
				}
				name := ce.Name()
				lower := strings.ToLower(name)

				// Check for per-exercise history: <base>._history.json
				if strings.HasSuffix(lower, "._history.json") {
					base := name[:len(name)-len("._history.json")]
					data, err := os.ReadFile(filepath.Join(catDir, name))
					if err != nil {
						continue
					}
					var pf persistFile
					if err := json.Unmarshal(data, &pf); err == nil {
						for _, r := range pf.Runs {
							if r.ID != "" {
								for _, res := range r.Results {
									if res.TestID != "" {
										s.exerciseMap[res.TestID] = exerciseLocation{GroupID: catName, Base: base}
									}
								}
								mergeRunIntoMap(runMap, r)
							}
						}
					}
					continue
				}

				// Check for legacy category-level history: _history.json, .history.json, history.json
				if name == "_history.json" || name == ".history.json" || strings.EqualFold(name, "history.json") {
					histPath := filepath.Join(catDir, name)
					data, err := os.ReadFile(histPath)
					if err != nil {
						continue
					}
					var pf persistFile
					if err := json.Unmarshal(data, &pf); err == nil {
						for _, r := range pf.Runs {
							if r.ID != "" {
								mergeRunIntoMap(runMap, r)
							}
						}
						legacyCategoryToDelete = append(legacyCategoryToDelete, histPath)
					}
				}
			}
		}
	}

	for _, r := range runMap {
		s.runs = append(s.runs, r)
	}

	// If any legacy files were migrated, persist per-exercise history files and clean up legacy files
	if len(legacyRootToDelete) > 0 || len(legacyCategoryToDelete) > 0 {
		exercises := s.allExercisesLocked()
		for ex := range exercises {
			_ = s.saveExerciseLocked(ex.GroupID, ex.Base)
		}
		for _, p := range legacyRootToDelete {
			_ = os.Remove(p)
		}
		for _, p := range legacyCategoryToDelete {
			_ = os.Remove(p)
		}
	}

	return nil
}

// SaveRun appends or updates a run and persists to each affected exercise's <exercise>._history.json.
func (s *ResultStore) SaveRun(run *BatteryRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	found := false
	for i := range s.runs {
		if s.runs[i].ID == run.ID {
			s.runs[i] = *run
			found = true
			break
		}
	}
	if !found {
		s.runs = append(s.runs, *run)
	}

	affected := make(map[exerciseLocation]struct{})
	for _, res := range run.Results {
		gid, base := s.resolveExerciseLocked(res.TestID, run.GroupID)
		affected[exerciseLocation{GroupID: gid, Base: base}] = struct{}{}
	}

	if len(affected) == 0 && run.GroupID != "" && run.GroupID != "all" {
		affected[exerciseLocation{GroupID: run.GroupID, Base: "results"}] = struct{}{}
	}

	for ex := range affected {
		if err := s.saveExerciseLocked(ex.GroupID, ex.Base); err != nil {
			return err
		}
	}
	return nil
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
				gid, base := s.resolveExerciseLocked(testID, s.runs[i].GroupID)
				return s.saveExerciseLocked(gid, base)
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
				gid, base := s.resolveExerciseLocked(testID, s.runs[i].GroupID)
				return s.saveExerciseLocked(gid, base)
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

// DeleteTestHistory removes all battery results for a test across every run and deletes its history file.
func (s *ResultStore) DeleteTestHistory(testID string) error {
	if testID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	targetGid, targetBase := s.resolveExerciseLocked(testID, "")

	affected := make(map[exerciseLocation]struct{})
	kept := make([]BatteryRun, 0, len(s.runs))
	for _, run := range s.runs {
		filtered := run.Results[:0]
		for _, res := range run.Results {
			if res.TestID == testID {
				gid, base := s.resolveExerciseLocked(res.TestID, run.GroupID)
				affected[exerciseLocation{GroupID: gid, Base: base}] = struct{}{}
				continue
			}
			filtered = append(filtered, res)
		}
		if len(filtered) == 0 {
			continue
		}
		if len(filtered) != len(run.Results) {
			run.Results = filtered
		}
		kept = append(kept, run)
	}

	s.runs = kept

	// Remove target exercise history file
	targetFile := filepath.Join(s.dir, targetGid, targetBase+"._history.json")
	_ = os.Remove(targetFile)

	// Save any other affected exercises
	for ex := range affected {
		if ex.GroupID == targetGid && ex.Base == targetBase {
			continue
		}
		_ = s.saveExerciseLocked(ex.GroupID, ex.Base)
	}

	return nil
}

// DeleteRun removes a run by ID.
func (s *ResultStore) DeleteRun(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.runs {
		if r.ID == id {
			affected := make(map[exerciseLocation]struct{})
			for _, res := range r.Results {
				gid, base := s.resolveExerciseLocked(res.TestID, r.GroupID)
				affected[exerciseLocation{GroupID: gid, Base: base}] = struct{}{}
			}
			s.runs = append(s.runs[:i], s.runs[i+1:]...)
			for ex := range affected {
				_ = s.saveExerciseLocked(ex.GroupID, ex.Base)
			}
			return nil
		}
	}
	return errors.New("run not found")
}

func (s *ResultStore) saveExerciseLocked(groupID, exerciseBase string) error {
	if s.dir == "" || exerciseBase == "" {
		return nil
	}
	if groupID == "" {
		groupID = "examples"
	}

	catDir := filepath.Join(s.dir, groupID)
	_ = os.MkdirAll(catDir, 0o755)
	target := filepath.Join(catDir, exerciseBase+"._history.json")

	var exerciseRuns []BatteryRun
	for _, r := range s.runs {
		var filtered []TestResult
		for _, res := range r.Results {
			gid, base := s.resolveExerciseLocked(res.TestID, r.GroupID)
			if gid == groupID && base == exerciseBase {
				filtered = append(filtered, res)
			}
		}
		if len(filtered) > 0 {
			runCopy := r
			runCopy.Results = filtered
			exerciseRuns = append(exerciseRuns, runCopy)
		}
	}

	if len(exerciseRuns) == 0 {
		_ = os.Remove(target)
		return nil
	}

	sort.Slice(exerciseRuns, func(i, j int) bool {
		return exerciseRuns[i].Timestamp.After(exerciseRuns[j].Timestamp)
	})

	data, err := json.MarshalIndent(persistFile{Runs: exerciseRuns}, "", "  ")
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

func (s *ResultStore) saveGroupLocked(groupID string) error {
	if s.dir == "" {
		return nil
	}
	if groupID == "" {
		groupID = "examples"
	}
	exercises := s.allExercisesLocked()
	for ex := range exercises {
		if ex.GroupID == groupID {
			if err := s.saveExerciseLocked(ex.GroupID, ex.Base); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ResultStore) allExercisesLocked() map[exerciseLocation]struct{} {
	m := make(map[exerciseLocation]struct{})
	for _, r := range s.runs {
		for _, res := range r.Results {
			gid, base := s.resolveExerciseLocked(res.TestID, r.GroupID)
			m[exerciseLocation{GroupID: gid, Base: base}] = struct{}{}
		}
	}
	for _, ex := range s.exerciseMap {
		m[ex] = struct{}{}
	}
	return m
}

// DataDir returns the root testing directory path.
func (s *ResultStore) DataDir() string {
	return s.dir
}
