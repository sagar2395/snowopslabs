// SPDX-License-Identifier: Apache-2.0
package main

import (
	"github.com/sagar2395/snowopslabs/internal/cli"
)

// version is stamped at build time via -ldflags "-X main.version=…" (see
// make/cli.mk and .goreleaser.yaml). It defaults to "dev" for local builds.
// This is the one declaration cmd/labctl is allowed (see TestMainStaysTrivial):
// a linker-stamped build var with no logic. Everything testable lives in
// internal/cli.
var version = "dev"

func main() {
	cli.Execute(version)
}
