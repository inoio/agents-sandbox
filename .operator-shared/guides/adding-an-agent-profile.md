---
description: Checklist for adding a built-in agent profile in `internal/agent`, covering the pitfalls earlier profiles hit. A file written outside the tool tree never reaches the runner image. Update-check and TLS behaviour must be verified on the built binary. The drop-in copies only credential-free files, and the read-back declares only classifiable files. The guide also lists every place a profile touches.
read_if: Adding or reviewing a new agent profile (`ImageSpec`, `ProvisionRules`, `ReadBackPaths`, the `driver.Specs` entry); writing an agent's install command, update switch, or credential docs; or diagnosing an agent setting that is missing from the shipped image, a secret placeholder sent upstream unsubstituted, or a claim about an agent that turned out wrong.
---

# Adding an agent profile

The profile code shows how a profile is built. This guide records what the code cannot show: the traps a new profile
falls into and how to verify each one.

## Verify on the built artifact, not the source

A claim about upstream behaviour goes into code comments, docs, or a PR only after it has been checked against the
binary the image actually installs: an update check, a subcommand, a config key, an environment variable, or a first-run
screen. Source and docs describe other builds and other versions, and features can sit behind build flags. Where a check
is not possible, the text says the claim is unverified. A switch that no code reads is not set, because a harmless-looking
extra variable reads as a working guarantee.

Probes that worked:

- **Effective config:** ask the agent's own config reader, for example Vibe's app-server `config/read`. Compare the
  result with and without the variable, and against a user config that sets the opposite value.
- **Subcommands:** run `<binary> --help` from the release asset the image installs. The upstream source tree is not
  enough.
- **First-run screen:** take it from the msb smoke test. Write the harness comment and the spec row from what the
  test showed.

## Image build

- **Install a pinned version, verified where upstream allows.** Check a published checksum (a `SHA256SUMS` file or a
  per-asset `.sha256`). Otherwise embed the digest of a pinned helper, as `mistral-vibe` does for uv. A PyPI install is
  held to the release tag's lock file. An official installer is used only if it can install a pinned version.
- **Only the tool tree ships.** The runner copies just `/opt/agents-sandbox` from the agent stage. Anything the install
  writes elsewhere (`/etc`, `/root`, `/usr`) is lost. Either relocate it into the tool tree through an environment
  variable the agent honours (qwen's `QWEN_CODE_SYSTEM_SETTINGS_PATH`), or write it in the runner stage with
  `ImageSpec.RunnerCommand` (grok's `/etc/grok/requirements.toml`).
- **The integration build does not prove the ancillary final-stage content.** `TestRenderDockerfileBuildsEveryAgent`
  builds the full final Dockerfile and runs the agent's version probe in the built final image, so the agent binary is
  proven. It does not assert ancillary final-stage content (`ImageSpec.RunnerCommand` artifacts such as grok's
  `/etc/grok/requirements.toml`). Pin that content with a render test instead (see the qwen and grok render tests).
- **Turn the update check off and prove it.** Use an environment variable or a pinned system setting, verified as
  above. The sandbox pins the baked version and offers upgrades itself.
- Integration builds of heavy installs exceed `go test`'s default 10-minute timeout, so pass `-timeout 40m`.

## TLS and secrets

Secret substitution needs TLS interception, and the image puts the interception CA into the system store and into
`SSL_CERT_FILE`/`REQUESTS_CA_BUNDLE`/`NODE_EXTRA_CA_CERTS`. An agent whose HTTP stack ignores both cannot reach its
provider once a secret is configured. Known cases:

- Rust clients built with webpki roots.
- Python on a strict `certifi` context.

Find the agent's system-trust switch (Vibe: `VIBE_ENABLE_SYSTEM_TRUST_STORE`) and set it in `AgentEnv`. Also check
that the provider key reaches only the hosts the docs name for its `env.secret`.

## Credentials

- **Drop-in:** copy only credential-free files, such as global instructions, skills, prompts, agents, and workflows.
  A file that mixes settings with keys, tokens, MCP headers, or env tables stays on the host, and its settings move to
  config snippets. The same goes for hooks, which run host commands, and for plugins, which can bundle MCP configs.
  The opencode `auth.json` copy is the only exception.
- **Read-back:** declare only files the reader fully classifies (see `specs/onboarding/learn-mode/home-read-back.md`).
  Declare nothing rather than an unclassified file.
- **Config family:** the launcher replaces or removes the agent's main config file in the VM on every run. Settings
  the agent writes there itself do not persist, so the docs send users to snippets. A login store kept outside the
  family persists on the home volume.

## Places a profile touches

- **Code:**
  - `internal/agent/<name>.go` and `<name>_release.go`, with their tests;
  - the profile entry in `builtinRegistry`;
  - the `TestReadBackPaths` map;
  - any login store in `readback.loginCredentialStores`.

  The CLI, runoptions, image, and readback contract tests derive their agent lists from the registry.
- **Harness:** the `driver.Specs` entry and the Ctrl-C/Ctrl-D comment; the quit-key table, the buffer split, the
  egress paragraph, and the cold-home build count in `specs/testing/harness-smoke-tests.md`.
- **Journeys:** add the harness adapter in `test/msb/journeys/harness_<name>.go` and register it in `Harnesses`;
  add the harness to `placedHarnesses` in `table.go`. For each scenario, add the harness to its `Harnesses` list or
  record an exemption in `table.go`, then regenerate `test/msb/journeys/JOURNEYS.md`.
- **Docs:** `docs/configuration/agent.md` is the canonical list. It takes:
  - the profile bullet under *Built-in agents*;
  - the snippet bullet and the TOML/YAML/JSON list;
  - the drop-in bullet;
  - the credentials bullet and its "do either" sentence;
  - a capability-matrix column and the ◐ sentence.

  `docs/runner-image.md` takes the install sentence and the version-source sentence. The README, the docs landing
  page, and the command reference name only the most-used agents and link to *Built-in agents*. A new agent adds
  nothing there.
- `changelog.d/<PR>.added.md`.
