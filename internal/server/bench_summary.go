package server

import (
	"strings"
)

// benchSummary is the per-model bench digest attached to /api/models rows
// so the model list can render a BENCH column without extra requests.
type benchSummary struct {
	Overall  *float64 `json:"bench_overall,omitempty"`
	Tested   bool     `json:"bench_tested,omitempty"`
	Complete bool     `json:"bench_complete,omitempty"`
	Missing  []string `json:"bench_missing,omitempty"`
	Possible []string `json:"bench_possible,omitempty"`
}

// missingScoreThreshold mirrors the leaderboard "⚡ missing" rule: a 0.0
// score counts as missing (see web/app-group-history.js missingByModel).
const missingScoreThreshold = 0.0001

// summarizeBench derives per-model bench state from leaderboard data.
//
// capsByModel holds lowercased-or-not capabilities per model; models absent
// from it (e.g. uninstalled ghosts whose caps are unknown) count as
// compatible with every active category — the same rule the leaderboard
// table uses. Names in ghosts are always rendered grey and read-only, so
// they never report complete and carry no missing/possible lists.
func summarizeBench(data *LeaderboardData, capsByModel map[string][]string, ghosts map[string]bool) map[string]benchSummary {
	out := make(map[string]benchSummary)
	if data == nil {
		return out
	}
	var activeCols []LeaderboardGroupCol
	for _, col := range data.Groups {
		// Optional categories don't count toward overall/coverage/completeness,
		// so they never appear as "missing" in the bench digest either.
		if col.ActiveTotal > 0 && col.IsRequired() {
			activeCols = append(activeCols, col)
		}
	}
	if len(activeCols) == 0 {
		return out
	}
	rows := make(map[string]*LeaderboardModelRow, len(data.Models))
	for i := range data.Models {
		rows[data.Models[i].Model] = &data.Models[i]
	}

	lacks := func(caps []string, req []string) bool {
		if len(req) == 0 {
			return false
		}
		set := make(map[string]struct{}, len(caps))
		for _, c := range caps {
			set[strings.ToLower(strings.TrimSpace(c))] = struct{}{}
		}
		for _, c := range req {
			if _, ok := set[strings.ToLower(strings.TrimSpace(c))]; !ok {
				return true
			}
		}
		return false
	}

	// Include models that have a leaderboard row; models without any bench
	// history simply get no entry (frontend treats them as untested).
	for name, row := range rows {
		if ghosts[name] {
			if row.Overall == nil {
				continue
			}
			out[name] = benchSummary{Overall: row.Overall, Tested: true}
			continue
		}
		caps, known := capsByModel[name]
		var missing, possible []string
		for _, col := range activeCols {
			if known && lacks(caps, col.RequiredCaps) {
				continue
			}
			possible = append(possible, col.ID)
			c, ok := row.Scores[col.ID]
			if !ok || c.Score == nil || *c.Score <= missingScoreThreshold {
				missing = append(missing, col.ID)
			}
		}
		if row.Overall == nil {
			continue
		}
		out[name] = benchSummary{
			Overall:  row.Overall,
			Tested:   true,
			Complete: len(possible) > 0 && len(missing) == 0,
			Missing:  missing,
			Possible: possible,
		}
	}
	return out
}
