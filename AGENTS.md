# AGENTS.md

## Project

`opencode-msb`: a minimal launcher that runs opencode inside a microsandbox VM, binding the current project directory (or an independent git clone for a selected branch) as `/workspace` and persisting opencode state in msb volumes.

## Code style (MVP)

- Write self-explanatory code.
- Keep abstractions minimal; do not introduce layers until they are clearly needed.
- Add inline comments only when the code is not self-explanatory.
- Disregard the existing German inline documentation and coding style from the POC spike.
- Prefer small, focused files with one clear responsibility.

## Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- No network rules for the MVP; default egress is allowed so web search works.
- API keys are personal. The launcher forwards them via `msb --secret` so the real value never enters the VM.
- Do not commit secrets or `.envrc` content into the working directory.

## Design decisions

- Dockerfile-first runner image.
- One ephemeral microsandbox VM per `opencode` invocation.
- The project is exposed as an independent git clone when a different branch is
  requested, so concurrent isolated sessions are possible.
- opencode state is stored in msb named volumes per project; if msb volumes are not viable, fall back to project-local host directories.
- Shared inoio LiteLLM provider/model definitions are shipped as a repo JSON fragment and passed into opencode via `OPENCODE_CONFIG_CONTENT`.

## Development

Installed tooling:

- go, gofmt, golangci-lint, gcc (for CGO). Provides `go mod`, `go run`, `go test`, `golangci-lint run`, `gofmt`, `.
- msb (microsandbox cli)
- shell tools like jq, yq

- **Toolchain:** `go` and a C compiler (`gcc` on Linux, `clang` on macOS — required by CGO for the microsandbox SDK). One `go` install provides `go mod`, `go run`, `go test`, `go vet`, `gofmt`.
- **Linter:** `golangci-lint` — install separately, e.g. `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest` or `brew install golangci-lint`.

Common commands (run from the Go module root):

- `go mod tidy` — sync `go.mod`/`go.sum` (run after adding/removing imports).
- `go run ./cmd/opencode-msb` — build and run locally without producing a binary.
- `go test ./...` — run all tests.
- `go vet ./...` — basic static checks (part of the toolchain).
- `gofmt -l .` — list files that need formatting (`-w` to write).
- `golangci-lint run` — run the full linter suite (replaces `ruff` for Go).

## Documentation

- Keep docs minimal and concise. A short README with a HOWTO is enough for this phase.

## Current limitations

- `.envrc` secrets in the project directory are not hidden from the VM yet.
- Network egress is unrestricted.
