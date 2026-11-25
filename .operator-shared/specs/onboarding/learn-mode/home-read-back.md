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

1. Which agent config files the session created or changed, and which of their entries are credentials. The
   [agent-config write-back](findings-and-apply.md#the-agent-config-write-back) is this fact's consumer: it copies a
   **changed** config file — only a changed one — into the per-slug user scope, and excludes the credential-store
   sources from the copy.
2. Which **static** secrets exist in the sandbox home, from either of two shapes:
   - A **dotenv** key names a secret when one of its `_`/`-`-separated segments is `KEY`, `TOKEN`, `SECRET`, or
     `PASSWORD`; the markers are matched as whole singular segments (`MAX_TOKENS` is configuration, not a secret,
     because `TOKENS` is a distinct segment from `TOKEN`).
   - An **inline** credential key (`api_key`/`apiKey`/`key`/`token`, and codex's `experimental_bearer_token`) inside
     a provider entry of a declared config file is a static secret too. A provider entry sits under a container keyed
     by provider id: `provider.<id>`, `providers.<id>`, codex's `model_providers.<id>`, or `models.providers.<id>`.
     Its provider and host come from that same entry (the endpoint walk below); the
     entry the walk reads includes its nested `options` object, where opencode keeps a provider's `apiKey` beside its
     `baseURL`. A value
     that is a reference rather than a literal — a `${…}` placeholder, a leading-`!` command reference, or a bare
     environment-variable identifier — is not a secret, and a provider that names its key indirectly
     (`api_key_env`) is not reported as one.
     A `models` map is a **model catalog**, not provider-keyed entries — opencode's
     `provider.<id>.models.<model>` — so a credential at that depth is not classified even when the entry carries
     an explicit `provider` field; only the documented `models.providers.<id>` wrapper is a provider container.
   Every static credential is scoped by an **env-var name**. Where the entry declares the name, it wins (kimi's
   `[providers.<id>.env]` table key, e.g. `KIMI_API_KEY`); otherwise the read-back derives `<PROVIDER>_API_KEY` from
   the entry's provider id — `moonshot` → `MOONSHOT_API_KEY`, `kimi` → `KIMI_API_KEY`, uppercased with
   non-alphanumerics folded to `_`. The derivation comes from the session's own entry, not a provider table
   (invariant 5). A credential the read-back cannot name is not scoped and is not proposed.
3. Which **login** credentials exist in the sandbox home (recorded, never copied). A login store is declared by an
   exact path or by a glob, so a store that is a **directory of files** — kimi's `~/.kimi-code/credentials/*.json` —
   is recorded as one login per matched file rather than missed. Claude Code's login is `~/.claude/.credentials.json`.

A profile declares a config file as a read-back path only when the reader classifies every credential shape that file
can hold. An inline key the walk does not recognise would otherwise reach the managed-config write-back verbatim
(its content is sanitized only against the *classified* static set), so an as-yet-unclassified file is withheld until
its shape is covered: `grok-build` declares `~/.grok/auth.json` only, withholding `~/.grok/config.toml` because its
`[model.<id>] api_key` is not classified yet. `codex` declares its `config.toml`: its provider entries'
`experimental_bearer_token` is classified, and a value in its `http_headers` or `query_params` tables is secret unless
the name is known configuration (`api-version`), so the host seed and the write-back (`RefuseUndeclaredSecretKeys`)
refuse such a file rather than copy it. The guard also refuses two harmless codex keys whose names carry a secret
marker (`env_key_instructions`, `credential_file`): a refusal, never a leak. Its `auth.json` stays a login store whose
value is never learned (see [exposed login keys](#exposed-login-keys)); #873 tracks `env_key` references. `claude-code`
withholds `~/.claude/settings.json` for the same reason, and more strictly: it is also excluded from the inline walk
and the first-run host seed (`readback.SkipsInlineCredentials`), because it carries credentials in values whose key
names no secret (custom auth headers, a proxy URL with userinfo, hook commands, `apiKeyHelper`).

An agent whose profile declares no read-back — unknown, or a harness whose layout is not yet verified — yields an empty
manifest and an empty read-back, never an error and never a false "no credentials" result.

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
`value` = JSON `{"type":"key","key":"…"}` — a static key, not a login. The row's `value.type` discriminates them:
`key` is a static credential whose `key` field is read; any other type (`oauth`) is a login whose value is not read
out. Its sessions and transcripts are in the same
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

## Exposed login keys

A harness's own API-key login can store the key in plain text in the home volume, where it bypasses the secret
mechanism. codex's `codex login --with-api-key` writes `OPENAI_API_KEY` into `~/.codex/auth.json` (its ChatGPT login
leaves that field null). claude-code's `/login` with a Console key writes `primaryApiKey` into `~/.claude.json`, its
global config; its subscription login lives in `.credentials.json`, and `/logout` clears the key (both read from the
claude-code 2.1.331 binary, not from a real Console login).

The read-back reports such a store as **exposed**, naming the store, the env secret that replaces the key, and that
secret's API hosts — never the key. It reads the file only to decide that; the key is not kept, not learned, and not
proposed as a secret, so the store stays a login store. A key that is a secret's guest value (microsandbox's default
`$MSB_<NAME>`, or an explicit `${…}` reference), as the documented route leaves it, is not exposed; a custom
`placeholder:` is not recognised and reads as a plain-text key.

The store is read directly, not through the manifest, so declaring an exposed-key store adds nothing to the config
write-back (`~/.claude.json` also holds onboarding and per-project state that must never be copied). The sandboxed
agent controls the home, so the read stays inside it (`os.Root`: a symlinked file or parent leading out is refused),
opens non-blocking (a FIFO cannot hang the after-run), takes only a regular file as checked on the opened file, and is
bounded. An exposed key yields a report-only finding (see [findings and apply](findings-and-apply.md#applying)); learn
mode never rewrites the store, because it writes only config scopes, never an agent's home volume.

## Baseline manifest

Change detection rests on a manifest of the agent config dirs and the known credential paths — **path, size, mtime,
and content hash** for files under a small size cap — taken when the learning session starts. It is recorded with the
session (paths and a digest, never a value), so `config learn` compares the same baseline the session began with. It
is not a scan of the home volume.

UC2's file-attributed detection will need a baseline from an ordinary run too, refreshed at the end of every run once
that detection lands. A manifest must expect large, frequently-changing files inside a config
dir — opencode2's `opencode.db` is ~13 MB, almost all of it a churny model catalog — so a whole-file hash changes
often and only a changed entry is evidence, not every refreshed line. A credential-probe entry carries neither size
nor mtime and is compared on its row digest alone; that digest covers only the rows the probe selected, never the
store file, so a catalog change does not fire it.

## Open validation

- **Per-harness read-back** — verified for opencode2 only. The other harnesses' layouts, and whether any keep
  config outside the home volume (a socket, a keychain, a read-only image path), are unverified. If a harness keeps
  its config outside the home volume, the copy-back source for that harness becomes the observation channel or a
  startup-hook export, not the home volume. The inline-key and glob-login-store classification is harness-agnostic;
  the kimi profile declares `.kimi-code/config.toml` and `.kimi-code/credentials/*.json`, so its shapes are exercised
  against the real profile.
- The home volume's database and WAL are live while the VM runs; a stopped-versus-running dump must confirm the WAL
  checkpoints into the main file before read-back relies on reading it.
