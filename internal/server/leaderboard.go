package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LeaderboardGroupCol represents a category column in the cached leaderboard.
type LeaderboardGroupCol struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Order           int      `json:"order"`
	RequiredCaps    []string `json:"required_caps,omitempty"`
	ActiveTotal     int      `json:"activeTotal"`
	ActiveCases     int      `json:"activeCases"`
	ActiveMaxPoints float64  `json:"activeMaxPoints"`
}

// LeaderboardScore holds the precomputed scores of a model in a specific test category.
type LeaderboardScore struct {
	Passed          int      `json:"passed"`
	Tested          int      `json:"tested"`
	PassedCases     int      `json:"passedCases"`
	TotalCases      int      `json:"totalCases"`
	Pts             float64  `json:"pts"`
	ActiveTotal     int      `json:"activeTotal"`
	ActiveCases     int      `json:"activeCases"`
	ActiveMaxPoints float64  `json:"activeMaxPoints"`
	Unrun           int      `json:"unrun"`
	Score           *float64 `json:"score"`
}

// LeaderboardModelRow represents a model row in the cached leaderboard.
type LeaderboardModelRow struct {
	Model       string                      `json:"model"`
	Overall     *float64                    `json:"overall"`
	Passed      int                         `json:"passed"`
	Total       int                         `json:"total"`
	PassedCases int                         `json:"passedCases"`
	TotalCases  int                         `json:"totalCases"`
	Points      float64                     `json:"points"`
	MaxPoints   float64                     `json:"maxPoints"`
	Evaluated   int                         `json:"evaluated"`
	Compatible  int                         `json:"compatible"`
	Coverage    float64                     `json:"coverage"`
	Scores      map[string]LeaderboardScore `json:"scores"`
}

// LeaderboardData represents the serialized payload stored in _leaderboard.json.
type LeaderboardData struct {
	UpdatedAt string                `json:"updated_at"`
	Groups    []LeaderboardGroupCol `json:"groups"`
	Models    []LeaderboardModelRow `json:"models"`
}

// LeaderboardPath returns the absolute path to /testing/_leaderboard.json.
func (s *Server) LeaderboardPath() string {
	if s.testsStore == nil {
		return ""
	}
	dir := s.testsStore.Dir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "_leaderboard.json")
}

