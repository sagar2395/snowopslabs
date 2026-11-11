// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/checkfixture"
	"github.com/snowops-oss/snowopslabs/internal/labtest"
)

// drillMarker exists while the drill scenario's exercise is unsolved.
const drillMarker = "scenarios/drill/BROKEN"

// writeDrill adds a scenario whose setup leaves an exercise to solve: its
// check fails until the learner removes the marker the setup created. It has
// one parameter, an objective and a remediation, so every part of the
// scenario commands has something to print.
func writeDrill(t *testing.T, l *terminal) {
	t.Helper()
	l.Write(t, "scenarios/drill/scenario.yaml", `apiVersion: scenario.snowops.net/v2
name: drill
displayName: Drill
description: Practise on {{.WorkloadName}}
category: reliability
objectives:
  - Bring {{.WorkloadName}} back
prerequisites:
  apps: ["{{.WorkloadName}}"]
parameters:
  - name: Level
    displayName: "Difficulty"
    description: "How hard the drill is"
    default: "1"
    type: int
    min: 1
    max: 3
components:
  - name: setup
    type: script
    script: setup.sh
    uninstallScript: teardown.sh
checks:
  - name: fixed
    type: script
    script: checks/fixed.sh
    timeoutSeconds: 5
    remediation: remove the BROKEN marker
`)
	l.Write(t, "scenarios/drill/setup.sh", "#!/usr/bin/env bash\nset -eu\ndir=$(dirname \"$0\")\ntouch \"$dir/BROKEN\"\necho \"$WORKLOAD_NAME\" >\"$dir/BOUND\"\necho \"drill set up for $WORKLOAD_NAME\"\n")
	l.Write(t, "scenarios/drill/teardown.sh", "#!/usr/bin/env bash\nset -eu\ndir=$(dirname \"$0\")\nrm -f \"$dir/BROKEN\" \"$dir/BOUND\"\necho \"drill torn down for $WORKLOAD_NAME\"\n")
	l.Write(t, "scenarios/drill/checks/fixed.sh", "#!/usr/bin/env bash\nset -eu\ndir=$(dirname \"$0\")/..\necho \"$WORKLOAD_NAME\" >\"$dir/CHECKED\"\nif [ -f \"$dir/BROKEN\" ]; then\n  echo \"the drill is not solved yet\" >&2\n  exit 1\nfi\n")
}

// A scenario at the terminal: activate it, fail verification while the
// exercise is unsolved, pass once it is solved, retry, and tear down.
func TestScenario_Journey(t *testing.T) {
	l := newTerminal(t)
	writeDrill(t, l)
	var steps []cliResult
	step := func(args ...string) cliResult {
		t.Helper()
		r := l.run(args...)
		steps = append(steps, r)
		return r
	}
	ok := func(args ...string) cliResult {
		t.Helper()
		r := step(args...)
		if r.err != nil {
			t.Fatalf("labctl %s failed: %v\n%s", strings.Join(args, " "), r.err, r.stderr)
		}
		return r
	}

	ok("scenario", "list")
	assertScenarioJSON(t, ok("scenario", "list", "--output", "json"), "drill", false)
	ok("scenario", "info", "drill")
	ok("scenario", "status")

	ok("scenario", "up", "drill")
	if !l.exists(drillMarker) {
		t.Fatal("up reported success but did not run the scenario's setup")
	}
	ok("scenario", "list")
	assertScenarioJSON(t, ok("scenario", "list", "-o", "json"), "drill", true)
	ok("scenario", "status")
	ok("scenario", "status", "drill")

	unsolved := step("scenario", "verify", "drill")
	if unsolved.exitCode() != 1 {
		t.Errorf("verify on the unsolved drill exited %d, want 1", unsolved.exitCode())
	}
	for _, want := range []string{"FAIL", "the drill is not solved yet", "1 of 1 checks failed", "remove the BROKEN marker"} {
		if !strings.Contains(unsolved.stdout+unsolved.stderr, want) {
			t.Errorf("verify on the unsolved drill does not print %q:\n%s%s", want, unsolved.stdout, unsolved.stderr)
		}
	}

	l.remove(drillMarker) // the learner solves it
	if r := ok("scenario", "verify", "drill"); !strings.Contains(r.stdout, "All 1 checks passed.") {
		t.Errorf("verify on the solved drill does not report the pass:\n%s", r.stdout)
	}

	if r := ok("scenario", "up", "drill"); !strings.Contains(r.stderr, "already active") || l.exists(drillMarker) {
		t.Errorf("a second up on an active scenario reran its setup; stderr: %q", r.stderr)
	}
	ok("scenario", "up", "drill", "--force")
	if !l.exists(drillMarker) {
		t.Error("up --force did not reinstall the scenario")
	}

	l.remove(drillMarker)
	ok("scenario", "reset", "drill")
	if !l.exists(drillMarker) {
		t.Error("reset did not put the exercise back")
	}

	ok("scenario", "down", "drill")
	if l.exists(drillMarker) {
		t.Error("down reported success but did not run the scenario's teardown")
	}
	if r := ok("scenario", "down", "drill"); !strings.Contains(r.stderr, "is not active") {
		t.Errorf("a second down = %q, want it to say the scenario is not active", r.stderr)
	}
	ok("scenario", "status")
	l.golden("scenario_journey", steps...)
}

