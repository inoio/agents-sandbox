package reprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/inoio/agents-sandbox/internal/agent"
	config "github.com/inoio/agents-sandbox/internal/configmerge"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"

	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
)

// evalProvisionRules is a seam over agent.EvalProvisionRules so tests can
// exercise the provisioning-error path. A direct call cannot fail because the
// copy callback only records files in memory.
var evalProvisionRules = agent.EvalProvisionRules

// VMHomeDir is the sandbox home mount point. It is fixed by the project VM
// layout regardless of the configured runtime user.
const VMHomeDir = mounts.VMHomeDir

// ConfigSource is the resolved launcher config-dir pair for an agent. Fallback
// is the legacy dir name actually in use, or "" when the primary dirs are used.
type ConfigSource struct {
	UserDir    string
	ProjectDir string
	Fallback   string
}

// ResolveConfigSource picks the agent's launcher config dirs. The primary dirs
// win when either exists in any scope; otherwise the first alias whose dir
// exists in either scope is used for both scopes. When nothing exists the
// primary dirs are returned and Fallback is empty.
func ResolveConfigSource(a agent.Agent) (ConfigSource, error) {
	userCands := cp.Get().UserAgentConfigDirCandidates(a)
	projectCands := cp.Get().ProjectAgentConfigDirCandidates(a)
	if len(userCands) == 0 || len(userCands) != len(projectCands) {
		return ConfigSource{}, fmt.Errorf(
			"agent %s resolved %d user / %d project config-dir candidates; expected a non-empty parallel pair",
			a.Name(), len(userCands), len(projectCands))
	}
	primaryExists, err := anyDirExists(userCands[0], projectCands[0])
	if err != nil {
		return ConfigSource{}, err
	}
	if primaryExists {
		return ConfigSource{UserDir: userCands[0], ProjectDir: projectCands[0], Fallback: ""}, nil
	}
	// The candidate lists are parallel by construction: both come from the
	// same ordered name list via agentConfigDirCandidates, so userCands[i] and
	// projectCands[i] name the same config dir in their respective scopes.
	for i := 1; i < len(userCands); i++ {
		exists, err := anyDirExists(userCands[i], projectCands[i])
		if err != nil {
			return ConfigSource{}, err
		}
		if exists {
			return ConfigSource{
				UserDir:    userCands[i],
				ProjectDir: projectCands[i],
				Fallback:   filepath.Base(userCands[i]),
			}, nil
		}
	}
	return ConfigSource{UserDir: userCands[0], ProjectDir: projectCands[0], Fallback: ""}, nil
}

// anyDirExists reports whether any path names an existing directory. Every
// candidate is tried, so an unreadable candidate in one scope does not hide a
// usable one in the other; a stat error other than "not exist" is remembered
// and surfaced only when no candidate exists, rather than being treated as
// absent or aborting the search.
func anyDirExists(dirs ...string) (bool, error) {
	var statErr error
	for _, d := range dirs {
		info, err := os.Stat(d)
		switch {
		case err == nil:
			if info.IsDir() {
				return true, nil
			}
		case errors.Is(err, fs.ErrNotExist):
			// absent in this scope; try the next
		default:
			if statErr == nil {
				statErr = fmt.Errorf("stat config dir %q: %w", d, err)
			}
		}
	}
	return false, statErr
}

