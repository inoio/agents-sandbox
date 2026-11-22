---
description: Manual test procedure (host accept) for the KVM-guest agent environment - the ordered checks that `make kvm-agent`, `make kvm-agent-provision`, and the `agents-sandbox config provision` command they drive behave on a real KVM host, where the agent runs directly on the guest.
read_if: Accepting the KVM-guest agent work on a host (`make kvm-agent`/`kvm-agent-provision`); verifying an agent runs on the guest, gets the launcher's provisioned config, and can build the launcher and run the `msb` suites; or debugging a guest where opencode2, tmux, or the toolchain is missing.
---

# Testing the KVM-guest agent environment

This is the **host accept** for running an agent (opencode2) directly on the KVM guest — the machine whose `/dev/kvm`,
docker, and `msb` work, unlike the development VM. Automated coverage stops at the script's faked `ssh`/`rsync`/`virsh`
seams and the launcher's unit tests; the rest needs a real hypervisor, so it is run by a person.

Run the `make` commands from a checkout of the branch on the **KVM host**, not in the development VM. The workflow and
its rationale are in `guides/kvm-guest.md`.

`make kvm-agent-up` is the one-command path: it ensures the guest is running, waits for cloud-init, syncs, provisions,
and attaches, reading `LITELLM_API_KEY` from the environment. The T1–T6 steps below stay the per-step detail for
diagnosing that flow.

## Automated preflight (development VM; no KVM)

Record these as the non-manual half of the accept:

```console
make check-local-changes   # test, lint, validate-fmt, compile-all, shellcheck, docs-linkcheck: all PASS
make kvm-guest-test        # ok  github.com/inoio/agents-sandbox/test/scripts
make brain-check           # brain-check: ok
go run ./cmd/agents-sandbox config provision --agent opencode2 --target /tmp/asb-provision-test
                           # exit 0; writes nothing in a bare checkout (project config untrusted,
                           # and no user launcher config) — the guest supplies the latter
```

## Host prerequisites

- `qemu-kvm libvirt-daemon-system cloud-image-utils genisoimage`; your user in `libvirt` and `kvm`.
- Nested virtualization enabled (`options kvm-amd nested=1` + reboot).
- The libvirt `default` network, and the image cache dir:
  `sudo install -d -o "$USER" -g "$USER" -m 0755 /var/lib/libvirt/images/agents-sandbox`.
- A host launcher config carrying the agent's providers/models. **Highest-value case:** a populated
  `~/.config/agents-sandbox/opencode2/` (what the launcher merges). Without it, T2 writes nothing and T3 has no
  provider to call.

## T1 — the guest is provisioned for an agent

```console
make kvm-up        # first boot runs cloud-init for a few minutes
make kvm-ssh
```

In the guest, every line should succeed:

```console
ls -la /dev/kvm          # nested KVM is present
msb doctor               # microsandbox runtime works
go version               # /usr/local/go/bin on PATH
opencode2 --version      # the agent (also reachable as `opencode`)
tmux -V
gh --version             # the GitHub CLI, signed in by `kvm-agent-up` from GITHUB_PAT
shellcheck --version
golangci-lint version    # matches the Makefile pin (2.15.0)
exit
```

If `msb doctor` reports `KVM access: permission denied` — "you are in the 'kvm' group, but this login
session predates the change" — that is expected on the first login after `up`: cloud-init's
`usermod -aG docker,kvm` ran after your session opened. Reconnect (`exit`, then `make kvm-ssh`) so a new login
applies the groups; `msb doctor --fix` (or `sudo setfacl -m u:dev:rw /dev/kvm`) fixes the current session.

If a tool is missing, check which seed the guest actually booted before debugging the tool:

```console
grep -c 'opencode.ai/v2/install' /var/lib/cloud/instance/user-data.txt
```

`0` means `make kvm-up` ran from a checkout without this branch's provisioning; `1` but tools still missing means a
`runcmd` line failed — inspect `cloud-init status --long` and `sudo tail -80 /var/log/cloud-init-output.log`.

Failures: `opencode2: command not found` means the installer failed or `~/.local/bin` is not on `PATH` (a fresh SSH
login sources `/etc/profile.d/zz-asb-kvm.sh`; check `echo $PATH`). Missing `golangci-lint` means the pin was not read
from the Makefile at `up` time. Cloud-init is one-shot: after changing provisioning, `make kvm-down && make kvm-up`.

## T2 — `make kvm-agent-provision` materializes the agent config

```console
make kvm-sync              # the checkout, so the guest has the `config provision` command
make kvm-agent-provision
```

