// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"io"
	"slices"

	"github.com/snowops-oss/snowopslabs/internal/config"
)

// legacyDomainSuffixes are the suffixes k3d and kind labs used by default
// before lab hostnames moved under *.localhost. Those names need hosts-file
// entries, and a Windows browser never sees WSL's /etc/hosts.
var legacyDomainSuffixes = []string{"k3d.local", "kind.local"}

// domainMove returns the suffix a lab's hostnames move to, and whether they
// move at all. Only a local lab still on a legacy default moves, and never
// one whose suffix the user chose in the environment or .env.
func domainMove(c *config.Config) (string, bool) {
	if !c.LocalIngress() || c.DomainSuffixPinned || !slices.Contains(legacyDomainSuffixes, c.DomainSuffix) {
		return "", false
	}
	return c.ClusterName + ".localhost", true
}

// moveLegacyDomain moves an existing lab from a legacy suffix to
// <cluster>.localhost in place, which every browser resolves to this machine
// without hosts-file entries. The lab keeps its apps, scenarios and data.
func moveLegacyDomain(out io.Writer) error {
	to, ok := domainMove(cfg)
	if !ok {
		return nil
	}
	from := cfg.DomainSuffix
	fmt.Fprintf(out, "\n=== Moving lab URLs from *.%s to *.%s ===\n", from, to)
	if err := scriptExec.RunScript("runtimes/_lib/move-domain.sh", cfg.ClusterName, from, to); err != nil {
		return fmt.Errorf("moving the lab's hostnames to *.%s failed: %w\n"+
			"Re-run 'labctl init' to retry, or keep the old names by setting DOMAIN_SUFFIX=%s in .env", to, err, from)
	}
	reloadIngress()
	if hostsBlockPresent() {
		fmt.Fprintf(out, "The lab no longer needs its /etc/hosts entries; remove them with 'labctl hosts remove'.\n")
	}
	return nil
}
