---
description: Contract for guiding a fresh install to a working sandbox — the Get started flow keeps system requirements and the `doctor` verification in Installation and the `doctor` command out of running pages, and a failing prerequisite check prints a copy-pasteable fix plus a link to its matching troubleshooting section.
read_if: Changing the Get started docs, `installation.md`, `tutorial.md`, or the README quick start; changing `internal/sandbox/doctor` output, the prerequisite checks, or the `troubleshooting.html` anchors doctor links to.
---

# First-run guidance

How agents-sandbox routes a new user from install to a working sandbox, and how a failed prerequisite check becomes a fix.

## Doctor remediation contract

The prerequisite checks run automatically as a preflight on `run` and `shell` (`build` checks only the Docker
prerequisite), so starting a sandbox does not require invoking `agents-sandbox doctor` by hand. The `doctor` command is
the standalone diagnostic and the fresh-install verification.

Each failing check reports, in order:

1. a one-line diagnosis that keeps a stable, searchable prefix (`/dev/kvm not found`, `docker API unreachable`,
   `msb runtime setup failed`);
2. the copy-pasteable command that fixes it, where one exists — platform-specific through the `doctor_linux.go` /
   `doctor_darwin.go` build-tagged pair (`dockerRemedyHint`, `kvmUnavailableError`, `appleSiliconRequiredError`);
3. a `Docs:` line linking `https://inoio.github.io/agents-sandbox/troubleshooting.html#<anchor>` for what a command
   cannot fix.

`troubleshootingURL` builds the link. The anchors are the troubleshooting headings, and the coupling is silent:
renaming a heading breaks the doctor link with no test failing. Current pairs: `kvm-unavailable`, `docker-not-running`,
`microsandbox-runtime`, `not-apple-silicon`.

## Get-started flow

There is no separate prerequisites page. `docs/install/installation.md` is the only home for:

- **System requirements** — the supported platforms (Linux with KVM; macOS on Apple Silicon) and the Docker
  requirement, stated before the install methods.
- **Verify your setup** — the `agents-sandbox doctor` run, after the install methods.

Installation ends with a fork to the two onboarding paths: switching an existing agent (`switch.md`) or a first sandbox
(`tutorial.md`).

Pages about running agents-sandbox — the tutorial and the README quick start — do not tell the reader to run `doctor`,
because the preflight already runs it. Only the Installation verification step and the troubleshooting page name it by
command.

`docs/troubleshooting.md` owns the fix prose; the doctor messages stay terse and defer to it.
