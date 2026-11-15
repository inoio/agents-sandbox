#!/usr/bin/env bash
# S6 — can the launcher terminate TLS and attribute a credential? Host-only.
#
#   ./spike/learn-mode/s6_tls_termination.sh
#
# The pivotal experiment. It plants mitmproxy's CA in the guest three ways and
# drives a spread of consumers through the terminating proxy, then repeats with a
# configured secret to observe the collision with the runtime's own interception.
# Whether the runtime's interception is itself observable (approach D) is a
# separate sub-step, driven by d_runtime_interception.sh. Header values land in
# the rendered flows — redact secrets before pasting results anywhere.
set -euo pipefail
# shellcheck source=lib.sh disable=SC1091
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

spike_require msb mitmdump curl
dir="$(spike_capture_dir)"
proxy_ip="${PROXY_IP:-127.0.0.1}"
name="$(spike_sandbox_name s6)"
msb_trust="$SPIKE_ROOT/internal/sandbox/image/data/msb-trust.sh"

# Starting the terminating proxy once generates its CA under $dir/mitmproxy/.
spike_proxy_up terminate
trap 'spike_rm "$name"; spike_proxy_down terminate' EXIT
ca="$dir/mitmproxy/mitmproxy-ca-cert.pem"
[ -f "$ca" ] || { echo "mitmproxy CA not found at $ca" >&2; exit 1; }

echo "== 1. plant mitmproxy CA three ways, then drive consumers via the terminating proxy =="
spike_create "$name" alpine \
  --proxy "http://$proxy_ip:$PROXY_PORT" --net public \
  --copy-file "$ca:/tmp/spike-ca.pem" \
  --copy-file "$msb_trust:/usr/local/bin/msb-trust" \
  --copy-file "$SPIKE_DIR/guest/plant-ca.sh:/tmp/plant-ca.sh"

spike_exec "$name" sh -lc '
  chmod +x /usr/local/bin/msb-trust /tmp/plant-ca.sh
  sh /tmp/plant-ca.sh /tmp/spike-ca.pem
  apk add --no-cache curl git nodejs npm py3-pip >/dev/null 2>&1 || echo "apk add failed"
  echo "== curl =="; curl -sS -m 20 -o /dev/null -w "%{http_code}\n" https://example.com/ || echo failed
  echo "== git https =="; git ls-remote https://github.com/git/git HEAD >/dev/null 2>&1 && echo ok || echo failed
  echo "== npm =="; npm ping --registry=https://registry.npmjs.org >/dev/null 2>&1 && echo ok || echo failed
  echo "== pip =="; (pip install --quiet --disable-pip-version-check requests || pip3 install --quiet --disable-pip-version-check requests) && echo ok || echo failed
  echo "== curl with a static credential (header-name visibility) =="
  curl -sS -m 20 -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer spike-static-key" -H "X-Api-Key: spike-static-key" https://example.com/ || echo failed
' | tee "$dir/s6-consumers.txt"

spike_dump_logs "$name"

echo
echo "== 4. collision: a configured secret turns on the runtime interception too =="
export SPIKE_TOKEN="${SPIKE_TOKEN:-spike-token-0123456789}"
coll="$name-collision"
# A secret makes the runtime intercept too. The guest trusts the runtime's CA
# (agentd installs it in the system bundle), but the runtime is now the client to
# OUR terminating proxy, so it must trust mitmproxy's CA: --tls-upstream-ca-cert.
spike_create "$coll" alpine --proxy "http://$proxy_ip:$PROXY_PORT" \
  --net-default-egress deny --net-strict=false --net-rule 'allow@example.com' \
  --tls-upstream-ca-cert "$ca" \
  --secret "SPIKE_TOKEN@example.com" \
  --copy-file "$ca:/tmp/spike-ca.pem" \
  --copy-file "$SPIKE_DIR/guest/plant-ca.sh:/tmp/plant-ca.sh"
spike_exec "$coll" sh -lc '
  sh /tmp/plant-ca.sh /tmp/spike-ca.pem >/dev/null 2>&1
  env | grep SPIKE_TOKEN || true
  echo "== wget with Authorization placeholder =="
  wget -q -O /dev/null -T 20 --header "Authorization: $SPIKE_TOKEN" https://example.com/ && echo "wget ok" || echo "wget failed"
' | tee "$dir/s6-collision.txt"
spike_dump_logs "$coll"
spike_rm "$coll"

spike_proxy_down terminate
spike_proxy_render terminate
echo "-- credential-bearing header names seen by the proxy --"
grep -ioE '^[[:space:]]*(authorization|x-api-key|cookie|api-key|proxy-authorization):' "$dir/proxy-terminate.txt" 2>/dev/null \
  | sed -E 's/^[[:space:]]*//' | sort | uniq -c | tee "$dir/s6-headers.txt" || echo "(none)"

echo
echo "Record: which consumers trust the CA and which break; whether the header name is"
echo "visible (authorization / x-api-key / cookie); whether streaming survives; whether the"
echo "runtime's interception collides or can be kept off; pinning; and whether the CA"
echo "survives a VM recreate (it is image state). For approach D, run"
echo "d_runtime_interception.sh; the two questions there are separate."