// A scenario that needs a platform component the lab lacks: with
// --deploy-prereqs, `scenario up` installs it as a recorded run, `platform
// status` lists it with that run, and `platform teardown` removes it.
func TestScenario_DeployPrereqsInstallsThePlatformComponent(t *testing.T) {
	l := newTerminal(t)
	l.Write(t, "platform/cost/opencost/install.sh", "#!/usr/bin/env bash\nset -eu\necho installing opencost\ntouch \"$(dirname \"$0\")/INSTALLED\"\n")
	l.Write(t, "platform/cost/opencost/uninstall.sh", "#!/usr/bin/env bash\nset -eu\necho removing opencost\nrm -f \"$(dirname \"$0\")/INSTALLED\"\n")
	l.Write(t, "scenarios/costed/scenario.yaml", `apiVersion: scenario.snowops.net/v2
name: costed
displayName: Costed
description: A scenario for {{.WorkloadName}} that needs a cost tool
category: reliability
prerequisites:
  platform: [cost/opencost]
components:
  - name: setup
    type: script
    script: setup.sh
`)
	l.Write(t, "scenarios/costed/setup.sh", "#!/usr/bin/env bash\necho costed set up\n")
	// The cluster holds the opencost namespace while its install marker exists.
	if err := os.Rename(filepath.Join(l.Bin, "kubectl"), filepath.Join(l.Bin, "kubectl.fixture")); err != nil {
		t.Fatal(err)
	}
	l.Stub(t, "kubectl", `#!/bin/sh
fixture="$(dirname "$0")/kubectl.fixture"
if [ -f '`+filepath.Join(l.Root, "platform", "cost", "opencost", "INSTALLED")+`' ]; then
  case "$*" in
    "get namespaces -o name") "$fixture" "$@"; echo namespace/opencost; exit 0 ;;
    "get namespace opencost --no-headers") echo "opencost Active 1h"; exit 0 ;;
  esac
fi
exec "$fixture" "$@"
`)

	up := l.mustRun("scenario", "up", "costed", "--deploy-prereqs")
	for _, want := range []string{"installing cost/opencost...", "install cost/opencost started (run ", "installing opencost", "costed set up"} {
		if !strings.Contains(up.stdout, want) {
			t.Errorf("scenario up --deploy-prereqs does not print %q:\n%s", want, up.stdout)
		}
	}

	status := l.mustRun("platform", "status")
	listed := false
	for line := range strings.Lines(status.stdout) {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "cost/opencost" {
			listed = f[1] == "installed" && f[2] != "-"
		}
	}
	if !listed {
		t.Errorf("status does not list cost/opencost as installed with the run that installed it:\n%s", status.stdout)
	}

	if r := l.mustRun("platform", "teardown"); !strings.Contains(r.stdout, "removing opencost") {
		t.Errorf("teardown does not remove the prerequisite scenario up installed:\n%s", r.stdout)
	}
	if l.exists("platform/cost/opencost/INSTALLED") {
		t.Error("teardown reported success but did not run opencost's uninstall script")
	}
}

