#!/usr/bin/env bash
# S5 — are denials observable? Host-only.
#
#   ./spike/learn-mode/s5_denials.sh
#
# Denies a known host under `none`, attempts it, then inspects everything the
# launcher can read: msb logs (all sources), msb metrics, and the guest's own
# HTTP response in JSON deny mode. Whether the *runtime's interception* is also
# observable is a separate sub-step (D), driven by d_runtime_interception.sh.
set -euo pipefail
# shellcheck source=lib.sh disable=SC1091
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

spike_require msb curl
dir="$(spike_capture_dir)"

cat >"$dir/deny-json.yaml" <<'YAML'
http:
  deny_response: true
  deny_response_format: json
YAML

net_args=(--net-default-egress deny --net-strict=false
  --net-rule 'allow@dns' --net-rule 'allow@example.com')

run_case() {
  local desc="$1"
  shift
  local name
  name="$(spike_sandbox_name s5)"
  {
    echo "### $desc"
    spike_create "$name" alpine "$@"
    spike_exec "$name" sh -lc \
      'curl -sS -m 15 -w "\n-> %{http_code}\n" https://denied.example.net/ ; echo "curl exit=$?"' 2>&1 || true
  } | tee "$dir/s5-$desc.guest.txt"
  spike_dump_logs "$name"
  spike_dump_metrics "$name"
  echo "-- matches for 'denied.example.net' in the launcher-readable logs --"
  grep -i 'denied.example.net' "$dir/$name.logs" "$dir/$name.metrics.json" 2>/dev/null | tee "$dir/s5-$desc.launcher.txt" || echo "(none)"
  spike_rm "$name"
  echo
}

run_case "none-text" --conf "$dir/deny-json.yaml" "${net_args[@]}"

echo "Record where (if anywhere) the denied destination appears: msb logs --source system,"
echo "system/all, msb metrics, or only the guest's response. For whether the runtime's own"
echo "interception is observable (approach D), run d_runtime_interception.sh."
