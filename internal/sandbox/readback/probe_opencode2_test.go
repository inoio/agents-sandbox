package readback

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// opencode2DBPath is where opencode2 keeps its credential rows, relative to the
// sandbox home.
const opencode2DBPath = ".local/share/opencode/opencode.db"

// opencode2ProbeKey is the manifest key of the opencode2 credential probe.
const opencode2ProbeKey = "opencode2:opencode2-sqlite"

// writeOpenCode2DB creates a SQLite opencode.db under home with the given
// credential rows, using the real column names opencode2's store carries. A
// catalog table stands in for the churny model catalog the probe must ignore.
func writeOpenCode2DB(t *testing.T, home string, credentials ...[2]string) {
	t.Helper()
	full := filepath.Join(home, opencode2DBPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	db, err := sql.Open("sqlite", full)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	_, err = db.Exec(`
		CREATE TABLE credential (
			integration_id TEXT NOT NULL,
			label TEXT NOT NULL,
			active INTEGER NOT NULL,
			value TEXT NOT NULL
		);
		CREATE TABLE catalog (id TEXT NOT NULL, payload TEXT NOT NULL);
	`)
	require.NoError(t, err)
	for _, credential := range credentials {
		_, err = db.Exec(
			`INSERT INTO credential (integration_id, label, active, value) VALUES (?, ?, 1, ?)`,
			credential[0], credential[0], credential[1],
		)
		require.NoError(t, err)
	}
}

// TestReadOpenCode2Probe extracts opencode2's static API key from its SQLite
// credential row, and asserts the value stays out of the manifest.
func TestReadOpenCode2Probe(t *testing.T) {
	home := t.TempDir()
	writeOpenCode2DB(t, home, [2]string{"openai", `{"type":"key","key":"sk-test"}`})

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, CredentialStatic, rb.Static[0].Kind)
	require.Equal(t, "openai", rb.Static[0].Provider)
	require.Equal(t, "sk-test", rb.Static[0].Value)
	require.Equal(t, opencode2ProbeKey, rb.Static[0].Source)

	manifest, err := TakeManifest(home, "opencode2")
	require.NoError(t, err)
	for _, entry := range manifest.Entries {
		require.NotContains(t, entry.Key, "sk-test", "a manifest entry is never a value")
		require.NotContains(t, entry.Hash, "sk-test", "a manifest entry is never a value")
	}
}

// TestReadOpenCode2ProbeOAuthIsLogin: an oauth credential is a login, whose
// value is never read out.
func TestReadOpenCode2ProbeOAuthIsLogin(t *testing.T) {
	home := t.TempDir()
	writeOpenCode2DB(t, home,
		[2]string{"github-copilot", `{"type":"oauth","refresh":"gho-secret","access":"gho-secret"}`},
	)

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static)
	require.Len(t, rb.Login, 1)
	require.Equal(t, CredentialLogin, rb.Login[0].Kind)
	require.Equal(t, "github-copilot", rb.Login[0].Provider)
	require.Equal(t, opencode2ProbeKey, rb.Login[0].Source)
	require.Empty(t, rb.Login[0].Value, "a login value is never read")
}

// TestTakeManifestRecordsOneProbePerCredentialProbe: the probe contributes one
// entry keyed "<agent>:<probe>", with no size or mtime, and a digest that is not
// the whole database's.
func TestTakeManifestRecordsOneProbePerCredentialProbe(t *testing.T) {
	home := t.TempDir()
	writeOpenCode2DB(t, home, [2]string{"openai", `{"type":"key","key":"sk-test"}`})

	manifest, err := TakeManifest(home, "opencode2")

	require.NoError(t, err)
	entry, ok := entryFor(manifest, opencode2ProbeKey)
	require.True(t, ok, "the probe contributes exactly one entry")
	require.Zero(t, entry.Size, "a probe entry carries no size")
	require.True(t, entry.ModTime.IsZero(), "a probe entry carries no mtime")
	require.NotEmpty(t, entry.Hash)
	require.Len(t, entry.Hash, 64, "the probe hash is a sha256 hex digest")
	require.Empty(t, manifestWithout(manifest, opencode2ProbeKey), "opencode2 declares no file paths")
}

