# AGENTS.md

## Project

`opencode-msb`: a launcher that runs opencode inside a microsandbox VM, binding a host directory as `/workspace` and persisting user state via a home directory volume.

## Code style (MVP)

- Write self-explanatory code.
- Keep abstractions minimal; do not introduce layers until they are clearly needed.
- Add inline comments only when the code is not self-explanatory.
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

- One ephemeral microsandbox VM per `opencode` invocation.
- The project is exposed as an independent git clone when a different branch is
  requested, so concurrent isolated sessions are possible.
- opencode state is stored in msb named volumes per project; if msb volumes are not viable, fall back to project-local host directories.
- Shared inoio LiteLLM provider/model definitions are shipped as a repo JSON fragment and passed into opencode via `OPENCODE_CONFIG_CONTENT`.

## Development

Installed tooling:

- go, gofmt, golangci-lint, gcc (for CGO)
- msb (microsandbox cli)
- shell tools like jq, yq

Common development commands (run from the Go module root):

- `go mod tidy` — sync `go.mod`/`go.sum` (run after adding/removing imports).
- `go run ./cmd/opencode-msb --dry-run` — build and run locally without producing a binary or starting interactively (skips launching opencode)
- `go test ./...` — run all tests.
- `golangci-lint fmt` — format all files
- `golangci-lint run` — run the full linter suite.

### Testing

- prefer unit tests on pure functions with mocks over integration tests

## Documentation



## Current limitations

- `.envrc` secrets in the project directory are not hidden from the VM yet.
- Network egress is unrestricted.
