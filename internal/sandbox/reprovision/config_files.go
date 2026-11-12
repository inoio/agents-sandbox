package reprovision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	config "github.com/inoio/agents-sandbox/internal/configmerge"
	cp "github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// evalProvisionRules is indirect because a direct call cannot fail (the copy
// callback only records files in memory), so only a substitute reaches the
// provisioning-error path.
var evalProvisionRules = agent.EvalProvisionRules

// VMHomeDir is the sandbox home mount point. It is fixed by the project VM
// layout regardless of the configured runtime user.
const VMHomeDir = mounts.VMHomeDir

// ConfigSource is the resolved launcher config dirs for an agent. UserDirs holds
// every user scope's config dir in precedence order (generic, per-slug), and
// ProjectDir holds the project scope's. Fallback is the legacy dir name actually
// in use, or "" when the primary dirs are used. PrimaryUserDirs/PrimaryProjectDir
// are the primary (non-fallback) dirs the agent migrates to, carried from
// resolution so a caller need not re-derive (and re-validate) them.
type ConfigSource struct {
	UserDirs          []string
	ProjectDir        string
	Fallback          string
	PrimaryUserDirs   []string
	PrimaryProjectDir string
}

// ResolveConfigSource picks the agent's launcher config dirs from the ordered
// config scopes. The primary namespace wins when its dir exists in any scope;
// otherwise the first alias whose dir exists in any scope is used for every scope.
// When nothing exists the primary dirs are returned and Fallback is empty. The
// choice is one global decision: a per-slug primary dir drops a generic legacy
// dir rather than letting each scope choose independently.
func ResolveConfigSource(a agent.Agent, scopes []cp.ConfigScope) (ConfigSource, error) {
	names, err := agent.ConfigDirNames(a)
	if err != nil {
		return ConfigSource{}, err
	}
	userScopes, projectScope, hasProject := splitConfigScopes(scopes)
	userCandidates := make([][]string, len(names))
	projectCandidates := make([]string, len(names))
	for i, name := range names {
		for _, scope := range userScopes {
			userCandidates[i] = append(userCandidates[i], absoluteAgentConfigDir(scope, name))
		}
		if hasProject {
			projectCandidates[i] = absoluteAgentConfigDir(projectScope, name)
		}
	}
	src := ConfigSource{
		UserDirs:          userCandidates[0],
		ProjectDir:        projectCandidates[0],
		Fallback:          "",
		PrimaryUserDirs:   userCandidates[0],
		PrimaryProjectDir: projectCandidates[0],
	}
	primaryExists, err := anyDirExists(candidateDirs(userCandidates[0], projectCandidates[0])...)
	if err != nil {
		return ConfigSource{}, err
	}
	if primaryExists {
		return src, nil
	}
	// gomutants:disable-next-line INTEGER_DECREMENT reason="starting at 0 re-checks the primary pair, which was just found absent without an error, so the result is the same"
	for i := 1; i < len(userCandidates); i++ {
		exists, err := anyDirExists(candidateDirs(userCandidates[i], projectCandidates[i])...)
		if err != nil {
			return ConfigSource{}, err
		}
		if exists {
			src.UserDirs = userCandidates[i]
			src.ProjectDir = projectCandidates[i]
			src.Fallback = names[i]
			return src, nil
		}
	}
	return src, nil
}

// splitConfigScopes separates the project scope from the user scopes. The project
// scope is the last one ConfigScopes emits, but it is found by kind so a caller
// passing a subset still gets the user dirs in order.
func splitConfigScopes(scopes []cp.ConfigScope) ([]cp.ConfigScope, cp.ConfigScope, bool) {
	userScopes := make([]cp.ConfigScope, 0, len(scopes))
	var projectScope cp.ConfigScope
	hasProject := false
	for _, scope := range scopes {
		if scope.Kind == cp.ScopeProject {
			projectScope = scope
			hasProject = true
		} else {
			userScopes = append(userScopes, scope)
		}
	}
	return userScopes, projectScope, hasProject
}

