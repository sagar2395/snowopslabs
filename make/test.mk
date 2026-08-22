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
SHELL_TEST_DIR   := test/shell

.PHONY: test test-go test-shell test-api test-ui test-e2e test-cluster \
        test-coverage coverage-check fuzz lint lint-go lint-shell lint-ui \
        fmt fmt-check sec vuln golden-update validate-content

## test: layers 1-3 (Go unit, shell, contract) with the race detector
test: test-go test-shell

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

## test-shell: bats suites with kubectl/helm stubbed onto PATH
test-shell:
	@command -v bats >/dev/null 2>&1 || { \
	  echo "bats not found. Install it:"; \
	  echo "  macOS:  brew install bats-core"; \
	  echo "  Linux:  npm install -g bats   (or your distro package)"; \
	  exit 1; }
	@echo "==> Shell tests (bats)"
	@bats --recursive $(SHELL_TEST_DIR)

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

## lint: every static-analysis gate
lint: fmt-check lint-go lint-shell lint-ui sec vuln

lint-go:
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "golangci-lint not found: https://golangci-lint.run/welcome/install/"; exit 1; }
	@golangci-lint run ./...

## lint-shell: shellcheck + formatting + the portability gate (golden rule 1)
lint-shell:
	@command -v shellcheck >/dev/null 2>&1 || { \
	  echo "shellcheck not found (brew install shellcheck / apt install shellcheck)"; exit 1; }
	@echo "==> shellcheck"
	@find . -name '*.sh' -not -path './.git/*' -not -path '*/node_modules/*' -print0 \
	  | xargs -0 shellcheck --severity=warning
	@command -v shfmt >/dev/null 2>&1 && { echo "==> shfmt"; shfmt -d -i 2 -ci $$(find . -name '*.sh' -not -path './.git/*' -not -path '*/node_modules/*'); } || \
	  echo "shfmt not found — skipping format check (install: go install mvdan.cc/sh/v3/cmd/shfmt@latest)"
	@$(MAKE) --no-print-directory lint-portability

## lint-portability: reject GNU-only constructs that break macOS
lint-portability:
	@echo "==> portability gate (macOS + Linux)"
	@bash scripts/lint-portability.sh

lint-ui:
	@cd $(UI_DIR) && npm run typecheck

fmt:
	@gofmt -w $$(find . -name '*.go' -not -path './.git/*')

fmt-check:
	@echo "==> gofmt"
	@out=$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*')); \
	  if [ -n "$$out" ]; then echo "not gofmt'd:"; echo "$$out"; exit 1; fi

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