// TestTakeManifestProbeIgnoresOtherTables: a change elsewhere in the database,
// such as the churny model catalog, does not fire the probe.
func TestTakeManifestProbeIgnoresOtherTables(t *testing.T) {
	home := t.TempDir()
	writeOpenCode2DB(t, home, [2]string{"openai", `{"type":"key","key":"sk-test"}`})
	base, err := TakeManifest(home, "opencode2")
	require.NoError(t, err)

	db, err := sql.Open("sqlite", filepath.Join(home, opencode2DBPath))
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO catalog (id, payload) VALUES ('gpt-5', '{}')`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	next, err := TakeManifest(home, "opencode2")

	require.NoError(t, err)
	require.Empty(t, next.Changed(base), "a catalog write must not fire the credential probe")
}

// TestTakeManifestProbeFiresOnCredentialChange: the probe's digest moves when a
// credential row changes, which is the whole point of tracking it.
func TestTakeManifestProbeFiresOnCredentialChange(t *testing.T) {
	home := t.TempDir()
	writeOpenCode2DB(t, home, [2]string{"openai", `{"type":"key","key":"sk-test"}`})
	base, err := TakeManifest(home, "opencode2")
	require.NoError(t, err)

	db, err := sql.Open("sqlite", filepath.Join(home, opencode2DBPath))
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE credential SET value = ? WHERE integration_id = 'openai'`,
		`{"type":"key","key":"sk-rotated"}`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	next, err := TakeManifest(home, "opencode2")

	require.NoError(t, err)
	require.Equal(t, []string{opencode2ProbeKey}, next.Changed(base))
}

// TestReadOpenCode2ProbeOverCapValueIsUnreadable: the sandboxed agent controls
// the credential table, so a row whose value exceeds the probe's bound is not
// materialized. The trip is surfaced through Unreadable, not hidden as an
// absent credential, and the probe still emits exactly one manifest entry.
func TestReadOpenCode2ProbeOverCapValueIsUnreadable(t *testing.T) {
	home := t.TempDir()
	oversized := strings.Repeat("x", maxProbeValueBytes+1)
	writeOpenCode2DB(t, home, [2]string{"openai", `{"type":"key","key":"` + oversized + `"}`})

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static, "an over-cap value is never materialized")
	require.Empty(t, rb.Login)
	require.Equal(t, []string{opencode2ProbeKey}, rb.Unreadable,
		"an over-cap credential row must be surfaced, not silently dropped")

	manifest, err := TakeManifest(home, "opencode2")
	require.NoError(t, err)
	entry, ok := entryFor(manifest, opencode2ProbeKey)
	require.True(t, ok, "the probe still emits exactly one manifest entry")
	require.Equal(t, unreadableProbeHash, entry.Hash, "a tripped bound carries the sentinel hash")
}

// TestReadOpenCode2ProbeTotalProjectionCap: a store packed with many
// near-cap rows trips the total projected-bytes budget and is reported
// unreadable, so host memory stays bounded by row count as well.
func TestReadOpenCode2ProbeTotalProjectionCap(t *testing.T) {
	home := t.TempDir()
	value := strings.Repeat("x", maxProbeValueBytes)
	writeOpenCode2DB(t, home,
		[2]string{"a", value}, [2]string{"b", value}, [2]string{"c", value},
		[2]string{"d", value}, [2]string{"e", value},
	)

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static)
	require.Equal(t, []string{opencode2ProbeKey}, rb.Unreadable,
		"a projection over the total budget is undetermined, not empty")
}

// TestReadOpenCode2ProbeUnreadableDatabase: a database the probe cannot read
// still contributes exactly one entry, carrying the sentinel hash, so a
// transient failure cannot be mistaken for the probe's removal. Read stays
// best-effort: no credentials, no error.
func TestReadOpenCode2ProbeUnreadableDatabase(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, opencode2DBPath, "not a sqlite database at all")

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static)
	require.Empty(t, rb.Login)
	require.Equal(t, []string{opencode2ProbeKey}, rb.Unreadable,
		"an unreadable store must be named, not silently reported as no credentials")

	manifest, err := TakeManifest(home, "opencode2")
	require.NoError(t, err)
	require.Equal(t, []string{opencode2ProbeKey}, manifestKeys(manifest))
	entry, ok := entryFor(manifest, opencode2ProbeKey)
	require.True(t, ok)
	require.Equal(t, unreadableProbeHash, entry.Hash, "a failing probe carries the sentinel hash")
	require.NotEqual(t, hashCredentialRows(nil), entry.Hash, "the sentinel is not the empty-set digest")
}

