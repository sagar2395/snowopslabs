// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseLimit(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"", defaultPageLimit, false},
		{"10", 10, false},
		{"1000", maxPageLimit, false}, // clamped
		{"0", 0, true},
		{"-5", 0, true},
		{"abc", 0, true},
	}
	for _, c := range cases {
		got, err := parseLimit(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseLimit(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("parseLimit(%q)=%d, want %d", c.in, got, c.want)
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	for _, off := range []int{0, 1, 42, 999} {
		got, err := parseCursor(encodeCursor(off))
		if err != nil {
			t.Fatalf("parseCursor(encodeCursor(%d)) errored: %v", off, err)
		}
		if got != off {
			t.Errorf("round trip: got %d, want %d", got, off)
		}
	}
}

func TestParseCursor_Rejects(t *testing.T) {
	for _, bad := range []string{"not-base64!!", "b2Zmc2V0", "", "\x00"} {
		if bad == "" {
			// empty cursor is legitimately offset 0
			if off, err := parseCursor(bad); off != 0 || err != nil {
				t.Errorf("empty cursor: got (%d,%v), want (0,nil)", off, err)
			}
			continue
		}
		if _, err := parseCursor(bad); err == nil {
			t.Errorf("parseCursor(%q) should have errored", bad)
		}
	}
}

func TestPaginate_PagesThroughAll(t *testing.T) {
	items := []int{0, 1, 2, 3, 4}
	r := httptest.NewRequest(http.MethodGet, "/?limit=2", nil)

	page, err := paginate(items, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0] != 0 {
		t.Fatalf("first page: got %v", page.Items)
	}
	if page.NextCursor == "" {
		t.Fatal("first page should have a next cursor")
	}

	// Walk the cursor to the end and confirm we see every item exactly once.
	seen := append([]int{}, page.Items...)
	cursor := page.NextCursor
	for cursor != "" {
		r := httptest.NewRequest(http.MethodGet, "/?limit=2&cursor="+cursor, nil)
		page, err = paginate(items, r)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, page.Items...)
		cursor = page.NextCursor
	}
	if len(seen) != len(items) {
		t.Fatalf("paged items=%v, want all of %v", seen, items)
	}
	for i, v := range seen {
		if v != i {
			t.Fatalf("order/coverage wrong at %d: %v", i, seen)
		}
	}
}

func TestPaginate_EmptySliceYieldsEmptyArray(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	page, err := paginate([]int{}, r)
	if err != nil {
		t.Fatal(err)
	}
	if page.Items == nil {
		t.Error("Items should marshal as [] not null")
	}
	if page.NextCursor != "" {
		t.Error("no next cursor on an empty collection")
	}
}

func TestPaginate_OffsetPastEnd(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?cursor="+encodeCursor(100), nil)
	page, err := paginate([]int{1, 2, 3}, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.NextCursor != "" {
		t.Errorf("offset past end: got %+v, want empty page", page)
	}
}

// The v2 catalog envelope must be {items, nextCursor}; the v1 alias must stay a
// bare array. This exercises the real router so the version tag is applied.
func TestRespondCatalog_VersionShapes(t *testing.T) {
	s := newChallengeServer(t) // one challenge in the catalog
	s.setupRoutes()

	// v2 → envelope
	r2 := httptest.NewRequest(http.MethodGet, "/api/v2/challenges", nil)
	w2 := httptest.NewRecorder()
	s.router.ServeHTTP(w2, r2)
	var env struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	if err := json.NewDecoder(w2.Body).Decode(&env); err != nil {
		t.Fatalf("v2 body is not the paginated envelope: %v", err)
	}
	if len(env.Items) != 1 {
		t.Errorf("v2 items: got %d, want 1", len(env.Items))
	}

	// v1 → bare array
	r1 := httptest.NewRequest(http.MethodGet, "/api/challenges", nil)
	w1 := httptest.NewRecorder()
	s.router.ServeHTTP(w1, r1)
	var arr []map[string]any
	if err := json.NewDecoder(w1.Body).Decode(&arr); err != nil {
		t.Fatalf("v1 body is not a bare array: %v", err)
	}
	if len(arr) != 1 {
		t.Errorf("v1 array: got %d, want 1", len(arr))
	}
}

func TestRespondCatalog_BadCursorIsClientError(t *testing.T) {
	s := newChallengeServer(t)
	s.setupRoutes()

	r := httptest.NewRequest(http.MethodGet, "/api/v2/challenges?cursor=!!bad!!", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor: got %d, want 400", w.Code)
	}
}
