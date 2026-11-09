FROM docker.io/library/debian:trixie-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends bsdmainutils ca-certificates curl dnsutils git iptables ripgrep fd-find file fuse-overlayfs gawk iproute2 iputils-ping jq less libatomic1 libfuse3-4 lz4 man-db moreutils net-tools netcat-openbsd parallel procps psmisc python3 recode rsync slirp4netns socat tar tree uidmap unzip uuid wget xz-utils yq zip zstd \
    && rm -rf /var/lib/apt/lists/*
