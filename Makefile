.PHONY: build build-release build-release-all bootstrap test test-integration test-integration-msb compile-all coverage coverage-junit lint fmt validate-fmt run check verify all clean install-bash-completion install-head install-head-and-bash-completion upgrade-deps docs-diagrams docs-linkcheck docs-linkcheck-test docs-serve

VERSION ?= dev

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
ZIG_TARGET ?=
PLANTUML ?= plantuml
# keep in sync with versions in .agents-sandbox/Dockerfile
GO_VERSION = $(shell cat .go-version)
GOLANGCI_LINT_VERSION = 2.14.0
ZIG_VERSION ?= 0.17.0
GOTESTSUM_VERSION = 1.13.0

ARTIFACT = agents-sandbox-$(GOOS)-$(GOARCH)

RELEASE_TARGETS = \
	linux/amd64/x86_64-linux-gnu \
	linux/arm64/aarch64-linux-gnu \
	darwin/arm64/aarch64-macos.11.0-none

build:
	CGO_ENABLED=1 go build -ldflags "-X main.version=$(VERSION)" -o agents-sandbox ./cmd/agents-sandbox

build-release: export CC=zig cc -target $(ZIG_TARGET)
build-release: export CXX=zig c++ -target $(ZIG_TARGET)

build-release:
	@test -n "$(ZIG_TARGET)" || { echo "ZIG_TARGET is required (e.g. x86_64-linux-gnu)"; exit 1; }
	@echo "building $(ARTIFACT) (zig target: $(ZIG_TARGET))"
ifeq ($(GOOS),darwin)
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    CGO_CFLAGS="-isystem $(shell dirname $(shell which zig))/lib/libc/include/any-darwin-any" \
	    CGO_LDFLAGS="-F $(shell dirname $(shell which zig))/lib/libc/darwin/System/Library/Frameworks -L $(shell dirname $(shell which zig))/lib/libc/darwin/usr/lib -Wl,-undefined,dynamic_lookup" \
	    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" -o $(ARTIFACT) ./cmd/agents-sandbox
else
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" -o $(ARTIFACT) ./cmd/agents-sandbox
endif

build-release-all:
	@for t in $(RELEASE_TARGETS); do \
	    goos=$${t%/*/*}; rest=$${t#*/}; goarch=$${rest%/*}; zig=$${t##*/}; \
	    $(MAKE) build-release GOOS=$$goos GOARCH=$$goarch ZIG_TARGET=$$zig; \
	done


# One-shot dev environment bootstrap: requires goenv; installs the pinned Go
# version, golangci-lint, and Zig (for cross-compilation). Idempotent.
bootstrap:
	@command -v goenv >/dev/null 2>&1 || { echo "goenv is required; see CONTRIBUTING.md"; exit 1; }
	@if goenv versions 2>/dev/null | grep -qw "$(GO_VERSION)"; then \
	    echo "go $(GO_VERSION) already installed"; \
	else \
	    goenv install "$(GO_VERSION)"; \
	fi
	goenv tools install "golangci-lint@v$(GOLANGCI_LINT_VERSION)"
	@if command -v zig >/dev/null 2>&1 && [ "$$(zig version)" = "$(ZIG_VERSION)" ]; then \
	    echo "zig $(ZIG_VERSION) already installed"; \
	else \
	    ZIG_ARCH=$$(if [ "$$(uname -m)" = aarch64 ]; then echo aarch64; else echo x86_64; fi); \
	    if [ "$$(uname -s)" = "Darwin" ]; then \
	        ZIG_OS=macos; ZIG_PREFIX="$${HOME}/.local"; \
	    else \
	        ZIG_OS=linux; ZIG_PREFIX=/usr/local; \
	    fi; \
	    ZIG_TARBALL=zig-$${ZIG_ARCH}-$${ZIG_OS}-$(ZIG_VERSION).tar.xz; \
	    ZIG_DIR=zig-$${ZIG_ARCH}-$${ZIG_OS}-$(ZIG_VERSION); \
	    mkdir -p "$$ZIG_PREFIX"; \
	    curl -fsSL "https://ziglang.org/download/$(ZIG_VERSION)/$${ZIG_TARBALL}" -o /tmp/zig.tar.xz \
	 && tar -xf /tmp/zig.tar.xz -C "$$ZIG_PREFIX" \
	 && rm /tmp/zig.tar.xz \
	 && ln -sfn "$$ZIG_PREFIX/$${ZIG_DIR}" "$$ZIG_PREFIX/zig"; \
	fi

