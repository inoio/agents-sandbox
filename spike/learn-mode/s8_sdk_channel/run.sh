#!/usr/bin/env bash
# Run the S1 SDK channel-semantics probe. Host-only: it needs a KVM host with
# the microsandbox runtime. One command, as the plan asks; the cases are
# self-contained (the observer is in-process), so there is no proxy to start.
#
#   ./spike/learn-mode/s8_sdk_channel/run.sh              # all cases
#   ./spike/learn-mode/s8_sdk_channel/run.sh -case q2     # one case
#
# Overridable: SPIKE_IMAGE, SPIKE_ALLOWED_HOST, SPIKE_BYPASS_HOST, SPIKE_MSB_HOME.
set -euo pipefail
cd "$(dirname "$0")"

command -v go >/dev/null 2>&1 || { echo "run.sh: go not found on PATH" >&2; exit 1; }

# microsandbox refuses a storage root with a group-/other-writable ancestor; the
# umask keeps directories this run creates from becoming one. An existing
# writable directory is not repaired by the umask, so warn about it by name.
umask 022
home="${SPIKE_MSB_HOME:-$HOME/.microsandbox-spike}"
dir="$home"
while [ -n "$dir" ] && [ "$dir" != "/" ]; do
  if [ -e "$dir" ] && find "$dir" -maxdepth 0 -perm /022 -print -quit 2>/dev/null | grep -q .; then
    echo "run.sh: $dir is group/other-writable; microsandbox refuses a storage root under it." >&2
    echo "run.sh: fix with: chmod g-w,o-w $dir" >&2
  fi
  dir="$(dirname "$dir")"
done

echo "s8_sdk_channel: image=${SPIKE_IMAGE:-alpine:3.20} allowed=${SPIKE_ALLOWED_HOST:-example.com} bypass=${SPIKE_BYPASS_HOST:-www.iana.org}"
exec go run . "$@"
