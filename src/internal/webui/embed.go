// SPDX-License-Identifier: Apache-2.0
package webui

import "embed"

// DistFS holds the embedded UI static assets (copied from ui/dist/ at build time).
// In development, the dist/ directory may only contain .gitkeep — the server
// will detect this and fall back to serving from the filesystem.
//
//go:embed all:dist
var DistFS embed.FS
