.PHONY: build build-release build-release-all bootstrap test fuzz test-race test-integration test-msb compile-all vulncheck deadcode deadcode-test mutation coverage coverage-junit lint fmt validate-fmt run cleanup-branches check verify all clean install-bash-completion install-head install-head-and-bash-completion upgrade-deps docs-diagrams docs-linkcheck docs-linkcheck-test changelog-check changelog-check-test docs-serve

VERSION ?= dev

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
ZIG_TARGET ?=
PLANTUML ?= plantuml
# keep in sync with versions in .agents-sandbox/Dockerfile
GO_VERSION = $(shell cat .go-version)
GOLANGCI_LINT_VERSION = 2.15.0
ZIG_VERSION ?= 0.17.0
# zig's install root (the dir holding lib/libc/...), following a symlink to the binary so
# it resolves whether zig is reached via ~/.local/bin or the ~/.local/zig install-root
# symlink. Empty when zig is absent. Assumes a flat tarball layout: the binary's directory
# is the libc root. A package-manager layout (Homebrew: bin/ and lib/ siblings) is not
# resolved and the stub probe below will miss — see specs/darwin-cross-compile.md.
ZIG_ROOT = $(shell f="$$(command -v zig)"; [ -n "$$f" ] || exit 0; cd "$$(dirname "$$f")" 2>/dev/null || exit 0; t="$$(readlink "$$f" 2>/dev/null || true)"; if [ -n "$$t" ]; then cd "$$(dirname "$$t")" 2>/dev/null || exit 0; fi; pwd -P)
# Realpath of the bootstrap-managed zig install root, or empty when absent. Lets
# the recipe tell the managed zig apart from one PATH resolves to elsewhere.
ZIG_MANAGED_ROOT = $(shell cd "$$HOME/.local/zig" 2>/dev/null && pwd -P)
GOTESTSUM_VERSION = 1.13.0
GOVULNCHECK_VERSION = v1.9.0
GOMUTANTS_VERSION = v0.7.2
DEADCODE_VERSION = v0.52.0
# Per-binary cap for the unit suite: a wedged test panics with a stack dump
# instead of blocking for go test's default 10m. Override for a slow machine.
TEST_TIMEOUT ?= 5m
# The SDK's microsandbox_ffi_path tag swaps its //go:embed for reading the FFI
# library from $MICROSANDBOX_FFI_PATH at runtime. The unit and integration
# suites reach the SDK only through seams, so they never read that path and need
# no env var — the ~36 MB library simply leaves the test binary. The msb-tagged
# tests keep the embed: they are the tests that load it.
NO_EMBEDDED_FFI_TAG = microsandbox_ffi_path

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
	@command -v zig >/dev/null 2>&1 || { echo "zig not found on PATH; run 'make bootstrap', then put \$$HOME/.local/bin on PATH"; exit 1; }
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    CGO_CFLAGS="-isystem $(ZIG_ROOT)/lib/libc/include/any-darwin-any" \
	    CGO_LDFLAGS="-F $(ZIG_ROOT)/lib/libc/darwin/System/Library/Frameworks -L $(ZIG_ROOT)/lib/libc/darwin/usr/lib -Wl,-undefined,dynamic_lookup" \
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
# version, golangci-lint, and Zig (for cross-compilation). Idempotent. Zig lands
# in ~/.local/zig-<arch>-<os>-<version> with a stable ~/.local/zig install-root
# symlink, a ~/.local/bin/zig binary symlink, and the macOS cross-compile stubs
# baked in. A zig that PATH resolves to elsewhere is left untouched and reported.
bootstrap:
	@command -v goenv >/dev/null 2>&1 || { echo "goenv is required; see CONTRIBUTING.md"; exit 1; }
	@if goenv versions 2>/dev/null | grep -qw "$(GO_VERSION)"; then \
	    echo "go $(GO_VERSION) already installed"; \
	else \
	    goenv install "$(GO_VERSION)"; \
	fi
	goenv tools install "golangci-lint@v$(GOLANGCI_LINT_VERSION)"
	@MANAGED_ROOT='$(ZIG_MANAGED_ROOT)'; \
	ON_PATH_ROOT='$(ZIG_ROOT)'; \
	ZIG_ON_PATH="$$(command -v zig 2>/dev/null || true)"; \
	if [ -n "$$ZIG_ON_PATH" ] && [ "$$ON_PATH_ROOT" != "$$MANAGED_ROOT" ]; then \
	    echo "warning: zig resolves to $$ZIG_ON_PATH ($$(zig version 2>/dev/null || echo unknown)) outside the managed install $${HOME}/.local/zig; leaving it untouched" >&2; \
	    { [ ! -f "$$ON_PATH_ROOT/lib/libc/darwin/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation.tbd" ] \
	   || [ ! -f "$$ON_PATH_ROOT/lib/libc/darwin/System/Library/Frameworks/Security.framework/Security.tbd" ] \
	   || [ ! -f "$$ON_PATH_ROOT/lib/libc/darwin/usr/lib/libresolv.tbd" ]; } \
	 && echo "warning: no macOS cross-compile stubs found under $$ON_PATH_ROOT/lib/libc/darwin; a darwin build with this zig may fail to link CoreFoundation/Security/resolv" >&2; \
	    true; \
	else \
	    ZIG_MANAGED_VERSION=""; \
	    if [ -x "$${HOME}/.local/zig/zig" ]; then ZIG_MANAGED_VERSION="$$("$${HOME}/.local/zig/zig" version 2>/dev/null || true)"; fi; \
	    if [ "$$ZIG_MANAGED_VERSION" != "$(ZIG_VERSION)" ]; then \
	        case "$$(uname -m)" in aarch64|arm64) ZIG_ARCH=aarch64 ;; *) ZIG_ARCH=x86_64 ;; esac; \
	        case "$$(uname -s)" in Darwin) ZIG_OS=macos ;; *) ZIG_OS=linux ;; esac; \
	        ZIG_DIR=zig-$${ZIG_ARCH}-$${ZIG_OS}-$(ZIG_VERSION); \
	        mkdir -p "$${HOME}/.local" \
	     && curl -fsSL "https://ziglang.org/download/$(ZIG_VERSION)/$${ZIG_DIR}.tar.xz" -o /tmp/zig.tar.xz \
	     && tar -xf /tmp/zig.tar.xz -C "$${HOME}/.local" \
	     && rm /tmp/zig.tar.xz \
	     && ln -sfn "$${HOME}/.local/$${ZIG_DIR}" "$${HOME}/.local/zig" \
	     && echo "zig $(ZIG_VERSION) installed under $${HOME}/.local" \
	     || { echo "error: installing zig $(ZIG_VERSION) failed" >&2; exit 1; }; \
	    fi; \
	    mkdir -p "$${HOME}/.local/bin" \
	 && ln -sfn "$${HOME}/.local/zig/zig" "$${HOME}/.local/bin/zig" \
	 && echo "zig binary link ensured at $${HOME}/.local/bin/zig" \
	 || { echo "error: creating zig binary link failed" >&2; exit 1; }; \
	    ZIG_DARWIN="$${HOME}/.local/zig/lib/libc/darwin"; \
	    mkdir -p "$$ZIG_DARWIN/System/Library/Frameworks/CoreFoundation.framework" \
	             "$$ZIG_DARWIN/System/Library/Frameworks/Security.framework" \
	             "$$ZIG_DARWIN/usr/lib" \
	 && cp .github/stubs/CoreFoundation.tbd "$$ZIG_DARWIN/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation.tbd" \
	 && cp .github/stubs/Security.tbd "$$ZIG_DARWIN/System/Library/Frameworks/Security.framework/Security.tbd" \
	 && cp .github/stubs/libresolv.tbd "$$ZIG_DARWIN/usr/lib/libresolv.tbd" \
	 && echo "macOS cross-compile stubs ensured in $$ZIG_DARWIN" \
	 || { echo "error: installing zig stubs failed" >&2; exit 1; }; \
	    if [ -z "$$ZIG_ON_PATH" ]; then \
	        echo "note: 'zig' is not on PATH; add $${HOME}/.local/bin (managed zig: $${HOME}/.local/zig/zig)"; \
	    fi; \
	fi

