# BuildKit runner-image build Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the runner image with BuildKit when the engine recommends it (a `github.com/moby/buildkit/client` build against the daemon's embedded builder), keep the classic moby `ImageBuild` path for podman, expose a `type=gha` build-cache surface, and make the CI integration job cached (main writes, PRs restore) and required.

**Architecture:** `buildImage` pings the daemon once and dispatches: `Builder-Version: 2` -> a BuildKit build over the Docker API's `/grpc` + `/session` hijack (context and registry auth carried by a BuildKit session), `1`/empty -> the existing classic `ImageBuild`. Both paths produce the same tagged Docker image, so `ImageInfo` and every downstream consumer stay builder-agnostic. Two config keys feed BuildKit cache import/export entries; the CI sets them per event.

**Tech Stack:** Go 1.27, `github.com/moby/buildkit/client`, `github.com/moby/buildkit/session` (+ `filesync`/`authprovider`), the existing `moby/moby/client` Docker API client, GitHub Actions workflows.

**Spec:** `.operator-shared/specs/runner-image/buildkit-build.md`. The CI half of the contract is owned by `.operator-shared/specs/ci/ci-pipeline.md` and is folded there by Task 6 (the spec is not edited ahead of the implementation). Source-backed engine facts: `.operator-shared/guides/buildkit-engine-compatibility.md`.

## Global Constraints

- Pin `github.com/moby/buildkit` at `v0.34.2`; it must not pull `github.com/moby/moby` into the graph (it does not — keep it so). Run `go mod tidy` after.
- Builder selection is from `docker.Client.Ping(ctx).BuilderVersion` only, once per build command; never a build-time probe, never a retry into the other builder.
- The classic path's behaviour is unchanged; the BuildKit path must produce the same content for the same render, so `ImageInfo` stays the only downstream interface.
- The two cache config keys are `image-cache-from` and `image-cache-to` (`[]string`, env-bound, no CLI flag); empty means no cache entry.
- Concrete types are `client.CacheOptionsEntry`, `client.SolveOpt`, `client.ExportEntry`, `session.Attachable` from the buildkit module.
- New and changed production lines are covered by tests; run `make fmt`, `make lint`, `make compile-all`, and `make check` before the final commit.
- Package-facing identifiers stay self-documenting: no `bk`, `so`, `cf` abbreviations in production code.

## Review Focus

1. **Builder-agnostic output** — a BuildKit build must yield the same tagged Docker image (same `ImageInspect` digest surface) the classic path produced, or every downstream step (msb load, prune, reconfig) silently breaks. Task 4 test.
2. **Private-base-image auth** — a private `FROM` must resolve on the BuildKit path as it does on the classic path; the session must carry the docker credentials. Task 4 test / host check.
3. **No silent downgrade** — a BuildKit build failure must surface, never fall back to classic. Task 2 test.
4. **Cache neutrality** — a disabled, empty, or missing cache must be indistinguishable from a cache miss and must not alter the image. Task 3 and Task 5 tests.
5. **Podman stays on classic** — `Builder-Version: 1` must take the classic path. Task 2 test.

---

## Part 1 — the build path

### Task 1: The `DialHijack` docker seam

**Files:**
- Modify: `internal/sandbox/docker/docker.go`
- Modify: `internal/sandbox/docker/dockertest/testmock.go`
- Test: `internal/sandbox/docker/docker_test.go`

**Interfaces:**
- Produces: `DialHijack(ctx context.Context, url, protocol string, meta map[string][]string) (net.Conn, error)` on `docker.Client`; `realDockerClient` delegates to `mobyClient.DialHijack` after `ensureMobyClient()`; `dockertest.MockDockerClient` gains a `DialHijackFn` field (and method), and `dockertest.failFastDockerClient` gains the method so it still satisfies `docker.Client`.

- [ ] **Step 1: Write the failing test**

Add to `internal/sandbox/docker/docker_test.go` (the `ensureMobyClient` package-var seam the other construction-failure tests already use):

```go
func TestRealClientDialHijackConstructionFailure(t *testing.T) {
	wantErr := errors.New("no docker")
	orig := ensureMobyClient
	ensureMobyClient = func() error { return wantErr }
	t.Cleanup(func() { ensureMobyClient = orig })

	_, err := (realDockerClient{}).DialHijack(context.Background(), "/grpc", "h2c", nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("DialHijack error = %v, want %v", err, wantErr)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/docker/ -run TestRealClientDialHijackConstructionFailure`
