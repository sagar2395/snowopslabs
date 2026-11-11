// SPDX-License-Identifier: Apache-2.0

package scenario

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/checkfixture"
	"github.com/snowops-oss/snowopslabs/pkg/checks"
)

const varsScenario = `name: vars-demo
displayName: Vars Demo
description: checks that use every kind of variable
category: testing
parameters:
  - name: Threshold
    default: "25"
    type: int
checks:
  - name: replicas
    type: kubectl
    resource: deployment/{{.WorkloadName}}
    namespace: "{{.WorkloadNamespace}}"
    jsonpath: "{.status.readyReplicas}"
    operator: ">="
    value: "{{.Threshold}}"
  - name: rate
    type: promql
    query: 'max_over_time(up{namespace="{{.MonitoringNamespace}}"}[{{.SinceActivation}}:1m])'
    operator: ">"
    value: "0"
  - name: health
    type: http
    url: "http://{{.WorkloadName}}.{{.IngressURLSuffix}}/health"
`

// A recorded verify's variables render the checks exactly as Verify did.
func TestChecksWithVars_RendersWhatVerifyRenders(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "vars-demo", varsScenario)
	e := NewEngine(root, "lab.test", "k3d")
	e.MonitoringNamespace = "observability"
	if err := e.markActive("vars-demo", map[string]string{"Threshold": "15"}); err != nil {
		t.Fatalf("markActive: %v", err)
	}

	vars, err := e.CheckVars("vars-demo")
	if err != nil {
		t.Fatalf("CheckVars: %v", err)
	}
	for k, want := range map[string]string{"Threshold": "15", "MonitoringNamespace": "observability", "IngressURLSuffix": "lab.test", "SinceActivation": "1m"} {
		if vars[k] != want {
			t.Errorf("vars[%s] = %q, want %q", k, vars[k], want)
		}
	}

	got, err := e.ChecksWithVars("vars-demo", vars)
	if err != nil {
		t.Fatalf("ChecksWithVars: %v", err)
	}
	s, _ := e.Get("vars-demo")
	restoreWorkload := e.withActivationWorkload("vars-demo")
	restoreParams := e.withActivationParams(s)
	want := make([]checks.Check, len(s.Checks))
	for i, c := range s.Checks {
		want[i] = e.resolveCheck(c)
	}
	restoreParams()
	restoreWorkload()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChecksWithVars =\n%+v\nVerify renders\n%+v", got, want)
	}
	if got[0].Value != "15" || !strings.Contains(got[2].URL, "go-api.lab.test") {
		t.Errorf("rendered checks %+v do not carry the activation's values", got)
	}
}

func TestCheckVars_UnknownScenario(t *testing.T) {
	e := NewEngine(t.TempDir(), "lab.test", "k3d")
	if _, err := e.CheckVars("nope"); err == nil {
		t.Error("CheckVars(nope) succeeded")
	}
	if _, err := e.ChecksWithVars("nope", nil); err == nil {
		t.Error("ChecksWithVars(nope) succeeded")
	}
}

// unrecorded lists the scenarios that have no recorded fixtures yet. The list
// only shrinks: the cluster suite's record mode writes the fixtures.
func unrecorded(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "unrecorded-scenarios.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
			names = append(names, line)
		}
	}
	return names
}

// fixtureStates are the lab states every scenario's record mode writes.
var fixtureStates = []string{"untouched", "solved"}

// Every scenario has its fixtures, or is on the list of those still to record.
func TestCheckFixtures_EveryScenarioIsRecorded(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(root, "lab.test", "k3d")
	pending := unrecorded(t)
	for _, s := range e.List() {
		var missing []string
		for _, state := range fixtureStates {
			if _, err := os.Stat(filepath.Join(s.Dir, "test", "fixtures", state+".json")); err != nil {
				missing = append(missing, state)
			}
		}
		listed := slices.Contains(pending, s.Name)
		switch {
		case len(missing) > 0 && !listed:
			t.Errorf("%s has no %v fixtures; record them on a lab: RECORD=1 make test-cluster ITEM=scenario/%s", s.Name, missing, s.Name)
		case len(missing) == 0 && listed:
			t.Errorf("%s is recorded; remove it from testdata/unrecorded-scenarios.txt", s.Name)
		}
	}
}

// Each recorded lab state replays to the results it was recorded with.
func TestCheckFixtures_Replay(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(root, "scenarios", "*", "test", "fixtures", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(root, "lab.test", "k3d")
	for _, path := range paths {
		name := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path))))
		state := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name+"/"+state, func(t *testing.T) {
			replayFixture(t, e, name, state, path)
		})
	}
}

func replayFixture(t *testing.T, e *Engine, name, state, path string) {
	t.Helper()
	f, err := checkfixture.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range replayProblems(t, e, name, state, f) {
		t.Error(p)
	}
}

