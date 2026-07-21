# Go Rewrite Design: opencode-msb

**Date:** 2026-07-21
**Status:** Draft
**Author:** brainstorming session

## Overview

Rewrite the Python launcher (`src/inoio_sandbox/`) in Go, using the microsandbox
Go SDK. The Go binary becomes the new primary implementation; the Python code
stays in-tree during a transition period for behavioral comparison.

**Goals:**

- Faithful 1:1 port: same CLI commands, flags, and user-facing behavior as the
  Python launcher.
- Use the microsandbox Go SDK for sandbox lifecycle, volumes, secrets, image
  cache, and filesystem operations — replacing `msb` subprocess calls.
- Use the moby Docker client (`github.com/docker/docker/client`) for image
  build/save/inspect with real content-digest idempotency.
- Use `os/exec` only for `git worktree` operations (no Go SDK equivalent; go-git
  lacks linked-worktree support).
- Keep the Python code untouched for parity diffing during transition.

**Non-goals:**

- Removing or deprecating the Python implementation (that happens later,
  separately).
- Changing user-facing behavior, flag names, or config file locations.
- Adding new features.

## Architecture & Module Layout

**Module:** `github.com/inoio/opencode-msb` at repo root (`go.mod` alongside
`pyproject.toml`).

**Directory structure:**

```
cmd/
  opencode-msb/
    main.go              # entrypoint, calls internal/opencodemsb.Execute()
internal/
  opencodemsb/
    cmd.go               # cobra root + doctor/run subcommands, timing helper
    config.go            # JSON5 config merge, returns config JSON string
    doctor.go            # preflight checks (msb, docker, kvm, git)
    image.go             # docker build (moby), image inspect (digest), SDK image cache load
    log.go               # colored stderr output (info/warn/error/timing)
    runner.go            # orchestrates the full run flow, SDK sandbox creation + attach
    secrets.go           # env-var → SDK SecretEntry mapping
    sysinfo.go           # CPU count (runtime.NumCPU) + /proc/meminfo reader
    volumes.go           # SDK volume create/remove + VolumeFs prefill + host-dir fallback
    worktree.go          # git worktree operations (os/exec)
    data/
      Dockerfile          # embedded via //go:embed data/Dockerfile
      provider-config.json
go.mod
go.sum
```

One file per responsibility, mirroring the Python module structure so a reviewer
can diff `runner.py` against `runner.go` etc.

## Dependencies

| Concern | Go library |
|---|---|
| CLI framework | `github.com/spf13/cobra` |
| JSON5 parsing | `github.com/titanous/json5` |
| Sandbox VM / volumes / secrets / FS / image cache | `github.com/superradcompany/microsandbox/sdk/go` |
| Docker build/save/inspect | `github.com/docker/docker/client` (moby) |
| Git worktree | `os/exec` → `git` (go-git lacks linked-worktree support) |
| System info | `runtime.NumCPU()` + `/proc/meminfo` direct read |
| Logging | custom `log.go` (no framework — cobra has no logging helpers) |

## Components

### `cmd.go` — CLI dispatch

Cobra root command `opencode-msb` with two subcommands mirroring Python's
click CLI:

- `opencode-msb doctor` — preflight checks
- `opencode-msb run [flags] [ARGS...]` — launch opencode in a sandbox

