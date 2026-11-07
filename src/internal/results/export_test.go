// SPDX-License-Identifier: Apache-2.0

package results

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

var day = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

// exportFixture is one of each kind of run a review sees.
func exportFixture() []Record {
	return []Record{
		{Kind: KindIncident, Name: "oom-kill", User: "alice", Workload: "go-api", StartedAt: day, EndedAt: day.Add(754 * time.Second), Elapsed: 754, Score: -1, Outcome: OutcomeResolved, HintsUsed: 2},
		{Kind: KindIncident, Name: "dns-outage", User: "bob", StartedAt: day.Add(time.Hour), EndedAt: day.Add(time.Hour + 300*time.Second), Elapsed: 300, Score: -1, Outcome: OutcomeAutoResolved},
		{Kind: KindChallenge, Name: "on-call-shift", User: "alice", StartedAt: day.Add(2 * time.Hour), EndedAt: day.Add(2*time.Hour + time.Minute), Elapsed: 60, Score: 0, Outcome: "failed", HintsUsed: 1},
		{Kind: KindScenario, Name: "drill", User: "carol", StartedAt: day.Add(24 * time.Hour), EndedAt: day.Add(24*time.Hour + time.Second), Elapsed: 1, Score: 100, Outcome: "passed"},
	}
}

func TestExport(t *testing.T) {
	tests := []struct {
		name   string
		filter ExportFilter
		want   []string // name/resolvedBy/score of each row
	}{
		{"everything", ExportFilter{}, []string{"oom-kill/fix/-", "dns-outage/escape-hatch/-", "on-call-shift//0", "drill//100"}},
		{"one kind", ExportFilter{Kind: KindIncident}, []string{"oom-kill/fix/-", "dns-outage/escape-hatch/-"}},
		{"since is inclusive", ExportFilter{Since: day.Add(time.Hour)}, []string{"dns-outage/escape-hatch/-", "on-call-shift//0", "drill//100"}},
		{"until is exclusive", ExportFilter{Until: day.Add(time.Hour)}, []string{"oom-kill/fix/-"}},
		{"a range and a kind", ExportFilter{Kind: KindChallenge, Since: day, Until: day.Add(24 * time.Hour)}, []string{"on-call-shift//0"}},
		{"a kind with no runs", ExportFilter{Kind: KindComparison}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := Export(exportFixture(), tt.filter)
			var got []string
			for _, r := range rows {
				score := "-"
				if r.Score != nil {
					score = strconv.Itoa(*r.Score)
				}
				got = append(got, r.Name+"/"+r.ResolvedBy+"/"+score)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("Export = %v, want %v", got, tt.want)
			}
		})
	}
}

// The exported JSON has every documented field, with null for an unscored
// run, and nothing else.
func TestExport_JSONFields(t *testing.T) {
	rows := Export(exportFixture()[:1], ExportFilter{})
	data, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(ExportColumns) {
		t.Errorf("JSON has %d fields, the documented set has %d: %s", len(got), len(ExportColumns), data)
	}
	for _, col := range ExportColumns {
		if _, ok := got[col]; !ok {
			t.Errorf("JSON is missing the documented field %q: %s", col, data)
		}
	}
	want := map[string]any{"user": "alice", "elapsedSeconds": float64(754), "hintsUsed": float64(2), "score": nil, "resolvedBy": "fix", "startedAt": "2026-11-07T09:00:00Z"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestExportFilter_Validate(t *testing.T) {
	tests := []struct {
		name    string
		filter  ExportFilter
		wantErr string
	}{
		{"no filter", ExportFilter{}, ""},
		{"a known kind", ExportFilter{Kind: KindModule}, ""},
		{"an unknown kind", ExportFilter{Kind: "incidents"}, `unknown kind "incidents": use one of incident, challenge, scenario, module, comparison`},
		{"since before until", ExportFilter{Since: day, Until: day.Add(time.Second)}, ""},
		{"since equal to until", ExportFilter{Since: day, Until: day}, "the time range is empty"},
		{"since after until", ExportFilter{Since: day.Add(time.Hour), Until: day}, "the time range is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.filter.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate = %v, want it to say %q", err, tt.wantErr)
			}
		})
	}
}

func TestWriteCSV(t *testing.T) {
	recs := exportFixture()
	recs = append(recs, Record{Kind: KindIncident, Name: "x", User: "=HYPERLINK(\"http://evil\")", StartedAt: day, EndedAt: day, Score: -1, Outcome: OutcomeResolved})
	var buf bytes.Buffer
	if err := WriteCSV(&buf, Export(recs, ExportFilter{})); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	table, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("the output is not CSV: %v\n%s", err, buf.String())
	}
	if strings.Join(table[0], ",") != strings.Join(ExportColumns, ",") {
		t.Errorf("header = %v, want %v", table[0], ExportColumns)
	}
	want := []string{"incident", "oom-kill", "alice", "go-api", "2026-11-07T09:00:00Z", "2026-11-07T09:12:34Z", "754", "2", "", "resolved", "fix"}
	if strings.Join(table[1], ",") != strings.Join(want, ",") {
		t.Errorf("first row = %v, want %v", table[1], want)
	}
	if got := table[3][8]; got != "0" {
		t.Errorf("a score of 0 is written as %q, want 0", got)
	}
	if got := table[5][2]; !strings.HasPrefix(got, "'=") {
		t.Errorf("a user that starts with = is written as %q, which a spreadsheet runs as a formula", got)
	}
}

func TestWriteCSV_NoRows(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteCSV(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != strings.Join(ExportColumns, ",") {
		t.Errorf("CSV with no rows = %q, want only the header", got)
	}
}
