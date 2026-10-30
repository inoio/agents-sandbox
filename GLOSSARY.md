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
