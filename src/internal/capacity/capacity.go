// SPDX-License-Identifier: Apache-2.0

// Package capacity reads how much CPU and memory the Docker engine has,
// compares it with what the lab needs, and words the fix for the user's setup.
// doctor, init's preflight and the activation gates share it so their numbers
// and advice agree.
package capacity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

// Floor is the smallest Docker engine the lab supports: the cluster plus the
// baseline platform (ingress, Prometheus, Grafana). Below it the API server
// runs out of memory.
var Floor = Need{CPUs: 2, MemGiB: 4}

// memTolerance lets a VM sized at N GiB meet an N GiB requirement, because the
// guest kernel keeps part of it (an 8 GB colima VM reports about 7.7 GiB).
const memTolerance = 0.95

const gib = 1 << 30

// Sentinel errors from Probe. Their text is shown to users as it is, so it
// reads as a sentence rather than carrying a package prefix.
var (
	// ErrDockerMissing means the docker CLI is not on PATH.
	ErrDockerMissing = errors.New("docker is not installed")
	// ErrDaemonDown means the CLI is present but the engine does not answer.
	ErrDaemonDown = errors.New("the Docker daemon is not running")
	// ErrNoPermission means the daemon refused this user: they are not in the
	// docker group, or their login session started before they joined it.
	ErrNoPermission = errors.New("the Docker daemon refused this user (permission denied on its socket)")
	// ErrWSLIntegration means Docker Desktop's WSL shim is present but
	// integration is off for this distro.
	ErrWSLIntegration = errors.New("this WSL distro has Docker Desktop integration turned off")
)

// probeError reports one of the sentinels above while keeping the underlying
// cause in the chain for errors.Is and debugging.
type probeError struct {
	kind  error
	cause error
}

func (e *probeError) Error() string   { return e.kind.Error() }
func (e *probeError) Unwrap() []error { return []error{e.kind, e.cause} }

// Need is a CPU and memory requirement for the Docker engine.
type Need struct {
	CPUs   int
	MemGiB float64
}

// String formats the need as "2 CPU / 4 GB".
func (n Need) String() string {
	return fmt.Sprintf("%d CPU / %s GB", n.CPUs, formatGiB(n.MemGiB))
}

// Resources is what the Docker engine reports it has.
type Resources struct {
	CPUs     int
	MemBytes int64
}

// MemGiB is the engine's memory in GiB.
func (r Resources) MemGiB() float64 { return float64(r.MemBytes) / gib }

// String formats the resources as "4 CPU / 7.7 GB".
func (r Resources) String() string {
	return fmt.Sprintf("%d CPU / %s GB", r.CPUs, formatGiB(r.MemGiB()))
}

// Meets reports whether the engine satisfies n, allowing for the memory a VM's
// own kernel keeps.
func (r Resources) Meets(n Need) bool {
	return r.CPUs >= n.CPUs && r.MemGiB() >= n.MemGiB*memTolerance
}

// Probe asks the Docker engine for its CPU count and memory. When the engine
// cannot be asked, the error matches ErrDockerMissing, ErrDaemonDown,
// ErrNoPermission or ErrWSLIntegration.
func Probe(ctx context.Context, runner toolchain.Runner) (Resources, error) {
	path, err := runner.LookPath("docker")
	if err != nil {
		return Resources{}, &probeError{kind: ErrDockerMissing, cause: err}
	}
	// A hung daemon must not hang the caller.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	_, err = runner.Run(ctx, toolchain.Command{
		Path:   path,
		Args:   []string{"info", "--format", "{{.NCPU}} {{.MemTotal}}"},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return Resources{}, &probeError{kind: classifyInfoFailure(stderr.String() + stdout.String()), cause: err}
	}
	return parseInfo(stdout.String())
}

// classifyInfoFailure maps the output of a failed `docker info` to the
// sentinel that names its cause.
func classifyInfoFailure(output string) error {
	switch {
	case strings.Contains(output, "could not be found in this WSL 2 distro"):
		return ErrWSLIntegration
	case strings.Contains(output, "permission denied"):
		return ErrNoPermission
	default:
		return ErrDaemonDown
	}
}

// parseInfo reads the "<NCPU> <MemTotal>" line Probe asks `docker info` for.
func parseInfo(out string) (Resources, error) {
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return Resources{}, fmt.Errorf("unexpected `docker info` output %q", out)
	}
	ncpu, err := strconv.Atoi(fields[0])
	if err != nil {
		return Resources{}, fmt.Errorf("parsing CPU count from `docker info`: %w", err)
	}
	mem, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return Resources{}, fmt.Errorf("parsing memory from `docker info`: %w", err)
	}
	if ncpu <= 0 || mem <= 0 {
		return Resources{}, fmt.Errorf("`docker info` reported %d CPUs and %d bytes of memory", ncpu, mem)
	}
	return Resources{CPUs: ncpu, MemBytes: mem}, nil
}

