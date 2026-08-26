# src/ — the self-contained labctl Go module (issue #7).
#
# Everything needed to build and test the labctl binary lives under src/: the
# Go source, go.mod/go.sum, the embedded SPA (ui/), the Go coverage ratchet and
# the .golangci.yml config. `cd src && make <target>` works with no reference to
# the repo root, which keeps a future "the Go engine gets its own repo" split a
# straight lift of this directory.
#
# The repository root Makefile delegates its build and test targets in here
# (make -C src ...) so `make` still works from the content root. Repo-wide shell
# and gofmt gates, and the cluster/app orchestration, stay in the root Makefile
# because they operate on content that lives outside this module.

include make/cli.mk
include make/test.mk

.DEFAULT_GOAL := help

.PHONY: help

help:
	@echo "labctl module (src/) — build and test the CLI"
	@echo ""
	@echo "  make cli-build        Build bin/labctl with the UI embedded"
	@echo "  make cli-build-all    Cross-compile every release target into dist/"
	@echo "  make cli-install      Build and install labctl onto PATH"
	@echo "  make test             Go unit + contract tests (race + coverage gate)"
	@echo "  make test-go          Same as test"
	@echo "  make test-ui          vitest component tests with coverage"
	@echo "  make test-e2e         playwright journeys"
	@echo "  make lint             gofmt-independent Go + UI static analysis"
	@echo "  make fuzz             short fuzz run over every parser"
	@echo ""
	@echo "Run from the repository root instead (make cli-build, make test) to"
	@echo "drive these through the top-level Makefile, which also runs the"
	@echo "repo-wide shell gates and the cluster/app orchestration targets."
