// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

// writeJSONCached serializes data and attaches a content-derived ETag so
// clients can revalidate cheaply. When the request carries a matching
// If-None-Match, it answers 304 Not Modified with no body; otherwise it writes
// the status and body as usual. Cache-Control: no-cache tells clients to store
// the response but always revalidate via the ETag rather than serve it blindly.
//
// It is safe on any API version: a client that never sends If-None-Match (the
// current UI) simply gains an ETag header and always receives 200 + body.
func writeJSONCached(w http.ResponseWriter, r *http.Request, status int, data any) {
	body, err := json.Marshal(data)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", "could not encode response")
		return
	}
	etag := etagFor(body)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")

	if inm := r.Header.Get("If-None-Match"); inm != "" && ifNoneMatch(inm, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// etagFor derives a strong ETag from the response body. Half a SHA-256 (64 bits
// hex) is ample to make an accidental collision between two catalog states
// vanishingly unlikely while keeping the header compact.
func etagFor(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// ifNoneMatch reports whether the client's If-None-Match header matches the
// current ETag. Per RFC 9110 the header is either "*" (matches anything) or a
// comma-separated list of entity-tags; a leading weak indicator (W/) is ignored
// because we only ever emit strong tags.
func ifNoneMatch(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(part), "W/") == etag {
			return true
		}
	}
	return false
}