// describeConfigScopes renders "user: <dir>, project: <dir>" for the given
// absolute scopes. When onlyExisting is set, scopes whose dir is absent are
// omitted rather than named as if read.
func describeConfigScopes(userDir, projectDir string, onlyExisting bool) string {
	parts := make([]string, 0, 2)
	if !onlyExisting || isDir(userDir) {
		parts = append(parts, fmt.Sprintf("user: %q", userDir))
	}
	if !onlyExisting || isDir(projectDir) {
		parts = append(parts, fmt.Sprintf("project: %q", projectDir))
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}

// SourceDirs returns the user and project config dirs actually read for the
// resolved source. They come straight from the source so display can never
// diverge from what was read; the seam builds candidates absolute, so both are
// absolute.
func SourceDirs(src ConfigSource) (string, string) {
	return src.UserDir, src.ProjectDir
}

// PrimaryDirs returns the absolute primary (non-fallback) user and project
// config dirs an agent migrates to. It reads the candidate lists, which never
// create dirs: naming the migration target must not manufacture the very dir
// whose existence switches the agent off the legacy fallback.
func PrimaryDirs(a agent.Agent) (string, string) {
	user := cp.Get().UserAgentConfigDirCandidates(a)
	project := cp.Get().ProjectAgentConfigDirCandidates(a)
	if len(user) == 0 || len(project) == 0 {
		return "", ""
	}
	return user[0], project[0]
}

// isDir reports whether path names an existing directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ConfigFiles holds the merged agent config, the set of home files to
// provision into the VM, the default drop-in copy from the host, and the VM
// paths to remove so stale host config cannot shadow the merged config.
type ConfigFiles struct {
	HasSnippets bool                   // whether any agent snippet existed
	Merged      []byte                 // merged agent config content
	MergedPath  string                 // VM path of the merged config ("" when no snippets)
	Sources     []string               // host snippet paths merged into Merged
	Source      ConfigSource           // resolved launcher config-dir pair (with legacy fallback marker)
	HomeFiles   map[string][]byte      // VM absolute path -> content (from the home: key)
	Provisioned map[string][]byte      // VM absolute path -> content (drop-in copy)
	Mirror      map[string][]byte      // VM absolute path -> content (verbatim <agent> mirror)
	Modes       map[string]os.FileMode // VM absolute path -> ordinary file permission bits
	Remove      []string               // VM absolute paths to delete before writing
	Hooks       []homeconfig.HookSpec  // startup hooks to run at setUpSandbox
	Keys        []string               // merged config path first (when snippets exist), then the remaining VM paths sorted
}

// LoadConfigFiles builds the desired VM state for the given agent using the
// host home from the config-paths seam. The agent determines its own config
// directories.
func LoadConfigFiles(a agent.Agent, ui termio.UI, provisionHostConfig bool) (*ConfigFiles, error) {
	hostHome, _ := cp.Get().UserHomeDir()
	return LoadConfigFilesForHost(a, hostHome, VMHomeDir, ui, provisionHostConfig)
}

// LoadConfigFilesForHost builds the desired VM state for the given agent with
// explicit host and VM home directories: the merged agent config, the home
// files (from the home: key of the config files), and the default drop-in copy of the
// agent's host config (per its provision rules, unless host config provisioning
// is disabled). It warns about any home source that does not exist on the
// host and about malformed provision rules. Home files and the merged config
// override provisioned defaults for the same VM path. The agent's merged-config
// path is reserved: a home target colliding with it is rejected.
func LoadConfigFilesForHost(
	a agent.Agent,
	hostHome, vmHome string,
	ui termio.UI,
	provisionHostConfig bool,
) (*ConfigFiles, error) {
	src, err := ResolveConfigSource(a)
	if err != nil {
		return nil, err
	}
	mergedPath, mergedConfig, sources, hasSnippets, err := buildMergedConfig(a, vmHome, src)
	if err != nil {
		return nil, err
	}
	reserved, err := reservedHomeTargets(mergedPath, vmHome)
	if err != nil {
		return nil, err
	}
	hooks, err := loadHooks(a, vmHome, reserved)
	if err != nil {
		return nil, err
	}
	homeFiles, homeModes, err := loadHomeFiles(a, vmHome, reserved, ui)
	if err != nil {
		return nil, err
	}
	provisioned, modes, err := loadProvisioned(a, hostHome, vmHome, ui, provisionHostConfig)
	if err != nil {
		return nil, err
	}

	// Precedence: home files always override provisioned defaults, and the
	// merged agent config overrides the provisioned config when snippets exist
	// (no merged config means the drop-in default is provisioned).
	for p := range homeFiles {
		delete(provisioned, p)
		delete(modes, p)
	}
	if hasSnippets {
		delete(provisioned, mergedPath)
		delete(modes, mergedPath)
	}
	mirrorEntries, err := buildMirror(a, vmHome, src)
	if err != nil {
		return nil, fmt.Errorf("build config mirror: %w", err)
	}
	mirror, mirrorModes := mirrorMaps(mirrorEntries)
	// Precedence: home overrides the mirror, the merged config overrides
	// the mirror, and the mirror overrides the drop-in copy for the same path.
	maps.Copy(modes, mirrorModes)
	applyMirrorPrecedence(mirror, provisioned, homeFiles, mergedPath, hasSnippets, modes)
	maps.Copy(modes, homeModes)
	ensureHookExecutable(modes, hooks)
	// Remove stale host config so it cannot shadow the merged config: when
	// snippets exist the merged config must be the only config, and when host
	// config provisioning is disabled no host file may remain.
	remove := configFileFamilyPaths(mergedPath, configFamilyNames(a))
	if !provisionHostConfig {
		remove = append(remove, provisionDestinations(a, hostHome, vmHome)...)
	}
	keys := configKeys(mergedPath, hasSnippets, homeFiles, mirror, provisioned)
	if src.Fallback != "" {
		legacyUser, legacyProject := SourceDirs(src)
		primaryUser, primaryProject := PrimaryDirs(a)
		ui.Warnf(
			"agent %s is reading launcher config from the legacy shared %q dir (%s), which agent opencode also uses; "+
				"a %q dir in either the user or project scope switches both scopes, so copy (do not move) everything you need in one go into the %q namespace (%s): "+
				"a partial copy will not fall back for the rest. The guest ~/.config/opencode path is unchanged.",
			a.Name(),
			src.Fallback,
			describeConfigScopes(legacyUser, legacyProject, true),
			a.ConfigDirName(),
			a.ConfigDirName(),
			describeConfigScopes(primaryUser, primaryProject, false),
		)
	}
	return &ConfigFiles{
		HasSnippets: hasSnippets,
		Merged:      mergedConfig,
		MergedPath:  mergedPath,
		Sources:     sources,
		Source:      src,
		HomeFiles:   homeFiles,
		Provisioned: provisioned,
		Mirror:      mirror,
		Modes:       modes,
		Remove:      remove,
		Hooks:       hooks,
		Keys:        keys,
	}, nil
}

// reservedHomeTargets returns the VM-relative merged-config path, if any, that
// home targets may not collide with.
func reservedHomeTargets(mergedPath, vmHome string) ([]string, error) {
	if mergedPath == "" {
		return nil, nil
	}
	rel, err := filepath.Rel(vmHome, mergedPath)
	if err != nil {
		return nil, fmt.Errorf("derive reserved home target: %w", err)
	}
	return []string{rel}, nil
}

// loadHomeFiles builds the home files and their modes, warning about any home
// source missing on the host.
func loadHomeFiles(
	a agent.Agent,
	vmHome string,
	reserved []string,
	ui termio.UI,
) (map[string][]byte, map[string]os.FileMode, error) {
	userConfigDir := filepath.Dir(cp.Get().UserAgentConfigDir(a))
	homeSources, missing, _, err := homeconfig.BuildHomeFilesWithModes(
		userConfigDir, // user config lives one level above the agent subdir
		cp.Get().ProjectConfigDir(),
		vmHome,
		reserved,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("build home files: %w", err)
	}
	for _, src := range missing {
		ui.Warnf("home source %q does not exist on the host; skipping", src)
	}
	homeFiles := make(map[string][]byte, len(homeSources))
	homeModes := make(map[string]os.FileMode, len(homeSources))
	for path, source := range homeSources {
		homeFiles[path] = source.Data
		homeModes[path] = source.Mode
	}
	return homeFiles, homeModes, nil
}

// loadHooks builds the merged startup hooks for the agent.
func loadHooks(a agent.Agent, vmHome string, reserved []string) ([]homeconfig.HookSpec, error) {
	userConfigDir := filepath.Dir(cp.Get().UserAgentConfigDir(a))
	hooks, err := homeconfig.BuildHooks(
		userConfigDir, // user config lives one level above the agent subdir
		cp.Get().ProjectConfigDir(),
		vmHome,
		reserved,
	)
	if err != nil {
		return nil, fmt.Errorf("build hooks: %w", err)
	}
	return hooks, nil
}

// ensureHookExecutable adds the owner-execute bit to every startup-hook target
// so the VM can execute it directly (the kernel applies the script's shebang)
// even when the host source is not marked executable.
func ensureHookExecutable(modes map[string]os.FileMode, hooks []homeconfig.HookSpec) {
	for _, h := range hooks {
		mode := modes[h.Target]
		if mode == 0 {
			mode = 0o600
		}
		modes[h.Target] = mode | 0o100
	}
}

// loadProvisioned evaluates the agent's provision rules into the drop-in copy
// maps, unless host config provisioning is disabled.
func loadProvisioned(
	a agent.Agent,
	hostHome, vmHome string,
	ui termio.UI,
	enabled bool,
) (map[string][]byte, map[string]os.FileMode, error) {
	provisioned := make(map[string][]byte)
	modes := make(map[string]os.FileMode)
	if !enabled {
		return provisioned, modes, nil
	}
	p, ok := agent.AsProvisioner(a)
	if !ok {
		return provisioned, modes, nil
	}
	for _, w := range agent.ValidateProvisionRules(p.ProvisionRules()) {
		ui.Warnf("provision rule: %s", w)
	}
	onCopy := func(dst string, data []byte, mode os.FileMode) error {
		provisioned[dst] = data
		modes[dst] = mode
		return nil
	}
	if _, err := evalProvisionRules(p.ProvisionRules(), hostHome, vmHome, onCopy); err != nil {
		return nil, nil, fmt.Errorf("eval provision rules: %w", err)
	}
	return provisioned, modes, nil
}

// applyMirrorPrecedence prunes the mirror and drop-in copy maps so the mirror
// overrides the drop-in copy for the same VM path, while home files and the
// merged config override the mirror.
func applyMirrorPrecedence(
	mirror, provisioned, homeFiles map[string][]byte,
	mergedPath string,
	hasSnippets bool,
	modes map[string]os.FileMode,
) {
	for p := range mirror {
		delete(provisioned, p)
	}
	for p := range homeFiles {
		delete(mirror, p)
		delete(modes, p)
	}
	if hasSnippets {
		delete(mirror, mergedPath)
		delete(modes, mergedPath)
	}
}

// configKeys returns the VM paths to provision and compare: the merged config
// (when snippets exist) first, then the home/mirror/drop-in paths sorted. It is
// the sole ordering authority for ConfigFiles.Keys — callers must not re-sort,
// or the merged config would no longer lead. The map-backed inputs are
// unordered, so the remainder must be sorted to keep Keys stable across runs.
func configKeys(mergedPath string, hasSnippets bool, homeFiles, mirror, provisioned map[string][]byte) []string {
	rest := make([]string, 0, len(homeFiles)+len(provisioned)+len(mirror))
	for p := range homeFiles {
		rest = append(rest, p)
	}
	for p := range mirror {
		rest = append(rest, p)
	}
	for p := range provisioned {
		rest = append(rest, p)
	}
	sort.Strings(rest)
	if hasSnippets {
		return append([]string{mergedPath}, rest...)
	}
	return rest
}

// configFamilyNames returns the config filenames the agent reads from its VM
// config directory that the merged config supersedes, or nil for an agent
// without a ConfigMerger.
func configFamilyNames(a agent.Agent) []string {
	if cm, ok := agent.AsConfigMerger(a); ok {
		return cm.ConfigFileNames()
	}
	return nil
}

// configFileFamilyPaths returns the VM paths of the config files in the merged
// config's directory (the given names) that would otherwise merge into (or
// shadow) it.
func configFileFamilyPaths(mergedPath string, names []string) []string {
	dir := filepath.Dir(mergedPath)
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths
}

// buildMergedConfig merges the agent's snippet files into a single config and
// returns its destination VM path, content, the snippet source paths, whether
// any snippet existed, and an error. Every agent in the registry implements
// ConfigMerger, so a non-merger agent produces no merged config.
func buildMergedConfig(a agent.Agent, vmHome string, src ConfigSource) (string, []byte, []string, bool, error) {
	if cm, ok := agent.AsConfigMerger(a); ok {
		target := cm.VMConfigPath(vmHome)
		merged, sources, hasSnippets, err := config.BuildMerged(
			target,
			cm.SnippetPattern(),
			src.UserDir,
			src.ProjectDir,
		)
		if err != nil {
			return "", nil, nil, false, fmt.Errorf("merge agent config: %w", err)
		}
		return target, merged, sources, hasSnippets, nil
	}
	return "", nil, nil, false, nil
}

// buildMirror returns the verbatim mirror files for agent a: every file in the
// user and project <agent> config dirs that is neither a top-level snippet
// match nor a top-level config-family name, keyed by VM path under the agent's
// VM config directory. Agents without a ConfigMerger have no mirror.
func buildMirror(a agent.Agent, vmHome string, src ConfigSource) (map[string]config.MirrorEntry, error) {
	cm, ok := agent.AsConfigMerger(a)
	if !ok {
		return map[string]config.MirrorEntry{}, nil
	}
	entries, err := config.ScanMirror(
		cm.SnippetPattern(),
		cm.ConfigFileNames(),
		src.UserDir,
		src.ProjectDir,
		filepath.Dir(cm.VMConfigPath(vmHome)),
	)
	if err != nil {
		return nil, err
	}
	mirror := make(map[string]config.MirrorEntry, len(entries))
	for _, e := range entries {
		mirror[e.VMPath] = e
	}
	return mirror, nil
}

func mirrorMaps(entries map[string]config.MirrorEntry) (map[string][]byte, map[string]os.FileMode) {
	mirror := make(map[string][]byte, len(entries))
	modes := make(map[string]os.FileMode, len(entries))
	for path, entry := range entries {
		mirror[path] = entry.Data
		modes[path] = entry.Mode
	}
	return mirror, modes
}

// provisionDestinations returns the VM paths the agent's provision rules would
// copy for the given host, without copying anything. It mirrors the drop-in
// copy's destinations so they can be removed when host provisioning is disabled.
func provisionDestinations(a agent.Agent, hostHome, vmHome string) []string {
	p, ok := agent.AsProvisioner(a)
	if !ok {
		return nil
	}
	var dsts []string
	_, _ = agent.EvalProvisionRules(
		p.ProvisionRules(),
		hostHome,
		vmHome,
		func(dst string, _ []byte, _ os.FileMode) error {
			dsts = append(dsts, dst)
			return nil
		},
	)
	return dsts
}

// HostFile is one host file the drop-in provisioning would copy.
type HostFile struct {
	HostPath string
	VMPath   string
	Merged   bool
}

// MirrorFile is one host file the verbatim config-dir mirror would provision.
type MirrorFile struct {
	HostPath string
	VMPath   string
}

// Describe returns the merged config, the snippet sources, the host drop-in
// files, the verbatim mirror files (host path → VM path), and the resolved
// config source for agent a, without touching a VM.
func Describe(
	a agent.Agent,
	hostHome, vmHome string,
	ui termio.UI,
	provisionHostConfig bool,
) ([]byte, []string, []HostFile, []MirrorFile, ConfigSource, error) {
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, ui, provisionHostConfig)
	if err != nil {
		return nil, nil, nil, nil, ConfigSource{}, err
	}
	hostFiles := hostFilesFromProvisioner(a, hostHome, vmHome, cf)
	return cf.Merged, cf.Sources, hostFiles, mirrorFilesFromConfig(a, vmHome, cf.Source), cf.Source, nil
}

