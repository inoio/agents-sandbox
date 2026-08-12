# SDD ledger — plan: /workspace/docs/superpowers/plans/2026-08-12-sandbox-cohesion-coupling-remediation.md

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

Task 3: REVIEW COMPLETE:
- Reviewer verdict: Steps 1,3,4,5,6,7 ✅; Step 2 ❌ (orphaned options.ExitError left in options/options.go) + known serve-only scope violation.
- Fix round 1: 29d80cf deleted orphaned options.ExitError + its Error() + fmt import; 87ace2e reverted serve-only drift (preconditioned).
- Re-review: All findings ADDRESSED; no new breakage.
- Task 3 COMPLETE (commits 03dabd1..29d80cf, review clean after 1 fix round). Base of branch = 29d80cf (green: make check passes).
- Minor/deferred (ledger, not loop): AutoFlag value is `--auto` (original), brief typo said `--auto-reap`; slugPath used once; stale nolint:gosec comment in vm.go.