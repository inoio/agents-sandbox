.PHONY: build build-release build-release-all bootstrap test test-uncached fuzz test-race test-integration test-msb test-msb-cold test-msb-journeys test-msb-in-vm test-msb-reuse msb-reuse-root compile-all vulncheck deadcode deadcode-test mutation coverage coverage-junit lint actionlint fmt validate-fmt run cleanup-branches cleanup-branches-test surface-sweep-test test-scrubbed test-hermetic test-hermetic-sentinel envelope-test check check-test verify print-check-targets check-local-changes check-branch-changes check-selected changed-files-test changed-targets-test changelog-presence all clean install-bash-completion install-head install-head-and-bash-completion upgrade-deps docs-diagrams docs-linkcheck docs-linkcheck-test ci-guards-check ci-guards-check-test changelog-check changelog-check-test enqueue-pr-test backport-audit-test open-backport-pr-test kvm-guest-test kvm-up kvm-sync kvm-prime kvm-ssh kvm-run kvm-agent kvm-agent-up kvm-agent-provision kvm-sync-back kvm-down event-base-test classify-changes-test brain-check brain-check-test shellcheck docs-serve

VERSION ?= dev

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
ZIG_TARGET ?=
PLANTUML ?= plantuml
# keep in sync with versions in .agents-sandbox/Dockerfile
GO_VERSION = $(shell cat .go-version)
GOLANGCI_LINT_VERSION = 2.15.0
ZIG_VERSION ?= 0.17.1
# zig's install root (the dir holding lib/libc/...), following a symlink to the binary so
# it resolves whether zig is reached via ~/.local/bin or the ~/.local/zig install-root
# symlink. Empty when zig is absent. Assumes a flat tarball layout: the binary's directory
# is the libc root. A package-manager layout (Homebrew: bin/ and lib/ siblings) is not
# resolved and the stub probe below will miss — see specs/release/darwin-cross-compile.md.
ZIG_ROOT = $(shell f="$$(command -v zig)"; [ -n "$$f" ] || exit 0; cd "$$(dirname "$$f")" 2>/dev/null || exit 0; t="$$(readlink "$$f" 2>/dev/null || true)"; if [ -n "$$t" ]; then cd "$$(dirname "$$t")" 2>/dev/null || exit 0; fi; pwd -P)
# Realpath of the bootstrap-managed zig install root, or empty when absent. Lets
# the recipe tell the managed zig apart from one PATH resolves to elsewhere.
ZIG_MANAGED_ROOT = $(shell cd "$$HOME/.local/zig" 2>/dev/null && pwd -P)
GOTESTSUM_VERSION = 1.13.0
GOVULNCHECK_VERSION = v1.9.0
ACTIONLINT_VERSION = v1.7.12
GOMUTANTS_VERSION = v0.7.3
DEADCODE_VERSION = v0.52.0
# Per-binary cap for the unit suite: a wedged test panics with a stack dump
# instead of blocking for go test's default 10m. Override for a slow machine.
TEST_TIMEOUT ?= 5m
# Bounded concurrency for the host-only tiers' parallel stages; both suites are
# otherwise serial across their top-level tests. test-integration uses
# INTEGRATION_PARALLEL. test-msb sizes its parallel harnesses from the host's
# free memory (test/msb/driver) and is additionally capped by an internal
# semaphore, so a bare `go test -tags msb` cannot start a VM per harness; set
# MSB_PARALLEL to a count to force it instead of the memory-derived default.
INTEGRATION_PARALLEL ?= 4
# Per-binary cap for the integration suite. The default mirrors the intent of
# the unit suite's cap; the serialized podman job raises it, since serializing
# the image builds lengthens the one binary that dominates the run.
INTEGRATION_TIMEOUT ?= 60m
MSB_PARALLEL ?=
# Per-smoke-VM vCPU cap for the in-guest KVM run (`test-msb-in-vm`). Each smoke
# VM otherwise claims every host vCPU, so parallel harnesses oversubscribe a
# nested guest's few vCPUs. Passed as AGENTS_SANDBOX_MSB_VM_CPUS.
MSB_GUEST_VM_CPUS ?= 2
# Parallel-harness count the in-guest KVM run uses when MSB_PARALLEL is unset.
# The host's memory-derived default sizes for a full host; a nested guest with
# MSB_GUEST_VM_CPUS-capped VMs wants a small fixed count instead.
MSB_GUEST_PARALLEL ?= 2
# Multiplier the in-guest KVM run applies to every smoke readiness and quit
# ceiling. A nested guest starts a VM far slower, so budgets sized for a host
# time out there. Passed as AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR.
MSB_GUEST_TIMEOUT_FACTOR ?= 2
# Launcher-side VM-setup readiness ceilings for the in-guest KVM run: the
# in-VM Docker engine and the agent serve daemon. Raised from the built-in 30s /
# 60s, which a slow nested guest can exceed. Passed as
# AGENTS_SANDBOX_DOCKER_READY_TIMEOUT / AGENTS_SANDBOX_DAEMON_READY_TIMEOUT.
MSB_GUEST_DOCKER_READY_TIMEOUT ?= 90s
MSB_GUEST_DAEMON_READY_TIMEOUT ?= 180s
# Standing root for the caching `test-msb`. Two constraints: it must stay short
# enough for microsandbox's derived sandbox control socket (see
# test/msb/driver/root_test.go — the smoke root must be under 30 bytes),
# and it must live on a real disk, because the suite caches 10+ GiB of runner
# images here. /tmp is a RAM-backed tmpfs where systemd mounts it as one, and
# /var/tmp often is too; a tmpfs root either exhausts at the largest runner
# image ("Disk quota exceeded") or silently spends 14+ GiB of RAM. /opt is
# disk-backed on Linux and macOS. `msb-reuse-root` creates it as your user where
# the parent is writable; otherwise run the one-time command it prints:
#   sudo install -d -o "$USER" -g "$USER" -m 0755 /opt/asb-reuse
# The KVM guest pre-creates it in cloud-init (scripts/kvm-guest.sh). Remove it to
# start fresh.
MSB_REUSE_ROOT ?= /opt/asb-reuse
# Host-only: the msb suites need a KVM host, and the dev VM has no working
# /dev/kvm. `make kvm-up` builds a libvirt guest and these run the suite inside
# it. See .operator-shared/guides/kvm-guest.md. Never run by CI.
KVM_RUN ?= make test-msb-in-vm
# The agent command `make kvm-agent` runs in the guest, and the agent profile
# `make kvm-agent-provision` materializes. Override KVM_AGENT to pass a command
# and credentials, e.g. KVM_AGENT='LITELLM_API_KEY=… opencode'.
KVM_AGENT ?= opencode2
KVM_AGENT_NAME ?= opencode2
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

