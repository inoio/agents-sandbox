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
#   ./scripts/kvm-guest.sh sync      # point the guest's git at the host and pull its branch
#   ./scripts/kvm-guest.sh prime     # copy the host's runner images + msb home into the guest
#   ./scripts/kvm-guest.sh run make test-msb-journeys   # run a command in the guest
#   ./scripts/kvm-guest.sh agent            # interactive agent session in the guest
#   ./scripts/kvm-guest.sh agent-up         # ensure up + cloud-init + sync + provision + attach
#   ./scripts/kvm-guest.sh agent-provision  # materialize the agent config into the guest
#   ./scripts/kvm-guest.sh sync-back [branch]  # fetch the guest's branches back to the host
#   ./scripts/kvm-guest.sh firewall  # re-apply the host nft rules guest egress needs
#   ./scripts/kvm-guest.sh down      # destroy the guest and its disk
#
# The agent subcommands run a coding agent (opencode2) *in the guest*, where
# /dev/kvm, docker and msb work, so it can build the launcher and run the msb
# suites. `agent` attaches an interactive tmux session; `agent-provision` syncs
# the host's launcher config and materializes the agent's provisioned config into
# the guest home. Model credentials are not provisioned: pass them as env assignments on
# the agent command, e.g.
#   make kvm-agent KVM_AGENT='LITELLM_API_KEY=… opencode'
#
# `agent-up` additionally signs the guest's `gh` in from a host-environment
# `GITHUB_PAT` when set (see `gh_auth`), so the in-guest agent can work with
# GitHub issues and PRs. Unset it to skip the sign-in. The guest's git remote is
# the host repository, not GitHub, so the agent pushes to the host.
#
# Host prerequisites:
#   qemu-kvm libvirt-daemon-system cloud-image-utils genisoimage
#   your user in the `libvirt` and `kvm` groups
#   nested virtualization enabled (e.g. options kvm-amd nested=1 + reboot)
#
# Env overrides:
#   VM_NAME=asb-kvm  VM_VCPUS=12  VM_MEMORY_MIB=16384  VM_DISK_GIB=80
#   VM_USER=dev  SSH_PUBKEY=<path-or-key>  ARTIFACT_DIR=$PWD/.kvm-guest
#   KVM_HOST_USER=<host user>  KVM_GUEST_STATE=<guest git key/state dir>
#   LIBVIRT_URI=qemu:///system
#   UBUNTU_CODENAME=resolute  IMAGE_URL=<full cloud-image URL>
#   KVM_IMAGE_CACHE=/var/lib/libvirt/images/agents-sandbox  KVM_REFRESH_IMAGE=1  KVM_BOOT_TIMEOUT=300
#   REPO_URL=https://github.com/inoio/agents-sandbox.git  REPO_BRANCH=<current branch>
#   GO_VERSION=<from .go-version>  GOLANGCI_LINT_VERSION=<from the Makefile>
#   GH_VERSION=<from .agents-sandbox/Dockerfile>  # the gh CLI installed in the guest
#   OPENCODE2_VERSION=<the opencode2 installer's --version; unset installs latest>
#   KVM_AGENT_SESSION=agents-sandbox  # the guest tmux session the agent runs in
#   KVM_AGENT_NAME=opencode2  # the profile `agent-up`/`agent-provision` materializes
#   KVM_CLOUD_INIT_ATTEMPTS=120  KVM_CLOUD_INIT_INTERVAL=5  # the agent-up SSH/cloud-init gate
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
# The guest authenticates to the host for git with a key kept in a host-global
# state dir (not ARTIFACT_DIR, which is per-worktree while the domain is shared).
KVM_HOST_USER="${KVM_HOST_USER:-${USER:-$(id -un)}}"
KVM_GUEST_STATE="${KVM_GUEST_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/agents-sandbox/kvm-guest}"
KVM_GUEST_KEY="$KVM_GUEST_STATE/id_kvm_guest"
# The cloud-init gate waits for sshd, then for cloud-init itself. The attempts ×
# interval bound only how long the guest may take to accept SSH; the
# `cloud-init status --wait` that follows blocks until cloud-init reports.
KVM_CLOUD_INIT_ATTEMPTS="${KVM_CLOUD_INIT_ATTEMPTS:-120}"
KVM_CLOUD_INIT_INTERVAL="${KVM_CLOUD_INIT_INTERVAL:-5}"

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

