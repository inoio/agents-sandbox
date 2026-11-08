// Package projecttrust decides whether the launcher may act on a checkout's
// project config layer (.agents-sandbox): it computes the layer's projection,
// compares it with the one the user approved, and asks for approval of a
// change (see specs/configuration/project-config-trust.md).
package projecttrust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/inoio/agents-sandbox/internal/configpaths"
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
	// KindHomeManifest entries are the launcher config file's home: entries as
	// home provisioning parses them, keys case-sensitive.
	KindHomeManifest InputKind = "home"
	// KindEnv entries are the variables the env file sets.
	KindEnv InputKind = "env"
	// KindSecrets entries are each secret's substitution scope, never its value.
	KindSecrets InputKind = "secrets"
	// KindFile entries are the file's content lines.
	KindFile InputKind = "file"
)

// maxContentBytes bounds a file input shown line by line and the memory read
// for it; a larger file is represented by its digest alone.
const maxContentBytes = 64 * 1024

// errNotRegularFile rejects a project input that is no regular file (a device
// or FIFO a symlink points at), which could not be read to its end.
var errNotRegularFile = errors.New("not a regular file")

// openInput opens a project input file for reading.
var openInput = func(path string) (fs.File, error) { return os.Open(path) }

// noFinalNewline marks a file whose last line has no newline, so a content
// change in the final byte is not lost.
const noFinalNewline = `\ No newline at end of file`

// Input is one consumed project input: its source path, its kind, and its
// canonical effect as display entries.
//
//exhaustruct:ignore
type Input struct {
	Path    string    `json:"path"    yaml:"path"`
	Kind    InputKind `json:"kind"    yaml:"kind"`
	Entries []string  `json:"entries" yaml:"entries"`
	// Digest is a file input's digest of every byte, so content its display
	// entries do not tell apart (a final newline, a line equal to a marker)
	// still changes the projection. Empty for parsed inputs.
	Digest string `json:"digest,omitempty" yaml:"digest,omitempty"`
}

// key identifies an input: one path can be read in two ways (an env file that
// is also a home: source), and each way is its own input.
func (i Input) key() string {
	return i.Path + "\x00" + string(i.Kind)
}

// Projection is the project layer's own declared contribution: every input it
// contributes.
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
	slices.SortFunc(inputs, func(a, b Input) int { return strings.Compare(a.key(), b.key()) })
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
// projection. An unparsable launcher config or a project file that cannot be
// read is an error, so an input the gate could not see is never approved.
func Compute(sources Sources) (Projection, error) {
	projectDir := sources.ProjectConfigDir
	if projectDir == "" {
		return Projection{}, nil
	}
	collector := inputCollector{byKey: map[string]Input{}}

	configPath, settings, err := viperconfig.LayerSettings(projectDir)
	if err != nil {
		return Projection{}, err
	}
	manifest, _, err := homeconfig.ReadHomeFromConfigDir(projectDir)
	if err != nil {
		return Projection{}, err
	}
	if configPath != "" {
		collector.add(Input{Path: configPath, Kind: KindLauncherConfig, Entries: settingEntries(settings)})
		collector.add(Input{Path: configPath, Kind: KindHomeManifest, Entries: manifestEntries(manifest)})
	}

	scope := configpaths.ConfigScope{Kind: configpaths.ScopeProject, Dir: projectDir}
	secretFiles := []string{scope.EnvSecretFile(), scope.EnvSecretYAMLFile()}
	collector.addEnvFile(scope.EnvFile())
	collector.addSecretFile(scope.EnvSecretFile(), envsecret.ParseSecretSpecLegacy)
	collector.addSecretFile(scope.EnvSecretYAMLFile(), envsecret.ParseSecretSpecYAML)
	if err := collector.addFile(filepath.Join(projectDir, configpaths.DockerFileName)); err != nil {
		return Projection{}, err
	}
	for _, name := range sources.AgentConfigDirNames {
		if err := collector.addTree(scope.AgentConfigDir(name)); err != nil {
			return Projection{}, err
		}
	}
	for _, target := range slices.Sorted(maps.Keys(manifest)) {
		source := homeconfig.ResolveManifestSource(target, manifest[target].Source, projectDir)
		if !guestWritable(source, projectDir, sources.CheckoutRoot) {
			continue
		}
		collector.addHomeSource(source, slices.Contains(secretFiles, source))
	}
	return collector.projection(), nil
}

// quietLogger drops the warnings the env and secret parsers emit while the
// projection reads a file: the run's own load of the same file reports them.
func quietLogger() termio.Logger {
	return termio.New(nil, io.Discard, io.Discard, false, termio.LevelError, true, false)
}