// A scenario stays bound to the app it was activated for: verify and down act
// on that app without being told again.
func TestScenario_KeepsTheAppItWasActivatedFor(t *testing.T) {
	l := newTerminal(t)
	writeDrill(t, l)
	bound := func(file string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(l.Root, "scenarios", "drill", file))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(data))
	}

	l.mustRun("scenario", "up", "drill", "--app", "go-api")
	if got := bound("BOUND"); got != "go-api" {
		t.Fatalf("the setup ran for %q, want go-api", got)
	}
	l.run("scenario", "verify", "drill")
	if got := bound("CHECKED"); got != "go-api" {
		t.Errorf("verify checked %q, want the app the scenario was activated for", got)
	}
	if r := l.mustRun("scenario", "info", "drill"); !strings.Contains(r.stdout, "Practise on go-api") {
		t.Errorf("info describes another app than the one activated:\n%s", r.stdout)
	}
	if r := l.mustRun("scenario", "down", "drill"); !strings.Contains(r.stdout, "drill torn down for go-api") {
		t.Errorf("down tore down another app than the one activated:\n%s", r.stdout)
	}
}

func TestScenario_Parameters(t *testing.T) {
	tests := []struct {
		name    string
		set     []string
		wantOut string
		wantErr string
	}{
		{"the default", nil, "Level = 1", ""},
		{"an override", []string{"--set", "Level=3"}, "Level = 3  (overridden)", ""},
		{"a value out of range", []string{"--set", "Level=9"}, "", "Level"},
		{"a value of the wrong type", []string{"--set", "Level=hard"}, "", "Level"},
		{"a parameter the scenario does not have", []string{"--set", "Speed=2"}, "", `unknown parameter "Speed" (scenario "drill" accepts: Level)`},
		{"an override that is not Name=value", []string{"--set", "Level"}, "", "Level"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			writeDrill(t, l)
			r := l.run(append([]string{"scenario", "up", "drill"}, tt.set...)...)
			if tt.wantErr != "" {
				if r.exitCode() != 1 || !strings.Contains(r.stdout+r.stderr, tt.wantErr) {
					t.Errorf("exit %d, output %q; want exit 1 mentioning %q", r.exitCode(), r.stdout+r.stderr, tt.wantErr)
				}
				if l.exists(drillMarker) {
					t.Error("the scenario's setup ran with a parameter that was refused")
				}
				return
			}
			if r.err != nil || !strings.Contains(r.stdout, tt.wantOut) {
				t.Errorf("err = %v; output does not show %q:\n%s", r.err, tt.wantOut, r.stdout)
			}
		})
	}
}

// --watch keeps checking until the lab converges, and gives up at --timeout.
func TestScenario_VerifyWatch(t *testing.T) {
	// The check passes on its third run.
	const countingCheck = "#!/usr/bin/env bash\nset -eu\nf=$(dirname \"$0\")/../RUNS\necho x >>\"$f\"\n[ \"$(wc -l <\"$f\")\" -ge 3 ]\n"

	t.Run("passes once the lab converges", func(t *testing.T) {
		l := newTerminal(t)
		writeDrill(t, l)
		l.Write(t, "scenarios/drill/checks/fixed.sh", countingCheck)
		r := l.run("scenario", "verify", "drill", "--watch", "--interval", "10ms", "--timeout", "30s")
		if r.err != nil || !strings.Contains(r.stdout, "All 1 checks passed.") {
			t.Errorf("err = %v; want a pass after retries:\n%s", r.err, r.stdout)
		}
		if got := strings.Count(r.stdout, "retrying in"); got != 2 {
			t.Errorf("retried %d times, want 2:\n%s", got, r.stdout)
		}
	})
	t.Run("fails when the lab never converges", func(t *testing.T) {
		l := newTerminal(t)
		writeDrill(t, l)
		l.mustRun("scenario", "up", "drill")
		r := l.run("scenario", "verify", "drill", "--watch", "--interval", "10ms", "--timeout", "150ms")
		if r.exitCode() != 1 || !strings.Contains(r.stderr, "1 of 1 checks failed") {
			t.Errorf("exit %d, stderr %q; want exit 1 with the failed count", r.exitCode(), r.stderr)
		}
	})
}

