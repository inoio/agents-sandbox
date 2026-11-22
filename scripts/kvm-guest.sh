#!/usr/bin/env bash
#
# kvm-guest.sh — host-side provisioner for the `msb`-tagged test suites.
#
# Run this on a KVM host, not inside the agents-sandbox development VM: the dev
# VM has no working /dev/kvm, so the msb suites must run one level above it.
# The guest this builds *is* the KVM host for those suites: the launcher runs
# directly in it as a host session, and the microsandbox VMs it creates are
# nested. The guest is deliberately NOT an agents-sandbox sandbox.
#
# Guest: Ubuntu cloud image (resolute), seeded by cloud-init. The spike and the
# first successful host boot used it; the Debian forky daily did not reach DHCP
# here. Override with IMAGE_URL for another image (e.g. a Debian cloud image),
# or UBUNTU_CODENAME to pick a different Ubuntu release. The domain is defined with
# `virsh define` (no virt-install): virt-manager's Python bindings are a common
# host breakage and nothing here needs them.
#
# Usage (on the host):
#   ./scripts/kvm-guest.sh up        # create + boot the guest
#   ./scripts/kvm-guest.sh render    # write the cloud-init seed + domain XML, no boot
#   ./scripts/kvm-guest.sh ssh       # ssh into the guest
#   ./scripts/kvm-guest.sh sync      # rsync the current checkout into the guest
#   ./scripts/kvm-guest.sh prime     # copy the host's runner images + msb home into the guest
#   ./scripts/kvm-guest.sh run make test-msb-journeys   # run a command in the guest
#   ./scripts/kvm-guest.sh agent            # interactive agent session in the guest
#   ./scripts/kvm-guest.sh agent-provision  # materialize the agent config into the guest
#   ./scripts/kvm-guest.sh sync-back [branch]  # fetch the guest's branches back to the host
#   ./scripts/kvm-guest.sh firewall  # re-apply the host nft rules guest egress needs
#   ./scripts/kvm-guest.sh down      # destroy the guest and its disk
#
# The agent subcommands run a coding agent (opencode2) *in the guest*, where
# /dev/kvm, docker and msb work, so it can build the launcher and run the msb
# suites. `agent` attaches an interactive tmux session; `agent-provision` syncs
# the host's launcher config and materializes the agent's provisioned config into
# the guest home. Credentials are not provisioned: pass them as env assignments on
# the agent command, e.g.
#   make kvm-agent KVM_AGENT='LITELLM_API_KEY=… opencode'
#
# Host prerequisites:
#   qemu-kvm libvirt-daemon-system cloud-image-utils genisoimage
#   your user in the `libvirt` and `kvm` groups
#   nested virtualization enabled (e.g. options kvm-amd nested=1 + reboot)
#
# Env overrides:
#   VM_NAME=asb-kvm  VM_VCPUS=12  VM_MEMORY_MIB=16384  VM_DISK_GIB=80
#   VM_USER=dev  SSH_PUBKEY=<path-or-key>  ARTIFACT_DIR=$PWD/.kvm-guest
#   SOURCE_DIR=<checkout to sync>  LIBVIRT_URI=qemu:///system
#   UBUNTU_CODENAME=resolute  IMAGE_URL=<full cloud-image URL>
#   KVM_IMAGE_CACHE=/var/lib/libvirt/images/agents-sandbox  KVM_REFRESH_IMAGE=1  KVM_BOOT_TIMEOUT=300
#   REPO_URL=https://github.com/inoio/agents-sandbox.git  REPO_BRANCH=<current branch>
#   GO_VERSION=<from .go-version>  GOLANGCI_LINT_VERSION=<from the Makefile>
#   OPENCODE2_VERSION=<the opencode2 installer's --version; unset installs latest>
#   KVM_AGENT_SESSION=agents-sandbox  # the guest tmux session the agent runs in
#   KVM_PRIME_ROOT=/opt/asb-reuse  # the root `prime` copies from and to
#
# The base image is cached in KVM_IMAGE_CACHE (shared across worktrees and
# untouched by `down`), so only the first `up` fetches it. Set
# KVM_REFRESH_IMAGE=1 to re-fetch when the URL is a moving alias (a Debian
# `daily/latest`, or an Ubuntu `current`, does not change filename). The default
# is libvirt's own image dir: libvirt runs QEMU as its own user (e.g.
# libvirt-qemu) and, on Debian/Ubuntu, confines it with AppArmor to paths
# libvirt knows about, so a qcow2 backing file under $HOME is denied even at
# 0644 in a 0755 dir. /var/lib/libvirt/images is allowed, and its ancestors are
# traversable. Create it once, owned by you so the fetch can write:
#   sudo install -d -o "$USER" -g "$USER" -m 0755 "$KVM_IMAGE_CACHE"
#
# Firewall: where Docker manages the iptables-nft `ip filter` table (its base
# chains carry `policy drop`) and libvirt uses its nftables backend, libvirt's
# accepts land in a separate table that cannot override that drop — the guest
# then reaches the host for ICMP/SSH/DHCP only, not DNS or egress. `firewall`
# applies the name-keyed accepts that fix it (INPUT for host-bound traffic, and
# DOCKER-USER for forwarded traffic — Docker rebuilds FORWARD but preserves
# DOCKER-USER); `up` probes the guest and points at them when it can.
set -euo pipefail

