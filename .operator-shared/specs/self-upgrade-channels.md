---
description: Design for extending the launcher's self-upgrade with release channels - an `upgrade.channel` setting (`stable` default, `canary` for opted-in testers) where canary means "the newest release eligible for that channel" (a final release or a `canary`-identified prerelease), a tag-addressed download replacing the `/latest` URL, no downgrade path (leaving canary is "get the next release", with a documented manual reinstall as the immediate escape), an undocumented dev-only lever that lifts the `dev`-build short-circuit so a locally built binary can drive the real check/install, and the in-scope release-CI change that must classify a `v*-canary.N` tag as a GitHub prerelease and skip the Homebrew bump.
read_if: Implementing or reviewing self-upgrade release channels (`upgrade.channel`), canary resolution and eligibility, the tag-pinned release download, the undocumented dev-build upgrade lever, or the release CI's prerelease classification and the docs workflow's prerelease guard (`.github/workflows/pages.yml`).
---

# Self-upgrade release channels

## Intent

The launcher's self-upgrade resolves exactly one artifact: GitHub's `releases/latest`, which by design never serves a
prerelease. That is correct for the general population but leaves no way to hand a build to a small group before it is a
release. Two needs drive this:

- **A test fest.** A hand-picked group is invited to exercise an upcoming release. Fresh machines must be able to install
  the canary; existing installs must be able to switch to it at the start of the event with one action.
- **A standing canary pool.** Users worldwide opt in to early feedback on major releases, permanently, and the operator
  has a lever to push a prerelease to exactly that pool without touching everyone else.

A third, internal need: the upgrade machinery itself is otherwise untestable without cutting a release, because `dev`
builds short-circuit it.

## Goals

- A config setting that selects which releases the launcher is willing to offer itself.
- Canary prereleases reach only installs that opted in; stable installs never see one.
- Canary testers roll forward onto the real release automatically when it ships, with no special rule.
- A locally built `dev` binary can drive the real resolution/install path.
- Documentation: opt-in instructions, the canary install command, and the Homebrew non-story.

## Non-goals

