FROM debian:trixie-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends bsdmainutils ca-certificates curl dnsutils git iptables ripgrep fd-find file gawk iproute2 iputils-ping jq less lz4 man-db moreutils net-tools netcat-openbsd parallel procps psmisc python3 recode rsync socat tar tree unzip uuid wget xz-utils yq zip zstd \
    && rm -rf /var/lib/apt/lists/*
