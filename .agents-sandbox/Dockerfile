FROM debian:forky-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends bsdmainutils ca-certificates curl dnsutils git iptables ripgrep fd-find file gawk iproute2 iputils-ping jq less lz4 man-db moreutils net-tools netcat-openbsd parallel procps psmisc python3 recode rsync socat sqlite3 tar tree unzip uuid wget xz-utils yq zip zstd \
    && rm -rf /var/lib/apt/lists/*

# fd-find ships the binary as `fdfind`; expose it as `fd` too, so an agent that
# reaches for the common name finds it. AGENTS.md/guides name `fdfind`.
RUN ln -s "$(command -v fdfind)" /usr/local/bin/fd

# default versions, keep in sync with versions in Makefile
ARG GO_VERSION=1.27.4
ARG GOLANGCI_LINT_VERSION=2.15.0
ARG GOTESTSUM_VERSION=1.13.0
ARG GOPLS_VERSION=0.24.0
ARG XTOOLS_VERSION=0.52.0
ARG AST_GREP_VERSION=0.51.2
ARG ZIG_VERSION=0.17.1
ARG GH_VERSION=2.103.3
ARG PLANTUML_VERSION=1.2026.10

USER root
# Go & linter
ENV PATH="/usr/local/go/bin:$PATH"
# GOBIN (where `go install` drops tools) lives under the home volume, so put it on
# PATH: a tool the agent installs is then findable in later shells. Baked tools go
# to /usr/local/bin (already on PATH) because /home/dev is the mounted home volume,
# which would shadow anything installed under it at build time.
ENV PATH="/home/dev/go/bin:${PATH}"
RUN if [ "$(uname -m)" = aarch64 ]; then GOARCH=arm64; else GOARCH=amd64; fi && \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" | tar -C /usr/local -xzf -
RUN curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b /usr/local/bin v${GOLANGCI_LINT_VERSION}
# gotestsum: emits JUnit XML (junit.xml) for Codecov Test Analytics alongside coverage.
RUN GOBIN=/usr/local/bin go install gotest.tools/gotestsum@v${GOTESTSUM_VERSION}
# gopls: type-checked navigation/refactor (definition, references, rename, symbols,
# call hierarchy). goimports: add/remove imports across a package move. deadcode: the
# same x/tools pinned by the `make deadcode` gate (DEADCODE_VERSION), prebuilt so the
# gate needs no first-use fetch.
RUN GOBIN=/usr/local/bin go install golang.org/x/tools/gopls@v${GOPLS_VERSION}
RUN GOBIN=/usr/local/bin go install \
    golang.org/x/tools/cmd/goimports@v${XTOOLS_VERSION} \
    golang.org/x/tools/cmd/deadcode@v${XTOOLS_VERSION}
# ast-grep: structural search/rewrite over the AST (Rust). Its GitHub releases carry
# only the GUI app, so pull the manylinux wheel from PyPI and extract the CLI binary
# directly - no pip, Cargo, or Node. The upstream `sg` alias is omitted: on Linux `sg`
# is setgroups' command.
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
# build-essential for CGO, shellcheck for shell script linting, default-jre for the PlantUML jar
# (the full JRE, not -headless: PlantUML renders fonts and needs libharfbuzz/libfontmanager)
RUN apt-get update && \
    apt-get install -y --no-install-recommends build-essential bundler shellcheck default-jre && \
    rm -rf /var/lib/apt/lists/*

# zig (tarball and install dir are architecture-specific; a stable symlink keeps PATH fixed)

RUN if [ "$(uname -m)" = aarch64 ]; then ZIG_ARCH=aarch64; else ZIG_ARCH=x86_64; fi && \
    curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/zig-${ZIG_ARCH}-linux-${ZIG_VERSION}.tar.xz" -o /tmp/zig.tar.xz \
 && tar -xf /tmp/zig.tar.xz -C /usr/local \
 && rm /tmp/zig.tar.xz \
 && ln -s "/usr/local/zig-${ZIG_ARCH}-linux-${ZIG_VERSION}" /usr/local/zig

ENV PATH="/usr/local/zig:${PATH}"

# zig ships macOS C headers but not the framework/library stubs that Go's
# linker needs (-framework CoreFoundation, -framework Security, -lresolv).
# These empty stubs satisfy the linker; symbols resolve at runtime on macOS.
# The canonical copies live in .github/stubs/ and are used by the release CI job
# and by `make bootstrap`. They are re-inlined here as printf(1) output because
# the image build context is synthetic — the rendered Dockerfile plus the
# launcher's own assets — so repository files are not available to COPY. printf
# works on legacy and BuildKit builders alike; heredoc syntax fails on legacy.
RUN ZIG_DARWIN="$(dirname "$(which zig)")/lib/libc/darwin" \
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

# GitHub CLI (gh)
# In order to preauthenticate the client, use a startup hook, e.g.:
# ```
# #!/bin/sh
# if [ ! -z "${GITHUB_PAT:-}" ]; then
#   printf '%s\n' "$GITHUB_PAT" | gh auth login --with-token
#   gh auth setup-git
# fi
# ```
RUN curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_$(dpkg --print-architecture).deb" -o /tmp/gh.deb \
 && dpkg -i /tmp/gh.deb \
 && rm -f /tmp/gh.deb

# PlantUML (standalone jar; the apt package is too old for C4-PlantUML)
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

# agents-sandbox:docker

