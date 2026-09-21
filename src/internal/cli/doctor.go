// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// Minimum Docker resources, matching the README. The 3-node k3d cluster and
// platform run out of memory or time out on a default 2 GB VM, often with an
// API server "TLS handshake timeout".
const (
	minDockerCPU      = 4
	minDockerMemBytes = 8 << 30 // 8 GiB
)

// doctorCmd checks the user's environment before a cluster build. Each
// failure names the tool, what breaks without it, and how to fix it on this
// platform.
func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that your environment can run SnowOps Labs",
		Long: `Verifies every external tool SnowOps Labs depends on: that it is
installed, that it is new enough, and that the cluster is reachable.

Each problem is reported with the reason it matters and how to fix it. Exits
non-zero if anything required is missing or out of date, so it is safe to use
as a gate in a script.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd.Context(), cmd.OutOrStdout(), toolchain.NewExec())
		},
	}
}

// runDoctor runs the checks and prints the results to out. It takes a runner
// so tests can use a fake.
func runDoctor(ctx context.Context, out io.Writer, runner toolchain.Runner) error {
	if ctx == nil {
		ctx = context.Background()
	}

	results, err := toolchain.NewPreflight(runner).Check(ctx)
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "SnowOps Labs environment check")
	fmt.Fprintln(out)

	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "TOOL\tSTATUS\tVERSION\tREQUIRED")
	for _, r := range results {
		version := r.Version
		if version == "" {
			version = "-"
		}
		required := r.Required
		if required == "" {
			required = "any"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Binary, statusLabel(r), version, required)
	}
	_ = w.Flush()

	var problems, warnings []toolchain.CheckResult
	for _, r := range results {
		if r.Detail == "" {
			continue
		}
		if r.OK() {
			if r.Status != toolchain.CheckOK {
				warnings = append(warnings, r)
			}
			continue
		}
		problems = append(problems, r)
	}

	dockerNote := dockerResourceWarning(ctx, runner)

	if len(warnings) > 0 || dockerNote != "" {
		fmt.Fprintln(out, "\nNotes:")
		for _, r := range warnings {
			fmt.Fprintf(out, "  - %s\n", r.Detail)
		}
		if dockerNote != "" {
			fmt.Fprintf(out, "  - %s\n", dockerNote)
		}
	}

	if len(problems) > 0 {
		fmt.Fprintln(out, "\nProblems to fix:")
		for _, r := range problems {
			fmt.Fprintf(out, "  ✗ %s\n", r.Detail)
		}
		fmt.Fprintln(out)
		return fmt.Errorf("%d required tool(s) missing or out of date", len(problems))
	}

	if !hostsBlockPresent() {
		fmt.Fprintln(out, "\nNotes:")
		fmt.Fprintln(out, "  - Ingress hostnames (e.g. http://grafana.k3d.local) won't resolve until you")
		fmt.Fprintln(out, "    run 'labctl hosts add' (one-time, needs sudo). Not needed for the UI at :3939.")
	}

	if isWSL() {
		fmt.Fprintln(out, "\nWSL notes:")
		for _, n := range wslDoctorNotes() {
			fmt.Fprintf(out, "  - %s\n", n)
		}
	}

	fmt.Fprintln(out, "\n✓ Everything SnowOps Labs needs is installed and current.")
	return nil
}

func statusLabel(r toolchain.CheckResult) string {
	switch r.Status {
	case toolchain.CheckOK:
		return "ok"
	case toolchain.CheckMissing:
		if r.Optional {
			return "missing (optional)"
		}
		return "MISSING"
	case toolchain.CheckOutdated:
		if r.Optional {
			return "outdated (optional)"
		}
		return "OUTDATED"
	default:
		return "unknown"
	}
}

// dockerResourceWarning returns a warning when Docker has fewer CPUs or less
// memory than the minimum, or "" when it has enough or the values cannot be
// read (docker missing, daemon down, output unparseable). A missing docker is
// already reported by the tool checks.
func dockerResourceWarning(ctx context.Context, runner toolchain.Runner) string {
	if ctx == nil {
		ctx = context.Background()
	}

	ncpu, mem, ok := dockerResources(ctx, runner)
	if !ok {
		return ""
	}
	if ncpu >= minDockerCPU && mem >= minDockerMemBytes {
		return ""
	}

	const gib = 1 << 30
	// Show "2 GiB" rather than "2.0 GiB" for whole numbers.
	detectedMem := strconv.FormatFloat(float64(mem)/gib, 'f', -1, 64)

	return fmt.Sprintf(
		"⚠️  Docker has %d CPU / %s GiB available; SnowOps Labs needs at least %d CPU / %d GiB.\n"+
			"    A 3-node k3d cluster plus the platform stack (Prometheus, Grafana, Loki, …)\n"+
			"    OOM-kills pods or fails with an API-server \"TLS handshake timeout\" below this.\n"+
			"    Colima:         colima stop && colima start --cpu %d --memory %d\n"+
			"    Docker Desktop: Settings → Resources → raise CPUs to %d and Memory to %d GB",
		ncpu, detectedMem, minDockerCPU, minDockerMemBytes/gib,
		minDockerCPU, minDockerMemBytes/gib, minDockerCPU, minDockerMemBytes/gib,
	)
}

// dockerResources reports the CPU count and total memory (bytes) the Docker
// engine has, via `docker info`. ok is false when the value cannot be
// determined (docker absent, daemon down, or unparseable output).
func dockerResources(ctx context.Context, runner toolchain.Runner) (ncpu int, memBytes int64, ok bool) {
	path, err := runner.LookPath("docker")
	if err != nil {
		return 0, 0, false
	}

	// A hung daemon must not hang doctor.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var buf bytes.Buffer
	// --format gives machine-readable output; MemTotal is in bytes.
	_, err = runner.Run(ctx, toolchain.Command{
		Path:   path,
		Args:   []string{"info", "--format", "{{.NCPU}} {{.MemTotal}}"},
		Stdout: &buf,
	})
	if err != nil {
		return 0, 0, false
	}

	fields := strings.Fields(buf.String())
	if len(fields) != 2 {
		return 0, 0, false
	}
	ncpu, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, false
	}
	memBytes, err = strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if ncpu <= 0 || memBytes <= 0 {
		return 0, 0, false
	}
	return ncpu, memBytes, true
}

func init() {
	rootCmd.AddCommand(doctorCmd())
}
