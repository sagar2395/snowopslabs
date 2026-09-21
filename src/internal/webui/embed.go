// SPDX-License-Identifier: Apache-2.0

// Package webui embeds the built web UI so labctl can serve it.
package webui

import "embed"

// DistFS holds the UI's built assets, copied from ui/dist/ at build time. In a
// development build dist/ may hold only .gitkeep; the server detects that and
// serves the UI from disk instead.
//
//go:embed all:dist
var DistFS embed.FS
