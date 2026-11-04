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

`--dry-run` promises a preview ("Validate setup without running the agent", `docs/commands.md`), so it must make no
changes. `session.Run`/`session.Shell` prepare through `prepare.PrepareSandbox`, whose side-effecting steps include
`image.EnsureImage` building the runner image when the plan's build decision says so, `image.resolveBaseDigest` pulling
the base image, the agent-version network resolution (`image.DefaultAgentVersionResolver` when the resolved version is
empty — what `prepare.determineAgentVersion` returns on a first run), `volume.EnsureNewHome` calling `CreateVolume`,
prefilling, and writing state, the chosen home action (`volume.Manager.ApplyHomeAction`), and VM start/attach. The two
flags `--dry-run` (user-facing: "tell me what this would do") and `--dry-run-vm` (KVM-less: "prepare everything, skip
the VM") diverge only at those suppression sites; the user-facing flag is the one that promises a preview and must not
mutate.

## Decision

`--dry-run` is a **read-only plan**: it runs the pure planning and read-only inspection steps and reports the decision,
then returns before any mutation. It subsumes `--dry-run-vm`: `runoptions.ResolveMode` maps the flag pair to a single
`options.Mode`, and `ModePreview` skips the VM (`Mode.SkipsVM`) exactly as `ModeSkipVM` does.

`--dry-run-vm` is unchanged: it still prepares fully (image ensure/build, home volume ensure/create, state writes,
reconfig) and only skips the VM lifecycle. That is its purpose — running the tool on machines without `/dev/kvm`. One
exception: a project with no home volume gets none under `--dry-run-vm`. Creating a fresh home needs the prefill VM, and
recording an unprefilled volume would make every later run reuse an empty home, so the fresh volume and its state are
left to the first run with a VM (#429). An existing home volume is still reused and its state updated.

### The preview (contract)

Under `--dry-run`, after resolving the workspace bind layout and looking up the agent (both pure), the pipeline reports
and returns, performing **none** of: image build, base-image pull, agent-version network resolution, volume creation,
volume prefill, home-state write, config-hash persist, upgrade prompt, migration offer, VM create/start, daemon setup,
attach.

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
   `prepare.recoverAgentVersion`, the preview does **not** record the reconciled version — it only reports.
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
   (`prepare.liveHomeVolume`, `GetSandbox` + `ConfigMounts`) so the preview reports `would reuse` for the volume a real
   run would recover, rather than an unfaithful `would create`. Report `would create home volume` (the fresh volume's
   name embeds a timestamp, so the preview prints no name it cannot honour) or `would reuse home volume <name>`. No
   volume is created and no recovered state is written.
4. **Session.** Report `would start agent session` (or the shell equivalent).

The result is carried in a small value (e.g. `prepare.Preview{Image, NeedsBuild, Reasons, AgentVersion, HomeVolume,
Create}` plus the session line) so it is asserted as data in unit tests and as the rendered lines in CLI tests.

### Purity boundaries (implementation shape)

- Split the image path so the preview can compute the decision without building: `image.EnsureImageWithClient` factors a
  pure `image.planRunnerImage` (render + build decision) from `image.buildFromPlan`. The preview calls the planner only;
  `image.buildFromPlan` — and therefore `image.resolveBaseDigest` — is never reached.
- The preview must not call `BuildOptions.resolveAgentVersion(ctx, a, "")`. It passes the pin, or the
  label-reconciled baseline (`image.ExistingAgentVersion`, local) with the recorded `agent_version` as the fallback —
  the same precedence the real run applies for every registered agent — and reports a deferred resolution only when
  neither yields a version.
- Home resolution in the preview shares `volume.resolveHomeVolumePlan` with `volume.Manager.ResolveHomeVolume`, so the
  two take the same reuse → recover → create decision (including the recorded-volume, adoption-marker, and live-VM
  branches, in the preview only reporting `name`/`wouldCreate`): the preview reads state and probes the recorded
  volume, and on the recovery path consults `prepare.liveHomeVolume` (also read-only), but never calls
  `commitHomeVolumePlan`/`EnsureNewHome`/`state.RecoverHomeState` and never writes the recovered state.
- The setup hub (`prepare.PrepareSandbox`) takes a `skipVM bool` and has no `--dry-run` branch. The mode lives at the
  session/CLI boundary (`options.Mode`, resolved by `runoptions.ResolveMode`): the session enters the hub only for
  `ModeRun`/`ModeSkipVM` and returns the preview before reaching it, so the hub never runs under `--dry-run` and its
  migration offer and version determination are simply never reached. The preview entry point
  (`session.previewAndRender`) is unchanged.
- Project identity is resolved read-only too: the preview runs `vm.AdoptLegacyIdentity` with `apply=false`, so it
  reports against the slug a real run would find first (the legacy slug while a migration is pending) without moving
  state, and an error from that resolution (e.g. a corrupt legacy state document) surfaces rather than being ignored.
- The root preflight provisions no runtime for a preview (#424): the `cmd` root preflight checks
  `runoptions.ResolveMode(cmd).Previews()` and, instead of `PrepareRuntime`, only applies the private `MSB_HOME`
  (`runtime.ApplyPreviewHome`, inspect/install skipped). A fresh host thus renders the plan with no download and no
  runtime install, while the preview's read-only msb inspection (volumes, sandboxes) still resolves against the
  private home rather than the user's `~/.microsandbox`. `shell --dry-run` behaves the same: like `run`, it skips the
  `doctor.CheckAll` preflight on a preview (`cmd.runStartupStages`), so it installs nothing either.

### Test seam

`prepare.agentLatestVersion` is the upgrade check's release lookup; the image build's injected resolver
(`BuildOptions.ResolveVersion`, supplied by `prepare.agentVersionResolverFor`) reads it too. The run/shell CLI tests run
`--dry-run-vm`, so they reach `resolveBuildVersion`, whose `pendingUpgrade` calls it when a project has a recorded
baseline and the check is due — a real network request. `options.Seams.AgentLatestVersion` (on `options.RunOptions`)
replaces it; `cmd`'s `TestMain` (beside the migration-offer default) installs a **fail-fast** default on the boundary
`runSeams` var that panics: a test that reaches the network path fails loudly and opts in with an explicit stub, rather
than silently bypassing it. This is not an `InstallFailFast*` family, so the `seamFamilies` registry in
`internal/testutil` is untouched.

The preview itself needs no new seam: it composes the existing docker (`WithNoopDockerMock`) and msb (`WithMsbMock`)
mocks. Its purity is pinned by tests that install a resolver/`agentLatestVersion` which fails the test if called, and by
asserting `CreateVolume`/`ImageBuild` are absent from the mock call history.

### `volume <name> --dry-run`

`volume migrate/reset/edit --dry-run` must honour the same flag contract. `prepareVolumeOp` returns early under
`--dry-run` (before `image.EnsureImage`), because the dry-run message needs only the state-derived old volume, not the
image info, and the volume op already tolerates a zero `image.ImageInfo`. Tests cover that `--dry-run` performs no
`ImageInspect`/`ImageBuild` and still reports the would-do message.

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
