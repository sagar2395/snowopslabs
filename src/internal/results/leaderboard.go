// SPDX-License-Identifier: Apache-2.0
package results

import "sort"

// LeaderboardEntry is the per-user aggregate used by the team leaderboard
// (task 063). It rolls up every scored run in the store for one user.
type LeaderboardEntry struct {
	User                string `json:"user"`
	TotalScore          int    `json:"totalScore"`          // sum of challenge scores (>=0 only)
	ChallengesCompleted int    `json:"challengesCompleted"` // challenge runs with outcome=passed
	IncidentsResolved   int    `json:"incidentsResolved"`   // incident runs resolved (manual or auto)
	ModulesCompleted    int    `json:"modulesCompleted"`    // learn module completions
	HintsUsed           int    `json:"hintsUsed"`           // total hints across all runs
	AvgMTTRSeconds      int64  `json:"avgMttrSeconds"`      // mean time-to-resolve over resolved incidents
	Runs                int    `json:"runs"`                // total recorded runs
}

// Leaderboard aggregates all records by user and returns them ranked. The
// ranking favours, in order: higher total score, more challenges completed,
// then a lower average incident MTTR (faster responders break ties). Users with
// no incidents are not penalised on the MTTR tie-breaker (they only reach it
// when score and challenge counts already tie).
func (s *Store) Leaderboard() ([]LeaderboardEntry, error) {
	recs, err := s.All()
	if err != nil {
		return nil, err
	}
	return BuildLeaderboard(recs), nil
}

// BuildLeaderboard computes the ranked leaderboard from a record slice. It is
// separated from the store so it can be unit-tested without disk I/O.
func BuildLeaderboard(recs []Record) []LeaderboardEntry {
	type acc struct {
		entry     LeaderboardEntry
		mttrTotal int64
		mttrCount int
	}
	byUser := map[string]*acc{}
	order := []string{} // preserve first-seen order for stable ranking of ties

	for _, r := range recs {
		user := r.User
		if user == "" {
			user = "unknown"
		}
		a, ok := byUser[user]
		if !ok {
			a = &acc{entry: LeaderboardEntry{User: user}}
			byUser[user] = a
			order = append(order, user)
		}
		a.entry.Runs++
		a.entry.HintsUsed += r.HintsUsed

		switch r.Kind {
		case KindChallenge:
			if r.Score > 0 {
				a.entry.TotalScore += r.Score
			}
			if r.Outcome == "passed" {
				a.entry.ChallengesCompleted++
			}
		case KindIncident:
			if r.Outcome == "resolved" || r.Outcome == "auto-resolved" {
				a.entry.IncidentsResolved++
				if r.Elapsed > 0 {
					a.mttrTotal += r.Elapsed
					a.mttrCount++
				}
			}
		case KindModule:
			if r.Outcome == "completed" {
				a.entry.ModulesCompleted++
			}
		}
	}

	entries := make([]LeaderboardEntry, 0, len(order))
	for _, user := range order {
		a := byUser[user]
		if a.mttrCount > 0 {
			a.entry.AvgMTTRSeconds = a.mttrTotal / int64(a.mttrCount)
		}
		entries = append(entries, a.entry)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].TotalScore != entries[j].TotalScore {
			return entries[i].TotalScore > entries[j].TotalScore
		}
		if entries[i].ChallengesCompleted != entries[j].ChallengesCompleted {
			return entries[i].ChallengesCompleted > entries[j].ChallengesCompleted
		}
		// Lower average MTTR ranks higher; treat 0 (no incidents) as "worst"
		// so responders who actually resolved incidents rank above those who
		// did not, when everything else is tied.
		ai, aj := entries[i].AvgMTTRSeconds, entries[j].AvgMTTRSeconds
		if ai == 0 {
			ai = 1<<63 - 1
		}
		if aj == 0 {
			aj = 1<<63 - 1
		}
		return ai < aj
	})
	return entries
}
