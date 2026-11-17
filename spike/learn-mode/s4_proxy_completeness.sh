#!/usr/bin/env bash
# S4 — with the proxy configured, what still bypasses it? Host-only.
#
#   ./spike/learn-mode/s4_proxy_completeness.sh
#
# Runs one sandbox under `public` (so direct egress is permitted) with the blind
# proxy configured, and attempts a spread of traffic kinds. The question is not
# whether each succeeds but whether the proxy log saw it; anything absent reached
# the network directly.
set -euo pipefail
# shellcheck source=lib.sh disable=SC1091
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

spike_require msb mitmdump curl
dir="$(spike_capture_dir)"
proxy_ip="${PROXY_IP:-127.0.0.1}"
name="$(spike_sandbox_name s4)"

spike_proxy_up blind
trap 'spike_rm "$name"; spike_proxy_down blind' EXIT

spike_create "$name" alpine --proxy "http://$proxy_ip:$PROXY_PORT" --net public
spike_exec "$name" sh -lc '
  apk add --no-cache curl git openssh-client >/dev/null 2>&1 || true
  echo "== ip literal =="
  curl -sS -m 15 -o /dev/null -w "%{http_code}\n" https://1.1.1.1/ || echo "failed"
  echo "== http/3 (quic) =="
  curl -sS --http3 -m 15 -o /dev/null -w "%{http_code}\n" https://cloudflare-quic.com/ 2>&1 || echo "failed (curl may lack HTTP/3)"
  echo "== git https =="
  git ls-remote https://github.com/git/git HEAD >/dev/null 2>&1 && echo "ok" || echo "failed"
  echo "== ssh =="
  ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -T git@github.com 2>&1 | head -2 || true
  echo "== package install =="
  apk add --no-cache jq >/dev/null 2>&1 && echo "ok" || echo "failed"
' | tee "$dir/s4-guest.txt"

spike_dump_logs "$name"
spike_proxy_down blind
spike_proxy_render blind

echo
echo "Record which of {ip literal, quic, git https, ssh, package install, docker build pull}"
echo "appear in the proxy log and which reach the network directly. The docker build pull"
echo "needs a dockerd in the guest; run it in the launcher VM (S1) if you want that case."
