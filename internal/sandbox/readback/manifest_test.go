package readback

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/filewalk"
	"github.com/inoio/agents-sandbox/internal/projectguard"
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

// TestManifestHashCapBoundary pins the hash cap's literal bounds: a file exactly
// at 256 KiB is hashed, one byte more is opaque. The sizes are literal so the
// constant's own integer mutants change the outcome.
func TestManifestHashCapBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		size   int
		hashed bool
	}{
		{name: "exactly at the cap", size: 256 * 1024, hashed: true},
		{name: "one byte over the cap", size: 256*1024 + 1, hashed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeFileSized(t, home, ".config/opencode/opencode.jsonc", tc.size)

			entry, ok := entryFor(mustOpencodeManifest(t, home), ".config/opencode/opencode.jsonc")

			require.True(t, ok)
			if tc.hashed {
				require.NotEmpty(t, entry.Hash, "a file at the cap is hashed")
			} else {
				require.Empty(t, entry.Hash, "a file over the cap stays opaque")
			}
		})
	}
}

// TestTakeManifestPropagatesAWalkRefusal: a declared path the projectguard
// refuses to list aborts the walk and surfaces as an error, never as an empty
// manifest.
func TestTakeManifestPropagatesAWalkRefusal(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config/opencode")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	gone := filepath.Join(dir, "gone.jsonc")
	t.Cleanup(projectguard.Install(
		[]string{dir},
		map[string]projectguard.Approval{gone: projectguard.ApprovalOf([]byte("x"))},
	))

	_, err := TakeManifest(home, "opencode")

	require.ErrorIs(t, err, projectguard.ErrNotApproved)
}

// TestAppendDeclaredPathDedupesOverlappingPaths: a file two declared paths both
// select is recorded once, because the first walk marks it seen.
func TestAppendDeclaredPathDedupesOverlappingPaths(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc", "a")
	var manifest Manifest
	seen := map[string]bool{}

	require.NoError(t, appendDeclaredPath(&manifest, seen, home, ".config/opencode/*"))
	require.NoError(t, appendDeclaredPath(&manifest, seen, home, ".config/opencode/opencode.jsonc"))

	require.Len(t, manifest.Entries, 1, "a file selected by two declared paths is recorded once")
}

// TestDescribeFileFallsBackToWalkRelativeKey: a walked path that cannot be made
// home-relative keeps the walk's own relative name rather than dropping the
// entry.
func TestDescribeFileFallsBackToWalkRelativeKey(t *testing.T) {
	entry := filewalk.Entry{Path: "/absolute/path.txt", Rel: "path.txt", Size: 1}

	got := describeFile("relative-home", entry)

	require.Equal(t, "path.txt", got.Key)
}

// TestChangedReportsAKeyPresentInOneManifestOnly: a key present in the current
// manifest and absent from the baseline is a change, even when its entry is the
// zero value.
func TestChangedReportsAKeyPresentInOneManifestOnly(t *testing.T) {
	next := Manifest{Entries: []ManifestEntry{{Key: "only"}}}

	require.Equal(t, []string{"only"}, next.Changed(Manifest{}))
	require.Equal(t, []string{"only"}, (Manifest{}).Changed(next))
}

// TestChangedSortsItsKeys: Changed returns its keys sorted, however the entries
// are ordered.
func TestChangedSortsItsKeys(t *testing.T) {
	baseline := Manifest{Entries: []ManifestEntry{
		{Key: "c", Hash: "1"}, {Key: "a", Hash: "1"}, {Key: "b", Hash: "1"},
	}}
	next := Manifest{Entries: []ManifestEntry{
		{Key: "c", Hash: "2"}, {Key: "a", Hash: "2"}, {Key: "b", Hash: "2"},
	}}

	require.Equal(t, []string{"a", "b", "c"}, next.Changed(baseline))
}

// TestDiffersComparesFileOnSizeAndMtime: for a file entry, a size or mtime
// change is a difference even when the hash is unchanged.
func TestDiffersComparesFileOnSizeAndMtime(t *testing.T) {
	base := ManifestEntry{Key: "f", Size: 1, ModTime: time.Unix(1, 0), Hash: "h"}

	require.False(t, differs(base, base))
	require.True(t, differs(ManifestEntry{Key: "f", Size: 2, ModTime: base.ModTime, Hash: "h"}, base),
		"a size change differs")
	require.True(t, differs(ManifestEntry{Key: "f", Size: 1, ModTime: time.Unix(2, 0), Hash: "h"}, base),
		"an mtime change differs")
}

