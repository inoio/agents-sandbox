# Docker Test Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the runner-image build/composition integration tests run without KVM, by adding a test-only Docker-backed BuildKit `buildkitd` beside the production msb-VM one, and moving the behavior tests to `docker run`.

**Architecture:** Split `internal/sandbox/builder`'s endpoint acquisition from its solve/export logic behind a `backend` interface: `msbBackend` (current VM) and `dockerBackend` (runs `moby/buildkit` via the `docker` CLI). `Client.Build` keeps its solve path and asks the backend for the endpoint and the exporter. Backend choice is an env var read at call time. The integration tests select the backend, and the two "run the image" tests are ported to a small run abstraction (msb sandbox vs `docker run`). KVM-only tests are gated.

**Tech Stack:** Go 1.27, `github.com/moby/buildkit/client`, `os/exec` (Docker CLI), existing msb SDK.

**Spec:** `.operator-shared/specs/docker-test-harness.md`

## Global Constraints

- Follow `AGENTS.md`: idiomatic Go, self-documenting non-abbreviated identifiers, run `make fmt` and `make lint` (never `go vet`).
- Test-only feature: production defaults to the msb backend; no runtime Docker fallback.
- `moby/buildkit:v0.25.2` (the existing `builderImage`), guest port `1234`, host port base `11234`, cache mount `/var/lib/buildkit`.
- Env selector: `AGENTS_SANDBOX_BUILDER` = `msb` (default) | `docker`.
- Every task ends with `make test` (or the named subset) passing and a commit.
- Verification-only risk to confirm during Task 4: the `moby/buildkit` container needs `--privileged`, and `--oci-worker-snapshotter native` must work under the dev VM's `vfs` daemon.

## Review Focus

- **Docker daemon absent/unreachable**: `make test-integration` must fail with a clear message, not hang or silently skip.
- **Container fails to start** (privileged/snapshotter wrong): the error must name the failing `docker` command.
- **Leftover harness container/volume** from a killed run: the next run must reap it before starting.
- **Unknown `AGENTS_SANDBOX_BUILDER` value**: must not silently pick docker; fall back to msb (production default).
- **Digest/Env absent from a `type=docker` export**: docker-tier tests must not assert `BuildResult.Digest`/`Env`.

---

### Task 1: Backend seam + msb backend (no behavior change)

**Files:**
- Create: `internal/sandbox/builder/backend.go`
- Modify: `internal/sandbox/builder/client.go:165-185,223-234,276-279`
- Test: `internal/sandbox/builder/backend_test.go`, `internal/sandbox/builder/client_test.go`

**Interfaces:**
- Consumes: `VM.Ensure(ctx) (int, error)`, `VM.PruneCache(ctx) error` (existing, `vm.go`).
- Produces:
  - `type exportSpec struct { Type string; Attrs map[string]string }`
  - `type backend interface { Ensure(ctx context.Context) (int, error); PruneCache(ctx context.Context) error; Export(tag string) exportSpec }`
  - `type msbBackend struct { vm *VM }` implementing it (`Export` → `exportSpec{Type: "oci"}`)
  - `newBackend() backend` (msb only for now)
  - `type Client struct { backend backend }` (drops `Msb`, `vm`)

- [ ] **Step 1: Write the failing test** in `backend_test.go`

```go
func TestMsbBackendExportIsOCI(t *testing.T) {
	spec := (&msbBackend{}).Export("any")
	if spec.Type != "oci" || len(spec.Attrs) != 0 {
		t.Fatalf("msb export = %+v, want type oci with no attrs", spec)
	}
}
```

- [ ] **Step 2: Run it and verify it fails**

Run: `go test ./internal/sandbox/builder/ -run TestMsbBackendExportIsOCI`
Expected: FAIL — `undefined: msbBackend`.

- [ ] **Step 3: Add `backend.go`** with the interface, `exportSpec`, and `msbBackend` delegating to `vm.Ensure`/`vm.PruneCache`, plus `newBackend()` returning `&msbBackend{vm: newVM()}` where `newVM()` builds the current `&VM{Msb: msb.Get()}` with `Ready = vm.defaultReady`.

- [ ] **Step 4: Rewire `client.go`**

`newClient` returns `&Client{backend: newBackend()}`. In `Build`, replace `c.vm.Ensure` with `c.backend.Ensure`; replace the hardcoded `Exports[0].Type: "oci"` with `spec := c.backend.Export(req.Tag)` → `Type: spec.Type, Attrs: spec.Attrs`. In `PruneCache`, call `c.backend.PruneCache`.

