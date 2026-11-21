---
description: The cmd/agents-sandbox Cobra CLI tree - entry point, command wiring, injection seams, and per-command output renderers.
read_if: Adding or changing a CLI command, flag, or its rendering in cmd/agents-sandbox.
---

# cmd

## Coverage

- `cmd/agents-sandbox/` — the Cobra CLI

## Architecture

- `main.go` is the entry point; `commands_*.go` is one file per command area, with its `<area>_output.go` renderers beside it; `cli_*_test.go` holds the CLI integration tests, one per command/area.
- The command injection seams (the msb/docker/client mocks) and the root/tree/version/upgrade wiring live in `cli.go`/`commands.go`.

## `cmd/agents-sandbox` Index

- `main.go`, `cli.go`, `commands.go` — entry point, Cobra root, injection seams (msb/docker mocks), and root/tree/version/upgrade wiring.
- `commands_*.go` — one file per command area (run/shell, system, config incl. `config paths`, `config learn`, and `config harden`, inspect, storage/`df`); `<area>_output.go` — renderers; `constants.go`/`tree.go` — command and flag names plus `tree`.
- `cli_*_test.go` — CLI integration tests, one per command/area.
