---
description: The internal/ packages outside internal/sandbox - agent profiles, config resolution and trust, git identity, and the leaf utilities.
read_if: Working in an internal/ package that is not under internal/sandbox.
---

# Internal

## Coverage

- `internal/` — every package except `internal/sandbox/` (see the [sandbox subindex](sandbox.md))

## Architecture

- Two concerns dominate this half: **agent/harness profiles** (`agent/`) and the **launcher config surface** (`viperconfig/`, `configpaths/`, `configdecode/`, `configread/`, `homeconfig/`, `runoptions/`).
- `internal/sandbox/` is the other half of `internal/` and has its own subindex; the packages here are the ones a sandbox stage or the CLI reaches into, not the VM machinery itself.

## `internal/` Index

- `agent/` — Agent profiles (one `<name>.go` + `<name>_release.go` per built-in agent, registered in `builtinRegistry`): version resolution, capabilities (including the optional `EnvIndirectionDescriber` a harness like kimi uses to read its key from an env-var name), config-dir names, image selection.
- `configpaths/` — Config/home path resolution and guards, plus shared config-file discovery (`FindConfigFile`) used by both config loaders; `configpathstest/` — its test double.
- `configread/` — `ReadFile` under a 16 MiB cap (`MaxBytes`/`ErrTooLarge`) for untrusted config, secret, and snippet reads.
- `configdecode/` — The one config-file read/classify/decode core (`FormatFor`/`Decode`/`DecodeFile`) over `configread` and `configpaths`; JSON5/JSONC via `json5`, YAML via `go.yaml.in/yaml/v4`, TOML via `go-toml/v2`.
- `homeconfig/` — Provisions arbitrary user files (the `home:` key) into a sandbox home from the ordered config scopes.
- `firstrun/` — First-run onboarding: the decision predicate (`Offer`/`BuildFacts`), the three-choice menu (`Prompt`) on `termio.Prompter.Select`, the host-config probe (`HostFiles`), and the UC4 host-config seed (`SeedFromHost`), wired into the `run` attach path before `session.Run` (not `shell`, `--serve-only`, or `--dry-run-vm`); the decision is recorded in `state.RootState` keyed by `<repo slug>/<agent>`.
- `docslink/` — Composes links to the published docs site (`BaseURL`, section anchors) for the doctor and build-error `Docs:` hints.
- `git/` — Git helpers (go-git wrapper) and project identity: the repo slug (origin-derived; per-slug config dir, grouping label) vs the checkout slug (git common dir; state/VM/volume/locks/image tag).
- `hostlock/` — Advisory `flock` file locks, shared by the state transactions and the per-project VM lock.
- `notify/` — Desktop notifications (backend, dedup, events, watch); `notifytest/` — its test double.
- `opener/` — Host link-opener channel: watches the home volume's spool for in-VM `xdg-open` requests, allowlist-checks the target, and hands it to the host handler.
- `termio/` — Terminal UI: role interfaces (`Logger`/`Output`/`Prompter`/`SpinnerFactory`/`TerminalState`/`Streams`), printer, table, spinner, ansi, levels; `termiotest/` — its test double; and the terminal-reset emit (`ResetTerminal`/`TerminalResetSequence`), shared by the agent attach and the startup-hook attach.
- `upgrade/` — Self-upgrade (channel selection, Homebrew).
- `projectguard/` — Leaf holding, for one command, the digests of the approved project files; `ReadFile` verifies every project read against them and records a violation the image build and VM stage stop on.
- `projecttrust/` — Project config trust gate: the `.agents-sandbox` layer's projection (`Compute`), the decision against the per-checkout approval in `RootState` (`Evaluate`), the approval prompt/refusal, and the launcher-write approval (`ApproveSettingWrite`); wired into the root command's `PersistentPreRunE` (`cmd/agents-sandbox/trust.go`).
- `viperconfig/` — Viper-based launcher config: `Config`/`Resolver`, layer discovery (`ConfigLayers`, the six-layer precedence list), env/flag binding, the derived config-key registry, decode hooks, and validation.
- `runoptions/` — `*cobra.Command`-driven run/shell option resolution (`Build`) into `options.RunOptions`.
- `cliout/` + `view/` — Output rendering: `cliout` is domain-free (generic list/JSON plus `RenderPruneReport`); `view` holds the sandbox-shaped printers and report DTOs (see `specs/architecture/package-boundaries.md`).
- `sandbox/` — **See [sandbox subindex](sandbox.md)** (VM lifecycle, docker, image, msb, volumes, pruning, sessions, state).
- Leaf utilities — `atomicwrite` (durable state/config writes), `configmerge`, `filewalk`, `globmatch` (snippet glob name matching, shared by the agent profiles and `configmerge`), `hostfigures` (the installed-RAM/CPU-count/available-memory seam `vm`/`prepare`/`session` read, with the hermetic sentinel in its `hostfigurestest`), `humanize`, `sysinfo`, `yamlfmt`.
- `testutil/` — Test-only helpers: the git-isolation environment (`GitIsolationEnvironment`, `RunGit`, `ConfigureRepo`), `InitRepo`, `WritePath`/`WriteFile`, the fail-fast mock installers, and the module-wide contract scans. `testgit.go` is the only coverage-ignored file.
