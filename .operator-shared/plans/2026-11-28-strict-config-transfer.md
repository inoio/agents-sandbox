# Strict automatic config transfer — implementation plan

**Goal:** implement original proposal A in #849 against the
[target contract](../specs/onboarding/learn-mode/config-transfer.md), incorporating #897 and the adversarial review.
This is a TDD plan, not a claim that the security policies or their implementation are complete.

## Execution boundary

- Work on `fix/849-readback-boundary` or a dedicated successor; leave other worktrees and #897 unchanged.
- This plan is prepared on `ffb94823`. Before code work, preserve the reviewed documentation changes and
  assess upstream drift; update the branch safely onto current `main`. No reset/stash/rebase of other work.
- The SDK migration (#906), adapter changes (#925/#928), host defaults (#930), and lifetime changes (#934)
  are integration inputs, not dependencies to reimplement. #935 is still a separate raw-copy feature.
- Candidate delivery is three slices, each referencing #849 and #897; only the completed strict boundary
  can close #849. Boundaries follow dependencies, not a requirement to produce exactly three PRs:
  - **Slice A — independent collision/dependency fixes:** red tests from Task 1, the conservative
    conflicting-binding checks from Task 3, and the required Apply/secret-writer coordination from Task 5.
    These may land before the full policy work, but intentionally refuse formerly accepted conflicts.
    Do not promise unchanged acceptance, scope union, or complete closure of #849.
  - **Slice B — strict document plus targeted transformation:** Task 0's parser decision, Task 2,
    snapshot/location-aware classification from Task 3, and targeted replacement from Task 4. Exact
    source-position changes depend on the checked document; do not implement a temporary raw-text parser
    in Slice A to claim earlier completion. Ambiguous inputs are refused rather than silently normalized.
  - **Slice C — reviewed policies and feature enablement:** complete Task 0's compatibility gate,
    remaining Tasks 4 and 6–8. Dependency fixes and positive whole-config tests must already pass.
  Each slice has an explicit supported-behavior statement and focused acceptance tests. If an independent
  fix cannot preserve a coherent safe intermediate state, include it in the document-based slice instead.
  Keep #897 as evidence until the final implementation successor is linked.
- Do not install tools, add a parser dependency speculatively, run mutation sweeps in this environment,
  or touch private agent configs to recover the 40/3 measurements.
- Every task uses tests first: establish that the regression fails for the intended behavior, implement
  the smallest coherent change, then run focused tests and lint. Follow the repository's unit/flow test
  naming rules. A no-longer-supported old happy path becomes an explicit refusal regression, not a deletion.
- Scope (decided): #849 closes on its core — AC1–AC3, amended AC4, AC5, both entry points, refusal that
  preserves existing state, the no-weaker-fallback rule, and the per-run report for unsupported harnesses.
  The write-back integrity confirmation, the rejected-import retry, and policies beyond the initial evidence
  harness are follow-up issues (see "Follow-up issues" at the end). Do not grow this into a full
  config-schema project or an onboarding assistant.

## Task 0 — settle the policy and parser feasibility gate

**Owners:** `internal/configdecode`, `internal/sandbox/readback`, `internal/agent`; no production enablement yet.

- [ ] Use the dated [harness schema evidence guide](../guides/harness-config-schema-evidence.md) as the
  inventory baseline. Revalidate each entry against current-main profiles/docs and the exact installed/tested
  harness version; the guide is evidence with explicit gaps, not an authorization list.
- [ ] Inspect current-main parser APIs for duplicate-key/source-location support before choosing an API.
  JSON5 currently reduces objects to maps; a second decode of that map cannot recover duplicates. Prefer
  existing parser facilities. If adequate strict parsing needs a dependency change or a bounded syntax
  subset, present that concrete choice before proceeding; do not write an independent regex grammar.
- [ ] Define a single strict transfer-document API in `configdecode`, with a bounded structural result,
  duplicate/ambiguity refusal, and safe reason codes. Ordinary launcher loading need not silently change.
- [ ] Establish whether supported permission/pattern objects depend on member insertion order, not only
  array order. Characterize overlapping rules in both orders against the pinned harness contract. Choose
  an order-preserving transfer representation/serializer where needed; an unordered Go map is not proof
  of preserved behavior.
- [ ] Complete the initial policy table: harness/schema, exact path, accepted types/value domain, dynamic-key
  restrictions, credential transform, rationale/limitations, positive fixture, negative cases. Move settled
  domains into the target spec; unresolved rows remain refused, not implicitly enabled.
- [ ] Assess legacy `provider.*.options` separately from native-v2 provider/settings forms. Explicitly pass
  the selected harness/context; do not infer it from the shared guest filename or trust `$schema` URLs.
- [ ] Establish actual domains for endpoint host/path, provider/model ids, package selectors, references,
  and permission resources. A URL parser, arbitrary identifier grammar, or token-length test is not a
  completed justification. Document assumptions where schema-authorized literals cannot reveal intent.
- [ ] Use the journey's `opencodeProviderDocument`, both permission examples in `docs/configuration/agent.md`,
  and published #897 regressions. Use a non-sensitive path summary of the original 40/3 inputs only if
  available from their owner; otherwise retain their compatibility as unverified.
- [ ] Where available, pin schema evidence and review eligible configuration paths/roles before reusing its
  validators. A finite enum or bounded integer is not automatically safe: test a numeric PIN and a secret
  enum under credential-bearing paths. Without a reliable schema, use versioned docs and fixtures.
- [ ] Establish policy revision/provenance and the maintenance checklist: upstream new/changed/removed
  fields and semantic changes require review; updating a schema alone must authorize no new path. No new
  update-bot infrastructure is required to close #849.
- [ ] Inventory every harness the heuristic transfers today. #849 enables policies for the initial evidence
  harness only; every other harness gets the per-run "not supported yet" report. Record this as a breaking
  default-behavior change for the changelog. Provider-only fixture coverage is not evidence that typical
  complete host configs remain transferable.
- [ ] Ensure #849's refusal text does not imply a later release will import the file automatically: the
  retry path is a follow-up, and today a recorded first-run decision suppresses an unchanged host config.
- [ ] **Exit gate:** publish the completed table and whole-config accept/refuse results for review. If no
  useful provider setup survives or typical documented protection rules cannot be preserved, stop before
  feature enablement and surface the product consequence. Do not silently substitute partial import.

## Task 1 — preserve the reproduced defects as red tests

**Files:** `readback/sanitize_test.go`, `readback/read_test.go`, `analyser/observation_test.go`,
`learn/analyse_test.go`, plus unit-named or flow tests for the actual new responsibility.

- [ ] Add duplicate `apiKey` followed by a reference, including escaped duplicate-key spellings.
- [ ] Add a key literally equal to `deny` beside an unrelated deny permission: accepting output must never
  replace the permission effect. Under the raw-value-retention rule, whole-candidate refusal is valid;
  assert it leaves an existing snippet unchanged. Add a non-conflicting accepted positive control.
- [ ] Add `corp-west`/`corp_west` with different keys/hosts: no shared placeholder, merged host proposal,
  or first/last-value resolution is permitted. Add explicit same-env-name conflicts across source files.
- [ ] Drive actual findings through Apply: decline secret, accept config; also remembered decline and
  failing secret write. Expect no newly installed dependent config and no raw values in diagnostics.
- [ ] Retain compatible-existing-secret and credential-free positive controls so blanket refusal cannot
  satisfy the suite. Add existing-managed-config preservation on candidate refusal.
- [ ] Port #897's useful failure cases, keeping attribution to behavior in commits rather than production
  comments. Reassess its accepted commands/prompts/paths instead of porting broad exemptions.

Suggested focused loop (module root):

```sh
go test -tags microsandbox_ffi_path ./internal/sandbox/readback ./internal/sandbox/analyser ./internal/sandbox/learn
```

## Task 2 — strict parsing and immutable transfer documents

**Files:** new unit-named `internal/configdecode/transfer.go` / `transfer_test.go` (names may follow the
API agreed in Task 0); `readback` owns no second general-purpose parser.

- [ ] Reject duplicate entries before map reduction, extra documents, trailing data, invalid/non-string
  mapping keys, and ambiguous supported-normalization collisions. Errors exposed to users contain no
  parser source excerpts or untrusted key text.
- [ ] Define and test depth/node/alias-expansion bounds; reject unsupported scalar/tag/alias forms and
  non-finite/out-of-domain numbers. Reject unknown formats rather than guessing.
- [ ] Test JSON/JSONC/JSON5, YAML, and TOML according to the agreed supported subset. Mark unsupported
  forms explicitly; no format claim without positive tests. Include all original line-break dimensions.
- [ ] Serialize only the checked structure, remove non-semantic comments, and round-trip supported scalar
  semantics, ordered arrays, and order-sensitive mapping members. A round trip through the same unordered
  map cannot test preservation of lost source order. Test failure paths and escaped strings without
  returning partial output.
- [ ] Expose stable structural locations in memory so classification and transformation share a candidate
  interpretation. Do not re-read mutable home files after approval to obtain their payload.
- [ ] Run configdecode and existing loader tests to show the strict transfer API did not silently alter
  ordinary config loading; update `config-file-decoding.md` if an explicit API is added.

## Task 3 — structural credential identities and collision preflight

**Files:** `readback/read.go`, focused new `readback/transfer_credentials.go` / `_test.go` if needed;
`analyser/observation.go`, `learn/analyse.go`, their unit tests.

- [ ] Classify credentials and endpoints from the same transfer document; retain source/path identity
  and the harness's supported reference semantics. Keep renewing credential stores excluded.
- [ ] Stop treating an env-var name as sufficient identity for source resolution or host aggregation.
  Keep raw values in memory only. Do not persist a hash of a low-entropy credential as dependency metadata.
- [ ] Refuse generated/explicit binding collisions with differing values or incompatible host/header scope,
  including existing declarations and effective higher-precedence scopes. Do not overwrite or broaden them.
- [ ] Reuse only independently compatible bindings; equality of values is not authority to union hosts.
- [ ] Test source order independence, provider normalization collisions, duplicate placeholders under
  different names, multi-file inputs, and collisions with existing broader/different/shadowed declarations.
- [ ] Keep network attribution derived from evidence/config, not a newly invented provider-host table.
- [ ] In an intermediate fix without the strict document, refuse ambiguous identity conservatively and
  state the remaining parser/heuristic limits. Do not infer exact AST locations from strings or names.

## Task 4 — contextual authorization and targeted transformation

**Files:** `readback/sanitize.go`; proposed focused `transfer_policy.go` / `transfer_policy_test.go` and
`transfer_document.go` / `transfer_document_test.go` as responsibilities emerge; both callers supply context.

- [ ] Walk the complete structure deterministically, authorizing entries and containers before descent.
  Unknown empty maps/lists, booleans, nulls, and native scalars cannot evade a string-leaf-only guard.
- [ ] Implement only Task 0's reviewed policies; unsupported harness/schema/domain defaults to refusal.
  No global safe-key/suffix, short-word, numeric, filesystem-path, URL, or reference-prefix exemption.
- [ ] Replace only identified credential values; native-indirection changes refuse sibling conflicts.
  A known secret appearing elsewhere refuses instead of triggering global text replacement.
- [ ] Validate final structure, raw-value absence, and semantic equivalence outside authorized changes.
  Preserve both permission content and order. Reject policy/serializer mismatches.
- [ ] Return structured, value-free refusal reasons and redacted/indexed locations. Refactor or remove
  old parallel deciders when superseded; `TextLeaksSecret` still has a separate instruction-seed consumer.
- [ ] A shared argument/reference parser is required wherever forms are supported; unbounded commands and
  opaque positional values stay refused. Do not build a permissive shell parser to make fixtures pass.

## Task 5 — safe dependent writes and per-slug coordination

**Files:** `envsecret/write.go` and tests; `analyser/finding.go`, `apply.go` and tests; shared leaf helper
only if required by both seed and Apply. Use `hostlock`, not a new locking library.

- [ ] Represent config-to-secret dependencies without raw values in findings; the in-memory resolver uses
  structural identity. Extend the existing model concretely rather than create a general workflow engine.
- [ ] Prompt outside write locks. At apply time, acquire destination-wide coordination, revalidate the
  existing effective secret bindings and target state, then perform permitted writes.
- [ ] Audit automatic mutations of the same secret destination (including removals/harden) so they honor
  the same coordination. Lock identity follows shared per-slug state across checkouts and agents, with
  a defined ordering relative to existing state locks. Do not assume per-file atomic rename prevents lost updates.
- [ ] Enforce new secret writes before dependent config installation. Skipped/declined/failed dependencies
  cannot satisfy a config just because `proposedSecretEnvVars` contained their names. Save all obeys this too.
- [ ] Compatible existing declarations permit reuse without overwriting user state; incompatibility refuses.
- [ ] Test preflight conflict = no candidate-related writes. Test secret succeeds/config fails = failure
  reported, compatible existing config retained unless installation itself already occurred, no automatic
  secret deletion/rollback. Handle `atomicwrite.ErrInstalled` honestly for each file.
- [ ] Test concurrency with cooperating writers on the same repo slug and different checkouts/agents.
  Cover prompt-time changes, ordered locking, and repeated execution (`-count=2`). Do not claim protection
  against arbitrary external writers that ignore the lock or a crash-atomic multi-file transaction.
- [ ] Test the activation conflict: an already-managed snippet holds `${ENVVAR}` with no (or a removed)
  secret entry, and a new transfer proposes the same env-var name. The new secret must not be written in a
  way that silently activates the older placeholder; require compatible identity/scope or refuse. Cover the
  secret-succeeds/config-fails leftover-secret case against an older managed snippet.

## Task 6 — wire both entry points and preserve rejection semantics

**Files:** `firstrun/seed.go`, `analyser/managed_config.go`, `learn/analyse.go`, corresponding unit/flow tests;
CLI tests only where output/behavior changes are visible.

- [ ] Host seed plans from one capped snapshot and preflights candidate bindings before writing. A refused
  update retains the existing managed snippet, source, and candidate-related secret state unchanged.
- [ ] Write-back consumes the same structural transfer result and explicit dependencies. Remove the
  proposed-versus-applied authorization assumption in `learn.proposedSecretEnvVars`.
- [ ] Preserve independent valid files in a mixed batch. Report unsupported files honestly without
  converting rejection into an empty managed file, intentional removal, or successful full import.
- [ ] Test reserved snippet names, all credential-store exclusions, unknown harnesses, unsupported policies,
  preview/non-interactive behavior, review/Save all, and safe diagnostic rendering at every caller.
- [ ] Test the per-run report for an unsupported harness: no heuristic fallback runs, nothing is written,
  the message names the harness and does not claim a successful import.
- [ ] Keep refused-input diagnostics from raw parse errors, filenames, nested key names, or secret env names
  from leaking candidate values. Test terminal-control characters as well as ordinary credential text.
- [ ] Verify replay/decline semantics when sanitized config or dependency evidence changes; avoid a decline
  keyed only by filename suppressing all later legitimate revisions. Hash only non-secret authorized evidence.
- [ ] Refusal messages explain only known consequences: seed = reviewed contents withheld, unknown contents
  not guessed, manual configuration path; write-back = no export and no home edit now, but old managed
  values may return on next provisioning. Test old snippet + changed live config + refusal + subsequent
  provisioning; do not promise that refused home changes keep working or that another config source cannot exist.

## Task 7 — complete regression matrix and lifecycle/host acceptance

- [ ] Generate the original context × key × value × format × line-break matrix. Mark unrepresentable or
  unsupported cells explicitly. Use opaque values and positive controls; “all refused” is not success.
- [ ] Include #897 reference-prefix, URL-path, shell-punctuation, nested-assignment, and name/value cases;
  add dynamic-key probes, numeric/native forms, comments, escaped duplicates, and new identity/Apply cases.
- [ ] Add metamorphic tests for equivalent supported representations and unchanged unrelated values;
  add bounded fuzz targets to the relevant unit files. Finite testing is regression evidence, not proof
  of arbitrary-string secrecy. Triage meaningful mutation survivors on a host; do not run the sweep here.
- [ ] Drive accepted whole configs through seed/write-back, apply, snippet merge, and repeated provisioning.
  Assert effective permission rules, denied operations where testable, and provider config semantics.
- [ ] Update real read-back tests for #928's aligned runtime-home helper. Inject #930's host-env defaults
  rather than reaching real HOME, git identity, timezone, or locale.
- [ ] Test after-run/replay with a stopped/expired VM and available home; separately retain refusal of a
  replaced or missing home. Persisted home is not evidence that a config was exported or reset-recoverable.
- [ ] On a suitable host, run the focused UC4/learn/replay journeys and an actual provider request with an
  isolated fixture. Validate subsequent starts and home-reset reconstruction of supported managed config;
  distinguish this evidence from a unit test or a title-only provider request. Record host-only gaps honestly.

## Task 8 — documentation, release evidence, and handoff

- [ ] Replace the legacy heuristic description in `findings-and-apply.md` with the implemented contract;
  reconcile `home-read-back.md`, `onboarding.md`, configuration ownership, and scope/secret-writing specs
  with actual APIs and behavior. Remove the target-only status only after the implementation exists.
- [ ] Update README, learn/setup/agent/troubleshooting docs with supported scope, intentional refusals,
  comment removal, home-only versus exported state, and safe remediation. Do not recommend raw `sandbox cp`
  or host provisioning as if it were sanitized import. Do not claim snapshots solve reset recovery.
- [ ] Add the policy-maintenance runbook: evidence provenance, known-path semantic drift, fail-closed schema
  updates, no-schema harnesses, no runtime schema fetch. Test each claimed maintenance behavior instead of
  promising auto-support. Note in the changelog that unsupported harnesses lose automatic transfer.
- [ ] Add the new PR's changelog fragment and update code indexes for significant new units only after they
  exist. Link #897's reusable evidence; close it as superseded only once its successor is linked.
- [ ] Run `make fmt`, `make lint`, focused tests, `make check`, and focused race/repeat checks. Run the
  applicable hermetic/host suites when the environment supports them; never substitute `go vet`.
- [ ] Re-review permitting rules, identity/host boundaries, parser ambiguities, and partial-write paths.
  No closing #849 with acknowledged bypass classes or an unreviewed compatibility collapse.
- [ ] Keep durable contracts in specs and review techniques in the guide. Remove this plan after its work
  lands, as the brain-maintenance convention requires. Do not commit/push/open the implementation PR as a
  side effect of this documentation task; agree on finalization with the user.

## Completion map

| Requirement | Primary tasks |
|---|---|
| Default deny, no heuristic escape hatches, both entry points | 0, 4, 6 |
| Single interpretation, duplicate/raw-content boundary, representation matrix | 2, 7 |
| Exact transforms, no scope-merging identity collisions | 1, 3, 4 |
| No orphan references, safe failure ordering and shared destinations | 1, 5, 6 |
| Documented supported whole configs and honest compatibility assessment | 0, 7, 8 |
| Refusal preservation, redacted outcomes, stopped VM/replay | 5, 6, 7 |
| Current-main integration and real operational acceptance | 0, 7, 8 |
| #849 AC1/AC2/AC3 (matrix, no unprovable leaf, listed bypasses) | 1, 4, 7 |
| #849 AC4 amended (retain only policy-justified cases) | 0, 8 |
| #849 AC5 (spec/changelog describe actual behavior) | 8 |

## Follow-up issues (deferred by decision, not part of #849's closure)

Create each as a linked issue when #849's implementation PR opens. Ordering matters:

1. **Rejected host-import retry.** A first-run decision is recorded even when the seed refuses a file, so
   `firstrun.Offer` suppresses an unchanged host config. Provide an explicit retry that reaches a refused
   file after a policy update or a partial multi-file seed, without nagging, duplicate writes, or changing
   unrelated decisions. Do not overload sandbox `config learn` replay. **Must land before or with item 2.**
2. **Policies for further harnesses**, one per issue (codex, hermes, kimi-code, oh-my-pi, pi, …), each with
   whole-config evidence through seed, apply, provisioning, and a provider request.
3. **Write-back integrity confirmation.** Conservative classification of changed security-relevant groups
   (including deny removal and reordering) against host-owned managed state; one confirmation per sensitive
   candidate even under `Save all`, never overriding a refusal. A breaking default-behavior change for the
   changelog. Until it lands, the agent-widened write-back persistence threat is a known residual.