test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -timeout $(TEST_TIMEOUT) ./...

# Race-detector run across the module. The launcher runs concurrent state
# transactions, legacy-volume copy campaigns, daemon health polling, and
# watchers, so a data race is a real bug; kept out of `test`/`check` because
# it is substantially slower.
test-race:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -race ./...

# Drive the fuzz targets with generated input for a bounded time per target.
# The seed corpus (and any committed crasher) already runs as part of `make
# test`, so this only explores new inputs; it needs no network and no VM.
# Targets are discovered from the test sources because `go test -fuzz` refuses
# to run against more than one package at once and accepts exactly one target
# per run, so a package's targets are enumerated and each runs on its own. A
# failing target does not stop the others. FUZZPKG=<package path> narrows the
# run to one package and FUZZTARGET=<func name> to one target (alone, or within
# FUZZPKG), which is how CI splits the work: one job per target, so a package
# with several targets does not multiply a single job's budget. A narrowed run
# that matches nothing fails loudly rather than reading as a green sweep.
FUZZTIME ?= 30s
FUZZPKG ?=
FUZZTARGET ?=
fuzz:
	@pkgs=$$(grep -rl '^func Fuzz' --include='*_test.go' internal cmd test | xargs -n1 dirname | sort -u); \
	if [ -n "$(FUZZPKG)" ]; then pkgs=$$(printf '%s\n' "$$pkgs" | grep -x -F "$(FUZZPKG)" || true); fi; \
	if [ -z "$$pkgs" ]; then echo "no fuzz targets found for FUZZPKG='$(FUZZPKG)'"; exit 1; fi; \
	status=0; matched=0; \
	for pkg in $$pkgs; do \
		targets=$$(grep -rhoE '^func Fuzz[A-Za-z0-9_]+' --include='*_test.go' "$$pkg" | awk '{print $$2}' | sort -u); \
		if [ -n "$(FUZZTARGET)" ]; then targets=$$(printf '%s\n' "$$targets" | grep -x -F "$(FUZZTARGET)" || true); fi; \
		for target in $$targets; do \
			matched=$$((matched + 1)); \
			echo "== fuzz $$pkg $$target"; \
			CGO_ENABLED=1 go test -run='^$$' -fuzz="^$$target$$" -fuzztime=$(FUZZTIME) "./$$pkg" || status=1; \
		done; \
	done; \
	if [ -n "$(FUZZTARGET)" ] && [ "$$matched" -eq 0 ]; then echo "no fuzz target found for FUZZTARGET='$(FUZZTARGET)'"; exit 1; fi; \
	exit $$status

