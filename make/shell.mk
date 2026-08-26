# Repo-wide shell and formatting gates.
#
# These run from the repository root because they scan the whole tree — the
# shell scripts under bootstrap/, platform/, runtimes/ and src/engine/,
# src/services/ alike. The Go/UI build and test gates live in src/ (delegated
# from the root Makefile); these stay here so the `find .` sweeps see every
# script regardless of where the Go module now lives.

SHELL_TEST_DIR := src/test/shell

.PHONY: test-shell lint-shell lint-portability fmt fmt-check

## test-shell: bats suites with kubectl/helm stubbed onto PATH
test-shell:
	@command -v bats >/dev/null 2>&1 || { \
	  echo "bats not found. Install it:"; \
	  echo "  macOS:  brew install bats-core"; \
	  echo "  Linux:  npm install -g bats   (or your distro package)"; \
	  exit 1; }
	@echo "==> Shell tests (bats)"
	@bats --recursive $(SHELL_TEST_DIR)

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
	@bash src/scripts/lint-portability.sh

## fmt: gofmt the whole tree (the module under src/ plus the sample apps)
fmt:
	@gofmt -w $$(find . -name '*.go' -not -path './.git/*')

## fmt-check: fail if anything under the tree is not gofmt'd
fmt-check:
	@echo "==> gofmt"
	@out=$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*')); \
	  if [ -n "$$out" ]; then echo "not gofmt'd:"; echo "$$out"; exit 1; fi
