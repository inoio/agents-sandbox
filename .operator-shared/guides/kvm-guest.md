---
description: The host-only libvirt KVM guest for running the msb test suites and an agent that works on this repo - why the development VM cannot run them, the `scripts/kvm-guest.sh` provisioner and its `make kvm-up`/`kvm-sync`/`kvm-prime`/`kvm-run`/`kvm-agent`/`kvm-agent-provision`/`kvm-down` targets, the host git remote the guest pulls from and pushes to over a `git-shell`-restricted key, the disk-backed reuse root the guest pre-creates, the nested-KVM prerequisites, and the volatile host-firewall fix guest egress needs.
read_if: Running the msb suites (`make test-msb`, `make test-msb-journeys`) and needing a KVM host; running an agent (opencode2) on the guest so it can build the launcher and drive real VMs (`make kvm-agent`, `make kvm-agent-provision`); provisioning or destroying the libvirt guest; priming the guest's runner-image/msb cache from the host (`make kvm-prime`) or diagnosing a `prime` that copies nothing; diagnosing a guest that never gets a DHCP lease, a `virsh start` `Permission denied` on the cache (AppArmor), or no DNS/egress behind Docker's `ip filter` table; the guest's host git remote (`make kvm-sync`, guest `git push`, the `git-shell`-restricted key) or a sync that refuses; or changing `scripts/kvm-guest.sh`.
---

# The KVM guest for the msb suites

The `msb`-tagged suites start real microsandbox VMs, so they need KVM. The agents-sandbox **development VM is itself a
microsandbox sandbox** and has no working `/dev/kvm`, so the suites cannot run inside it; they run on a KVM host.
`scripts/kvm-guest.sh` builds one: a libvirt guest whose `/dev/kvm` is the nested one, where the launcher runs as a
host session and the microsandbox VMs it creates are nested one level below.

The guest is **not** an agents-sandbox sandbox. Provisioning installs docker, Go, `msb`, and a clone of the repo into
the guest; the suites then build the launcher there and drive it directly.

## Host prerequisites

- `qemu-kvm libvirt-daemon-system cloud-image-utils genisoimage` on the host, with the user in the `libvirt` and `kvm`
  groups.
- Nested virtualization enabled (for example `options kvm-amd nested=1` and a reboot). `up` checks this and refuses to
  build a guest that would not have KVM.
- The libvirt `default` network.
- An `sshd` the guest can reach at the bridge gateway, and `git-shell` on the host (part of `git`). The guest's git
  remote is the host, reached over SSH, so `make kvm-sync` fails with a diagnostic when either is missing.

## Workflow

```console
make kvm-up                 # create + boot the guest; cloud-init provisions it on first boot
make kvm-ssh                # a shell in the guest (or: scripts/kvm-guest.sh ssh)
make kvm-sync               # point the guest's git at the host and pull its branch
make kvm-prime              # copy the host's built runner images + msb home into the guest
make kvm-run                # run KVM_RUN (default: make test-msb-in-vm) in the guest
make kvm-run KVM_RUN='make test-msb'          # the host variant, unchanged
make kvm-run KVM_RUN='make test-msb-journeys'
make kvm-down               # destroy the guest and its disk (the base image stays cached)
make kvm-agent-up           # one command: ensure up + cloud-init + pull + provision + attach (gh sign-in from GITHUB_PAT)
```

