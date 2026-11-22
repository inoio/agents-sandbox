---
description: Manual end-to-end runbook and promotion bar for removing the experimental flag from claude-code, codex, and pi for 0.8 (#779 Prio 1, first item); per-harness journeys, blockers, and the results table.
read_if: Running or resuming the 0.8 harness e2e pass; deciding whether an agent profile may drop `Experimental()`; or triaging a finding from that pass.
---

# Harness promotion e2e (0.8)

Tracked in #783 (results table and findings mirror this plan).

**Goal:** decide, per harness, whether `Experimental()` can return `false` for 0.8. In scope: `claude-code`, `codex`,
`pi` — the most-used agents next to opencode (#772's README selection). The other experimental profiles stay
experimental for 0.8.

**Driver:** the agent drives every step through `tmux -L e2e-<agent>` on the host; the maintainer only performs
logins and supplies keys. Real model turns are in scope (unlike the release surface sweep).

**Binary under test:** `make build` in this worktree (`./agents-sandbox`, aliased `asb-dev`), never `make install-head`
(that replaces the maintainer's installed binary).

**Isolation:** the release sweep's helper (`scripts/surface-sweep.sh`, see `specs/testing/release-surface-testing.md`)
with `ASB_SURFACE_ROOT=/tmp/asb-e2e`, one journey per agent (`eval "$(bash scripts/surface-sweep.sh env <agent>)"`).
This exercises the new-user path. Journey F seeds a host config into the isolated `HOME`; nothing touches the
maintainer's real `~/.claude`, `~/.codex`, or `~/.config/agents-sandbox`.

## Promotion bar

A harness drops the flag when journeys A–I pass and no open bug in the "Blockers" list applies to it. A failure with a
filed issue and a documented workaround may be accepted by the maintainer.

## Journeys (per agent)

- **A — first run:** `asb-dev --agent <a>` on the isolated env builds the image and reaches the TUI; the experimental
  notice shows; quitting restores the terminal.
- **B — auth:** the agent's documented login works inside the VM under the default network profile:
  - claude-code: `/login` (OAuth; the URL must be openable/copyable from the host) and the `ANTHROPIC_API_KEY`
    `env.secret` route;
  - codex: ChatGPT device login and the `OPENAI_API_KEY` `env.secret` route;
  - pi: an API-key provider via `env.secret`.
  For the `env.secret` route the agent sees only the placeholder and a turn still succeeds (TLS-interception trust in
  that agent's runtime).
- **C — real turn:** prompt the agent to create `/workspace/e2e.txt` and run a shell command; the file appears in the
  host project dir owned by the host user.
- **D — persistence:** quit and rerun: the login survives (home volume), and the agent's resume (`-- --continue` /
  `codex resume --last`) restores the last session.
- **E — second client:** a second `asb-dev --agent <a>` in another pane attaches to the same VM while the first runs;
  both work, quitting one leaves the other alive.
- **F — host drop-in:** with a seeded host config, exactly the documented files reach the VM and no file that can hold
  a secret does (see #767).
- **G — snippet:** a config snippet under the isolated `config/agents-sandbox/<a>/` takes effect in the agent (e.g. a
  model choice) and wins over the drop-in.
- **H — upgrade:** `--agent-version <older>` then a plain run offers the upgrade; accepting rebuilds; the agent's own
  updater stays silent.
- **I — abnormal end:** Ctrl-C, closing the pane, and killing the VM mid-turn each leave a clean host terminal, and the
  next run recovers.

## Blockers known before the pass

- #767 — the drop-in copies `~/.claude/settings.json` and `~/.codex/config.toml`, which can hold secrets: blocks
  claude-code and codex until fixed.
- #768 — the agent's main config is reset/removed every run: decide before promotion, because it shapes what G and D
  mean for settings changed inside the VM.

## Results

| Journey | claude-code | codex | pi |
|---|---|---|---|
| A | ✅ | ✅ | ✅ |
| B | ✅ OAuth; env.secret tested earlier by the maintainer | ✅ ChatGPT (callback via host :1455) | ✅ Anthropic browser login (callback via host :53692) |
| C | ✅ | ✅ | ✅ |
| D | ✅ login + `--continue` | ✅ login + `resume --last`; trust asked every run (#768) | ✅ login + `--continue`; default model lost every run (#768) |
| E | ✅ | ✅ | ✅ |
| F | ✖ #767 (settings.json with `env` secret copied verbatim) | ✖ #767 (`config.toml` with bearer token copied verbatim) | ✅ settings/AGENTS.md/prompts copied, auth.json/models.json not |
| G | ✅ | ✅ snippet replaces drop-in; trust snippet stops the prompt | ✅ |
| H | ✅ | ✅ | ✅ |
| I | ✅ | ✅ | ✅ |

Decision: pi meets the bar and drops the flag; claude-code and codex wait for #767 (landed with or after the #768 stopgap).

Findings so far:

- claude-code A ✅, B (OAuth) ✅ — the host opener opened the login tab; D (login survives a restart) ✅.
- #781 — the trust warning lists `.agents-sandbox/config.yaml` twice (one input per kind).
- #782 — shorten the "No launcher config dir" notice and show it only on a new project VM.
- The VM clock runs in UTC while the host is in local time, so agents report times an hour off (no issue yet).
- #786 — every image-digest change (an agent upgrade too) asks keep/migrate/reset for the home volume; proposal: never ask, merge the image home per file.
- #787 — a VM kill during attach is reported as `agent connection closed during terminal attach` (low prio).
- codex asks folder trust on every run and every attach: it saves `trust_level` in `~/.codex/config.toml`, which reprovision removes each run (#768); a `[projects."/workspace"]` snippet avoids it.
- codex warns on every start that the launcher's `-c` overrides force embedded mode ("Running without the shared background server") (no issue yet).
- pi saves its default model (`/model`, Ctrl+S) in `~/.pi/agent/settings.json`, removed every run (#768): it falls back to Opus, which subscription auth bills as extra usage.
- #780 — claude-code asks the folder-trust question for `/workspace` with "No, exit" as default; pre-accept it.

File an issue per defect and link it here. Retire this plan once the flags are decided (fold the bar into the
agent-profile guide if it should stay).
