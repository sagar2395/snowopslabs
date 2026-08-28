// SPDX-License-Identifier: Apache-2.0
package auth

import "strings"

// operatorOnlyPrefixes are the version-less API sub-paths whose *mutating*
// requests (anything other than GET/OPTIONS) require the operator role.
// Participants get a 403 on these. Everything else that mutates — scenarios,
// challenges, incidents, learn — is part of "running" a lab and is allowed for
// participants.
//
// Rationale (task 062): a participant can run challenges/incidents/learn and
// read status, but must not uninstall platform, switch runtime, reset the lab,
// build/deploy apps and shared services, or drive shared load generation.
var operatorOnlyPrefixes = []string{
	"/platform",
	"/runtimes",
	"/lab",
	"/apps",
	"/services",
	"/traffic",
	"/runs",
}

// RequiresOperator reports whether a request to the given API path with the
// given HTTP method may only be performed by an operator. Read requests
// (GET/OPTIONS) never require operator; neither do paths outside the
// operator-only set. The version prefix is normalised first, so /api and
// /api/v2 are gated identically.
func RequiresOperator(method, path string) bool {
	if method == "GET" || method == "OPTIONS" || method == "HEAD" {
		return false
	}
	sub := apiSubPath(path)
	for _, p := range operatorOnlyPrefixes {
		if sub == p || strings.HasPrefix(sub, p+"/") {
			return true
		}
	}
	return false
}

// apiSubPath strips the API version prefix (/api/v2 or /api) so policy matches
// on the version-less remainder — e.g. both /api/platform/up and
// /api/v2/platform/up become /platform/up. A path without an /api prefix is
// returned unchanged.
func apiSubPath(path string) string {
	if rest, ok := strings.CutPrefix(path, "/api/v2"); ok {
		return rest
	}
	if rest, ok := strings.CutPrefix(path, "/api"); ok {
		return rest
	}
	return path
}

// Authorize reports whether a user with the given role may perform method on
// path. Operators may do anything; participants are denied operator-only
// mutations.
func Authorize(role, method, path string) bool {
	if role == RoleOperator {
		return true
	}
	return !RequiresOperator(method, path)
}
