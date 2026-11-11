// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/k8s"
	"github.com/snowops-oss/snowopslabs/internal/platform"
	platsvc "github.com/snowops-oss/snowopslabs/internal/service/platform"
)

// requireCluster fails with what to do next when the cluster's API server
// does not answer. Commands that read the cluster to decide what to do call
// it first, so a cluster that is down is never reported as an app that is not
// deployed.
func requireCluster(ctx context.Context) error {
	return withClusterAdvice(k8s.RequireReachable(ctx))
}

// withClusterAdvice adds the commands that diagnose and restart the lab to an
// error that reports an unreachable cluster. Any other error is returned as
// it is.
func withClusterAdvice(err error) error {
	if !errors.Is(err, k8s.ErrUnreachable) {
		return err
	}
	return fmt.Errorf("%w\nCheck it with:          labctl status\nBring the lab back with: labctl init", err)
}

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

// ensureAppsDeployed checks that each app in apps is running before a
// scenario or challenge uses it. Missing apps are built and deployed when
// autoDeploy is true; otherwise it returns one error naming the commands to
// run.
func ensureAppsDeployed(ctx context.Context, apps []string, autoDeploy bool) error {
	var missing, unknown []string
	for _, app := range apps {
		if !appExists(app) {
			// Not a repo app, so it cannot be deployed from here; report it.
			unknown = append(unknown, app)
			continue
		}
		st, _ := k8s.GetAppStatus(ctx, app, appNamespace(app))
		if st != nil && st.Deployed {
			continue
		}
		if autoDeploy {
			fmt.Printf("Prerequisite app %q is not deployed - building and deploying it...\n", app)
			if err := scriptExec.RunScript("src/engine/build.sh", app); err != nil {
				return fmt.Errorf("building prerequisite app %s: %w", app, err)
			}
			if err := scriptExec.RunScript("src/engine/deploy.sh", "deploy", app); err != nil {
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

// ensurePlatformPrereqs checks that every platform prerequisite (such as
// "cost/opencost", "ingress" or "mesh") is installed. With autoInstall it
// installs the missing ones; otherwise it returns an error with the command
// that installs each. Prerequisites the registry does not know are skipped:
// scenario preflight already reports those.
func ensurePlatformPrereqs(ctx context.Context, out io.Writer, prereqs []string, autoInstall bool) error {
	missing := []string{}
	for _, pre := range prereqs {
		if installed, known := platformPrereqInstalled(ctx, pre); known && !installed {
			missing = append(missing, pre)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if autoInstall {
		return installPlatformPrereqs(ctx, out, missing)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "platform prerequisite(s) not installed: %s\nInstall them with:\n", strings.Join(missing, ", "))
	for _, pre := range missing {
		fmt.Fprintf(&b, "  labctl platform up %s\n", pre)
	}
	b.WriteString("or re-run with --deploy-prereqs to install them first")
	return errors.New(b.String())
}

// installPlatformPrereqs installs each prerequisite as a run through the
// platform service, as `platform up <target>` does, so it holds the
// component's lock and lands in the inventory that `platform teardown` reads.
func installPlatformPrereqs(ctx context.Context, out io.Writer, prereqs []string) error {
	svc, st, eng, cleanup, err := platformEngineFactory(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := startEngine(ctx, out, eng); err != nil {
		return err
	}
	for _, pre := range prereqs {
		category, provider, err := resolveTarget(pre)
		if err != nil {
			return fmt.Errorf("resolving platform prerequisite %s: %w", pre, err)
		}
		fmt.Fprintf(out, "Platform prerequisite %s is not installed - installing %s/%s...\n", pre, category, provider)
		err = streamSubmittedRun(ctx, out, st, "install "+platsvc.Component(category, provider), func() (string, error) {
			return svc.Install(ctx, category, provider)
		})
		if err != nil {
			return fmt.Errorf("installing platform prerequisite %s: %w", pre, err)
		}
	}
	return nil
}

// platformPrereqInstalled reports whether any provider a prerequisite names
// is installed; known is false when the registry does not know it.
func platformPrereqInstalled(ctx context.Context, pre string) (installed, known bool) {
	providers := prereqProviders(reg, pre)
	for i := range providers {
		if providerInstalled(ctx, &providers[i]) {
			return true, true
		}
	}
	return false, len(providers) > 0
}

// prereqProviders resolves a platform prerequisite to the providers that
// satisfy it:
//   - a (sub)category such as "ingress" or "monitoring/metrics" → any of its
//     providers;
//   - a category/provider pair such as "cost/opencost" → that provider.
//
// It returns nil when the registry does not know the prerequisite.
func prereqProviders(reg *platform.Registry, pre string) []platform.Provider {
	if provs := reg.GetProviders(pre); len(provs) > 0 {
		return provs
	}
	if i := strings.LastIndex(pre, "/"); i >= 0 {
		if p, err := reg.GetProvider(pre[:i], pre[i+1:]); err == nil {
			return []platform.Provider{*p}
		}
	}
	return nil
}

// providerInstalled reports whether a platform provider is installed: by its
// Helm release when it shares a namespace with other components, otherwise by
// its namespace.
func providerInstalled(ctx context.Context, p *platform.Provider) bool {
	if p.SharesNamespace() {
		return k8s.HelmReleaseExists(ctx, p.Namespace(), p.Name)
	}
	return k8s.NamespaceExists(ctx, p.Namespace())
}