# The sentinel installer reads AGENTS_SANDBOX_HERMETIC_SENTINEL in TestMain, before
# Go's test-cache log is active, so the variable is not part of the cache key: a
# cached PASS could stand in for a run that never pinned the seams. Force -count=1
# when the sentinel is requested, so the pass really runs.
TEST_COUNT = $(if $(AGENTS_SANDBOX_HERMETIC_SENTINEL),-count=1,)

test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -timeout $(TEST_TIMEOUT) $(TEST_COUNT) ./...

test-uncached:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -timeout $(TEST_TIMEOUT) -count=1 ./...

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
	CGO_ENABLED=1 go test -tags 'integration $(NO_EMBEDDED_FFI_TAG)' -timeout $(INTEGRATION_TIMEOUT) -count=1 -parallel $(INTEGRATION_PARALLEL) ./...

# Harness smoke tests plus the real-client msb tests: start every registered
# harness to its TUI in a real microsandbox VM, and drive the real SDK client
# against the host runtime. Host-only (needs /dev/kvm, a docker daemon, and
# network); excluded from test/check via the `msb` build tag. These keep the
# embedded native library: they are the tests that actually load it.
#
# Ensure the standing reuse root exists before `test-msb` runs. Without it the
# suite either fails deep in the image prebuild (a tmpfs root) or hits a bare
# permission error. Created as the current user where the parent allows it; on a
# stock host /opt is root-owned, so print the one-time command instead.
msb-reuse-root:
	@mkdir -p "$(MSB_REUSE_ROOT)" 2>/dev/null || { \
		printf '%s\n' \
			"error: cannot create $(MSB_REUSE_ROOT)." \
			"Create it once on a short, disk-backed path (not tmpfs):" \
			"  sudo install -d -o \"$$(id -un)\" -g \"$$(id -gn)\" -m 0755 $(MSB_REUSE_ROOT)" >&2; \
		exit 1; \
	}