// absoluteAgentConfigDir returns the agent's config dir under scope as an
// absolute path. The project scope's dir may be relative to the CWD, while
// candidate rendering and stats need absolute paths. filepath.Abs only fails
// when the CWD cannot be read, which can only affect a relative project scope
// dir; that degrades to the relative candidate, mirroring ProjectConfigDirAbs.
func absoluteAgentConfigDir(scope cp.ConfigScope, name string) string {
	dir := scope.AgentConfigDir(name)
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// candidateDirs returns the user candidates followed by the project candidate.
func candidateDirs(userDirs []string, projectDir string) []string {
	var dirs []string
	dirs = append(dirs, userDirs...)
	if projectDir != "" {
		dirs = append(dirs, projectDir)
	}
	return dirs
}

// anyDirExists reports whether any path names an existing directory. Every
// candidate is tried, so an unreadable candidate in one scope does not hide a
// usable one in another; a stat error other than "not exist" is remembered
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

// describeConfigScopes renders every user scope dir and the project dir. When
// onlyExisting is set, scopes whose dir is absent are omitted rather than named as
// if read.
func describeConfigScopes(userDirs []string, projectDir string, onlyExisting bool) string {
	var parts []string
	for _, dir := range userDirs {
		if !onlyExisting || IsDir(dir) {
			parts = append(parts, fmt.Sprintf("user: %q", dir))
		}
	}
	if projectDir != "" && (!onlyExisting || IsDir(projectDir)) {
		parts = append(parts, fmt.Sprintf("project: %q", projectDir))
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}

// SourceDirs returns the user and project config dirs actually read for the
// resolved source. They come straight from the source so display can never
// diverge from what was read; the seam builds candidates absolute, so all are
// absolute.
func SourceDirs(src ConfigSource) ([]string, string) {
	return src.UserDirs, src.ProjectDir
}

// noLauncherConfigDir reports whether the agent has no launcher config dir in any
// scope: no primary dir exists and no legacy fallback was found.
func noLauncherConfigDir(src ConfigSource) bool {
	if src.Fallback != "" {
		return false
	}
	if slices.ContainsFunc(src.PrimaryUserDirs, IsDir) {
		return false
	}
	return !IsDir(src.PrimaryProjectDir)
}

// IsDir reports whether path names an existing directory.
func IsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ConfigFiles holds the merged agent config, the set of home files to
// provision into the VM, the default drop-in copy from the host, and the VM
// paths to remove so stale host config cannot shadow the merged config.
type ConfigFiles struct {
	HasSnippets bool                    // whether any agent snippet existed
	Merged      []byte                  // merged agent config content
	MergedPath  string                  // VM path of the merged config ("" when no snippets)
	Sources     []string                // host snippet paths merged into Merged
	Skipped     []config.SkippedSnippet // matching snippets skipped on read or parse
	Source      ConfigSource            // resolved launcher config-dir pair (with legacy fallback marker)
	HomeFiles   map[string][]byte       // VM absolute path -> content (from the home: key)
	Provisioned map[string][]byte       // VM absolute path -> content (drop-in copy)
	Mirror      map[string][]byte       // VM absolute path -> content (verbatim <agent> mirror)
	Modes       map[string]os.FileMode  // VM absolute path -> ordinary file permission bits
	Remove      []string                // VM absolute paths to delete before writing
	Hooks       []homeconfig.HookSpec   // startup hooks to run at vm.SetUpSandbox
	Keys        []string                // merged config path first (when snippets exist), then the remaining VM paths sorted
}

// configSubject materializes the agent facts the config pipeline needs, derived
// once from the agent instead of re-asserted and re-derived per builder: the
// merger's VM config path, snippet pattern and config-file family, and the
// provisioner's drop-in rules. The builders consume this value, so the merged
// config path and the mirror's base directory cannot diverge between them.
type configSubject struct {
	merger         agent.ConfigMerger    // nil when the agent does not merge snippets
	provisioner    agent.Provisioner     // nil when the agent does not provision host config
	vmHome         string                // VM home the paths below were derived against
	vmConfigPath   string                // merged config's VM path ("" without a merger)
	snippetPattern string                // host snippet glob ("" without a merger)
	familyNames    []string              // config filenames the merged config supersedes
	provisionRules []agent.ProvisionRule // drop-in copy rules
}

// resolveConfigSubject reads the agent's config capabilities once: the merger's
// VM config path (against vmHome), snippet pattern and config-file family, and
// the provisioner's rules. An agent with neither capability yields the zero
// subject, so the builders keep their no-merger/no-provisioner behavior.
func resolveConfigSubject(a agent.Resolved, vmHome string) configSubject {
	var subject configSubject
	subject.vmHome = vmHome
	if a.ConfigMerger != nil {
		subject.merger = a.ConfigMerger
		subject.vmConfigPath = a.ConfigMerger.VMConfigPath(vmHome)
		subject.snippetPattern = a.ConfigMerger.SnippetPattern()
		subject.familyNames = a.ConfigMerger.ConfigFileNames()
	}
	if a.Provisioner != nil {
		subject.provisioner = a.Provisioner
		subject.provisionRules = a.Provisioner.ProvisionRules()
	}
	return subject
}

// LoadConfigFiles builds the desired VM state for the given agent using the
// host home from the config-paths seam. The agent determines its own config
// directories.
func LoadConfigFiles(
	a agent.Resolved,
	configSlug string,
	ui termio.Logger,
	provisionHostConfig bool,
) (*ConfigFiles, error) {
	hostHome, _ := cp.Get().UserHomeDir()
	return LoadConfigFilesForHost(a, configSlug, hostHome, VMHomeDir, ui, provisionHostConfig)
}

// LoadConfigFilesForHost builds the desired VM state for the given agent with
// explicit host and VM home directories: the merged agent config, the home
// files (from the home: key of the config files), and the default drop-in copy of the
// agent's host config (per its provision rules, unless host config provisioning
// is disabled). Home files and hooks are read from every config scope in
// precedence order (generic user, per-slug user when configSlug is set, then
// project), with later scopes overriding earlier ones. It warns about any home
// source that does not exist on the host and about malformed provision rules.
// Home files and the merged config override provisioned defaults for the same VM
// path. The agent's merged-config path is reserved: a home target colliding with
// it is rejected.
func LoadConfigFilesForHost(
	a agent.Resolved,
	configSlug string,
	hostHome, vmHome string,
	ui termio.Logger,
	provisionHostConfig bool,
) (*ConfigFiles, error) {
	scopes := cp.ConfigScopes(cp.Get(), configSlug)
	src, err := ResolveConfigSource(a.Profile, scopes)
	if err != nil {
		return nil, err
	}
	scopeDirs := cp.ScopeDirs(scopes)
	subject := resolveConfigSubject(a, vmHome)
	merged, err := subject.buildMergedConfig(src)
	if err != nil {
		return nil, err
	}
	for _, s := range merged.skipped {
		ui.Warnf("skipping agent config snippet %s: %v", s.Path, s.Err)
	}
	reserved, err := reservedHomeTargets(merged.path, vmHome)
	if err != nil {
		return nil, err
	}
	hooks, err := loadHooks(scopeDirs, vmHome, reserved)
	if err != nil {
		return nil, err
	}
	homeFiles, homeModes, err := loadHomeFiles(scopeDirs, vmHome, reserved, ui)
	if err != nil {
		return nil, err
	}
	provisioned, modes, err := subject.loadProvisioned(hostHome, ui, provisionHostConfig)
	if err != nil {
		return nil, err
	}

	mirror, mirrorModes := mirrorMaps(subject.buildMirror(src))
	subject.applyPrecedence(provisioned, homeFiles, mirror, modes, mirrorModes, homeModes, merged.hasSnippets)
	ensureHookExecutable(modes, hooks)
	// Remove stale host config so it cannot shadow the merged config: when
	// snippets exist the merged config must be the only config, and when host
	// config provisioning is disabled no host file may remain.
	remove := configFileFamilyPaths(merged.path, subject.familyNames)
	if !provisionHostConfig {
		remove = append(remove, subject.provisionDestinations(hostHome)...)
	}
	keys := configKeys(merged.path, merged.hasSnippets, homeFiles, mirror, provisioned)
	if src.Fallback != "" {
		legacyUserDirs, legacyProject := SourceDirs(src)
		ui.Warnf(
			"agent %s is reading launcher config from the legacy shared %q dir (%s), which agent opencode also uses; "+
				"a %q dir in any user or project scope switches all scopes, so copy (do not move) everything you need in one go into the %q namespace (%s): "+
				"a partial copy will not fall back for the rest. The guest ~/.config/opencode path is unchanged.",
			a.Profile.Name(),
			src.Fallback,
			describeConfigScopes(legacyUserDirs, legacyProject, true),
			a.Profile.ConfigDirName(),
			a.Profile.ConfigDirName(),
			describeConfigScopes(src.PrimaryUserDirs, src.PrimaryProjectDir, false),
		)
	}
	if noLauncherConfigDir(src) {
		ui.Infof(
			"No launcher config dir for agent %s yet (%s); create one to add config snippets, "+
				"mirrored files, and hooks. Run 'agents-sandbox config agent' to inspect what gets provisioned.",
			a.Profile.Name(), describeConfigScopes(src.PrimaryUserDirs, src.PrimaryProjectDir, false),
		)
	}
	return &ConfigFiles{
		HasSnippets: merged.hasSnippets,
		Merged:      merged.content,
		MergedPath:  merged.path,
		Sources:     merged.sources,
		Skipped:     merged.skipped,
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

// loadHomeFiles builds the home files and their modes from the ordered config
// scope dirs, warning about any home source missing on the host.
func loadHomeFiles(
	dirs []string,
	vmHome string,
	reserved []string,
	ui termio.Logger,
) (map[string][]byte, map[string]os.FileMode, error) {
	homeSources, missing, _, err := homeconfig.BuildHomeFilesWithModes(
		dirs,
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

// loadHooks builds the merged startup hooks from the ordered config scope dirs.
func loadHooks(dirs []string, vmHome string, reserved []string) ([]homeconfig.HookSpec, error) {
	hooks, err := homeconfig.BuildHooks(
		dirs,
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

// loadProvisioned evaluates the subject's provision rules into the drop-in copy
// maps, unless host config provisioning is disabled.
func (s configSubject) loadProvisioned(
	hostHome string,
	ui termio.Logger,
	enabled bool,
) (map[string][]byte, map[string]os.FileMode, error) {
	provisioned := make(map[string][]byte)
	modes := make(map[string]os.FileMode)
	if !enabled || s.provisioner == nil {
		// gomutants:disable-next-line RETURN_ZERO reason="callers only range over, index and delete from the map, so an empty and a nil map behave the same"
		return provisioned, modes, nil
	}
	for _, w := range agent.ValidateProvisionRules(s.provisionRules) {
		ui.Warnf("provision rule: %s", w)
	}
	onCopy := func(dst string, data []byte, mode os.FileMode) error {
		provisioned[dst] = data
		modes[dst] = mode
		return nil
	}
	if _, err := evalProvisionRules(s.provisionRules, hostHome, s.vmHome, onCopy); err != nil {
		return nil, nil, fmt.Errorf("eval provision rules: %w", err)
	}
	return provisioned, modes, nil
}

// applyPrecedence resolves the config sources that can supply the same VM path
// so the precedence contract holds, highest priority first:
//
//	merged config, home files  >  mirror  >  drop-in copy
//
// The merged config and a home file cannot collide: a home target at the
// merged-config path is rejected as reserved. The subject's merged-config path
// is the one the merge builder wrote to, so this is the single statement of
// which source wins. It is the only place the mirror and home file modes are
// installed.
func (s configSubject) applyPrecedence(
	provisioned, homeFiles, mirror map[string][]byte,
	modes, mirrorModes, homeModes map[string]os.FileMode,
	hasSnippets bool,
) {
	// Home files override the drop-in copy.
	for path := range homeFiles {
		delete(provisioned, path)
	}
	// The merged config overrides the drop-in copy when snippets exist.
	if hasSnippets {
		delete(provisioned, s.vmConfigPath)
		// gomutants:disable-next-line STATEMENT_REMOVE reason="the mirror-override block below removes this same key from modes under the identical hasSnippets condition, so dropping the statement leaves ConfigFiles.Modes unchanged"
		delete(modes, s.vmConfigPath)
	}
	// The mirror's modes take effect, then the home files' modes are applied
	// last so they win over both the drop-in copy and the mirror.
	maps.Copy(modes, mirrorModes)
	// The mirror overrides the drop-in copy.
	for path := range mirror {
		delete(provisioned, path)
	}
	// Home files and the merged config override the mirror.
	for path := range homeFiles {
		delete(mirror, path)
	}
	if hasSnippets {
		delete(mirror, s.vmConfigPath)
		delete(modes, s.vmConfigPath)
	}
	maps.Copy(modes, homeModes)
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

// mergedConfig is the outcome of merging an agent's snippet files: the
// destination VM path, the merged content, the snippet source paths, and
// whether any snippet existed.
type mergedConfig struct {
	path        string
	content     []byte
	sources     []string
	skipped     []config.SkippedSnippet
	hasSnippets bool
}

// orderedSourceDirs returns the source's dirs lowest precedence first: every
// user scope dir, then the project dir. It is candidateDirs over the source's
// fields, so the "user dirs then project" ordering rule has one home.
func orderedSourceDirs(src ConfigSource) []string {
	return candidateDirs(src.UserDirs, src.ProjectDir)
}

// buildMergedConfig merges the subject's snippet files into a single config.
// Every agent in the registry implements ConfigMerger, so a non-merger subject
// produces an empty mergedConfig.
func (s configSubject) buildMergedConfig(src ConfigSource) (mergedConfig, error) {
	if s.merger == nil {
		return mergedConfig{}, nil
	}
	built, err := config.BuildMerged(
		s.vmConfigPath,
		s.snippetPattern,
		orderedSourceDirs(src),
	)
	if err != nil {
		return mergedConfig{}, fmt.Errorf("merge agent config: %w", err)
	}
	return mergedConfig{
		path:        s.vmConfigPath,
		content:     built.Content,
		sources:     built.Sources,
		skipped:     built.Skipped,
		hasSnippets: built.HasSnippets,
	}, nil
}

// buildMirror returns the verbatim mirror files for the subject: every file in
// the user and project <agent> config dirs that is neither a top-level snippet
// match nor a top-level config-family name, keyed by VM path under the agent's
// VM config directory. A subject without a merger has no mirror.
func (s configSubject) buildMirror(src ConfigSource) map[string]config.MirrorEntry {
	if s.merger == nil {
		// gomutants:disable-next-line RETURN_ZERO reason="the caller only ranges over the map, so an empty and a nil map read the same"
		return map[string]config.MirrorEntry{}
	}
	entries := config.ScanMirror(
		s.snippetPattern,
		s.familyNames,
		orderedSourceDirs(src),
		filepath.Dir(s.vmConfigPath),
	)
	mirror := make(map[string]config.MirrorEntry, len(entries))
	for _, e := range entries {
		mirror[e.VMPath] = e
	}
	return mirror
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

// provisionDestinations returns the VM paths the subject's provision rules
// would copy for the given host, without copying anything. It mirrors the
// drop-in copy's destinations so they can be removed when host provisioning is
// disabled.
func (s configSubject) provisionDestinations(hostHome string) []string {
	if s.provisioner == nil {
		return nil
	}
	var dsts []string
	_, _ = agent.EvalProvisionRules(
		s.provisionRules,
		hostHome,
		s.vmHome,
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
// files, the verbatim mirror files (host path → VM path), the matching snippets
// skipped on read or parse, and the resolved config source for agent a, without
// touching a VM.
func Describe(
	a agent.Agent,
	configSlug, hostHome, vmHome string,
	ui termio.Logger,
	provisionHostConfig bool,
) (merged []byte, sources []string, hostFiles []HostFile, mirrorFiles []MirrorFile, skipped []config.SkippedSnippet, source ConfigSource, err error) {
	resolved := agent.Resolve(a)
	cf, err := LoadConfigFilesForHost(resolved, configSlug, hostHome, vmHome, ui, provisionHostConfig)
	if err != nil {
		return nil, nil, nil, nil, nil, ConfigSource{}, err
	}
	subject := resolveConfigSubject(resolved, vmHome)
	hostFiles = subject.hostFilesFromProvisioner(hostHome, cf)
	return cf.Merged, cf.Sources, hostFiles, subject.mirrorFilesFromConfig(cf.Source), cf.Skipped, cf.Source, nil
}

// mirrorFilesFromConfig returns the mirror display records for the subject.
func (s configSubject) mirrorFilesFromConfig(src ConfigSource) []MirrorFile {
	if s.merger == nil {
		return nil
	}
	entries := config.ScanMirror(
		s.snippetPattern,
		s.familyNames,
		orderedSourceDirs(src),
		filepath.Dir(s.vmConfigPath),
	)
	files := make([]MirrorFile, 0, len(entries))
	for _, e := range entries {
		files = append(files, MirrorFile{HostPath: e.HostPath, VMPath: e.VMPath})
	}
	return files
}

// hostFilesFromProvisioner walks the subject's provision rules against hostHome
// and records each host file it would copy, marking whether the VM destination
// is merged into the single config rather than copied as a drop-in. It never
// modifies any state.
func (s configSubject) hostFilesFromProvisioner(hostHome string, cf *ConfigFiles) []HostFile {
	if s.provisioner == nil {
		return nil
	}
	merged := make(map[string]struct{})
	if cf.HasSnippets {
		for _, path := range configFileFamilyPaths(cf.MergedPath, s.familyNames) {
			merged[path] = struct{}{}
		}
	}
	var files []HostFile
	_, _ = agent.EvalProvisionRules(
		s.provisionRules,
		hostHome,
		s.vmHome,
		func(dst string, _ []byte, _ os.FileMode) error {
			_, isMerged := merged[dst]
			files = append(files, HostFile{
				HostPath: hostPathForDst(dst, hostHome, s.vmHome),
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
func ReadVMConfig(ctx context.Context, sb msb.SandboxExecutor, paths []string, ui termio.Logger) map[string][]byte {
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

// ChangedAgentConfigPaths returns the guest paths whose agent config differs
// from the VM state: the merged config first (compared semantically), then the
// verbatim mirror files that differ or are missing (byte-for-byte), sorted. Any
// returned path triggers a daemon restart. Home and drop-in files are
// intentionally ignored: they are provisioned on every startup and do not
// require a daemon restart.
func ChangedAgentConfigPaths(cf *ConfigFiles, vmData map[string][]byte) []string {
	var changed []string
	if cf.HasSnippets {
		vm, ok := vmData[cf.MergedPath]
		// gomutants:disable-next-line BRANCH_IF,EXPRESSION_REMOVE reason="DocumentsEqual fails to parse a missing (nil) VM document against any non-empty merged one, so it already reports the difference"
		if !ok || !config.DocumentsEqual(cf.MergedPath, cf.Merged, vm) {
			changed = append(changed, cf.MergedPath)
		}
	}
	var mirrorChanged []string
	for path, want := range cf.Mirror {
		got, ok := vmData[path]
		if !ok || !bytes.Equal(want, got) {
			mirrorChanged = append(mirrorChanged, path)
		}
	}
	sort.Strings(mirrorChanged)
	return append(changed, mirrorChanged...)
}
