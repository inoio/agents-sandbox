# The dev-VM image, composed from one stage per concern so a change re-runs only
# what it touches. The launcher renders this file as a project Dockerfile: the
# stages below the last FROM are copied into the final stage, which the sandbox
# runs. A tool's pinned version sits in the stage that installs it, so bumping one
# version does not invalidate the other stages.
#
#   system  - the OS package set; the final stage inherits it
#   fetch   - a minimal base the tool stages download onto
#   go      - the Go toolchain and the tools it installs
#   zig     - zig, plus the macOS linker stubs the release cross-build needs
#   extras  - standalone CLI tools (gh, ast-grep, plantuml, operator-helper)
#
# Keep the pinned versions in sync with the Makefile.

# ---- system: the OS package set the sandbox runs on ----
FROM debian:forky-slim AS system

# Dev-VM toolchain, grouped by the consumer that depends on each package (a
# line's comment names its dependee). One apt layer for all of them; the
# build/doc/lint packages live here rather than in a tool stage because they are
# needed at build time by more than one consumer.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      # apt/curl/wget/git/gh trust; the microsandbox CA is injected here
      ca-certificates \
      # HTTP fetching, file sync, and archive handling
      curl wget rsync tar zip unzip xz-utils zstd lz4 \
      # installing/verifying third-party software (apt keys, release signatures)
      gnupg \
      # source control and GitHub
      git \
      # code search, file/tree inspection
      ripgrep fd-find file tree \
      # manual pages and paging (git, make, tools)
      man-db less \
      # process inspection and signalling
      procps psmisc \
      # debugging: port/file holders, syscall+namespace tracing, TLS inspection
      lsof strace openssl \
      # network inspection, DNS, ad-hoc sockets, packet capture
      dnsutils iputils-ping netcat-openbsd socat tcpdump \
      # shell-scripting helpers (arithmetic, timing, ids)
      bc time uuid-runtime \
      # text, JSON/YAML, and data munging
      bsdextrautils gawk moreutils jq yq sqlite3 \
      # Python interpreter with venv and pip
      python3 python3-venv python3-pip \
      # GNU parallel
      parallel \
      # rootless Docker engine prerequisites (ip/ss are also the network tools)
      rootlesskit slirp4netns fuse-overlayfs iptables iproute2 \
      # CGO builds (gcc/make/pkg-config) for `make test` and the msb SDK
      build-essential pkg-config \
      # Node tooling: the Operator Helper CLI copied below, and JS LSP servers
      nodejs \
      # shell scripts: shellcheck (the make shellcheck gate)
      shellcheck \
      # Jekyll docs site (`make docs-serve`)
      bundler \
      # PlantUML jar: the full JRE, not -headless, so fonts render
      default-jre \
 && rm -rf /var/lib/apt/lists/*

# fd-find ships the binary as `fdfind`; expose it as `fd` too, so an agent that
# reaches for the common name finds it. AGENTS.md/guides name `fdfind`.
RUN ln -s "$(command -v fdfind)" /usr/local/bin/fd

# ---- fetch: the minimal base every tool stage downloads onto ----
# Kept separate from `system`, so editing a system package does not re-run a tool
# download, and the tool layers cache across bases.
FROM debian:forky-slim AS fetch
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl tar xz-utils jq unzip \
 && rm -rf /var/lib/apt/lists/*

# ---- go: the Go toolchain and the tools it installs ----
FROM fetch AS go
ARG GO_VERSION=1.27.4
# Go & linter
ENV PATH="/usr/local/go/bin:$PATH"
# GOBIN (where `go install` drops tools) lives under the home volume, so put it on
# PATH: a tool the agent installs is then findable in later shells. Baked tools go
# to /usr/local/bin (already on PATH) because /home/dev is the mounted home volume,
# which would shadow anything installed under it at build time.
ENV PATH="/home/dev/go/bin:${PATH}"
RUN if [ "$(uname -m)" = aarch64 ]; then GOARCH=arm64; else GOARCH=amd64; fi && \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" | tar -C /usr/local -xzf -
ARG GOLANGCI_LINT_VERSION=2.15.0
RUN curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b /usr/local/bin v${GOLANGCI_LINT_VERSION}
ARG GOTESTSUM_VERSION=1.13.0
# gotestsum: emits JUnit XML (junit.xml) for Codecov Test Analytics alongside coverage.
RUN GOBIN=/usr/local/bin go install gotest.tools/gotestsum@v${GOTESTSUM_VERSION}
ARG GOPLS_VERSION=0.24.0
ARG XTOOLS_VERSION=0.53.0
# gopls: type-checked navigation/refactor (definition, references, rename, symbols,
# call hierarchy). goimports: add/remove imports across a package move. deadcode: the
# same x/tools pinned by the `make deadcode` gate (DEADCODE_VERSION), prebuilt so the
# gate needs no first-use fetch.
RUN GOBIN=/usr/local/bin go install golang.org/x/tools/gopls@v${GOPLS_VERSION}
RUN GOBIN=/usr/local/bin go install \
    golang.org/x/tools/cmd/goimports@v${XTOOLS_VERSION} \
    golang.org/x/tools/cmd/deadcode@v${XTOOLS_VERSION}
ARG GOVULNCHECK_VERSION=1.10.0
ARG GOMUTANTS_VERSION=0.7.3
# govulncheck: the `make vulncheck` scanner (part of `make verify`), and
# gomutants: the on-demand `make mutation` tool. Both follow the deadcode
# rationale - prebuilt so the target reuses the module cache instead of
# fetching on first run.
RUN GOBIN=/usr/local/bin go install \
    golang.org/x/vuln/cmd/govulncheck@v${GOVULNCHECK_VERSION} && \
    GOBIN=/usr/local/bin go install \
    github.com/gomutants/gomutants@v${GOMUTANTS_VERSION}

# ---- zig: at a stable /usr/local/zig, plus the macOS linker stubs ----
FROM fetch AS zig
# The tarball and install dir are architecture-specific; installing at the stable
# path /usr/local/zig keeps PATH fixed and lets the final stage COPY one directory.
ARG ZIG_VERSION=0.17.1
RUN if [ "$(uname -m)" = aarch64 ]; then ZIG_ARCH=aarch64; else ZIG_ARCH=x86_64; fi && \
    curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/zig-${ZIG_ARCH}-linux-${ZIG_VERSION}.tar.xz" -o /tmp/zig.tar.xz \
 && tar -xf /tmp/zig.tar.xz -C /tmp \
 && mv "/tmp/zig-${ZIG_ARCH}-linux-${ZIG_VERSION}" /usr/local/zig \
 && rm /tmp/zig.tar.xz

# zig ships macOS C headers but not the framework/library stubs that Go's
# linker needs (-framework CoreFoundation, -framework Security, -lresolv).
# These empty stubs satisfy the linker; symbols resolve at runtime on macOS.
# The canonical copies live in .github/stubs/ and are used by the release CI job
# and by `make bootstrap`. They are re-inlined here as printf(1) output because
# the image build context is synthetic — the rendered Dockerfile plus the
# launcher's own assets — so repository files are not available to COPY. printf
# works on legacy and BuildKit builders alike; heredoc syntax fails on legacy.
RUN ZIG_DARWIN=/usr/local/zig/lib/libc/darwin \
 && mkdir -p "$ZIG_DARWIN/System/Library/Frameworks/CoreFoundation.framework" \
    "$ZIG_DARWIN/System/Library/Frameworks/Security.framework" \
    "$ZIG_DARWIN/usr/lib" \
 && printf '%b' \
      '--- !tapi-tbd\ntbd-version:     4\ntargets:         [ arm64-macos, x86_64-macos, arm64e-macos ]\n' \
      "install-name:    '/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation'\n" \
      'current-version: 1\nexports:\n  - targets:         [ arm64-macos, x86_64-macos, arm64e-macos ]\n    symbols:         [ ]\n' \
      > "$ZIG_DARWIN/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation.tbd" \
 && printf '%b' \
      '--- !tapi-tbd\ntbd-version:     4\ntargets:         [ arm64-macos, x86_64-macos, arm64e-macos ]\n' \
      "install-name:    '/System/Library/Frameworks/Security.framework/Security'\n" \
      'current-version: 1\nexports:\n  - targets:         [ arm64-macos, x86_64-macos, arm64e-macos ]\n    symbols:         [ ]\n' \
      > "$ZIG_DARWIN/System/Library/Frameworks/Security.framework/Security.tbd" \
 && printf '%b' \
      '--- !tapi-tbd\ntbd-version:     4\ntargets:         [ arm64-macos, x86_64-macos, arm64e-macos ]\n' \
      "install-name:    '/usr/lib/libresolv.9.dylib'\n" \
      'current-version: 1\nexports:\n  - targets:         [ arm64-macos, x86_64-macos, arm64e-macos ]\n    symbols:         [ ]\n' \
      > "$ZIG_DARWIN/usr/lib/libresolv.tbd"

# ---- extras: standalone CLI tools with no shared toolchain ----
FROM fetch AS extras

# GitHub CLI (gh). The .deb is extracted rather than installed system-wide, so the
# final stage can COPY its `/usr` tree in.
# In order to preauthenticate the client, use a startup hook, e.g.:
# ```
# #!/bin/sh
# if [ ! -z "${GITHUB_PAT:-}" ]; then
#   printf '%s\n' "$GITHUB_PAT" | gh auth login --with-token
#   gh auth setup-git
# fi
# ```
ARG GH_VERSION=2.105.0
RUN curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_$(dpkg --print-architecture).deb" -o /tmp/gh.deb \
 && dpkg-deb -x /tmp/gh.deb /opt/gh \
 && rm -f /tmp/gh.deb

# ast-grep: structural search/rewrite over the AST (Rust). Its GitHub releases carry
# only the GUI app, so pull the manylinux wheel from PyPI and extract the CLI binary
# directly - no pip, Cargo, or Node. The upstream `sg` alias is omitted: on Linux `sg`
# is setgroups' command.
ARG AST_GREP_VERSION=0.51.3
RUN ARCH="$(dpkg --print-architecture)" \
 && case "$ARCH" in \
      amd64) SG_WHEEL=x86_64 ;; \
      arm64) SG_WHEEL=aarch64 ;; \
      *) echo "ast-grep: unsupported architecture $ARCH" >&2; exit 1 ;; \
    esac \
 && SG_URL="$(curl -fsSL "https://pypi.org/pypi/ast-grep-cli/${AST_GREP_VERSION}/json" \
      | jq -r --arg w "$SG_WHEEL" '.urls[].url | select(test("manylinux_2_28_" + $w + "\\.whl$"))')" \
 && test -n "$SG_URL" \
 && curl -fsSL "$SG_URL" -o /tmp/ast-grep.whl \
 && unzip -q /tmp/ast-grep.whl 'ast_grep_cli-*.data/scripts/ast-grep' -d /tmp/ast-grep \
 && install -m 0755 /tmp/ast-grep/ast_grep_cli-*.data/scripts/ast-grep /usr/local/bin/ast-grep \
 && rm -rf /tmp/ast-grep /tmp/ast-grep.whl

# PlantUML (standalone jar; the apt package is too old for C4-PlantUML). The
# launcher merges the microsandbox egress CA into a user-writable JVM truststore so
# HTTPS !include fetches trust the injected cert.
ARG PLANTUML_VERSION=1.2026.10
RUN mkdir -p /usr/local/plantuml \
 && curl -fsSL "https://github.com/plantuml/plantuml/releases/download/v${PLANTUML_VERSION}/plantuml.jar" -o /usr/local/plantuml/plantuml.jar \
 && printf '%b' \
      '#!/bin/sh\n' \
      '# Launcher that merges the microsandbox egress CA into a user-writable\n' \
      '# JVM truststore so HTTPS !include fetches trust the injected cert.\n' \
      'PLANTUML_JAR=/usr/local/plantuml/plantuml.jar\n' \
      'MICRO_CA=/usr/local/share/ca-certificates/microsandbox-ca.crt\n' \
      'JAVA=$(command -v java || echo /usr/bin/java)\n' \
      'DEFAULT_CACERTS="$(readlink -f "$(dirname "$(readlink -f "$JAVA")")/../lib/security/cacerts" 2>/dev/null)"\n' \
      '[ -f "$DEFAULT_CACERTS" ] || DEFAULT_CACERTS=/etc/ssl/certs/java/cacerts\n' \
      'if [ -n "$MICRO_CA" ] && [ -f "$MICRO_CA" ] && [ -f "$DEFAULT_CACERTS" ] && command -v keytool >/dev/null 2>&1; then\n' \
      '    DIR="${PLANTUML_HOME:-$HOME/.plantuml}"\n' \
      '    TRUSTSTORE="$DIR/cacerts"\n' \
      '    if [ ! -f "$TRUSTSTORE" ]; then\n' \
      '        mkdir -p "$DIR"\n' \
      '        cp "$DEFAULT_CACERTS" "$TRUSTSTORE"\n' \
      '        keytool -importcert -noprompt -alias microsandbox -file "$MICRO_CA" \\\n' \
      '            -keystore "$TRUSTSTORE" -storepass changeit 2>/dev/null\n' \
      '    fi\n' \
      '    exec "$JAVA" -Djavax.net.ssl.trustStore="$TRUSTSTORE" \\\n' \
      '        -Djavax.net.ssl.trustStorePassword=changeit -jar "$PLANTUML_JAR" "$@"\n' \
      'fi\n' \
      'exec "$JAVA" -jar "$PLANTUML_JAR" "$@"\n' \
      > /usr/local/bin/plantuml \
 && chmod +x /usr/local/bin/plantuml

# Operator Helper (the Operator Memory setup CLI) is an npm package, but its
# published dist/index.js bundles its dependencies and imports only Node
# built-ins, so it runs on Debian's nodejs with no npm/npx. The hand-pinned
# version (see .agents/skills/release/NON-GO-DEPS.md) also means its npm/bun
# self-update cannot run, which is fine for a baked dev image.
ARG OPERATOR_HELPER_VERSION=1.13.0
RUN curl -fsSL "https://registry.npmjs.org/@aerovato/operator-helper/-/operator-helper-${OPERATOR_HELPER_VERSION}.tgz" -o /tmp/operator-helper.tgz \
 && mkdir -p /opt/operator-helper \
 && tar -xzf /tmp/operator-helper.tgz -C /opt/operator-helper --strip-components=1 \
 && rm /tmp/operator-helper.tgz

# ---- the sandbox image: system packages plus the tool trees ----
FROM system
# COPY is used rather than a shared toolchain because only the final stage ships;
# the tool stages above exist so their downloads cache independently.
COPY --from=go /usr/local/go /usr/local/go
# The go stage's /usr/local/bin holds exactly the go-installed tools (the fetch
# base ships nothing there), so the whole directory is copied.
COPY --from=go /usr/local/bin/ /usr/local/bin/
COPY --from=zig /usr/local/zig /usr/local/zig
# /opt/gh/usr is merged into / so gh lands in /usr/bin.
COPY --from=extras /opt/gh/ /
COPY --from=extras /opt/operator-helper /opt/operator-helper
COPY --from=extras /usr/local/plantuml /usr/local/plantuml
COPY --from=extras /usr/local/bin/ /usr/local/bin/
RUN ln -s /opt/operator-helper/dist/index.js /usr/local/bin/operator-helper
ENV PATH="/usr/local/go/bin:/usr/local/zig:/home/dev/go/bin:${PATH}"

# agents-sandbox:docker
