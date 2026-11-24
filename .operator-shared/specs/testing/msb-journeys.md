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
  `changelog.d/<PR>.<type>.md` fragment. A tracked `scripts/*.sh` and a non-test `.go` file under `test/`
  count as production code for the fragment; docs-only and `_test.go`-only changes need none.
- `test-isolation.md`: every test package's `TestMain` installs its acyclic fail-fast mocks. The
  dead-code gate ignores findings under `test/`, so the untagged `driver` needs no
  `//deadcode:ignore` (its host calls keep their `//testenv:allow` markers); a **tagged-only** package
  is silently dropped by `go test ./...` (verified), so a package that must run in the default suite
  needs an untagged file.
- A host that cannot run the suite — no `/dev/kvm`, an unreachable docker daemon, or a platform other
  than Linux/darwin — skips, never fails; `driver.Preflight` owns the check and runs before anything
  is built.
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
  which would otherwise escape the ambient scan because they are not `_test.go` files. The linter follows
  the same scope: a `^test/` path rule in `.golangci.yml` relaxes `exhaustruct`, `goconst`, `gosec`,
  `nonamedreturns`, and `reassign` there (the test-only subset of the `_test.go` rule), so non-test files
  under `test/` carry no `//exhaustruct:ignore` or `//nolint` markers for them.
- **B — marker-driven:** add the file-scope marker to `driver` and key the ambient scans on it.
  Rejected as fiddlier for no extra reach.
- **C — keep host-touching calls in `_test.go`, extract only pure helpers.** Rejected: it defeats
  sharing the pty session, the most valuable shared piece.

### Provider for a journey

A journey's model turns go to a **host-side stub** with a dummy provider seed, so no turn needs public egress.
The VM reaches it through the host gateway, which completes turns but bypasses the learn-mode observation proxy;
whether any route both completes a turn and is observed is #793's open question (see "Route" below). The
read-back journeys (config changes, static secrets, managed-config write-back) do not depend on it.

#### Stub provider

The stub is a host-side loopback HTTP listener (`test/msb/journeys/stub_provider.go`), started per journey and
closed by `tb.Cleanup`. It records every request it served (`StubProvider.Requests`) and answers a chat completion
with a numbered reply (`stub-reply-<n>`), streamed when the request asks for a stream, so a journey can assert that a turn
reached the stub and completed. The provider config declares a separate `small_model` (`stub/stub-title`), which
opencode uses for side requests such as the session title, and the stub answers it with a different text: a title
on screen cannot pass for the turn's reply, and a title request does not count as the turn.

