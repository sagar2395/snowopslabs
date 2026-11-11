// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd.Context(), cmd.OutOrStdout(), toolchain.NewExec())
		},
	}
}

// runDoctor runs the checks and prints the results to out. It takes a runner
// so tests can use a fake.
func runDoctor(ctx context.Context, out io.Writer, runner toolchain.Runner) error {
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

	problems, notes := toolFindings(results)
	lab, labErr := config.FindLab(projectDir)
	if labErr != nil {
		problems = append(problems, labErr.Error())
	} else {
		fmt.Fprintf(out, "\nLab:      %s%s\n", lab, labVersionSuffix(config.LabVersion(lab)))
	}
	docker := dockerCapacity(ctx, runner, capacity.Platform{GOOS: runtime.GOOS, WSL: isWSL()})
	if docker.line != "" {
		fmt.Fprintf(out, "\nDocker:   %s\n", docker.line)
	}
	if docker.problem != "" {
		problems = append(problems, docker.problem)
	}
	if docker.note != "" {
		notes = append(notes, docker.note)
	}
	if labErr == nil && docker.res.MemBytes > 0 {
		if note := memoryRequestsNote(ctx, runner, lab, docker.res); note != "" {
			notes = append(notes, note)
		}
		if note := inotifyNote(ctx, runner, lab); note != "" {
			notes = append(notes, note)
		}
	}
	suffix, local := labDomain(lab, labErr)
	if !local && !hostsBlockPresent() {
		notes = append(notes, "Ingress hostnames (e.g. http://grafana."+suffix+") won't resolve until you\n"+
			"    run 'labctl hosts add' (one-time, needs sudo). Not needed for the UI at :3939.")
	}

	printBullets(out, "Notes:", "-", notes)
	if isWSL() {
		dir := lab
		if labErr != nil {
			dir = workingDir()
		}
		printBullets(out, "WSL notes:", "-", wslDoctorNotes(dir, suffix, local))
	}
	if len(problems) > 0 {
		printBullets(out, "Problems to fix:", "✗", problems)
		fmt.Fprintln(out)
		return fmt.Errorf("%d problem(s) to fix before SnowOps Labs can run", len(problems))
	}

	if len(notes) > 0 {
		fmt.Fprintln(out, "\n✓ Ready to run SnowOps Labs (see the notes above).")
	} else {
		fmt.Fprintln(out, "\n✓ Ready to run SnowOps Labs.")
	}
	return nil
}

// toolFindings splits the tool checks into blocking problems and notes about
// optional tools.
func toolFindings(results []toolchain.CheckResult) (problems, notes []string) {
	problems, notes = []string{}, []string{}
	for _, r := range results {
		switch {
		case r.Detail == "":
		case !r.OK():
			problems = append(problems, r.Detail)
		case r.Status != toolchain.CheckOK:
			notes = append(notes, r.Detail)
		}
	}
	return problems, notes
}

// printBullets prints a titled list, or nothing when items is empty.
func printBullets(out io.Writer, title, bullet string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(out, "\n%s\n", title)
	for _, item := range items {
		fmt.Fprintf(out, "  %s %s\n", bullet, item)
	}
}

// labDomain is the domain suffix of the lab's hostnames and whether browsers
// resolve it without hosts-file entries. Without a readable lab it reports the
// default, which does.
func labDomain(lab string, labErr error) (string, bool) {
	if labErr == nil {
		if labCfg, err := config.Load(lab); err == nil {
			return labCfg.DomainSuffix, labCfg.ResolvesLocally()
		}
	}
	return "snowops.localhost", true
}

// labVersionSuffix formats a lab's LAB_VERSION for the doctor report.
func labVersionSuffix(version string) string {
	if version == "" {
		return ""
	}
	return " (" + version + ")"
}

// workingDir is the current directory, or "" when it cannot be read; the
// caller then skips the notes that depend on it.
func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
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

// dockerReport is doctor's view of the Docker engine: a summary line, plus at
// most one blocking problem or one note, and the resources when it could read
// them.
type dockerReport struct {
	line    string
	problem string
	note    string
	res     capacity.Resources
}

