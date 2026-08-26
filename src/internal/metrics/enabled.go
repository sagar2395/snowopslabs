// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"os"
	"strings"
)

// Enabled reports whether the Prometheus /metrics endpoint should be served.
// It is OFF by default and turned on with LABCTL_METRICS=true, mirroring the
// LABCTL_AUTH gate. Keeping it opt-in means the default local experience
// exposes no scrape surface at all.
func Enabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("LABCTL_METRICS")), "true")
}
