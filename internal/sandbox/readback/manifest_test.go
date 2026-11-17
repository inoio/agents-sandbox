package readback

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeFile writes content to rel under home, creating parent directories.
func writeFile(t *testing.T, home, rel, content string) {
	t.Helper()
	full := filepath.Join(home, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

// writeFileSized writes a file of exactly size bytes to rel under home.
func writeFileSized(t *testing.T, home, rel string, size int) {
	t.Helper()
	writeFile(t, home, rel, string(make([]byte, size)))
}

// entryFor returns the manifest entry keyed key, and whether it exists.
func entryFor(m Manifest, key string) (ManifestEntry, bool) {
	for _, entry := range m.Entries {
		if entry.Key == key {
			return entry, true
		}
	}
	return ManifestEntry{}, false
}

func TestManifestChangedDetectsContentAndPresence(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc", "a")
	base, _ := TakeManifest(home, "opencode")
	writeFile(t, home, ".config/opencode/opencode.jsonc", "b")
	next, _ := TakeManifest(home, "opencode")
	if got := next.Changed(base); len(got) != 1 || got[0] != ".config/opencode/opencode.jsonc" {
		t.Fatalf("got %v", got)
	}
}

func TestManifestOpaqueOverCap(t *testing.T) {
	home := t.TempDir()
	writeFileSized(t, home, ".config/opencode/opencode.jsonc", maxHashedBytes+1)
	m, _ := TakeManifest(home, "opencode")
	found := false
	for _, e := range m.Entries {
		if e.Key == ".config/opencode/opencode.jsonc" {
			found = true
			if e.Hash != "" {
				t.Fatal("over-cap file must be recorded opaque")
			}
		}
	}
	if !found {
		t.Fatal("an over-cap declared file must still appear as an opaque entry")
	}
}

func TestTakeManifestRecordsDeclaredFiles(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc", "a")
	m, err := TakeManifest(home, "opencode")

	require.NoError(t, err)
	entry, ok := entryFor(m, ".config/opencode/opencode.jsonc")
	require.True(t, ok, "declared file must appear in the manifest")
	require.EqualValues(t, 1, entry.Size)
	require.NotEmpty(t, entry.Hash)
	require.False(t, entry.ModTime.IsZero(), "a file entry carries its mtime")
}

func TestTakeManifestHashesDistinctContentDistinctly(t *testing.T) {
	sameHome, otherHome := t.TempDir(), t.TempDir()
	writeFile(t, sameHome, ".config/opencode/opencode.jsonc", "a")
	writeFile(t, otherHome, ".config/opencode/opencode.jsonc", "a")
	changedHome := t.TempDir()
	writeFile(t, changedHome, ".config/opencode/opencode.jsonc", "b")

	sameEntry, ok := entryFor(mustOpencodeManifest(t, sameHome), ".config/opencode/opencode.jsonc")
	require.True(t, ok)
	otherEntry, ok := entryFor(mustOpencodeManifest(t, otherHome), ".config/opencode/opencode.jsonc")
	require.True(t, ok)
	changedEntry, ok := entryFor(mustOpencodeManifest(t, changedHome), ".config/opencode/opencode.jsonc")
	require.True(t, ok)

	require.Equal(t, sameEntry.Hash, otherEntry.Hash, "equal content hashes equally")
	require.NotEqual(t, sameEntry.Hash, changedEntry.Hash, "distinct content hashes distinctly")
}

func TestTakeManifestLeavesUndeclaredFilesOut(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc", "a")
	writeFile(t, home, "notes.txt", "irrelevant")

	m := mustOpencodeManifest(t, home)

	_, ok := entryFor(m, "notes.txt")
	require.False(t, ok, "an undeclared file must not be walked")
}

func TestTakeManifestExactFileSelectsNoSibling(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".claude/settings.json", "a")
	writeFile(t, home, ".claude/settings.local.json", "b")

	m := mustManifest(t, home, "claude-code")

	require.Equal(t, []string{".claude/settings.json"}, manifestKeys(m),
		"an exact declared file selects itself, not a similarly named sibling")
}

func TestTakeManifestGlobSelectsDirectChildrenOnly(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc", "a")
	writeFile(t, home, ".config/opencode/other.jsonc", "b")
	writeFile(t, home, ".config/opencode/nested/deep.jsonc", "c")

	m := mustOpencodeManifest(t, home)

	require.Equal(t, []string{".config/opencode/opencode.jsonc", ".config/opencode/other.jsonc"}, manifestKeys(m),
		"a dir/* glob selects the files directly under it, not nested ones")
}

func TestTakeManifestUnknownAgentIsEmpty(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc", "a")

	m, err := TakeManifest(home, "no-such-agent")

	require.NoError(t, err)
	require.Empty(t, m.Entries)
}

func TestTakeManifestMissingDeclaredPathIsNotAnError(t *testing.T) {
	m, err := TakeManifest(t.TempDir(), "opencode")

	require.NoError(t, err)
	require.Empty(t, m.Entries)
}

func TestManifestChangedIgnoresUnchangedManifest(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc", "a")

	base := mustOpencodeManifest(t, home)
	next := mustOpencodeManifest(t, home)

	require.Empty(t, next.Changed(base))
}

func TestManifestChangedReportsAddedAndRemovedKeys(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc", "a")
	base := mustOpencodeManifest(t, home)

	writeFile(t, home, ".config/opencode/other.jsonc", "b")
	next := mustOpencodeManifest(t, home)

	require.Equal(t, []string{".config/opencode/other.jsonc"}, next.Changed(base))
	require.Equal(t, []string{".config/opencode/other.jsonc"}, base.Changed(next))
}

func TestManifestChangedDetectsOpaqueSizeChange(t *testing.T) {
	home := t.TempDir()
	writeFileSized(t, home, ".config/opencode/opencode.jsonc", maxHashedBytes+1)
	base := mustOpencodeManifest(t, home)

	writeFileSized(t, home, ".config/opencode/opencode.jsonc", maxHashedBytes+2)
	next := mustOpencodeManifest(t, home)

	require.Equal(t, []string{".config/opencode/opencode.jsonc"}, next.Changed(base))
}

func TestManifestEntriesAreSortedByKey(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/z.jsonc", "z")
	writeFile(t, home, ".config/opencode/a.jsonc", "a")

	m := mustOpencodeManifest(t, home)

	require.Equal(t, []string{".config/opencode/a.jsonc", ".config/opencode/z.jsonc"}, manifestKeys(m))
}

func manifestKeys(m Manifest) []string {
	keys := make([]string, 0, len(m.Entries))
	for _, entry := range m.Entries {
		keys = append(keys, entry.Key)
	}
	return keys
}

func mustOpencodeManifest(t *testing.T, home string) Manifest {
	t.Helper()
	m, err := TakeManifest(home, "opencode")
	require.NoError(t, err)
	return m
}

// mustManifest is mustOpencodeManifest for any agent name.
func mustManifest(t *testing.T, home, agentName string) Manifest {
	t.Helper()
	m, err := TakeManifest(home, agentName)
	require.NoError(t, err)
	return m
}
