// SPDX-License-Identifier: Apache-2.0

package cli

// The CLI harness: the real command tree, run in-process over the fixture lab
// with stand-in tools on PATH, with what a user would read on the terminal
// compared against a golden transcript.

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/snowops-oss/snowopslabs/internal/labtest"
)

var updateGolden = flag.Bool("update", false, "rewrite the CLI golden transcripts under testdata/golden")

// terminal is one test's lab and the prompt it types labctl commands at.
type terminal struct {
	*labtest.Lab
	t *testing.T
	// ingress answers every lab URL, so no check or status query leaves the
	// test. A test adds routes to serve what its command reads.
	ingress *http.ServeMux
	// port is the host port the lab's ingress listens on.
	port string
	// stdin is what the next command reads when it prompts.
	stdin string
}

// cliResult is one command as the user saw it.
type cliResult struct {
	args   []string
	stdout string
	stderr string
	err    error
}

// exitCode is the status the labctl process would end with.
func (r cliResult) exitCode() int {
	if r.err != nil {
		return 1
	}
	return 0
}

// newTerminal builds a lab whose ingress is a local test server. The lab binds
// to the fixture's demo app, as a user's .env does with APP_NAME, and keeps
// the default <cluster>.localhost hostnames, which need no /etc/hosts entry.
func newTerminal(t *testing.T) *terminal {
	t.Helper()
	logPollInterval = 5 * time.Millisecond
	l := &terminal{Lab: labtest.New(t), t: t, ingress: http.NewServeMux()}
	srv := httptest.NewServer(l.ingress)
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	l.port = port
	l.Write(t, ".env", "APP_NAME="+labtest.App+"\nCLUSTER_NAME=clitest\nHTTP_PORT="+port+"\n")
	return l
}

// run types `labctl <args>` and returns what it printed and how it ended.
func (l *terminal) run(args ...string) cliResult {
	l.t.Helper()
	return l.runCtx(l.t.Context(), args...)
}

// runCtx is run with the context labctl's signal handler would give the
// command: cancelling it is the user pressing Ctrl-C.
func (l *terminal) runCtx(ctx context.Context, args ...string) cliResult {
	l.t.Helper()
	dir := l.t.TempDir()
	open := func(name string) *os.File {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			l.t.Fatal(err)
		}
		return f
	}
	stdout, stderr, stdin := open("stdout"), open("stderr"), open("stdin")
	if _, err := stdin.WriteString(l.stdin); err != nil {
		l.t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		l.t.Fatal(err)
	}
	l.stdin = ""

	// Commands print through cobra, straight to os.Stdout, and through the
	// script executor; all three write to the process's own streams.
	savedOut, savedErr, savedIn, savedLog := os.Stdout, os.Stderr, os.Stdin, slog.Default()
	os.Stdout, os.Stderr, os.Stdin = stdout, stderr, stdin
	resetCommands(ctx, rootCmd)
	// With no writers of its own cobra uses the process's, as labctl does.
	rootCmd.SetOut(nil)
	rootCmd.SetErr(nil)
	rootCmd.SetIn(nil)
	rootCmd.SetArgs(append([]string{"--project-dir", l.Root}, args...))

	err := rootCmd.ExecuteContext(ctx)

	os.Stdout, os.Stderr, os.Stdin = savedOut, savedErr, savedIn
	slog.SetDefault(savedLog)
	rootCmd.SetArgs(nil)

	read := func(f *os.File) string {
		_ = f.Close()
		data, rerr := os.ReadFile(f.Name())
		if rerr != nil {
			l.t.Fatal(rerr)
		}
		return string(data)
	}
	_ = stdin.Close()
	return cliResult{args: args, stdout: read(stdout), stderr: read(stderr), err: err}
}

