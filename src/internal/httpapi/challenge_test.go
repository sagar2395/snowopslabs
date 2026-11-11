// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/challenge"
	"github.com/snowops-oss/snowopslabs/internal/config"
)

const challengeTestYAML = `
name: test-challenge
displayName: "Test Challenge"
description: "A test challenge"
category: workload
parTime: "10m"
setup:
  type: incident
  ref: bad-deploy-rollout
grading:
  useDetectionCheck: true
hintPenalty: 5
`

func newChallengeServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	cDir := filepath.Join(root, "challenges", "test-challenge")
	if err := os.MkdirAll(cDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cDir, "challenge.yaml"), []byte(challengeTestYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Server{
		cfg: &config.Config{ProjectRoot: root, StateDir: filepath.Join(root, ".labctl"), DomainSuffix: "k3d.local"},
	}
}

func TestHandleListChallenges(t *testing.T) {
	s := newChallengeServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/challenges", nil)
	w := httptest.NewRecorder()
	s.handleListChallenges(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body)
	}
	if w.Body.String() == "" {
		t.Error("expected non-empty body")
	}
}

func TestHandleChallengeInfo_Found(t *testing.T) {
	s := newChallengeServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/challenges/test-challenge", nil)
	req = setVars(req, map[string]string{"name": "test-challenge"})
	w := httptest.NewRecorder()
	s.handleChallengeInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body)
	}
}

func TestHandleChallengeInfo_NotFound(t *testing.T) {
	s := newChallengeServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/challenges/nonexistent", nil)
	req = setVars(req, map[string]string{"name": "nonexistent"})
	w := httptest.NewRecorder()
	s.handleChallengeInfo(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleChallengeStatus_NoActive(t *testing.T) {
	s := newChallengeServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/challenges/status", nil)
	w := httptest.NewRecorder()
	s.handleChallengeStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if body == "" {
		t.Error("expected JSON body")
	}
}

func TestHandleChallengeHistory_Empty(t *testing.T) {
	s := newChallengeServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/challenges/history", nil)
	w := httptest.NewRecorder()
	s.handleChallengeHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}

func TestNewChallengeRunResp_ElapsedIsWholeSeconds(t *testing.T) {
	started := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		elapsed time.Duration
		want    int64
	}{
		{"seven minutes", 7 * time.Minute, 420},
		{"a fraction of a second rounds down", 1999 * time.Millisecond, 1},
		{"under a second is zero", 400 * time.Millisecond, 0},
		{"a long run", 3*time.Hour + 2*time.Second, 10802},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newChallengeRunResp(challenge.RunRecord{
				ChallengeName: "c", StartedAt: started, FinishedAt: started.Add(tt.elapsed),
				Elapsed: tt.elapsed, Score: 80, Outcome: "passed",
			})
			if got.ElapsedSeconds != tt.want {
				t.Errorf("ElapsedSeconds = %d, want %d", got.ElapsedSeconds, tt.want)
			}
			body, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if want := fmt.Sprintf(`"elapsedSeconds":%d`, tt.want); !strings.Contains(string(body), want) {
				t.Errorf("JSON = %s, want it to contain %s", body, want)
			}
		})
	}
}

// breakDemoLab injects the demo fault and starts the challenge that grades
// it, as `labctl challenge start` leaves the lab.
func breakDemoLab(t *testing.T, s *Server) {
	t.Helper()
	injectDemoFault(t, s)
	startDemoChallenge(t, s)
}

