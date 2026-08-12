# SDD ledger — plan: /workspace/docs/superpowers/plans/2026-08-12-sandbox-cohesion-coupling-remediation.md

## RESUME on new worktree (was: .../sandbox-cohesion-remediation-2)
Worktree: .../sandbox-modularization-rework
Branch: opencode/sandbox-modularization-rework
Base (trusted /workspace main): 0c889b1 (merge of Tasks 1-3)
Tasks 1-3 committed and merged into main at 0c889b1. Resume at Task 4.

## Prior worktree history (record, now merged):
Worktree: .../sandbox-cohesion-remediation-2
Branch: opencode/sandbox-cohesion-remediation-2
Base (trusted /workspace main): d0919bd

## CONTEXT — the old worktree (sandbox-cohesion-remediation) was discarded
It became dirty/interfered-with (a concurrent desktop-as-client session + an implementer whose cwd was /workspace/main, not the worktree). Result: Task 3's commit d0919bd contained OUT-OF-SCOPE serve-only drift that broke the build (ensureDaemon/restartDaemons 4-arg calls against a 3-arg def). Working in the old worktree is abandoned.

## Repair on the new worktree (base d0919bd)
- 87ace2e fix: revert out-of-scope serve-only drift in ensureDaemon/restartDaemons (run.go + run_test.go back to 3-arg forms; keeps Task 3's AutoFlag/ExitError/EnvKeyValueParts/StaleType symbol moves). Now `go build ./...` and `make check` pass on this worktree.

## Branch refs today
- /workspace main = d0919bd (user keeps unedited)
- opencode/sandbox-cohesion-remediation-2 = 87ace2e (our continuing base)
- opencode/desktop-as-client = 44672a3 (concurrent feature, do not disturb)

Task 1: complete (commits 6701424..5075f1a) — verified clean & reviewed.
Task 2: complete (commits 5075f1a..8db924d) — verified clean & reviewed.
Task 3: IN PROGRESS — commit d0919bd implemented but NOT yet reviewed. Now being reviewed (BASE 03dabd1, HEAD d0919bd) ON TOP of repair 87ace2e. NOTE: reviewer must treat the out-of-scope serve-only drift as a Task-3 scope violation (now fixed in 87ace2e) and verify the legitimate Task-3 scope (StaleType→pruning, ExitError→session, AutoFlag→session, EnvKeyValueParts→reprovision, MibPerGib→private, timeouts.go, flock collapse, slugDir/slugPath, StateDir alias) is COMPLETE and correct.

NOTE: GOOS=darwin go build steps in the plan are NOT runnable here (microsandbox SDK excludes darwin FFI). Treat as linux build + build-tag reasoning.

Task 4: COMPLETE (commit c642a04 on opencode/sandbox-modularization-rework; base 0c889b1). Implementer DONE, reviewer Approved (spec ✅, no Critical/Important). Minor (deferred): vm.go/run.go left as bare `package session` stubs (16 bytes). Matches brief intent; consider deleting in a later cleanup — park for final review triage.

Task 5: COMPLETE (commit 4f31e03 on opencode/sandbox-modularization-rework; base c642a04). Implementer DONE, reviewer Approved (spec ✅, no Critical/Important). findStaleVMs was in report.go, moved to catalog.go. Minors (deferred): (1) image.go left as bare 2-line doc stub; consider git rm + fold doc into build.go; (2) progress.md ledger drift (Task-4 COMPLETE line + a pre-existing "Commit pending" section read as contradictory) — sweep in final review. Image stub + empty file cleanup parked for final review triage.

Task 6: COMPLETE (commit 3dca71b on opencode/sandbox-modularization-rework; base 4f31e03). Implementer DONE, reviewer Approved (spec ✅, no Critical/Important). checkMsb split to install.go (ensureMsbInstalled/msbBinPath/appendPathHint) behavior-identical; checkKvm moved to linux build tag w/ darwin no-op; reprovision parseKeyValueLines dedup. Minors (deferred): (1) `_ = parseKeyValueLines(...)` error channel always nil (plan-mandated signature, cosmos); (2) consider adding a parseKeyValueLines unit test (blank/comment/'='-in-value/'#' cases) — harden shared helper. Both parked for final review triage.

Task 3: REVIEW COMPLETE:
- Reviewer verdict: Steps 1,3,4,5,6,7 ✅; Step 2 ❌ (orphaned options.ExitError left in options/options.go) + known serve-only scope violation.
- Fix round 1: 29d80cf deleted orphaned options.ExitError + its Error() + fmt import; 87ace2e reverted serve-only drift (preconditioned).
- Re-review: All findings ADDRESSED; no new breakage.
- Task 3 COMPLETE (commits 03dabd1..29d80cf, review clean after 1 fix round). Base of branch = 29d80cf (green: make check passes).
- Minor/deferred (ledger, not loop): AutoFlag value is `--auto` (original), brief typo said `--auto-reap`; slugPath used once; stale nolint:gosec comment in vm.go.

## Task 4 (this worktree): split session run.go and vm.go
- Task 4 COMPLETE: mechanical split of session run.go and vm.go into focused files (Commit pending).
- New files:
  - vm_lifecycle.go: projectPortBindings, vmAction(+consts), decideVMAction, ensureProjectVM, createProjectVM, acquireProjectFlock
  - vm_resources.go: reconcileResourceConfig, summarizeConflicts
  - vm_name.go: projectVMName
  - vm_env.go: experimentalWorkspacesValue, buildProjectVMEnv
  - vm_control.go: stopOrKillProjectVM, StopProjectVM, KillProjectVM
  - run_orchestrate.go: buildAttachCommand, buildOpencodeArgs, serveOnlyMessage, runServeOnly, sandboxSession/cleanup, prepareSandbox, Run, Shell, BuildImage, finalizeRun, tmpMountPath, buildMounts
  - run_envstate.go: currentEnvState, currentSecretState, persistEnvSecrets (matches existing run_envstate_test.go)
  - reconfig.go: setUpSandbox, decideReconfig, restartDaemons
- run.go and vm.go reduced to thin package stubs (all symbols relocated).
- Verified: `go build ./...`, `go test ./internal/sandbox/session/...`, and `make check` (fmt + lint 0 issues + full test suite) all PASS.