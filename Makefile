# EasyDrop – developer Makefile
#
# Toolchain: Go >= 1.27 (see go.mod; GOTOOLCHAIN=auto resolves it).
# Run `make` (or `make help`) for the target list.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# --- variables ---------------------------------------------------------------

BINARY      := easydrop
CMD         := ./cmd/easydrop
BIN_DIR     := bin
DIST_DIR    := $(BIN_DIR)/dist
COVER_FILE  := $(BIN_DIR)/coverage.out
VERSION_FILE := internal/version/version.go
CHANGELOG   := CHANGELOG.md

# Version is stamped into internal/version.Version (used by `--version` and
# the MCP serverInfo). Override explicitly for releases: `make build VERSION=1.2.3`.
#
# The leading `v` of a tag is stripped: `git describe` returns the tag name
# verbatim, so without this a local build after tagging would report
# `v1.0.0` while the tagged CI release reports `1.0.0`. A commit hash has no
# `v` to strip, and a repo with no commits falls back to `dev` rather than to an
# empty string.
VERSION     ?= $(shell _v=$$(git describe --tags --always --dirty 2>/dev/null); \
	if [ -n "$$_v" ]; then echo "$${_v#v}"; else echo dev; fi)
LDFLAGS     := -s -w -X easydrop/internal/version.Version=$(VERSION)

# PREFIX follows the usual GNU layout, so the binary lands in $(PREFIX)/bin.
# The default is a per-user prefix: `make install` needs no sudo and normally
# targets a directory that is already on PATH. For a system-wide install use
#   sudo make install PREFIX=/usr/local
PREFIX      ?= $(HOME)/.local
BINDIR      := $(PREFIX)/bin

GO          ?= go
GOFILES     := $(shell find . -name '*.go' -not -path './$(BIN_DIR)/*')

# Cross-compilation matrix for NFR-03 (single binary per platform).
# Single source of truth: the CI workflow lists the same triples and `make
# check-platforms` fails the build if the two ever disagree.
PLATFORMS   := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
WORKFLOW    := .github/workflows/ci.yml

# --- help --------------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@echo "EasyDrop make targets:"
	@echo
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Current VERSION: $(VERSION)"

# --- build / run -------------------------------------------------------------

.PHONY: build
build: ## Compile the single binary into bin/
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(CMD)
	@echo "built $(BIN_DIR)/$(BINARY) ($(VERSION))"

.PHONY: install
install: build ## Install the binary into $(PREFIX)/bin (default: ~/.local/bin, no sudo)
	@mkdir -p $(BINDIR)
	install -m 0755 $(BIN_DIR)/$(BINARY) $(BINDIR)/$(BINARY)
	@echo "installed $(BINDIR)/$(BINARY) ($(VERSION))"
	@case ":$$PATH:" in \
		*":$(BINDIR):"*) echo "on PATH: $(BINDIR)/$(BINARY)" ;; \
		*) echo ""; \
		   echo "WARNING: $(BINDIR) is not on your PATH, so '$(BINARY)' will not resolve."; \
		   echo "         add it once:  echo 'export PATH=\"\$$PATH:$(BINDIR)\"' >> ~/.bashrc && source ~/.bashrc" ;; \
	esac

.PHONY: uninstall
uninstall: ## Remove the installed binary from $(PREFIX)/bin
	@rm -f $(BINDIR)/$(BINARY) && echo "removed $(BINDIR)/$(BINARY)"

.PHONY: run
run: build ## Build, then run the CLI (pass ARGS="status -n 20")
	./$(BIN_DIR)/$(BINARY) $(ARGS)

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)
	$(GO) clean -testcache

.PHONY: deps
deps: ## Download Go module dependencies
	$(GO) mod download

.PHONY: tidy
tidy: ## Run go mod tidy
	$(GO) mod tidy

# --- quality -----------------------------------------------------------------

.PHONY: fmt
fmt: ## Format all Go files
	$(GO) fmt ./...

