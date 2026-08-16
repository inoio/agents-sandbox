# Backlog / Ideas

## Done

[*] secrets containing @ kann nicht funktionieren -> yaml. Keine CLI-Params.
[*] make cli output pretty and UX'd

## Testing

[ ] volume migrate, edit, reset
[ ] config changes to root size, tmp size, cpu, mem, opencode, home
[ ] opencode config dir & merging nach alphabet
[ ] config file(s): support arbitrary files in VM home
[ ] config file(s) übersicht, beispiele
[ ] config show: list all files that would apply to a VM startup.

## In Progress

[ ] inoio providers ausbauen, anders zur Verfügung stellen.
    * inoio-Anleitung, wie man das einrichtet
    * getting-started: provider beispiel mit secret
[ ] OPENCODE_DISABLE_AUTOUPDATE and detect releases, offer vm rebuild
[ ] shell subcommand user flag (other flags from msb)

## 1st prio

[ ] env.secret[.yaml] im Projekt, wie verstecken?
[ ] default pattern for hiding project files, configurable (*.secret), not checked in?
[ ] OPENCODE_EXPERIMENTAL_EXA, OPENCODE_EXPERIMENTAL_BACKGROUND_SUBAGENTS, OPENCODE_EXPERIMENTAL_PARALLEL, OPENCODE_EXPERIMENTAL_SCOUT? 
[ ] remove msb load exec call by spooling to tmp file and using msb SDKs Image.Load 
[ ] enforce basic auth for `run --serve-only` (published opencode port on host loopback is currently unauthenticated)
[ ] configurable host port for `run --serve-only` (currently fixed 4096)

## 2nd prio

[ ] request --auto in opencode project for serve&attach
[ ] after docker image import, remove docker built image? Or delete docker layers when deleting msb images? Regular auto-cleanup for this?
[ ] clean commands for volumes, sandboxes, images. docker images/cache/... - with filters, best same ones. Also readable list output (project name, not number) (labels an docker images für cleanup?)
[ ] optionale Versionierung von dependencies (opencode, node.js)
[ ] dev doku, design principles usw. (z.B. CLI design aligned with docker,msb)

## 3rd prio

[ ] neben docker auch buildah, podman, ??? supporten?
[ ] was ist mit den LSP-Servern wenn ein Projekt node braucht?
[ ] cli interaction library instead of prompt.go? https://github.com/pterm/pterm
[ ] testify/mock & mockery statt manuell?

## Sandbox cohesion refactor — deferred follow-ups (from 2026-08-12 remediation plan)

Larger, higher-risk items deliberately deferred out of the cohesion/coupling remediation; a future plan should implement them.

1. Extract mock code out of production binaries (`msb/testmock.go` ~688 lines, `docker/testmock.go`) into `_test.go`/testutil — removes `testing` from the production import graph.
4. `reprovision` sub-package split (configfiles / envstate / reconfig) — the largest single cohesion debt.
6. `reprovision`/`doctor` parameter narrowings (`RunOptions`, `ParseMemory` error-return, `checkForActiveVMs` relocation) to reduce behavioral risk-avoidance.
7. `naming.ArtifactFor` vs. `ExtractProjectSlugAndDigest` dual-dispatch unification.