// mirrorFilesFromConfig returns the mirror display records for agent a.
func mirrorFilesFromConfig(a agent.Agent, vmHome string, src ConfigSource) []MirrorFile {
	cm, ok := agent.AsConfigMerger(a)
	if !ok {
		return nil
	}
	entries, err := config.ScanMirror(
		cm.SnippetPattern(),
		cm.ConfigFileNames(),
		src.UserDir,
		src.ProjectDir,
		filepath.Dir(cm.VMConfigPath(vmHome)),
	)
	if err != nil {
		return nil
	}
	files := make([]MirrorFile, 0, len(entries))
	for _, e := range entries {
		files = append(files, MirrorFile{HostPath: e.HostPath, VMPath: e.VMPath})
	}
	return files
}

// hostFilesFromProvisioner walks the agent's provision rules against hostHome
// and records each host file it would copy, marking whether the VM destination
// is merged into the single config rather than copied as a drop-in. It never
// modifies any state.
func hostFilesFromProvisioner(a agent.Agent, hostHome, vmHome string, cf *ConfigFiles) []HostFile {
	p, ok := agent.AsProvisioner(a)
	if !ok {
		return nil
	}
	mergedPath := cf.MergedPath
	merged := make(map[string]struct{})
	if cf.HasSnippets {
		for _, path := range configFileFamilyPaths(mergedPath, configFamilyNames(a)) {
			merged[path] = struct{}{}
		}
	}
	var files []HostFile
	_, _ = agent.EvalProvisionRules(
		p.ProvisionRules(),
		hostHome,
		vmHome,
		func(dst string, _ []byte, _ os.FileMode) error {
			_, isMerged := merged[dst]
			files = append(files, HostFile{
				HostPath: hostPathForDst(dst, hostHome, vmHome),
				VMPath:   dst,
				Merged:   isMerged,
			})
			return nil
		},
	)
	return files
}