**Flags (match Python 1:1):**

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--worktree` | string | `""` | create/use a git worktree by that name |
| `--image-rebuild` | bool | `false` | force rebuild of the runner image |
| `--volume-fallback` | bool | `false` | use project-local dirs instead of msb volumes |
| `--reset-home` | bool | `false` | wipe the opencode home volume before run |
| `--cpus` | uint8 | host CPU count | vCPUs for the sandbox |
| `--memory` | string | `"4G"` | memory limit (human-readable, e.g. `4G`, `512M`) |
| `--timing` | bool | `false` | print per-phase launcher timing to stderr |

Bare `opencode-msb` with no subcommand (or with positional `ARGS` only) is
treated as `run` — matches Python's forwarding behavior.

A `--version` flag on the root reports `dev` (overridable via `-ldflags`).

**Timing helper:** mirrors Python's `_timing(enabled)` — a `tick(label)` /
`summary()` closure pair that prints `[timing] {label}: {elapsed}s` to stderr
when `--timing` is set.

### `log.go` — colored stderr

Minimal helpers: `Info`, `Warn`, `Error`, `Timing`. ANSI color codes applied
only when stderr is a TTY (detected via `term.IsTerminal` or equivalent).
Replaces Python's `click.secho`-based `log.py`. No structured logging
framework.

### `sysinfo.go` — host resources

```go
func NumCPUs() uint8         // runtime.NumCPU()
func TotalMemoryGiB() int    // parse /proc/meminfo MemTotal, convert to GiB
```

Reads `/proc/meminfo` directly (Linux/KVM is the primary target). On non-Linux
platforms, `TotalMemoryGiB` returns 0 and the caller falls back to a default.
`doctor` warns if meminfo is unavailable. Replaces Python's `psutil` dependency.

### `config.go` — JSON5 config merge

Mirrors Python's `config.py`. Reads JSON5 config files from:
1. User dir: `~/.config/inoio-sandbox/opencode/` (JSON5 `.json`/`.jsonc` files)
2. Project dir: `.sandbox/opencode/` (if it exists)
3. Embedded provider config: `data/provider-config.json` (via `//go:embed`)

Merges JSON files by name (deep merge, last-wins for scalars, recursive for
objects). The provider litellm config is merged into `opencode.jsonc` or
`opencode.json` (whichever exists), or creates `opencode.jsonc` if neither
exists. Non-JSON files are copied as-is.

```go
func BuildMergedConfig(userDir, projectDir string) (files map[string][]byte, err error)
```

Returns a map of filename → file content. The caller (`runner.go`) writes these
to the guest filesystem via `sb.FS().WriteString` after sandbox creation.

Uses `github.com/titanous/json5` for parsing (provider-config.json contains
`//` comments and trailing commas, which standard `encoding/json` rejects).

### `secrets.go` — env var → SDK secrets

Mirrors Python's `secrets.py`. Static mapping of env vars to allowed hosts:

```go
var SecretMap = map[string]string{
    "LITELLM_API_KEY": "litellm.inoio.de",
    "GITHUB_TOKEN":    "github.com",
}
```

```go
func BuildSecrets() ([]microsandbox.SecretEntry, error)
```

For each entry in `SecretMap`, reads the env var from the host environment. If
set, creates a `microsandbox.SecretEntry` via
`microsandbox.Secret.Env(varName, value, microsandbox.SecretEnvOptions{AllowHosts: []string{host}})`.
If unset, emits a warning to stderr (matching Python behavior). The real secret
value never enters the VM — the SDK handles placeholder substitution.

### `volumes.go` — SDK volumes + fallback

Mirrors Python's `volumes.py`. Volume naming:
`opencode-msb-{project_slug}-home-{image_digest}` (replaces Python's
Dockerfile-hash-based naming with content-digest-based naming).

```go
type VolumeManager struct {
    fallback bool      // --volume-fallback
    stateDir string    // ~/.local/share/opencode-msb
}

func (vm *VolumeManager) EnsureHome(ctx context.Context, projectSlug, imageDigest, imageTag string, reset bool) (volumeRef string, err error)
```

**SDK path (default):**
1. If `--reset-home`, `microsandbox.RemoveVolume(ctx, name)` first.
2. `microsandbox.CreateVolume(ctx, name, WithVolumeQuota(...), WithVolumeKind(VolumeKindDir))`.
   If the volume already exists (not a reset), skip prefill — it's already
   populated from a prior run.
