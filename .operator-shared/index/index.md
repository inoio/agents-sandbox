---
description: Main codebase map for agents-sandbox, a Go CLI that launches coding agents (opencode, opencode2, pi, Claude Code, …) inside a microsandbox VM.
read_if: Navigating the codebase.
---

# Shared Project Index

## Architecture

- `agents-sandbox` is a Go CLI (module `github.com/inoio/agents-sandbox`, Go 1.27) that isolates coding agents (opencode, opencode2, claudecode, codex, pi, hermes, oh-my-pi) inside a dedicated microsandbox VM per project/agent.
- It uses `msb` (the microsandbox CLI / SDK) and Docker to build/reuse the VM, and Viper + Cobra for config and CLI.
- Layout: `cmd/agents-sandbox/` is the Cobra CLI; `internal/` holds the packages, dominated by `internal/sandbox/` (see its subindex). `docs/` is a Jekyll (just-the-docs) site; `ci/`, `Formula/`, `.github/` handle build/verification scripts, Homebrew, and CI (the macOS linker stubs the darwin cross-build needs live in `.github/stubs/`).
- Target platforms: Linux (KVM) and macOS (Apple Silicon); platform-specific code lives in `_darwin.go`/`_linux.go` files.
- Heavily test-driven; behaviour doubles and fail-fast installers live in a sibling `<pkg>/<name>test` subpackage so the production package's dependency graph never links `testing` (`specs/testing/test-isolation.md`).

## Project Index

