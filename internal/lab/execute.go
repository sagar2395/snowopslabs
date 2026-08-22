// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/internal/platform"
	"github.com/sagar2395/snowopslabs/internal/scenario"
)

// Deps are the engines a plan executes through. The lab package never talks
// to the cluster itself — every action goes through the existing idempotent
// install/uninstall paths (golden rules 2 and 5).
type Deps struct {
	Exec     *executor.Executor
	Registry *platform.Registry
	Scenes   *scenario.Engine
	// ContinueOnError collects failures instead of stopping (used by reset:
	// tear down as much as possible, then report what stuck).
	ContinueOnError bool
	// Progress, if set, is called before each action (e.g. "[2/5] app-deploy go-api").
	Progress func(i, total int, a Action)
}

// splitComponent separates "category/provider" at the last slash (categories
// may nest, e.g. monitoring/metrics/prometheus).
func splitComponent(target string) (category, name string, err error) {
	i := strings.LastIndex(target, "/")
	if i <= 0 || i == len(target)-1 {
		return "", "", fmt.Errorf("invalid platform component %q (want category/provider)", target)
	}
	return target[:i], target[i+1:], nil
}

// Execute runs a restore or reset plan in order.
func Execute(plan []Action, d Deps) error {
	var failures []string
	for i, a := range plan {
		if d.Progress != nil {
			d.Progress(i+1, len(plan), a)
		}
		if err := executeOne(a, d); err != nil {
			if !d.ContinueOnError {
				return fmt.Errorf("%s: %w", a, err)
			}
			failures = append(failures, fmt.Sprintf("%s: %v", a, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d of %d actions failed:\n  - %s",
			len(failures), len(plan), strings.Join(failures, "\n  - "))
	}
	return nil
}

func executeOne(a Action, d Deps) error {
	switch a.Kind {
	case "platform-install":
		category, name, err := splitComponent(a.Target)
		if err != nil {
			return err
		}
		return d.Registry.InstallStreamed(category, name, d.Exec)
	case "platform-uninstall":
		category, name, err := splitComponent(a.Target)
		if err != nil {
			return err
		}
		return d.Registry.UninstallStreamed(category, name, d.Exec)
	case "app-deploy":
		return d.Exec.RunScript("engine/deploy.sh", "deploy", a.Target)
	case "app-destroy":
		return d.Exec.RunScript("engine/deploy.sh", "destroy", a.Target)
	case "scenario-up":
		err := d.Scenes.Up(a.Target, d.Exec)
		if errors.Is(err, scenario.ErrAlreadyActive) {
			return nil // idempotent restore over a converged lab
		}
		return err
	case "scenario-down":
		return d.Scenes.Down(a.Target, d.Exec)
	case "traffic-stop":
		_, err := d.Exec.RunScriptStreamed("Stop traffic", "services/traffic/stop.sh")
		return err
	default:
		return fmt.Errorf("unknown action kind %q", a.Kind)
	}
}
