#!/usr/bin/env bash
#
# test-envelope.sh - run a command (default: the test suite) in a hermetic
# envelope: a copy of the module outside any git checkout, with a scrubbed
# environment.
#
# The command runs under `env -i` with an explicit allowlist, so no ambient
# variable reaches it, and (when a directory is copied) from a copy in a temp
# work dir, so nothing it writes touches the source tree. See
# specs/test-isolation.md.
#
# Usage:
#   test-envelope.sh [options] [-- COMMAND...]
#
# Options:
#   --source DIR    Directory to copy into the work dir (default: this repo's
#                   root).
#   --work-dir DIR  Work dir for the copy and the scrubbed HOME/TMPDIR
#                   (default: a fresh mktemp dir).
#   --keep          Keep the work dir on exit (default: remove it).
#   --print         Print the plan (work dir, environment, command) and exit
#                   without running anything.
#   -h, --help      Show this help.
#
# With no `-- COMMAND...`, the command is `make test`.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
source_dir="$(cd "$script_dir/.." && pwd -P)"
work_dir=""
keep=0
print_only=0
command=()

usage() {
  sed -n '3,22p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

while (( $# > 0 )); do
  case "$1" in
    --source)
      [[ $# -ge 2 ]] || { echo "test-envelope: --source needs a directory" >&2; exit 2; }
      source_dir="$2"; shift 2 ;;
    --work-dir)
      [[ $# -ge 2 ]] || { echo "test-envelope: --work-dir needs a directory" >&2; exit 2; }
      work_dir="$2"; shift 2 ;;
    --keep) keep=1; shift ;;
    --print) print_only=1; shift ;;
    --) shift; command=("$@"); break ;;
    -h|--help) usage; exit 0 ;;
    *) echo "test-envelope: unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if (( ${#command[@]} == 0 )); then
  command=(make test)
fi

source_dir="$(cd "$source_dir" && pwd -P)"

if [[ -z "$work_dir" ]]; then
  work_dir="$(mktemp -d "${TMPDIR:-/tmp}/test-envelope.XXXXXX")"
else
  # Keep the path as given (do not resolve symlinks: the child must see the
  # same PWD the caller asked for, which matters on hosts where TMPDIR lives
  # under a symlink, e.g. macOS /var -> /private/var).
  case "$work_dir" in
    /*) : ;;
    *) work_dir="$PWD/$work_dir" ;;
  esac
  mkdir -p "$work_dir"
fi

# The environment the child sees, and no other. PWD is set explicitly so the
# child's working directory is deterministic on every host rather than derived
# from the machine's real path.
env_args=(
  "PATH=$PATH"
  "HOME=$work_dir/home"
  "TMPDIR=$work_dir/tmp"
  "PWD=$work_dir"
  "GOCACHE=${GOCACHE:-$(go env GOCACHE)}"
  "GOMODCACHE=${GOMODCACHE:-$(go env GOMODCACHE)}"
  "GOFLAGS=-count=1${GOFLAGS:+ $GOFLAGS}"
  "CGO_ENABLED=${CGO_ENABLED:-0}"
  "LANG=${LANG:-C.UTF-8}"
  "TZ=${TZ:-UTC}"
)

if (( print_only )); then
  printf 'work_dir=%s\n' "$work_dir"
  for key_value in "${env_args[@]}"; do
    printf 'env %s\n' "$key_value"
  done
  printf 'command: %s\n' "${command[*]}"
  exit 0
fi

cleanup() {
  if (( keep == 0 )); then
    rm -rf "$work_dir"
  fi
}
trap cleanup EXIT

mkdir -p "$work_dir/home" "$work_dir/tmp"
rsync -a --exclude=.git --exclude=.gomutants-cache.json "$source_dir"/ "$work_dir"/

( cd "$work_dir" && env -i "${env_args[@]}" "${command[@]}" )
