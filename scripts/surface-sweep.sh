#!/usr/bin/env bash
# Isolation helper for a release surface sweep.
#
# See .operator-shared/specs/testing/release-surface-testing.md. One shared
# XDG_DATA_HOME holds the microsandbox home (image cache, sandbox records) so a
# serial spine pays the image build once and parallel journeys reuse it; each
# journey gets its own HOME/XDG_*/TMPDIR tree and throwaway project under
# <root>/runs/<journey>, away from the maintainer's real state. It also drops the
# ambient AGENTS_SANDBOX_*/OPENCODE_SANDBOX_*/MSB_* namespaces, so an inherited
# override (internal/viperconfig binds AGENTS_SANDBOX_<KEY> for every derived
# key) cannot silently change what a journey measures; the wider shell env
# (PATH/TERM/tmux/...) stays, since the `env` snippet is sourced into an
# interactive journey shell.
#
# The root must be short enough for the sandbox control socket, and 0700:
# microsandbox refuses a data home under an ancestor another account could
# rename ("permits path substitution").
#
# Usage:
#   surface-sweep.sh init          # create <root>/{data,runs}; print <root>
#   surface-sweep.sh env <journey> # print shell exports for that journey
#   surface-sweep.sh sweep         # remove every sandbox and home volume
#   surface-sweep.sh lock          # take <root>.lock for the run (portable mkdir)
#   surface-sweep.sh unlock        # release <root>.lock
#
# ASB_SURFACE_ROOT overrides the root (default /tmp/asb-surface); ASB_BIN
# overrides the launcher binary (default `asb`).
set -euo pipefail

ROOT="${ASB_SURFACE_ROOT:-/tmp/asb-surface}"
# Resolve the root absolute once: a relative ASB_SURFACE_ROOT would stage
# HOME/XDG_*/TMPDIR under the caller's CWD, and configpaths ignores a
# non-absolute XDG_DATA_HOME.
if [[ "$ROOT" != /* ]]; then
  ROOT="$(pwd -P)/$ROOT"
fi
# microsandbox derives its control socket from <XDG_DATA_HOME>/agents-sandbox/msb
# and appends a fixed suffix; the sum must stay below sun_path (104).
SOCKET_SUFFIX=50
SUN_PATH_LIMIT=104

socket_path_len() {
  printf '%s' "$ROOT/data/agents-sandbox/msb" | wc -c | tr -d ' '
}

# Runs for every subcommand, not just init, so an env/sweep from a fresh shell
# cannot stage a root whose derived sandbox socket path overflows sun_path.
check_root_fits_sandbox_socket() {
  local len
  len="$(socket_path_len)"
  if [ $((len + SOCKET_SUFFIX)) -ge "$SUN_PATH_LIMIT" ]; then
    echo "root $ROOT derives a $((len + SOCKET_SUFFIX))-byte sandbox socket path, >= $SUN_PATH_LIMIT" >&2
    exit 1
  fi
}

cmd_init() {
  mkdir -p "$ROOT/data" "$ROOT/runs"
  # microsandbox refuses a data home under an ancestor that "permits path
  # substitution"; 0700 matches mkdtemp's protection.
  chmod 700 "$ROOT"
  echo "$ROOT"
}

cmd_env() {
  local name="${1:?usage: surface-sweep env <journey>}"
  local base="$ROOT/runs/$name"
  mkdir -p "$base/home" "$base/config" "$base/state" "$base/cache" "$base/tmp" "$base/project"
  # Drop the ambient config-override namespaces (AGENTS_SANDBOX_<KEY> and its
  # OPENCODE_SANDBOX_/MSB_ aliases); enumerated from the current env, so no key
  # list is needed.
  local leaked=() var
  while read -r var; do
    case "$var" in
      AGENTS_SANDBOX_*|OPENCODE_SANDBOX_*|MSB_*) leaked+=("$var") ;;
    esac
  done < <(compgen -e)
  if ((${#leaked[@]})); then
    printf 'unset'
    printf ' %s' "${leaked[@]}"
    printf '\n'
  fi
  # The six isolated vars; PATH/TERM/etc. are inherited from the caller.
  cat <<EOF
export HOME="$base/home"
export XDG_CONFIG_HOME="$base/config"
export XDG_DATA_HOME="$ROOT/data"
export XDG_STATE_HOME="$base/state"
export XDG_CACHE_HOME="$base/cache"
export TMPDIR="$base/tmp"
EOF
}

# A portable lock: mkdir is atomic everywhere, while flock(1) is util-linux and
# absent on macOS (a supported target). The lock is held by the directory's
# presence for the whole run; a crashed coordinator leaves it behind, so remove
# <root>.lock once no coordinator is running.
cmd_lock() {
  local lockdir="$ROOT.lock"
  if ! mkdir "$lockdir" 2>/dev/null; then
    echo "lock $lockdir is already held; remove it if no coordinator is running" >&2
    exit 1
  fi
  echo "$lockdir"
}

cmd_unlock() {
  local lockdir="$ROOT.lock"
  if ! rmdir "$lockdir" 2>/dev/null; then
    echo "lock $lockdir is not held" >&2
    exit 1
  fi
}

cmd_sweep() {
  local asb="${ASB_BIN:-asb}"
  local envfile name volume sandboxes volumes
  envfile="$(mktemp)"
  cmd_env coordinator >"$envfile"
  # Capture each list first so a failing list aborts the sweep with a
  # diagnostic instead of silently reporting success having removed nothing.
  # shellcheck disable=SC1090
  sandboxes="$(
    . "$envfile"
    "$asb" sandbox list -q
  )" || {
    echo "sweep: sandbox list failed" >&2
    rm -f "$envfile"
    exit 1
  }
  # shellcheck disable=SC1090
  while read -r name; do
    [ -n "$name" ] || continue
    (
      . "$envfile"
      "$asb" sandbox remove --force --yes "$name"
    ) ||
      echo "sweep: failed to remove sandbox $name" >&2
  done <<<"$sandboxes"
  # `volume list -q` prints one identifier per line (no header), so it is immune
  # to an inherited AGENTS_SANDBOX_QUIET dropping a line-1 volume.
  # shellcheck disable=SC1090
  volumes="$(
    . "$envfile"
    "$asb" volume list -q
  )" || {
    echo "sweep: volume list failed" >&2
    rm -f "$envfile"
    exit 1
  }
  # `volume remove` refuses non-interactively without --yes, so pass it too.
  # shellcheck disable=SC1090
  while read -r volume; do
    [ -n "$volume" ] || continue
    (
      . "$envfile"
      "$asb" volume remove --force --yes "$volume"
    ) ||
      echo "sweep: failed to remove volume $volume" >&2
  done <<<"$volumes"
  rm -f "$envfile"
}

check_root_fits_sandbox_socket
case "${1:-}" in
init) cmd_init ;;
env)
  shift
  cmd_env "$@"
  ;;
sweep) cmd_sweep ;;
lock) cmd_lock ;;
unlock) cmd_unlock ;;
*)
  echo "usage: surface-sweep {init|env <journey>|sweep|lock|unlock}" >&2
  exit 2
  ;;
esac