// hostPathForDst reverses a VM destination path to the corresponding host path,
// since provision rules copy host → VM at the same relative path.
func hostPathForDst(dst, hostHome, vmHome string) string {
	rel := strings.TrimPrefix(dst, vmHome)
	return filepath.Join(hostHome, strings.TrimPrefix(rel, string(filepath.Separator)))
}

// ReadVMConfig reads the given absolute VM paths, returning a map of path to
// content for files that exist.
func ReadVMConfig(ctx context.Context, sb msb.Sandbox, paths []string, ui termio.UI) map[string][]byte {
	result := make(map[string][]byte)
	for _, p := range paths {
		data, err := sb.FS().Read(ctx, p)
		if err != nil {
			ui.Verbosef("read %s failed: %v", p, err)
			continue
		}
		result[p] = data
		ui.Verbosef("OK: %s (%d bytes)", p, len(data))
	}
	return result
}

// AgentConfigEqual reports whether the merged agent config and the verbatim
// mirror files match the VM state. The merged config is compared semantically
// and mirror files byte-for-byte, so a change to either triggers a daemon
// restart. Home and drop-in files are intentionally ignored: they are
// provisioned on every startup and do not require a daemon restart.
func AgentConfigEqual(cf *ConfigFiles, vmData map[string][]byte) bool {
	if cf.HasSnippets {
		vm, ok := vmData[cf.MergedPath]
		if !ok {
			return false
		}
		if !configEqual(cf.MergedPath, cf.Merged, vm) {
			return false
		}
	}
	for path, want := range cf.Mirror {
		got, ok := vmData[path]
		if !ok || !bytes.Equal(want, got) {
			return false
		}
	}
	return true
}

// configEqual reports whether two config documents are semantically equal,
// parsing them the way the agent reads the file at targetPath: YAML for a YAML
// config path, JSON otherwise. Comparing a YAML merged config through the JSON
// parser would always report a change, since the parser rejects it — and the
// caller treats "changed" as "restart the agent", which Hermes has no server
// for.
func configEqual(targetPath string, a, b []byte) bool {
	va, err1 := parseConfig(targetPath, a)
	vb, err2 := parseConfig(targetPath, b)
	if err1 != nil || err2 != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

// parseConfig unmarshals a config document for deep equality comparison, using
// the format its destination file implies.
func parseConfig(targetPath string, data []byte) (any, error) {
	switch strings.ToLower(filepath.Ext(targetPath)) {
	case ".yaml", ".yml":
		var v any
		if err := yaml.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return v, nil
	default:
		return parseJSON(data)
	}
}

// parseJSON unmarshals data into map[string]any for deep equality comparison.
func parseJSON(data []byte) (any, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}
