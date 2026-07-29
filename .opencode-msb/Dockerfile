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

USER dev
# microsandbox so the agent can check --help, sources.
RUN curl -fsSL https://github.com/superradcompany/microsandbox/releases/download/v0.6.7/install.sh | sh

USER root
ENV PATH="/home/dev/.microsandbox/bin:$PATH"

USER dev