FROM debian:trixie-slim

# used for pruning our dangling images
LABEL org.opencode-sandbox.managed=true
ARG OPENCODE_VERSION

RUN apt-get update && \
    apt-get install -y --no-install-recommends bsdmainutils ca-certificates curl git ripgrep file gawk jq less lz4 man-db moreutils net-tools parallel recode uuid wget xz-utils yq \
    && rm -rf /var/lib/apt/lists/*

# OpenCode: pinned to the resolved version and never auto-upgrade at runtime.
ENV OPENCODE_DISABLE_AUTOUPDATE=true
LABEL org.opencode-sandbox.opencode-version="$OPENCODE_VERSION"

RUN curl -fsSL https://opencode.ai/install | bash -s -- --version "$OPENCODE_VERSION" && cp /root/.opencode/bin/opencode /usr/local/bin

# Node.js (LSP servers such as pyright need a Node runtime).
RUN curl -fsSL https://deb.nodesource.com/setup_26.x | bash - && \
    apt-get install -y --no-install-recommends nodejs && \
    rm -rf /var/lib/apt/lists/*

ARG USER_UID=1000
ARG USER_GID=1000
RUN groupadd -g "$USER_GID" dev && \
    useradd -m -u "$USER_UID" -g "$USER_GID" -s /bin/bash dev && \
    chown -R dev:dev /home/dev

USER dev
WORKDIR /workspace
