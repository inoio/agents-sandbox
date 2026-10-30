---
description: Contract for the CLI output package boundary (#357, review finding E3) - internal/cliout stays domain-free (generic mode/list/JSON rendering only) and the sandbox-shaped Printer implementations plus prune/compact report JSON DTOs live in internal/view, enforced by an import-boundary test.
read_if: Moving a Printer, report-JSON DTO, or renderer between internal/cliout and the launcher's view package, or deciding where new list/inspect/report rendering belongs.
---

# CLI output package boundary

Date: 2026-10-28. Issue #357, from the repository-wide architectural review (finding E3).

## Intent

`internal/cliout` is the reusable command-output core and must stay importable by any package, including the
sandbox domain packages. Before #357 it imported `sandbox/image`, `sandbox/vm`, `sandbox/volume`, and
`sandbox/pruning`, so the generic renderer was welded to the union of four domains. The domain-shaped code now
lives in `internal/view`.

## Contract

- `internal/cliout` imports no `internal/sandbox/...` package. It keeps `Mode`/`ResolveMode`, `WriteJSON`,
  `Printer[T]`/`PrintList`, and `RenderPruneReport` — the last switches between a caller's human renderer and a
  prebuilt JSON value, so it is domain-free despite the name.
- `internal/view` holds the domain renderers: `SandboxPrinter`/`ImagePrinter`/`VolumePrinter` (the
  `cliout.Printer` implementations, including digest truncation and the volume SIZE-column rules) and the
  prune/compact report JSON DTOs with their constructors.
- `internal/cliout/boundary_test.go` enforces the rule by reading the package's own non-test imports, so a new
  domain import fails the suite rather than shipping.
- `internal/view` is importable like every other `internal/` package but is the launcher's presentation of the
  domain, imported only by `cmd/agents-sandbox` today. Adding a renderer for another domain belongs there, not
  in `cliout`.
