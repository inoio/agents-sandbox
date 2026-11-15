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
#
# The client-side probe is BusyBox `wget`, not `curl`: under `none` the guest
# cannot `apk add` a client without widening the policy, and socks5 needs no
# client-side SOCKS support (the runtime dials the proxy transparently).
#
# Note on the `-allow-proxy` cases: because the runtime dials the proxy
# host-side, a guest-visible `allow@<proxy-addr>:tcp:<port>` rule is almost
# certainly a no-op — the proxy address never appears as a guest destination.
# The rule is kept for the record, not because it is expected to matter.
set -euo pipefail
# shellcheck source=lib.sh disable=SC1091
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

spike_require msb mitmdump
dir="$(spike_capture_dir)"
proxy_ip="${PROXY_IP:-127.0.0.1}"
gw_ip="$(spike_host_ip)"

# A SOCKS5 dial the runtime cannot complete stalls the guest past wget's own
# timeout; cap the exec so one wedged case cannot stall the sweep.
SPIKE_EXEC_TIMEOUT="${SPIKE_EXEC_TIMEOUT:-60}"

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
      'wget -q -O- -T 15 https://example.com >/dev/null && echo "    wget https://example.com -> ok"' 2>&1; then
      echo "    wget: ok"
    else
      echo "    wget: FAILED (denied, proxy unreachable, or timed out)"
    fi
  } | tee "$dir/s3-$desc.txt"
  spike_rm "$name"
}

for addr in "$proxy_ip" "$gw_ip"; do
  for proto in http socks5; do
    run_case "none-$proto-$addr" "$addr" "$proto" \
      --net-default-egress deny --net-strict=false --net-rule 'allow@example.com'
    run_case "none-allowproxy-$proto-$addr" "$addr" "$proto" \
      --net-default-egress deny --net-strict=false --net-rule 'allow@example.com' \
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
