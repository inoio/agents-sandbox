---
description: Contract for scoping a secret's placeholder substitution to named request-header fields via env.secret.yaml header_fields (#150): the lowercase/dedupe/sort normalization shared by the built entry and the fingerprint, the all-headers default for an absent or empty list, and the HeaderFields fold in SecretsContentHash.
read_if: Changing per-secret header substitution, envsecret.SecretSpec/BuildSecretsFromSpecs, docs/configuration/secrets.md's header_fields, or state.SecretsContentHash's field set.
---

# Secret header scoping

microsandbox substitutes a secret's placeholder in request locations selected by `SecretSubstitution`.
Header substitution is all-headers by default, which lets an untrusted guest place the placeholder in
a header the upstream reflects back and read the real secret from the response. `header_fields`
scopes substitution to named header fields, closing that reflected-header exfiltration path.

## Contract

- `envsecret.SecretSpec` carries `HeaderFields []string` (`yaml:"header_fields"`), parsed only from
  `env.secret.yaml` / JSON. The legacy `env.secret` line format cannot carry it, like the deferred
  per-secret `allow_placeholder_for`.
- `envsecret.BuildSecretsFromSpecs` normalizes the list once — lowercase, dedupe, sort — and uses that
  same slice for both the built entry's `Substitution.HeaderFields` and the fingerprint. A header name
  carries no meaningful case, so the normalization is lossless and makes a case- or order-only edit
  produce an identical entry (and an identical fingerprint).
- An absent or empty `header_fields` normalizes to nil, which is the SDK's all-headers default.
  `Substitution.Headers` stays nil so the SDK's `true` default also applies. Nothing about a project
  that omits the key changes.
- `state.SecretsContentHash` folds the (normalized) `Substitution.HeaderFields` as its own field, so a
  behaviorally-distinct scope change triggers exactly one VM recreate. Header scoping is baked into the
  VM at creation, so omitting it from the hash would leave the VM running the old all-headers scope
  until some other change forced a recreate.

## Boundaries

- Query and body substitution stay on/off booleans; this contract scopes headers only.
- The default stays all-headers. There is no built-in safe-field list; scoping is opt-in, because a
  guessed default silently breaks secrets sent in non-standard headers (GitLab `private-token`, Azure
  `api-key`, Google `x-goog-api-key`).
- The reflected-header rationale and the proxy model live in
  `guides/secret-placeholder-transcript-trap.md`; the user-facing surface is
  `docs/configuration/secrets.md`.
