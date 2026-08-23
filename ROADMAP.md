# Roadmap

This is the public, forward-looking roadmap for opencode-sandbox. It is aspirational, not a commitment – priorities
shift and items may be reordered, split, or dropped. This document is the directional view, Issues complement and
detail.

Feedback and contributions are welcome. Open an issue or start a discussion for anything you'd like to see.

Items follow [MoSCoW](https://en.wikipedia.org/wiki/MoSCoW_method) priority: **[M]** = Must, **[S]** = Should, **[C]** = Could.

- [M] **Open-Sourcing:** Finalize and publish the project, remove GitLab specifics.
- [M] **Secrets:** Hide project-local secrets from the VM. Files like `.env` / `.envrc` in a project directory are currently visible
  inside the sandbox; create a configurable mechanism (e.g. `*.secret` patterns) to keep them out of the VM while still
  keeping them in the project directory.
- [M] **Tech debt:** Review/refactor LLM-generated code.
- [S] **Opencode:** Request support for `--auto` in the opencode project for serve-and-attach workflows.
- [S] **Runtimes:** Explore supporting buildah and podman alongside Docker.
- [C] **CLI:** extend `--verbose`, `--error` with `--info`, `--warning`
- [C] **CLI:** Configurable host port for `run --serve-only` (currently fixed at `4096`).
- [C] **Opencode:** Document potentially valuable `OPENCODE_EXPERIMENTAL_*` flags that the sandbox passes through.
- [C] **Tooling:** Optional pinning/versioning of base image dependencies (currently Node.js).
- [C] **UX:** Replace the hand-rolled CLI prompt/UI with a dedicated interaction library like lipgloss or pterm.
- [C] **Testing:** Replace handwritten mocks with `testify/mock` + `mockery` where it improves clarity.
- [C] **Tech debt:** Extract mock code out of production binaries (e.g. `msb/testmock.go`, `docker/testmock.go`) into `_test.go`/testutil,
  removing `testing` from the production import graph.
- [C] **Tech debt:** Split the `reprovision` sub-package (configfiles / envstate / reconfig).
- [C] **Tech debt:** Narrow `reprovision`/`doctor` parameter types and error returns to reduce behavioral risk.

## Refactorings

- Remove dependency on termio from git package

## Bugs

- Resetting the home volume and restarting does not pick up the new volume.