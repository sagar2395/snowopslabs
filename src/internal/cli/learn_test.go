// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"testing"

	"github.com/sagar2395/snowopslabs/internal/learn"
)

func TestExpandVars(t *testing.T) {
	t.Setenv("MY_HOST", "example.com")
	cases := []struct {
		in, suffix, want string
	}{
		// ${VAR:-default} default form — the case that broke module 2.
		{"http://go-api.${DOMAIN_SUFFIX:-k3d.local}/health", "prod.internal", "http://go-api.prod.internal/health"},
		// default applies when the suffix is empty.
		{"http://go-api.${DOMAIN_SUFFIX:-k3d.local}/health", "", "http://go-api.k3d.local/health"},
		// plain ${VAR} form.
		{"http://x.${DOMAIN_SUFFIX}/y", "k3d.local", "http://x.k3d.local/y"},
		// other env vars still resolve.
		{"http://${MY_HOST}/z", "k3d.local", "http://example.com/z"},
		// nothing to expand.
		{"http://static/health", "k3d.local", "http://static/health"},
	}
	for _, c := range cases {
		if got := expandVars(c.in, c.suffix); got != c.want {
			t.Errorf("expandVars(%q, %q) = %q, want %q", c.in, c.suffix, got, c.want)
		}
	}
}

func TestChecksCheckHTTP(t *testing.T) {
	// expectStatus (canonical) must be honoured, and the URL fully expanded so
	// url.Parse never sees a raw ${...}.
	c := learn.Check{
		Name:         "go-api-healthy",
		Type:         "http",
		URL:          "http://go-api.${DOMAIN_SUFFIX:-k3d.local}/health",
		ExpectStatus: 200,
	}
	got := checksCheck(c, "/tmp", "k3d.local")
	if got.URL != "http://go-api.k3d.local/health" {
		t.Errorf("URL = %q, want expanded", got.URL)
	}
	if got.ExpectStatus != 200 {
		t.Errorf("ExpectStatus = %d, want 200", got.ExpectStatus)
	}
}