// TestDiffersComparesProbeOnHashAlone: a probe entry is compared on its hash
// alone, so a file's size and mtime never make an unchanged probe differ.
func TestDiffersComparesProbeOnHashAlone(t *testing.T) {
	probe := ManifestEntry{Key: opencode2ProbeKey, Hash: "h"}
	previous := ManifestEntry{Key: opencode2ProbeKey, Size: 5, ModTime: time.Unix(3, 0), Hash: "h"}

	require.False(t, differs(probe, previous), "a probe ignores size and mtime")
	require.True(t, differs(ManifestEntry{Key: opencode2ProbeKey, Hash: "other"}, previous))
}

// TestIsProbeClassifiesSizeAndMtime pins the classifier: only an entry with
// both a zero size and a zero mtime is a probe.
func TestIsProbeClassifiesSizeAndMtime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry ManifestEntry
		want  bool
	}{
		{name: "probe", entry: ManifestEntry{Size: 0, ModTime: time.Time{}}, want: true},
		{name: "file with content", entry: ManifestEntry{Size: 5}, want: false},
		{name: "empty file with mtime", entry: ManifestEntry{Size: 0, ModTime: time.Unix(1, 0)}, want: false},
		{name: "one byte file", entry: ManifestEntry{Size: 1}, want: false},
		{name: "negative size", entry: ManifestEntry{Size: -1}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isProbe(tc.entry))
		})
	}
}

// TestSortEntriesOrdersByKey: sortEntries sorts a manifest's entries by key.
func TestSortEntriesOrdersByKey(t *testing.T) {
	entries := []ManifestEntry{{Key: "b"}, {Key: "c"}, {Key: "a"}}

	sortEntries(entries)

	require.Equal(t, []string{"a", "b", "c"}, manifestKeys(Manifest{Entries: entries}))
}

// TestEntryKeyLessIsAStrictWeakOrdering: distinct keys order by value, and a key
// is never less than itself, so equal keys keep their order.
func TestEntryKeyLessIsAStrictWeakOrdering(t *testing.T) {
	a := ManifestEntry{Key: "a"}
	b := ManifestEntry{Key: "b"}

	require.True(t, entryKeyLess(a, b))
	require.False(t, entryKeyLess(b, a))
	require.False(t, entryKeyLess(a, a), "equal keys are not ordered")
}

// readBacklessAgent is a registered stub that does not implement
// agent.ReadBackDescriber, so tests can drive the "no read-back capability"
// branch.
type readBacklessAgent struct{ name string }

func (a readBacklessAgent) Name() string             { return a.name }
func (readBacklessAgent) ConfigDirName() string      { return "stub" }
func (readBacklessAgent) Experimental() bool         { return false }
func (readBacklessAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

// withReadBacklessAgent registers a describer-less stub under name for the test,
// restoring the built-in afterward.
func withReadBacklessAgent(t *testing.T, name string) {
	t.Helper()
	original, ok := agent.Lookup(name)
	require.True(t, ok, "the replaced name must be a registered agent")
	agent.Register(readBacklessAgent{name: name})
	t.Cleanup(func() { agent.Register(original) })
}

// TestTakeManifestAgentWithoutDescriberIsEmpty: an agent without a
// ReadBackDescriber yields an empty manifest, never a panic or an error.
func TestTakeManifestAgentWithoutDescriberIsEmpty(t *testing.T) {
	withReadBacklessAgent(t, "codex")
	home := t.TempDir()
	writeFile(t, home, ".codex/auth.json", "{}")

	manifest, err := TakeManifest(home, "codex")

	require.NoError(t, err)
	require.Empty(t, manifest.Entries)
}

// TestAppendCredentialProbesSkipsASeenSource: a probe source already recorded is
// not probed or appended again.
func TestAppendCredentialProbesSkipsASeenSource(t *testing.T) {
	var manifest Manifest
	seen := map[string]bool{probeSource("opencode2", opencode2ProbeID): true}

	outcomes := appendCredentialProbes(&manifest, seen, t.TempDir(), "opencode2")

	require.Empty(t, outcomes)
	require.Empty(t, manifest.Entries)
}
