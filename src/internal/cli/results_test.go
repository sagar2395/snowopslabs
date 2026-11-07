// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/labtest"
)

// resultsFile is where the test lab keeps its results history.
func resultsFile(l *terminal) string {
	return filepath.Join(l.Home, "state", "clitest", "history", "results.jsonl")
}

// writeHistory replaces the lab's results history with lines.
func writeHistory(t *testing.T, l *terminal, lines ...string) {
	t.Helper()
	path := resultsFile(l)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A game-day history: a fault fixed by hand, one ended with the escape hatch,
// a challenge and a scenario verification.
var gameDayHistory = []string{
	`{"kind":"incident","name":"oom-kill","user":"alice","workload":"go-api","startedAt":"2026-11-06T09:00:00Z","endedAt":"2026-11-06T09:12:34Z","elapsedSeconds":754,"score":-1,"outcome":"resolved","hintsUsed":2,"meta":{"silent":true,"resolvedBy":"manual"}}`,
	`{"kind":"incident","name":"dns-outage","user":"bob","workload":"go-api","startedAt":"2026-11-06T10:00:00Z","endedAt":"2026-11-06T10:05:00Z","elapsedSeconds":300,"score":-1,"outcome":"auto-resolved","meta":{"resolvedBy":"auto"}}`,
	`{"kind":"challenge","name":"on-call-shift","user":"alice","startedAt":"2026-11-06T11:00:00Z","endedAt":"2026-11-06T11:40:00Z","elapsedSeconds":2400,"score":85,"outcome":"passed","hintsUsed":1}`,
	`{"kind":"scenario","name":"drill","user":"carol","startedAt":"2026-11-07T08:00:00Z","endedAt":"2026-11-07T08:00:02Z","elapsedSeconds":2,"score":100,"outcome":"passed","meta":{"checksPassed":1,"checksTotal":1}}`,
}

// Reviewing a game day at the terminal: an empty history exports nothing,
// then the recorded runs come out as JSON and CSV, filtered, and as cards.
func TestResults_Journey(t *testing.T) {
	l := newTerminal(t)
	var steps []cliResult
	ok := func(args ...string) cliResult {
		t.Helper()
		r := l.mustRun(args...)
		steps = append(steps, r)
		return r
	}

	if r := ok("results", "export"); strings.TrimSpace(r.stdout) != "[]" {
		t.Errorf("export of an empty history = %q, want []", r.stdout)
	}

	writeHistory(t, l, gameDayHistory...)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(ok("results", "export").stdout), &rows); err != nil || len(rows) != 4 {
		t.Fatalf("export = %d rows, %v; want all 4 runs as a JSON array", len(rows), err)
	}
	if rows[0]["resolvedBy"] != "fix" || rows[1]["resolvedBy"] != "escape-hatch" || rows[0]["user"] != "alice" || rows[0]["hintsUsed"] != float64(2) {
		t.Errorf("incident rows do not carry who, hints and how it ended: %v / %v", rows[0], rows[1])
	}

	csvOut := ok("results", "export", "--kind", "incident", "--since", "2026-11-06T09:30:00Z", "-o", "csv")
	table, err := csv.NewReader(strings.NewReader(csvOut.stdout)).ReadAll()
	if err != nil || len(table) != 2 || table[1][1] != "dns-outage" || table[1][10] != "escape-hatch" {
		t.Errorf("filtered CSV = %v, %v; want the header and the dns-outage run", table, err)
	}
	ok("results", "export", "--until", "2026-11-06T10:30:00Z", "-o", "csv")

	card := ok("results", "card")
	for _, want := range []string{"Scenario: drill", "Self-reported"} {
		if !strings.Contains(card.stdout, want) {
			t.Errorf("card of the latest run does not show %q:\n%s", want, card.stdout)
		}
	}
	escaped := ok("results", "card", "dns-outage")
	if !strings.Contains(escaped.stdout, "escape hatch") {
		t.Errorf("card of an escaped run does not say so:\n%s", escaped.stdout)
	}
	svg := ok("results", "card", "--kind", "incident", "--by", "Team Blue", "-o", "svg")
	for _, want := range []string{"<svg", "Incident: dns-outage", "Team Blue", "Self-reported"} {
		if !strings.Contains(svg.stdout, want) {
			t.Errorf("SVG card does not show %q:\n%s", want, svg.stdout)
		}
	}
	for _, r := range []cliResult{card, escaped, svg} {
		for _, leak := range []string{"alice", "bob", "carol", "go-api", "clitest", l.Home, l.Root} {
			if strings.Contains(r.stdout, leak) {
				t.Errorf("`labctl %s` shows %q, which the card must not carry", strings.Join(r.args, " "), leak)
			}
		}
	}
	l.golden("results_journey", steps...)
}

