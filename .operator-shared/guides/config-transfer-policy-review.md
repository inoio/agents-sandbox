# Reviewing the automatic config-transfer boundary

This is a design-review checklist for the host seed and managed-config write-back, not a claim that the
current implementation enforces these properties. The normative target is
[automatic config transfer](../specs/onboarding/learn-mode/config-transfer.md); the checklist supplies
adversarial questions and test techniques rather than a separate source of product requirements.

## Review the complete transfer, not only the permitting predicates

The security unit is a candidate's decoded configuration, its classified credentials and destinations,
the transformed output, and the secret declarations actually available when that output is applied.
A strict leaf guard alone cannot establish that contract.

- **Retain structural identity.** Classification must identify the exact credential location in the same
  immutable candidate snapshot the policy walk checks and transforms. Document-wide string replacement
  can change unrelated permission values, identifiers, or endpoints when they equal a credential. Preserve
  unrelated configuration semantics; refuse conflicting native-indirection rewrites instead of overwriting
  a sibling setting.
- **Reject ambiguous identity.** A generated env-var name is not a unique credential identity. Normalization
  can collapse different provider ids; different entries can explicitly name the same variable. Reject
  conflicting values before any write, rather than unioning their destination sets or selecting the first
  or last value. Reuse across destinations requires a deliberate, verified identity decision, not equal
  normalized names. Include collisions with existing managed secret declarations in that assessment.
- **Distinguish proposed from available secrets.** Independently accepting a managed-config finding after
  declining, skipping, or failing its secret finding must not create an orphan placeholder. Bind introduced
  references to compatible existing declarations or successfully applied dependencies. Specify write ordering
  and partial-failure behavior; dependency enforcement does not by itself provide a multi-file transaction.
- **Parse without losing evidence.** Detect duplicate keys and ambiguous/ignored documents before reducing
  input to a map. Re-encoding a map removes comments but cannot reveal discarded duplicate occurrences.
  Check normalized-key collisions separately from syntactic duplicate keys.
- **Validate transformed semantics.** A syntactically valid output is not necessarily a valid harness config.
  Preserve permission rule order and the full supported structure. Check round-trip equivalence except for
  explicitly authorized credential transformations and non-semantic comment removal.

## Make each permitting policy operational

A policy needs a harness/schema context, an exact structural location, an accepted domain, a transformation,
a rationale, and negative tests. A field inventory or one passing fixture does not establish that domain.

- URL validity, an empty query, and a token-length test do not establish that a path is credential-free.
  An endpoint such as `https://gateway.example/rose` can contain an opaque credential. Explicitly decide
  the supported endpoint-path domain; unestablished forms remain refused. Assess hostname and identifier
  assumptions too, rather than claiming arbitrary user-selected identifiers are proved non-secret.
- Dynamic provider/model/server ids, permission resources, package selectors, and reference arguments
  require their own policies. A package field can select executable code, not just label a provider.
- A v1 field shape accepted by v2 is not evidence that the v2 native equivalent is classified. Keep
  harness/schema identity explicit; a shared guest path cannot select the security policy reliably.
- Challenge schema-assisted rules with a numeric PIN inside the declared integer bounds and a credential
  equal to an enum member. Schema validity/finite cardinality alone is not a permitting rationale. A new
  property in an upstream schema must stay unpermitted until its role and domain are reviewed.
- Define the limitation honestly: schema-authorized literals cannot reveal user intent, and this is not
  prevention of arbitrary covert encoding by a malicious writer. Unsupported ordinary strings still
  default to refusal; the limitation is not permission for generic spelling-based escape hatches.

## Test outcomes and lifecycle effects

Use the original bypass matrix and representative whole configs, but also test sequences:

- classify, transform, serialize, apply, provision, and attach;
- secret acceptance, decline memory, execution failure, and compatible/incompatible existing declarations;
- colliding names with equal and unequal values and different host sets;
- repeated runs, shared per-slug scope, and home reset;
- unknown fields with empty containers, booleans, nulls, and native numeric values;
- refusal with an existing managed snippet: retain it unchanged and distinguish a refused update from
  intentional removal. Do not feed an empty replacement into reconciliation, where omitted formerly
  launcher-owned keys have removal semantics.
- overlapping permission/pattern rules in opposite orders, including mapping members where the harness
  gives them order-dependent semantics; an unordered-map round trip can hide the change;
- a rejected host seed retried after policy revision with unchanged source bytes, including independent
  files already seeded and the managed-layer predicate now true;
- security-relevant removals/reorderings, not only additions, and one confirmation for a whole candidate
  rather than prompt-per-leaf behavior or a prompt that overrides structural refusal.

Diagnostics are another output channel. Prefer fixed policy paths and indexed/redacted dynamic segments;
escaping terminal characters alone does not remove a credential embedded in a key, filename, or parser error.
Show values neither in warnings nor in findings. Distinguish unsupported content from a confirmed credential.

For compatibility evidence, keep separate counts for synthetically supported fixtures, documented examples,
and actual host-validated setups. A successful provider request proves that connection, not preservation of
MCP behavior or effective permission rules. If the owner cannot supply a non-sensitive path summary of a
private config, leave its measured compatibility unverified rather than reconstructing the file.

## Adjacent changes to include in a review

- A manual raw-copy command is another transfer mechanism, not authorization for the automatic importer.
  Check that diagnostics and remediation do not conflate the two or recommend raw copying as a safe fallback.
- Adapter/runtime-home changes can invalidate a real read-back test's premise even when sanitizer unit tests
  pass. Pin creation and read-back to the same runtime home; an empty/unavailable observation is not proof
  of no credentials.
- Host environment defaults can change VM fingerprints and hide ambient test inputs. Inject them; do not
  interpret the existence of a deliberate default producer as permission to copy arbitrary env entries.
- Lifecycle expiry tests should separate a stopped VM with a readable home from missing/replaced home state.
  Snapshot/fork proposals need a separate credential-isolation assessment and do not establish config portability.