if [ -z "${GH_VERSION:-}" ]; then
  GH_VERSION="$(sed -n 's/^ARG GH_VERSION=//p' "$repo_root/.agents-sandbox/Dockerfile" 2>/dev/null | head -1 | tr -d '[:space:]' || true)"
  [ -n "$GH_VERSION" ] || GH_VERSION="2.104.0"
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

# require_safe_agent_command rejects a command that cannot be embedded in the
# remote shell string `agent_in` builds: it lands inside a single-quoted remote
# script and is re-parsed by a nested `bash -c "…"`, so a quote or an expansion
# character would end the string or be expanded early.
require_safe_agent_command() {
  local command="$1" char
  for char in "'" '"' '$' '`' "\\"; do
    case "$command" in
      *"$char"*) die "the agent command must not contain a single quote, double quote, backslash, dollar sign, or backtick (it is embedded in a shell string)" ;;
    esac
  done
}

# require_safe_branch rejects a branch name that cannot be embedded safely in the
# guest shell string the sync commands build. A git branch name may contain shell
# metacharacters (`;`, `&`, `|`, …), so the name is placed inside double quotes in
# the remote `bash -lc` script; the double quote, backslash, dollar sign, and
# backtick — the characters double quotes do not neutralize — must be absent.
require_safe_branch() {
  local branch="$1" char
  for char in '"' '$' '`' "\\"; do
    case "$branch" in
      *"$char"*) die "the branch name must not contain a double quote, backslash, dollar sign, or backtick (it is embedded in a shell string): $branch" ;;
    esac
  done
}

usage() {
  cat <<'EOF'
usage: kvm-guest.sh {up|render|ssh|sync|prime|run <cmd...>|agent [cmd]|agent-up [cmd]|agent-provision [name]|sync-back [branch]|firewall|down}

  up                create and boot the KVM guest (cloud-init provisions it)
  render            write the cloud-init seed and domain XML to ARTIFACT_DIR, no boot
  ssh               open a shell in the guest
  sync              point the guest's git at the host and pull its branch
  prime             copy the host's runner images and microsandbox home into the guest
  run <..>          run a command in ~/agents-sandbox in the guest
  agent [cmd]       open an interactive agent session in a guest tmux session (default: opencode2)
  agent-up [cmd]    ensure the guest is up, wait for cloud-init, sync, provision,
                    and attach an agent session (one-command bring-up)
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
  local pubkey opencode2_install golangci_install golangci_line gh_install
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

  # gh: the GitHub CLI, installed from the pinned release .deb the same way the
  # dev image does, so the in-guest agent can work with issues, PRs, and pushes.
  gh_install="curl -fsSL https://github.com/cli/cli/releases/download/v$GH_VERSION/gh_${GH_VERSION}_linux_$GO_ARCH.deb -o /tmp/gh.deb && dpkg -i /tmp/gh.deb && rm -f /tmp/gh.deb"

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
  - $gh_install
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

# default_gateway prints the host's address on the guest's bridge: the guest
# reaches the host there, and the guest's git remote points at it.
default_gateway() {
  local gateway
  gateway="$(virsh_cmd net-dumpxml default 2>/dev/null |
    sed -n "s/.*<ip address='\([^']*\)'.*/\1/p" | head -1)"
  printf '%s\n' "$gateway"
}

require_gateway() {
  local gateway
  gateway="$(default_gateway)"
  [ -n "$gateway" ] || die "could not determine the '$VM_NAME' bridge gateway; is the libvirt 'default' network active?"
  printf '%s\n' "$gateway"
}

