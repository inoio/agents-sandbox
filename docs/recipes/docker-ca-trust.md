---
title: Trust the Sandbox CA in Docker
description: "Make an in-VM docker build or docker run container trust the microsandbox interception CA, so HTTPS from inside a container succeeds."
layout: default
parent: Recipes
nav_order: 60
---

# Trust the Sandbox CA in Docker

Inside the sandbox, a browser or a command-line tool that uses the system trust store already trusts the interception
CA. A container does not: it carries its own root store, so an HTTPS download from inside a `docker build` or
`docker run` fails certificate verification. This recipe makes that container trust the CA.

> **Note:** The `Dockerfile` snippets in this recipe add the microsandbox CA to the container's bundle. Everything here
> applies when interception is active — when a [secret]({% link configuration/secrets.md %}) is configured,
> `network.intercept-tls` is `on`, or a `network.tls-bypass` is configured (which activates interception under `auto`
> for every host except the bypass).

## No step is needed for the daemon

A base-image pull already succeeds with no setup. microsandbox installs its CA into the VM's system store when the
sandbox boots, and the in-VM dockerd reads that store, so `docker pull` verifies the registry's certificate. Only the
container — not the daemon — needs the CA added.

## In a build

Copy the CA out of the sandbox into the build context, then add it to the container's bundle before any step that
reaches HTTPS. Run the copy from a shell in the sandbox:

```console
$ cp /usr/local/share/ca-certificates/microsandbox-ca.crt ./microsandbox-ca.crt
```

### Alpine

The Alpine base ships the bundle but no `update-ca-certificates`, so append the CA to the bundle directly:

```dockerfile
# .agents-sandbox/Dockerfile
FROM alpine:3.20
COPY microsandbox-ca.crt /usr/local/share/ca-certificates/microsandbox-ca.crt
RUN cat /usr/local/share/ca-certificates/microsandbox-ca.crt >> /etc/ssl/certs/ca-certificates.crt
```

### Debian and Ubuntu

`update-ca-certificates` rebuilds the bundle from `/usr/local/share/ca-certificates/`. apt's default sources are `http`,
so the install step itself is not intercepted:

```dockerfile
# .agents-sandbox/Dockerfile
FROM debian:trixie-slim
COPY microsandbox-ca.crt /usr/local/share/ca-certificates/microsandbox-ca.crt
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
 && update-ca-certificates \
 && rm -rf /var/lib/apt/lists/*
```

Place the `COPY` and the bundle step ahead of every other `RUN` that reaches HTTPS, so the container trusts the CA by
the time it makes its first request.

## In `docker run`

A container started with `docker run` has the same problem. Mount the CA read-only and add it to the bundle at entry:

```console
$ docker run --rm \
    -v /usr/local/share/ca-certificates/microsandbox-ca.crt:/microsandbox-ca.crt:ro \
    alpine:3.20 \
    sh -c 'cat /microsandbox-ca.crt >> /etc/ssl/certs/ca-certificates.crt && your-command'
```

Alternatively bake a derived image with the `Dockerfile` from the build section and run that, so the container trusts the
CA on every start without the mount.

## Related

- To skip interception for a host instead — for example a registry whose client you cannot reconfigure — use
  `network.tls-bypass` in the [Networking]({% link configuration/networking.md %}) configuration. A bypassed host is
  still denied under the runtime's strict mode unless you also set `network.disable-strict: true`.
- The [Java recipe]({% link recipes/java-ca-trust.md %}) covers a JVM's own trust store, a similar case where the
  application does not read the system store.
