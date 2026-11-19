---
description: The learn-mode passive read-back - the facts the analyser reads from a sandbox home with no channel (which agent config files changed and which entries are credentials, which static secrets exist - a dotenv key or an inline provider key - and which login credentials exist, recorded but never copied); the per-harness layout truth (some credentials are inline in an agent database or config file, some login stores are directories); the env-var name that scopes a static secret; and the baseline manifest that makes change detection possible.
read_if: Working on the learn-mode passive read-back or the after-run analyser's fact source - reading a home volume, an agent's config or credential layout (including a SQLite credential row or an inline config key), a glob-declared directory login store, the baseline manifest and its hashing, classifying a credential as static or login, or the env-var name a static secret is scoped by.
---

# Learn mode — home read-back

Passive read-back is the channel that is always needed: after a session, the launcher reads the sandbox home and
derives the config and secret facts. It needs nothing new — the home volume is host-readable by path — and it is the
only channel that works when the launcher-owned proxy
([the observation channel](observation-channel.md)) is unavailable.

## What read-back establishes

1. Which agent config files the session created or changed, and which of their entries are credentials.
2. Which **static** secrets exist in the sandbox home, from either of two shapes:
   - A **dotenv** key names a secret when one of its `_`/`-`-separated segments is `KEY`, `TOKEN`, `SECRET`, or
     `PASSWORD`; the markers are matched as whole segments, so a token-count setting such as `MAX_TOKENS` is
     configuration, not a secret.
   - An **inline** credential key (`api_key`/`apiKey`/`key`/`token`) inside a provider entry of a declared config
     file is a static secret too. Its provider and host come from that same entry (the endpoint walk below). A value
     that is a reference rather than a literal — a `${…}` placeholder, a leading-`!` command reference, or a bare
     environment-variable identifier — is not a secret, and a provider that names its key indirectly
     (`api_key_env`) is not reported as one.
   Every static credential is scoped by an **env-var name**. Where the entry declares the name, it wins (kimi's
   `[providers.<id>.env]` table key, e.g. `KIMI_API_KEY`); otherwise the read-back derives `<PROVIDER>_API_KEY` from
   the entry's provider id — `moonshot` → `MOONSHOT_API_KEY`, `kimi` → `KIMI_API_KEY`, uppercased with
   non-alphanumerics folded to `_`. The derivation comes from the session's own entry, not a provider table
   (invariant 5). A credential the read-back cannot name is not scoped and is not proposed.
3. Which **login** credentials exist in the sandbox home (recorded, never copied). A login store is declared by an
   exact path or by a glob, so a store that is a **directory of files** — kimi's `~/.kimi-code/credentials/*.json` —
   is recorded as one login per matched file rather than missed.

Facts 1–3 come from reading the home volume. It also gives configured providers, login presence, and
harness-specific host and error mentions where the harness happens to record them (usually failure paths, and never
as reliable attribution). It does not give the destinations actually used, or which credential went where; alone it cannot propose a network
change (its attribution fallback can still scope a static secret to the endpoint the entry's own config names — see
[findings and apply](findings-and-apply.md)).

Reading a credential is not always reading a dotenv file. A harness may keep its API key inline in its own database
(opencode2) or inline in a provider entry of its config file (kimi's `api_key`, hermes' `model.api_key`); the reader
must therefore parse the agent's database and walk its config files, not only its `.env`. The spike verified the
database shape for opencode2 alone: `~/.config/opencode/` is empty, and the API key sits inline in
`~/.local/share/opencode/opencode.db` (SQLite), table `credential` (`integration_id`, `label`, `active`), column
`value` = JSON `{"type":"key","key":"…"}` — a static key, not a login. Its sessions and transcripts are in the same
DB, with prompt history and small state files under `~/.local/state/opencode/` and its log under
`~/.local/share/opencode/log/`. The other harnesses' layouts are unverified.

Login credentials found here are recorded (presence, source, provider) but **never copied out of the sandbox**
(invariant 2).

Reading the database is not literally read-only: even an SQLite connection opened `mode=ro` may create an
`opencode.db-shm` shared-memory file beside `opencode.db`, so the volume directory must stay host-writable for the
read-back.

The read is bounded because the sandboxed agent controls the database. A credential row's value is materialized
only through a length-capped SQL projection, and a store whose rows exceed the probe's bound is recorded unreadable
rather than loaded into host memory; the probe still contributes exactly one manifest entry, carrying the unreadable
sentinel digest.

## Baseline manifest

Change detection rests on a manifest of the agent config dirs and the known credential paths — **path, size, mtime,
and content hash** for files under a small size cap — taken when the learning session starts. It is recorded with the
session (paths and a digest, never a value), so `config learn` compares the same baseline the session began with. It
is not a scan of the home volume.

UC2's file-attributed detection will need a baseline from an ordinary run too, refreshed at the end of every run once
that detection lands. A manifest must expect large, frequently-changing files inside a config
dir — opencode2's `opencode.db` is ~13 MB, almost all of it a churny model catalog — so a whole-file hash changes
often and only a changed entry is evidence, not every refreshed line.

## Open validation

- **Per-harness read-back** — verified for opencode2 only. The other harnesses' layouts, and whether any keep
  config outside the home volume (a socket, a keychain, a read-only image path), are unverified. If a harness keeps
  its config outside the home volume, the copy-back source for that harness becomes the observation channel or a
  startup-hook export, not the home volume. The inline-key and glob-login-store classification is harness-agnostic;
  the kimi profile declares `.kimi-code/config.toml` and `.kimi-code/credentials/*.json`, so its shapes are exercised
  against the real profile.
- The home volume's database and WAL are live while the VM runs; a stopped-versus-running dump must confirm the WAL
  checkpoints into the main file before read-back relies on reading it.
