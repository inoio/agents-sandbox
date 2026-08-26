# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Version tags use a `v` prefix (e.g. `v0.1.0`); the `version`
command reports the bare version (e.g. `0.1.0`).

## [Unreleased]

### Added

* Docs: The roadmap now lives as GitHub issues. `ROADMAP.md` is a pointer to the issue-driven process, with labels (`priority:critical`, `priority:high`, `priority:low`, `chore`, `upstream`) and filtered search links.

### Changed

* Docs: Pages are now published only after a successful release, rebuilding from the published tag rather than in parallel on tag push. Each page footer and the home page display the release (or branch) they were built from.
* Docs: Support for dark mode; follows the OS/browser color-scheme preference by default and expose a sun/moon toggle in the header (next to the GitHub link) that overrides and persists the choice.
* Docs: The README's Documentation section now links directly to the hosted GitHub Pages docs.
* Bugfix: A change to a secret's allowed hosts (or other secret properties such as placeholder) now triggers a VM recreate; previously only the secret value was part of the change hash, so host changes were silently ignored.
* Bugfix: After a home-volume `reset` or `migrate`, restarting now recreates the project VM so the new home volume is actually mounted; previously the running VM kept serving the old volume because the mounted home volume was not compared during reconfiguration.

## [0.1.0] - 2026-08-??

### Added

* everything ;)

### Changed

* lives & experiences, hopefully :)