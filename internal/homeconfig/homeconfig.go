// Package homeconfig provisions arbitrary user files into a sandbox home
// directory from an optional YAML manifest (home.yaml) at the user and project
// level. It supports the project's home-file provisioning without embedding
// any vendor-specific configuration.
package homeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/titanous/json5"
	"gopkg.in/yaml.v3"

	"github.com/inoio/opencode-sandbox/internal/yamlfmt"
)

// manifestName is the fixed manifest filename.
const manifestName = "home.yaml"

// supportedExts are the config-file extensions, tried in order. The order MUST
// match internal/viperconfig's supportedExts so both packages select the same
// config file for a directory.
//
//nolint:gochecknoglobals // package-level constant slice
var supportedExts = []string{".yaml", ".yml", ".json", ".jsonc", ".json5"}

const (
	extJSON5 = ".json5"
	extJSONC = ".jsonc"
)

// findConfigFile returns the first existing config file in dir.
func findConfigFile(dir string) (string, string, bool) {
	if dir == "" {
		return "", "", false
	}
	for _, ext := range supportedExts {
		path := filepath.Join(dir, "config"+ext)
		if _, err := os.Stat(path); err == nil {
			return path, ext, true
		}
	}
	return "", "", false
}

// ParseHomeSection parses the home manifest from raw config-file data (YAML or
// JSON; JSON5/JSONC must be normalized first). The boolean reports whether a
// top-level "home" key was present. path is used only for error messages.
func ParseHomeSection(data []byte, path string) (Manifest, bool, error) {
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, false, yamlfmt.WrapErr(path, err)
	}
	homeRaw, ok := root["home"]
	if !ok {
		return Manifest{}, false, nil
	}
	m, err := parseManifestValue(homeRaw, path)
	if err != nil {
		return nil, false, err
	}
	return m, true, nil
}

// parseManifestValue decodes a home value (a map of target to source) into a
// Manifest.
func parseManifestValue(raw any, path string) (Manifest, error) {
	m := Manifest{}
	if raw == nil {
		return m, nil
	}
	rawMap, ok := raw.(map[string]any)
	if !ok {
		// yaml.v3 decodes maps with non-string keys (e.g. numeric targets) into
		// map[any]any; accept them by stringifying each key.
		anyMap, ok := raw.(map[any]any)
		if !ok {
			return nil, fmt.Errorf("parse %s: home must be a map of target to source", path)
		}
		rawMap = make(map[string]any, len(anyMap))
		for k, v := range anyMap {
			rawMap[fmt.Sprint(k)] = v
		}
	}
	for target, v := range rawMap {
		e, err := parseEntry(v)
		if err != nil {
			return nil, fmt.Errorf("parse %s entry %q: %w", path, target, err)
		}
		m[target] = e
	}
	return m, nil
}

// ReadHomeFromConfigDir reads the config file in dir and returns its home
// manifest. The boolean reports whether a home key was present.
func ReadHomeFromConfigDir(dir string) (Manifest, bool, error) {
	path, ext, ok := findConfigFile(dir)
	if !ok {
		return Manifest{}, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read config %s: %w", path, err)
	}
	if ext == extJSON5 || ext == extJSONC {
		var m map[string]any
		if unmarshalErr := json5.Unmarshal(data, &m); unmarshalErr != nil {
			return nil, false, fmt.Errorf("parse config %s: %w", path, unmarshalErr)
		}
		if data, err = json.Marshal(m); err != nil {
			return nil, false, fmt.Errorf("normalize config %s: %w", path, err)
		}
	}
	return ParseHomeSection(data, path)
}

// startupHook is the only supported startup-hook value.
const startupHook = "startup"

// Entry describes a single home.yaml mapping: the host source path and the
// optional startup-hook metadata. A plain-string value is equivalent to an
// Entry with only Source set.
type Entry struct {
	Source string // host source path, resolved like the plain string form
	Hook   string // optional; only "startup" is supported
	Root   bool   // optional; run the hook as root (default: the sandbox user, dev)
}

// Manifest maps a VM-home-relative target path to its Entry.
type Manifest map[string]Entry

