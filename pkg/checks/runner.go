// SPDX-License-Identifier: Apache-2.0
package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ExecFunc runs an external command and returns its stdout. Tests inject a
// stub; the default implementation shells out (golden rule 2: the CLI
// orchestrates, external tools do the work).
type ExecFunc func(ctx context.Context, name string, args ...string) (string, error)

// Runner executes checks. The zero value is not usable; construct with
// NewRunner and override fields as needed.
type Runner struct {
	HTTPClient     *http.Client
	Exec           ExecFunc
	PrometheusURL  string        // base URL of the Prometheus HTTP API
	ScriptDir      string        // base dir for relative script paths (the scenario dir)
	Env            []string      // extra KEY=VALUE pairs for script checks
	DefaultTimeout time.Duration // per-check timeout when the check doesn't set one
}

// NewRunner returns a Runner with production defaults.
func NewRunner() *Runner {
	r := &Runner{
		HTTPClient:     &http.Client{},
		DefaultTimeout: 30 * time.Second,
	}
	r.Exec = r.defaultExec
	return r
}

func (r *Runner) defaultExec(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), r.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			return stdout.String(), fmt.Errorf("%s: %w", name, err)
		}
		return stdout.String(), fmt.Errorf("%s: %w: %s", name, err, msg)
	}
	return stdout.String(), nil
}

// RunAll executes every check in order and returns one result per check.
// A failing check does not stop the run — operators want the full picture.
func (r *Runner) RunAll(ctx context.Context, cs []Check) []Result {
	results := make([]Result, 0, len(cs))
	for _, c := range cs {
		results = append(results, r.Run(ctx, c))
	}
	return results
}

// Run executes a single check, honoring its timeout.
func (r *Runner) Run(ctx context.Context, c Check) Result {
	timeout := r.DefaultTimeout
	if c.TimeoutSeconds > 0 {
		timeout = time.Duration(c.TimeoutSeconds) * time.Second
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	start := time.Now()
	res := Result{Name: c.Name, Type: c.Type}

	if err := c.Validate(); err != nil {
		res.Error = err.Error()
	} else {
		switch c.Type {
		case TypeHTTP:
			res = r.runHTTP(ctx, c)
		case TypeKubectl:
			res = r.runKubectl(ctx, c)
		case TypePromQL:
			res = r.runPromQL(ctx, c)
		case TypeScript:
			res = r.runScript(ctx, c)
		}
	}

	if res.Attempts == 0 {
		res.Attempts = 1
	}
	res.Explanation = res.explain()
	res.DurationMS = time.Since(start).Milliseconds()
	return res
}

func (r *Runner) runHTTP(ctx context.Context, c Check) Result {
	res := Result{Name: c.Name, Type: c.Type}
	wantStatus := c.ExpectStatus
	if wantStatus == 0 {
		wantStatus = http.StatusOK
	}
	res.Want = fmt.Sprintf("status %d", wantStatus)
	if c.BodyContains != "" {
		res.Want += fmt.Sprintf(", body contains %q", c.BodyContains)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	res.Got = fmt.Sprintf("status %d", resp.StatusCode)
	res.Pass = resp.StatusCode == wantStatus
	if res.Pass && c.BodyContains != "" && !strings.Contains(string(body), c.BodyContains) {
		res.Pass = false
		res.Got += ", body does not contain expected text"
	}
	return res
}

func (r *Runner) runKubectl(ctx context.Context, c Check) Result {
	res := Result{Name: c.Name, Type: c.Type}

	args := []string{"get", c.Resource}
	if c.Namespace != "" {
		args = append(args, "--namespace", c.Namespace)
	}
	if c.JSONPath == "" {
		// Existence check: at least one matching resource.
		args = append(args, "-o", "name")
		res.Want = fmt.Sprintf("%s exists", c.Resource)
		out, err := r.Exec(ctx, "kubectl", args...)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		got := strings.TrimSpace(out)
		res.Pass = got != ""
		if res.Pass {
			res.Got = firstLine(got)
		} else {
			res.Got = "no matching resources"
		}
		return res
	}

	args = append(args, "-o", "jsonpath="+c.JSONPath)
	res.Want = fmt.Sprintf("%s %s", c.Operator, c.Value)
	out, err := r.Exec(ctx, "kubectl", args...)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	got := strings.TrimSpace(out)
	res.Got = got
	pass, err := compare(got, c.Operator, c.Value)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Pass = pass
	return res
}

func (r *Runner) runPromQL(ctx context.Context, c Check) Result {
	res := Result{Name: c.Name, Type: c.Type, Want: fmt.Sprintf("%s %s", c.Operator, c.Value)}
	if r.PrometheusURL == "" {
		res.Error = "no Prometheus URL configured (set PROMETHEUS_URL)"
		return res
	}

	endpoint := strings.TrimSuffix(r.PrometheusURL, "/") + "/api/v1/query?query=" + url.QueryEscape(c.Query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		res.Error = fmt.Sprintf("prometheus returned status %d", resp.StatusCode)
		return res
	}

	var pr struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []interface{} `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		res.Error = fmt.Sprintf("decoding prometheus response: %v", err)
		return res
	}
	if pr.Status != "success" {
		res.Error = fmt.Sprintf("prometheus query status %q", pr.Status)
		return res
	}
	if len(pr.Data.Result) == 0 {
		res.Got = "no samples"
		return res
	}
	v := pr.Data.Result[0].Value
	if len(v) < 2 {
		res.Error = "malformed prometheus sample"
		return res
	}
	got, ok := v[1].(string)
	if !ok {
		res.Error = "malformed prometheus sample value"
		return res
	}
	res.Got = got
	pass, err := compare(got, c.Operator, c.Value)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Pass = pass
	return res
}

func (r *Runner) runScript(ctx context.Context, c Check) Result {
	res := Result{Name: c.Name, Type: c.Type, Want: "exit 0"}
	path := c.Script
	if !filepath.IsAbs(path) && r.ScriptDir != "" {
		path = filepath.Join(r.ScriptDir, path)
	}
	out, err := r.Exec(ctx, "bash", path)
	if err != nil {
		res.Got = "non-zero exit"
		res.Error = err.Error()
		return res
	}
	res.Pass = true
	res.Got = "exit 0"
	_ = out
	return res
}

// compare evaluates `got <op> want`. Ordering operators require numbers;
// equality falls back to string comparison when either side isn't numeric.
func compare(got, op, want string) (bool, error) {
	if op == "contains" {
		return strings.Contains(got, want), nil
	}

	gf, gerr := strconv.ParseFloat(got, 64)
	wf, werr := strconv.ParseFloat(want, 64)
	numeric := gerr == nil && werr == nil

	switch op {
	case "==":
		if numeric {
			return gf == wf, nil
		}
		return got == want, nil
	case "!=":
		if numeric {
			return gf != wf, nil
		}
		return got != want, nil
	case "<", "<=", ">", ">=":
		if !numeric {
			return false, fmt.Errorf("operator %q needs numeric operands, got %q and %q", op, got, want)
		}
		switch op {
		case "<":
			return gf < wf, nil
		case "<=":
			return gf <= wf, nil
		case ">":
			return gf > wf, nil
		default:
			return gf >= wf, nil
		}
	default:
		return false, fmt.Errorf("unknown operator %q", op)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
