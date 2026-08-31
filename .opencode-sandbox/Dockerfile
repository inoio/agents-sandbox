FROM opencode-sandbox/runner-base-dind:latest

USER root
# Go & linter
ENV PATH="/usr/local/go/bin:$PATH"
RUN curl -fsSL https://go.dev/dl/go1.26.6.linux-amd64.tar.gz | tar -C /usr/local -xzf -
RUN curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b /usr/local/bin v2.12.2
# build-essential for CGO, shellcheck for shell script linting, default-jre for the PlantUML jar
# (the full JRE, not -headless: PlantUML renders fonts and needs libharfbuzz/libfontmanager)
RUN apt-get update && \
    apt-get install -y --no-install-recommends build-essential bundler shellcheck default-jre && \
    rm -rf /var/lib/apt/lists/*

# zig
ARG ZIG_VERSION=0.16.0
ARG ZIG_TARBALL=zig-x86_64-linux-${ZIG_VERSION}.tar.xz

RUN curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/${ZIG_TARBALL}" -o "/tmp/${ZIG_TARBALL}" \
 && tar -xf "/tmp/${ZIG_TARBALL}" -C /usr/local \
 && rm "/tmp/${ZIG_TARBALL}"

ENV PATH="/usr/local/zig-x86_64-linux-${ZIG_VERSION}:${PATH}"

# zig 0.16.0 ships macOS C headers but not the framework/library stubs that Go's
# linker needs (-framework CoreFoundation, -framework Security, -lresolv).
# These empty stubs satisfy the linker; symbols resolve at runtime on macOS.
# The build context here contains only the Dockerfile, so the stubs are inlined
# as printf(1) output (works on legacy and BuildKit builders alike) instead of
# heredoc syntax (fails on legacy builder).
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
ARG GH_VERSION=2.62.0
RUN curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_amd64.deb" -o /tmp/gh.deb \
 && dpkg -i /tmp/gh.deb \
 && rm -f /tmp/gh.deb

# PlantUML (standalone jar; the apt package is too old for C4-PlantUML)
ARG PLANTUML_VERSION=1.2026.7
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

USER dev
# microsandbox so the agent can check --help, sources. Running VMs doesn't work because /dev/kvm is missing.
RUN curl -fsSL https://github.com/superradcompany/microsandbox/releases/download/v0.6.14/install.sh | sh

USER root
ENV PATH="/home/dev/.microsandbox/bin:$PATH"

USER dev
