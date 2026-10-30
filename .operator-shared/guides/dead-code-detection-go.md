---
description: How to find production code in this repo that nothing calls but tests keep alive, using x/tools deadcode in its two modes (reachability from main vs -test), the partition between test-only seams and genuinely dead functions, and the triage noise this repo's non-`_test.go` test-support files create.
read_if: Looking for dead or unreferenced production code, auditing whether a seam is still wired, evaluating a dead-code linter, or adding an on-demand dead-code Makefile target.
---

# Dead-code detection for Go — deadcode and the test-usage blind spot

The problem: `staticcheck`'s `unused` check (U1000) *counts test files as usage*, so a function that only `_test.go` calls looks alive. Ditto every "unused symbol" feature built on whole-package analysis. `go vet` does not look for this at all.

`golang.org/x/tools/cmd/deadcode` is the tool that sees it. It builds a call graph with Rapid Type Analysis from **main packages only** and reports unreachable functions. By default it does **not** load test files, so a function referenced only from tests is reported as unreachable — exactly the blind spot. Adding `-test` loads test executables as additional roots, which flips the question to "is this reachable from anything at all".

```sh
go run golang.org/x/tools/cmd/deadcode@latest ./...          # not reachable from production
go run golang.org/x/tools/cmd/deadcode@latest -test ./...   # not reachable from anything
```

## The two-mode partition

Run both and diff. The result sorts every reported function into two buckets:

| Bucket | Meaning | Action |
|---|---|---|
| In default output only | reachable from tests but never from `main` | Decide: deliberate test seam, or a production symbol whose callers were lost |
| In both outputs | reachable from neither | Strong delete candidate |

The default-only bucket is large and mostly benign. Measured on this repo at `origin/main` `e17a06e`: **249** functions in default mode, **1** with `-test` (`internal/sandbox/docker/docker.go:58 RealClient`, in a package already scheduled for removal per the BuildKit plan). So the interesting signal is almost entirely in the default-only bucket, and it needs per-item triage rather than bulk deletion.

## Noise to filter before reading the default output

Test-support code that lives in **non-`_test.go`** files can never be reached from `main` and is always reported. Filter these paths out of the default output:

- `internal/**/testmock.go`, `internal/**/mock.go`
- `internal/testutil/**`
- `test/harness/**` (non-test files behind the `msb` build tag)

What is left over is production files whose functions only tests reach. Real examples from the run above — all zero non-test call sites, verified with `rg`:

- `internal/upgrade/upgrade.go:193 IsNewer` (its own test is named `TestIsNewerExportedWrapper`)
- `internal/homeconfig/homeconfig.go:176 MergeManifests`
- `internal/sandbox/runtime/runtime.go:107 InspectRuntime`
- `internal/agent/agent.go:55 Register`, `internal/sandbox/reprovision/config_files.go:154 AgentConfigPath`, `internal/sandbox/state/root_txn.go:28 RootTxn.Exists`, `internal/viperconfig/load.go:182 NewResolverWithConfig`, … plus intentional seams installed by tests (`vm.SetDaemonHealthFunc`, `vm.SetRuntimeHomeMigrationOffer`, `image.WithMockAgentVersion*`).

Seams are legitimate; the rest are either dead or a missing wiring. `deadcode -whylive=<pkg.Func>` explains why something is *not* dead by printing a shortest call path from `main`, which is the fastest way to tell a seam from an accident.

## Caveats

- **One configuration per run.** The analysis is valid only for a single `GOOS`/`GOARCH`/`-tags` combination, so a function legitimately dead on linux may be live on darwin, or only behind a build tag. This repo has `_darwin.go`/`_linux.go` files and the `integration` and `msb` tags — run the relevant variants (`deadcode -tags integration ./...`) and use the line-oriented output to intersect across runs.
- **Method retention.** A method required to satisfy an interface but never invoked is reported dead; deleting it breaks compilation. Likewise anything reached only through `//go:linkname` is a false positive.
- **Generated files** are excluded by default (use `-generated` to include them); marker interface methods are excluded unconditionally.
- Not a CI gate. Like `make mutation`, this is an on-demand audit — the default output is deliberately noisy and every entry needs judgement.
