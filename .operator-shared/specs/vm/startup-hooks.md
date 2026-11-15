---
description: The startup-hook contract: a hook may prompt but must never require a terminal, the
  two-fd interactivity predicate, the streaming-exec requirement for the non-interactive arm, and
  the no-fallback rule.
read_if: Changing runStartupHooks / SetUpSandbox hook handling, the HookInteractive seam, or the
  home-provisioning docs; or diagnosing a startup hook that did not run, or a prompting hook that
  failed without a terminal.
---

# Startup hooks

`home:` `hook: startup` entries run once per VM boot, before the agent session
(`specs/vm/lifecycle.md`). They are declared and provisioned through the home-provisioning path;
this spec covers only their execution contract.

## Contract

- A hook may prompt (password, MFA) but must never *require* a terminal. It runs through the
  interactive PTY attach (`sb.AttachWith`) only when the launcher's stdin **and** stdout are both
  terminals; otherwise it runs through a streaming exec (`sb.ExecStream`) with stdin closed, so a
  prompting hook reads EOF and fails visibly.
- Requiring stdout is deliberate. A redirected or piped run (`agents-sandbox | tee run.log`) falls
  back to the non-interactive path instead of blocking on a prompt the user cannot see; the docs
  state this pitfall.
- The non-interactive arm uses `sb.ExecStream`, never the collected `sb.Exec`. The collected
  response is capped at ~1 MiB, which would misreport a large-output hook as failed.
- A failed attach is a warning and the remaining hooks still run. There is no automatic fallback
  from a failed attach to the exec path: re-running a hook whose side effects may already have
  started is worse than skipping it.
- The predicate takes no `RunOptions`, so `--yes` does not disable hook interactivity. Interactivity
  is a property of the terminal, not of the launcher's confirmation mode.
- Hooks run only when the VM boots (created, recreated, or booted from stopped/crashed), never on
  attach to an already-running VM.

## Seam

`options.Seams.HookInteractive func() bool` injects the predicate; nil uses the production default
(`hookInteractiveDefault`, stdin and stdout both terminals). `SetUpSandbox` resolves it once per
boot and threads the resulting bool, so `runStartupHooks` stays pure over a bool and a test never
reads the ambient TTY.