# host_repo_path prints the host repository the guest's origin is routed to.
# --git-common-dir is the bare repository in the bare-worktree layout and the
# .git gitdir in a plain clone; git serves both as a remote path.
host_repo_path() {
  local path
  path="$(git -C "$repo_root" rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)"
  [ -n "$path" ] || die "cannot resolve the host repository path from $repo_root"
  printf '%s\n' "$path"
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

# configure_guest_remote routes the guest checkout's single origin remote to the
# host: a url.<host>.insteadOf rewrite sends every git operation on origin to the
# host repository, while origin keeps the URL the boot clone recorded. The branch
# upstream is pinned to origin and the guest's git uses the dedicated key. Each
# setting is repository-local (in ~/agents-sandbox/.git/config), not --global:
# `agent-provision` truncates the guest's ~/.gitconfig, which would wipe a global
# rewrite and send the agent's pushes back to GitHub. The host URL is recorded in
# kvmguest.hostUrl so a re-run against a different host first removes the previous
# url.<host> section — two insteadOf rules for the same URL would let the stale
# one shadow the new one. push.autoSetupRemote makes the agent's plain `git push`
# of a new branch work.
configure_guest_remote() {
  local ip="$1" gateway="$2" repo_path="$3" host_url branch
  branch="${REPO_BRANCH:-main}"
  require_safe_branch "$branch"
  host_url="ssh://$KVM_HOST_USER@$gateway$repo_path"
  log "routing the guest origin to $host_url"
  ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
    -- "bash -lc 'set -e && cd ~/agents-sandbox && prev=\$(git config kvmguest.hostUrl 2>/dev/null || true); if [ -n \"\$prev\" ]; then git config --remove-section \"url.\$prev\" || true; fi; git config url.\"$host_url\".insteadOf $REPO_URL && git config kvmguest.hostUrl \"$host_url\" && git config push.autoSetupRemote true && git config branch.\"$branch\".remote origin && git config branch.\"$branch\".merge refs/heads/\"$branch\" && git config core.sshCommand \"ssh -i \$HOME/.ssh/id_kvm_guest -o StrictHostKeyChecking=accept-new\"'"
}

# ensure_guest_git_key gives the guest a key that authenticates it to the host for
# git: the key is generated once under the host-global state dir (so a guest
# rebuild reuses it and authorized_keys does not accumulate lines), authorized on
# the host, and installed in the guest.
ensure_guest_git_key() {
  local ip="$1"
  if [ ! -f "$KVM_GUEST_KEY" ]; then
    log "generating the guest git key at $KVM_GUEST_KEY"
    mkdir -p "$KVM_GUEST_STATE"
    chmod 700 "$KVM_GUEST_STATE"
    ssh-keygen -t ed25519 -N '' -C agents-sandbox-kvm-guest -f "$KVM_GUEST_KEY" >/dev/null
  fi
  ensure_host_authorized_key
  log "installing the guest git key in $VM_USER@$ip"
  ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
    -- "bash -lc 'install -d -m 700 ~/.ssh && cat > ~/.ssh/id_kvm_guest && chmod 600 ~/.ssh/id_kvm_guest'" \
    < "$KVM_GUEST_KEY"
}

# ensure_host_authorized_key authorizes the guest key for git on this host,
# confined to git-shell (it can run git-upload-pack/receive-pack, not a shell).
# Idempotent by the whole key: a rotated key replaces the marker-tagged line.
ensure_host_authorized_key() {
  local pubkey line auth="$HOME/.ssh/authorized_keys"
  command -v git-shell >/dev/null 2>&1 || die "git-shell not found on the host; install git"
  pubkey="$(cat "$KVM_GUEST_KEY.pub")"
  line="command=\"git-shell -c \\\"\$SSH_ORIGINAL_COMMAND\\\"\",restrict $pubkey"
  mkdir -p "$HOME/.ssh"
  touch "$auth"
  chmod 600 "$auth"
  if grep -qF "$line" "$auth"; then
    return 0
  fi
  if grep -qF 'agents-sandbox-kvm-guest' "$auth"; then
    # grep exits 1 when it selects no lines (the only marker line was removed),
    # which is success here; any higher status is a read error and the rewrite
    # must abort rather than move a partial temp file over authorized_keys.
    local rewrite_status=0
    grep -vF 'agents-sandbox-kvm-guest' "$auth" > "$auth.tmp" || rewrite_status=$?
    if [ "$rewrite_status" -gt 1 ]; then
      rm -f "$auth.tmp"
      die "could not rewrite $auth (grep exited $rewrite_status); it is unchanged"
    fi
    mv "$auth.tmp" "$auth"
  fi
  # A last line with no trailing newline (written by printf, some editors, some
  # config managers) would otherwise be merged onto the key line's options, which
  # both breaks the previous key and hides the appended key from sshd.
  if [ -s "$auth" ] && [ -n "$(tail -c 1 "$auth")" ]; then
    printf '\n' >> "$auth"
  fi
  printf '%s\n' "$line" >> "$auth"
}

# probe_guest_to_host_ssh checks that the guest can complete a git exchange with
# the host, so a host without sshd (or one refusing the key) fails with a
# diagnostic instead of an opaque git error. There is no fallback transport. It
# runs a real `git ls-remote`: the key is forced through `git-shell`, which rejects
# any command but git-upload-pack/git-receive-pack, so a plain `ssh … true` would
# always fail on a correctly configured host. Relies on configure_guest_remote
# having set the rewrite and core.sshCommand.
probe_guest_to_host_ssh() {
  local ip="$1" gateway="$2"
  if ! ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=8 -o LogLevel=ERROR \
      "$VM_USER@$ip" \
      "bash -lc 'cd ~/agents-sandbox && git ls-remote origin >/dev/null 2>&1'" \
      >/dev/null 2>&1; then
    die "the guest cannot reach the host over SSH at $KVM_HOST_USER@$gateway for git.
    On the host: run sshd and accept the guest's key (authorized in $HOME/.ssh/authorized_keys),
    and install git-shell. Retry: $0 sync"
  fi
}

# ensure_guest_git_remote makes the host the guest's git remote. Idempotent.
ensure_guest_git_remote() {
  local ip="$1" gateway repo_path
  gateway="$(require_gateway)"
  repo_path="$(host_repo_path)"
  ensure_guest_git_key "$ip"
  configure_guest_remote "$ip" "$gateway" "$repo_path"
  probe_guest_to_host_ssh "$ip" "$gateway"
}

# sync_in brings the guest checkout to the host's branch: it ensures the host is
# the guest's git remote, then, in the guest, fetches the host, switches to the
# branch, and fast-forwards. A diverged branch refuses (never a reset), so the
# agent's work is not clobbered; only committed host work moves — uncommitted
# changes are no longer copied.
sync_in() {
  local ip branch
  branch="${REPO_BRANCH:-main}"
  require_safe_branch "$branch"
  ip="$(require_guest_ip)"
  ensure_guest_git_remote "$ip"
  log "pulling $branch from the host into $VM_USER@$ip:~/agents-sandbox"
  ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
    -- "bash -lc 'set -e && cd ~/agents-sandbox && git fetch origin --prune && git checkout \"$branch\" && git merge --ff-only origin/\"$branch\"'"
}

# sync_back fetches the guest checkout's branches back to the host: by default
# every branch into the refs/remotes/kvm-guest/* namespace (reachable locally
# without touching any local branch), or one branch named by the argument into
# the local branch of the same name (fast-forward only). A branch checked out
# here refuses the fetch — git protects it, because updating the ref without the
# working tree would make the next commit silently revert the fetch — so update
# it by merging the namespace ref instead (`git merge kvm-guest/<name>`). The
# guest keeps its own .git (sync pulls it from the host, it is not copied), so
# this fetch is the fallback return path; the guest's origin already resolves to
# the host, so a plain `git push` works and is the primary path.
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
  require_safe_agent_command "$command"
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
# checkout carries the command. It also gives the guest the host's git identity:
# only `user.name`/`user.email`, streamed over ssh stdin, never the rest of the
# host's ~/.gitconfig (credential helpers, aliases, signing config).
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
  local name email
  name="$(git config --global --get user.name 2>/dev/null || true)"
  email="$(git config --global --get user.email 2>/dev/null || true)"
  if [ -n "$name" ] || [ -n "$email" ]; then
    log "writing the host git identity into $VM_USER@$ip (name and email only)"
    {
      printf '[user]\n'
      if [ -n "$name" ]; then printf '\tname = %s\n' "$name"; fi
      if [ -n "$email" ]; then printf '\temail = %s\n' "$email"; fi
    } | ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" -- "bash -lc 'cat > ~/.gitconfig'"
  else
    log "no host git user.name/user.email; the guest keeps its own git identity"
  fi
}