Expect, in order: `==> syncing …/.config/agents-sandbox → dev@<ip>` (or a "no … on this host" line when you have no
user launcher config), then `==> materializing the opencode2 config into the guest home`, then the launcher's own
output ending in `Provisioned config for agent opencode2 into /home/dev.`

```console
make kvm-ssh
find ~/.config/opencode -type f | sort      # provisioned files: opencode.jsonc, mirror files, drop-ins
```

Two behaviors that are correct, not bugs:

- The repository's **project** config (`.agents-sandbox/`) is ignored unless it was approved on the guest — the
  project-config trust gate applies here exactly as in a run.
- **Credentials are not written.** opencode2 keeps them in its own store and the guest has no launcher secret proxy, so
  T3 supplies them by environment.

Provisioning also gives the guest the host's git identity — only `user.name` and `user.email`, not the rest of the
host's `~/.gitconfig` (credential helpers, aliases, signing config) — so the agent can commit as the host user. A host
with neither key keeps the guest's own identity.

Negative: with no `~/.config/agents-sandbox/` on the host, expect the informative log line, no rsync, and the
materialization still running against the guest's project scope.

## T3 — start an interactive agent session

```console
make kvm-agent KVM_AGENT='LITELLM_API_KEY=<key> opencode'
```

Expect the opencode2 TUI in the guest, in `~/agents-sandbox`. Ask it something cheap ("summarise AGENTS.md") and
confirm it answers — that proves the credentials reached the process.

- `Ctrl-b d` detaches the tmux session without killing the agent. Then `make kvm-agent` **re-attaches** to the same
  session; it must not start a second agent.
- From a second guest shell (`make kvm-ssh`), `tmux ls` shows the `agents-sandbox` session while detached.
- Quoting: the command is embedded in a single-quoted remote command, so `KVM_AGENT` must not contain a single quote.
  A value with spaces is fine (`KVM_AGENT='A=1 B=2 opencode'`). The auth value is visible in the host's process list
  and shell history — expected for this channel, not a leak to fix here.
- `make kvm-agent KVM_AGENT=definitely-not-a-command` should fail loudly in the session, not silently.
- `GITHUB_PAT` in the host environment makes `kvm-agent-up` sign `gh` in the guest; verify in the session with
  `gh auth status` and `gh issue list`. A guest built before `gh` was installed fails the sign-in loudly (rebuild it).

## T4 — the agent does the work the development VM cannot

In the session, in `~/agents-sandbox`:

```console
make build
make test
make test-msb      # real nested microsandbox VMs — the point of the guest
```

Expect `make test-msb` to start VMs nested one level below the guest and report results. Record the observed outcome.
The first run builds runner images; `make kvm-prime` from the host can pre-seed them.

## T5 — work returns to the host

Commit on a branch in the guest (the agent's worktree flow per `AGENTS.md`), then from the host:

```console
make kvm-sync-back                 # every guest branch -> kvm-guest/<name>
make kvm-sync-back BRANCH=<branch> # one branch -> local <branch>
git log --oneline kvm-guest/<branch> -1   # the guest's commit
```

Expect each guest branch under `kvm-guest/<name>` after the default run, and the local `<branch>` created or
fast-forwarded by the `BRANCH=` form.

There is no push unless `make kvm-agent-up` signed `gh` in with a `GITHUB_PAT` (then a push over the HTTPS remote and
`gh pr create` work); without a PAT the guest does not push, and `kvm-sync-back` is the return path either way. Confirm
the hazard is real: `make kvm-sync` is one-way and `--delete`s, so never run it over a live agent session.

## T6 — re-provision after a rebuild

```console
make kvm-down && make kvm-up && make kvm-sync && make kvm-agent-provision && make kvm-agent
```

The guest is disposable, so `agent-provision` must be re-runnable and the agent session must re-establish from scratch.

## Record for the accept

- T1: the guest tool versions.
- T2: the provisioned file list (`find ~/.config/opencode`).
- T3: one model turn answered; detach/reattach confirmed.
- T4: the `make test-msb` outcome.
- T5: the commit fetched to the host.
- Any deviation, with the command and its output.

## Known limitations (not failures)

- Credentials are inline environment assignments, not a secret mechanism; they are visible in the host process
  list/history.
- Materialization runs in the guest, so the host's `~/.config/agents-sandbox/` (which may hold `env.secret`) is rsynced
  into the guest.
- The guest-specific instruction overlay is not built yet: the agent reads the repository's `AGENTS.md`, which describes
  a sandbox that is not there — prompt it accordingly, or track that as follow-up.
