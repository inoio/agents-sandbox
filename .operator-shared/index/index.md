---
description: Main codebase map for agents-sandbox, a Go CLI that launches coding agents (opencode, opencode2, pi, Claude Code) inside a microsandbox VM.
read_if: Navigating the codebase.
---

# Shared Project Index

## Architecture

- `agents-sandbox` is a Go CLI (module `github.com/inoio/agents-sandbox`, Go 1.26) that isolates coding agents (opencode, opencode2, pi, Claude Code) inside a dedicated microsandbox VM per project/agent.
- It uses `msb` (the microsandbox CLI / SDK) and Docker to build/reuse the VM, and Viper + Cobra for config and CLI.
- Layout: `cmd/agents-sandbox/` is the Cobra CLI; `internal/` holds the packages, dominated by `internal/sandbox/` (see its subindex). `docs/` is a Jekyll (just-the-docs) site; `ci/`, `Formula/`, `.github/` handle build/verification scripts, Homebrew, and CI (the macOS linker stubs the darwin cross-build needs live in `.github/stubs/`).
- Target platforms: Linux (KVM) and macOS (Apple Silicon); platform-specific code lives in `_darwin.go`/`_linux.go` files.
- Heavily test-driven; each `internal/` package pairs source files with `*_test.go` (and often `main_test.go`/`testmock.go`/`coverage_*.go`) for mocks and integration seams.

## Project Index

