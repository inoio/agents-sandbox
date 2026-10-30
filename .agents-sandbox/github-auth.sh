#!/bin/sh
if [ -n "${GITHUB_PAT:-}" ]; then
  printf '%s\n' "$GITHUB_PAT" | gh auth login --with-token
  gh auth setup-git
fi

