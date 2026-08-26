// SPDX-License-Identifier: Apache-2.0
package traffic

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeProject creates a project root containing the given profile names.
func fakeProject(t *testing.T, profiles ...string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ScriptDir, "profiles")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if err := os.WriteFile(filepath.Join(dir, p+".js"), []byte("// stub"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestProfiles(t *testing.T) {
	root := fakeProject(t, "steady", "spike", "soak")
	got, err := Profiles(root)
	if err != nil {
		t.Fatalf("Profiles: %v", err)
	}
	want := []string{"soak", "spike", "steady"} // sorted
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Profiles = %v, want %v", got, want)
	}
}

func TestValidate(t *testing.T) {
	root := fakeProject(t, "steady", "spike")

	tests := []struct {
		name    string
		opts    Options
		wantErr string // "" = valid
	}{
		{"defaults are valid", Options{Profile: "steady", RPS: 10}, ""},
		{"full options valid", Options{Profile: "spike", RPS: 50, Duration: "1h30m", Target: "http://echo-server.k3d.local/"}, ""},
		{"missing profile", Options{RPS: 10}, "profile is required"},
		{"unknown profile", Options{Profile: "tsunami", RPS: 10}, `unknown profile "tsunami"`},
		{"unknown profile lists available", Options{Profile: "tsunami", RPS: 10}, "spike, steady"},
		{"zero rps", Options{Profile: "steady", RPS: 0}, "rps must be between"},
		{"absurd rps", Options{Profile: "steady", RPS: 50000}, "rps must be between"},
		{"bad duration", Options{Profile: "steady", RPS: 10, Duration: "10 minutes"}, "invalid duration"},
		{"negative duration", Options{Profile: "steady", RPS: 10, Duration: "-5m"}, "duration must be positive"},
		{"non-http target", Options{Profile: "steady", RPS: 10, Target: "go-api.k3d.local"}, "must be an http(s) URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate(root)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestEnv(t *testing.T) {
	full := Options{Profile: "spike", RPS: 25, Duration: "30m", Target: "http://x/health"}
	env := full.Env()
	want := map[string]string{
		"TRAFFIC_PROFILE":  "spike",
		"TRAFFIC_RPS":      "25",
		"TRAFFIC_DURATION": "30m",
		"TRAFFIC_TARGET":   "http://x/health",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("Env = %v, want %v", env, want)
	}

	// Unset knobs must be omitted so script defaults apply.
	minimal := Options{Profile: "steady", RPS: 10}
	env = minimal.Env()
	if _, ok := env["TRAFFIC_TARGET"]; ok {
		t.Error("empty target must not be exported")
	}
	if _, ok := env["TRAFFIC_DURATION"]; ok {
		t.Error("empty duration must not be exported")
	}
}

// TestRepoProfilesExist pins the shipped profiles: removing one breaks the
// CLI's documented surface, so CI must notice.
func TestRepoProfilesExist(t *testing.T) {
	root := repoRoot(t)
	got, err := Profiles(root)
	if err != nil {
		t.Fatalf("Profiles: %v", err)
	}
	for _, want := range []string{"steady", "spike", "soak"} {
		found := false
		for _, p := range got {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("shipped profile %q missing from %v", want, got)
		}
	}
}
