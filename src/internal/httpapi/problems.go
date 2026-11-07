// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
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
	"hidden_run",
	"incident_active",
	"inject_failed",
	"internal_error",
	"invalid_credentials",
	"invalid_input",
	"invalid_kind",
	"invalid_request",
	"method_not_allowed",
	"no_active_challenge",
	"no_active_incident",
	"no_checks",
	"no_eligible_fault",
	"no_more_hints",
	"not_found",
	"path_not_started",
	"recovery_required",
	"request_too_large",
	"resolve_failed",
	"too_many_requests",
	"unauthorized",
	"unavailable",
}

// respondInternal answers a failure that is the server's own: state it could
// not read or write. The cause goes to the server log with the request ID; the
// client is told where to look, and is not shown the error itself, which names
// paths on the server's disk.
func respondInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "path", r.URL.Path, "request_id", requestIDFrom(r.Context()), "error", err)
	respondError(w, r, http.StatusInternalServerError, "internal_error",
		"the server could not complete this request: its log has the cause, under this response's requestId")
}

// respondLoadError answers a failure to load one content item by name. A
// missing item is a 404 that names it; anything else is the item being
// malformed on disk, which is the server's problem. Neither repeats the
// filesystem path the loader reports.
func respondLoadError(w http.ResponseWriter, r *http.Request, kind, name string, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		respondError(w, r, http.StatusNotFound, "not_found", fmt.Sprintf("%s %q not found", kind, name))
		return
	}
	slog.Error("content item failed to load", "kind", kind, "name", name, "error", err)
	respondError(w, r, http.StatusInternalServerError, "internal_error",
		fmt.Sprintf("%s %q is malformed: run 'labctl validate' for the reason", kind, name))
}
