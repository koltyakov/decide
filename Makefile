.DEFAULT_GOAL := help
.PHONY: help tidy deps deps-update deps-check fmt fmt-check lint vet test test-race check cov bench build install bundle build-all release-local release-check vuln ci

GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN_DIR ?= bin
DIST_DIR ?= dist
COVER_PROFILE ?= tmp/coverage.out
COVER_HTML ?= tmp/coverage.html
GOLANGCI_LINT_VERSION ?= v2.14.0
GOVULNCHECK_VERSION ?= v1.8.0
ACTIONLINT_VERSION ?= v1.7.12
PLATFORMS ?= linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64
EXE := $(if $(filter windows,$(shell $(GO) env GOOS)),.exe,)

help:
	@echo "make deps / tidy          Download dependencies / tidy go.mod"
	@echo "make deps-update          Update dependencies to current minor/patch releases"
	@echo "make deps-check           List available dependency updates"
	@echo "make fmt / fmt-check      Format Go files / check formatting"
	@echo "make lint / vet           Run pinned golangci-lint / go vet"
	@echo "make test / test-race     Run native tests / race detector"
	@echo "make check                Vet and race tests"
	@echo "make cov / bench          Generate coverage HTML / run benchmarks"
	@echo "make build / install      Build bin/decide / install decide to GOBIN"
	@echo "make bundle / build-all   Build six platform binaries and checksums"
	@echo "make release-local        Build and verify a release bundle"
	@echo "make release-check        Verify the existing release bundle"
	@echo "make vuln / ci            Scan dependencies / run all CI checks"

tidy:
	$(GO) mod tidy

deps:
	$(GO) mod download
	$(GO) mod tidy

deps-update:
	$(GO) get -u -t ./...
	$(GO) mod tidy

deps-check:
	$(GO) list -m -u all

fmt:
	gofmt -w -s cmd internal skills

fmt-check:
	@files="$$(gofmt -s -l cmd internal skills)"; \
	if [ -n "$$files" ]; then printf 'Unformatted Go files:\n%s\n' "$$files"; exit 1; fi

lint:
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run --timeout=5m

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -timeout=120s

test-race:
	$(GO) test -race ./... -timeout=120s

check: vet test-race

cov:
	@mkdir -p "$(dir $(COVER_PROFILE))" "$(dir $(COVER_HTML))"
	$(GO) test -race -coverprofile="$(COVER_PROFILE)" -covermode=atomic ./... -timeout=120s
	$(GO) tool cover -html="$(COVER_PROFILE)" -o "$(COVER_HTML)"

bench:
	$(GO) test ./... -run '^$$' -bench . -benchmem

build:
	$(GO) run ./cmd/decide-bundle --binary "$(BIN_DIR)/decide$(EXE)" --version "$(VERSION)"

install:
	CGO_ENABLED=0 $(GO) install -trimpath -ldflags '-s -w -X main.version=$(VERSION)' ./cmd/decide

bundle:
	$(GO) run ./cmd/decide-bundle --out "$(DIST_DIR)" --platforms "$(PLATFORMS)" --version "$(VERSION)"

build-all: bundle

release-local: bundle
	$(MAKE) release-check

release-check:
	$(GO) run ./cmd/decide-bundle --out "$(DIST_DIR)" --check

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

ci: fmt-check lint check vuln build release-local
	$(GO) mod verify
	$(GO) run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)
