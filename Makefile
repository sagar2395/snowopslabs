ifneq (,$(wildcard .env))
	include .env
	# .env values keep any whitespace between the value and an inline
	# "# comment": Make strips the comment text but not the spaces before it, so
	# `PROFILE=k3d   # ...` becomes "k3d   ". Left as-is, that whitespace flows
	# into recipes (bash runtimes/k3d   /up.sh) and into the exported environment
	# the Go tools read, breaking cluster names, ports and profile lookups.
	# Normalise every variable the file defines with $(strip). This keeps any
	# existing .env working, including ones copied from an older, inline-commented
	# .env.example.
$(foreach v,$(shell sed -n 's/^[[:space:]]*\([A-Za-z_][A-Za-z0-9_]*\)[[:space:]]*=.*/\1/p' .env),$(eval $(v) := $(strip $($(v)))))
	export
endif

PROFILE ?= k3d
CLUSTER_NAME ?= snowops
APP_NAME ?= go-api
HELM_RELEASE_NAME ?= go-api
HELM_VALUES ?= values-dev.yaml
# Mirrors src/make/test.mk; used only in the help text below.
COVERAGE_MIN ?= 80

# Defaults are read from .env if present; any command may override them by
# passing VAR=val on the make command line.

# Orchestration and repo-wide gates run from the content root. The Go/UI build
# and test targets live in the self-contained module under src/ (src/Makefile)
# and are delegated in below, so `make cli-build`, `make test`, etc. still work
# from here.
include make/vars.mk
include make/bootstrap.mk
include make/runtime.mk
include make/app.mk
include make/platform.mk
include make/check.mk
include make/services.mk
include make/shell.mk

# Targets implemented by the src/ module. Running any of them here delegates to
# `make -C src <target>` so the build executes where go.mod lives.
SRC_TARGETS := cli-build cli-build-all cli-install cli-clean ui-build ui-deps \
               test-go test-api test-coverage coverage-check fuzz \
               lint-go lint-ui sec vuln golden-update validate-content \
               test-ui test-e2e test-cluster

.PHONY: $(SRC_TARGETS)
$(SRC_TARGETS):
	@$(MAKE) --no-print-directory -C src $@

.DEFAULT_GOAL := help

.PHONY: help init teardown reset run test lint

# ── Aggregate test & lint (Go module + repo-wide shell gates) ─────────────────

## test: Go unit + contract tests (src/) and the shell (bats) suites
test: test-go test-shell

## lint: every static-analysis gate — gofmt + shell (root) and Go/UI (src/)
lint: fmt-check lint-shell lint-go lint-ui sec vuln

# ── Lifecycle ────────────────────────────────────────────────────────────────

init:
	@$(MAKE) setup-tools PROFILE=$(PROFILE)
	@$(MAKE) runtime-up
	@$(MAKE) platform-up

teardown:
	@$(MAKE) destroy-all-apps || true
	@$(MAKE) platform-down || true
	@$(MAKE) runtime-down || true

reset: teardown init

run:
	@$(MAKE) local-run APP_NAME=$(APP_NAME)

# ── Help ─────────────────────────────────────────────────────────────────────

help:
	@echo "SnowOps Labs — Kubernetes platform-engineering simulator"
	@echo ""
	@echo "  Quick start:"
	@echo "    make cli-build           Build src/bin/labctl (UI embedded)"
	@echo "    make init                setup-tools + runtime-up + platform-up"
	@echo "    make teardown            Remove apps, platform and cluster"
	@echo "    make reset               teardown then init"
	@echo ""
	@echo "  Test & quality (all four layers are mandatory — docs/TESTING.md):"
	@echo "    make test                Go unit + shell tests, race detector, coverage gate"
	@echo "    make test-go             Go unit + contract tests (>= $(COVERAGE_MIN)% per package)"
	@echo "    make test-shell          bats suites with kubectl/helm stubbed"
	@echo "    make test-api            HTTP contract suite only"
	@echo "    make test-ui             vitest component tests with coverage"
	@echo "    make test-e2e            playwright journeys"
	@echo "    make test-cluster        nightly real-cluster e2e on kind (slow)"
	@echo "    make test-coverage       HTML coverage report -> coverage.html"
	@echo "    make fuzz                short fuzz run over every parser"
	@echo "    make lint                every static-analysis gate"
	@echo "    make fmt                 gofmt the tree"
	@echo ""
	@echo "  CLI (labctl):"
	@echo "    make cli-build           Build for the host platform"
	@echo "    make cli-build-all       Cross-compile every release target into dist/"
	@echo "    make cli-install         Build and install onto PATH"
	@echo "    make cli-clean           Remove build outputs"
	@echo ""
	@echo "  Cluster (PROFILE=$(PROFILE) — k3d | kind | incluster):"
	@echo "    make runtime-up          Create cluster '$(CLUSTER_NAME)'"
	@echo "    make runtime-down        Destroy the current cluster"
	@echo "    make runtime-status      Show cluster info and nodes"
	@echo "    make check-tools         Verify required binaries are on PATH"
	@echo "    make check-cluster       Ensure the current context reaches the cluster"
	@echo "    make check-ingress       Confirm the ingress controller is ready"
	@echo ""
	@echo "  Platform components:"
	@echo "    make platform-up         Install the configured providers"
	@echo "    make platform-down       Remove them"
	@echo "    make platform-status     Show provider status"
	@echo "    Provider variables (set in .env or on the command line):"
	@echo "      INGRESS_PROVIDER=traefik|nginx          (default: traefik)"
	@echo "      METRICS_PROVIDER=prometheus             (default: prometheus)"
	@echo "      LOGGING_PROVIDER=loki                   (unset = disabled)"
	@echo "      TRACING_PROVIDER=tempo                  (unset = disabled)"
	@echo ""
	@echo "  Applications:"
	@echo "    make build APP_NAME=<name>       Run the app's build strategy"
	@echo "    make run APP_NAME=<name>         Run it locally"
	@echo "    make deploy APP_NAME=<name>      Run its deploy strategy"
	@echo "    make destroy-app APP_NAME=<name> Remove it"
	@echo "    make deploy-all / destroy-all-apps"
	@echo ""
	@echo "  Shared services:"
	@echo "    make service-list                List available services"
	@echo "    make service-up SERVICE=<name>   Install one (e.g. redis)"
	@echo "    make service-down SERVICE=<name> Uninstall one"
	@echo "    make service-status              Show status"
	@echo ""
	@echo "Per-app configuration lives in apps/<name>/app.env (BUILD_STRATEGY,"
	@echo "DEPLOY_STRATEGY, HELM_VALUES). Override on the command line:"
	@echo "  make build APP_NAME=foo BUILD_STRATEGY=golang"
