# Builder Backends Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship two runner-image builder backends behind one selection seam — the default `msb` backend (buildkitd in a reused microsandbox VM, cache in an embedded per-user OCI registry) and an opt-in `moby` backend (Docker Engine API) — and remove the build-performance spike leftovers.

**Architecture:** `builder.Get()` becomes the single selection seam: it resolves `msb` (default) or `moby` from launcher config/env and returns either the existing BuildKit `Client` or a new `mobyBuilder`, both implementing `Builder`. The BuildKit path keeps its internal `backend` seam (now msb-only), gains builder-VM reuse via a persisted port record, and swaps its worker cache for a `tmpfs` worker + `type=registry` cache against a new embedded `distribution/v3` registry daemon. The moby path is restored from pre-removal history and returns a `docker save` archive that the existing `EnsureLoaded` path loads — both backends deliver through the same `msb.Client.ImageLoad` route.

**Tech Stack:** Go (module `github.com/inoio/agents-sandbox`), `github.com/moby/buildkit` (host-side client), `github.com/moby/moby/client` (Engine API, already a dep), `github.com/distribution/distribution/v3` (embedded registry), Viper (config), `internal/sandbox/msb` (microsandbox SDK wrapper), `internal/termio`.

**Spec:** `.operator-shared/specs/builder-backends.md`

## Global Constraints

- Idiomatic Go; self-documenting non-abbreviated identifiers; no comments unless they carry durable decision value.
- Format/lint with `make fmt` / `make lint` (`golangci-lint`); **never** `go vet`. Run `make check` when finalizing.
- **No CLI flag** and **no interactive prompt** for backend selection. Selection surfaces are the launcher config `builder` key and `AGENTS_SANDBOX_BUILDER`; precedence env > config > default `msb`; empty/unknown resolves to `msb`.
- Production selection accepts only `msb` and `moby`. The test-only `docker` value is removed with the harness.
- **Platforms**: Linux (KVM) and macOS (Apple Silicon); `platform=linux/amd64` on x86_64, `linux/arm64` on Apple Silicon. The embedded registry and moby path must be pure-Go and cross-compilable.
- **Delivery boundary unchanged**: images enter the VM cache only through `msb.Client.ImageLoad`.
- **Secrets**: the registry token lives only in a `0600` state file. It is never written to config and never world-readable.
- **Build isolation (msb)**: Dockerfile `RUN` steps execute only inside the microVM; no host daemon, socket, or privileged container in the default path.

## Resolved Decisions

The spec left implementation mechanics open; this plan fixes them:

1. **Selection surface.** New `viperconfig.BuilderBackend()` reads the config `builder` key (generic user + project config; builder is host-level, not per-slug) with `AGENTS_SANDBOX_BUILDER` overriding it. `builder.Get()` maps `moby` → `mobyBuilder`, everything else → the BuildKit `Client`.
2. **Registry embed.** `distribution/v3` `registry/handlers.NewApp` produces the `http.Handler`, wrapped in a constant-time Bearer-token middleware.
3. **Registry lifecycle.** Per-user singleton: hidden command `__builder-registry` re-exec'd detached by `Ensure`; endpoint discovered via a `0600` state file + flock start lock; idle-reaps after a quiet period.
4. **Registry bind address.** The builder VM's `IPv4Pool` is pinned to a dedicated `/30`; the registry binds that subnet's gateway address (network+1), **not** `0.0.0.0`. This is the security-sensitive detail and is validated in Task 2 Step 1.
5. **Builder-VM reuse.** The chosen host port is recorded in a `0600` state file at creation and read back on reuse when the SDK returns no port (`SandboxHandle.Config()` drops port bindings).
6. **moby delivery.** Both backends deliver through the image package's `EnsureLoaded`: `mobyBuilder.Build` performs `ImageBuild` → `ImageSave` and returns the `docker save` archive as `BuildResult.ArchivePath`; the image package loads it via `EnsureLoaded` exactly as it loads the buildkit OCI archive. `mobyBuilder` does **not** hold an `msb.Client`, so `builder` keeps no microsandbox dependency and delivery stays a single boundary (`msb.Client.ImageLoad`).
7. **Internal seam.** The buildkit-specific `backend` interface is retained with the msb VM as its sole implementation. `dockerBackend`, `dockerCLI`, `ResetDockerHarness`, and the `docker` value are removed.

## Review Focus

Inputs/conditions the spec implies but no happy-path test exercises; each gets a test in its owning task:

1. Two processes race to start the registry: the flock serializes them and both attach to one daemon with one storage (Task 2).
2. A registry request with a missing or wrong token is rejected with 401 (Task 2).
3. The endpoint file names a dead pid, or health fails: `Ensure` restarts the daemon (Task 2).
4. `moby` is selected but no Docker daemon is reachable: a clear error naming Docker AND how to switch back to msb building, not a panic or hang (Task 4).
5. The builder sandbox exists but has no usable port and/or no matching record: `Ensure` recreates it rather than hanging (Task 1).

---

### Task 1: Builder-VM reuse via a persisted port record

**Files:**
- Create: `internal/sandbox/builder/vmstate.go`
- Modify: `internal/sandbox/builder/vm.go` (`resolveBuilderPort`)
- Test: `internal/sandbox/builder/vmstate_test.go`, add cases to `internal/sandbox/builder/vm_test.go`

**Interfaces:**
- Consumes: `internal/configpaths` (`UserStateDir`), `internal/sandbox/msb`.
- Produces:
  - `type builderRecord struct { Name string `json:"name"`; HostPort int `json:"hostPort"` }`
  - `func builderRecordPath() string` → `filepath.Join(configpaths.Get().UserStateDir(), "builder-vm.json")`
  - `func writeBuilderRecord(name string, hostPort int) error` — write to a temp file in the same dir, `chmod 0600`, `os.Rename`.
  - `func readBuilderRecord() (builderRecord, bool)` — false on any read/parse error.
  - `func clearBuilderRecord() error`
  - `resolveBuilderPort` behavior: on a found sandbox, use `publishedHostPort(handle)`; when that is 0, fall back to `readBuilderRecord()` if `record.Name == name`; when still 0, treat as absent (recreate). After `CreateSandbox`, `writeBuilderRecord(name, hostPort)` before returning.
  - `Ensure` behavior change: after `resolveBuilderPort` returns a port, run the readiness probe; on failure `clearBuilderRecord()` and recreate once (re-enter the flock, `CreateSandbox` with `WithReplace()`, rewrite the record, re-probe). A second failure returns the probe error. This makes "exists but buildkitd is dead" recover by recreation instead of surfacing an error.

