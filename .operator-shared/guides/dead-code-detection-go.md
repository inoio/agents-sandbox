---
description: How to find production code in this repo that nothing calls but tests keep alive, using x/tools deadcode in its two modes (reachability from main vs -test), the partition between test-only seams and genuinely dead functions, and the triage noise this repo's non-`_test.go` test-support files create.
read_if: Looking for dead or unreferenced production code, auditing whether a seam is still wired, evaluating a dead-code linter, or working on the `make deadcode` gate or its `//deadcode:ignore` markers.
---

# Dead-code detection for Go — deadcode and the test-usage blind spot

The problem: `staticcheck`'s `unused` check (U1000) *counts test files as usage*, so a function that only `_test.go` calls looks alive. Ditto every "unused symbol" feature built on whole-package analysis. `go vet` does not look for this at all.

`golang.org/x/tools/cmd/deadcode` is the tool that sees it. It builds a call graph with Rapid Type Analysis from **main packages only** and reports unreachable functions. By default it does **not** load test files, so a function referenced only from tests is reported as unreachable — exactly the blind spot. Adding `-test` loads test executables as additional roots, which flips the question to "is this reachable from anything at all".

```sh
# Pin the Makefile's DEADCODE_VERSION (currently v0.52.0), not @latest.
go run golang.org/x/tools/cmd/deadcode@v0.52.0 ./...          # not reachable from production
go run golang.org/x/tools/cmd/deadcode@v0.52.0 -test ./...    # not reachable from anything
```

## The two-mode partition

Run both and diff. The result sorts every reported function into two buckets:

| Bucket | Meaning | Action |
|---|---|---|
| In default output only | reachable from tests but never from `main` | Decide: deliberate test seam, or a production symbol whose callers were lost |
| In both outputs | reachable from neither | Strong delete candidate |

The default-only bucket is large and mostly benign. Measured on this repo at `origin/main` `e17a06e`: **249** functions in default mode, **1** with `-test` (`docker.RealClient`, in a package already scheduled for removal per the BuildKit plan). So the interesting signal is almost entirely in the default-only bucket, and it needs per-item triage rather than bulk deletion.

## Noise to filter before reading the default output

Test-support code that lives in **non-`_test.go`** files can never be reached from `main` and is always reported. Filter these paths out of the default output:

- `internal/**/testmock.go`, `internal/**/mock.go`
- `internal/testutil/**`

What is left over is production files whose functions only tests reach. Triage (recorded in `specs/ci/quality-gates.md`) sorts this class three ways:

- **An intentional seam**, kept in production and marked in source with `//deadcode:ignore reason="…"` — `agent.Register`, `viperconfig.NewResolverWithConfig`, `git.MustRepoSlug`/`git.MustCheckoutSlug`, `image.RunnerDockerfileID`, `docker.RealClient`, `lifecycle.Order`/`(*lifecycle.Trace).Stages`.
- **A convenience helper** reached only from `_test.go`, moved into test code.
- **Genuinely dead code**, deleted.

`deadcode -whylive=<pkg.Func>` explains why something is *not* dead by printing a shortest call path from `main`, which is the fastest way to tell a seam from an accident.

## Caveats

- **One configuration per run.** The analysis is valid only for a single `GOOS`/`GOARCH`/`-tags` combination, so a function legitimately dead on linux may be live on darwin, or only behind a build tag. This repo has `_darwin.go`/`_linux.go` files and the `integration` and `msb` tags — run the relevant variants (`deadcode -tags integration ./...`) and use the line-oriented output to intersect across runs.
- **Method retention.** A method required to satisfy an interface but never invoked is reported dead; deleting it breaks compilation. Likewise anything reached only through `//go:linkname` is a false positive.
- **Runtime types widen liveness.** RTA treats a type's exported methods as potentially callable as soon as a value of that type is converted to an interface, and it walks the boxed type's fields and method signatures too — so the boxed value need only *transitively contain* the receiver type. Boxing a struct whose field or method-signature types reach a deliberately dead method's receiver (e.g. a `Session` field whose method takes `options.RunOptions`, which carries a `*lifecycle.Trace`) keeps that method alive via `reflect`, and `-whylive` then reports it "reachable only through reflection". Removing the boxing, or keeping the boxed value from reaching the receiver's type (e.g. storing a string-only copy instead of the struct that carries it), restores the finding.
- **Generated files** are excluded by default (use `-generated` to include them); marker interface methods are excluded unconditionally.
- **A gate, not just an audit.** `make deadcode` runs `ci/check-deadcode.sh` over the pinned `x/tools/cmd/deadcode` (`DEADCODE_VERSION` in the `Makefile`, currently `v0.52.0`), analysing the four build-tag configurations and failing on any unmarked unreachable function; it is wired into `make verify` and a dedicated CI job. `specs/ci/quality-gates.md` owns the contract. The raw default output stays noisy — run `make deadcode` for the verdict and use the two-mode diff above for triage.
