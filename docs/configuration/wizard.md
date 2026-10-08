---
title: Setup wizard
description: "Interactive tiered setup wizard that fills in launcher configuration, on first run and on demand via config wizard."
layout: default
parent: Configuration
nav_order: 15
---

# Setup wizard

On the first interactive `run`/`shell`, agents-sandbox asks a short set of setup questions before starting the sandbox,
so the essentials are in place without hand-editing YAML. The same wizard is re-runnable at any time with
`agents-sandbox config wizard` (see [Commands]({% link commands.md %}#config-wizard)).

Answers are written to your user-level [configuration file]({% link configuration/launcher.md %}#user-level-defaults),
preserving comments and key order. Nothing is written when every question already has a value or every question is
skipped.

## Tiers

Questions are grouped into four tiers, asked from the essentials outward:

| Tier | Name         | Questions                                                                             | Defaults                          |
|------|--------------|---------------------------------------------------------------------------------------|-----------------------------------|
| 1    | Essentials   | `agent` (only when unset), `network.profile`                                           | `opencode`, `none`                |
| 2    | Resources    | `cpus`, `memory`, `disk-size`, `workspace-quota`                                       | `4`, `8G`, `16G`, `16G`           |
| 3    | Runtime      | `dind`, `tmp-size`, `log-level`                                                        | `false`, `2G`, `info`             |
| 4    | Integrations | `notify.desktop`, `notify.audio`, `upgrade.mode`, `upgrade.interval`, `auto-prune-age`  | `false`, `off`, `prompt`, `1d`, `30d` |

## First run

An interactive first run asks **tiers 1–3** (essentials, resources, runtime). Tier 4 (integrations) is left for an
explicit opt-in later so the first run stays short.

The wizard runs *before* credential migration, and run options are re-resolved afterwards, so migration sees the agent
and network posture you chose.

Only the essentials are mandatory. Both essential questions default to a value, so pressing Enter through the run is
always safe. `network.profile` is asked at every first run (it is the one question that is always relevant to credential
handling), unless a more specific scope already fixes it.

Completed first runs are remembered in `$XDG_STATE_HOME/agents-sandbox/config-wizard.yaml`, so the first-run questions
do not reappear.

## `config wizard`

`agents-sandbox config wizard` asks all tiers that still have unanswered questions. Use `--tier=N` to ask exactly one
tier (1–4), for example to opt into integrations after first run:

```console
agents-sandbox config wizard            # every unanswered tier
agents-sandbox config wizard --tier=4   # integrations only
```

Non-interactive runs (including `--quiet`/piped input) skip the wizard entirely rather than hanging.

## Answering questions

Every question has a default. Value questions (like `memory`) offer "Use default" or "Enter a custom value"; choice and
confirm questions present their options with the default preselected. Custom values are validated; an invalid entry is
rejected and re-asked.

Related resource questions are **collapsed behind one confirmation**: the resources tier first asks "Use the recommended
resource defaults?", and only expands `cpus`/`memory`/`disk-size`/`workspace-quota` into individual questions when you
decline. The integrations tier collapses its notify/upgrade questions the same way.

**Early exit:** every non-essential question also offers "Enough questions — use defaults for the rest", which stops
the interview immediately and applies defaults to all remaining questions. The essentials (agent selection when unset,
and network posture) come first and cannot be skipped this way, because the flow cannot proceed without them.

### Skipping what is already set

A value already set at a more specific scope — [per-slug user config]({% link configuration/launcher.md %}#per-slug-configuration),
project config, environment variable, or CLI flag — is never overwritten. The wizard skips that question and reports it.

## Where state lives

Answered and first-run state is stored outside your config so it never changes what the launcher reads:

```
$XDG_STATE_HOME/agents-sandbox/config-wizard.yaml
```

`XDG_STATE_HOME` defaults to `~/.local/state`, so the file is usually
`~/.local/state/agents-sandbox/config-wizard.yaml`. It records which questions were answered and whether the first run
is done, so the wizard never nags. Deleting it re-arms the first-run questions.

## Related

- [Commands reference]({% link commands.md %}) — `config wizard` flags.
- [Configuration files & Environment variables]({% link configuration/launcher.md %}) — the fields the wizard writes
  and their precedence.
