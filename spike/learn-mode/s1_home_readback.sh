#!/usr/bin/env bash
# S1 — dump what a harness's home volume contains. Host-only.
#
#   ./spike/learn-mode/s1_home_readback.sh --list
#   ./spike/learn-mode/s1_home_readback.sh <home-volume-name>
#
# The interactive part (configure the harness, send one prompt, exit) is manual;
# see the runbook. Run this with the VM stopped as well as running, and diff the
# two listings to catch files that only exist while the VM lives.
set -euo pipefail
# shellcheck source=lib.sh disable=SC1091
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

asb="$SPIKE_ROOT/agents-sandbox"
[ -x "$asb" ] || { echo "build the launcher first: make build" >&2; exit 1; }
spike_require jq

if [ "${1:-}" = "--list" ] || [ $# -eq 0 ]; then
  "$asb" volume list -q
  exit 0
fi

vol="$1"
dir="$(spike_capture_dir)"
path="$("$asb" volume inspect --format json "$vol" | jq -r .path)"
[ -d "$path" ] || { echo "volume path not a directory: $path" >&2; exit 1; }
out="$dir/s1-$vol.txt"

list_files() {
  find "$path" -type f 2>/dev/null | sort | while IFS= read -r f; do
    printf '%10s  %s\n' "$(wc -c <"$f" 2>/dev/null || echo '?')" "${f#"$path"/}"
  done
}

{
  echo "# S1 home-volume readback: $vol"
  echo "path:  $path"
  echo "when:  $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  echo "## files (bytes, path-relative)"
  list_files
  echo
  echo "## likely agent-config files"
  find "$path" -type f \( -name '*.json' -o -name '*.jsonc' -o -name '*.yaml' -o -name '*.yml' \
    -o -name '*.toml' -o -name 'config*' -o -name 'settings*' \) 2>/dev/null | sort | sed "s#^$path/##"
  echo
  echo "## likely credential files (inline vs referenced)"
  find "$path" -type f 2>/dev/null | grep -iE '/(auth|credential|token|secret|key|\.netrc|oauth|account)' \
    | sort | sed "s#^$path/##"
  echo
  echo "## hosts / URLs mentioned in text files"
  grep -rIn --exclude-dir=.git -E 'https?://[a-zA-Z0-9._-]+' "$path" 2>/dev/null \
    | sed "s#^$path/##" | sort -u | head -200
  echo
  echo "## denial / block / forbidden / 403 mentions"
  grep -rIn --exclude-dir=.git -iE 'denied|blocked|forbidden|\b403\b|not allowed' "$path" 2>/dev/null \
    | sed "s#^$path/##" | sort -u | head -100
} | tee "$out"

echo
echo "recorded -> $out"
echo "Record per the runbook: which files hold config, credentials (inline vs referenced),"
echo "transcripts with hosts, logs with denials, and anything unreadable or changing."
