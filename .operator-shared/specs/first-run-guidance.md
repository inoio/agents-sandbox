---
description: Contract for guiding a fresh install to a working sandbox — the Get started flow keeps the prerequisite list and the `doctor` verification in one Installation section and the `doctor` command out of running pages, and a failing prerequisite check prints a copy-pasteable fix plus a link to its matching troubleshooting section.
read_if: Changing the Get started docs, `installation.md`, `tutorial.md`, or the README quick start; changing `internal/sandbox/doctor` output, the prerequisite checks, or the `troubleshooting.html` anchors doctor links to.
---

# First-run guidance

How agents-sandbox routes a new user from install to a working sandbox, and how a failed prerequisite check becomes a fix.

## Doctor remediation contract

The prerequisite checks run automatically as a preflight on `run` and `shell` (`build` checks only the Docker
prerequisite), so starting a sandbox does not require invoking `agents-sandbox doctor` by hand. The `doctor` command is
the standalone diagnostic and the fresh-install verification; it deliberately does not go through that runtime preflight.
`doctor` evaluates the runtime as one of its own checks, so a failed runtime install is reported as `msb runtime setup
failed` with its troubleshooting link alongside the KVM and Docker results rather than aborting before they run (#423).

Each failing check reports, in order:

1. a one-line diagnosis that keeps a stable, searchable prefix (`/dev/kvm not found`, `docker API unreachable`,
   `msb runtime setup failed`);
2. the copy-pasteable command that fixes it, where one exists — platform-specific through the `doctor_linux.go` /
   `doctor_darwin.go` build-tagged pair (`dockerRemedyHint`, `kvmUnavailableError`, `appleSiliconRequiredError`);
3. a `Docs:` line linking `https://inoio.github.io/agents-sandbox/troubleshooting.html#<anchor>` for what a command
   cannot fix.

`docslink.SectionURL("troubleshooting", …)` builds the link. The anchors are the troubleshooting headings, and
`ci/check-docs.sh` fails when a printed fragment matches no heading (see `docs-link-integrity.md`). Current pairs:
`kvm-unavailable`, `docker-not-running`, `microsandbox-runtime`, `not-apple-silicon`.

## Get-started flow

There is no separate prerequisites page. `docs/install/installation.md` is the only home for:

- **Prerequisites** (before the install methods) — leads with the reassurance that agents-sandbox verifies the host
  automatically before a sandbox starts, then the supported platforms (Linux with KVM; macOS on Apple Silicon) and the
  container-engine requirement, and names the standalone `agents-sandbox doctor` run as an optional check available once
  installed.

Installation ends with a fork to the two onboarding paths: switching an existing agent (`switch.md`) or a first sandbox
(`tutorial.md`).

Pages about running agents-sandbox — the tutorial and the README quick start — do not tell the reader to run `doctor`,
because the preflight already runs it. Only the Installation prerequisites section and the troubleshooting page name it
by command.

`docs/troubleshooting.md` owns the fix prose; the doctor messages stay terse and defer to it. The Docker prerequisite is
stated as any container engine that serves the Docker API, in three tiers: the tested engines named (Docker Engine, Docker
Desktop, colima, podman), other engines as an open, untested list, and engines with no Docker API as unsupported. An
engine that refuses an unqualified `FROM` — podman without search registries — is documented as its own build-failure
subsection with the engine-side remedies.