- **No downgrade.** `agents-sandbox upgrade` will not reinstall an older (stable) binary over a newer prerelease. See
  [Why there is no downgrade](#why-there-is-no-downgrade).
- **Homebrew installs are out of scope.** The tap only ever serves full releases, so the channel is inert for keg
  installs. This is documented, not silently ignored.
- **No channel in persisted state.** The channel is configuration; `LauncherState` (`updates.launcher`) is unchanged.
- **One prerelease channel.** `canary` is the only prerelease channel for now; further channels (`rc`, `alpha`) are
  introduced when a need appears, not designed up front.
- **No repeatable canary ceremony.** Several prereleases per version without the full release flow is not designed here;
  the release skill cuts one at a time, as it does today.
- **No version pinning** (`--version <tag>`) and no per-invocation install of an arbitrary tag.

## Anti-goals

- A canary offered to a stable-channel install, however narrow the window.
- A downgrade code path: an older launcher driving a newer runtime home and state would fail in ways that are expensive
  to reason about. A feature that "technically works" but drags that tail in is a failure here.
- The dev lever reachable by accident: it must stay undocumented and off unless explicitly set.

## Decisions the maintainer made

- **A canary is a tagged prerelease** of the real upcoming version: `v1.9.0-canary.1`, `v1.9.0-canary.2`, … accumulated
  under that version.
- **`canary` is the only new channel.** Other prerelease channels come later if needed.
- **The publishing CI change is in scope.** A `v*-canary.N` tag must not reach stable-channel installs and must not bump
  the Homebrew formula (see [Publishing](#publishing)).
- **Leaving canary changes nothing immediately.** Setting `channel: stable` while on a prerelease leaves the install
  where it is until the next release. In between, the canary stays.
- **No user-triggered downgrade.** Dropped, not warned-about.
- **The dev lever offers the newest release for the configured channel** and shortens the check interval to
  `MinInterval`; it does not name an exact tag and never removes the throttle.

## Design

### Config surface

A new key beside `upgrade.mode`/`upgrade.interval`:

| Key               | Values                  | Default  | Env                            |
|-------------------|-------------------------|----------|--------------------------------|
| `upgrade.channel` | `stable`, `canary`      | `stable` | `AGENTS_SANDBOX_UPGRADE_CHANNEL` |

- Parsed by a new `upgrade.ParseChannel` mirroring `ParseMode` (case-insensitive, trimmed), with a `Channel` type so an
  unknown value fails loudly.
- Validated in `viperconfig.validateUpgrade`; added to `configEnvKeys` so the env binding and key listing stay
  consistent.
- A `Resolver.UpgradeChannel()` accessor, defaulting to `ChannelStable`.
- Threaded into `upgrade.Options` and into `upgrade.Upgrade`.
- The `upgrade` command respects the configured channel, and gains a one-shot `--channel` flag so a test-fest
  instruction is a single command line.

### Resolution

`stable` keeps today's single call to `releases/latest`.

`canary` enumerates `GET /releases` and takes the maximum by semver among **eligible** non-draft releases. A release is
eligible for the canary channel when it is either a final release or a prerelease whose first prerelease identifier is
`canary` (`1.9.0-canary.3`). That single rule yields the behaviours the design needs:

- `1.9.0-canary.3` is eligible and outranks the `1.8.0` a canary tester started from, so the canary is offered.
- `1.9.0` is eligible and outranks every one of its own canaries, so when the release ships the pool rolls onto it with
  no rule.
- `2.0.0-alpha.1` is *not* eligible, so a prerelease line with a different identifier never reaches the canary pool —
  it needs its own channel (Non-goals).

Masterminds/semver already orders prereleases correctly (`1.9.0-canary.10` > `1.9.0-canary.9`, and
`1.9.0-canary.1` < `1.9.0`), so no custom comparison is needed. Tags that do not parse as semver are skipped when
choosing the maximum.

Resolution must yield the exact release to install, not a floating "latest": the download is addressed by the resolved
release's tag (below). Because this repo's release tags are always `v<version>` — the publish job only triggers on `v*`
tags — the tag is reconstructed from the resolved version. The asset name stays
`agents-sandbox-<os>-<arch>`, so the planted URL is
`https://github.com/inoio/agents-sandbox/releases/download/v<version>/agents-sandbox-<os>-<arch>`.

### Download

`updateExecutable` currently ignores the version it is handed and fetches `releases/latest/download/<asset>`. `/latest`
cannot serve a prerelease, so the download becomes tag-addressed: `releases/download/<tag>/<asset>`, where `<tag>` is the
tag resolution chose.

This applies to both channels. For stable it is a small improvement rather than a change in outcome: pinning the tag
closes the race where the release moves between resolution and download, so the installed artifact is the version that
was resolved.

### The dev lever

An undocumented, test-only lever that lifts the `dev`-build short-circuit in `Check` and `Upgrade`:

- Set via `AGENTS_SANDBOX_UPGRADE_ANY` (truthy). Read at the `cmd` boundary into an `upgrade.Options` field, so
  `internal/upgrade` stays pure and the behaviour is testable without the environment.
- With it on, a current version that is empty, `dev`, or otherwise unparseable is treated as older than any release, so
  the newest release for the configured channel is offered and installable. A parseable current version behaves
  normally, which is what keeps the lever from re-offering a version it just installed.
- The Homebrew skip is unchanged: a keg install stays out of the self-upgrade path.
- It does not appear in `--help`, the docs, or any config key.
- It **reduces** the check interval rather than removing it: with the lever on, the effective interval is
  `MinInterval` (1h) regardless of `upgrade.interval`. Resolution spends one unauthenticated GitHub request per check —
  `/releases/latest` on the stable channel, the `/releases` listing on the canary channel, never both — against a limit
  shared by every install behind the same address. An unthrottled check on every `run`/`shell` would exhaust it. A
  one-hour floor is short enough to exercise the automatic path within a testing session.

### Choosing the right lever for the job

- **Immediate, deterministic verification of the check/install mechanics:** the dev lever.
- **A test fest:** set `upgrade.channel: canary` (or the one-shot flag) and run `agents-sandbox upgrade`, which ignores
  the interval. The automatic prompt then keeps the tester current at the normal cadence.
- **Leaving canary now rather than at the next release:** reinstall the stable binary by hand, exactly as the install
  docs already describe. No launcher code.

### Why there is no downgrade

A downgrade makes an older binary read and rewrite state a newer canary may have written. The parsers ignore unknown
fields, so nothing crashes, but the older binary's next write silently drops any field the canary added. Worse, the
older launcher drives the private microsandbox runtime home, so it can force-reinstall an older runtime and then fail
against a VM the newer launcher created. The convenience being bought is skipping a wait; the cost is an open-ended
tail of version-skew bugs. It is dropped, and can be revisited if testers actually need it.

### Publishing

Canaries are published as GitHub prereleases tagged `v<version>-canary.N`, and **the CI change that makes that true is
part of this work**, because the current pipeline would publish a canary as a normal release.

The tag-triggered `publish` job in `.github/workflows/ci.yml` runs on any `v*` tag and calls
`softprops/action-gh-release@v3` with `tag_name`/`name`/`generate_release_notes`/`files` and no `prerelease` input,
which defaults to `false`. The action does not infer a prerelease from a hyphen in the tag. So pushing
`v1.9.0-canary.1` creates a normal release that `/releases/latest` serves, and `isNewer("1.8.0", "1.9.0-canary.1")` is
true: every stable-channel install is offered the canary until someone flips the flag by hand — the anti-goal, and not a
narrow window. The same job also force-pushes a Homebrew bump pinned to the canary tag, contradicting the Homebrew
non-story.

Required change:

- The `publish` job classifies the tag as a prerelease when it carries a semver prerelease part, and passes
  `prerelease: true` to `action-gh-release`. `make_latest` needs no change: GitHub does not allow a prerelease to be
  "latest". The test is the **semver prerelease part**, not a hyphen test: build metadata may itself contain a hyphen
  (`v1.9.0+build-42` is a final release), so strip a `+…` suffix before looking for the prerelease hyphen. Note the two
  rules are independent — this flag is true for `-rc.N`/`-alpha.N` too, which are prereleases but not canary-eligible.
- The Homebrew bump steps are skipped for a prerelease tag, so the tap keeps pointing at a full release.

The `release` job already resolves `BARE_VERSION=${GITHUB_REF_NAME#v}` and bakes it into the binary, which is what makes
the canary report its own version and therefore be comparable — no change needed there. The release skill's step-8
`gh release edit … --prerelease` becomes redundant for prereleases but stays harmless.

Docs publishing falls out of the same classification. `.github/workflows/pages.yml` triggers on `release: published`, on
`v*` tag pushes, and on manual dispatch, so a canary tag would otherwise build and deploy the Pages site, stamping the
canary version into the footer. Its `classify` job resolves the tag (release tag, dispatch input, tag ref, else the
newest final release tag) and sets a `prerelease` output by the same semver-prerelease-part rule; the `build`/`deploy`
job runs only when that output is not `true`. The guard is unconditional — a manually dispatched prerelease tag is
blocked too — because the Pages site is the stable-facing surface.

Two constraints fall out of the design and belong to the publishing side:

- Canary numbers must increase monotonically per version, and a published canary tag must never be re-pushed. The check
  is version-string based, so a moved tag is neither newer nor re-offered.
- The channel is inert until the first prerelease exists.

### Docs, changelog, deprecation

- `docs/configuration/self-upgrade.md`: a Channels section (what `canary` means, the opt-in, leaving, the Homebrew
  note, the roll-forward onto the real release).
- `docs/install/installation.md`: a canary install snippet pinned to a prerelease tag.
- `docs/configuration/launcher.md`'s config-key and env-var tables and `docs/commands.md`'s `### upgrade` section (the
  `--channel` flag) are kept in sync too.
- `CHANGELOG.md` `[Unreleased]`: one line for the channel, one for the canary publishing fix.
- No deprecation-register entry: a new key, nothing removed.

## Invariants

- **The channel gates the offer.** A stable-channel install is never offered a prerelease; a canary-channel install is
  offered the newest eligible release, where eligible means a final release or a `canary`-identified prerelease.
- **A non-canary prerelease reaches no channel yet.** An `-alpha`/`-rc` prerelease is offered to nobody until a channel
  for it exists.
- **The published Release is a prerelease.** A `v*-canary.N` tag must produce a GitHub prerelease and must not bump the
  Homebrew formula, because `/releases/latest` is the stable channel's only source of truth.
- **The docs site is published only from a final release.** A prerelease tag never builds or deploys Pages, under any
  trigger (including manual dispatch), because the site is the stable-facing surface.
- **The installed artifact is the resolved version.** The download is addressed by the tag resolution chose, not by
  `/latest`.
- **The channel is configuration, not state.** Nothing about the channel is persisted; `LauncherState` keeps
  `last_checked` and `dismissed_versions` unchanged.
- **Dismissals are exact versions.** A dismissed canary (`1.9.0-canary.3`) does not suppress the final release
  (`1.9.0`) or a later canary.
- **Homebrew installs never self-upgrade**, channel or not.
- **The dev lever is invisible and inert by default.** With it unset, `dev`/empty builds behave exactly as before.
- **No downgrade path exists.** No code reinstalls an older binary.

## Edge cases

- Canary channel, no eligible release newer than the running release: nothing is offered.
- Canary channel, a newer final release than the newest canary: the final release is offered; eligibility has no
  prerelease preference.
- A newer `-alpha.N`/`-rc.N` than every canary: not eligible, so it is offered to nobody — a missing channel, not a
  bug. (No such tags exist today; the repo's tags are plain `vX.Y.Z`.)
- A prerelease tag that does not parse as semver: skipped when choosing the maximum.
- A canary tester on `1.9.0-canary.3` when `1.9.0` ships: offered `1.9.0`, and thereafter rides the stable line while
  still flagged canary, so the next cycle's canaries reach them.
- A canary tester who set `channel: stable`: no offer until a release newer than their prerelease exists.
- Dev lever on a release build: normal behaviour (the current version parses, so the lever changes nothing).
- Dev lever with no releases reachable: nothing offered, same as today's network-failure path.

## Left to the builder

- Where the env var is read, and how the `MinInterval` cap is applied alongside an explicitly configured
  `upgrade.interval` (the floor wins).
- The exact canary release-listing call (page size, draft handling) and how the chosen tag is carried into the download.
- How the `publish` job detects a prerelease tag (a semver prerelease part versus a known-identifier match) and how the
  Homebrew steps are skipped for it.
- Whether the prompt wording calls out "canary" explicitly or relies on the version string.
- Test shape for both channels, the tag-pinned download, the dev lever, and the CI prerelease classification.
