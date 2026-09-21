// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"os"
	"strings"
)

// Enabled reports whether the Prometheus /metrics endpoint should be served.
// It is off unless LABCTL_METRICS=true.
func Enabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("LABCTL_METRICS")), "true")
}
