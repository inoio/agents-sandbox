// Package projecttrust decides whether the launcher may act on a checkout's
// project config layer (.agents-sandbox): it computes the layer's projection,
// compares it with the one the user approved, and asks for approval of a
// change (see specs/configuration/project-config-trust.md).
package projecttrust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/filewalk"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// InputKind names how an input's effect is represented.
type InputKind string

// The input kinds of a projection.
const (
	// KindLauncherConfig entries are the keys the launcher config file sets.
	KindLauncherConfig InputKind = "launcher config"
	// KindEnv entries are the variables the env file sets.
	KindEnv InputKind = "env"
	// KindSecrets entries are each secret's substitution scope, never its value.
	KindSecrets InputKind = "secrets"
	// KindFile entries are the file's content lines.
	KindFile InputKind = "file"
)

// maxContentBytes bounds a file input shown line by line; a larger file is
// represented by its digest.
const maxContentBytes = 64 * 1024

// Input is one consumed project input: its source path, its kind, and its
// canonical effect as sorted or ordered display entries.
type Input struct {
	Path    string    `json:"path"    yaml:"path"`
	Kind    InputKind `json:"kind"    yaml:"kind"`
	Entries []string  `json:"entries" yaml:"entries"`
}

// Projection is the project layer's own declared contribution: every input it
// contributes, sorted by path.
type Projection struct {
	Inputs []Input
}

// Empty reports whether the project layer contributes nothing the launcher
// consumes.
func (p Projection) Empty() bool {
	return len(p.Inputs) == 0
}

// Digest is the projection's content digest, independent of input order.
func (p Projection) Digest() string {
	inputs := slices.Clone(p.Inputs)
	for i := range inputs {
		// A stored approval drops empty entries, so empty and absent must hash alike.
		if inputs[i].Entries == nil {
			inputs[i].Entries = []string{}
		}
	}
	slices.SortFunc(inputs, func(a, b Input) int { return strings.Compare(a.Path, b.Path) })
	// Marshalling strings, a string type, and string slices cannot fail.
	data, _ := json.Marshal(inputs)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Sources names where the project layer's inputs live.
type Sources struct {
	// ProjectConfigDir is the absolute project config dir (.agents-sandbox);
	// empty means there is no project layer.
	ProjectConfigDir string
	// CheckoutRoot is the checkout bound as /workspace: a home: source inside
	// it is guest-writable and gated by content.
	CheckoutRoot string
	// AgentConfigDirNames are the agent config dir names any registered agent
	// reads from the project config dir.
	AgentConfigDirNames []string
}

// Compute reads every input the launcher consumes from the project layer and
// returns its projection. A missing or empty project config dir yields an empty
// projection; an unparsable launcher config is an error.
func Compute(sources Sources) (Projection, error) {
	projectDir := sources.ProjectConfigDir
	if projectDir == "" {
		return Projection{}, nil
	}
	collector := inputCollector{byPath: map[string]Input{}}

	configPath, settings, err := viperconfig.LayerSettings(projectDir)
	if err != nil {
		return Projection{}, err
	}
	if configPath != "" {
		collector.add(Input{Path: configPath, Kind: KindLauncherConfig, Entries: settingEntries(settings)})
	}

	scope := configpaths.ConfigScope{Kind: configpaths.ScopeProject, Dir: projectDir}
	collector.addEnvFile(scope.EnvFile())
	collector.addSecretFile(scope.EnvSecretFile(), envsecret.ParseSecretSpecLegacy)
	collector.addSecretFile(scope.EnvSecretYAMLFile(), envsecret.ParseSecretSpecYAML)
	collector.addFile(filepath.Join(projectDir, configpaths.DockerFileName))
	for _, name := range sources.AgentConfigDirNames {
		collector.addTree(scope.AgentConfigDir(name))
	}

	if err := collector.addHomeSources(projectDir, sources.CheckoutRoot); err != nil {
		return Projection{}, err
	}
	return collector.projection(), nil
}

// quietLogger drops the warnings the env and secret parsers emit while the
// projection reads a file: the run's own load of the same file reports them.
func quietLogger() termio.Logger {
	return termio.New(nil, io.Discard, io.Discard, false, termio.LevelError, true, false)
}

// inputCollector gathers inputs keyed by path, so a file reached twice (an agent
// config file that is also a home: source) is one input.
type inputCollector struct {
	byPath map[string]Input
}

func (c inputCollector) add(input Input) {
	if _, seen := c.byPath[input.Path]; !seen {
		c.byPath[input.Path] = input
	}
}

func (c inputCollector) projection() Projection {
	paths := slices.Sorted(maps.Keys(c.byPath))
	inputs := make([]Input, 0, len(paths))
	for _, path := range paths {
		inputs = append(inputs, c.byPath[path])
	}
	return Projection{Inputs: inputs}
}

func (c inputCollector) addEnvFile(path string) {
	if !isRegularFile(path) {
		return
	}
	vars := envsecret.BuildEnvMap(path, quietLogger())
	entries := make([]string, 0, len(vars))
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		entries = append(entries, name+"="+vars[name])
	}
	c.add(Input{Path: path, Kind: KindEnv, Entries: entries})
}

