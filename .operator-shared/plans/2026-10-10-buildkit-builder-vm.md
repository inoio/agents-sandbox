# BuildKit builder VM Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the per-project runner image with no host Docker/Podman daemon, by running `buildkitd` inside a shared microsandbox VM and driving it from the host-side BuildKit Go client over a published TCP port.

**Architecture:** A new `internal/sandbox/builder` package owns a single shared builder VM (pinned `moby/buildkit` image, named `/var/lib/buildkit` cache volume, free host port published on `127.0.0.1`) and exposes a narrow `Builder` interface. `internal/sandbox/image` keeps its orchestration (version resolution, Dockerfile rendering, identity/cache decision) but delegates `docker build` + `docker save` + `docker inspect` to the builder, and loads the exported archive through the unchanged `msb.Client.ImageLoad` boundary. `internal/sandbox/docker` and `doctor.CheckDocker` are removed.

**Tech Stack:** Go (module `github.com/inoio/agents-sandbox`), `github.com/moby/buildkit` (client + `solver`/`exporter` types), `github.com/docker/cli/cli/config` (registry auth), `github.com/superradcompany/microsandbox/sdk/go` via `internal/sandbox/msb`, `internal/termio`.

**Spec:** `.operator-shared/specs/buildkit-builder-vm.md` (research: `.operator/guides/buildkit-inprocess-feasibility.md`; spike: `spike/buildkit-vm/`).

## Global Constraints

- Idiomatic Go; self-documenting, non-abbreviated identifiers; no comments unless they add durable decision value.
- Format/lint with `make fmt` / `make lint` (`golangci-lint`); **never** `go vet`. Run `make check` when finalizing.
- **No host container runtime** in the normal path: no Docker/Podman daemon, no Docker socket, no host `runc`. `internal/sandbox/docker` and `doctor.CheckDocker` are deleted.
- **Free/unique host port per builder VM**, bound on `127.0.0.1` via `msbSdk.WithPortBindings` (never the legacy `WithPorts` map), allocated with `options.FirstFreeHostPort`. Never a fixed host port.
- **Identity compatibility**: rendered Dockerfile, build args, and `org.agents-sandbox.*` labels are unchanged, so existing images and reconfig/provenance logic keep working. `dockerfile-id` does not depend on `BASE_IMAGE`.
- **Delivery boundary unchanged**: images enter the VM cache only through `msb.Client.ImageLoad`; it accepts an OCI Image Layout archive.
- **Platforms**: Linux (KVM) and macOS (Apple Silicon) through the same code path; `platform=linux/amd64` on x86_64, `linux/arm64` on Apple Silicon.
- **Dry-run** must not boot a builder VM or start `buildkitd`.
- Pinned builder image: `moby/buildkit:v0.25.2` (spike-validated).
- Do **not** commit/push on the `/workspace` bind mount — leave finalization to the user.

## Resolved spec decisions

The spec left open items; this plan fixes them:

1. **Export format: `type=oci`** (OCI Image Layout). `microsandbox.Image.Load` accepts it (spike-validated).
2. **Registry credentials: `~/.docker/config.json`** via `github.com/docker/cli/cli/config` (`LoadDefaultConfigFile`, including credential helpers), behind a small provider seam so launcher-config registry entries can be added later. This is what the spike used and it is platform-uniform.
3. **Cache-volume scope: global** (`agents-sandbox-buildkit-cache`).
4. **Base digest resolution**: resolved via a BuildKit gateway `ResolveImageConfig` call before the solve, then passed as the `BASE_IMAGE` build arg. The spec's `containerimage.buildinfo`-after-solve route cannot work because the arg must be set *before* the FROM is resolved; the gateway resolver is the BuildKit-native replacement for `resolveBaseDigest` with no extra dependency. The spec will be updated to match.

## Review Focus

Inputs/conditions the spec implies but no happy-path test exercises; each gets a test in its owning task:

1. A second launcher process racing to create the shared builder VM: the flock serializes creation and both reuse one VM (Task 1).
2. An existing builder VM whose `buildkitd` died (readiness never succeeds): ensure must fail/recreate promptly rather than hang the whole run (Task 2).
3. A base image ref that cannot be resolved (bad/private ref, no creds): a clear error naming the ref and the cause, not a bare gRPC failure (Task 2).
4. Two concurrent `Solve` calls to the same shared builder complete independently (Task 1/2).
5. A non-amd64 host: the `platform` frontend attr is `linux/arm64` (Task 2).

---

### Task 1: Builder VM lifecycle (`builder.VM.Ensure`)

**Files:**
- Create: `internal/sandbox/builder/builder.go`
- Create: `internal/sandbox/builder/vm.go`
- Test: `internal/sandbox/builder/vm_test.go`

