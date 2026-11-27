---
description: Design for issue #920 - add a bounded `sandbox cp`/`copy` command for host↔project-VM transfer, adopt the wider `SandboxFS` methods it needs (`CopyFromHost`/`CopyToHost`, plus `List`/`Stat` for directories), and amend the spec's "dropped" decision.
read_if: Implementing #920 (the `sandbox cp` command, its guards, or the `CopyFromHost`/`CopyToHost`/`List`/`Stat` seam additions); changing `msb.SandboxFS` or its implementation; reopening the `sandbox copy`/`cp` "dropped" note in `specs/volumes/runtime-management-commands.md`; or working on the patch-export/worktree copy-out flow.
---

# msb filesystem/transfer: a `sandbox cp` command and the `SandboxFS` it needs

Status: proposed. Issue #920. Base: branch `chore/923-msb-adapter` (PR #928); lands after #923
merges. Supersedes the #920 half of `guides/msb-sdk-integration.md` §1.6 and reverses the
`sandbox copy`/`cp` "dropped" decision in `specs/volumes/runtime-management-commands.md`.

## Intent

The launcher has no way to move a file between the host and a project VM except through the shared
`/workspace` bind, `git`, or `docker cp`. The SDK's filesystem surface supports it directly
(`CopyFromHost`/`CopyToHost`), and its own CLI exposes it as `msb copy`/`cp`. The management spec
deliberately dropped a `sandbox copy`/`cp` command; this design **adopts it** as a bounded,
project-scoped transfer command, which is also the consumer that justifies widening the seam's
`SandboxFS` (so no dead surface is added).

## Goals

- A `sandbox copy`/`cp <src> <dst>` command for host↔project-VM transfer, and project-VM↔project-VM.
- Cover the motivating flow: export a patch/artifact out of a VM (for example from a `--worktree`
  session) without an in-guest `git` push or a `docker cp`.
- Adopt the `SandboxFS` methods the command needs (`CopyFromHost`, `CopyToHost`, and - if directory
  support is in scope - `List`/`Stat`) with their real consumer.
- Amend `specs/volumes/runtime-management-commands.md`: replace the `sandbox copy`/`cp` drop with the
  command's contract, and keep the rest of the bounded-surface non-goals.

## Non-goals

- SSH/SFTP (`guides/msb-sdk-integration.md` §1.8 declines it; SFTP must not re-open this surface as a
  second path).
- Arbitrary sandbox access: the command addresses agents-sandbox project VMs by the same fail-closed
  classification guard as `sandbox remove` (not task sandboxes, volumes, or images).
- Data-portability commands (`image pull`/`load`/`save`), registry, cloud, or msb self-management.
- `ReadStream`/`WriteStream` as explicit seam methods: `CopyFromHost`/`CopyToHost` move the file; a
  raw stream is only needed if the command streams to/from stdout, which v1 does not.
- A `run --export` or other transfer smuggled into the run lifecycle.

## Anti-goals

- A command that silently overwrites a host file or a guest path with no report of what moved.
- Reaching a non-project sandbox, or copying between two VMs with different owners, without the
  classification guard.
- Adding the wider methods with no consumer (the `deadcode` gate would reject it, and a reviewer
  should reject it earlier).

## Constraints

- `msb-seam.md`: `SandboxFS` is a launcher-owned interface; new methods take launcher-shaped
  signatures. `phase4_boundary_test.go` keeps `msbSdk` inside `msb`.
- Command conventions (`specs/volumes/runtime-management-commands.md`): the `also: sandbox X` tree
  annotation, aliases, `-q/--quiet`, `--format json` where a listing is involved, and `termio`
  output. A transfer prints a per-item result; `--dry-run` is not meaningful, a confirmation is not
  needed (it is not destructive), and the reference/in-use guards do not apply.
- The target VM must be running to use its filesystem; a stopped or created project VM is started
  through the existing `ConnectOrStart` path (the same one `run` uses) and left running (its idle
  timeout stops it later).
- Format with `make fmt`; lint with `make lint`; run `make check` before finalizing. A new command
  needs its `cmd/agents-sandbox/cli_*_test.go` coverage and a changelog fragment.

## Current state (measured on the #923 base)

- `msb.SandboxFS` has `Exists`, `ReadString`, `Mkdir`, `Write`, `Read`, `Remove`; `realSandbox.FS()`
  returns the SDK `*msbSdk.SandboxFSOps` directly, which satisfies the interface structurally.
- The SDK `SandboxFSOps` offers `List`, `Stat`, `Copy`, `Rename`, `RemoveDir`, `CopyFromHost`,
  `CopyToHost`, and `ReadStream`/`WriteStream`; `CopyFromHost`/`CopyToHost` copy a single file.
- `msb copy`/`cp` on the SDK CLI takes `hostpath` and `sandbox:path` operands, supports host→sandbox,
  sandbox→host, and sandbox↔sandbox, starts a stopped sandbox temporarily, and has `-q`.
- The `sandbox` group (`cmd/agents-sandbox/commands_system.go` `buildSandboxCmd`) has `list`, `run`,
  `shell`, `stop`, `kill`, `inspect <name>`, `remove <name>`, `prune`, `compact [name]`; `sandbox
  copy`/`cp` is listed as **dropped** in the spec.
- Transfers today ride the `/workspace` bind (`specs/worktrees/worktree-layout.md`).

## Approaches considered

### Command shape

- **A (chosen): `sandbox copy`/`cp <src> <dst>` with `<name>:<path>` operands.** A sandbox operand is
  `<project-vm-name>:<path>`; a bare path is a host path. Direction is inferred: one sandbox operand
  → host↔VM; two → VM↔VM; none → an error. This mirrors `msb copy`, disambiguates VM↔VM, and keeps
  host paths writable in the ordinary cwd-relative form.
