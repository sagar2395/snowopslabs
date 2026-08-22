// SPDX-License-Identifier: Apache-2.0
package incident

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/executor"
)

func amServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/alerts" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if !strings.Contains(r.URL.Query().Get("filter"), "alertname=") {
			t.Errorf("filter missing alertname: %q", r.URL.Query().Get("filter"))
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
}

func TestQueryAlert(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		status    int
		wantFired bool
		wantErr   bool
	}{
		{
			name:      "active alert fires",
			body:      `[{"startsAt":"2026-06-12T17:00:00Z","status":{"state":"active"}}]`,
			wantFired: true,
		},
		{
			name:      "suppressed alert does not count",
			body:      `[{"startsAt":"2026-06-12T17:00:00Z","status":{"state":"suppressed"}}]`,
			wantFired: false,
		},
		{
			name:      "no alerts",
			body:      `[]`,
			wantFired: false,
		},
		{
			name:    "alertmanager error",
			body:    `oops`,
			status:  http.StatusInternalServerError,
			wantErr: true,
		},
		{
			name:    "malformed response",
			body:    `{not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := tt.status
			if status == 0 {
				status = http.StatusOK
			}
			srv := amServer(t, tt.body, status)
			defer srv.Close()

			st := queryAlert(context.Background(), srv.Client(), srv.URL, "LabFaultOOMKill")
			if tt.wantErr {
				if st.Error == "" {
					t.Fatalf("expected error, got %+v", st)
				}
				return
			}
			if st.Error != "" {
				t.Fatalf("unexpected error: %s", st.Error)
			}
			if st.Fired != tt.wantFired {
				t.Fatalf("Fired = %v, want %v", st.Fired, tt.wantFired)
			}
			if tt.wantFired && st.Since.IsZero() {
				t.Fatal("fired alert must carry its start time")
			}
		})
	}
}

func TestQueryAlert_NoURLConfigured(t *testing.T) {
	st := queryAlert(context.Background(), http.DefaultClient, "", "X")
	if st.Error == "" || !strings.Contains(st.Error, "ALERTMANAGER_URL") {
		t.Fatalf("expected configuration error, got %+v", st)
	}
}

// writeAlertFault builds a fault that declares expectAlert (with its rule file).
func writeAlertFault(t *testing.T, root, name string) {
	t.Helper()
	writeFault(t, root, name)
	dir := filepath.Join(root, "incidents", name)
	yaml := strings.Replace(testFaultYAML, "%s", name, 1) + "expectAlert: LabFaultTest\n"
	os.WriteFile(filepath.Join(dir, "fault.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "alerts"), 0755)
	os.WriteFile(filepath.Join(dir, "alerts", "rule.yaml"), []byte("# rule with alert: LabFaultTest\n"), 0644)
}

func TestStatus_ReportsAlertState(t *testing.T) {
	root := t.TempDir()
	createApp(t, root, "go-api")
	writeAlertFault(t, root, "fault-a")

	srv := amServer(t, `[{"startsAt":"2026-06-12T17:00:00Z","status":{"state":"active"}}]`, http.StatusOK)
	defer srv.Close()

	e := NewEngine(root, "k3d.local")
	e.AlertmanagerURL = srv.URL
	if _, err := e.Inject("fault-a", executor.New(root), false, false); err != nil {
		t.Fatal(err)
	}

	res, err := e.Status(context.Background(), testRunner(), "")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if res.Alert == nil {
		t.Fatal("StatusResult.Alert must be set when the fault declares expectAlert")
	}
	if res.Alert.Expected != "LabFaultTest" || !res.Alert.Fired {
		t.Fatalf("alert status wrong: %+v", res.Alert)
	}
}

func TestStatus_NoExpectAlertSkipsQuery(t *testing.T) {
	e, root := testEngine(t, "fault-a")      // plain fault, no expectAlert
	e.AlertmanagerURL = "http://127.0.0.1:1" // would fail if queried
	if _, err := e.Inject("fault-a", executor.New(root), false, false); err != nil {
		t.Fatal(err)
	}
	res, err := e.Status(context.Background(), testRunner(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Alert != nil {
		t.Fatalf("Alert must be nil without expectAlert: %+v", res.Alert)
	}
}

func TestLoadFault_ExpectAlertRequiresRuleFile(t *testing.T) {
	root := t.TempDir()
	writeAlertFault(t, root, "fault-a")
	os.Remove(filepath.Join(root, "incidents", "fault-a", "alerts", "rule.yaml"))

	e := NewEngine(root, "k3d.local")
	if len(e.List()) != 0 {
		t.Fatal("expectAlert without alerts/rule.yaml must not load")
	}
	if err := e.LoadErrors()["fault-a"]; err == nil || !strings.Contains(err.Error(), "alerts/rule.yaml") {
		t.Fatalf("got %v", err)
	}
}

// TestRepoFaults_AlertRulesConsistent pins the shipped paging faults: the
// rule file must declare exactly the alert named in expectAlert, carry the
// labfault routing label, and the kube-prometheus-stack rule-selector label.
func TestRepoFaults_AlertRulesConsistent(t *testing.T) {
	root := repoRoot(t)
	e := NewEngine(root, "k3d.local")

	paging := 0
	for _, f := range e.List() {
		if f.ExpectAlert == "" {
			continue
		}
		paging++
		data, err := os.ReadFile(filepath.Join(f.Dir, "alerts", "rule.yaml"))
		if err != nil {
			t.Errorf("%s: %v", f.Name, err)
			continue
		}
		rule := string(data)
		for _, want := range []string{
			"alert: " + f.ExpectAlert,
			`labfault: "true"`,
			"release: prometheus",
		} {
			if !strings.Contains(rule, want) {
				t.Errorf("%s: alerts/rule.yaml missing %q", f.Name, want)
			}
		}
		// inject must arm the rule, resolve must disarm it.
		inject, _ := os.ReadFile(filepath.Join(f.Dir, "inject.sh"))
		if !strings.Contains(string(inject), "alerts/rule.yaml") {
			t.Errorf("%s: inject.sh does not apply alerts/rule.yaml", f.Name)
		}
		resolve, _ := os.ReadFile(filepath.Join(f.Dir, "resolve.sh"))
		if !strings.Contains(string(resolve), "delete prometheusrule") {
			t.Errorf("%s: resolve.sh does not remove the alert rule", f.Name)
		}
	}
	if paging < 3 {
		t.Errorf("expected at least 3 paging faults, found %d", paging)
	}
}
