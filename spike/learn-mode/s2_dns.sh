#!/usr/bin/env bash
# S2 — is DNS worth keeping as corroboration? Host-only.
#
#   ./spike/learn-mode/s2_dns.sh public
#   ./spike/learn-mode/s2_dns.sh none-allow
#
# Starts a logging dnsmasq on the host, points the sandbox's DNS forwarder at
# it, and drives a few consumers. Inspect the captured dns.log for which queries
# arrived and from which case.
#   DNS_UPSTREAM=127.0.0.1   override the resolver the sandbox forwards to
set -euo pipefail
# shellcheck source=lib.sh disable=SC1091
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

spike_require msb dnsmasq curl
variant="${1:-public}"
dns_upstream="${DNS_UPSTREAM:-127.0.0.1}:$DNS_PORT"
name="$(spike_sandbox_name s2 "$variant")"
dir="$(spike_capture_dir)"

spike_dns_up
: >"$dir/dns-$variant.log"

net_args=(--dns-nameserver "$dns_upstream")
case "$variant" in
  public) net_args+=(--net public) ;;
  none-allow)
    net_args+=(--net-default-egress deny --net-strict=false
      --net-rule 'allow@dns'
      --net-rule 'allow@example.com,allow@github.com'
      --net-rule 'allow@dl-cdn.alpinelinux.org,allow@mirror.leaseweb.com')
    ;;
  *) echo "usage: $0 [public|none-allow]" >&2; exit 2 ;;
esac

trap 'spike_rm "$name"; spike_dns_down' EXIT

spike_create "$name" alpine "${net_args[@]}"
spike_exec "$name" sh -lc '
  curl -sS -m 15 -o /dev/null -w "curl example.com -> %{http_code}\n" https://example.com || echo "curl example.com failed"
  curl -sS -m 15 -o /dev/null -w "curl denied.example.net -> %{http_code}\n" https://denied.example.net || echo "curl denied.example.net failed (expected under none-allow)"
  apk update >/dev/null 2>&1 && echo "apk update ok" || echo "apk update failed"
  git ls-remote https://github.com/git/git HEAD >/dev/null 2>&1 && echo "git fetch ok" || echo "git fetch failed"
'

echo "--- $variant: queries seen by the host listener ---"
grep -iE 'query|reply' "$dir/dns.log" 2>/dev/null | tail -200 | tee "$dir/dns-$variant.log"
echo
echo "Record: did every consumer's query arrive? Did the container engine's? What did a"
echo "not-in-allow host produce under none (NXDOMAIN / timeout / forwarded-then-blocked)?"
echo "The docker-build pull case is exercised in the launcher VM (S1), not here."
