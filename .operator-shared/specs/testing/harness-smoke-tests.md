---
description: Contract for the `msb`-tagged harness smoke tests that start every registered harness to its TUI in a real microsandbox VM (readiness by alternate-screen marker, a startup-line marker, or a settled wait; optional seeded home files), the per-harness isolated home tree with a shared microsandbox image cache, the parallel subtests sized to the host's free memory and the bounded image prebuild, the smaller per-VM memory cap, the reusable-root escape hatch, and the isolation that keeps them off the developer's existing agents-sandbox state.
read_if: Adding or changing the harness smoke tests, the `msb` build tag, `make test-msb`/`make test-msb-reuse` or
  `AGENTS_SANDBOX_SMOKE_ROOT`, the harness quit-press table, the readiness signals or seeded home files, the
  per-harness home split or why the harnesses run serially, or when a harness's TUI startup or quit behavior changes.
---

# Harness smoke tests

## Problem

Harness support can break without any test noticing. `opencode2` did: its `DaemonProvider.DaemonHealthCmd`
sent no credentials, v2's `serve` answers every `/api/*` route with `401` when unauthenticated, and
`ensureDaemon` (`internal/sandbox/vm/daemon.go`) polled to its `daemonReadyTimeout` (60s) before failing. The
daemon was healthy; the launcher could not see it, and the user got "… did not become healthy within 60s"
instead of a TUI. See `specs/agents/opencode2.md` for the fix.

No existing test could catch it:

- Every `ensureDaemon` test stubs the health seam (`daemonHealthFunc` plus fake `DaemonProvider`s), so they only
  assert the profile matches the fake the test installed — never the shipped harness binary.
- The `integration` tests build real images and run `docker run … --version` probes
  (`internal/sandbox/image/render_integration_test.go`); they never start a daemon or attach a client.
- Nothing drives the launcher end to end.

The same gap means a newly registered harness has no test that it starts at all.

## Contract

**Tag and location.** The suite lives in package `test/harness/` behind the build tag `msb` (a tag names the
runtime it requires, as `integration` names a real docker daemon). `make test-msb` runs it, together with the
`msb`-tagged real-client tests in `internal/sandbox/msb/` (`realstack_msb_test.go`); it is not part of
`make check`, `make test`, or the default CI. `make compile-all` compiles it (`-tags 'integration msb'`) so a
tagged-only compile regression is caught on every PR.

**Scope of the assertion.** For every name in `agent.Names()`, the suite starts the harness the way a user does
(`agents-sandbox run --agent <name>` under a pseudo-terminal, `github.com/creack/pty`) and asserts:

1. the attach client reached its full-screen TUI, within a timeout; a first-run/login screen counts. A harness
   whose UI takes over the alternate screen (`ESC [ ? 1049 h`) announces this while the process is still alive; one
   that keeps the terminal's normal buffer emits no such marker and declares a startup-line `ReadyMarker` instead
   (see Readiness);
2. the process then exited `0` after the harness's quit presses were written to the terminal; escalation
   (`SIGTERM`/`SIGKILL`) is cleanup only and fails the subtest if reached;
3. the output never contains the daemon-timeout marker (`did not become healthy`).

It proves "the harness reached its TUI", not "the client session is functional": a healthy daemon whose client
renders a connection-error screen still passes. Daemon health itself is covered because the launcher blocks in
`ensureDaemon` before any TUI, so a broken daemon fails the TUI-wait or assertion 3.

