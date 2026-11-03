# Secret Header-Field Scoping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `env.secret.yaml` scope a secret's placeholder substitution to named request-header fields, closing the reflected-header exfiltration path, while the absent/empty default keeps today's all-headers behavior.

**Architecture:** `envsecret.SecretSpec` gains `HeaderFields`; `BuildSecretsFromSpecs` normalizes it once (lowercase, dedupe, sort) and uses that slice for both the built entry's `Substitution.HeaderFields` and the fingerprint. `state.SecretsContentHash` folds the field so a scope change recreates the VM. `Substitution.Headers` stays nil, so the SDK's `true` all-headers default applies.

**Tech Stack:** Go 1.26, microsandbox SDK `github.com/superradcompany/microsandbox/sdk/go` v0.7.11 (`SecretSubstitution.HeaderFields`), `go.yaml.in/yaml/v3`, standard `testing`.

**Spec:** `.operator-shared/specs/secret-header-scoping.md` (design: `docs/superpowers/specs/2026-11-03-secret-header-fields-design.md`).

## Global Constraints

- `header_fields` is per-secret `env.secret.yaml` input, not a launcher `config.yaml` key. The legacy `env.secret` line format cannot carry it.
- Absent or empty `header_fields` means all headers — the SDK's contract. An empty slice and a nil slice must both normalize to nil and change nothing.
- Normalization is lowercase → dedupe → sort, nil when empty; the exact same slice is used for the built entry and the fingerprint, so a case- or order-only edit produces an identical entry.
- `Substitution.Headers` stays nil; only `Substitution.HeaderFields` is set.
- Run `make fmt` and `make lint` after edits; run tests with `make test` or `go test -tags microsandbox_ffi_path ./...`.
- Changelog fragment type vocabulary is `added`/`changed`/`deprecated`/`docs`/`fixed`; this change is `added`.

## Review Focus

- **Absent `header_fields` (existing file, upgrade):** the built entry must have an empty `HeaderFields` so the SDK substitutes in every header — no behavior change, no recreate (Task 1).
- **Explicit `header_fields: []`:** must also mean all-headers, not "scope to nothing" (Task 1).
- **Case/order/duplicate-only difference:** must produce an identical built entry, so the fingerprint does not flip and no spurious recreate happens (Task 1).
- **A genuinely different field set:** must flip `SecretsContentHash` so exactly one recreate applies the scope (Task 2).
- **YAML shape:** `header_fields` parses beside `host`/`hosts`/`allow_any_host_dangerous` and does not disturb the existing merge/precedence behavior (Task 1).

---

### Task 0: Land the design docs

**Files:**
- Add: `.operator-shared/specs/secret-header-scoping.md` (already written)
- Modify: `.operator-shared/catalog.md` (entry already added)
- Add: `docs/superpowers/specs/2026-11-03-secret-header-fields-design.md` (already written)
- Add: `.operator-shared/plans/2026-11-03-secret-header-fields.md` (this file)

- [ ] **Step 1: Commit the design docs**

```bash
git add .operator-shared/specs/secret-header-scoping.md .operator-shared/catalog.md \
  docs/superpowers/specs/2026-11-03-secret-header-fields-design.md \
  .operator-shared/plans/2026-11-03-secret-header-fields.md
git commit -m "docs: spec secret header-field scoping (#150)"
```

---

### Task 1: Carry and normalize `header_fields` in `envsecret`

**Files:**
- Modify: `internal/sandbox/envsecret/secrets.go`
- Test: `internal/sandbox/envsecret/secrets_test.go`

**Interfaces:**
- Consumes (existing): `SecretSpec`, `BuildSecretsFromSpecs(specs map[string]SecretSpec, allowPlaceholderFor []string, ui termio.UI) []msbSdk.SecretEntry`, `msbSdk.Secret.Env`, `msbSdk.SecretSubstitution`.
- Produces: `SecretSpec.HeaderFields []string` (`yaml:"header_fields"`); `normalizeHeaderFields(fields []string) []string` (unexported, returns lowercase/deduped/sorted or nil).

- [ ] **Step 1: Write the failing tests**

Add to `internal/sandbox/envsecret/secrets_test.go`:

```go
func TestParseSecretSpecYAMLHeaderFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.secret.yaml")
	testutil.WritePath(t, path, "FOO:\n  value: x\n  host: h.example\n  header_fields: [X-Api-Key, Authorization]\n")
	testUI := termio.NewTestMock(t)

	specs := ParseSecretSpecYAML(path, &testUI)
	want := map[string]SecretSpec{"FOO": {
		Value: "x", Host: "h.example", HeaderFields: []string{"X-Api-Key", "Authorization"},
	}}
	if !reflect.DeepEqual(specs, want) {
		t.Errorf("got %#v, want %#v", specs, want)
	}
}

func TestBuildSecretsFromSpecsHeaderFieldsScoped(t *testing.T) {
	testUI := termio.NewTestMock(t)
	in := map[string]SecretSpec{"K": {
		Value: "v", Host: "h.example", HeaderFields: []string{"authorization", "x-api-key"},
	}}
	secrets := BuildSecretsFromSpecs(in, nil, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if !slices.Equal(secrets[0].Substitution.HeaderFields, []string{"authorization", "x-api-key"}) {
		t.Errorf("HeaderFields = %v, want [authorization x-api-key]", secrets[0].Substitution.HeaderFields)
	}
}

func TestBuildSecretsFromSpecsHeaderFieldsAbsentIsAllHeaders(t *testing.T) {
	testUI := termio.NewTestMock(t)
	in := map[string]SecretSpec{"K": {Value: "v", Host: "h.example"}}
	secrets := BuildSecretsFromSpecs(in, nil, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if len(secrets[0].Substitution.HeaderFields) != 0 {
		t.Errorf("HeaderFields = %v, want empty so all headers substitute", secrets[0].Substitution.HeaderFields)
	}
}

func TestBuildSecretsFromSpecsHeaderFieldsEmptySliceIsAllHeaders(t *testing.T) {
	testUI := termio.NewTestMock(t)
	in := map[string]SecretSpec{"K": {Value: "v", Host: "h.example", HeaderFields: []string{}}}
	secrets := BuildSecretsFromSpecs(in, nil, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if len(secrets[0].Substitution.HeaderFields) != 0 {
		t.Errorf("HeaderFields = %v, want empty so all headers substitute", secrets[0].Substitution.HeaderFields)
	}
}

// TestBuildSecretsFromSpecsHeaderFieldsCanonical pins that case-, order- and
// duplicate-only differences collapse to one canonical slice, so the built
// entry (and its fingerprint) is identical for equivalent inputs.
func TestBuildSecretsFromSpecsHeaderFieldsCanonical(t *testing.T) {
	testUI := termio.NewTestMock(t)
	in := map[string]SecretSpec{"K": {
		Value: "v", Host: "h.example",
		HeaderFields: []string{"X-Api-Key", "authorization", "x-api-key", "AUTHORIZATION"},
	}}
	secrets := BuildSecretsFromSpecs(in, nil, &testUI)
	if len(secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secrets))
	}
	if !slices.Equal(secrets[0].Substitution.HeaderFields, []string{"authorization", "x-api-key"}) {
		t.Errorf("HeaderFields = %v, want [authorization x-api-key]", secrets[0].Substitution.HeaderFields)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/envsecret/ -run 'HeaderFields' -v`
Expected: FAIL — `SecretSpec` has no `HeaderFields`, so the package does not compile.

- [ ] **Step 3: Add the field and normalization, wire it into the built entry**

In `internal/sandbox/envsecret/secrets.go`:

Add to `SecretSpec`:

```go
HeaderFields []string `yaml:"header_fields"`
```

Add the helper:

```go
// normalizeHeaderFields lowercases, deduplicates and sorts header field names
// so case- or order-only edits compare equal, and returns nil when nothing
// remains (an empty list means every header may be substituted).
func normalizeHeaderFields(fields []string) []string {
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(fields))
	normalized := make([]string, 0, len(fields))
	for _, field := range fields {
		lower := strings.ToLower(field)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		normalized = append(normalized, lower)
	}
	sort.Strings(normalized)
	return normalized
}
```

In `BuildSecretsFromSpecs`, set the substitution on the existing `Secret.Env` call:

```go
			msbSdk.SecretEnvOptions{
				Allow:               hosts,
				AllowPlaceholderFor: placeholderHostsFor(hosts, allowPlaceholderFor),
				Substitution: msbSdk.SecretSubstitution{
					HeaderFields: normalizeHeaderFields(spec.HeaderFields),
				},
			},
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/envsecret/ -v`
Expected: PASS (all tests, including the pre-existing ones).

- [ ] **Step 5: Format, lint and commit**

```bash
make fmt
make lint
git add internal/sandbox/envsecret/secrets.go internal/sandbox/envsecret/secrets_test.go
git commit -m "feat(envsecret): scope secret substitution to header_fields (#150)"
```

---

### Task 2: Fold `HeaderFields` into the secret fingerprint

**Files:**
- Modify: `internal/sandbox/state/fingerprint.go`
- Test: `internal/sandbox/state/fingerprint_test.go`

**Interfaces:**
- Consumes (existing): `SecretsContentHash(entries []msbSdk.SecretEntry) string`, `msbSdk.SecretSubstitution.HeaderFields []string`.
- Produces: no signature change; `SecretsContentHash`'s record line gains the joined `Substitution.HeaderFields`.

- [ ] **Step 1: Write the failing test**

In `internal/sandbox/state/fingerprint_test.go`, add a case to the `tests` slice in `TestSecretsContentHashIncludesOtherFields`:

```go
		{"Substitution header fields", func(e *msbSdk.SecretEntry) { e.Substitution.HeaderFields = []string{"authorization"} }},
```

Add a test that a scoped entry hashes differently from an unscoped one with the same value:

```go
func TestSecretsContentHashIncludesHeaderFieldsScope(t *testing.T) {
	// A change to a secret's header scope (same value, same hosts) must
	// produce a different hash so the launcher recreates the VM. See
	// SecretsContentHash.
	allHeaders := SecretsContentHash([]msbSdk.SecretEntry{
		{EnvVar: "K", Value: "v", Allow: []string{"h.example"}},
	})
	scoped := SecretsContentHash([]msbSdk.SecretEntry{
		{EnvVar: "K", Value: "v", Allow: []string{"h.example"}, Substitution: msbSdk.SecretSubstitution{HeaderFields: []string{"authorization"}}},
	})
	if allHeaders == scoped {
		t.Error("SecretsContentHash should differ when Substitution.HeaderFields changes")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/state/ -run 'SecretsContentHash' -v`
Expected: FAIL — both new assertions report the hashes are equal because the field is not folded.

- [ ] **Step 3: Fold the field into the hash**

In `internal/sandbox/state/fingerprint.go`, inside `SecretsContentHash`, extend the `fmt.Fprintf` record line with one more field — the joined header fields — and pass it. Add a format verb and the corresponding argument so the record stays one `|`-separated line per secret:

```go
		fmt.Fprintf(&b, "%s=%s|%s|%s|%s|%s|%s|%t|%t|%s|%s\n",
			k,
			e.Value,
			strings.Join(e.Allow, ","),
			strings.Join(e.Passthrough, ","),
			e.Placeholder,
			tls,
			headers,
			e.Substitution.Query,
			e.Substitution.Body,
			string(e.ViolationAction),
			strings.Join(e.Substitution.HeaderFields, ","),
		)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/state/ -v`
Expected: PASS.

- [ ] **Step 5: Format, lint and commit**

```bash
make fmt
make lint
git add internal/sandbox/state/fingerprint.go internal/sandbox/state/fingerprint_test.go
git commit -m "fix(state): fold secret header_fields into the secret fingerprint (#150)"
```

---

### Task 3: Document `header_fields` and add the changelog fragment

**Files:**
- Modify: `docs/configuration/secrets.md`
- Add: `changelog.d/150.added.md`

**Interfaces:**
- Consumes: the `added` changelog type from the fragment vocabulary; the `header_fields` behavior from Tasks 1–2.
- Produces: user-facing docs and a changelog fragment only; no code.

- [ ] **Step 1: Document the key in the YAML format section**

In `docs/configuration/secrets.md`, extend the YAML-format prose and example to show `header_fields`. State that microsandbox substitutes the placeholder in every request header by default, that `header_fields` restricts substitution to the named fields, that an absent or empty list keeps the all-headers default, and that an untrusted guest could otherwise place the placeholder in a reflected header to read the secret back. Follow `STYLEGUIDE.md` and keep the Jekyll link rules (in-page anchors used by other docs must still resolve).

- [ ] **Step 2: Add the changelog fragment**

Create `changelog.d/150.added.md` as a single-bullet entry, e.g.:

```markdown
Secrets: `env.secret.yaml` accepts a per-secret `header_fields` list to scope placeholder substitution to named request headers, closing the reflected-header exfiltration path; omitting it keeps the all-headers default (#150).
```

- [ ] **Step 3: Verify docs and fragment**

Run: `make check-docs 2>/dev/null || bash ci/check-docs.sh`
Expected: PASS (no broken links or style errors).

Run: `bash ci/check-changelog.sh`
Expected: PASS (the fragment is present and well-formed).

- [ ] **Step 4: Commit**

```bash
git add docs/configuration/secrets.md changelog.d/150.added.md
git commit -m "docs: document secret header_fields (#150)"
```