# gh_auth signs the guest's `gh` in to GitHub from the host environment's
# GITHUB_PAT, so the in-guest agent can read and comment on issues and PRs. The
# guest's git targets the host (see ensure_guest_git_remote), not GitHub, so no
# GitHub remote rewrite is set up here. It is a no-op when GITHUB_PAT is unset,
# and the token rides stdin, never argv. It must run after `agent_provision`,
# which rewrites the guest ~/.gitconfig.
gh_auth() {
  local ip="$1"
  if [ -z "${GITHUB_PAT:-}" ]; then
    return 0
  fi
  log "signing gh in to GitHub in '$VM_NAME'"
  if ! printf '%s\n' "$GITHUB_PAT" | ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
      -- "bash -lc 'gh auth login --with-token && gh auth setup-git'"; then
    die "gh sign-in failed in '$VM_NAME'; check the token (GITHUB_PAT), or rebuild a guest that predates gh: make kvm-down && make kvm-up"
  fi
}

# ensure_running brings the guest to a running state without recreating an
# existing domain: undefined is created, a stopped domain is started (sharing
# the boot path's lease wait), a running one is left alone. `create`'s
# check_host refuses an existing domain, so it is never called over one.
ensure_running() {
  if ! virsh_cmd dominfo "$VM_NAME" >/dev/null 2>&1; then
    create
    return
  fi
  if [ "$(virsh_cmd domstate "$VM_NAME" 2>/dev/null | tr -d '[:space:]')" = "running" ]; then
    log "guest '$VM_NAME' is running"
    return
  fi
  local ip
  log "starting the stopped guest '$VM_NAME'"
  # A host reboot drops the default network and the firewall accepts the guest
  # egress needs, so the start path re-ensures both, matching `create`.
  ensure_default_network
  virsh_cmd start "$VM_NAME" >/dev/null
  ip="$(wait_for_lease)"
  probe_guest_egress "$ip"
}