- [ ] **Step 5: Update `TestNewClientWiresReadySeam`** in `client_test.go` to assert `c.backend` is a `*msbBackend` whose `vm.Ready != nil`.

- [ ] **Step 6: Run tests**

Run: `make test`
Expected: PASS (existing builder tests, including `vm_test.go`, unchanged).

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/builder/backend.go internal/sandbox/builder/backend_test.go \
        internal/sandbox/builder/client.go internal/sandbox/builder/client_test.go
git commit -m "refactor(builder): extract backend seam for endpoint acquisition"
```

---

### Task 2: `dockerCLI` seam

**Files:**
- Create: `internal/sandbox/builder/docker.go`
- Test: `internal/sandbox/builder/docker_test.go`

**Interfaces:**
- Produces:
  - `type dockerCLI interface { run(ctx context.Context, args ...string) (string, error) }`
  - `type realDockerCLI struct{}` — `exec.CommandContext(ctx, "docker", args...)`, combined stdout/stderr, error wraps the command line and captured output.

- [ ] **Step 1: Write the failing test** (also defines the reusable `fakeDockerCLI`)

```go
type fakeDockerCLI struct {
	calls   [][]string
	outputs map[string]string // keyed on args[0] (+ ":" + args[1] for two-word subcommands)
	err     error
}

func (f *fakeDockerCLI) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if f.err != nil {
		return "", f.err
	}
	return f.outputs[strings.Join(args[:min(2, len(args))], ":")], nil
}

func TestRealDockerCLIReportsCommandOnFailure(t *testing.T) {
	_, err := (&realDockerCLI{}).run(context.Background(), "info", "--bogus")
	if err == nil {
		t.Fatal("want error for an invalid docker invocation")
	}
	if !strings.Contains(err.Error(), "docker info --bogus") {
		t.Errorf("error %q does not name the command", err)
	}
}
```

- [ ] **Step 2: Run it and verify it fails**

Run: `go test ./internal/sandbox/builder/ -run TestRealDockerCLI`
Expected: FAIL — `undefined: realDockerCLI`.

- [ ] **Step 3: Implement `docker.go`** with the interface and `realDockerCLI`; error message `fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(out))`.

- [ ] **Step 4: Run tests** — `make test` (PASS).

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/builder/docker.go internal/sandbox/builder/docker_test.go
git commit -m "feat(builder): add docker CLI seam"
```

---

### Task 3: `dockerBackend` (Ensure / PruneCache / Export)

**Files:**
- Modify: `internal/sandbox/builder/docker.go`
- Modify: `internal/sandbox/builder/client.go` (extract readiness)
- Test: `internal/sandbox/builder/docker_test.go`

**Interfaces:**
- Consumes: `dockerCLI.run`, `options.FirstFreeHostPort(base int) int`, `builderImage`, `builderGuestPort`, `cacheMountPath`, `builderHostPortBase`, `builderReadyTimeout`.
- Produces:
  - `const dockerBuilderContainer = "agents-sandbox-buildkit-harness"`
  - `const dockerBuilderVolume = "agents-sandbox-buildkit-harness-cache"`
  - `func waitForBuildkit(ctx context.Context, address string) error`
  - `type dockerBackend struct { docker dockerCLI; port int }` implementing `backend`:
    - `Export` → `exportSpec{Type: "docker", Attrs: map[string]string{"name": tag}}`
    - `Ensure`: reuse running container (read `docker port <name> 1234/tcp`), else `docker rm -f`, `docker volume create`, `docker run -d --privileged --name <name> -p 127.0.0.1:<hostPort>:1234 -v <vol>:/var/lib/buildkit moby/buildkit:v0.25.2 --oci-worker-snapshotter native --addr tcp://0.0.0.0:1234`, then `waitForBuildkit`.
    - `PruneCache`: no-op when not running, else `docker exec <name> buildctl --addr tcp://127.0.0.1:1234 prune --filter until=24h --all`.

- [ ] **Step 1: Extract readiness** — move `VM.defaultReady`'s retry loop into `waitForBuildkit(ctx, address)`, leaving `defaultReady` calling it with `fmt.Sprintf("tcp://127.0.0.1:%d", hostPort)`. Run `go test ./internal/sandbox/builder/ -run TestDefaultReady` — PASS (no test change).

- [ ] **Step 2: Write failing tests** (fake `dockerCLI`, fake `newBuildkitClient`)

