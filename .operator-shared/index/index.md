---
description: Main codebase map for agents-sandbox, a Go CLI that launches coding agents (opencode, opencode2, pi, Claude Code, …) inside a microsandbox VM.
read_if: Navigating the codebase.
---

# Shared Project Index

## Architecture

- `agents-sandbox` is a Go CLI (module `github.com/inoio/agents-sandbox`, Go 1.27) that isolates coding agents (opencode, opencode2, claudecode, codex, pi, hermes, oh-my-pi, kimi-code, qwen-code, goose, grok-build, mistral-vibe) inside a dedicated microsandbox VM per project/agent.
- It uses `msb` (the microsandbox CLI / SDK) and Docker to build/reuse the VM, and Viper + Cobra for config and CLI.
- Layout: `cmd/agents-sandbox/` is the Cobra CLI; `internal/` holds the packages, dominated by `internal/sandbox/` (see its subindex). `docs/` is a Jekyll (just-the-docs) site; `ci/`, `Formula/`, `.github/` handle build/verification scripts, Homebrew, and CI (the macOS linker stubs the darwin cross-build needs live in `.github/stubs/`).
- Target platforms: Linux (KVM) and macOS (Apple Silicon); platform-specific code lives in `_darwin.go`/`_linux.go` files.
- Heavily test-driven; behaviour doubles and fail-fast installers live in a sibling `<pkg>/<name>test` subpackage so the production package's dependency graph never links `testing` (`specs/testing/test-isolation.md`).

## Project Index

Root-level files:

- `README.md` — Overview, comparison table, boundary/tradeoffs.
- `STYLEGUIDE.md` — Documentation styleguide for user-facing docs.
- `AGENTS.md` — Authoritative agent instructions (dev workflow, toolchain, lint/test commands).
- `GLOSSARY.md` — Project terminology.
- `CHANGELOG.md`, `CHANGELOG` fragments in `changelog.d/`, `ROADMAP.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `LICENSE.md`, `SECURITY.md` — Project metadata and policy.
- `Makefile` — fmt/lint/test/build targets.
- `go.mod`, `go.sum`, `.go-version` — Module deps (cobra, viper, go-git, msb SDK, docker/moby, testify) and the pinned Go toolchain.
- `opencode.jsonc` — opencode config for this repo.
- `.golangci.yml`, `.gitignore` — Lint config and ignore rules.
- Tooling/ignored (`.idea/`, `.run/`, `docs/vendor/`) — IDE/tooling config and the gitignored gem bundle; omitted.

Subindexes:

- `cmd/` — the Cobra CLI entry point, command wiring, and output renderers; see the [cmd subindex](cmd.md).
- `internal/` — the core packages outside `internal/sandbox`; see the [internal subindex](internal.md).
- `internal/sandbox/` — VM lifecycle, docker, image, msb, volumes, pruning, sessions, state; see the [sandbox subindex](sandbox.md).
- `scripts/`, `spike/`, `test/`, `ci/`, `docs/`, `Formula/`, `.github/`, `.agents-sandbox/`, `.agents/skills/` — maintenance scripts, CI gates and their Go harnesses, the docs site, Homebrew, the dev-VM image, and the repo skills; see the [tooling subindex](tooling.md).
