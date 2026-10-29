---
description: Contract for the `msb`-tagged harness smoke tests that start every registered harness to its TUI in a real microsandbox VM (readiness by alternate-screen marker, a startup-line marker, or a settled wait; optional seeded home files), and the isolation that keeps them off the developer's existing agents-sandbox state.
read_if: Adding or changing the harness smoke tests, the `msb` build tag or `make test-msb`, the harness quit-press
  table, the readiness signals or seeded home files, or when a harness's TUI startup or quit behavior changes.
---

# Harness smoke tests

## Problem

Harness support can break without any test noticing. `opencode2` did: its `DaemonProvider.DaemonHealthCmd`
sent no credentials, v2's `serve` answers every `/api/*` route with `401` when unauthenticated, and
`ensureDaemon` (`internal/sandbox/vm/daemon.go`) polled to its `daemonReadyTimeout` (60s) before failing. The
daemon was healthy; the launcher could not see it, and the user got "… did not become healthy within 60s"
instead of a TUI. See `opencode2-server-auth.md` for the fix.

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
   that keeps the terminal's normal buffer has no such marker and is given the launcher's start-up time instead
   (see Readiness);
2. the process then exited `0` after the harness's quit presses were written to the terminal; escalation
   (`SIGTERM`/`SIGKILL`) is cleanup only and fails the subtest if reached;
3. the output never contains the daemon-timeout marker (`did not become healthy`).

It proves "the harness reached its TUI", not "the client session is functional": a healthy daemon whose client
renders a connection-error screen still passes. Daemon health itself is covered because the launcher blocks in
`ensureDaemon` before any TUI, so a broken daemon fails the TUI-wait or assertion 3.

**Readiness.** The alternate screen is the strong signal: it means the harness's own UI has taken over the terminal.
Two other signals cover the harnesses that never emit `ESC [ ? 1049 h`.

**A ready marker.** A normal-buffer harness whose CLI prints a stable startup line declares it in `ReadyMarker`, and
its subtest ends the readiness wait as soon as that line appears. The match ignores escape sequences, so a
diff-painting TUI (claude-code's Ink) still matches when one interrupts the run of characters being matched. Both
normal-buffer harnesses use one: `hermes` prints `Hermes Agent v<version> (<date>)` (`show_banner`) before it accepts
input, so the spec waits for `Hermes Agent v`, and `claude-code`'s startup splash prints its product name. The markers
are version-independent; the versions themselves are not pinned.

**A wall-clock settle (fallback).** A normal-buffer harness with no marker would wait out the launcher instead; two
measurements shape that wait, kept as the fallback for a future harness whose CLI prints nothing stable.

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
| `claude-code` | `0x03` (Ctrl-C) | repeated presses leave it — its screen announces "Press Ctrl-C again to exit", and three is what a user found; readied on its startup-splash marker |
| `hermes` | `0x04` (Ctrl-D) | its classic CLI's REPL exits on Ctrl-D; readied on its banner marker and seeded (below) so it starts the REPL instead of its first-run setup guard |

**Seeded home files.** A harness whose host-side state decides whether its CLI starts headful declares `SeedHomeFiles`,
a map of home-relative path to content written under the isolated HOME (`<root>/home`) before the run; the launcher
provisions it into the VM the way it provisions a real `~/.hermes/config.yaml`. `hermes` needs it: with no provider its
classic CLI stops at a first-run setup guard that always exits `1` (`_first_run_setup_guard`), so no quit key could
leave it `0`. The seed makes a dummy, loopback provider count as configured (and settles the shared-metrics offer) so
hermes reaches its REPL, where Ctrl-D exits `0`. The seed is a config, not a credential: the endpoint is a closed
loopback port and no request leaves the VM before a message is submitted.

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
configuration or state. Each run creates one temp root, shared by all subtests (which run sequentially), and:

- the root is created under a **short, fixed base** (`/tmp/asb-…`), not the host's `TMPDIR`: microsandbox
  derives its sandbox control-socket path from the private home under `XDG_DATA_HOME` (`<root>/data/agents-sandbox/msb`)
  and rejects a derived path that does not fit a Unix socket's `sun_path` (104 bytes on macOS, the tighter
  target), so a long `TMPDIR` — macOS's default is ~49 bytes — would fail every harness before its TUI with
  "sandbox runtime socket path is too long"; a fast unit test pins the budget (`msb` appends a fixed 50-byte
  suffix and rejects a home above 55 bytes on Linux, microsandbox v0.7.8);
- the child environment is built from an **allowlist**, dropping inherited `HOME`, `XDG_*`,
  `AGENTS_SANDBOX_*`, `OPENCODE_SANDBOX_*`, and `MSB_*`; the inherited variables kept include `PATH`, `TERM`, `LANG`,
  `LC_ALL`, `LC_CTYPE`, and the `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` family. It sets `HOME=<root>/home`,
  `XDG_CONFIG_HOME=<root>/config`, `XDG_DATA_HOME=<root>/data`, `XDG_STATE_HOME=<root>/state`,
  `XDG_CACHE_HOME=<root>/cache`, and `TMPDIR=<root>/tmp`; `TERM` defaults to `xterm-256color` when the
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
TUI window. The shared host docker daemon builds the runner images, as the existing `integration` tests already
do. The suite's own process installs the fail-fast seam mocks in `TestMain`; `TestMain` does nothing else, so
`compile-all`'s `-run=^$` stays a genuine no-op. The suite's phases run in an order that lets a host which
cannot run it skip before paying: the platform/`/dev/kvm` preflight first, then the shared root and binary
build behind a `sync.Once`, then the isolated-env docker preflight, then the per-harness image prebuild
behind its own `sync.Once`, then the harnesses.

**Network.** A harness runs with `--network none` (the secure default) unless the table overrides it, and `claude-code`
does, with `public`: offline it exits `1` (`ECONNREFUSED api.anthropic.com`), because it checks in with its API before
drawing anything. `hermes` runs under the default `none` — its seeded provider endpoint is loopback, so it needs no
egress to reach its REPL. The other four render identically with and without egress.

**Preflight.** Skip, never fail, when the environment cannot run the suite: `/dev/kvm` missing on Linux, a
non-Linux/non-darwin platform, or an unreachable docker daemon. The platform/KVM check runs before anything
is built (no binary, no image), and the docker check runs before the first image build, so an environment
that cannot run the suite never pays for it.

**Teardown.** A clean quit lets the launcher stop its VM. A deferred cleanup best-effort destroys any sandbox
left in the isolated home before the temp root is removed, so a failed subtest cannot leak a VM pointing at a
deleted home; it lists with `sandbox list -q` and removes each name with `sandbox remove --force --yes` (`--yes`
is required, or the non-interactive remove blocks on its confirmation). The temp root's removal is registered as
soon as the root is created, so a skip (the docker preflight) or a failed launcher build removes it too; the
sandbox sweep stays separate because it needs the binary and a reachable docker daemon.

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
- Six harnesses mean six runner-image builds on a cold home; the image prebuild pays for them before any
  harness's TUI window opens, and the host-only placement absorbs the cost.
