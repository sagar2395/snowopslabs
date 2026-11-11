// SPDX-License-Identifier: Apache-2.0

package checkfixture

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/pkg/checks"
)

func TestKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "an ingress host keeps its first label", in: "http://go-api.snowops.localhost:8080/health", want: "go-api/health"},
		{name: "a query is kept as sent", in: "http://prometheus.lab.test/api/v1/query?query=up%7Bjob%3D%22x%22%7D", want: "prometheus/api/v1/query?query=up%7Bjob%3D%22x%22%7D"},
		{name: "an address with no dots", in: "http://localhost:9090/api/v1/query?query=up", want: "localhost/api/v1/query?query=up"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := Key(u); got != tc.want {
				t.Errorf("Key(%s) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// lab is a live lab as the checks see it: kubectl answers and a server that
// is both the workload's ingress and Prometheus.
func lab(t *testing.T, replicas string) (*checks.Runner, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/grafana":
			http.Redirect(w, r, "/login", http.StatusFound)
		case "/login":
			fmt.Fprint(w, "sign in")
		case "/api/v1/query":
			if r.URL.Query().Get("query") == "missing" {
				fmt.Fprint(w, `{"status":"success","data":{"result":[]}}`)
				return
			}
			fmt.Fprint(w, `{"status":"success","data":{"result":[{"value":[1,"0.97"]}]}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	r := checks.NewRunner()
	r.PrometheusURL = srv.URL
	r.Exec = func(_ context.Context, name string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case name != "kubectl":
			return "", nil
		case strings.Contains(joined, "deployment/gone"):
			return "", errors.New(`kubectl: Error from server (NotFound): deployments.apps "gone" not found`)
		case strings.Contains(joined, "readyReplicas"):
			return replicas, nil
		default:
			return "deployment.apps/go-api", nil
		}
	}
	return r, srv
}

func labChecks(url string) []checks.Check {
	return []checks.Check{
		{Name: "exists", Type: checks.TypeKubectl, Resource: "deployment/go-api", Namespace: "go-api"},
		{Name: "ready", Type: checks.TypeKubectl, Resource: "deployment/go-api", Namespace: "go-api", JSONPath: "{.status.readyReplicas}", Operator: ">=", Value: "2"},
		{Name: "gone", Type: checks.TypeKubectl, Resource: "deployment/gone", Namespace: "go-api"},
		{Name: "slo", Type: checks.TypePromQL, Query: "availability", Operator: ">=", Value: "0.95"},
		{Name: "no-series", Type: checks.TypePromQL, Query: "missing", Operator: ">", Value: "0"},
		{Name: "health", Type: checks.TypeHTTP, URL: url + "/health"},
		{Name: "redirected", Type: checks.TypeHTTP, URL: url + "/grafana", BodyContains: "sign in"},
		{Name: "script", Type: checks.TypeScript, Script: "check.sh"},
	}
}

func outcomes(results []checks.Result) map[string]bool {
	m := map[string]bool{}
	for _, r := range results {
		if r.Type != checks.TypeScript {
			m[r.Name] = r.Pass
		}
	}
	return m
}

// A recorded lab replays to the same results, from a file, with no lab.
func TestRecordThenReplay(t *testing.T) {
	for _, replicas := range []string{"3", "1"} {
		t.Run("ready replicas "+replicas, func(t *testing.T) {
			r, srv := lab(t, replicas)
			f := New()
			f.Vars["WorkloadName"] = "go-api"
			live := Record(r, f).RunAll(t.Context(), labChecks(srv.URL))
			f.SetResults(live)

			path := filepath.Join(t.TempDir(), "fixtures", "state.json")
			if err := f.Save(path); err != nil {
				t.Fatalf("Save: %v", err)
			}
			srv.Close()
			loaded, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			replayed := Replay(loaded).RunAll(t.Context(), labChecks(srv.URL))
			want, got := outcomes(live), outcomes(replayed)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("replayed %v, recorded %v", got, want)
			}
			if fmt.Sprint(loaded.Results) != fmt.Sprint(want) {
				t.Errorf("saved results %v, want %v", loaded.Results, want)
			}
			if _, ok := loaded.Results["script"]; ok {
				t.Error("a script check's result was recorded; its behaviour tests own it")
			}
			if loaded.Vars["WorkloadName"] != "go-api" {
				t.Errorf("vars = %v", loaded.Vars)
			}
		})
	}
}

func TestReplay_RefusesWhatWasNotRecorded(t *testing.T) {
	f := New()
	f.PrometheusURL = "http://prometheus.lab.test"
	r := Replay(f)
	tests := []struct {
		name  string
		check checks.Check
	}{
		{name: "a kubectl call", check: checks.Check{Name: "k", Type: checks.TypeKubectl, Resource: "pod/x"}},
		{name: "a query", check: checks.Check{Name: "q", Type: checks.TypePromQL, Query: "up", Operator: ">", Value: "0"}},
		{name: "a URL", check: checks.Check{Name: "h", Type: checks.TypeHTTP, URL: "http://go-api.lab.test/health"}},
		{name: "a script", check: checks.Check{Name: "s", Type: checks.TypeScript, Script: "x.sh"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := r.Run(t.Context(), tc.check)
			if res.Pass || !strings.Contains(res.Error, "not in the recorded fixture") {
				t.Errorf("result = %+v, want a refusal naming the fixture", res)
			}
		})
	}
}

func TestLoad_Failures(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := New().Save(bad); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "missing.json"), bad} {
		if _, err := Load(path); err == nil {
			t.Errorf("Load(%s) succeeded", path)
		}
	}
}

func TestSave_FailsWhereItCannotWrite(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := New().Save(filepath.Join(file, "under-a-file.json")); err == nil {
		t.Error("Save under a regular file succeeded")
	}
}

// A request the lab never answered is not recorded, and the error reaches the check.
func TestRecord_PassesTransportErrorsThrough(t *testing.T) {
	r := checks.NewRunner()
	r.HTTPClient = &http.Client{Transport: failingTransport{}}
	f := New()
	res := Record(r, f).Run(t.Context(), checks.Check{Name: "h", Type: checks.TypeHTTP, URL: "http://go-api.lab.test/health"})
	if res.Pass || !strings.Contains(res.Error, "connection refused") {
		t.Errorf("result = %+v", res)
	}
	if len(f.HTTP) != 0 {
		t.Errorf("recorded %v for a request that never got an answer", f.HTTP)
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: connection refused")
}
