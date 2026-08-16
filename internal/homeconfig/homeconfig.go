// Package homeconfig provisions arbitrary user files into a sandbox home
// directory from an optional YAML manifest (home.yaml) at the user and project
// level. It supports the project's home-file provisioning without embedding
// any vendor-specific configuration.
package homeconfig

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// manifestName is the fixed manifest filename.
const manifestName = "home.yaml"

// opencodeConfigPath is the reserved VM path for the snippet-merged opencode
// config; the manifest must not target it.
const opencodeConfigPath = ".config/opencode/opencode.json"

// resolveLayers returns each layer's sources resolved against that layer's own
// manifest dir, for the user layer then the project layer. The returned resolved
// layers keep project-wins-per-key when merged.
func resolveLayers(layers []map[string]string, dirs []string) ([]map[string]string, error) {
	resolved := make([]map[string]string, 0, len(layers))
	for i, layer := range layers {
		out := make(map[string]string, len(layer))
		for target, source := range layer {
			src, err := ResolveSource(target, source, dirs[i])
			if err != nil {
				return nil, err
			}
			out[target] = src
		}
		resolved = append(resolved, out)
	}
	return resolved, nil
}

// LoadManifest parses a home.yaml manifest into a map from VM-home-relative
// target path to host source string (possibly empty).
func LoadManifest(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

// MergeManifests returns a single map with later layers overriding earlier ones
// by key.
func MergeManifests(layers ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, layer := range layers {
		maps.Copy(merged, layer)
	}
	return merged
}

// ResolveSource resolves a manifest source value to a host path.
//
//	empty            -> host $HOME/<target>
//	starts with "/"  -> absolute
//	starts with "~"  -> host $HOME/<rest>
//	otherwise        -> relative to manifestDir
func ResolveSource(target, source, manifestDir string) (string, error) {
	home, _ := os.UserHomeDir()
	switch {
	case source == "":
		return filepath.Join(home, target), nil
	case strings.HasPrefix(source, "/"):
		return source, nil
	case strings.HasPrefix(source, "~"):
		return filepath.Join(home, strings.TrimPrefix(source, "~")), nil
	default:
		return filepath.Join(manifestDir, source), nil
	}
}

// ResolveVMTarget validates a VM-home-relative target and returns its absolute
// path under homeBase. It rejects empty targets, absolute paths, `~`-prefixed
// paths (targets are already home-relative), paths that escape homeBase (e.g.
// ".." traversal), and the reserved opencode config path.
func ResolveVMTarget(homeBase, relTarget string) (string, error) {
	if relTarget == "" {
		return "", errors.New("home manifest target must not be empty")
	}
	if filepath.IsAbs(relTarget) {
		return "", fmt.Errorf("home manifest target %q must be relative to the home directory", relTarget)
	}
	if strings.HasPrefix(relTarget, "~") {
		return "", fmt.Errorf(
			"home manifest target %q must not start with ~; targets are already relative to the home directory",
			relTarget,
		)
	}
	clean := filepath.Clean(relTarget)
	if clean == opencodeConfigPath {
		return "", fmt.Errorf("home manifest target %q is reserved for the merged opencode config", relTarget)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("home manifest target %q escapes the home directory", relTarget)
	}
	home := filepath.Clean(homeBase)
	abs := filepath.Join(home, clean)
	if abs != home && !strings.HasPrefix(abs, home+string(filepath.Separator)) {
		return "", fmt.Errorf("home manifest target %q is not within the home directory", relTarget)
	}
	return abs, nil
}

// loadLayers reads each present manifest; an absent manifest yields an empty
// layer. The boolean reports whether at least one manifest file exists.
func loadLayers(userConfigDir, projectConfigDir string) ([]map[string]string, bool, error) {
	var layers []map[string]string
	has := false
	for _, dir := range []string{userConfigDir, projectConfigDir} {
		layer := map[string]string{}
		path := filepath.Join(dir, manifestName)
		if _, err := os.Stat(path); err == nil {
			has = true
			m, err := LoadManifest(path)
			if err != nil {
				return nil, false, err
			}
			layer = m
		}
		layers = append(layers, layer)
	}
	return layers, has, nil
}

// BuildHomeFiles loads the user and project home.yaml manifests, merges them
// (project wins per key), resolves each entry, and reads the host source files.
// It returns the desired home files keyed by absolute VM path and whether any
// manifest existed. A missing host source file is skipped, not fatal.
func BuildHomeFiles(userConfigDir, projectConfigDir, homeBase string) (map[string][]byte, bool, error) {
	layers, has, err := loadLayers(userConfigDir, projectConfigDir)
	if err != nil {
		return nil, false, err
	}
	resolved, err := resolveLayers(layers, []string{userConfigDir, projectConfigDir})
	if err != nil {
		return nil, false, err
	}
	merged := MergeManifests(resolved...)
	if len(merged) == 0 {
		return map[string][]byte{}, has, nil
	}
	files := make(map[string][]byte)
	for target, src := range merged {
		vmPath, vErr := ResolveVMTarget(homeBase, target)
		if vErr != nil {
			return nil, false, vErr
		}
		data, rErr := os.ReadFile(src)
		if rErr != nil {
			continue // missing source: warn+skip
		}
		files[vmPath] = data
	}
	return files, has, nil
}

// DescribeManifest returns the merged home.yaml manifest as resolved
// (VM target path, host source path) pairs sorted by VM path, independent of
// whether the source files exist. The boolean reports whether at least one
// manifest file exists. It is used by `config home` to list all mappings.
func DescribeManifest(userConfigDir, projectConfigDir, homeBase string) ([][2]string, bool, error) {
	layers, has, err := loadLayers(userConfigDir, projectConfigDir)
	if err != nil {
		return nil, false, err
	}
	resolved, err := resolveLayers(layers, []string{userConfigDir, projectConfigDir})
	if err != nil {
		return nil, false, err
	}
	var pairs [][2]string
	for target, src := range MergeManifests(resolved...) {
		vmPath, err := ResolveVMTarget(homeBase, target)
		if err != nil {
			return nil, false, err
		}
		pairs = append(pairs, [2]string{vmPath, src})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i][0] < pairs[j][0]
	})
	return pairs, has, nil
}
