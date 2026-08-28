// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
)

const (
	// defaultPageLimit is applied when the client sends no ?limit. The content
	// catalog is small, so this comfortably returns most collections in one page
	// while still exercising the cursor path for clients that opt into it.
	defaultPageLimit = 50
	// maxPageLimit caps a client-supplied ?limit so one request can't ask the
	// server to marshal an unbounded slice.
	maxPageLimit = 200
	// cursorPrefix namespaces our opaque cursor so a stray value from elsewhere
	// is rejected rather than misread as an offset.
	cursorPrefix = "off:"
)

// pageResponse is the v2 envelope for a paginated collection. NextCursor is
// empty on the last page; a client pages until it comes back empty.
type pageResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// paginate slices items according to the request's ?limit and ?cursor and
// returns the v2 page envelope. The cursor is opaque (base64 of an offset), so
// callers treat it as a token and never construct it themselves. A malformed
// cursor or limit is a client error, surfaced as a returned error the handler
// maps to 400.
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

	end := offset + limit
	if end > len(items) {
		end = len(items)
	}

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

// respondCatalog writes a catalog collection in the shape appropriate to the
// API version: the paginated {items, nextCursor} envelope under /api/v2, or the
// bare legacy slice under /api. Both carry an ETag for conditional revalidation.
func respondCatalog[T any](w http.ResponseWriter, r *http.Request, items []T) {
	if items == nil {
		items = []T{}
	}
	if apiVersionFrom(r.Context()) != apiV2 {
		writeJSONCached(w, r, http.StatusOK, items)
		return
	}
	page, err := paginate(items, r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSONCached(w, r, http.StatusOK, page)
}
