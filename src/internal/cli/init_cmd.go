// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/k8s"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

// Timeouts for init's closing checks. A restarted cluster brings every pod up
// at once, so the platform gets several minutes to settle.
const (
	podsReadyTimeout  = 6 * time.Minute
	podsReadyInterval = 5 * time.Second
	nodesReadyTimeout = 150 * time.Second
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the lab (setup tools + create cluster + install platform)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, out := cmd.Context(), cmd.OutOrStdout()

		fmt.Fprintln(out, "=== Setting up tools ===")
		if err := scriptExec.RunScript("bootstrap/setup-tools.sh", cfg.Profile); err != nil {
			return fmt.Errorf("setup-tools failed: %w", err)
		}

		if cfg.Profile != "incluster" {
			platform := capacity.Platform{GOOS: runtime.GOOS, WSL: isWSL()}
			if err := preflightDocker(ctx, out, toolchain.NewExec(), platform); err != nil {
				return err
			}
		}

		fmt.Fprintln(out, "\n=== Creating runtime ===")
		if err := scriptExec.RunScript(fmt.Sprintf("runtimes/%s/up.sh", cfg.Profile), cfg.ClusterName); err != nil {
			return fmt.Errorf("bringing up the cluster failed: %w", err)
		}
		reloadIngress()

		// An existing lab has nothing to install, and helm upgrades would race
		// pods that are still restarting, so it only waits for them instead.
		if namespaces, ok := baselineDeployed(ctx, baselineComponents()); ok {
			fmt.Fprintln(out, "\n=== Platform already installed - waiting for it to be ready ===")
			if err := waitPodsReady(ctx, out, namespaces); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(out, "\n=== Installing platform ===")
			if err := platformUpRun(cmd, args); err != nil {
				return err
			}
		}
		if err := checkClusterHealthy(ctx, out); err != nil {
			return err
		}
		if err := moveLegacyDomain(out); err != nil {
			return err
		}
		if err := ensureIngressReachable(ctx, out); err != nil {
			return err
		}
		if err := ensureGrafanaQueriesPrometheus(ctx, out); err != nil {
			return err
		}
		printPostInitHints(out)
		return nil
	},
}

// preflightDocker fails when the Docker engine cannot be reached or is below
// capacity.Floor, with the command that fixes it on the user's setup. It never
// changes the engine itself.
func preflightDocker(ctx context.Context, out io.Writer, runner toolchain.Runner, p capacity.Platform) error {
	fmt.Fprintln(out, "\n=== Checking Docker resources ===")
	host := capacity.DetectHost(ctx, runner, p)
	res, err := capacity.Probe(ctx, runner)
	switch {
	case errors.Is(err, capacity.ErrNoPermission), errors.Is(err, capacity.ErrWSLIntegration):
		return fmt.Errorf("%w.\nFix it, then re-run 'labctl init':\n  %s", err, host.AccessHint(err))
	case errors.Is(err, capacity.ErrDockerMissing), errors.Is(err, capacity.ErrDaemonDown):
		return fmt.Errorf("%w.\nStart it, then re-run 'labctl init':\n  %s", err, host.StartHint(capacity.Floor))
	case err != nil:
		return fmt.Errorf("reading Docker's CPU and memory: %w", err)
	}
	if short := capacity.Shortfall(res, capacity.Floor); short != "" {
		return fmt.Errorf("docker is too small for the lab. %s\n"+
			"labctl does not resize a running Docker engine, because that would stop your other containers.\n"+
			"Resize it, then re-run 'labctl init':\n  %s", short, host.ResizeHint(capacity.Floor))
	}
	fmt.Fprintf(out, "Docker has %s (minimum %s).\n", res, capacity.Floor)
	return nil
}

// platformComponent names a platform provider by category and provider.
type platformComponent struct {
	category string
	provider string
}

// baselineComponents is the platform `init` installs: Grafana, plus the
// configured ingress and metrics providers.
func baselineComponents() []platformComponent {
	components := []platformComponent{{category: "monitoring", provider: "grafana"}}
	if cfg.IngressProvider != "" {
		components = append(components, platformComponent{category: "ingress", provider: cfg.IngressProvider})
	}
	if cfg.MetricsProvider != "" {
		components = append(components, platformComponent{category: "monitoring/metrics", provider: cfg.MetricsProvider})
	}
	return components
}

// baselineDeployed reports whether every component has a deployed Helm
// release, and returns the namespaces they run in.
func baselineDeployed(ctx context.Context, components []platformComponent) ([]string, bool) {
	namespaces := make([]string, 0, len(components))
	for _, c := range components {
		p, err := reg.GetProvider(c.category, c.provider)
		if err != nil || !k8s.HelmReleaseDeployed(ctx, p.Namespace(), p.Name) {
			return nil, false
		}
		if !slices.Contains(namespaces, p.Namespace()) {
			namespaces = append(namespaces, p.Namespace())
		}
	}
	return namespaces, true
}

