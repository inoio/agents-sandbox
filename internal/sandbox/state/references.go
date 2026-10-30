package state

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

// projectStateReferences walks every project state file under the state root and
// maps an object name to the project keys that reference it. extract returns the
// object names a state references (empty when it references none). Both the
// per-agent layout <root>/<slug>/<agent>/state.yaml and the legacy agent-less
// <root>/<slug>/state.yaml are scanned; the launcher-global root state file is
// skipped.
//
// A state file that cannot be read or parsed is an error: the caller cannot
// prove the object is unreferenced, so a destructive removal must fail closed
// (with an explicit override) rather than risk orphaning a project.
func projectStateReferences(extract func(Key, *HomeState) []string) (map[string][]Key, error) {
	root := stateRoot()
	references := map[string][]Key{}
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// A missing state root means there are no project references to
			// protect, so a deletion of the launcher state must not force
			// --force on an otherwise unreferenced object. An unreadable
			// directory or file still fails closed.
			if path == root && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() || entry.Name() != "state.yaml" {
			return nil
		}
		key, ok := keyForStateFile(root, path)
		if !ok {
			return nil
		}
		st, readErr := ReadState(key)
		if readErr != nil {
			return readErr
		}
		for _, name := range extract(key, st) {
			references[name] = append(references[name], key)
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("scan project state references: %w", walkErr)
	}
	return references, nil
}

// HomeVolumeReferences maps each home volume name recorded in a project state
// file to the project keys that reference it, so a destructive removal can fail
// closed on an unreadable or corrupt state (see projectStateReferences). A
// pending RecoveredHomeVolume (a VM-only identity adoption, #144) counts: the
// volume is still the project's saved home until home resolution recovers it.
func HomeVolumeReferences() (map[string][]Key, error) {
	return projectStateReferences(func(_ Key, st *HomeState) []string {
		if st.HomeVolume != "" {
			return []string{st.HomeVolume}
		}
		if st.RecoveredHomeVolume != "" {
			return []string{st.RecoveredHomeVolume}
		}
		return nil
	})
}

// ImageReferences maps each per-agent runner image reference to the project keys
// that own it. A project owns both the prefix-free runner image reference its
// slug and agent name derive (naming.RunnerImageRef) and the pre-change prefixed
// one (naming.DockerImageRef) that an image loaded by an earlier version sits
// under, whether or not its ImageDigest is set: ownership is state presence, not
// digest content. A legacy agent-less key owns none, because runner images are
// per-agent; a legacy VM pinned to a non-"-latest" ref is caught by the
// kept-sandbox scan in image.InUseImageReferences instead. Removal of an image a
// project state still owns must fail closed (see projectStateReferences).
func ImageReferences() (map[string][]Key, error) {
	return projectStateReferences(func(k Key, _ *HomeState) []string {
		if k.Agent == "" {
			return nil
		}
		return []string{naming.RunnerImageRef(k.Slug, k.Agent), naming.DockerImageRef(k.Slug, k.Agent)}
	})
}

// FormatKeys renders referencing project keys as "slug/agent" (or "slug" for a
// legacy agent-less key), sorted for stable output.
func FormatKeys(keys []Key) string {
	labels := make([]string, 0, len(keys))
	for _, k := range keys {
		if k.Agent == "" {
			labels = append(labels, k.Slug)
			continue
		}
		labels = append(labels, k.Slug+"/"+k.Agent)
	}
	sort.Strings(labels)
	return strings.Join(labels, ", ")
}

// keyForStateFile derives the project key for a state.yaml path discovered under
// root. The launcher-global root state file (<root>/state.yaml) is not a project
// state and returns false.
func keyForStateFile(root, path string) (Key, bool) {
	rel, _ := filepath.Rel(root, path)
	parts := strings.Split(rel, string(filepath.Separator))
	switch len(parts) {
	case 2:
		return Key{Slug: parts[0], Agent: ""}, true
	case 3:
		return Key{Slug: parts[0], Agent: parts[1]}, true
	default:
		return Key{}, false
	}
}