```go
func TestDockerBackendEnsureStartsContainer(t *testing.T) {
	restore := newBuildkitClient
	t.Cleanup(func() { newBuildkitClient = restore })
	newBuildkitClient = func(context.Context, string) (buildkitClient, error) { return &closeCountingClient{...}, nil }
	fake := &fakeDockerCLI{} // "port" fails so it creates
	b := &dockerBackend{docker: fake}
	port, err := b.Ensure(context.Background())
	// assert err nil, port > 0, and that a "run" call exists whose args include
	// "--privileged", dockerBuilderContainer, builderImage,
	// "--oci-worker-snapshotter", "native".
}

func TestDockerBackendEnsureReusesRunningContainer(t *testing.T) {
	fake := &fakeDockerCLI{outputs: map[string]string{"port": "127.0.0.1:11234"}}
	// assert port == 11234 and no "run" call was made.
}

func TestDockerBackendPruneNoContainerIsNoop(t *testing.T) {
	// fake "port" fails; PruneCache returns nil and no "exec" call is made.
}

func TestDockerBackendExportIsDockerTagged(t *testing.T) {
	spec := (&dockerBackend{}).Export("my/image:latest")
	// assert Type == "docker" and Attrs["name"] == "my/image:latest".
}
```

`fakeDockerCLI` needs per-subcommand output; key on `args[0]` (and `args[1]` for `volume`/`port`).

- [ ] **Step 3: Run and verify failure** — `go test ./internal/sandbox/builder/ -run TestDockerBackend` FAIL.

- [ ] **Step 4: Implement `dockerBackend`** per the Interfaces block. Parse the host port from `docker port` output (`127.0.0.1:<port>`).

- [ ] **Step 5: Run tests** — `make test` PASS.

- [ ] **Step 6: Verify against a real daemon (manual, this environment has Docker)** — write a throwaway `_ = ` scratch test or `go run` that calls `docker run --privileged ... moby/buildkit:v0.25.2 --oci-worker-snapshotter native --addr tcp://0.0.0.0:1234`, then dials it. Confirm it starts under `vfs` and `--privileged` is required. Record the outcome in the spec if it differs.

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/builder/docker.go internal/sandbox/builder/docker_test.go internal/sandbox/builder/client.go
git commit -m "feat(builder): docker-backed buildkitd endpoint"
```

---

### Task 4: Backend selection

**Files:**
- Modify: `internal/sandbox/builder/backend.go`
- Test: `internal/sandbox/builder/backend_test.go`

**Interfaces:**
- Produces: `const builderBackendEnv = "AGENTS_SANDBOX_BUILDER"`, `const builderBackendMsb = "msb"`, `const builderBackendDocker = "docker"`; `newBackend()` reads `os.Getenv(builderBackendEnv)` **at call time** and returns `&dockerBackend{docker: realDockerCLI{}}` only for the exact value `docker`, else `&msbBackend{vm: newVM()}`.

- [ ] **Step 1: Write failing tests**

```go
func TestNewBackendDefaultsToMsb(t *testing.T) {
	t.Setenv(builderBackendEnv, "")
	if _, ok := newBackend().(*msbBackend); !ok { t.Fatal("want msbBackend") }
}

func TestNewBackendDockerEnv(t *testing.T) {
	t.Setenv(builderBackendEnv, builderBackendDocker)
	if _, ok := newBackend().(*dockerBackend); !ok { t.Fatal("want dockerBackend") }
}

func TestNewBackendUnknownFallsBackToMsb(t *testing.T) {
	t.Setenv(builderBackendEnv, "podman")
	if _, ok := newBackend().(*msbBackend); !ok { t.Fatal("unknown value must fall back to msb") }
}
```

- [ ] **Step 2: Run and verify failure.**
- [ ] **Step 3: Implement selection in `newBackend`.**
- [ ] **Step 4: `make test` PASS.**
- [ ] **Step 5: Commit** — `feat(builder): select docker backend via AGENTS_SANDBOX_BUILDER`.

---

### Task 5: Harness lifecycle + TestMain hook

**Files:**
- Modify: `internal/sandbox/builder/docker.go` (add `ResetDockerHarness`)
- Modify: `internal/testutil/mocks/testutil.go` (add `InstallFailFastMocks()`)
- Modify: `internal/sandbox/image/main_test.go`
- Create: `internal/sandbox/image/integration_harness_test.go` (`//go:build integration`)
- Test: `internal/sandbox/builder/docker_test.go`