# test-msb reuses a standing microsandbox home and smoke root, so the multi-GB
# `docker save | msb ImageLoad` transfer and the docker layers are paid once and
# each agent's runner image is rebuilt in place under one tag. The home outlives
# the run, so remove $(MSB_REUSE_ROOT) to start fresh, and a reused home is held
# under a lock so a second run waits rather than racing. Add -v for timings.
test-msb: msb-reuse-root
	AGENTS_SANDBOX_SMOKE_ROOT=$(MSB_REUSE_ROOT)/smoke \
	AGENTS_SANDBOX_MSB_TEST_HOME=$(MSB_REUSE_ROOT)/msb \
	AGENTS_SANDBOX_MSB_PARALLEL=$(MSB_PARALLEL) \
	CGO_ENABLED=1 go test -tags msb -timeout 60m -count=1 ./test/msb/startup/... ./internal/sandbox/msb/...

# Like test-msb, but on a fresh, isolated microsandbox home that caches nothing
# between runs. It forces the reuse variables unset, so an exported
# AGENTS_SANDBOX_SMOKE_ROOT or AGENTS_SANDBOX_MSB_TEST_HOME cannot turn it into a
# caching run. It still creates its fresh root under the disk-backed
# $(MSB_REUSE_ROOT) via AGENTS_SANDBOX_MSB_ROOT_BASE, because the fresh root holds
# the same 10+ GiB of runner images a tmpfs /tmp cannot. The suite removes the
# runner images it built (and its temp root),
# untagging only the leaf so the classic builder's layer cache survives, so a
# cold run leaves no tagged runner image.
test-msb-cold: msb-reuse-root
	AGENTS_SANDBOX_SMOKE_ROOT= AGENTS_SANDBOX_MSB_TEST_HOME= AGENTS_SANDBOX_MSB_ROOT_BASE=$(MSB_REUSE_ROOT) AGENTS_SANDBOX_MSB_PARALLEL=$(MSB_PARALLEL) CGO_ENABLED=1 go test -tags msb -timeout 60m -count=1 ./test/msb/startup/... ./internal/sandbox/msb/...

# The harness-journey acceptance suite: drives full user journeys (v1: learn
# mode on opencode2) through real harnesses in real VMs, beyond the startup
# smoke's "the TUI came up" gate. It shares test-msb's standing reuse root and
# image cache (so a journey's budget only absorbs its VM start), and the shared
# reuse lock serializes the two targets. Host-only; excluded from test/check via
# the `msb` build tag. See .operator-shared/specs/testing/msb-journeys.md.
test-msb-journeys: msb-reuse-root
	AGENTS_SANDBOX_SMOKE_ROOT=$(MSB_REUSE_ROOT)/smoke \
	AGENTS_SANDBOX_MSB_TEST_HOME=$(MSB_REUSE_ROOT)/msb \
	AGENTS_SANDBOX_MSB_PARALLEL=$(MSB_PARALLEL) \
	CGO_ENABLED=1 go test -tags msb -timeout 60m -count=1 ./test/msb/journeys/...

