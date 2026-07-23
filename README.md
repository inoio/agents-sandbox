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
opencode-msb -b my-feature      # run in an isolated git clone
opencode-msb doctor             # check prerequisites
opencode-msb build -r           # rebuild the runner image
opencode-msb list               # list running sandboxes
```

## Commands

| Command | Aliases | Purpose |
|---|---|---|
| `run` (default) | `sandbox run` | Run opencode in the sandbox VM |
| `doctor` | — | Check host prerequisites (docker, kvm, git, msb) |
| `build` | `image build` | Build or rebuild the runner image |
| `list` | `ls`, `sandbox list` | List sandboxes for this host |
| `shell` | `sandbox shell` | Start sandbox and open a shell (debug) |
| `config show` | — | Print merged opencode config (debug) |
| `image list` | `image ls` | List cached runner images |
| `volume list` | `volume ls` | List managed volumes |

Bare `opencode-msb` (or flags-only invocation) implicitly runs `run`.

## Branch sessions

By default `opencode-msb` runs in the current directory. To start an isolated
session for a different branch, use `-b`/`--branch <branch>`:

```bash
opencode-msb -b my-feature
```

Rules:

- If the current checkout is already on `<branch>`, the current directory is used.
- Otherwise the launcher creates or reuses an independent git clone under
  `~/.local/state/opencode-msb/isolated-workspaces/<project>/<branch>`.
- If `<branch>` does not exist, you are prompted whether to create it. Use
  `--yes`/`-y` to create it from `HEAD` without prompting.
- When the launcher created the managed clone, it asks after the session whether to
  keep it, remove it, or merge it back into the original branch. With `--yes`,
  the default is to remove the managed clone and keep the branch.

## Flags

### Global

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--yes` | `-y` | `false` | Assume yes to all prompts |
| `--verbose` | `-v` | `false` | Show debug-level output |
| `--quiet` | `-q` | `false` | Suppress non-error output |
| `--tree` | — | `false` | Print the full command tree and exit |
| `--version` | `-V` | `false` | Print version and exit |

### Run / Shell

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--branch` | `-b` | `""` | Isolated git clone for the given branch |
| `--cpus` | `-c` | `0` (all) | vCPUs for the sandbox |
| `--memory` | `-m` | `4G` | Memory limit (e.g. `4G`, `512M`) |
| `--rebuild` | `-r` | `false` | Rebuild the runner image before starting |

### Run only

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--dry-run` | `-n` | `false` | Validate setup without running opencode |
| `--no-auto` | — | `false` | Do not pass `--auto` to opencode |

### Build

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--rebuild` | `-r` | `false` | Force a clean rebuild |

## Project overrides

### Make project specific tools / toolchains available to OpenCode

In your project directory, you can create `.opencode-msb/Dockerfile`. opencode-msb will execute OpenCode in a VM initialized with the built Docker image.

In order for opencode to have the commands installed in the image available, extend PATH:

* for tools installed via dev user (default), via `/home/dev/.profile`, e.g. in Dockerfile, add `RUN echo 'export PATH="$PATH:/home/dev/.tool/bin"' >> /home/dev/.profile`
* for tools installed via root user, via `/etc/profile`, e.g. in Dockerfile, add `RUN echo 'export PATH="$PATH:/path/to/tool/bin"' >> /etc/profile`

### Extend VM environment

Create `.opencode-msb/env` to add environment variables, e.g.

```shell
FOO=bar
BAZ=qoox
```

These are available to opencode.

### User-level defaults

You can also put defaults in `~/.config/opencode-msb/` so they apply to every
project unless overridden:

- `~/.config/opencode-msb/env` — environment variables forwarded to every sandbox.
- `~/.config/opencode-msb/env.secret` — secret environment variables in
  `value@host` format.
- `~/.config/opencode-msb/config.*` — launcher defaults for CLI flags.

Supported config names are `config.yaml`, `config.yml`, `config.json`,
`config.jsonc`, and `config.json5`. The first one found in the directory is
used.

Example `~/.config/opencode-msb/config.yaml`:

```yaml
verbose: true
cpus: 4
memory: 8G
```

Example `.opencode-msb/config.yaml` that overrides the user default only for
this project:

```yaml
memory: 16G
rebuild: true
```

Precedence for both env files and launcher config is:

1. Built-in defaults
2. `~/.config/opencode-msb/`
3. `.opencode-msb/` — project-level values win
4. CLI flags — always win

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
