FROM docker.io/library/debian:trixie-slim

# Recommended CLI tools, grouped by the consumer that depends on each package (a
# line's comment names its dependee). The Docker system packages must be here:
# the docker stage installs only the static tarballs into toolDir, which is all
# the runner copies from it, so a system package the engine needs has to come
# from this base.
RUN apt update \
 && apt install -y --no-install-recommends \
      # tls, microsandbox certificate provisioning target
      ca-certificates \
      # HTTP fetching, file sync, and archive handling
      curl wget rsync tar zip unzip xz-utils zstd lz4 \
      # installing/verifying third-party software (apt keys, release signatures)
      gnupg \
      # source control
      git \
      # code search, file/tree inspection
      ripgrep fd-find file tree \
      # manual pages and paging
      man-db less \
      # process inspection, signalling, and debugging (lsof: socket/file holders, strace: syscalls, openssl: TLS)
      procps psmisc lsof strace openssl \
      # network inspection, DNS, and ad-hoc sockets
      dnsutils iputils-ping netcat-openbsd socat \
      # text streams, JSON/YAML, and shell plumbing
      bsdextrautils gawk moreutils jq yq uuid-runtime \
      # Python interpreter with venv and pip
      python3 python3-venv python3-pip \
      # GNU parallel
      parallel \
      # hermes's managed Node.js links libatomic.so.1 on glibc
      libatomic1 \
      # rootless Docker: fuse-overlayfs (pulls libfuse3-4), uidmap/slirp4netns/iproute2/iptables runtime
      fuse-overlayfs uidmap slirp4netns iproute2 iptables \
 && rm -rf /var/lib/apt/lists/*