// waitPodsReady polls until every pod in namespaces is Ready. It re-reads the
// pod list on each poll because pods are replaced while a cluster settles.
func waitPodsReady(ctx context.Context, out io.Writer, namespaces []string) error {
	ctx, cancel := context.WithTimeout(ctx, podsReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(podsReadyInterval)
	defer ticker.Stop()
	for _, ns := range namespaces {
		for {
			ready, total, _ := k8s.NamespaceHealth(ctx, ns)
			if total > 0 && ready == total {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("only %d of %d platform pods in %q are Ready: %w\n"+
					"See which with 'kubectl get pods -n %s'. "+
					"Pending or OOMKilled pods mean Docker is short of memory ('labctl doctor')",
					ready, total, ns, ctx.Err(), ns)
			case <-ticker.C:
			}
		}
	}
	fmt.Fprintln(out, "Platform pods are Ready.")
	return nil
}

// checkClusterHealthy confirms the API answers and every node is Ready, so
// `init` only reports success for a lab that works.
func checkClusterHealthy(ctx context.Context, out io.Writer) error {
	fmt.Fprintln(out, "\n=== Checking the lab ===")
	if err := k8s.Reachable(ctx); err != nil {
		return fmt.Errorf("the cluster is not answering: %w\n"+
			"Docker running out of memory is the usual cause. "+
			"Check with 'labctl doctor', then re-run 'labctl init'", err)
	}
	ctx, cancel := context.WithTimeout(ctx, nodesReadyTimeout+30*time.Second)
	defer cancel()
	timeout := fmt.Sprintf("--timeout=%s", nodesReadyTimeout)
	if _, err := k8s.RunKubectl(ctx, "wait", "--for=condition=Ready", "node", "--all", timeout); err != nil {
		return fmt.Errorf("not every node became Ready: %w\nSee which with 'kubectl get nodes', then re-run 'labctl init'", err)
	}
	fmt.Fprintln(out, "API server ready; all nodes Ready.")
	return nil
}

// printPostInitHints prints the lab's URLs and next steps. It reloads the
// ports and domain suffix first, because the runtime records the ones the
// cluster actually uses.
func printPostInitHints(out io.Writer) {
	if fresh, err := config.Load(cfg.ProjectRoot); err == nil {
		cfg.HTTPPort, cfg.HTTPSPort, cfg.DomainSuffix = fresh.HTTPPort, fresh.HTTPSPort, fresh.DomainSuffix
	}
	fmt.Fprintln(out, "\n=== Lab is up ===")
	fmt.Fprintf(out, "\n  Grafana:     %s   (admin / admin)\n", cfg.IngressURL("grafana"))
	fmt.Fprintf(out, "  Prometheus:  %s\n", cfg.IngressURL("prometheus"))
	fmt.Fprintln(out, "  labctl UI:   run 'labctl ui', then open http://localhost:3939")
	if cfg.HTTPPort != "" && cfg.HTTPPort != "80" {
		fmt.Fprintf(out, "\nPort 80 was taken, so the lab's ingress listens on %s; every URL and check uses it.\n", cfg.HTTPPort)
	}
	if !cfg.ResolvesLocally() && !hostsBlockPresent() {
		fmt.Fprintln(out, "\nTo open those URLs in your browser, add the lab hostnames to /etc/hosts")
		fmt.Fprintln(out, "(one-time, needs sudo):  labctl hosts add")
	}
	fmt.Fprintln(out, "\nNext: deploy an app, then run a scenario:")
	fmt.Fprintln(out, "  labctl app build go-api && labctl app deploy go-api")
	fmt.Fprintln(out, "  labctl scenario up observability-sre   (add --deploy-prereqs to auto-deploy its apps)")
	fmt.Fprintln(out, "\nAfter a reboot, 'labctl init' brings the lab back.")
}

var teardownCmd = &cobra.Command{
	Use:   "teardown",
	Short: "Tear down the lab (deactivate scenarios/incidents + destroy apps + platform + cluster)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Deactivate scenarios and incidents before removing the platform and
		// apps they depend on.
		fmt.Println("=== Deactivating scenarios and incidents ===")
		if cleared := scenes.DeactivateAll(); len(cleared) > 0 {
			fmt.Printf("Deactivated scenarios: %s\n", strings.Join(cleared, ", "))
		}
		if incEng != nil {
			if fault := incEng.ClearActiveState(); fault != "" {
				fmt.Printf("Deactivated incident: %s\n", fault)
			}
		}

		fmt.Println("\n=== Destroying all apps ===")
		apps, _ := config.ListApps(cfg.ProjectRoot)
		for _, app := range apps {
			fmt.Printf("Destroying %s...\n", app)
			_ = scriptExec.RunScript("src/engine/deploy.sh", "destroy", app)
		}

		// A failed uninstall does not stop the teardown: destroying the
		// cluster next removes whatever is left.
		fmt.Println("\n=== Removing platform ===")
		if err := platformDownRun(cmd, args); err != nil {
			fmt.Printf("Warning: %v\n", err)
		}

		fmt.Println("\n=== Destroying runtime ===")
		return scriptExec.RunScript(fmt.Sprintf("runtimes/%s/down.sh", cfg.Profile))
	},
}

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset the lab (teardown + init)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := teardownCmd.RunE(cmd, args); err != nil {
			fmt.Printf("Warning during teardown: %v\n", err)
		}
		return initCmd.RunE(cmd, args)
	},
}

var setupToolsCmd = &cobra.Command{
	Use:   "setup-tools",
	Short: "Install the required CLI tools (kubectl, helm, k3d/kind, container runtime)",
	Long: `Install the tools SnowOps Labs needs, version-pinned from versions.env, for the
active PROFILE. This is the same step 'labctl init' runs first; use it on its own
to prepare a machine without creating a cluster. Equivalent to 'make setup-tools'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return scriptExec.RunScript("bootstrap/setup-tools.sh", cfg.Profile)
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(teardownCmd)
	rootCmd.AddCommand(resetCmd)
	rootCmd.AddCommand(setupToolsCmd)
}
