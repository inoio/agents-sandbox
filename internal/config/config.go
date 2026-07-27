package config

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	json5 "github.com/titanous/json5"
)

func LoadProviderConfig(data []byte) (map[string]any, error) {
	var cfg map[string]any
	if err := json5.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func DeepMerge(base, override map[string]any) map[string]any {
	result := make(map[string]any, len(base))
	maps.Copy(result, base)
	for k, v := range override {
		if existing, ok := result[k]; ok {
			if existingMap, ok := existing.(map[string]any); ok {
				if overrideMap, ok := v.(map[string]any); ok {
					result[k] = DeepMerge(existingMap, overrideMap)
					continue
				}
			}
		}
		result[k] = v
	}
	return result
}

func isJSONFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".json" || ext == ".jsonc"
}

func scanJSONFiles(dirs ...string) map[string]map[string]any {
	files := make(map[string]map[string]any)
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
			name := entry.Name()
			files[name] = DeepMerge(files[name], cfg)
		}
	}
	return files
}

func scanOtherFiles(dirs ...string) map[string][]byte {
	files := make(map[string][]byte)
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
			if entry.IsDir() || isJSONFile(entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			files[entry.Name()] = data
		}
	}
	return files
}

type FileDesc struct {
	Name    string
	Sources []string
}

func findSources(name string, dirs ...string) []string {
	var sources []string
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Name() == name {
				sources = append(sources, filepath.Join(dir, name))
			}
		}
	}
	return sources
}

func DescribeConfig(userDir, projectDir string, _ map[string]any) ([]FileDesc, error) {
	dirs := []string{userDir, projectDir}
	var result []FileDesc

	jsonFiles := scanJSONFiles(dirs...)
	for name := range jsonFiles {
		sources := findSources(name, dirs...)
		if name == "opencode.jsonc" || name == "opencode.json" {
			sources = append(sources, "embedded provider config")
		}
		result = append(result, FileDesc{Name: name, Sources: sources})
	}

	otherFiles := scanOtherFiles(dirs...)
	for name := range otherFiles {
		result = append(result, FileDesc{Name: name, Sources: findSources(name, dirs...)})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

func BuildMergedConfig(userDir, projectDir string, providerConfig map[string]any) (map[string][]byte, error) {
	jsonFiles := scanJSONFiles(userDir, projectDir)
	otherFiles := scanOtherFiles(userDir, projectDir)

	result := make(map[string][]byte)
	for name, cfg := range jsonFiles {
		var merged map[string]any
		if name == "opencode.jsonc" || name == "opencode.json" {
			// embedded provider config is the base; user/project config overrides
			merged = DeepMerge(providerConfig, cfg)
		} else {
			merged = cfg
		}
		data, err := json.MarshalIndent(merged, "", "  ")
		if err != nil {
			return nil, err
		}
		result[name] = data
	}

	if _, hasJsonc := result["opencode.jsonc"]; !hasJsonc {
		if _, hasJSON := result["opencode.json"]; !hasJSON {
			data, err := json.MarshalIndent(providerConfig, "", "  ")
			if err != nil {
				return nil, err
			}
			result["opencode.jsonc"] = data
		}
	}

	maps.Copy(result, otherFiles)

	return result, nil
}
