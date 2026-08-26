// SPDX-License-Identifier: Apache-2.0

package toolchain

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Requirement describes an external binary SnowOps Labs depends on.
//
// The point of this table is that a missing or outdated tool becomes a sentence
// the user can act on — "helm 3.9.0 found, 3.12+ required — brew upgrade helm" —
// instead of an opaque failure forty seconds into a cluster build.
type Requirement struct {
	// Binary is the executable name looked up on PATH.
	Binary string
	// MinVersion is the lowest acceptable version, "major.minor.patch".
	// Empty means any version will do.
	MinVersion string
	// VersionArgs produce a version string on stdout.
	VersionArgs []string
	// Why explains, in one clause, what breaks without it.
	Why string
	// Optional marks a tool that is only needed for some runtimes or features;
	// its absence is reported as a warning, not a failure.
	Optional bool
	// InstallHint is shown when the tool is missing or too old. Written per-OS
	// because "install helm" is not useful advice.
	InstallHint func(goos string) string
}

// Requirements returns the toolchain SnowOps Labs expects, in check order.
func Requirements() []Requirement {
	return []Requirement{
		{
			Binary:      "bash",
			MinVersion:  "3.2.0", // macOS still ships 3.2; scripts must work there
			VersionArgs: []string{"--version"},
			Why:         "every platform provider and scenario hook is a bash script",
			InstallHint: func(goos string) string {
				if goos == "darwin" {
					return "bash ships with macOS; if it is missing your PATH is broken"
				}
				return "install bash with your distribution's package manager"
			},
		},
		{
			Binary:      "kubectl",
			MinVersion:  "1.28.0",
			VersionArgs: []string{"version", "--client", "-o", "json"},
			Why:         "applying manifests and reading cluster state",
			InstallHint: func(goos string) string {
				if goos == "darwin" {
					return "brew install kubectl"
				}
				return "https://kubernetes.io/docs/tasks/tools/#kubectl"
			},
		},
		{
			Binary:      "helm",
			MinVersion:  "3.12.0",
			VersionArgs: []string{"version", "--short"},
			Why:         "installing platform components and scenario charts",
			InstallHint: func(goos string) string {
				if goos == "darwin" {
					return "brew install helm"
				}
				return "https://helm.sh/docs/intro/install/"
			},
		},
		{
			Binary:      "docker",
			VersionArgs: []string{"version", "--format", "{{.Client.Version}}"},
			Why:         "k3d and kind both run the cluster inside Docker",
			InstallHint: func(goos string) string {
				if goos == "darwin" {
					return "brew install colima docker && colima start"
				}
				return "https://docs.docker.com/engine/install/"
			},
		},
		{
			Binary:      "k3d",
			MinVersion:  "5.6.0",
			VersionArgs: []string{"version"},
			Why:         "creating the local cluster (the default runtime)",
			Optional:    true,
			InstallHint: func(goos string) string {
				if goos == "darwin" {
					return "brew install k3d — or use PROFILE=kind"
				}
				return "https://k3d.io/#installation — or use PROFILE=kind"
			},
		},
		{
			Binary:      "kind",
			MinVersion:  "0.20.0",
			VersionArgs: []string{"version"},
			Why:         "the headless runtime used by CI and the nightly e2e job",
			Optional:    true,
			InstallHint: func(goos string) string {
				if goos == "darwin" {
					return "brew install kind — or use PROFILE=k3d"
				}
				return "https://kind.sigs.k8s.io/docs/user/quick-start/#installation — or use PROFILE=k3d"
			},
		},
	}
}

// CheckStatus is the outcome of one preflight check.
type CheckStatus string

const (
	// CheckOK means present and new enough.
	CheckOK CheckStatus = "ok"
	// CheckMissing means the binary is not on PATH.
	CheckMissing CheckStatus = "missing"
	// CheckOutdated means present but below MinVersion.
	CheckOutdated CheckStatus = "outdated"
	// CheckUnknown means present, but the version could not be determined.
	// Not a failure: an unparseable version string is our problem, not the
	// user's, and blocking on it would be worse than proceeding.
	CheckUnknown CheckStatus = "unknown"
)

// CheckResult is one line of doctor output.
type CheckResult struct {
	Binary   string
	Status   CheckStatus
	Version  string
	Required string
	Path     string
	Optional bool
	// Detail is the actionable sentence shown to the user.
	Detail string
}

// OK reports whether this result should block work. An optional tool that is
// missing does not.
func (r CheckResult) OK() bool {
	switch r.Status {
	case CheckOK, CheckUnknown:
		return true
	default:
		return r.Optional
	}
}

// Preflight checks the toolchain.
type Preflight struct {
	Runner Runner
	// GOOS selects install hints; defaults to the running platform.
	GOOS string
	// Requirements defaults to Requirements().
	Requirements []Requirement
}

