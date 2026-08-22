// SPDX-License-Identifier: Apache-2.0
package auth

import "strings"

// operatorOnlyPrefixes are the API path prefixes whose *mutating* requests
// (anything other than GET/OPTIONS) require the operator role. Participants get
// a 403 on these. Everything else that mutates — scenarios, challenges,
// incidents, learn — is part of "running" a lab and is allowed for participants.
//
// Rationale (task 062): a participant can run challenges/incidents/learn and
// read status, but must not uninstall platform, switch runtime, reset the lab,
// or build/deploy apps and shared services.
var operatorOnlyPrefixes = []string{
	"/api/platform",
	"/api/runtimes",
	"/api/lab",
	"/api/apps",
	"/api/services",
}

// RequiresOperator reports whether a request to the given API path with the
// given HTTP method may only be performed by an operator. Read requests
// (GET/OPTIONS) never require operator; neither do paths outside the
// operator-only set.
func RequiresOperator(method, path string) bool {
	if method == "GET" || method == "OPTIONS" || method == "HEAD" {
		return false
	}
	for _, p := range operatorOnlyPrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
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
