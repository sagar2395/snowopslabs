// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"fmt"
	"io"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
)

// AdmitFunc decides whether a fault may be injected given what it adds to the
// lab. It writes any warnings to out and returns an error to block it.
type AdmitFunc func(ctx context.Context, out io.Writer, next capacity.Demand) error

// Demand is what injecting f adds to the lab: its own workloads (such as a
// noisy neighbour) and the app it breaks. Naming the app lets the capacity
// check warn when an active scenario is graded against the same app.
func (e *Engine) Demand(f *Fault) (capacity.Demand, error) {
	d := capacity.Demand{
		Name:   "incident " + f.Name,
		Items:  []capacity.Item{{Kind: capacity.ItemApp, Name: e.Workload.Name}},
		CPUs:   f.Requirements.CPUs,
		Agents: f.Requirements.Agents,
		App:    e.Workload.Name,
	}
	own, err := f.Requirements.MemoryMiB()
	if err != nil {
		return d, fmt.Errorf("incident %s: %w", f.Name, err)
	}
	d.OwnMiB = own
	return d, nil
}
