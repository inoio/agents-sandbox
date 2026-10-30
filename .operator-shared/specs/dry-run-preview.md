---
description: Contract making the user-facing `--dry-run` a true make-no-changes preview of a run/shell: it inspects the existing runner image and home volume and reports would-build/would-reuse, but on the preview path performs no image build, volume creation, prefill, state write, VM start, attach, or network request. Complies with issue #228; `--dry-run-vm` remains the KVM-less "prepare everything, skip the VM" flag.
read_if: Changing `--dry-run`/`--dry-run-vm` behavior, the `options.Mode`/`runoptions.ResolveMode` boundary, the run/shell preview, `image.EnsureImageWithClient`'s build decision, or the `agentLatestVersion` test seam.
---

# `--dry-run` preview

Status: implemented.

Closes [issue #228](https://github.com/inoio/agents-sandbox/issues/228). Supersedes the "`--dry-run` is side-effect-free"
clause in [upgrade-state-scoping.md](upgrade-state-scoping.md) (which held only for the upgrade check) and the
"documentation-only" resolution in [project-state-transactions.md](project-state-transactions.md).

## Problem

`--dry-run` is documented as "Validate setup without running the agent" (`docs/commands.md`), and
`upgrade-state-scoping.md` states it is side-effect-free. It is not. `session.Run`/`session.Shell` call
`PrepareSandbox` and only early-return **after** it completes (`internal/sandbox/session/run.go:132`, `:209`), so the
whole preparation runs. The parts that *are* suppressed are the upgrade network check and legacy fold
(`vm/run_orchestrate.go:221`), the legacy home-migration offer (`:321`), the chosen home action
(`volume/volume.go:113`,`:123`), and VM start/attach. Everything else runs under `--dry-run`:

- `image.EnsureImage` builds the runner image when `buildDecision` says so (`internal/sandbox/image/build.go:407`).
  `Build` documents that "dry-run handling [is] the caller's concern" and no caller handles it.
- `EnsureNewHome` calls `CreateVolume`, prefills, and writes state (`internal/sandbox/volume/state.go:157`,`:176`).

Two further leaks reach the network under `--dry-run`:

- `resolveBaseDigest` (`image/build.go:617`) pulls the base image when a build runs.
- `resolveAgentVersion` (`image/fetch.go:13`) hits the agent's `LatestVersion` when the resolved version is empty —
  which is exactly what `determineAgentVersion` returns on a first run (`vm/run_orchestrate.go:221-227`).

The two flags`--dry-run` (user-facing: "tell me what this would do") and `--dry-run-vm` (KVM-less: "prepare
everything, skip the VM") only diverge at the four suppression sites above. The user-facing flag is the one that
promises a preview and still mutates.

## Decision

`--dry-run` becomes a **read-only plan**: it runs the pure planning and read-only inspection steps and reports the
decision, then returns before any mutation. It subsumes `--dry-run-vm`: `runoptions.ResolveMode` maps the flag pair to a
single `options.Mode`, and `ModePreview` skips the VM (`Mode.SkipsVM`) exactly as `ModeSkipVM` does.

`--dry-run-vm` is unchanged: it still prepares fully (image ensure/build, home volume ensure/create, state writes,
reconfig) and only skips the VM lifecycle. That is its purpose — running the tool on machines without `/dev/kvm`.
One exception: a project with no home volume gets none under `--dry-run-vm`. Creating a fresh home needs the prefill
VM, and recording an unprefilled volume would make every later run reuse an empty home, so the fresh volume and its
state are left to the first run with a VM (#429). An existing home volume is still reused and its state updated.

### The preview (contract)

Under `--dry-run`, after resolving the workspace bind layout and looking up the agent (both pure), the pipeline
reports and returns, performing **none** of: image build, base-image pull, agent-version network resolution, volume
creation, volume prefill, home-state write, config-hash persist, upgrade prompt, migration offer, VM create/start,
daemon setup, attach.

It reports, via `termio`:

1. **Agent version.** The explicit pin, else the release the project's existing image reports through its
   `agent-version` label, else the recorded `agent_version` cache; when none, a message that the version would be
   resolved at build time. Never a network call. The label is the authority for a present image and the record follows
   it ([agent-version-sync.md](agent-version-sync.md)), so a stale recorded value must not be reported. A user-provided
   agent (recorded `agent_source` is `user`) takes the recorded version and ignores the pin, exactly as
   `resolveBuildVersion` does — a real run would never rebuild such an agent to the pin, so the preview must not report
   the resulting phantom rebuild.
2. **Image.** Render the build plan (pure: `RenderDockerfile` plus the `dockerfileID`/identity) and run the existing
   build decision (local `docker inspect` only). Report `would build runner image <tag> (<reasons>)` or
   `would reuse runner image <tag>`. The reasons are the same ones a real build logs. For a non-user-provided agent
   with no pin, the preview reads the baked version from the project's existing image label via
   `image.ExistingAgentVersion` (a local `docker inspect`, no network) and uses it ahead of the recorded cache, mirroring
   `resolveBuildVersion`'s reconciliation: the label is the authority for a present image, so a stale record must not
   drive the reported image decision. The recorded `agent_version` is used only when the image yields nothing. Unlike
   `recoverAgentVersion` (`vm/upgrade.go:315`), the preview does **not** record the reconciled version — it only reports.
   This makes a post-migration project with an unchanged image report `would reuse` rather than always `would build`.
   Only when neither the image nor the record yields a version does the preview report that no version is known — that
   is item 1's deferred-resolution message — and the image decision then likely reports `would build`.
   The preview does not run the upgrade check, so its image decision reflects the known version, not a possible
   upgrade. When a concrete version is known and not pinned — the project's recorded `agent_version` or the
   label-recovered baseline — the agent-version line carries the note `(upgrade check not run)`. An explicit
   `--agent-version` pin, a user-provided agent, and a deferred build-time resolution carry no suffix: a pin is never
   upgraded, a user-provided agent is never upgrade-checked by a real run either, and a deferred resolution knows no
   version to disclose. A user-provided agent likewise never has a version recovered from the image label (the real
   run leaves resolution to the build), so its no-record case reports the deferred-resolution message.
3. **Home volume.** Read the project state and check the recorded volume's existence via `GetVolume` (local msb). When
   state is missing or its recorded volume is gone, consult the live VM's mounted volume read-only
   (`liveHomeVolume`, `GetSandbox` + `ConfigMounts`) so the preview reports `would reuse` for the volume a real run
   would recover, rather than an unfaithful `would create`. Report `would create home volume` (the fresh volume's name
   embeds a timestamp, so the preview prints no name it cannot honour) or `would reuse home volume <name>`. No volume
   is created and no recovered state is written.
4. **Session.** Report `would start agent session` (or the shell equivalent).

The result is carried in a small value (e.g. `vm.Preview{Image, NeedsBuild, Reasons, AgentVersion, HomeVolume, Create}`
plus the session line) so it is asserted as data in unit tests and as the rendered lines in CLI tests.

### Purity boundaries (implementation shape)

- Split the image path so the preview can compute the decision without building: `EnsureImageWithClient`
  (`image/build.go:414`) factors a pure `planRunnerImage` (render + `buildDecision`) from `buildFromPlan` (`:602`).
  The preview calls the planner only; `buildFromPlan` — and therefore `resolveBaseDigest` — is never reached.
- The preview must not call `resolveAgentVersion(ctx, a, "")` (`image/build.go:443`). It passes the pin, or the
  label-reconciled baseline (`image.ExistingAgentVersion`, local) with the recorded `agent_version` as the fallback —
  the same precedence the real run applies for every registered agent — and reports a deferred resolution only when
  neither yields a version.
- Home resolution in the preview is a read-only variant of `ResolveHomeVolume` (`volume/state.go:23`): it reads state
  and probes the recorded volume, and on the recovery path consults `liveHomeVolume` (`vm/run_orchestrate.go:433`,
  also read-only), but never calls `recoverOrCreateHome`/`EnsureNewHome`/`RecoverHomeState` and never writes the
  recovered state.
- The setup hub (`vm.PrepareSandbox`) takes a `skipVM bool` and has no `--dry-run` branch. The mode lives at the
  session/CLI boundary (`options.Mode`, resolved by `runoptions.ResolveMode`): the session enters the hub only for
  `ModeRun`/`ModeSkipVM` and returns the preview before reaching it, so the hub never runs under `--dry-run` and its
  migration offer and version determination are simply never reached. The preview entry point
  (`session.previewAndRender`) is unchanged.
- Project identity is resolved read-only too: the preview runs `AdoptLegacyIdentity` with `apply=false`, so it reports
  against the slug a real run would find first (the legacy slug while a migration is pending) without moving state, and
  an error from that resolution (e.g. a corrupt legacy state document) surfaces rather than being ignored.
- The root preflight provisions no runtime for a preview (#424): `cmd/agents-sandbox/commands.go` checks
  `runoptions.ResolveMode(cmd).Previews()` and, instead of `PrepareRuntime`, only applies the private `MSB_HOME`
  (`runtime.ApplyPreviewHome`, inspect/install skipped). A fresh host thus renders the plan with no download and no
  runtime install, while the preview's read-only msb inspection (volumes, sandboxes) still resolves against the
  private home rather than the user's `~/.microsandbox`. `shell --dry-run` is unaffected: the root preflight installs
  nothing, and shell's own `doctor.CheckAll` preflight is what may still install the runtime.

### Test seam

`vm.agentLatestVersion` (`vm/upgrade.go:21`) is a package var with **no exported setter**, so `cmd` tests cannot stub
it and `image.WithMockAgentVersion` does not cover it (a different var). Once the run/shell CLI tests move to
`--dry-run-vm` they run `resolveBuildVersion`, whose `pendingUpgrade` can call it when a project has a recorded
baseline and the check is due — a real network request. Add `vm.SetAgentLatestVersion` and install a **fail-fast**
default in `cmd`'s `TestMain` (beside the existing migration-offer default, `cmd/agents-sandbox/main_test.go`),
mirroring the isolation discipline: a test that reaches the network path fails loudly and opts in with an explicit
stub, rather than silently bypassing it. This is not an `InstallFailFast*` family, so the `seamFamilies` registry in
`internal/testutil/isolation_contract_test.go` is untouched.

The preview itself needs no new seam: it composes the existing docker (`WithNoopDockerMock`) and msb
(`WithMsbMock`) mocks. Its purity is pinned by tests that install a resolver/`agentLatestVersion` which fails the test
if called, and by asserting `CreateVolume`/`ImageBuild` are absent from the mock call history.

### CLI test migration

The run/shell CLI tests currently express "prepare everything, skip the VM" through `--dry-run`
(`cli_run_shell_test.go:100`,`:133`; `cli_bare_checkout_test.go:76`; `cli_test.go:64`,`:80`). That is `--dry-run-vm`'s
contract, reached only transitively today. Move them to `--dry-run-vm`, which is required rather than cosmetic:
`TestRunCommandReadsBareRoot` asserts the ignored-worktree warning emitted **inside** `EnsureImage`, which a pure
`--dry-run` would no longer emit. Rewrite the two assertions that change (`run`'s "Would start agent session in VM"
vs the old "Would run agent session"; drop the "dry-run-vm: auto-enabled" verbose expectation) and keep a dedicated
set of tests for the new preview.

### `volume <name> --dry-run`

`volume migrate/reset/edit --dry-run` carries the same class of defect: `commands_system.go:126` calls
`image.EnsureImage` (which can build) before the `volumeOp` dry-run early-return (`volume/operations.go:216`), even
though the dry-run message needs only the state-derived old volume, not `info`, and `volumeOp` already tolerates a
zero `ImageInfo`. **In scope**: under `--dry-run` skip the image ensure, so the volume ops honour the same flag
contract. Tests cover that `--dry-run` performs no `ImageInspect`/`ImageBuild` and still reports the would-do message.

## Non-goals

- No change to `--dry-run-vm` behavior.
- No change to a real run.
- No change to what prune/stop/kill/`image build`/`build dockerfile` `--dry-run` print or report: they are already pure
  previews with their own local flags. They do share the preview-mode preflight change above (#424) — none of them
  provisions the runtime — since they all reach the same root preflight.
- No change to what a real build decides (same `buildDecision`, same reasons).
- No new user flag and no `--dry-run-vm` deprecation.

## Anti-goals

- A preview that still builds, creates a volume, or writes state.
- A preview that reads the network (version resolution, base-image pull).
- Weakening the preview into a bare "would do a thing" with no inspection — it must still say would-build vs
  would-reuse, with reasons.
- Coupling `--dry-run` and `--dry-run-vm` so that fixing one breaks the other's purpose.

## Acceptance

- On a fresh project, `run --dry-run` creates no volume, builds no image, writes no state, starts no VM, and makes no
  network request, reporting would-build and would-create decisions; `shell --dry-run` previews the same way, but its
  preflight still runs the prerequisite check (`doctor.CheckAll`), which may install the microsandbox runtime.
- On a project with an unchanged image and an existing home volume, they report would-reuse for both.
- `--dry-run-vm` on the same project still prepares fully (image ensured, volume ensured, state written) and skips
  only the VM.
- `--dry-run-vm` on a project without a home volume creates no volume and writes no state; the next real run creates
  and prefills the home (#429).
- CLI tests exercise the prepare path through `--dry-run-vm`; dedicated tests pin the `--dry-run` preview and its
  no-side-effect/no-network guarantees.
- `volume migrate/reset/edit --dry-run` inspects/builds no image and performs no volume/state mutation, while still
  reporting its would-do message.
- `make check` and `make compile-all` green.

## Docs and changelog

- `docs/commands.md`: the `--dry-run` row states the preview and what it reports; the `--dry-run-vm` row keeps its
  "skip VM lifecycle but prepare everything else" wording.
- `specs/upgrade-state-scoping.md`: its "`--dry-run` is side-effect-free" clause is superseded by this spec; narrow it
  to the upgrade check it was written for and point here.
- `specs/project-state-transactions.md`: replace the "documentation-only, tracked in #228" resolution with a pointer
  to this spec.
- `CHANGELOG.md` `[Unreleased]`: `--dry-run` no longer builds an image, creates a home volume, or writes state (and no
  longer resolves a version over the network).
