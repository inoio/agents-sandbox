---
description: How the alternative-container-engine claim is verified — the `integration-podman` CI job runs the docker-build suite against a rootless podman socket, the short-name resolution failure is the engine-side hazard the runner image avoids by qualifying its base, and the dev VM cannot run a container engine at all.
read_if: Verifying podman (or another Docker-API engine) against the integration suite; changing the `integration-podman` job, `distroBaseRef`, `baseImageRef`/`resolveBaseDigest`, or `buildFailureError`; wondering why neither docker nor podman can build in the dev VM.
---

# Testing against podman

## What is actually engine-specific

The launcher has no engine abstraction: `internal/sandbox/docker` dials whatever answers the Docker API at the
`DOCKER_HOST` / docker-context endpoint. Engines therefore differ only in the API surface they implement and in their own
registry configuration. That makes the integration suite over their socket the useful test, and the launcher's own image
references the useful contract.

## The verification path

The `integration-podman` job in `.github/workflows/ci.yml` installs podman, starts `podman system service` on a socket,
points `DOCKER_HOST` at it, and runs the same `make test-integration` as the Docker job. Gating, the
`run-integration-podman` opt-in label, and why the job stays outside `release.needs`: `specs/ci-job-gating.md`. A green
run of that job is the evidence behind the docs naming podman a **Tested** engine; without it the docs claim is
unfounded.

## Short-name resolution

The runner image's distro base is registry-qualified (`distroBaseRef` in the image package) where it is rendered, in both
the managed-base tools block and the tool base, so neither depends on the engine's `unqualified-search-registries`:

- empty list → podman refuses the name: `short-name "…" did not resolve to an alias and no unqualified search registries
  are defined`
- one or more registries → podman resolves it, or tries each, rather than failing

`short_name_mode = "permissive"` does not compensate for an empty list. Two rules keep this from overreaching:
`baseImageRef` returns the rendered FROM token **unchanged**, because only the engine knows which registry a name without
one maps to (search registries, a mirror, or a prefix rewrite) — the launcher's base-digest lookup and the build must
resolve to the same image, so re-qualifying a user's own base would send it to Docker Hub and record the wrong provenance
label. The same failure therefore still reaches a user's own project Dockerfile `FROM`; it is documented in the
troubleshooting page's build-failure section, and `buildFailureError` appends that section's link to the build error. That
link's fragment is the heading `### The base image cannot be found` in `docs/troubleshooting.md`; nothing tests the
coupling, so renaming the heading silently breaks the link the error prints.

## Why not in the dev VM

The dev VM is an unprivileged `dev`: no `sudo`, no docker binary or socket, no `newuidmap`/`newgidmap`. podman can be
fetched (a static bundle such as `mgoltzsche/podman-static`) and started inside a self-made user namespace
(`unshare -r`), which is enough to read `podman info` and to observe registry resolution — but not to build: a
single-UID mapping fails on `lchown` when unpacking any real base image (`ignore_chown_errors` is not honoured on podman
6.1.4), and buildah additionally cannot mount its build-context overlay without fuse/overlay support. Treat engine builds
as host-only, like `make test-msb`.
