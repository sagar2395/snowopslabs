// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/k8s"
	"github.com/sagar2395/snowopslabs/internal/platform"
)

// appNamespace returns the namespace an app deploys into: its app.env NAMESPACE,
// or the app name by default (matching engine/deploy.sh).
func appNamespace(app string) string {
	if ac, err := config.LoadAppConfig(cfg.ProjectRoot, app); err == nil && ac.Namespace != "" {
		return ac.Namespace
	}
	return app
}

// appExists reports whether apps/<name>/app.env exists, i.e. the app is one this
// repo can build and deploy (as opposed to an arbitrary namespace/workload).
func appExists(app string) bool {
	_, err := config.LoadAppConfig(cfg.ProjectRoot, app)
	return err == nil
}

// ensureAppsDeployed makes sure each required app is actually running in the
// cluster before an operation that depends on it (a scenario or challenge).
//
// People don't always follow the happy path — they'll start a scenario before
// deploying its app. Rather than fail deep inside with a cryptic error, we check
// up front and either (autoDeploy) build+deploy the missing apps, or return one
// clear error naming the exact commands to run.
func ensureAppsDeployed(ctx context.Context, apps []string, autoDeploy bool) error {
	var missing, unknown []string
	for _, app := range apps {
		if !appExists(app) {
			// Not one of our apps (e.g. a synthetic namespace a fault creates).
			// We can't build/deploy it; surface it so the user knows.
			unknown = append(unknown, app)
			continue
		}
		st, _ := k8s.GetAppStatus(ctx, app, appNamespace(app))
		if st != nil && st.Deployed {
			continue
		}
		if autoDeploy {
			fmt.Printf("Prerequisite app %q is not deployed — building and deploying it...\n", app)
			if err := exec.RunScript("src/engine/build.sh", app); err != nil {
				return fmt.Errorf("building prerequisite app %s: %w", app, err)
			}
			if err := exec.RunScript("src/engine/deploy.sh", "deploy", app); err != nil {
				return fmt.Errorf("deploying prerequisite app %s: %w", app, err)
			}
			continue
		}
		missing = append(missing, app)
	}

	if len(missing) == 0 && len(unknown) == 0 {
		return nil
	}

	var b strings.Builder
	if len(missing) > 0 {
		fmt.Fprintf(&b, "required app(s) not deployed: %s\n", strings.Join(missing, ", "))
		b.WriteString("Deploy them first, or re-run with --deploy-prereqs to do it automatically:\n")
		for _, app := range missing {
			fmt.Fprintf(&b, "  labctl app build %s && labctl app deploy %s\n", app, app)
		}
	}
	if len(unknown) > 0 {
		fmt.Fprintf(&b, "required target(s) not present and not a repo app (deploy them yourself): %s\n",
			strings.Join(unknown, ", "))
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

// warnMissingPlatformPrereqs prints a non-fatal heads-up for each declared
// platform prerequisite (e.g. "cost/opencost", "monitoring/metrics", "ingress")
// that does not appear to be installed, naming the exact command to install it.
//
// Unlike app prerequisites, platform components are never auto-installed by
// `scenario up`, and a missing one (say OpenCost for a cost scenario) otherwise
// only surfaces as a cryptic check failure much later. This is intentionally a
// warning, not a hard error: detection is best-effort (namespace existence), so
// we must never wrongly block a correctly-provisioned cluster. Writes to w so
// callers/tests can capture it; a nil or empty prereq list is a no-op.
func warnMissingPlatformPrereqs(ctx context.Context, w io.Writer, prereqs []string) {
	if len(prereqs) == 0 {
		return
	}
	reg := platform.NewRegistry(cfg.ProjectRoot)

	var missing []string
	for _, pre := range prereqs {
		namespaces := prereqNamespaces(reg, pre)
		if len(namespaces) == 0 {
			// Can't resolve this prereq to a namespace — don't guess, don't warn.
			continue
		}
		present := false
		for _, ns := range namespaces {
			if k8s.NamespaceExists(ctx, ns) {
				present = true
				break
			}
		}
		if !present {
			missing = append(missing, pre)
		}
	}
	if len(missing) == 0 {
		return
	}

	fmt.Fprintf(w, "Warning: platform prerequisite(s) not detected: %s\n", strings.Join(missing, ", "))
	fmt.Fprintln(w, "Some checks will fail until they are installed. Install them with:")
	for _, pre := range missing {
		fmt.Fprintf(w, "  labctl platform up %s\n", pre)
	}
}

// prereqNamespaces resolves a platform-prerequisite string to the namespace(s)
// whose existence signals it is installed. A prereq is one of:
//   - a (sub)category with providers under it (e.g. "monitoring/metrics") → the
//     namespace each of its providers installs into;
//   - a category/provider pair (e.g. "cost/opencost") → that provider's namespace;
//   - a bare category with providers (e.g. "ingress") → each provider's namespace
//     (any one present counts, since ingress providers are mutually exclusive).
//
// Returns nil when the prereq cannot be resolved against the registry, so the
// caller can skip it rather than emit a misleading warning.
func prereqNamespaces(reg *platform.Registry, pre string) []string {
	if provs := reg.GetProviders(pre); len(provs) > 0 {
		return providerNamespaces(provs)
	}
	if i := strings.LastIndex(pre, "/"); i >= 0 {
		if p, err := reg.GetProvider(pre[:i], pre[i+1:]); err == nil {
			return []string{p.Namespace()}
		}
	}
	return nil
}

// providerNamespaces returns the deduplicated namespaces of the given providers.
func providerNamespaces(provs []platform.Provider) []string {
	seen := map[string]bool{}
	var out []string
	for i := range provs {
		ns := provs[i].Namespace()
		if ns != "" && !seen[ns] {
			seen[ns] = true
			out = append(out, ns)
		}
	}
	return out
}
