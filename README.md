# opencode-msb

Run opencode inside an ephemeral microsandbox VM.

## Install

Download the latest Linux binary from [GitLab Releases](https://gitlab.inoio.de/inoio/opencode-msb/-/releases):

```bash
curl -fsSL -o opencode-msb "https://gitlab.inoio.de/inoio/opencode-msb/-/releases/latest/download/opencode-msb-linux-amd64"
chmod +x opencode-msb && sudo mv opencode-msb /usr/local/bin/
```

Or install from source (requires Go + CGO toolchain):

```bash
export GOPRIVATE=gitlab.inoio.de
go install gitlab.inoio.de/inoio/opencode-msb/cmd/opencode-msb@latest
```

## Usage

```bash
opencode-msb                    # run opencode in a sandbox
opencode-msb --worktree my-feature  # run in a named git worktree
opencode-msb doctor             # check prerequisites
opencode-msb run --worktree my-feature  # explicit run subcommand
```

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--worktree` | `""` | create/use a git worktree by that name |
| `--image-rebuild` | `false` | force rebuild of the runner image |
| `--volume-fallback` | `false` | use project-local dirs instead of msb volumes |
| `--reset-home` | `false` | wipe the opencode home volume before run |
| `--cpus` | host CPU count | vCPUs for the sandbox |
| `--memory` | `4G` | memory limit (e.g. `4G`, `512M`) |
| `--timing` | `false` | print per-phase launcher timing to stderr |

## Project overrides

Create `.sandbox/Dockerfile` to override the runner image.
Create `.sandbox/env` to add environment variables.
