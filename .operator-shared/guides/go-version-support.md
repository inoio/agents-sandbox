---
description: The module's minimum Go version (`go 1.27.0`) and the constraint that sets it - our own code uses Go 1.27 features (`strings.CutLast`, promoted fields as struct-literal keys), above the 1.26.8 dependency floor.
read_if: Changing the `go 1.27.0` directive, `.go-version`, the Dockerfile `GO_VERSION`, or a `actions/setup-go` pin; deciding whether a lower Go version can be advertised or a dependency bump raised the floor; diagnosing a `go` error that a dependency or a promoted-field struct literal requires a newer Go, a `GOTOOLCHAIN` refusal, or the README `go-mod/go-version` badge; or using `strings.CutLast`.
---

# Go version support

## What we advertise

`go.mod`'s `go 1.27.0` is the only stated *minimum*. Everything else is a toolchain *pin*: `.go-version`, the
dev image's `GO_VERSION` (`ARG GO_VERSION=1.27.4`), and every `actions/setup-go` step that reads `.go-version`;
`go.mod` has no `toolchain` directive. The README badge
(`img.shields.io/github/go-mod/go-version/...`) reads the `go` directive, so the advertised minimum tracks `go.mod`.

Per the Go Modules Reference, the `go` directive is a **mandatory minimum** (not advisory) and must be greater than or
equal to the `go` line of every dependency; the compiler also rejects language features newer than the directive.

## The floor is Go 1.27.0, set by our own code

Two constraints stack, and the higher one wins:

- **Dependency floor — 1.26.8.** Among modules that actually provide packages imported (transitively) by our module,
  the highest `go` directive is `github.com/moby/buildkit` at `1.26.8`; next is `github.com/containerd/containerd/v2`
  at `1.26.6`, then a band of `1.26.0` modules. The floor is the same for linux/amd64 and darwin/arm64, so the
  supported platforms agree. Because `go` must be `>=` every dependency's `go` line, the module cannot declare below
  `1.26.8`. (Windows-only modules such as `Microsoft/hcsshim` carry a `go 1.26.8` directive too, but no platform
  provides a package we import, so they are not part of the build list and cannot set the floor here.)