# Integration tests across the module: the RenderDockerfile docker-build tests
# in internal/sandbox/image and the real Docker-daemon delegation tests in
# internal/sandbox/docker. Excluded from `test`/`check` via the `integration`
# build tag. Requires a working docker daemon and network access (base image
# pulls, agent installs, registry round-trips).
test-integration:
	CGO_ENABLED=1 go test -tags 'integration $(NO_EMBEDDED_FFI_TAG)' -timeout 60m -count=1 ./...

# Harness smoke tests plus the real-client msb tests: start every registered
# harness to its TUI in a real microsandbox VM, and drive the real SDK client
# against the host runtime. Host-only (needs /dev/kvm, a docker daemon, and
# network); excluded from test/check via the `msb` build tag. These keep the
# embedded native library: they are the tests that actually load it.
test-msb:
	CGO_ENABLED=1 go test -tags msb -timeout 60m -count=1 ./test/harness/... ./internal/sandbox/msb/...

# Compile every test binary including build-tagged files (`//go:build
# integration`, `//go:build msb`) so a regression that only breaks tagged code
# is caught without a docker daemon or VM. `-run=^$` matches no test.
compile-all:
	CGO_ENABLED=1 go test -tags 'integration msb' -run='^$$' ./...

# Scan the module for reachable vulnerabilities against the Go vulnerability
# database. Needs network access on first run (and to refresh the DB); exits
# non-zero when a reachable vulnerability is found.
vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

# Fail when production code is unreachable from any main package and carries no
# reason-bearing //deadcode:ignore marker. deadcode always exits 0, so
# ci/check-deadcode.sh decides the verdict; a failed analysis is a hard error,
# never a silent pass. Analyses the host's own GOOS, once per build-tag
# configuration. See the specs/dead-code-gate contract in .operator-shared/.
#   make deadcode
#   make deadcode PKG=./cmd/...         # inspection only; stale markers not checked
#   make deadcode TAGS=integration      # one configuration instead of the matrix
deadcode:
	DEADCODE_VERSION='$(DEADCODE_VERSION)' PKG='$(PKG)' $(if $(TAGS),TAGS='$(TAGS)',) ci/check-deadcode.sh

