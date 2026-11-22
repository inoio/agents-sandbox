---
description: Design for harness journeys - a second `msb`-tagged suite (`test/msb/journeys`) that drives full user journeys through real harnesses (v1: learn mode on opencode2), the shared untagged `test/msb/driver` package, the `test/harness`→`test/msb/startup` rename, the "everything under test/ is test code" gate scope with the production-closure contract test, and the surface-sweep promotion flow.
read_if: Working on the msb journeys suite (`test/msb/journeys`, `make test-msb-journeys`), the shared `test/msb/driver` package, the `test/harness`→`test/msb/startup` rename, the dead-code/ambient "test/ is test code" gate scope, the production-closure contract test, or wiring the release surface sweep to the journeys suite.
---

# Harness journeys (msb) — design

## Intent

Learn mode (#697) is implemented but under-verified: every harness profile declares the read-back and
provision capabilities, yet only opencode2's read-back was ever exercised (spike S1), and the whole
learn flow has never been driven end to end by a harness. The existing `msb`-tagged suite deliberately
stops at "the harness reached its TUI" (`specs/testing/harness-smoke-tests.md`), so it cannot catch a
broken learn flow — only a harness that fails to start.

We want a **repeatable acceptance harness**: an automated blackbox tier that drives full journeys
through real harnesses in a real VM, plus an agent-run exploratory tier whose findings graduate into
the automated tier. When a harness's own layout changes and an automated journey fails, an agent
triages and adapts the test rather than treating the failure as a product regression.

## Goals

- A second `msb`-tagged suite that drives harnesses **beyond first start** — through a real user
  journey — without touching the minimal startup gate.
- v1 scope: **learn mode** (UC1, UC4, `config learn`).
- The exploratory tier (the release surface sweep) can invoke the automated tier as its spine, and
  the two share one isolation model.
- Findings management: exploratory findings become automated tests; automated failures are triaged by
  an agent against the harness's current layout.
- Keep the repo's isolation and gate guarantees intact while adding a shared test-support package.

## Non-goals

- Changing `make test-msb`'s contract (every registered harness reaches its TUI and exits cleanly).
- CI: `msb`-tagged suites need KVM + docker + network, so they stay host-only (`make compile-all`
  still compiles them on every PR).
- Real credentials or real egress. A journey uses a host-side stub provider; the assertion is about
  what the launcher observed, not about model output.
- A new generic acceptance framework; v1 is the learn-mode journeys + the machinery they need.

## Anti-goals

- A suite that only passes when a specific harness version is installed, or that pins harness versions.
- Duplicating the pty/isolation machinery (two drifting drivers).
- Letting the new tier's runtime leak into the default `make test` (it must not run a VM).
- Weakening the dead-code or ambient gates to add the driver.

## Constraints (repo)

- Go 1.27; TDD; `make fmt`, `make lint`, `make check`. A behavior change updates docs and adds a
  `changelog.d/<PR>.<type>.md` fragment.
- `test-isolation.md`: every test package's `TestMain` installs its acyclic fail-fast mocks; a
  non-test test-support file carries a file-scope `//deadcode:ignore`; a **tagged-only** package is
  silently dropped by `go test ./...` (verified), so a package that must run in the default suite
  needs an untagged file.
- The build tag names the **runtime** (`msb` = KVM + docker + network), not the scope.
- `harness-smoke-tests.md`: the harness-table completeness test must run in the default suite, so a
  newly registered harness fails `make test` until covered.

## Approaches and decisions

### Suite placement

- **A — ride the existing tiers:** extend `test/harness` toward a first turn and add a learn-mode
  journey to the surface sweep. Rejected: it violates "keep `test-msb` minimal".
- **B — a second `msb` suite + the surface sweep as the exploratory tier** (chosen). The automated
  half is a new suite; the exploratory half is the existing sweep.
- **C — drive from `test/scripts`:** wrong runtime (no VM) and no pty machinery.

**Decision:** a new suite, `test/msb/journeys`. The build tag stays `msb`; **scope lives in the
package and target name**, not the tag. This keeps `compile-all` and `lint` free (both glob `./...`
with `msb` already) and keeps the runtime tag honest.

### `test/` layout

```
test/
  scripts/           # unchanged — drives shell tools, no VM
  msb/
    driver/          # shared driving support (untagged)
    startup/         # was test/harness — the minimal startup smoke (msb-tagged)
    journeys/        # the new full-journey suite (msb-tagged)
```

`test/harness` → `test/msb/startup`. "startup" pairs cleanly with "journeys" (harness lifecycle
phase); "smoke" names a depth, not a phase. Targets: `make test-msb` (startup, unchanged intent) and
`make test-msb-journeys`. The `AGENTS_SANDBOX_SMOKE_ROOT` env var is **kept by name** (documented,
CI-facing); only the package path changes.

`driver` is **untagged** so its unit tests run in the default `make test` and never start a VM. The
**harness table** (`driver.Specs`, quit keys, seeds, readiness markers) moves into `driver` — both
suites need "how to start and quit harness X", and keeping the table untagged keeps its
completeness test running in the default suite (the tagged-only-package trap).

### Shared machinery: A1

- **A1 — extract a shared driver package** (chosen): pty session, `driver.IsolatedEnv`, root resolution +
  reuse lock, readiness matching, harness table, into `test/msb/driver`.
- **A2 — self-contained journeys suite:** rejected; two drivers that drift, and the pty
  `TCSANOW`/buffer quirks are exactly what must be fixed once.

The driver keeps the launcher-binary exec; the microsandbox SDK is never imported (the suite drives
the built binary, not the SDK).

### "Everything under `test/` is test code" (gate scope)

- **A (chosen):** teach the dead-code gate to ignore findings under `test/`, and extend the ambient
  and ambient-tool contract tests to scan **non-test `.go` files under `test/`** as well as `_test.go`.
  No `//deadcode:ignore` on `driver`; its host calls keep the `//testenv:allow` markers they carry
  today. The widening reaches the shared untagged driver's non-test files (they live under `test/`),
  which would otherwise escape the ambient scan because they are not `_test.go` files.
- **B — marker-driven:** add the file-scope marker to `driver` and key the ambient scans on it.
  Rejected as fiddlier for no extra reach.
- **C — keep host-touching calls in `_test.go`, extract only pure helpers.** Rejected: it defeats
  sharing the pty session, the most valuable shared piece.

### Provider for a journey

A learn-mode journey must make the harness **send** an outbound request so the terminating proxy sees
the credential→destination pair. **Correction (builder check):** a *guest-loopback* stub never
traverses the host-owned terminating proxy, so it cannot produce attribution — the earlier "loopback
stub" wording was wrong for the attribution half. The destination must be a **host-side stub** the VM
reaches through the proxy, with the same dummy-provider seed pattern `hermes`/`goose` use. That keeps
the turn egress-free (no public destination) while the proxy still terminates TLS and observes the
credential→destination pair.

**Decision:** proving the real agent turn through the proxy is a **prerequisite**, not a deferrable
unknown. The plan includes a spike that lands the host-side stub destination and a real turn the
proxy carries, before the attribution journeys are written. The read-back journeys (config changes,
static secrets, managed-config write-back) are independent and can land in parallel; the attribution
journeys depend on the spike.

#### Stub provider (Task 13; provisional pending the host accept)

The stub is a host-side loopback HTTP listener (`test/msb/journeys/stub_provider.go`), started per journey and
closed by `tb.Cleanup`.

- **Protocol: plain HTTP.** The agent's request traverses the terminating proxy as an absolute-URI HTTP
  request, which the proxy records (`protocol: "http"`), so the stub needs no CA and the run needs no
  `network.observe.skip-upstream-verify`. An `https` advertised route would need `skip-upstream-verify`,
  because the stub's certificate is not trusted.
- **Provider config.** `StubProvider.ProviderConfig()` returns the VM-home-relative config manifest
  (`driver.SeedLauncherConfig`'s target→content map): one target, opencode's `.config/opencode/opencode.json`
  (the file opencode actually reads; `config.json < opencode.json < opencode.jsonc`), holding an
  OpenAI-compatible provider `stub` whose `options.baseURL` is `<advertised>/v1` plus the top-level
  `model: stub/stub`. `StepSeedStub` provisions it through the launcher's `home:` manifest. That path
  matters: seeding a launcher snippet under `XDG_CONFIG_HOME/agents-sandbox/<agent dir>` makes
  `firstrun.managedConfigPresent` count a managed layer and suppresses the menu, and seeding the isolated
  HOME's `~/.config/opencode` makes `firstrun.HostFiles` read it as host config; the `home:` manifest
  avoids both, so the first-run menu (and its "Learn by using it" choice) is still offered.
- **Advertised route.** The listener always binds `127.0.0.1:0`; the *advertised* origin (scheme + host) is
  `AGENTS_SANDBOX_JOURNEYS_STUB_ORIGIN`, default `http://agents-sandbox-stub.invalid`. A host accept pins a
  route that both the runtime interception and the proxy's upstream dial reach, without a code change; the
  port is always the listener's own ephemeral one.
- **Assertion.** The spike asserts the saved observation names the advertised stub host. A denied proxy dial
  is still recorded, so the assertion passes whenever the turn reaches the proxy at all.

**Provisional.** The one open question the host accept settles is whether the default route reaches the
proxy; the managed-layer suppression is resolved by provisioning through the `home:` manifest (above).

### Isolation between journeys

All v1 journeys drive the same project directory (`driver.ProjectDir(root, "opencode2")`), so they share one
project slug and therefore one microsandbox VM and one home volume. The runner sweeps sandboxes only at
suite start and teardown, so a journey must not rely on the shared volume being clean: per-journey state it
asserts must live in the journey's own isolated HOME (a fresh temp dir per subtest, threaded by
`driver.IsolatedEnv`). A future journey that needs a clean shared VM must reset it itself.

### Harness scope for v1

**Decision:** v1 covers **opencode2 only** (the S1-verified profile, with its SQLite credential
probe). The journey spec is a **table field**, so adding a harness is one entry, and the remaining
harnesses are recorded as explicit exemptions — the #252 acceptance shape ("covered, or exempted with
the reason recorded") — so S1's partial state is visible rather than silently so. Coverage of the
other harnesses is tracked separately (the "extend to all agents" issue). One agent may be driven by
**several journeys** (v1 drives opencode2 with UC1 and UC4); the completeness contract reports an agent
only when it is neither covered nor exempted, never because it has more than one entry.

### Production-closure gate

Add `internal/testutil/production_closure_contract_test.go`: run `go list -deps ./cmd/agents-sandbox`
(no `-test`, so it is the production closure) and fail if `testing` or any test-support package
appears. A sibling check, `TestDriverClosureLinksNoTesting`, runs the same scan over `./test/msb/driver`
— the shared untagged driver the suites import — so a production package leaked into the driver fails
there, where the cmd closure cannot see it. Together they convert the "nothing test-only ships"
convention into an enforced gate. Reuses `internal/testutil`'s existing `TestMain`.

## Journey suite v1 (learn mode)

- **UC1** — fresh install, no host config: the menu offers "Learn by using it"; drive the stub
  provider, exit, assert the exit prompt ("Review and save / Save all / Not now"), accept, and assert
  the managed config and `env.secret.yaml` land in the per-slug user scope.
- **UC4** — host config present: assert the menu offers "Learn from my host setup" and that
  `firstrun.SeedFromHost` pre-populates the per-slug scope (static secrets placeholdered, login stores
  dropped) before the session.
- **`config learn`** — replay a finished session (owning process gone) through the review path.
  **Call:** seed the `state.LearnSession` record and a real home volume (what the CLI tests already
  do) rather than abnormally killing a launcher mid-turn. Avoids an abnormal-kill path and a leaked
  VM the suite's clean-quit model would not cover; the journey exercises the analysis, which is the
  unit `config learn` owns. The CLI test's seams (`seedLearnState`, `stubLearnHomeVolume`) are
  unavailable to a blackbox suite that drives the real binary, and there is no `volume create` — the
  volume commands only replace an existing home volume — so the journey obtains the real volume from
  a **preliminary `run --learn`** that reaches the TUI, quits, and answers the exit prompt with
  "Not now": it creates the VM and home volume, provisions the stub provider into the home (where it
  persists for the replay's read-back), writes nothing, and clears its own session record. The
  journey then seeds the record and a scrubbed observation through `state.RecordLearnSession` and
  `channel.Save` under an isolated state root.

**Assertion granularity (call).** A journey asserts the *semantic* effect, not a byte-for-byte
golden: the credential's env-var name appears in `env.secret.yaml` with its host scoping, the managed
config file contains the `${ENVVAR}` placeholder, and the expected config keys are present. This
keeps the "adapt the test when the harness's layout changes" triage cheap; a full-file golden would
recouple the test to every incidental byte of a harness's authored config.

Two of those three hold only as far as the product produces them. For a session that learns purely by
writing back, the managed-config assertion is bounded: `analyser.ManagedConfigFindings` covers only a
config file the session *changed* since the baseline manifest, and the launcher provisions the seeded
provider config (through the `home:` manifest) during `prepareSandbox`, **before** `CaptureBaseline` —
so a provisioned provider file is never "changed" and UC1 gets no `${ENVVAR}` placeholder or config-key
assertion. UC1 therefore asserts the static-secret write only.

UC4 is the journey that asserts the placeholder and keys from the host seed, and it does so without
the changed-config write-back: the host-seed choice runs `firstrun.SeedFromHost` before the session,
which writes the placeholdered managed config (and the login store it drops) ahead of the baseline, so
the assertion does not depend on the session editing anything. The `config learn` replay also asserts
the placeholder, but through the changed-config write-back: its seeded baseline is empty (the
journey cannot synthesize the run's real baseline, whose hashes live only in the volume), so every
read-back path is "changed" and the provisioned provider file reaches `ManagedConfigFindings` and is
written back placeholdered. The seeded baseline is therefore load-bearing — a future change that
records the real baseline would drop the replay's managed-config assertion.

**Attribution journey (v1 limitation).** The attribution journey (`test/msb/journeys/attribution.go`)
drives `run --learn` against the host-side stub and asserts the learned scope keeps the credential
scoped to the stub host and does not widen the network. It does **not** demonstrate that observation
attribution supersedes the config-entry fallback: v1 never populates the attribution's credential.
`recorder.attribute` (`internal/sandbox/channel/observation.go`) records header NAMES only and
initialises `Attribution.Credential` to `""`; the proxy (`internal/sandbox/channel/proxy.go`) passes
header names only; and `analyser.credentialHosts` (`internal/sandbox/analyser/observation.go`) matches
`entry.Credential == credential.EnvVar`, so its attribution branch is unreachable and hosts always
come from the read-back config entry. The plan's "config-entry source but no hosts" is likewise
unachievable: the credential must name the stub baseURL for the agent to reach the stub, and that
baseURL host is exactly the fallback host the assertion checks. The journey therefore discriminates
only that a `run --learn` session writes a stub-host-scoped credential and keeps the network posture;
proving attribution over the fallback needs the product gap closed first (a follow-up, not #793).

This also absorbs **#252**'s "the first turn round-trips and no first-run scaffolding appeared": that
assertion belongs to a journey, not to the minimal startup gate.

Non-interactive and dry-run behaviour are asserted per the repo's invariants (warn, never hang).

## Exploratory tier and the promotion flow

The surface sweep (`specs/testing/release-surface-testing.md`) gains a learn-mode journey block and a
spine step that runs `make test-msb-journeys` (warm the shared root once). Journey agents then
hand-drive the same flows under the sweep's isolation.

**Shared-root interface (call).** The spine passes the sweep's root to the suite through the env vars
the driver already resolves — `AGENTS_SANDBOX_SMOKE_ROOT=<sweep>/smoke` and
`AGENTS_SANDBOX_MSB_TEST_HOME=<sweep>/msb` — so the suite's image/data home and reuse lock live inside
the sweep root. No new interface and no second lock; the Go suite's existing reuse lock serializes the
spine's invocation against any other carry-over run. The `test-msb-journeys` target derives both vars from its
`MSB_REUSE_ROOT` make variable, so the spine sets `MSB_REUSE_ROOT=<sweep>` when it invokes the target.

- Exploratory finding → new/extended automated journey test (recorded as an issue, then implemented).
- Automated failure → agent triages against the harness's current layout; if the harness changed, the
  test is adapted; if the product regressed, it is a real finding.
- One issue tracks **other areas to drive through harnesses** (notifications, worktrees, serve-only,
  docker-in-sandbox), added as journey blocks later.

## Failure modes the implementation must survive

- A tagged-only package is dropped by `go test ./...` — the table and completeness test must stay
  untagged (in `driver`).
- The rename touches specs/brain/docs; the doc-link gate must stay green (relative links updated).
- `make test-msb`'s explicit package list must be updated so startup stays the quick gate.
- The driver's own `TestMain` must install its acyclic fail-fast mocks, like every test package.
- A journey under load must not become a host-dependent flake (no fixed wall-clock against a real
  VM; reuse the readiness/quit-tap pattern).

## Left to the builder

Exact driver API and file split; the precise
ambient-scan generalization (which non-test files under `test/` to parse, and its fixtures); the
deadcode `test/` filter in `ci/deadcodecheck`; the surface-sweep journey task text; the doc/brain/
changelog edits; and whether any journey runs under the `startup` suite's image prebuild or its own.

## Resolved in review

- **Proxy turn is a prerequisite.** The plan includes the spike that proves a real agent turn through
  the host-owned terminating proxy (host-side stub destination), ahead of the attribution journeys.
- **v1 scope is opencode2 only.** The journey spec is a table field; the other harnesses are recorded
  exemptions, with their coverage tracked by a follow-up issue.

## Calls recorded for the user to check

- Package/target: `test/msb/{driver,startup,journeys}`, tag stays `msb`, `make test-msb-journeys`.
- Harness table in `driver`; `driver` untagged.
- Keep `AGENTS_SANDBOX_SMOKE_ROOT`.
- Gate scope: option A (all of `test/` is test code).
- Provider: **host-side** stub reached through the proxy; proving the proxy turn is a prerequisite.
- v1 harness scope: opencode2 only; the journey spec is a table field with recorded exemptions.
- `config learn` journey: seed the session record + a real home volume, no abnormal kill.
- Assertions: semantic (env-var name, host scoping, placeholder, keys), not byte-for-byte goldens.
- Shared root: the spine sets `AGENTS_SANDBOX_SMOKE_ROOT` / `AGENTS_SANDBOX_MSB_TEST_HOME` into the
  sweep root; no new interface.
- #252's first-turn assertion moves into the journeys suite; other areas tracked separately.
