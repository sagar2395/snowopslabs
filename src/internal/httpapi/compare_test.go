// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sagar2395/snowopslabs/internal/compare"
	"github.com/sagar2395/snowopslabs/internal/config"
)

func newCompareServer(t *testing.T) *Server {
	t.Helper()
	return &Server{cfg: &config.Config{ProjectRoot: t.TempDir(), DomainSuffix: "k3d.local"}}
}

func writeComparison(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, ".labctl", "history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rep := &compare.Report{
		Scenario: "autoscaling-under-load", Apps: []string{"go-api", "java-api"},
		Profile: "steady", RPS: 30, WarmupSeconds: 60, WindowSeconds: 120,
		StartedAt: time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC),
		Measurements: []compare.Measurement{
			{App: "go-api", Values: map[string]float64{compare.KeyRPS: 30, compare.KeyP99: 0.005}},
			{App: "java-api", Values: map[string]float64{compare.KeyRPS: 30, compare.KeyP99: 0.010}},
		},
	}
	id, err := compare.NewStore(dir).Append(rep)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestListComparisonsCarriesTheMetricSet(t *testing.T) {
	s := newCompareServer(t)
	writeComparison(t, s.cfg.ProjectRoot)

	rr := httptest.NewRecorder()
	s.handleListComparisons(rr, httptest.NewRequest(http.MethodGet, "/api/v2/comparisons", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body)
	}

	var got []comparisonView
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d comparisons, want 1", len(got))
	}

	// The conditions travel with the numbers: without them a client cannot say
	// what the measurement means.
	c := got[0]
	if c.RPS != 30 || c.WarmupSeconds != 60 || c.WindowSeconds != 120 || c.Profile != "steady" {
		t.Errorf("conditions lost in the response: %+v", c)
	}
	if len(c.Metrics) == 0 {
		t.Fatal("no metric set sent; the client would have to hardcode one")
	}
	// Direction and scale are the server's to declare, not the client's to guess.
	var p99 *metricView
	for i := range c.Metrics {
		if c.Metrics[i].Key == compare.KeyP99 {
			p99 = &c.Metrics[i]
		}
	}
	if p99 == nil {
		t.Fatal("p99 missing from the metric set")
	}
	if !p99.LowerBetter {
		t.Error("p99 should be lower-is-better")
	}
	if p99.Scale != 1000 || p99.Unit != "ms" {
		t.Errorf("p99 display = %v %s, want a seconds→ms scale", p99.Scale, p99.Unit)
	}
}

func TestGetComparisonByID(t *testing.T) {
	s := newCompareServer(t)
	id := writeComparison(t, s.cfg.ProjectRoot)

	rr := httptest.NewRecorder()
	req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/v2/comparisons/"+id, nil), map[string]string{"id": id})
	s.handleGetComparison(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body)
	}
	var got comparisonView
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != id || len(got.Measurements) != 2 {
		t.Errorf("got %+v", got)
	}
}

func TestGetUnknownComparisonIs404(t *testing.T) {
	s := newCompareServer(t)
	rr := httptest.NewRecorder()
	req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/v2/comparisons/nope", nil), map[string]string{"id": "nope"})
	s.handleGetComparison(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestListComparisonsEmptyIsAnEmptyArray(t *testing.T) {
	s := newCompareServer(t)
	rr := httptest.NewRecorder()
	s.handleListComparisons(rr, httptest.NewRequest(http.MethodGet, "/api/v2/comparisons", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	// [] rather than null, so the client can map over it without a guard.
	if body := rr.Body.String(); body != "[]\n" && body != "[]" {
		t.Errorf("body = %q, want an empty array", body)
	}
}