**Interfaces:**
- Produces:
  - `func ResetDockerHarness(ctx context.Context) error` — `docker rm -f <name>` and `docker volume rm -f <vol>`, ignoring not-found, returning the last non-not-found error only if it is not "no such".
  - `func InstallFailFastMocks()` in `internal/testutil/mocks` (installs the three mocks, no `m.Run`); `InitFailFastMocks(m)` becomes `InstallFailFastMocks(); m.Run()`.
  - In `internal/sandbox/image`: `var integrationHarnessSetup = func() func() { return func() {} }`, overridden in the integration file's `init()`.
  - In `integration_harness_test.go`: `const backendDocker = "docker"` and `func activeBackend() string` (`os.Getenv("AGENTS_SANDBOX_BUILDER")`, empty → `"msb"`), owned here so Task 6 consumes them.
- Consumes (integration file): `builder.ResetDockerHarness`, `os.Getenv(integrationEnvVar)`.

- [ ] **Step 1: Write failing test** for `ResetDockerHarness` using a fake docker CLI injectable? `ResetDockerHarness` uses `realDockerCLI` directly; test it at the command-construction level by extracting `func removeDockerHarness(ctx context.Context, d dockerCLI) error` and testing that. Assert it calls `rm -f <name>` then `volume rm -f <vol>`.

- [ ] **Step 2: Run and verify failure.**
- [ ] **Step 3: Implement** `removeDockerHarness`, `ResetDockerHarness` (calling it with `realDockerCLI{}`), and `InstallFailFastMocks`.
- [ ] **Step 4: Update `internal/sandbox/image/main_test.go`**

```go
func TestMain(m *testing.M) {
	termio.InstallFailFastMocks()
	teardown := integrationHarnessSetup()
	code := m.Run()
	teardown()
	os.Exit(code)
}
```

- [ ] **Step 5: Add `integration_harness_test.go`**

```go
//go:build integration

package image

func init() { integrationHarnessSetup = setupIntegrationHarness }

func setupIntegrationHarness() func() {
	if os.Getenv(integrationEnvVar) == "" || activeBackend() != backendDocker {
		return func() {}
	}
	ctx := context.Background()
	_ = builder.ResetDockerHarness(ctx)
	return func() { _ = builder.ResetDockerHarness(ctx) }
}
```

