# Docker-Alternative zu setup-microsandbox.sh
# Baut ein vergleichbares Image mit denselben Tools wie die microsandbox-Snapshots.
# Nutzung: docker build -t runner . && docker run -it --rm runner
#
# Fuer Docker-in-Docker: docker run -it --rm --privileged runner
FROM debian:latest

# === Build-Args: Host-UID/GID uebernehmen ================================
ARG USER_UID=1000
ARG USER_GID=1000

# === Basis-Pakete ========================================================
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        direnv \
    && rm -rf /var/lib/apt/lists/*

# === Docker installieren =================================================
# GPG-Key + APT-Repo + Pakete + vfs storage driver
RUN install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc && \
    chmod a+r /etc/apt/keyrings/docker.asc && \
    . /etc/os-release && \
    ARCH=$(dpkg --print-architecture) && \
    echo "Types: deb\nURIs: https://download.docker.com/linux/debian\nSuites: ${VERSION_CODENAME}\nComponents: stable\nArchitectures: ${ARCH}\nSigned-By: /etc/apt/keyrings/docker.asc" \
        > /etc/apt/sources.list.d/docker.sources && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        docker-ce \
        docker-ce-cli \
        containerd.io \
        docker-buildx-plugin \
        docker-compose-plugin && \
    mkdir -p /etc/docker && \
    echo '{"storage-driver":"vfs"}' > /etc/docker/daemon.json && \
    rm -rf /var/lib/apt/lists/*

# === User dev anlegen ====================================================
# UID/GID vom Host uebernehmen fuer korrekte File-Ownership bei Mounts.
RUN groupadd -g "$USER_GID" dev && \
    useradd -m -u "$USER_UID" -g "$USER_GID" -s /bin/bash dev && \
    usermod -aG docker dev && \
    mkdir -p /home/dev/.config /home/dev/workspace && \
    chown -R dev:dev /home/dev

# === nodenv installieren =================================================
USER dev
RUN git clone https://github.com/nodenv/nodenv.git ~/.nodenv && \
    mkdir -p ~/.nodenv/plugins && \
    git clone https://github.com/nodenv/node-build.git ~/.nodenv/plugins/node-build

# === Shell-Konfiguration =================================================
RUN echo '\n\
# direnv\n\
eval "$(direnv hook bash)"\n\
\n\
# nodenv\n\
export PATH="$HOME/.nodenv/bin:$PATH"\n\
eval "$(nodenv init - bash)"\n\
\n\
# opencode\n\
export PATH="$HOME/.opencode/bin:$PATH"\n' >> ~/.bashrc

# === opencode installieren ===============================================
RUN mkdir -p /var/tmp && \
    export TMPDIR=/var/tmp && \
    curl -fsSL https://opencode.ai/install | bash

# === Finale Konfiguration ================================================
ENV HOME=/home/dev
ENV PATH=/home/dev/.opencode/bin:/home/dev/.nodenv/bin:/home/dev/.local/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin

USER dev
WORKDIR /home/dev/workspace

# Fuer Docker-in-Docker: dockerd muss als root starten.
# Beim Ausfuehren mit --privileged:
#   sudo dockerd -H unix:///var/run/docker.sock &
#   export DOCKER_HOST=unix:///var/run/docker.sock
#   docker run --rm hello-world