// LoadManifest parses a home.yaml manifest into a Manifest. Each value may be
// either a plain source string (as before) or a mapping with optional
// source/hook/user fields. Unknown hook values are rejected.
func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, yamlfmt.WrapErr(path, err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	m := Manifest{}
	for target, v := range raw {
		e, err := parseEntry(v)
		if err != nil {
			return nil, fmt.Errorf("parse %s entry %q: %w", path, target, err)
		}
		m[target] = e
	}
	return m, nil
}

func parseEntry(v any) (Entry, error) {
	switch val := v.(type) {
	case nil:
		return Entry{}, nil
	case string:
		return Entry{Source: val, Hook: "", Root: false}, nil
	case map[string]any:
		var e Entry
		if s, ok := val["source"]; ok {
			src, ok := s.(string)
			if !ok {
				return e, errors.New("source must be a string")
			}
			e.Source = src
		}
		if h, ok := val["hook"]; ok {
			hook, ok := h.(string)
			if !ok {
				return e, errors.New("hook must be a string")
			}
			e.Hook = hook
		}
		if e.Hook != "" && e.Hook != startupHook {
			return e, fmt.Errorf("hook must be %q, got %q", startupHook, e.Hook)
		}
		if r, ok := val["root"]; ok {
			root, ok := r.(bool)
			if !ok {
				return e, errors.New("root must be a boolean")
			}
			e.Root = root
		}
		return e, nil
	default:
		return Entry{}, fmt.Errorf("unsupported value type %T", v)
	}
}

// MergeManifests returns a single map with later layers overriding earlier ones
// by key.
func MergeManifests(layers ...Manifest) Manifest {
	merged := Manifest{}
	for _, layer := range layers {
		maps.Copy(merged, layer)
	}
	return merged
}

// resolveManifestSources returns each layer's sources resolved against that layer's own
// manifest dir, for the dirs passed. The returned resolved layers keep project-wins-per-key when merged.
func resolveManifestSources(manifests []Manifest, dirs []string) ([]Manifest, error) {
	resolved := make([]Manifest, 0, len(manifests))
	for i, layer := range manifests {
		out := make(Manifest, len(layer))
		for target, e := range layer {
			src, err := ResolveManifestSource(target, e.Source, dirs[i])
			if err != nil {
				return nil, err
			}
			e.Source = src
			out[target] = e
		}
		resolved = append(resolved, out)
	}
	return resolved, nil
}

