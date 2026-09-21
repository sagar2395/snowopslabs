// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
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
// listed here rather than discovered, so `hosts add` also covers components
// that are not installed yet.
var platformSubdomains = []string{
	"grafana",
	"prometheus",
	// The incident engine queries alertmanager.<suffix>.
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

// collectHosts returns every hostname the lab serves. If the apps or the
// cluster cannot be read, it writes a warning to warn and returns the hosts it
// could find.
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

// mergeHosts returns the sorted, de-duplicated hostnames under domainSuffix:
// one per subdomain, plus each discovered host under the suffix. Hosts outside
// the suffix are ignored.
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
	for line := range strings.SplitSeq(content, "\n") {
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

// warnMissingHosts prints the Ingress hosts that /etc/hosts does not list yet.
// It prints nothing when the cluster cannot be read or /etc/hosts has no
// managed block.
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

// hostsBlockPresent reports whether /etc/hosts has the managed block. It needs
// no privileges.
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
	c := exec.Command("sudo", args...) //nolint:gosec,noctx // re-exec of this same CLI under sudo; a context would not manage the replacement process
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

// writeManagedHostsFile rewrites path with the managed block replaced, or
// removed when block is empty. It writes atomically and keeps the file's
// permission bits.
func writeManagedHostsFile(path, block string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	result := computeHostsContent(string(data), block)

	// Keep the current mode (0644 for a new file). The new file is owned by
	// root, which is correct for /etc/hosts.
	perm := os.FileMode(0o644)
	if fi, statErr := os.Stat(path); statErr == nil {
		perm = fi.Mode().Perm()
	}

	if err := atomicWriteFile(path, []byte(result), perm); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// computeHostsContent returns a hosts file's content with the managed block
// removed and, when block is non-empty, the new block appended.
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
// over path, so readers only ever see the complete old or new file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".snowops-hosts-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Remove the temp file if we return before the rename.
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
