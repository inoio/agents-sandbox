# Registry-Pull Delivery Implementation Plan

**Status:** implemented on `opencode/buildkit-builder-vm` (Tasks 1–7); live msb/KVM validation pending on the host.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the runner image to microsandbox by pushing it to the embedded registry during the build and pulling it by reference at VM creation, retiring the OCI-archive round-trip for the msb backend.

**Architecture:** The msb backend's solve exports the image directly to the embedded registry (`type=image`, `push=true`, guest-authority name) instead of writing an OCI archive; the builder returns no archive. At VM creation, callers create sandboxes with `WithImage(<host-dial pull ref>)` + `WithRegistryInsecure()` + `WithPullPolicy(Always when freshly built else default IfMissing)`, holding the registry's keep-alive across `CreateSandbox`. The identity probe reads the registry-qualified ref. The moby backend keeps the archive path unchanged (D7).

**Tech Stack:** Go 1.26, BuildKit Go client (`github.com/moby/buildkit`), `distribution/v3` embedded registry, microsandbox Go SDK `v0.7.7`, Cobra/Viper.

**Spec:** `.operator-shared/specs/registry-pull-delivery.md`

## Global Constraints

- msb backend only this phase; the moby backend and its archive route (`BuildResult.ArchivePath`, `ImageLoad`) must keep working unchanged.
- Anonymous `GET`/`HEAD` on the loopback embedded registry; bearer token required for every mutating method (`POST`/`PUT`/`PATCH`/`DELETE`) — approved security posture (O1).
- Registry binds loopback only; never `0.0.0.0`.
- Push uses the guest authority (`Endpoint.RegistryHost`); pull uses the host-dial authority (`Endpoint.dialAddress`); both address the same repository (distribution storage is authority-independent).
- A freshly built image must never be served stale to a VM: msb's default `IfMissing` short-circuits a same-tag re-push (measured: 0 blobs), so a build implies a refresh.
- `make check` (fmt, lint 0 issues, tests, docs) must stay green after every task.
- Tests drive real seams (`client_test.go` fake buildkit client / `build_test.go` fakeBackend; `msb.MockMsbClient`; `builder.MockBuilder`); no live KVM/msb in unit tests.

## Review Focus

Inputs/conditions the spec implies but whose behaviour is easy to break — each has a test in the owning task:

1. Registry reaped and restarted on a different port between build and VM create — the image must still be served from persisted storage (authority-independent repo path). (Task 3)
2. A rebuild under the stable tag must not boot a VM from the stale msb-cached image. (Task 4)
3. A pull longer than the 30s registry idle timeout must not be aborted by the idle reaper. (Task 3)
4. A stopped/running sandbox still referencing the old image must not break delivery (no forced `ImageRemove`). (Task 4)
5. `prune` clearing the cache registry must not leave a preserved runner-image manifest pointing at deleted blobs; `image list`/`prune` must match registry-qualified refs. (Task 6)

---

### Task 1: Registry read auth and ref accessors

**Files:**
- Modify: `internal/sandbox/builder/cacheregistry/auth.go`
- Modify: `internal/sandbox/builder/cacheregistry/cache.go`
- Test: `internal/sandbox/builder/cacheregistry/auth_test.go`, `internal/sandbox/builder/cacheregistry/cache_test.go`

**Interfaces:**
- Produces: `func (e Endpoint) PullRef(imageTag string) string`; `func (e Endpoint) PushRef(imageTag string) string`; `tokenAuth` allows anonymous `GET`/`HEAD`.
- Consumes: existing `Endpoint.dialAddress()`, `Endpoint.RegistryHost()`.

- [ ] **Step 1: Write the failing auth test**

```go
func TestTokenAuthAllowsAnonymousRead(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/v2/", nil) // no Authorization header
		ok := false
		tokenAuth("secret", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ok = true })).ServeHTTP(rec, req)
		if !ok || rec.Code == http.StatusUnauthorized {
			t.Fatalf("%s read rejected: code=%d", method, rec.Code)
		}
	}
}

func TestTokenAuthRequiresTokenForWrites(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/v2/foo/manifests/x", nil) // no token
		tokenAuth("secret", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token: code=%d, want 401", method, rec.Code)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/sandbox/builder/cacheregistry/ -run TestTokenAuth -v`
