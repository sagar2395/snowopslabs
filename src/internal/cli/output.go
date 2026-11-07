// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// Output formats a command can print with -o.
const (
	outputTable    = "table"
	outputJSON     = "json"
	outputCSV      = "csv"
	outputMarkdown = "markdown"
	outputSVG      = "svg"
)

// addOutputFlag registers -o/--output on cmd, defaulting to the first of
// allowed, and lists the allowed values in the help text.
func addOutputFlag(cmd *cobra.Command, dst *string, allowed ...string) {
	cmd.Flags().StringVarP(dst, "output", "o", allowed[0], "output format: "+strings.Join(allowed, ", "))
}

// checkOutput rejects a format that is not one of allowed, naming the ones
// that are.
func checkOutput(format string, allowed ...string) error {
	if slices.Contains(allowed, format) {
		return nil
	}
	return fmt.Errorf("unknown output format %q: use -o %s", format, strings.Join(allowed, " or -o "))
}

// writeJSON prints v as indented JSON followed by a newline, so the output
// can be piped to jq.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
