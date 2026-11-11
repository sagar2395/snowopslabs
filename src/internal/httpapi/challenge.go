// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/challenge"
	"github.com/snowops-oss/snowopslabs/internal/incident"
	"github.com/snowops-oss/snowopslabs/internal/scenario"
	chalsvc "github.com/snowops-oss/snowopslabs/internal/service/challenge"
	"github.com/snowops-oss/snowopslabs/pkg/checks"
)

func challengeEngine(s *Server) *challenge.Engine {
	return challenge.New(
		filepath.Join(s.cfg.ProjectRoot, "challenges"),
		filepath.Join(s.cfg.StateDir, "challenges"),
		filepath.Join(s.cfg.StateDir, "history"),
	)
}

func (s *Server) handleListChallenges(w http.ResponseWriter, r *http.Request) {
	eng := challengeEngine(s)
	cs, err := eng.Challenges()
	if err != nil {
		respondInternal(w, r, err)
		return
	}
	type summary struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
		Category    string `json:"category"`
		ParTime     string `json:"parTime"`
	}
	out := make([]summary, 0, len(cs))
	for _, c := range cs {
		out = append(out, summary{
			Name:        c.Name,
			DisplayName: c.DisplayName,
			Description: c.Description,
			Category:    c.Category,
			ParTime:     c.ParTime,
		})
	}
	respondCatalog(w, r, out)
}

func (s *Server) handleChallengeInfo(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "challenge")
	if !ok {
		return
	}
	eng := challengeEngine(s)
	c, err := eng.Load(name)
	if err != nil {
		respondLoadError(w, r, "challenge", name, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"name":        c.Name,
		"displayName": c.DisplayName,
		"description": c.Description,
		"category":    c.Category,
		"parTime":     c.ParTime,
		"hintPenalty": c.HintPenalty,
		"setup":       challengeSetupView(c),
		"silent":      c.Silent,
	})
}

// challengeSetupView is the setup a client may see. A silent challenge gives
// its type and how many faults it injects, not their names.
func challengeSetupView(c *challenge.Challenge) any {
	if !c.Silent {
		return c.Setup
	}
	return map[string]any{"type": c.Setup.Type, "count": len(c.Setup.All())}
}

func (s *Server) handleChallengeStatus(w http.ResponseWriter, r *http.Request) {
	eng := challengeEngine(s)
	active, err := eng.Active()
	if err != nil {
		respondInternal(w, r, err)
		return
	}
	if active == nil {
		respondJSON(w, http.StatusOK, map[string]any{"active": false})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"active":    true,
		"challenge": active.ChallengeName,
		"startedAt": active.StartedAt,
		"hintsUsed": active.HintsUsed,
		"elapsed":   active.Elapsed().String(),
	})
}

func (s *Server) handleChallengeHistory(w http.ResponseWriter, r *http.Request) {
	eng := challengeEngine(s)
	history, err := eng.History()
	if err != nil {
		respondInternal(w, r, err)
		return
	}
	out := make([]challengeRunResp, 0, len(history))
	for _, rec := range history {
		out = append(out, newChallengeRunResp(rec))
	}
	respondJSON(w, http.StatusOK, out)
}

// challengeRunResp is one finished challenge run as the API reports it. The
// engine's own record keeps the duration in nanoseconds; clients are promised
// whole seconds.
type challengeRunResp struct {
	Challenge      string    `json:"challenge"`
	StartedAt      time.Time `json:"startedAt"`
	FinishedAt     time.Time `json:"finishedAt"`
	ElapsedSeconds int64     `json:"elapsedSeconds"`
	HintsUsed      int       `json:"hintsUsed"`
	ChecksPassed   int       `json:"checksPassed"`
	ChecksTotal    int       `json:"checksTotal"`
	Score          int       `json:"score"`
	Outcome        string    `json:"outcome"`
}

