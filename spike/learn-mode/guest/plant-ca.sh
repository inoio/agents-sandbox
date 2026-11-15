#!/bin/sh
# Install a PEM certificate authority inside a sandbox, three ways, so every
# class of TLS consumer trusts it:
#   1. append to /.msb/tls/ca.pem — the file agentd points SSL_CERT_FILE,
#      REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE, and NODE_EXTRA_CA_CERTS at;
#   2. install into the system store (/usr/local/share/ca-certificates + update-ca-certificates);
#   3. import with msb-trust for Java (cacerts) and Chrome (NSS), if present.
#
# Usage: sh plant-ca.sh /path/to/ca.pem
set -eu

pem="${1:?usage: plant-ca.sh <ca.pem>}"
[ -f "$pem" ] || { echo "plant-ca: no such file: $pem" >&2; exit 1; }

# 1. agentd's combined CA file.
mkdir -p /.msb/tls
if [ -f /.msb/tls/ca.pem ]; then
  cat "$pem" >>/.msb/tls/ca.pem
  echo "plant-ca: appended to /.msb/tls/ca.pem"
else
  cp "$pem" /.msb/tls/ca.pem
  echo "plant-ca: created /.msb/tls/ca.pem"
fi

# 2. System store (Debian/Alpine tooling).
mkdir -p /usr/local/share/ca-certificates
cp "$pem" /usr/local/share/ca-certificates/spike-ca.crt
if command -v update-ca-certificates >/dev/null 2>&1; then
  if update-ca-certificates >/dev/null 2>&1; then
    echo "plant-ca: installed into the system store"
  else
    echo "plant-ca: system-store update failed (continuing)" >&2
  fi
else
  echo "plant-ca: update-ca-certificates absent; skipped the system store" >&2
fi

# 3. Application stores the system store cannot reach.
if command -v msb-trust >/dev/null 2>&1; then
  msb-trust java >/dev/null 2>&1 && echo "plant-ca: msb-trust java" || echo "plant-ca: msb-trust java skipped/failed" >&2
  msb-trust chrome >/dev/null 2>&1 && echo "plant-ca: msb-trust chrome" || echo "plant-ca: msb-trust chrome skipped/failed" >&2
else
  echo "plant-ca: msb-trust absent (copy internal/sandbox/image/data/msb-trust.sh in as /usr/local/bin/msb-trust)" >&2
fi