// The route grades on the server. Whatever counts a client sends, the answer
// is what the challenge's own check found in the lab.
func TestChallengeComplete_GradesOnTheServer(t *testing.T) {
	marker := func(s *Server) string { return filepath.Join(s.cfg.ProjectRoot, "incidents", labFault, "BROKEN") }
	tests := []struct {
		name        string
		body        string
		fix         bool
		wantPassed  bool
		wantOutcome string
		wantRuns    int
	}{
		{"a client-reported perfect score on a broken lab is not a pass", `{"passed":1,"total":1}`, false, false, "failed", 0},
		{"no body on a broken lab is not a pass", "", false, false, "failed", 0},
		{"a body that is not JSON changes nothing", "{", false, false, "failed", 0},
		{"a fixed lab passes", "", true, true, "passed", 1},
		{"a fixed lab passes whatever the client claims", `{"passed":0,"total":9}`, true, true, "passed", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newContractLab(t)
			breakDemoLab(t, s)
			if _, err := os.Stat(marker(s)); err != nil {
				t.Fatalf("the demo fault did not break the lab: %v", err)
			}
			if tt.fix {
				if err := os.Remove(marker(s)); err != nil {
					t.Fatal(err)
				}
			}

			w := do(s, http.MethodPost, "/challenges/complete", tt.body, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200\nbody: %s", w.Code, w.Body)
			}
			var got struct {
				Passed       bool   `json:"passed"`
				Outcome      string `json:"outcome"`
				Score        int    `json:"score"`
				ChecksPassed int    `json:"checksPassed"`
				ChecksTotal  int    `json:"checksTotal"`
				Checks       []struct {
					Name string `json:"name"`
					Pass bool   `json:"pass"`
				} `json:"checks"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding %s: %v", w.Body, err)
			}
			if got.Passed != tt.wantPassed || got.Outcome != tt.wantOutcome {
				t.Errorf("passed = %v, outcome = %q; want %v, %q", got.Passed, got.Outcome, tt.wantPassed, tt.wantOutcome)
			}
			if got.ChecksTotal != 1 || len(got.Checks) != 1 || got.Checks[0].Pass != tt.wantPassed {
				t.Errorf("checks = %d/%d %+v, want the one detection check with pass=%v",
					got.ChecksPassed, got.ChecksTotal, got.Checks, tt.wantPassed)
			}
			if !tt.wantPassed && got.Score != 0 {
				t.Errorf("score = %d on a broken lab, want 0", got.Score)
			}

			history, err := challengeEngine(s).History()
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != tt.wantRuns {
				t.Errorf("history has %d runs, want %d: %+v", len(history), tt.wantRuns, history)
			}
			board := do(s, http.MethodGet, "/results/challenge", "", nil)
			if recorded := strings.Contains(board.Body.String(), labChallenge); recorded != tt.wantPassed {
				t.Errorf("in the challenge results = %v, want %v\nbody: %s", recorded, tt.wantPassed, board.Body)
			}

			active, err := challengeEngine(s).Active()
			if err != nil {
				t.Fatal(err)
			}
			if stillRunning := active != nil; stillRunning == tt.wantPassed {
				t.Errorf("challenge still running = %v, want %v", stillRunning, !tt.wantPassed)
			}
			if incident, _ := s.incidents.Active(); (incident != nil) == tt.wantPassed {
				t.Errorf("incident still active = %v, want %v", incident != nil, !tt.wantPassed)
			}
		})
	}
}

// A pass is recorded for the user who submitted it.
func TestChallengeComplete_RecordsTheSessionUser(t *testing.T) {
	s, _, participant := newAuthedLab(t)
	startDemoChallenge(t, s)

	w := do(s, http.MethodPost, "/challenges/complete", "", participant)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d\nbody: %s", w.Code, w.Body)
	}
	board := do(s, http.MethodGet, "/results/challenge", "", participant)
	if !strings.Contains(board.Body.String(), `"pat"`) {
		t.Errorf("the challenge results do not name the participant who passed: %s", board.Body)
	}
}

func TestChallengeComplete_ReportsAFailedCleanup(t *testing.T) {
	s := newContractLab(t)
	startDemoChallenge(t, s)
	writeLabFile(t, filepath.Join(s.cfg.ProjectRoot, "incidents", labFault, "resolve.sh"), script+"exit 3\n")

	w := do(s, http.MethodPost, "/challenges/complete", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d\nbody: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"passed":true`) || !strings.Contains(body, `"cleanupWarning"`) {
		t.Errorf("want a pass that carries a cleanup warning, got %s", body)
	}
	if strings.Contains(body, s.cfg.ProjectRoot) {
		t.Errorf("the warning leaks a filesystem path: %s", body)
	}
}

// A challenge set up by a scenario is graded against the app that scenario
// was activated for.
func TestBindToChallenge(t *testing.T) {
	s := newContractLab(t)
	tests := []struct {
		name  string
		setup challenge.SetupSpec
	}{
		{"incident with nothing injected keeps the current binding", challenge.SetupSpec{Type: "incident", Ref: labFault}},
		{"scenario that is not active keeps the current binding", challenge.SetupSpec{Type: "scenario", Ref: labScenario}},
		{"unknown setup type keeps the current binding", challenge.SetupSpec{Type: "wizardry"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenes, incidents, release := s.bindToChallenge(&challenge.Challenge{Setup: tt.setup})
			defer release()
			if scenes.Workload != s.scenes.Workload || incidents.Workload != s.incidents.Workload {
				t.Errorf("bound to %q/%q, want the server's binding %q", scenes.Workload.Name, incidents.Workload.Name, s.scenes.Workload.Name)
			}
		})
	}
}

// A silent challenge's faults are the exercise: info gives their count, never
// their names. A challenge that is not silent still shows its setup.
func TestHandleChallengeInfo_SilentHidesTheFaults(t *testing.T) {
	s := newChallengeServer(t)
	dir := filepath.Join(s.cfg.ProjectRoot, "challenges", "shift")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shift := "name: shift\ndisplayName: Shift\nsilent: true\nsetup:\n  type: incident\n  refs: [oom-kill, network-blackhole]\ngrading:\n  useDetectionCheck: true\n"
	if err := os.WriteFile(filepath.Join(dir, "challenge.yaml"), []byte(shift), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		want    []string
		mustNot []string
	}{
		{"shift", []string{`"silent":true`, `"count":2`, `"type":"incident"`}, []string{"oom-kill", "network-blackhole"}},
		{"test-challenge", []string{`"silent":false`, `"ref":"bad-deploy-rollout"`}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := setVars(httptest.NewRequest(http.MethodGet, "/api/v2/challenges/"+tt.name, nil), map[string]string{"name": tt.name})
			w := httptest.NewRecorder()
			s.handleChallengeInfo(w, req)
			body := w.Body.String()
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, body)
			}
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Errorf("body %s is missing %s", body, want)
				}
			}
			for _, leak := range tt.mustNot {
				if strings.Contains(body, leak) {
					t.Errorf("body %s names %s", body, leak)
				}
			}
		})
	}
}
