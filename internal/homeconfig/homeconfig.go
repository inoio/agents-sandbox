// Package homeconfig provisions arbitrary user files into a sandbox home
// directory from an optional home: key in the user and project config files.
// It supports the project's home-file provisioning without embedding any
// vendor-specific configuration.
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
	"go.yaml.in/yaml/v3"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/yamlfmt"
)

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
			// gomutants:disable-next-line ERRORF_WRAP reason="parseEntry errors are dynamic errors.New/fmt.Errorf values with no sentinel or typed cause, so no caller matches the chain and %v and %w yield the same message"
			return nil, fmt.Errorf("parse %s entry %q: %w", path, target, err)
		}
		m[target] = e
	}
	return m, nil
}

// ReadHomeFromConfigDir reads the config file in dir and returns its home
// manifest. The boolean reports whether a home key was present.
func ReadHomeFromConfigDir(dir string) (Manifest, bool, error) {
	path, ext, ok := configpaths.FindConfigFile(dir)
	if !ok {
		return Manifest{}, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read config %s: %w", path, err)
	}
	if ext == configpaths.ExtJSON5 || ext == configpaths.ExtJSONC {
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

// Entry describes a single home mapping: the host source path and the
// optional startup-hook metadata. A plain-string value is equivalent to an
// Entry with only Source set.
type Entry struct {
	Source string // host source path, resolved like the plain string form
	Hook   string // optional; only "startup" is supported
	Root   bool   // optional; run the hook as root (default: the sandbox user, dev)
}

// Manifest maps a VM-home-relative target path to its Entry.
type Manifest map[string]Entry

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
				return Entry{}, errors.New("source must be a string")
			}
			e.Source = src
		}
		if h, ok := val["hook"]; ok {
			hook, ok := h.(string)
			if !ok {
				return Entry{}, errors.New("hook must be a string")
			}
			e.Hook = hook
		}
		if e.Hook != "" && e.Hook != startupHook {
			return Entry{}, fmt.Errorf("hook must be %q, got %q", startupHook, e.Hook)
		}
		if r, ok := val["root"]; ok {
			root, ok := r.(bool)
			if !ok {
				return Entry{}, errors.New("root must be a boolean")
			}
			e.Root = root
		}
		return e, nil
	default:
		return Entry{}, fmt.Errorf("unsupported value type %T", v)
	}
}

// Layer pairs a home manifest with the config dir its relative sources resolve
// against.
type Layer struct {
	Manifest Manifest
	Dir      string
}

// Layers is an ordered set of home layers, one per config dir.
type Layers []Layer

// LoadLayers reads the home manifest from each config dir's config file, one
// Layer per dir in order (later layers override earlier ones per target). The
// boolean reports whether any config file declared a home key.
func LoadLayers(dirs []string) ([]Layer, bool, error) {
	layers := make([]Layer, 0, len(dirs))
	has := false
	for _, dir := range dirs {
		m, present, err := ReadHomeFromConfigDir(dir)
		if err != nil {
			return nil, false, err
		}
		if present {
			has = true
		}
		layers = append(layers, Layer{Manifest: m, Dir: dir})
	}
	return layers, has, nil
}

// resolveLayerSources resolves each layer's sources against that layer's own
// config dir.
func resolveLayerSources(layers []Layer) []Layer {
	for i := range layers {
		out := make(Manifest, len(layers[i].Manifest))
		for target, e := range layers[i].Manifest {
			e.Source = ResolveManifestSource(target, e.Source, layers[i].Dir)
			out[target] = e
		}
		layers[i].Manifest = out
	}
	return layers
}

