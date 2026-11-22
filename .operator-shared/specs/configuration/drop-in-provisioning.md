---
description: Contract for the host→VM drop-in copy's file scope - which provision-rule selections are launcher-managed single config-format files and which are user content (the instruction files and the skills/commands/agents/prompts/extensions/themes/workflows trees) - shared by the disabled drop-in's stale-removal set and the first-run host probe and seed.
read_if: Changing `agent.ProvisionRule`/`ProvisionRules`, `agent.IsContentTreePattern`/`ProvisionRulesWithoutContentTrees`, `reprovision.provisionDestinations`/`ConfigFiles.Remove`/`Provision`/`removeStalePaths`, or `firstrun.HostFiles`/`SeedFromHost`; or diagnosing an in-VM instruction or skill file deleted when `provision-host-config` is off, or a config-named file inside a skill tree seeding over the agent's real config.
---

# Drop-in provisioning file scope

The opt-in drop-in copy (`provision-host-config: true`) walks each agent's `agent.ProvisionRule`s and
copies the selected host files into the VM. Two mechanisms reuse those same rules for a narrower
purpose and must not treat user content as launcher-managed config: the stale-removal set built when
provisioning is off, and the first-run host probe and seed.

## Launcher-managed files vs user content

A provision rule selects either a single launcher-managed file — a config or credential file, a
single-file pattern whose name has a config-format extension (`settings.json`, `config.toml`,
`auth.json`) — or user content. `agent.IsContentTreePattern` classifies a content tree: a non-negated
gitignore directory pattern (a trailing slash) for `skills/`, `commands/`, `agents/`, `prompts/`,
`extensions/`, `themes/`, `workflows/`, or `recipes/`. A negation (a leading `!`) only narrows a
broader selection and is never a content tree. The instruction files (`CLAUDE.md`, `AGENTS.md`,
`AGENTS.override.md`) are single files, not trees, but user content too.

`agent.ProvisionRulesWithoutContentTrees` returns the rules with the tree patterns removed (dropping
a rule left with no patterns), keeping single-file patterns and negations.

## Stale removal when provisioning is off

With `provision-host-config` off, `reprovision.LoadConfigFilesForHost` appends the drop-in
destinations to `ConfigFiles.Remove` so a previously copied host file cannot linger, and
`Provision`/`Materialize` delete them. Only the launcher-managed files are removed: the config-file
family, plus single files with a config-format extension. A content tree or an instruction file is
left alone, because the agent may also have written it inside the sandbox (an in-VM `/memory`
`CLAUDE.md`, an in-sandbox skill); deleting it would discard content the launcher never wrote. This
matches the user-facing scope in `docs/configuration/agent.md`: only previously drop-in-copied config
and credential files are removed. `Provision` reports each removal through the UI, so a path that
disappears has a visible cause.

## Host probe and seed

The first-run host probe (`firstrun.HostFiles`) and its UC4 seed consider only the single
launcher-managed files. The seed writes each selected file under the managed config dir by base name,
so a config-named file inside a content tree (a codex skill bundling `config.toml`, a claude-code
skill shipping `settings.json`) would collide with — and overwrite — the agent's real config. Walking
only the single-file provision rules removes that collision and keeps a skill change from re-arming
the first-run decision.