VM_NAME="${VM_NAME:-asb-kvm}"
VM_VCPUS="${VM_VCPUS:-12}"
VM_MEMORY_MIB="${VM_MEMORY_MIB:-16384}"
VM_DISK_GIB="${VM_DISK_GIB:-80}"
VM_USER="${VM_USER:-dev}"
LIBVIRT_URI="${LIBVIRT_URI:-qemu:///system}"
REPO_URL="${REPO_URL:-https://github.com/inoio/agents-sandbox.git}"
ARTIFACT_DIR="${ARTIFACT_DIR:-$PWD/.kvm-guest}"
KVM_IMAGE_CACHE="${KVM_IMAGE_CACHE:-/var/lib/libvirt/images/agents-sandbox}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"

if [ -z "${REPO_BRANCH:-}" ]; then
  REPO_BRANCH="$(git -C "$repo_root" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  [ -n "$REPO_BRANCH" ] && [ "$REPO_BRANCH" != "HEAD" ] || REPO_BRANCH="main"
fi

if [ -z "${GO_VERSION:-}" ]; then
  GO_VERSION="$(tr -d '[:space:]' < "$repo_root/.go-version" 2>/dev/null || true)"
  [ -n "$GO_VERSION" ] || GO_VERSION="$(sed -n 's/^go \([0-9][0-9.]*\).*/\1/p' "$repo_root/go.mod" 2>/dev/null | head -1 || true)"
  [ -n "$GO_VERSION" ] || GO_VERSION="1.27.4"
fi

if [ -z "${GOLANGCI_LINT_VERSION:-}" ]; then
  GOLANGCI_LINT_VERSION="$(sed -n 's/^GOLANGCI_LINT_VERSION[[:space:]]*=[[:space:]]*//p' "$repo_root/Makefile" 2>/dev/null | head -1 || true)"
fi

case "$(uname -m)" in
  x86_64) IMAGE_ARCH="amd64"; GO_ARCH="amd64" ;;
  *) echo "error: this provisioner targets x86_64 (host arch: $(uname -m)); the guest domain XML is x86_64-only" >&2; exit 1 ;;
esac

# Ubuntu publishes each release under `/<codename>/current/`, with the image
# named `<codename>-server-cloudimg-<arch>.img`.
UBUNTU_CODENAME="${UBUNTU_CODENAME:-resolute}"
IMAGE_URL="${IMAGE_URL:-https://cloud-images.ubuntu.com/$UBUNTU_CODENAME/current/$UBUNTU_CODENAME-server-cloudimg-$IMAGE_ARCH.img}"
IMAGE_PATH="$KVM_IMAGE_CACHE/$(basename "$IMAGE_URL")"
DISK_PATH="$ARTIFACT_DIR/$VM_NAME.qcow2"
SEED_PATH="$ARTIFACT_DIR/$VM_NAME-seed.img"
USERDATA_PATH="$ARTIFACT_DIR/$VM_NAME-user-data"
METADATA_PATH="$ARTIFACT_DIR/$VM_NAME-meta-data"
DOMAIN_XML_PATH="$ARTIFACT_DIR/$VM_NAME.xml"