**Interfaces:**
- Consumes: `internal/sandbox/msb` (`Client`, `Sandbox`, `SandboxHandle`, errors), `internal/sandbox/options` (`FirstFreeHostPort`, `DefaultVMIdleTimeout` if reused), `internal/sandbox/state` (`FlockExclusive`), `internal/configpaths`, `msbSdk`.
- Produces:
  - Constants: `buildkitVersion = "v0.25.2"`, `builderImage = "moby/buildkit:" + buildkitVersion`, `builderGuestPort uint16 = 1234`, `cacheVolumeName = "agents-sandbox-buildkit-cache"`, `cacheMountPath = "/var/lib/buildkit"`, `builderHostPortBase = 11234`, `builderIdleTimeout = 5 * time.Minute`, `buildkitdCommand = "exec buildkitd --oci-worker-snapshotter native --addr tcp://0.0.0.0:1234"`.
  - `func builderSandboxName() string` → `"agents-sandbox-buildkit-" + buildkitVersion`.
  - `type VM struct { Msb msb.Client; Ready func(ctx context.Context, hostPort int) error }` — `Ready` is the readiness seam (nil → `defaultReady`, implemented in Task 2).
  - `func (v *VM) Ensure(ctx context.Context) (int, error)` — returns the published host port of a ready shared builder VM, creating and starting it if absent. Reads the port of an existing VM from `handle.Config().PortBindings`.
  - `func builderSandboxOptions(hostPort int) []msbSdk.SandboxOption`.

- [ ] **Step 1: Write the failing tests** in `internal/sandbox/builder/vm_test.go`:
  - `TestEnsureCreatesBuilderVM`: fresh `msb.MockMsbClient` (`GetSandboxFn` → not-found; capture `CreatedSandboxCalls`), `Ready` stub returns nil. Assert `Ensure` returns a free port `>= builderHostPortBase`; the created call's name is `builderSandboxName()`; options include a detached sandbox, the cache mount `cacheMountPath` → named `cacheVolumeName`, and a `127.0.0.1` `PortBinding` with the returned host port; the cache volume was created; `buildkitdCommand` was passed to `ShellStream`.
  - `TestEnsureReusesExistingBuilderVM`: `GetSandboxFn` returns a `msb.MockSandboxHandle` whose `Config()` exposes `PortBindings{{HostPort: 4242}}`; assert `Ensure` returns `4242`, makes no `CreateSandbox` call, and does not re-create the volume.
  - `TestEnsureSerializesConcurrentCreation` (Review Focus 1): run two `Ensure` calls concurrently against a mock that fails `GetSandbox` with not-found until the first create; assert exactly one `CreateSandbox` call (flock serialization) and both callers receive the same port.
  - `TestBuilderSandboxNameIncludesVersion`: name equals `"agents-sandbox-buildkit-" + buildkitVersion`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/builder/ -run 'TestEnsure|TestBuilderSandboxName' -count=1`
Expected: FAIL — package/identifiers undefined.

- [ ] **Step 3: Implement `builder.go` + `vm.go`**

`builder.go`: constants, `builderSandboxName`, `VM`, `builderSandboxOptions(hostPort)`. Options: `msbSdk.WithImage(builderImage)`, `msbSdk.WithMounts(map[string]msbSdk.MountConfig{cacheMountPath: msbSdk.Mount.Named(cacheVolumeName, msbSdk.MountOptions{})})`, `msbSdk.WithPortBindings(msbSdk.PortBinding{Bind: "127.0.0.1", HostPort: uint16(hostPort), GuestPort: builderGuestPort, Protocol: msbSdk.PortProtocolTCP})`, `msbSdk.WithNetwork(msbSdk.NetworkPolicy.AllowAll())`, `msbSdk.WithDetached()`, `msbSdk.WithIdleTimeout(builderIdleTimeout)`, `msbSdk.WithReplace()`, `msbSdk.WithLabels(map[string]string{naming.LabelProject ...})` (a builder-specific label, e.g. `{"agents-sandbox.component": "buildkit"}`).

`vm.go` `Ensure`:
1. `name := builderSandboxName()`.
2. Acquire a host-wide flock at `filepath.Join(configpaths.Get().UserStateDir(), "builder-vm.lock")` (`os.MkdirAll` then `state.FlockExclusive`), defer release.
3. `handle, err := v.Msb.GetSandbox(ctx, name)`; if found, read host port from `handle.Config()`; if `> 0`, return it (do not restart `buildkitd`; readiness is Task 2's concern).
4. Ensure the cache volume: `v.Msb.CreateVolume(ctx, cacheVolumeName)`; tolerate an already-exists error (compare `msbSdk.IsKind(err, msbSdk.ErrVolumeAlreadyExists)`; if the wrapper does not surface it, treat any error whose string contains "exists" as success — the spike did the former).
5. `hostPort := options.FirstFreeHostPort(builderHostPortBase)`.
6. `sb, err := v.Msb.CreateSandbox(ctx, name, builderSandboxOptions(hostPort)...)`.
7. `_, err = sb.ShellStream(ctx, buildkitdCommand)`; drain it in a goroutine (mirror output to verbose via an optional `Progress`—omit for now; ensure the handle is not GC'd). A `ShellStream` error fails ensure.
8. Return `hostPort`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/builder/ -run 'TestEnsure|TestBuilderSandboxName' -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + commit**

Run `make lint`. Then:
```bash
git add internal/sandbox/builder/builder.go internal/sandbox/builder/vm.go internal/sandbox/builder/vm_test.go
git commit -m "feat(builder): shared BuildKit builder VM lifecycle"
```

---

### Task 2: Host-side BuildKit client seam, readiness, and registry auth

**Files:**
- Create: `internal/sandbox/builder/client.go`
- Create: `internal/sandbox/builder/auth.go`
- Test: `internal/sandbox/builder/client_test.go`

**Interfaces:**
- Consumes: Task 1's `VM`; `github.com/moby/buildkit/client`, `.../client/session`, `.../client/session/auth/authprovider`, `.../client/llb/sourceresolver`, `.../frontend/gateway`, `github.com/docker/cli/cli/config`.
- Produces:
  - `type buildkitClient interface { ListWorkers(ctx context.Context) error; Solve(ctx context.Context, def *llb.Definition, opt client.SolveOpt, status chan *client.SolveStatus) (*client.SolveResponse, error); ResolveImageConfig(ctx context.Context, ref string, opt sourceresolver.Opt) (string, digest.Digest, []byte, error); Close() error }`
  - `var newBuildkitClient = realNewBuildkitClient` — `func(ctx context.Context, addr string) (buildkitClient, error)`; dials `tcp://127.0.0.1:<port>` and blocks on `ListWorkers` to prove readiness.
  - `func (v *VM) defaultReady(ctx context.Context, hostPort int) error` — retries `newBuildkitClient` until `builderReadyTimeout` (30s), then closes.
  - `func registryAuthSession() []session.Attachable` — `authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{ConfigFile: config.LoadDefaultConfigFile(os.Stderr)})`.
  - `type BuildRequest` / `type BuildResult` (see Task 3).
  - `var Get = func() Builder { return newClient() }`; `type Builder interface { Build(ctx context.Context, req BuildRequest) (BuildResult, error) }`; `type Client struct { Msb msb.Client; vm *VM }`; `func newClient() *Client`.

