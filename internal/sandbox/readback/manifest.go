package readback

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/filewalk"
)

// MaxHashedBytes caps the size of a file whose content is hashed. A larger
// declared file (opencode2's ~13 MB churny database) is recorded opaque:
// size and mtime only. It is also the bound the write-back reads a candidate
// config file under, so a file the read-back recorded opaque is never copied
// back half-read.
const MaxHashedBytes = 256 * 1024

// ManifestEntry is one declared path or credential probe in a baseline
// manifest. It holds metadata and a digest, never a file or secret value.
type ManifestEntry struct {
	// Key is the sandbox-home-relative file path, or "<agent>:<probe>" for a
	// database probe.
	Key string
	// Size is the entry's byte size, 0 for a probe.
	Size int64
	// ModTime is the entry's modification time, zero for a probe.
	ModTime time.Time
	// Hash is the sha256 hex digest of the entry's content. It is empty only
	// for an opaque (over-cap) file entry; for a probe it is the digest of the
	// probed rows.
	Hash string
}

// Manifest is the baseline of an agent's declared config and credential paths,
// taken when a session starts and refreshed when it ends. Change detection
// compares two manifests; the manifest itself never carries a value.
type Manifest struct {
	Entries []ManifestEntry
}

// TakeManifest records a ManifestEntry for every declared path of the named
// agent plus exactly one probe digest per agent credential probe. Files over
// MaxHashedBytes are recorded opaque (size+mtime, no hash); a probe entry
// carries neither size nor mtime, only the digest of the rows it read — or the
// sentinel hash when that read failed. An agent with no declared paths yields
// an empty manifest, never an error; a declared path that does not exist yet is
// simply absent, so a first run has nothing to compare against.
func TakeManifest(home, agentName string) (Manifest, error) {
	collection, err := collectReadBack(home, agentName)
	if err != nil {
		return Manifest{}, err
	}
	return collection.manifest, nil
}

// readBackCollection is one pass over an agent's home: the baseline manifest and
// the outcome of each credential probe. TakeManifest and Read share it, so a
// probe's manifest digest and the credentials Read reports come from the same
// database read and cannot disagree.
type readBackCollection struct {
	manifest Manifest
	probes   []probeOutcome
}

// probeOutcome is one credential probe's result: the source key it is recorded
// under, the credentials it found, and whether its store was unreadable this run.
//
//exhaustruct:ignore
type probeOutcome struct {
	source      string
	credentials []Credential
	unreadable  bool
}

// collectReadBack walks the named agent's declared paths and runs its credential
// probes once, producing the manifest together with the probe outcomes.
func collectReadBack(home, agentName string) (readBackCollection, error) {
	var manifest Manifest
	seen := map[string]bool{}
	for _, declaredPath := range declaredReadBackPaths(agentName) {
		if err := appendDeclaredPath(&manifest, seen, home, declaredPath); err != nil {
			return readBackCollection{}, err
		}
	}
	probes := appendCredentialProbes(&manifest, seen, home, agentName)
	sortEntries(manifest.Entries)
	return readBackCollection{manifest: manifest, probes: probes}, nil
}

// appendCredentialProbes appends exactly one entry per credential probe the
// named agent declares, keyed "<agent>:<probe>" and carrying the digest of the
// rows the probe read — or the sentinel hash when that read failed, so an
// unreadable database never looks like a removed probe. It returns each probe's
// outcome for Read to report.
func appendCredentialProbes(
	manifest *Manifest,
	seen map[string]bool,
	home, agentName string,
) []probeOutcome {
	var outcomes []probeOutcome
	for _, probe := range declaredCredentialProbes(agentName) {
		key := probeSource(agentName, probe)
		if seen[key] {
			continue
		}
		result := runCredentialProbe(agentName, home, probe)
		seen[key] = true
		manifest.Entries = append(manifest.Entries, ManifestEntry{
			Key:     key,
			Size:    0,
			ModTime: time.Time{},
			Hash:    result.RowHash,
		})
		outcomes = append(outcomes, probeOutcome{
			source:      key,
			credentials: result.Credentials,
			unreadable:  result.Unreadable,
		})
	}
	return outcomes
}

// declaredReadBackPaths returns the home-relative paths the named agent's
// read-back declares, or nothing for an agent without a ReadBackDescriber
// (unknown, or not yet verified). An unverified agent yields an empty manifest,
// never an error.
func declaredReadBackPaths(agentName string) []string {
	// An unknown agent yields the nil Agent, for which AsReadBackDescriber is
	// false, so no separate lookup guard is needed.
	a, _ := agent.Lookup(agentName)
	describer, ok := agent.AsReadBackDescriber(a)
	if !ok {
		return nil
	}
	return describer.ReadBackPaths()
}

