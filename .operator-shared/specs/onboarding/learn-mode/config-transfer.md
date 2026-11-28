---
description: "Target contract for strict automatic agent-config transfer: whole-candidate authorization, snapshot-bound credential transformation, collision refusal, effective secret dependencies, safe diagnostics, and unchanged existing configuration on refusal."
read_if: Changing SanitizeConfig, SeedFromHost, ManagedConfigFindings, credential classification/identity, generated placeholders, or their apply/provisioning dependencies; defining harness/key-path policies; or diagnosing a rejected import, orphan placeholder, or setting changed by secret substitution.
---

# Automatic config transfer

## Status and scope

This is the **target contract for #849**, not a description of a completed implementation. The current
heuristic sanitizer, text substitution, and independently applied findings do not satisfy it. The
[decision](https://github.com/inoio/agents-sandbox/issues/849#issuecomment-7224012185) selects the original
structural proposal A; the [adversarial review](https://github.com/inoio/agents-sandbox/issues/849#issuecomment-7230222580)
adds transfer-wide identity and dependency requirements.

The contract covers the first-run host seed and the after-run managed-config write-back, including
`config learn`. Both use the same authorization and transformation boundary. A candidate is one complete
agent config file; rejection of it does not prevent independent, valid candidates from being considered.
The target remains full transfer of supported semantic configuration, with authorized credential
transformations and non-semantic comment removal. There is no silent provider-only projection.

## Threat model per source

The two entry points read differently trusted input, so they protect different properties:

- **Host seed** reads the host setup the user selected, which can include generated or third-party content.
  The threat includes an accidental credential the classifier did not name crossing into the managed scope
  and the VM. Host provenance is no exemption from validation, and no config reference or command is
  executed on the host to decide whether the file is safe.
- **Write-back** reads files the sandboxed agent controls, possibly under prompt injection. Besides secrecy,
  the threat is *integrity and persistence*: a written-back managed snippet is provisioned into every later VM
  of the repo slug, including other clones and after a home reset. A setting the agent widened in the VM
  (permissions, MCP servers and their commands, hooks, plugins/packages, provider endpoints) must therefore
  not reach the host silently.

Treat any changed supported security-relevant group as sensitive, including removals and reordered rules,
not only apparent additions or widening. There is no general solver that proves one permission policy is
stricter than another. Compare against host-owned managed configuration, not an agent-editable VM baseline;
an absent or ambiguous trusted comparison is sensitive. Use existing scope/merge order when identifying
the proposed managed effect. Never remove omitted fields from that proposal merely to obtain approval.

**Deferred to a follow-up issue (not part of #849's closure).** The confirmation below is the agreed
target, tracked separately. Until it lands, `Save all` keeps its current accept-all semantics, and the
integrity/persistence threat of an agent-widened write-back is a **known residual**, not something #849
claims to close. #849's core still applies: structural authorization refuses unsupported content (an
arbitrary new MCP command or prompt is refused, not confirmed), and no credential crosses.

One write-back confirmation per candidate summarizes these changes using fixed paths, policy-authorized
non-secret literals, and indexed/redacted dynamic segments; it covers the per-slug effect on other clones.
An existing per-finding review satisfies that confirmation. Under `Save all`, a sensitive candidate still
asks once, with skip as the default. Unchanged ordinary settings do not cause per-leaf prompts. If a safe
comparison cannot be shown, state that rather than rendering raw contents. This is a target exception to
the current accept-all behavior; diagnostics and choice descriptions must advertise it, and the change is
a breaking default-behavior change to be called out in the changelog and release notes.

Confirmation occurs only after structural authorization and never overrides a refusal or a missing secret
dependency. A new arbitrary MCP command or prompt is not authorized by accepting a confirmation. Host seed
has no additional write-back-integrity confirmation beyond the first-run choice, but obeys the same
structural and secrecy checks.

## Authorization

**Every transferred entry is authorized by an explicit contextual policy or the whole candidate is refused.**
Entries include mapping keys, container structure, and empty containers, not only non-empty string leaves.
Unknown paths, unsupported value types/forms, ambiguous syntax, and unestablished policy domains refuse.
A boolean, null, number, path, URL, short lowercase word, familiar key suffix, or reference-like prefix
does not bypass that decision.

A permitting rule states:

- the explicit harness/schema context and structural path, including any dynamic-key restrictions;
- accepted types and value domains, with supported reference/template semantics where applicable;
- the allowed transformation, its security rationale and limitations;
- positive whole-config evidence and adversarial boundary cases.

A filename shared by harness versions cannot select the policy. Unsupported harnesses and schema forms
have no weaker heuristic fallback. A `$schema` supplied by the candidate is not a trusted policy selector
and causes no network fetch. Existing shape detection may add refusals, never authorize content.

**Schema-assisted policies.** A pinned schema describes validity, not credential-freedom. A six-digit PIN
fits a bounded integer, and a credential can equal an enum member. Neither a type, bounds, nor finite
cardinality alone authorizes transfer. Each eligible path needs a reviewed configuration role and value
domain, such as a particular permission-effect enum or a documented token-count limit. Credential-bearing
paths remain subject to credential transformation/refusal even if their schema uses a bounded type.

Where a reliable schema exists, reviewed path families may share schema-derived validation to avoid a
hand-written validator per field. The checked-in policy records which paths/roles it authorizes; a newly
added schema property does not become permitted automatically. Conditional schema branches, dynamic keys,
and open objects have no permissive fallback. If no suitable schema exists, use a small reviewed policy
backed by versioned documentation and fixtures rather than making schema generation a prerequisite.

Schema-authorized literals do not establish user intent: a value identical to an allowed setting literal
cannot be distinguished from a credential by intent. This is a bounded supported transfer schema, not
prevention of arbitrary covert encoding by a malicious writer. That limitation does not authorize
unrestricted identifiers, prompts, commands, permission resources, package selectors, or endpoint paths.

## One candidate snapshot and one structural interpretation

Candidate bytes are read once under the read-back cap. Classification, endpoint attribution, policy
authorization, transformation, and the final output all refer to that immutable snapshot. The baseline
manifest detects changes; it is not evidence that the later candidate has already been authorized.
Sandbox-home reads retain the containment, regular-file, non-blocking, and bounded-read requirements in
[home read-back](home-read-back.md#reading-an-agent-controlled-home).

Duplicate mapping keys are rejected before reduction to a map loses an occurrence. Escaped duplicate
spellings count as duplicates. Relevant normalization collisions are checked separately; normalization
never silently combines identities. Additional documents, trailing input, unsupported native scalars,
and parser ambiguities cannot be ignored. Nesting/node and expansion work is bounded as well as input size;
unsupported aliases, tags, or syntax are refused rather than guessed.

The transfer decoder stays within the common [config-decoding boundary](../../configuration/config-file-decoding.md).
It may require a stricter, explicit API than ordinary config loading; no independently maintained
secret-scanning grammar may override parser interpretation. Unsupported syntax or formats remain refused.
The supported syntax set is documented and tested rather than presented as all JSON5/YAML/TOML by default.

Output is serialized from the authorized structure, not copied from the original text. Non-semantic
comments are omitted. No semantic setting is dropped to make a candidate pass. Decoding the serialized
output preserves the supported structure, scalar semantics, and ordered permission rules, except at
explicitly authorized credential locations. Serialization failure refuses the candidate.
Order preservation includes object/mapping members wherever a harness uses their order, not only arrays.
A duplicate-aware decoder that then loses meaningful member order is insufficient. Preserve that order
through the transfer document and serializer, or refuse a shape whose semantics cannot be preserved.

## Credential transformation and identity

A classified credential carries its exact source location and endpoint association in memory.
Transformation changes only authorized credential locations. Equal text in another setting is not a
reason to replace it. A native-indirection rewrite that conflicts with a sibling setting is refused.
Known raw credential values remaining anywhere in output, including keys, cause refusal rather than
document-wide replacement. If such a value also equals an unrelated required setting literal, refusal
is preferable to changing that setting. Renewing login stores remain excluded.

An env-var name or generated placeholder is an output binding, not a credential identity. Different
credential values cannot share that binding. Source ids collapsed by normalization cannot silently
merge values or host scopes. Equality of values alone does not grant permission to union destinations:
reuse requires explicit, independently established compatible identity and scope. Otherwise the transfer
is refused with a non-value-bearing conflict reason.

An existing declaration is reused only when its value binding, placeholder, host/header scope, effective
precedence, and harness reference semantics are compatible. A broader existing scope, a different value,
an unresolved/shadowed declaration, or ambiguous duplicate names is a conflict, not permission to
overwrite, widen, or select a first/last credential. New declarations never set a dangerous-any-host flag.

Credential values stay in memory and the secret writer; they do not enter findings, logs, or persisted
dependency records. Dependency metadata identifies structural bindings, not raw values or persistent
value-derived fingerprints.

## Applying dependent writes

Authorization happens before candidate-related writes. On acceptance, required new secret declarations
are written before the managed config. A config is installed only when all references introduced by the
transfer have compatible declarations actually available, not merely proposed. Existing native references
also need an explicit context policy; recognizing reference syntax is insufficient.

Review mode preserves per-finding consent: refusing a required secret does not silently accept it through
the config. The dependent config is skipped with a safe explanation unless a compatible existing
declaration independently satisfies the dependency. Remembered declines, shadowing, and dependency
failures have the same effect. `Save all` changes prompting only and bypasses no safety condition.

Preflight checks all candidate/dependency conflicts before its first write. The availability check and
installation must not race with another cooperating transfer writing the same per-slug bindings; lock
scope follows the shared repo-slug destination, not only the checkout or agent. Locks are not held across
interactive prompts. Preflight is revalidated under the write lock; all participating automatic secret
mutation paths must honor the same coordination. Arbitrary external edits are not a transactional guarantee.

There is **no claim of crash-atomic multi-file installation**. If a new secret write succeeds and config
installation fails, the new secret may remain and the failure is reported; existing user declarations
are never automatically rolled back or deleted. No config is intentionally installed after a dependency
failure. Per-file installed-but-not-durable errors retain their meaning from the
[atomic-write contract](../../state/state-model.md#installed-write-marker); do not report an already
installed file as untouched or destructively roll it back.

Writing the secret before the config assumes the binding is new. A previously managed config can already
carry a placeholder for the same env-var name whose secret entry is absent or was removed; adding the
secret then changes that existing placeholder's behavior even though the config write never ran. Treat that
case as a binding conflict and refuse unless the existing declaration and the new one are independently
compatible. A leftover secret after a failed config install must not silently re-activate a placeholder in
an older managed snippet.

## Refusal, existing state, and diagnostics

A validation refusal writes neither a replacement nor an empty marker for that candidate, changes no
source file, and leaves its existing managed snippet unchanged. Refusal is distinct from intentional
removal: do not feed a missing/empty replacement into reconciliation, where omitted previously owned
keys have removal semantics. A stricter implementation does not retroactively certify existing managed
configs; auditing/remediation of those is explicit and must not silently delete user settings.

Diagnostics distinguish unsupported content, identified credentials, invalid syntax, and binding
conflicts. They use stable reasons and fixed policy paths with indexed/redacted dynamic segments, not
raw values, untrusted key/file segments, or parser excerpts. Name the offending entry by its config-relative
path with control characters escaped; do not drop the location or dump its contents, since an unidentifiable
refusal is unusable. Unsupported content is not falsely described as a confirmed credential. Non-interactive and preview
runs perform no proposal writes. Runtime expiry or a stopped VM does not require a live harness for
home read-back; missing/replaced home state retains the existing replay refusal behavior.

A refusal states its consequence and next step, per source. For the host seed: the file was not imported;
report which reviewed portions were withheld, without claiming to understand unknown contents or that
the harness cannot have another configuration source. Show the manual-configuration path; mention an
explicit retry only once the follow-up retry surface exists.
For write-back: no new managed version is installed, and this action does not change the home file.
Do not promise that version keeps working: the next start can reapply older launcher-owned values from
an existing managed snippet. The rejected changes are not made portable or reset-recoverable; an older
managed version may still be restored or used by other clones. A refusal never recommends raw host
provisioning or manual copying as if it were a checked import.

The managed config remains ordinary placeholder-bearing configuration and new secret files remain
private. Existing file-mode and scope/trust rules are not relaxed by transfer authorization.

## Support and compatibility acceptance

Today the heuristic transfer serves every harness that declares read-back or seed config paths (opencode,
opencode2, codex, hermes, kimi-code, oh-my-pi, pi, among others). The strict boundary refuses a harness
without policies, so enabling it without coverage silently ends automatic transfer for those harnesses.
Each harness whose config the heuristic transfers today either gets policies for at least its provider
entry and credential form before the strict path replaces the heuristic, or its loss of automatic transfer
is an explicit, documented release decision reported per run ("automatic transfer is not supported for
<harness> yet"). Once the strict path ships, the heuristic remains the path for no harness.

**Scope split.** #849 ships the strict boundary with policies for the initial evidence harness, the
no-weaker-fallback rule, and the per-run report for every other harness. That loss of automatic transfer
for the remaining harnesses is a breaking default-behavior change called out in the changelog and release
notes. Adding policies for further harnesses is follow-up work, one harness at a time.

The initial evidence harness is `opencode2`, with v1-compatible and native-v2 forms assessed separately.
The repository provider journey and both documented permission examples are inputs to the assessment,
not an automatic grant for arbitrary values at similar paths. Finite enum support does not authorize
arbitrary resource patterns or dynamic keys. Endpoint-path and identifier domains need concrete reviewed
rules before they can be enabled; until then those candidates are refused.

The old accepted set is retained only where explicit policies justify it. Compatibility evidence
separates unit fixtures, documentation examples, and host-validated complete setups. The private 40/3
measurements from #897 are not reproducible from the published summary alone. Necessary refusals are
documented. A meaningful supported whole config must work through seed, apply, provisioning, a provider
request, and subsequent starts; passing leaf tests alone is not feature acceptance.

## Keeping support current and retrying an import

Support ships with agents-sandbox as reviewed policy revisions, identified separately from the input-file
digest. Each revision records its harness/schema context, evidence version/source, and positive and
negative fixtures. Optional schema snapshots are pinned to immutable upstream revisions with provenance;
configuration cannot select or download another schema at run time.

On a harness/schema update, compare it with the recorded support: distinguish new fields, changed value
domains, removals, and changed semantics at existing paths. Only reviewed policy changes widen support;
merging a schema snapshot alone does not. A newer harness release does not automatically authorize new
contents, nor is the version number alone a reason to reject a config using a still-established supported
schema context. An unestablished interpretation is refused. No schema-change bot is required for the
initial fix; the release/update checklist owns this review, and automation may prepare reviewable diffs.

A rejected host import has a documented explicit retry path even when its source bytes are unchanged,
including after a policy update or after independent files/instructions created a managed layer. A prior
rejection is not successful import, and is distinct from a user decline. A new applicable policy revision
must not leave it permanently suppressed by the source hash/managed-layer predicate. Ordinary launches
must not repeatedly nag or silently retry writes; keep retry explicit and preserve unrelated decisions.
Determine the minimal existing or additional retry surface before feature enablement rather than
recommending `config learn` as a host-import command when it only replays sandbox analysis.

**Deferred to a follow-up issue, with an ordering constraint.** Today a first-run choice records the
decision even when the seed refuses a file, so an unchanged host config stays suppressed. The retry path
is follow-up work, but it **must land before or with the first follow-up that adds support for another
harness or widens a policy**: otherwise users refused under #849 can never reach the import that a later
release would accept. Until then, #849's refusal message must not imply that a later update will import
the file automatically.

## Relationship to #849's acceptance criteria

#849 states five acceptance criteria. This target meets or amends them as follows, so closure is auditable
rather than asserted:

- **AC1 (table-driven matrix test in `internal/sandbox/readback`).** Met by Task 7; the matrix asserts the
  fail-closed outcome per cell and marks unsupported cells explicitly. Where the strict walker replaces the
  old deciders, the matrix targets the new boundary rather than the removed heuristics.
- **AC2 (no unprovable string leaf in output).** Met as a bounded supported-schema claim, not proof about
  arbitrary strings; the honest limitation for a literal that equals an allowed value is stated above.
- **AC3 (the listed bypass spellings red before, green after).** Met by Task 1 and Task 7 regressions.
- **AC4 (keep the whole previously accepted set green).** **Amended.** The decision supersedes it: the old
  accepted set is retained only where explicit policies justify it, and necessary refusals are documented.
  This criterion is not silently dropped; the compatibility assessment records what is retained and lost.
- **AC5 (over-refusals fixed or removed; spec and `changelog.d/832.fixed.md` describe the code).** Met in
  Task 8 by reconciling `findings-and-apply.md` and the spec with the implemented behavior.

The write-back integrity confirmation, the rejected-import retry, and policies for harnesses beyond the
initial evidence harness are **deferred to linked follow-up issues by decision**; #849 closes on its core
(AC1–AC3, amended AC4, AC5, both entry points, refusal preserving existing state). The per-run report for
unsupported harnesses and the no-weaker-fallback rule stay in #849, since without them the strict switch
would silently drop transfer.

## Adjacent surfaces

- Explicit raw host provisioning and user-invoked file copy (`sandbox cp`, when available) are separate
  trust decisions, not sanitizer fallbacks or evidence of checked config transfer. This issue makes no
  claim to sanitize arbitrary manual copying, mounted workspace files, or instruction/Markdown seeding.
- Host environment defaults are a separate producer. They authorize no general env-table exception;
  tests isolate them rather than read host identity or locale.
- Runtime-home resolution in real read-back tests must name the same private microsandbox home as
  volume creation. Unknown runtime observations do not prove absence of configuration or credentials.
- VM lifetime bounds and future snapshot/fork features do not change this boundary. Home persistence
  is not export or reset recovery; reusable snapshots are not automatically credential-safe baselines.