log() { printf '%s\n' "==> $*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
usage: kvm-guest.sh {up|render|ssh|sync|prime|run <cmd...>|agent [cmd]|agent-provision [name]|sync-back [branch]|firewall|down}

  up                create and boot the KVM guest (cloud-init provisions it)
  render            write the cloud-init seed and domain XML to ARTIFACT_DIR, no boot
  ssh               open a shell in the guest
  sync              rsync SOURCE_DIR (default: this checkout) into the guest
  prime             copy the host's runner images and microsandbox home into the guest
  run <..>          run a command in ~/agents-sandbox in the guest
  agent [cmd]       open an interactive agent session in a guest tmux session (default: opencode2)
  agent-provision [name]
                    materialize the agent's provisioned config into the guest home
  sync-back [branch]
                    fetch the guest's branches back (all into
                    refs/remotes/kvm-guest/*, or one into a local branch)
  firewall          re-apply the host nft rules the guest's egress needs
  down              destroy the guest and its disk

See the header of this script for host prerequisites and env overrides.
EOF
}

virsh_cmd() { virsh -c "$LIBVIRT_URI" "$@"; }

require_tool() {
  command -v "$1" >/dev/null 2>&1 || die "missing '$1' — install: sudo apt-get install -y qemu-kvm libvirt-daemon-system cloud-image-utils genisoimage"
}

resolve_ssh_key() {
  if [ -n "${SSH_PUBKEY:-}" ]; then
    if [ -r "$SSH_PUBKEY" ]; then cat "$SSH_PUBKEY"; else printf '%s\n' "$SSH_PUBKEY"; fi
    return
  fi
  local candidate
  for candidate in "$HOME"/.ssh/id_ed25519.pub "$HOME"/.ssh/id_ecdsa.pub "$HOME"/.ssh/id_rsa.pub; do
    if [ -r "$candidate" ]; then cat "$candidate"; return; fi
  done
  die "no SSH public key found; set SSH_PUBKEY to a key path or the key text"
}

# write_seed_files writes the cloud-init user-data and metadata. It touches
# nothing outside ARTIFACT_DIR, so `render` can use it without a host.
write_seed_files() {
  local pubkey opencode2_install golangci_install golangci_line
  pubkey="$(resolve_ssh_key)"

  # opencode2 is installed for the guest user (a root install would not be on the
  # user's PATH). Its binary lands in ~/.opencode/bin; ~/.local/bin is already on
  # PATH via the profile drop-in below, so link it there under both names.
  opencode2_install='mkdir -p ~/.local/bin && curl -fsSL https://opencode.ai/v2/install | bash -s --'
  [ -n "${OPENCODE2_VERSION:-}" ] && opencode2_install="$opencode2_install --version $OPENCODE2_VERSION"
  opencode2_install="$opencode2_install && ln -sfn ~/.opencode/bin/opencode ~/.local/bin/opencode && ln -sfn ~/.opencode/bin/opencode ~/.local/bin/opencode2"

  # golangci-lint at the Makefile-pinned version, the same installer the dev image
  # uses, so the guest's `make check` matches the host's gate.
  golangci_install=""
  golangci_line=""
  if [ -n "${GOLANGCI_LINT_VERSION:-}" ]; then
    golangci_install="curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b /usr/local/bin v$GOLANGCI_LINT_VERSION"
    golangci_line="  - $golangci_install"
  fi

  cat > "$USERDATA_PATH" <<EOF
#cloud-config
hostname: $VM_NAME
users:
  - name: $VM_USER
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    shell: /bin/bash
    ssh_authorized_keys:
      - $pubkey
ssh_pwauth: false
package_update: true
package_upgrade: false
packages:
  - docker.io
  - git
  - make
  - gcc
  - build-essential
  - ca-certificates
  - curl
  - jq
  - unzip
  - xz-utils
  - tmux
  - shellcheck
write_files:
  - path: /etc/profile.d/zz-asb-kvm.sh
    content: |
      export PATH="\$PATH:/usr/local/go/bin:\$HOME/.local/bin"
  - path: /etc/udev/rules.d/99-kvm.rules
    content: |
      KERNEL=="kvm", GROUP="kvm", MODE="0660"
runcmd:
  - systemctl enable --now docker
  - getent group kvm >/dev/null || groupadd -r kvm
  - usermod -aG docker,kvm $VM_USER
  # The msb suites cache 10+ GiB of runner images under MSB_REUSE_ROOT (default
  # /opt/asb-reuse); the guest's /tmp is a RAM-backed tmpfs that would exhaust.
  - install -d -o $VM_USER -g $VM_USER -m 0755 /opt/asb-reuse
  - udevadm control --reload-rules
  - udevadm trigger /dev/kvm || true
  - curl -fsSL "https://go.dev/dl/go$GO_VERSION.linux-$GO_ARCH.tar.gz" -o /tmp/go.tgz
  - rm -rf /usr/local/go
  - tar -C /usr/local -xzf /tmp/go.tgz
  - sudo -u $VM_USER -H bash -lc 'curl -fsSL https://install.microsandbox.dev | sh'
  - sudo -u $VM_USER -H bash -lc 'git clone "$REPO_URL" ~/agents-sandbox'
  - sudo -u $VM_USER -H bash -lc 'cd ~/agents-sandbox && git checkout "$REPO_BRANCH"'
  # The agent toolchain is installed last so a failure here cannot sit ahead of
  # the checkout; cloud-init is one-shot, so a failed boot is rebuilt, not retried.
  - sudo -u $VM_USER -H bash -lc '$opencode2_install'
$golangci_line
EOF

  cat > "$METADATA_PATH" <<EOF
instance-id: $VM_NAME
local-hostname: $VM_NAME
EOF
}

write_domain_xml() {
  cat > "$DOMAIN_XML_PATH" <<EOF
<domain type='kvm'>
  <name>$VM_NAME</name>
  <memory unit='MiB'>$VM_MEMORY_MIB</memory>
  <vcpu>$VM_VCPUS</vcpu>
  <cpu mode='host-passthrough'/>
  <os>
    <type arch='x86_64' machine='q35'>hvm</type>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/>
    <apic/>
  </features>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='$DISK_PATH'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='$SEED_PATH'/>
      <target dev='sda' bus='sata'/>
      <readonly/>
    </disk>
    <interface type='network'>
      <source network='default'/>
      <model type='virtio'/>
    </interface>
    <serial type='pty'>
      <target type='isa-serial' port='0'/>
    </serial>
    <console type='pty'>
      <target type='serial' port='0'/>
    </console>
  </devices>
</domain>
EOF
}

build_seed() { cloud-localds "$SEED_PATH" "$USERDATA_PATH" "$METADATA_PATH"; }

# render writes everything `up` would, without booting: useful to inspect the
# seed, and the seam the fixture harness drives.
render() {
  mkdir -p "$ARTIFACT_DIR"
  write_seed_files
  write_domain_xml
  printf 'image: %s\nimage path: %s\nrendered:\n%s\n%s\n%s\n' \
    "$IMAGE_URL" "$IMAGE_PATH" "$USERDATA_PATH" "$METADATA_PATH" "$DOMAIN_XML_PATH"
}

check_host() {
  local tool
  for tool in virsh qemu-img cloud-localds curl; do require_tool "$tool"; done
  [ -w /dev/kvm ] || die "/dev/kvm not writable on the host (add \$USER to the kvm group)"
  local nested="" module path
  for module in kvm_amd kvm_intel; do
    path="/sys/module/$module/parameters/nested"
    [ -r "$path" ] && nested="$(cat "$path")"
  done
  case "$nested" in
    Y|1|y|on) : ;;
    "") log "warn: could not read the host nested-virt parameter; continuing" ;;
    *) die "host nested virtualization is off (nested=$nested); set options kvm-amd nested=1 and reboot" ;;
  esac
  virsh_cmd list >/dev/null 2>&1 || die "cannot reach libvirt at $LIBVIRT_URI (is it running? set LIBVIRT_URI?)"
  virsh_cmd net-info default >/dev/null 2>&1 || die "libvirt network 'default' not found at $LIBVIRT_URI"
  if virsh_cmd dominfo "$VM_NAME" >/dev/null 2>&1; then
    die "libvirt domain '$VM_NAME' already exists (run: $0 down)"
  fi
}

ensure_default_network() {
  local info
  info="$(virsh_cmd net-info default 2>/dev/null || true)"
  if grep -qE '^Active:[[:space:]]+yes' <<<"$info"; then
    return 0
  fi
  if ! virsh_cmd net-start default; then
    die "could not start libvirt network 'default'.
    If the error above was \"iptables ... table 'filter' is incompatible, use 'nft' tool\",
    libvirt is using its legacy iptables backend on an nftables host. Switch it once:
      echo 'firewall_backend = \"nftables\"' | sudo tee /etc/libvirt/network.conf
      sudo systemctl restart libvirtd
      sudo virsh -c $LIBVIRT_URI net-start default"
  fi
  virsh_cmd net-autostart default >/dev/null 2>&1 || true
}

default_bridge() {
  local bridge
  bridge="$(virsh_cmd net-dumpxml default 2>/dev/null |
    sed -n "s/.*<bridge name='\([^']*\)'.*/\1/p" | head -1)"
  printf '%s\n' "${bridge:-virbr0}"
}

