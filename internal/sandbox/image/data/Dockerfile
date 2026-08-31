FROM debian:trixie-slim

# used for pruning our dangling images
LABEL org.opencode-sandbox.managed=true

RUN apt-get update && \
    apt-get install -y --no-install-recommends bsdmainutils ca-certificates curl dnsutils git ripgrep fd-find file gawk iproute2 iputils-ping jq less lz4 man-db moreutils net-tools netcat-openbsd parallel procps psmisc python3 recode rsync socat tar tree unzip uuid wget xz-utils yq zip zstd \
    && rm -rf /var/lib/apt/lists/*

# AGENT_INSTALL_BLOCK

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
