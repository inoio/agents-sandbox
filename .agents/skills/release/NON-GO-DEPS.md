# Non-Go tool versions

Dependabot manages exactly two ecosystems in this repo (`.github/dependabot.yml`): `gomod` and
`github-actions`. Every other pinned version is bumped by hand and can silently drift. Bump them
during [step 9](SKILL.md#9-post-release).

## Inventory

| Tool | Version | Pinned in |
|------|---------|-----------|
| Go | `1.27.3` | `.go-version` (source), `.agents-sandbox/Dockerfile` (`ARG GO_VERSION`), `.github/workflows/ci.yml` (`go-version-file: .go-version`) |
| golangci-lint | `2.15.0` | `Makefile` (`GOLANGCI_LINT_VERSION`), `.agents-sandbox/Dockerfile`, `.github/workflows/ci.yml` (`v2.15`) |
| gotestsum | `1.13.0` | `Makefile`, `.agents-sandbox/Dockerfile` |
| govulncheck | `v1.9.0` | `Makefile` (`GOVULNCHECK_VERSION`) |
| deadcode | `v0.52.0` | `Makefile` (`DEADCODE_VERSION`) |
| gomutants | `v0.7.2` | `Makefile` (`GOMUTANTS_VERSION`) |
| zig | `0.17.0` | `Makefile` (`ZIG_VERSION`), `.agents-sandbox/Dockerfile`, `.github/workflows/ci.yml` |
| gh | `2.103.2` | `.agents-sandbox/Dockerfile` |
| plantuml | `1.2026.9` | `.agents-sandbox/Dockerfile`, `.github/workflows/pages.yml` |
| Node | `v24.22.1` | `internal/sandbox/image/dockerfile.go` (`nodeVersion`) — see below |

A tool's version lives in more than one place; update every location in its row. Go is the exception: `ci.yml` reads
`.go-version` directly via setup-go's `go-version-file`, so `.go-version` is the single source there and cannot drift.
golangci-lint is pinned exactly in the `Makefile` and Dockerfile (`2.15.0`) but only to major.minor in `ci.yml` (`v2.15`) — keep them on the same minor.

## Node pin

Keep `nodeVersion` **below v25**. Node v25 and v26 tarballs carry a hard `DT_NEEDED` on
`libatomic.so.1`, which the managed base and most custom bases do not provide; on those bases the
npm-agent install fails and an image can be left with a `node`/`npx` that cannot start. The v24 LTS
line has no libatomic dependency on x86_64 or arm64. There is **no solution yet for custom bases**
that need node >= 25. Regression guard: `TestRenderDockerfileNpmAgentNodeRuns` (integration).