// inputCollector gathers inputs by path and kind, so a file reached twice the
// same way (an agent config file that is also a home: source) is one input.
type inputCollector struct {
	byKey map[string]Input
}

func (c inputCollector) add(input Input) {
	c.byKey[input.key()] = input
}

func (c inputCollector) projection() Projection {
	var inputs []Input
	for _, key := range slices.Sorted(maps.Keys(c.byKey)) {
		inputs = append(inputs, c.byKey[key])
	}
	return Projection{Inputs: inputs}
}

func (c inputCollector) addEnvFile(path string) {
	if !isRegularFile(path) {
		return
	}
	vars := envsecret.BuildEnvMap(path, quietLogger())
	var entries []string
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
	var entries []string
	for _, name := range slices.Sorted(maps.Keys(specs)) {
		entries = append(entries, describeSecretSpec(name, specs[name]))
	}
	c.add(Input{Path: path, Kind: KindSecrets, Entries: entries})
}

// addFile adds a file's content. A missing file adds nothing; any other read
// failure, a file that is not regular included, is an error.
func (c inputCollector) addFile(path string) error {
	input, err := readFileInput(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	c.add(input)
	return nil
}

// addTree adds every file under root. A missing root or an entry the walk
// cannot read adds nothing, as provisioning, which walks the same way, does not
// copy it either.
func (c inputCollector) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil //nolint:nilerr // an entry the walk cannot read is not provisioned, so it is skipped
		}
		return c.addFile(path)
	})
}

// addHomeSource adds a guest-writable home: source by content, or by digest
// alone for a secret file, so its values stay out of the projection. Like home
// provisioning, which copies single files and skips a source it cannot read, it
// adds nothing for a source that is missing, a directory, or unreadable.
func (c inputCollector) addHomeSource(source string, secret bool) {
	input, err := readFileInput(source)
	if err != nil {
		return
	}
	if secret {
		input.Entries = []string{input.Digest}
	}
	c.add(input)
}

// readFileInput reads a regular file with bounded memory: the digest of every
// byte, and its lines when it is UTF-8 text no larger than maxContentBytes.
func readFileInput(path string) (Input, error) {
	file, err := openInput(path)
	if err != nil {
		return Input{}, fmt.Errorf("read project input %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return Input{}, fmt.Errorf("read project input %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return Input{}, fmt.Errorf("read project input %s: %w", path, errNotRegularFile)
	}
	hash := sha256.New()
	head := &boundedBuffer{data: nil, limit: maxContentBytes, overflowed: false}
	if _, err := io.Copy(io.MultiWriter(hash, head), file); err != nil {
		return Input{}, fmt.Errorf("read project input %s: %w", path, err)
	}
	digest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	entries := []string{digest}
	if !head.overflowed && utf8.Valid(head.data) {
		entries = contentLines(head.data)
	}
	return Input{Path: path, Kind: KindFile, Entries: entries, Digest: digest}, nil
}

// boundedBuffer keeps the first limit bytes written to it and notes whether
// more followed.
type boundedBuffer struct {
	data       []byte
	limit      int
	overflowed bool
}

// Write keeps what fits and reports every byte written, so the hash beside it
// in an io.MultiWriter sees the whole file.
func (b *boundedBuffer) Write(p []byte) (int, error) {
	kept := p
	if room := b.limit - len(b.data); len(p) > room {
		b.overflowed = true
		kept = p[:room]
	}
	b.data = append(b.data, kept...)
	return len(p), nil
}

// guestWritable reports whether path lies inside one of the guest-writable
// roots, comparing each as spelled and with symlinks resolved.
func guestWritable(path string, roots ...string) bool {
	for _, root := range roots {
		for _, rootForm := range withResolved(root) {
			for _, pathForm := range withResolved(path) {
				if within(rootForm, pathForm) {
					return true
				}
			}
		}
	}
	return false
}

// withResolved returns path and, when it resolves, its symlink-free form.
func withResolved(path string) []string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return []string{path}
	}
	return []string{path, resolved}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// contentLines returns a file's lines, marking a missing final newline.
func contentLines(data []byte) []string {
	text := string(data)
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if !strings.HasSuffix(text, "\n") {
		lines = append(lines, noFinalNewline)
	}
	return lines
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
	if nested, ok := value.(map[string]any); ok {
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

// manifestEntries renders the parsed home: manifest, one sorted entry per
// target with the source, hook, and root flag provisioning acts on.
func manifestEntries(manifest homeconfig.Manifest) []string {
	var entries []string
	for _, target := range slices.Sorted(maps.Keys(manifest)) {
		entry := manifest[target]
		entries = append(entries, fmt.Sprintf("%s ← %q hook=%q root=%t", target, entry.Source, entry.Hook, entry.Root))
	}
	return entries
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
