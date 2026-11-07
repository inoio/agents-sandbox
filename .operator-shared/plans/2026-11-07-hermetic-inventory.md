# Hermetic envelope inventory and runtime budget

First-run record for the hermetic envelope (Plan 1 of the design
`.operator-shared/plans/2026-11-07-hermetic-test-suite-design.md`). The
envelope is `scripts/test-envelope.sh` via `make test-scrubbed` and
`make test-hermetic`. This inventory scopes the follow-up surface plans.

## Runtime budget

Measured on the dev VM, otherwise idle, Go build cache warm:

| Command | Wall | Notes |
|---|---|---|
| `make test` | ~7 s | reuses Go's **test-result cache** (no `-count=1`) — mostly does not re-run tests |
| `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -count=1 ./...` | ~22 s | the fair baseline: the envelope's `-count=1` forbids the test cache |
| `make test-scrubbed` | ~32 s | envelope in a module copy |
| `make test-hermetic` | ~32 s | strict envelope (adds the network namespace) |

Ratio: the envelope is ~1.5× an uncached `go test` (+~10 s) and ~4.5× a cached
`make test`. The module copy is ~8.6 MB / ~0.5 s; the overhead is the uncached
run plus re-linking in the copy, not the copy itself. That is inside the
"small multiple" the design targets, so promotion to a per-PR run (and
eventually the default) is realistic once the uid/permission surface below is
fixed.

Compare against a fair baseline, never a cached `make test`: the difference is
the test cache, not the envelope.

## Result

- `make test-scrubbed` is **green** (all 49 packages) — no test depends on the
  ambient environment, the CWD being a checkout, or the real home.
- `make test-hermetic` is **red** in 3 packages, all one surface (below).

### Filesystem scratch (resolved in Plan 1)

The first scrubbed run failed the `internal/testutil` module-walk tests because
the envelope put `TMPDIR` inside the copied tree, and the walk read ephemeral
temp files. Fixed by copying the module into `<work>/module` with `HOME` and
`TMPDIR` as siblings. No follow-up needed.

## Failing tests by surface

Groups with no failing test are stated empty.

- **Identity / CWD** — empty. The copy has no `.git`; nothing tripped on it.
- **Host resource figures (RAM/CPU)** — empty. The values are unchanged by the
  envelope; this surface needs the sentinel pass, not the envelope.
- **Environment (ambient/env/secret)** — empty. The `env -i` scrub and the
  fail-fast seams already cover it.
- **Network** — empty. No test failed under the loopback-only namespace; the
  suite does not reach the network (network-needing tests stay `integration`-tagged).
- **Wall-clock** — empty. The envelope does not skew time; this surface needs the
  `time.Sleep` gate category, not the envelope.
- **uid / permission (newly surfaced)** — 6 tests, 3 packages. These fixtures
  make a file unreadable/unwritable with `chmod` and expect an I/O error; under
  the strict run's `unshare --net --map-root-user` the suite runs as uid 0, so
  the permission bits do not deny access and the expected error never occurs.
  The result depends on the effective uid — a real dependency on the execution
  environment, so it belongs to this work, not to the envelope:
  - `internal/configmerge` — `TestScanMirrorSkipsUnreadableEntries`
  - `internal/sandbox/runtimemigration` — `TestDetectLegacyArtifactsMarksUnreadableVolumes`,
    `TestShowNoticeUnreadableVolumesSuppressesWholesaleCommand`
  - `internal/sandbox/vm` — `TestAdoptLegacyIdentityPersistRecoveredVolumeFailureFailsClosed`,
    `TestAdoptLegacyIdentityClaimCommitFailureFailsClosed`,
    `TestRecordImageProvenanceWarnsWhenStateCannotBeWritten`
- **Other** — empty.

## Follow-up scope

The uid/permission surface is the one concrete work item the envelope produced.
The fix is to make those six tests independent of the effective uid by injecting
the failing reader/writer (a seam that returns an error) instead of relying on
`chmod` denial — which is also what lets `make test-hermetic` run under
`--map-root-user`. The identity/CWD, host-figure, and wall-clock surfaces are not
exercised by the envelope; they get their own enforcement (the git seam, the
host-figure sentinel pass, the `time.Sleep` gate category) per the design.
