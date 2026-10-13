# Spike: BuildKit VM (issue #15)

**Throwaway feasibility spike — not production code.** Separate Go module.

## Idea

Unify on **one build mechanism for every platform**: run `buildkitd` inside a
microsandbox (msb) VM. This is the only option that works on macOS (BuildKit
needs a Linux kernel), and using it on Linux too keeps a single code path with
no Docker/Podman on the host.

```
host launcher ──BuildKit gRPC over published TCP──▶ msb VM (buildkitd)
     │  streams build context up (session)                 │
     └◀─ streams exported OCI/docker archive down ─────────┘
                                   cache persists in a named msb volume
```

The BuildKit **client** runs on the host, so the context and the output archive
stream over the session — nothing needs to be bind-mounted into the VM. The
builder sandbox only needs to exist while building; `buildkitd`'s cache lives in
a named msb volume mounted at `/var/lib/buildkit` so layers survive across
ephemeral builder VMs.

## Run (needs a KVM host)

`/dev/kvm` is unavailable inside the agents-sandbox dev VM, so this must run on
real Linux (or macOS) hardware.

```sh
cd spike/buildkit-vm
go build -o bkvmspike .

# Default transport: buildctl inside the VM over bind mounts.
./bkvmspike -scratch

# Probe the host-side gRPC client path over the published TCP port:
./bkvmspike -scratch -transport grpc

# Real base image (exercises registry egress), keep the sandbox to poke at it:
./bkvmspike -image moby/buildkit:v0.25.2 -keep
```

Flags: `-transport buildctl|grpc`, `-image`, `-guest-port`, `-host-port`,
`-exporter oci|docker`, `-out`, `-scratch`, `-msb-load` (default true),
`-keep`, `-cache-volume`, `-handshake-timeout` (default 6s),
`-ready-timeout` (default 30s).

Transports:

- **`buildctl`** (default): `buildctl` runs inside the VM against the bind-mounted
  `/in` (context, ro) and `/out` (output, rw); no network gRPC, so it sidesteps
  the published-port question entirely.
- **`grpc`**: the host-side BuildKit client streams context up / archive down
  over the published TCP port. It first does a short TCP probe, then a bounded
  dial so a dead port fails fast instead of hanging. Solve progress (build log
  lines and `CACHED` vertices) is now printed as `[step] …`.
- **`diagnose`**: connect to an **existing** BuildKit host and replay the
  launcher's exact pre-build sequence with a per-step timeout, naming the step
  that hangs. Use it against a wedged launcher build:

  ```sh
  go build -o bkvmspike .

  # Derive the host port from a running builder sandbox and tail its log:
  ./bkvmspike -transport diagnose -attach agents-sandbox-buildkit-v0.25.2

  # Or point at the port directly:
  ./bkvmspike -transport diagnose -addr tcp://127.0.0.1:11234

  # Pick the base ref the launcher is resolving:
  ./bkvmspike -transport diagnose -attach agents-sandbox-buildkit-v0.25.2 -base docker.io/library/debian:trixie-slim
  ```

  Steps run in launcher order: `ListWorkers` → `ResolveImageConfig`
  (the pre-build `resolveBaseDigest` gateway call) → `Solve`. Each prints
  `-> …` / `ok … (duration)` / `!! … HUNG — no result within <step-timeout>`.
  With `-attach`, buildkitd's in-VM log is tailed as `[buildkitd.log] …`;
  client build steps print as `[step] …`.

  This matters because a `buildkitd.log` that stops at `running server` means the
  launcher never issued a `Solve` — the freeze is before the build starts, so the
  working `grpc` transport (which skips `ResolveImageConfig`) does not reproduce
  it. `diagnose` does.

  `-attach` handles a **Stopped** builder (e.g. reaped by its idle timeout):
  it `ConnectOrStart`s the sandbox, starts `buildkitd` if the fresh boot has
  none, and holds the VM's idle timer open (`Touch` every 2s, matching the
  launcher's keep-alive) for the whole run so the reaper cannot fake a hang. Any
  `Touch` failure is printed. It finds the published host port from the sandbox
  config, and if that does not carry the binding, probes the `-host-port` range
  for a live BuildKit endpoint (or pass `-addr` explicitly). If `diagnose`
  completes every step but the launcher still hangs, the difference is the
  launcher-side idle/keep-alive handling, not BuildKit.

Flags: `-transport buildctl|grpc|diagnose`, `-image`, `-guest-port`,
`-host-port`, `-exporter oci|docker`, `-out`, `-scratch`, `-msb-load` (default
true), `-keep`, `-cache-volume`, `-handshake-timeout` (default 6s),
`-ready-timeout` (default 30s); diagnose: `-addr`, `-attach`, `-base`
(default `debian:trixie-slim`), `-step-timeout` (default 20s),
`-buildkitd-log` (default `/var/lib/buildkit/buildkitd.log`).

The sandbox is created from the BuildKit image, then the spike **starts
`buildkitd` explicitly** via `ShellStream` (msb does not auto-run a long-lived
service from the image entrypoint) and mirrors its output to stderr as
`[buildkitd] …`. If readiness or the solve fails, it dumps `sb.Logs` plus
`ps -ef` / `ls /var/lib/buildkit` to help diagnose.

## Things most likely to need adjustment

1. **TLS interception.** msb injects an inspection CA for egress. A real
   `FROM alpine` build may fail TLS to the registry unless you either trust the
   CA inside the builder or bypass MITM for registry domains via
   `microsandbox.NetworkConfig.TLS.Bypass` (e.g. `*.docker.io`,
   `production.cloudflare.docker.com`, `ghcr.io`). `-image ... -scratch` avoids
   this by not pulling. **This is the main unknown to settle.**
2. **Snapshotter.** `--oci-worker-snapshotter native` is used to avoid
   overlayfs/`fuse` requirements inside the VM; if overlayfs works, switching to
   `auto` is faster.
3. **Root vs rootless image.** `moby/buildkit:v0.25.2` runs `buildkitd` as root
   inside the microVM (expected to work). The `-rootless` image variant needs
   user namespaces inside the VM and is unlikely to be necessary.
4. **Cache volume permissions.** If buildkitd cannot write `/var/lib/buildkit`,
   drop the mount to test without persistence, then adjust ownership.

## Residual unknowns (need real hardware)

- Does the msb VM let `buildkitd` run its `runc` build steps (nested containers
  inside the microVM)? Expected yes — the microVM is a full Linux kernel — but
  unverified.
- TLS-egress behaviour for registry pulls (item 1).
- Cross-run cache reuse through the named volume.
- Does `microsandbox.Image.Load` accept the archive end-to-end (SDK documents
  both docker-save and OCI layouts; unverified here).
