---
title: Deprecations
description: "Deprecation policy and the register of every deprecated flag, config key, environment variable, and Dockerfile spelling, with its replacement and removal target."
layout: default
parent: Reference
nav_order: 30
---
# Deprecations

agents-sandbox is pre-1.0 but holds a high UX bar: a renamed flag, config key, environment variable, or command keeps
working with a one-time warning before it is removed.

**How a deprecation behaves:**

- A renamed flag, config key, or environment variable still works with **lower precedence** than its replacement and
  prints a **one-time warning** to stderr.
- The **replacement always wins**: a deprecated form resolves only when its replacement is unset. Setting both is not an
  error.
- A deprecated project-Dockerfile spelling has no replacement to resolve against, so it warns on **every** ensure that
  uses the Dockerfile (see [Project Dockerfiles](#project-dockerfiles)).
- Deprecated forms are **hidden from `--help`** but still honored.
- A deprecated form survives **at least one minor release** after the release that introduces it, and its removal is
  announced in the release notes. A removal only lands in a minor release, never a patch.

This list is the current register. It is also recorded in the `Deprecated` section of the
[changelog](https://github.com/inoio/agents-sandbox/blob/main/CHANGELOG.md).

| Deprecated form | Replacement | Eligible removal |
|---|---|---|
| `--dind` flag, `dind` config key, `AGENTS_SANDBOX_DIND` | `--docker`, `docker`, `AGENTS_SANDBOX_DOCKER` | 0.8.0 |
| `agents-sandbox/runner-base-dind` base image | `agents-sandbox/runner-base-docker` | 0.8.0 |
| `# agents-sandbox:dind` Dockerfile marker | `# agents-sandbox:docker` | 0.8.0 |
| `OPENCODE_SANDBOX_*` environment variables | `AGENTS_SANDBOX_*` | 0.8.0 |
| `sandbox list --names` | global `-q` / `--quiet` | 0.8.0 |
| `prune --age` (and `-a`) | `--older-than` | 0.8.0 |
| `volume migrate` / `reset` / `edit` `--rebuild` | run `agents-sandbox build` first | 0.8.0 |
| `--opencode-version` | `--agent-version` | 0.7.0 |

The removal column is an **eligibility floor, not a promise**: a removed form only disappears in a minor release, never a
patch.

## Retiring a form

A **rename** is shimmed: the old flag, config key, environment variable, or subcommand stays as an alias of the new one
for at least one minor release. A rename is never a silent break.

A **clean break** is only acceptable for a change of *default behaviour*, where no alias can carry the old meaning. Such
a change is documented as breaking in the changelog and release notes rather than shimmed — for example, the prune
commands began confirming by default in 0.6.0, with `--yes` as the escape.

A user-facing flag, config key, environment variable, or command is **never removed outright**: it is deprecated first. A
change that drops one without a deprecation cycle is not accepted.

## Project Dockerfiles

A project Dockerfile enables the Docker engine by itself: a final-stage `# agents-sandbox:docker` marker or a `FROM
agents-sandbox/runner-base-docker` base turns it on with no launcher config, for a custom base and a managed base alike.
Explicit `docker` config, `--docker`, or `AGENTS_SANDBOX_DOCKER` overrides that declaration in both directions, including
turning the engine off when the Dockerfile asks for it. See [Runner Image]({% link runner-image.md %}#docker-in-docker)
for the precedence and the marker's final-stage scope.

The legacy `dind` spellings still work as deprecated aliases. A project Dockerfile can reference the managed docker base
or place the injection marker with the legacy `dind` spelling; agents-sandbox accepts both, enables the engine, and warns
whenever the project Dockerfile is used to ensure the runner image (including when the cached image is reused). Prefer
the `docker` spelling:

```dockerfile
FROM agents-sandbox/runner-base-docker
# agents-sandbox:docker
```

See [Runner Image]({% link runner-image.md %}) for the base references and the marker.
