// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
)

// problemContentType is the RFC 7807 media type for machine-readable errors.
const problemContentType = "application/problem+json"

// problemTypeBase turns a `type` slug into a URI. The URI need not resolve to a
// document (RFC 7807 §3.1), but it must not change, because clients branch on
// it.
const problemTypeBase = "https://snowopslabs.dev/problems/"

// problemDetail is an RFC 7807 problem+json body. Fields are ordered and named
// per the spec; RequestID is a permitted extension member that ties an error to
// the X-Request-ID the client saw and the server logged.
type problemDetail struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Instance  string `json:"instance,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

// problemType maps a stable code slug to its `type` URI. An empty code yields
// the spec's sentinel "about:blank", meaning "no semantics beyond the status".
func problemType(code string) string {
	if code == "" {
		return "about:blank"
	}
	return problemTypeBase + code
}

// respondProblem writes an RFC 7807 problem+json error. Title is the standard
// reason phrase for the status, and the specific message goes in Detail.
func respondProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	w.Header().Set("Content-Type", problemContentType)
	respondJSON(w, status, problemDetail{
		Type:      problemType(code),
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    detail,
		Instance:  r.URL.Path,
		RequestID: requestIDFrom(r.Context()),
	})
}

// knownProblemSlugs lists every `type` slug the API returns (ADR-0006).
// TestProblemSlugs_StableSet fails if the handlers use a slug that is not
// listed here, so adding or renaming one is always deliberate.
var knownProblemSlugs = []string{
	"already_finished",
	"auth_disabled",
	"confirmation_required",
	"forbidden_origin",
	"forbidden_role",
	"incident_active",
	"inject_failed",
	"internal_error",
	"invalid_credentials",
	"invalid_input",
	"invalid_kind",
	"invalid_request",
	"no_active_incident",
	"no_checks",
	"no_eligible_fault",
	"no_more_hints",
	"not_found",
	"resolve_failed",
	"session_error",
	"too_many_requests",
	"unauthorized",
	"unavailable",
}