// dockerCapacity checks the Docker engine against capacity.Floor. A missing
// docker is left to the tool checks, which already report it.
func dockerCapacity(ctx context.Context, runner toolchain.Runner, p capacity.Platform) dockerReport {
	host := capacity.DetectHost(ctx, runner, p)
	res, err := capacity.Probe(ctx, runner)
	switch {
	case errors.Is(err, capacity.ErrDockerMissing):
		return dockerReport{}
	case errors.Is(err, capacity.ErrNoPermission), errors.Is(err, capacity.ErrWSLIntegration):
		return dockerReport{line: "not usable", problem: fmt.Sprintf("%v. Fix:\n    %s", err, host.AccessHint(err))}
	case errors.Is(err, capacity.ErrDaemonDown) && host.Engine == capacity.EngineColima:
		return dockerReport{line: "not running", note: fmt.Sprintf(
			"colima is not running. 'labctl init' starts it for you at %s, or run:\n    %s",
			capacity.Floor, host.StartHint(capacity.Floor))}
	case errors.Is(err, capacity.ErrDaemonDown):
		return dockerReport{line: "not running", problem: fmt.Sprintf(
			"The Docker daemon is not running. Start it:\n    %s", host.StartHint(capacity.Floor))}
	case err != nil:
		return dockerReport{line: "unknown", note: fmt.Sprintf("Could not read Docker's CPU and memory: %v", err)}
	}
	if short := capacity.Shortfall(res, capacity.Floor); short != "" {
		return dockerReport{line: res.String(), res: res, problem: fmt.Sprintf(
			"%s\n    Below this the API server runs out of memory and every command fails with\n"+
				"    \"TLS handshake timeout\". Fix:\n    %s", short, host.ResizeHint(capacity.Floor))}
	}
	return dockerReport{line: fmt.Sprintf("%s - meets the minimum (%s). Heavier scenarios say what they need.",
		res, capacity.Floor), res: res}
}

// requestsWarnShare is the share of Docker's memory the pods' memory requests
// may reach before doctor warns. Past it, new pods soon stay Pending, or, on a
// cluster whose nodes each offer all of Docker's memory, the VM runs out.
const requestsWarnShare = 0.85

// memoryRequestsNote warns when the memory requests of the pods on the lab's
// local cluster come close to what the Docker engine has. It says nothing when
// the lab runs elsewhere or its cluster does not answer.
func memoryRequestsNote(ctx context.Context, runner toolchain.Runner, lab string, res capacity.Resources) string {
	cfg, err := config.Load(lab)
	if err != nil {
		return ""
	}
	if cfg.Profile != "k3d" && cfg.Profile != "kind" {
		return ""
	}
	requested, err := capacity.Requested(ctx, runner, cfg.Profile+"-"+cfg.ClusterName)
	if err != nil {
		return ""
	}
	engineMiB := int(res.MemBytes / (1 << 20))
	if float64(requested) < requestsWarnShare*float64(engineMiB) {
		return ""
	}
	return fmt.Sprintf("The lab's pods ask for %s GB of memory, %d%% of Docker's %s GB. New pods may\n"+
		"    not fit. Stop a scenario you are done with ('labctl scenario down <name>') first.",
		formatMiB(requested), requested*100/engineMiB, formatMiB(engineMiB))
}

// inotifyNote warns when a k3d lab's fs.inotify.max_user_instances is back at
// the Docker VM's default, as a VM restart leaves it. It only reports; the next
// command that runs scripts against the lab raises it.
func inotifyNote(ctx context.Context, runner toolchain.Runner, lab string) string {
	cfg, err := config.Load(lab)
	if err != nil || cfg.Profile != "k3d" {
		return ""
	}
	label, _ := capacity.NodeLabel(cfg.Profile, cfg.ClusterName)
	limit, err := capacity.InotifyLimit(ctx, runner, label)
	if err != nil || limit == 0 || limit >= capacity.InotifyInstances {
		return ""
	}
	return fmt.Sprintf("fs.inotify.max_user_instances is %d; the lab needs %d. A Docker VM restart\n"+
		"    resets it, and k3s on a node then stops with \"too many open files\". The next\n"+
		"    lab, platform, scenario, incident or traffic command raises it; 'labctl\n"+
		"    status' lists nodes that need a restart.", limit, capacity.InotifyInstances)
}

// formatMiB formats MiB as GB with one decimal, the way doctor shows Docker's
// memory.
func formatMiB(mib int) string {
	return strconv.FormatFloat(float64(mib)/1024, 'f', 1, 64)
}

func init() {
	rootCmd.AddCommand(doctorCmd())
}
