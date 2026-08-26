// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/catalog"
	"github.com/sagar2395/snowopslabs/internal/config"
)

// `labctl validate` loads every content item (scenarios, incidents, learning
// paths, challenges) from the in-repo root and any SNOWOPS_CONTENT_PATH
// roots, and reports schema, cross-reference and template problems. It exits
// non-zero when anything is wrong so CI can gate on it (W2-T07).
func validateCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate all declarative content and report any problems",
		Long: `Load every scenario, incident, learning path and challenge and check it
against the content model: required fields, cross-references (a path or
challenge must point at content that exists), and templates (an unknown
variable is an error). Reports the file, line and reference of each problem and
exits non-zero if any are found.

Extra content roots named in SNOWOPS_CONTENT_PATH are validated too.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(projectDir)
			if err != nil {
				return fmt.Errorf("locating project root: %w", err)
			}
			roots := catalog.ResolveRoots(cfg.ProjectRoot)
			cat, err := catalog.Load(roots...)
			if err != nil {
				return err
			}
			if err := writeValidationReport(cmd.OutOrStdout(), cat, asJSON); err != nil {
				return err
			}
			if !cat.IsValid() {
				// Non-zero exit without a duplicate cobra error line.
				return errSilent
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}

// errSilent signals a non-zero exit whose message has already been printed.
var errSilent = fmt.Errorf("")

// writeValidationReport renders the catalog's validation outcome. On success it
// prints a one-line summary; on failure it prints every problem, sorted, in the
// stable "file:line: [kind/name] message" form, followed by a count.
func writeValidationReport(w io.Writer, c *catalog.Catalog, asJSON bool) error {
	counts := c.Counts()
	if asJSON {
		report := struct {
			Valid    bool              `json:"valid"`
			Counts   map[string]int    `json:"counts"`
			Roots    []string          `json:"roots"`
			Problems []catalog.Problem `json:"problems"`
		}{
			Valid:    c.IsValid(),
			Counts:   map[string]int{},
			Roots:    c.Roots,
			Problems: c.Problems(),
		}
		for k, n := range counts {
			report.Counts[string(k)] = n
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	if c.IsValid() {
		_, err := fmt.Fprintf(w, "✓ all content valid — %s\n", summarize(counts))
		return err
	}

	problems := c.Problems()
	for _, p := range problems {
		if _, err := fmt.Fprintln(w, p.String()); err != nil {
			return err
		}
	}
	noun := "problem"
	if len(problems) != 1 {
		noun += "s"
	}
	_, err := fmt.Fprintf(w, "\n✗ %d %s found across %s\n", len(problems), noun, summarize(counts))
	return err
}

// summarize renders the per-kind counts in a stable order.
func summarize(counts map[catalog.Kind]int) string {
	labels := map[catalog.Kind]string{
		catalog.KindScenario:  "scenarios",
		catalog.KindIncident:  "incidents",
		catalog.KindPath:      "paths",
		catalog.KindChallenge: "challenges",
	}
	kinds := make([]catalog.Kind, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	out := ""
	for i, k := range kinds {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%d %s", counts[k], labels[k])
	}
	return out
}
