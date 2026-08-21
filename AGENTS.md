# AGENTS.md

## Project

`opencode-sandbox`: a launcher that runs opencode inside a microsandbox VM, binding a host directory as `/workspace` and
persisting user state via a home directory volume.

## Code style (MVP)

- Write self-explanatory code.
- Keep abstractions minimal; do not introduce layers until they are clearly needed.
- Before adding inline comments, try to make the code self-explanatory.
- Prefer small, focused files with one clear responsibility.
- Apply the following principles
    * SOLID
    * DRY
    * KISS
    * YAGNI
    * Convention over Configuration
    * Composition over Inheritance
    * Law of Demeter
    * Go Style
    * Effective Go
    * Go Proverbs

## Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- Secrets are only passed to VMs via msb's secret mechanism.

## Design decisions

- One ephemeral microsandbox VM per project serving an opencode server, multiple clients can attach to the vm & connect
  to the server.
- The project is exposed as an independent git clone when a different branch is requested, so concurrent isolated
  sessions are possible.

## Development

You are dogfooding the project, you are not on the host, but in an opencode-sandbox VM. Filesystem layout:

- `/workspace` bind mount of the host CWD, mounted rw. When working there, there's potential for parallel edits by other agents/humans.
- `~/.local/share/opencode/worktree/` git worktrees of `/workspace`, created by opencode. If that's your CWD, when finalizing the session, after merging/pushing, always delete the worktree.

Installed tooling (see .opencode-sandbox/Dockerfile):

- go, gofmt, golangci-lint, gcc (for CGO)
- msb (microsandbox cli) - since /dev/kvm is not functional in the VM, you can't actually start VMs yourself. Must be
  tested manually by the user
- shell tools like jq, yq, rg
- docker
- git: No SSH keys in the VM, git cmds against remotes won't work.

Don't install additional tools yourself without permission.

Common development commands (run from the Go module root):

- `go mod tidy` — sync `go.mod`/`go.sum` (run after adding/removing imports).
- `go run ./cmd/opencode-sandbox --dry-run` — build and run locally without producing a binary or starting interactively
  (skips launching opencode)
- `make fmt`/`golangci-lint fmt` — format all files. ALWAYS use! DON'T manually rewrite / use `go fmt`!
- `make lint`/`golangci-lint run` — run the linter. ALWAYS use! DON'T use `go vet`!
- `make test`/`go test ./...` — run all tests.
- `make check` — run fmt, lint, test targets. Execute this when finalizing work.
- `make build` - build binary to `./opencode-sandbox`

Use the linter as a guide for code style: Run it after every major edit, for smaller edits run it after at most 3 edits.

Content search: use `rg` (recursive by default, don't use `-r` -> `--replace`).

### Superpowers

Always use your superpowers for appropriate tasks, never skip defined user approval.

### Testing

- Default to TDD - writing tests first, validating they compile and fail, implementing changes, validating passing tests
- Make sure that new/changed CLI commands/flags are thoroughly tested in the cmd/opencode-sandbox/cli_*_test.go tests
- Also write valuable unit tests for internal functionality with every implementation.

## Documentation

- When changing or adding behavior, keep `README.md` and `docs` directory (except `docs/superpowers`) in sync and
  current, and add a line to the `[Unreleased]` section in `CHANGELOG.md`. 
- When you struggled with something non-obvious, propose to the user to document it in `AGENTS.md`.

## Current limitations

- `.env(rc)` secrets in the project directory are not hidden from the VM yet.
- microsandbox injects a tls cert into the VM for egress inspection. This can cause docker image builds to fail with
  self-signed cert errors. Workaround (example base image):

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
  go run ./cmd/opencode-sandbox build -y
  ```