test:
	CGO_ENABLED=1 go test ./...

# Integration tests across the module, moby backend: real image builds through an
# existing Docker daemon's Engine API, so no /dev/kvm or msb runtime is needed.
# Runs on any host with a Docker daemon (the PR tier). Excluded from
# `test`/`check` via the `integration` build tag. AGENTS_SANDBOX_INTEGRATION is
# set here.
test-integration:
	AGENTS_SANDBOX_INTEGRATION=1 AGENTS_SANDBOX_BUILDER=moby CGO_ENABLED=1 go test -tags integration -count=1 ./...

# Integration tests across the module, msb backend: the real BuildKit builder-VM
# image-build, registry cache-reuse, shared builder-VM, and login-shell PATH tests
# in internal/sandbox/image. Requires the msb runtime, a functional /dev/kvm, and
# network access (base image and registry pulls, agent installs). The gated tier
# (main/tags/manual).
test-integration-msb:
	AGENTS_SANDBOX_INTEGRATION=1 AGENTS_SANDBOX_BUILDER=msb CGO_ENABLED=1 go test -tags integration -count=1 ./...

# Compile every test binary including build-tagged files (`//go:build
# integration`) so a regression that only breaks tagged code is caught without a
# docker daemon. `-run=^$` matches no test, so nothing is executed.
compile-all:
	CGO_ENABLED=1 go test -tags integration -run='^$$' ./...

coverage:
	CGO_ENABLED=1 go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

# Single test run that also emits JUnit XML (junit.xml) for Codecov Test Analytics.
coverage-junit:
	CGO_ENABLED=1 go run gotest.tools/gotestsum@v$(GOTESTSUM_VERSION) --junitfile junit.xml -- -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

validate-fmt:
	golangci-lint fmt -d ./...

run:
	go run ./cmd/agents-sandbox

check: fmt lint test compile-all docs-linkcheck docs-linkcheck-test

# Read-only check for CI parity: fmt-check + lint + test (does not modify files).
verify: validate-fmt lint test compile-all docs-linkcheck docs-linkcheck-test

all: fmt lint test docs-linkcheck docs-linkcheck-test build

clean:
	rm -f agents-sandbox
	rm -f agents-sandbox-linux-* agents-sandbox-darwin-*
	rm -f coverage.out junit.xml

install-bash-completion:
	mkdir -p ~/.local/share/bash-completion/completions
	go run ./cmd/agents-sandbox completion bash > ~/.local/share/bash-completion/completions/agents-sandbox

# Render PlantUML diagrams in docs/diagrams/ to SVG (local preview; CI re-renders on release).
# Excludes the vendored C4-PlantUML library files (C4*.puml), which are not standalone diagrams.
docs-diagrams:
	cd docs/diagrams && for f in *.puml; do case "$$f" in C4*) ;; *) $(PLANTUML) -DRELATIVE_INCLUDE -tsvg -o . "$$f" || exit 1;; esac; done

# Validate documentation links and linking style (run by `make check` and CI).
docs-linkcheck:
	ci/check-docs.sh

# Test the documentation link checker itself.
docs-linkcheck-test:
	ci/test-check-docs.sh

# Serve the docs locally the same way GitHub Pages does (Jekyll build + live reload) at http://localhost:4000/.
docs-serve:
	cd docs && bundle install && bundle exec jekyll serve --livereload

install-head:
	mkdir -p ~/.local/bin
	cp agents-sandbox ~/.local/bin/agents-sandbox.tmp
	mv -f ~/.local/bin/agents-sandbox.tmp ~/.local/bin/agents-sandbox

install-head-and-bash-completion: build install-head install-bash-completion

upgrade-deps:
	go get -u ./...
	go mod tidy
