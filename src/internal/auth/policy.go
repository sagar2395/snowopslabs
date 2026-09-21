// SPDX-License-Identifier: Apache-2.0

package auth

import "strings"

// operatorOnlyPrefixes are the API paths, without the /api/v2 prefix, where a
// request other than GET or OPTIONS needs the operator role. Participants may
// run scenarios, challenges, incidents and learning paths, but may not change
// the platform, runtime, lab, apps, shared services or traffic, which affect
// everyone.
var operatorOnlyPrefixes = []string{
	"/platform",
	"/runtimes",
	"/lab",
	"/apps",
	"/services",
	"/traffic",
	"/runs",
}

// RequiresOperator reports whether only an operator may send a request with
// this method to this API path. GET and OPTIONS never need the operator role.
// /api and /api/v2 paths are treated the same.
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

// apiSubPath strips the /api/v2 or /api prefix, so /api/v2/platform/up becomes
// /platform/up. Other paths are returned unchanged.
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