3. **Prefill (new volumes only):** Boot a throwaway sandbox with the runner
   image, mount the volume at `/mnt/home`, and `cp -a /home/dev/. /mnt/home/ &&
   chown -R dev:dev /mnt/home`. This copies the opencode installation and
   default home structure from the image into the volume. Done via SDK:
   `CreateSandbox(WithImage(imageTag), WithMounts({"/mnt/home": Mount.Named(name, ...)}))`,
   `sb.Exec(ctx, "sh", []string{"-c", "cp -a /home/dev/. /mnt/home/ && chown -R dev:dev /mnt/home"})`,
   then `sb.Stop` + `sb.Close` + `RemoveSandbox`.

   **Why a throwaway sandbox and not `VolumeFs`:** The prefill needs the
   runner image's `/home/dev` contents (opencode binary, `.local/bin`,
   etc.). These live inside the Docker image, not on the host. `VolumeFs`
   writes to the volume's host path but has no access to image contents. The
   throwaway sandbox boots the image, making `/home/dev` available for the copy.

**Fallback path (`--volume-fallback`):**
`os.MkdirAll(stateDir/state/{slug}/home/{digest}, 0o755)`. Prefill uses the
same throwaway-sandbox approach but with a `Mount.Bind(hostPath, ...)`
instead of `Mount.Named`. If the directory is non-empty, skip prefill.