- **B: `--to-vm`/`--to-host` flags with paths as operands.** Verbose and cannot express VM↔VM
  cleanly; rejected.
- **C: always project-scoped to the cwd VM with a single path.** Half the use case (copy between two
  VMs) and the export flow wants an explicit source; rejected.

### Target resolution and guards

- **A (chosen): a project-VM name resolved by the existing lookup, with the `sandbox remove`
  classification guard.** The name must classify as an agents-sandbox project VM; a task sandbox,
  volume, or image name is refused hard. Where the cwd's own project VM should be nameable without
  typing its full `<slug>-<agent>` name, the builder may accept `.`/an agreed alias - that is a
  builder call left open below.
- **B: any sandbox in the private home.** Would expose task sandboxes and break the
  project-scoped model; rejected.

### Directories and streams

- **A (chosen): files in v1; directories only with a recursive walk if the plan adopts it.** The
  motivating export is a file. If a recursive transfer is wanted, it walks with `List` and creates
  directories with `Mkdir` (already present), so `List`/`Stat` join the seam. Streams stay out
  (`CopyFromHost`/`CopyToHost` already move the bytes).
- **B: streams for everything.** Rejected: the SDK transfer methods are the higher-level call; raw
  streams are for stdout piping, which v1 does not do.

## The design

### 1. Widen `SandboxFS` for the command's consumer

```go
// SandboxFS gains, for sandbox cp:
CopyFromHost(ctx context.Context, hostPath, guestPath string) error
CopyToHost(ctx context.Context, guestPath, hostPath string) error
// If directory transfer is in scope:
List(ctx context.Context, path string) ([]FsEntry, error)
Stat(ctx context.Context, path string) (*FsStat, error)
```

`FsEntry`/`FsStat` are launcher-owned structs (the SDK's carry a `Kind`/`Mode`/`ModTime`); the
adapter projects them. Add a launcher `realSandboxFS` wrapping `*msbSdk.SandboxFSOps` and return it
from `realSandbox.FS()`, so the interface is implemented by a launcher type and the SDK type stays
inside the adapter. The mock's `TestFS` gains the new methods.

### 2. The `sandbox cp` command

- Operands: `sandbox cp <src> <dst>`; each is `<name>:<path>` (a project VM) or a bare host path.
- Guards: each named sandbox passes the project-vs-task classification guard; the guest path must be
  absolute; the host path resolves against the cwd.
- VM lifecycle: resolve the project VM (via the identity/adoption path the other `sandbox` commands
  use), `ConnectOrStart` it, and run `CopyFromHost`/`CopyToHost` on its `FS`; a VM that could not be
  started surfaces the start error.
- Output: one line per copied item (source → destination); `-q/--quiet` suppresses it. `--format
  json` emits the copied items (kept consistent with the group's conventions).
- Tree: `sandbox copy` with alias `cp` and the `also: sandbox copy` annotation, matching the group.

### 3. Spec change

`specs/volumes/runtime-management-commands.md`: replace the `sandbox copy`/`cp` drop with its
contract (operands, guard, start behaviour, `-q`/`--format json`), and keep "not a mirror of msb's
lifecycle/portability" for the rest. The `storage`/`df` and prune sections are unaffected.

## Spec and guide updates

- `specs/volumes/runtime-management-commands.md`: the new command contract; drop the "dropped"
  line.
- `specs/architecture/msb-seam.md`: the `SandboxFS` implementation/currency clause and the new
  methods.
- `guides/msb-sdk-integration.md` §1.6: fold in the adopted transfer surface.
- `README`/`docs` command reference: add `sandbox cp`.
- `changelog.d/<PR-number>.added.md`: the `sandbox cp` command (user-visible).

## Testing

- CLI (`cmd/agents-sandbox`): direction inference (host→VM, VM→host, VM↔VM, none → error); the
  classification guard refuses a task-sandbox/volume/image name; a stopped VM is started; `-q` and
  `--format json` shapes; a copy error surfaces.
- Unit (`msb`): the `realSandboxFS` adapter delegates `CopyFromHost`/`CopyToHost` (and `List`/`Stat`);
  `TestFS` records copies.
- Real VM (`msb` tag / KVM host): a file round-trips host→VM→host; a directory transfer (if adopted)
  recreates the tree.

## Estimates

- `msb`: two to four seam methods, `FsEntry`/`FsStat`, the `realSandboxFS` adapter, mock support.
  Medium.
- `cmd`: the command, operand parsing, direction inference, guards, render, tests. Medium.
- Specs/docs. Small-to-medium (the spec change is a contract, not a one-liner).

## What is left to the builder

- Whether the cwd project VM is nameable by an alias (`.`) or only by its full name.
- Whether v1 includes recursive directory transfer, and therefore `List`/`Stat`.
- Whether a started VM is left running (the design says yes, relying on the idle timeout) or stopped
  after the transfer when it was stopped before.
- The `--format json` shape for copied items.

## References

- `.operator-shared/guides/msb-sdk-integration.md` §1.6, §1.8
- microsandbox v0.8.3 `docs/sandboxes/filesystem.mdx`, `docs/cli/sandbox-commands.mdx` (`msb copy`),
  `sdk/go/core/fs.go` (`SandboxFSOps`)
- `.operator-shared/specs/volumes/runtime-management-commands.md`,
  `specs/architecture/msb-seam.md`, `specs/worktrees/worktree-layout.md`
- `internal/sandbox/msb/fs.go`, `internal/sandbox/msb/sandbox.go` (`realSandbox.FS`),
  `cmd/agents-sandbox/commands_system.go` (`buildSandboxCmd`)