// replayProblems replays a fixture through the scenario's current checks and
// returns what does not hold.
func replayProblems(t *testing.T, e *Engine, name, state string, f *checkfixture.Fixture) []string {
	t.Helper()
	cs, err := e.ChecksWithVars(name, f.Vars)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	runner := checkfixture.Replay(f)
	declared := map[string]bool{}
	for _, c := range cs {
		if c.Type == checks.TypeScript {
			continue
		}
		declared[c.Name] = true
		want, ok := f.Results[c.Name]
		if !ok {
			problems = append(problems, "check "+c.Name+" is not in the fixture; re-record it")
			continue
		}
		res := runner.Run(t.Context(), c)
		if strings.Contains(res.Error, checkfixture.ErrNotRecorded.Error()) {
			problems = append(problems, "check "+c.Name+" asks the lab something new: "+res.Error)
			continue
		}
		if res.Pass != want {
			problems = append(problems, fmt.Sprintf("check %s: replay pass=%v, recorded pass=%v (got %s, want %s)", c.Name, res.Pass, want, res.Got, res.Want))
		}
		// A pending check grades the learner's work, so it cannot pass on a
		// lab where none was done; on a solved lab every check passes.
		if state == "untouched" && c.Pending && want {
			problems = append(problems, "pending check "+c.Name+" passes on the untouched lab, so it grades nothing")
		}
		if state == "solved" && !want {
			problems = append(problems, "check "+c.Name+" fails on the solved lab")
		}
	}
	for n := range f.Results {
		if !declared[n] {
			problems = append(problems, "the fixture records check "+n+", which the scenario no longer declares; re-record it")
		}
	}
	slices.Sort(problems)
	return problems
}

const replayScenario = `name: replay-demo
displayName: Replay Demo
description: one lab check and one learner check
category: testing
checks:
  - name: app-ready
    type: kubectl
    resource: deployment/{{.WorkloadName}}
    namespace: "{{.WorkloadNamespace}}"
    jsonpath: "{.status.readyReplicas}"
    operator: ">="
    value: "1"
  - name: learner-scaled
    type: promql
    query: 'count(kube_deployment_spec_replicas{deployment="{{.WorkloadName}}"} >= 3)'
    operator: ">="
    value: "1"
    pending: true
  - name: graded-by-script
    type: script
    script: checks/x.sh
`

// fixtureFor answers the replay scenario's checks: replicas ready, and the
// learner's scale-out seen or not.
func fixtureFor(ready, scaled string) *checkfixture.Fixture {
	f := checkfixture.New()
	f.Vars = map[string]string{"WorkloadName": "go-api", "WorkloadNamespace": "go-api"}
	f.PrometheusURL = "http://prometheus.lab.test"
	f.Kubectl["get deployment/go-api --namespace go-api -o jsonpath={.status.readyReplicas}"] = checkfixture.Answer{Out: ready}
	q := `count(kube_deployment_spec_replicas{deployment="go-api"} >= 3)`
	f.HTTP["prometheus/api/v1/query?query="+url.QueryEscape(q)] = checkfixture.Response{
		Status: 200,
		Body:   `{"status":"success","data":{"result":[{"value":[1,"` + scaled + `"]}]}}`,
	}
	return f
}

func TestReplayProblems(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "replay-demo", replayScenario)
	e := NewEngine(root, "lab.test", "k3d")

	tests := []struct {
		name    string
		state   string
		fixture func() *checkfixture.Fixture
		want    []string
	}{
		{
			name: "an untouched lab: the lab check passes, the learner check waits", state: "untouched",
			fixture: func() *checkfixture.Fixture {
				f := fixtureFor("2", "0")
				f.Results = map[string]bool{"app-ready": true, "learner-scaled": false}
				return f
			},
		},
		{
			name: "a solved lab passes every check", state: "solved",
			fixture: func() *checkfixture.Fixture {
				f := fixtureFor("3", "1")
				f.Results = map[string]bool{"app-ready": true, "learner-scaled": true}
				return f
			},
		},
		{
			name: "a learner check that passes untouched grades nothing", state: "untouched",
			fixture: func() *checkfixture.Fixture {
				f := fixtureFor("2", "1")
				f.Results = map[string]bool{"app-ready": true, "learner-scaled": true}
				return f
			},
			want: []string{"pending check learner-scaled passes on the untouched lab, so it grades nothing"},
		},
		{
			name: "a check that fails on the solved lab", state: "solved",
			fixture: func() *checkfixture.Fixture {
				f := fixtureFor("3", "0")
				f.Results = map[string]bool{"app-ready": true, "learner-scaled": false}
				return f
			},
			want: []string{"check learner-scaled fails on the solved lab"},
		},
		{
			name: "the check now judges the recorded answer differently", state: "untouched",
			fixture: func() *checkfixture.Fixture {
				f := fixtureFor("0", "0")
				f.Results = map[string]bool{"app-ready": true, "learner-scaled": false}
				return f
			},
			want: []string{"check app-ready: replay pass=false, recorded pass=true (got 0, want >= 1)"},
		},
		{
			name: "the check asks something the lab was never asked", state: "untouched",
			fixture: func() *checkfixture.Fixture {
				f := fixtureFor("2", "0")
				f.Vars["WorkloadNamespace"] = "elsewhere"
				f.Results = map[string]bool{"app-ready": true, "learner-scaled": false}
				return f
			},
			want: []string{"check app-ready asks the lab something new: kubectl get deployment/go-api --namespace elsewhere -o jsonpath={.status.readyReplicas}: " + checkfixture.ErrNotRecorded.Error()},
		},
		{
			name: "a check added and a check removed since recording", state: "untouched",
			fixture: func() *checkfixture.Fixture {
				f := fixtureFor("2", "0")
				f.Results = map[string]bool{"learner-scaled": false, "retired": true}
				return f
			},
			want: []string{
				"check app-ready is not in the fixture; re-record it",
				"the fixture records check retired, which the scenario no longer declares; re-record it",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := replayProblems(t, e, "replay-demo", tc.state, tc.fixture())
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("problems =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
	if got := replayProblems(t, e, "nope", "untouched", checkfixture.New()); len(got) != 1 {
		t.Errorf("an unknown scenario reported %q", got)
	}
}