- [ ] **Step 1: Write failing tests** in `client_test.go`:
  - `TestDefaultReadyRetriesThenSucceeds` (Review Focus 2): override `newBuildkitClient` with a fake that fails the first two dials then succeeds; assert `defaultReady` returns nil and was retried.
  - `TestDefaultReadyTimesOut`: fake always errors; assert a timeout error mentioning the address.
  - `TestRegistryAuthSessionNonNil`: returns one attachable (does not assert credential contents).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/builder/ -run 'TestDefaultReady|TestRegistryAuth' -count=1`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `client.go` (seams + `Build`) + `auth.go`**

`realNewBuildkitClient(ctx, addr)`: `c, err := client.New(ctx, addr)`; `if _, err := c.ListWorkers(ctx); err != nil { c.Close(); return nil, err }`; wrap in `realBuildkitClient` adapter implementing `buildkitClient` (delegates to `*client.Client`; `ResolveImageConfig` is unsupported on the bare client — return an error there; the gateway resolver is used inside `Build`). `defaultReady` loops `newBuildkitClient` with a 250ms backoff until `builderReadyTimeout`, closing any successful client after probing, returning the last error wrapped with the address.

`Build` (full body in Task 3). For this task, implement the connection/dial helpers and `VM.Ensure` wiring only; `Build` may return `errors.New("not implemented")` until Task 3 — but its signature and `Get`/`Builder`/`BuildRequest`/`BuildResult` types land here so Task 3 only fills the body and tests.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/builder/ -run 'TestDefaultReady|TestRegistryAuth' -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + commit**

```bash
git add internal/sandbox/builder/client.go internal/sandbox/builder/auth.go internal/sandbox/builder/client_test.go
git commit -m "feat(builder): BuildKit client seam, readiness, and registry auth"
```

---

### Task 3: The build — context, solve, export, and metadata

**Files:**
- Modify: `internal/sandbox/builder/client.go`
- Create: `internal/sandbox/builder/build.go`
- Test: `internal/sandbox/builder/build_test.go`

**Interfaces:**
- Consumes: Task 2 seam.
- Produces:
  ```go
  type BuildRequest struct {
      ContextDir string            // holds Dockerfile + agents-sandbox-path.sh
      Tag        string
      BaseRef    string            // final-stage base image reference
      BuildArgs  map[string]string // every arg except BASE_IMAGE
      NoCache    bool
      Platform   string            // "linux/amd64" | "linux/arm64"
      Progress   func(line string)
  }
  type BuildResult struct {
      ArchivePath string // OCI image-layout tar on the host
      Digest      string
      Env         []string
      BaseDigest  string // "<BaseRef>@sha256:<...>"
  }
  ```
  - `func HostPlatform() string` — `"linux/amd64"` / `"linux/arm64"` from `runtime.GOARCH`.
  - `var resolveBaseDigest = realResolveBaseDigest` — `func(ctx, bc buildkitClient, baseRef, platform string) (string, error)`.
  - `func (c *Client) Build(ctx context.Context, req BuildRequest) (BuildResult, error)`.

- [ ] **Step 1: Write failing tests** in `build_test.go` using a fake `buildkitClient` and a stub `resolveBaseDigest`:
  - `TestBuildSolveOptsAndExports`: assert the `SolveOpt` has `Frontend == "dockerfile.v0"`, `LocalDirs["context"]`/`["dockerfile"] == req.ContextDir`, `FrontendAttrs["platform"] == req.Platform`, `FrontendAttrs["no-cache"] == ""` iff `req.NoCache`, every `build-arg:<K>=<V>` from `BuildArgs`, and `build-arg:BASE_IMAGE == BaseDigest`; a single export of `Type == "oci"` whose `Output` writes a file; the returned `ArchivePath` exists and is non-empty.
  - `TestBuildParsesExporterMetadata`: fake `Solve` returns `SolveResponse{ExporterResponse: {"containerimage.digest": "sha256:abc", "containerimage.config": <JSON with Env>}}`; assert `BuildResult.Digest`/`Env`.
  - `TestBuildAcceptsOCIExportType` (Review Focus 4): two concurrent `Build` calls on the same fake each get a distinct archive path and complete.
  - `TestResolveBaseDigestWrapsResolutionError` (Review Focus 3): stub resolver returns an error; `Build` wraps it as `resolve base image <ref>: ...` and does not call `Solve`.
  - `TestHostPlatform`: returns `linux/amd64` or `linux/arm64` matching `runtime.GOARCH`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/builder/ -run 'TestBuild|TestResolveBase|TestHostPlatform' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement `Build`**

1. `hostPort, err := c.vm.Ensure(ctx)` (uses `c.Msb`).
2. `bc, err := newBuildkitClient(ctx, fmt.Sprintf("tcp://127.0.0.1:%d", hostPort))`; `defer bc.Close()`.
3. `baseDigest, err := resolveBaseDigest(ctx, bc, req.BaseRef, req.Platform)`; on error wrap `resolve base image %s: %w`.
4. `archivePath := filepath.Join(os.TempDir(), fmt.Sprintf("agents-sandbox-image-%d.tar", time.Now().UnixNano()))` opening a file for the exporter `Output func` (return the `*os.File`).
5. `attrs := map[string]string{"platform": req.Platform}`; for `k,v := range req.BuildArgs { attrs["build-arg:"+k]=v }`; `attrs["build-arg:BASE_IMAGE"]=baseDigest`; if `req.NoCache { attrs["no-cache"]="" }`.
6. `statusCh := make(chan *client.SolveStatus)`; goroutine ranges it and forwards non-empty progress to `req.Progress` (derive a line from `status.Logs[].Data`, trimmed). 
7. `resp, err := bc.Solve(ctx, nil, client.SolveOpt{Frontend: "dockerfile.v0", LocalDirs: map[string]string{"context": req.ContextDir, "dockerfile": req.ContextDir}, FrontendAttrs: attrs, Exports: []client.ExportEntry{{Type: "oci", Output: exportOutput}}, Session: registryAuthSession()}, statusCh)`; close `statusCh` before returning on each path.
8. Parse `resp.ExporterResponse`: `Digest = resp["containerimage.digest"]`; `Env` from `resp["containerimage.config"]` JSON (a `struct{ Config struct{ Env []string } }`).
9. Return `BuildResult{ArchivePath, Digest, Env, BaseDigest: baseDigest}`.

`realResolveBaseDigest`: call `bc` `Build`-as-gateway: if the adapter cannot, implement using `client.Client.Build(ctx, client.SolveOpt{Session: registryAuthSession()}, "", func(ctx, gw gateway.Client) (*gateway.Result, error) { ref, dgst, _, err := gw.ResolveImageConfig(ctx, baseRef, sourceresolver.Opt{Platform: normalizePlatform(platform)}); if err != nil { return nil, err }; resolved = baseRef + "@" + dgst.String(); return nil, nil }, nil)`. Expose it through the adapter as `ResolveImageConfig(ctx, ref, opt)` implemented by exactly that `Build` call so tests fake the interface method.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/builder/ -count=1`
Expected: PASS (whole package).