// Engine is where Docker runs, which decides how it is started and resized.
type Engine int

const (
	// EngineUnknown is the zero value: no engine has been detected.
	EngineUnknown Engine = iota
	// EngineNative is Docker Engine running directly on a Linux host.
	EngineNative
	// EngineColima is the colima VM on macOS.
	EngineColima
	// EngineDockerDesktop is Docker Desktop on macOS.
	EngineDockerDesktop
	// EngineWSL is any Docker inside WSL2; its memory comes from .wslconfig.
	EngineWSL
)

// Platform is the operating system labctl runs on. Callers pass it in so tests
// can exercise every platform from one machine.
type Platform struct {
	GOOS string
	WSL  bool
}

// Host describes the machine for the purpose of printing a fix.
type Host struct {
	Engine Engine
}

// DetectHost works out which Docker engine the user has.
func DetectHost(ctx context.Context, runner toolchain.Runner, p Platform) Host {
	switch {
	case p.WSL:
		return Host{Engine: EngineWSL}
	case p.GOOS != "darwin":
		return Host{Engine: EngineNative}
	}
	if engine, ok := engineFromContext(ctx, runner); ok {
		return Host{Engine: engine}
	}
	// A colima that has never been started has not registered its docker
	// context yet, so an installed colima is the better guess.
	if _, err := runner.LookPath("colima"); err == nil {
		return Host{Engine: EngineColima}
	}
	return Host{Engine: EngineDockerDesktop}
}

// engineFromContext reads the selected docker context. colima's context stays
// selected while its VM is stopped, so this also recognises a stopped colima.
func engineFromContext(ctx context.Context, runner toolchain.Runner) (Engine, bool) {
	path, err := runner.LookPath("docker")
	if err != nil {
		return EngineUnknown, false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	if _, err := runner.Run(ctx, toolchain.Command{Path: path, Args: []string{"context", "show"}, Stdout: &out}); err != nil {
		return EngineUnknown, false
	}
	switch name := strings.TrimSpace(out.String()); {
	case strings.HasPrefix(name, "colima"):
		return EngineColima, true
	case strings.HasPrefix(name, "desktop"):
		return EngineDockerDesktop, true
	default:
		return EngineUnknown, false
	}
}

// StartHint is the command that starts a stopped engine at size n.
func (h Host) StartHint(n Need) string {
	switch h.Engine {
	case EngineColima:
		return fmt.Sprintf("colima start --cpu %d --memory %s", n.CPUs, formatGiB(n.MemGiB))
	case EngineDockerDesktop:
		return "open -a Docker   (then wait for Docker Desktop to report it is running)"
	case EngineWSL:
		return "sudo service docker start   " +
			"(or, with Docker Desktop, start it on Windows and enable WSL Integration for this distro)"
	default:
		return "sudo systemctl start docker"
	}
}

// AccessHint is the fix for ErrNoPermission and ErrWSLIntegration.
func (h Host) AccessHint(err error) string {
	if errors.Is(err, ErrWSLIntegration) {
		return "Docker Desktop → Settings → Resources → WSL Integration → enable this distro,\n" +
			"Apply & Restart, then reopen this terminal"
	}
	relogin := "log out and back in (or run `newgrp docker`)"
	if h.Engine == EngineWSL {
		relogin = "run `wsl --shutdown` in PowerShell and reopen this terminal"
	}
	return "sudo usermod -aG docker \"$USER\", then " + relogin
}

// ResizeHint explains how to give the engine at least n.
func (h Host) ResizeHint(n Need) string {
	mem := formatGiB(n.MemGiB)
	switch h.Engine {
	case EngineColima:
		return fmt.Sprintf("colima stop && colima start --cpu %d --memory %s", n.CPUs, mem)
	case EngineDockerDesktop:
		return fmt.Sprintf("Docker Desktop → Settings → Resources → CPUs %d, Memory %s GB → Apply & Restart", n.CPUs, mem)
	case EngineWSL:
		return fmt.Sprintf("in %%UserProfile%%\\.wslconfig on Windows set [wsl2] processors=%d and memory=%sGB,\n"+
			"then run `wsl --shutdown` in PowerShell and reopen this terminal", n.CPUs, mem)
	default:
		return fmt.Sprintf("Docker uses this machine's own CPU and memory: it needs %d CPUs and %s GB free -\n"+
			"close other memory-hungry programs, or use a bigger machine", n.CPUs, mem)
	}
}

// Shortfall is the one-line statement of what is missing, or "" when r meets n.
func Shortfall(r Resources, n Need) string {
	if r.Meets(n) {
		return ""
	}
	return fmt.Sprintf("Docker has %s; this needs at least %s.", r, n)
}

// formatGiB prints "4" for whole numbers and "7.7" otherwise.
func formatGiB(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}