func newChallengeRunResp(rec challenge.RunRecord) challengeRunResp {
	return challengeRunResp{
		Challenge:      rec.ChallengeName,
		StartedAt:      rec.StartedAt,
		FinishedAt:     rec.FinishedAt,
		ElapsedSeconds: int64(rec.Elapsed / time.Second),
		HintsUsed:      rec.HintsUsed,
		ChecksPassed:   rec.ChecksPassed,
		ChecksTotal:    rec.ChecksTotal,
		Score:          rec.Score,
		Outcome:        rec.Outcome,
	}
}

// challengeGradeBudget is the time limit for grading one submission. Checks
// run one after another, each with its own timeout.
const challengeGradeBudget = 5 * time.Minute

// challengeGradeResp is one graded submission: the run as it was scored, and
// the result of every check behind that score.
type challengeGradeResp struct {
	challengeRunResp
	Passed bool            `json:"passed"`
	Checks []checks.Result `json:"checks"`
	// CleanupWarning is set when the challenge passed but its setup could not
	// be undone.
	CleanupWarning string `json:"cleanupWarning,omitempty"`
}

// handleChallengeComplete grades the active challenge by running its checks
// against the lab. It reads nothing from the request body, so a client cannot
// report a score. A pass is recorded and ends the run; a fail leaves it going.
func (s *Server) handleChallengeComplete(w http.ResponseWriter, r *http.Request) {
	eng := challengeEngine(s)
	active, err := eng.Active()
	if err != nil {
		respondInternal(w, r, err)
		return
	}
	if active == nil {
		respondNoActiveChallenge(w, r)
		return
	}
	c, err := eng.Load(active.ChallengeName)
	if err != nil {
		respondInternal(w, r, err)
		return
	}

	scenes, incidents, release := s.bindToChallenge(c)
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), challengeGradeBudget)
	defer cancel()

	grader := &chalsvc.Grader{Engine: eng, Scenarios: scenes, Incidents: incidents, Config: s.cfg, Exec: s.exec}
	sub, err := grader.Submit(ctx, actingUser(r))
	switch {
	case errors.Is(err, challenge.ErrNoActive):
		respondNoActiveChallenge(w, r)
		return
	case errors.Is(err, chalsvc.ErrNoChecks):
		respondError(w, r, http.StatusConflict, "no_checks", err.Error())
		return
	case err != nil:
		respondInternal(w, r, err)
		return
	}

	resp := challengeGradeResp{challengeRunResp: newChallengeRunResp(*sub.Record), Passed: sub.Passed, Checks: sub.Checks}
	if sub.CleanupErr != nil {
		slog.Warn("challenge passed but its setup was not undone", "challenge", c.Name, "error", sub.CleanupErr)
		resp.CleanupWarning = "the challenge passed, but the lab was not cleaned up: run 'labctl challenge abort' or resolve it by hand"
	}
	respondJSON(w, http.StatusOK, resp)
}

func respondNoActiveChallenge(w http.ResponseWriter, r *http.Request) {
	respondError(w, r, http.StatusConflict, "no_active_challenge", "no challenge is running: start one with 'labctl challenge start <name>'")
}

// bindToChallenge returns engines bound to the workload the challenge's setup
// acted on: the app its fault was injected into, or the app its scenario was
// activated against. Grading the wrong app would pass a lab that is broken.
func (s *Server) bindToChallenge(c *challenge.Challenge) (*scenario.Engine, *incident.Engine, func()) {
	app := ""
	switch c.Setup.Type {
	case "incident":
		if a, err := s.incidents.Active(); err == nil && a != nil {
			app = a.App
		}
	case "scenario":
		app = s.scenes.ActiveApp(c.Setup.Ref)
	}
	scenes, incidents, release, err := s.withWorkload(app)
	if err != nil {
		// The recorded app is gone. Grade on the current binding so the
		// challenge can still be finished.
		scenes, incidents, release, _ = s.withWorkload("")
	}
	return scenes, incidents, release
}
