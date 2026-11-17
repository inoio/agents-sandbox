package readback

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/filewalk"
)

// maxHashedBytes caps the size of a file whose content is hashed. A larger
// declared file (opencode2's ~13 MB churny database) is recorded opaque:
// size and mtime only.
const maxHashedBytes = 256 * 1024

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
// maxHashedBytes are recorded opaque (size+mtime, no hash); a probe entry
// carries neither size nor mtime, only the digest of the rows it read — or the
// sentinel hash when that read failed. An agent with no declared paths yields
// an empty manifest, never an error; a declared path that does not exist yet is
// simply absent, so a first run has nothing to compare against.
func TakeManifest(home, agentName string) (Manifest, error) {
	var manifest Manifest
	seen := map[string]bool{}
	for _, declaredPath := range declaredReadBackPaths(agentName) {
		if err := appendDeclaredPath(&manifest, seen, home, declaredPath); err != nil {
			return Manifest{}, err
		}
	}
	appendCredentialProbes(&manifest, seen, home, agentName)
	sortEntries(manifest.Entries)
	return manifest, nil
}

// appendCredentialProbes appends exactly one entry per credential probe the
// named agent declares, keyed "<agent>:<probe>" and carrying the digest of the
// rows the probe read — or the sentinel hash when that read failed, so an
// unreadable database never looks like a removed probe.
func appendCredentialProbes(manifest *Manifest, seen map[string]bool, home, agentName string) {
	for _, probe := range declaredCredentialProbes(agentName) {
		key := probeSource(agentName, probe)
		if seen[key] {
			continue
		}
		result := runCredentialProbe(home, probe)
		seen[key] = true
		manifest.Entries = append(manifest.Entries, ManifestEntry{
			Key:     key,
			Size:    0,
			ModTime: time.Time{},
			Hash:    result.RowHash,
		})
	}
}

// declaredReadBackPaths returns the home-relative paths the named agent's
// read-back declares, or nothing for an agent without a ReadBackDescriber
// (unknown, or not yet verified). An unverified agent yields an empty manifest,
// never an error.
func declaredReadBackPaths(agentName string) []string {
	a, ok := agent.Lookup(agentName)
	if !ok {
		return nil
	}
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
	a, ok := agent.Lookup(agentName)
	if !ok {
		return nil
	}
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
	take := func(rel string, isDir bool) bool {
		if isDir {
			return true
		}
		matches, err := filepath.Match(base, rel)
		return err == nil && matches
	}
	return filewalk.Walk(root, take, func(entry filewalk.Entry) error {
		manifestEntry, err := describeFile(home, entry)
		if err != nil {
			return err
		}
		if seen[manifestEntry.Key] {
			return nil
		}
		seen[manifestEntry.Key] = true
		manifest.Entries = append(manifest.Entries, manifestEntry)
		return nil
	})
}

// describeFile records the size and mtime of the file behind entry, keyed by its
// sandbox-home-relative path, and hashes its content when it is within the cap.
// An over-cap file is recorded opaque.
func describeFile(home string, entry filewalk.Entry) (ManifestEntry, error) {
	key, err := filepath.Rel(home, entry.Path)
	if err != nil {
		return ManifestEntry{}, fmt.Errorf("relativize declared path %s: %w", entry.Path, err)
	}
	info, err := os.Stat(entry.Path)
	if err != nil {
		return ManifestEntry{}, fmt.Errorf("stat declared path %s: %w", key, err)
	}
	manifestEntry := ManifestEntry{Key: key, Size: info.Size(), ModTime: info.ModTime(), Hash: ""}
	if info.Size() > maxHashedBytes {
		return manifestEntry, nil
	}
	digest := sha256.Sum256(entry.Data)
	manifestEntry.Hash = hex.EncodeToString(digest[:])
	return manifestEntry, nil
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
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
}

// isProbe reports whether entry names a credential probe rather than a file. A
// probe carries neither a size nor an mtime, while every file entry carries at
// least one of them (a file's mtime is never zero).
func isProbe(entry ManifestEntry) bool {
	return entry.Size == 0 && entry.ModTime.IsZero()
}