print_firewall_hint() {
  local bridge
  bridge="$(default_bridge)"
  cat >&2 <<EOF

------------------------------------------------------------
Host firewall: the guest reaches the host for ICMP/SSH/DHCP only,
not DNS or egress. Docker owns the iptables-nft 'ip filter' table
and drops host-bound and forwarded traffic by policy, while
libvirt's nftables backend writes its accepts into a separate table
that cannot override that drop. Add three name-keyed accepts:

  sudo nft insert rule ip filter INPUT       iifname "$bridge" accept
  sudo nft insert rule ip filter DOCKER-USER iifname "$bridge" accept
  sudo nft insert rule ip filter DOCKER-USER oifname "$bridge" accept

or let this script do it:  sudo $0 firewall
Docker preserves DOCKER-USER across daemon restarts, but a reboot drops
these unless your base ruleset re-applies them — re-run if the guest
loses DNS after a reboot.
------------------------------------------------------------
EOF
}

fix_firewall() {
  local bridge runner=()
  bridge="$(default_bridge)"
  [ "$(id -u)" -eq 0 ] || runner=(sudo)
  if ! "${runner[@]}" nft list table ip filter >/dev/null 2>&1; then
    log "no Docker-managed 'ip filter' table here; guest egress needs no extra rules"
    return 0
  fi
  log "accepting bridge '$bridge' in INPUT and DOCKER-USER (Docker preserves DOCKER-USER)"
  "${runner[@]}" nft insert rule ip filter INPUT iifname "$bridge" accept
  "${runner[@]}" nft insert rule ip filter DOCKER-USER iifname "$bridge" accept
  "${runner[@]}" nft insert rule ip filter DOCKER-USER oifname "$bridge" accept
  log "done — Docker preserves DOCKER-USER; a reboot drops these unless your base ruleset re-applies them"
}