- `README.md` — Overview, comparison table, boundary/tradeoffs.
- `STYLEGUIDE.md` — Documentation styleguide for user-facing docs.
- `AGENTS.md` — Authoritative agent instructions (dev workflow, toolchain, lint/test commands).
- `GLOSSARY.md` — Project terminology.
- `CHANGELOG.md`, `CHANGELOG` fragments in `changelog.d/`, `ROADMAP.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `LICENSE.md`, `SECURITY.md` — Project metadata and policy.
- `Makefile` — fmt/lint/test/build targets.
- `scripts/` — Repo-maintenance shell scripts: `cleanup-merged-branches.sh` (`make cleanup-branches`), `upstream-drift.sh`, `rebase-status.sh`, and the hermetic envelope driver `test-envelope.sh` (`make test-scrubbed`/`test-hermetic`; see `specs/testing/test-isolation.md`); each has a `test-*.sh` harness.
- `go.mod`, `go.sum`, `.go-version` — Module deps (cobra, viper, go-git, msb SDK, docker/moby, testify) and the pinned Go toolchain.
- `opencode.jsonc` — opencode config for this repo.
- `.golangci.yml`, `.gitignore` — Lint config and ignore rules.
- Tooling/ignored (`.idea/`, `.run/`, `docs/vendor/`) — IDE/tooling config and the gitignored gem bundle; omitted.

### `cmd/agents-sandbox/` — Cobra CLI entry and commands

- `main.go`, `cli.go`, `commands.go` — entry point, Cobra root, injection seams (msb/docker mocks), and root/tree/version/upgrade wiring.
- `commands_*.go` — one file per command area (run/shell, system, config incl. `config paths`, inspect, storage/`df`); `<area>_output.go` — renderers; `constants.go`/`tree.go` — command and flag names plus `tree`.
- `cli_*_test.go` — CLI integration tests, one per command/area.

### `internal/` — Core packages (non-sandbox)

- `agent/` — Agent profiles (opencode/opencode2/claudecode/codex/pi/hermes/oh-my-pi): version resolution, capabilities, config-dir names, image selection.
- `configpaths/` — Config/home path resolution and guards, plus shared config-file discovery (`FindConfigFile`) used by both config loaders.
- `configread/` — `ReadFile` under a 16 MiB cap (`MaxBytes`/`ErrTooLarge`) for untrusted config, secret, and snippet reads.
- `configdecode/` — The one config-file read/classify/decode core (`FormatFor`/`Decode`/`DecodeFile`) over `configread` and `configpaths`; JSON5/JSONC via `json5`, YAML via `go.yaml.in/yaml/v4`, TOML via `go-toml/v2`.
- `homeconfig/` — Provisions arbitrary user files (the `home:` key) into a sandbox home from the ordered config scopes.
- `docslink/` — Composes links to the published docs site (`BaseURL`, section anchors) for the doctor and build-error `Docs:` hints.
- `git/` — Git helpers (go-git wrapper) and project identity: the repo slug (origin-derived; per-slug config dir, grouping label) vs the checkout slug (git common dir; state/VM/volume/locks/image tag).
- `hostlock/` — Advisory `flock` file locks, shared by the state transactions and the per-project VM lock.
- `notify/` — Desktop notifications (backend, dedup, events, watch).
- `opener/` — Host link-opener channel: watches the home volume's spool for in-VM `xdg-open` requests, allowlist-checks the target, and hands it to the host handler.
- `termio/` — Terminal UI: role interfaces (`Logger`/`Output`/`Prompter`/`SpinnerFactory`/`TerminalState`/`Streams`), printer, table, spinner, ansi, levels.
- `upgrade/` — Self-upgrade (channel selection, Homebrew).
- `projectguard/` — Leaf holding, for one command, the digests of the approved project files; `ReadFile` verifies every project read against them and records a violation the image build and VM stage stop on.
- `projecttrust/` — Project config trust gate: the `.agents-sandbox` layer's projection (`Compute`), the decision against the per-checkout approval in `RootState` (`Evaluate`), the approval prompt/refusal, and the launcher-write approval (`ApproveSettingWrite`); wired into the root command's `PersistentPreRunE` (`cmd/agents-sandbox/trust.go`).
- `viperconfig/` — Viper-based launcher config: `Config`/`Resolver`, layer discovery (`ConfigLayers`, the six-layer precedence list), env/flag binding, the derived config-key registry, decode hooks, and validation.
- `runoptions/` — `*cobra.Command`-driven run/shell option resolution (`Build`) into `options.RunOptions`.
- `cliout/` + `view/` — Output rendering: `cliout` is domain-free (generic list/JSON plus `RenderPruneReport`); `view` holds the sandbox-shaped printers and report DTOs (see `specs/architecture/package-boundaries.md`).
- `sandbox/` — **See [sandbox subindex](sandbox.md)** (VM lifecycle, docker, image, msb, volumes, pruning, sessions, state).
- Leaf utilities — `atomicwrite` (durable state/config writes), `configmerge`, `filewalk`, `humanize`, `sysinfo`, `yamlfmt`.
- `testutil/` — Test-only helpers: in-process go-git repo init, the fail-fast mock installers, and the module-wide scans the contract tests share.

### `test/harness/` — `msb`-tagged harness smoke tests

- Starts every registered harness in a real microsandbox VM under a sized pty and asserts it reaches its TUI and exits (`harness.go` table, `pty.go` driver, `smoke_msb_test.go`; untagged unit tests for the table/pty). See `specs/testing/harness-smoke-tests.md`.

### `docs/` — Jekyll documentation site (just-the-docs)

- Navigation groups pages by user need (Get started / Guides / Recipes / Configuration / Concepts / Reference); per-feature pages live in `configuration/`, C4/lifecycle diagrams in `diagrams/*.puml`, and `vendor/` is a gitignored gem bundle.

### `ci/` — Build and verification scripts

- `check-docs.sh`/`test-check-docs.sh` (doc link/style gate), `check-deadcode.sh`/`test-check-deadcode.sh` + `deadcodecheck/` (dead-code gate driver and helper), `check-changelog.sh`/`test-check-changelog.sh` (changelog fragments).

### `Formula/` — Homebrew formula

- `agents-sandbox.rb` — Formula for macOS install.

### `.github/` — CI, Pages, and templates

- `workflows/` — `ci.yml`, `pages.yml`, `codeql.yml`, `fuzz.yml`, `mutation.yml`.
- `stubs/*.tbd` — macOS framework/library stubs the darwin cross-build needs (see `specs/release/darwin-cross-compile.md`); `ISSUE_TEMPLATE/`, `PULL_REQUEST_TEMPLATE.md`, `dependabot.yml`.

### `.agents-sandbox/` — Development VM

- `Dockerfile`, `config.yaml`, `*.sh`, `opencode2/` — dev environment image (go, msb, docker, …) and its provisioning.

### `.agents/skills/` — Repo engineering skills

- `release/` — the release runbook (`SKILL.md`, `RELEASE-NOTES.md`, `NON-GO-DEPS.md`); `bare-worktrees/` — the bare-checkout layout this repo uses.
- `handle-pr-feedback/`, `maintain/`, `pr/` — repo-adapted skills: the PR-mergeability loop, the GitHub maintainer flow, and the PR-body template.
- `compacting-context/` — the `session.compact` recipe so an agent told to compact its own context loads the skill instead of scanning for a nonexistent tool; `opencode2-self-management/`, `opencode2-probe/`, `terminal-reset/` — runbooks retired from the shared brain (the `session.compact` API, driving the opencode2 daemon's event stream, and verifying the post-attach terminal reset).
- `upstream-drift/`, `rebase-status/` — the two reporter scripts (`scripts/upstream-drift.sh`, `scripts/rebase-status.sh`) for checking how far `origin/main` moved before a rebase, and the state of an in-progress rebase.
