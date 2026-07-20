# AGENTS.md

## Project

inoio-sandbox: runs opencode inside a microsandbox (msb) microVM. Host launcher
(Python + click) manages VM lifecycle; opencode state persists per-repo on host.

## Code style

MVP style: self-explanatory code, minimal abstractions, comments only when code
isn't self-explanatory. Disregard pre-existing inline docs/coding style in this
repo (POC spike).

## Constraints (MVP)

Linux only (KVM). No network rules. No secrets-in-workdir masking. API keys via
host env, not msb secrets.

## Design spec

`docs/superpowers/specs/2026-07-20-inoio-sandbox-design.md`
