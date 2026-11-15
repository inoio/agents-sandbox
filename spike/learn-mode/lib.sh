#!/usr/bin/env bash
# Shared helpers for the learn-mode observation spike (host-only).
#
# Source this file; do not execute it:
#   source spike/learn-mode/lib.sh
#
# It assumes `msb`, `mitmdump`, and (for S2) `dnsmasq` are installed on the host.

# Resolve the directory holding this file so drivers can be run from anywhere.
spike_self_dir() {
  local source="${BASH_SOURCE[0]}"
  while [ -L "$source" ]; do
    local dir
    dir="$(cd -P "$(dirname "$source")" && pwd)"
    source="$(readlink "$source")"
    [[ $source != /* ]] && source="$dir/$source"
  done
  cd -P "$(dirname "$source")" && pwd
}

SPIKE_DIR="$(spike_self_dir)"
SPIKE_ROOT="$(cd "$SPIKE_DIR/../.." && pwd)"
MSB="${MSB:-msb}"
PROXY_PORT="${PROXY_PORT:-8088}"
DNS_PORT="${DNS_PORT:-5354}"
SPIKE_CAPTURE_DIR="${SPIKE_CAPTURE_DIR:-$SPIKE_ROOT/spike-capture}"

spike_require() {
  local tool
  for tool in "$@"; do
    command -v "$tool" >/dev/null 2>&1 || {
      echo "spike: '$tool' not found on PATH (see the runbook's Prerequisites)" >&2
      return 1
    }
  done
}

spike_capture_dir() {
  mkdir -p "$SPIKE_CAPTURE_DIR"
  printf '%s\n' "$SPIKE_CAPTURE_DIR"
}

# The address a guest uses to reach the host. The microsandbox runtime dials a
# proxy host-side, so 127.0.0.1 usually works; this probes the gateway form for
# the cases where it does not. Override by exporting HOST_GATEWAY_IP.
spike_host_ip() {
  if [ -n "${HOST_GATEWAY_IP:-}" ]; then
    printf '%s\n' "$HOST_GATEWAY_IP"
    return
  fi
  local cached="$SPIKE_CAPTURE_DIR/host-gateway-ip"
  if [ -s "$cached" ]; then
    cat "$cached"
    return
  fi
  spike_capture_dir >/dev/null
  local ip
  ip="$("$MSB" run alpine --net-rule allow@host -- sh -lc \
    'getent hosts host.microsandbox.internal || nslookup host.microsandbox.internal' 2>/dev/null |
    awk '{ for (i = 1; i <= NF; i++) if ($i ~ /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/) { print $i; exit } }')"
  [ -n "$ip" ] || {
    echo "spike: could not resolve host.microsandbox.internal; export HOST_GATEWAY_IP" >&2
    return 1
  }
  printf '%s\n' "$ip" | tee "$cached"
}

# spike_proxy_up [blind|terminate]
# Starts mitmdump as a regular HTTP proxy on 0.0.0.0:$PROXY_PORT, logging flows.
# blind: pass CONNECT through without intercepting (no trust needed), still logs
#        the destination.
# terminate: intercept TLS with mitmproxy's CA (plant it in the guest), so the
#        request target and credential-bearing header names are visible.
spike_proxy_up() {
  local mode="${1:-blind}" dir log pidfile
  dir="$(spike_capture_dir)"
  log="$dir/proxy-$mode.log"
  pidfile="$dir/proxy-$mode.pid"
  spike_proxy_down "$mode" >/dev/null 2>&1 || true
  local args=(--mode regular --listen-host 0.0.0.0 --listen-port "$PROXY_PORT"
    -w "$dir/proxy-$mode.flows" --set "confdir=$dir/mitmproxy" --set flow_detail=0)
  if [ "$mode" = blind ]; then
    args+=(--ignore-hosts '.*')
  fi
  mitmdump "${args[@]}" >"$log" 2>&1 &
  echo $! >"$pidfile"
  sleep 1
  if ! kill -0 "$(cat "$pidfile")" 2>/dev/null; then
    echo "spike: mitmdump failed to start; see $log" >&2
    tail -5 "$log" >&2
    return 1
  fi
  if [ "$mode" = terminate ]; then
    local ca="$dir/mitmproxy/mitmproxy-ca-cert.pem"
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      [ -f "$ca" ] && break
      sleep 1
    done
    [ -f "$ca" ] || echo "spike: mitmproxy CA still absent at $ca after 10s" >&2
  fi
  echo "proxy up ($mode) on 0.0.0.0:$PROXY_PORT, flows -> $dir/proxy-$mode.flows"
}

spike_proxy_down() {
  local mode="${1:-blind}" dir pidfile
  dir="$(spike_capture_dir)"
  pidfile="$dir/proxy-$mode.pid"
  [ -s "$pidfile" ] || return 0
  kill "$(cat "$pidfile")" 2>/dev/null || true
  rm -f "$pidfile"
}

# Render a recorded flow file to text (hosts, paths, header names/values) with
# `mitmdump -nr`. Run after spike_proxy_down so the file is flushed. Header
# values are printed too — redact secrets before pasting anywhere.
spike_proxy_render() {
  local mode="${1:-blind}" dir
  dir="$(spike_capture_dir)"
  mitmdump -nr "$dir/proxy-$mode.flows" --set flow_detail=4 >"$dir/proxy-$mode.txt" 2>/dev/null || true
  echo "rendered -> $dir/proxy-$mode.txt"
}

# Absolute path to the terminating proxy's CA certificate.
spike_proxy_ca() {
  local dir
  dir="$(spike_capture_dir)"
  printf '%s\n' "$dir/mitmproxy/mitmproxy-ca-cert.pem"
}

# spike_dns_up — logs every query dnsmasq receives to the capture dir.
spike_dns_up() {
  local dir
  dir="$(spike_capture_dir)"
  spike_dns_down >/dev/null 2>&1 || true
  dnsmasq --conf-file=/dev/null --port="$DNS_PORT" --listen-address=0.0.0.0 \
    --no-hosts --no-resolv --server=1.1.1.1 --log-queries \
    --log-facility="$dir/dns.log" --pid-file="$dir/dns.pid"
  echo "dns listener up on 0.0.0.0:$DNS_PORT, queries -> $dir/dns.log"
}

spike_dns_down() {
  local dir pidfile
  dir="$(spike_capture_dir)"
  pidfile="$dir/dns.pid"
  [ -s "$pidfile" ] || return 0
  kill "$(cat "$pidfile")" 2>/dev/null || true
  rm -f "$pidfile"
}

# spike_create <name> <msb create args...>  (image + flags; --name/--replace added)
spike_create() {
  local name="$1"
  shift
  "$MSB" create "$@" --name "$name" --replace >/dev/null
}

spike_exec() {
  local name="$1"
  shift
  "$MSB" exec "$name" -- "$@"
}

spike_rm() {
  "$MSB" remove "$1" >/dev/null 2>&1 || true
}

# spike_dump_logs <name> — all sources (stdout/stderr/output/system) to a file.
spike_dump_logs() {
  local name="$1" dir
  dir="$(spike_capture_dir)"
  "$MSB" logs --source all --timestamps "$name" >"$dir/$name.logs" 2>&1 || true
  echo "logs -> $dir/$name.logs"
}

# spike_dump_metrics <name> — live metrics snapshot to a file.
spike_dump_metrics() {
  local name="$1" dir
  dir="$(spike_capture_dir)"
  "$MSB" metrics --format json "$name" >"$dir/$name.metrics.json" 2>&1 || true
  echo "metrics -> $dir/$name.metrics.json"
}

# spike_sandbox_name <experiment> [suffix]
spike_sandbox_name() {
  printf 'spike-%s-%s\n' "${1:?experiment}" "${2:-$(date +%s)}"
}