// declaredCredentialProbes returns the credential probes the named agent's
// read-back declares, or nothing for an agent without a ReadBackDescriber. An
// unverified agent yields no probe, never an error.
func declaredCredentialProbes(agentName string) []string {
	// An unknown agent yields the nil Agent, for which AsReadBackDescriber is
	// false, so no separate lookup guard is needed.
	a, _ := agent.Lookup(agentName)
	describer, ok := agent.AsReadBackDescriber(a)
	if !ok {
		return nil
	}
	return describer.CredentialProbes()
}

// probeSource names a credential probe in a Credential source or a manifest
// key: "<agent>:<probe>".
func probeSource(agentName, probe string) string {
	return agentName + ":" + probe
}

// appendDeclaredPath walks one declared path (a file or a glob) under home and
// appends a manifest entry for every file it selects. A file selected twice by
// overlapping declared paths is recorded once.
func appendDeclaredPath(manifest *Manifest, seen map[string]bool, home, declaredPath string) error {
	root := filepath.Join(home, filepath.Dir(declaredPath))
	base := filepath.Base(declaredPath)
	// A declared pattern is a bare basename or "*", neither of which contains a
	// separator, and filepath.Match never lets "*" cross one. No file reached
	// through a subdirectory can therefore match, so the filter's answer for a
	// directory only decides whether the walk descends into it, never which
	// files are selected; the directory flag is irrelevant.
	take := func(rel string, _ bool) bool {
		matches, _ := filepath.Match(base, rel)
		return matches
	}
	return filewalk.WalkCapped(root, MaxHashedBytes, take, func(entry filewalk.Entry) error {
		manifestEntry := describeFile(home, entry)
		if seen[manifestEntry.Key] {
			return nil
		}
		seen[manifestEntry.Key] = true
		manifest.Entries = append(manifest.Entries, manifestEntry)
		return nil
	})
}

// describeFile records the size and mtime of the file behind entry, keyed by its
// sandbox-home-relative path, and hashes its content when the walk read it. The
// walk leaves an over-cap file's data nil, so that entry stays opaque. A path
// that cannot be made home-relative falls back to its walk-relative name rather
// than dropping the entry: the walk is best-effort.
func describeFile(home string, entry filewalk.Entry) ManifestEntry {
	key, err := filepath.Rel(home, entry.Path)
	if err != nil {
		key = entry.Rel
	}
	manifestEntry := ManifestEntry{Key: key, Size: entry.Size, ModTime: entry.ModTime, Hash: ""}
	if entry.Data == nil {
		return manifestEntry
	}
	digest := sha256.Sum256(entry.Data)
	manifestEntry.Hash = hex.EncodeToString(digest[:])
	return manifestEntry
}

// Changed returns the keys whose size, mtime, or hash differs from prev, plus
// keys present in one manifest and not the other, sorted. A probe entry carries
// neither a size nor an mtime, so it is compared on its hash alone.
func (m Manifest) Changed(prev Manifest) []string {
	previous := indexEntries(prev.Entries)
	current := indexEntries(m.Entries)

	var changed []string
	for key, entry := range current {
		other, seen := previous[key]
		if !seen || differs(entry, other) {
			changed = append(changed, key)
		}
	}
	for key := range previous {
		if _, seen := current[key]; !seen {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}

// indexEntries indexes entries by key.
func indexEntries(entries []ManifestEntry) map[string]ManifestEntry {
	indexed := make(map[string]ManifestEntry, len(entries))
	for _, entry := range entries {
		indexed[entry.Key] = entry
	}
	return indexed
}

// differs reports whether the current entry's size, mtime, or hash differs from
// the previous one. A probe entry is compared on its hash alone.
func differs(current, previous ManifestEntry) bool {
	if current.Hash != previous.Hash {
		return true
	}
	if isProbe(current) {
		return false
	}
	return current.Size != previous.Size || !current.ModTime.Equal(previous.ModTime)
}

// sortEntries orders entries by key, so a manifest is deterministic.
func sortEntries(entries []ManifestEntry) {
	sort.Slice(entries, func(i, j int) bool { return entryKeyLess(entries[i], entries[j]) })
}

// entryKeyLess orders two entries by key. It is a strict weak ordering: an entry
// is never less than itself, so entries with equal keys keep their order.
func entryKeyLess(a, b ManifestEntry) bool {
	return a.Key < b.Key
}

// isProbe reports whether entry names a credential probe rather than a file. A
// probe carries neither a size nor an mtime, while every file entry carries at
// least one of them (a file's mtime is never zero).
func isProbe(entry ManifestEntry) bool {
	return entry.Size == 0 && entry.ModTime.IsZero()
}
