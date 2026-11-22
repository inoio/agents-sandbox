---
description: Contract for who owns an agent's main config file in the VM - the launcher owns only the keys it writes (merged snippets, or the drop-in copy), re-applies them every run, and removes the keys it dropped using a recorded base on the home volume, so the agent's own settings survive every run and attach; the reconfig decision compares against that base.
read_if: Changing `reprovision.Provision`/`Materialize`, `reconcileMainConfig`, `configmerge.Reconcile`, `ConfigFiles.Remove`/`MergedBasePath`, `ChangedAgentConfigPaths`; or diagnosing an agent setting lost between runs, a main config rewritten without a snippet change, or a reconfig prompt caused by the agent's own write.
---

# Main config ownership

## Contract

- **The launcher owns only its keys.** The launcher's content for an agent's main config (`ConfigMerger.VMConfigPath`) is the merged snippets, or without snippets the drop-in copy of the host file, or nothing. It is reconciled with the VM file, never written over it and never removed because no snippet exists.
- **Launcher keys are re-applied every run.** Every key of the launcher's content is set to the launcher's value on each reconciliation, so a policy set through a snippet (`permissions.deny`, codex `sandbox_mode`) cannot be changed from inside the VM for good. Every other key of the VM file is kept; tables merge key by key, arrays are values.
- **A recorded base detects dropped keys.** The content written last is recorded under `~/.local/state/agents-sandbox/config-base/<home-relative path>` on the home volume (mode 0600: a drop-in copy can carry keys), so it is reset together with the file. A key in the base that the launcher no longer writes is removed from the VM file.
- **No difference, no write.** A result equal to the VM file leaves it untouched, so its formatting and comments survive. A write re-serializes the whole document (key order and comments are not preserved).
- **No base counts as empty.** On a VM from before the base existed, or with an unreadable base, a snippet key removed in that same run stays in the file once, as if the agent had set it. A VM file that cannot be read or decoded is replaced by the launcher's content.
- **A file with no keys left is removed**, as is the base when the launcher has no content.
- **Siblings are still removed.** The other names of the config-file family (`ConfigFileNames`) are deleted on every run, so a stale sibling cannot shadow the main config.
- **Reconfig ignores the agent's own writes.** `ChangedAgentConfigPaths` compares the merged snippets with the recorded base, and with the VM file only when no base exists.

## Rationale

Deleting or resetting the main config on every run lost every in-VM setting (codex trust, `/model`, goose's telemetry answer, pi's default model) once #775 made the drop-in opt-in. Removing it only when snippets exist would wipe all in-VM settings at the first snippet. Learn mode remains the opt-in bridge that makes an in-VM setting portable as a snippet.