`make kvm-run` defaults to `make test-msb-in-vm`, the in-guest variant of `make test-msb`: the same caching
suite, with changes that only matter on a nested guest. The two `msb`-tagged packages run serialized
(`go test -p 1`), because each starts its own VMs and a nested guest cannot absorb both at once. Each smoke
VM's vCPUs are capped (`MSB_GUEST_VM_CPUS`, default 2, passed as `AGENTS_SANDBOX_MSB_VM_CPUS`), because a
project VM otherwise claims every guest vCPU. The parallel-harness count defaults to
`MSB_GUEST_PARALLEL` (2) when `MSB_PARALLEL` is unset, instead of the host-sized memory heuristic. And the
readiness/quit ceilings scale (`MSB_GUEST_TIMEOUT_FACTOR`, default 3, passed as
`AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR`), while the launcher's own VM-setup readiness ceilings are raised
(`MSB_GUEST_DOCKER_READY_TIMEOUT`/`MSB_GUEST_DAEMON_READY_TIMEOUT`, default 90s / 180s, forwarded to the
launcher's `AGENTS_SANDBOX_DOCKER_READY_TIMEOUT`/`AGENTS_SANDBOX_DAEMON_READY_TIMEOUT`), because a nested
guest starts a VM and its services slower than the fixed host budgets allow. The ceilings absorb CPU
starvation from any source: the guest shares the host's cores, so a busy host — or a concurrent `run`/`shell`
on the guest that is not vCPU-capped with `--cpus` — can make a harness miss its ceiling.
`make test-msb` and `make test-msb-cold` are unchanged.

`up` prints the guest's SSH address and a next-steps checklist; cloud-init takes a few minutes on first boot. Once it
is done, gate the guest (`ls -la /dev/kvm`, `msb doctor`) before running a suite. `up` waits up to `KVM_BOOT_TIMEOUT`
seconds (default 300) for a DHCP lease, printing progress, and warns with diagnostics if none arrives. `scripts/kvm-guest.sh render` writes
the cloud-init seed and domain XML under `ARTIFACT_DIR` (default `./.kvm-guest`) without booting, useful to inspect
before `up`. Env overrides (`VM_NAME`, `VM_VCPUS`, `VM_MEMORY_MIB`, `VM_DISK_GIB`, `VM_USER`, `REPO_URL`,
`REPO_BRANCH`, `GO_VERSION`, `SSH_PUBKEY`, `KVM_HOST_USER`, `KVM_GUEST_STATE`, `LIBVIRT_URI`, `UBUNTU_CODENAME`,
`IMAGE_URL`, `ARTIFACT_DIR`, `KVM_IMAGE_CACHE`, `KVM_REFRESH_IMAGE`, `KVM_BOOT_TIMEOUT`) are documented in the script
header.

cloud-init also creates `/opt/asb-reuse`, owned by the guest user, for the msb suites' standing reuse root: `make
test-msb` caches 10+ GiB of runner images under `MSB_REUSE_ROOT` (default `/opt/asb-reuse`), while the guest's `/tmp`
is a RAM-backed tmpfs that would exhaust at the largest runner image. See
`.operator-shared/specs/testing/harness-smoke-tests.md`.

### Priming the guest from the host

The guest builds its runner images and loads them into microsandbox itself; on a cold guest that is the suite's
dominant cost (the `docker` build plus the multi-GB `docker save | msb ImageLoad` transfer). `make kvm-prime` copies
both halves from the host so the guest's prebuild finds them and skips it: the runner images over
`docker save | ssh docker load`, and the microsandbox home (`<root>/smoke/data/agents-sandbox/msb`) over `rsync`.
It prints the image list before starting and one `Loaded image: <ref>` line per image as each lands (the stream is
one save/load, not a save/load per image, so a shared layer is sent once), and copies the home with
`rsync --info=progress2`, so neither transfer is a silent wait.

It only matches when host and guest agree on `MSB_REUSE_ROOT` (`KVM_PRIME_ROOT`, default `/opt/asb-reuse`): the
runner-image slug hashes the project directory's absolute path, so a different root names different images and
nothing is reused. Host and guest must also share the CPU architecture and the microsandbox runtime version. Build
the host's cache first (`make test-msb` on the host), and prime before a run, never while a run is using the home.

The base cloud image is cached in `KVM_IMAGE_CACHE` (default
`/var/lib/libvirt/images/agents-sandbox`), **outside** `ARTIFACT_DIR`: only the first `up` fetches it, `down` leaves it
in place, and every worktree of a bare checkout reuses the one copy. The default is libvirt's own image dir because
libvirt runs QEMU as its own user (`libvirt-qemu`) and, on Debian/Ubuntu, confines it with AppArmor to paths libvirt
knows about — a qcow2 backing file under `$HOME` is denied even at `0644` in a `0755` dir (see below). Create the dir
once, owned by you so the fetch can write:

```console
sudo install -d -o "$USER" -g "$USER" -m 0755 /var/lib/libvirt/images/agents-sandbox
```

Because an Ubuntu `current` alias (like a Debian `daily/latest`) keeps a stable filename, the cache cannot notice a
newer build on its own — set `KVM_REFRESH_IMAGE=1` to discard and re-fetch it.

### AppArmor: a base image outside libvirt's allowed paths

On a host with AppArmor for libvirt (the default on Ubuntu/Debian), the per-domain QEMU profile only allows paths
libvirt knows about. A qcow2 **backing file** at `~/.agents-sandbox-kvm` (or anywhere outside libvirt's dirs) is denied
even when it is `0644` and the dir `0755` — `virsh define` succeeds but `virsh start` fails with
`Could not open '…': Permission denied` from `qemu-system-x86_64`. Confirm with
`sudo dmesg | grep -i 'apparmor.*DENIED'` (profile `libvirt-…`). The default avoids this by living in
`/var/lib/libvirt/images`; the alternative is to allow a `$HOME` cache in the qemu abstraction, once per host:

```console
sudo tee /etc/apparmor.d/local/abstractions/libvirt-qemu >/dev/null <<'EOF'
/home/*/.agents-sandbox-kvm/** r,
EOF
sudo systemctl restart apparmor libvirtd          # virt-aa-helper regenerates the profile
KVM_IMAGE_CACHE=~/.agents-sandbox-kvm make kvm-up
```

## The guest-firewall hazard

Where Docker manages the iptables-nft `ip filter` table (its base chains carry `policy drop`) and libvirt uses its
nftables backend, libvirt's accepts land in a separate table that cannot override the drop: the guest then reaches the
host for ICMP/SSH/DHCP only, not DNS or egress. `up` probes the guest for this and points at the fix; apply it with
`scripts/kvm-guest.sh firewall` (or the printed `nft insert rule` lines). It accepts the bridge in **INPUT** (host-bound
traffic) and in **DOCKER-USER** (forwarded traffic) — Docker rebuilds `FORWARD` on every start but preserves
`DOCKER-USER`. Docker preserves `DOCKER-USER` across daemon restarts; a reboot drops the rules unless your base
ruleset re-applies them. Do not reload a `flush ruleset` base config on a running host: it also wipes Docker's and
libvirt's tables, so a live apply needs `systemctl restart docker` then `systemctl restart libvirtd` then
`virsh net-start default`.

## Running an agent in the guest

The guest can do work the development VM structurally cannot: its `/dev/kvm` is real, so it can build the launcher and
run the `msb` suites and drive real microsandbox VMs. To let an **agent** do that work (rather than a person typing
commands over `make kvm-ssh`), the guest is provisioned with an agent to run: opencode2 (linked into `~/.local/bin`,
which the profile already puts on `PATH`), plus `tmux` for a durable session, the GitHub CLI `gh` (pinned to the same
version as the dev image), and the repo's `shellcheck` / `Makefile`-pinned `golangci-lint` so the agent can run
`make check`.

```console
make kvm-sync              # pull the checkout from the host, so the guest has the `config provision` command
make kvm-agent-provision   # materialize the agent config into the guest home
make kvm-agent             # attach the agent session
```

The one-command path is `make kvm-agent-up`: it ensures the guest is running (idempotent — a created, stopped, or
running guest all work), waits for cloud-init, pulls the host branch into the guest, materializes the config, and
attaches, reading `LITELLM_API_KEY` from the host environment (extend the credential set with `KVM_AGENT`). It fails
fast when the key is unset. When the agent's tmux session is already running, it **reattaches without syncing** — the
pull would check out the host branch and disturb the live session — so re-running it to recover a dropped connection
cannot overwrite the live agent's work. The three commands above stay for manual control.

`make kvm-agent-up` also signs the guest's `gh` in to GitHub when `GITHUB_PAT` is set in the host environment: it runs
`gh auth login --with-token` over ssh **stdin** (the token never reaches a command line), then `gh auth setup-git`. The
agent can then read and comment on issues and PRs; unset `GITHUB_PAT` to skip it. The guest's git remote is the host,
not GitHub, so the guest does not push to GitHub and no GitHub `insteadOf` rewrite is set up — `gh`'s remote-based repo
inference sees the host, so pass `--repo <owner/repo>` or set `GH_REPO`. A guest booted before `gh` was installed fails
the sign-in loudly — rebuild it with `make kvm-down && make kvm-up`.

`make kvm-agent-provision` runs the launcher's own `config provision` **in the guest** — so it writes exactly the
config a sandbox would receive (merged snippets, verbatim mirror, host drop-in, home files), reusing the launcher's
pipeline rather than re-implementing it — and first rsyncs the host's `~/.config/agents-sandbox/` so the guest resolves
the same config scopes. It runs in the guest because the Go toolchain lives there, not on the KVM host. The repository's
own agent config (`opencode.jsonc` → `AGENTS.md`, `.agents/skills/`, `.operator-shared/`) is picked up from the working
directory, not provisioned. It also gives the guest the host's git identity — only `user.name` and `user.email`, not the
rest of the host's `~/.gitconfig` (credential helpers, aliases, signing config) — so the agent commits as the host user;
a host with neither key keeps the guest's own identity. Because this rewrites the guest's identity file, running
`make kvm-agent-provision` **after** a `kvm-agent-up` sign-in drops gh's git configuration (its credential helper);
re-run `make kvm-agent-up` to restore it.

`make kvm-agent` allocates a TTY and runs the agent inside a named `tmux` session (`KVM_AGENT_SESSION`, default
`agents-sandbox`), so a dropped SSH connection leaves the agent running and re-running the command reattaches. The
default command is `opencode2`; pass another, and credentials as an inline assignment, with `KVM_AGENT`:

```console
make kvm-agent KVM_AGENT='LITELLM_API_KEY=… opencode'
```

**Model credentials are not provisioned** — the guest's opencode2 is not behind the launcher's secret proxy, so an
inline `VAR=value` (or an interactive login in the guest) is the channel; treat the value as visible in the host's
process list and shell history. The GitHub token is the exception: `make kvm-agent-up` signs `gh` in from the host's
`GITHUB_PAT` over ssh stdin, so it never reaches a command line. The `KVM_AGENT` command is embedded in a nested shell
string, so it must not contain a single quote, double quote, backslash, dollar sign, or backtick.

The guest checkout is the agent's own: it commits and creates worktrees there.

### Git transport

The guest's git remote is the **host repository**, not GitHub. The host generates one ed25519 key
(`KVM_GUEST_STATE/id_kvm_guest`, default under `$XDG_STATE_HOME/agents-sandbox/kvm-guest` — host-global, so a guest
rebuild reuses it instead of accumulating `authorized_keys` lines), authorizes its public half in the host's
`~/.ssh/authorized_keys` restricted to `git-shell`, and installs the private half in the guest. A
`url."ssh://<host-user>@<gateway><repo-path>".insteadOf <repo-url>` rewrite then routes the guest's single `origin`
remote — the clone the boot made — to the host, reached at the bridge gateway over the guest→host SSH. `make kvm-sync`
applies that setup and pulls: `git fetch origin --prune`, switch to the host branch, `git merge --ff-only`. Uncommitted
host work is not copied; a diverged guest branch refuses rather than being reset.

The agent pushes its branches straight to the host with plain `git push`, so the host's worktrees see them without a
fetch. A branch checked out in a host worktree is refused (`branch is currently checked out`), so the host's current
branches stay protected and integration stays a host-side merge. `make kvm-sync-back` remains the host-initiated
fallback: by default it fetches every guest branch into the `refs/remotes/kvm-guest/*` namespace, so each is reachable
as `kvm-guest/<name>` without touching a local branch; `BRANCH=<name>` instead fetches one branch into the local branch
of the same name (fast-forward only), and a branch checked out here refuses the fetch — update it by merging the
namespace ref (`git merge kvm-guest/<name>`).

**Known limitation:** the `git-shell` key confines the *command*, not the *path* — a git-shell session reaches any
repository the host user can read, so treat the guest as sharing the host user's git trust.

`guides/kvm-agent-testing.md` is the manual host-accept procedure for this workflow (T1–T6), since it needs a real
hypervisor and cannot run in the development VM.

## Host accept

`make kvm-run` executes the suites; a suite's real result is a **human accept** (`verification-before-completion` stops
at the PR), because `/dev/kvm` is unusable in the dev VM and no agent can claim the run. The provisioner and the pure
parts of the script (arg handling, the generated cloud-init seed and domain XML) are covered by `make kvm-guest-test`,
which fakes `virsh`/`ssh`/`rsync` and never touches a hypervisor.