// BuildLeaderboardData constructs the precomputed leaderboard data from testsStore and runnerStore.
func (s *Server) BuildLeaderboardData() (*LeaderboardData, error) {
	if s.testsStore == nil || s.runnerStore == nil {
		return nil, errors.New("tests store or runner store not initialized")
	}

	groups, allTests := s.testsStore.List()

	activeCountByGroup := make(map[string]int)
	activeCasesByGroup := make(map[string]int)
	activeMaxPointsByGroup := make(map[string]float64)

	for _, tst := range allTests {
		if tst.Active {
			activeCountByGroup[tst.GroupID]++
			cCount := len(tst.Cases)
			if cCount == 0 {
				cCount = 1
			}
			activeCasesByGroup[tst.GroupID] += cCount
			activeMaxPointsByGroup[tst.GroupID] += float64(cCount + 1)
		}
	}

	// Sort groups by Order then Name/ID
	sortedGroups := make([]struct {
		id           string
		name         string
		order        int
		requiredCaps []string
	}, len(groups))

	for i, g := range groups {
		var caps []string
		for _, c := range g.RequiredCaps {
			caps = append(caps, strings.ToLower(c))
		}
		sortedGroups[i] = struct {
			id           string
			name         string
			order        int
			requiredCaps []string
		}{
			id:           g.ID,
			name:         g.Name,
			order:        g.Order,
			requiredCaps: caps,
		}
	}

	sort.Slice(sortedGroups, func(i, j int) bool {
		if sortedGroups[i].order != sortedGroups[j].order {
			return sortedGroups[i].order < sortedGroups[j].order
		}
		nameI := sortedGroups[i].name
		if nameI == "" {
			nameI = sortedGroups[i].id
		}
		nameJ := sortedGroups[j].name
		if nameJ == "" {
			nameJ = sortedGroups[j].id
		}
		return strings.ToLower(nameI) < strings.ToLower(nameJ)
	})

	cols := make([]LeaderboardGroupCol, 0, len(sortedGroups))
	for _, g := range sortedGroups {
		name := g.name
		if name == "" {
			name = g.id
		}
		cols = append(cols, LeaderboardGroupCol{
			ID:              g.id,
			Name:            name,
			Order:           g.order,
			RequiredCaps:    g.requiredCaps,
			ActiveTotal:     activeCountByGroup[g.id],
			ActiveCases:     activeCasesByGroup[g.id],
			ActiveMaxPoints: activeMaxPointsByGroup[g.id],
		})
	}

	// Calculate per-model scores
	modelSet := make(map[string]bool)
	scoresByModel := make(map[string]map[string]LeaderboardScore)

	for _, col := range cols {
		summaries := s.runnerStore.GetGroupHistory(col.ID)
		for _, sm := range summaries {
			modelSet[sm.Model] = true
			if scoresByModel[sm.Model] == nil {
				scoresByModel[sm.Model] = make(map[string]LeaderboardScore)
			}

			hasLast := sm.LastRunMax > 0 || sm.LastRunTotal > 0
			var passed, tested, passedCases, totalCases int
			var pts, maxPts float64
			var score *float64
			var activeTotal, activeCases int
			var activeMaxPts float64
			var unrun int

			if hasLast {
				passed = sm.LastRunPassed
				tested = sm.LastRunTotal
				passedCases = sm.LastRunPassedCs
				totalCases = sm.LastRunCases
				pts = sm.LastRunPoints
				maxPts = sm.LastRunMax
				if maxPts > 0 {
					v := math.Min(100.0, math.Max(0.0, (pts/maxPts)*100.0))
					score = &v
				}
				unrun = 0
				activeTotal = tested
				activeCases = totalCases
				activeMaxPts = maxPts
			} else {
				passed = sm.Passed
				tested = sm.TotalTests
				passedCases = sm.PassedCases
				if passedCases == 0 && passed > 0 {
					passedCases = passed
				}
				totalCases = sm.TotalCases
				if totalCases == 0 && tested > 0 {
					totalCases = tested
				}
				pts = sm.ScorePoints
				if pts == 0 && passed > 0 {
					pts = float64(passed * 2)
				}
				colActiveTotal := col.ActiveTotal
				colActiveCases := col.ActiveCases
				colActiveMaxPoints := col.ActiveMaxPoints

				activeTotal = colActiveTotal
				if tested > activeTotal {
					activeTotal = tested
				}
				activeCases = colActiveCases
				if totalCases > activeCases {
					activeCases = totalCases
				}
				activeMaxPts = colActiveMaxPoints
				if sm.MaxPoints > activeMaxPts {
					activeMaxPts = sm.MaxPoints
				}
				if activeMaxPts == 0 {
					activeMaxPts = float64(activeTotal * 2)
				}
				if colActiveTotal > tested {
					unrun = colActiveTotal - tested
				}
				if activeMaxPts > 0 {
					v := math.Min(100.0, math.Max(0.0, (pts/activeMaxPts)*100.0))
					score = &v
				} else if activeTotal > 0 {
					v := (float64(passed) / float64(activeTotal)) * 100.0
					score = &v
				}
			}

			scoresByModel[sm.Model][col.ID] = LeaderboardScore{
				Passed:          passed,
				Tested:          tested,
				PassedCases:     passedCases,
				TotalCases:      totalCases,
				Pts:             pts,
				ActiveTotal:     activeTotal,
				ActiveCases:     activeCases,
				ActiveMaxPoints: activeMaxPts,
				Unrun:           unrun,
				Score:           score,
			}
		}
	}

	modelRows := make([]LeaderboardModelRow, 0, len(modelSet))
	for model := range modelSet {
		var totalPassed int
		var totalActive int
		var totalPassedCases int
		var totalActiveCases int
		var totalPoints float64
		var totalMaxPoints float64

		modelScores := scoresByModel[model]
		for _, col := range cols {
			c, ok := modelScores[col.ID]
			if ok && c.ActiveMaxPoints > 0 {
				totalPassed += c.Passed
				totalActive += c.ActiveTotal
				totalPassedCases += c.PassedCases
				totalActiveCases += c.ActiveCases
				totalPoints += c.Pts
				totalMaxPoints += c.ActiveMaxPoints
			}
		}

		var overall *float64
		if totalMaxPoints > 0 {
			v := math.Min(100.0, math.Max(0.0, (totalPoints/totalMaxPoints)*100.0))
			overall = &v
		}

		compatible := 0
		evaluated := 0
		for _, col := range cols {
			if col.ActiveTotal > 0 {
				compatible++
				if c, ok := modelScores[col.ID]; ok && c.Score != nil {
					evaluated++
				}
			}
		}
		coverage := 0.0
		if compatible > 0 {
			coverage = float64(evaluated) / float64(compatible)
		}

		modelRows = append(modelRows, LeaderboardModelRow{
			Model:       model,
			Overall:     overall,
			Passed:      totalPassed,
			Total:       totalActive,
			PassedCases: totalPassedCases,
			TotalCases:  totalActiveCases,
			Points:      totalPoints,
			MaxPoints:   totalMaxPoints,
			Evaluated:   evaluated,
			Compatible:  compatible,
			Coverage:    coverage,
			Scores:      modelScores,
		})
	}

	// Sort models default: most evaluated first, then coverage, then overall, points, total, model
	sort.Slice(modelRows, func(i, j int) bool {
		a := modelRows[i]
		b := modelRows[j]
		if a.Evaluated != b.Evaluated {
			return a.Evaluated > b.Evaluated
		}
		if a.Coverage != b.Coverage {
			return a.Coverage > b.Coverage
		}
		aOverall := -1.0
		if a.Overall != nil {
			aOverall = *a.Overall
		}
		bOverall := -1.0
		if b.Overall != nil {
			bOverall = *b.Overall
		}
		if aOverall != bOverall {
			return aOverall > bOverall
		}
		if a.Points != b.Points {
			return a.Points > b.Points
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		return a.Model < b.Model
	})

	return &LeaderboardData{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Groups:    cols,
		Models:    modelRows,
	}, nil
}

// RegenerateLeaderboardCache recomputes the leaderboard data and persists it atomically to /testing/_leaderboard.json.
func (s *Server) RegenerateLeaderboardCache() (*LeaderboardData, error) {
	s.leaderboardMu.Lock()
	defer s.leaderboardMu.Unlock()

	targetPath := s.LeaderboardPath()
	if targetPath == "" {
		return nil, errors.New("leaderboard path empty")
	}

	data, err := s.BuildLeaderboardData()
	if err != nil {
		return nil, fmt.Errorf("build leaderboard: %w", err)
	}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal leaderboard: %w", err)
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create testing dir for leaderboard: %w", err)
	}

	tmpPath := targetPath + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o644); err != nil {
		return nil, fmt.Errorf("write leaderboard tmp: %w", err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("rename leaderboard tmp to target: %w", err)
	}

	return data, nil
}

// handleGetLeaderboard returns the precomputed leaderboard JSON from disk, or regenerates it if missing or requested.
func (s *Server) handleGetLeaderboard(w http.ResponseWriter, r *http.Request) {
	targetPath := s.LeaderboardPath()
	if targetPath == "" {
		writeError(w, http.StatusInternalServerError, errors.New("leaderboard store not available"))
		return
	}

	refresh := r.URL.Query().Get("refresh")
	forceRefresh := refresh == "true" || refresh == "1"

	if forceRefresh {
		data, err := s.RegenerateLeaderboardCache()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, data)
		return
	}

	// Try reading directly from cached file
	raw, err := os.ReadFile(targetPath)
	if err == nil && len(raw) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}

	// If missing or unreadable, regenerate
	data, err := s.RegenerateLeaderboardCache()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}
