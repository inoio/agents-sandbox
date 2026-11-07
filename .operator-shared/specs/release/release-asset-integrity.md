---
description: Contract for verifying a downloaded release asset before the self-update installs it (#338): the release publishes one `checksums.txt`, the updater verifies SHA-256 against it, and every metadata failure is fail-closed.
read_if: Changing the self-update download/verify/replace path, the release job's asset or checksums publishing, or `internal/upgrade`'s checksum handling.
---

# Release asset integrity

## Publishing (`.github/workflows/ci.yml`, `publish` job)

- Each release — prereleases included, so canary installs verify too — publishes one `checksums.txt`
  asset beside the binaries, in standard `sha256sum` format: one line
  `<64-hex><two spaces><asset-name>`, with the flat asset names as uploaded. The `Write checksums.txt`
  step runs before `Create GitHub Release` and covers every `agents-sandbox-<target>` artifact; the
  release step uploads `artifacts/checksums.txt` alongside `artifacts/*/*`.
- The same artifacts feed the Homebrew formula bump, so the published digest and the formula digest
  stay consistent.

## Verification (`internal/upgrade`)

- `installRelease` is the whole download → verify → replace path: it downloads the platform asset,
  fetches `checksums.txt` for the same release tag, compares the downloaded file's SHA-256 with the
  asset's entry, and only then calls `replaceExecutable`. The replacement is unreachable unless
  verification passes.
- Fail-closed on every metadata problem: a mismatched digest, a missing `checksums.txt` (non-200),
  and a missing or malformed entry for the asset all return an error before the rename; the temp file
  is removed and the running binary is untouched.
- `checksumForAsset` parses the `sha256sum` shape; a line that names the asset with any other shape is
  malformed. Whitespace is collapsed and the digest is validated as 64 hex characters, and a duplicate
  entry for the asset resolves to the first match.
- `assetName()` is the single source of the flat asset name, shared by the download URL and the
  checksums lookup.

## Known limitation

- The checksum is fetched over the same TLS channel as the asset, so it defends against a corrupted
  or swapped asset and an inconsistent CDN, not against an active intermediary that rewrites both.
  Signature verification (cosign/Sigstore) is the deliberate follow-up, out of scope for #338.
