---
title: Upgrading to 0.6
description: "What changes when you upgrade agents-sandbox from 0.5.x or earlier, how to migrate your old home volumes, and how to retire a system-wide microsandbox."
layout: default
parent: Guides
nav_order: 30
---

# Upgrading to 0.6

## Your system-wide microsandbox is now optional

Up to 0.5.x, agents-sandbox used your system-wide microsandbox (`~/.microsandbox`)
as its runtime. Starting with 0.6, it provisions its own isolated runtime under its
data directory — `~/.local/share/agents-sandbox/msb`, or
`$XDG_DATA_HOME/agents-sandbox/msb`. The runtime never uses `~/.microsandbox` and
never writes to it; the one-time migration reads it only to copy your old
volumes. It also no longer requires an `msb` command or any particular microsandbox
version on your machine.

Nothing broke in the upgrade: agents-sandbox stopped depending on software
it does not own. `agents-sandbox doctor` reports the private runtime home and its
location.

For the durable "why" behind this split, see
[Sandboxes]({% link sandboxes.md %}#why-agents-sandbox-runs-its-own-runtime).
This page covers what to do about the old home.

## Migrating your old home volumes

Versions before the private runtime home stored each project's persistent home
volume under `~/.microsandbox/volumes/agents-sandbox-home-*`. On the first
interactive `run`/`shell` after upgrading, agents-sandbox offers once to copy
those volumes into its private home. You choose:

- **Copy the volumes** — carry the old data over.
- **Start fresh** — leave the old volumes untouched and never ask again.
- **Quit** — abort this session; ask again next time.

The option is labelled **Dismiss** instead of "Start fresh" when a private volume
of unclear status must be removed, or when an interrupted copy must be resumed.

The copy is crash- and concurrency-safe: progress is tracked in agents-sandbox's
host state, so an interrupted campaign resumes exactly where it stopped, and two
sessions cannot clobber a live copy. A volume that cannot be copied is reported
and left in place; the post-migration notice names every legacy volume that was
not verifiably copied into the private home — a failed copy, a volume you chose
not to migrate, or one whose private counterpart's contents are unverified — so
it is never silently dropped.

The decision is required before a session can proceed, because starting would
otherwise create a fresh home volume and overwrite the project's recorded
reference before the old data could be copied. Non-interactive runs (including
`--yes`) therefore never copy; while legacy volumes remain, they stop with a
message to run an interactive `run`/`shell` once.

If a private volume is in unclear status (for example after a lost `state.yaml`),
use `agents-sandbox volume remove --force <name>` to drop it and let the next
offer re-copy it. See
[Legacy home volumes after upgrading]({% link troubleshooting.md %}#legacy-home-volumes-after-upgrading)
for the full cleanup path.

## Removing the old microsandbox home

Once the migration is resolved — or when there is nothing to migrate — a
one-time notice tells you that your system-wide microsandbox is now optional. An
interactive session shows the full notice and waits for a keypress before
continuing; a non-interactive session (including `--yes`) prints only a one-line
pointer, without the removal commands and without recording the acknowledgment,
so the full notice still appears the next time you are interactive. Nothing is
deleted for you.

`~/.microsandbox` holds two different things:

- **Ours**, safe to remove:
  - `~/.microsandbox/volumes/agents-sandbox-home-*`
  - `~/.microsandbox/sandboxes/agents-sandbox-vm-*`
  - `~/.microsandbox/sandboxes/agents-sandbox-task-*`
- **Yours** — any other microsandbox data. agents-sandbox never touches it.

The notice names every legacy home volume that was not verifiably copied into
the private home — a failed copy, a volume you chose not to migrate, or one whose
private counterpart's contents are unverified. Those volumes still hold data and
must not be removed. While any remain, the notice withholds both
the wholesale `rm -rf ~/.microsandbox` and the `agents-sandbox-home-*` glob,
since both match an uncopied volume: remove the other volumes individually and
leave the uncopied ones in place.

Remove just our leftovers:

```console
rm -rf ~/.microsandbox/volumes/agents-sandbox-home-*
rm -rf ~/.microsandbox/sandboxes/agents-sandbox-vm-*
rm -rf ~/.microsandbox/sandboxes/agents-sandbox-task-*
```

Remove the whole folder only if microsandbox was installed solely for
agents-sandbox:

```console
rm -rf ~/.microsandbox
```

When uncopied volumes remain, the notice also withholds the wholesale command:
do not remove the whole folder while one remains.

Removing `~/.microsandbox` does not uninstall a system-wide microsandbox by
itself. Since agents-sandbox no longer requires the system-wide `msb` command,
you can also uninstall a system-wide microsandbox the way you installed it.

## A footnote: renamed flags and commands

These renames are a footnote to 0.6, not one of its headline stories. The
old forms still work with a one-time warning;
[Deprecations]({% link deprecations.md %}) has the full table and removal
targets.

- `--dind` / `dind` / `AGENTS_SANDBOX_DIND` → `--docker` / `docker` / `AGENTS_SANDBOX_DOCKER`
- the `OPENCODE_SANDBOX_*` environment-variable prefix → `AGENTS_SANDBOX_*`
- `sandbox list --names` → the global `-q` / `--quiet`
- `prune --age` (and `-a`) → `--older-than`
- `--opencode-version` → `--agent-version`
- `volume migrate|reset|edit --rebuild` → run `agents-sandbox build` first

One behaviour changed rather than being aliased: `prune` and its `image`,
`volume`, and `sandbox` subcommands now ask for confirmation before removing
anything, with `--yes` to skip the prompt. Non-interactively, `--yes` is
required.

Release notes live on the
[GitHub Releases page](https://github.com/inoio/agents-sandbox/releases); this
documentation does not keep a separate release history.
