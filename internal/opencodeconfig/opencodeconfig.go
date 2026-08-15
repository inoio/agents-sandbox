// Package opencodeconfig merges opencode configuration snippet files into a single
// opencode config consumed by the sandbox.
package opencodeconfig

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/titanous/json5"
)

// isJSONFile reports whether name is an opencode config snippet (json, jsonc,
// or json5) regardless of case.
func isJSONFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".json", ".jsonc", ".json5":
		return true
	}
	return false
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

// scanSnippets reads every json/jsonc/json5 file across dirs and returns a
// single deep-merged map plus the ordered list of source files that produced
// it. Directory order is user first then project; within a directory files are
// merged in alphabetical order, so later files override earlier ones. The
// source list is in the same merge order.
func scanSnippets(dirs ...string) (map[string]any, []string) {
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
			if entry.IsDir() || !isJSONFile(entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var cfg map[string]any
			if err := json5.Unmarshal(data, &cfg); err != nil {
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

// scanSnippetFiles reads every json/jsonc/json5 file across dirs and returns a
// single deep-merged map.
func scanSnippetFiles(dirs ...string) map[string]any {
	merged, _ := scanSnippets(dirs...)
	return merged
}

// BuildOpenCodeJSON merges all opencode snippet files under userDir and
// projectDir into a single opencode.json document. It returns the marshaled
// bytes, the ordered list of snippet files that were merged, a boolean
// reporting whether any snippet existed, and an error. When no snippet exists
// the returned bytes and source list are nil and the boolean is false; no
// opencode.json should then be provisioned into the VM.
func BuildOpenCodeJSON(userDir, projectDir string) ([]byte, []string, bool, error) {
	merged, sources := scanSnippets(userDir, projectDir)
	if len(sources) == 0 {
		return nil, nil, false, nil
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, nil, false, err
	}
	return append(data, '\n'), sources, true, nil
}