// TestReadOpenCode2ProbeSkipsInactiveCredentials: only an active credential row
// is a fact. An inactive row is skipped, while the store stays readable.
func TestReadOpenCode2ProbeSkipsInactiveCredentials(t *testing.T) {
	home := t.TempDir()
	writeOpenCode2DB(t, home, [2]string{"openai", `{"type":"key","key":"sk-test"}`})
	db, err := sql.Open("sqlite", filepath.Join(home, opencode2DBPath))
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE credential SET active = 0 WHERE integration_id = 'openai'`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static, "an inactive credential is not a fact")
	require.Empty(t, rb.Unreadable, "the store was readable")
}

// TestReadOpenCode2ProbeToleratesNullActive: a NULL active column does not abort
// the probe; the row is simply treated as inactive.
func TestReadOpenCode2ProbeToleratesNullActive(t *testing.T) {
	home := t.TempDir()
	full := filepath.Join(home, opencode2DBPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	db, err := sql.Open("sqlite", full)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE credential (
		integration_id TEXT NOT NULL, label TEXT NOT NULL, active INTEGER, value TEXT NOT NULL)`)
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO credential (integration_id, label, active, value) VALUES ('null-active', 'X', NULL, ?)`,
		`{"type":"key","key":"ignored"}`,
	)
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO credential (integration_id, label, active, value) VALUES ('openai', 'O', 1, ?)`,
		`{"type":"key","key":"sk"}`,
	)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Unreadable, "a NULL active row must not abort the probe")
	require.Len(t, rb.Static, 1)
	require.Equal(t, "openai", rb.Static[0].Provider)
}

// TestTakeManifestUnreadableProbeIsStable: two failed reads produce the same
// digest, so an unreadable database is not reported as a change on every run;
// going from unreadable to readable is.
func TestTakeManifestUnreadableProbeIsStable(t *testing.T) {
	unreadableHome := t.TempDir()
	writeFile(t, unreadableHome, opencode2DBPath, "not a sqlite database at all")
	first, err := TakeManifest(unreadableHome, "opencode2")
	require.NoError(t, err)
	second, err := TakeManifest(unreadableHome, "opencode2")
	require.NoError(t, err)
	require.Empty(t, second.Changed(first), "a persistent failure is not a change")

	readableHome := t.TempDir()
	writeOpenCode2DB(t, readableHome, [2]string{"openai", `{"type":"key","key":"sk-test"}`})
	readable, err := TakeManifest(readableHome, "opencode2")
	require.NoError(t, err)
	require.Equal(t, []string{opencode2ProbeKey}, readable.Changed(first),
		"becoming readable is a change")
}

// TestReadOpenCode2ProbeMissingDatabaseIsNotAnError: a home with no opencode2
// database yields no credentials and never an error. The probe still
// contributes its entry, digesting the empty row set.
func TestReadOpenCode2ProbeMissingDatabaseIsNotAnError(t *testing.T) {
	home := t.TempDir()

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static)
	require.Empty(t, rb.Login)
	manifest, err := TakeManifest(home, "opencode2")
	require.NoError(t, err)
	entry, ok := entryFor(manifest, opencode2ProbeKey)
	require.True(t, ok, "a declared probe always contributes its digest")
	require.Equal(t, hashCredentialRows(nil), entry.Hash, "a missing database digests the empty row set")
}

// TestManifestChangedComparesProbeOnHashAlone: the frozen rule for a probe
// entry, tested directly: only the hash matters, never size or mtime.
func TestManifestChangedComparesProbeOnHashAlone(t *testing.T) {
	probe := func(hash string) Manifest {
		return Manifest{Entries: []ManifestEntry{{Key: opencode2ProbeKey, Hash: hash}}}
	}

	require.Equal(t, []string{opencode2ProbeKey}, probe("aaaa").Changed(probe("bbbb")))
	require.Empty(t, probe("aaaa").Changed(probe("aaaa")))
}

// TestOpenCode2ProbeHashIgnoresCredentialsNotDeclared is a guard that the probe
// reads nothing but the credential table: another database file in the same
// directory cannot change its digest.
func TestOpenCode2ProbeHashIgnoresCredentialsNotDeclared(t *testing.T) {
	home := t.TempDir()
	writeOpenCode2DB(t, home, [2]string{"openai", `{"type":"key","key":"sk-test"}`})
	base, err := TakeManifest(home, "opencode2")
	require.NoError(t, err)
	writeFileSized(t, home, strings.TrimSuffix(opencode2DBPath, ".db")+"-wal", 4096)

	next, err := TakeManifest(home, "opencode2")

	require.NoError(t, err)
	require.Empty(t, next.Changed(base), "the probe hashes rows, not database files")
}

// manifestWithout returns the manifest's keys other than exempt.
func manifestWithout(manifest Manifest, exempt string) []string {
	keys := make([]string, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		if entry.Key != exempt {
			keys = append(keys, entry.Key)
		}
	}
	return keys
}
