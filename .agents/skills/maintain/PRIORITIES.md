# Priorities

Priority is orthogonal to the category/state machine: an issue can be
`needs-info` and still `priority:high`. Priority communicates *when* work is
worth scheduling, not *what stage* it is at.

Use only the labels that already exist on the repo:

| Label               | Meaning                                                     |
| ------------------- | ----------------------------------------------------------- |
| `priority:critical` | Blocks a release or is a serious bug; act now.              |
| `priority:high`     | Important; schedule next.                                   |
| `priority:medium`   | Should be done; not scheduled ahead of `high` work.         |
| `priority:low`      | Nice to have; no committed timeline.                        |

## Application rules

- **Propose, never auto-set.** The skill proposes a priority in the batch plan;
  it is applied only when the maintainer approves that row.
- **Unset is valid and common.** When there is no committed urgency, propose no
  priority. Never invent urgency to make an issue look complete.
- **One priority at most.** If an issue already carries a `priority:*` label,
  propose a change only when there is a clear reason, and name the reason.
- **Removing is an action.** If an existing priority is wrong, propose removing
  it (`remove priority:low`) rather than silently leaving it or swapping it.

## Reading the rubric

- `critical` is reserved: a release blocker or a serious bug (data loss, a
  security issue without a workaround, a crash on a main path). When in doubt,
  it is not critical.
- `high` means the maintainer should schedule it, not that it is urgent this
  instant.
- `medium` is work worth doing that is not queued ahead of `high`.
- `low` is the honest default for "good idea, no timeline" — including most
  tech-debt and upstream-blocked work.
- Absence of a label is not `low`; it means priority has not been decided.
