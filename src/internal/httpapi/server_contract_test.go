// SPDX-License-Identifier: Apache-2.0

package httpapi

// The route contract: every route the server registers has a row here saying
// what a correct request answers, what a wrong one answers, and who may call
// it. TestContract_EveryRouteHasARow fails for a route with no row, so a new
// route cannot ship without one.

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/mux"

	"github.com/snowops-oss/snowopslabs/internal/auth"
	"github.com/snowops-oss/snowopslabs/internal/labtest"
)

var updateGolden = flag.Bool("update", false, "rewrite the contract golden files under testdata/contract")

const apiPrefix = "/api/v2"

// contractRow is one route's contract.
type contractRow struct {
	method string
	route  string // the route template, without /api/v2

	// The correct request and its answer.
	path   string // concrete path; "" means the route itself
	body   string // JSON request body
	status int
	// prepare puts the lab in the state the request needs. A path it returns
	// replaces path, for a route whose ID only exists once the lab is prepared.
	prepare func(t *testing.T, s *Server) string
	// authOn builds the lab with auth enabled, for the routes that only
	// answer usefully then.
	authOn bool

	// One wrong request and the problem it answers.
	badPath string
	badBody string
	problem string // the problem type slug; "" when the route has no wrong request
	code    int

	// operatorOnly routes refuse a participant when auth is on.
	operatorOnly bool
}

func (r contractRow) key() string { return r.method + " " + r.route }

func (r contractRow) goodPath() string {
	if r.path != "" {
		return r.path
	}
	return r.route
}

// goldenName is the file the success answer is compared against.
func (r contractRow) goldenName() string {
	name := strings.NewReplacer("/", "_", "{", "", "}", "").Replace(strings.TrimPrefix(r.route, "/"))
	return r.method + "_" + name + ".json"
}