// mustRun is run for a step the test only needs to succeed.
func (l *terminal) mustRun(args ...string) cliResult {
	l.t.Helper()
	r := l.run(args...)
	if r.err != nil {
		l.t.Fatalf("labctl %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), r.err, r.stdout, r.stderr)
	}
	return r
}

// resetCommands puts the command tree back as a new labctl process finds it:
// every flag at its default, usage shown on a mistake, and this test's
// context. The tree is package state, so without this what one command set
// stays set for the next.
func resetCommands(ctx context.Context, c *cobra.Command) {
	reset := func(f *pflag.Flag) {
		if f.Value.Type() == "stringToString" {
			// pflag merges into a map flag once it has been set and offers
			// no way to undo that, so the one such flag is emptied by name.
			if c != scenarioUpCmd || f.Name != "set" {
				panic("resetCommands does not know how to reset the map flag --" + f.Name + " of " + c.CommandPath())
			}
			clear(scenarioUpParams)
		} else if sv, ok := f.Value.(pflag.SliceValue); ok {
			var def []string
			if inner := strings.Trim(f.DefValue, "[]"); inner != "" {
				def = strings.Split(inner, ",")
			}
			_ = sv.Replace(def)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	c.Flags().VisitAll(reset)
	c.PersistentFlags().VisitAll(reset)
	c.SilenceUsage = false
	c.SetContext(ctx)
	for _, sub := range c.Commands() {
		resetCommands(ctx, sub)
	}
}

// Values that differ between runs, replaced in a transcript so it can be
// compared.
var (
	rfc3339Re  = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
	minuteRe   = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}(:\d{2})?`)
	dateRe     = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
	runIDRe    = regexp.MustCompile(`\brun_\d+_[0-9a-f]+\b`)
	uuidRe     = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	durationRe = regexp.MustCompile(`\b\d+(\.\d+)?(ms|µs|ns)\b|\b\d+(\.\d+)?s\b`)
	portRe     = regexp.MustCompile(`(127\.0\.0\.1|localhost):\d{4,5}`)
	// A followed run's header shows its state when printed, which depends on
	// whether the engine has picked it up yet; the summary line pins the outcome.
	runStateRe = regexp.MustCompile(`(?m)^(Run <run> - .+) \((queued|running|succeeded|failed|cancelled|timed_out)\)$`)
	trailingWS = regexp.MustCompile(`[ \t]+\n`)
)

// normalise replaces the paths, times, IDs and ports that change from run to
// run with placeholders.
func (l *terminal) normalise(s string) string {
	for path, name := range map[string]string{l.Root: "<lab>", l.Home: "<home>", l.Bin: "<bin>"} {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			s = strings.ReplaceAll(s, resolved, name)
		}
		s = strings.ReplaceAll(s, path, name)
	}
	s = rfc3339Re.ReplaceAllString(s, "<time>")
	s = minuteRe.ReplaceAllString(s, "<when>")
	s = dateRe.ReplaceAllString(s, "<date>")
	s = runIDRe.ReplaceAllString(s, "<run>")
	s = runStateRe.ReplaceAllString(s, "$1 (<state>)")
	s = uuidRe.ReplaceAllString(s, "<id>")
	s = durationRe.ReplaceAllString(s, "<dur>")
	s = portRe.ReplaceAllString(s, "$1:<port>")
	s = regexp.MustCompile(`\b`+l.port+`\b`).ReplaceAllString(s, "<port>")
	return trailingWS.ReplaceAllString(s, "\n")
}

// transcript renders commands the way a terminal session reads: the command,
// what it printed, what it wrote to stderr, and a non-zero exit status.
func (l *terminal) transcript(results ...cliResult) string {
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "$ labctl %s\n", strings.Join(r.args, " "))
		b.WriteString(l.normalise(r.stdout))
		if r.stderr != "" {
			b.WriteString("[stderr]\n" + l.normalise(r.stderr))
		}
		if r.err != nil {
			fmt.Fprintf(&b, "[exit %d]\n", r.exitCode())
		}
	}
	return b.String()
}

// golden compares a transcript of results with testdata/golden/<name>.txt.
// With -update it rewrites the file instead.
func (l *terminal) golden(name string, results ...cliResult) {
	l.t.Helper()
	got := l.transcript(results...)
	path := filepath.Join("testdata", "golden", name+".txt")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // test data directory
			l.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil { //nolint:gosec // test data file
			l.t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		l.t.Fatalf("no golden transcript %s: run `make golden-update` and review it\n--- got ---\n%s", path, got)
	}
	if got != string(want) {
		l.t.Errorf("the terminal output differs from %s.\nIf the change is intended, run `make golden-update` and review the diff.\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// exists reports whether a path under the project root exists.
func (l *terminal) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(l.Root, rel))
	return err == nil
}

// remove deletes a path under the project root: the learner fixing the lab by
// hand.
func (l *terminal) remove(rel string) {
	l.t.Helper()
	if err := os.Remove(filepath.Join(l.Root, rel)); err != nil {
		l.t.Fatal(err)
	}
}

// undeploy makes the cluster answer as if app were not deployed: every
// kubectl call that names it fails, and the rest answer as before.
func (l *terminal) undeploy(app string) {
	l.t.Helper()
	if err := os.Rename(filepath.Join(l.Bin, "kubectl"), filepath.Join(l.Bin, "kubectl.deployed")); err != nil {
		l.t.Fatal(err)
	}
	l.Stub(l.t, "kubectl", "#!/bin/sh\ncase \"$*\" in *"+app+"*) exit 1 ;; esac\nexec \"$(dirname \"$0\")/kubectl.deployed\" \"$@\"\n")
}

// clusterDown makes kubectl answer as it does when the API server is not
// listening: the context is still configured, and every call that needs the
// server fails.
func (l *terminal) clusterDown() {
	l.t.Helper()
	l.Stub(l.t, "kubectl", `#!/bin/sh
case "$*" in
  "config current-context") echo k3d-clitest; exit 0 ;;
  "config view"*) echo https://127.0.0.1:6443; exit 0 ;;
esac
echo 'The connection to the server 127.0.0.1:6443 was refused - did you specify the right host or port?' >&2
exit 1
`)
}

// serveGrafana makes the lab's ingress answer as a healthy Grafana does: the
// health endpoint `init` uses to prove the port reaches this lab, and the
// Prometheus datasource check behind every dashboard.
func (l *terminal) serveGrafana(datasourceStatus string) {
	l.ingress.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"database":"ok"}`))
	})
	l.ingress.HandleFunc("/api/datasources/uid/prometheus/health", func(w http.ResponseWriter, _ *http.Request) {
		if datasourceStatus != "OK" {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_, _ = w.Write([]byte(`{"status":"` + datasourceStatus + `","message":"connection refused"}`))
	})
}
