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
# Guest: Debian forky (14, testing) genericcloud image, seeded by cloud-init.
# Override with IMAGE_URL for a different image (e.g. an Ubuntu cloud image). The domain is defined with
# `virsh define` (no virt-install): virt-manager's Python bindings are a common
# host breakage and nothing here needs them.
#
# Usage (on the host):
#   ./scripts/kvm-guest.sh up        # create + boot the guest
#   ./scripts/kvm-guest.sh render    # write the cloud-init seed + domain XML, no boot
#   ./scripts/kvm-guest.sh ssh       # ssh into the guest
#   ./scripts/kvm-guest.sh sync      # rsync the current checkout into the guest
#   ./scripts/kvm-guest.sh run make test-msb-journeys   # run a command in the guest
#   ./scripts/kvm-guest.sh firewall  # re-apply the host nft rules guest egress needs
#   ./scripts/kvm-guest.sh down      # destroy the guest and its disk
#
# Host prerequisites:
#   qemu-kvm libvirt-daemon-system cloud-image-utils genisoimage
#   your user in the `libvirt` and `kvm` groups
#   nested virtualization enabled (e.g. options kvm-amd nested=1 + reboot)
#
# Env overrides:
#   VM_NAME=asb-kvm  VM_VCPUS=8  VM_MEMORY_MIB=16384  VM_DISK_GIB=80
#   VM_USER=dev  SSH_PUBKEY=<path-or-key>  ARTIFACT_DIR=$PWD/.kvm-guest
#   SOURCE_DIR=<checkout to sync>  LIBVIRT_URI=qemu:///system
#   IMAGE_URL=<full cloud-image URL>
#   KVM_IMAGE_CACHE=~/.agents-sandbox-kvm  KVM_REFRESH_IMAGE=1
#   REPO_URL=https://github.com/inoio/agents-sandbox.git  REPO_BRANCH=<current branch>
#   GO_VERSION=<from .go-version>
#
# The base image is cached in KVM_IMAGE_CACHE (shared across worktrees and
# untouched by `down`), so only the first `up` fetches it. Set
# KVM_REFRESH_IMAGE=1 to re-fetch (the forky `daily/latest` filename is stable,
# so the cache cannot otherwise notice a newer daily build). The cache is a
# single dir directly under $HOME, not a nested path and not the XDG
# cache/data dirs: libvirt runs QEMU as its own user (e.g. libvirt-qemu), which
# must traverse every path component to read the base image, and ~/.cache and
# ~/.local are 0700 by design, while a nested dir can inherit a 0700 parent
# under a restrictive umask. $HOME itself is normally world-traversable.
#
# Firewall: where Docker manages the iptables-nft `ip filter` table (its base
# chains carry `policy drop`) and libvirt uses its nftables backend, libvirt's
# accepts land in a separate table that cannot override that drop — the guest
# then reaches the host for ICMP/SSH/DHCP only, not DNS or egress. `firewall`
# applies the three name-keyed accepts that fix it; `up` probes the guest and
# points at them when it can.
set -euo pipefail

VM_NAME="${VM_NAME:-asb-kvm}"
VM_VCPUS="${VM_VCPUS:-8}"
VM_MEMORY_MIB="${VM_MEMORY_MIB:-16384}"
VM_DISK_GIB="${VM_DISK_GIB:-80}"
VM_USER="${VM_USER:-dev}"
LIBVIRT_URI="${LIBVIRT_URI:-qemu:///system}"
REPO_URL="${REPO_URL:-https://github.com/inoio/agents-sandbox.git}"
ARTIFACT_DIR="${ARTIFACT_DIR:-$PWD/.kvm-guest}"
KVM_IMAGE_CACHE="${KVM_IMAGE_CACHE:-$HOME/.agents-sandbox-kvm}"

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

case "$(uname -m)" in
  x86_64) IMAGE_ARCH="amd64"; GO_ARCH="amd64" ;;
  *) echo "error: this provisioner targets x86_64 (host arch: $(uname -m)); the guest domain XML is x86_64-only" >&2; exit 1 ;;
esac

