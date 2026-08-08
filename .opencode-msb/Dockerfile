FROM opencode-msb/runner-base-dind:latest

USER root
# Go & linter
ENV PATH="/usr/local/go/bin:$PATH"
RUN curl -fsSL https://go.dev/dl/go1.26.5.linux-amd64.tar.gz | tar -C /usr/local -xzf -
RUN curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b /usr/local/bin v2.12.2
# build-essential for CGO, shellcheck for shell script linting
RUN apt-get update && \
    apt-get install -y --no-install-recommends build-essential shellcheck && \
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

USER dev
# microsandbox so the agent can check --help, sources. Running VMs doesn't work because /dev/kvm is missing.
RUN curl -fsSL https://github.com/superradcompany/microsandbox/releases/download/v0.6.7/install.sh | sh

USER root
ENV PATH="/home/dev/.microsandbox/bin:$PATH"

USER dev