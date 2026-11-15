#!/usr/bin/env bash
# Sub-step D — is the runtime's own TLS interception (with the launcher's CA) an
# observation channel? Host-only.
#
#   ./spike/learn-mode/d_runtime_interception.sh
#
# Answers two separate questions, and records them separately:
#   D1 destinations — does an intercepted request reveal its host (facts 4–6)?
#   D2 attribution  — does it reveal which credential went where (fact 7)?
#
# The runtime logs destinations only at debug verbosity, and logs only *violations*
# of the secret policy — never a successful substitution — so the driver runs at
# `--log-level debug`, drives one allowed and one disallowed request, and reads the
# runtime's own diagnostics (`msb logs --source system`) plus any host-side runtime
# log directory.
set -euo pipefail
# shellcheck source=lib.sh disable=SC1091
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

spike_require msb openssl
dir="$(spike_capture_dir)"
name="$(spike_sandbox_name d)"
allowed="example.com"
disallowed="www.iana.org"

# -addext makes the CA constraints explicit: the runtime signs leaf certs with
# this key, so it must be a CA (openssl req -x509 does not always add them).
openssl req -x509 -newkey rsa:2048 -nodes -days 30 \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -keyout "$dir/spike-ca.key" -out "$dir/spike-ca.crt" -subj "/CN=spike-ca" >/dev/null 2>&1

export SPIKE_TOKEN="${SPIKE_TOKEN:-spike-token-0123456789}"
trap 'spike_rm "$name"' EXIT

# A secret whose only allowed host is $allowed, so a request to $disallowed is a
# policy violation the runtime logs. block-and-log (not the default) is what makes
# the violation appear in the logs rather than only in the guest's error.
spike_create "$name" alpine \
  --log-level debug \
  --tls-intercept --tls-intercept-ca-cert "$dir/spike-ca.crt" --tls-intercept-ca-key "$dir/spike-ca.key" \
  --secret "SPIKE_TOKEN:headers=authorization@$allowed" \
  --secret-violation-action block-and-log \
  --net public

# shellcheck disable=SC2016 # $SPIKE_TOKEN must be expanded in the guest shell
spike_exec "$name" sh -lc '
  apk add --no-cache curl >/dev/null 2>&1 || echo "apk add curl failed"
  echo "placeholder visible to the guest: $SPIKE_TOKEN"
  echo "== success: placeholder to the allowed host =="
  curl -sS -m 20 -o /dev/null -w "%{http_code}\n" \
    -H "Authorization: $SPIKE_TOKEN" https://'"$allowed"'/ || echo failed
  echo "== violation: placeholder to a disallowed host =="
  curl -sS -m 20 -o /dev/null -w "%{http_code}\n" \
    -H "Authorization: $SPIKE_TOKEN" https://'"$disallowed"'/ || echo failed
' | tee "$dir/d-guest.txt"

spike_dump_logs "$name"
spike_dump_metrics "$name"
"$MSB" logs --source system --timestamps "$name" >"$dir/d-system.logs" 2>&1 || true

# Best-effort: a host-side runtime log directory, if one exists.
for logs_dir in "${MSB_HOME:-$HOME/.microsandbox}/logs" "$HOME/.microsandbox/logs"; do
  if [ -d "$logs_dir" ]; then
    cp -R "$logs_dir" "$dir/d-runtime-logs" 2>/dev/null || true
    echo "copied host-side runtime logs from $logs_dir"
  fi
done

echo
echo "-- D1: did the runtime log the destination? (TLS intercept/bypass, sni/dst) --"
grep -iE 'TLS intercept|TLS bypass|sni=|dst=' "$dir/d-system.logs" 2>/dev/null \
  | tee "$dir/d-destinations.txt" || echo "(nothing)"

echo
echo "-- D2: did the runtime log which credential went where? (substitution / violation) --"
grep -iE 'secret violation|placeholder|location=|secret_env_var=|authorization' "$dir/d-system.logs" 2>/dev/null \
  | tee "$dir/d-attribution.txt" || echo "(nothing)"

echo
echo "Record two separate verdicts:"
echo "  D1 destinations: yes/no — evidenced by a sni/dst line for the allowed host."
echo "  D2 attribution:  yes/no — the runtime logs violations (host, path, secret env var,"
echo "                   location) but not successful substitutions nor header field names,"
echo "                   so a 'yes' for fact 7 needs a header name to appear here."
echo "If D1 is yes and D2 is no, D is a certificate-free B (destinations), not a C replacement."