// ResolveManifestSource resolves a manifest source value to a host path.
//
//	empty            -> host $HOME/<target>
//	starts with "/"  -> absolute
//	starts with "~"  -> host $HOME/<rest>
//	otherwise        -> relative to manifestDir
func ResolveManifestSource(target, source, manifestDir string) (string, error) {
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
// ".." traversal), and any target listed in reserved. The reserved entries are
// home-relative paths the manifest must not target (e.g., the agent's merged
// config path); when reserved is empty, no target is rejected.
func ResolveVMTarget(homeBase, relTarget string, reserved []string) (string, error) {
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
	if slices.Contains(reserved, clean) {
		return "", fmt.Errorf(
			"home manifest target %q is reserved and cannot be provisioned from home.yaml",
			relTarget,
		)
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
func loadLayers(userConfigDir, projectConfigDir string) ([]Manifest, bool, error) {
	var layers []Manifest
	has := false
	for _, dir := range []string{userConfigDir, projectConfigDir} {
		layer := Manifest{}
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
// It returns the desired home files keyed by absolute VM path, the resolved
// source paths that could not be read (missing files), and whether any manifest
// existed. A missing host source file is skipped, not fatal; its resolved path
// is reported so callers can warn. The reserved list holds home-relative VM
// paths the manifest must not target (e.g., the agent's merged config path);
// when empty, no target is reserved.
func BuildHomeFiles(
	userConfigDir, projectConfigDir, homeBase string,
	reserved []string,
) (map[string][]byte, []string, bool, error) {
	layers, has, err := loadLayers(userConfigDir, projectConfigDir)
	if err != nil {
		return nil, nil, false, err
	}
	resolved, err := resolveManifestSources(layers, []string{userConfigDir, projectConfigDir})
	if err != nil {
		return nil, nil, false, err
	}
	merged := MergeManifests(resolved...)
	if len(merged) == 0 {
		return map[string][]byte{}, nil, has, nil
	}
	files := make(map[string][]byte)
	var missing []string
	for target, e := range merged {
		vmPath, vErr := ResolveVMTarget(homeBase, target, reserved)
		if vErr != nil {
			return nil, nil, false, vErr
		}
		data, rErr := os.ReadFile(e.Source)
		if rErr != nil {
			missing = append(missing, e.Source)
			continue
		}
		files[vmPath] = data
	}
	return files, missing, has, nil
}

// DescribeManifest returns the merged home.yaml manifest as resolved
// (VM target path, host source path) pairs sorted by VM path, independent of
// whether the source files exist. The boolean reports whether at least one
// manifest file exists. The reserved list holds home-relative VM paths the
// manifest must not target (e.g., the agent's merged config path); when empty,
// no target is reserved. It is used by `config home` to list all mappings.
func DescribeManifest(userConfigDir, projectConfigDir, homeBase string, reserved []string) ([][2]string, bool, error) {
	layers, has, err := loadLayers(userConfigDir, projectConfigDir)
	if err != nil {
		return nil, false, err
	}
	resolved, err := resolveManifestSources(layers, []string{userConfigDir, projectConfigDir})
	if err != nil {
		return nil, false, err
	}
	var pairs [][2]string
	for target, e := range MergeManifests(resolved...) {
		vmPath, err := ResolveVMTarget(homeBase, target, reserved)
		if err != nil {
			return nil, false, err
		}
		pairs = append(pairs, [2]string{vmPath, e.Source})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i][0] < pairs[j][0]
	})
	return pairs, has, nil
}

// HookSpec describes a single startup-hook entry: the provisioned VM target,
// its resolved host source, the interpreter declared by the script's shebang,
// and whether to run it as root.
type HookSpec struct {
	Target      string // absolute VM path to the provisioned script
	Source      string // resolved host source path
	Interpreter string // script's shebang interpreter; empty falls back to /bin/sh
	Root        bool   // run as root; false runs as the sandbox user (dev)
}

// shebangInterpreter returns the interpreter named by the first `#!` line of
// the file at path, or "" if there is none. `#!/usr/bin/env bash` yields
// "/usr/bin/env bash" so the env command resolves the real interpreter inside
// the VM.
func shebangInterpreter(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var line []byte
	buf := make([]byte, 1)
	for range 2 {
		if _, err := f.Read(buf); err != nil {
			return ""
		}
		line = append(line, buf[0])
	}
	if string(line) != "#!" {
		return ""
	}
	for {
		b, err := f.Read(buf)
		if err != nil {
			break
		}
		if b == 0 {
			break
		}
		if buf[0] == '\n' {
			break
		}
		line = append(line, buf[0])
	}
	interp := strings.TrimSpace(strings.TrimPrefix(string(line), "#!"))
	if interp == "" {
		return ""
	}
	return interp
}

// BuildHooks returns the merged manifest's startup-hook entries (Hook ==
// "startup") whose host source exists, sorted by VM target. A hook whose host
// source is missing is skipped (its script will not have been provisioned). The
// reserved list holds home-relative VM paths the manifest must not target (e.g.,
// the agent's merged config path); when empty, no target is reserved.
func BuildHooks(userConfigDir, projectConfigDir, homeBase string, reserved []string) ([]HookSpec, error) {
	layers, _, err := loadLayers(userConfigDir, projectConfigDir)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveManifestSources(layers, []string{userConfigDir, projectConfigDir})
	if err != nil {
		return nil, err
	}
	var hooks []HookSpec
	for target, e := range MergeManifests(resolved...) {
		if e.Hook != startupHook {
			continue
		}
		if _, err := os.Stat(e.Source); err != nil {
			continue
		}
		vmPath, vErr := ResolveVMTarget(homeBase, target, reserved)
		if vErr != nil {
			return nil, vErr
		}
		hooks = append(
			hooks,
			HookSpec{Target: vmPath, Source: e.Source, Interpreter: shebangInterpreter(e.Source), Root: e.Root},
		)
	}
	sort.Slice(hooks, func(i, j int) bool {
		return hooks[i].Target < hooks[j].Target
	})
	return hooks, nil
}
