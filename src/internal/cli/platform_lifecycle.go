// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/run"
	platsvc "github.com/sagar2395/snowopslabs/internal/service/platform"
	"github.com/sagar2395/snowopslabs/internal/store"
)

var platformTeardownCmd = &cobra.Command{
	Use:   "teardown",
	Short: "Uninstall exactly the platform components the inventory records as installed",
	Long: `Removes every component labctl recorded as installed, in reverse order,
and reports anything it could not remove — instead of exiting 0 and hoping. Each
uninstall is a recorded, cancellable run. Components installed outside labctl are
not in the inventory and are left untouched.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return platformTeardown(cmd)
	},
}

// Single-target `labctl platform up|down` and the default `platform status`
// run through the durable engine (W3-T08): a per-component install/uninstall is
// a recorded, cancellable run under an exclusive `platform:<category>/<provider>`
// lock, and status is answered from the store. Bulk `up`/`down` (no target) and
// `status --live` stay on the legacy executor path; wholesale orchestration and
// live cluster probing across every discovered provider is not part of this
// slice.

// platformEngineFactory builds a platform service over the shared run-engine
// bootstrap. Overridable in tests. cleanup shuts the engine down (no-op if never
// started) and closes the store; always defer it.
var platformEngineFactory = func(ctx context.Context) (*platsvc.Service, *store.Store, *run.Engine, func(), error) {
	eng, st, cleanup, err := newRunEngine(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	svc, err := platsvc.New(eng, st, platsvc.WithEnv(scriptEnv()))
	if err != nil {
		cleanup()
		return nil, nil, nil, nil, err
	}
	return svc, st, eng, cleanup, nil
}

// runPlatformComponentOp routes a single-target install/uninstall through the
// durable run engine, streaming the recorded run and exiting non-zero on failure.
func runPlatformComponentOp(cmd *cobra.Command, verb, category, provider string, submit func(context.Context, *platsvc.Service) (string, error)) error {
	ctx := cmd.Context()
	svc, st, eng, cleanup, err := platformEngineFactory(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	return runEngineOperation(ctx, cmd.OutOrStdout(), eng, st, verb+" "+platsvc.Component(category, provider), func() (string, error) {
		return submit(ctx, svc)
	})
}

// platformStatusFromStore prints each component's state derived from the run
// history — fast, no cluster round-trip. With a target it shows that one
// component; without, every component labctl has installed or uninstalled.
func platformStatusFromStore(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	svc, st, _, cleanup, err := platformEngineFactory(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	out := cmd.OutOrStdout()

	var targets [][2]string
	if len(args) == 1 {
		category, provider, rerr := resolveTarget(args[0])
		if rerr != nil {
			return rerr
		}
		targets = append(targets, [2]string{category, provider})
	} else {
		targets, err = platformComponentsWithHistory(ctx, st)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			fmt.Fprintln(out, "No platform components have been installed through labctl yet.")
			fmt.Fprintln(out, "Install one with: labctl platform up <category|category/provider>")
			fmt.Fprintln(out, "Probe the live cluster with: labctl platform status --live")
			return nil
		}
	}

	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "COMPONENT\tSTATE\tLAST RUN")
	for _, t := range targets {
		s, serr := svc.Status(ctx, t[0], t[1])
		if serr != nil {
			return serr
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Component, s.State, dash(s.RunID))
	}
	return w.Flush()
}

// platformComponentsWithHistory returns the distinct components that have an
// install or uninstall run recorded, so `platform status` shows what labctl has
// actually touched rather than every provider on disk.
func platformComponentsWithHistory(ctx context.Context, st *store.Store) ([][2]string, error) {
	seen := map[string]bool{}
	var out [][2]string
	for _, kind := range []string{platsvc.KindInstall, platsvc.KindUninstall} {
		runs, err := st.ListRuns(ctx, store.RunFilter{Kind: kind, Limit: 500})
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			if r.Target == "" || seen[r.Target] {
				continue
			}
			seen[r.Target] = true
			cat, prov := splitComponent(r.Target)
			out = append(out, [2]string{cat, prov})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return platsvc.Component(out[i][0], out[i][1]) < platsvc.Component(out[j][0], out[j][1])
	})
	return out, nil
}

// splitComponent turns a run target ("monitoring/metrics/prometheus") back into
// its category ("monitoring/metrics") and provider ("prometheus").
func splitComponent(target string) (category, provider string) {
	i := strings.LastIndex(target, "/")
	if i < 0 {
		return "", target
	}
	return target[:i], target[i+1:]
}

// platformTeardown uninstalls exactly the components the inventory records as
// installed, and reports what it could not remove instead of exiting 0 silently
// (W3-T04). Each uninstall is a recorded run; a failure is collected and the
// teardown carries on, so one stuck component does not strand the rest.
func platformTeardown(cmd *cobra.Command) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	svc, st, eng, cleanup, err := platformEngineFactory(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	installed, err := st.ListComponents(ctx, store.ComponentFilter{
		Status: store.ComponentInstalled, Kind: "platform",
	})
	if err != nil {
		return err
	}
	if len(installed) == 0 {
		fmt.Fprintln(out, "Nothing to tear down — the inventory records no installed platform components.")
		return nil
	}

	if err := eng.Start(ctx); err != nil {
		return err
	}

	var removed, failed []string
	// Reverse order: later installs (e.g. dashboards that depend on monitoring)
	// come down before what they sit on.
	for i := len(installed) - 1; i >= 0; i-- {
		comp := installed[i]
		category, provider := splitComponent(comp.Ref)
		fmt.Fprintf(out, "\n── uninstalling %s ──\n", comp.Ref)
		if uerr := streamOneUninstall(ctx, out, svc, st, category, provider); uerr != nil {
			failed = append(failed, comp.Ref+": "+uerr.Error())
			fmt.Fprintf(out, "could not remove %s: %v\n", comp.Ref, uerr)
			continue
		}
		removed = append(removed, comp.Ref)
	}

	fmt.Fprintf(out, "\nTeardown complete. Removed %d of %d recorded components.\n", len(removed), len(installed))
	if len(failed) > 0 {
		fmt.Fprintln(out, "Could not remove:")
		for _, f := range failed {
			fmt.Fprintf(out, "  - %s\n", f)
		}
		return fmt.Errorf("%d component(s) could not be removed", len(failed))
	}
	return nil
}

// streamOneUninstall submits one uninstall and streams it, returning an error if
// the run did not succeed. The teardown owns the (already-started) engine for the
// whole batch, so this streams without starting it again.
func streamOneUninstall(ctx context.Context, out io.Writer, svc *platsvc.Service, st *store.Store, category, provider string) error {
	return streamSubmittedRun(ctx, out, st, "uninstall "+platsvc.Component(category, provider), func() (string, error) {
		return svc.Uninstall(ctx, category, provider)
	})
}