guest_ip() {
  local ip=""
  ip="$(virsh_cmd domifaddr "$VM_NAME" --source lease 2>/dev/null |
    awk '$3 == "ipv4" { sub(/\/.*/, "", $4); print $4; exit }')"
  [ -n "$ip" ] || ip="$(virsh_cmd domifaddr "$VM_NAME" --source arp 2>/dev/null |
    awk '$3 == "ipv4" { sub(/\/.*/, "", $4); print $4; exit }')"
  printf '%s\n' "$ip"
}

require_guest_ip() {
  local ip
  ip="$(guest_ip)"
  [ -n "$ip" ] || die "no DHCP lease for '$VM_NAME' — still booting? check: virsh -c $LIBVIRT_URI net-dhcp-leases default"
  printf '%s\n' "$ip"
}

ssh_in() {
  exec ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$(require_guest_ip)"
}

sync_in() {
  local ip source
  ip="$(require_guest_ip)"
  source="${SOURCE_DIR:-$(git -C "$repo_root" rev-parse --show-toplevel 2>/dev/null || printf '%s' "$repo_root")}"
  log "syncing $source → $VM_USER@$ip:~/agents-sandbox"
  # Exclude the `.git` entry, not a `.git/` directory: the guest owns its own
  # clone's .git, and the host checkout may be a linked worktree whose .git is a
  # *file* recording a relative `gitdir: ../.git/worktrees/<name>`. A
  # directory-only `.git/` pattern does not match that file, so rsync would copy
  # the dangling pointer over the guest's real .git directory and every launcher
  # command in the guest would then fail to resolve the project identity.
  rsync -az --delete --exclude '.git' --exclude '.superpowers/' \
    --exclude "$(basename "$ARTIFACT_DIR")/" \
    "$source/" "$VM_USER@$ip:~/agents-sandbox/"
}

