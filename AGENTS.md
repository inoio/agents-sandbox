# AGENTS.md

## Project

`opencode-msb`: a launcher that runs opencode inside a microsandbox VM, binding a host directory as `/workspace` and persisting user state via a home directory volume.

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

- One ephemeral microsandbox VM per project serving an opencode server, multiple clients can attach to the vm & connect to the server.
- The project is exposed as an independent git clone when a different branch is
  requested, so concurrent isolated sessions are possible.
- Shared inoio LiteLLM provider/model definitions are shipped as a repo JSON fragment and passed into opencode via `OPENCODE_CONFIG_CONTENT`.

## Development

You are dogfooding the project, you are not on the host, but in an opencode-msb VM.

Installed tooling (see .opencode-msb/Dockerfile):

- go, gofmt, golangci-lint, gcc (for CGO)
- msb (microsandbox cli) - since /dev/kvm is not functional in the VM, you can't actually start VMs yourself. Must be tested manually by the user
- shell tools like jq, yq
- docker

Common development commands (run from the Go module root):

- `go mod tidy` — sync `go.mod`/`go.sum` (run after adding/removing imports).
- `go run ./cmd/opencode-msb --dry-run` — build and run locally without producing a binary or starting interactively (skips launching opencode)
- `go test ./...` — run all tests.
- `golangci-lint fmt` — format all files. Always use this command for formatting files correctly, don't rewrite files yourself.
- `golangci-lint run` — run the full linter suite.

Use the linter as a guide for code style: Run it after every major edit, for smaller edits run it after at most 3 edits.

### Superpowers

Always use your superpowers for appropriate tasks, never skip defined user approval.

### Testing

- Default to TDD - writing tests first, validating they fail, implementing changes, validating passing tests
- Make sure that new/changed CLI commands/flags are thoroughly tested in the cmd/opencode-msb/cli_*_test.go tests
- Also write valuable unit tests for internal functionality with every implementation.

## Documentation

- When changing or adding behavior, keep `README.md` and `docs` directory (except `docs/superpowers`) in sync and current.
- When you struggled with something non-obvious, propose to the user to document it in `AGENTS.md`.

## Current limitations

- `.env(rc)` secrets in the project directory are not hidden from the VM yet.
- Network egress is unrestricted.