func (c inputCollector) addSecretFile(
	path string,
	parse func(string, termio.Logger) map[string]envsecret.SecretSpec,
) {
	if !isRegularFile(path) {
		return
	}
	specs := parse(path, quietLogger())
	entries := make([]string, 0, len(specs))
	for _, name := range slices.Sorted(maps.Keys(specs)) {
		entries = append(entries, describeSecretSpec(name, specs[name]))
	}
	c.add(Input{Path: path, Kind: KindSecrets, Entries: entries})
}

// addFile adds a regular file's content; a missing file adds nothing.
func (c inputCollector) addFile(path string) {
	data, err := configread.ReadFile(path)
	if err != nil {
		return
	}
	c.add(Input{Path: path, Kind: KindFile, Entries: contentEntries(data)})
}

// addTree adds every regular file under root.
func (c inputCollector) addTree(root string) {
	_ = filewalk.Walk(root, func(string, bool) bool { return true }, func(entry filewalk.Entry) error {
		c.add(Input{Path: entry.Path, Kind: KindFile, Entries: contentEntries(entry.Data)})
		return nil
	})
}

// addHomeSources adds the content of every project home: source the guest can
// write: one whose resolved path, or its symlink target, lies in the project
// config dir or the checkout.
func (c inputCollector) addHomeSources(projectDir, checkoutRoot string) error {
	manifest, _, err := homeconfig.ReadHomeFromConfigDir(projectDir)
	if err != nil {
		return err
	}
	for _, target := range slices.Sorted(maps.Keys(manifest)) {
		source := homeconfig.ResolveManifestSource(target, manifest[target].Source, projectDir)
		if !guestWritable(source, projectDir, checkoutRoot) {
			continue
		}
		info, statErr := os.Stat(source)
		switch {
		case statErr != nil:
			continue
		case info.IsDir():
			c.addTree(source)
		default:
			c.addFile(source)
		}
	}
	return nil
}

// guestWritable reports whether path, as spelled or after resolving symlinks,
// lies inside one of the guest-writable roots.
func guestWritable(path string, roots ...string) bool {
	candidates := []string{path}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		candidates = append(candidates, resolved)
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			resolvedRoot = root
		}
		for _, candidate := range candidates {
			if within(root, candidate) || within(resolvedRoot, candidate) {
				return true
			}
		}
	}
	return false
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// contentEntries returns a file's lines, or a single digest entry for a file
// too large or not UTF-8 text to show line by line.
func contentEntries(data []byte) []string {
	if len(data) > maxContentBytes || !utf8.Valid(data) {
		sum := sha256.Sum256(data)
		return []string{"sha256:" + hex.EncodeToString(sum[:])}
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// settingEntries flattens the launcher config settings into sorted
// "key.path: <JSON value>" entries.
func settingEntries(settings map[string]any) []string {
	var entries []string
	appendSettingEntries("", settings, &entries)
	slices.Sort(entries)
	return entries
}

func appendSettingEntries(prefix string, value any, entries *[]string) {
	if nested, ok := value.(map[string]any); ok && len(nested) > 0 {
		for key, child := range nested {
			appendSettingEntries(joinKey(prefix, key), child, entries)
		}
		return
	}
	// Parsed config values are strings, numbers, booleans, lists, and maps, so
	// they always encode.
	encoded, _ := json.Marshal(value)
	*entries = append(*entries, prefix+": "+string(encoded))
}

func joinKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// describeSecretSpec renders a secret's substitution scope without its value.
func describeSecretSpec(name string, spec envsecret.SecretSpec) string {
	var parts []string
	if spec.AllowAnyHostDangerous {
		parts = append(parts, "any host (dangerous)")
	}
	hosts := slices.Clone(spec.Hosts)
	if spec.Host != "" {
		hosts = append(hosts, spec.Host)
	}
	if len(hosts) > 0 {
		slices.Sort(hosts)
		parts = append(parts, "hosts "+strings.Join(slices.Compact(hosts), ", "))
	}
	if len(spec.HeaderFields) > 0 {
		fields := slices.Clone(spec.HeaderFields)
		slices.Sort(fields)
		parts = append(parts, "header fields "+strings.Join(fields, ", "))
	}
	if len(parts) == 0 {
		return name
	}
	return name + ": " + strings.Join(parts, "; ")
}
