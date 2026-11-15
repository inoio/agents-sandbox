#!/usr/bin/env bash
# S3 — is the outbound proxy reachable, and under which policy? Host-only.
#
#   ./spike/learn-mode/s3_proxy_reachability.sh
#
# Starts a blind CONNECT proxy on the host and tries it as the sandbox's
# outbound proxy across {address} x {protocol} x {policy}. The runtime dials the
# proxy host-side, so 127.0.0.1 is the natural address; the gateway form is
# tried for completeness. A `none` policy names only the destination host
# (example.com) and, in the -allow-proxy cases, the proxy itself.
set -euo pipefail
# shellcheck source=lib.sh disable=SC1091
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

spike_require msb mitmdump curl
dir="$(spike_capture_dir)"
proxy_ip="${PROXY_IP:-127.0.0.1}"
gw_ip="$(spike_host_ip)"

spike_proxy_up blind
trap 'spike_proxy_down blind' EXIT

run_case() {
  local desc="$1" addr="$2" proto="$3"
  shift 3
  local name
  name="$(spike_sandbox_name s3)"
  {
    echo "### $desc"
    echo "    proxy: $proto://$addr:$PROXY_PORT"
    echo "    policy: $*"
    if spike_create "$name" alpine --proxy "$proto://$addr:$PROXY_PORT" "$@" >/dev/null 2>&1; then
      echo "    create: ok"
    else
      echo "    create: FAILED"
      return
    fi
    if spike_exec "$name" sh -lc \
      'curl -sS -m 15 -o /dev/null -w "    curl https://example.com -> %{http_code}\n" https://example.com' 2>&1; then
      echo "    curl: ok"
    else
      echo "    curl: FAILED (denied, or proxy unreachable)"
    fi
  } | tee "$dir/s3-$desc.txt"
  spike_rm "$name"
}

for addr in "$proxy_ip" "$gw_ip"; do
  for proto in http socks5; do
    run_case "none-$proto-$addr" "$addr" "$proto" \
      --net-default-egress deny --net-strict=false --net-rule 'allow@dns' --net-rule 'allow@example.com'
    run_case "none-allowproxy-$proto-$addr" "$addr" "$proto" \
      --net-default-egress deny --net-strict=false --net-rule 'allow@dns' --net-rule 'allow@example.com' \
      --net-rule "allow@$addr:tcp:$PROXY_PORT"
    run_case "public-$proto-$addr" "$addr" "$proto" --net public
    run_case "public-host-$proto-$addr" "$addr" "$proto" --net public,host
  done
done

spike_proxy_down blind
spike_proxy_render blind
echo
echo "Record which combinations connected; whether denial was silent at the guest or"
echo "reported; whether the proxy dial bypassed the policy entirely. The vsock variant"
echo "is optional: it needs a guest-side vsock->TCP bridge, so record it as not tested."