// `scenario new` scaffolds a scenario that validates and verifies as created.
func TestScenario_New(t *testing.T) {
	l := newTerminal(t)
	created := l.mustRun("scenario", "new", "my-drill")
	if !l.exists("scenarios/my-drill/scenario.yaml") {
		t.Fatalf("new did not create the scenario:\n%s", created.stdout)
	}
	if r := l.mustRun("validate"); !strings.Contains(r.stdout, "2 scenarios") {
		t.Errorf("the scaffolded scenario does not validate:\n%s%s", r.stdout, r.stderr)
	}
	if r := l.mustRun("scenario", "list"); !strings.Contains(r.stdout, "my-drill") {
		t.Errorf("the scaffolded scenario is not listed:\n%s", r.stdout)
	}

	again := l.run("scenario", "new", "my-drill")
	if again.exitCode() != 1 || !strings.Contains(again.stderr, "--force") {
		t.Errorf("a second new = exit %d, stderr %q; want it refused with a pointer to --force", again.exitCode(), again.stderr)
	}
	l.mustRun("scenario", "new", "my-drill", "--force")
}

func TestCLICurrentDir_ReturnsEmptyWhenTheWorkingDirectoryIsGone(t *testing.T) {
	old := cliGetwd
	cliGetwd = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { cliGetwd = old })
	if got := cliCurrentDir(); got != "" {
		t.Errorf("cliCurrentDir() = %q, want empty string", got)
	}
}

// `scenario render` prints a scenario file as the engine would apply it.
func TestScenario_Render(t *testing.T) {
	l := newTerminal(t)
	writeDrill(t, l)
	l.Write(t, "scenarios/drill/manifests/pdb.yaml", "name: {{.WorkloadName}}-pdb\nnamespace: {{.WorkloadNamespace}}\nlevel: {{.Level}}\n")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"the lab's bound app", []string{"scenario", "render", "drill", "manifests/pdb.yaml"}, "name: demo-app-pdb\nnamespace: demo-app\nlevel: 1\n"},
		{"the app --app names", []string{"scenario", "render", "drill", "manifests/pdb.yaml", "--app", "go-api"}, "name: go-api-pdb\nnamespace: go-api\nlevel: 1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if r := l.mustRun(tt.args...); r.stdout != tt.want {
				t.Errorf("render = %q, want %q", r.stdout, tt.want)
			}
		})
	}
}

func TestScenario_Failures(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, l *terminal)
		args    []string
		wantErr string
	}{
		{"up a scenario that does not exist", nil, []string{"scenario", "up", "ghost"}, "see 'labctl scenario list'"},
		{"info on a scenario that does not exist", nil, []string{"scenario", "info", "ghost"}, "see 'labctl scenario list'"},
		{"status of a scenario that does not exist", nil, []string{"scenario", "status", "ghost"}, "see 'labctl scenario list'"},
		{"verify a scenario that does not exist", nil, []string{"scenario", "verify", "ghost"}, "see 'labctl scenario list'"},
		{"reset a scenario that does not exist", nil, []string{"scenario", "reset", "ghost"}, "see 'labctl scenario list'"},
		{"down a scenario that does not exist", nil, []string{"scenario", "down", "ghost"}, "see 'labctl scenario list'"},
		{"render a file the scenario does not have", nil, []string{"scenario", "render", "drill", "manifests/ghost.yaml"}, "ghost.yaml"},
		{"render a file outside the scenario", nil, []string{"scenario", "render", "drill", "../../.env"}, ".env"},
		{"new with a name that is not a valid scenario name", nil, []string{"scenario", "new", "Bad Name"}, "Bad Name"},
		{"list in a format that does not exist", nil, []string{"scenario", "list", "-o", "yaml"}, `unknown output format "yaml": use -o table or -o json`},
		{"up when the app is not deployed",
			func(_ *testing.T, l *terminal) { l.undeploy(labtest.App) },
			[]string{"scenario", "up", "drill"}, "labctl app build demo-app && labctl app deploy demo-app"},
		{"up when a component fails to install",
			func(t *testing.T, l *terminal) {
				l.Write(t, "scenarios/drill/setup.sh", "#!/usr/bin/env bash\necho 'helm: release failed' >&2\nexit 3\n")
			},
			[]string{"scenario", "up", "drill"}, "activate drill failed"},
		{"up for an app that does not exist", nil, []string{"scenario", "up", "drill", "--app", "ghost"}, "app ghost"},
		{"verify a scenario with no checks",
			func(t *testing.T, l *terminal) {
				l.Write(t, "scenarios/bare/scenario.yaml", "apiVersion: scenario.snowops.net/v2\nname: bare\ndisplayName: Bare\ndescription: No checks\ncategory: reliability\ncomponents:\n  - {name: setup, type: script, script: setup.sh}\n")
				l.Write(t, "scenarios/bare/setup.sh", "#!/usr/bin/env bash\nexit 0\n")
			},
			[]string{"scenario", "verify", "bare"}, "no checks"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			writeDrill(t, l)
			if tt.prepare != nil {
				tt.prepare(t, l)
			}
			r := l.run(tt.args...)
			if r.exitCode() != 1 {
				t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", r.exitCode(), r.stdout, r.stderr)
			}
			if !strings.Contains(r.stderr, tt.wantErr) {
				t.Errorf("stderr = %q, want it to say %q", r.stderr, tt.wantErr)
			}
		})
	}
}