# sync_back fetches the guest checkout's branches back to the host: by default
# every branch into the refs/remotes/kvm-guest/* namespace (reachable locally
# without touching any local branch), or one branch named by the argument into
# the local branch of the same name (fast-forward only). A branch checked out
# here refuses the fetch — git protects it, because updating the ref without the
# working tree would make the next commit silently revert the fetch — so update
# it by merging the namespace ref instead (`git merge kvm-guest/<name>`). The
# guest keeps its own .git (sync excludes it), so this fetch is the return path;
# the guest never pushes.
sync_back() {
  local ip branch
  branch="${1:-}"
  ip="$(require_guest_ip)"
  if [ -z "$branch" ]; then
    log "fetching every guest branch into refs/remotes/kvm-guest/*"
    git -C "$repo_root" fetch --prune \
      "$VM_USER@$ip:~/agents-sandbox" '+refs/heads/*:refs/remotes/kvm-guest/*'
    return
  fi
  log "fetching guest branch '$branch' into local '$branch'"
  git -C "$repo_root" fetch \
    "$VM_USER@$ip:~/agents-sandbox" "refs/heads/$branch:refs/heads/$branch"
}

# prime copies the host's runner images (docker save | ssh docker load) and
# microsandbox home into the guest, so the suite's image prebuild finds both and
# skips it. Both sides must use the same MSB_REUSE_ROOT (KVM_PRIME_ROOT, default
# /opt/asb-reuse): the runner-image slug hashes the project directory's absolute
# path, so a different root names different images and nothing matches. Host and
# guest must also share the CPU architecture and the microsandbox runtime version;
# prime before a run, never while one is using the home.
prime() {
  local ip root="${KVM_PRIME_ROOT:-/opt/asb-reuse}" tags home
  ip="$(require_guest_ip)"
  home="$root/smoke/data/agents-sandbox/msb"

  tags="$(docker images --format '{{.Repository}}:{{.Tag}}' \
    --filter reference='agents-sandbox/runner-*' 2>/dev/null || true)"
  if [ -n "$tags" ]; then
    log "copying host runner images to $VM_USER@$ip; 'docker load' prints one 'Loaded image' line per image:"
    # shellcheck disable=SC2086  # word-split the newline-separated tag list, one per line
    printf '==>   %s\n' $tags >&2
    # One save|load stream, not a save/load per image, so a layer shared between
    # two images is sent once; docker load still reports each image as it lands.
    # shellcheck disable=SC2086  # word-split the newline-separated tag list into args
    docker save $tags | ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" 'docker load'
  else
    log "no host runner images (agents-sandbox/runner-*); build them first: 'make test-msb' on the host"
  fi

  if [ -d "$home" ]; then
    log "copying the microsandbox home from $home"
    ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" "mkdir -p '$root/smoke/data/agents-sandbox'"
    # --info=progress2 gives an overall progress line; --no-inc-recursive makes
    # its total (and so its percentage) meaningful for a full copy.
    rsync -a --info=progress2 --no-inc-recursive "$home/" "$VM_USER@$ip:$home/"
  else
    log "no host microsandbox home at $home; build it first: 'make test-msb' on the host"
  fi

  log "primed — run 'make kvm-run' (host and guest must both use MSB_REUSE_ROOT=$root)"
}

run_in() {
  [ "$#" -gt 0 ] || { usage >&2; exit 2; }
  # A login shell so /etc/profile.d puts Go and ~/.local/bin (msb) on PATH for
  # the suite the command runs.
  exec ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$(require_guest_ip)" \
    -- "bash -lc 'cd ~/agents-sandbox && $*'"
}

# agent_in opens an interactive agent session in the guest: it allocates a TTY
# (the TUI needs one) and runs the command inside a named tmux session, so a
# dropped SSH connection leaves the agent running and re-running the command
# reattaches to it. The default command is the opencode2 binary; pass another
# command — including an inline `VAR=value` assignment for credentials, which the
# guest shell interprets — as arguments. The command must not contain a single
# quote, because it is embedded in a single-quoted remote command.
agent_in() {
  local ip session command
  session="${KVM_AGENT_SESSION:-agents-sandbox}"
  command="${*:-opencode2}"
  ip="$(require_guest_ip)"
  exec ssh -tt -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
    -- "bash -lc 'cd ~/agents-sandbox && exec tmux new -A -s $session bash -c \"$command\"'"
}

