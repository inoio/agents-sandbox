# Custom runner with Go, golangci-lint and shellcheck layered on top of the default runner image.
FROM opencode-msb/runner:base

USER root
# Go & linter
RUN curl -fsSL https://go.dev/dl/go1.26.5.linux-amd64.tar.gz | tar -C /usr/local -xzf - && echo 'export PATH="$PATH:/usr/local/go/bin"' >> /etc/profile
RUN curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b /usr/local/bin v2.12.2
# build-essential for CGO, shellcheck for shell script linting
RUN apt-get update && \
    apt-get install -y --no-install-recommends build-essential shellcheck && \
    rm -rf /var/lib/apt/lists/*

USER dev
# microsandbox so the agent can check --help, sources.
RUN curl -fsSL https://github.com/superradcompany/microsandbox/releases/download/v0.6.7/install.sh | sh && echo 'export PATH="$PATH:/home/dev/.microsandbox/bin"' >> /home/dev/.profile