// ResolveManifestSource resolves a manifest source value to a host path.
//
//	empty            -> host $HOME/<target>
//	starts with "/"  -> absolute
//	starts with "~"  -> host $HOME/<rest>
//	otherwise        -> relative to manifestDir
func ResolveManifestSource(target, source, manifestDir string) string {
	switch {
	case source == "":
		home, _ := configpaths.Get().UserHomeDir()
		return filepath.Join(home, target)
	case strings.HasPrefix(source, "/"):
		return source
	case strings.HasPrefix(source, "~"):
		home, _ := configpaths.Get().UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(source, "~"))
	default:
		return filepath.Join(manifestDir, source)
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
			"home manifest target %q is reserved and cannot be provisioned from the config file home: key",
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

// HomeFile contains a home-provisioning source's content and ordinary Unix
// permission bits.
type HomeFile struct {
	Data []byte
	Mode os.FileMode
}

// BuildHomeFilesWithModes loads the user and project config files' home
// manifests, merges them (project wins per target), resolves each entry, and
// reads the host source files. It returns the desired home files keyed by
// absolute VM path, the resolved source paths that could not be read (missing
// files), and whether any manifest existed. A missing host source file is
// skipped, not fatal; its resolved path is reported so callers can warn. The
// reserved list holds home-relative VM paths the manifest must not target (e.g.,
// the agent's merged config path); when empty, no target is reserved. Each
// returned file carries its source's ordinary Unix permission bits.
func BuildHomeFilesWithModes(
	userConfigDir, projectConfigDir, homeBase string,
	reserved []string,
) (map[string]HomeFile, []string, bool, error) {
	layers, has, err := LoadLayers([]string{userConfigDir, projectConfigDir})
	if err != nil {
		return nil, nil, false, err
	}
	resolved := resolveLayerSources(layers)
	merged := Manifest{}
	for _, l := range resolved {
		maps.Copy(merged, l.Manifest)
	}
	// An empty merged manifest short-circuits to an empty (non-nil) map, which is
	// exactly what the general path below returns for zero entries; the boundary
	// and branch mutations there are unobservable.
	// gomutants:disable-next-line INTEGER_DECREMENT,BRANCH_IF reason="with zero entries the early return and the general path both yield an empty non-nil map, a nil missing slice, and the same has flag"
	if len(merged) == 0 {
		return map[string]HomeFile{}, nil, has, nil
	}
	files := make(map[string]HomeFile)
	var missing []string
	for _, target := range slices.Sorted(maps.Keys(merged)) {
		e := merged[target]
		vmPath, vErr := ResolveVMTarget(homeBase, target, reserved)
		if vErr != nil {
			return nil, nil, false, vErr
		}
		info, infoErr := os.Stat(e.Source)
		// A source that Stat cannot stat also fails ReadFile below, which reports
		// it missing the same way, so skipping this guard is unobservable.
		// gomutants:disable-next-line BRANCH_IF reason="when Stat fails ReadFile on the same path also fails, so the fall-through appends the same missing source and continues"
		if infoErr != nil {
			missing = append(missing, e.Source)
			continue
		}
		data, rErr := os.ReadFile(e.Source)
		if rErr != nil {
			missing = append(missing, e.Source)
			continue
		}
		files[vmPath] = HomeFile{Data: data, Mode: info.Mode().Perm()}
	}
	// Reaching here means merged has at least one entry, which requires a layer
	// that declared a home key, so has is always true here.
	// gomutants:disable-next-line RETURN_TRUE reason="a non-empty merged manifest is only possible when a layer declared a home key, which sets has, so forcing has true changes nothing"
	return files, missing, has, nil
}

// DescribeLayers resolves each layer's sources and returns the (VM target,
// host source) pairs sorted by VM target, independent of whether the source
// files exist.
func DescribeLayers(layers []Layer, homeBase string, reserved []string) ([][2]string, error) {
	resolved := resolveLayerSources(layers)
	merged := Manifest{}
	for _, l := range resolved {
		maps.Copy(merged, l.Manifest)
	}
	var pairs [][2]string
	for target, e := range merged {
		vmPath, err := ResolveVMTarget(homeBase, target, reserved)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, [2]string{vmPath, e.Source})
	}
	sort.Slice(pairs, func(i, j int) bool { return describePairLess(pairs[i], pairs[j]) })
	return pairs, nil
}

// describePairLess orders (VM target, host source) pairs by VM target.
func describePairLess(a, b [2]string) bool { return a[0] < b[0] }

// HookSpec describes a single startup-hook entry: the provisioned VM target,
// its resolved host source, whether the source declares a shebang, and whether
// to run it as root. The hook is executed directly in the VM, where the kernel
// applies the shebang; without one it falls back to /bin/sh.
type HookSpec struct {
	Target     string // absolute VM path to the provisioned script
	Source     string // resolved host source path
	HasShebang bool   // source begins with a #! line; without one the hook runs via /bin/sh
	Root       bool   // run as root; false runs as the sandbox user (dev)
}

// hasShebang reports whether the file at path begins with a `#!` marker, so the
// VM kernel resolves the script's interpreter. A missing file or a bare marker
// yields false.
func hasShebang(path string) bool {
	f, err := os.Open(path)
	// gomutants:disable-next-line BRANCH_IF reason="a nil *os.File reads as ErrInvalid, so the missing-file fall-through reaches the read guard below and returns false exactly like this branch"
	if err != nil {
		return false
	}
	defer f.Close()
	var line []byte
	buf := make([]byte, 1)
	for range 2 {
		// gomutants:disable-next-line BRANCH_IF reason="a skipped read appends the zero byte, so both the early return and the fall-through yield an all-zero line that is not #!"
		if _, err := f.Read(buf); err != nil {
			return false
		}
		line = append(line, buf[0])
	}
	return string(line) == "#!"
}

// BuildHooks returns the merged manifest's startup-hook entries (Hook ==
// "startup") whose host source exists, sorted by VM target. A hook whose host
// source is missing is skipped (its script will not have been provisioned). The
// reserved list holds home-relative VM paths the manifest must not target (e.g.,
// the agent's merged config path); when empty, no target is reserved.
func BuildHooks(userConfigDir, projectConfigDir, homeBase string, reserved []string) ([]HookSpec, error) {
	layers, _, err := LoadLayers([]string{userConfigDir, projectConfigDir})
	if err != nil {
		return nil, err
	}
	resolved := resolveLayerSources(layers)
	merged := Manifest{}
	for _, l := range resolved {
		maps.Copy(merged, l.Manifest)
	}
	var hooks []HookSpec
	for _, target := range slices.Sorted(maps.Keys(merged)) {
		e := merged[target]
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
			HookSpec{Target: vmPath, Source: e.Source, HasShebang: hasShebang(e.Source), Root: e.Root},
		)
	}
	sort.Slice(hooks, func(i, j int) bool { return hookSpecLess(hooks[i], hooks[j]) })
	return hooks, nil
}

// hookSpecLess orders startup hooks by VM target.
func hookSpecLess(a, b HookSpec) bool { return a.Target < b.Target }
