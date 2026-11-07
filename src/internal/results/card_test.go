// SPDX-License-Identifier: Apache-2.0

package results

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func TestNewCard(t *testing.T) {
	ended := time.Date(2026, 11, 7, 9, 12, 34, 0, time.UTC)
	fixed := Record{Kind: KindIncident, Name: "oom-kill", User: "alice", Workload: "go-api", EndedAt: ended, Elapsed: 754, HintsUsed: 2, Score: -1, Outcome: OutcomeResolved,
		Meta: map[string]any{"silent": true, "category": "resources"}}
	escaped := fixed
	escaped.Outcome = OutcomeAutoResolved
	escaped.Meta = nil
	challenge := Record{Kind: KindChallenge, Name: "on-call-shift", User: "alice", EndedAt: ended, Elapsed: 3700, HintsUsed: 1, Score: 85, Outcome: "passed"}
	scenario := Record{Kind: KindScenario, Name: "drill", User: "alice", EndedAt: ended, Elapsed: 2, Score: 50, Outcome: "failed",
		Meta: map[string]any{"checksPassed": float64(1), "checksTotal": float64(2)}}

	tests := []struct {
		name    string
		rec     Record
		by      string
		want    []string
		notWant []string
	}{
		{"an incident fixed by hand", fixed, "",
			[]string{"Incident: oom-kill", "Time to resolve", "12m 34s", "Hints used", "2", "fixed by hand, detection check passed", "Mode", "silent", "2026-11-07"},
			[]string{"escape hatch", "By"}},
		{"an incident ended with the escape hatch", escaped, "",
			[]string{"Incident: oom-kill", "Time before it ended", "12m 34s", "escape hatch (labctl incident resolve)"},
			[]string{"fixed by hand", "Mode", "Time to resolve"}},
		{"a challenge", challenge, "",
			[]string{"Challenge: on-call-shift", "1h 01m", "85/100", "passed"}, nil},
		{"a scenario", scenario, "",
			[]string{"Scenario: drill", "failed", "1 of 2"}, []string{"Time"}},
		{"a name the player adds", fixed, "  Team Blue ",
			[]string{"By", "Team Blue"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewCard(tt.rec, tt.by)
			if err != nil {
				t.Fatalf("NewCard: %v", err)
			}
			for format, out := range map[string]string{"markdown": c.Markdown(), "svg": c.SVG()} {
				if !strings.Contains(out, SelfReported) {
					t.Errorf("%s card does not say it is self-reported:\n%s", format, out)
				}
				for _, w := range tt.want {
					if !strings.Contains(out, w) {
						t.Errorf("%s card does not show %q:\n%s", format, w, out)
					}
				}
				for _, nw := range append(tt.notWant, "alice", "go-api", "resources") {
					if strings.Contains(out, nw) {
						t.Errorf("%s card shows %q:\n%s", format, nw, out)
					}
				}
			}
		})
	}
}

func TestNewCard_KindsWithoutACard(t *testing.T) {
	for _, kind := range []string{KindModule, KindComparison, "unknown"} {
		if _, err := NewCard(Record{Kind: kind, Name: "x"}, ""); err == nil || !strings.Contains(err.Error(), "incident, challenge or scenario") {
			t.Errorf("NewCard(%s) = %v, want an error naming the kinds a card can show", kind, err)
		}
	}
}

// A name edited into the results file cannot break the SVG or the Markdown
// table, or inject markup.
func TestCard_EscapesRecordText(t *testing.T) {
	c, err := NewCard(Record{Kind: KindIncident, Name: "x</text><script>alert(1)</script>|`y`", Outcome: OutcomeResolved}, "a | b")
	if err != nil {
		t.Fatal(err)
	}
	svg := c.SVG()
	if strings.Contains(svg, "<script>") {
		t.Errorf("SVG card carries markup from the record:\n%s", svg)
	}
	if err := xml.Unmarshal([]byte(svg), new(struct{})); err != nil {
		t.Errorf("SVG card is not well-formed XML: %v\n%s", err, svg)
	}
	md := c.Markdown()
	for line := range strings.SplitSeq(md, "\n") {
		if strings.HasPrefix(line, "| By") && strings.Count(strings.ReplaceAll(line, `\|`, ""), "|") != 3 {
			t.Errorf("a | in a value breaks the Markdown table row: %q", line)
		}
	}
	if strings.Contains(md, "`") {
		t.Errorf("a backtick from the record reached the Markdown: %q", md)
	}
}

func TestFormatElapsed(t *testing.T) {
	tests := []struct {
		seconds int64
		want    string
	}{
		{-5, "0s"}, {0, "0s"}, {45, "45s"}, {60, "1m 00s"}, {754, "12m 34s"}, {3599, "59m 59s"}, {3600, "1h 00m"}, {90061, "25h 01m"},
	}
	for _, tt := range tests {
		if got := FormatElapsed(tt.seconds); got != tt.want {
			t.Errorf("FormatElapsed(%d) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

func TestLatest(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 11, 7, 9, m, 0, 0, time.UTC) }
	recs := []Record{
		{Kind: KindIncident, Name: "a", EndedAt: at(1)},
		{Kind: KindChallenge, Name: "c", EndedAt: at(3)},
		{Kind: KindIncident, Name: "b", EndedAt: at(2)},
		{Kind: KindComparison, Name: "drill", EndedAt: at(9)},
		{Kind: KindModule, Name: "p/m", EndedAt: at(8)},
	}
	tests := []struct {
		name, kind, runName string
		want                string
		found               bool
	}{
		{"the newest run a card can show", "", "", "c", true},
		{"the newest of one kind", KindIncident, "", "b", true},
		{"one name", "", "a", "a", true},
		{"a kind with no runs", KindScenario, "", "", false},
		{"a kind a card cannot show", KindComparison, "", "", false},
		{"a name with no runs", "", "ghost", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Latest(recs, tt.kind, tt.runName)
			if ok != tt.found || got.Name != tt.want {
				t.Errorf("Latest = %q, %v; want %q, %v", got.Name, ok, tt.want, tt.found)
			}
		})
	}
}
