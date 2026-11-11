// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
)

// CapacityResponse is the Docker engine's size and how much of it the lab
// uses, in MiB.
type CapacityResponse struct {
	CPUs      int `json:"cpus"`
	MemoryMiB int `json:"memoryMiB"`
	// UsedMiB is the memory in use on the machine that runs the cluster.
	UsedMiB int `json:"usedMiB"`
	// UsableMiB is the part of MemoryMiB the capacity check lets the lab plan
	// to use; the rest absorbs start-up spikes.
	UsableMiB int `json:"usableMiB"`
}

// handleCapacity reports the Docker engine's size and the lab's memory use.
// A runtime without a local Docker engine (incluster) has nothing to report.
func (s *Server) handleCapacity(w http.ResponseWriter, r *http.Request) {
	label, ok := capacity.NodeLabel(s.cfg.Profile, s.cfg.ClusterName)
	if !ok {
		respondError(w, r, http.StatusNotFound, "not_found", "the "+s.cfg.Profile+" runtime has no local Docker engine to measure")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	res, err := capacity.Probe(ctx, s.docker)
	if err != nil {
		respondError(w, r, http.StatusServiceUnavailable, "unavailable", err.Error())
		return
	}
	used, err := capacity.Usage(ctx, s.docker, label)
	if err != nil {
		respondError(w, r, http.StatusServiceUnavailable, "unavailable", err.Error())
		return
	}
	respondJSON(w, http.StatusOK, CapacityResponse{
		CPUs:      res.CPUs,
		MemoryMiB: int(res.MemBytes >> 20),
		UsedMiB:   used,
		UsableMiB: capacity.UsableMiB(res),
	})
}
