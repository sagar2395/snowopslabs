// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/pkg/checks"
)

// A multi-line check error must not break the table: every row stays one line
// with its columns aligned, and the rest of the error follows the table.
func TestPrintCheckResultsKeepsRowsOnOneLine(t *testing.T) {
	var out bytes.Buffer
	printCheckResultsTo(&out, []checks.Result{
		{Name: "loki-ready", Type: "kubectl", Pass: true, Got: "1", Want: ">= 1", DurationMS: 5},
		{Name: "traces-arrived", Type: "script", Pending: true,
			Error: "FAIL: no spans.\n  Two things have to be true:\n    1. export to the collector\n", DurationMS: 800},
		{Name: "logs-carry-trace-ids", Type: "script", Pending: true, Error: "FAIL: no logs.", DurationMS: 700},
	})

	table, after, _ := strings.Cut(out.String(), "\n\n")
	rows := strings.Split(strings.TrimRight(table, "\n"), "\n")
	if len(rows) != 3 {
		t.Fatalf("want 3 table rows, got %d:\n%s", len(rows), out.String())
	}
	col := strings.Index(rows[0], "loki-ready")
	for _, row := range rows[1:] {
		if !strings.HasSuffix(row, "ms") {
			t.Errorf("row lost its duration column: %q", row)
		}
		if name := strings.Fields(row)[1]; strings.Index(row, name) != col {
			t.Errorf("row %q is not aligned with the first row:\n%s", name, out.String())
		}
	}
	if !strings.Contains(rows[1], "error: FAIL: no spans.") {
		t.Errorf("row should keep the first line of the error: %q", rows[1])
	}
	if !strings.Contains(after, "traces-arrived:\n  Two things have to be true:\n    1. export to the collector") {
		t.Errorf("the rest of the error should follow the table under the check's name:\n%s", after)
	}
}
