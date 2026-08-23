# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Version tags use a `v` prefix (e.g. `v0.1.0`); the `version`
command reports the bare version (e.g. `0.1.0`).

## [Unreleased]

### Added

### Changed

- Moved sandbox-VM listing (`ListSandboxes`/`Info`/`ListOption`) from `session` to `internal/sandbox/sandbox`, and moved runner-image building to `image.Build`, so the `cmd` layer talks to the owning packages directly.
- Split the former `session` package: sandbox setup/lifecycle (VM provisioning, upgrade, worktrees, daemon, stop/kill, `ExitError`) moved to `internal/sandbox/sandbox`, leaving `session` scoped to run/shell attach, serve, and reap. `cmd` now imports both `sandbox` and `session`.

## [0.1.0] - 2026-08-??

Initial release
