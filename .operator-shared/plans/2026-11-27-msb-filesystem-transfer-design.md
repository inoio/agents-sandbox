---
description: Design for issue #920 - add a bounded `sandbox cp`/`copy` command for host↔current-project-VM transfer of files, adopt the `SandboxFS` `CopyFromHost`/`CopyToHost` methods it needs, and amend the spec's "dropped" decision.
read_if: Implementing #920 (the `sandbox cp` command, its guards, or the `CopyFromHost`/`CopyToHost` seam additions); changing `msb.SandboxFS` or its implementation; reopening the `sandbox copy`/`cp` "dropped" note in `specs/volumes/runtime-management-commands.md`; or working on the patch-export/worktree copy-out flow.
---

# msb filesystem/transfer: a `sandbox cp` command and the `SandboxFS` it needs

Status: proposed. Issue #920. Base: branch `chore/923-msb-adapter` (PR #928); lands after #923
merges. Supersedes the #920 half of `guides/msb-sdk-integration.md` §1.6 and reverses the
`sandbox copy`/`cp` "dropped" decision in `specs/volumes/runtime-management-commands.md`.

## Intent

The launcher has no way to move a file between the host and a project VM except through the shared
`/workspace` bind, `git`, or `docker cp`. The SDK's filesystem surface supports it directly
(`CopyFromHost`/`CopyToHost`), and its own CLI exposes it as `msb copy`/`cp`. The management spec
deliberately dropped a `sandbox copy`/`cp` command; this design **adopts a bounded version of it**,
which is also the consumer that justifies widening the seam's `SandboxFS` (so no dead surface is
added). The bound: it transfers **files between the host and the current checkout's project VM** -
not arbitrary sandboxes, and not VM-to-VM.

## Goals

- A `sandbox copy`/`cp <src> <dst>` command moving a **file** between the host and the current
  checkout's project VM, either direction.
- Cover the motivating flow: export a patch/artifact out of the VM (for example from a `--worktree`
  session) without an in-guest `git` push or a `docker cp`; and push a host file in.
- Adopt `SandboxFS.CopyFromHost`/`CopyToHost` with their real consumer, and wrap the SDK FS type in a
  launcher adapter.
- Amend `specs/volumes/runtime-management-commands.md`: replace the `sandbox copy`/`cp` drop with the
  command's contract.

## Non-goals

- **VM↔VM transfer.** The SDK has no cross-sandbox copy (`Copy` is within one sandbox); two sandbox
  operands could only be served by staging through a host temp file, which is a new design (temp
  location, mode, cleanup, partial-failure handling, and putting VM contents on host disk). Dropped
  from v1.
