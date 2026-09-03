// Package configmerge merges agent configuration snippet files matching a
// caller-supplied glob pattern into a single JSON config document consumed by
// the sandbox. It is agent-agnostic: the snippet pattern and config path come
// from the agent.
package configmerge

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/titanous/json5"
	"gopkg.in/yaml.v3"
)

// snippetFileMatches reports whether name matches the agent's snippet pattern
// glob (e.g. "opencode*.json*", "pi-*.{json,yaml}" or "settings*.json*").
func snippetFileMatches(pattern, name string) bool {
	return matchesGlob(pattern, name)
}

// matchesGlob reports whether name matches pattern, supporting *, **, ?, and
// {a,b} brace alternatives. Matching is done against the basename only.
func matchesGlob(pattern, name string) bool {
	base := path.Base(name)
	for _, concrete := range expandBraces(pattern) {
		ok, err := path.Match(concrete, base)
		if err == nil && ok {
			return true
		}
	}
	return false
}

// expandBraces expands {a,b,c} alternatives into a list of concrete patterns.
// Patterns without braces are returned unchanged.
func expandBraces(pattern string) []string {
	open := strings.Index(pattern, "{")
	if open < 0 {
		return []string{pattern}
	}
	closeIdx := strings.Index(pattern[open:], "}")
	if closeIdx < 0 {
		return []string{pattern}
	}
	closeIdx += open
	prefix := pattern[:open]
	suffix := pattern[closeIdx+1:]
	var expanded []string
	for alt := range strings.SplitSeq(pattern[open+1:closeIdx], ",") {
		expanded = append(expanded, expandBraces(prefix+alt+suffix)...)
	}
	return expanded
}

// deepMerge merges override into base recursively. Maps are merged key by key;
// any other value in override replaces the base value.
func deepMerge(base, override map[string]any) map[string]any {
	result := make(map[string]any, len(base))
	maps.Copy(result, base)
	for k, v := range override {
		if existing, ok := result[k]; ok {
			existingMap, ok1 := existing.(map[string]any)
			overrideMap, ok2 := v.(map[string]any)
			if ok1 && ok2 {
				result[k] = deepMerge(existingMap, overrideMap)
				continue
			}
		}
		result[k] = v
	}
	return result
}

// scanSnippets reads every file whose basename matches pattern across dirs and
// returns a single deep-merged map plus the ordered list of source files that
// produced it. Directory order is user first then project; within a directory
// files are merged in alphabetical order, so later files override earlier ones.
// The source list is in the same merge order.
func scanSnippets(dirs []string, pattern string) (map[string]any, []string) {
	var merged map[string]any
	var sources []string
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if entry.IsDir() || !snippetFileMatches(pattern, entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var cfg map[string]any
			if err := parseConfigFile(entry.Name(), data, &cfg); err != nil {
				continue
			}
			merged = deepMerge(merged, cfg)
			sources = append(sources, path)
		}
	}
	if merged == nil {
		merged = map[string]any{}
	}
	return merged, sources
}

// parseConfigFile unmarshals config data into cfg, choosing a parser based on
// the file extension: .yaml/.yml use YAML, everything else uses JSON5.
func parseConfigFile(name string, data []byte, cfg *map[string]any) error {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml":
		return yaml.Unmarshal(data, cfg)
	default:
		return json5.Unmarshal(data, cfg)
	}
}

// BuildMerged merges all snippet files matching pattern under userDir and
// projectDir into a single config document (JSON output). It returns the
// marshaled bytes, the ordered list of snippet files that were merged, a
// boolean reporting whether any snippet existed, and an error. When no snippet
// exists the returned bytes and source list are nil and the boolean is false.
func BuildMerged(pattern string, userDir, projectDir string) ([]byte, []string, bool, error) {
	merged, sources := scanSnippets([]string{userDir, projectDir}, pattern)
	if len(sources) == 0 {
		return nil, nil, false, nil
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, nil, false, err
	}
	return append(data, '\n'), sources, true, nil
}

// MirrorEntry is one verbatim file the config-dir mirror provisions: its host
// source path, its VM destination path, and the file content.
type MirrorEntry struct {
	HostPath string
	VMPath   string
	Data     []byte
}

// ScanMirror walks userDir then projectDir recursively and returns the verbatim
// mirror entries: every file except top-level files whose basename matches the
// snippet pattern (they are merged, not mirrored) and top-level config-family
// names (reserved for the merged output). Project entries override user entries
// with the same destination path. Entries are sorted by VMPath for
// determinism. Unreadable files are skipped, never fatal.
func ScanMirror(pattern string, family []string, userDir, projectDir, destDir string) ([]MirrorEntry, error) {
	byDest := make(map[string]MirrorEntry)
	for _, dir := range []string{userDir, projectDir} {
		if dir == "" {
			continue
		}
		if err := scanMirrorDir(pattern, family, dir, destDir, byDest); err != nil {
			return nil, err
		}
	}
	dests := make([]string, 0, len(byDest))
	for dest := range byDest {
		dests = append(dests, dest)
	}
	sort.Strings(dests)
	entries := make([]MirrorEntry, 0, len(dests))
	for _, dest := range dests {
		entries = append(entries, byDest[dest])
	}
	return entries, nil
}

// scanMirrorDir walks dir and adds every mirrorable file to byDest, keyed by
// its VM destination path under destDir.
func scanMirrorDir(pattern string, family []string, dir, destDir string, byDest map[string]MirrorEntry) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries; never fail provisioning
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil || rel == "." {
			return nil //nolint:nilerr // skip entries outside the walk root
		}
		topLevel := !strings.Contains(rel, string(filepath.Separator))
		if topLevel && (snippetFileMatches(pattern, d.Name()) || inFamily(family, d.Name())) {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // reads within the config dir we own
		if readErr != nil {
			return nil //nolint:nilerr // skip unreadable file; never fail provisioning
		}
		dest := filepath.Join(destDir, rel)
		byDest[dest] = MirrorEntry{HostPath: path, VMPath: dest, Data: data}
		return nil
	})
}

// inFamily reports whether name is one of the reserved config-family names.
func inFamily(family []string, name string) bool {
	return slices.Contains(family, name)
}
