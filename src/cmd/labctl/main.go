// SPDX-License-Identifier: Apache-2.0

// Command labctl is the SnowOps Labs CLI. It builds a local Kubernetes lab,
// installs platform components, activates scenarios, injects faults and
// grades the fixes, and serves the web UI with `labctl ui`. All of its logic
// is in internal/cli.
package main

import (
	"github.com/sagar2395/snowopslabs/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=..." (see
// make/cli.mk and .goreleaser.yaml); local builds report "dev". It is the only
// declaration allowed here (TestMainStaysTrivial); all logic lives in
// internal/cli.
var version = "dev"

func main() {
	cli.Execute(version)
}
