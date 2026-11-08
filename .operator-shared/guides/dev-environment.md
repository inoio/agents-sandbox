---
description: The development VM's installed CLI tools and the standing environment limitations (remote git over SSH, CI trigger behavior on conflicted PRs, TLS interception during image builds).
read_if: Choosing a CLI tool for a task, or hitting an environment limitation such as an unusable SSH remote, an absent CI run on a conflicted PR, or a self-signed-cert docker build failure.
---

# Development environment

Reference for the project's agents-sandbox VM that does not need to sit in `AGENTS.md` on every
turn. The toolchain and the common `make` commands stay in `AGENTS.md` → Project toolchain.

## System tools

Prefer these installed CLI tools over other tools or custom solutions:

| When you need to...                        | Use                                             |
|--------------------------------------------|-------------------------------------------------|
| Search file contents                       | `rg` (recursive by default)                     |
| Find files by name/pattern                 | `fdfind` (also reachable as `fd`)               |
| Parse/transform JSON or YAML               | `jq` / `yq` (never brittle text parsing)        |
| Transform text streams, perform mass edits | `awk` / `sed` / `recode`                        |
| Run independent commands in parallel       | `parallel`                                      |
| Hit HTTP endpoints / download              | `curl` / `wget`                                 |
| Inspect network/ports/sockets              | `ip` / `ss` / `nc`                              |
| Resolve DNS / check connectivity           | `dig` / `nslookup` / `ping`                     |
| Inspect a file or tree                     | `file` / `tree`                                 |
| Sync directories/files                     | `rsync`                                         |
| Read docs / manuals                        | `man`                                           |
| Create/extract archives                    | `tar` / `zip` / `unzip` / `xz` / `lz4` / `zstd` |
| Inspect a SQLite database                  | `sqlite3`                                       |

## Current limitations

- No SSH keys in the VM. **HTTPS remotes work** (`git fetch`, `git push`, and `gh` are
  authenticated); only SSH remotes fail. Prefer `gh` for GitHub operations.
- GitHub only runs `pull_request` workflows when the PR is mergeable. While a PR has merge
  conflicts, the `ci` workflows (`lint`, `test`, codecov) silently do **not** run — they are
  absent, not failed. Rebase to resolve the conflict and they trigger on the next push.
- microsandbox injects a TLS cert into the VM for egress inspection. This can cause docker image
  builds to fail with self-signed cert errors. Workaround (example base image):

  ```
  # 1. Build a CA-trusting replacement for debian:trixie-slim.
  mkdir -p /tmp/cabase && cd /tmp/cabase
  cp /usr/local/share/ca-certificates/microsandbox-ca.crt ./
  cat > Dockerfile <<'EOF'
  FROM debian:trixie-slim
  COPY microsandbox-ca.crt /usr/local/share/ca-certificates/microsandbox-ca.crt
  RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
  update-ca-certificates && rm -rf /var/lib/apt/lists/*
  EOF
  docker build -t debian:trixie-slim .

  # 2. Build the runner image as usual.
  go run ./cmd/agents-sandbox build -r
  ```