.PHONY: fmtcheck
fmtcheck: ## Fail if any Go file is not gofmt-clean
	@unformatted=$$(gofmt -l $(GOFILES)); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi
	@echo "gofmt: clean"

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: fmtcheck vet ## Static checks (gofmt + go vet)

.PHONY: test
test: ## Run the unit test suite
	$(GO) test ./... -count=1

.PHONY: test-race
test-race: ## Run the test suite with the race detector
	$(GO) test ./... -count=1 -race

.PHONY: cover
cover: ## Run tests with coverage and open the HTML report
	@mkdir -p $(BIN_DIR)
	$(GO) test ./... -count=1 -coverprofile=$(COVER_FILE) -covermode=atomic
	$(GO) tool cover -func=$(COVER_FILE) | tail -1
	@echo "html report: file://$(PWD)/$(COVER_FILE)"

.PHONY: check-version
check-version: ## Fail unless the source version and CHANGELOG match EXPECTED (e.g. EXPECTED=1.0.0)
	@test -n "$(EXPECTED)" || { echo "usage: make check-version EXPECTED=1.0.0"; exit 1; }
	@src=$$(sed -n 's/^var Version = "\(.*\)"$$/\1/p' $(VERSION_FILE)); \
	if [ -z "$$src" ]; then echo "could not read 'var Version' from $(VERSION_FILE)"; exit 1; fi; \
	if [ "$$src" != "$(EXPECTED)" ]; then \
		echo "version mismatch – a release whose binary reports a different number"; \
		echo "  than its tag is worse than no release at all:"; \
		echo "  tag / EXPECTED: $(EXPECTED)"; \
		echo "  $(VERSION_FILE): $$src"; \
		echo "fix $(VERSION_FILE), or tag $$src instead."; \
		exit 1; \
	fi; \
	if [ -f $(CHANGELOG) ] && ! grep -qF "## [$(EXPECTED)]" $(CHANGELOG) \
		&& ! grep -qF "## $(EXPECTED)" $(CHANGELOG); then \
		echo "$(CHANGELOG) has no '## [$(EXPECTED)]' entry – add the release notes"; \
		echo "before tagging, or bump the version if this change is not part of $(EXPECTED)."; \
		exit 1; \
	fi; \
	echo "version: $(EXPECTED) matches $(VERSION_FILE) and $(CHANGELOG)"

.PHONY: verify
verify: fmtcheck vet check-platforms test ## CI gate: formatting + vet + platform matrix + tests

.PHONY: ci
ci: verify ## Alias for `make verify`

# --- release -----------------------------------------------------------------

.PHONY: platforms
platforms: ## List the release platforms, one per line
	@for p in $(PLATFORMS); do echo $$p; done