# The in-guest variant `kvm-run` drives by default: the same caching suite as
# test-msb, with changes that only matter on a nested guest. The two msb-tagged
# packages run serialized (`-p 1`), because each starts its own VMs and a nested
# guest cannot absorb both at once; each smoke VM's vCPUs are capped
# (AGENTS_SANDBOX_MSB_VM_CPUS), because a project VM otherwise claims every guest
# vCPU; the parallel-harness count defaults to MSB_GUEST_PARALLEL instead of the
# host-sized heuristic; and the readiness/quit ceilings are scaled
# (AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR). The host targets are unchanged — the cap
# env var is unset, the parallel default is untouched, and the factor is unset.
test-msb-in-vm: msb-reuse-root
	AGENTS_SANDBOX_MSB_VM_CPUS=$(MSB_GUEST_VM_CPUS) \
	AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR=$(MSB_GUEST_TIMEOUT_FACTOR) \
	AGENTS_SANDBOX_MSB_DOCKER_READY_TIMEOUT=$(MSB_GUEST_DOCKER_READY_TIMEOUT) \
	AGENTS_SANDBOX_MSB_DAEMON_READY_TIMEOUT=$(MSB_GUEST_DAEMON_READY_TIMEOUT) \
	AGENTS_SANDBOX_SMOKE_ROOT=$(MSB_REUSE_ROOT)/smoke \
	AGENTS_SANDBOX_MSB_TEST_HOME=$(MSB_REUSE_ROOT)/msb \
	AGENTS_SANDBOX_MSB_PARALLEL=$(or $(MSB_PARALLEL),$(MSB_GUEST_PARALLEL)) \
	CGO_ENABLED=1 go test -tags msb -p 1 -timeout 60m -count=1 ./test/msb/startup/... ./internal/sandbox/msb/...

# Host-only KVM-guest workflow. These build a libvirt guest (cloud-init seeds
# docker, Go, msb, and the repo), sync a checkout into it, and run a command
# there — the msb suites need a KVM host, which the dev VM is not. They are
# never run by CI. See .operator-shared/guides/kvm-guest.md.
kvm-up:
	scripts/kvm-guest.sh up

kvm-sync:
	scripts/kvm-guest.sh sync

# Copy the host's runner images and microsandbox home into the guest so its
# image prebuild is a no-op. Both sides must use the same $(MSB_REUSE_ROOT): the
# runner-image slug hashes the project directory's absolute path, so a different
# root names different images. See the guide.
kvm-prime:
	scripts/kvm-guest.sh prime

kvm-ssh:
	scripts/kvm-guest.sh ssh

kvm-run:
	scripts/kvm-guest.sh run '$(KVM_RUN)'

# Open an interactive agent session in the guest, in a tmux session so a dropped
# SSH connection leaves it running. Pass credentials as an inline assignment:
#   make kvm-agent KVM_AGENT='LITELLM_API_KEY=… opencode'
kvm-agent:
	scripts/kvm-guest.sh agent $(KVM_AGENT)

# Materialize the agent's provisioned config into the guest home, reusing the
# launcher's own config pipeline (`config provision`). Run `make kvm-sync` first
# so the checkout carries the command.
kvm-agent-provision:
	scripts/kvm-guest.sh agent-provision $(KVM_AGENT_NAME)

# One command from nothing to an interactive guest agent session: ensure the
# guest is running (idempotent — a created, stopped, or running guest all work),
# wait for cloud-init, sync this checkout, materialize the agent config, and
# attach. LITELLM_API_KEY is read from the host environment and handed to the
# agent; extend the credential set with KVM_AGENT='GITHUB_PAT=… opencode2'. If
# KVM_AGENT runs a different binary, set KVM_AGENT_NAME to the profile to
# materialize (default opencode2), so the config matches the agent attached. The
# step-by-step targets above stay for manual control.
kvm-agent-up:
	@test -n "$$LITELLM_API_KEY" || { printf 'error: set LITELLM_API_KEY\n' >&2; exit 1; }
	KVM_AGENT_NAME="$(KVM_AGENT_NAME)" scripts/kvm-guest.sh agent-up "LITELLM_API_KEY=$$LITELLM_API_KEY $(KVM_AGENT)"

# Fetch the guest agent's branches back to the host. Default: every branch into
# refs/remotes/kvm-guest/*. BRANCH=<name> fetches just that branch into the local
# branch of the same name (fast-forward only). A branch checked out here refuses
# the fetch — merge kvm-guest/<name> instead.
kvm-sync-back:
	scripts/kvm-guest.sh sync-back $(BRANCH)

kvm-down:
	scripts/kvm-guest.sh down

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
# configuration. See the specs/ci/quality-gates contract in .operator-shared/.
#   make deadcode
#   make deadcode PKG=./cmd/...         # inspection only; stale markers not checked
#   make deadcode TAGS=integration      # one configuration instead of the matrix
deadcode:
	DEADCODE_VERSION='$(DEADCODE_VERSION)' PKG='$(PKG)' $(if $(TAGS),TAGS='$(TAGS)',) ci/check-deadcode.sh

