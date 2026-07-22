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
opencode-msb                    # run opencode in a microsandbox VM
opencode-msb --branch my-feature  # run in an isolated git clone
opencode-msb doctor             # check prerequisites
opencode-msb run --branch my-feature  # explicit run subcommand
```

## Branch sessions

By default `opencode-msb` runs in the current directory. To start an isolated
session for a different branch, use `--branch <branch>`:

```bash
opencode-msb --branch my-feature
```

Rules:

- If the current checkout is already on `<branch>`, the current directory is used.
- Otherwise the launcher creates or reuses an independent git clone under
  `~/.local/share/opencode-msb/worktrees/<project>/<branch>`.
- If `<branch>` does not exist, you are prompted whether to create it. Use
  `--yes` to create it from `HEAD` without prompting.
- When the launcher created the managed clone, it asks after the session whether to
  keep it, remove it, or merge it back into the original branch. With `--yes`,
  the default is to remove the managed clone and keep the branch.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--branch` | `""` | run in an isolated git clone for the given branch |
| `--yes` / `-y` | `false` | do not prompt; use default actions |
| `--image-rebuild` | `false` | force rebuild of the runner image |
| `--volume-fallback` | `false` | use project-local dirs instead of msb volumes |
| `--reset-home` | `false` | wipe the opencode home volume before run |
| `--cpus` | host CPU count | vCPUs for the sandbox |
| `--memory` | `4G` | memory limit (e.g. `4G`, `512M`) |

## Project overrides

Create `.opencode-msb/Dockerfile` to override the runner image.
Create `.opencode-msb/env` to add environment variables.

## Extending the runner image

The launcher builds and runs a base runner image that contains:

- `opencode` (symlinked to `/usr/local/bin/opencode`)
- Node.js 22
- common CLI tooling: `git`, `ripgrep`, `jq`, `yq`, `curl`, `wget`, `xz-utils`, `file`, `gawk`, `less`, `lz4`, `moreutils`, `net-tools`, `nmap`, `parallel`, `recode`, `uuid`
- a `dev` user with `HOME=/home/dev`

Projects that need additional tooling can provide a `.opencode-msb/Dockerfile` starting from the base image:
```dockerfile
FROM opencode-msb/runner:base
...
```

The resulting image must define `USER dev`. If you need to run commands as root, use this pattern:

```dockerfile
FROM opencode-msb/runner:base

USER root
# do stuff as root
USER dev
# do stuff as dev
```

The launcher does not inject image-specific environment variables or run initialization commands. Anything the project image needs on `PATH` must be configured inside the image itself (for example by symlinking binaries into `/usr/local/bin` or by adding a script under `/etc/profile.d/`).
