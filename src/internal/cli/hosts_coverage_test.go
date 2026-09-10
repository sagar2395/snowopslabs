// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every hostname labctl builds a URL for has to be one it also puts in
// /etc/hosts. Alertmanager was not, so `incident status` could never reach the
// pager and every expectAlert fault reported that its page had not fired.
func TestKnownSubdomainsCoverEveryURLTheCLIBuilds(t *testing.T) {
	known := make(map[string]bool, len(knownSubdomains))
	for _, s := range knownSubdomains {
		known[s] = true
	}

	// Matches `"http://name." + something` — how the CLI and the API compose a
	// default ingress URL from the domain suffix.
	pattern := regexp.MustCompile(`"https?://([a-z0-9-]+)\." \+`)

	root := filepath.Join("..", "..", "internal")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range pattern.FindAllStringSubmatch(string(data), -1) {
			if !known[m[1]] {
				t.Errorf("%s builds a URL for %q but knownSubdomains has no entry, so the host will not resolve", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}
}
