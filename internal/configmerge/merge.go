// Package configmerge merges agent configuration snippet files matching a
// caller-supplied glob pattern into a single config document consumed by the
// sandbox, written as YAML for a .yaml/.yml config path, as TOML for a .toml
// path, and as JSON otherwise. It is agent-agnostic: the snippet pattern and
// config path come from the agent.
package configmerge

import (
	"encoding/json"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/titanous/json5"
	"go.yaml.in/yaml/v3"

	"github.com/inoio/agents-sandbox/internal/filewalk"
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
		// path.Match reports ok=false for a malformed pattern, so its error
		// cannot change the result: only the match outcome matters.
		if ok, _ := path.Match(concrete, base); ok {
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
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY,INTEGER_INCREMENT reason="the searched substring starts with '{', so the '}' index is -1 or at least 1, never 0"
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
			overrideMap, ok2 := v.(map[string]any)
			// deepMerge(nil, overrideMap) copies the override, so a non-map
			// base value needs no special case: only the override decides.
			if ok2 {
				existingMap, _ := existing.(map[string]any)
				result[k] = deepMerge(existingMap, overrideMap)
				continue
			}
		}
		result[k] = v
	}
	return result
}

// readDir is overridable so tests can exercise the partial-read error path
// without provoking a real directory-read fault.
var readDir = os.ReadDir