# agent_provision materializes the agent config into the guest home. It syncs the
# host's launcher config (so the guest resolves the same scopes) and runs the
# launcher's `config provision` in the guest, which writes exactly the config a
# sandbox would receive — merged snippets, verbatim mirror, host drop-in, home
# files — by reusing the launcher's own pipeline. It runs in the guest because the
# Go toolchain lives there, not on the KVM host. Run it after `sync`, so the
# checkout carries the command.
agent_provision() {
  local ip agent
  agent="${1:-opencode2}"
  ip="$(require_guest_ip)"
  if [ -d "$HOME/.config/agents-sandbox" ]; then
    log "syncing $HOME/.config/agents-sandbox → $VM_USER@$ip"
    ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" -- "bash -lc 'mkdir -p ~/.config/agents-sandbox'"
    rsync -az --delete "$HOME/.config/agents-sandbox/" "$VM_USER@$ip:.config/agents-sandbox/"
  else
    log "no $HOME/.config/agents-sandbox on this host; the guest will use its project config only"
  fi
  log "materializing the $agent config into the guest home"
  ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
    -- "bash -lc 'cd ~/agents-sandbox && go run -tags microsandbox_ffi_path ./cmd/agents-sandbox config provision --agent $agent --target \"\$HOME\"'"
  if [ -f "$HOME/.gitconfig" ]; then
    log "syncing $HOME/.gitconfig → $VM_USER@$ip (signing disabled in the guest)"
    rsync -az "$HOME/.gitconfig" "$VM_USER@$ip:.gitconfig"
    ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
      -- "bash -lc 'git config --global commit.gpgsign false && git config --global tag.gpgsign false'"
  else
    log "no $HOME/.gitconfig on this host; the guest keeps its own git identity"
  fi
}

probe_guest_egress() {
  local ip="${1:-}" result=""
  if [ -z "$ip" ]; then
    log "guest not reachable yet; skipping the DNS/egress probe (retry: $0 ssh)"
    return 0
  fi
  result="$(ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new \
    -o ConnectTimeout=8 -o LogLevel=ERROR "$VM_USER@$ip" \
    'getent hosts install.microsandbox.dev >/dev/null 2>&1 && echo ok || echo no-dns' 2>/dev/null || true)"
  case "$result" in
    ok) log "guest DNS/egress: ok" ;;
    no-dns) log "guest DNS/egress: BROKEN"; print_firewall_hint ;;
    *) log "could not reach the guest over ssh to probe DNS/egress (sshd may still be starting)" ;;
  esac
}

print_next_steps() {
  local ip="${1:-}"
  cat >&2 <<EOF

============================================================
Guest '$VM_NAME' is booting. cloud-init provisions it on first
boot: docker, Go $GO_VERSION, msb, and the repo at $REPO_BRANCH.

Watch progress:  virsh -c $LIBVIRT_URI console $VM_NAME    (leave with Ctrl-])
SSH:             ssh $VM_USER@${ip:-<guest-ip>}   (or: $0 ssh)

When cloud-init is done, gate the guest (after: make kvm-ssh):

  ls -la /dev/kvm                                   # nested KVM present
  msb doctor && msb run alpine -- uname -a          # runtime works
  go version                                        # Go from /etc/profile.d
  exit

Then, on the host, sync this checkout and run the suite:

  make kvm-sync && make kvm-run                     # KVM_RUN (default: make test-msb)

Artifacts (disk, seed): $ARTIFACT_DIR
Base image (cached, reused): $IMAGE_PATH
============================================================
EOF
}

