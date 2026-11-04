# agents-sandbox

A launcher that runs coding agents inside a per-project microsandbox VM, built on a composed runner image.

## Language

### Runner image composition

**Runner image**:
The per-project image agents-sandbox builds, tags per agent, and boots the sandbox from.
_Avoid_: sandbox image

**Project Dockerfile**:
The Dockerfile a project supplies to extend the runner image; only its final stage's body is replayed, and its earlier stages are preserved.
_Avoid_: user Dockerfile

**Managed base**:
A runner-image base agents-sandbox provides (`FROM agents-sandbox/runner-base`), replaced in place by the embedded `debian:trixie-slim` tools block.
_Avoid_: default base

**Custom base**:
A runner-image base the project supplies (`FROM <any other image>`); its final `FROM` becomes the base stage.
_Avoid_: user base

**Stage**:
A named build stage in the rendered runner Dockerfile. The reserved aliases (`agents-sandbox-base`, `-toolbase`, `-node`, `-agent`, `-docker`, `-runner`) are owned by the renderer; a project declaring one is a hard error.
_Avoid_: layer

**Tool stage**:
A stage agents-sandbox owns that installs one tool (node, agent, docker) into the tool artifact, merged into the runner with `COPY`.
_Avoid_: tool layer

**Tool base**:
The internal stage (`agents-sandbox-toolbase`) the tool stages build on, carrying only what the tool downloads need (`curl`, `ca-certificates`, `tar`). It is not part of the runner's lineage.
_Avoid_: fetch base

**Tool artifact**:
The `/opt/agents-sandbox` tree a tool stage produces and the runner merges; binaries are exposed through `/opt/agents-sandbox/bin`, which the runner appends to `PATH`.
_Avoid_: tool dir

**Adoption**:
The post-body step that records each tool's provenance and, for docker, checks the runtime prerequisites. It runs after the project body, so a base binary keeps precedence.
_Avoid_: finalize

**Provenance**:
The `agent-source`/`docker-source` files (`tool` or `user`) recording whether a running tool came from the tool stage or the base.

### Project and run lifecycle

**Repo slug**:
The `<folder>-<hash>` identifier derived from a project's origin remote URL; it names the per-slug user config directory and the sandbox grouping label, and every clone of one remote shares it.
_Avoid_: project slug

**Checkout slug**:
The `<folder>-<hash>` identifier derived from a checkout's git common directory, or from the worktree root for a worktree outside its checkout; it names the checkout's state directory, project VM, home volume, locks, and runner-image tag.
_Avoid_: project slug; state slug

**Checkout**:
A clone together with the worktrees that share its git common directory, so they share one checkout slug, state, VM, and home volume. A worktree outside its checkout is its own checkout.
_Avoid_: workspace

**Home volume**:
The persistent volume mounted at `/home/dev` that carries the agent's credentials and files across project VM recreation; its name is recorded in the project state.
_Avoid_: user volume

**Project VM**:
The per-checkout, per-agent microVM the agent runs in, recreated from the runner image when needed; the user-facing docs call it the sandbox.
_Avoid_: container; sandbox VM

**Task sandbox**:
The short-lived sandbox a volume operation starts to prefill, copy, or edit a home volume; it is never a project VM and never appears in `sandbox list`.
_Avoid_: temp VM

**Legacy identity**:
A checkout's pre-split identity — the origin-derived repo slug that was its only slug before the repo and checkout identities were separated, plus the path-derived slug a bare root or non-repo folder records.
_Avoid_: old slug

**Identity adoption**:
The automatic, noticed, once-only migration to a project's checkout slug: it destroys the legacy project VM first, then merges the state directory into the checkout location, preserves the existing home volume and its name, and rebuilds the runner image under the checkout-scoped tag. A second clone adopting the same shared legacy VM claims the volume and starts with a fresh one instead.
_Avoid_: migration

**Session**:
The overloaded word for an agent's time in a sandbox; the code uses it for three distinct concepts — a prepared sandbox, an attach session, and a client lease. Prefer the specific term.
_Avoid_: session (unqualified)

**Prepared sandbox**:
The ready-to-attach result of a run's or shell's preparation: the project VM and the target the agent attaches to.
_Avoid_: prepared session

**Attach session**:
One client's interactive connection to a project VM: the agent's own interface for `run`, or a login shell for `shell`.
_Avoid_: client session

**Client lease**:
The per-client lock under the project state that records one attached client; when the last client releases it, the VM is reaped according to policy.
_Avoid_: client lock