# Test the dead-code gate script itself (fixtures only: never runs deadcode).
deadcode-test:
	ci/test-check-deadcode.sh

# Mutation testing: mutate the code and check whether the tests notice. Run on
# demand (at release time) and as an advisory CI signal, never as a gate — a
# full run takes minutes per package and a survivor needs human triage, so it
# stays out of check/verify and is never a required CI check (the `mutation`
# workflow runs it on every code-changing PR). By default it mutates
# only lines changed since the last release tag; FULL=1 runs every mutant and
# PKG picks the package(s).
#   make mutation                        # whole module, lines changed since the last tag
#   make mutation PKG=./internal/foo     # one package, changed lines
#   make mutation FULL=1                 # whole module, all lines (slow)
#   make mutation ANNOTATIONS=github     # also print ::warning:: annotations (CI only)
PKG   ?= ./...
TAGS  ?=
SINCE ?= $(shell git describe --tags --abbrev=0)
FULL  ?=
# Empty by default, so a plain `make mutation` prints no workflow commands. The
# `mutation` CI workflow sets ANNOTATIONS=github so each survivor lands on the
# PR's diff; locally that output is just noise, hence the opt-in.
ANNOTATIONS ?=
mutation:
	@test -n "$(FULL)" || test -n "$(SINCE)" || { \
		echo "make mutation: cannot resolve a release tag; pass SINCE=<ref> or FULL=1"; exit 1; }
	CGO_ENABLED=1 go run github.com/gomutants/gomutants@$(GOMUTANTS_VERSION) \
		--detect-equivalent \
		$(if $(ANNOTATIONS),--annotations $(ANNOTATIONS)) \
		$(if $(FULL),,--changed-since $(SINCE)) \
		$(PKG)

coverage:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -timeout $(TEST_TIMEOUT) -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

# Single test run that also emits JUnit XML (junit.xml) for Codecov Test Analytics.
coverage-junit:
	CGO_ENABLED=1 go run gotest.tools/gotestsum@v$(GOTESTSUM_VERSION) --junitfile junit.xml -- -tags $(NO_EMBEDDED_FFI_TAG) -timeout $(TEST_TIMEOUT) -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

validate-fmt:
	golangci-lint fmt -d ./...

run:
	go run ./cmd/agents-sandbox

# Local repository maintenance: remove merged worktrees and branches, then report the rest.
# Forward flags with ARGS, e.g. `make cleanup-branches ARGS=--dry-run`.
cleanup-branches:
	./scripts/cleanup-merged-branches.sh $(ARGS)

check: fmt lint test compile-all docs-linkcheck docs-linkcheck-test changelog-check changelog-check-test deadcode-test

# Read-only check for CI parity: fmt-check + lint + test + race + vulncheck (does not modify files).
verify: validate-fmt lint test test-race compile-all vulncheck deadcode docs-linkcheck docs-linkcheck-test changelog-check changelog-check-test deadcode-test

all: fmt lint test docs-linkcheck docs-linkcheck-test changelog-check changelog-check-test build

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

# Validate the changelog fragment convention (shape). See specs/changelog-fragments.md.
changelog-check:
	ci/check-changelog.sh

# Test the changelog fragment checker itself.
changelog-check-test:
	ci/test-check-changelog.sh

# Serve the docs locally the same way GitHub Pages does (Jekyll build + live reload) at http://localhost:4000/.
docs-serve: docs-diagrams
	cd docs && bundle config set --local path vendor/bundle && bundle install && bundle exec jekyll serve --livereload

install-head: build
	mkdir -p ~/.local/bin
	cp agents-sandbox ~/.local/bin/agents-sandbox.tmp
	mv -f ~/.local/bin/agents-sandbox.tmp ~/.local/bin/agents-sandbox

install-head-and-bash-completion: install-head install-bash-completion

upgrade-deps:
	go get -u ./...
	go mod tidy
