// SPDX-License-Identifier: Apache-2.0
package checks

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRunner() *Runner {
	r := NewRunner()
	r.DefaultTimeout = 5 * time.Second
	return r
}

// --- http checks -----------------------------------------------------------

func TestRunHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/teapot":
			w.WriteHeader(http.StatusTeapot)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	r := testRunner()
	ctx := context.Background()

	tests := []struct {
		name  string
		check Check
		pass  bool
	}{
		{"status ok", Check{Name: "h", Type: "http", URL: srv.URL + "/health"}, true},
		{"body match", Check{Name: "h", Type: "http", URL: srv.URL + "/health", BodyContains: `"ok"`}, true},
		{"body mismatch", Check{Name: "h", Type: "http", URL: srv.URL + "/health", BodyContains: "nope"}, false},
		{"explicit status", Check{Name: "h", Type: "http", URL: srv.URL + "/teapot", ExpectStatus: 418}, true},
		{"wrong status", Check{Name: "h", Type: "http", URL: srv.URL + "/missing"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := r.Run(ctx, tt.check)
			if res.Pass != tt.pass {
				t.Fatalf("Pass = %v, want %v (got=%q err=%q)", res.Pass, tt.pass, res.Got, res.Error)
			}
		})
	}
}

func TestRunHTTP_ConnectionRefused(t *testing.T) {
	r := testRunner()
	res := r.Run(context.Background(), Check{
		Name: "h", Type: "http", URL: "http://127.0.0.1:1", TimeoutSeconds: 1,
	})
	if res.Pass {
		t.Fatal("expected failure for unreachable server")
	}
	if res.Error == "" {
		t.Fatal("expected an error message")
	}
}

// --- kubectl checks (stubbed exec) ------------------------------------------

func TestRunKubectl_JSONPath(t *testing.T) {
	r := testRunner()
	var gotArgs []string
	r.Exec = func(ctx context.Context, name string, args ...string) (string, error) {
		gotArgs = append([]string{name}, args...)
		return "2\n", nil
	}

	res := r.Run(context.Background(), Check{
		Name: "loki", Type: "kubectl", Resource: "statefulset/loki", Namespace: "monitoring",
		JSONPath: "{.status.readyReplicas}", Operator: ">=", Value: "1",
	})

	if !res.Pass {
		t.Fatalf("expected pass, got=%q err=%q", res.Got, res.Error)
	}
	want := []string{"kubectl", "get", "statefulset/loki", "--namespace", "monitoring", "-o", "jsonpath={.status.readyReplicas}"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("kubectl args:\n got %v\nwant %v", gotArgs, want)
	}
}

func TestRunKubectl_Existence(t *testing.T) {
	r := testRunner()

	r.Exec = func(ctx context.Context, name string, args ...string) (string, error) {
		return "prometheusrule.monitoring.coreos.com/go-api-alerts\n", nil
	}
	res := r.Run(context.Background(), Check{Name: "rules", Type: "kubectl", Resource: "prometheusrules", Namespace: "monitoring"})
	if !res.Pass {
		t.Fatalf("expected pass for existing resource, err=%q", res.Error)
	}

	r.Exec = func(ctx context.Context, name string, args ...string) (string, error) {
		return "", nil // kubectl get with no items: exit 0, empty output
	}
	res = r.Run(context.Background(), Check{Name: "rules", Type: "kubectl", Resource: "prometheusrules"})
	if res.Pass {
		t.Fatal("expected failure when no resources match")
	}
}

func TestRunKubectl_ExecError(t *testing.T) {
	r := testRunner()
	r.Exec = func(ctx context.Context, name string, args ...string) (string, error) {
		return "", fmt.Errorf("kubectl: connection refused")
	}
	res := r.Run(context.Background(), Check{
		Name: "x", Type: "kubectl", Resource: "deploy/x",
		JSONPath: "{.status.replicas}", Operator: "==", Value: "1",
	})
	if res.Pass || res.Error == "" {
		t.Fatalf("expected failed result with error, got pass=%v err=%q", res.Pass, res.Error)
	}
}

// --- promql checks ------------------------------------------------------------

func promServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("query") == "" {
			t.Error("query parameter missing")
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
}