Expected: FAIL — reads without a token currently get 401.

- [ ] **Step 3: Allow anonymous reads in `tokenAuth`**

In `auth.go`, before the bearer check, pass the request through when `request.Method` is `http.MethodGet` or `http.MethodHead` (return `next.ServeHTTP(writer, request)`). Keep the constant-time bearer check for all other methods. Update the doc comment to state reads are anonymous on the loopback socket and writes require the token.

- [ ] **Step 4: Write the failing ref test**

```go
func TestEndpointRefsShareRepository(t *testing.T) {
	e := Endpoint{Host: GuestHost, Port: 5000, dialHost: "127.0.0.1"}
	if got := e.PushRef("agents-sandbox/runner-p:opencode-latest"); got != "host.microsandbox.internal:5000/agents-sandbox/runner-p:opencode-latest" {
		t.Fatalf("PushRef = %q", got)
	}
	if got := e.PullRef("agents-sandbox/runner-p:opencode-latest"); got != "127.0.0.1:5000/agents-sandbox/runner-p:opencode-latest" {
		t.Fatalf("PullRef = %q", got)
	}
}
```

- [ ] **Step 5: Implement `PullRef`/`PushRef`**

In `cache.go`, next to `CacheRef`:

```go
// PushRef is the guest-visible reference the builder pushes imageTag to. It uses
// the same authority as the cache reference, which BuildKit resolves auth for.
func (e Endpoint) PushRef(imageTag string) string {
	return e.RegistryHost() + "/" + imageTag
}

// PullRef is the host-side reference microsandbox pulls imageTag from. msb's
// runtime runs on the host and reaches the registry over loopback, so the
// authority differs from the guest-visible PushRef but names the same repository.
func (e Endpoint) PullRef(imageTag string) string {
	return e.dialAddress() + "/" + imageTag
}
```

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/sandbox/builder/cacheregistry/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/builder/cacheregistry/
git commit -m "feat(builder): anonymous registry reads and pull/push ref accessors"
```

---

### Task 2: msb backend exports the image to the registry (no archive)

**Files:**
- Modify: `internal/sandbox/builder/backend.go`
- Modify: `internal/sandbox/builder/client.go`
- Test: `internal/sandbox/builder/backend_test.go`, `internal/sandbox/builder/build_test.go`

**Interfaces:**
- Consumes: `Endpoint.PushRef` (Task 1).
- Produces: `exportSpec` gains `Stream bool`; `msbBackend.Export(pushRef string) exportSpec` returns a registry push spec; `Client.Build` creates an archive only when `spec.Stream`.

- [ ] **Step 1: Update the failing backend test**

Replace `TestMsbBackendExportIsOCI` with:

```go
func TestMsbBackendExportPushesToRegistry(t *testing.T) {
	spec := (&msbBackend{}).Export("host.microsandbox.internal:5000/agents-sandbox/runner-p:opencode-latest")
	if spec.Type != "image" {
		t.Fatalf("type = %q, want image", spec.Type)
	}
	if spec.Stream {
		t.Fatal("registry push must not stream a local output")
	}
	for key, want := range map[string]string{
		"name":              "host.microsandbox.internal:5000/agents-sandbox/runner-p:opencode-latest",
		"push":              "true",
		"oci-mediatypes":    "true",
		"registry.insecure": "true",
	} {
		if got := spec.Attrs[key]; got != want {
			t.Fatalf("attr %s = %q, want %q", key, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/sandbox/builder/ -run TestMsbBackendExport -v`
Expected: FAIL — current export is an OCI archive spec.

- [ ] **Step 3: Implement the registry export spec**

In `backend.go`:

```go
type exportSpec struct {
	Type   string
	Attrs  map[string]string
	// Stream is true when the exporter writes an OCI archive to the solve's
	// output writer (moby/archive); false when BuildKit pushes to a registry.
	Stream bool
}

// Export selects the containerimage exporter that pushes the solved image to the
// embedded registry. The name authority is the guest-visible registry the
// builder VM reaches; the build's auth session (shared with the cache export)
// supplies the bearer token and lets BuildKit HEAD-skip already-pushed blobs.
func (b *msbBackend) Export(pushRef string) exportSpec {
	attrs := map[string]string{
		"name":              pushRef,
		"push":              buildkitAttrTrue,
		"oci-mediatypes":    buildkitAttrTrue,
		"registry.insecure": buildkitAttrTrue,
	}
	applyCompressionAttrs(attrs)
	return exportSpec{Type: "image", Attrs: attrs, Stream: false}
}
```

Update the `backend` interface method comment/param name accordingly.

- [ ] **Step 4: Make `Client.Build` archive-or-push**

In `client.go`:
- Compute the push ref after acquiring the registry: `pushRef := cacheEndpoint.PushRef(req.Tag)`.
- `spec := c.backend.Export(pushRef)`.
- Create the temp archive only when `spec.Stream`; pass `archive` as `nil` otherwise.
- In `solve()`, gate the `opt.Exports` `Output` closure on `spec.Stream` (not on `imageOut != nil`), and keep the `Exports` entry with no `Output` for the push case.
- In `solveAndExport()`, close the archive only when non-nil.

- [ ] **Step 5: Update the fake backend and add the failing Build test**

In `build_test.go`, change `fakeBackend.Export` to `func (b *fakeBackend) Export(string) exportSpec { return exportSpec{Type: "oci", Stream: true} }`. Add:

```go
func TestBuildPushesImageToRegistryWithoutArchive(t *testing.T) {
	// Arrange a client whose fakeBackend returns a Stream:false push spec and a
	// fake buildkit client capturing SolveOpt.Exports.
	// Act: Build(...)
	// Assert: result.ArchivePath == ""; the captured export entry's Type ==
	// "image", has no Output func, and Attrs["push"] == "true" and
	// Attrs["name"] is the guest-authority push ref.
}
```

Reuse the existing fake-buildkit-client/`solve` capture helpers already in `build_test.go`.

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/sandbox/builder/ -run 'TestMsbBackendExport|TestBuildPushesImage|TestBuildSolveOpts|TestBuildExportsCache' -v`
Expected: PASS, including the existing cache-export tests (the image export and cache export share one solve).

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/builder/
git commit -m "feat(builder): push the runner image to the embedded registry from the solve"
```

---

### Task 3: Registry-pull seam (`ImagePullRef`, `HoldRegistry`)

**Files:**
- Create: `internal/sandbox/builder/registrypull.go`
- Test: `internal/sandbox/builder/registrypull_test.go`

**Interfaces:**
- Consumes: `ensureCacheRegistry` seam, `resolveBackendName`, `Endpoint.PullRef`, `Endpoint.KeepAlive`.
- Produces: `func ImagePullRef(ctx context.Context, imageTag string) (ref string, ok bool, err error)`; `func HoldRegistry(ctx context.Context) (stop func(), err error)`.

- [ ] **Step 1: Write the failing test**

```go
func TestImagePullRefReturnsRegistryRefForMsb(t *testing.T) {
	t.Setenv("AGENTS_SANDBOX_BUILDER", "msb")
	restore := ensureCacheRegistry
	defer func() { ensureCacheRegistry = restore }()
	ensureCacheRegistry = func(context.Context) (cacheregistry.Endpoint, error) {
		return cacheregistry.Endpoint{Host: "host.microsandbox.internal", Port: 5000, Token: "x"}, nil
	}
	ref, ok, err := ImagePullRef(context.Background(), "agents-sandbox/runner-p:opencode-latest")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(ref, "host.microsandbox.internal:5000/") {
		t.Fatalf("ref = %q", ref)
	}
}

func TestImagePullRefIsNoopForMoby(t *testing.T) {
	t.Setenv("AGENTS_SANDBOX_BUILDER", "moby")
	ref, ok, err := ImagePullRef(context.Background(), "agents-sandbox/runner-p:opencode-latest")
	if err != nil || ok || ref != "" {
		t.Fatalf("ref=%q ok=%v err=%v", ref, ok, err)
	}
}

func TestHoldRegistryIsNoopForMoby(t *testing.T) {
	t.Setenv("AGENTS_SANDBOX_BUILDER", "moby")
	stop, err := HoldRegistry(context.Background())
	if err != nil || stop == nil {
		t.Fatalf("stop=%v err=%v", stop, err)
	}
	stop()
}
```

Note: confirm the builder backend env override name used by `resolveBackendName`/`viperconfig.BuilderBackend()` before writing; use that exact env/var.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/sandbox/builder/ -run 'TestImagePullRef|TestHoldRegistry' -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement the seam**

`registrypull.go`:

```go
package builder

// ImagePullRef returns the reference microsandbox should pull imageTag from when
// the active backend delivers through the embedded registry, and ok=false when
// the backend delivers through an archive. It ensures the registry daemon is
// running, so the returned authority is live.
func ImagePullRef(ctx context.Context, imageTag string) (string, bool, error) {
	if resolveBackendName() != backendMsb {
		return "", false, nil
	}
	endpoint, err := ensureCacheRegistry(ctx)
	if err != nil {
		return "", false, err
	}
	return endpoint.PullRef(imageTag), true, nil
}

// HoldRegistry keeps the embedded registry's idle timer open for the duration of
// a pull. msb pulls the image inside CreateSandbox, which the registry only sees
// as HTTP blob reads; without a keep-alive the idle reaper could close the
// daemon mid-transfer on a multi-GB image. It returns a stop function; when the
// backend has no registry it returns a no-op.
func HoldRegistry(ctx context.Context) (func(), error) {
	if resolveBackendName() != backendMsb {
		return func() {}, nil
	}
	endpoint, err := ensureCacheRegistry(ctx)
	if err != nil {
		return nil, err
	}
	return endpoint.KeepAlive(ctx), nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/builder/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/builder/registrypull.go internal/sandbox/builder/registrypull_test.go
git commit -m "feat(builder): expose registry pull-ref and keep-alive seam"
```

---

### Task 4: `image` delivery model (ref resolution, availability, sandbox options)

**Files:**
- Modify: `internal/sandbox/image/build.go`
- Modify: `internal/sandbox/image/inspect.go`
- Create: `internal/sandbox/image/delivery.go`
- Test: `internal/sandbox/image/delivery_test.go`, `internal/sandbox/image/build_pipeline_test.go`

**Interfaces:**
- Consumes: `builder.ImagePullRef`, `builder.HoldRegistry` (Task 3); `msb.Client.ImageInspect/ImageGet/ImageLoad`.
- Produces:
  - `ImageInfo` gains `Ref string`, `RegistryInsecure bool`, `Refresh bool`.
  - `func (info ImageInfo) ImageRef() string`.
  - `func SandboxImageOptions(info ImageInfo) []msbSdk.SandboxOption`.
  - `func EnsureAvailable(ctx context.Context, mclient msb.Client, info ImageInfo, ui termio.UI) (stop func(), err error)` (replaces `EnsureLoaded`).

- [ ] **Step 1: Write the failing delivery tests**

```go
func TestEnsureAvailableLoadsArchive(t *testing.T) {
	m := &msb.MockMsbClient{}
	stop, err := EnsureAvailable(context.Background(), m, ImageInfo{Tag: "t", ArchivePath: "/tmp/a.tar"}, &termio.Mock{})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if len(m.LoadedImages) != 1 {
		t.Fatalf("loaded = %v", m.LoadedImages)
	}
}

func TestEnsureAvailableHoldsRegistryForPull(t *testing.T) {
	restore := holdRegistry
	defer func() { holdRegistry = restore }()
	held, stopped := false, false
	holdRegistry = func(context.Context) (func(), error) {
		held = true
		return func() { stopped = true }, nil
	}
	m := &msb.MockMsbClient{}
	stop, err := EnsureAvailable(context.Background(), m, ImageInfo{Tag: "bare", Ref: "127.0.0.1:5000/bare"}, &termio.Mock{})
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if !held || !stopped || len(m.LoadedImages) != 0 {
		t.Fatalf("held=%v stopped=%v loads=%v", held, stopped, m.LoadedImages)
	}
}

func TestImageRefPrefersRegistryRef(t *testing.T) {
	if got := (ImageInfo{Tag: "bare", Ref: "127.0.0.1:5000/bare"}).ImageRef(); got != "127.0.0.1:5000/bare" {
		t.Fatalf("got %q", got)
	}
	if got := (ImageInfo{Tag: "bare"}).ImageRef(); got != "bare" {
		t.Fatalf("got %q", got)
	}
}

func TestSandboxImageOptionsRefresh(t *testing.T) {
	opts := SandboxImageOptions(ImageInfo{Tag: "bare", Ref: "127.0.0.1:5000/bare", RegistryInsecure: true, Refresh: true})
	cfg := &msbSdk.SandboxConfig{}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.Image != "127.0.0.1:5000/bare" || !cfg.RegistryInsecure || cfg.PullPolicy != msbSdk.PullPolicyAlways {
		t.Fatalf("cfg=%+v", cfg)
	}
}
```

Expose `builder.HoldRegistry` through a package-level seam in `image` (e.g. `var holdRegistry = builder.HoldRegistry`) so the test can stub it without a live registry.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/sandbox/image/ -run 'TestEnsureAvailable|TestImageRef|TestSandboxImageOptions' -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Add the delivery fields and helpers**

In `build.go`, add to `ImageInfo`:

```go
	// Ref is the reference callers pass to msb's WithImage. It is the
	// registry-qualified pull ref for registry-pull backends and empty when the
	// image is delivered as an archive (moby), where Tag is used instead.
	Ref string
	// RegistryInsecure marks Ref as pulled over the embedded registry's plain
	// HTTP endpoint.
	RegistryInsecure bool
	// Refresh reports that a build ran this invocation, so msb must re-resolve
	// the ref (its default IfMissing short-circuits a same-tag re-push).
	Refresh bool
```

`delivery.go`:

```go
package image

// ImageRef is the reference microsandbox uses to run this image.
func (info ImageInfo) ImageRef() string {
	if info.Ref != "" {
		return info.Ref
	}
	return info.Tag
}

// SandboxImageOptions returns the msb options that reference the runner image,
// marking the embedded registry's plain-HTTP pull and forcing a re-resolve after
// a fresh build so a rebuilt same-tag image is not served stale.
func SandboxImageOptions(info ImageInfo) []msbSdk.SandboxOption {
	options := []msbSdk.SandboxOption{msbSdk.WithImage(info.ImageRef())}
	if info.RegistryInsecure {
		options = append(options, msbSdk.WithRegistryInsecure())
	}
	if info.Refresh {
		options = append(options, msbSdk.WithPullPolicy(msbSdk.PullPolicyAlways))
	}
	return options
}

// EnsureAvailable makes the runner image available to microsandbox for a sandbox
// that is about to be created. An archive is loaded (moby); a registry image
// needs no load, but its registry must stay alive across CreateSandbox, so the
// returned stop function must be called after the sandbox is created.
func EnsureAvailable(ctx context.Context, mclient msb.Client, info ImageInfo, ui termio.UI) (func(), error) {
	if info.ArchivePath != "" {
		spin := ui.Spinner("Loading image into microsandbox")
		if err := mclient.ImageLoad(ctx, info.Tag, info.ArchivePath); err != nil {
			spin.StopError(err)
			return func() {}, err
		}
		spin.Stop()
		return func() {}, nil
	}
	if info.Ref != "" {
		return holdRegistry(ctx)
	}
	if err := mclient.ImageGet(ctx, info.Tag); err == nil {
		return func() {}, nil
	}
	return func() {}, fmt.Errorf(
		"runner image %s is not in the microsandbox cache and no build archive is available",
		info.ImageRef(),
	)
}

var holdRegistry = builder.HoldRegistry
```

Remove `EnsureLoaded` (its callers move in Task 5). Keep `Build` but have it call `EnsureAvailable(ctx, msb.Get(), info, ui)` and stop the returned keep-alive immediately after (no sandbox is created here).

- [ ] **Step 4: Resolve the ref and use it for the probe/reuse**

In `EnsureImageWithClient`, before `buildDecision`:

```go
ref, insecure, err := resolveImageRef(ctx, rTag)
if err != nil {
	return ImageInfo{}, fmt.Errorf("resolve image ref: %w", err)
}
probeRef := rTag
if ref != "" {
	probeRef = ref
}
```

Change `buildDecision(ctx, rTag, ...)` to `buildDecision(ctx, probeRef, ...)` and `msb.Get().ImageInspect(ctx, rTag)` (reuse path) to `ImageInspect(ctx, probeRef)`.

Add helper:

```go
// resolveImageRef returns the reference microsandbox will use for rTag and
// whether it is pulled from the insecure embedded registry. It returns an empty
// ref for archive backends.
func resolveImageRef(ctx context.Context, rTag string) (string, bool, error) {
	pullRef, ok, err := builder.ImagePullRef(ctx, rTag)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	return pullRef, true, nil
}
```

Set the build-path info:

```go
info = ImageInfo{
	Tag: rTag, Ref: ref, RegistryInsecure: insecure, Refresh: true,
	Digest: res.Digest, Env: parseImageEnv(res.Env), ArchivePath: res.ArchivePath,
}
```

and the reuse path: `ImageInfo{Tag: rTag, Ref: ref, RegistryInsecure: insecure, Digest: inspect.Digest, Env: parseImageEnv(inspect.Env)}`.

- [ ] **Step 5: Add the refresh-invalidation test**

```go
// Review Focus 2: a rebuilt same-tag image must carry Refresh so the VM create
// re-resolves the ref; a cache hit must not.
func TestEnsureImageSetsRefreshOnlyOnBuild(t *testing.T) {
	// build path: assert ImageInfo.Refresh == true and Ref == the registry ref.
	// reuse path (fake msb.ImageInspect succeeds): assert Refresh == false.
}
```

Drive via the existing `builder.MockBuilder` + `msb.MockMsbClient` seams used by `build_pipeline_test.go`.

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/sandbox/image/ -v`
Expected: PASS. Fix any test that referenced `EnsureLoaded` or `ArchivePath` semantics.

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/image/
git commit -m "feat(image): registry-pull delivery model and availability helper"
```

---

### Task 5: Thread `ImageInfo` through VM and volume creation

**Files:**
- Modify: `internal/sandbox/vm/vm_lifecycle.go`, `internal/sandbox/vm/run_orchestrate.go`, `internal/sandbox/vm/reconfig.go`
- Modify: `internal/sandbox/volume/volume.go`, `internal/sandbox/volume/operations.go`
- Modify: `cmd/agents-sandbox/commands_system.go`
- Test: the `*_test.go` files in `internal/sandbox/vm/`, `internal/sandbox/volume/`, and `cmd/agents-sandbox/cli_*_test.go` that touch volume/image signatures

**Interfaces:**
- Consumes: `ImageInfo.ImageRef`, `image.SandboxImageOptions`, `image.EnsureAvailable` (Task 4).
- Produces: volume/reconfig functions take `image.ImageInfo` in place of the `(imageTag, archivePath string)` pair; `volumeOpFunc` changes accordingly.

- [ ] **Step 1: Replace `(imageTag, archivePath)` with `image.ImageInfo`**

Mechanical sweep; let the compiler guide. Exact new signatures:

- `volume.PrefillVolume(ctx, client, k, volumeName string, info image.ImageInfo, ui)` 
- `volume.CopyVolume(ctx, client, k, oldVolume, newVolume string, info image.ImageInfo, ui)`
- `volume.ApplyHomeAction(ctx, client, k, oldVolume string, info image.ImageInfo, currentDigest string, action VolumeAction, opts, ui)`
- `volume.Manager.ResolveHomeVolume(ctx, client, k, info image.ImageInfo, dryRunVM bool, ui)` (find its current definition in `volume/state.go` and adapt)
- `volume.CmdMigrate/CmdReset/CmdEdit(ctx, k, volumeName string, info image.ImageInfo, currentDigest string, rmOld, dryRun bool, ui)` and `volumeOp(...)`.
- `vm.decideReconfig(ctx, client, vm, k, opts, info image.ImageInfo, homeVol, hs, cfs, ui)`

Inside each, replace `imageTag` with `info.ImageRef()`, replace `image.EnsureLoaded(ctx, client, archivePath, imageTag, ui)` with `stop, err := image.EnsureAvailable(ctx, client, info, ui)` + `defer stop()`, and replace `msbSdk.WithImage(imageTag)` with `image.SandboxImageOptions(info)...`.

`decideReconfig` passes `info.ImageRef()` to `reconfig.PlanReconfig`'s `imageRef` and `info` to `ApplyHomeAction`.

- [ ] **Step 2: Update `run_orchestrate.go`**

`resolveHomeVolume` passes `info` instead of `(info.Digest, info.Tag, info.ArchivePath)` (keep `dryRunVM`). `removeBuildArchive(imageInfo.ArchivePath)` stays (the archive path is still produced only by moby; msb yields ""). The `decideReconfig` call passes `imageInfo` and `imageInfo.Digest`.

- [ ] **Step 3: Update the volume command plumbing**

`cmd/agents-sandbox/commands_system.go`: change `volumeOpFunc` to accept `image.ImageInfo` in place of `imageTag, archivePath`; the `buildVolumeOpsCmd` closure passes `info` and `info.Digest`. Keep `defer os.Remove(info.ArchivePath)` (moby-only guard).

- [ ] **Step 4: Update tests**

Run `go build ./...` and `go test ./... -run xxx` to enumerate breakages, then update the affected tests to the new signatures. Preserve each test's assertions; a test that asserted `WithImage(tag)` now asserts `WithImage(info.ImageRef())`.

- [ ] **Step 5: Verify**

Run: `go test ./internal/sandbox/vm/ ./internal/sandbox/volume/ ./cmd/agents-sandbox/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/vm/ internal/sandbox/volume/ cmd/agents-sandbox/
git commit -m "refactor(vm,volume): thread image delivery through creation call sites"
```

---

### Task 6: Registry-qualified refs in naming, list, and prune

**Files:**
- Modify: `internal/sandbox/naming/naming.go`, `internal/sandbox/naming/artifact.go`
- Modify: `internal/sandbox/image/list.go`
- Modify: `internal/sandbox/pruning/images.go`
- Test: `internal/sandbox/naming/*_test.go`, `internal/sandbox/pruning/*_test.go`, `internal/sandbox/image/list_test.go`, `internal/sandbox/builder/vm_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `naming.IsRunnerImageRef(ref string) bool`; `ParseImageTag`/`ArtifactFor` tolerate an optional leading registry authority.

- [ ] **Step 1: Write the failing naming tests**

```go
func TestIsRunnerImageRefToleratesRegistryAuthority(t *testing.T) {
	for _, ref := range []string{
		"agents-sandbox/runner-p:opencode-latest",
		"127.0.0.1:5000/agents-sandbox/runner-p:opencode-latest",
	} {
		if !IsRunnerImageRef(ref) {
			t.Fatalf("IsRunnerImageRef(%q) = false", ref)
		}
	}
	if IsRunnerImageRef("agents-sandbox/agents-sandbox-runner-p-amd64:cache") {
		t.Fatal("cache repo must not match a runner image")
	}
}

func TestArtifactForRegistryQualifiedImage(t *testing.T) {
	got := ArtifactFor("127.0.0.1:5000/agents-sandbox/runner-p:opencode-latest")
	if got.Slug != "p" || got.Agent != "opencode" {
		t.Fatalf("got %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/sandbox/naming/ -run 'TestIsRunnerImageRef|TestArtifactForRegistry' -v`
Expected: FAIL — undefined / empty parse.

- [ ] **Step 3: Implement tolerant naming**

In `naming.go`:

```go
// IsRunnerImageRef reports whether ref names a runner image, tolerating an
// optional registry authority prefix (the embedded registry's host:port).
func IsRunnerImageRef(ref string) bool {
	return strings.HasPrefix(ref, ImagePrefix) || strings.Contains(ref, "/"+ImagePrefix)
}

// runnerImageSuffix returns the "<ImagePrefix>...:tag" portion of ref, dropping
// any registry authority the embedded registry prepends.
func runnerImageSuffix(ref string) string {
	if i := strings.Index(ref, ImagePrefix); i >= 0 {
		return ref[i:]
	}
	return ref
}
```

In `artifact.go`, make `ParseImageTag` and `ArtifactFor`'s image case operate on `runnerImageSuffix(name)`.

- [ ] **Step 4: Apply to list and prune**

`image/list.go`: `strings.HasPrefix(ref, naming.ImagePrefix)` → `naming.IsRunnerImageRef(ref)`.
`pruning/images.go`: replace `hasPrefix(ref, naming.ImagePrefix)` with `naming.IsRunnerImageRef(ref)`; `naming.ArtifactFor(ref)` now parses registry-qualified refs.

- [ ] **Step 5: Add the registry-prune test (Review Focus 5)**

In the registry prune tests (`internal/sandbox/builder/vm_test.go` / `cacheregistry` tests), assert that the prune predicate `isAgentSandboxCacheRepository` matches both a cache repo and a runner-image repo, and that `Prune` drops both along with their blobs (a partial preserve is impossible because distribution's blob store is global). Also extend `internal/sandbox/pruning` tests so image-list handling uses `IsRunnerImageRef`.

- [ ] **Step 6: Verify**

Run: `go test ./internal/sandbox/naming/ ./internal/sandbox/pruning/ ./internal/sandbox/image/ ./internal/sandbox/builder/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/naming/ internal/sandbox/image/list.go internal/sandbox/pruning/
git commit -m "feat(naming): match registry-qualified runner image refs in list and prune"
```

---

### Task 7: Docs, spec, and changelog

**Files:**
- Modify: `CHANGELOG.md` (`[Unreleased]`)
- Modify: `.operator-shared/specs/builder-backends.md` (delivery clauses)
- Modify: `docs/` pages that describe image loading/archives (find via `rg -l "ImageLoad|load.*archive|oci archive" docs/`)

- [ ] **Step 1: Update the builder-backends spec delivery clauses**

State the shipped contract: msb pushes the runner image to the embedded registry during the solve and VMs pull it by reference (`WithImage` + insecure + refresh-after-build); the OCI archive/`ImageLoad` route remains only for the moby backend; the registry requires anonymous reads and token'd writes; prune clears cache and runner-image repos together.

- [ ] **Step 2: Update the perf guide**

In `.operator/guides/builder-build-performance-followup.md`, record that the export vertex is now a registry push (no archive tar), so the measured `[internal] exporting to oci image format` cost no longer materializes an archive.

- [ ] **Step 3: Update user docs and `CHANGELOG.md`**

Add a line under `[Unreleased]` describing registry-pull delivery for the msb backend. Update any docs page that says the image is loaded from an archive (msb path).

- [ ] **Step 4: Run the full check**

Run: `make check`
Expected: fmt clean, lint 0 issues, all tests pass, docs linkcheck passes.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md .operator-shared/specs/builder-backends.md .operator/guides/builder-build-performance-followup.md docs/
git commit -m "docs: registry-pull delivery for the msb backend"
```

---

## Deferred (explicitly out of scope)

- moby registry push (phase 2); the archive route stays.
- Content-addressed image tags; the stable `-latest` tag is kept and refreshed via `WithPullPolicy(Always)` after a build.
- Reading identity/`Env` from the registry instead of msb's cache on reuse (D10).
- Persisting the registry port across reap+restart (D5). Distribution storage is authority-independent, so a restarted port still serves the image and msb still dedupes layers by digest; the existing `endpointRecord` already reuses the port while healthy.
- Live msb/KVM integration coverage (registry push on build, pull on VM create, one-layer rebuild serving only the changed layer). The dev VM has no `/dev/kvm`, so the msb integration tier must be run on the host by the user; the plan covers these behaviours at the unit-seam level instead.

## Manual validation (host)

After the tasks pass `make check`, the user should run on a KVM host: a cold `run` (build + push + pull), a one-layer-change rebuild (only the changed layer served, no archive tar in the export vertex), and `prune` (registry drops both cache and runner-image repos).