- [ ] **Step 5: Lint + commit**

```bash
git add internal/sandbox/builder/build.go internal/sandbox/builder/client.go internal/sandbox/builder/build_test.go
git commit -m "feat(builder): solve rendered Dockerfile and export OCI archive with metadata"
```

---

### Task 4: Add the BuildKit dependency

**Files:**
- Modify: `go.mod`, `go.sum`

**Interfaces:** none (build only).

- [ ] **Step 1: Add and tidy**

Run:
```bash
go get github.com/moby/buildkit@v0.25.2 github.com/docker/cli@v28.4.0+incompatible github.com/opencontainers/go-digest
go mod tidy
```
Expected: `go.mod` gains `github.com/moby/buildkit`, `github.com/docker/cli`, and their transitive deps; `go build ./...` succeeds.

- [ ] **Step 2: Build + lint**

Run: `CGO_ENABLED=1 go build ./... && make lint`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build: add moby/buildkit and docker/cli config dependencies"
```

---

### Task 5: Rewire the `image` package onto the builder

**Files:**
- Modify: `internal/sandbox/image/build.go`
- Modify: `internal/sandbox/image/inspect.go`
- Modify: `internal/sandbox/image/image.go` (add `ArchivePath` doc only if needed)
- Test: `internal/sandbox/image/build_pipeline_test.go`, `coverage_test.go`, `extra_test.go`, `image_test.go`, `build_verbose_test.go`, `inspect_test`/identity tests as affected
- Create: `internal/sandbox/image/builder_fake_test.go` (test helper)

**Interfaces:**
- Consumes: `builder.Get()`, `builder.BuildRequest`, `builder.BuildResult`, `builder.HostPlatform()`, `msb.Client.ImageInspect`.
- Produces:
  - `ImageInfo` gains `ArchivePath string`.
  - `userBuildArgs(...)` no longer sets `BASE_IMAGE` (builder adds it).
  - `EnsureImageWithClient(ctx, a, projectDockerfile, projectSlug, buildOpts, ui) (ImageInfo, error)` — same signature; returns `ArchivePath` when a build happened.
  - `EnsureLoaded(ctx, mclient msb.Client, archivePath, imageRef string, ui termio.UI) error` — loads `archivePath`; no-op when `mclient.ImageGet(imageRef)` succeeds; errors when the image is missing from the cache and `archivePath == ""`.
  - `Build(...)` passes `info.ArchivePath`.
  - `inspectRunnerImage`/`buildDecision` read `msb.Get().ImageInspect` (labels + digest + env), not Docker.
  - `builder.MockBuilder{ BuildFn func(...) }` exported for fakes in `image`/`cmd` tests (create in `internal/sandbox/builder/testmock.go`).

- [ ] **Step 1: Write/adjust failing tests**
  - New `TestEnsureImageBuildsViaBuilder`: install `builder.WithMockBuilder(t, fake)` returning `BuildResult{ArchivePath, Digest:"sha256:new", Env:[]string{"PATH=/usr/bin"}}`; `msb.MockMsbClient.ImageInspectFn` returns not-found → `EnsureImageWithClient` calls the builder once, returns `ArchivePath`, `Digest`, `Env`.
  - New `TestEnsureImageSkipsBuildOnCacheHit`: `msb` inspect returns a `Config` whose `Labels[dockerfileIDLabelKey]` matches → builder is **not** called; `ArchivePath` empty; Digest/Env come from the msb config.
  - New `TestEnsureLoadedLoadsArchive`: `msb.ImageGetFn` returns not-found, `ImageLoadFn` records ref; `EnsureLoaded(ctx, mc, archivePath, tag, ui)` loads and returns nil.
  - New `TestEnsureLoadedSkipsWhenCached`: `ImageGetFn` nil; no load; no error; `archivePath` may be empty.
  - New `TestEnsureLoadedErrorsWithoutArchive`: `ImageGetFn` not-found and `archivePath == ""` → error mentioning the missing archive.
  - New `TestBuildDecisionReadsMsbCache`: forced=false, msb inspect present+matching → no build; absent → build.
  - Rewrite the docker-based cases in `coverage_test.go`, `image_test.go`, `extra_test.go`, `build_pipeline_test.go`, `build_verbose_test.go` to use `builder.WithMockBuilder` + `msb.MockMsbClient` (replace `docker.WithDockerMock`, `dockerConfigWith`, `dockerspec` imports).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/image/ -count=1`