create() {
  check_host
  mkdir -p "$ARTIFACT_DIR"
  # The base image must be readable by libvirt's QEMU user, and on
  # Debian/Ubuntu AppArmor only allows libvirt's own paths, so the cache
  # defaults to /var/lib/libvirt/images/agents-sandbox. Create it once,
  # owned by us so the fetch can write.
  if [ ! -d "$KVM_IMAGE_CACHE" ]; then
    die "image cache '$KVM_IMAGE_CACHE' does not exist. Create it once:
      sudo install -d -o \"\$USER\" -g \"\$USER\" -m 0755 '$KVM_IMAGE_CACHE'
    (a base image on a path libvirt's QEMU user cannot read, or AppArmor does
     not allow, makes 'virsh start' fail with Permission denied)"
  fi
  [ -w "$KVM_IMAGE_CACHE" ] || die "image cache '$KVM_IMAGE_CACHE' is not writable by $USER; chown it: sudo chown \"\$USER\" '$KVM_IMAGE_CACHE'"

  if [ "${KVM_REFRESH_IMAGE:-}" = "1" ]; then
    log "KVM_REFRESH_IMAGE set; discarding the cached base image"
    rm -f "$IMAGE_PATH"
  fi
  if [ ! -f "$IMAGE_PATH" ]; then
    log "fetching $IMAGE_URL"
    # Download to a .part and move on success, so a failed fetch (a 404, a
    # dropped connection) leaves no partial base image to be mistaken for a
    # complete one on the next run.
    curl -fL --progress-bar "$IMAGE_URL" -o "$IMAGE_PATH.part"
    mv "$IMAGE_PATH.part" "$IMAGE_PATH"
  else
    log "using cached base image $IMAGE_PATH"
  fi
  chmod 0644 "$IMAGE_PATH"
  if [ ! -f "$DISK_PATH" ]; then
    log "creating ${VM_DISK_GIB}G overlay on $(basename "$IMAGE_PATH")"
    qemu-img create -f qcow2 -F qcow2 -b "$IMAGE_PATH" "$DISK_PATH" "${VM_DISK_GIB}G" >/dev/null
  fi

  log "writing cloud-init seed"
  write_seed_files
  build_seed

  log "starting the default libvirt network"
  ensure_default_network

  log "defining and booting '$VM_NAME' ($VM_VCPUS vCPU, ${VM_MEMORY_MIB} MiB)"
  write_domain_xml
  virsh_cmd define "$DOMAIN_XML_PATH" >/dev/null
  virsh_cmd start "$VM_NAME"

  local timeout="${KVM_BOOT_TIMEOUT:-300}" waited=0 ip=""
  log "waiting up to ${timeout}s for a DHCP lease (cloud-init first boot takes a few minutes)"
  while [ "$waited" -lt "$timeout" ]; do
    ip="$(guest_ip)"
    [ -n "$ip" ] && break
    sleep 5
    waited=$((waited + 5))
    [ $((waited % 30)) -eq 0 ] && log "  ...still waiting (${waited}s)"
  done
  if [ -z "$ip" ]; then
    log "warn: no DHCP lease after ${timeout}s — the guest may still be booting"
    log "  lease:     virsh -c $LIBVIRT_URI net-dhcp-leases default"
    log "  console:   virsh -c $LIBVIRT_URI console $VM_NAME   (leave with Ctrl-])"
    log "  retry ssh: $0 ssh"
  fi

  print_next_steps "$ip"
  probe_guest_egress "$ip"
}

destroy() {
  if virsh_cmd dominfo "$VM_NAME" >/dev/null 2>&1; then
    log "destroying domain '$VM_NAME'"
    virsh_cmd destroy "$VM_NAME" >/dev/null 2>&1 || true
    virsh_cmd undefine "$VM_NAME" --nvram >/dev/null 2>&1 || virsh_cmd undefine "$VM_NAME" >/dev/null 2>&1 || true
  else
    log "domain '$VM_NAME' is not defined; removing any leftover disk"
  fi
  rm -f "$DISK_PATH" "$SEED_PATH" "$USERDATA_PATH" "$METADATA_PATH" "$DOMAIN_XML_PATH"
  log "removed '$VM_NAME' and its disk (base image kept in cache at $IMAGE_PATH)"
}

case "${1:-}" in
  up|create)       create ;;
  render)          render ;;
  ssh)             ssh_in ;;
  sync)            sync_in ;;
  prime)           prime ;;
  run)             shift; run_in "$@" ;;
  agent)           shift; agent_in "$@" ;;
  agent-provision) shift; agent_provision "$@" ;;
  sync-back)       shift; sync_back "$@" ;;
  firewall|fix-net) fix_firewall ;;
  down|destroy)    destroy ;;
  *) usage >&2; exit 2 ;;
esac