(`activeBackend()`/`backendDocker` are defined in this same file, per Task 5's Interfaces.)

- [ ] **Step 6: `make test` and `make compile-all` PASS.**
- [ ] **Step 7: Commit** — `test: docker harness lifecycle and TestMain hook`.

---

### Task 6: Port behavior tests + gate msb-only tests

**Files:**
- Modify: `internal/sandbox/image/integration_build_test.go`
- Test: `internal/sandbox/image/integration_build_test.go` (the tests themselves)

**Interfaces:**
- Consumes (from Task 5): `backendDocker`, `activeBackend()`.
- Produces (test helpers in `integration_build_test.go`):
  - `func requireMsbBackend(t *testing.T)` → `t.Skip` when `activeBackend() == backendDocker`.
  - `type shellResult struct { success bool; exitCode int; stdout, stderr string }`
  - `func buildImageForRun(t *testing.T, a agent.Agent, project []byte, docker bool, tag string) string`
  - `func runInImage(t *testing.T, ref, command string) shellResult`
  - `func loadDockerArchive(t *testing.T, archivePath string)` (`docker load -i`)
  - `func dockerRun(t *testing.T, ref, command string) shellResult` (`docker run --rm <ref> sh -c <command>`)
- Consumes (package funcs): `builder.Get().Build`, `EnsureLoaded`, `RenderDockerfile`, `resolveAgentVersion`, `renderBytesVersion`, `writeDockerfileContext`, `baseImageRef`, `buildArgStrings`, `userBuildArgs`, `computeDockerfileID`, `computeImageIdentity`, `effectiveDocker`, `msb.Get`.

- [ ] **Step 1: Write the failing/changed tests** — add `requireMsbBackend(t)` to the top of `TestBuildRunnerImageInBuilderVM` and `TestBuilderCacheReuseAcrossVMs`. Rewrite `TestRunnerImageNodeAgentNodeRuns` and `TestRunnerImageLoginShellPath` to use `buildImageForRun` + `runInImage` (no direct `EnsureImageWithClient`/sandbox). For login-shell, pass the project bytes `[]byte("FROM agents-sandbox/runner-base:latest\nENV PATH=\"/opt/my-tools:${PATH}\"\n")` to `buildImageForRun` instead of writing a config file.

- [ ] **Step 2: Run under docker** (this environment has Docker)

Run: `AGENTS_SANDBOX_INTEGRATION=1 AGENTS_SANDBOX_BUILDER=docker CGO_ENABLED=1 go test -tags integration -count=1 -run 'TestRenderDockerfile|TestRunnerImage' ./internal/sandbox/image/`
Expected: PASS; `TestBuildRunnerImageInBuilderVM`/`TestBuilderCacheReuseAcrossVMs` SKIP (msb-gated).

- [ ] **Step 3: Implement the helpers** so the above passes. `buildImageForRun` renders via `renderBytesVersion`, builds via `builder.Get().Build`, then `loadDockerArchive` (docker) or `EnsureLoaded` (msb); `runInImage` uses `dockerRun` (docker) or an msb sandbox. Register archive/`ImageRemove`/`docker rmi` cleanup with `t.Cleanup`.

- [ ] **Step 4: Full docker suite** — `make test-integration` PASS (msb tests skip).

- [ ] **Step 5: Commit** — `test(image): run behavior tests via docker and gate msb tests`.

---

### Task 7: Makefile targets + CI jobs

**Files:**
- Modify: `Makefile:79-92`
- Modify: `.github/workflows/ci.yml:54-92`
- Test: `make -n test-integration`, `python3 -c "import yaml;yaml.safe_load(open('.github/workflows/ci.yml'))"`

**Interfaces:**
- Produces:
  - `test-integration:` → `AGENTS_SANDBOX_INTEGRATION=1 AGENTS_SANDBOX_BUILDER=docker CGO_ENABLED=1 go test -tags integration -count=1 ./...`
  - `test-integration-msb:` → same with `AGENTS_SANDBOX_BUILDER=msb`
  - CI job `integration` (docker; `runs-on: ubuntu-latest`; runs `make test-integration`; every PR and main/tags/manual)
  - CI job `integration-msb` (the existing KVM+msb job; gated to main/tags/manual; runs `make test-integration-msb`)
  - `release.needs` includes `integration-msb` (and `integration`).

- [ ] **Step 1: Update the Makefile** targets and their comments.
- [ ] **Step 2: Verify** `make -n test-integration test-integration-msb` shows the right env.
- [ ] **Step 3: Update CI** — rename the current KVM job to `integration-msb`, keep its KVM+"Install msb runtime" steps and use `make test-integration-msb`; add the `integration` docker job (checkout + setup-go + `make test-integration`); update `release.needs`.
- [ ] **Step 4: Validate YAML** and `make docs-linkcheck`.
- [ ] **Step 5: Commit** — `ci(integration): docker PR tier and gated msb tier`.

---

### Task 8: Docs, spec status, CHANGELOG

**Files:**
- Modify: `AGENTS.md` (Common development commands)
- Modify: `.operator-shared/specs/docker-test-harness.md` (status → implemented)
- Modify: `.operator-shared/specs/buildkit-builder-vm.md` (note the docker tier)
- Modify: `.operator-shared/catalog.md` (status)
- Modify: `CHANGELOG.md`
- Test: `make check`

**Interfaces:**
- Produces: updated docs only.

- [ ] **Step 1: Update `AGENTS.md`** — `make test-integration` (docker, no KVM) and `make test-integration-msb` (KVM host); note the dev VM can run the docker tier.
- [ ] **Step 2: Update the specs/catalog** to implemented and describe the two tiers.
- [ ] **Step 3: Add a CHANGELOG `[Unreleased]` entry** under Added/Internal.
- [ ] **Step 4: Run `make check`** — PASS.
- [ ] **Step 5: Commit** — `docs: docker test-harness tiers`.

---

## Self-Review

**Spec coverage:** backend seam (T1), Docker backend via `docker` CLI (T2/T3), ephemeral per-run cache volume + no leaks (T5 reset), env selection with default (T4), export format decision — docker `type=docker` tagged (T3) with digest/env not asserted in docker tier (T6), behavior tests via `docker run` (T6), tier split + CI/Make (T7), verification risks confirmed in T3 Step 6, docs (T8).

**Type consistency:** `backend`/`exportSpec`/`msbBackend`/`dockerBackend`/`dockerCLI`/`newBackend`/`waitForBuildkit`/`ResetDockerHarness`/`activeBackend`/`buildImageForRun`/`runInImage`/`shellResult` are defined once and referenced with the same names throughout.

**Review Focus coverage:** unknown backend value (T4 test); leftover container/volume (T5 reset + test); container start failure message (T2 test); daemon absence surfaces via `realDockerCLI` error (T2/T3); digest/env not asserted in docker tier (T6 constraint).

**Proportion:** the plan fixes interfaces and tests; bodies are left to the implementer except where a value is pinned (commands, names, env values).