Expected: FAIL to compile / assertions fail.

- [ ] **Step 3: Implement**

- Delete `buildImage`, `buildDockerImage`, `scanBuildOutput`, `dockerBuildMessage`, `resolveBaseDigest`, `readImageInfoFromDocker`, `cachedImageMatchesDocker`, and the docker import.
- Add `writeDockerfileContext(dir string, dockerfile []byte) error` writing `Dockerfile` (mode 0644) and `pathMergeAsset` → `pathMergeScript()` (replaces `writeDockerfileTar`; keep `computeDockerfileID` folding `pathMergeScript()`).
- `buildRunnerImage(ctx, a, rendered, rTag, noCache, agentVersion, identity, ui) (builder.BuildResult, error)`: create temp context dir (`os.MkdirTemp`), `writeDockerfileContext`, `line := func(s string){ui.Verbose(s)}`, `res, err := builder.Get().Build(ctx, builder.BuildRequest{ContextDir: dir, Tag: rTag, BaseRef: baseImageRef(rendered), BuildArgs: buildArgStrings(userBuildArgs(...)), NoCache: noCache, Platform: builder.HostPlatform(), Progress: line})`. Wrap with the existing spinner (`ui.Spinner("Ensuring runner image")`). Remove the temp context dir after solve; keep the archive.
- `buildArgStrings(map[string]*string) map[string]string` dereferences pointers.
- `EnsureImageWithClient`: on `needsBuild`, call `buildRunnerImage`; set `ImageInfo{Tag, Digest: res.Digest, Env: parseImageEnv(res.Env), ArchivePath: res.ArchivePath}`. On reuse, read `msb.Get().ImageInspect(ctx, rTag)` for Digest/Env/Labels; still enforce the `deferVersionResolution` re-resolution branch but skip the builder.
- `inspectRunnerImage` uses `msb.Get().ImageInspect`: `Present`, `DockerfileID = cfg.Labels[dockerfileIDLabelKey]`, `Labels = cfg.Labels`, plus `Digest`, `Env`.
- `EnsureLoaded`: per the Produces contract above; remove `cachedImageMatchesDocker`.
- `Build`: `EnsureImageWithClient` then `EnsureLoaded(ctx, msb.Get(), info.ArchivePath, info.Tag, ui)`; `defer os.Remove(info.ArchivePath)` when non-empty.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/image/ -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + commit**

```bash
git add internal/sandbox/image internal/sandbox/builder/testmock.go
git commit -m "feat(image): build via the BuildKit builder; drop the host Docker image store"
```