// do sends a request through the whole router, middleware included.
func do(s *Server, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, apiPrefix+path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

// waitForJobs blocks until no background job the API started is still running,
// so a test never ends with a script writing into its temp directory.
func waitForJobs(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		running := 0
		for _, j := range s.exec.Broadcast.Jobs() {
			if j.Status == "running" {
				running++
			}
		}
		if running == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d background job(s) still running after 20s", running)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// registeredRoutes lists every method and route template the server serves
// under /api/v2, apart from CORS preflight.
func registeredRoutes(t *testing.T, s *Server) []string {
	t.Helper()
	var out []string
	err := s.router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		tpl, err := route.GetPathTemplate()
		if err != nil || !strings.HasPrefix(tpl, apiPrefix+"/") {
			return nil //nolint:nilerr // a route with no template is not an API route
		}
		methods, err := route.GetMethods()
		if err != nil {
			methods = []string{http.MethodGet} // the WebSocket route registers no methods
		}
		for _, m := range methods {
			if m != http.MethodOptions {
				out = append(out, m+" "+strings.TrimPrefix(tpl, apiPrefix))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the router: %v", err)
	}
	sort.Strings(out)
	return out
}

func TestContract_EveryRouteHasARow(t *testing.T) {
	s := newContractLab(t)
	rows := map[string]bool{}
	for _, r := range contract {
		if rows[r.key()] {
			t.Errorf("route %s has two contract rows", r.key())
		}
		rows[r.key()] = true
	}
	for _, key := range streamingRoutes {
		rows[key] = true
	}

	registered := map[string]bool{}
	for _, key := range registeredRoutes(t, s) {
		registered[key] = true
		if !rows[key] {
			t.Errorf("route %s has no contract row: add one to the contract table in server_contract_test.go", key)
		}
	}
	for key := range rows {
		if !registered[key] {
			t.Errorf("contract row %s matches no registered route: remove it or fix its method and template", key)
		}
	}
}

// labFor builds the lab a row asks for.
func labFor(t *testing.T, row contractRow) *Server {
	t.Helper()
	if row.authOn {
		s, _, _ := newAuthedLab(t)
		return s
	}
	return newContractLab(t)
}

func TestContract_Success(t *testing.T) {
	for _, row := range contract {
		t.Run(row.key(), func(t *testing.T) {
			s := labFor(t, row)
			path := row.goodPath()
			if row.prepare != nil {
				if p := row.prepare(t, s); p != "" {
					path = p
				}
			}
			w := do(s, row.method, path, row.body, nil)
			waitForJobs(t, s)

			if w.Code != row.status {
				t.Fatalf("status = %d, want %d\nbody: %s", w.Code, row.status, w.Body)
			}
			if w.Header().Get("X-Request-ID") == "" {
				t.Error("the response carries no X-Request-ID")
			}
			if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			compareGolden(t, s, row.goldenName(), w.Body.Bytes())
		})
	}
}

func TestContract_Problems(t *testing.T) {
	for _, row := range contract {
		if row.problem == "" {
			continue
		}
		t.Run(row.key(), func(t *testing.T) {
			s := labFor(t, row)
			path := row.badPath
			if path == "" {
				path = row.goodPath()
			}
			w := do(s, row.method, path, row.badBody, nil)
			waitForJobs(t, s)
			assertProblem(t, w, row.code, row.problem, instanceOf(t, path))
		})
	}
}

// instanceOf is the problem `instance` a request to path carries: the decoded
// path, without the query.
func instanceOf(t *testing.T, path string) string {
	t.Helper()
	decoded, err := url.PathUnescape(strings.SplitN(path, "?", 2)[0])
	if err != nil {
		t.Fatalf("path %q: %v", path, err)
	}
	return apiPrefix + decoded
}

// assertProblem checks an error answer against the problem+json contract.
func assertProblem(t *testing.T, w *httptest.ResponseRecorder, status int, slug, instance string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d\nbody: %s", w.Code, status, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json\nbody: %s", ct, w.Body)
	}
	var p struct {
		Type      string `json:"type"`
		Title     string `json:"title"`
		Status    int    `json:"status"`
		Detail    string `json:"detail"`
		Instance  string `json:"instance"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("the error body is not JSON: %v\nbody: %s", err, w.Body)
	}
	if want := problemTypeBase + slug; p.Type != want {
		t.Errorf("type = %q, want %q", p.Type, want)
	}
	if p.Status != status {
		t.Errorf("status field = %d, want %d", p.Status, status)
	}
	if p.Title == "" || p.Detail == "" {
		t.Errorf("title and detail must both say something, got title=%q detail=%q", p.Title, p.Detail)
	}
	if p.Instance != instance {
		t.Errorf("instance = %q, want %q", p.Instance, instance)
	}
	if p.RequestID == "" || p.RequestID != w.Header().Get("X-Request-ID") {
		t.Errorf("requestId = %q, want the X-Request-ID header %q", p.RequestID, w.Header().Get("X-Request-ID"))
	}
	if strings.Contains(p.Detail, os.TempDir()) || strings.Contains(p.Detail, "/var/folders/") {
		t.Errorf("the error detail leaks a filesystem path: %q", p.Detail)
	}
}

func TestContract_UnservedMethodIsA405Problem(t *testing.T) {
	s := newContractLab(t)
	served := map[string]map[string]bool{}
	for _, r := range contract {
		if served[r.route] == nil {
			served[r.route] = map[string]bool{}
		}
		served[r.route][r.method] = true
	}
	for _, row := range contract {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			if served[row.route][method] {
				continue
			}
			path := row.goodPath()
			w := do(s, method, path, "", nil)
			if w.Code != http.StatusMethodNotAllowed && w.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 405 (or 404 when another route owns the path)", method, path, w.Code)
				continue
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("%s %s answered %q, want application/problem+json", method, path, ct)
			}
		}
	}
	waitForJobs(t, s)
}

// newAuthedLab builds the lab with auth on, one operator and one participant,
// and returns each one's session cookie.
func newAuthedLab(t *testing.T) (s *Server, operator, participant *http.Cookie) {
	t.Helper()
	hash, err := auth.HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	users := filepath.Join(t.TempDir(), "users.yaml")
	content := "users:\n" +
		"  - name: olive\n    role: operator\n    passwordHash: " + hash + "\n" +
		"  - name: pat\n    role: participant\n    passwordHash: " + hash + "\n"
	if err := os.WriteFile(users, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	lab := labtest.New(t)
	t.Setenv("LABCTL_AUTH", "true")
	t.Setenv("LABCTL_USERS_FILE", users)
	s = contractServer(t, lab)

	session := func(user string) *http.Cookie {
		w := do(s, http.MethodPost, "/auth/login", `{"username":"`+user+`","password":"pw"}`, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("login as %s = %d: %s", user, w.Code, w.Body)
		}
		for _, c := range w.Result().Cookies() {
			return c
		}
		t.Fatalf("login as %s set no cookie", user)
		return nil
	}
	return s, session("olive"), session("pat")
}

func TestContract_Auth(t *testing.T) {
	s, operator, participant := newAuthedLab(t)
	for _, row := range contract {
		if strings.HasPrefix(row.route, "/auth/") {
			continue // the auth endpoints are how a session is obtained
		}
		t.Run(row.key(), func(t *testing.T) {
			path := row.goodPath()

			anonymous := do(s, row.method, path, row.body, nil)
			assertProblem(t, anonymous, http.StatusUnauthorized, "unauthorized", instanceOf(t, path))

			asParticipant := do(s, row.method, path, row.body, participant)
			if row.operatorOnly {
				assertProblem(t, asParticipant, http.StatusForbidden, "forbidden_role", instanceOf(t, path))
			} else if asParticipant.Code == http.StatusUnauthorized || asParticipant.Code == http.StatusForbidden {
				t.Errorf("a participant is refused (%d) a route that is not operator-only", asParticipant.Code)
			}

			asOperator := do(s, row.method, path, row.body, operator)
			if asOperator.Code == http.StatusUnauthorized || asOperator.Code == http.StatusForbidden {
				t.Errorf("an operator is refused (%d)", asOperator.Code)
			}
			waitForJobs(t, s)
		})
	}
}

func TestContract_MutationsOutsideLearnerFlowsAreOperatorOnly(t *testing.T) {
	// A participant drives scenarios, incidents, challenges and learning paths.
	// Starting and ending the team's one incident is the facilitator's, so
	// those routes are an operator's, as is every other route that changes
	// something.
	learner := []string{"/scenarios", "/incidents", "/challenges", "/learn"}
	facilitator := []string{"/incidents/{name}/inject", "/incidents/inject-random", "/incidents/resolve"}
	for _, row := range contract {
		if row.method == http.MethodGet || strings.HasPrefix(row.route, "/auth/") {
			continue
		}
		open := false
		for _, p := range learner {
			if strings.HasPrefix(row.route, p) {
				open = true
			}
		}
		if slices.Contains(facilitator, row.route) {
			open = false
		}
		if open == row.operatorOnly {
			t.Errorf("%s: operatorOnly=%v, but a route %s the learner flows must be the opposite",
				row.key(), row.operatorOnly, map[bool]string{true: "inside", false: "outside"}[open])
		}
	}
}

var (
	timestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
	jobID     = regexp.MustCompile(`"(action|job)-[0-9a-f-]+"`)
)

// zeroTimings sets every measured duration in a decoded answer to zero: how
// long a check took differs from run to run and is not part of the contract.
func zeroTimings(v any) {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			if key == "durationMs" {
				node[key] = 0
				continue
			}
			zeroTimings(child)
		}
	case []any:
		for _, child := range node {
			zeroTimings(child)
		}
	}
}

// compareGolden compares a JSON answer with its golden file, after replacing
// what differs between runs: the temp project root, timestamps, job IDs and
// measured durations.
// With -update it rewrites the file instead.
func compareGolden(t *testing.T, s *Server, name string, body []byte) {
	t.Helper()
	text := strings.ReplaceAll(string(body), s.cfg.ProjectRoot, "<root>")
	text = timestamp.ReplaceAllString(text, "<time>")
	text = jobID.ReplaceAllString(text, `"<job>"`)

	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("the answer is not JSON: %v\nbody: %s", err, body)
	}
	zeroTimings(decoded)
	var got bytes.Buffer
	enc := json.NewEncoder(&got)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(decoded); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join("testdata", "contract", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil { //nolint:gosec // a golden file is world-readable test data
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden file %s: run 'make golden-update' and review the new file", path)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("the answer differs from %s.\ngot:\n%s\nwant:\n%s\nIf the change is intended, run 'make golden-update' and review the diff.",
			path, got.Bytes(), want)
	}
}

// contractIndexEntry tells a client of the golden files which request each
// one answers.
type contractIndexEntry struct {
	Method string `json:"method"`
	Route  string `json:"route"`
	Status int    `json:"status"`
	Golden string `json:"golden"`
}

// The index lists every golden answer with its method, route template and
// status. The UI tests read it to serve the server's real answers, so a change
// to a response shape here fails a test there.
func TestContract_IndexIsCurrent(t *testing.T) {
	index := make([]contractIndexEntry, 0, len(contract))
	for _, row := range contract {
		index = append(index, contractIndexEntry{Method: row.method, Route: row.route, Status: row.status, Golden: row.goldenName()})
	}
	var got bytes.Buffer
	enc := json.NewEncoder(&got)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(index); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join("testdata", "contract", "index.json")
	if *updateGolden {
		if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil { //nolint:gosec // a golden file is world-readable test data
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no %s: run 'make golden-update' and review the new file", path)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("%s is out of step with the contract table: run 'make golden-update' and review the diff", path)
	}

	// Every file in the directory is one the index names.
	named := map[string]bool{"index.json": true}
	for _, e := range index {
		named[e.Golden] = true
	}
	files, err := os.ReadDir(filepath.Join("testdata", "contract"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !named[f.Name()] {
			t.Errorf("testdata/contract/%s answers no contract row: delete it", f.Name())
		}
	}
}

// Every key the API sends is camelCase. An upper-case key is a Go field with
// no json tag: the client reads `action.type` and gets nothing.
func TestContract_KeysAreCamelCase(t *testing.T) {
	var walk func(golden, at string, v any)
	walk = func(golden, at string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				if r, _ := utf8.DecodeRuneInString(k); unicode.IsUpper(r) {
					t.Errorf("%s: key %q at %s starts with an upper-case letter; give the Go field a json tag", golden, k, at)
				}
				walk(golden, at+"."+k, child)
			}
		case []any:
			for _, child := range v {
				walk(golden, at+"[]", child)
			}
		}
	}
	for _, row := range contract {
		data, err := os.ReadFile(filepath.Join("testdata", "contract", row.goldenName()))
		if err != nil {
			t.Fatalf("%s: %v", row.goldenName(), err)
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			t.Fatalf("%s: %v", row.goldenName(), err)
		}
		walk(row.goldenName(), "$", v)
	}
}