- [ ] **Step 1: Write the failing tests** in `vmstate_test.go` and `vm_test.go`:
  - `TestBuilderRecordRoundTrip`: `writeBuilderRecord("n", 4242)` then `readBuilderRecord()` returns `{n, 4242}`, true; file mode is `0600`.
  - `TestBuilderRecordAbsentOrCorrupt`: no file → false; garbage file → false.
  - `TestEnsureReusesRecordedPortWhenConfigEmpty` (Review Focus 5): `GetSandboxFn` returns a `MockSandboxHandle` whose `Config()` has no port bindings; a record `{name, 4242}` exists; assert `Ensure` returns `4242` and makes no `CreateSandbox` call.
  - `TestEnsureRecreatesWhenNoPortAndNoRecord` (Review Focus 5): found sandbox, `Config()` empty, no record; assert a `CreateSandbox` call and a new record written.
  - `TestEnsureRecreatesOnFailedReadiness` (Review Focus 5): a found sandbox resolves a port but the `Ready` seam fails once then succeeds; assert exactly one recreation and a rewritten record, and that `Ensure` returns the new port rather than an error.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/builder/ -run 'TestBuilderRecord|TestEnsureReuses|TestEnsureRecreates' -count=1`
Expected: FAIL — `writeBuilderRecord`/`readBuilderRecord` undefined.

- [ ] **Step 3: Implement `vmstate.go` and the `resolveBuilderPort` fallback**

Use `os.MkdirAll(dir, 0o750)` before writing. JSON encode with `encoding/json`; mode via `os.WriteFile(tmp, data, 0o600)` then `os.Rename`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/builder/ -run 'TestBuilderRecord|TestEnsureReuses|TestEnsureRecreates' -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + commit**

Run `make lint`. Then:
```bash
git add internal/sandbox/builder/vmstate.go internal/sandbox/builder/vmstate_test.go internal/sandbox/builder/vm.go internal/sandbox/builder/vm_test.go
git commit -m "feat(builder): reuse the builder VM via a persisted port record"
```

---

### Task 2: Embedded cache registry (per-user singleton daemon)

**Files:**
- Create: `internal/sandbox/builder/cacheregistry/registry.go` (config + `Serve` + handler assembly)
- Create: `internal/sandbox/builder/cacheregistry/endpoint.go` (`Ensure`, state file, flock, re-exec, liveness)
- Create: `internal/sandbox/builder/cacheregistry/auth.go` (token middleware)
- Create: `internal/sandbox/builder/cacheregistry/cache.go` (deterministic cache ref + prune)
- Create: `cmd/agents-sandbox/commands_registry.go` (hidden `__builder-registry` command)
- Modify: `cmd/agents-sandbox/commands.go` (register the hidden command)
- Modify: `internal/sandbox/builder/builder.go` (`builderNetworkPolicy`: pin the builder VM `IPv4Pool`)
- Test: `internal/sandbox/builder/cacheregistry/*_test.go`, `cmd/agents-sandbox/cli_registry_test.go`

**Interfaces:**
- Consumes: `configpaths`, `options.FirstFreeHostPort`, `state.FlockExclusive`, `distribution/v3` (`configuration`, `registry/handlers`), `net/http`.
- Produces:
  - `type Endpoint struct { Host string; Port int; Token string }`
  - `func Ensure(ctx context.Context) (Endpoint, error)` — returns a ready endpoint, starting the singleton daemon if needed.
  - `func Serve(ctx context.Context, configPath string) error` — runs the registry until ctx is done (called by the hidden command).
  - `func (e Endpoint) CacheRef(imageTag, platform string) string` — `<host>:<port>/agents-sandbox/<sanitized(imageTag)>-<arch>:cache`.
  - `func Prune(ctx context.Context, e Endpoint, match func(ref string) bool) error` — deletes matching manifests/blobs via the registry HTTP API.
  - `func registryBindHost() string` and `func registryBindPort() int`.
  - `func tokenAuth(token string, next http.Handler) http.Handler`.
  - Constants: `endpointFileName = "builder-registry.json"`, `configFileName = "builder-registry-config.json"`, `lockFileName = "builder-registry.lock"`, `cacheDirName = "builder-registry-cache"`, `registryIdleTimeout = 5 * time.Minute`.
  - Internal config struct persisted for the child: `{Root, Host string; Port int; Token string; IdleTimeout time.Duration}`.

- [ ] **Step 1: Validate the bind + reachability mechanism (spike deliverable)**

On a KVM host with msb, pin the builder VM's `IPv4Pool` to a dedicated `/30` (e.g. `172.30.0.0/30`) via `NetworkConfig.IPv4Pool`, run a throwaway listener on the gateway address (network+1), and confirm the builder VM reaches it at `host.microsandbox.internal:<port>` (and that no other local address is required). If binding the synthetic gateway address fails, record the address that does work (still not `0.0.0.0`) and use it in `registryBindHost`. Record the outcome in the spec's "Reachability/Bind scope" clause. **This step gates the rest of the task.**

- [ ] **Step 2: Write the failing tests:**
  - `TestServeRejectsMissingOrWrongToken` (Review Focus 2): build the handler with a known token; `GET /v2/` with no `Authorization` and with a wrong Bearer both return 401; with the correct Bearer returns 200.
  - `TestServeRoundTripStoresBlob`: push a manifest+blob through the authed handler into a temp root, restart the app on the same root, read it back — proving filesystem persistence.
  - `TestEnsureStartsThenReusesDaemon` (Review Focus 1): call `Ensure` twice concurrently against a temp state dir; exactly one child is spawned (spawn seam counted); both endpoints are equal; the second call does not spawn.
  - `TestEnsureRestartsOnDeadEndpoint` (Review Focus 3): write an endpoint file with a dead pid / failing health; `Ensure` spawns a new daemon and rewrites the file.
  - `TestEndpointFileMode`: the written endpoint file is `0600`.
  - `TestServeIdleReaps`: with a shortened idle timeout, the handler stops accepting after a quiet period with no requests and after activity then quiet.
  - `TestCacheRefDeterministicAndDistinct`: same `(tag, platform)` → same ref; different project/agent/platform → different refs.
  - `TestPruneDeletesMatchingRepos`: seed two `agents-sandbox/*` repos and one unrelated repo; `Prune` with an `agents-sandbox/` predicate removes the two and leaves the unrelated one.

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/sandbox/builder/cacheregistry/ -count=1`
Expected: FAIL — package/identifiers undefined.

- [ ] **Step 4: Implement the package**

`registry.go`: build a `configuration.Configuration` with `Storage.Type() == "filesystem"` and `RootDirectory` = `<state>/builder-registry-cache`; `handlers.NewApp(ctx, config)`; wrap `app` in `tokenAuth`; serve via `http.Server` on `net.Listen("tcp", net.JoinHostPort(registryBindHost(), itoa(registryBindPort())))`. Track last-request time in the middleware; a ticker shuts the server down after `registryIdleTimeout` of quiet.

`endpoint.go` `Ensure`: flock `<state>/builder-registry.lock`; read endpoint file; if present and `GET /v2/` with the token succeeds within a short budget, return it; otherwise allocate a free port, mint a random token (`crypto/rand`, hex), write the child config file (`0600`) and the endpoint file (`0600`), and spawn `os.Executable()` with args `__builder-registry --config <configPath>` detached (`SysProcAttr{Setsid: true}`), stdout/stderr to `<state>/builder-registry.log`. Expose `var spawnRegistry = realSpawnRegistry` as the test seam.

`auth.go`: constant-time compare (`crypto/subtle`) of the `Authorization: Bearer <token>` value; 401 with `WWW-Authenticate` otherwise.

`cache.go`: `CacheRef` sanitizes the tag into a single path component and appends the platform arch; `Prune` lists `_catalog`, filters repos by `match`, and deletes each tag and its blobs through the registry API.

`commands_registry.go`: hidden cobra command `cmdBuilderRegistry`, `Hidden: true`, `DisableFlagParsing`, invoked as `agents-sandbox __builder-registry --config <path>`, calling `cacheregistry.Serve(ctx, configPath)`. Register it in `commands.go`.

`builder.go`: `builderNetworkPolicy` sets `policy.IPv4Pool` to the pinned subnet from Task 2 Step 1.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/sandbox/builder/cacheregistry/ ./cmd/agents-sandbox/ -run 'TestServe|TestEnsure|TestEndpoint|TestCacheRef|TestPruneDeletes|Registry' -count=1`
Expected: PASS.

- [ ] **Step 6: Lint + commit**

Run `make lint`. Then:
```bash
git add internal/sandbox/builder/cacheregistry cmd/agents-sandbox/commands_registry.go cmd/agents-sandbox/commands.go internal/sandbox/builder/builder.go go.mod go.sum
git commit -m "feat(builder): embedded per-user OCI cache registry"
```

---

### Task 3: Wire the registry cache into the BuildKit path; collapse cache modes and spike knobs

**Files:**
- Modify: `internal/sandbox/builder/client.go` (`builderCacheExportOptions`, `registryAuthSession` usage)
- Modify: `internal/sandbox/builder/auth.go` (static registry credentials for the embedded host)
- Modify: `internal/sandbox/builder/builder.go` (remove knobs; pin constants; tmpfs worker + registry cache)
- Modify: `internal/sandbox/builder/backend.go` (msb-only)
- Delete: `internal/sandbox/builder/docker.go`, `docker_test.go`, `tuning_test.go`, `backend_test.go` (replaced), `bench`-knob helpers in `builder.go`
- Modify: `internal/sandbox/builder/client_test.go`, `build_test.go`, `vm_test.go` as affected
- Test: `internal/sandbox/builder/client_test.go` (cache options), `auth_test.go`

**Interfaces:**
- Consumes: Task 2 `cacheregistry.Ensure`, `Endpoint.CacheRef`.
- Produces:
  - `func builderCacheExportOptions(ctx context.Context, cacheRef string) (imports, exports []client.CacheOptionsEntry, err error)` — registry-only.
  - `func builderCacheRef(imageTag, platform string) string` — delegates to `cacheregistry`.
  - `func registryAuthSession(extra map[string]authprovider.AuthConfig) []session.Attachable` — merges `~/.docker/config.json` with the embedded-registry token.
  - `buildkitdCommand()` always `--oci-worker-snapshotter native`; `builderCacheMount()` always `Mount.Tmpfs{SizeMiB: builderCacheTmpfsSizeMiB}`; `builderIdleTimeout` is the constant `5 * time.Second`.
  - `Client.Build` computes `ref := builderCacheRef(req.Tag, req.Platform)`, obtains the endpoint via `cacheregistry.Ensure`, builds the auth session with `endpoint.Host → endpoint.Token`, and passes the ref into `builderCacheExportOptions`.

- [ ] **Step 1: Write/adjust failing tests:**
  - `TestBuilderCacheExportOptionsUseRegistry`: mode is fixed; imports/exports are `type=registry` with `ref == CacheRef(tag, platform)` and export `mode=max`; no `type=local` entries remain.
  - `TestRegistryAuthSessionIncludesCacheToken`: the returned session includes a static credential for the embedded registry host.
  - `TestBuildkitdCommandPinsNativeSnapshotter`: command contains `native` and the guest port; no env override can change it.
  - `TestBuilderCacheMountIsTmpfs`: the mount is tmpfs with `builderCacheTmpfsSizeMiB`.
  - Delete `tuning_test.go` cases that assert removed knobs; delete `backend_test.go` cases for the `docker` value.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/builder/ -run 'TestBuilderCache|TestRegistryAuth|TestBuildkitdCommand|TestBuilderCacheMount' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

Replace `builderCacheExportOptions()` with the ctx+ref registry version; delete the `cacheModeVolume/Bind/TmpfsExport/Registry` switch, the `builderTLSConfig`/`builderMemoryMiB`/`builderSnapshotter`/`builderCacheRegistryRef`/`builderCacheRegistryInsecure`/`builderCacheBindPath`/`builderCacheExportPath`/`builderTracef` helpers and their env constants. Keep `builderKeepAliveInterval`, `builderHostPortBase`, `builderGuestPort`, `builderImage`, `cacheMountPath`, `builderLogPath`, `builderSandboxName`. Add the static-credential merge in `auth.go`. In `backend.go`, keep `backend` + `msbBackend`; delete `dockerBackend`/`newBackend`'s docker branch. Delete `docker.go` and its test.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/builder/ -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + commit**

Run `make lint`. Then:
```bash
git add -A internal/sandbox/builder
git commit -m "feat(builder): registry-backed cache; remove spike knobs and docker harness"
```

---

### Task 4: The moby backend as a second `Builder`

**Files:**
- Create: `internal/sandbox/builder/moby.go`, `internal/sandbox/builder/mobycontext.go`
- Modify: `internal/sandbox/builder/testmock.go` (if the `Builder` surface grows)
- Test: `internal/sandbox/builder/moby_test.go`

**Interfaces:**
- Consumes: `github.com/moby/moby/client`, `builder.BuildRequest`/`BuildResult`, a shared `buildContextTar`, `os.CreateTemp`.
- Produces:
  - `type mobyEngine interface { ImageBuild(ctx, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error); ImageInspect(ctx, string, ...client.ImageInspectOption) (client.ImageInspectResult, error); ImageSave(ctx, []string, ...client.ImageSaveOption) (client.ImageSaveResult, error); ImagePull(ctx, string, client.ImagePullOptions) (io.ReadCloser, error); ImagePrune(ctx, client.ImagePruneOptions) (client.ImagePruneResult, error); Ping(ctx, client.PingOptions) (client.PingResult, error) }`
  - `var newMobyEngine = realMobyEngine` (`func() (mobyEngine, error)`).
  - `type mobyBuilder struct { engine mobyEngine }`; `var _ Builder = (*mobyBuilder)(nil)`. **No `msb.Client`** — delivery is the image package's `EnsureLoaded`.
  - `func newMobyBuilder() Builder`.
  - `func buildContextTar(dir string) (io.Reader, error)` — tar of `Dockerfile` + `agents-sandbox-path.sh` (reuse `pathMergeScript` if shared; otherwise the asset bytes are passed in the request).
  - `func clientOptions() []client.Opt` — restored from `internal/sandbox/docker/dockercontext.go@5ed59d4^` (DOCKER_HOST, then active docker context).

- [ ] **Step 1: Write the failing tests** (stub `mobyEngine`; no msb mock):
  - `TestMobyBuildInvokesImageBuild`: asserts `ImageBuildOptions.Tags == [req.Tag]`, `Remove == true`, `NoCache == req.NoCache`, and `BuildArgs` contains `BASE_IMAGE == baseRef+"@"+inspectID` plus every `req.BuildArgs` entry.
  - `TestMobyBuildReturnsSaveArchive`: the stubbed `ImageSave` yields known bytes; `BuildResult.ArchivePath` is a readable file whose contents equal those bytes (the archive the shared `EnsureLoaded` will load).
  - `TestMobyBuildResolvesBaseByPullingWhenAbsent`: `ImageInspect` of the base first errors, `ImagePull` is called, then `ImageInspect` succeeds.
  - `TestMobyEngineDownNamesDocker` (Review Focus 4): `newMobyEngine` returns an error containing "docker"; `Build` wraps it as a clear error and does not panic.
  - `TestMobyPruneMapsToImagePrune`: `PruneCache` calls `ImagePrune`.
  - `TestMobyBuildResultPopulatesDigestEnv`: digest from `ImageInspect`, env from the image config.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/builder/ -run 'TestMoby' -count=1`
Expected: FAIL — `mobyBuilder` undefined.

- [ ] **Step 3: Implement** `moby.go` + `mobycontext.go`

Build flow: `buildContextTar(req.ContextDir)`; resolve base digest via `engine` (inspect → pull → inspect); `ImageBuild`; drain the JSON stream to `req.Progress`; `ImageInspect(req.Tag)` for digest/env; `ImageSave([req.Tag])` copied into a temp file (`os.CreateTemp`, removed by the image package's existing `defer os.Remove(info.ArchivePath)` in `image.Build`). Return `BuildResult{ArchivePath: tempPath, Digest, Env, BaseDigest}` — the image package's `EnsureLoaded` loads it. `PruneCache` → `engine.ImagePrune(ctx, client.ImagePruneOptions{Filters: ...})`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/builder/ -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + commit**

Run `make lint`. Then:
```bash
git add internal/sandbox/builder/moby.go internal/sandbox/builder/mobycontext.go internal/sandbox/builder/moby_test.go internal/sandbox/builder/testmock.go go.mod go.sum
git commit -m "feat(builder): restore the moby Engine-API backend as a second Builder"
```

---

### Task 5: Backend selection at `builder.Get()`

**Files:**
- Modify: `internal/viperconfig/viperconfig.go` (`Config.Builder`, `configEnvKeys`, `Resolver.Builder()`, `BuilderBackend()`)
- Modify: `internal/viperconfig/viperconfig_test.go`
- Modify: `internal/sandbox/builder/backend.go` (`Get` selection)
- Create: `internal/sandbox/builder/selection.go`
- Test: `internal/sandbox/builder/selection_test.go`, `cmd/agents-sandbox/cli_config_test.go`/`cli_test.go` as needed

**Interfaces:**
- Consumes: Task 4 `newMobyBuilder`, existing `newClient`.
- Produces:
  - viperconfig: `Config.Builder string `mapstructure:"builder"``; `"builder"` in `configEnvKeys`; `func (r *Resolver) Builder() string`; `func BuilderBackend() string` (env > merged config dirs > `""`). `BuilderBackend` reuses the same config-dir merge and env binding as `NewResolver` but **skips validation**, so an unrelated invalid config value cannot fail backend selection. `builder` importing `viperconfig` introduces no cycle (viperconfig does not import `builder`).
  - builder: `const backendMsb = "msb"`, `const backendMoby = "moby"`; `func resolveBackendName() string` → `moby` if `viperconfig.BuilderBackend() == backendMoby`, else `msb`; `var Get = func() Builder { if resolveBackendName() == backendMoby { return newMobyBuilder() }; return newClient() }`.

- [ ] **Step 1: Write the failing tests:**
  - viperconfig `TestBuilderBackendFromEnvAndConfig`: config `builder: moby` → `moby`; env `AGENTS_SANDBOX_BUILDER=msb` over config → `msb`; neither → `""`.
  - builder `TestSelectionDefaultsToMsb`: no env/config → `Get()` is `*Client`.
  - builder `TestSelectionMobyFromEnv`: `AGENTS_SANDBOX_BUILDER=moby` → `*mobyBuilder`.
  - builder `TestSelectionUnknownFallsBackToMsb`: `AGENTS_SANDBOX_BUILDER=podman` → `*Client`.
  - cmd `TestNoBuilderFlag`: the command tree exposes no backend-selection flag.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/viperconfig/ ./internal/sandbox/builder/ -run 'TestBuilderBackend|TestSelection|TestNoBuilderFlag' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement** the viperconfig field/binding/helpers and the builder selection switch; delete the now-unused `builderBackendEnv` docker branch.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/viperconfig/ ./internal/sandbox/builder/ ./cmd/agents-sandbox/ -run 'TestBuilderBackend|TestSelection|TestNoBuilderFlag' -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + commit**

Run `make lint`. Then:
```bash
git add internal/viperconfig internal/sandbox/builder/backend.go internal/sandbox/builder/selection.go internal/sandbox/builder/selection_test.go cmd/agents-sandbox
git commit -m "feat(builder): select msb/moby backend from config and env"
```

---

### Task 6: `doctor` reports the selected backend's prerequisites

**Files:**
- Modify: `internal/sandbox/doctor/doctor.go` (gate a restored Docker check on moby)
- Modify: `internal/sandbox/doctor/testmock.go`, `internal/sandbox/doctor/doctor_test.go`
- Modify: `internal/testutil/mocks/testutil.go` if a fail-fast hook is needed
- Test: `internal/sandbox/doctor/doctor_test.go`, `cmd/agents-sandbox/cli_doctor_test.go`

**Interfaces:**
- Consumes: `builder` selection (`resolveBackendName` exported as `builder.SelectedName()`), `client.New`/`Ping`.
- Produces:
  - `func builder.SelectedName() string` (exported wrapper over `resolveBackendName`).
  - doctor: `var selectedBackend = builder.SelectedName`; `checkDocker(ctx) error` (restored from `internal/sandbox/doctor/doctor.go@5ed59d4^`); `collectChecks` appends `checkDocker` **only** when `selectedBackend() == "moby"`.

- [ ] **Step 1: Write the failing tests:**
  - `TestCollectChecksSkipsDockerForMsb`: backend seam returns `msb`; the docker check is not in the collected checks and no docker error is reported.
  - `TestCollectChecksRunsDockerForMoby`: backend seam returns `moby`; a failing docker check is reported.
  - cmd `TestDoctorMobyReportsDocker`: `AGENTS_SANDBOX_BUILDER=moby` with an unreachable daemon reports a Docker prerequisite.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/doctor/ ./cmd/agents-sandbox/ -run 'TestCollectChecks|TestDoctorMoby' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement** the gated check and the seam; keep the msb/platform checks unchanged.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/doctor/ ./cmd/agents-sandbox/ -run 'TestCollectChecks|TestDoctorMoby' -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + commit**

Run `make lint`. Then:
```bash
git add internal/sandbox/doctor internal/sandbox/builder/selection.go cmd/agents-sandbox
git commit -m "feat(doctor): report prerequisites for the selected builder backend"
```

---

### Task 7: Backend-aware `PruneCache`

**Files:**
- Modify: `internal/sandbox/builder/client.go` (`Client.PruneCache`), `moby.go` (`mobyBuilder.PruneCache`)
- Modify: `internal/sandbox/builder/vm.go` (`VM.PruneCache` → also prune the embedded registry)
- Modify: `internal/sandbox/pruning/images.go` (`pruneBuilderCache`, report text)
- Test: `internal/sandbox/builder/vm_test.go`, `internal/sandbox/pruning/images_test.go`

**Interfaces:**
- Consumes: Task 2 `cacheregistry.Ensure`/`Prune`.
- Produces:
  - `VM.PruneCache`: when the builder VM exists, run `buildctl prune` as today; then `ep, err := cacheregistry.Ensure(ctx)` and `cacheregistry.Prune(ctx, ep, func(ref string) bool { return strings.HasPrefix(ref, "agents-sandbox/") })`. Errors are joined.
  - `mobyBuilder.PruneCache`: `engine.ImagePrune` with the dangling filter.
  - `pruning.ImageReport.BuildkitCachePruned` keeps its meaning (a builder-cache prune was attempted and succeeded).

- [ ] **Step 1: Write failing tests:**
  - `TestMsbPruneCacheAlsoPrunesRegistry`: with a fake builder VM present and a fake registry endpoint, both `buildctl prune` and the registry prune run.
  - `TestMobyPruneCachePrunesDaemon`: `ImagePrune` is called.
  - `TestPruneImagesCallsBuilderPrune`: existing seam test still asserts a single builder prune call.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/builder/ ./internal/sandbox/pruning/ -run 'TestMsbPrune|TestMobyPrune|TestPruneImages' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement** the backend-aware prune and update the report wording.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/builder/ ./internal/sandbox/pruning/ -count=1`
Expected: PASS.

- [ ] **Step 5: Lint + commit**

Run `make lint`. Then:
```bash
git add internal/sandbox/builder internal/sandbox/pruning
git commit -m "feat(pruning): backend-aware builder cache prune"
```

---

### Task 8: Remove the experiment leftovers

**Files:**
- Delete: `spike/` (all)
- Delete: `internal/sandbox/image/bench_integration_test.go`, `internal/sandbox/image/bench_moby_integration_test.go`
- Modify: `internal/sandbox/image/integration_harness_test.go` (drop `ResetDockerHarness`; moby needs no per-suite reset)
- Modify: any tests referencing removed knobs/harness/`BENCH_*`
- Verify: `internal/sandbox/builder/builder.go` has no `AGENTS_SANDBOX_BUILDER_*` knob (Task 3)

**Interfaces:** none (mechanical).

- [ ] **Step 1: Verify no production reference to removed items**

Run: `rg -n 'BENCH_|AGENTS_SANDBOX_BUILDER_|ResetDockerHarness|dockerBuilderContainer|spike/' internal cmd --glob '*.go'`
Expected: empty.

- [ ] **Step 2: Delete and adjust**

Delete `spike/` and the two bench files; rewrite `integration_harness_test.go` so the Docker tier no longer resets a harness (moby talks to the user's daemon); remove any leftover knob/harness imports.

- [ ] **Step 3: Build + full unit suite + tagged compile**

Run:
```bash
CGO_ENABLED=1 go build ./... && go test ./... -count=1
CGO_ENABLED=1 go test -tags integration -run='^$' ./...
```
Expected: builds; unit tests PASS; tagged test files compile.

- [ ] **Step 4: Lint + commit**

Run `make lint`. Then:
```bash
git add -A spike internal
git commit -m "chore: remove build-performance spike and experiment leftovers"
```

---

### Task 9: Integration tier retarget (moby non-KVM, msb KVM)

**Files:**
- Modify: `Makefile` (`test-integration` → `AGENTS_SANDBOX_BUILDER=moby`)
- Modify: `.github/workflows/ci.yml` if it pins the backend
- Modify: `internal/sandbox/image/integration_build_test.go`, `integration_harness_test.go`
- Test: integration-tagged (manual on the appropriate host)

**Interfaces:**
- Consumes: real builder backends + `msb` runtime + (moby tier) a Docker daemon.
- Produces: nothing other tasks use.

- [ ] **Step 1: Retarget `make test-integration` to moby** and drop the `docker` value everywhere.

- [ ] **Step 2: Write the msb-tier assertions:**
  - `TestMsbSecondBuildHitsRegistryCache`: build twice (removing the builder sandbox between runs); the second build reports cached Dockerfile steps (reuse the progress-based cache detector).
  - `TestMsbParallelBuildsShareOneBuilderVM`: start two concurrent builds; assert a single builder sandbox/port is used.

- [ ] **Step 3: Write the moby-tier assertions:**
  - `TestMobyBuildLoadsIntoMicrosandbox`: a real `moby` build succeeds and `msb.Get().ImageInspect(tag)` succeeds.

- [ ] **Step 4: Run (host-only)**

Run (moby tier, any host with Docker): `make test-integration`
Run (msb tier, KVM host): `make test-integration-msb`
Expected: PASS on the appropriate host. In the agents-sandbox dev VM only confirm the tagged files compile:
`CGO_ENABLED=1 go test -tags integration -run='^$' ./...`

- [ ] **Step 5: Commit**

```bash
git add Makefile .github internal/sandbox/image
git commit -m "test(integration): moby non-KVM tier; msb registry-cache and shared-VM coverage"
```

---

### Task 10: Docs, CHANGELOG, spec, and brain

**Files:**
- Modify: `docs/runner-image.md`, `docs/configuration/*`, `docs/how-it-works.md`, `docs/troubleshooting.md` (msb default; moby "alternative" section with prerequisites)
- Modify: `CHANGELOG.md` (`[Unreleased]`: new default embedded-registry msb; opt-in moby; removed spike/harness)
- Modify: `.operator-shared/specs/builder-backends.md` (record Task 2 Step 1's bind decision; mark implemented)
- Modify: `.operator-shared/specs/buildkit-builder-vm.md` (note the docker test harness is superseded)
- Modify: `.operator/guides/builder-build-performance.md` (reframe as a historical decision record, not active guidance)
- Modify: `.operator-shared/catalog.md`, `.operator/catalog.md` (catalog this plan; update guide descriptions)
- Modify: `.operator-shared/index/sandbox.md` (builder subtree: cacheregistry, moby; integration tiers)

**Interfaces:** none.

- [ ] **Step 1: Update docs + CHANGELOG**; keep Jekyll link style per `AGENTS.md`.
- [ ] **Step 2: Update specs and the performance guide** (present tense; decision record, not guidance).
- [ ] **Step 3: Update the index and catalogs.**
- [ ] **Step 4: Full check**

Run: `make check`
Expected: fmt / lint (0 issues) / unit tests / docs linkcheck all pass.

- [ ] **Step 5: Commit**

```bash
git add docs CHANGELOG.md .operator-shared .operator
git commit -m "docs: document builder backends; retire spike guidance"
```

---

## Execution Handoff

Tasks 1→6 build up the interfaces the later tasks consume (1 and 2 are independent; 3–7 depend on 1/2; 8–10 close out). Run with `superpowers:subagent-driven-development` (a fresh reviewer per task fits, especially for the security-sensitive registry tasks 2–3).

Two items the controller must confirm before Task 2 proceeds:
- **Registry bind mechanism** (Task 2 Step 1): pinned `/30` + gateway bind must be validated on a KVM host; this gates the whole registry effort.
- **Registry prune scope** (Task 2/7): pruning every `agents-sandbox/*` cache repo is the chosen baseline; if per-identity pruning is required, extend `Prune`'s predicate.

`make test-integration-msb` and the new msb integration tests cannot run in the agents-sandbox dev VM (no `/dev/kvm`); the user must run them on a KVM host.
