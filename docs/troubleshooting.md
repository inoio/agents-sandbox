# Troubleshooting

Common issues and how to resolve them.

## `opencode-msb doctor` fails

If the doctor command reports missing prerequisites:

### Docker not running

```console
# Start the Docker daemon
sudo systemctl start docker        # rootful
 dockerd &                          # rootless (current user)
```

Verify: `docker info` should show a healthy daemon.

### KVM unavailable

```console
# Check if /dev/kvm exists
ls -la /dev/kvm
```

If missing, enable KVM in your system BIOS/UEFI or ensure your user is in the `kvm` group:

```console
sudo usermod -aG $USER kvm
```

Log out and back in for the group change to take effect.

## macOS-specific issues

### Not Apple Silicon

The doctor command will exit with an error if you are running the x86_64 binary under Rosetta 2. Download the `darwin-arm64` binary instead. To check your current architecture:

```
uname -m
```

Expected output on supported hardware: `arm64`.

### Docker socket not found

On macOS, the Docker socket is managed by Docker Desktop or colima. Verify it is running:

```
docker info
```

If Docker Desktop is installed but not running, launch it from Applications. If using colima:

```
colima start
```

### KVM not available

KVM is a Linux-only feature. On macOS, the microsandbox runtime uses the Hypervisor.framework instead. The doctor command skips the KVM check on macOS — this is expected.

### msb not found

```console
# Install microsandbox CLI
curl -fsSL https://github.com/superradcompany/microsandbox/releases/latest/download/install.sh | sh
```

This installs msb to `~/.microsandbox/bin`. Add it to your PATH if needed:

```console
echo 'export PATH="$PATH:$HOME/.microsandbox/bin"' >> ~/.bashrc
```

## VM won't start

### "cannot connect to Docker daemon"

Ensure Docker is running and your user has access:

```console
docker run --rm hello-world
```

### "create sandbox: ..." errors

Try `--verbose` to see the full error:

```console
opencode-msb run --verbose
```

Common causes:
- Not enough memory — reduce with `-m 2G` or `-c 2`
- Port conflicts — the sandbox uses internal networking, usually not an issue

## Stale sandboxes consume resources

### List and prune

```console
# See what's running
opencode-msb list

# Remove old resources
opencode-msb prune --dry-run    # preview
opencode-msb prune --force      # remove
```

### Stop a specific VM

```console
opencode-msb stop               # graceful stop
opencode-msb kill               # force kill
```

## Branch session issues

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

### Branch prompt hangs

If the branch creation prompt doesn't respond, check that the repository has a remote configured:

```console
git remote -v
```

Without a remote, some git operations may hang. Add one or use an absolute branch name:

```console
git remote add origin https://example.com/repo.git
```

## Missing tools inside the sandbox

If you need a tool that isn't in the base image (e.g., `go`, `rustc`, `python3`), add it to your project's custom Dockerfile:

```dockerfile
# .opencode-msb/Dockerfile
FROM opencode-msb/runner:base

USER root
RUN apt-get update && apt-get install -y python3 && rm -rf /var/lib/apt/lists/*
USER dev
```

Then rebuild:

```console
opencode-msb build -r
```

## Secrets not available

Verify the secret is set in `env.secret` with the correct format:

```shell
# .opencode-msb/env.secret
MY_SECRET=secretvalue@microsandbox
```

Format is `KEY=value@host`. If the `@host` part is missing, the secret won't be set. To debug:

```console
# Check env.secret syntax
cat .opencode-msb/env.secret
```

Note: `.envrc` files in the project directory are automatically removed from the VM. Migrate any secrets from `.envrc` to `.opencode-msb/env.secret`.

## Memory or CPU limits too low

If opencode runs slowly or VMs fail to start with resource errors, increase allocation:

```console
opencode-msb run -c 4 -m 8G
```

Or set defaults in config:

```yaml
# ~/.config/opencode-msb/config.yaml
cpus: 4
memory: 8G
```

## Config not applying

If your config file isn't being picked up:

1. Verify the file exists in the right location:

   ```console
   ls ~/.config/opencode-msb/config.yaml
   ls .opencode-msb/config.yaml
   ```

2. Check valid syntax:

   ```console
   # For YAML files
   python3 -c "import yaml; yaml.safe_load(open('config.yaml'))"
   
   # For JSON/JSONC files
   python3 -c "import json; json.load(open('config.json'))"
   ```

3. Check that CLI flags aren't overriding your config (flags always win).

4. Use `--verbose` to see which config files were loaded:

   ```console
   opencode-msb run --verbose
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
    docker build -f .opencode-msb/Dockerfile -t test-image .
    ```

## Corrupted state file

If the state file at `~/.local/state/opencode-msb/{slug}/state.yaml` is corrupted
or missing, opencode-msb will warn and create a fresh home volume.

To recover: manually remove the state directory:

    rm -rf ~/.local/state/opencode-msb/{slug}/

The next `opencode-msb run` will create a fresh home volume.

## No home volume found

If you see errors about an existing home volume not being found, the volume may have
been deleted externally. The next `opencode-msb run` will create a fresh volume and
warn you about it.
