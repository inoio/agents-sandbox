# Contributing to opencode-sandbox

Thanks for considering a contribution. This guide explains how to build, test, and submit changes.

## Project overview

The layout is a standard Go module:

- `cmd/opencode-sandbox` — the CLI entry point
- `internal/` — packages for sandbox lifecycle, VM control, image building, pruning, config, and more
- `docs/` — user documentation

## Requirements

- Go 1.26 (see `go.mod`)
- `golangci-lint` v2 for linting and formatting
- Docker for building the runner image (`opencode-sandbox build`)
- `zig` 0.16.0 - only needed for cross-compiling release binaries (`make build-release`)

## Common commands

Run these from the module root:

| Command              | Purpose                                                    |
|----------------------|------------------------------------------------------------|
| `make test`          | Run the test suite                                         |
| `make lint`          | Run the linter                                             |
| `make fmt`           | Format all source files                                    |
| `make check`         | Format, lint, and test — run before finalizing any change  |
| `make build`         | Build the `opencode-sandbox` binary                        |
| `make coverage`      | Run tests and print the coverage total                     |
| `go mod tidy`        | Sync `go.mod` / `go.sum` after changing imports            |

## Writing code

- Prefer self-explanatory code to comments.
- Keep abstractions minimal. Prefer small, focused files with one clear responsibility.
- Follow Go conventions: idiomatic error handling, no globals where avoidable.

### Formatting

Use `make fmt` (which runs `golangci-lint fmt`). Do not hand-format or use plain `go fmt`.

### Linting

Use `make lint` (`golangci-lint run ./...`), which includes `vet`.

## Testing

This project defaults to **test-driven development**:

1. Write a failing test first.
2. Confirm it compiles and fails for the expected reason.
3. Implement the change.
4. Confirm the test passes.

- Every new or changed CLI command or flag should be covered in `cmd/opencode-sandbox/cli_*_test.go`.
- Add focused unit tests for internal functionality with each implementation.
- Do manual tests and run the full suite with `make check` before submitting.

## Documentation

Keep user documentation current with any behavior change:

- `README.md` for anything visible at a glance (quick start, installation, badges).
- `docs/` for commands, configuration, sandbox lifecycle, and troubleshooting.

If you change a CLI command or flag, update the relevant docs in the same PR.

## Submitting a PR

1. Open an issue first if the change is non-trivial, so the approach can be discussed.
2. Create a focused branch with a descriptive name.
3. Make your changes, following the testing and formatting rules above.
4. Run `make check` and make sure it passes.
5. Open a pull request and fill out the template.
6. Address review feedback; keep the commit history clean.

## Changelog & versioning

- The project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Release tags are `vMAJOR.MINOR.PATCH`
  (e.g. `v0.1.0`); releases and the `version` command use the bare version (e.g. `0.1.0`).
- Keep `CHANGELOG.md` current. When you make a user-visible change, add an item under the
  `[Unreleased]` section.

## Security

If you find a security vulnerability, do not open a public issue. Report it privately per `SECURITY.md` (or open a
private/confidential issue) so it can be addressed before disclosure.