- `README.md` — Overview, comparison table, boundary/tradeoffs.
- `STYLEGUIDE.md` — Documentation styleguide for user-facing docs.
- `AGENTS.md` — Authoritative agent instructions (dev workflow, toolchain, lint/test commands).
- `GLOSSARY.md` — Project terminology.
- `CHANGELOG.md`, `ROADMAP.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `LICENSE.md`, `SECURITY.md` — Project metadata and policy.
- `Makefile` — fmt/lint/test/build targets.
- `scripts/` — Repo-maintenance shell scripts; `cleanup-merged-branches.sh` prunes merged worktrees and branches (`make cleanup-branches`).
- `go.mod`, `go.sum` — Module deps (cobra, viper, go-git, msb SDK, docker/moby, testify).
- `opencode.jsonc` — opencode config for this repo.
- `.golangci.yml`, `.gitignore` — Lint config and ignore rules.
- Tooling dirs (`.idea/`, `.run/`, `.opencode/`, `.pi/`, `.superpowers/`, `.junie/`, `docs/vendor/`) — IDE/tooling config; omitted.

### `cmd/agents-sandbox/` — Cobra CLI entry and command implementations

- `main.go` — Entry point; wires termio UI and calls `execute`.
- `cli.go` — Cobra root, `execute`, injection seams (msb/docker mocks).
- `launcher_store.go` — Adapter binding the root-state transaction API to the `internal/upgrade` store seam (turns a contended non-blocking lock into an error so the check defers rather than reading a false empty state).
- `commands.go` — Root/tree/version/upgrade commands and wiring.
- `commands_cli.go` — run/shell/stop/kill commands.
- `commands_system.go` — doctor/list/config/home/agent/build/dockerfile/image/volume/sandbox/prune commands, including `config paths` (the config layers in precedence order, driven by `viperconfig.ConfigLayers`).
- `commands_inspect.go` — `volume inspect`/`image inspect`/`sandbox inspect` command builders.
- `commands_storage.go` — `df` command and the `storage` group alias (`storage df`/`storage prune`).
- `inspect_output.go`, `storage_output.go` — single-object inspect rendering and the msb-shaped `df` report.
- `cli_storage_test.go` — CLI tests for `df` (table/JSON/verbose/quiet/degradation) and the `storage` group.
- `constants.go`, `tree.go` — command name/flag constants and `tree` output.
- `cli_*_test.go` — Extensive CLI integration tests (one per command/area).

### `internal/` — Core packages (non-sandbox)

- `agent/` — Agent (opencode/opencode2/pi/claudecode) version resolution, manifests, image selection.
- `atomicwrite/` — Atomic file writes (unique temp file + fsync + rename, 0600/0700); shared by the persistent state/config writers (`state.WriteState`, `state.WriteRootState`, `vm.saveUpgradeState`, `upgrade.saveState`).
- `cliout/` — Output rendering core, domain-free: the generic list/JSON rendering (`PrintList`/`Mode`/`WriteJSON`) and the report-mode renderer (`RenderPruneReport`). A `boundary_test.go` pins the no-domain-import rule; the domain printers live in `internal/view` (see `specs/cliout-package-boundary.md`).
- `configmerge/` — Merge config with precedence.
- `configpaths/` — Config/home path resolution and guards, plus the shared config-file discovery (`FindConfigFile`, the single extension order) that both config loaders call.
- `filewalk/` — Best-effort recursive file walk shared by the tool.
- `git/` — Git helpers (go-git wrapper) and project identity: `Identity`/`ResolveIdentity` with the repo slug (origin-derived; keys the per-slug user config dir and the grouping label) and the checkout slug (git common dir; keys state/VM/volume/locks/image tag), plus the bind-root rule (#144).
- `homeconfig/` — Home config provisioning.
- `hostlock/` — Advisory `flock` file locks, shared by the state transactions and the per-project VM lock.
- `humanize/` — Human-readable formatting.
- `notify/` — Desktop notifications (backend, dedup, events, watch).
- `sandbox/` — **See [sandbox subindex](sandbox.md)** (VM lifecycle, docker, image, msb, volumes, pruning, sessions, state).
- `sysinfo/` — Platform system info (`_darwin.go`/`_linux.go`).
- `termio/` — Terminal UI (printer, table, prompt, spinner, ansi, levels).
- `testutil/` — Test helpers (mocks, testgit) and the test-isolation contract test (`isolation_contract_test.go`).
- `upgrade/` — Self-upgrade (incl. Homebrew).
- `view/` — Domain-shaped output renderers for the launcher (#357): `SandboxPrinter`/`ImagePrinter`/`VolumePrinter` (the `cliout.Printer` implementations, plus digest truncation and the volume SIZE-column rules) and the prune/compact report JSON DTOs. Keeps `cliout` domain-free (see `specs/cliout-package-boundary.md`).
- `viperconfig/` — Viper-based launcher config: `Config`/`Resolver`, layer discovery (`ConfigLayers`, the six-layer precedence list that drives both `NewResolver`'s merge order and the `config paths` display), env/flag binding, decode hooks, and validation. Split across focused files (`config.go`, `load.go`, `env.go`, `flags.go`, `docker.go`, `decode.go`, `validate.go`, `accessors.go`).
- `runoptions/` — The `*cobra.Command`-driven run/shell option resolution (`Build`): flags, agent, notify, network, sizes, and mounts combined with the resolved `viperconfig.Config` into `options.RunOptions`. Extracted from `viperconfig` so the config-loading contract and the session-configuration contract change independently (#356). `ResolveDockerSetting` is shared with the build command's `resolveBuildDockerSetting` so the run and build paths cannot resolve docker/dind differently (#363).
- `yamlfmt/` — YAML formatting.

### `test/harness/` — `msb`-tagged harness smoke tests

- `harness.go` — Harness table (per-harness quit key, network override, normal-buffer and reacts-only flags), child-env allowlist, daemon-timeout/ANSI/alt-screen helpers.
- `pty.go` — pty driver running a command under a sized pseudo-terminal and capturing its output.
- `smoke_msb_test.go` — The `msb`-tagged test: platform/KVM preflight, then shared root + binary build, isolated-env docker preflight, per-harness image prebuild, then starts each harness (`agents-sandbox run --agent <name>`) in a real microsandbox VM and asserts TUI + clean exit (taps the quit key until the process goes).
- `harness_test.go`, `pty_test.go` — Untagged unit tests (table covers every registered agent, env allowlist, pty round-trip/exit/tail).
- `main_test.go` — Inert `TestMain` installing the fail-fast seam mocks.

### `docs/` — Jekyll documentation site (just-the-docs)

- `_config.yml`, `Gemfile`, `_includes/*.html` — Site config and templates.
- `*.md` — Section landings (`get-started.md`, `guides.md`, `recipes.md`, `configuration/index.md`, `concepts.md`, `reference.md`) and top-level pages (`index.md`, `introduction.md`, `switch.md`, `manage-config.md`, `tutorial.md`, `sandboxes.md`, `runner-image.md`, `branch-sessions.md`, `commands.md`, `troubleshooting.md`, `deprecations.md`, `how-it-works.md`). Navigation groups pages by user need (Get started / Guides / Recipes / Configuration / Concepts / Reference) via front-matter `parent`.
- `configuration/` — Per-feature docs (agent, launcher, mounts, networking, secrets, notifications, home-provisioning, self-upgrade).
- `diagrams/*.puml` — PlantUML C4 and lifecycle diagrams.
- `vendor/` — Gitignored gem bundle; omit.

### `ci/` — Build and verification scripts

- `check-docs.sh`, `test-check-docs.sh` — Documentation link/style gate and its own tests.
- `check-deadcode.sh`, `test-check-deadcode.sh` — Dead-code gate driver and its own tests.

### `ci/deadcodecheck/` — Dead-code gate helper

- `main.go` — Pure `ParseReport`/`ScanMarkers`/`Check` behind one testable `run(args, stdin, stdout, stderr) int`; parses the `deadcode` analysis report, scans `//deadcode:ignore` markers, and returns the pass/fail verdict with distinct exit codes. Invoked by `ci/check-deadcode.sh`.

### `Formula/` — Homebrew formula

- `agents-sandbox.rb` — Formula for macOS install.

### `.github/` — CI and issue templates

- `workflows/ci.yml`, `workflows/pages.yml` — CI and GitHub Pages workflows.
- `stubs/*.tbd` — Empty macOS framework/library stubs (`CoreFoundation`, `Security`, `libresolv`). zig ships macOS headers but not these, so the release job copies them into zig's `lib/libc/darwin` before the darwin cross-build; the dev VM image does the same.
- `ISSUE_TEMPLATE/*.yml`, `PULL_REQUEST_TEMPLATE.md`, `dependabot.yml` — Templates and dependabot config.

### `.agents-sandbox/` — Development VM

- `Dockerfile` — Dev environment image (go, msb, docker, etc.).
- `config.yaml`, `*.sh`, `opencode/` — Dev VM provisioning/scripts.

### `.agents/skills/` — Repo engineering skills

- `release/` — The release runbook (tracked): `SKILL.md`'s release flow, `RELEASE-NOTES.md`, and `NON-GO-DEPS.md` (the pinned non-Go tool versions).
- Everything else — local-only engineering skills (ask-matt, code-review, tdd, ...); untracked tooling, not shipped with the repo.
