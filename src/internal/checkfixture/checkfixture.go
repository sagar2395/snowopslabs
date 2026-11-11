// SPDX-License-Identifier: Apache-2.0

// Package checkfixture records what a live lab answered a scenario's declared
// checks and replays it, so a test can grade the checks against a real
// cluster's state with no cluster. A fixture holds every kubectl answer and
// HTTP response the checks received, the template variables they were
// rendered with, and each check's result.
package checkfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/snowops-oss/snowopslabs/pkg/checks"
)

// Answer is what one kubectl call printed, and its error if it failed.
type Answer struct {
	Out string `json:"out"`
	Err string `json:"err,omitempty"`
}

// Response is the status, body and redirect target of one HTTP request.
type Response struct {
	Status   int    `json:"status"`
	Body     string `json:"body"`
	Location string `json:"location,omitempty"`
}

// Fixture is one recorded lab state.
type Fixture struct {
	// Vars are the template variables and parameters the checks were
	// rendered with.
	Vars map[string]string `json:"vars"`
	// PrometheusURL is the Prometheus the checks queried.
	PrometheusURL string `json:"prometheusURL"`
	// Kubectl maps a kubectl call's arguments, joined by spaces, to its answer.
	Kubectl map[string]Answer `json:"kubectl"`
	// HTTP maps a request, keyed by Key, to its response.
	HTTP map[string]Response `json:"http"`
	// Results maps each declared check to whether it passed. Script checks are
	// left out; their behaviour tests cover them.
	Results map[string]bool `json:"results"`

	mu sync.Mutex
}

// New returns an empty fixture.
func New() *Fixture {
	return &Fixture{
		Vars:    map[string]string{},
		Kubectl: map[string]Answer{},
		HTTP:    map[string]Response{},
		Results: map[string]bool{},
	}
}

// Key names an HTTP request by the first label of its host and its path and
// query, so a fixture recorded on one lab's port and domain replays on another.
func Key(u *url.URL) string {
	host := u.Hostname()
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	key := host + u.EscapedPath()
	if u.RawQuery != "" {
		key += "?" + u.RawQuery
	}
	return key
}

// Record makes r capture every kubectl answer and HTTP response into f, and
// returns r. Script checks still run; their output is not recorded.
func Record(r *checks.Runner, f *Fixture) *checks.Runner {
	f.PrometheusURL = r.PrometheusURL
	exec := r.Exec
	r.Exec = func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := exec(ctx, name, args...)
		if name == "kubectl" {
			a := Answer{Out: out}
			if err != nil {
				a.Err = err.Error()
			}
			f.mu.Lock()
			f.Kubectl[strings.Join(args, " ")] = a
			f.mu.Unlock()
		}
		return out, err
	}
	client := *r.HTTPClient
	next := client.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	client.Transport = recordingTransport{next: next, f: f}
	r.HTTPClient = &client
	return r
}

type recordingTransport struct {
	next http.RoundTripper
	f    *Fixture
}

func (t recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	t.f.mu.Lock()
	t.f.HTTP[Key(req.URL)] = Response{Status: resp.StatusCode, Body: string(body), Location: resp.Header.Get("Location")}
	t.f.mu.Unlock()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// SetResults records each declared check's outcome.
func (f *Fixture) SetResults(results []checks.Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range results {
		if r.Type != checks.TypeScript {
			f.Results[r.Name] = r.Pass
		}
	}
}

// ErrNotRecorded reports a call the fixture holds no answer for: the check
// asks the lab something it did not ask when the fixture was recorded.
var ErrNotRecorded = errors.New("not in the recorded fixture; re-record it on a lab (make test-cluster RECORD=1)")

// Replay returns a runner that answers every kubectl call and HTTP request
// from f, and fails any f did not record.
func Replay(f *Fixture) *checks.Runner {
	r := checks.NewRunner()
	r.Exec = func(_ context.Context, name string, args ...string) (string, error) {
		if name != "kubectl" {
			return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), ErrNotRecorded)
		}
		a, ok := f.Kubectl[strings.Join(args, " ")]
		if !ok {
			return "", fmt.Errorf("kubectl %s: %w", strings.Join(args, " "), ErrNotRecorded)
		}
		if a.Err != "" {
			return a.Out, errors.New(a.Err)
		}
		return a.Out, nil
	}
	r.HTTPClient = &http.Client{Transport: replayTransport{f: f}}
	r.PrometheusURL = f.PrometheusURL
	return r
}

type replayTransport struct{ f *Fixture }

func (t replayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, ok := t.f.HTTP[Key(req.URL)]
	if !ok {
		return nil, fmt.Errorf("%s: %w", Key(req.URL), ErrNotRecorded)
	}
	header := http.Header{}
	if resp.Location != "" {
		header.Set("Location", resp.Location)
	}
	return &http.Response{
		StatusCode: resp.Status,
		Body:       io.NopCloser(strings.NewReader(resp.Body)),
		Header:     header,
		Request:    req,
	}, nil
}

// Load reads a fixture written by Save.
func Load(path string) (*Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f := New()
	if err := json.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Save writes f to path as indented JSON, creating its directory.
func (f *Fixture) Save(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