# Debian forky publishes only date-stamped daily builds, under a stable
# `daily/latest` alias; the image matches the dev image's base (debian:forky-slim)
# and its newer kernel suits nested KVM.
IMAGE_URL="${IMAGE_URL:-https://cloud.debian.org/images/cloud/forky/daily/latest/debian-14-genericcloud-$IMAGE_ARCH-daily.qcow2}"
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
usage: kvm-guest.sh {up|render|ssh|sync|run <cmd...>|firewall|down}

  up        create and boot the KVM guest (cloud-init provisions it)
  render    write the cloud-init seed and domain XML to ARTIFACT_DIR, no boot
  ssh       open a shell in the guest
  sync      rsync SOURCE_DIR (default: this checkout) into the guest
  run <..>  run a command in ~/agents-sandbox in the guest
  firewall  re-apply the host nft rules the guest's egress needs
  down      destroy the guest and its disk

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
  local pubkey
  pubkey="$(resolve_ssh_key)"

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
  - udevadm control --reload-rules
  - udevadm trigger /dev/kvm || true
  - curl -fsSL "https://go.dev/dl/go$GO_VERSION.linux-$GO_ARCH.tar.gz" -o /tmp/go.tgz
  - rm -rf /usr/local/go
  - tar -C /usr/local -xzf /tmp/go.tgz
  - sudo -u $VM_USER -H bash -lc 'curl -fsSL https://install.microsandbox.dev | sh'
  - sudo -u $VM_USER -H bash -lc 'git clone "$REPO_URL" ~/agents-sandbox'
  - sudo -u $VM_USER -H bash -lc 'cd ~/agents-sandbox && git checkout "$REPO_BRANCH"'
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

  sudo nft insert rule ip filter INPUT   iifname "$bridge" accept
  sudo nft insert rule ip filter FORWARD iifname "$bridge" accept
  sudo nft insert rule ip filter FORWARD oifname "$bridge" accept

or let this script do it:  sudo $0 firewall
These rules are volatile: a 'virsh net-destroy default', a Docker
restart, or a reboot drops them — re-run if the guest loses DNS.
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
  log "adding name-keyed accepts for bridge '$bridge' to the ip filter table"
  "${runner[@]}" nft insert rule ip filter INPUT iifname "$bridge" accept
  "${runner[@]}" nft insert rule ip filter FORWARD iifname "$bridge" accept
  "${runner[@]}" nft insert rule ip filter FORWARD oifname "$bridge" accept
  log "done — volatile: a net-destroy, Docker restart, or reboot drops these rules"
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
  [ -n "$ip" ] || die "no DHCP lease for '$VM_NAME'; is it running?"
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
  rsync -az --delete --exclude '.git/' --exclude '.superpowers/' \
    --exclude "$(basename "$ARTIFACT_DIR")/" \
    "$source/" "$VM_USER@$ip:~/agents-sandbox/"
}

run_in() {
  [ "$#" -gt 0 ] || { usage >&2; exit 2; }
  # A login shell so /etc/profile.d puts Go and ~/.local/bin (msb) on PATH for
  # the suite the command runs.
  exec ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$(require_guest_ip)" \
    -- "bash -lc 'cd ~/agents-sandbox && $*'"
}

probe_guest_egress() {
  local ip="${1:-}" result=""
  [ -n "$ip" ] || return 0
  result="$(ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new \
    -o ConnectTimeout=8 -o LogLevel=ERROR "$VM_USER@$ip" \
    'getent hosts install.microsandbox.dev >/dev/null 2>&1 && echo ok || echo no-dns' 2>/dev/null || true)"
  case "$result" in
    ok) log "guest DNS/egress: ok" ;;
    no-dns) log "guest DNS/egress: BROKEN"; print_firewall_hint ;;
    *) : ;;
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

When cloud-init is done, gate the guest before running the suites:

  ls -la /dev/kvm                                   # nested KVM present
  msb doctor && msb run alpine -- uname -a          # runtime works
  cd ~/agents-sandbox && make build                  # build the launcher
  make test-msb-journeys                             # the journeys suite

Artifacts (disk, seed): $ARTIFACT_DIR
Base image (cached, reused): $IMAGE_PATH
============================================================
EOF
}

create() {
  check_host
  mkdir -p "$ARTIFACT_DIR" "$KVM_IMAGE_CACHE"
  # The base image must be readable (and the cache dir traversable) by
  # libvirt's QEMU user, which is not us on a system libvirt.
  chmod 0755 "$KVM_IMAGE_CACHE"
  if [ -n "$(find "$HOME" -maxdepth 0 ! -perm -o+x 2>/dev/null)" ]; then
    log "warn: $HOME is not traversable by other users; libvirt's QEMU user may not read $IMAGE_PATH (chmod o+x \"$HOME\")"
  fi

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

  log "waiting for an IP address (cloud-init first boot takes a few minutes)"
  local ip="" _
  for _ in $(seq 1 60); do
    ip="$(guest_ip)"
    [ -n "$ip" ] && break
    sleep 5
  done

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
  run)             shift; run_in "$@" ;;
  firewall|fix-net) fix_firewall ;;
  down|destroy)    destroy ;;
  *) usage >&2; exit 2 ;;
esac
