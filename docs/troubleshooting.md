---
title: Troubleshooting
description: "Fixes for common agents-sandbox problems, such as failing doctor checks, Docker and microsandbox runtime issues."
layout: default
parent: Reference
nav_order: 20
---
# Troubleshooting

Fixes for the problems you are most likely to hit while installing or running agents-sandbox.

> **Note:** Run `agents-sandbox doctor` first. It checks the host prerequisites and reports what is missing; the
> sections on this page cover the cases it cannot resolve on its own.

## `agents-sandbox doctor` fails

If the doctor command reports missing prerequisites:

### Docker not running

Start your container engine. agents-sandbox talks to it over the Docker API, so what matters is that the engine serves
that API, not which vendor provides it:

- **Tested** — **Docker Engine**, **Docker Desktop**, **colima**, **podman**.
- **Expected to work, but untested** — Podman Desktop and macOS podman machines, Rancher Desktop, OrbStack, Lima, and
  any other engine that exposes a Docker-compatible socket.
- **Not supported** — an engine with no Docker API, such as bare containerd with `nerdctl`, or colima started with its
  `containerd` runtime.

podman only: one podman setting can stop a build that works everywhere else, see
[The base image cannot be found](#the-base-image-cannot-be-found).

How you start the engine depends on which one you use: Docker Desktop starts from Applications, and a Docker Engine
installed as a system-wide package on Linux starts with `sudo systemctl start docker`.

You can verify afterwards by running `docker info`. It should show a healthy daemon.

The launcher finds the Docker endpoint the same way the `docker` CLI does: `DOCKER_HOST` first, then the active docker
context (`docker context ls`), then the default `/var/run/docker.sock`. An engine that publishes its socket through a
docker context, such as colima or Docker Desktop, needs no extra configuration; point `DOCKER_HOST` at an engine that
publishes none.

### Microsandbox runtime

agents-sandbox provisions its own private microsandbox runtime under its data
directory (`~/.local/share/agents-sandbox/msb`, or
`$XDG_DATA_HOME/agents-sandbox/msb`) and points the SDK at it. The runtime never
reads or writes your own `~/.microsandbox`; the one-time upgrade migration reads
it only to copy your old volumes (see [Upgrading to 0.6]({% link install/upgrading.md %})).
It does not install a user-facing `msb` command, and does not require any
particular `msb` version on your machine. If agents-sandbox is the only thing
installed, the absence of an `msb` command is expected, not a defect.

If you encounter a runtime error, submit a bug report at:

<https://github.com/inoio/agents-sandbox/issues/new?template=bug_report.yml>

### Legacy home volumes after upgrading

For the upgrade overview — what changed, the migration choices, and how to retire
`~/.microsandbox` — see [Upgrading to 0.6]({% link install/upgrading.md %}). See
[Private volumes with unclear status](#private-volumes-with-unclear-status) for diagnosing an interrupted or unclear copy.

An interrupted copy resumes where it stopped, because progress is tracked in
agents-sandbox's host state. If a private volume already exists under a legacy
name — for example after `~/.local/state/agents-sandbox/state.yaml` was removed —
its status is unclear; [Private volumes with unclear status](#private-volumes-with-unclear-status) covers that case.

#### Private volumes with unclear status

The offer says so, cannot tell whether such a volume is a complete copy or a
partial one, and gives two options:

- **Copy the missing volumes; dismiss the existing private volumes from the
  migration** — copies only the absent volumes and leaves the existing ones
  untouched and never asked about again.
- **Quit** — if you want a specific volume fully migrated, remove its private
  copy with `agents-sandbox volume remove --force <name>` (the copy is still
  referenced by its project's state, hence `--force`), then run again: the next
  offer will re-copy it from the legacy home. Keep the copies you do not want
  migrated.

### KVM unavailable

```console
# Check if /dev/kvm exists
ls -la /dev/kvm
```

If missing, enable virtualization in your system BIOS/UEFI (INTEL-VT, AMD-V or similar) and ensure your user is in the `kvm` group:

```console
sudo usermod -aG kvm "$USER"
```

Log out and back in for the group change to take effect.

## macOS-specific issues

### Not Apple Silicon

The doctor command will exit with an error if you are running the x86_64 binary under Rosetta 2. Download the `darwin-arm64` binary instead. To check your current architecture:

```console
uname -m
```

Expected output on supported hardware: `arm64`.

### Docker socket not found

On macOS the engine runs inside a VM, so the socket comes from whichever engine you started. Verify it is running:

```console
docker info
```

If Docker Desktop is installed but not running, launch it from Applications. If using colima:

```console
colima start
```

agents-sandbox finds the socket the same way the `docker` CLI does: `DOCKER_HOST` wins, then the active
docker context, then the default `/var/run/docker.sock`. If `docker info` works but agents-sandbox reports
the socket as unreachable, check which context is active and where it points:

```console
docker context ls
```

A context marked `*` that points at a stopped provider explains the failure — switch it with
`docker context use <name>`, or point agents-sandbox at the socket directly:

```console
export DOCKER_HOST=unix://$HOME/.colima/default/docker.sock
```

Note that `DOCKER_HOST` takes precedence over the active context, so an outdated value in your shell
profile overrides a correct context.

## VM won't start

When a VM won't start, check the general troubleshooting steps first.

### "create sandbox: ..." errors

Try `--log-level verbose` to see the full error:

```console
agents-sandbox run --log-level verbose
```

Common causes:
- Not enough system memory — reduce with `-m 2G`

## State file errors

agents-sandbox keeps host state in YAML files under `~/.local/state/agents-sandbox/`: one launcher-global `state.yaml`
and one `state.yaml` per project and agent. It never overwrites a state file it cannot read.

### "state file was written by a newer agents-sandbox"

A newer agents-sandbox wrote the file with a schema this version does not know. To keep that version's data intact,
this version refuses to change the file. Upgrade with `agents-sandbox upgrade`, or run the newer binary.

### "parse root state file ... run `agents-sandbox state repair`"

The launcher-global `state.yaml` is corrupt, which blocks update checks, the runtime migration, and identity adoption.
Run [`agents-sandbox state repair`]({% link commands.md %}#state-repair) to move it aside as
`state.yaml.corrupt-<timestamp>`. The next run starts from a fresh launcher state.

## Stale sandboxes consume resources

A sandbox that is no longer in use is stopped by its idle policy and reclaimed by auto-pruning. To list, stop, or
reclaim sandboxes by hand, see [Sandboxes]({% link sandboxes.md %}) for the `list`, `stop`, `kill`, and `prune` commands.

## Worktree session issues

### "failed to create worktree"

If the managed worktree creation fails:

```console
# Clean up stale worktrees in your repo
git worktree prune
```

Then retry the command. If the problem persists, check for uncommitted changes or locked worktrees:

```console
git worktree list
```

### Worktree prompt hangs

If the worktree creation prompt doesn't respond, check that the repository has a remote configured:

```console
git remote -v
```

Without a remote, some git operations may hang. Add one or use an absolute branch name:

```console
git remote add origin https://example.com/repo.git
```

## Missing tools inside the sandbox

If you need a tool that isn't in the base image (e.g., `go`, `rustc`, `python3`), add it to your project's custom Dockerfile, see [Runner Image]({% link runner-image.md %}) documentation.

## Secrets not available

Verify the secret is set with the correct format. For legacy `env.secret`, the format is `KEY=value@host` where the
part after the **last** `@` is the host policy tag — values may contain `@`. If no `@host` part is present the
secret is dropped with a warning.

For `env.secret.yaml`, values may contain `@` without issues. Ensure the entry defines either `host`, `hosts`, or
`allow_any_host_dangerous: true` — entries with no hosts definition are silently skipped. An empty `value` is valid
and passed through unchanged.

## TLS certificate errors for one application

Once a secret is configured, requests to the matching hosts go through microsandbox's TLS proxy, whose interception CA
the guest system store trusts. An application with its own trust store does not, and fails with a certificate-path
error — Java reports
`SunCertPathBuilderException: unable to find valid certification path to requested target`. Import the CA into that
store with the `msb-trust` helper from the runner image:

```console
agents-sandbox shell --root
msb-trust java
```

See [Trusting the CA in applications]({% link configuration/secrets.md %}) for which applications already work, and the
[Java recipe]({% link recipes/java-ca-trust.md %}) for the boot hook that repeats the import after a recreate.

## Memory or CPU limits too low

If the agent runs slowly or VMs fail to start with resource errors, increase allocation:

```console
agents-sandbox run -c 4 -m 8G
```

Or set defaults in config:

```yaml
# ~/.config/agents-sandbox/config.yaml
cpus: 4
memory: 8G
```

## Config not applying

If your config files aren't being picked up:

1. Verify the file exists in the right location:

   ```console
   ls ~/.config/agents-sandbox/config.yaml
   ls .agents-sandbox/config.yaml
   ```

2. Check valid syntax:

   ```console
   # For YAML files
   python3 -c "import yaml; yaml.safe_load(open('config.yaml'))"
   
   # For JSON/JSONC files
   python3 -c "import json; json.load(open('config.json'))"
   ```

3. Check that CLI flags aren't overriding your config (flags always win).

4. Use `--log-level verbose` to see which config files were loaded:

   ```console
   agents-sandbox run --log-level verbose
   ```

## Image build fails

Docker build failures are usually due to:

1. **Network issues** — The base image downloads packages from the internet. Ensure outbound access:

    ```console
    curl -fsSL https://debian.org | head -c 100
    ```

2. **Docker daemon memory** — Large builds may need more memory:

    ```console
    docker info | grep "Total Memory"
    ```

3. **Custom Dockerfile errors** — If using a project Dockerfile, build it manually to isolate the issue:

    ```console
    docker build -f .agents-sandbox/Dockerfile -t test-image .
    ```

If an older image or updater state reports `Release vuser-provided not found`, update agents-sandbox and rebuild the
runner image. `user-provided` identifies an agent that was previously found in a custom base; it is not a valid release
version. A rebuilt image will keep an agent supplied by the base, or install a real resolved release if that binary is
no longer present.

### The base image cannot be found

If the build fails before it runs any of your steps because the engine could not find an image named in a `FROM`, the
engine is being asked to resolve a name with no registry in it. agents-sandbox pulls its own base under its full name
(`docker.io/library/debian:trixie-slim`), so that name comes from your project Dockerfile or from a base it starts from.
Docker Engine assumes Docker Hub and never fails this way; podman does when it has no search registries:

```console
Error: short-name "debian:trixie-slim" did not resolve to an alias and no unqualified search
registries are defined in "/etc/containers/registries.conf"
```

Either fix works: name the registry in the `FROM`, such as `FROM docker.io/library/debian:trixie-slim`, or tell podman
which registry to search by adding it to `registries.conf`:

```toml
unqualified-search-registries = ["docker.io"]
```

`short_name_mode = "permissive"` on its own does not help while that list is empty.

When the build reports `base image not found or not logged in` for a name that already carries a registry, the registry
rejected the pull instead: the image does not exist, it is private, or the engine is not logged in. Fix the name and
retry, or log in to that registry (`docker login <registry>`) before building again.