// assertScenarioJSON checks that `scenario list -o json` printed a JSON array
// and nothing else, with name in it and its active state as want.
func assertScenarioJSON(t *testing.T, r cliResult, name string, active bool) {
	t.Helper()
	var items []struct {
		Name     string `json:"name"`
		Category string `json:"category"`
		Active   bool   `json:"active"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &items); err != nil {
		t.Fatalf("scenario list -o json is not a JSON array: %v\n%s", err, r.stdout)
	}
	for _, it := range items {
		if it.Name == name {
			if it.Active != active || it.Category == "" {
				t.Errorf("scenario %s in JSON = %+v, want active=%v and a category", name, it, active)
			}
			return
		}
	}
	t.Errorf("scenario list -o json does not list %s:\n%s", name, r.stdout)
}

// A scenario whose install fails is not left looking active.
func TestScenario_FailedUpIsNotActive(t *testing.T) {
	l := newTerminal(t)
	writeDrill(t, l)
	l.Write(t, "scenarios/drill/setup.sh", "#!/usr/bin/env bash\nexit 3\n")
	if r := l.run("scenario", "up", "drill"); r.err == nil {
		t.Fatal("the failing up reported success")
	}
	if r := l.mustRun("scenario", "status", "drill"); !strings.Contains(r.stdout, "inactive") {
		t.Errorf("a failed up left the scenario active:\n%s", r.stdout)
	}
}

// The demo scenario in the fixture lab passes its one check as shipped.
func TestScenario_VerifyRecordsTheResult(t *testing.T) {
	l := newTerminal(t)
	l.mustRun("scenario", "verify", labtest.Scenario)
	data, err := os.ReadFile(filepath.Join(l.Home, "state", "clitest", "history", "results.jsonl"))
	if err != nil {
		t.Fatalf("verify recorded no result: %v", err)
	}
	if !strings.Contains(string(data), `"name":"`+labtest.Scenario+`"`) || !strings.Contains(string(data), "marker-present") {
		t.Errorf("the recorded result does not name the scenario and its check: %s", data)
	}
}

// `scenario down` that cannot remove a component says so, exits 1 and leaves
// the scenario active to retry; --force marks it inactive.
func TestScenario_DownWhenAnUninstallFails(t *testing.T) {
	l := newTerminal(t)
	writeDrill(t, l)
	l.mustRun("scenario", "up", "drill")
	l.Write(t, "scenarios/drill/teardown.sh", "#!/usr/bin/env bash\necho 'Unable to connect to the server: TLS handshake timeout' >&2\nexit 1\n")

	failed := l.run("scenario", "down", "drill")
	if failed.exitCode() != 1 || !strings.Contains(failed.stderr, "deactivate drill failed") {
		t.Errorf("down = exit %d, stderr %q; want the run reported failed", failed.exitCode(), failed.stderr)
	}
	for _, want := range []string{"could not uninstall setup", "labctl scenario down drill --force"} {
		if !strings.Contains(failed.stdout, want) {
			t.Errorf("down does not say %q:\n%s", want, failed.stdout)
		}
	}
	if r := l.mustRun("scenario", "status", "drill"); !strings.Contains(r.stdout, ": active") {
		t.Errorf("a failed down left the scenario inactive:\n%s", r.stdout)
	}

	forced := l.mustRun("scenario", "down", "drill", "--force")
	if !strings.Contains(forced.stdout, "Left behind (remove it by hand): setup") {
		t.Errorf("down --force does not name what was left behind:\n%s", forced.stdout)
	}
	if r := l.mustRun("scenario", "status", "drill"); !strings.Contains(r.stdout, "inactive") {
		t.Errorf("down --force left the scenario active:\n%s", r.stdout)
	}
}

// `scenario verify --record` writes what the lab answered the declared checks,
// with the variables they were rendered with, and the replay tests read it.
func TestScenario_VerifyRecord(t *testing.T) {
	l := newTerminal(t)
	l.Write(t, "scenarios/recorded/scenario.yaml", `apiVersion: scenario.snowops.net/v2
name: recorded
displayName: Recorded
description: Checks on {{.WorkloadName}}
category: reliability
parameters:
  - name: Path
    default: /health
checks:
  - name: app-answers
    type: http
    url: "http://{{.WorkloadName}}.{{.IngressURLSuffix}}{{.Path}}"
  - name: namespace-listed
    type: kubectl
    resource: namespace/{{.WorkloadNamespace}}
  - name: by-script
    type: script
    script: checks/ok.sh
`)
	l.Write(t, "scenarios/recorded/checks/ok.sh", "#!/usr/bin/env bash\nexit 0\n")
	l.ingress.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") })
	l.mustRun("scenario", "up", "recorded")

	tests := []struct {
		name string
		args []string
		ok   bool
	}{
		{name: "a failing verify records its answers too", args: []string{"scenario", "verify", "recorded"}},
		{name: "a watch records the attempt it ends on", args: []string{"scenario", "verify", "recorded", "--watch", "--interval", "10ms", "--timeout", "50ms"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixtures", "untouched.json")
			r := l.run(append(tc.args, "--record", path)...)
			if r.err == nil {
				t.Fatalf("verify passed; the kubectl check should fail on the stand-in cluster:\n%s", r.stdout)
			}
			if !strings.Contains(r.stdout, "Recorded the lab's answers to "+path) {
				t.Errorf("stdout does not say where the fixture went:\n%s", r.stdout)
			}
			f, err := checkfixture.Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if want := map[string]bool{"app-answers": true, "namespace-listed": false}; !reflect.DeepEqual(f.Results, want) {
				t.Errorf("results = %v, want %v", f.Results, want)
			}
			if f.Vars["Path"] != "/health" || f.Vars["WorkloadName"] != labtest.App {
				t.Errorf("vars = %v", f.Vars)
			}
			for _, local := range []string{"ProjectRoot", "StateDir"} {
				if v, ok := f.Vars[local]; ok {
					t.Errorf("the fixture records this machine's %s %q", local, v)
				}
			}
			if _, ok := f.Kubectl["get namespace/"+labtest.App+" -o name"]; !ok {
				t.Errorf("kubectl answers = %v", f.Kubectl)
			}
			if _, ok := f.HTTP[labtest.App+"/health"]; !ok {
				t.Errorf("http answers = %v", f.HTTP)
			}
		})
	}

	t.Run("a fixture it cannot write fails the verify", func(t *testing.T) {
		l.Write(t, "blocker", "x")
		r := l.run("scenario", "verify", "recorded", "--record", filepath.Join(l.Root, "blocker", "f.json"))
		if r.err == nil || !strings.Contains(r.stderr, "writing the check fixture") {
			t.Errorf("err = %v, stderr %q", r.err, r.stderr)
		}
	})
}
