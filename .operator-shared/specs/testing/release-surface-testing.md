---
description: The release surface sweep - an agent-executable, mostly-isolated runbook that exercises the user-facing journeys the automated tiers (cli_*_test.go, unit, integration, test-msb) do not, ordered by release-risk, plus the maintainer-run real-state section.
read_if: Verifying a release before tagging; running the surface sweep; adding or changing a surface journey; building the sweep's isolation helper.
---

# Release surface testing

> **For agentic workers:** This is an executable runbook, not code. Tasks are journeys. Each step has a command (or a
> `tmux` interaction), an expected outcome, and an evidence command. Track progress with `- [ ]`. Execution method is
> **H**: a serial spine (Task 0 + Task 2) warms a shared image cache, then Tasks 3–14 are dispatched to parallel journey
> agents under one coordinator; Task 18 is the coordinator's consolidation; Tasks 1 and 15–17 are maintainer-run. Steps
> the coordinator and subagents run are labelled **[I]**; maintainer-run steps are **[M]**.
>
> **Execution notes (learned the hard way):**
> - Drive `tmux` on a **private socket** (`tmux -L <journey>`), never the default one: a `tmux kill-server` on the
>   default socket kills sibling journey agents mid-run.
> - The isolation root must be **`0700`** (the helper's `init` does this): microsandbox refuses a data home under an
>   ancestor another account could rename.
> - The final sweep removes **both sandboxes and home volumes**, and `volume remove` needs `--yes` (a bare `--force`
>   still prompts and aborts non-interactively).

**Goal:** Before tagging the next release, give the user-facing surface a release-confidence pass — cross-command
journeys, real end-to-end behaviour the doubles stand in for, and UX judgment — without mutating the maintainer's real
state (except the deliberate **[M]** section).

**Architecture:** Model A isolation. One shared, standing root under a short `/tmp/asb-*` base holds one microsandbox
data home (the image cache and every sandbox record) plus one `runs/<journey>/` tree per journey with its own
`HOME`/`XDG_CONFIG_HOME`/`XDG_STATE_HOME`/`XDG_CACHE_HOME`/`TMPDIR` and throwaway project dir. A serial spine pays the
image build/transfer once; parallel journey agents share the warm data home. The coordinator owns the root and sweeps
once at the end; no parallel agent sweeps.

**Tech Stack:** `asb` (installed HEAD) and `asb-dev` (`go run ./cmd/agents-sandbox`); `tmux` for driving and capturing
interactive screens; `script(1)` for a session typescript; `jq`/`yq`, `curl`/`nc`; `git`, `docker`.

## Global constraints

- **Candidate**: `main` HEAD, cut as the next minor. Delta base `v0.6.0` (`git diff v0.6.0..main`).
- **Binary under test**: `make install-head` for `asb`; `go run ./cmd/agents-sandbox` for `asb-dev`. Use `asb` for
  install/upgrade journeys, `asb-dev` for the rest unless a step says otherwise.
- **Isolation A**: every `[I]` step runs under the sweep env from Task 0. A step that would touch
  `~/.config/agents-sandbox`, `~/.local/{share,state}/agents-sandbox`, `~/.cache/agents-sandbox`, or `~/.microsandbox`
  is **not** an `[I]` step.
- **No credentials, no real egress for agent turns.** Agent/model wiring is verified against a loopback/dummy provider;
  a session must start and accept a prompt, not produce a real answer.
- **Anti-goal (fails the whole run):** mutating or deleting a real home volume, sandbox, config file, or root state.
- **Config-file shapes are owned by `docs/`**: each journey gives the minimal file and links its page; do not invent
  keys. `docs/commands.md` is the flag reference.
- **Evidence is required**: every check leaves captured pane text, command output, or a typescript under the run dir.

## Review focus

Failure modes the surface implies; each is pinned by a check in the task named.

1. **Non-TTY behaviour** — every interactive prompt (project-config trust, home-volume choice, reconfig keep/quit,
   `prune` confirm, upgrade offer) must fail fast or warn with a clear exit, never hang, when stdin is not a terminal.
   → Tasks 2, 3, 12, 13, 15.
2. **Idempotence** — a second run must reuse the image, the home volume, and the VM, and must not re-ask an already
   approved project config or re-trigger a fingerprint recreate. → Tasks 3, 4, 5, 11.
3. **`--dry-run` contract** — no image build, volume, VM, state/config write, or network request; the report is the plan.
   → Tasks 2, 3.
4. **Error surfacing** — a failure names the fix (doctor remedy + `Docs:` line, build-error troubleshooting hint,
   registry `unauthorized` pointer) instead of a bare error. → Tasks 2, 10.
5. **Terminal hygiene** — after any abnormal attach end no alternate screen or mouse mode is left on; piped `list`
   output degrades to plain text with no colour codes. → Tasks 11, 12.

---

## Sweep layout

- **Root**: a standing path, e.g. `/tmp/asb-surface` (must satisfy the microsandbox `sun_path` budget — see Task 0).
  Holds `data/` (the shared `XDG_DATA_HOME`) and `runs/<journey>/`.
- **Run env** (`<root>/runs/<journey>`): `home/`, `config/`, `state/`, `cache/`, `tmp/`, `project/`.
- **Findings**: `<run>/findings.md` per journey, one per finding; the coordinator copies them into the report.
- **Report**: `<root>/report.md`, throwaway, not committed.
- **Lock**: the coordinator holds `<root>.lock` for the whole run via `surface-sweep.sh lock` (an atomic `mkdir`, portable
  to macOS, where `flock(1)` is absent); `surface-sweep.sh unlock` releases it. A crashed coordinator leaves the
  directory behind — remove it once no coordinator is running.

## Task 0 [I]: Sweep scaffolding (isolation helper + spine root)

**Files:**
- `scripts/surface-sweep.sh` — the isolation helper (committed; follows `test/harness/harness.go`'s isolation intent —
  the `rootFitsSandboxSocket` budget check and dropping the ambient `HOME`/`XDG_*`/`AGENTS_SANDBOX_*`/`OPENCODE_SANDBOX_*`/
  `MSB_*` — but is sourced, not imported; that package is `msb`-tagged test-only. Unlike `isolatedEnv`'s strict
  allow-list it keeps the caller's wider shell env (`PATH`/`TERM`/`tmux`/…), because its `env` snippet is sourced into an
  interactive journey shell).
- `scripts/test-surface-sweep.sh` — its tests, runnable without a VM or a real `asb`.

**Interfaces:**
- `surface-sweep.sh init` → prints the root path, creating `<root>/{data,runs}`, `chmod 700`-ing the root, and
  validating the socket-path budget (`len("<root>/data/agents-sandbox/msb") + 50 < 104`); the budget is re-checked for
  every subcommand, and a relative `ASB_SURFACE_ROOT` is resolved absolute first.
- `surface-sweep.sh env <journey>` → creates `<root>/runs/<journey>/{home,config,state,cache,tmp,project}` and prints
  shell `export` lines for `HOME`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME` (the shared `data/`), `XDG_STATE_HOME`,
  `XDG_CACHE_HOME`, `TMPDIR`, prefixed by `unset`s of the ambient `AGENTS_SANDBOX_*`/`OPENCODE_SANDBOX_*`/`MSB_*`;
  `PATH`/`TERM`/etc. are inherited from the caller.
- `surface-sweep.sh sweep` → `sandbox list -q` / `sandbox remove --force --yes` and `volume list -q` / `volume remove
  --force --yes` over the shared data home.
- `surface-sweep.sh lock` / `unlock` → take/release `<root>.lock` (an atomic `mkdir`/`rmdir`, portable to macOS where
  `flock(1)` is absent) for the whole run.

- [ ] **Step 1 [I]: Create the spine root and acquire the lock.**
  `bash scripts/surface-sweep.sh lock; bash scripts/surface-sweep.sh init`. Expected: prints the lock directory then
  `/tmp/asb-surface`; the lock holds for the whole run, so a second coordinator sharing `/tmp/asb-surface` is excluded
  from sweeping the first's live sandboxes/volumes. Record the root in every journey's environment.
- [ ] **Step 2 [I]: Warm the cache (spine).** Run Task 2 (core first run) first, serially, so the BuildKit build and the
  multi-GB image transfer are paid once in the shared `data/` home before Tasks 3–14 fan out.

## Task 1 [M]: Real-state section — safety net

- [ ] Back up the state path set before any Task 16/17 step, so the section is reversible:
  `tar -C ~ -czf /tmp/asb-realstate-backup.tgz .config/agents-sandbox .local/share/agents-sandbox
  .local/state/agents-sandbox .microsandbox`. Restore with `tar -C ~ -xzf …` after. (`~/.microsandbox` is only read by
  the migration journey, but include it: it is inside the no-mutation anti-goal.)

## Task 2 [I]: Read-only sweep and core first run (spine)

Proves the command surface drifts nothing, then the launcher's primary path and the `--dry-run` contract end to end.
Config shapes are owned by `docs/get-started.md`/`docs/sandboxes.md`.

- [ ] **Step 1: Read-only surface.** `asb --help`, `asb help run`, `asb tree`, `asb version`, `asb completion
  bash|zsh|fish|powershell`, `asb config paths`, `asb config agent opencode2`, `asb config home`, `asb list`,
  `asb image list`, `asb volume list`, `asb df`. Expected: every command exits 0 and its output matches `docs/commands.md`
  (flags, aliases, columns); a deprecated form (`--names`, `--age`, `--opencode-version`, `--dind`) is honored with a
  warning. Evidence: captured output. (Design coverage item 1.)
- [ ] **Step 2: Doctor.** `asb doctor`. Expected: each prerequisite reported; on a healthy host all pass. Evidence:
  capture stdout.
- [ ] **Step 2: `--dry-run` writes nothing.**
  `snapshot_before=$(find "$HOME" "$XDG_DATA_HOME" -newer /dev/null | sort)`; then `asb --dry-run`; then diff before/after
  and confirm no state/config/volume/VM change and (via `strace -f -e trace=network` or a proxy check) no network
  request. Expected: the report names the agent version (or "resolves at build time"), whether the image builds or is
  reused, and whether the home volume is created or reused; nothing is created.
- [ ] **Step 3: First run.** Under `tmux`: `tmux new-session -d -s run 'asb'`, then `tmux capture-pane -p -t run`.
  Expected: the BuildKit image build, VM creation, home-volume creation, then opencode2's TUI. Evidence: pane text.
- [ ] **Step 4: Reach and leave the TUI.** Confirm the interactive screen is up; `tmux send-keys -t run C-d`; confirm the
  process exits and the session ends. Expected: clean exit; no daemon-timeout text in the pane.
- [ ] **Step 5: Second run reuses.** `asb` again. Expected: no rebuild, existing image/volume/VM reused, TUI within
  seconds. Evidence: pane text lacks a build log.
- [ ] **Step 6: `shell` and `shell --root`.** `asb shell` then `id`; `asb shell --root` then `id`. Expected: `dev` and
  `root` respectively; sandbox ready. Evidence: commands' output.
- [ ] **Step 7: `stop`.** `asb stop`; `asb list`. Expected: sandbox `stopped`; state kept.
- [ ] **Step 8: Home-volume choice.** Change `.agents-sandbox/Dockerfile` so the image digest changes; run. Expected: the
  `keep`/`reset`/`migrate` prompt is shown; non-interactively the existing volume is kept and the image change is
  reported (no hang) (Review focus 1). Acceptance executes the choice without updating the state file early.
- [ ] **Step 9: Findings** → `<run>/findings.md`.

## Task 3 [I]: Config and the project-config trust gate

Proves #604/#608/#229: a changed project config is shown, approved once, and enforced. Shapes: `docs/manage-config.md`,
`docs/configuration/launcher.md`, `docs/configuration/agent.md`.

- [ ] **Step 1: Seed a project.** In `<run>/project`, `git init`; write `.agents-sandbox/config.yaml` (e.g. `log-level:
  verbose`, `network.profile: [public]`), `.agents-sandbox/env` (`FOO=bar`), a `home:` entry, and an agent snippet.
- [ ] **Step 2: First run shows the change.** `asb --dry-run` and `asb`. Expected: the launcher shows what the project
  config sets and asks to approve it for this checkout before starting. Evidence: pane text.
- [ ] **Step 3: Refuse.** Answer no. Expected: the run stops or runs without the project layer, and `config paths` still
  shows the project file as present-but-unapproved.
- [ ] **Step 4: Approve.** Re-run and approve. Expected: the run proceeds; `config paths` and `config agent` reflect the
  approved layers.
- [ ] **Step 5: Re-run is a no-op.** Run again. Expected: **no** re-prompt (Review focus 2).
- [ ] **Step 6: Edit re-arms.** Append a byte to `.agents-sandbox/config.yaml`; run. Expected: the change is shown again
  and re-approval is required (#608).
- [ ] **Step 7: Non-TTY refusal.** `printf '' | asb 2>&1; echo "exit=$?"`. Expected: refuses to start, clear exit, no
  hang (Review focus 1). `--yes` must **not** approve: `asb --yes` non-interactively also refuses to use the changed
  project config.
- [ ] **Step 8: Per-slug user config.** Create `~/.config/agents-sandbox/<repo-slug>/config.yaml` (repo slug from
  `asb config paths`) with a distinguishing key; `asb config paths`. Expected: the per-slug layer merges between generic
  user and project config (#580).
- [ ] **Step 9: Findings.**

## Task 4 [I]: Secrets

Proves placeholder substitution, `hosts` scoping, `header_fields`, and `allow-placeholder-for`. Shapes:
`docs/configuration/secrets.md`.

- [ ] **Step 1: Seed secrets.** In `.agents-sandbox/env.secret.yaml`: `DEMO_KEY: {value: sk-demo, host: example.com,
  header_fields: [authorization]}`; an entry with no hosts (expect a drop-with-warning). Top-level
  `secrets.common.allow-placeholder-for: [model.example.com]`.
- [ ] **Step 2: Placeholder in the VM, not the value.** `asb shell` then `env | grep DEMO_KEY`. Expected:
  `DEMO_KEY=$MSB_DEMO_KEY` (placeholder), never `sk-demo`.
- [ ] **Step 3: Substitution only for allowed hosts/headers.** From inside the VM, a request to an allowed host with the
  placeholder in `authorization` is substituted; the same placeholder in another header is not; a request to a
  non-allowed host is blocked with `Request blocked by secret policy`. Evidence: curl output from the VM.
- [ ] **Step 4: Dropped entry warns.** Expected: the no-hosts entry is dropped with a warning naming the key (#576).
- [ ] **Step 5: `header_fields` fingerprint recreate.** Change `header_fields`; run. Expected: the VM is recreated once
  (#499).
- [ ] **Step 6: `allow-placeholder-for` fingerprint recreate + the model-context block.** Change the key; run. Expected:
  recreation once (#484). Then confirm the block message when a placeholder reaches the model endpoint.
- [ ] **Step 7: Findings.**

## Task 5 [I]: Network profiles

Proves profile composition and `profile: none` DNS. Shapes: `docs/configuration/networking.md`.

- [ ] **Step 1: Default `public`.** `asb --dry-run` then `asb --network public`; in `asb shell`, `curl -sS
  https://example.com` succeeds. Expected: public egress.
- [ ] **Step 2: `none` denies and NXDOMAINs.** `asb --network none`; in the shell, `curl -sS https://example.com` is
  denied (the #310 `403`), and `getent hosts example.com` returns nothing (`NXDOMAIN`) (#480).
- [ ] **Step 3: `egress-allow` opens one host.** Set `network.profile: none` + `egress-allow: [example.com]`; run;
  verify `example.com` resolves and connects while another name does not.
- [ ] **Step 4: profile change recreates once.** Change `--network`; run. Expected: one recreate, then stable.
- [ ] **Step 5: `--dns`.** `asb --dns 1.1.1.1`; `asb config paths` unaffected; verify resolution still works. Invalid
  `--dns` is rejected at config-load.
- [ ] **Step 6: Findings.**

## Task 6 [I]: Agent matrix (changed bits)

- [ ] **Step 1: Experimental notice.** `asb --agent pi --dry-run` and `asb --agent codex --dry-run`. Expected: a notice
  that non-`opencode`/`opencode2` agents are experimental (#473).
- [ ] **Step 2: `oh-my-pi`.** `asb --agent oh-my-pi`; reach the editor, with the first-run setup wizard skipped by a
  seeded `~/.omp/agent/config.yml` (`startup.setupWizard: false`). Expected: editor up (#478). Evidence: pane text.
- [ ] **Step 3: Session wiring.** With a loopback/dummy provider (the `test-msb` hermes-style seed), start a session and
  submit a prompt. Expected: the session starts and accepts input; the send path does not hang (no real answer expected).
- [ ] **Step 4: Findings.**

## Task 7 [I]: Worktree / branch sessions

Shapes: `docs/branch-sessions.md`.

- [ ] **Step 1: Create.** `asb -w sweep-wt --dry-run` then for real. Expected: an isolated agent worktree named
  `sweep-wt`; the agent starts there.
- [ ] **Step 2: Reuse.** Run `asb -w sweep-wt` again. Expected: the existing worktree is reused, not recreated.
- [ ] **Step 3: Base ref.** `asb -w sweep-wt2:main`. Expected: the worktree starts from `main`.
- [ ] **Step 4: Outside-worktree identity.** Run from a worktree outside its checkout. Expected: its own identity (state,
  sandbox, volume, image tag) and the shared-git-dir mount; a `git` command resolves inside the VM.
- [ ] **Step 5: Findings.**

## Task 8 [I]: serve-only and desktop

Shapes: `docs/recipes/connect-desktop.md`, `docs/commands.md` (`run`).

- [ ] **Step 1: Extra is authoritative.** `asb -s`; capture the printed `http://127.0.0.1:<port>`. Expected: the URL is
  printed and reachable.
- [ ] **Step 2: Basic auth.** With `OPENCODE_SERVER_PASSWORD` set (opencode), `curl` without credentials is rejected;
  with them it succeeds. For opencode2, the baked `opencode:opencode` credential works.
- [ ] **Step 3: Exit.** `Ctrl-D` in a tty exits; when stdin is not a tty, `SIGINT`/`SIGTERM` exits. Expected: clean exit.
- [ ] **Step 4: Findings.**

## Task 9 [I]: Expose a dev server (published ports)

Shapes: `docs/recipes/expose-a-dev-server.md`, `docs/configuration/networking.md#published-ports`.

- [ ] **Step 1: Publish.** `asb -p 8080:3000`; inside the VM start a server on `0.0.0.0:3000` (`python3 -m http.server
  3000 --bind 0.0.0.0`). Expected: `curl -sS http://127.0.0.1:8080` on the host reaches it (bound to loopback, not LAN).
- [ ] **Step 2: Config sources.** Repeat via `ports:` in config and `AGENTS_SANDBOX_PORTS`. Expected: same result.
- [ ] **Step 3: Change recreates.** Run without `-p` against the VM that has ports. Expected: one recreate that drops the
  ports, with a warning if another client is attached (#485).
- [ ] **Step 4: Invalid rejected.** Duplicate host ports and an out-of-range spec are rejected with an error.
- [ ] **Step 5: Findings.**

## Task 10 [I]: Docker in the sandbox

Shapes: `docs/runner-image.md#docker-in-docker`.

- [ ] **Step 1: Bake rootless.** `asb --docker build -r` (`--docker` overrides the Dockerfile). Expected: build succeeds;
  missing prerequisites (`uidmap`, `slirp4netns`, `iproute2`) for a custom base are named.
- [ ] **Step 1b: Registry errors point at the fix.** A project `FROM` on a private registry builds without a prior
  `docker pull` (credentials from the Docker CLI config); an `unauthorized`/`denied` response points at the
  troubleshooting section (#647). (Review focus 4.)
- [ ] **Step 2: Engine runs rootless as `dev`.** In `asb shell`: `docker info | grep -i rootless`; `id`; attempt
  `docker run --rm alpine mount /` and confirm it does **not** expose root-only files (#624).
- [ ] **Step 3: fuse-overlayfs.** `docker info | grep -i 'storage driver'` shows `fuse-overlayfs` when `/dev/fuse` is
  usable, else `vfs` (#489); `docker build -t t .` and `docker run --rm t` work.
- [ ] **Step 4: Detach warning.** Leave a container running, detach the last client. Expected: a warning that the idle
  stop will stop the containers.
- [ ] **Step 5: Findings.**

## Task 11 [I]: Resource limits and the raise proposals

Shapes: `docs/commands.md` (`run`), `docs/sandboxes.md`.

- [ ] **Step 1: Validation.** `asb --memory 64M` (below the 128 MiB floor) is rejected naming `memory`; `asb --tmp-size
  2G --memory 2G` is rejected (`tmp-size < memory`) (#607); `asb --disk-size 0` is rejected. `asb --memory 2G` with no
  `--tmp-size` now mounts `min(2 GiB, memory/2)` = 1 GiB, not an error (#620).
- [ ] **Step 2: `--dry-run` previews the sizes.** Expected: the report names the memory/disk/quota it would use.
- [ ] **Step 3: Memory OOM raise.** With a small `--memory` (e.g. `256M`), force an OOM inside the VM; after the session,
  the launcher offers to raise `memory` and (accepted) writes it to the owning config layer, preserving format and
  permissions (#554). A non-interactive run records the hit for the next interactive launch. A raise larger than host
  RAM warns (#582).
- [ ] **Step 4: Disk quota/tmp raise + live grow.** Fill `/workspace` past `--workspace-quota`; after the session the
  launcher offers to raise `workspace-quota`/`tmp-size`; a larger `--disk-size` grows the root disk without a rebuild
  (#585).
- [ ] **Step 5: Terminal reset.** After an abnormal attach end, no alternate-screen or mouse residue remains (#544);
  `reset` is unnecessary.
- [ ] **Step 6: Findings.**

## Task 12 [I]: Lifecycle and management

- [ ] **Step 1: Inspect.** `asb list`, `asb sandbox inspect <name>`, `asb list --format json | jq .`. Expected: name,
  status, mounts, resources; JSON parses.
- [ ] **Step 2: Stop/kill.** `asb stop -f`, `asb kill`, `asb kill -f`. Expected: documented state behaviour.
- [ ] **Step 3: Idle stop warning.** After the last detach, the VM stops after `auto-stop-timeout`; with containers
  running a warning is printed.
- [ ] **Step 4: Concurrent attach.** Two `asb` invocations share one VM; the second connects.
- [ ] **Step 5: Reconfig prompt.** With one client attached, change a config key that restarts the daemon/recreates the
  VM; the second run shows a keep/quit prompt listing the changes (mount sources, network, ports, secret hosts, env
  names, changed agent config files, old→new image/volume) and never prints values (#602, #230). With no other client,
  the same list prints as a warning.
- [ ] **Step 6: Findings.**

## Task 13 [I]: Storage and prune

Shapes: `docs/sandboxes.md#pruning`, `docs/commands.md`.

- [ ] **Step 1: `df`.** `asb df`, `asb df --verbose`, `asb df --format json | jq .`. Expected: categories with host paths
  in verbose; `-` for unknown counters, never a fake `0`.
- [ ] **Step 2: Safe tier.** `asb prune --dry-run` then `asb prune --yes`. Expected: removes only sandbox/image/dangling/
  runtime/internals; **never** a home volume; lists retired projects and points at `prune --purge` (#593).
- [ ] **Step 3: Purge.** `asb prune --purge --dry-run` names the plan; `asb prune --purge --yes` removes the project
  (sandbox, volume, image, state, claims). A running sandbox fails fast until `--force`.
- [ ] **Step 4: Non-TTY.** `printf '' | asb prune 2>&1; echo "exit=$?"`. Expected: fails fast (or with `--yes`
  proceeds), no hang; declining returns `termio.ErrCancelled`-style clean exit (#529) (Review focus 1).
- [ ] **Step 5: Sub-prunes and volumes.** `image prune`, `volume prune`, `sandbox prune`; `volume list/inspect`;
  `volume remove` refuses a referenced volume, `--force` overrides; `volume migrate/reset/edit --dry-run` name the plan;
  `sandbox compact --dry-run` reports layer counts (#593).
- [ ] **Step 6: `state repair`.** Corrupt `state.yaml` (isolated), run, confirm the quarantine rename and the fresh-start
  behaviour; a newer-version file is refused.
- [ ] **Step 7: Findings.**

## Task 14 [I]: Links, notifications, mounts, home provisioning

Shapes: `docs/configuration/opening-links.md`, `docs/configuration/notifications.md`,
`docs/configuration/home-provisioning.md`, `docs/configuration/mounts.md`, `docs/recipes/git-access.md`.

- [ ] **Step 1: Open links.** With `--open-links` (default), open a URL from the VM (xdg-open) and confirm the host
  opener channel handles it; `--open-links=false` disables it.
- [ ] **Step 2: Notify.** `--notify on|off|desktop|audio` with an opencode/opencode2 session; confirm the event fires
  (on-input/on-done/on-error) and is suppressed when off. Other agents ignore it.
- [ ] **Step 3: Home provisioning + hook.** A `home:` entry with `hook: startup` runs on VM start only (not on attach),
  interactively, as `dev` or `root: true`; a bad `hook` value is rejected. `asb config home` lists the mapping.
- [ ] **Step 4: Mounts.** A `mounts:` entry appears in `sandbox inspect`; a managed target (`/home/dev`, `/workspace`,
  `/tmp`) is rejected; a missing host source is rejected; a change recreates the VM.
- [ ] **Step 5: Git access wiring.** Against a local/loopback remote (no real SSH host), clone/fetch/push inside the VM.
  Expected: HTTPS works; an SSH remote is only reachable per `docs/recipes/git-access.md`.
- [ ] **Step 6: Findings.**

## Task 15 [M]: Upgrade and self-update

Real state only. Run after Task 1's backup.

- [ ] **Step 1: Automatic prompt.** With an older recorded version, a `run`/`shell` offers the upgrade per
  `upgrade.mode`/`upgrade.interval`; accepting records the check in root state.
- [ ] **Step 2: `upgrade`.** `asb upgrade` downloads, verifies the checksum, and replaces the binary; the new version
  takes effect next invocation. Homebrew-managed install prints `brew update && brew upgrade agents-sandbox` (#286).
- [ ] **Step 3: Channels.** `asb upgrade --channel stable` on a canary install switches to the newest stable even when
  older; without the flag it never downgrades and prints the switch command (#479).
- [ ] **Step 4: `--dry-run`? / state.** Confirm `state.yaml` records the check and the `agent-version` provenance label.
- [ ] **Step 5: Findings.**

## Task 16 [M]: Runtime-home migration

Real state only; `~/.microsandbox` is read, never written.

- [ ] **Step 1: Private home.** Confirm the runtime lives under `$XDG_DATA_HOME/agents-sandbox/msb` and no user-facing
  `msb` is installed.
- [ ] **Step 2: Legacy copy prompt.** With legacy `~/.microsandbox` volumes present, the one-time offer copies them and
  tracks status in root `state.yaml`. Expected: copy once, then quiet.
- [ ] **Step 3: Post-migration notice.** The one-time notice names copied/kept/excluded volumes; when some were not
  copied it prints a removal command per volume (#494, #285). `volume remove` is the cleanup path.
- [ ] **Step 4: Restore.** `tar -C ~ -xzf /tmp/asb-realstate-backup.tgz`.
- [ ] **Step 5: Findings.**

## Task 17 [M]: Bare worktrees

- [ ] **Step 1: Config root.** In a bare checkout (`core.bare` in `config.worktree`), project config is read from the
  bare root next to `.git`, not the launched worktree (#469).
- [ ] **Step 2: Workspace bind.** A worktree nested in the checkout binds the enclosing root; an outside worktree binds
  its own root and mounts its shared git dir. `git status` resolves inside the VM.
- [ ] **Step 3: Findings.**

## Task 18 [I]: Consolidated report

- [ ] **Step 1: Merge.** Collect every `<run>/findings.md`; de-dupe; rank by severity (Blocker = stops the
  release; Major = a documented feature is wrong; Minor = cosmetic, docs/wording, or low-confidence). Write
  `<root>/report.md`.
- [ ] **Step 2: Record the unexercised.** List each surface that could not be exercised and why (no egress, no
  credentials, `[M]` not run, `oh-my-pi` wizard, …).
- [ ] **Step 3: Sweep the shared home.** `surface-sweep.sh sweep` (coordinator only, once, at the very end; it removes
  every sandbox and home volume in the shared data home), then release the lock (`surface-sweep.sh unlock`). Confirm
  `list`/`volume list` are empty and no real artifact was touched.