# wait_for_cloud_init gates on the guest being provisioned: it retries the SSH
# connect (sshd accepts well after the DHCP lease appears), then runs
# `cloud-init status --wait`, failing when cloud-init reports an error.
wait_for_cloud_init() {
  local ip="$1" attempt=0 status
  while [ "$attempt" -lt "$KVM_CLOUD_INIT_ATTEMPTS" ]; do
    if ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=8 \
        -o LogLevel=ERROR "$VM_USER@$ip" true >/dev/null 2>&1; then
      break
    fi
    attempt=$((attempt + 1))
    sleep "$KVM_CLOUD_INIT_INTERVAL"
  done
  [ "$attempt" -lt "$KVM_CLOUD_INIT_ATTEMPTS" ] \
    || die "guest ssh did not come up; check the console: virsh -c $LIBVIRT_URI console $VM_NAME"
  log "waiting for cloud-init to finish in '$VM_NAME'"
  if ! status="$(ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=8 \
        "$VM_USER@$ip" 'cloud-init status --wait' 2>&1)"; then
    printf '%s\n' "$status" >&2
    die "cloud-init failed in '$VM_NAME'; inspect: $0 run 'cloud-init status --long'"
  fi
  log "cloud-init: done"
}

# guest_agent_session_state prints "present" or "absent" for the agent's tmux
# session, and returns non-zero only when the guest could not be reached. A
# failed probe must not read as "absent": the caller pulls on "absent", which
# checks out the host branch and would disturb a live session's work.
guest_agent_session_state() {
  local ip="$1" session="${KVM_AGENT_SESSION:-agents-sandbox}"
  ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=8 \
    -o LogLevel=ERROR "$VM_USER@$ip" \
    "tmux has-session -t $session >/dev/null 2>&1 && echo present || echo absent" 2>/dev/null
}

# agent_up is the one-command bring-up: ensure the guest is running, gate on
# cloud-init, then — checking for a live session only now that the guest is
# reachable — pull the host branch and materialize the agent config (or, for a
# live session, reattach without syncing), sign gh in from GITHUB_PAT, and
# attach. It composes the subcommands so the step-by-step targets remain
# available.
agent_up() {
  local ip session_state
  require_safe_agent_command "${*:-opencode2}"
  ensure_running
  ip="$(require_guest_ip)"
  wait_for_cloud_init "$ip"
  session_state="$(guest_agent_session_state "$ip" || true)"
  case "$session_state" in
    present)
      log "agent session '${KVM_AGENT_SESSION:-agents-sandbox}' is already running; reattaching without syncing"
      ;;
    absent)
      sync_in
      agent_provision "${KVM_AGENT_NAME:-opencode2}"
      ;;
    *)
      die "could not determine whether an agent session is running in '$VM_NAME' (probe returned '$session_state'); refusing to sync over it"
      ;;
  esac
  gh_auth "$ip"
  agent_in "$@"
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

Then, on the host, pull the host branch into the guest and run the suite:

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

  local ip=""
  ip="$(wait_for_lease)"
  print_next_steps "$ip"
  probe_guest_egress "$ip"
}

# wait_for_lease blocks until the guest has a DHCP lease, printing progress, and
# warns (printing nothing) on timeout. It is the boot gate `create` and the
# ensure-running path share.
wait_for_lease() {
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
  printf '%s\n' "$ip"
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
  agent-up)        shift; agent_up "$@" ;;
  agent-provision) shift; agent_provision "$@" ;;
  sync-back)       shift; sync_back "$@" ;;
  firewall|fix-net) fix_firewall ;;
  down|destroy)    destroy ;;
  *) usage >&2; exit 2 ;;
esac