# Test the dead-code gate script itself (fixtures only: never runs deadcode).
deadcode-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run Deadcode

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

# Lint the whole module, including the integration, msb, and
# microsandbox_ffi_path build-tagged files that golangci-lint's default build
# constraints otherwise omit.
lint:
	golangci-lint run --build-tags 'integration msb microsandbox_ffi_path' ./...

fmt:
	golangci-lint fmt ./...

validate-fmt:
	golangci-lint fmt -d ./...

# Lint the GitHub Actions workflows. Pinned and run through `go run` like
# vulncheck, so CI and the dev VM share one version and no binary is installed.
# actionlint covers general workflow errors (expression types, undefined
# contexts, shellcheck on `run:`); the two condition invariants it does not cover
# are enforced by ci-guards-check. See specs/ci/ci-pipeline.md.
actionlint:
	go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) $(wildcard .github/workflows/*.yml)

# Lint every tracked shell script with the shellcheck installed in the dev image
# and on the CI runner. Deliberately unpinned: the two environments' builds
# differ today (0.11.0 vs 0.9.0) and a finding from either must be fixed. The
# file list is carried NUL-separated, so a path containing whitespace survives.
shellcheck:
	@count="$$(git ls-files '*.sh' | wc -l | tr -d '[:space:]')"; \
	test "$$count" -gt 0 || { echo "shellcheck: no tracked *.sh files found" >&2; exit 1; }; \
	shellcheck --version | grep '^version:'; \
	git ls-files -z '*.sh' | xargs -0 shellcheck --

run:
	go run ./cmd/agents-sandbox

# Local repository maintenance: remove merged worktrees and branches, then report the rest.
# Forward flags with ARGS, e.g. `make cleanup-branches ARGS=--dry-run`.
cleanup-branches:
	./scripts/cleanup-merged-branches.sh $(ARGS)

# Test the branch/worktree cleanup script itself (fixtures only: never touches this repo).
cleanup-branches-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run CleanupMergedBranches

# Test the release-surface sweep isolation helper itself (fixtures only: no VM, no real state).
surface-sweep-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run SurfaceSweep

# Run the default suite in a hermetic envelope: a module copy outside any git
# checkout and a scrubbed environment, with the host-figure sentinel pass (which
# macOS relies on, since it has no network namespace). See
# specs/testing/test-isolation.md.
test-scrubbed:
	./scripts/hermetic-envelope.sh -- make test
	./scripts/hermetic-envelope.sh --sentinel -- make test

# Strict envelope: adds a network namespace with only loopback (Linux).
# Runs the suite as uid 0, so a test must not depend on the effective uid.
# See specs/testing/test-isolation.md.
test-hermetic:
	./scripts/hermetic-envelope.sh --netns -- make test
	./scripts/hermetic-envelope.sh --netns --sentinel -- make test

# The sentinel pass alone: the strict envelope with the host figures pinned to
# non-host sentinels, so a test that still reads the real host figures fails.
# See specs/testing/test-isolation.md.
test-hermetic-sentinel:
	./scripts/hermetic-envelope.sh --netns --sentinel -- make test

# Test the hermetic envelope driver itself (fixtures only: never touches this repo).
envelope-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run Envelope

# Test the upstream-drift reporter itself (fixtures only: never touches this repo).
upstream-drift-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run UpstreamDrift

# Test the rebase-status reporter itself (fixtures only: never touches this repo).
rebase-status-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run RebaseStatus

# Test the merge-queue enqueue helper itself (fixtures only: never touches this repo).
enqueue-pr-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run EnqueuePr

# Structural checks on the Operator brain (.operator-shared/): frontmatter,
# relative links, catalog coverage, plan citations, Read If style, partition
# isolation. See .operator-shared/guides/operator-brain-maintenance.md.
brain-check:
	./scripts/brain-check.sh

# Test the brain checker itself (fixtures only: never touches this repo).
brain-check-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run BrainCheck

# Test the outstanding-backport detector itself (fixtures only: never touches this repo).
backport-audit-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run BackportAudit

# Test the backport-PR opener itself (fixtures only: never touches this repo).
open-backport-pr-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run OpenBackport

# Test the libvirt guest provisioner's arg handling, cloud-init seed, and
# domain XML (fixtures only: fakes virsh/ssh/rsync, never touches a hypervisor).
kvm-guest-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run KvmGuest

# Test the check runner itself (fixtures only: never runs the real targets).
check-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run TestCheck

# The gates' targets are independent and read-only. `check`/`verify` delegate to
# scripts/check.sh, which captures each target, prints one line per target, and
# dumps the failing targets' logs last. The CPU-heavy targets
# (CHECK_HEAVY_TARGETS) run one at a time; the rest run in a pool of JOBS.
#
# The `go test ./test/scripts -run X` harness targets (`*-test`) are NOT in
# CHECK_TARGETS: `test` already runs that whole package, so listing them would
# execute it twice. They stay defined as selectable targets for the fast loop
# (scripts/changed-targets.sh); CI names a subset in its `lint` job and covers
# the rest through the `test` job's package run.
#
# Each heavy target parallelizes internally (~GOMAXPROCS workers), so overlapping
# them oversubscribes the box: on 16 cores a one-leaf edit took 33s at -j16, 26s
# at -j8, and 20s at -j4. Default the light pool to a quarter of the cores
# (floored at 2). nproc on Linux, hw.ncpu on macOS, with a fixed fallback;
# JOBS=<n> overrides the pool width.
JOBS ?= $(shell n=$$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 2); q=$$((n / 4)); [ "$$q" -lt 2 ] && q=2; echo "$$q")

CHECK_HEAVY_TARGETS = lint test compile-all
CHECK_LIGHT_TARGETS = validate-fmt actionlint shellcheck docs-linkcheck ci-guards-check changelog-check brain-check
CHECK_TARGETS = $(CHECK_HEAVY_TARGETS) $(CHECK_LIGHT_TARGETS)
VERIFY_HEAVY_TARGETS = $(CHECK_HEAVY_TARGETS) test-race vulncheck deadcode
VERIFY_TARGETS = $(CHECK_TARGETS) test-race vulncheck deadcode

# The accumulated `make check` target set, expanded by make for
# scripts/changed-targets.sh's fail-safe. Not itself a check target.
print-check-targets:
	@printf '%s\n' "$(CHECK_TARGETS)"

# Read-only local gate (does not modify files): formatter check + lint + test +
# compile-all + the docs/changelog/deadcode/envelope harness tests. Run this when
# finalizing; `make fmt` fixes formatting. CI runs the same formatter check via
# `make validate-fmt`.
check:
	@CHECK_HEAVY_TARGETS="$(CHECK_HEAVY_TARGETS)" JOBS=$(JOBS) ./scripts/check.sh $(CHECK_TARGETS)

# Read-only check for CI parity: fmt-check + lint + test + race + vulncheck (does not modify files).
verify:
	@CHECK_HEAVY_TARGETS="$(VERIFY_HEAVY_TARGETS)" JOBS=$(JOBS) ./scripts/check.sh $(VERIFY_TARGETS)

# Fast per-change inner loop. The map lives in scripts/changed-targets.sh, the
# diff itself in scripts/changed-files.sh. Like check/verify these are read-only,
# and unlike check they are NOT the gate: run `make check` before finalizing.
# check-local-changes covers the uncommitted working tree; check-branch-changes
# covers everything since the merge base with origin/main (override with BASE=<ref>).
# A map-script failure is an error, never "no changed files": an empty selection
# means the script succeeded and found none. The changelog-presence check always
# runs beside the selected targets: the presence rule otherwise fires only in CI.
check-local-changes:
	@targets="$$(./scripts/changed-targets.sh local)" || { echo "check-local-changes: scripts/changed-targets.sh failed" >&2; exit 1; }; \
	if [ -z "$$targets" ]; then \
		$(MAKE) --no-print-directory changelog-presence MODE=local; \
		echo "check-local-changes: no changed files"; exit 0; \
	fi; \
	selected="$$(printf '%s ' $$targets)"; \
	echo "check-local-changes: $$selected"; \
	$(MAKE) --no-print-directory check-selected TARGETS="$$selected" MODE=local

check-branch-changes:
	@targets="$$(./scripts/changed-targets.sh branch $${BASE:+--base $$BASE})" || { echo "check-branch-changes: scripts/changed-targets.sh failed" >&2; exit 1; }; \
	if [ -z "$$targets" ]; then \
		$(MAKE) --no-print-directory changelog-presence MODE=branch $${BASE:+BASE=$$BASE}; \
		echo "check-branch-changes: no changed files"; exit 0; \
	fi; \
	selected="$$(printf '%s ' $$targets)"; \
	echo "check-branch-changes: $$selected"; \
	$(MAKE) --no-print-directory check-selected TARGETS="$$selected" MODE=branch $${BASE:+BASE=$$BASE}

# Internal: run a diff-gated selection through the check runner. The heavy set is
# filtered to the selection, since the runner rejects a heavy target it was not
# handed. MODE/BASE feed changelog-presence; CHECK_RUNNER is a test seam.
CHECK_RUNNER ?= ./scripts/check.sh
check-selected:
	@MODE="$(MODE)" BASE="$(BASE)" CHECK_HEAVY_TARGETS="$(filter $(TARGETS),$(CHECK_HEAVY_TARGETS))" JOBS=$(JOBS) $(CHECK_RUNNER) $(TARGETS) changelog-presence

# Changelog presence in the fast loop: a code change with no changelog.d/
# fragment should fail locally, not only in CI. The changed paths come from
# scripts/changed-files.sh (the same diff the target map uses). No --pr: the
# number is unknown locally, so this checks presence only, not the slug. An
# unresolvable base (branch mode) is skipped, not failed, since an undecidable
# diff must not block the inner loop. See specs/ci/ci-pipeline.md.
MODE ?= local
changelog-presence:
	@changed_file="$$(mktemp)"; trap 'rm -f "$$changed_file"' EXIT; \
	status=0; \
	if [ "$(MODE)" = branch ]; then \
		./scripts/changed-files.sh $${BASE:+--base $$BASE} branch >"$$changed_file" 2>/dev/null || status=$$?; \
	else \
		./scripts/changed-files.sh local >"$$changed_file" || status=$$?; \
	fi; \
	if [ "$$status" -eq 3 ]; then \
		echo "changelog-presence: base ref unresolvable; skipping presence check"; exit 0; \
	fi; \
	if [ "$$status" -ne 0 ]; then \
		echo "changelog-presence: scripts/changed-files.sh failed" >&2; exit 1; \
	fi; \
	ci/check-changelog.sh --changed "$$changed_file"

# Test the changed-file enumerator itself (fixtures only: never touches this repo).
changed-files-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run ChangedFiles

# Test the changed-file map script itself (fixtures only: never touches this repo).
changed-targets-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run ChangedTargets

all: fmt lint shellcheck test docs-linkcheck docs-linkcheck-test changelog-check changelog-check-test build cleanup-branches-test changed-files-test upstream-drift-test rebase-status-test enqueue-pr-test backport-audit-test open-backport-pr-test

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

# Validate that the CI gate's non-code path lists agree across ci.yml,
# mutation.yml, codeql.yml, and ci/check-changelog.sh, and that the two
# workflow-condition invariants actionlint does not cover hold: every
# `github.event_name == '<event>'` names a declared event, and a declared
# on.pull_request.types repeats the defaults. See specs/ci/ci-pipeline.md.
ci-guards-check:
	ci/check-ci-guards.sh

# Test the guards checker itself (fixtures plus a self-check of this repo).
ci-guards-check-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run CIGuards

# Test the documentation link checker itself.
docs-linkcheck-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run CheckDocs

# Validate the changelog fragment convention (shape). See specs/ci/ci-pipeline.md.
changelog-check:
	ci/check-changelog.sh

# Test the changelog fragment checker itself.
changelog-check-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run Changelog

# Test the CI base-SHA resolver itself (fixtures only: never touches this repo).
event-base-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run EventBase

# Test the CI change-classifier itself (fixtures only: never touches this repo).
classify-changes-test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -count=1 ./test/scripts/ -run ClassifyChanges

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
