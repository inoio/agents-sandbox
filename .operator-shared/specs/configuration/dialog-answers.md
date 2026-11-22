---
description: Contract for recurring run-path dialogs (#845) - each gets a launcher config key whose values are its answers plus `ask`, a shipped default that never loses data, one info line when a configured answer applies, and a "remember this answer" offer under `ask` that writes the owning config layer.
read_if: Adding or changing a recurring interactive dialog (a `termio.Prompter.Select` on the run path), its config key, or its non-interactive fallback; or touching `home-volume.on-image-change`, `volume.ResolveHomeAction`, or `rememberHomeVolumeAnswer`.
---

# Configurable dialog answers

## Contract

- **One key per recurring dialog.** Its values are the dialog's answers plus `ask`; `ask` shows the dialog as it was. The key is an ordinary launcher key (config file and env var), validated against that set.
- **The shipped default never loses anything** and does not interrupt. `home-volume.on-image-change` defaults to `keep`.
- **A configured answer is visible.** It applies without the dialog and prints one info line naming the answer and the key.
- **Remembering under `ask`.** After an answer (not quit), the dialog offers, defaulting to no, to make it the configured one. The write goes to the user config layer that sets `ask`, so it takes effect. The answer is a personal preference, so a project config that sets the key is never rewritten (the write is refused with a pointer to it), an environment variable that would override the write refuses it, and a skip-VM (`--dry-run-vm`) run offers nothing.
- **Non-interactive under `ask`** keeps the dialog's former non-interactive fallback.
- **Never configurable:** the project-config trust approval, whose answer must stay a human decision.

## Applied

- `home-volume.on-image-change: keep | migrate | reset | ask` (default `keep`). Further candidates are listed in #845.