// The records labctl writes itself come out of export and onto a card: a
// fault fixed by hand and the same fault ended with the escape hatch.
func TestResults_RecordsFromRealRuns(t *testing.T) {
	l := newTerminal(t)
	l.mustRun("incident", "inject", labtest.Fault)
	l.mustRun("incident", "hint")
	l.remove(faultMarker)
	l.mustRun("incident", "status") // confirms the hand fix
	l.mustRun("incident", "inject", labtest.Fault)
	l.mustRun("incident", "resolve")

	var rows []struct {
		Name       string `json:"name"`
		HintsUsed  int    `json:"hintsUsed"`
		Score      *int   `json:"score"`
		ResolvedBy string `json:"resolvedBy"`
		Outcome    string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(l.mustRun("results", "export", "--kind", "incident").stdout), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ResolvedBy != "fix" || rows[0].HintsUsed != 1 || rows[1].ResolvedBy != "escape-hatch" || rows[0].Score != nil {
		t.Fatalf("export of a fixed and an escaped run = %+v", rows)
	}
	card := l.mustRun("results", "card").stdout
	if !strings.Contains(card, "Incident: "+labtest.Fault) || !strings.Contains(card, "escape hatch") || !strings.Contains(card, "Self-reported") {
		t.Errorf("card of the escaped run:\n%s", card)
	}
}

// A damaged history line is reported with its number, and the other runs are
// still exported.
func TestResults_DamagedLineIsReported(t *testing.T) {
	l := newTerminal(t)
	writeHistory(t, l, gameDayHistory[0], `{"kind":"incident","name":"trunc`, gameDayHistory[1])
	r := l.mustRun("results", "export")
	if !strings.Contains(r.stderr, "skipped 1 damaged line(s)") || !strings.Contains(r.stderr, "(line 2)") || !strings.Contains(r.stderr, "results.jsonl") {
		t.Errorf("export does not report the damaged line:\n%s", r.stderr)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &rows); err != nil || len(rows) != 2 {
		t.Errorf("export with a damaged line = %d rows, %v; want the 2 valid runs on stdout", len(rows), err)
	}
	if c := l.mustRun("results", "card"); !strings.Contains(c.stderr, "damaged") {
		t.Errorf("card does not report the damaged line:\n%s", c.stderr)
	}
}

func TestResults_Failures(t *testing.T) {
	tests := []struct {
		name    string
		history bool
		args    []string
		wantErr string
	}{
		{"card with no runs recorded", false, []string{"results", "card"}, "labctl incident inject --random"},
		{"card for a name with no runs", true, []string{"results", "card", "ghost"}, "see 'labctl results export'"},
		{"card for a kind a card cannot show", true, []string{"results", "card", "--kind", "module"}, `unknown kind "module": use one of incident, challenge, scenario`},
		{"card in a format that does not exist", true, []string{"results", "card", "-o", "png"}, `unknown output format "png": use -o markdown or -o svg`},
		{"export of a kind that does not exist", false, []string{"results", "export", "--kind", "incidents"}, `unknown kind "incidents"`},
		{"export in a format that does not exist", false, []string{"results", "export", "-o", "yaml"}, `unknown output format "yaml": use -o json or -o csv`},
		{"export with a time that cannot be read", false, []string{"results", "export", "--since", "yesterday"}, `--since "yesterday": use a date`},
		{"export with an empty time range", false, []string{"results", "export", "--since", "2026-11-07", "--until", "2026-11-06"}, "the time range is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			if tt.history {
				writeHistory(t, l, gameDayHistory...)
			}
			r := l.run(tt.args...)
			if r.exitCode() != 1 {
				t.Fatalf("exit code = %d, want 1\nstdout:\n%s", r.exitCode(), r.stdout)
			}
			if !strings.Contains(r.stderr, tt.wantErr) {
				t.Errorf("stderr = %q, want it to say %q", r.stderr, tt.wantErr)
			}
		})
	}
}

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		value   string
		want    time.Time
		wantErr bool
	}{
		{"", time.Time{}, false},
		{"4h", now.Add(-4 * time.Hour), false},
		{"90m", now.Add(-90 * time.Minute), false},
		{"2026-11-01T09:00:00Z", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), false},
		{"2026-11-01", time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local), false},
		{"-4h", time.Time{}, true},
		{"0s", time.Time{}, true},
		{"2026-13-01", time.Time{}, true},
		{"last week", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := parseWhen("--since", tt.value, now)
			if (err != nil) != tt.wantErr || !got.Equal(tt.want) {
				t.Errorf("parseWhen(%q) = %v, %v; want %v, error %v", tt.value, got, err, tt.want, tt.wantErr)
			}
		})
	}
}
