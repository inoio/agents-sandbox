FROM golang:1.26

ARG ZIG_VERSION=0.16.0
ARG ZIG_TARBALL=zig-x86_64-linux-${ZIG_VERSION}.tar.xz

RUN apt-get update \
 && apt-get install -y --no-install-recommends xz-utils ca-certificates curl file python3 \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/${ZIG_TARBALL}" -o "/tmp/${ZIG_TARBALL}" \
 && tar -xf "/tmp/${ZIG_TARBALL}" -C /usr/local \
 && rm "/tmp/${ZIG_TARBALL}"

ENV PATH="/usr/local/zig-x86_64-linux-${ZIG_VERSION}:${PATH}"

# zig 0.16.0 ships macOS C headers but not the framework/library stubs that Go's
# linker needs (-framework CoreFoundation, -framework Security, -lresolv).
# These empty stubs satisfy the linker; symbols resolve at runtime on macOS.
COPY ci/builder/stubs/ /tmp/darwin-stubs/
RUN ZIG_DARWIN="$(dirname "$(which zig)")/lib/libc/darwin" \
 && mkdir -p "$ZIG_DARWIN/System/Library/Frameworks/CoreFoundation.framework" \
 && mkdir -p "$ZIG_DARWIN/System/Library/Frameworks/Security.framework" \
 && mkdir -p "$ZIG_DARWIN/usr/lib" \
 && cp /tmp/darwin-stubs/CoreFoundation.tbd "$ZIG_DARWIN/System/Library/Frameworks/CoreFoundation.framework/" \
 && cp /tmp/darwin-stubs/Security.tbd      "$ZIG_DARWIN/System/Library/Frameworks/Security.framework/" \
 && cp /tmp/darwin-stubs/libresolv.tbd     "$ZIG_DARWIN/usr/lib/" \
 && rm -rf /tmp/darwin-stubs

RUN zig version && go version
