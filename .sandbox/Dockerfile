# inoio-sandbox runner with shellcheck layered on top of the default runner image.
# The base image is built and tagged inoio-sandbox/runner:base by the launcher
# (see image.ensure_base_image) before this image is built.
FROM inoio-sandbox/runner:base

USER root
RUN apt-get update && \
    apt-get install -y --no-install-recommends shellcheck && \
    rm -rf /var/lib/apt/lists/* \
USER dev
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
RUN curl -fsSL https://github.com/superradcompany/microsandbox/releases/download/v0.6.6/install.sh | sh