// NewPreflight builds a Preflight over the given runner.
func NewPreflight(r Runner) *Preflight {
	return &Preflight{Runner: r, GOOS: runtime.GOOS, Requirements: Requirements()}
}

// Check runs every requirement check and returns one result per binary. It
// never returns an error for a failing tool — the failure is the data.
func (p *Preflight) Check(ctx context.Context) ([]CheckResult, error) {
	reqs := p.Requirements
	if reqs == nil {
		reqs = Requirements()
	}
	goos := p.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}

	out := make([]CheckResult, 0, len(reqs))
	for _, req := range reqs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		out = append(out, p.checkOne(ctx, req, goos))
	}
	return out, nil
}

func (p *Preflight) checkOne(ctx context.Context, req Requirement, goos string) CheckResult {
	res := CheckResult{
		Binary:   req.Binary,
		Required: req.MinVersion,
		Optional: req.Optional,
	}

	path, err := p.Runner.LookPath(req.Binary)
	if err != nil {
		res.Status = CheckMissing
		res.Detail = fmt.Sprintf("%s is not on your PATH — needed for %s. %s",
			req.Binary, req.Why, hint(req, goos))
		return res
	}
	res.Path = path

	version, err := p.version(ctx, path, req)
	if err != nil || version == "" {
		res.Status = CheckUnknown
		res.Detail = fmt.Sprintf("%s is installed but did not report a version we could parse; continuing", req.Binary)
		return res
	}
	res.Version = version

	if req.MinVersion == "" {
		res.Status = CheckOK
		return res
	}

	cmp, err := compareVersions(version, req.MinVersion)
	if err != nil {
		res.Status = CheckUnknown
		res.Detail = fmt.Sprintf("%s reported version %q, which we could not compare against %s; continuing",
			req.Binary, version, req.MinVersion)
		return res
	}
	if cmp < 0 {
		res.Status = CheckOutdated
		res.Detail = fmt.Sprintf("%s %s found, %s+ required for %s. %s",
			req.Binary, version, req.MinVersion, req.Why, hint(req, goos))
		return res
	}

	res.Status = CheckOK
	return res
}

func (p *Preflight) version(ctx context.Context, path string, req Requirement) (string, error) {
	if len(req.VersionArgs) == 0 {
		return "", nil
	}
	var stdout bytes.Buffer
	_, err := p.Runner.Run(ctx, Command{
		Path:   path,
		Args:   req.VersionArgs,
		Stdout: &stdout,
		Stderr: &stdout, // some tools print their version to stderr
	})
	if err != nil {
		// A non-zero exit still often prints something usable, so parse
		// whatever we got before giving up.
		if v := parseVersion(stdout.String()); v != "" {
			return v, nil
		}
		return "", err
	}
	return parseVersion(stdout.String()), nil
}

func hint(req Requirement, goos string) string {
	if req.InstallHint == nil {
		return ""
	}
	return "Install: " + req.InstallHint(goos)
}

// versionRe finds the first dotted version in a tool's output. Every tool
// formats its version differently ("v5.8.3", "Client Version: v1.29.0",
// {"clientVersion":{"gitVersion":"v1.29.0"}}), and all of them contain a
// recognisable x.y.z somewhere.
var versionRe = regexp.MustCompile(`v?(\d+)\.(\d+)(?:\.(\d+))?`)

// parseVersion extracts the first version-looking token from s.
func parseVersion(s string) string {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	patch := m[3]
	if patch == "" {
		patch = "0"
	}
	return fmt.Sprintf("%s.%s.%s", m[1], m[2], patch)
}

// compareVersions returns -1, 0 or 1 comparing a to b as major.minor.patch.
func compareVersions(a, b string) (int, error) {
	av, err := splitVersion(a)
	if err != nil {
		return 0, err
	}
	bv, err := splitVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range 3 {
		switch {
		case av[i] < bv[i]:
			return -1, nil
		case av[i] > bv[i]:
			return 1, nil
		}
	}
	return 0, nil
}

func splitVersion(v string) ([3]int, error) {
	var out [3]int
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 4)
	if len(parts) < 2 {
		return out, fmt.Errorf("toolchain: %q is not a version", v)
	}
	for i := range 3 {
		if i >= len(parts) {
			break
		}
		// Trim any suffix: "3.2.57(1)-release", "1.29.0-rc1".
		digits := strings.TrimLeftFunc(parts[i], func(r rune) bool { return r < '0' || r > '9' })
		digits = strings.TrimRightFunc(digits, func(r rune) bool { return r < '0' || r > '9' })
		if digits == "" {
			return out, fmt.Errorf("toolchain: %q is not a version", v)
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return out, fmt.Errorf("toolchain: %q is not a version", v)
		}
		out[i] = n
	}
	return out, nil
}
