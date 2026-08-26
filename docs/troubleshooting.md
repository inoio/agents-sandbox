---
title: Troubleshooting
layout: default
nav_order: 7
---
# Troubleshooting

Common issues and how to resolve them.

## `opencode-sandbox doctor` fails

If the doctor command reports missing prerequisites:

### Docker not running

Start the Docker daemon. How to start it depends on your environment. When using Docker Desktop, start it. With Docker installed as system wide package on Linux, the normal startup procedure is to execute `sudo systemctl start docker`

You can verify afterwards by running `docker info`. It should show a healthy daemon.

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

## VM won't start

When a VM won't start, check the general troubleshooting steps first.

### "create sandbox: ..." errors

Try `--log-level verbose` to see the full error:

```console
opencode-sandbox run --log-level verbose
```

Common causes:
- Not enough system memory — reduce with `-m 2G`

## Stale sandboxes consume resources

### List and prune

```console
# See what's running
opencode-sandbox list

# Remove old resources
opencode-sandbox prune --dry-run    # preview
opencode-sandbox prune --force      # remove
```

### Stop a specific VM

```console
opencode-sandbox stop               # graceful stop
opencode-sandbox kill               # force kill
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

If you need a tool that isn't in the base image (e.g., `go`, `rustc`, `python3`), add it to your project's custom Dockerfile, see [Runner Image]({% link runner-image.md %}) documentation.

## Secrets not available

Verify the secret is set with the correct format. For legacy `env.secret`, the format is `KEY=value@host` where the
part after the **last** `@` is the host policy tag — values may contain `@`. If no `@host` part is present the
secret is dropped with a warning.

For `env.secret.yaml`, values may contain `@` without issues. Ensure the entry defines either `host`, `hosts`, or
`allow_any_host_dangerous: true` — entries with no hosts definition are silently skipped. An empty `value` is valid
and passed through unchanged.

## Memory or CPU limits too low

If opencode runs slowly or VMs fail to start with resource errors, increase allocation:

```console
opencode-sandbox run -c 4 -m 8G
```

Or set defaults in config:

```yaml
# ~/.config/opencode-sandbox/config.yaml
cpus: 4
memory: 8G
```

## Config not applying

If your config files aren't being picked up:

1. Verify the file exists in the right location:

   ```console
   ls ~/.config/opencode-sandbox/config.yaml
   ls .opencode-sandbox/config.yaml
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
   opencode-sandbox run --log-level verbose
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
    docker build -f .opencode-sandbox/Dockerfile -t test-image .
    ```
