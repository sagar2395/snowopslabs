// SPDX-License-Identifier: Apache-2.0
// Package traffic validates options for the k6 traffic generator and turns
// them into the environment the services/traffic scripts consume. All the
// real work happens in those scripts (golden rule 2) — this package only
// guards inputs and discovers profiles.
package traffic

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Options are the user-tunable knobs for a traffic run.
type Options struct {
	Profile  string // profile name (a .js file under services/traffic/profiles/)
	Target   string // URL to load; empty keeps the script default (go-api in-cluster)
	RPS      int    // requests per second (baseline for spike)
	Duration string // k6/Go duration syntax (e.g. 10m, 1h30m); empty keeps profile default
}

// ScriptDir is the location of the traffic scripts relative to the project root.
const ScriptDir = "services/traffic"

// Profiles lists the available profile names (sorted) by scanning
// services/traffic/profiles/*.js under the project root.
func Profiles(projectRoot string) ([]string, error) {
	pattern := filepath.Join(projectRoot, ScriptDir, "profiles", "*.js")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, m := range matches {
		names = append(names, strings.TrimSuffix(filepath.Base(m), ".js"))
	}
	sort.Strings(names)
	return names, nil
}

// Validate rejects bad options before anything touches the cluster.
func (o Options) Validate(projectRoot string) error {
	var errs []string

	if o.Profile == "" {
		errs = append(errs, "profile is required")
	} else {
		profilePath := filepath.Join(projectRoot, ScriptDir, "profiles", o.Profile+".js")
		if _, err := os.Stat(profilePath); err != nil {
			available, _ := Profiles(projectRoot)
			errs = append(errs, fmt.Sprintf("unknown profile %q (available: %s)",
				o.Profile, strings.Join(available, ", ")))
		}
	}

	if o.RPS < 1 || o.RPS > 10000 {
		errs = append(errs, fmt.Sprintf("rps must be between 1 and 10000, got %d", o.RPS))
	}

	if o.Duration != "" {
		if d, err := time.ParseDuration(o.Duration); err != nil {
			errs = append(errs, fmt.Sprintf("invalid duration %q (use Go syntax: 30s, 10m, 1h30m)", o.Duration))
		} else if d <= 0 {
			errs = append(errs, "duration must be positive")
		}
	}

	if o.Target != "" && !strings.HasPrefix(o.Target, "http://") && !strings.HasPrefix(o.Target, "https://") {
		errs = append(errs, fmt.Sprintf("target must be an http(s) URL, got %q", o.Target))
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid traffic options: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Env returns the TRAFFIC_* variables for the start script. Unset knobs are
// omitted so the script's own defaults apply.
func (o Options) Env() map[string]string {
	env := map[string]string{
		"TRAFFIC_PROFILE": o.Profile,
		"TRAFFIC_RPS":     strconv.Itoa(o.RPS),
	}
	if o.Target != "" {
		env["TRAFFIC_TARGET"] = o.Target
	}
	if o.Duration != "" {
		env["TRAFFIC_DURATION"] = o.Duration
	}
	return env
}
