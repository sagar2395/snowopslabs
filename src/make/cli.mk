# Building the labctl binary (module root is the repo root — ADR-0005).
#
# The SPA is built first and embedded, so a release is one self-contained
# artifact with no separate frontend deploy.

CLI_BIN     := bin/labctl
CLI_PKG     := ./cmd/labctl
CLI_UI_SRC  := ui/dist
CLI_UI_DEST := internal/webui/dist

# Release targets. Every one is cgo-free (ADR-0002) so cross-compilation is a
# plain GOOS/GOARCH change.
CLI_TARGETS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: cli-build cli-build-all cli-install cli-clean ui-build ui-deps

## ui-deps: install SPA dependencies (reproducible; uses the lockfile)
ui-deps:
	@cd ui && npm ci --prefer-offline --no-audit --fund=false

## ui-build: typecheck and bundle the SPA into ui/dist
ui-build: ui-deps
	@echo "==> Building UI"
	@cd ui && npm run build

## cli-build: build labctl for the host platform with the UI embedded
cli-build: ui-build
	@echo "==> Embedding UI assets"
	@mkdir -p $(CLI_UI_DEST)
	@cp -R $(CLI_UI_SRC)/. $(CLI_UI_DEST)/
	@echo "==> Building labctl $(VERSION) for $$(go env GOOS)/$$(go env GOARCH)"
	@mkdir -p bin
	@CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(CLI_BIN) $(CLI_PKG)
	@echo "Binary: $(CLI_BIN)"

## cli-build-all: cross-compile every release target into dist/
cli-build-all: ui-build
	@mkdir -p $(CLI_UI_DEST) dist
	@cp -R $(CLI_UI_SRC)/. $(CLI_UI_DEST)/
	@for target in $(CLI_TARGETS); do \
	  goos=$${target%/*}; goarch=$${target#*/}; \
	  out="dist/labctl-$${goos}-$${goarch}"; \
	  echo "  $${out}"; \
	  CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
	    go build -trimpath -ldflags '$(LDFLAGS)' -o $$out $(CLI_PKG) || exit 1; \
	done
	@echo "Cross-compiled binaries in dist/"

## cli-install: build and copy labctl onto PATH
cli-install: cli-build
	@dest="$${GOBIN:-$$(go env GOPATH)/bin}"; \
	  mkdir -p "$$dest" && cp $(CLI_BIN) "$$dest/labctl" && \
	  echo "Installed $$dest/labctl"

cli-clean:
	@rm -f $(CLI_BIN) coverage.out coverage.html
	@rm -rf dist/ ui/dist ui/coverage ui/test-results ui/playwright-report
	@rm -rf $(CLI_UI_DEST)/assets
	@find $(CLI_UI_DEST) -name 'index.html' -delete 2>/dev/null || true
