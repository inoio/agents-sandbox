#!/usr/bin/env bash
# S7 — what does proxying cost? Host-only.
#
#   ./spike/learn-mode/s7_cost.sh
#
# Times the same work direct and through the terminating CONNECT proxy, in two
# sandboxes (the proxy is creation-only). The real agent turn belongs to the
# launcher, but the launcher has no `proxy:` surface yet (#149) — so this
# measures the transport with curl/git/npm and leaves the agent turn as a
# follow-up once #149 lands. Streaming is checked with `curl -N`.
set -euo pipefail
# shellcheck source=lib.sh disable=SC1091
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

spike_require msb mitmdump curl
dir="$(spike_capture_dir)"
proxy_ip="${PROXY_IP:-127.0.0.1}"
direct="$(spike_sandbox_name s7-direct)"
proxied="$(spike_sandbox_name s7-proxied)"

# shellcheck disable=SC2016 # $((end - start)) must run in the guest shell
body='
  apk add --no-cache curl git >/dev/null 2>&1
  echo "== streaming read (first-byte + total) =="
  curl -sS -N -m 30 -o /dev/null -w "starttransfer=%{time_starttransfer}s total=%{time_total}s\n" \
    https://raw.githubusercontent.com/git/git/master/README.md || echo failed
  echo "== large-ish fetch =="
  curl -sS -m 60 -o /dev/null -w "total=%{time_total}s size=%{size_download}\n" \
    https://registry.npmjs.org/typescript/-/typescript-5.4.5.tgz || echo failed
  echo "== git clone --depth 1 =="
  start="$(date +%s)"; git clone --depth 1 https://github.com/git/git /tmp/g >/dev/null 2>&1 || true; end="$(date +%s)"
  echo "wall=$((end - start))s"
'

spike_proxy_up terminate
trap 'spike_rm "$direct"; spike_rm "$proxied"; spike_proxy_down terminate' EXIT
ca="$dir/mitmproxy/mitmproxy-ca-cert.pem"
msb_trust="$SPIKE_ROOT/internal/sandbox/image/data/msb-trust.sh"
[ -f "$ca" ] || { echo "mitmproxy CA missing: $ca" >&2; exit 1; }

echo "== direct =="
spike_create "$direct" alpine --net public
spike_exec "$direct" sh -lc "$body" | tee "$dir/s7-direct.txt"

echo
echo "== through the terminating proxy =="
spike_create "$proxied" alpine --proxy "http://$proxy_ip:$PROXY_PORT" --net public \
  --copy-file "$ca:/tmp/spike-ca.pem" \
  --copy-file "$msb_trust:/usr/local/bin/msb-trust" \
  --copy-file "$SPIKE_DIR/guest/plant-ca.sh:/tmp/plant-ca.sh"
spike_exec "$proxied" sh -lc '
  chmod +x /usr/local/bin/msb-trust /tmp/plant-ca.sh
  sh /tmp/plant-ca.sh /tmp/spike-ca.pem >/dev/null 2>&1
  '"$body" | tee "$dir/s7-proxied.txt"

echo
echo "== proxy host CPU during the run =="
ps -o pid,pcpu,pmem,comm -p "$(cat "$dir/proxy-terminate.pid")" 2>/dev/null | tee "$dir/s7-proxy-cpu.txt" || true

echo
echo "Record overhead per operation, whether streaming stayed unbuffered, host CPU, and"
echo "whether mitmdump ran unprivileged. Then run the same turn with a real agent once"
echo "#149 exposes the proxy to the launcher."