# build-platform compiles exactly one triple. CI calls it once per matrix entry
# so a broken platform fails that entry alone, and `release` calls it for all of
# them – one build path, so the two cannot drift in flags or output naming.
# CGO is off throughout: the binary only talks to Docker over a socket, so a
# static build is both correct and the reason one Linux runner can produce every
# target without a cross toolchain.
.PHONY: build-platform
build-platform:
	@test -n "$(PLATFORM)" || { echo "PLATFORM is required, e.g. PLATFORM=darwin/arm64 (see: make platforms)"; exit 1; }
	@mkdir -p $(DIST_DIR)
	@os=$${PLATFORM%/*}; arch=$${PLATFORM#*/}; \
	case "$$os/$$arch" in */*) ;; *) echo "bad PLATFORM '$(PLATFORM)', want <os>/<arch>"; exit 1 ;; esac; \
	ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	out="$(DIST_DIR)/$(BINARY)_$${os}_$${arch}$$ext"; \
	echo "building $$out"; \
	GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
		$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o "$$out" $(CMD)

# check-platforms keeps the CI matrix and PLATFORMS in step. Two lists of
# platforms is two chances to ship a release that quietly stops building for
# one of them, so CI treats the disagreement as an error rather than a nit.
.PHONY: check-platforms
check-platforms: ## Fail if the CI matrix and PLATFORMS disagree
	@if [ ! -f "$(WORKFLOW)" ]; then echo "$(WORKFLOW) not present, nothing to compare"; exit 0; fi
	@want=$$(for p in $(PLATFORMS); do echo $$p | tr / -; done | sort | tr '\n' ' '); \
	got=$$(grep -oE 'platform: (linux|darwin|windows)/[a-z0-9]+' "$(WORKFLOW)" \
		| sed 's/platform: //' | tr / - | sort -u | tr '\n' ' '); \
	if [ "$$want" != "$$got" ]; then \
		echo "platform matrix drift between the Makefile and $(WORKFLOW):"; \
		echo "  make:    $$want"; \
		echo "  ci.yml:  $$got"; \
		echo "fix one of them, then re-run: make check-platforms"; \
		exit 1; \
	fi; \
	echo "platform matrix: $(words $(PLATFORMS)) triples, Makefile and CI agree"

.PHONY: release
release: clean ## Cross-compile all release binaries + checksums into bin/dist/
	@for platform in $(PLATFORMS); do \
		$(MAKE) --no-print-directory build-platform PLATFORM=$$platform VERSION=$(VERSION) || exit 1; \
	done
	@cd $(DIST_DIR) && sha256sum * > SHA256SUMS
	@echo
	@ls -1 $(DIST_DIR)
	@echo "checksums: $(DIST_DIR)/SHA256SUMS"

.PHONY: version
version: ## Print the version that would be stamped
	@echo $(VERSION)

# --- end-to-end smoke (requires a working local docker) ----------------------

.PHONY: docker-check
docker-check: ## Fail fast when docker is unusable
	@command -v docker >/dev/null || { echo "docker not found"; exit 1; }
	@docker info >/dev/null 2>&1 || { echo "docker daemon unreachable"; exit 1; }

.PHONY: smoke
smoke: build docker-check ## End-to-end: deploy a throwaway app locally, then tear it down
	@set -euo pipefail; \
	work=$$(mktemp -d); \
	ed=$$(pwd)/$(BIN_DIR)/$(BINARY); \
	cleanup() { \
		[ -n "$${SMOKE_KEEP:-}" ] || { \
			docker rm -f smoke-active smoke-green smoke-active-previous >/dev/null 2>&1 || true; \
			docker rmi easydrop/smoke:latest >/dev/null 2>&1 || true; \
			rm -rf "$$work"; \
			[ -z "$$created_staging" ] || rm -rf "$$created_staging"; \
		}; \
	}; \
	trap cleanup EXIT; \
	created_staging=""; \
	if docker info --format '{{.DockerRootDir}}' 2>/dev/null | grep -q '^/var/snap/docker'; then \
		if [ -z "$${EASYDROP_STAGING_BASE:-}" ]; then \
			export EASYDROP_STAGING_BASE="$$HOME/easydrop-staging"; \
			created_staging="$$EASYDROP_STAGING_BASE"; \
		fi; \
		echo "note: snap docker detected, staging moved to $$EASYDROP_STAGING_BASE"; \
	fi; \
	printf 'FROM nginx:alpine\n' > "$$work/Dockerfile"; \
	echo "== init ==";   (cd "$$work" && $$ed init); \
	{ \
		echo '[app]'; \
		echo 'name = "smoke"'; \
		echo 'port = 80'; \
		echo 'host_port = 18087'; \
		echo ''; \
		echo '[server]'; \
		echo 'host = "localhost"'; \
		echo ''; \
		echo '[build]'; \
		echo 'strategy = "remote"'; \
		echo ''; \
		echo '[driver]'; \
		echo 'type = "single"'; \
	} > "$$work/easydrop.toml"; \
	echo "== deploy =="; (cd "$$work" && $$ed deploy -c easydrop.toml --skip-bootstrap); \
	echo "== status =="; (cd "$$work" && $$ed status -c easydrop.toml); \
	echo "== logs ==";   (cd "$$work" && $$ed logs -n 3 -c easydrop.toml); \
	echo "== smoke OK =="