// scanSnippets reads every file whose basename matches pattern across dirs and
// returns a single deep-merged map plus the ordered list of source files that
// produced it. Directory order is user first then project; within a directory
// files are merged in alphabetical order, so later files override earlier ones.
// The source list is in the same merge order.
func scanSnippets(dirs []string, pattern string) (map[string]any, []string) {
	var merged map[string]any
	var sources []string
	for _, dir := range dirs {
		// gomutants:disable-next-line BRANCH_IF reason="os.ReadDir of an empty path errors and the next guard continues, so skipping it agrees with falling through"
		if dir == "" {
			continue
		}
		entries, err := readDir(dir)
		// readDir (os.ReadDir) can return the entries it read before an
		// error; those partial entries must be discarded, not merged.
		if err != nil {
			continue
		}
		// gomutants:disable-next-line STATEMENT_REMOVE reason="os.ReadDir already returns entries sorted by filename, so the explicit sort is order-preserving"
		sort.Slice(entries, func(i, j int) bool {
			// gomutants:disable-next-line CONDITIONALS_BOUNDARY,RETURN_FALSE reason="filenames within a directory are unique and ReadDir already sorted them, so < and <= agree and always-false leaves the order unchanged"
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			// gomutants:disable-next-line EXPRESSION_REMOVE reason="a directory that slips past reaches os.ReadFile, which errors and the guard below continues, so IsDir is redundant"
			if entry.IsDir() || !snippetFileMatches(pattern, entry.Name()) {
				continue
			}
			snippetPath := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(snippetPath)
			if err != nil {
				continue
			}
			var cfg map[string]any
			if err := parseConfigFile(entry.Name(), data, &cfg); err != nil {
				continue
			}
			merged = deepMerge(merged, cfg)
			sources = append(sources, snippetPath)
		}
	}
	if merged == nil {
		merged = map[string]any{}
	}
	return merged, sources
}

// configFormat is the serialization format a config path implies.
type configFormat int

const (
	configJSON configFormat = iota
	configTOML
	configYAML
)

// configFormatFor classifies a config path by extension: .toml is TOML,
// .yaml/.yml is YAML, everything else (.json, .jsonc, .json5) is the JSON
// family. It is the single source of the extension-to-format mapping, shared by
// parsing and marshaling.
func configFormatFor(configPath string) configFormat {
	switch strings.ToLower(filepath.Ext(configPath)) {
	case ".toml":
		return configTOML
	case ".yaml", ".yml":
		return configYAML
	default:
		return configJSON
	}
}

// parseConfigFile unmarshals config data into cfg in the format its extension
// implies.
func parseConfigFile(name string, data []byte, cfg *map[string]any) error {
	switch configFormatFor(name) {
	case configTOML:
		return toml.Unmarshal(data, cfg)
	case configYAML:
		return yaml.Unmarshal(data, cfg)
	case configJSON:
		return json5.Unmarshal(data, cfg)
	default:
		return json5.Unmarshal(data, cfg)
	}
}

// MergedConfig is the outcome of merging snippet files: the marshaled config
// document and the ordered list of snippet files it came from. When no snippet
// existed, HasSnippets is false and Content and Sources are nil.
type MergedConfig struct {
	Content     []byte
	Sources     []string
	HasSnippets bool
}

// BuildMerged merges all snippet files matching pattern under userDir and
// projectDir into a single config document for configPath: YAML when configPath
// ends in .yaml/.yml, TOML when it ends in .toml, indented JSON otherwise.
func BuildMerged(configPath, pattern, userDir, projectDir string) (MergedConfig, error) {
	merged, sources := scanSnippets([]string{userDir, projectDir}, pattern)
	if len(sources) == 0 {
		return MergedConfig{}, nil
	}
	data, err := marshalConfig(configPath, merged)
	if err != nil {
		return MergedConfig{}, err
	}
	return MergedConfig{Content: data, Sources: sources, HasSnippets: true}, nil
}

// marshalConfig encodes merged in the format configPath calls for: YAML for a
// .yaml/.yml path, TOML for a .toml path, indented JSON otherwise. A YAML
// config file (hermes' config.yaml) is both the agent's input and something
// users edit, so it is never handed a JSON document under a .yaml name.
func marshalConfig(configPath string, merged map[string]any) ([]byte, error) {
	switch configFormatFor(configPath) {
	case configTOML:
		return toml.Marshal(merged)
	case configYAML:
		return yaml.Marshal(merged)
	case configJSON:
		return marshalJSONConfig(merged)
	default:
		return marshalJSONConfig(merged)
	}
}

// marshalJSONConfig encodes merged as indented JSON, which both JSON5 and JSONC
// readers accept.
func marshalJSONConfig(merged map[string]any) ([]byte, error) {
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DocumentsEqual reports whether a and b decode to the same config document,
// parsed in the format configPath's extension calls for. Either side failing
// to parse counts as a difference.
func DocumentsEqual(configPath string, a, b []byte) bool {
	var documentA, documentB map[string]any
	if parseConfigFile(configPath, a, &documentA) != nil || parseConfigFile(configPath, b, &documentB) != nil {
		return false
	}
	return reflect.DeepEqual(documentA, documentB)
}

// MirrorEntry is one verbatim file the config-dir mirror provisions: its host
// source path, its VM destination path, and the file content.
type MirrorEntry struct {
	HostPath string
	VMPath   string
	Data     []byte
	Mode     os.FileMode
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
		// gomutants:disable-next-line BRANCH_IF reason="filewalk.Walk of an empty path swallows its Lstat error and visits nothing, so skipping it agrees with walking it"
		if dir == "" {
			continue
		}
		// gomutants:disable-next-line BRANCH_IF reason="filewalk.Walk never returns a non-nil error, so this guard is unreachable"
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
	return filewalk.Walk(
		dir,
		func(rel string, isDir bool) bool {
			if isDir {
				return true
			}
			if strings.Contains(rel, string(filepath.Separator)) {
				return true
			}
			name := filepath.Base(rel)
			return !snippetFileMatches(pattern, name) && !inFamily(family, name)
		},
		func(entry filewalk.Entry) error {
			dest := filepath.Join(destDir, entry.Rel)
			byDest[dest] = MirrorEntry{
				HostPath: entry.Path,
				VMPath:   dest,
				Data:     entry.Data,
				Mode:     entry.Mode,
			}
			return nil
		},
	)
}

// inFamily reports whether name is one of the reserved config-family names.
func inFamily(family []string, name string) bool {
	return slices.Contains(family, name)
}