Expected: FAIL — `DialHijack` is not defined on `docker.Client`/`realDockerClient`.

- [ ] **Step 3: Add the method**

Add `DialHijack` to the `Client` interface (import `net`), implement it on `realDockerClient` mirroring the other methods' `ensureMobyClient` guard, and add `DialHijackFn` + the method to `dockertest.MockDockerClient` and the method to `dockertest.failFastDockerClient` (both in `dockertest/testmock.go`). The mock's interface-conformity check now requires it, so let the compiler drive the additions.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/docker/`
Expected: PASS, including the existing interface-completeness test.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/docker/docker.go internal/sandbox/docker/dockertest/testmock.go internal/sandbox/docker/docker_test.go
git commit -m "feat(docker): expose DialHijack for the BuildKit path"
```

### Task 2: Builder selection and dispatch

**Files:**
- Create: `internal/sandbox/image/builder.go`
- Modify: `internal/sandbox/image/docker_build.go`
- Test: `internal/sandbox/image/builder_test.go`

**Interfaces:**
- Produces: `type builderKind int` with `builderClassic`/`builderBuildKit` and `func selectBuilder(ctx context.Context, dclient docker.Client) (builderKind, error)`.
- Produces: `BuildOptions.CacheFrom`/`CacheTo []string` (struct in `internal/sandbox/image/build.go`) threaded into `buildRequest{plan, baseDigest, docker, cacheFrom, cacheTo}`.
- Consumes: `docker.Client.Ping(ctx, client.PingOptions{})` (the two-argument form the seam exposes today); the `BuilderVersion` constants live in `github.com/moby/moby/api/types/build` (imported as `build`).

- [ ] **Step 1: Write the failing tests**

`internal/sandbox/image/builder_test.go`:

```go
func TestSelectBuilder(t *testing.T) {
	cases := []struct {
		name string
		ping build.BuilderVersion
		want builderKind
	}{
		{"buildkit", build.BuilderBuildKit, builderBuildKit},
		{"classic advertises 1", build.BuilderV1, builderClassic},
		{"empty means classic", "", builderClassic},
	}
	// for each case: a &dockertest.MockDockerClient{PingFn: ...} returns
	// client.PingResult{BuilderVersion: c.ping}; assert the selectBuilder result.
}

func TestSelectBuilderPropagatesPingError(t *testing.T) {
	// PingFn returns errors.New("cannot connect to Docker daemon"); assert the error surfaces.
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/ -run 'TestSelectBuilder'`
Expected: FAIL — `selectBuilder` / `builderKind` undefined.

- [ ] **Step 3: Implement `selectBuilder` and dispatch**

Implement `selectBuilder` calling `dclient.Ping(ctx, client.PingOptions{})`, mapping `build.BuilderBuildKit` to `builderBuildKit` and everything else to `builderClassic`, and returning the ping error unwrapped. In `buildImage`, call `selectBuilder` first, dispatch to `buildImageClassic` (the current body of `buildImage`) or `buildImageBuildKit` (Task 4), and rewrap `line`/`noCache` unchanged. Do not add a retry: a buildkit error is returned as-is.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/ -run 'TestSelectBuilder|TestBuildImage'`
Expected: PASS. The existing classic-path tests still pass because the mock's default ping is empty/`BuilderV1`.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image/builder.go internal/sandbox/image/builder_test.go internal/sandbox/image/docker_build.go
git commit -m "feat(image): select the builder from the engine's Builder-Version"
```

### Task 3: BuildKit solve-option construction

**Files:**
- Create: `internal/sandbox/image/buildkit_build.go`
- Test: `internal/sandbox/image/buildkit_build_test.go`

**Interfaces:**
- Produces: `func buildKitSolveOpt(req buildRequest, tag string, noCache bool) (client.SolveOpt, func(), error)` — the returned cleanup removes the context temp dir.
- Produces: `func writeBuildKitContext(dir string, dockerfile []byte) error` — writes `Dockerfile` plus every `buildContextFiles()` entry with its mode.

- [ ] **Step 1: Write the failing tests**

`internal/sandbox/image/buildkit_build_test.go` asserts, on a `buildKitSolveOpt` result:

- `Frontend == "dockerfile.v0"` and `FrontendAttrs["filename"] == "Dockerfile"`.
- `LocalDirs` has `context` and `dockerfile` both set to the same temp dir.
- `Exports == []client.ExportEntry{{Type: "moby", Attrs: map[string]string{"name": tag}}}`.
- `NoCache == noCache`.
- `CacheImports`/`CacheExports` are empty when `req.cacheFrom`/`cacheTo` are empty, and each entry passes through verbatim (`Type: "gha"`, attrs parsed from `type=gha,scope=x,mode=max`).
- `writeBuildKitContext` produces a dir containing `Dockerfile` and each `buildContextFiles()` name at the right mode.
- `cleanup()` removes the dir.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/ -run 'TestBuildKit'`
Expected: FAIL — `buildKitSolveOpt` undefined.

- [ ] **Step 3: Implement the solve-option construction**

Implement `writeBuildKitContext` (reuse `buildContextFiles()` so the context has one source of truth with the classic tar) and `buildKitSolveOpt` (`os.MkdirTemp`, `writeBuildKitContext`, `client.SolveOpt{Frontend: "dockerfile.v0", FrontendAttrs: {"filename": "Dockerfile"}, LocalDirs: {"context": dir, "dockerfile": dir}, Exports: ..., NoCache: noCache, CacheImports: cacheEntries(req.cacheFrom), CacheExports: cacheEntries(req.cacheTo)}`). Parse each cache spec with `strings.Cut`/`strings.Split` into a `client.CacheOptionsEntry`; leave the syntax unvalidated beyond shape so the caller's value passes through to BuildKit.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/ -run 'TestBuildKit'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image/buildkit_build.go internal/sandbox/image/buildkit_build_test.go
git commit -m "feat(image): construct the BuildKit solve options"
```

### Task 4: The BuildKit build and its session

**Files:**
- Modify: `internal/sandbox/image/buildkit_build.go`
- Modify: `internal/sandbox/image/docker_build.go` (add `buildImageClassic`, dispatch already added in Task 2)
- Test: `internal/sandbox/image/buildkit_build_test.go`
- Test: `internal/sandbox/image/buildkit_integration_test.go` (new, `integration` tag)

**Interfaces:**
- Produces: `func buildImageBuildKit(ctx context.Context, dclient docker.Client, a agent.Agent, req buildRequest, tag string, noCache bool, line func(string)) error`.
- Produces (seam): `var newBuildKitClient = func(ctx context.Context, dclient docker.Client) (*client.Client, error)` — unit tests replace it.
- Consumes: `docker.Client.DialHijack` (Task 1), `buildKitSolveOpt` (Task 3).

- [ ] **Step 1: Write the failing tests**

`buildkit_build_test.go`:

- `TestBuildKitClientDialers`: drive the dialer pair `newBuildKitClient` builds and assert the control dialer calls `DialHijack(ctx, "/grpc", "h2c", meta)` and the session dialer calls `DialHijack(ctx, "/session", proto, meta)` (record the arguments via the mock's `DialHijackFn`).
- `TestBuildImageBuildKitReportsSolveFailure`: replace `newBuildKitClient` with a stub whose `Solve` returns an error; assert `buildImageBuildKit` returns a `buildFailureError`-wrapped error and never calls the classic path.
- `TestBuildImageBuildKitStreamsStatus`: a stub client whose `Solve` writes one status line; assert `line` received it.

`buildkit_integration_test.go` (`//go:build integration`): `TestBuildImageBuildKitRealDocker` — build a minimal Dockerfile through `buildImageBuildKit` against the real daemon (skips when `Docker-Version` is not `2`), and assert the tagged image is inspectable with a digest.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/ -run 'TestBuildKit|TestBuildImageBuildKit'`
Expected: FAIL — `buildImageBuildKit` / `newBuildKitClient` undefined.

- [ ] **Step 3: Implement the BuildKit build and the client**

Add the module: `go get github.com/moby/buildkit@v0.34.2 && go mod tidy`. Implement `newBuildKitClient` as `buildkitclient.New(ctx, "", client.WithContextDialer(controlDial), client.WithSessionDialer(sessionDial))` where the dialers close over `dclient.DialHijack`. Implement `buildImageBuildKit`: build the solve options, construct `[]session.Attachable` (a `filesync`/`localprovider` provider over the context dir and an `authprovider` docker-config provider), run `Solve` with `SolveOpt.Session = attachables` and stream `SolveStatus` lines through `line`, close the client, run the cleanup, and wrap any error with `buildFailureError`. The `agent`/`req` are used only for the context and tag; no classic-path type is reused beyond `buildRequest`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/ -run 'TestBuildKit|TestBuildImageBuildKit'`
Expected: PASS. Then, on a host: `CGO_ENABLED=1 go test -tags 'integration microsandbox_ffi_path' ./internal/sandbox/image/ -run TestBuildImageBuildKitRealDocker`
Expected: PASS (skips on an engine that does not advertise BuildKit).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/sandbox/image/buildkit_build.go internal/sandbox/image/buildkit_build_test.go internal/sandbox/image/buildkit_integration_test.go
git commit -m "feat(image): build the runner image with BuildKit"
```

### Task 5: The cache config surface

**Files:**
- Modify: `internal/viperconfig/config.go`
- Modify: `internal/viperconfig/configkeys_test.go`
- Modify: `internal/sandbox/options/options.go` (`RunOptions`)
- Modify: `internal/runoptions/` (thread the config values)
- Modify: `internal/sandbox/prepare/prepare.go` (`buildOptions`)
- Modify: `internal/sandbox/image/build.go` (`buildFromPlan` sets `buildRequest.cacheFrom/cacheTo`)
- Test: `internal/viperconfig/configkeys_test.go`, `internal/sandbox/image/build_test.go`

**Interfaces:**
- Produces: `Config.ImageCacheFrom`/`ImageCacheTo []string` (`mapstructure:"image-cache-from"`/`"image-cache-to"`); no CLI flag registered (the registry skips keys without one).
- Produces: `options.RunOptions.CacheFrom`/`CacheTo []string`; `image.BuildOptions.CacheFrom`/`CacheTo []string`; `buildRequest.cacheFrom`/`cacheTo`.
- Consumes: Task 3's `buildKitSolveOpt` (already reads `buildRequest.cacheFrom`/`cacheTo`).

- [ ] **Step 1: Write the failing tests**

- `configkeys_test.go`: add `image-cache-from`/`image-cache-to` to the golden env-key list (`AGENTS_SANDBOX_IMAGE_CACHE_FROM`/`_TO`) and assert neither is in the flag-bind set.
- `build_test.go`: `TestBuildRequestCarriesCacheEntries` — with `BuildOptions{CacheFrom: []string{"type=gha"}, CacheTo: []string{"type=gha,scope=x,mode=max"}}`, assert the `buildRequest` the build constructs carries them (via the `newBuildKitClient` seam or a captured solve opt).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./internal/viperconfig/ ./internal/sandbox/image/ -run 'TestConfigKeys|TestBuildRequestCarriesCacheEntries'`
Expected: FAIL — the keys and fields do not exist.

- [ ] **Step 3: Add the config keys and thread them**

Add the two `Config` fields, thread them through `runoptions.Build` into `RunOptions`, through `prepare.buildOptions` into `BuildOptions`, and through `buildFromPlan` into `buildRequest`. Update the config documentation (`docs/configuration/`, or the closest existing page) and the golden test. Do not register a CLI flag for either key.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./internal/viperconfig/ ./internal/sandbox/image/ ./internal/runoptions/ ./internal/sandbox/prepare/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/viperconfig internal/sandbox/options internal/runoptions internal/sandbox/prepare internal/sandbox/image docs
git commit -m "feat(config): add image-cache-from/image-cache-to build cache keys"
```

## Part 2 — the CI build cache and the required check

### Task 6: CI cache wiring and the required integration check

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `.operator-shared/specs/ci/ci-pipeline.md`
- Create: `changelog.d/<slug>.changed.md`

**Interfaces:**
- Consumes: the `image-cache-from`/`image-cache-to` env keys (Task 5).

- [ ] **Step 1: Set the cache env per event**

In the `integration` job, set `AGENTS_SANDBOX_IMAGE_CACHE_FROM` on every run that builds, and `AGENTS_SANDBOX_IMAGE_CACHE_TO` only on a `push` to `refs/heads/main` (the single writer), with `mode=max`. A PR run gets `from` only, so it never publishes a merge-ref cache. Confirm against the current `changes`/`concurrency` blocks that a PR now reaches the job (see Step 2) without changing the gate's other three paths.

- [ ] **Step 2: Run integration on PRs and make it required**

Extend the `integration` job condition so a `pull_request` (with `code=true`) runs it, and remove `integration` from the opt-in-only set. Keep the merge-queue consequence in view: a `merge_group`-skipped required job reports Success (the gating invariant), so the queue does not pay the build.

**Outstanding post-merge action (do not run now).** The `protect main` ruleset must additionally require the `integration` check, whose context is the job's `name:` — `integration (docker build)`, not `integration` — so an admin applies this after the workflow change lands on `main`:

```console
gh api -X PUT repos/inoio/agents-sandbox/rulesets/<id> --input - <<'JSON'
{ "rules": [ { "type": "required_status_checks", "parameters": { "required_status_checks": [
  {"context": "lint"}, {"context": "test"}, {"context": "race"},
  {"context": "deadcode"}, {"context": "changelog"}, {"context": "integration (docker build)"} ] } } ] }
JSON
```

(fetch the existing ruleset and merge the `integration (docker build)` context rather than replacing the rule wholesale). Until this lands, the job runs on every code-affecting PR but is not yet enforced.

- [ ] **Step 3: Fold the CI spec and add the changelog fragment**

Rewrite the `integration` gating paragraphs in `.operator-shared/specs/ci/ci-pipeline.md` for the new contract (cached, required, main-writer/PR-restore, merge-group behaviour) and the cache env. Add a `changelog.d/<slug>.changed.md` bullet describing the cached, required integration check.

- [ ] **Step 4: Verify what can be verified locally**

Run: `make lint` and `ci/check-changelog.sh . --changed <(git diff --name-only main)`
Expected: PASS. The workflow itself runs only on GitHub; a maintainer confirms the first cached PR run lands under the target time and the queue stays green.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/ci.yml .operator-shared/specs/ci/ci-pipeline.md changelog.d
git commit -m "ci: cache the integration build and make it required"
```

### Task 7: Documentation

**Files:**
- Modify: `docs/troubleshooting.md`
- Modify: `docs/configuration/` (the cache keys)
- Modify: `README.md` and/or `docs/install/installation.md` (the engine note)
- Modify: `changelog.d/<slug>.changed.md`

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Document the per-engine build divergence**

In `docs/troubleshooting.md`, under the engine list, state that the dockerd family builds with BuildKit and podman (classic) does not, so a Dockerfile using heredoc `RUN` or `RUN --mount` builds on Docker/colima/OrbStack/Rancher/Lima and fails on podman, with the classic builder's error. Keep the existing tested/untested/unsupported framing.

- [ ] **Step 2: Document the cache keys**

Add the two cache keys to the configuration docs (with a one-line "CI uses this; local builds leave it empty"). The spec, plan, and engine-compat guide are already cataloged in `.operator-shared/catalog.md` (landed ahead of implementation), so no catalog change is needed here.

- [ ] **Step 3: Verify the docs gate**

Run: `make docs-linkcheck` and `ci/check-docs.sh`
Expected: PASS.

- [ ] **Step 4: Run the full gate**

Run: `make check`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add docs README.md changelog.d
git commit -m "docs: document the BuildKit build path and its engine divergence"
```

---

## Self-Review

- **Spec coverage:** builder selection -> Task 2; the BuildKit path (client, session, frontend, exporter) -> Tasks 3-4; the cache surface -> Tasks 3 and 5; the engine contract -> Tasks 2 and 7; the CI cache/required check -> Task 6. The spec's "no silent downgrade", "builder-agnostic output", and "cache neutrality" invariants each have a Review Focus line and an owning task.
- **Step scan:** each step names one action and a checkable result; no step transcribes a body the signature and tests determine, and no step decides nothing.
- **Type consistency:** `builderKind`/`selectBuilder`, `buildRequest.cacheFrom`/`cacheTo`, `buildKitSolveOpt`, `newBuildKitClient`, `buildImageBuildKit`, `image-cache-from`/`image-cache-to`, `AGENTS_SANDBOX_IMAGE_CACHE_FROM`/`_TO` are used with the same names throughout.
- **Review Focus:** five lines, each pinned to a task test (Tasks 2, 3, 4, 5). The two host-only checks (real BuildKit build, cached CI run) are stated as host/manual verification rather than unit tests.
- **Proportion:** the plan is the wiring the implementer cannot infer; the BuildKit `Solve`/session bodies are delegated to the buildkit source and buildx's docker driver, not transcribed.
- **Scope note:** Part 2 depends on Part 1's cache keys; if a reviewer prefers, Part 2 is self-contained enough to land as a follow-up plan once Part 1 is verified on a host.
