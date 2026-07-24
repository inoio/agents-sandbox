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

RUN zig version && go version