- **Our own code — 1.27.0.** The module uses two features introduced in Go 1.27, so the floor is 1.27.0:
  - `strings.CutLast`, used in `internal/git`, `internal/sandbox/naming`, and `internal/sandbox/envsecret`.
  - A promoted (embedded) field used as a struct-literal key, used in `internal/sandbox/vm` (a `msb.JobSpec` literal
    naming `ExecRequest`'s promoted `User`/`Env`) and `internal/sandbox/docker/dockertest` (an
    `ImageInspectResult` literal naming `InspectResponse`'s promoted `Config`). Go 1.27 generalised struct-literal
    keys to any valid field selector; before that, only the struct's own (top-level) field names were allowed.

No Go 1.27 *patch* feature is in use — API additions land in `.0` releases, not patches — so `1.27.0` is sufficient.

## Why `1.27.0` and not lower

`1.27.0` is the lowest the module can declare, and the directive carries no patch:

- **Below `1.27.0` is impossible.** `go 1.26.8` (the highest sub-1.27 candidate) fails on both counts: the promoted
  fields are rejected (`use of promoted field ... requires go1.27 or later (-lang was set to go1.26)`) and
  `strings.CutLast` is undefined under a real 1.26 toolchain. Candidates below `1.26.8` fail earlier, at module
  selection, on `github.com/moby/buildkit`.
- **No dependency requires a 1.27 patch** (the highest dependency `go` line is `1.26.8`), and API additions land in
  `.0` releases, so no 1.27 patch feature exists to require; the whole tree — production, tests, and the
  `integration`/`msb` tagged files — type-checks and builds under a real `go1.27.0` toolchain. `go 1.27.0` and
  `go 1.27` are equivalent. Pinning a patch here (as `1.27.1`, matching `.go-version`, once did) only makes a user on
  `1.27.0` with `GOTOOLCHAIN=local` fail and `GOTOOLCHAIN=auto` download a patch toolchain, for no benefit.

## Evidence

Dependency floor (identical for `GOOS=linux GOARCH=amd64` and `GOOS=darwin GOARCH=arm64`):

```
$ go list -deps -f '{{if .Module}}{{.Module.Path}} {{.Module.GoVersion}}{{end}}' ./... \
    | grep -v '^$' | awk '$2!=""' | sort -k2 -V | tail -3
github.com/containerd/containerd/v2 1.26.6
github.com/moby/buildkit 1.26.8
github.com/inoio/agents-sandbox 1.27.1
```

Below the dependency floor, from a throwaway module copy outside the repo (`go mod edit -go=<V>` then build):

```
$ GOTOOLCHAIN=go1.26.6 go build ./...
go: github.com/moby/buildkit@v0.35.0 requires go >= 1.26.8 (running go 1.26.6; GOTOOLCHAIN=go1.26.6)
```

At `go 1.26.8`, our own code blocks (real toolchain and, for the language features, the stock 1.27.4 toolchain with
`-lang=go1.26`):

```
$ go mod edit -go=1.26.8 && GOTOOLCHAIN=go1.26.8 go build ./...
internal/sandbox/naming/artifact.go:147:31: undefined: strings.CutLast
internal/git/git.go:196:29: undefined: strings.CutLast
internal/sandbox/envsecret/secrets.go:143:32: undefined: strings.CutLast
internal/sandbox/docker/dockertest/testmock.go:160:3: unknown field Config in struct literal of type "github.com/moby/moby/client".ImageInspectResult

$ go mod edit -go=1.26.8 && GOTOOLCHAIN=go1.27.4 go build ./...
internal/sandbox/docker/dockertest/testmock.go:160:3: use of promoted field InspectResponse.Config in struct literal of type "github.com/moby/moby/client".ImageInspectResult requires go1.27 or later (-lang was set to go1.26; check go.mod)
internal/sandbox/vm/detached_service.go:118:15: use of promoted field ExecRequest.User in struct literal of type msb.JobSpec requires go1.27 or later (-lang was set to go1.26; check go.mod)
internal/sandbox/vm/detached_service.go:118:39: use of promoted field ExecRequest.Env in struct literal of type msb.JobSpec requires go1.27 or later (-lang was set to go1.26; check go.mod)
```

At `go 1.27.0` the module is green (real downloaded toolchain, from the copy):

```
$ go mod edit -go=1.27.0
$ GOTOOLCHAIN=go1.27.0 go build ./...            # linux/amd64, exit 0
$ GOTOOLCHAIN=go1.27.0 go vet -tags 'integration msb' ./...   # exit 0
```

`strings.CutLast` is a Go 1.27 addition (`api/go1.27.txt`: `pkg strings, func CutLast(string, string) (string, string,
bool)`; the Go 1.27 release notes list it under `strings`). A bare `GOOS=darwin GOARCH=arm64 go build ./...` fails, but
for a platform reason, not a version one: with `CGO_ENABLED=0` the microsandbox SDK's cgo-based `internal/ffi` package
is excluded, leaving its types undefined. It fails identically at `1.27.0` and `1.27.1`; our own packages build for
darwin/arm64 at `1.27.0`, and the release darwin build goes through cgo + the zig stubs
(see `specs/release/darwin-cross-compile.md`).

## Primary sources

- Language features per release: Go 1.27 release notes (`https://go.dev/doc/go1.27`) — generic methods, struct-literal
  keys as field selectors.
- Stdlib additions per version: `$(go env GOROOT)/api/go1.27.txt` (and `go1.26.txt`, …).
- `go` directive semantics (mandatory minimum, `>=` every dependency's `go` line, compiler gating of newer features):
  Go Modules Reference, `go` directive (`https://go.dev/ref/mod`); toolchain selection (`https://go.dev/doc/toolchain`).
