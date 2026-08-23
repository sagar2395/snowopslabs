// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/k8s"
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
			if err := exec.RunScript("engine/build.sh", app); err != nil {
				return fmt.Errorf("building prerequisite app %s: %w", app, err)
			}
			if err := exec.RunScript("engine/deploy.sh", "deploy", app); err != nil {
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
