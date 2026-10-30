---
title: Self-upgrade
description: "How agents-sandbox checks for new releases and upgrades itself, and how to configure the upgrade mode, interval, and release channel."
layout: default
parent: Configuration
nav_order: 80
---

# Self-upgrade

agents-sandbox checks GitHub for a newer release when you start `run`/`shell`. The check is throttled to at most once
per `upgrade.interval` (default `1d`, minimum `1h`) and is skipped entirely for local `dev` builds, for Homebrew-managed
installs (see [Homebrew Installs](#homebrew-installs)), and when a check is already within the interval. Transient
network failures are ignored so an offline start is never blocked. When a newer
release is found, the `upgrade.mode` decides what happens:

| Mode                 | Behavior                                                                                                            |
|----------------------|---------------------------------------------------------------------------------------------------------------------|
| `prompt` (default)   | Ask what to do: continue, don't ask again for this version, upgrade & continue, or upgrade & exit. Falls back to a plain notice when not interactive. |
| `notify`             | Print a notice that a newer release exists; never installs anything.                                                |
| `auto`       | Silently download and replace the binary, then continue running the current version.                                |
| `auto-exit`  | Silently download and replace the binary, then exit so the next invocation uses the new version.                    |

The `upgrade` command (`agents-sandbox upgrade`) checks for and installs the latest release at any time, independent of
`upgrade.mode`/`upgrade.interval`. Upgrading replaces the running executable with the release binary for your platform
(`agents-sandbox-<os>-<arch>` from the GitHub release assets); because a running process cannot swap its own binary, an
upgrade (or `auto-exit`) takes effect on the next invocation. Before replacing the binary, the launcher verifies the
download's SHA-256 against the release's published `checksums.txt`; if that file is missing, or the entry for the asset
is missing, malformed, or does not match, the install aborts and the running binary stays untouched.

## Release channels

By default an install follows full releases only: GitHub's `latest` release, which never points at a prerelease.
`upgrade.channel` opts an install into a prerelease line instead:

| Channel            | Follows                                                                                                                                     |
|--------------------|---------------------------------------------------------------------------------------------------------------------------------------------|
| `stable` (default) | The newest full release.                                                                                                                    |
| `canary`           | The newest release eligible for the canary line: the newest canary prerelease such as `1.9.0-canary.3`, and the real release once it ships. |

Set the key in the launcher config (or export `AGENTS_SANDBOX_UPGRADE_CHANNEL=canary`):

```yaml
upgrade:
  channel: canary
```

Canary installs roll forward on their own: once `1.9.0` ships it outranks `1.9.0-canary.3`, so it is offered like any
other release and a tester reaches it without switching channels. Only prereleases whose first identifier is `canary`
are eligible for the line; an `-alpha` or `-rc` prerelease is offered to no channel.

The `upgrade` command takes a one-shot override: `agents-sandbox upgrade --channel canary` checks and installs from the
canary line for that invocation, leaving the setting untouched.

Setting `upgrade.channel` back to `stable` changes nothing until the next release ships, because there is no downgrade —
the installed canary stays until something newer is offered. To leave the canary line immediately, reinstall the stable
binary following the [installation instructions]({% link install/installation.md %}).

Homebrew installs ignore the setting: the tap serves full releases only, so a keg stays on the stable line whatever
`upgrade.channel` says, and a canary cannot be installed through Homebrew. To install a canary, follow the
release-binary instructions on the [installation page]({% link install/installation.md %}) and set the channel to
`canary` so future upgrades follow the same line.

## Homebrew installs

When agents-sandbox detects that the running binary is a Homebrew-managed keg (it lives under a `Cellar` directory), it
defers version management to Homebrew so the launcher never replaces the binary out from under `brew`:

* The automatic `run`/`shell` check is skipped entirely, regardless of `upgrade.mode`.
* The `upgrade` command prints `brew update && brew upgrade agents-sandbox` instead of downloading a release.

Update these installs with `brew update && brew upgrade agents-sandbox`.
