---
description: Main codebase map for agents-sandbox, a Go CLI that launches coding agents (opencode, pi, Claude Code) inside a microsandbox VM.
read_if: Navigating the codebase.
---

# Shared Project Index

## Architecture

- `agents-sandbox` is a Go CLI (module `github.com/inoio/agents-sandbox`, Go 1.26) that isolates coding agents (opencode, opencode2, pi, Claude Code) inside a dedicated microsandbox VM per project/agent.
- It uses `msb` (the microsandbox CLI / SDK) and Docker to build/reuse the VM, and Viper + Cobra for config and CLI.
- Layout: `cmd/agents-sandbox/` is the Cobra CLI; `internal/` holds the packages, dominated by `internal/sandbox/` (see its subindex). `docs/` is a Jekyll (just-the-docs) site; `ci/builder/`, `Formula/`, `.github/` handle cross-compile, Homebrew, and CI.
- Target platforms: Linux (KVM) and macOS (Apple Silicon); platform-specific code lives in `_darwin.go`/`_linux.go` files.
- Heavily test-driven; each `internal/` package pairs source files with `*_test.go` (and often `main_test.go`/`testmock.go`/`coverage_*.go`) for mocks and integration seams.

## Project Index

- `README.md` — Overview, comparison table, boundary/tradeoffs.
- `AGENTS.md` — Authoritative agent instructions (dev workflow, toolchain, lint/test commands).
- `CHANGELOG.md`, `ROADMAP.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `LICENSE.md`, `SECURITY.md` — Project metadata and policy.
- `Makefile` — fmt/lint/test/build targets.
- `go.mod`, `go.sum` — Module deps (cobra, viper, go-git, msb SDK, docker/moby, testify).
- `opencode.jsonc` — opencode config for this repo.
- `.golangci.yml`, `.gitignore` — Lint config and ignore rules.
- Tooling dirs (`.idea/`, `.run/`, `.opencode/`, `.pi/`, `.superpowers/`, `.junie/`, `docs/vendor/`) — IDE/tooling config; omitted.

### `cmd/agents-sandbox/` — Cobra CLI entry and command implementations

- `main.go` — Entry point; wires termio UI and calls `execute`.
- `cli.go` — Cobra root, `execute`, injection seams (msb/docker mocks).
- `commands.go` — Root/tree/version/upgrade commands and wiring.
- `commands_cli.go` — run/shell/stop/kill commands.
- `commands_system.go` — doctor/list/config/home/agent/build/dockerfile/image/volume/sandbox/prune commands.
- `constants.go`, `tree.go` — command name/flag constants and `tree` output.
- `cli_*_test.go` — Extensive CLI integration tests (one per command/area).

### `internal/` — Core packages (non-sandbox)

- `agent/` — Agent (opencode/opencode2/pi/claudecode) version resolution, manifests, image selection.
- `configfile/` — Comment/order-preserving launcher-config editor (`Config`, `Parse`, `LoadDir`, `WriteAtomic`); shared by `configmigration` and the setup wizard.
- `configmerge/` — Merge config with precedence.
- `configmigration/` — **See [configmigration subindex](configmigration.md)** (native agent config migration, guided onboarding, network plan).
- `configpaths/` — Config/home path resolution and guards.
- `git/` — Git helpers (go-git wrapper).
- `homeconfig/` — Home config provisioning.
- `humanize/` — Human-readable formatting.
- `notify/` — Desktop notifications (backend, dedup, events, watch).
- `sandbox/` — **See [sandbox subindex](sandbox.md)** (VM lifecycle, docker, image, msb, volumes, pruning, sessions, state).
- `sysinfo/` — Platform system info (`_darwin.go`/`_linux.go`).
- `termio/` — Terminal UI (printer, table, prompt, spinner, ansi, levels).
- `testutil/` — Test helpers (mocks, testgit).
- `upgrade/` — Self-upgrade (incl. Homebrew).
- `viperconfig/` — Viper-based launcher config.
- `yamlfmt/` — YAML formatting.

### `docs/` — Jekyll documentation site (just-the-docs)

- `_config.yml`, `Gemfile`, `_includes/*.html` — Site config and templates.
- `*.md` — Pages (introduction, install, commands, configuration, how-it-works, sandboxes, switch, troubleshooting, recipes, runner-image, manage-config, branch-sessions).
- `configuration/` — Per-feature docs (agent, launcher, mounts, networking, secrets, notifications, home-provisioning, self-upgrade).
- `diagrams/*.puml` — PlantUML C4 and lifecycle diagrams.
- `vendor/` — Gitignored gem bundle; omit.

### `ci/builder/` — Cross-compile builder image

- `Dockerfile`, `stubs/*.tbd` — Builder with macOS linker stubs.
- `ci/check-docs.sh` — Docs build check script.

### `Formula/` — Homebrew formula

- `agents-sandbox.rb` — Formula for macOS install.

### `.github/` — CI and issue templates

- `workflows/ci.yml`, `workflows/pages.yml` — CI and GitHub Pages workflows.
- `ISSUE_TEMPLATE/*.yml`, `PULL_REQUEST_TEMPLATE.md`, `dependabot.yml` — Templates and dependabot config.

### `.agents-sandbox/` — Development VM

- `Dockerfile` — Dev environment image (go, msb, docker, etc.).
- `config.yaml`, `*.sh`, `opencode/` — Dev VM provisioning/scripts.

### `.agents/` — Repo engineering skills (untracked tooling)

- `skills/` — Matt Pocock engineering skills (ask-matt, code-review, tdd, brainstorming, etc.); directory-only.
