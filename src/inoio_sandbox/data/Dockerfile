FROM debian:trixie-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl git ripgrep file gawk jq less lz4 moreutils net-tools nmap parallel recode uuid wget xz-utils yq \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js 22 (LSP servers such as pyright need a Node runtime).
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y --no-install-recommends nodejs && \
    rm -rf /var/lib/apt/lists/*

ARG USER_UID=1000
ARG USER_GID=1000
RUN groupadd -g "$USER_GID" dev && \
    useradd -m -u "$USER_UID" -g "$USER_GID" -s /bin/bash dev && \
    mkdir -p /home/dev/.config /home/dev/workspace && \
    chown -R dev:dev /home/dev

USER dev
RUN curl -fsSL https://opencode.ai/install | bash

ENV HOME=/home/dev
ENV PATH=/home/dev/.opencode/bin:/home/dev/.local/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
WORKDIR /home/dev/workspace
