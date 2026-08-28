// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/traffic"
)

// newTrafficServer builds a server whose project root carries a couple of k6
// profiles, so profile discovery and validation exercise the real filesystem.
func newTrafficServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, traffic.ScriptDir, "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"steady", "spike", "soak"} {
		if err := os.WriteFile(filepath.Join(dir, name+".js"), []byte("// k6"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Server{cfg: &config.Config{ProjectRoot: root}}
}

func TestHandleTrafficInfo_ListsProfiles(t *testing.T) {
	s := newTrafficServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/traffic", nil)
	w := httptest.NewRecorder()
	s.handleTrafficInfo(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /traffic = %d, want 200", w.Code)
	}
	var body struct {
		Profiles []string `json:"profiles"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Sorted by discovery: soak, spike, steady.
	if got := strings.Join(body.Profiles, ","); got != "soak,spike,steady" {
		t.Errorf("profiles = %q, want soak,spike,steady", got)
	}
	if w.Header().Get("ETag") == "" {
		t.Error("traffic info should carry an ETag")
	}
}

// startBody posts a JSON body to handleTrafficStart and returns the recorder.
func postStart(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/traffic/start", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleTrafficStart(w, req)
	return w
}

func TestHandleTrafficStart_RejectsBadInput(t *testing.T) {
	s := newTrafficServer(t)
	cases := []struct {
		name string
		body string
		code int
	}{
		{"malformed json", "{not json", http.StatusBadRequest},
		{"unknown profile", `{"profile":"nope","rps":10}`, http.StatusBadRequest},
		{"missing profile", `{"rps":10}`, http.StatusBadRequest},
		{"rps too low", `{"profile":"steady","rps":0}`, http.StatusBadRequest},
		{"rps too high", `{"profile":"steady","rps":99999}`, http.StatusBadRequest},
		{"bad duration", `{"profile":"steady","rps":10,"duration":"soon"}`, http.StatusBadRequest},
		{"non-http target", `{"profile":"steady","rps":10,"target":"ftp://x"}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := postStart(t, s, c.body).Code; got != c.code {
				t.Errorf("body %q: got %d, want %d", c.body, got, c.code)
			}
		})
	}
}
