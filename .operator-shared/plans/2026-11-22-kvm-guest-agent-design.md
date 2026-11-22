# KVM-guest agent environment — design

> Status: design, awaiting approval. Not yet implemented. When built, the durable how-to folds into
> `.operator-shared/guides/kvm-guest.md` and this design is retired.

## Intent and why

Let a coding agent (opencode2) work on **agents-sandbox itself from inside the KVM guest**, so it can do the work the
development VM structurally cannot: run the `msb`-tagged suites (`make test-msb`, `make test-msb-journeys`), build the
launcher, and drive real microsandbox VMs — the real app the suites exercise.

The development VM is itself a microsandbox sandbox, so it has no working `/dev/kvm` and no agent there can start nested
VMs. #793 (Phase 0) already builds the one machine that can: a libvirt guest whose `/dev/kvm` is the nested one, where
the launcher runs as a host session and the VMs it creates are nested one level below
(`.operator-shared/guides/kvm-guest.md`). But that guest is provisioned for **scripts**, not for an **agent**: it has no
opencode2, no opencode2 config, no model credentials, and no interactive launch path. `make kvm-ssh` gives a bare shell;
`make kvm-run` runs a non-interactive command.

This design closes that gap: a guest that is ready for agent work, and one command to start the agent on it.

## Goals

- **G1 — the guest is ready.** `make kvm-up` provisions a guest that can work on this repo: opencode2 installed and on
  PATH, the repo's day-to-day gate toolchain present (`golangci-lint`, `shellcheck`), plus the base it already has
  (Go, docker, `msb`).
- **G2 — one command starts a session.** An interactive opencode2 session on the guest, in the synced checkout, with the
  repository's own agent config (root `opencode.jsonc`, `AGENTS.md`, `.agents/skills/`, the `.operator-shared/` brain) in
  effect because the working directory is the checkout.
- **G3 — the session is configured exactly as a sandbox would be.** The guest's opencode2 config is not a hand-picked
  copy: it is **the launcher's own provisioned config** — the merged snippet document, the verbatim mirror, the host
  drop-in, and the home files — materialized by the launcher and applied to the guest home. If the launcher's config
  pipeline changes, the guest's config changes with it.
- **G4 — the session is usable and durable.** It has model API credentials (supplied as environment variables on the
  make command line), and it keeps running if the SSH connection drops; re-running the command reattaches.

## Non-goals

- **No GitHub/PR delivery in v1.** Whether the guest agent pushes branches or opens PRs is undecided; v1 does not wire
  a git or GitHub credential. Work leaves the guest by `git fetch` over SSH (G4/`Decisions`).
- **No general secret-delivery mechanism.** Model credentials are passed as environment variables by the user on the
  make command line (see `Model credentials`); the launcher's proxy-based secret substitution is not reproduced on the
  guest.
- **No new launcher surface beyond config materialization.** The one launcher addition is the command that
  materializes an agent's provisioned config into a host directory (G3); no `run`/lifecycle behavior changes.
- **No guest daemon / remote attach.** Running `opencode2 serve` on the guest and attaching a client over a tunnel is
  out of v1 (see Approaches).
- **No other agents.** v1 is opencode2 only, matching #793's v1 stance.
- **No change to how the suites run.** `make kvm-run` and the `msb` suites are untouched.

## Anti-goals

- A guest that needs undocumented manual setup each time. "Prepared" means `make kvm-up` is enough.
- Duplicating the launcher's config pipeline in bash. A hand-rolled merge that silently diverges from what the launcher
  produces is the failure this design exists to avoid (G3).
- Treating the guest as a product environment. It is deliberately **not** an agents-sandbox sandbox and must stay a host.

## Constraints

- **Host-only.** Needs libvirt + nested virtualization; cannot run in the dev VM; never runs in CI. `make compile-all` /
  the fixture harness cover what can be covered without a hypervisor.
- **One-shot provisioning.** The guest is a cloud-image domain seeded by cloud-init; it provisions on first boot and is
  destroyed by `make kvm-down`, so config seeding must be re-runnable.
- **Reuse the existing shape.** Extend `scripts/kvm-guest.sh` and its fixture harness `test/scripts/kvm_guest_test.go`
  (faked `virsh`/`ssh`/`rsync`/`docker`); do not start a parallel provisioner.
- **Pins come from the repo.** Go from `.go-version` (already), `golangci-lint` from the `Makefile`, matching
  `.agents-sandbox/Dockerfile`'s install method.

## Approaches

Three ways to get an agent working on the guest, and one for its config.

**Running the agent:**

1. **Attach over SSH, inside a guest `tmux` session (recommended).** Smallest surface; reuses everything #793 built; the
   agent's tools run where KVM works; a dropped connection leaves the agent running.
2. **Guest-side daemon + client attach.** `opencode2 serve` on the guest, client over a forwarded port with basic auth.
   Cleaner reconnects and a client that can run elsewhere, but adds a supervised daemon, a tunnel, and an auth story the
   existing probes and recipes do not assume. Deferred.

**Getting the config in:**

3. **Reproduce the launcher's config pipeline (chosen).** A launcher command materializes the agent's provisioned config
   set into a directory; the KVM tooling applies it to the guest home. Faithful by construction (G3). The launcher
   already computes the entire set — merged document, mirror, drop-in, home files, modes — as `reprovision.ConfigFiles`
   via `LoadConfigFilesForHost`; only the writer is VM-bound (`sb.FS()`), so a host-target writer reuses the computation
   verbatim. Chosen by the requester.
4. **Raw copy of the host's `~/.config/opencode/`.** Rejected: it silently misses everything the launcher merges from
   `~/.config/agents-sandbox/opencode2/`, which is where a launcher user's real provider/model config lives.

