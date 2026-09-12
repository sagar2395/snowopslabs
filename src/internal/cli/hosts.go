// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/k8s"
	"github.com/spf13/cobra"
)

const (
	hostsFile  = "/etc/hosts"
	hostsBegin = "# BEGIN snowops-labs"
	hostsEnd   = "# END snowops-labs"
)

// platformSubdomains are the hostnames platform components serve. They are
// listed rather than discovered so a `hosts add` run straight after `labctl init`
// already covers components that are not installed yet.
var platformSubdomains = []string{
	"grafana",
	"prometheus",
	// The incident engine asks alertmanager.<suffix> whether a paging fault fired.
	"alertmanager",
	"opencost",
	"argocd",
	"traefik",
	// The dashboard's Ingress host is dashboard.<suffix>, not its release name.
	"dashboard",
	"chaos",
	"vault",
}

// hostsAddResolved carries the host list into the sudo re-exec, because under
// sudo kubectl would read root's kubeconfig and find no cluster.
var hostsAddResolved []string

var hostsCmd = &cobra.Command{
	Use:   "hosts",
	Short: "Manage /etc/hosts entries for cluster ingress hostnames",
}

var hostsAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add (or update) the managed /etc/hosts block",
	Long: `Writes one /etc/hosts line naming every hostname the lab serves: the platform
components, each app under apps/, and every Ingress host in the cluster under
the domain suffix. Re-run it after activating a scenario that adds hostnames,
such as env-promotion's <app>-dev, -staging and -prod.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		hosts := hostsAddResolved
		if len(hosts) == 0 {
			hosts = collectHosts(cmd.Context(), cfg.ProjectRoot, cfg.DomainSuffix, os.Stderr)
		}
		if os.Getuid() != 0 {
			return reexecWithSudo("--resolved-hosts=" + strings.Join(hosts, ","))
		}
		before := managedHosts(readHostsFile())
		if err := writeHostsBlock(buildBlock(hosts)); err != nil {
			return err
		}
		printHostsSummary(os.Stdout, hosts, before)
		return nil
	},
}

var hostsRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove the managed /etc/hosts block",
	RunE: func(cmd *cobra.Command, args []string) error {
		if os.Getuid() != 0 {
			return reexecWithSudo()
		}
		if err := writeHostsBlock(""); err != nil {
			return err
		}
		fmt.Printf("Removed managed block from %s\n", hostsFile)
		return nil
	},
}

func init() {
	hostsAddCmd.Flags().StringSliceVar(&hostsAddResolved, "resolved-hosts", nil, "hostnames to write, as collected before elevating")
	_ = hostsAddCmd.Flags().MarkHidden("resolved-hosts")
	hostsCmd.AddCommand(hostsAddCmd)
	hostsCmd.AddCommand(hostsRemoveCmd)
	rootCmd.AddCommand(hostsCmd)
}

// collectHosts gathers every hostname the lab serves. A cluster that cannot be
// read is reported, not fatal: the platform and app hosts still get written.
func collectHosts(ctx context.Context, projectRoot, domainSuffix string, warn io.Writer) []string {
	subdomains := slices.Clone(platformSubdomains)
	apps, err := config.ListApps(projectRoot)
	if err != nil {
		fmt.Fprintf(warn, "Warning: could not list apps (%v); their hostnames are not included.\n", err)
	}
	subdomains = append(subdomains, apps...)

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ingress, err := k8s.IngressHosts(ctx)
	if err != nil {
		fmt.Fprintf(warn, "Warning: could not read Ingress hosts from the cluster (%v).\n", err)
		fmt.Fprintln(warn, "         Hostnames that scenarios add are not included. Re-run once the cluster is up,")
		fmt.Fprintln(warn, "         without sudo: labctl hosts add elevates itself after reading the cluster.")
	}
	return mergeHosts(domainSuffix, subdomains, ingress)
}

// mergeHosts returns the sorted, de-duplicated hostnames under domainSuffix: one
// per subdomain, plus each discovered host the suffix covers. A host outside the
// suffix belongs to someone else's DNS and is left alone.
func mergeHosts(domainSuffix string, subdomains, discovered []string) []string {
	set := map[string]bool{}
	for _, sub := range subdomains {
		set[sub+"."+domainSuffix] = true
	}
	for _, host := range discovered {
		if strings.HasSuffix(host, "."+domainSuffix) {
			set[host] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// managedHosts returns the hostnames in the managed block of a hosts file.
func managedHosts(content string) map[string]bool {
	hosts := map[string]bool{}
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		switch {
		case line == hostsBegin:
			inBlock = true
		case line == hostsEnd:
			inBlock = false
		case inBlock:
			if fields := strings.Fields(line); len(fields) > 1 {
				for _, h := range fields[1:] {
					hosts[h] = true
				}
			}
		}
	}
	return hosts
}

func printHostsSummary(w io.Writer, hosts []string, before map[string]bool) {
	var added []string
	for _, h := range hosts {
		if !before[h] {
			added = append(added, h)
		}
	}
	fmt.Fprintf(w, "Updated %s: %d hostnames", hostsFile, len(hosts))
	if len(added) == 0 {
		fmt.Fprintln(w, ", none new.")
		return
	}
	fmt.Fprintf(w, ", %d new:\n", len(added))
	for _, h := range added {
		fmt.Fprintf(w, "  + %s\n", h)
	}
}

// warnMissingHosts names the Ingress hosts /etc/hosts does not list yet, so a
// learner is told before a browser fails to resolve them. It stays quiet when
// the cluster cannot be read or the hosts file is not managed at all.
func warnMissingHosts(ctx context.Context, w io.Writer, domainSuffix string) {
	content := readHostsFile()
	if !strings.Contains(content, hostsBegin) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ingress, err := k8s.IngressHosts(ctx)
	if err != nil {
		return
	}
	missing := unlistedHosts(domainSuffix, ingress, managedHosts(content))
	if len(missing) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%d hostname(s) are not in /etc/hosts yet, so they will not resolve:\n", len(missing))
	for _, h := range missing {
		fmt.Fprintf(w, "  %s\n", h)
	}
	fmt.Fprintln(w, "Add them with: labctl hosts add")
}

func unlistedHosts(domainSuffix string, ingress []string, listed map[string]bool) []string {
	var missing []string
	for _, h := range mergeHosts(domainSuffix, nil, ingress) {
		if !listed[h] {
			missing = append(missing, h)
		}
	}
	return missing
}

func readHostsFile() string {
	data, err := os.ReadFile(hostsFile)
	if err != nil {
		return ""
	}
	return string(data)
}

// hostsBlockPresent reports whether the managed block is already in /etc/hosts.
// Reading the file needs no privileges, so init/doctor can advise without sudo.
func hostsBlockPresent() bool {
	return strings.Contains(readHostsFile(), hostsBegin)
}

func buildBlock(hosts []string) string {
	return hostsBegin + "\n" +
		"127.0.0.1 " + strings.Join(hosts, " ") + "\n" +
		hostsEnd + "\n"
}

// reexecWithSudo re-runs the current invocation under sudo, preserving all flags
// and appending extra arguments.
func reexecWithSudo(extra ...string) error {
	fmt.Fprintln(os.Stderr, "Root required — re-running with sudo...")
	args := append([]string{os.Args[0]}, os.Args[1:]...)
	args = append(args, extra...)
	c := osexec.Command("sudo", args...) //nolint:gosec,noctx // re-exec of this same CLI under sudo; a context would not manage the replacement process
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// writeHostsBlock replaces the managed block in /etc/hosts.
// Pass an empty string to remove the block.
func writeHostsBlock(block string) error {
	return writeManagedHostsFile(hostsFile, block)
}

// writeManagedHostsFile rewrites path with the managed block replaced (or removed
// when block is empty). The write is atomic — a temp file in the same directory
// swapped in by rename — so a crash or a full disk can never leave /etc/hosts
// half-written and unusable. The file's existing permission bits are preserved.
func writeManagedHostsFile(path, block string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	result := computeHostsContent(string(data), block)

	// Preserve the current mode (0644 for a not-yet-existing file). Ownership
	// follows the rename's new inode; this runs as root against a root-owned
	// /etc/hosts, so root:root/​wheel is what we want anyway.
	perm := os.FileMode(0o644)
	if fi, statErr := os.Stat(path); statErr == nil {
		perm = fi.Mode().Perm()
	}

	if err := atomicWriteFile(path, []byte(result), perm); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// computeHostsContent returns the contents of a hosts file with the managed
// block stripped and, when block is non-empty, the new block appended. It is
// pure so the block-rewriting logic can be tested without touching /etc/hosts.
func computeHostsContent(existing, block string) string {
	lines := strings.Split(existing, "\n")
	var out []string
	inBlock := false
	for _, line := range lines {
		if line == hostsBegin {
			inBlock = true
			continue
		}
		if inBlock {
			if line == hostsEnd {
				inBlock = false
			}
			continue
		}
		out = append(out, line)
	}

	// Drop trailing blank lines produced by the split.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}

	result := strings.Join(out, "\n")
	if block != "" {
		if result != "" {
			result += "\n"
		}
		result += block
	} else {
		result += "\n"
	}
	return result
}

// atomicWriteFile writes data to a temp file in path's directory and renames it
// over path, so readers ever see only the complete old or complete new file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".snowops-hosts-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename; a no-op once renamed.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
