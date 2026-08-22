// SPDX-License-Identifier: Apache-2.0

// Package schemas embeds the JSON Schema documents for SnowOps Labs's declarative
// content (scenario, incident, learning path, challenge, and the shared check).
// They are the authoring reference consumed by editors and external tooling; the
// authoritative, error-reporting validation lives in the Go loaders under
// internal/catalog. Keeping the two in the same repo lets a test assert they do
// not drift (see ADR-0002).
package schemas

import _ "embed"

//go:embed check.schema.json
var Check []byte

//go:embed scenario.schema.json
var Scenario []byte

//go:embed incident.schema.json
var Incident []byte

//go:embed path.schema.json
var Path []byte

//go:embed challenge.schema.json
var Challenge []byte

// ByKind maps a catalog content kind to its embedded JSON Schema. The keys match
// the kind strings used by internal/catalog.
var ByKind = map[string][]byte{
	"check":     Check,
	"scenario":  Scenario,
	"incident":  Incident,
	"path":      Path,
	"challenge": Challenge,
}
