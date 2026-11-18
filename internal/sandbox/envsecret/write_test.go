package envsecret

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	require.Error(t, err)
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

	require.Error(t, err)
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

	require.Error(t, err)
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
