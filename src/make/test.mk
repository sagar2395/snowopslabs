# Test and static-analysis targets.
#
# Four mandatory layers (docs/TESTING.md):
#   1. Go unit          — hermetic, race-clean, >=80% statements per package
#   2. Shell (bats)     — every script with logic, kubectl/helm stubbed
#   3. API/CLI contract — every endpoint and command, incl. its error envelope
#   4. UI               — vitest components, playwright journeys
#
# Every gate here fails the build. None are advisory.

COVERAGE_MIN     ?= 80
COVERAGE_PROFILE := coverage.out
UI_DIR           := ui

# Repo-wide shell tests, shellcheck and the portability gate, plus gofmt over
# the whole tree, live in the root Makefile (make/shell.mk): they must scan
# scripts outside this module (bootstrap/, platform/, runtimes/). The targets
# here are the Go and UI gates that belong to this self-contained module.

.PHONY: test test-go test-api test-ui test-e2e test-cluster \
        test-coverage coverage-check fuzz lint lint-go lint-ui \
        sec vuln golden-update validate-content

## test: Go unit + contract tests with the race detector and coverage gate
test: test-go

## validate-content: schema + cross-reference + template checks over all content
validate-content:
	@echo "==> Validating declarative content (labctl validate)"
	@go run ./cmd/labctl validate

## test-go: Go unit + contract tests, race detector on, coverage enforced
test-go:
	@echo "==> Go tests (race detector, coverage gate $(COVERAGE_MIN)%)"
	@go test -race -coverprofile=$(COVERAGE_PROFILE) -covermode=atomic ./...
	@$(MAKE) --no-print-directory coverage-check

## test-api: the HTTP contract suite only (a subset of test-go)
test-api:
	@go test -race ./internal/httpapi/... ./internal/cli/...

## coverage-check: per-package gate with a ratchet (.coverage-exceptions)
coverage-check:
	@COVERAGE_PROFILE=$(COVERAGE_PROFILE) COVERAGE_MIN=$(COVERAGE_MIN) \
	  bash scripts/coverage-gate.sh

## test-coverage: HTML coverage report at coverage.html
test-coverage:
	@go test -coverprofile=$(COVERAGE_PROFILE) -covermode=atomic ./...
	@go tool cover -html=$(COVERAGE_PROFILE) -o coverage.html
	@echo "Coverage report: coverage.html"

## test-ui: vitest component tests with coverage
test-ui:
	@cd $(UI_DIR) && npm run test:coverage

## test-e2e: playwright journeys against the built SPA
test-e2e:
	@cd $(UI_DIR) && npm run test:e2e

## test-cluster: nightly real-cluster e2e on kind (slow; needs Docker)
test-cluster:
	@bash test/cluster/run.sh

## fuzz: short fuzz run over every parser (extended run happens nightly)
fuzz:
	@echo "==> Fuzzing parsers ($(or $(FUZZTIME),20s) each)"
	@for pkg in $$(go list ./... ); do \
	  for target in $$(go test -list 'Fuzz.*' $$pkg 2>/dev/null | grep '^Fuzz' || true); do \
	    echo "  $$pkg $$target"; \
	    go test -run '^$$' -fuzz "^$$target$$" -fuzztime=$(or $(FUZZTIME),20s) $$pkg || exit 1; \
	  done; \
	done

## lint: the Go and UI static-analysis gates for this module. The repo-wide
## shell gate (lint-shell) and gofmt sweep (fmt-check) run from the root
## Makefile; the aggregate `make lint` there adds them.
lint: lint-go lint-ui sec vuln

lint-go:
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "golangci-lint not found: https://golangci-lint.run/welcome/install/"; exit 1; }
	@golangci-lint run ./...

lint-ui:
	@cd $(UI_DIR) && npm run typecheck

sec:
	@command -v gosec >/dev/null 2>&1 || { \
	  echo "gosec not found: go install github.com/securego/gosec/v2/cmd/gosec@latest"; exit 1; }
	@gosec -quiet ./...

vuln:
	@command -v govulncheck >/dev/null 2>&1 || { \
	  echo "govulncheck not found: go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; }
	@govulncheck ./...

## golden-update: regenerate CLI golden output files
golden-update:
	@go test ./internal/cli/... -update
	@echo "Golden files regenerated — review the diff before committing."
