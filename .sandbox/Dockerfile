# Custom runner with shellcheck, uv, golangci-lint, and Go layered on top of the default runner image.
FROM opencode-msb/runner:base

USER root
RUN apt-get update && \
    apt-get install -y --no-install-recommends build-essential shellcheck && \
    rm -rf /var/lib/apt/lists/*

# Install Go system-wide so the launcher does not need to initialize goenv.
RUN curl -fsSL https://go.dev/dl/go1.26.5.linux-amd64.tar.gz | tar -C /usr/local -xzf -
RUN ln -sf /usr/local/go/bin/go /usr/local/bin/go

USER dev
RUN curl -fsSL https://github.com/superradcompany/microsandbox/releases/download/v0.6.6/install.sh | sh
RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
