#!/usr/bin/env bash
# Fail when production code is unreachable from any `main` package and carries no
# reason-bearing //deadcode:ignore marker.
#
# deadcode always exits 0, so ci/deadcodecheck makes the decision. Exit statuses:
#   0  clean
#   1  a finding no marker excuses, a marker that excuses nothing, or a marker
#      that cannot be read
#   2  the analysis could not be trusted (bad payload, unreadable input)
#
# The gate analyses the host's own GOOS: no pin and no cross toolchain, because
# the microsandbox SDK needs cgo. Every build-tag configuration is analysed
# separately and the findings are unioned, so a function the default build cannot
# reach fails even when a tagged build can.
#
# Usage: DEADCODE_VERSION=v0.52.0 ci/check-deadcode.sh
#   DEADCODE_VERSION  required; the pinned golang.org/x/tools release (Makefile).
#   PKG               package pattern to analyse, default ./... . deadcode needs
#                     at least one main package in the pattern, so a leaf package
#                     cannot stand alone. A narrower pattern is an inspection aid:
#                     it cannot judge markers elsewhere in the module, so the
#                     stale-marker pass is skipped and reported as skipped.
#   TAGS              one build-tag configuration instead of the full matrix.
#
# Test-only seams, set by ci/test-check-deadcode.sh and by nothing else:
#   DEADCODE_FIXTURE_DIR  directory of <configuration>.json report payloads to
#                         use instead of running deadcode.
#   DEADCODE_FILES        file listing the source paths to scan, instead of
#                         `git ls-files '*.go'`.
# The checker always runs from the module root, so fixtures use absolute paths.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

: "${DEADCODE_VERSION:?DEADCODE_VERSION must be set, e.g. DEADCODE_VERSION=v0.52.0}"

# The microsandbox SDK needs cgo, so the analysis must not silently run with it
# off: an environment without a C compiler fails the analysis (exit 2) rather
# than reporting a package set the module is not really built from.
export CGO_ENABLED=1

PKG="${PKG:-./...}"

# A configuration's report file is named after its tags, so a diagnosis can say
# which build saw the finding.
configuration_name() {
  case "$1" in
    "") printf 'no-tags' ;;
    *) printf '%s' "${1// /+}" ;;
  esac
}

if [[ -n "${TAGS+set}" ]]; then
  configurations=("${TAGS}")
else
  configurations=("" "integration" "msb" "integration msb")
fi

scope=all
if [[ "$PKG" != "./..." ]]; then
  scope=single
fi

report_dir="$(mktemp -d)"
checker_dir="$(mktemp -d)"
trap 'rm -rf "$report_dir" "$checker_dir"' EXIT

# Build once and run the binary directly: `go run` reports any non-zero program
# exit as 1, which would erase the checker's distinction between a diagnosis and
# an untrustworthy analysis.
checker="$checker_dir/deadcodecheck"
go build -o "$checker" ./ci/deadcodecheck

for tags in "${configurations[@]}"; do
  name="$(configuration_name "$tags")"
  if [[ -n "${DEADCODE_FIXTURE_DIR:-}" ]]; then
    if [[ ! -f "$DEADCODE_FIXTURE_DIR/$name.json" ]]; then
      printf 'deadcode: missing fixture %s\n' "$DEADCODE_FIXTURE_DIR/$name.json" >&2
      exit 2
    fi
    cp "$DEADCODE_FIXTURE_DIR/$name.json" "$report_dir/$name.json"
    continue
  fi
  if ! go run "golang.org/x/tools/cmd/deadcode@${DEADCODE_VERSION}" -json -tags "$tags" "$PKG" \
    >"$report_dir/$name.json"; then
    printf 'deadcode: the analysis failed for tags=%s\n' "$tags" >&2
    exit 2
  fi
done

if [[ "$scope" == "single" ]]; then
  printf 'deadcode: PKG=%s covers only part of the module; skipping the stale-marker pass\n' "$PKG"
fi

if [[ -n "${DEADCODE_FILES:-}" ]]; then
  cat "$DEADCODE_FILES"
else
  git ls-files '*.go'
fi | "$checker" -reports "$report_dir" -scope "$scope"