func TestRunPromQL(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		status  int
		check   Check
		pass    bool
		wantErr bool
	}{
		{
			name:  "passing comparison",
			body:  `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"0.25"]}]}}`,
			check: Check{Name: "p99", Type: "promql", Query: "histogram_quantile(0.99, x)", Operator: "<", Value: "0.3"},
			pass:  true,
		},
		{
			name:  "failing comparison",
			body:  `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"0.9"]}]}}`,
			check: Check{Name: "p99", Type: "promql", Query: "x", Operator: "<", Value: "0.3"},
			pass:  false,
		},
		{
			name:  "no samples",
			body:  `{"status":"success","data":{"resultType":"vector","result":[]}}`,
			check: Check{Name: "p99", Type: "promql", Query: "x", Operator: "<", Value: "0.3"},
			pass:  false,
		},
		{
			name:    "prometheus error status",
			body:    `{"status":"error"}`,
			check:   Check{Name: "p99", Type: "promql", Query: "x", Operator: "<", Value: "0.3"},
			wantErr: true,
		},
		{
			name:    "http 500",
			body:    `oops`,
			status:  http.StatusInternalServerError,
			check:   Check{Name: "p99", Type: "promql", Query: "x", Operator: "<", Value: "0.3"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := tt.status
			if status == 0 {
				status = http.StatusOK
			}
			srv := promServer(t, tt.body, status)
			defer srv.Close()

			r := testRunner()
			r.PrometheusURL = srv.URL
			res := r.Run(context.Background(), tt.check)

			if tt.wantErr {
				if res.Error == "" {
					t.Fatalf("expected error, got pass=%v got=%q", res.Pass, res.Got)
				}
				return
			}
			if res.Pass != tt.pass {
				t.Fatalf("Pass = %v, want %v (got=%q err=%q)", res.Pass, tt.pass, res.Got, res.Error)
			}
		})
	}
}

func TestRunPromQL_NoURL(t *testing.T) {
	r := testRunner()
	res := r.Run(context.Background(), Check{Name: "p", Type: "promql", Query: "up", Operator: "==", Value: "1"})
	if res.Pass || !strings.Contains(res.Error, "PROMETHEUS_URL") {
		t.Fatalf("expected configuration error, got pass=%v err=%q", res.Pass, res.Error)
	}
}

// --- script checks ------------------------------------------------------------

func TestRunScript(t *testing.T) {
	dir := t.TempDir()
	pass := filepath.Join(dir, "pass.sh")
	fail := filepath.Join(dir, "fail.sh")
	os.WriteFile(pass, []byte("#!/usr/bin/env bash\nexit 0\n"), 0755)
	os.WriteFile(fail, []byte("#!/usr/bin/env bash\necho broken >&2\nexit 3\n"), 0755)

	r := testRunner()
	r.ScriptDir = dir

	res := r.Run(context.Background(), Check{Name: "ok", Type: "script", Script: "pass.sh"})
	if !res.Pass {
		t.Fatalf("expected pass, err=%q", res.Error)
	}

	res = r.Run(context.Background(), Check{Name: "bad", Type: "script", Script: "fail.sh"})
	if res.Pass {
		t.Fatal("expected failure for exit 3")
	}
	if !strings.Contains(res.Error, "broken") {
		t.Fatalf("expected stderr in error, got %q", res.Error)
	}
}

func TestRunScript_EnvPropagated(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "env.sh")
	os.WriteFile(script, []byte("#!/usr/bin/env bash\n[ \"$DOMAIN_SUFFIX\" = \"test.local\" ] || exit 1\n"), 0755)

	r := testRunner()
	r.ScriptDir = dir
	r.Env = []string{"DOMAIN_SUFFIX=test.local"}

	res := r.Run(context.Background(), Check{Name: "env", Type: "script", Script: "env.sh"})
	if !res.Pass {
		t.Fatalf("script should see runner env, err=%q", res.Error)
	}
}

// --- general runner behavior ---------------------------------------------------

func TestRun_InvalidCheckFailsWithoutExecuting(t *testing.T) {
	r := testRunner()
	r.Exec = func(ctx context.Context, name string, args ...string) (string, error) {
		t.Fatal("invalid check must not execute anything")
		return "", nil
	}
	res := r.Run(context.Background(), Check{Name: "x", Type: "kubectl"}) // missing resource
	if res.Pass || res.Error == "" {
		t.Fatalf("expected validation failure, got pass=%v err=%q", res.Pass, res.Error)
	}
}

func TestRunAll_ContinuesPastFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := testRunner()
	results := r.RunAll(context.Background(), []Check{
		{Name: "bad", Type: "http", URL: srv.URL, ExpectStatus: 500},
		{Name: "good", Type: "http", URL: srv.URL},
	})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Pass || !results[1].Pass {
		t.Fatalf("expected [fail, pass], got [%v, %v]", results[0].Pass, results[1].Pass)
	}
}

func TestRun_TimeoutRespected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	r := testRunner()
	start := time.Now()
	res := r.Run(context.Background(), Check{Name: "slow", Type: "http", URL: srv.URL, TimeoutSeconds: 1})
	if res.Pass {
		t.Fatal("expected timeout failure")
	}
	if time.Since(start) > 1900*time.Millisecond {
		t.Fatalf("check ran past its timeout: %v", time.Since(start))
	}
}
