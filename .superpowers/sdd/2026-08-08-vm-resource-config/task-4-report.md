### Task 4 Report: Recreate on disk/tmp/image mismatch

## What was implemented

Replaced `handleNeedsReplacement` (image-only check) with `needsRecreation(handle, imageRef, opts RunOptions) bool` that triggers VM recreation on three conditions:

1. **Image digest change** — identical to the original `handleNeedsReplacement` logic
2. **/tmp tmpfs size mismatch** — compares `cfg.Volumes["/tmp"].SizeMiB` against `resolveTmpSizeMiB(opts.TmpSize)`
3. **Root disk size mismatch** — when `opts.DiskSize != ""`, compares `cfg.RootDisk.SizeMiB` against `parseMemory(opts.DiskSize)`

Updated `EnsureProjectVM` to call `needsRecreation(handle, imageRef, opts)` instead of `handleNeedsReplacement(handle, imageRef)`, and updated the verbose log message to reference "image or resource config changed".

Removed `handleNeedsReplacement` entirely — no longer referenced anywhere.

## TDD Evidence

### RED
```
$ go test ./internal/sandbox/ -run TestNeedsRecreation -v
# gitlab.inoio.de/inoio/opencode-msb/internal/sandbox [gitlab.inoio.de/inoio/opencode-msb.internal/sandbox.test]
internal/sandbox/projectvm_test.go:574:14: undefined: needsRecreation
FAIL
```

### GREEN
```
=== RUN   TestNeedsRecreation
=== RUN   TestNeedsRecreation/image_change
=== RUN   TestNeedsRecreation/tmpsize_mismatch
=== RUN   TestNeedsRecreation/disk_mismatch_(explicit)
=== RUN   TestNeedsRecreation/disk_unset_ignores_disk
=== RUN   TestNeedsRecreation/no_change
--- PASS: TestNeedsRecreation (0.00s)
    --- PASS: TestNeedsRecreation/image_change (0.00s)
    --- PASS: TestNeedsRecreation/tmpsize_mismatch (0.00s)
    --- PASS: TestNeedsRecreation/disk_mismatch_(explicit) (0.00s)
    --- PASS: TestNeedsRecreation/disk_unset_ignores_disk (0.00s)
    --- PASS: TestNeedsRecreation/no_change (0.00s)
```

## Files changed

| File | Changes |
|------|---------|
| `internal/sandbox/projectvm.go` | Replaced `handleNeedsReplacement` with `needsRecreation`; updated call site in `EnsureProjectVM` |
| `internal/sandbox/projectvm_test.go` | Added `TestNeedsRecreation` with 5 subtests |

## Test results

**All 50+ tests pass** across all packages:
- `cmd/opencode-msb` — ok
- `internal/sandbox` — ok (11 EnsureProjectVM tests + 5 new TestNeedsRecreation subtests)
- `internal/sandbox/msb` — ok (cached)
- All other packages — ok (cached)

**Pre-existing EnsureProjectVM tests still pass** (verified):
- `TestEnsureProjectVM_CreatePath` — ✅
- `TestEnsureProjectVM_ReconnectPath` — ✅
- `TestEnsureProjectVM_ReconnectWhenImageUnchanged` — ✅
- `TestEnsureProjectVM_RecreatesWhenImageChangedRunning` — ✅
- `TestEnsureProjectVM_RecreatesWhenImageChangedStopped` — ✅
- `TestEnsureProjectVM_NoReplacementWhenExistingImageUnknown` — ✅

The pre-existing tests don't set `Cfg` on `MockSandboxHandle`, so `handle.Config()` returns nil, `needsRecreation` short-circuits to `false` on the config check, and image-change behavior is preserved exactly as before.

**Lint**: `golangci-lint fmt && golangci-lint run` — 0 issues
**Full check**: `make check` — clean

## handleNeedsReplacement removal

✅ Fully removed. The old function was only referenced at `projectvm.go:136` (now changed) and nowhere else.

## Self-review

- The `// TODO` comment for caching `SandboxConfig` is included as mandated by the brief
- `handle.Config()` error/nil returns are properly handled (returns `false`, preserving backward compatibility with mock handles that don't set `Cfg`)
- Root disk nil check handles the case where no disk size was set during creation
- The `/tmp` volume check safely skips when `cfg.Volumes` is nil or the key is absent
- Verbose log message updated from `"image changed; replacing project VM %s (%s → %s)"` to `"image or resource config changed; replacing project VM %s (%s)"` to account for non-image triggers
- No exported signatures changed; only internal function renamed and signature expanded

## Concerns

None. All pre-existing tests pass without modification. The design correctly defers to `false` when config is unavailable, preserving backward compatibility.

## Review Fix (Task 4 — Log Message)

### Finding 1: Verbose log omits imageRef on image-change path

**Before:** `"image or resource config changed; replacing project VM %s (%s)"` with only `name` and `handle.Image()` — the new `imageRef` was dropped entirely.

**After:** When `handle.Image() != imageRef` (image-change trigger):

```
"image or resource config changed; replacing project VM %s (%s → %s)"
```

This restores the old→old image transition: `"image or resource config changed; replacing project VM opencode-msb-vm-myproject (opencode-msb/runner-test:oldDigest → opencode-msb/runner-test:newDigest)"`.

### Finding 2: Log conflates image-change and resource-drift

**Before:** The opaque `(%s)` format gave the same output regardless of whether the trigger was image-change or resource-drift (tmp size / disk size).

**After:** Resource-drift trigger (image unchanged but tmp/disk mismatch):

```
"image or resource config changed; replacing project VM %s"
```

Only shows the VM name — no misleading or fabricated image transition.

### Command

```
$ make check
```

### Output

```
golangci-lint fmt ./...
golangci-lint run ./...
0 issues.
CGO_ENABLED=1 go test ./...
ok  	gitlab.inoio.de/inoio/opencode-msb/cmd/opencode-msb	2.502s
ok  	gitlab.inoio.de/inoio/opencode-msb/internal/config	(cached)
ok  	gitlab.inoio.de/inoio/opencode-msb/internal/git	(cached)
ok  	gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig	(cached)
ok  	gitlab.inoio.de/inoio/opencode-msb/internal/sandbox	0.718s
ok  	gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker	(cached)
ok  	gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb	(cached)
ok  	gitlab.inoio.de/inoio/opencode-msb/internal/sysinfo	(cached)
ok  	gitlab.inoio.de/inoio/opencode-msb/internal/termio	(cached)
ok  	gitlab.inoio.de/inoio/opencode-msb/internal/testutil	(cached)
```

All 10 EnsureProjectVM + TestNeedsRecreation subtests pass. No test asserted log text — confirmed by grepping `projectvm_test.go` for `Verbose|Verbosef`.