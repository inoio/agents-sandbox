---
description: The host-only libvirt KVM guest for running the msb test suites - why the development VM cannot run them, the `scripts/kvm-guest.sh` provisioner and its `make kvm-up`/`kvm-sync`/`kvm-run`/`kvm-down` targets, the nested-KVM prerequisites, and the volatile host-firewall fix guest egress needs.
read_if: Running the msb suites (`make test-msb`, `make test-msb-journeys`) and needing a KVM host; provisioning or destroying the libvirt guest; diagnosing a guest that never gets a DHCP lease, a `virsh start` `Permission denied` on the cache (AppArmor), or no DNS/egress behind Docker's `ip filter` table; or changing `scripts/kvm-guest.sh`.
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

## Workflow

```console
make kvm-up                 # create + boot the guest; cloud-init provisions it on first boot
make kvm-ssh                # a shell in the guest (or: scripts/kvm-guest.sh ssh)
make kvm-sync               # rsync this checkout into ~/agents-sandbox in the guest
make kvm-run                # run KVM_RUN (default: make test-msb) in the guest
make kvm-run KVM_RUN='make test-msb-journeys'
make kvm-down               # destroy the guest and its disk (the base image stays cached)
```

`up` prints the guest's SSH address and a next-steps checklist; cloud-init takes a few minutes on first boot. Once it
is done, gate the guest (`ls -la /dev/kvm`, `msb doctor`) before running a suite. `up` waits up to `KVM_BOOT_TIMEOUT`
seconds (default 300) for a DHCP lease, printing progress, and warns with diagnostics if none arrives. `scripts/kvm-guest.sh render` writes
the cloud-init seed and domain XML under `ARTIFACT_DIR` (default `./.kvm-guest`) without booting, useful to inspect
before `up`. Env overrides (`VM_NAME`, `VM_VCPUS`, `VM_MEMORY_MIB`, `VM_DISK_GIB`, `VM_USER`, `REPO_URL`,
`REPO_BRANCH`, `GO_VERSION`, `SSH_PUBKEY`, `SOURCE_DIR`, `LIBVIRT_URI`, `UBUNTU_CODENAME`, `IMAGE_URL`,
`ARTIFACT_DIR`, `KVM_IMAGE_CACHE`, `KVM_REFRESH_IMAGE`, `KVM_BOOT_TIMEOUT`) are documented in the script header.

The base cloud image is cached in `KVM_IMAGE_CACHE` (default `~/.agents-sandbox-kvm`), **outside** `ARTIFACT_DIR`: only
the first `up` fetches it, `down` leaves it in place, and every worktree of a bare checkout reuses the one copy. It is a
single dir directly under `$HOME`, not `.cache`/`.local` and not nested, because libvirt runs QEMU as its own user
(`libvirt-qemu`), which must traverse **every** path component to read the base image: `~/.cache`/`~/.local` are `0700`
by design, and a nested dir can inherit a `0700` parent under a restrictive umask. `$HOME` itself is normally
world-traversable; the script `chmod`s the cache dir readable and warns if `$HOME` is not. Because an Ubuntu
`current` alias (like a Debian `daily/latest`) keeps a stable filename, the cache cannot notice a newer build on its
own — set `KVM_REFRESH_IMAGE=1` to discard and re-fetch it.

### AppArmor: a base image outside libvirt's allowed paths

On a host with AppArmor for libvirt (the default on Ubuntu/Debian), the per-domain QEMU profile only allows paths
libvirt knows about. A qcow2 **backing file** at `~/.agents-sandbox-kvm` is denied even when it is `0644` and the dir
`0755` — `virsh define` succeeds but `virsh start` fails with `Could not open '…': Permission denied` from
`qemu-system-x86_64`. Confirm with `sudo dmesg | grep -i 'apparmor.*DENIED'` (profile `libvirt-…`). Two fixes:

```console
# Keep the cache under $HOME: allow it in the qemu abstraction, once per host
sudo tee /etc/apparmor.d/local/abstractions/libvirt-qemu >/dev/null <<'EOF'
/home/*/.agents-sandbox-kvm/** r,
EOF
sudo systemctl restart apparmor libvirtd          # virt-aa-helper regenerates the profile

# Or move the cache to the dir libvirt already allows (no AppArmor change)
sudo install -d -o "$USER" -g "$USER" -m 0755 /var/lib/libvirt/images/agents-sandbox
KVM_IMAGE_CACHE=/var/lib/libvirt/images/agents-sandbox make kvm-up
```

## The guest-firewall hazard

Where Docker manages the iptables-nft `ip filter` table (its base chains carry `policy drop`) and libvirt uses its
nftables backend, libvirt's accepts land in a separate table that cannot override the drop: the guest then reaches the
host for ICMP/SSH/DHCP only, not DNS or egress. `up` probes the guest for this and points at the fix; apply it with
`scripts/kvm-guest.sh firewall` (or the printed `nft insert rule` lines). The rules are volatile — a
`virsh net-destroy default`, a Docker restart, or a reboot drops them.

## Host accept

`make kvm-run` executes the suites; a suite's real result is a **human accept** (`verification-before-completion` stops
at the PR), because `/dev/kvm` is unusable in the dev VM and no agent can claim the run. The provisioner and the pure
parts of the script (arg handling, the generated cloud-init seed and domain XML) are covered by `make kvm-guest-test`,
which fakes `virsh`/`ssh`/`rsync` and never touches a hypervisor.
