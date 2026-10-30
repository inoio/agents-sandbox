---
description: Contract for the `fuzz` workflow (`.github/workflows/fuzz.yml`) — how it discovers targets from the sources (no hardcoded list; one matrix job per target), its triggers and the budget each passes, why it is never a PR gate and not part of `make check`/`make verify`, how the corpus accumulates between runs, how a crasher leaves the runner, and the `make fuzz` knobs it drives.
read_if: Changing `.github/workflows/fuzz.yml`, the `make fuzz` target or its `FUZZTIME`/`FUZZPKG`/`FUZZTARGET` knobs, adding a fuzz target and expecting CI to run it, or deciding whether a fuzzing session belongs in CI.
---

# Fuzz workflow

## Contract

`.github/workflows/fuzz.yml` explores every fuzz target discovered in the module — the persisted state files
(`state.FuzzParseState`, both the per-project and launcher-global documents), size and `--worktree` specs
(`options.FuzzParseOptions`), configured mount-target validation (`mounts.FuzzResolveMountTarget`), the retired
`updater.yaml` (`vm.FuzzParseLegacyUpdaterState`), the project Dockerfile (`image.FuzzRenderDockerfile`), the `home:`
manifest (`homeconfig.FuzzParseHomeSection`), `--older-than` durations (`viperconfig.FuzzParseHumanDuration`), and
the naming package's artifact-name, content-image-reference, and runner-image-reference round-trips
(`naming.FuzzArtifactNameRoundTrip`, `naming.FuzzContentImageRefRoundTrip`,
`naming.FuzzRunnerImageRefRoundTrip`) — on `ubuntu-latest`. These targets are pure Go:
no KVM, no docker daemon, no network. That is why they can run on a GitHub-hosted runner at all, unlike the `msb` smoke
tests and the `integration` job.

| Trigger | Budget passed to `make fuzz` | Why |
|---|---|---|
| `push` to `main` touching `internal/**`, `cmd/**`, or `test/**` | `60s` | the run is attributable to the merge that broke it; depth is not the point here |
| `schedule`, nightly at `17 3 * * *` | `15m` | where depth comes from, accumulating into the corpus |
| `workflow_dispatch`, `fuzztime` input | the input (default `15m`) | an explicit long run before a release |

### Target discovery

The list is derived, not hardcoded. A `discover` job walks every `*_test.go` under `internal/`, `cmd/`, and `test/` that
declares a `func Fuzz…`, emitting one entry per *target* — `{name, path, target}` — and the `fuzz` job builds its matrix
from `fromJSON(needs.discover.outputs.targets)`. Adding a `func Fuzz...` joins the matrix with no workflow edit, including
a second target in an existing package. The merge `paths` filter names the same three trees, because a new target can
only land under one of them, so CI and the Makefile agree on what a target is by construction. `name` is the package path
with `/` flattened to `-` plus the target (e.g. `internal-sandbox-naming-FuzzArtifactNameRoundTrip`), unique across the
matrix, so it keys both the per-target corpus cache and the concurrency group.

One matrix job per target, because `go test -fuzz` accepts exactly one target per run and each target spends the full
`FUZZTIME` budget on its own; the workflow passes `FUZZPKG=<path>` and `FUZZTARGET=<name>` to `make fuzz`, which runs
that target alone with `-fuzz='^Name$'`. A job per target keeps the budget independent — a package with three targets
(otherwise three times the budget in one job, which is what overran the job timeout) now runs them in parallel — and
`fail-fast: false` keeps one target's failure from hiding another's, as `make fuzz` does when it sweeps a package
locally. `timeout-minutes: 45` bounds a single target.

## Invariants

- **Never a gate.** Fuzzing is not a PR check, not a step in `make check`/`make verify` (which mirrors PR CI), and not in
  `ci.yml`'s `release.needs`. A red run reports a crasher; it cannot block a merge or a release. What makes a finding
  durable is the committed corpus, not the run.
- **Discovery can never yield an empty matrix.** The `discover` job exits non-zero when it finds no target, mirroring
  `make fuzz FUZZPKG`, so GitHub's "matrix must contain at least one value" error can never be reached; a target the
  module genuinely lost is a loud failure, not a silent disappear.
- **A cancelled run is a wasted run.** The job's concurrency group is `fuzz-<name>` with `cancel-in-progress: false`, so
  a new run waits for the in-flight one instead of cancelling it mid-budget. GitHub still drops a superseded *pending*
  job, which is intended: only the newest queued run matters.
- **The corpus accumulates in `$GOCACHE/fuzz`, cached on its own per target.** Go writes every input it finds there, so
  the job restores `~/.cache/go-build/fuzz` from an `actions/cache` entry keyed
  `fuzz-corpus-<name>-<run id>-<run attempt>` with a `fuzz-corpus-<name>-` restore prefix: a unique key always saves
  (GitHub caches are immutable, so a fixed key is written once and never updated), and the prefix restores the newest
  entry for that target. It must **not** rely on `setup-go`'s cache for this — that entry is keyed on `go.sum` and is
  never re-saved on an exact hit, so a run's discoveries would be thrown away, and other jobs sharing the key can save it
  first. The corpus is small: a 3s run yields tens of inputs and ~128K. `name` widened from the flattened package path to
  path-plus-target when discovery moved to one job per target, so each target's corpus restarts once under its new key.
- **A crasher leaves the runner as an artifact.** Go writes a failing input to the package's `testdata/fuzz/<Target>/` and
  prints it in the log; the failing job uploads `<path>/testdata/fuzz/<Target>/` for its own matrix entry, so a `cmd/` or
  `test/` target's crasher is captured too. CI does not commit the seed (that needs a push token, which the workflow
  deliberately does not have) — a maintainer or agent commits it, and from then on it runs under `make test`.
- **`FUZZPKG`/`FUZZTARGET` fail loudly.** `make fuzz FUZZPKG=<package path>` narrows to a package; `FUZZTARGET=<func
  name>` narrows to one target, used alone or within `FUZZPKG`. An unknown package, or a target matching nowhere in the
  searched set, exits non-zero — a typo cannot read as a green run over zero targets.
- A schedule runs only from the default branch's copy of the workflow, and always against the default branch: this
  workflow is inert until it merges, and it never fuzzes a PR branch.
