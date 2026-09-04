# AGENTS.md

## Project

`agents-sandbox`: a launcher that runs coding agents (opencode, pi, ...) inside a microsandbox VM, binding a host directory as `/workspace` and
persisting user state via a home directory volume.

## Code style

- Follow idiomatic Go (Effective Go / Go Proverbs).
- Write clear, self-documenting code (ex. extract a function and name it clearly).
- Use self-documenting, non-abbreviated identifiers (```additionResult := add(1,2)```). 
- Keep inline comments concise and avoid them when the code can instead be refactored to be self-documenting.
- Start/stay concrete and simple. Add abstractions when the code gives you a reason to.
- Keep units/files small and focused on a clear responsibility.
- When principles conflict, prioritize: KISS → YAGNI → SOLID → DRY

## Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- Secrets are only passed to VMs via msb's secret mechanism.

## Design decisions

- One ephemeral microsandbox VM per project, multiple clients can attach to the vm & connect
  to the server.

## Development

You are dogfooding the project, you are not on the host, but in an agents-sandbox VM. Filesystem layout:

- `/workspace` bind mount of the host CWD, mounted rw. When working there, don't commit/revert/stash etc yourself: 
  There's potential for parallel edits by other agents/humans. Always ask the user how to finalize the session.
- `~/.local/share/opencode/worktree/` git worktrees of `/workspace`, created by opencode. If that's your CWD, finalize 
  the session by pushing a PR to github and deleting the worktree.

## System tools

Prefer these installed CLI tools over other tools or custom solutions:

| When you need to...                        | Use                                             |
|--------------------------------------------|-------------------------------------------------|
| Search file contents                       | `rg` (recursive by default)                     |
| Find files by name/pattern                 | `fdfind`                                        |
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

Don't install additional tools yourself without permission.

## Project toolchain

(see .agents-sandbox/Dockerfile)

- go, gofmt, golangci-lint, gcc (for CGO), zig (for cross-compilation)
- msb (microsandbox cli) - since /dev/kvm is not functional in the VM, you can't actually start VMs yourself. Must be
  tested manually by the user
- docker

Common development commands (run from the Go module root):

- `make check` — run fmt, lint, test targets. Execute this when finalizing work.
- `go run ./cmd/agents-sandbox --dry-run` — build and run locally without producing a binary or starting interactively
  (skips launching the agent)
- `make test`/`go test ./...` — run all tests.
- `make lint`/`golangci-lint run` — run the linter. ALWAYS use! DON'T use `go vet`!
- `make fmt`/`golangci-lint fmt` — format all files. ALWAYS use! DON'T manually rewrite / use `go fmt`!
- `go mod tidy` — sync `go.mod`/`go.sum` (run after adding/removing imports).
- `make build` - build binary to `./agents-sandbox`

Use the linter as a guide for code style: Run it after every major edit, for smaller edits run it after at most 3 edits.

## Superpowers

Always use your superpowers for appropriate tasks, never skip user approval.

## Testing

- Default to TDD - writing tests first, validating they compile and fail, implementing changes, validating passing tests
- Make sure that new/changed CLI commands/flags are thoroughly tested in the cmd/agents-sandbox/cli_*_test.go tests
- Also write valuable unit tests for internal functionality with every implementation.

## Documentation

- When changing or adding behavior, keep `README.md` and `docs` directory (except `docs/superpowers`) in sync and
  current, and add a line to the `[Unreleased]` section in `CHANGELOG.md`.
- When you struggled with something non-obvious, propose to the user to document it in `AGENTS.md`.
- The Jekyll build (used by `make docs-serve` and GitHub Pages) minifies output HTML onto a single line. An inline
  `<script>` in `docs/_includes/` that starts with a `//` line comment silently disables the whole script (the comment
  swallows the rest of the line). Use `/* ... */` or no comment inside inline scripts in the docs. If inline docs JS
  "doesn't work in the browser", check the minified output for this before touching logic.

## Current limitations

- No SSH keys in the VM, git cmds against remotes won't work.
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
  go run ./cmd/agents-sandbox build -r
  ```
