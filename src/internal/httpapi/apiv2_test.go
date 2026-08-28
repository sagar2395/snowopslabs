// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Both the versioned (/api/v2) and unversioned (/api) prefixes must serve the
// same route table, so the current UI keeps working while third parties can pin
// /api/v2. This exercises the real router so the registration-order guard
// (v2 registered before /api) is actually covered.
func TestAPIVersions_BothPrefixesServeSameRoute(t *testing.T) {
	s := newChallengeServer(t)
	s.setupRoutes()

	for _, prefix := range []string{"/api", "/api/v2"} {
		path := prefix + "/challenges/status"
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: got %d, want 200", path, w.Code)
		}
	}
}

func TestRequestID_GeneratedAndEchoed(t *testing.T) {
	s := newChallengeServer(t)
	s.setupRoutes()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/challenges/status", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if got := w.Header().Get(requestIDHeader); got == "" {
		t.Fatalf("expected a generated %s header, got none", requestIDHeader)
	}
}

func TestRequestID_HonoursSaneInbound(t *testing.T) {
	s := newChallengeServer(t)
	s.setupRoutes()

	const want = "trace-abc-123"
	req := httptest.NewRequest(http.MethodGet, "/api/v2/challenges/status", nil)
	req.Header.Set(requestIDHeader, want)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if got := w.Header().Get(requestIDHeader); got != want {
		t.Fatalf("inbound request ID: got %q, want it echoed back as %q", got, want)
	}
}

func TestRequestID_RejectsGarbageInbound(t *testing.T) {
	s := newChallengeServer(t)
	s.setupRoutes()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/challenges/status", nil)
	req.Header.Set(requestIDHeader, "bad\x00id\nwith-controls")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	got := w.Header().Get(requestIDHeader)
	if got == "" {
		t.Fatal("expected a freshly minted request ID, got none")
	}
	if strings.ContainsAny(got, "\x00\n") {
		t.Fatalf("garbage inbound ID leaked into the response: %q", got)
	}
}

func TestSanitizeRequestID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "abc-123", "abc-123"},
		{"control char", "a\x01b", ""},
		{"newline", "a\nb", ""},
		{"non-ascii", "café", ""},
		{"too long", strings.Repeat("x", maxInboundRequestID+1), ""},
		{"at limit", strings.Repeat("x", maxInboundRequestID), strings.Repeat("x", maxInboundRequestID)},
	}
	for _, c := range cases {
		if got := sanitizeRequestID(c.in); got != c.want {
			t.Errorf("%s: sanitizeRequestID(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestRequestIDFrom(t *testing.T) {
	if got := requestIDFrom(context.Background()); got != "" {
		t.Errorf("empty context: got %q, want empty", got)
	}
	ctx := context.WithValue(context.Background(), requestIDKey, "xyz")
	if got := requestIDFrom(ctx); got != "xyz" {
		t.Errorf("populated context: got %q, want xyz", got)
	}
}

// The access log must emit one structured line per request carrying the
// correlation ID, so an operator can grep logs by the X-Request-ID a client
// saw. We capture the default slog output for the duration of one request.
func TestAccessLog_EmitsStructuredLineWithRequestID(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := newChallengeServer(t)
	s.setupRoutes()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/challenges/status", nil)
	req.Header.Set(requestIDHeader, "log-me-42")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	out := buf.String()
	if !strings.Contains(out, `"msg":"http_request"`) {
		t.Fatalf("expected an http_request access-log line, got: %s", out)
	}
	if !strings.Contains(out, `"request_id":"log-me-42"`) {
		t.Fatalf("access log missing the correlation ID, got: %s", out)
	}
	if !strings.Contains(out, `"route":"/api/v2/challenges/status"`) {
		t.Fatalf("access log missing the bounded route template, got: %s", out)
	}
}

// A websocket upgrade must be logged once at open and passed through untouched,
// rather than timed like a normal request (its lifetime is the socket's).
func TestAccessLog_WebsocketLoggedAtOpen(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := &Server{}
	called := false
	h := s.accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v2/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !called {
		t.Fatal("upgrade request should pass through to the handler")
	}
	if !strings.Contains(buf.String(), `"msg":"http_ws_open"`) {
		t.Fatalf("expected an http_ws_open line, got: %s", buf.String())
	}
	if strings.Contains(buf.String(), `"msg":"http_request"`) {
		t.Fatalf("websocket must not emit the timed http_request line, got: %s", buf.String())
	}
}

func TestClientAddr(t *testing.T) {
	cases := map[string]string{
		"192.0.2.1:54321": "192.0.2.1",
		"192.0.2.1":       "192.0.2.1",
		"":                "",
	}
	for in, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = in
		if got := clientAddr(r); got != want {
			t.Errorf("clientAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
