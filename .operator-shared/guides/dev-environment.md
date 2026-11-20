---
description: The development VM's installed CLI tools, its `GOCACHE` placement, and the standing environment limitations (remote git over SSH, CI trigger behavior on conflicted PRs, TLS interception during image builds, and the group-writable-ancestor storage-root refusal).
read_if: Choosing a CLI tool for a task, or hitting an environment limitation such as an unusable SSH remote, an absent CI run on a conflicted PR, a self-signed-cert docker build failure, or a "catalog ancestor ... permits path substitution" storage-root refusal on the launcher's private runtime home; or questioning why `GOCACHE` is not on the home volume.
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
| Transform text streams, perform mass edits | `awk` / `sed` / `iconv`                         |
| Run independent commands in parallel       | `parallel`                                      |
| Hit HTTP endpoints / download              | `curl` / `wget`                                 |
| Inspect network/ports/sockets              | `ip` / `ss` / `nc` (no `netstat`/`ifconfig`)    |
| Resolve DNS / check connectivity           | `dig` / `nslookup` / `ping`                     |
| Capture / inspect network packets          | `tcpdump`                                       |
| Find what holds a port or file             | `lsof` (or `fuser`, `ss -p`)                    |
| Inspect a file or tree                     | `file` / `tree`                                 |
| Sync directories/files                     | `rsync`                                         |
| Read docs / manuals                        | `man`                                           |
| Create/extract archives                    | `tar` / `zip` / `unzip` / `xz` / `lz4` / `zstd` |
| Inspect a SQLite database                  | `sqlite3`                                       |
| Search or rewrite code by structure (AST)  | `ast-grep`                                      |
| Debug a failing syscall / namespace setup  | `strace`                                        |
| Inspect a TLS certificate or trust chain   | `openssl` (`s_client`, `x509`)                  |
| Add an apt key / verify a signature        | `gpg`                                           |
| Create a Python environment                | `python3 -m venv`                               |
| Go: navigate, find references, rename      | `gopls` (`definition`, `references`, `rename`)  |
| Go: add/remove imports after a move        | `goimports`                                     |
| Wait for PR/CI checks to finish            | `gh pr checks <n> --watch` / `gh run watch`     |

`~/go/bin` (GOBIN) is on `PATH`, so a `go install`ed tool is runnable in a later
shell.

## Build cache

`GOCACHE` is `/var/tmp/go-build`, set in `.agents-sandbox/env`. The home volume
(`/home/dev`) is virtiofs, where small-file metadata operations run an order of
magnitude slower than the VM's overlay root — measured (2026-11-20) ~9× on create,
~17× on stat/read, ~10× on unlink, and ~23–33× tmpfs on the same ops — and the Go build
cache is thousands of small object files (ours had grown to 27 GB). Large sequential IO
is fine (virtiofs wrote a 512 MiB file faster than the overlay root), and a cold
`go build` is CPU-bound, so a cache on `/home/dev` cost only ~5–15% more wall time in a
direct test; the wins are bounding the cache against the home volume's disk and sparing
that small-file IO.
`/var/tmp` is on the local root disk and is ephemeral, so the cache rebuilds when the VM
is recreated; Go itself only prunes entries older than five days, so the location is
what bounds it, not a size cap.

## Current limitations

- No SSH keys in the VM, and no SSH client by default (`openssh-client` is not
  installed). **HTTPS remotes work** (`git fetch`, `git push`, and `gh` are
  authenticated); SSH remotes fail both for want of a client and of keys. Prefer
  `gh` for GitHub operations.
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
- The launcher's private runtime home is `~/.local/share/agents-sandbox/msb`
  (`$XDG_DATA_HOME/agents-sandbox/msb`). microsandbox refuses a storage root that
  has a group- or other-writable ancestor, and this VM runs with `umask 002`, so
  `~/.local/share` can be group-writable and the launcher then fails with
  `catalog ancestor ... permits path substitution`. Fix with
  `chmod g-w ~/.local/share` or run under `umask 022`. The default `~/.microsandbox`
  home used by a bare `msb` is unaffected (its ancestors are not group-writable).
  See `docs/troubleshooting.md` → Microsandbox runtime.
- **Do not run a mutation sweep (`make mutation`, or `go run gomutants`) inside this VM.**
  gomutants is memory-heavy (a per-package sweep spawns many test binaries) and its load
  reliably OOMs the VM, which has no swap. Run it on the host, or read the advisory `mutation`
  CI job's sticky PR comment instead. See `guides/mutation-testing.md`.
