// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

// InotifyInstances is the fs.inotify.max_user_instances value the lab needs.
// runtimes/k3d/up.sh sets it when it creates the cluster. The Docker VM's
// default (128 on colima) runs out once log shippers and containerd's CNI
// watcher start, and a VM restart puts the default back.
const InotifyInstances = 512

// StuckNode is a node the API server reports NotReady while its container
// keeps running, so Docker shows nothing wrong.
type StuckNode struct {
	Name string
	// TooManyFiles means the container's recent log holds "too many open
	// files": k3s ran out of inotify instances.
	TooManyFiles bool
}

// InotifyLimit reads fs.inotify.max_user_instances on the machine that runs
// the lab's containers, through the first running container carrying label.
// The nodes share that machine's kernel. It returns 0 when no container runs.
func InotifyLimit(ctx context.Context, runner toolchain.Runner, label string) (int, error) {
	docker, containers, err := runningContainers(ctx, runner, label)
	if err != nil || len(containers) == 0 {
		return 0, err
	}
	return readInotifyLimit(ctx, runner, docker, containers[0])
}

// RaiseInotifyLimit sets fs.inotify.max_user_instances to InotifyInstances
// through every running container carrying label when it reads lower, as
// raise_inotify_limits in runtimes/k3d/up.sh does at create time. It returns
// the limit it read, or 0 when no container runs, and an error when no
// container could raise it.
func RaiseInotifyLimit(ctx context.Context, runner toolchain.Runner, label string) (int, error) {
	docker, containers, err := runningContainers(ctx, runner, label)
	if err != nil || len(containers) == 0 {
		return 0, err
	}
	limit, err := readInotifyLimit(ctx, runner, docker, containers[0])
	if err != nil || limit >= InotifyInstances {
		return limit, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Like up.sh, try every container: the load balancer carries the label
	// too but cannot write the sysctl, and one node that can is enough.
	var lastErr error
	raised := false
	for _, c := range containers {
		if _, err := runner.Run(ctx, toolchain.Command{
			Path: docker,
			Args: []string{"exec", c, "sysctl", "-w", "fs.inotify.max_user_instances=" + strconv.Itoa(InotifyInstances)},
		}); err != nil {
			lastErr = err
			continue
		}
		raised = true
	}
	if !raised {
		return limit, fmt.Errorf("raising the inotify limit: %w", lastErr)
	}
	return limit, nil
}

// StuckNodes returns the nodes among notReady whose container carrying label
// is still running. k3d names each node after its container.
func StuckNodes(ctx context.Context, runner toolchain.Runner, label string, notReady []string) ([]StuckNode, error) {
	docker, containers, err := runningContainers(ctx, runner, label)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var stuck []StuckNode
	for _, name := range notReady {
		if !slices.Contains(containers, name) {
			continue
		}
		// k3s writes its log to both streams; a log that cannot be read only
		// leaves the cause out.
		var stdout, stderr bytes.Buffer
		_, _ = runner.Run(ctx, toolchain.Command{
			Path:   docker,
			Args:   []string{"logs", "--tail", "200", name},
			Stdout: &stdout,
			Stderr: &stderr,
		})
		stuck = append(stuck, StuckNode{
			Name: name,
			TooManyFiles: strings.Contains(stdout.String(), "too many open files") ||
				strings.Contains(stderr.String(), "too many open files"),
		})
	}
	return stuck, nil
}

// runningContainers resolves docker and lists the names of the running
// containers carrying label.
func runningContainers(ctx context.Context, runner toolchain.Runner, label string) (string, []string, error) {
	docker, err := runner.LookPath("docker")
	if err != nil {
		return "", nil, fmt.Errorf("finding docker: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var names bytes.Buffer
	if _, err := runner.Run(ctx, toolchain.Command{
		Path:   docker,
		Args:   []string{"ps", "--filter", "label=" + label, "--format", "{{.Names}}"},
		Stdout: &names,
	}); err != nil {
		return "", nil, fmt.Errorf("listing the lab's containers: %w", err)
	}
	return docker, strings.Fields(names.String()), nil
}

// readInotifyLimit reads fs.inotify.max_user_instances through container.
func readInotifyLimit(ctx context.Context, runner toolchain.Runner, docker, container string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out bytes.Buffer
	if _, err := runner.Run(ctx, toolchain.Command{
		Path:   docker,
		Args:   []string{"exec", container, "cat", "/proc/sys/fs/inotify/max_user_instances"},
		Stdout: &out,
	}); err != nil {
		return 0, fmt.Errorf("reading the inotify limit: %w", err)
	}
	limit, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		return 0, fmt.Errorf("reading the inotify limit: %w", err)
	}
	return limit, nil
}
