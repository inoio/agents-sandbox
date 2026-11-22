# One-command KVM-guest agent session — design

> Status: design, awaiting approval. Small change. When built, the durable how-to folds into
> `.operator-shared/guides/kvm-guest.md` and this design is retired.

## Intent and why

`guides/kvm-agent-testing.md` documents a five-step bring-up — `make kvm-up`, wait for cloud-init, `make kvm-sync`,
`make kvm-agent-provision`, `make kvm-agent` — and each step's failure mode (a domain that already exists, cloud-init
still running, a stale checkout) is left to the person to recognise. This change collapses the sequence into one target
so a maintainer goes from nothing to an interactive in-guest agent session in a single command, with the model
credential read from the host environment.

## Goals

- **G1 — one command.** `make kvm-agent-up` takes a guest that is absent, shut off, or already running and ends in an
  attached agent session, without a manual step between.
- **G2 — idempotent ensure.** It creates the guest when undefined, starts it when defined-but-stopped, and leaves a
  running guest alone. It never calls `create()` over an existing domain (`check_host` refuses that by design).
- **G3 — gate on cloud-init.** It runs `cloud-init status --wait` in the guest and fails loudly when cloud-init reports
  an error, rather than syncing into a half-provisioned guest.
- **G4 — credential from the host environment.** `LITELLM_API_KEY` is read from the host environment and handed to the
  in-guest agent; the target fails fast when it is unset.

## Non-goals

- **No new provisioning surface.** No change to what the guest installs or how it boots.
- **No secret mechanism.** The key still rides as an inline `VAR=value` assignment (the acknowledged channel); this
  change does not add the launcher's proxy substitution to the guest.
- **No removal of the lower-level targets.** `kvm-up`, `kvm-sync`, `kvm-agent-provision`, `kvm-agent` and the rest stay
  as they are; `kvm-agent-up` composes them.
- **No GitHub credential.** Unchanged from #823.

## Approaches

1. **A new `agent-up` subcommand in `scripts/kvm-guest.sh`, with a thin `make kvm-agent-up` wrapper — chosen.** The
   orchestration sits where the other subcommands and the fixture harness live; the Make target supplies only the
   credential. Every `kvm-*` target stays a one-line delegation.
2. **Makefile-only composition** (`kvm-agent-up: kvm-cloud-init kvm-sync kvm-agent-provision` plus a `wait-cloud-init`
   subcommand). More visible in the Makefile, but splits one flow across two files and is harder to test end-to-end.
3. **A parallel script.** Rejected: the #823 constraint is to extend `scripts/kvm-guest.sh` and its fixture harness, not
   start a second provisioner.

## The how (as decided)

### `scripts/kvm-guest.sh agent-up [cmd...]`

In order:

1. **Ensure running.** `virsh dominfo` decides existence: undefined → `create` (the full first-boot path); defined →
   `virsh domstate`; `running` → proceed; anything else → `virsh start` then wait for a lease. The lease wait now lives
   in a `wait_for_lease` helper extracted from `create`, used by both.
2. **Wait for cloud-init.** Poll an SSH connection to the guest (sshd accepts well after the DHCP lease appears), then
   run `cloud-init status --wait`; a non-zero cloud-init result is fatal and prints its output.
3. **Sync** the checkout (`sync_in`), so the guest carries the `config provision` command.
4. **Provision** (`agent_provision "${KVM_AGENT_NAME:-opencode2}"`).
5. **Attach** (`agent_in "$@"`) — last, because `agent_in` `exec`s into ssh.

`agent_in` gains a guard: a command containing a single quote dies with a clear message, since the command is embedded
in a single-quoted remote string (the #823 constraint, previously only documented).

### `make kvm-agent-up`

```make
kvm-agent-up:
	@test -n "$$LITELLM_API_KEY" || { printf 'error: set LITELLM_API_KEY\n' >&2; exit 1; }
	scripts/kvm-guest.sh agent-up "LITELLM_API_KEY=$$LITELLM_API_KEY $(KVM_AGENT)"
```

`KVM_AGENT` keeps its default `opencode2`; a caller extends the credential set by overriding it
(`KVM_AGENT='GITHUB_PAT=… opencode2'`).

### Tests, docs, changelog

- Extend `test/scripts/kvm_guest_test.go`: the `virsh` fake gains a `domstate` branch (knob-driven so a case can pin
  running/shut-off/undefined), and the `ssh` fake gains a cloud-init exit knob. Cases: undefined → create;
  running → no create; shut off → start; cloud-init error → die; the single-quote guard.
- Update `.operator-shared/guides/kvm-guest.md` with `kvm-agent-up` and update `guides/kvm-agent-testing.md`'s manual
  path to lead with it (T1–T6 stay as the accept detail).
- Add a `changelog.d/<slug>.added.md` fragment.

## Left to the builder

- The cloud-init poll's timeout and interval defaults (`KVM_CLOUD_INIT_*`).
- Whether a defined-but-stopped guest should probe egress after starting.
