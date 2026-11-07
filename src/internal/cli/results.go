// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sagar2395/snowopslabs/internal/display"
	resultspkg "github.com/sagar2395/snowopslabs/internal/results"
	"github.com/spf13/cobra"
)

func resultsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "results",
		Short: "Export recorded runs, or make a card of one to share",
		Long: `Every finished incident, challenge, scenario verification, learning module
and comparison is recorded in the lab's history. 'results export' writes those
records as JSON or CSV for a spreadsheet or a game-day review; 'results card'
turns one run into Markdown or an SVG image you can post.`,
	}
	cmd.AddCommand(resultsExportCmd())
	cmd.AddCommand(resultsCardCmd())
	return cmd
}

// resultsStore is the lab's results history.
func resultsStore() *resultspkg.Store {
	return resultspkg.NewStore(filepath.Join(cfg.StateDir, "history"))
}

func resultsExportCmd() *cobra.Command {
	var output, kind, since, until string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write recorded runs as JSON or CSV",
		Example: `  labctl results export > results.json
  labctl results export --kind incident --since 2026-11-01 -o csv > game-day.csv
  labctl results export --since 4h -o csv`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkOutput(output, outputJSON, outputCSV); err != nil {
				return err
			}
			now := time.Now()
			f := resultspkg.ExportFilter{Kind: kind}
			var err error
			if f.Since, err = parseWhen("--since", since, now); err != nil {
				return err
			}
			if f.Until, err = parseWhen("--until", until, now); err != nil {
				return err
			}
			return runResultsExport(cmd.OutOrStdout(), cmd.ErrOrStderr(), resultsStore(), f, output)
		},
	}
	addOutputFlag(cmd, &output, outputJSON, outputCSV)
	cmd.Flags().StringVar(&kind, "kind", "", "only runs of this kind: "+strings.Join(resultspkg.Kinds, ", "))
	cmd.Flags().StringVar(&since, "since", "", "only runs that started at or after this date, time or duration ago (2026-11-01, 2026-11-01T09:00:00Z, 4h)")
	cmd.Flags().StringVar(&until, "until", "", "only runs that started before this date, time or duration ago")
	return cmd
}

// runResultsExport writes the records f keeps to out. Damaged history lines
// are skipped and reported on errW, so the export on out stays parseable.
func runResultsExport(out, errW io.Writer, st *resultspkg.Store, f resultspkg.ExportFilter, output string) error {
	if err := f.Validate(); err != nil {
		return err
	}
	recs, err := readResults(errW, st)
	if err != nil {
		return err
	}
	rows := resultspkg.Export(recs, f)
	if output == outputCSV {
		return resultspkg.WriteCSV(out, rows)
	}
	return writeJSON(out, rows)
}

// readResults reads the history and warns on errW about any damaged lines.
func readResults(errW io.Writer, st *resultspkg.Store) ([]resultspkg.Record, error) {
	recs, damaged, err := st.Read()
	if err != nil {
		return nil, fmt.Errorf("reading the results history: %w", err)
	}
	if len(damaged) > 0 {
		lines := make([]string, len(damaged))
		for i, n := range damaged {
			lines[i] = strconv.Itoa(n)
		}
		fmt.Fprintf(errW, "Warning: skipped %d damaged line(s) in %s (line %s); the other runs are included.\n",
			len(damaged), display.Text(st.Path(), cfg.ProjectRoot, cfg.StateDir, cliCurrentDir()), strings.Join(lines, ", "))
	}
	return recs, nil
}

// parseWhen reads a --since or --until value: a date (local midnight), an RFC
// 3339 time, or a duration counted back from now. Empty is no bound.
func parseWhen(flag, value string, now time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(value); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, value, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%s %q: use a date (2026-11-01), a time (2026-11-01T09:00:00Z) or a duration before now (4h)", flag, value)
}

func resultsCardCmd() *cobra.Command {
	var output, kind, by string
	cmd := &cobra.Command{
		Use:   "card [name]",
		Short: "Print a shareable card for your latest run, marked self-reported",
		Long: `Print a card for the most recent incident, challenge or scenario run, or
the most recent run of [name], as Markdown or an SVG image. The card is made on
this machine and nothing is uploaded. It shows the run, the time it took, the
hints used and how it ended, and says it is self-reported: the history is a
local file, so the project has not checked it. It carries no user, host or
path; --by adds a name of your choosing.`,
		Example: `  labctl results card
  labctl results card oom-kill --by "Team Blue" -o svg > card.svg`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output, outputMarkdown, outputSVG); err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runResultsCard(cmd.OutOrStdout(), cmd.ErrOrStderr(), resultsStore(), kind, name, by, output)
		},
	}
	addOutputFlag(cmd, &output, outputMarkdown, outputSVG)
	cmd.Flags().StringVar(&kind, "kind", "", "only runs of this kind: "+strings.Join(resultspkg.CardKinds, ", "))
	cmd.Flags().StringVar(&by, "by", "", "a name to show on the card; none is shown otherwise")
	return cmd
}

// errNoRunForCard is returned when the history has no run a card can show.
var errNoRunForCard = errors.New("no finished incident, challenge or scenario run is recorded yet: finish one, for example 'labctl incident inject --random', then run this again")

// runResultsCard prints the card for the latest run matching kind and name.
func runResultsCard(out, errW io.Writer, st *resultspkg.Store, kind, name, by, output string) error {
	if kind != "" && !slices.Contains(resultspkg.CardKinds, kind) {
		return fmt.Errorf("unknown kind %q: use one of %s", kind, strings.Join(resultspkg.CardKinds, ", "))
	}
	recs, err := readResults(errW, st)
	if err != nil {
		return err
	}
	rec, ok := resultspkg.Latest(recs, kind, name)
	switch {
	case !ok && name != "":
		return fmt.Errorf("no recorded run of %q: see 'labctl results export' for the runs that are recorded", name)
	case !ok:
		return errNoRunForCard
	}
	card, err := resultspkg.NewCard(rec, by)
	if err != nil {
		return err
	}
	if output == outputSVG {
		_, err = io.WriteString(out, card.SVG())
	} else {
		_, err = io.WriteString(out, card.Markdown())
	}
	return err
}