---

### Task 6: Thread the image archive through VM creation and volume operations

**Rationale:** Task 5 changed `EnsureLoaded`'s third parameter from an ignored `_` to the `archivePath`. Every existing caller bound that slot to `k.Slug`, so the `volume` package and `createProjectVM` now try to load a file named after the slug. Threading the real `ArchivePath` fixes them. This is sound because a build-decision cache hit (msb `ImageInspect`) means the image is already in the msb cache (so the archive is empty and `EnsureLoaded` no-ops), while a build produced an archive to load.

**Files:**
- Modify: `internal/sandbox/vm/vm_lifecycle.go` (`ensureProjectVM`, `createOrAttachAfterLock`, `createProjectVM`; `EnsureLoaded` call)
- Modify: `internal/sandbox/vm/run_orchestrate.go` (`PrepareSandbox` call and its `ResolveHomeVolume` call)
- Modify: `internal/sandbox/volume/state.go` (`ResolveHomeVolume`, `EnsureNewHome`: accept `archivePath`, pass to `PrefillVolume`)
- Modify: `internal/sandbox/volume/volume.go` (`PrefillVolume`, `CopyVolume`: accept `archivePath`, use in `EnsureLoaded`)
- Modify: `internal/sandbox/volume/operations.go` (`CmdMigrate`, `CmdReset`, `CmdEdit`, `volumeOp`: accept `archivePath`; `CmdEdit`'s direct `EnsureLoaded` uses it)
- Modify: `cmd/agents-sandbox/commands_system.go` (`volumeOpFunc` type and the volume command builder: pass `info.ArchivePath` from `image.EnsureImage`)
- Test: `internal/sandbox/vm/{ensure_project_vm,create_project_vm,prepare_sandbox,coverage_*}_test.go`, `internal/sandbox/volume/*_test.go`, `cmd/agents-sandbox/cli_volume*_test.go`

**Interfaces:**
- Consumes: `image.ImageInfo` (now with `ArchivePath`).
- Produces:
  - `ensureProjectVM(ctx, opts, info image.ImageInfo, homeVol, repoPath string, k state.Key, ui termio.UI) (msb.Sandbox, vmBoot, error)`
  - `createOrAttachAfterLock(ctx, client, name, k, info, homeVol, repoPath, opts, ui)`
  - `createProjectVM(ctx, client, name, k, info image.ImageInfo, homeVol, repoPath string, opts, ui)`
  - `createProjectVM` calls `image.EnsureLoaded(ctx, client, info.ArchivePath, info.Tag, ui)` and uses `info.Tag` for `WithImage`/labels and `info.Env` for `buildProjectVMEnv`.
  - `volumeOpFunc func(context.Context, state.Key, volumeName, imageTag, archivePath, currentDigest string, rmOld, dryRun bool, ui termio.UI) error`
  - `volume.CmdMigrate/CmdReset/CmdEdit(ctx, k, volumeName, imageTag, archivePath, currentDigest string, rmOld, dryRun bool, ui)`
  - `volumeOp(...)`, `Manager.PrefillVolume(...)`, `Manager.CopyVolume(...)`, `ResolveHomeVolume(...)`, `EnsureNewHome(...)` each gain an `archivePath string` parameter next to the existing `imageTag string`.

- [ ] **Step 1: Write/adjust failing tests**
  - Update `TestCreateProjectVMPublishesResolvedServeHostPort` and other create/ensure tests to pass an `image.ImageInfo{Tag: "img:tag", ArchivePath: "/tmp/x.tar"}` and assert `EnsureLoaded` received the archive path (via the msb mock's `ImageLoadFn`/`LoadedImages`, given `ImageGetFn` reports not-found).
  - Add `TestEnsureProjectVMUsesArchiveForFirstBoot`: image not in msb cache → `ImageLoad` called with the tag from `info.Tag`.
  - In `internal/sandbox/volume` tests, pass a real archive path through the operations/prefill/copy/edit layers and assert `ImageLoadFn` records it (the 4 currently-failing `TestCmdEdit_LoadImage*`/`TestPrefillVolume_LoadImageFails`/`TestCopyVolume_LoadImageFails` cases).
  - In `cmd/agents-sandbox` volume command tests, assert the builder-produced `ArchivePath` reaches `volumeOpFunc`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/vm/ ./internal/sandbox/volume/ -run 'TestCreateProjectVM|TestEnsureProjectVM|TestPrepareSandbox|TestCmdEdit|TestPrefillVolume|TestCopyVolume' -count=1`
Expected: FAIL to compile / runtime errors.

- [ ] **Step 3: Implement** the signature changes and `EnsureLoaded(info.ArchivePath, info.Tag)` calls; in `PrepareSandbox` pass `imageInfo` instead of `imageInfo.Tag`/`imageInfo.Env`, and pass `info.ArchivePath` into `ResolveHomeVolume`; in the volume command builder pass `info.ArchivePath` into the `volumeOpFunc` closure.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/vm/ ./internal/sandbox/volume/ -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + commit**

```bash
git add internal/sandbox/vm internal/sandbox/volume cmd/agents-sandbox
git commit -m "refactor(vm,volume): thread the built image archive into VM and volume creation"
```

---

### Task 7: Prune the BuildKit cache instead of host Docker images

**Files:**
- Modify: `internal/sandbox/pruning/images.go`
- Modify: `internal/sandbox/builder/vm.go` (add `PruneCache`)
- Test: `internal/sandbox/pruning/images_test.go` (or new `images_dockerless_test.go`), `internal/sandbox/builder/vm_test.go`

**Interfaces:**
- Consumes: Task 1 `VM`.
- Produces:
  - `func (v *VM) PruneCache(ctx context.Context) error` — if no builder VM exists (`GetSandbox` → not found) returns nil; otherwise `sb.Shell(ctx, "buildctl --addr tcp://127.0.0.1:"+builderGuestPort+" prune --filter until=24h --all")` and errors on non-zero exit.
  - `ImageReport` drops `DockerImagesPruned` (or renames it `BuildkitCachePruned bool`) and `PruneImages` calls `builder.Get().PruneCache` via a `Pruner` seam `var pruneBuilderCache = func(ctx) error { ... }` (overridable in tests), plus the existing msb image prune.

- [ ] **Step 1: Write failing tests**
  - `TestPruneCacheSkipsWhenNoBuilderVM`: `GetSandbox` not-found → nil, no `Shell` call.
  - `TestPruneCacheRunsBuildctl`: VM present → `buildctl ... prune --filter until=24h --all` issued.
  - `TestPruneImagesCallsBuilderPrune`: override the seam; assert it is invoked and `pruneDockerImages` no longer participates.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/builder/ ./internal/sandbox/pruning/ -run 'TestPrune' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement** `PruneCache`, replace `pruneDockerImages` with the builder-cache prune, update `ImageReport`/`printImagePruneReport`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/builder/ ./internal/sandbox/pruning/ -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + commit**

```bash
git add internal/sandbox/builder/vm.go internal/sandbox/pruning
git commit -m "feat(pruning): reclaim the BuildKit cache instead of host docker images"
```

---

### Task 8: Remove `internal/sandbox/docker` and `doctor.CheckDocker`

**Files:**
- Delete: `internal/sandbox/docker/` (all files)
- Modify: `internal/sandbox/doctor/doctor.go` (drop `checkDockerFunc`, `CheckDocker`, `realCheckDocker`, and its entry in `collectChecks`)
- Modify: `internal/sandbox/doctor/testmock.go` (remove `checkDockerFunc`, `checkDockerMockFunc`, `MockedCheckDocker`; drop the panic hook from `InstallFailFast`)
- Modify: `internal/testutil/mocks/testutil.go` (drop `docker.InstallFailFastGet()` and the import)
- Modify: `internal/sandbox/msb/main_test.go` (drop `docker.InstallFailFastGet()` + import)
- Modify: `internal/sandbox/image/render_integration_test.go` — delete (replaced by Task 9's builder integration test); it imports `docker`.
- Modify fixtures/tests that call `doctor.MockedCheckDocker`, `docker.WithDockerMock`, `docker.Get`, `docker.RealClient`, or import the docker package: `cmd/agents-sandbox/cli_build_test.go`, `cli_fixture_test.go`, `cli_volume_test.go`, `cli_system_coverage_test.go`, `cli_test.go`, `cli_prune_test.go`, `cli_image_prune_test.go`, `cli_volume_prune_test.go`, `internal/configpaths/main_test.go`, `internal/termio/main_test.go`, `internal/sandbox/volume/*_test.go`, `internal/sandbox/vm/coverage_dryrun_test.go`, `internal/sandbox/vm/ensure_project_vm_test.go`.

**Interfaces:**
- Consumes: Tasks 5-7 (image/pruning no longer import docker).
- Produces: `doctor.CheckAll` verifies `checkPlatform` + msb only.

- [ ] **Step 1: Verify no production importer remains**

Run: `rg -l 'sandbox/docker' internal cmd --glob '*.go' | rg -v '_test\.go|/docker/'`
Expected: empty (only test files and the package itself remain).

- [ ] **Step 2: Delete the package and update doctor + fixtures**

Delete `internal/sandbox/docker`; edit `doctor.go`/`testmock.go`; then remove docker imports and `docker.InstallFailFastGet`/`MockedCheckDocker`/`docker.With*Mock` usages from the listed files (fixtures that only needed a docker mock now need none; build/volume tests use `builder.WithMockBuilder` + `msb.WithMsbMock`).

- [ ] **Step 3: Build + run the full unit suite**

Run: `CGO_ENABLED=1 go build ./... && go test ./... -count=1`
Expected: builds; PASS.

- [ ] **Step 4: Lint + commit**

Run `make lint`. Then:
```bash
git add -A internal cmd
git commit -m "refactor: remove the host Docker client and doctor docker check"
```

---

### Task 9: Integration test — real build in a builder VM, with cache reuse

**Rationale (controller ruling R6):** the test lives in the `image` package, not `builder`. Composing a valid `builder.BuildRequest` needs `baseImageRef`/`userBuildArgs`, which are unexported in `image`; a `package builder` test cannot import `image` (cycle), and a `package builder_test` test still couldn't call those helpers. The `image` package is where the full render → build → load pipeline is owned, and it replaces the deleted `render_integration_test.go`.

**Files:**
- Create: `internal/sandbox/image/integration_build_test.go` (`//go:build integration`)
- Modify: `Makefile` (`test-integration` comment: no docker daemon; msb + KVM + network only)

**Interfaces:**
- Consumes: `image.RenderProjectDockerfile`/`RenderDockerfile`, `image.Build`/`EnsureImageWithClient`, `image.EnsureLoaded`, `builder.Get()`, real msb runtime, a KVM host.
- Produces: nothing other tasks use.

- [ ] **Step 1: Write the tests** (build tag `integration`, each skipped unless `os.Getenv("AGENTS_SANDBOX_INTEGRATION") != ""`):

`TestBuildRunnerImageInBuilderVM`: render the managed runner Dockerfile for the `opencode` agent, run the full `image.Build`/`EnsureImageWithClient` path (real builder VM), assert a non-empty `ArchivePath`/`Digest`, and that `msb.Get().ImageInspect(tag)` then succeeds (i.e. the OCI archive was loaded).

`TestBuilderCacheReuseAcrossVMs`: run the build twice, removing the builder sandbox between runs (cache volume persists), and assert the second build reuses cached layers — follow the spike's `RUN … CACHED` observation (capture the `Progress` stream) or a materially faster second build.

`TestRunnerImageLoginShellPath` (if feasible): build the runner image, create a sandbox from it, run a login shell, and assert the composed `PATH` (custom Dockerfile `ENV PATH` + `/opt/agents-sandbox/bin`) resolves the expected binaries — restoring the end-to-end coverage the deleted `render_integration_test.go` `TestRenderDockerfileLoginShellHasImagePath` provided.

All require KVM/msb and are excluded from the default suite.

- [ ] **Step 2: Run (host-only, requires KVM)**

Run: `CGO_ENABLED=1 go test -tags integration -count=1 -timeout 40m -run 'TestBuildRunnerImage|TestBuilderCacheReuse|TestRunnerImageLoginShell' ./internal/sandbox/image/`
Expected: PASS on a host with msb + network; documented as unrunnable inside the agents-sandbox dev VM (no `/dev/kvm`). The committer must at minimum confirm `-tags integration` COMPILES in the dev VM (`go vet` is banned — use `go test -tags integration -run XXX_none -count=1 ./...` or `go build -tags integration ./...`).

- [ ] **Step 3: Commit**

```bash
git add internal/sandbox/image/integration_build_test.go Makefile
git commit -m "test(image): integration coverage for real image build and cache reuse"
```

---

### Task 10: Docs, spec, CHANGELOG, and brain updates

**Files:**
- Modify: `docs/runner-image.md` (how-it-works / prerequisites: no Docker; msb + KVM only)
- Modify: `docs/install.md`, `docs/commands/*`, `docs/troubleshooting.md` (remove Docker prerequisite/`doctor` docker guidance)
- Modify: `CHANGELOG.md` (`[Unreleased]`: Added — Docker-free BuildKit builder VM; Removed — host Docker requirement)
- Modify: `.operator-shared/specs/buildkit-builder-vm.md` (mark decisions resolved; correct base-digest route to the gateway resolver; note implemented)
- Modify: `.operator-shared/catalog.md` (add this plan entry)
- Cleanup (deferred from Task 8 review): stale "docker mocked" comment in `internal/sandbox/vm/prepare_sandbox_test.go:191`; stale "noop-docker" comment in `cmd/agents-sandbox/cli_run_shell_test.go:21`; drop the dead fake `docker` (and unused `git`) binary setup in `cmd/agents-sandbox/cli_doctor_test.go:56`.

**Interfaces:** none.

- [ ] **Step 1: Update docs + CHANGELOG** per the file list; keep Jekyll link style per `AGENTS.md`.
- [ ] **Step 2: Update the spec** — resolve Open Decisions 1-3, replace the `containerimage.buildinfo` base-digest paragraph with the gateway `ResolveImageConfig` route, and add a one-line "implemented" status.
- [ ] **Step 3: Catalog the plan** in `.operator-shared/catalog.md` under `plans/`.
- [ ] **Step 4: Full check**

Run: `make check`
Expected: fmt/lint (0 issues)/unit tests/docs linkcheck all pass.

- [ ] **Step 5: Commit**

```bash
git add docs CHANGELOG.md .operator-shared/specs/buildkit-builder-vm.md .operator-shared/catalog.md
git commit -m "docs: document Docker-free BuildKit builder; resolve spec decisions"
```

---

## Execution handoff

Run the whole plan with `superpowers:subagent-driven-development` (Task 1→10 are sequentially dependent through the builder/image interfaces; a fresh reviewer per task fits well). The final whole-branch review runs on the most capable available model.

## Rulings expected from the controller

- **Base-digest route**: this plan deliberately overrides the spec's `containerimage.buildinfo` suggestion with a gateway `ResolveImageConfig` call (the arg must exist before the FROM is resolved). Confirm before Task 3.
- **Registry creds first cut**: `~/.docker/config.json` only; launcher-config registry entries are deferred to a later change. Confirm the scope is acceptable.
- **Task 9** cannot run in the agents-sandbox dev VM (no `/dev/kvm`); it must be run manually by the user on a KVM host, like other integration tests.
