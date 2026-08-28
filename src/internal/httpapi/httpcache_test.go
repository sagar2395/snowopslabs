// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIfNoneMatch(t *testing.T) {
	const etag = `"abc123"`
	cases := []struct {
		header string
		want   bool
	}{
		{`"abc123"`, true},
		{`*`, true},
		{`"x", "abc123", "y"`, true},
		{`W/"abc123"`, true},
		{`"nope"`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := ifNoneMatch(c.header, etag); got != c.want {
			t.Errorf("ifNoneMatch(%q)=%v, want %v", c.header, got, c.want)
		}
	}
}

func TestEtagFor_StableAndContentSensitive(t *testing.T) {
	a := etagFor([]byte(`{"x":1}`))
	again := etagFor([]byte(`{"x":1}`))
	b := etagFor([]byte(`{"x":2}`))
	if a != again {
		t.Errorf("etag not stable for identical bytes: %q vs %q", a, again)
	}
	if a == b {
		t.Error("etag should differ for different bytes")
	}
	if len(a) == 0 || a[0] != '"' {
		t.Errorf("etag should be a quoted entity-tag, got %q", a)
	}
}

// A catalog read must carry an ETag, and a follow-up request that echoes it in
// If-None-Match must get 304 with no body — the whole point of conditional GET.
func TestCatalogRead_ETagThen304(t *testing.T) {
	s := newChallengeServer(t)
	s.setupRoutes()

	r1 := httptest.NewRequest(http.MethodGet, "/api/v2/challenges", nil)
	w1 := httptest.NewRecorder()
	s.router.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first GET: got %d, want 200", w1.Code)
	}
	etag := w1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("catalog read did not set an ETag")
	}

	r2 := httptest.NewRequest(http.MethodGet, "/api/v2/challenges", nil)
	r2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	s.router.ServeHTTP(w2, r2)
	if w2.Code != http.StatusNotModified {
		t.Fatalf("conditional GET: got %d, want 304", w2.Code)
	}
	if w2.Body.Len() != 0 {
		t.Errorf("304 must have an empty body, got %d bytes", w2.Body.Len())
	}
}