- **Addressing an arbitrary/named project VM.** cp acts on the **current checkout's** project VM, the
  same identity `sandbox stop`/`kill` resolve. This keeps it project-scoped and avoids an ownership
  check the name-shape guard does not provide (`vm/remove.go`'s guard is classification-only).
- **Booting a stopped VM from cp.** Starting via `ConnectOrStart` alone would skip `SetUpSandbox`'s
  startup hooks and provenance (`setup_sandbox.go` gates them on `VMBoot.Booted()`), and a later
  `run` would see the VM running and skip them too. cp therefore requires the VM to be **running**.
- Recursive directory transfer, raw `ReadStream`/`WriteStream`, SSH/SFTP, data-portability commands
  (`image pull`/`load`/`save`), registry, cloud, and msb self-management.
- A `run --export` or other transfer smuggled into the run lifecycle.

## Anti-goals

- A command that can reach a non-project sandbox: it addresses only the cwd project VM, so there is
  nothing to classify beyond the existing identity resolution.
- Silently overwriting a host file or a guest path with no report of what moved.
- Adding the wider methods with no consumer (the `deadcode` gate would reject it; a reviewer should
  reject it earlier).

## Constraints

- `msb-seam.md`: `SandboxFS` is a launcher-owned interface; new methods take launcher-shaped
  signatures. `phase4_boundary_test.go` keeps `msbSdk` inside `msb`.
- Identity: cp resolves the checkout identity the way `sandbox stop`/`kill` do (repo identity +
  agent + `AdoptLegacyIdentity`), so it acts on the same VM `run`/`shell` use, including a legacy
  adopted slug.
- The target VM must be running; a created/stopped/crashed VM is an error naming how to start it.
- Command conventions (`specs/volumes/runtime-management-commands.md`): the `also: sandbox X` tree
  annotation, alias `cp`, `-q/--quiet`, `--format json`. A transfer is not destructive, so no
  confirmation and no reference/in-use guard apply; `--dry-run` is not meaningful.
- `CopyFromHost`/`CopyToHost` do not document creating missing parent directories; the command
  defensively `Mkdir`s the guest destination's parent (the SDK `Mkdir` creates parents).
- Format with `make fmt`; lint with `make lint`; run `make check` before finalizing. The command
  needs `cmd/agents-sandbox/cli_*_test.go` coverage and a changelog fragment.

## Current state (measured on the #923 base)

- `msb.SandboxFS` has `Exists`, `ReadString`, `Mkdir`, `Write`, `Read`, `Remove`; `realSandbox.FS()`
  returns the SDK `*msbSdk.SandboxFSOps` directly, which satisfies the interface structurally.
- The SDK `SandboxFSOps` offers `List`, `Stat`, `Copy`, `Rename`, `RemoveDir`, `CopyFromHost`,
  `CopyToHost`, `ReadStream`/`WriteStream`; `CopyFromHost`/`CopyToHost` copy a single file and are
  the SDK's cross-channel transfer (same channel as exec; network-independent).
- `msb copy`/`cp` on the SDK CLI takes `hostpath` and `sandbox:path` operands and supports
  host↔sandbox, sandbox↔host, and sandbox↔sandbox; the Go SDK has no cross-sandbox call, so the SDK
  CLI implements the last by staging.
- The `sandbox` group (`cmd/agents-sandbox/commands_system.go` `buildSandboxCmd`) has `list`, `run`,
  `shell`, `stop`, `kill`, `inspect <name>`, `remove <name>`, `prune`, `compact [name]`; `stop`/`kill`
  act on the cwd project VM and `inspect`/`remove`/`compact` take a name. `sandbox copy`/`cp` is
  listed as **dropped** in the spec.
- Transfers today ride the `/workspace` bind (`specs/worktrees/worktree-layout.md`).

## Approaches considered

### Command shape

- **A (chosen): cwd-scoped, host↔VM only.** `sandbox cp <src> <dst>` where exactly one operand is a
  host path and the other is a VM path marked `vm:<path>` (e.g. `sandbox cp ./patch.diff
  vm:/workspace/patch.diff`). The VM is the current checkout's project VM, resolved like
  `sandbox stop`. Two `vm:` operands, or none, are an error. This drops VM↔VM (Staging) and needs no
  ownership check.
- **B: `msb copy`-style `<name>:<path>` operands including VM↔VM.** Rejected for v1: VM↔VM needs a
  host-staging design, and a named operand needs an ownership check the name guard does not give.
- **C: `--to-vm`/`--to-host` flags with two plain paths.** Verbose and no advantage over `vm:`; the
  `vm:` prefix reads the same as `msb copy`'s sandbox operand.

### VM lifecycle

- **A (chosen): require the VM running.** Resolve the cwd project VM and `ConnectOrStart`; a
  created/stopped/crashed VM fails with "not running; start it with `agents-sandbox run`/`shell`".
  Safe (no skipped setup), simple, and consistent with `stop`/`kill` operating on an existing VM.
- **B: boot via the full preparation path (`EnsureProjectVM` + `SetUpSandbox`).** Correct setup, but
  drags the image/home/prepare pipeline into a copy command; rejected for v1.
- **C: `ConnectOrStart` and leave running.** Rejected: skips `SetUpSandbox`'s hooks and provenance;
  a later `run` then sees the VM running and skips them too.

### Directories and streams

- **A (chosen): files in v1.** The motivating export is a file. A recursive transfer would add
  `List`/`Stat` and a walk; deferred (adds no method until adopted).
- **B: streams.** Rejected: `CopyFromHost`/`CopyToHost` move the bytes; raw streams are for stdout
  piping, which v1 does not do.

## The design

### 1. Widen `SandboxFS` for the command's consumer

```go
// SandboxFS gains, for sandbox cp:
CopyFromHost(ctx context.Context, hostPath, guestPath string) error
CopyToHost(ctx context.Context, guestPath, hostPath string) error
```

Add a launcher `realSandboxFS` wrapping `*msbSdk.SandboxFSOps` and return it from `realSandbox.FS()`,
so the interface is implemented by a launcher type and the SDK type stays inside the adapter. The
mock's `TestFS` gains the two methods (recording copies, backed by its file map).

### 2. The `sandbox cp` command

- Operands: `sandbox cp <src> <dst>`, each a host path or `vm:<guest-path>`. Exactly one side is
  `vm:`; the guest path must be absolute. Direction is inferred from which side carries `vm:`.
- Identity: resolve the cwd checkout identity + agent + legacy adoption (as `sandbox stop`), giving
  the project VM.
- Lifecycle: `ConnectOrStart` the VM; a VM that is not running is an error (no boot). Copy on its
  `FS`: `CopyFromHost` for host→VM, `CopyToHost` for VM→host; `Mkdir` the guest parent for a nested
  destination.
- Output: one line naming source → destination; `-q/--quiet` suppresses it; `--format json` emits
  the copied item(s). Tree: `sandbox copy` with alias `cp` and the `also: sandbox copy` annotation.

### 3. Spec change

`specs/volumes/runtime-management-commands.md`: replace the `sandbox copy`/`cp` drop with its
contract (the two-operand `vm:` form, cwd scope, running-only, `-q`/`--format json`), and keep "not a
mirror of msb's lifecycle/portability" for everything else. The `storage`/`df` and prune sections are
unaffected.

## Spec and guide updates

- `specs/volumes/runtime-management-commands.md`: the new command contract; drop the "dropped" line.
- `specs/architecture/msb-seam.md`: the `SandboxFS` implementation/currency clause and the two new
  methods.
- `guides/msb-sdk-integration.md` §1.6: fold in the adopted transfer surface.
- `README`/`docs` command reference: add `sandbox cp`.
- `changelog.d/<PR-number>.added.md`: the `sandbox cp` command (user-visible).

## Testing

- CLI (`cmd/agents-sandbox`): direction inference for host→VM and VM→host; a `vm:` operand with a
  non-absolute guest path is rejected; two `vm:` operands (or none) is rejected; a stopped VM errors
  and copies nothing; `-q` and `--format json` shapes; a copy error surfaces.
- Unit (`msb`): `realSandboxFS` delegates `CopyFromHost`/`CopyToHost`; `TestFS` records a copy.
- Real VM (`msb` tag / KVM host): a file round-trips host→VM→host; a nested guest destination's
  parent is created.

## Estimates

- `msb`: two seam methods, the `realSandboxFS` adapter, mock support. Small.
- `cmd`: the command, operand parsing, direction inference, identity resolution, running check,
  render, tests. Medium.
- Specs/docs. Small-to-medium (the spec change is a contract).

## What is left to the builder

- The exact VM-side marker (`vm:` proposed) and whether `.`/`--agent` affects resolution.
- Whether the guest parent `Mkdir` is unconditional or only when `Exists` reports the parent missing.
- The `--format json` item shape.

## References

- `.operator-shared/guides/msb-sdk-integration.md` §1.6, §1.8
- microsandbox v0.8.3 `docs/sandboxes/filesystem.mdx`, `docs/cli/sandbox-commands.mdx` (`msb copy`),
  `sdk/go/core/fs.go` (`SandboxFSOps`)
- `.operator-shared/specs/volumes/runtime-management-commands.md`,
  `specs/architecture/msb-seam.md`, `specs/worktrees/worktree-layout.md`
- `internal/sandbox/msb/fs.go`, `internal/sandbox/msb/sandbox.go` (`realSandbox.FS`),
  `cmd/agents-sandbox/commands_system.go` (`buildSandboxCmd`), `internal/sandbox/vm/vm_control.go`,
  `internal/sandbox/vm/setup_sandbox.go`