**Decision: run via approach 1, seed config via approach 3.**

## The how (as decided)

### Guest provisioning (cloud-init in `write_seed_files`)

- Add `tmux` to the guest packages.
- Add the gate toolchain the repo's `make check` needs: `shellcheck` (apt) and `golangci-lint` at the
  `Makefile`-pinned version, installed the same way `.agents-sandbox/Dockerfile` does. `gh` is left out while GitHub
  work is undecided; zig stays out (release-only).
- `runcmd`: install opencode2 from the v2 installer (`curl -fsSL https://opencode.ai/v2/install | bash`), with an
  optional `OPENCODE2_VERSION` pin (default: latest), and put it on PATH via `~/.local/bin` (already on PATH through
  `/etc/profile.d/zz-asb-kvm.sh`) under both `opencode` and `opencode2` names.

### Config materialization (new launcher command + `agent-provision` step)

- The launcher gains a command that materializes an agent's provisioned config for a **target home directory** — the
  same `reprovision.ConfigFiles` the sandbox receives (merged snippets, mirror, drop-in, home files, modes), written
  under the target root by mapping each VM-absolute path. It reuses `LoadConfigFilesForHost` so there is one config
  pipeline, not two.
- `kvm-agent-provision` rsyncs the host's `~/.config/agents-sandbox/` into the guest (so the guest resolves the same
  scopes) and runs that command **in the guest** against the guest home. Decided during implementation: the KVM host may
  not carry the Go toolchain (development happens in the dev VM, the guest is the machine with the toolchain), so the
  materialization runs where the toolchain is. The cost is that the host launcher config — which may carry `env.secret` —
  is synced into the guest; acceptable for a same-trust-domain dev guest, and revisitable.
- Re-runnable after a `kvm-down`/`kvm-up`, and after a `kvm-sync` (which the guest needs for the command itself).

### Model credentials (env vars, spiked first)

The launcher's normal path substitutes secrets through the sandbox proxy, which does not exist for a guest-local
opencode2 process — so a reproduced config that carries a `${ENVVAR}` placeholder is not by itself usable. v1 instead
takes model credentials as **environment variables the user passes through the make command**, e.g.
`make kvm-run KVM_RUN="LITELLM_API_KEY=… GITHUB_PAT=… opencode"`, which the guest's opencode2 reads. A literal key
placed in the guest config is the alternative. This is the acknowledged gap, handled by an explicit user-supplied
channel rather than a general mechanism.

- **Spike before building.** Validate the channel with a throwaway `make kvm-run` invocation: prove the env var reaches
  the guest's opencode2 and a model turn completes. The spike teaches us whether the env-var channel is enough; its
  output feeds back into this design. It is throwaway — keeping any of it is a new request.
- **Known costs.** An inline value is visible in the host's process list, shell history, and the SSH command line.
  Acceptable for a same-trust-domain dev guest and a spike; not a general mechanism.
- **A TUI needs an SSH TTY, which the run path does not allocate today** (`run_in` is non-interactive for the suites).
  The spike either drives opencode's non-interactive `opencode run`, or the run path gains a TTY — the design's `agent`
  subcommand.
- No GitHub credential is wired (undecided scope).

### Git transport

- The guest checkout is a **real git checkout the agent owns**: it commits and creates worktrees there.
- `make kvm-sync` (one-way, `--delete`, excludes `.git/`) must **refuse to run over a live agent session**; `kvm-agent`
  warns when the guest tree has diverged from the host's synced state.
- Work returns to the host with `git fetch` over SSH (documented in the guide). No automatic sync-back, no push.

### Guest instructions (settled after review)

`AGENTS.md` and the `.operator-shared/` brain describe the reader as being **inside an agents-sandbox VM**, with
`/workspace/main` a bind mount of a bare checkout and a rule against committing on `main`. On the guest none of that
holds: it is a plain clone at `~/agents-sandbox`, launched directly, not through the launcher. Since G2 deliberately has
the agent read the repo's own config, it will read those instructions and act on them wrongly. `agent-config` therefore
seeds a small **guest-specific instruction file** into the guest's opencode config directory stating where the agent
actually is: a KVM guest, the checkout at `~/agents-sandbox`, no sandbox, `/dev/kvm` works, work in a worktree. The
repository's tracked `AGENTS.md` is not modified.

### Launch (`agent` subcommand, `make kvm-agent`)

- `ssh -tt` into the guest and run the agent inside `tmux new -A -s agent`, in `~/agents-sandbox`, so a dropped
  connection leaves the agent running and re-running reattaches.
- The agent creates its own worktrees per `AGENTS.md`.

### Tests, docs, changelog

- Extend `test/scripts/kvm_guest_test.go`: the rendered seed contains the opencode2 install line, `tmux`, and the
  `golangci-lint` pin; the `agent` and `agent-config` subcommands issue the expected faked `ssh`/`rsync` calls; the
  `agent` SSH command allocates a TTY and names the tmux session; `kvm-sync` refuses over a live session.
- Unit-test the new launcher command against a temp home (no VM): the materialized tree equals `LoadConfigFilesForHost`'s
  output for a seeded launcher config.
- Update `.operator-shared/guides/kvm-guest.md` with the agent workflow; update `CONTRIBUTING.md`'s pointer; add a
  `changelog.d/<PR>.added.md` fragment.

## Left to the builder

- The new command's name, flags, and whether it is hidden.
- The exact credential opt-in shape (env var vs flag) and how loudly it warns.
- Whether `kvm-agent-config` is a distinct command or folded into `kvm-agent`.
- Whether the launch step syncs the checkout automatically or requires `make kvm-sync` first.
- Whether the operator helper belongs in the guest toolchain.
