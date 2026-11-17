---
description: The learn-mode passive read-back - the facts the analyser reads from a sandbox home with no channel (which agent config files changed and which entries are credentials, which static secrets exist, and which login credentials exist, recorded but never copied); the per-harness layout truth (some credentials are inline in an agent database, not a file); and the baseline manifest that makes change detection possible.
read_if: Working on the learn-mode passive read-back or the after-run analyser's fact source - reading a home volume, an agent's config or credential layout (including a SQLite credential row), the baseline manifest and its hashing, or classifying a credential as static or login.
---

# Learn mode — home read-back

Passive read-back is the channel that is always needed: after a session, the launcher reads the sandbox home and
derives the config and secret facts. It needs nothing new — the home volume is host-readable by path — and it is the
only channel that works when the launcher-owned proxy
([the observation channel](observation-channel.md)) is unavailable.

## What read-back establishes

1. Which agent config files the session created or changed, and which of their entries are credentials.
2. Which **static** secrets exist in the sandbox home.
3. Which **login** credentials exist in the sandbox home (recorded, never copied).

Facts 1–3 come from reading the home volume. It also gives configured providers, login presence, and
harness-specific host and error mentions where the harness happens to record them (usually failure paths, and never
as reliable attribution). It does not give the destinations actually used, or which credential went where; alone it
cannot propose a network or secret-substitution change.

Reading a credential is not always reading a file. A harness may write no config file and keep its API key inline in
its own database; the reader must therefore parse the agent's database, not only config files. The spike verified
this for opencode2 alone: `~/.config/opencode/` is empty, and the API key sits inline in
`~/.local/share/opencode/opencode.db` (SQLite), table `credential` (`integration_id`, `label`, `active`), column
`value` = JSON `{"type":"key","key":"…"}` — a static key, not a login. Its sessions and transcripts are in the same
DB, with prompt history and small state files under `~/.local/state/opencode/` and its log under
`~/.local/share/opencode/log/`. The other harnesses' layouts are unverified.

Login credentials found here are recorded (presence, source, provider) but **never copied out of the sandbox**
(invariant 2).

## Baseline manifest

Change detection rests on a manifest of the agent config dirs and the known credential paths — **path, size, mtime,
and content hash** for files under a small size cap — taken when the learning session starts. It is not a scan of
the home volume.

The manifest is refreshed at the end of every run once the mode exists, because UC2's file-attributed detection
needs a baseline from an ordinary run too. A manifest must expect large, frequently-changing files inside a config
dir — opencode2's `opencode.db` is ~13 MB, almost all of it a churny model catalog — so a whole-file hash changes
often and only a changed entry is evidence, not every refreshed line.

## Open validation

- **Per-harness read-back** — verified for opencode2 only. The other harnesses' layouts, and whether any keep
  config outside the home volume (a socket, a keychain, a read-only image path), are unverified. If a harness keeps
  its config outside the home volume, the copy-back source for that harness becomes the observation channel or a
  startup-hook export, not the home volume.
- The home volume's database and WAL are live while the VM runs; a stopped-versus-running dump must confirm the WAL
  checkpoints into the main file before read-back relies on reading it.