On SDK volume creation failure, automatically falls back to host-dir mode with
a warning (matches Python's graceful degradation).

### `image.go` — docker build + SDK image cache

Mirrors Python's `image.py` with a key improvement: **real content-digest
idempotency** instead of Dockerfile hashing.

```go
const BaseTag = "opencode-msb/runner:base"

func EnsureImage(ctx context.Context, dockerfile []byte, force bool) (imageRef, imageDigest string, err error)
```

**Flow:**

1. **Base image check:** If the project Dockerfile contains
   `FROM opencode-msb/runner:base`, build and load the base image first
   (via `EnsureImage` with the base Dockerfile).
2. **Build:** `docker build -t opencode-msb/runner:latest -f <dockerfile> .`
   via moby client `ImageBuild`. Docker's layer cache makes repeat builds fast.
3. **Inspect:** `cli.ImageInspect(ctx, "opencode-msb/runner:latest")` →
   real content digest (e.g. `sha256:abc123def456...`).
4. **Cache check:** `microsandbox.Image.Get(ctx, "opencode-msb/runner:"+truncatedDigest)`
   → if found (cache hit), skip save+load entirely.
5. **Save + load (cache miss only):** `cli.ImageSave(ctx, imageID)` → temp
   tarball → `microsandbox.Image.Load(ctx, tarPath, "opencode-msb/runner:"+truncatedDigest)`.
6. **Return** `"opencode-msb/runner:"+truncatedDigest` as the image reference
   for sandbox creation.

**Project override:** `.sandbox/Dockerfile` takes precedence over the embedded
default (matches Python).

**Why content digest, not Dockerfile hash:** Same Dockerfile can produce
different images when base images are updated (e.g. `debian:trixie-slim` gets a
new version). Content digest catches this; Dockerfile hash doesn't. This is
true idempotency — same image content = same digest = cache hit.

### `worktree.go` — git worktree (os/exec)

Mirrors Python's `worktree.py`. Uses `os/exec` to call `git` directly — go-git
does not support linked worktrees (`git worktree add`).

```go
func ProjectSlug() string                                    // sha256[:8] of git common dir
func BranchName(cwd string) (string, error)                  // git rev-parse --abbrev-ref HEAD
func CurrentWorktreePath(cwd string) (string, error)          // git rev-parse --show-toplevel
func EnsureWorktree(repoRoot, stateDir, projectSlug, branch string) (string, error)
func RemoveWorktree(path string) error
```

**Project slug:** `p-{sha256[:8]}` of the resolved git common dir path. Falls
back to CWD hash if not in a git repo (with a warning).

**Worktree path:** `{stateDir}/worktrees/{projectSlug}/{branchSlug}` where
`branchSlug` replaces `/` with `-`.

### `doctor.go` — preflight checks

Mirrors Python's `doctor.py`. Four checks, all must pass:

1. **msb:** `microsandbox.IsInstalled()` (SDK) + `microsandbox.RuntimeVersion(ctx)`
   for a version probe. Replaces `shutil.which("msb")`.
2. **docker:** `exec.LookPath("docker")` (the moby client also needs the Docker
   daemon, but a CLI presence check is sufficient for doctor).
3. **kvm:** `os.Stat("/dev/kvm")` on Linux; skip on macOS.
4. **git:** `exec.LookPath("git")`.

Returns non-zero exit on any failure; advisory messages go to stderr with ANSI
red. On success: `doctor: all checks passed`.

### `runner.go` — main flow (SDK-driven)

This is where the Python "build a `msb run` command list" becomes direct SDK
calls. The flow:

```go
func Run(ctx context.Context, opts RunOptions) error {
    tick, summary := newTiming(opts.Timing)
    defer summary()

    // 1. Preflight
    if !doctor.CheckAll() { return errors.New("preflight failed") }
    tick("preflight")

    // 2. Project / branch resolution
    projectSlug := worktree.ProjectSlug()
    branch := opts.Worktree
    if branch == "" { branch, _ = worktree.BranchName(cwd) }
    tick("project/branch resolution")

    // 3. Worktree resolution
    wtPath := worktree.CurrentWorktreePath(cwd)
    if wtPath == "" {
        wtPath, _ = worktree.EnsureWorktree(cwd, stateDir, projectSlug, branch)
    }
    tick("worktree resolution")

    // 4. Image build + cache load
    dockerfile := resolveDockerfile() // .sandbox/Dockerfile or embedded default
    imageRef, imageDigest, _ := image.EnsureImage(ctx, dockerfile, opts.ImageRebuild)
    tick("image hash/check/build")

    // 5. Home volume
    homeVol, _ := volumes.EnsureHome(ctx, projectSlug, imageDigest, imageRef, opts.ResetHome)
    tick("volume ensure")

    // 6. Config merge + secrets
    configFiles, _ := config.BuildMergedConfig(userConfigDir, projectConfigDir)
    secrets, _ := secrets.BuildSecrets()
    cpus := opts.CPUs
    if cpus == 0 { cpus = sysinfo.NumCPUs() }
    maxMemory := sysinfo.TotalMemoryGiB()
    name := sandboxName(projectSlug, branch) // "opencode-msb-{slug}-{branch}"[:128]
    tick("config/secrets")

    // 7. Read .sandbox/env for extra env vars
    envExtra := readSandboxEnv() // KEY=value lines, # comments

    // 8. Build mount map + env map
    mounts := map[string]microsandbox.MountConfig{
        "/home/dev":                        microsandbox.Mount.Named(homeVol, microsandbox.MountOptions{}),
        "/home/dev/workspace":              microsandbox.Mount.Bind(wtPath, microsandbox.MountOptions{}),
    }
    envMap := buildEnvMap(envExtra) // HOME, NODE_ENV, SANDBOX_USER, SHELL + extra

    // 9. Create + boot sandbox
    sb, err := microsandbox.CreateSandbox(ctx, name,
        microsandbox.WithImage(imageRef),
        microsandbox.WithMounts(mounts),
        microsandbox.WithSecrets(secrets...),
        microsandbox.WithEnv(envMap),
        microsandbox.WithUser("dev"),
        microsandbox.WithWorkdir("/home/dev/workspace"),
        microsandbox.WithCPUs(cpus),
        microsandbox.WithMaxCPUs(sysinfo.NumCPUs()),
        microsandbox.WithMemory(parseMemory(opts.Memory)),
        microsandbox.WithMaxMemory(uint32(maxMemory)*1024),
        microsandbox.WithReplace(),
    )
    if err != nil { return err }
    defer sb.Stop(ctx)
    defer sb.Close()
    defer microsandbox.RemoveSandbox(ctx, name)

    // 10. Write merged config to guest filesystem (separated from attach)
    fs := sb.FS()
    fs.Mkdir(ctx, "/home/dev/.config/opencode")
    for name, data := range configFiles {
        fs.Write(ctx, "/home/dev/.config/opencode/"+name, data)
    }
    // Remove .envrc* files from the worktree mount (security: secrets)
    removeEnvrcFiles(ctx, fs, wtPath)
    tick("config setup")

    // 11. Interactive attach — clean opencode invocation
    //     goenv init runs in a login shell before opencode (preserves Python behavior)
    setup := `eval "$(goenv init -)" && exec opencode ` + strings.Join(opts.Args, " ")
    exitCode, err := sb.Attach(ctx, "/bin/bash", "-lc", setup)
    tick("opencode session")

    // 12. Exit code propagation
    return exitCode
}
```

**Key behavioral notes:**

- **`sb.Attach`** opens a PTY and blocks until the process exits. This matches
  the Python `msb run` interactive foreground behavior — the user sees
  opencode's TUI directly.
- **`WithReplace()`** handles the "sandbox with this name already exists" case
  (same as Python's `msb run --replace`).
- **Deferred cleanup** (`Stop`/`Close`/`RemoveSandbox`) runs on Ctrl-C (context
  cancellation propagates to `Attach`).
- **Config setup is separated** from the attach: `fs.Write` calls write config
  files directly to the guest filesystem, replacing Python's `--copy-dir` +
  shell `cp` approach. No bind-mounted temp dir needed.
- **goenv init** preserves the Python behavior of initializing goenv (Go version
  manager) before launching opencode. This stays as a minimal shell string in
  the `Attach` call because goenv modifies the shell environment (PATH) that
  opencode inherits. A fully clean `sb.Attach(ctx, "opencode", args...)` would
  drop goenv, which would be a behavioral regression.
- **`.envrc*` removal:** After sandbox creation, `.envrc*` files in the worktree
  mount are removed via `fs.Remove` (matches Python's `--rm` flags). Since the
  worktree is bind-mounted read-write, this removes the files from the host
  worktree too — this is inherited Python behavior. See AGENTS.md "Current
  limitations" for the broader `.envrc` handling gap.

**Sandbox name:** `opencode-msb-{project_slug}-{branch_slug}` truncated to 128
bytes (SDK limit).

**Memory parsing:** `opts.Memory` is a human-readable string (`"4G"`, `"512M"`).
Parsed to MiB for `WithMemory`/`WithMaxMemory`.

**VM environment (from Python's `VM_ENV`):**
- `HOME=/home/dev`
- `NODE_ENV=development`
- `SANDBOX_USER=dev`
- `SHELL=/bin/bash`
- Plus any `KEY=value` entries from `.sandbox/env`

## Data Flow

```
user invokes `opencode-msb run --worktree feat-x`
    │
    ├─ doctor.CheckAll ──► SDK IsInstalled + exec.LookPath(docker, git) + /dev/kvm
    ├─ worktree.ProjectSlug / BranchName ──► os/exec git rev-parse
    ├─ worktree.EnsureWorktree ──► os/exec git worktree add
    │
    ├─ image.EnsureImage
    │     ├─ moby ImageBuild (docker build)
    │     ├─ moby ImageInspect ──► content digest
    │     ├─ SDK Image.Get ──► cache hit? skip
    │     └─ moby ImageSave ──► temp tarball ──► SDK Image.Load
    │
    ├─ volumes.EnsureHome ──► SDK CreateVolume + throwaway-sandbox prefill (or host-dir fallback)
    ├─ config.BuildMergedConfig ──► JSON5 parse + deep merge ──► map[name]bytes
    ├─ secrets.BuildSecrets ──► SDK SecretEntry (LITELLM_API_KEY, GITHUB_TOKEN)
    │
    ├─ microsandbox.CreateSandbox
    │     ├─ WithImage, WithMounts (home volume + worktree bind), WithSecrets, WithEnv
    │     ├─ WithCPUs, WithMaxCPUs, WithMemory, WithMaxMemory, WithUser, WithWorkdir
    │     └─ WithReplace
    │
    ├─ sb.FS().Write (config files to /home/dev/.config/opencode/)
    ├─ sb.FS().Remove (.envrc* files from worktree mount)
    │
    └─ sb.Attach("/bin/bash", "-lc", "eval $(goenv init -) && exec opencode ...")
          └─ user interacts with opencode TUI
          └─ exit code propagates to opencode-msb exit
```

## Error Handling

- **SDK errors:** Checked with `microsandbox.IsKind(err, microsandbox.ErrSandboxNotFound)`
  etc. where the kind matters. "Image not in cache" (`ErrImageNotFound`) is
  expected on first run — not an error, triggers the build+load path.
- **Docker errors:** moby client returns typed errors; surfaced directly with
  context (build failure, save failure, daemon not running).
- **Git errors:** `exec.ExitError` from `git` commands; surfaced with stderr
  output. "Not in a git repo" is a hard error for `run` (matches Python).
- **Preflight:** `doctor` returns non-zero on any check failure. `run` also
  runs preflight and fails fast if checks don't pass.
- **Exit codes:** `run` exits with the sandbox process's exit code from
  `Attach`. `doctor` exits 0 on success, 1 on failure. `Ctrl-C` exits 130
  (matches Python's `KeyboardInterrupt` handling).
- **Volume fallback:** SDK volume creation failure triggers automatic fallback
  to host-dir mode with a warning (matches Python's graceful degradation).

## Testing Strategy

Unit tests (`go test ./internal/opencodemsb/`) for pure-logic modules:

- **`config_test.go`:** JSON5 parsing, deep merge, provider config injection,
  non-JSON file passthrough, empty dirs.
- **`secrets_test.go`:** SecretEntry construction, missing env var handling,
  host mapping.
- **`sysinfo_test.go`:** `/proc/meminfo` parsing, fallback behavior.
- **`image_test.go`:** Content digest truncation, Dockerfile `FROM` base-image
  detection, tag construction.
- **`worktree_test.go`:** Project slug derivation, branch slug sanitization,
  worktree path construction. (Git operations themselves are integration-level.)
- **`cmd_test.go`:** Flag parsing, default values, timing helper behavior.

**Integration tests** (require Docker + msb runtime, tagged `//go:build integration`):
- End-to-end `run` with a minimal Dockerfile (verify sandbox boots, config
  written, opencode launches).
- Volume create/remove/prefill round-trip.
- Image build + cache hit on second run (idempotency).
- Worktree add/remove lifecycle.

Integration tests are skipped in CI without the `integration` build tag. They
document expected real-world behavior and serve as the parity verification
against the Python implementation during the transition.

**Parity verification:** During transition, run the Python and Go launchers
side-by-side on the same project. Compare: sandbox name, mounts, env vars,
secrets, config files written, exit codes. The Go CLI's `--timing` flag aids
this comparison.

## Embedded Data

`data/Dockerfile` and `data/provider-config.json` are embedded via
`//go:embed data/*` in a Go source file within `internal/opencodemsb/`. The
embedded copies are byte-identical to `src/inoio_sandbox/data/` — during
transition, any change to one must be mirrored in the other.

Project-local overrides at `.sandbox/Dockerfile` and `.sandbox/opencode/` take
precedence over embedded defaults (matches Python).

## State Directory

Default: `~/.local/share/opencode-msb/` (mirrors Python's
`~/.local/share/inoio-sandbox/` but uses the new name).

Contains:
- `worktrees/{project_slug}/{branch}/` — git worktrees
- `state/{project_slug}/home/{digest}/` — fallback home volumes

## Current Limitations (inherited from Python MVP)

- `.envrc` secrets in the project directory are removed from the VM (and the
  host worktree, since it's bind-mounted) via `fs.Remove`. This is destructive
  — inherited from Python's `--rm` approach. A non-destructive masking
  solution is a future improvement.
- Network egress is unrestricted (no network rules in the MVP).
- API keys are personal; forwarded via SDK secrets so the real value never
  enters the VM.
- goenv initialization (`eval "$(goenv init -)"`) assumes goenv is installed in
  the runner image; if absent, the `eval` fails silently and opencode launches
  without goenv's PATH modifications.