**The docker-storage sibling.** The suite also runs the docker-storage smoke — the launcher's `shell` in a
docker-enabled VM, asserting the in-VM dockerd uses fuse-overlayfs and can layered-build and run an image (#489) —
as a **sibling subtest** of `TestHarnessSmoke` (`runDockerStorageDriverSmoke`), not a second top-level test. It is
not a harness, so it cannot be a table entry (the table's key set must equal `agent.Names()`); as a sibling it shares
the suite's root, microsandbox home, prebuild, and cleanup, and overlaps the harnesses through the same slot pool.
Its docker-enabled image is built in the serial prebuild phase, so its subtest pays only its VM start. The shell
prints no startup marker, so its readiness is the first command echoed back, retried until it lands.

**Readiness.** The alternate screen is the strong signal: it means the harness's own UI has taken over the terminal.
Every other harness declares a startup-line marker.

**A ready marker.** A normal-buffer harness whose CLI prints a stable startup line declares it in `ReadyMarker`, and
its subtest ends the readiness wait as soon as that line appears. The match ignores escape sequences and whitespace, so
a diff-painting TUI (claude-code's Ink) still matches when it interleaves escapes or renders spaces as cursor moves
(`Welcome to Claude Code` arrives space-less). All three normal-buffer harnesses use one: `hermes` prints
`Hermes Agent v<version> (<date>)` (`show_banner`) before it accepts input, so the spec waits for `Hermes Agent v`;
`claude-code`'s startup splash prints its product name; and `oh-my-pi` prints `No default model selected` above its
editor's prompt bar when it starts with no model configured (the seeded config selects none, and the child's restricted
environment carries no API-key variables). The markers are version-independent; the versions themselves are not pinned.

**A wall-clock settle (fallback).** A normal-buffer harness whose CLI prints nothing stable falls back to waiting out
the launcher; no harness currently needs it, but it stays for one that does. Two measurements shape that wait.

- **quiet output does not mean the harness has rendered.** The launcher loads an image and starts a VM before the
  harness runs, printing nothing for seconds at a time while it does, so a quiescence-based fallback fires on
  launcher output before the harness has drawn anything.
- **input written to the pty before the attach client starts is buffered, not discarded.** msb's attach client puts
  the host terminal in raw mode with `TCSANOW` and flushes input (`TCIFLUSH`) only when it restores the terminal. That
  window is not what makes a quit unreliable, though: the presses that get ignored are made seconds *after* the attach
  is live and the harness is already painting, so the loss is on the harness's side — see "Press until it goes" below.

Once the image prebuild has run, the harness's UI comes up about 14s after launch, so the settled fallback is 25s. A
harness whose startup is much slower raises the ceiling with `ReadyTimeout`: `hermes` carries a whole Python/Node tool
store, so its spec sets `heavyweightReadyTimeout` (120s). The override bounds either wait (marker or settle), not a
sleep — hermes' banner marker ends its wait as soon as the CLI is up. An alternate-screen harness is bounded by the TUI
marker wait (180s) instead.

**Harness table completeness.** A single table maps harness name to the key the smoke test taps, an optional network
override, a `KeepsNormalBuffer` flag, an optional readiness marker, an optional readiness-timeout ceiling, and optional
seeded home files. Fast untagged unit tests assert the table's key set equals `agent.Names()` and that every entry names
a key, so a newly registered harness fails `make test` until it is added — a harness cannot be added without smoke
coverage.

| harness | key | how it ends |
| --- | --- | --- |
| `opencode`, `opencode2` | `0x04` (Ctrl-D) | their documented `app_exit`; they leave once a tap lands after the TUI has finished coming up |
| `pi` | `0x04` (Ctrl-D) | documented `app.exit`; leaves on the first tap |
| `oh-my-pi` | `0x04` (Ctrl-D) | documented `app.exit`, inherited from pi, which it forks; readied on its no-model startup warning |
| `claude-code` | `0x03` (Ctrl-C) | repeated presses leave it — its screen announces "Press Ctrl-C again to exit", and three is what a user found; readied on its startup-splash marker |
| `codex` | `0x03` (Ctrl-C) | quits on Ctrl-C; its TUI uses the alternate screen, so readiness is the alternate-screen marker |
| `hermes` | `0x04` (Ctrl-D) | its classic CLI's REPL exits on Ctrl-D; readied on its banner marker and seeded (below) so it starts the REPL instead of its first-run setup guard |

Three harnesses keep the terminal's normal buffer (`KeepsNormalBuffer`) and are all readied by a marker:
`claude-code`, `hermes`, and `oh-my-pi`. The alternate-screen harnesses are `opencode`, `opencode2`, `pi`, and
`codex`.

**Seeded home files.** A harness whose host-side state decides whether its CLI starts headful declares `SeedHomeFiles`,
a map of home-relative path to content written under the harness's isolated HOME
(`<root>/runs/<name>/home`) before the run; the launcher
provisions it into the VM the way it provisions a real `~/.hermes/config.yaml`. `hermes` needs it: with no provider its
classic CLI stops at a first-run setup guard that always exits `1` (`_first_run_setup_guard`), so no quit key could
leave it `0`. The seed makes a dummy, loopback provider count as configured (and settles the shared-metrics offer) so
hermes reaches its REPL, where Ctrl-D exits `0`. The seed is a config, not a credential: the endpoint is a closed
loopback port and no request leaves the VM before a message is submitted. `oh-my-pi` likewise seeds
`.omp/agent/config.yml` (`startup.setupWizard: false`) so its first-run setup wizard — a fullscreen overlay that would
otherwise come up before the editor — is skipped.

**Tap until it goes, then assert the exit.** `quit` taps the harness's key every `quitPressInterval` (400ms) until the
process exits, for at most `quitTimeout` (15s), then asserts exit code 0. A fixed sequence is wrong for two measured
reasons. A TUI paints its screen seconds before it accepts input, so taps made in that window are ignored: on a KVM host
`opencode2` leaves on a tap ~3s after the alternate-screen marker and ignores the same key at ~0ms and ~350ms, and
`opencode` ignores a tap at ~3s while its screen still shows the launcher's own output. Whether those early bytes sit
buffered in the guest and are consumed once the harness reads, or are delivered and ignored while it initialises, is not
pinned down; what is measured is that tapping on is what carries the timing, and that a fixed readiness wait does not.
A harness can also need the key more than once — `claude-code` leaves on a repeated Ctrl-C. An early probe concluded from
two presses that no key the suite could send left it and made it an exception that asserted only that its screen painted;
manual testing showed three presses leave it, so the exception is gone and every harness asserts an exit.

**Isolation (hard requirement).** The child never reads or writes the developer's existing agents-sandbox
configuration or state. Each run creates one temp root, shared by all subtests, and:

- the root is created under a **short, fixed base** (`/tmp/asb-…`), not the host's `TMPDIR`: microsandbox
  derives its sandbox control-socket path from the private home under `XDG_DATA_HOME` (`<root>/data/agents-sandbox/msb`)
  and rejects a derived path that does not fit a Unix socket's `sun_path` (104 bytes on macOS, the tighter
  target), so a long `TMPDIR` — macOS's default is ~49 bytes — would fail every harness before its TUI with
  "sandbox runtime socket path is too long"; a fast unit test pins the budget (`msb` appends a fixed 50-byte
  suffix and rejects a home above 55 bytes on Linux, microsandbox v0.7.8);
- the per-harness subtests run **in parallel**, bounded by a semaphore sized from the host's **free memory**
  (`harnessConcurrencyFor`: as many `smokeWorkingSetMiB` (1.5 GiB) VMs as fit after a fixed 2 GiB host reserve — the
  host OS, docker, and the concurrently running `internal/sandbox/msb` test binary — never fewer than one). The
  count is read via `sysinfo.AvailableMemoryMiB` (falling back to `TotalMemoryGiB`), and `AGENTS_SANDBOX_MSB_PARALLEL`
  overrides it. It is enforced by the semaphore, not `-parallel`, so even a bare `go test -tags msb ./...` cannot
  start a VM per harness; each smoke run boots its VM at `--memory` `smokeMemorySpec()` (2 GiB) instead of the
  launcher's 4 GiB default. The working set, not the VM ceiling, sizes the count because a guest allocates only the
  pages it touches (virtio-mem); it tracks the heaviest harness (measured peaks under 0.5 GiB for pi, over 1 GiB for
  opencode2), so sizing on the lightest would oversubscribe the host. Each harness gets its own home base
  `<root>/runs/<name>` — created before the child starts by `prepareHome`, including its `TMPDIR`, which the launcher
  needs (a missing `TMPDIR` fails sandbox creation with `io error: no such file or directory`) — so overlapping runs
  never share a `HOME` or a seeded config, while `XDG_DATA_HOME` stays `<root>/data` so every harness shares one
  microsandbox home (the image cache);
- the child environment is built from an **allowlist**, dropping inherited `HOME`, `XDG_*`,
  `AGENTS_SANDBOX_*`, `OPENCODE_SANDBOX_*`, and `MSB_*`; the inherited variables kept include `PATH`, `TERM`, `LANG`,
  `LC_ALL`, `LC_CTYPE`, and the `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` family. It sets
  `HOME=<root>/runs/<name>/home`, `XDG_CONFIG_HOME=<root>/runs/<name>/config`, `XDG_DATA_HOME=<root>/data`,
  `XDG_STATE_HOME=<root>/runs/<name>/state`, `XDG_CACHE_HOME=<root>/runs/<name>/cache`, and
  `TMPDIR=<root>/runs/<name>/tmp`; `TERM` defaults to `xterm-256color` when the
  inherited value is absent, empty, or `dumb`;
- the child deliberately keeps `DOCKER_HOST`, `DOCKER_CONTEXT`, and `DOCKER_CONFIG`: they carry the host
  docker endpoint the runner-image build needs and hold no agents-sandbox config or state, so keeping them
  does not weaken the isolation — and the docker preflight runs under the same isolated environment, before
  any image build, so it can never certify a daemon the child cannot reach;
- the child gets a sized terminal — the parent's window when the test process has a tty, else 40×120 — so a
  full-screen TUI does not render into a zero-column terminal;
- the working directory is a temp project dir, so project config and the derived slug are throwaway;
- consequently no real `~/.config/agents-sandbox`, `~/.local/{share,state}/agents-sandbox`, or
  `~/.cache/agents-sandbox` path is read or written, and the child starts from deterministic defaults.

The isolated data home forces microsandbox to install its private runtime and build/load each harness's runner
image on first run; that cost is paid once per run, during the image prebuild rather than inside any harness's
TUI window. The prebuild builds each image in its own project dir, up to `prebuildConcurrency` (2) at a time,
because the stage is dominated by the multi-GB `docker save | msb ImageLoad` transfer, which contends on disk.
The shared host docker daemon builds the runner images, as the existing `integration` tests already
do. The suite's own process installs the fail-fast seam mocks in `TestMain`; `TestMain` does nothing else, so
`compile-all`'s `-run=^$` stays a genuine no-op. The suite's phases run in an order that lets a host which
cannot run it skip before paying: the platform/`/dev/kvm` preflight first, then the shared root and binary
build behind a `sync.Once`, then the isolated-env docker preflight, then the per-harness image prebuild
behind its own `sync.Once`, then the harnesses.

**Reuse escape hatch.** A set `AGENTS_SANDBOX_SMOKE_ROOT` points the suite at an existing directory it keeps
between runs (validated to fit the socket-path budget) instead of a fresh temp root, so the microsandbox image
cache under `<root>/data` survives and the per-run image transfer is paid once. `make test-msb` uses a fresh
root; `make test-msb-reuse` sets it. The default (unset) is a fresh root removed after the run; the reused
directory is left in place.

**Network.** A harness runs with `--network none` (the secure default) unless the table overrides it, and `claude-code`
and `codex` do, with `public`: `claude-code` offline exits `1` (`ECONNREFUSED api.anthropic.com`), because it checks
in with its API before drawing anything, and `codex` may reach its API at startup. `hermes` runs under the default
`none` — its seeded provider endpoint is loopback, so it needs no egress to reach its REPL. The other four
(`opencode`, `opencode2`, `pi`, `oh-my-pi`) render identically with and without egress.

**Preflight.** Skip, never fail, when the environment cannot run the suite: `/dev/kvm` missing on Linux, a
non-Linux/non-darwin platform, or an unreachable docker daemon. The platform/KVM check runs before anything
is built (no binary, no image), and the docker check runs before the first image build, so an environment
that cannot run the suite never pays for it.

**Teardown.** A clean quit lets the launcher stop its VM. A `t.Cleanup` best-effort destroys any sandbox left in
the isolated home before the temp root is removed, so a failed subtest cannot leak a VM pointing at a deleted
home; it lists with `sandbox list -q` and removes each name with `sandbox remove --force --yes` (`--yes` is
required, or the non-interactive remove blocks on its confirmation). A freshly created temp root's removal is
registered as soon as the root is created, so a skip (the docker preflight) or a failed launcher build removes
it too; a reused root (`AGENTS_SANDBOX_SMOKE_ROOT`) is kept. The sandbox sweep stays separate because it needs
the binary and a reachable docker daemon.

## Non-goals

- TUI content or behaviour beyond reaching the full-screen TUI and leaving it on its quit key.
- Credentials, authenticated sessions, or harness network egress. A `SeedHomeFiles` config may name a dummy, loopback
  provider so a CLI reaches its REPL; it is not a usable credential and sends nothing off the VM.
- Replacing the mocked `internal/sandbox/vm` tests.
- Agent version resolution, the upgrade prompt, or the image build.
- Running on every PR (needs KVM, docker, network — host-only today) and a dedicated CI job.

## Risks and open items

- `claude-code`'s startup-splash marker is a broad product-name match, the weakest of the readiness signals: if a
  release stops printing it (or prints it only after the login screen it shows first), the subtest fails with the
  screen it stopped on and the marker needs a measured replacement. Its exit is also the least like a single
  documented keybinding — a repeated Ctrl-C on a first-run screen — so a change to its onboarding is the likeliest
  cause of a failed subtest.
- `hermes`'s seeded config couples the subtest to two upstream contracts: its `provider`/`base_url` config shape
  (whether a bare custom provider still counts as configured) and its shared-metrics `offer_version` (a bump re-asks
  the prompt before the REPL). A change to either fails the subtest with the screen hermes stopped on, and the fix is to
  update the seed, not the harness.
- The quit tap is a bound, not a diagnosis: a harness that starts ignoring its key fails after `quitTimeout` of taps
  rather than reporting why.
- Seven harnesses mean seven runner-image builds on a cold home. The cold home also re-transfers each
  multi-GB runner image into the microsandbox cache, which dominates the suite's wall time; `make test-msb-reuse`
  keeps the home (`AGENTS_SANDBOX_SMOKE_ROOT`) so the transfer is paid once, and the prebuilds and harness
  subtests run with small bounded concurrency (the harness count sized to the host's free memory). The host-only
  placement absorbs the remaining cost.
