// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

// writeJSONCached writes data as JSON with an ETag computed from the body. If
// the request's If-None-Match matches, it answers 304 with no body. The
// response carries Cache-Control: no-cache, so clients may store it but must
// revalidate with the ETag before reusing it.
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

// etagFor returns a strong ETag for body: the first 64 bits of its SHA-256,
// in hex.
func etagFor(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// ifNoneMatch reports whether an If-None-Match header matches etag. Per RFC
// 9110 the header is "*" or a comma-separated list of tags. A W/ prefix is
// ignored, since the server only issues strong tags.
func ifNoneMatch(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return true
	}
	for part := range strings.SplitSeq(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(part), "W/") == etag {
			return true
		}
	}
	return false
}
