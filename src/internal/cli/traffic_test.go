// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/store"
)

// showTrafficEnv replaces the generator's start script with one that prints
// the settings it was given.
func showTrafficEnv(t *testing.T, l *terminal) {
	t.Helper()
	l.Write(t, "src/services/traffic/start.sh", "#!/usr/bin/env bash\nenv | grep '^TRAFFIC_' | sort\n")
}

func TestTraffic_Start(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"defaults load the bound app at a steady rate", nil,
			[]string{"TRAFFIC_PROFILE=steady", "TRAFFIC_RPS=10", "TRAFFIC_TARGET=http://demo-app.demo-app.svc.cluster.local:8080/"}},
		{"--app picks the app to load", []string{"--app", "go-api"},
			[]string{"TRAFFIC_TARGET=http://go-api.go-api.svc.cluster.local:8080/"}},
		{"every setting is passed on", []string{"--profile", "spike", "--rps", "25", "--duration", "90s", "--method", "POST", "--target", "http://echo.test/echo"},
			[]string{"TRAFFIC_PROFILE=spike", "TRAFFIC_RPS=25", "TRAFFIC_DURATION=90s", "TRAFFIC_METHOD=POST", "TRAFFIC_TARGET=http://echo.test/echo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			showTrafficEnv(t, l)
			r := l.mustRun(append([]string{"traffic", "start"}, tt.args...)...)
			for _, want := range tt.want {
				if !strings.Contains(r.stdout, want+"\n") {
					t.Errorf("the generator was not started with %s:\n%s", want, r.stdout)
				}
			}
		})
	}
}

// Settings from one start do not carry into the next.
func TestTraffic_StartAgainUsesItsOwnSettings(t *testing.T) {
	l := newTerminal(t)
	showTrafficEnv(t, l)
	l.mustRun("traffic", "start", "--duration", "90s", "--method", "POST")
	r := l.mustRun("traffic", "start")
	for _, stale := range []string{"TRAFFIC_DURATION", "TRAFFIC_METHOD"} {
		if strings.Contains(r.stdout, stale) {
			t.Errorf("the second start inherited %s from the first:\n%s", stale, r.stdout)
		}
	}
}

func TestTraffic_Journey(t *testing.T) {
	l := newTerminal(t)
	l.golden("traffic_journey",
		l.mustRun("traffic", "profiles"),
		l.mustRun("traffic", "status"),
		l.mustRun("traffic", "start"),
		l.mustRun("traffic", "stop"),
		l.mustRun("runs", "list"),
	)
}

func TestTraffic_RecordsEveryOperation(t *testing.T) {
	for _, op := range []string{"start", "stop", "status"} {
		t.Run(op, func(t *testing.T) {
			l := newTerminal(t)
			l.mustRun("traffic", op)
			r := l.mustRun("runs", "list")
			if !strings.Contains(r.stdout, "traffic."+op) || !strings.Contains(r.stdout, "succeeded") {
				t.Fatalf("traffic %s is missing from successful run history:\n%s", op, r.stdout)
			}
		})
	}
}

func TestTraffic_InterruptStopsAndRecordsTheOperation(t *testing.T) {
	for _, op := range []string{"start", "stop", "status"} {
		t.Run(op, func(t *testing.T) {
			l := newTerminal(t)
			l.Write(t, "src/services/traffic/"+op+".sh", slowSetup)
			ctx, interrupt := context.WithCancel(t.Context())
			defer interrupt()
			done := make(chan struct{})
			go func() {
				defer close(done)
				deadline := time.NewTimer(5 * time.Second)
				defer deadline.Stop()
				tick := time.NewTicker(5 * time.Millisecond)
				defer tick.Stop()
				for {
					if l.exists("src/services/traffic/STARTED") {
						interrupt()
						return
					}
					select {
					case <-tick.C:
					case <-deadline.C:
						interrupt()
						return
					}
				}
			}()
			r := l.runCtx(ctx, "traffic", op)
			<-done
			if r.exitCode() != 1 || !l.exists("src/services/traffic/STOPPED") {
				t.Fatalf("interrupt did not stop traffic %s: exit=%d\n%s\n%s", op, r.exitCode(), r.stdout, r.stderr)
			}
			st, err := store.Open(t.Context(), filepath.Join(l.Home, "snowops.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			runs, err := st.ListRuns(t.Context(), store.RunFilter{Kind: "traffic." + op})
			if err != nil || len(runs) != 1 || runs[0].Status != store.StatusCancelled {
				t.Fatalf("cancelled traffic %s history=%+v, err=%v", op, runs, err)
			}
		})
	}
}

func TestTraffic_Failures(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, l *terminal)
		args    []string
		wantErr string
	}{
		{"a profile that does not exist", nil, []string{"traffic", "start", "--profile", "ghost"}, "ghost"},
		{"a rate of zero", nil, []string{"traffic", "start", "--rps", "0"}, "rps"},
		{"a duration that is not a duration", nil, []string{"traffic", "start", "--duration", "soon"}, "duration"},
		{"a method that is not an HTTP method", nil, []string{"traffic", "start", "--method", "YEET"}, "method"},
		{"a target that is not a URL", nil, []string{"traffic", "start", "--target", "not a url"}, "target"},
		{"a rate that is not a number", nil, []string{"traffic", "start", "--rps", "fast"}, "invalid argument"},
		{"the generator fails to start",
			func(t *testing.T, l *terminal) {
				l.Write(t, "src/services/traffic/start.sh", "#!/usr/bin/env bash\necho 'k6 image not found' >&2\nexit 9\n")
			},
			[]string{"traffic", "start"}, "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			if tt.prepare != nil {
				tt.prepare(t, l)
			}
			r := l.run(tt.args...)
			if r.exitCode() != 1 || !strings.Contains(strings.ToLower(r.stderr), strings.ToLower(tt.wantErr)) {
				t.Errorf("exit %d, stderr %q; want exit 1 mentioning %q", r.exitCode(), r.stderr, tt.wantErr)
			}
			if tt.prepare == nil && strings.Contains(r.stdout, "traffic started") {
				t.Errorf("the generator was started with settings that were refused:\n%s", r.stdout)
			}
			if tt.prepare != nil && !strings.Contains(r.stdout, "exit 9") {
				t.Errorf("the failed run did not retain the script's exit code:\n%s", r.stdout)
			}
		})
	}
}
