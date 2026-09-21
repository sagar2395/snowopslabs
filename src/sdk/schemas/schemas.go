// SPDX-License-Identifier: Apache-2.0

// Package schemas embeds the JSON Schema documents for SnowOps Labs's declarative
// content (scenario, incident, learning path, challenge, and the shared check).
// Editors and external tools use them while authoring. The validation labctl
// itself applies is in internal/catalog, and a test checks the two agree
// (ADR-0009).
package schemas

import _ "embed"

// Check is the JSON Schema for a check.
//
//go:embed check.schema.json
var Check []byte

// Scenario is the JSON Schema for scenario.yaml.
//
//go:embed scenario.schema.json
var Scenario []byte

// Incident is the JSON Schema for an incident's fault.yaml.
//
//go:embed incident.schema.json
var Incident []byte

// Path is the JSON Schema for a learning path's path.yaml.
//
//go:embed path.schema.json
var Path []byte

// Challenge is the JSON Schema for challenge.yaml.
//
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
