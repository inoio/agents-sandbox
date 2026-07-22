# Custom runner with shellcheck, uv, goenv, and golangci-lint layered on top of the default runner image.
FROM opencode-msb/runner:base

USER root
RUN apt-get update && \
    apt-get install -y --no-install-recommends build-essential shellcheck && \
    rm -rf /var/lib/apt/lists/* \
USER dev
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
RUN curl -fsSL https://github.com/superradcompany/microsandbox/releases/download/v0.6.6/install.sh | sh
RUN git clone https://github.com/go-nv/goenv.git ~/.goenv
ENV GOENV_ROOT="$HOME/.goenv"
ENV PATH="$GOENV_ROOT/bin:$PATH"
RUN echo 'eval "$(goenv init -)"' >> ~/.bashrc
RUN goenv install 1.26.5
RUN goenv global 1.26.5
RUN eval "$(goenv init -)" && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest