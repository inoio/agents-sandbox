package envsecret

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	oneyaml "oneyaml.dev/go"

	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// readBackSpecs parses dir/env.secret.yaml through the loader the VM build
// uses, so a test asserts the written file round-trips rather than only
// matching an expected byte string.
func readBackSpecs(t *testing.T, dir string) map[string]SecretSpec {
	t.Helper()
	testUI := termiotest.NewTestMock(t)
	return ParseSecretSpecYAML(filepath.Join(dir, "env.secret.yaml"), &testUI)
}

func TestWriteSecretSpecCreatesFileMode0600(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "s3cret", Host: "api.example"}))

	path := filepath.Join(dir, "env.secret.yaml")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	want := map[string]SecretSpec{"FOO": {Value: "s3cret", Host: "api.example"}}
	assert.Equal(t, want, readBackSpecs(t, dir))
}

func TestWriteSecretSpecSecondKeyPreservesFirst(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "first", Host: "a.example"}))

	require.NoError(t, WriteSecretSpec(dir, "BAR", SecretSpec{Value: "second", Hosts: []string{"b.example"}}))

	want := map[string]SecretSpec{
		"FOO": {Value: "first", Host: "a.example"},
		"BAR": {Value: "second", Hosts: []string{"b.example"}},
	}
	assert.Equal(t, want, readBackSpecs(t, dir))
}

// TestWriteSecretSpecPreservesCommentsAndOtherKeys pins the merge contract: the
// file is edited in place, so a comment and an unrelated entry survive the
// write. The unrelated entry is itself a secret-shaped mapping, because the
// loader reads the whole file into SecretSpec values and a scalar there would
// make the file unparseable for it.
func TestWriteSecretSpecPreservesCommentsAndOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	require.NoError(t, os.WriteFile(path, []byte(
		"# keep this comment\n"+
			"FOO:\n  value: old\n  host: a.example\n"+
			"# trailing comment\n"+
			"BAR:\n  value: kept\n  host: c.example\n",
	), 0o600))

	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "new", Host: "b.example"}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "# keep this comment")
	assert.Contains(t, string(data), "# trailing comment")
	assert.Equal(t, map[string]SecretSpec{
		"FOO": {Value: "new", Host: "b.example"},
		"BAR": {Value: "kept", Host: "c.example"},
	}, readBackSpecs(t, dir))
}

// TestWriteSecretSpecReplacesExistingKey pins that a replacement is a whole
// entry replace, not a field merge: a field the previous spec set and the new
// one does not must not survive, or the earlier spec would leak into the new
// secret's host set.
func TestWriteSecretSpecReplacesExistingKey(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "old", Host: "a.example"}))

	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "new", Hosts: []string{"b.example"}}))

	want := map[string]SecretSpec{"FOO": {Value: "new", Hosts: []string{"b.example"}}}
	assert.Equal(t, want, readBackSpecs(t, dir))
}

// TestWriteSecretSpecRefusesEmptyValue pins the contract that a spec the VM
// build would not honour is refused rather than persisted: an empty value
// substitutes nothing.
func TestWriteSecretSpecRefusesEmptyValue(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "keep", Host: "a.example"}))
	before := readFile(t, filepath.Join(dir, "env.secret.yaml"))

	err := WriteSecretSpec(dir, "BAR", SecretSpec{Value: "", Host: "b.example"})

	require.ErrorContains(t, err, "value is empty")
	assert.Equal(t, before, readFile(t, filepath.Join(dir, "env.secret.yaml")))
}

// TestWriteSecretSpecRefusesHostlessSpec pins the second half of the contract:
// a spec with neither hosts nor the explicit dangerous flag is dropped from the
// built entries, so writing it would persist a secret the VM never provisions.
func TestWriteSecretSpecRefusesHostlessSpec(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "keep", Host: "a.example"}))
	before := readFile(t, filepath.Join(dir, "env.secret.yaml"))

	err := WriteSecretSpec(dir, "BAR", SecretSpec{Value: "orphan"})

	require.ErrorContains(t, err, "no hosts and no allow_any_host_dangerous")
	assert.Equal(t, before, readFile(t, filepath.Join(dir, "env.secret.yaml")))
}

// TestWriteSecretSpecAcceptsDangerousFlag pins that the explicit dangerous flag
// is a host substitute: BuildSecretsFromSpecs provisions such a secret, so the
// writer must not refuse it.
func TestWriteSecretSpecAcceptsDangerousFlag(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, WriteSecretSpec(dir, "DANGER", SecretSpec{Value: "v", AllowAnyHostDangerous: true}))

	want := map[string]SecretSpec{"DANGER": {Value: "v", AllowAnyHostDangerous: true}}
	assert.Equal(t, want, readBackSpecs(t, dir))
}

// TestWriteSecretSpecWritesEveryField pins that a spec with a host list and
// header fields survives the round trip unchanged, so the applier can persist
// an attributed learned secret.
func TestWriteSecretSpecWritesEveryField(t *testing.T) {
	dir := t.TempDir()
	spec := SecretSpec{
		Value:        "multi\nline\nvalue",
		Hosts:        []string{"a.example", "b.example"},
		HeaderFields: []string{"authorization"},
	}

	require.NoError(t, WriteSecretSpec(dir, "FULL", spec))

	assert.Equal(t, map[string]SecretSpec{"FULL": spec}, readBackSpecs(t, dir))
}

// TestWriteSecretSpecRoundTripsPlaceholder pins that an explicit placeholder
// survives the write/parse round trip, so a later build reads back the same
// placeholder the managed config file carries.
func TestWriteSecretSpecRoundTripsPlaceholder(t *testing.T) {
	dir := t.TempDir()
	spec := SecretSpec{Value: "s3cret", Host: "api.example", Placeholder: PlaceholderFor("FOO")}

	require.NoError(t, WriteSecretSpec(dir, "FOO", spec))

	assert.Equal(t, map[string]SecretSpec{"FOO": spec}, readBackSpecs(t, dir))
}

// TestWriteSecretSpecRefusalLeavesMissingFileAbsent pins that a refused write
// creates nothing: the guard runs before the file is touched.
func TestWriteSecretSpecRefusalLeavesMissingFileAbsent(t *testing.T) {
	dir := t.TempDir()

	err := WriteSecretSpec(dir, "BAR", SecretSpec{Value: ""})

	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(dir, "env.secret.yaml"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

// TestWriteSecretSpecRejectsEmptyKey pins that an empty environment-variable
// name is refused: the runtime refuses to boot with an empty env name, and the
// key is the file's YAML key.
func TestWriteSecretSpecRejectsEmptyKey(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "keep", Host: "a.example"}))
	before := readFile(t, filepath.Join(dir, "env.secret.yaml"))

	err := WriteSecretSpec(dir, "", SecretSpec{Value: "v", Host: "a.example"})

	require.ErrorContains(t, err, "environment variable name is empty")
	assert.Equal(t, before, readFile(t, filepath.Join(dir, "env.secret.yaml")))
}

// TestWriteSecretSpecRejectsUnparseableFile pins that a file the env.secret.yaml
// loader cannot parse is never rewritten: editing it would silently drop what
// the launcher previously ignored with a warning.
func TestWriteSecretSpecRejectsUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	malformed := "{ not valid yaml\n"
	require.NoError(t, os.WriteFile(path, []byte(malformed), 0o600))

	err := WriteSecretSpec(dir, "FOO", SecretSpec{Value: "v", Host: "a.example"})

	require.Error(t, err)
	assert.Equal(t, malformed, readFile(t, path))
}

// TestWriteSecretSpecSurfacesANonMappingRoot pins that a file whose root cannot
// hold a mapping key is refused rather than rewritten.
func TestWriteSecretSpecSurfacesANonMappingRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	require.NoError(t, os.WriteFile(path, []byte("42\n"), 0o600))

	err := WriteSecretSpec(dir, "FOO", SecretSpec{Value: "v", Host: "h.example"})

	require.Error(t, err)
	assert.Equal(t, "42\n", readFile(t, path))
}

// TestWriteSecretSpecSurfacesAnAmbiguousDuplicateKey pins that a key occurring
// twice has no unambiguous target, so the write is refused and the file is left
// untouched rather than editing an arbitrary one of the entries.
func TestWriteSecretSpecSurfacesAnAmbiguousDuplicateKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	content := "FOO:\n  value: v\n  host: h.example\nFOO:\n  value: w\n  host: h.example\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	err := WriteSecretSpec(dir, "FOO", SecretSpec{Value: "v2", Host: "h.example"})

	require.Error(t, err)
	require.Error(t, errors.Unwrap(err), "the pointer error must be preserved with %w")
	assert.Equal(t, content, readFile(t, path))
}

// TestRemoveSecretSpecSurfacesAnAmbiguousDuplicateKey covers the same ambiguous
// key through the remover, so both entry points fail closed.
func TestRemoveSecretSpecSurfacesAnAmbiguousDuplicateKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	content := "FOO:\n  value: v\n  host: h.example\nFOO:\n  value: w\n  host: h.example\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	err := RemoveSecretSpec(dir, "FOO")

	require.Error(t, err)
	require.Error(t, errors.Unwrap(err), "the pointer error must be preserved with %w")
	assert.Equal(t, content, readFile(t, path))
}

// TestRemoveSecretSpecRefusesAnAnchorInUse pins that removing an anchored entry
// an alias elsewhere references is refused: the alias would be left dangling.
func TestRemoveSecretSpecRefusesAnAnchorInUse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	content := "FOO: &x\n  value: v\n  host: h.example\nBAR: *x\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	err := RemoveSecretSpec(dir, "FOO")

	require.Error(t, err)
	require.Error(t, errors.Unwrap(err), "the removal error must be preserved with %w")
	assert.Equal(t, content, readFile(t, path))
}

// TestWriteSecretSpecSurfacesAReadFailure pins that a path the reader cannot
// read (a directory named like the secret file) is refused, never treated as an
// absent file to create over.
func TestWriteSecretSpecSurfacesAReadFailure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "env.secret.yaml"), 0o700))

	err := WriteSecretSpec(dir, "FOO", SecretSpec{Value: "v", Host: "h.example"})

	require.Error(t, err)
}

// TestWriteSecretSpecSurfacesAWriteFailure pins that a failed atomic write is
// returned rather than swallowed, so a caller knows the secret did not land.
func TestWriteSecretSpecSurfacesAWriteFailure(t *testing.T) {
	dir := t.TempDir()
	orig := writeSecretFile
	writeSecretFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	t.Cleanup(func() { writeSecretFile = orig })

	err := WriteSecretSpec(dir, "FOO", SecretSpec{Value: "v", Host: "h.example"})

	require.ErrorContains(t, err, "disk full")
}

// TestValidateSecretFileReportsWhatCanBeEdited pins the validator's contract:
// an absent or parseable file can be edited, an unparseable one cannot.
func TestValidateSecretFileReportsWhatCanBeEdited(t *testing.T) {
	absent := t.TempDir()
	require.NoError(t, ValidateSecretFile(absent))

	valid := t.TempDir()
	require.NoError(t, WriteSecretSpec(valid, "FOO", SecretSpec{Value: "v", Host: "h.example"}))
	require.NoError(t, ValidateSecretFile(valid))

	unparseable := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(unparseable, "env.secret.yaml"), []byte("{ not yaml\n"), 0o600))
	require.Error(t, ValidateSecretFile(unparseable))
}

// TestWriteSecretSpecEscapesKey pins that a key whose characters JSON-pointer
// escaping would otherwise mangle is written and re-read under its own spelling:
// the write escapes the key for oneyaml's pointer and compares the resolved key
// node against the literal key.
func TestWriteSecretSpecEscapesKey(t *testing.T) {
	dir := t.TempDir()
	key := "WEIRD~1KEY/NAME"

	require.NoError(t, WriteSecretSpec(dir, key, SecretSpec{Value: "v", Host: "h.example"}))
	require.NoError(t, WriteSecretSpec(dir, key, SecretSpec{Value: "v2", Host: "h.example"}))

	specs := readBackSpecs(t, dir)
	assert.Equal(t, map[string]SecretSpec{key: {Value: "v2", Host: "h.example"}}, specs)
}

// TestWriteSecretSpecWritesThroughSymlink pins that a symlinked env.secret.yaml
// is edited at its target: a rename-based atomic write over the link would
// replace the link with a regular file.
func TestWriteSecretSpecWritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secrets.yaml")
	require.NoError(t, os.WriteFile(target, []byte("FOO:\n  value: old\n  host: a.example\n"), 0o600))
	link := filepath.Join(dir, "env.secret.yaml")
	require.NoError(t, os.Symlink(target, link))
	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "new", Host: "b.example"}))

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the link must survive the write")
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Contains(t, string(data), "new")
}

// TestWriteSecretSpecRefusesUnapprovedSymlinkedProjectFile pins that a symlinked
// project secret file is verified at its link path, where the guard keys the
// approval: a link whose resolved bytes differ from the approved bytes is
// refused. Reading the resolved target instead would let a link pointing outside
// the guarded project dir fall through to an unverified read, so a planted
// symlink could bypass the trust gate and rewrite its target.
func TestWriteSecretSpecRefusesUnapprovedSymlinkedProjectFile(t *testing.T) {
	projectDir := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "secrets.yaml")
	content := []byte("FOO:\n  value: planted\n  host: a.example\n")
	require.NoError(t, os.WriteFile(target, content, 0o600))
	link := filepath.Join(projectDir, "env.secret.yaml")
	require.NoError(t, os.Symlink(target, link))
	t.Cleanup(projectguard.Install(
		[]string{projectDir},
		map[string]projectguard.Approval{link: projectguard.ApprovalOf([]byte("different bytes"))},
	))
	before := readFile(t, target)

	err := WriteSecretSpec(projectDir, "FOO", SecretSpec{Value: "new", Host: "b.example"})

	require.ErrorIs(t, err, projectguard.ErrNotApproved)
	assert.Equal(t, before, readFile(t, target), "the refused write must not rewrite the symlink's target")
}

// TestWriteSecretSpecVerifiesApprovedIntraProjectSymlink pins the other side of
// the same key: a symlink to another file in the same guarded project dir whose
// resolved bytes are the approved bytes is verified, not spuriously refused.
// Resolving the target for the read would refuse it, since the target itself is
// under the guarded root but not an approved input.
func TestWriteSecretSpecVerifiesApprovedIntraProjectSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secrets.yaml")
	content := []byte("FOO:\n  value: old\n  host: a.example\n")
	require.NoError(t, os.WriteFile(target, content, 0o600))
	link := filepath.Join(dir, "env.secret.yaml")
	require.NoError(t, os.Symlink(target, link))
	t.Cleanup(projectguard.Install(
		[]string{dir},
		map[string]projectguard.Approval{link: projectguard.ApprovalOf(content)},
	))

	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "new", Host: "b.example"}))

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the link must survive the write")
	assert.Contains(t, readFile(t, target), "new")

	approved, err := projectguard.ReadFile(link, os.ReadFile)
	require.NoError(t, err, "the launcher's own write must become the approved bytes")
	assert.Contains(t, string(approved), "new")
}

// TestWriteSecretSpecRefusesUnapprovedProjectFile pins the trust contract: a
// project secret file the approval does not cover (or whose bytes changed since
// it was approved) is refused, so a learn-mode write can never launder a
// changed project file.
func TestWriteSecretSpecRefusesUnapprovedProjectFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	require.NoError(t, os.WriteFile(path, []byte("FOO:\n  value: old\n  host: a.example\n"), 0o600))
	t.Cleanup(projectguard.Install(
		[]string{dir},
		map[string]projectguard.Approval{path: projectguard.ApprovalOf([]byte("different bytes"))},
	))
	before := readFile(t, path)

	err := WriteSecretSpec(dir, "FOO", SecretSpec{Value: "new", Host: "b.example"})

	require.ErrorIs(t, err, projectguard.ErrNotApproved)
	assert.Equal(t, before, readFile(t, path))
}

// TestWriteSecretSpecRefusesDeletedApprovedProjectFile pins the deleted half of
// the trust contract: projectguard refuses an approved file that has since been
// deleted, and that refusal wraps fs.ErrNotExist alongside ErrNotApproved. The
// writer must honour the refusal rather than read it as an absent file, or it
// would recreate and re-bless a file the approval says must exist.
func TestWriteSecretSpecRefusesDeletedApprovedProjectFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	content := []byte("FOO:\n  value: old\n  host: a.example\n")
	require.NoError(t, os.WriteFile(path, content, 0o600))
	t.Cleanup(projectguard.Install(
		[]string{dir},
		map[string]projectguard.Approval{path: projectguard.ApprovalOf(content)},
	))
	require.NoError(t, os.Remove(path))

	err := WriteSecretSpec(dir, "FOO", SecretSpec{Value: "new", Host: "b.example"})

	require.ErrorIs(t, err, projectguard.ErrNotApproved)
	_, statErr := os.Stat(path)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "the refused write must not recreate the file")
}

// TestRemoveSecretSpecRefusesDeletedApprovedProjectFile covers the same deleted
// branch through the remover, so both entry points fail closed.
func TestRemoveSecretSpecRefusesDeletedApprovedProjectFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	content := []byte("FOO:\n  value: old\n  host: a.example\n")
	require.NoError(t, os.WriteFile(path, content, 0o600))
	t.Cleanup(projectguard.Install(
		[]string{dir},
		map[string]projectguard.Approval{path: projectguard.ApprovalOf(content)},
	))
	require.NoError(t, os.Remove(path))

	err := RemoveSecretSpec(dir, "FOO")

	require.ErrorIs(t, err, projectguard.ErrNotApproved)
}

func TestRemoveSecretSpecDeletesOneKeyAndLeavesOthers(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "first", Host: "a.example"}))
	require.NoError(t, WriteSecretSpec(dir, "BAR", SecretSpec{Value: "second", Host: "b.example"}))

	require.NoError(t, RemoveSecretSpec(dir, "FOO"))

	assert.Equal(t, map[string]SecretSpec{"BAR": {Value: "second", Host: "b.example"}}, readBackSpecs(t, dir))
}

// TestRemoveSecretSpecPreservesOtherEntriesComments pins that removing one key
// leaves the rest of the document intact. The comment above the removed key
// belongs to that entry (oneyaml drops it with the entry); a comment attached to
// a surviving entry is kept.
func TestRemoveSecretSpecPreservesOtherEntriesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	require.NoError(t, os.WriteFile(path, []byte(
		"FOO:\n  value: v\n  host: a.example\n"+
			"# keep this comment\n"+
			"BAR:\n  value: w\n  host: b.example\n",
	), 0o600))

	require.NoError(t, RemoveSecretSpec(dir, "FOO"))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "# keep this comment")
	assert.Equal(t, map[string]SecretSpec{"BAR": {Value: "w", Host: "b.example"}}, readBackSpecs(t, dir))
}

func TestRemoveSecretSpecAbsentKeyIsNoOp(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "first", Host: "a.example"}))
	before := readFile(t, filepath.Join(dir, "env.secret.yaml"))

	require.NoError(t, RemoveSecretSpec(dir, "MISSING"))

	assert.Equal(t, before, readFile(t, filepath.Join(dir, "env.secret.yaml")))
}

func TestRemoveSecretSpecAbsentFileIsNoOp(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, RemoveSecretSpec(dir, "FOO"))

	_, err := os.Stat(filepath.Join(dir, "env.secret.yaml"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// TestRemoveSecretSpecLastKeyRemovesOnlyTheKey pins that deleting the only
// entry leaves an empty (but parseable) document rather than an error: the
// removal is a per-key edit, not a file delete.
func TestRemoveSecretSpecLastKeyRemovesOnlyTheKey(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "only", Host: "a.example"}))

	require.NoError(t, RemoveSecretSpec(dir, "FOO"))

	assert.Empty(t, readBackSpecs(t, dir))
}

// TestRemoveSecretSpecRejectsUnparseableFile pins that a removal never rewrites
// a file the loader cannot parse, so it cannot silently discard the file's
// content.
func TestRemoveSecretSpecRejectsUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	malformed := "{ not valid yaml\n"
	require.NoError(t, os.WriteFile(path, []byte(malformed), 0o600))

	err := RemoveSecretSpec(dir, "FOO")

	require.Error(t, err)
	assert.Equal(t, malformed, readFile(t, path))
}

func TestRemoveSecretSpecRoundTripsWithWrite(t *testing.T) {
	dir := t.TempDir()
	spec := SecretSpec{Value: "v", Hosts: []string{"a.example"}, HeaderFields: []string{"x-api-key"}}
	require.NoError(t, WriteSecretSpec(dir, "K", spec))
	assert.Equal(t, spec, readBackSpecs(t, dir)["K"])

	require.NoError(t, RemoveSecretSpec(dir, "K"))

	assert.Empty(t, readBackSpecs(t, dir))
}

// TestParseSecretDocumentReportsExistence pins the reader's three outcomes: an
// absent file is an empty document that did not exist, a present file exists,
// and an unreadable or unparseable one is an error reporting no existence.
func TestParseSecretDocumentReportsExistence(t *testing.T) {
	absent := t.TempDir()
	_, existed, err := parseSecretDocument(absent)
	require.NoError(t, err)
	assert.False(t, existed, "an absent file did not exist")

	present := t.TempDir()
	require.NoError(t, WriteSecretSpec(present, "FOO", SecretSpec{Value: "v", Host: "h.example"}))
	_, existed, err = parseSecretDocument(present)
	require.NoError(t, err)
	assert.True(t, existed, "a present file exists")

	malformed := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(malformed, "env.secret.yaml"), []byte("{ not yaml\n"), 0o600))
	_, existed, err = parseSecretDocument(malformed)
	require.Error(t, err)
	assert.False(t, existed, "an unparseable file reports no existence")

	unreadable := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(unreadable, "env.secret.yaml"), 0o700))
	_, existed, err = parseSecretDocument(unreadable)
	require.Error(t, err)
	assert.False(t, existed, "an unreadable file reports no existence")
}

// TestParseSecretDocumentRefusesAnUnapprovedFile pins that a trust refusal is
// returned as itself and does not report the file as an existing one.
func TestParseSecretDocumentRefusesAnUnapprovedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	t.Cleanup(projectguard.Install(
		[]string{dir},
		map[string]projectguard.Approval{
			path: projectguard.ApprovalOf([]byte("FOO:\n  value: v\n  host: h.example\n")),
		},
	))

	_, existed, err := parseSecretDocument(dir)

	require.ErrorIs(t, err, projectguard.ErrNotApproved)
	assert.False(t, existed, "a refused read reports no existence")
}

// TestReadSecretFileAtSurfacesAReadError pins that a genuine read failure (a
// directory where the file should be) is returned as an error that preserves
// its cause.
func TestReadSecretFileAtSurfacesAReadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	require.NoError(t, os.MkdirAll(path, 0o700))

	_, err := readSecretFileAt(path)

	require.Error(t, err)
	require.Error(t, errors.Unwrap(err), "the read error must be preserved with %w")
}

// TestReadSecretFileAtReportsTrustErrorsUnwrapped pins the reader's contract
// that a guard refusal and an absent file are returned unwrapped and
// unmodified, so the caller distinguishes them by errors.Is rather than an
// extra wrapper.
func TestReadSecretFileAtReportsTrustErrorsUnwrapped(t *testing.T) {
	absent := t.TempDir()
	_, err := readSecretFileAt(filepath.Join(absent, "env.secret.yaml"))
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.False(t, strings.HasPrefix(err.Error(), "read "),
		"an absent file must be returned unwrapped, not re-labelled")

	guarded := t.TempDir()
	path := filepath.Join(guarded, "env.secret.yaml")
	t.Cleanup(projectguard.Install([]string{guarded}, map[string]projectguard.Approval{
		path: projectguard.ApprovalOf([]byte("FOO:\n  value: v\n  host: h.example\n")),
	}))
	_, err = readSecretFileAt(path)
	require.ErrorIs(t, err, projectguard.ErrNotApproved)
	assert.False(t, strings.HasPrefix(err.Error(), "read "),
		"a trust refusal must be returned unwrapped, not re-labelled")
}

// TestEntryValuePointerReportsAnAmbiguousKey pins that a key occurring twice has
// no pointer and is reported as not found, so the caller refuses rather than
// editing an arbitrary entry.
func TestEntryValuePointerReportsAnAmbiguousKey(t *testing.T) {
	document, err := oneyaml.Parse(
		[]byte("FOO:\n  value: v\n  host: a.example\nFOO:\n  value: w\n  host: b.example\n"),
		oneyaml.Untrusted(),
	)
	require.NoError(t, err)

	pointer, found, err := entryValuePointer(document, "FOO")

	require.Error(t, err)
	assert.False(t, found)
	assert.Empty(t, pointer)
}

// TestRemoveSecretSpecRemovesAKeyAfterAnother pins that the lookup scans past a
// non-matching entry rather than stopping at it: removing a key that is not the
// first entry still removes it.
func TestRemoveSecretSpecRemovesAKeyAfterAnother(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	require.NoError(t, os.WriteFile(path, []byte(
		"BAR:\n  value: b\n  host: b.example\n"+
			"FOO:\n  value: v\n  host: a.example\n",
	), 0o600))

	require.NoError(t, RemoveSecretSpec(dir, "FOO"))

	got := readFile(t, path)
	assert.NotContains(t, got, "FOO", "the addressed key must be removed")
	assert.Contains(t, got, "BAR", "the other key must survive")
}

// TestWriteSecretSpecKeepsAnExistingFileMode pins that an edit keeps the secret
// file's permissions, so a shared secret file does not turn private.
func TestWriteSecretSpecKeepsAnExistingFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.secret.yaml")
	require.NoError(t, os.WriteFile(path, []byte("FOO:\n  value: old\n  host: a.example\n"), 0o644))
	require.NoError(t, os.Chmod(path, 0o644))

	require.NoError(t, WriteSecretSpec(dir, "FOO", SecretSpec{Value: "new", Host: "a.example"}))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
