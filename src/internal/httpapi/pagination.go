// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
)

const (
	// defaultPageLimit applies when the client sends no ?limit. It is large
	// enough to return most collections in one page.
	defaultPageLimit = 50
	// maxPageLimit caps a client-supplied ?limit so one request can't ask the
	// server to marshal an unbounded slice.
	maxPageLimit = 200
	// cursorPrefix marks our cursors, so an unrelated value is rejected rather
	// than read as an offset.
	cursorPrefix = "off:"
)

// pageResponse is the v2 envelope for a paginated collection. NextCursor is
// empty on the last page; a client pages until it comes back empty.
type pageResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// paginate returns the page of items selected by the request's ?limit and
// ?cursor. The cursor is an opaque token (base64 of an offset) that clients
// must not build themselves. A malformed cursor or limit returns an error,
// which handlers answer with 400.
func paginate[T any](items []T, r *http.Request) (pageResponse[T], error) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		return pageResponse[T]{}, err
	}
	offset, err := parseCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return pageResponse[T]{}, err
	}
	if offset > len(items) {
		offset = len(items)
	}

	end := min(offset+limit, len(items))

	page := pageResponse[T]{Items: items[offset:end]}
	if page.Items == nil {
		page.Items = []T{}
	}
	if end < len(items) {
		page.NextCursor = encodeCursor(end)
	}
	return page, nil
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return defaultPageLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, errors.New("limit must be a positive integer")
	}
	if n > maxPageLimit {
		n = maxPageLimit
	}
	return n, nil
}

func parseCursor(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, errors.New("invalid cursor")
	}
	s := string(decoded)
	if len(s) <= len(cursorPrefix) || s[:len(cursorPrefix)] != cursorPrefix {
		return 0, errors.New("invalid cursor")
	}
	n, err := strconv.Atoi(s[len(cursorPrefix):])
	if err != nil || n < 0 {
		return 0, errors.New("invalid cursor")
	}
	return n, nil
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursorPrefix + strconv.Itoa(offset)))
}

// respondCatalog writes a catalog collection as the paginated
// {items, nextCursor} envelope, with an ETag for conditional revalidation.
func respondCatalog[T any](w http.ResponseWriter, r *http.Request, items []T) {
	if items == nil {
		items = []T{}
	}
	page, err := paginate(items, r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSONCached(w, r, http.StatusOK, page)
}