- **Protocol: plain HTTP.** The stub needs no CA, and the run needs no `network.observe.skip-upstream-verify`.
- **Provider config.** The harness adapter's `Stub.HomeFiles(addr)` returns the VM-home-relative config manifest
  (`driver.SeedLauncherConfig`'s target→content map); for opencode2 it is one target, opencode's `.config/opencode/opencode.json`
  (the file opencode actually reads; `config.json < opencode.json < opencode.jsonc`), holding an
  OpenAI-compatible provider `stub` whose `options.baseURL` is `<advertised>/v1` plus the top-level
  `model: stub/stub`. `StepSeedStub` provisions it through the launcher's `home:` manifest. That path
  matters: seeding a launcher snippet under `XDG_CONFIG_HOME/agents-sandbox/<agent dir>` makes
  `firstrun.managedConfigPresent` count a managed layer and suppresses the menu, and seeding the isolated
  HOME's `~/.config/opencode` makes `firstrun.HostFiles` read it as host config; the `home:` manifest
  avoids both, so the first-run menu (and its "Learn by using it" choice) is still offered.
- **Route: the host gateway.** The listener binds `127.0.0.1:0`; the *advertised* origin defaults to
  `http://host.microsandbox.internal` (overridable through `AGENTS_SANDBOX_JOURNEYS_STUB_ORIGIN`; the port is
  always the listener's). A VM reaches it under a network profile that includes `host`, and microsandbox
  delivers the connection to the host's loopback listener. Verified on a KVM host: a plain `run` on opencode2
  completes turns against the stub (`TestHarnessJourneys/core-session/opencode2`).
- **The route does not pass the observation proxy.** Host-gateway traffic bypasses the learn-mode terminating
  proxy, and the runtime enforces the egress policy before the proxy is consulted, so a name the policy denies
  or the guest cannot resolve (the former `agents-sandbox-stub.invalid` default) is never observed either. No
  route is known yet that both completes a turn and is observed; the proxy spike, the table journey
  `proxy-observes-the-stub` (opencode2), therefore opts in through `StepRequireOptIn`: it is skipped unless
  `AGENTS_SANDBOX_JOURNEYS_PROXY_SPIKE=1`, and stays the open question of the attribution half.

#### Typing into a harness

A TUI can paint before it takes input: opencode answers its terminal-capability queries first and, on the host,
swallowed an early write whole. `StepSendPrompt` therefore types the prompt, waits for the harness to echo it (only
output printed after the write counts), and retypes on a missing echo before it presses Enter. A partly swallowed
write or a late echo could leave stray characters before the retyped prompt; a journey only needs the turn to be
sent, so it tolerates that. A prompt must be text a repaint does not print on its own, since the echo is a
substring match. The echo also comes from the terminal itself while no harness reads it, so the step is only
sound after a readiness marker that means the harness takes keys (see "Rules the harnesses taught").

### Isolation between journeys

By default all journeys of an agent drive the same project directory (`driver.ProjectDir(root, <agent>)`), so they
share one project slug and therefore one microsandbox VM and one home volume. The runner sweeps sandboxes only at
suite start and teardown, so a journey on the shared project must not rely on the shared volume being clean:
per-journey state it asserts must live in the journey's own isolated HOME (a fresh temp dir per subtest, threaded
by `driver.IsolatedEnv`). The shared VM also carries state between journeys: a later journey attaches to the VM an
earlier one started, with that run's network policy and any agent session still running in it. A journey that
needs its own VM starts with `StepOwnProject(<label>)`, which builds the agent's runner image in
`<agent>-<label>` (cheap, the prebuild's layers are cached) and points the journey's project there; the teardown
sweep covers it, since it removes every sandbox in the suite's isolated home.

### Journey model: scenarios, steps, harnesses

A journey is a Go table entry (`test/msb/journeys/table.go`), not a `.feature` file: the table is the one
source of truth and its steps are type-checked. It reads as Gherkin anyway:

- **Keywords.** Every step carries `Given`/`When`/`Then`/`And` (the table test rejects one without) and a
  sentence name, so `Step.Text()` is a Gherkin line. The runner logs the whole scenario before it runs and one
  line per step with its duration, so the boot cost of a journey stays visible.
- **Actions and checks.** A step is an *action* (its failure ends the journey: later steps depend on it) or a
  *check* (`AsCheck`: its failure is recorded with `t.Errorf` and the journey goes on). The kind is about
  dependency, not the keyword: "Then the harness TUI comes up" is an action. Final assertions are checks, so
  one run reports every broken check that does not block the chain.
- **Parallel harnesses, serial journeys.** The journeys run one after another; a journey's harnesses run in
  parallel, as many at once as `driver.HarnessConcurrency` allows (the startup smoke's memory-sized limit).
  Parallel cells never share a VM (each harness has its own project). The journeys on a harness's shared
  project share its VM and its **home volume**, which outlives a journey (a host config one provisions is still
  there for the next), so their table order is part of what they assert. Running journeys in parallel was
  measured and rejected for now: it cut the suite from about 100 s to about 75 s, but the extra VMs starting at
  once made opencode2 take a typed prompt before its input read keys, failing its turns in most runs (see the
  readiness rule below). The one in-process global, the state-path swap of `StepSeedLearnSession`, is
  serialized by a lock.
- **Few, long journeys.** A harness boot dominates a journey's runtime, so a new check extends an existing
  journey when it can run on the state that journey already reached; a new journey is for a different
  starting state (another first-run choice, network profile, or a clean VM).
- **Journey × harness.** A journey lists the harnesses it runs on and runs once per harness as
  `TestHarnessJourneys/<journey>/<harness>`; `make test-msb-journeys JOURNEY=<name> HARNESS=<agent>` selects
  one cell (either may be left out), and the suite then prebuilds only the images the selected cells drive (never
  the startup smoke's whole set). `test/msb/journeys/JOURNEYS.md` is the generated scenario catalogue
  (`TestJourneyCatalogueIsCurrent` fails when it is stale; `-update-catalogue` regenerates it). The
  completeness contract is per pair: every (journey, registered agent) pair is driven or exempted with a
  recorded reason (`Exemptions`, derived per journey from one explicit `placedHarnesses` list, a fallback
  reason, and per-harness reasons), so a newly registered agent fails the default suite until it is placed, and a
  stale exemption for a driven pair fails too.
- **Seeds.** A journey starts from the startup smoke's seeds for the harness (`driver.Spec.Seeds`), so
  first-run state the smoke already settles (claude-code's `.claude.json`) is not restated per adapter:
  `StepSeedStub` applies the smoke's `home:` manifest with the adapter's `Stub.HomeFiles` added, and
  `StepConfigureLauncherEnv` the smoke's agent snippets with the adapter's on top (a snippet is a managed layer,
  and a learn journey needs the first-run menu). The smoke's host files are not seeded: the first-run menu reads
  host files as host setup, and a journey's host setup is its own (`StepSeedHostConfig`).
- **Harness adapter.** Everything a step needs to know about a harness beyond starting and quitting it
  (`driver.Spec`, shared with the startup smoke) lives in its `Harness` adapter (`Harnesses`), in three parts:
  `Stub` (how it reaches the stub: `HomeFiles` through the `home:` manifest, `Env` through the launcher env file,
  `Snippets` merged into its own config), `Drive` (how a journey drives its TUI: `ReadyMarker`, `LaunchArgs`,
  `ResumeArgs`, `WriteFileCall`, and `Setup`, the harness's own first-start screen), `Login` (how it signs in,
  for the login journey), and `Learn` (what learn mode
  finds: its host config and credential store, the credential env var the read-back derives, the managed-config
  keys, whether the managed config can hold the credential; nil without learn coverage). A learn journey
  (`LearnMode`) says only which evidence its path produces (`ScopeWant`: config keys, placeholder); the values come
  from `Learn`, and a placeholder is expected only where `ManagedConfigHoldsCredential` says the harness's managed
  config can hold one, and the completeness test requires `Learn` for every harness a learn journey lists. A step or
  journey carries no harness's path or format literal: the scope checks read every config dir of the agent,
  recursively (skipping `node_modules`), and the login-store check fails on the store's file name or its
  content under any name (so a store fixture holds no static value the host seed would rewrite). A flagless command such as
  `config learn` gets the journey's agent through `AGENTS_SANDBOX_AGENT`. The adapter grows a field only when a
  journey needs it.

The learn-mode journeys cover **opencode2 and oh-my-pi**; core-session covers opencode2, claude-code, codex,
pi, oh-my-pi, and hermes; the login journey covers codex. The rest of the #852 slice is blocked by the
product, not the suite: on claude-code, codex, and pi
the read-back finds no host-scoped credential to learn (the exemption reasons in `table.go` name each blocker:
#821, #873, #820/#849). The rollout harnesses are #794.

### Rules the harnesses taught

Six harnesses joined with an adapter each and no harness branch in shared code (the refactor hypothesis of
#851 held at every step). What they taught is now the rule:

- **A harness's natural config is often closed to the journey.** Its own config file is frequently the
  launcher's merged-config target or a reserved `home:` target, so the adapter has three routes to the stub:
  `Stub.HomeFiles` (the `home:` manifest), `Stub.Env` (the launcher env file), and `Stub.Snippets` (merged into
  its config). The env file and snippets are managed layers: they make core-session start configured, and they
  are unavailable to a learn journey, which needs the first-run menu.
- **A protocol is a stub handler, routed by path** (`/chat/completions`, `/messages`, `/responses`), not a
  harness setting. Numbered replies, the one-shot tool call, and the side-request split work for all.
- **A side request is one for the small model or without tools**; it gets `stubTitleReply`, no numbered reply,
  and is not a turn. A non-chat request (a model probe) is never a turn. An undecodable turn is answered 400 and
  recorded. A real tool-less turn would be misread; a journey driving one must refine the rule.
- **A failed turn wait prints the harness's own view.** `Drive.Diagnostics` is a shell command (opencode2:
  its sessions and the newest session's messages from its API) whose output a failed turn wait logs from the
  VM before the sweep removes it; opencode2's own log records no session events at its default level.
- **A readiness marker means the harness takes keys.** A marker printed before the input line reads the
  terminal lets a typed prompt lose its Enter, and the prompt step's echo check cannot notice, because the
  terminal's own echo satisfies it. `Drive.ReadyMarker` overrides the smoke's marker where the two differ.
- **A timing-based negative check needs a positive counterpart.** "No sign-in screen after a settle", "no setup
  screen after a settle" can pass on a slow host; each is paired with positive evidence (a login status, the
  readiness marker in output written after the exit keys).
- **Resume semantics differ per harness** (last active vs. last saved session); a journey orders its steps so
  every harness resumes the same session, rather than adding an adapter field.
- **A learn journey leaves the launcher unconfigured,** so a harness's journey snippet cannot silence its own
  first-start screen there: `Drive.Setup` names the screen, and `StepExitHarnessSetup` leaves it as a user
  would, waiting for it or the readiness marker and distrusting a marker that was already in the buffer.

### Adapter facts per harness

- **opencode2:** a provider file through `home:` (`opencode.json`), `write` with `path`, `--continue`; a
  `small_model` for side requests; the S1-verified learn profile (SQLite credential probe).
- **claude-code:** the environment (`ANTHROPIC_BASE_URL`, a dummy auth token) through the env file, since
  `settings.json` is reserved; `--permission-mode acceptEdits`; `Write` with `file_path`; resumes the last saved
  session.
- **codex:** a Responses provider in a `config*.toml` snippet (it ignores `OPENAI_BASE_URL`, and `-c` switches it
  to embedded mode), a dummy `auth.json` past its sign-in screen; `exec_command` with a quoted shell line;
  `resume --last`; its title is a tool-less main-model request. Its logins are the login journey's (below).
- **pi:** a `models.json` provider through `home:`, `--model stub/stub`, `write` with `path`, `--continue`.
  Its screen is up before its startup ends, and a prompt submitted then gets "Startup is still in progress"
  with the text kept; `Drive.StartupBusyMarker` makes the prompt step press Enter again while the notice
  shows and no turn arrived, ending on a turn at the stub or a screen quiet for a few scaled rechecks.
- **oh-my-pi:** pi's adapter with `models.yml`, plus a snippet that turns off the setup wizard, forked titles,
  and predictions (they would take numbered replies); `smol` on the stub's small model; its status line as the
  readiness marker. In learn journeys its wizard is left with `Drive.Setup`, and its key lives in `models.yml`,
  which the read-back reads but neither seed nor write-back copies (`Learn.ManagedConfigHoldsCredential` false,
  so UC4 covers the copy of a credential-free settings file and its dotenv fixture guards the store exclusion).
- **hermes:** a `custom` OpenAI-compatible provider in a `config*.yaml` snippet, `write_file` with `path`,
  `--continue`; the hint row under its input line as the readiness marker (its banner precedes input); it probes
  `GET /v1/models` and `POST /api/show` before its first turn. It dominates core-session's wall time.

### The core-session journey

`core-session` is the regression spine every harness is meant to pass: it chains the #783 promotion bar's
first run (A), real turn (C), second client (E), and persistence (D) over one project and two boots.

- **A:** the harness starts on a fresh home whose launcher env file is seeded (`StepConfigureLauncherEnv`), so
  the launcher counts it as configured and offers no first-run menu; the launcher prints the experimental notice
  exactly when the profile is experimental; the project gets no `.agents-sandbox` scaffolding. Core-session does
  **not** cover the first-run menu on any harness; the learn-mode journeys do.
- **C:** the stub answers the first turn with the harness's own write-file tool call (the adapter's
  `WriteFileCall`, e.g. opencode2's `write` with `path`/`content`), then with its text reply; the file
  appears in the project on the host, owned by the host user, and the reply shows. The stub numbers its
  replies (`stub-reply-1`, …), and a reply check accepts only a reply sent after the client's last prompt and
  shown after it, so a repainted earlier reply cannot pass.
- **E:** a second `run` attaches to the same VM while the first runs; it gets no reconfig prompt, and both
  clients complete a turn.
- **D:** both clients quit and `agents-sandbox stop` stops the VM; the next run boots it again (asserted from
  the launcher's "started existing project VM"), and with the adapter's `ResumeArgs` (opencode2: `--continue`) it
  shows the last session's message, so the session survived in the home volume, not in a live server. Client 1
  both sends the last prompt and quits last: opencode2 continues the last active session, claude-code the last
  saved one, and only that order makes them the same session.

A wrong tool argument is a tool error the harness reports back to the model and the turn survives, so only
the file check notices it: the adapter's tool call is pinned by a unit test to the argument keys the
harness's tool schema names (read from a recorded request). The journey runs on its own project (`StepOwnProject`), because it restarts and attaches twice, and
under the `host` profile the stub route needs. Per-journey projects stay opt-in: the learn-mode journeys run
in seconds on the shared project and assert only state in their own HOME.

### The login journey (#875)

`harness-login` covers a harness's own logins through the sandbox without a real account; a real provider
login stays in the exploratory tier. It runs on its own project, so the logins it stores stay out of the
shared home volume, and under `host`, which admits only the host gateway (disjoint from `public`), so no
connection reaches a real provider (DNS queries still resolve upstream). On codex (its browser login has no paste fallback, so the forward is its
only way back):

- **Link to the host.** The journey's launcher finds a fake host opener first on `PATH` (named like the
  opener's command: `xdg-open`, `open` on macOS). It records the links instead of opening them, so no run
  opens a real browser. Each login link must reach it unchanged and name a loopback `redirect_uri`.
- **Return leg.** For the provider login, the journey requests that callback on the host's loopback with
  the link's `state` and a dummy code. The answer must be the harness's own page (`Login.CallbackMarker`,
  which the forwarder's own answers lack) behind the host boundary: a sandboxing CSP and no `Set-Cookie`.
  The harness's token exchange then fails; that is expected.
- **A full OAuth login.** A stub authorization server on the host (`StubOAuthServer`) guards a stub MCP
  server the harness's config declares (`Login.MCPServer`). The MCP sign-in (`Login.MCPLoginArgs`) runs the
  whole flow: the MCP endpoint's 401 names the protected-resource metadata, the harness registers a client,
  the opener hands the authorize link to the fake browser, which follows the server's redirect to the
  callback on the host loopback, the forwarder replays it into the VM, and the harness exchanges the code
  with its PKCE verifier (the stub issues a token only for a matching one, and refreshes only a refresh
  token it issued). The fake browser follows only a link on the stub's origin. Every endpoint is on the host
  gateway's origin (codex refuses an authorize endpoint on another origin); the opener takes that name for
  a remote host, and only the fake browser resolves it to the host loopback.
- **Both logins persist.** An API-key login typed into `agents-sandbox shell` (codex takes the key only
  from a pipe) and the MCP token live in the VM's home, outside the `home:` manifest, which would
  re-provision them and mask a loss. After `agents-sandbox stop`, the harness reports the login from the
  shell (`Login.Status`, exit 0), the next run starts without the sign-in screen (`Login.SignInMarker`;
  the composer is painted behind it, so readiness says nothing), and the harness calls the MCP server with
  a token the sign-in issued; only calls after the sign-in count. Without the API-key
  login, both of its checks fail, which is how they were validated.

The harness facts are the adapter's `Login` group; a harness without it is exempted.

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
  the static credential lands scoped in `env.secret.yaml` under the per-slug user scope. It asserts no
  managed config: the provisioned provider file precedes `CaptureBaseline`, so the read-back reports
  nothing changed (see below).
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
that a `run --learn` session writes a stub-host-scoped credential and narrows a broad network posture,
not — as was intended — that observation attribution supersedes the config-entry fallback; proving the
latter needs the product gap closed first (a follow-up, not #793). The journey runs under `public`
rather than the agent spec's default `none`: under `none` the stub dial is denied, so the analyser
records no used destination and writes no network posture, leaving `StepAssertNetworkNotWidened`
vacuous; under `public` a turn that reaches the stub is a used destination, so the analyser's lock
(`none` plus the used host) actually exercises the guard. `public` does not include the host gateway the
default route names, so the dial is still denied; this only bites on a route `public` admits and the
observation proxy sees.

This also absorbs #252's project-scaffolding check: `StepAssertNoProjectScaffolding` asserts the run
creates no `.agents-sandbox` directory in the project, which belongs to a journey, not to the minimal
startup gate. It does **not** assert #252's other first-run prompts (upgrade, migration offer,
config-fork warning). The first-turn round-trip half is likewise **not** covered by the automated tier:
a journey's scope assertion is read-back-driven
(`analyser.SessionFacts.secretFindings` iterates the read-back's static credentials and `credentialHosts`
falls back to the config entry's hosts, since `channel.Attribution.Credential` is always empty), so
UC1/UC4/attribution pass even if no request leaves the VM. The core-session journey asserts real turns,
outside learn mode; that a learning session's turn reaches the proxy is the opt-in proxy spike's open
question (see "Stub provider").

Non-interactive and dry-run behaviour are asserted per the repo's invariants (warn, never hang).

## Exploratory tier and the promotion flow

The surface sweep (`specs/testing/release-surface-testing.md`) carries a learn-mode journey block and a
spine step that runs `make test-msb-journeys` once to warm the shared root. Journey agents then
hand-drive the same flows under the sweep's isolation.

**Shared-root interface (call).** The spine passes the sweep's root to the suite through
`AGENTS_SANDBOX_SMOKE_ROOT=<sweep>/smoke`, the env var `test/msb/driver` resolves; the suite's
image/data home, temp tree, and reuse lock all live inside that root. (`AGENTS_SANDBOX_MSB_TEST_HOME`
is the `internal/sandbox/msb` runtime's reuse home, which the journeys suite does not read.) No new
interface and no second lock; the Go suite's existing reuse lock serializes the spine's invocation
against any other carry-over run. The `test-msb-journeys` target derives the var from its
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
