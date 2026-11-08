package reprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestResolveConfigSourceStatError(t *testing.T) {
	// A candidate beneath a regular file makes os.Stat fail with ENOTDIR,
	// which must surface rather than be treated as "absent" — for the primary
	// candidate and for an alias candidate reached through the fallback loop.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := agent.Lookup("opencode2")

	t.Run("primary", func(t *testing.T) {
		withConfigBases(t, blocker, blocker)
		if _, err := resolveConfigSource(a); err == nil {
			t.Error("expected an error when the primary candidate stat fails")
		}
	})

	t.Run("primary only", func(t *testing.T) {
		// Only the primary is unreadable (a symlink loop); the alias is
		// absent, so the error must come from the primary check itself.
		base := t.TempDir()
		if err := os.Symlink("opencode2", filepath.Join(base, "opencode2")); err != nil {
			t.Fatal(err)
		}
		withConfigBases(t, base, t.TempDir())
		_, err := resolveConfigSource(a)
		if _, ok := errors.AsType[*fs.PathError](err); !ok || !strings.Contains(err.Error(), "stat config dir") {
			t.Errorf("ResolveConfigSource error = %v, want the wrapped stat failure of the primary dir", err)
		}
	})

	t.Run("alias", func(t *testing.T) {
		// The primary is absent; the alias is a symlink loop, so its stat
		// fails with ELOOP rather than reporting it absent.
		base := t.TempDir()
		if err := os.Symlink("opencode", filepath.Join(base, "opencode")); err != nil {
			t.Fatal(err)
		}
		withConfigBases(t, base, base)
		if _, err := resolveConfigSource(a); err == nil {
			t.Error("expected an error when an alias candidate stat fails")
		}
	})
}

func TestResolveConfigSourceStatErrorDoesNotHideUsableScope(t *testing.T) {
	// An unreadable user candidate must not abort when the project candidate
	// exists: "either scope exists" still resolves the primary.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectBase := t.TempDir()
	existing := filepath.Join(projectBase, "opencode2")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	withConfigBases(t, blocker, projectBase)

	a, _ := agent.Lookup("opencode2")
	got, err := resolveConfigSource(a)
	if err != nil {
		t.Fatalf("ResolveConfigSource: unexpected error %v", err)
	}
	if got.Fallback != "" || got.ProjectDir != existing {
		t.Errorf("ResolveConfigSource = %+v, want primary with project dir %q", got, existing)
	}
}

// TestResolveConfigSourceCarriesPrimaryDirs pins that the primary migration
// targets travel in the resolved source, so the fallback warning never
// re-derives (and re-validates) the profile's config-dir name.
func TestResolveConfigSourceCarriesPrimaryDirs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	a, _ := agent.Lookup("opencode2")

	// No dirs exist, so the primary dirs are returned with an empty fallback.
	src, err := resolveConfigSource(a)
	if err != nil {
		t.Fatalf("ResolveConfigSource: %v", err)
	}
	if want := []string{
		configpaths.CreateDir(filepath.Join(cp.UserConfigDir(), a.ConfigDirName())),
	}; !slices.Equal(
		src.PrimaryUserDirs,
		want,
	) {
		t.Errorf("PrimaryUserDirs = %v, want the seam's primary user dirs %v", src.PrimaryUserDirs, want)
	}
	if src.PrimaryProjectDir != configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())) {
		t.Errorf("PrimaryProjectDir = %q, want the seam's primary project dir", src.PrimaryProjectDir)
	}
	if !filepath.IsAbs(src.PrimaryProjectDir) {
		t.Errorf("PrimaryProjectDir %q should be absolute", src.PrimaryProjectDir)
	}
}

func TestResolveConfigSourcePropagatesInvalidConfigDirName(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if _, err := resolveConfigSource(invalidConfigDirAgent{}); err == nil {
		t.Fatal("ResolveConfigSource with an invalid config-dir name = nil error, want error")
	}
}

func TestResolveConfigSource(t *testing.T) {
	cases := []struct {
		name                       string
		user2, user1, proj2, proj1 bool
		wantFallback               string
	}{
		{"nothing", false, false, false, false, ""},
		{"user primary", true, false, false, false, ""},
		{"project primary", false, false, true, false, ""},
		{"user legacy", false, true, false, false, "opencode"},
		{"project legacy", false, false, false, true, "opencode"},
		{"primary beats legacy", true, true, true, true, ""},
		{"any primary wins", false, true, true, false, ""},
		{"user primary, project legacy", true, false, false, true, ""},
		{"alias in both scopes", false, true, false, true, "opencode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			cp := configpaths.Get()
			mk := func(dir, name string, on bool) {
				if on {
					if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			}
			mk(cp.UserConfigDir(), "opencode2", tc.user2)
			mk(cp.UserConfigDir(), "opencode", tc.user1)
			mk(cp.ProjectConfigDir(), "opencode2", tc.proj2)
			mk(cp.ProjectConfigDir(), "opencode", tc.proj1)

			a, _ := agent.Lookup("opencode2")
			got, err := resolveConfigSource(a)
			if err != nil {
				t.Fatalf("ResolveConfigSource: %v", err)
			}

			name := "opencode2"
			if tc.wantFallback != "" {
				name = tc.wantFallback
			}
			want := ConfigSource{
				UserDirs:          []string{filepath.Join(cp.UserConfigDir(), name)},
				ProjectDir:        filepath.Join(cp.ProjectConfigDir(), name),
				Fallback:          tc.wantFallback,
				PrimaryUserDirs:   []string{filepath.Join(cp.UserConfigDir(), "opencode2")},
				PrimaryProjectDir: filepath.Join(cp.ProjectConfigDirAbs(), "opencode2"),
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ResolveConfigSource = %+v, want %+v", got, want)
			}
		})
	}
}

func TestDescribeConfigScopes(t *testing.T) {
	existing := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	userDirs := func(dirs ...string) []string { return dirs }

	if got := describeConfigScopes(userDirs(missing), missing, true); got != "(none)" {
		t.Errorf("onlyExisting with nothing present = %q, want (none)", got)
	}
	onlyExisting := describeConfigScopes(userDirs(existing), missing, true)
	if !strings.Contains(onlyExisting, "user: ") || strings.Contains(onlyExisting, "project:") {
		t.Errorf("onlyExisting should list only the present scope, got %q", onlyExisting)
	}
	targets := describeConfigScopes(userDirs(existing), missing, false)
	if !strings.Contains(targets, "user: ") || !strings.Contains(targets, "project: ") {
		t.Errorf("targets should list both scopes, got %q", targets)
	}
	if strings.Contains(targets, "relative to CWD") {
		t.Errorf("targets should show explicit absolute paths, got %q", targets)
	}
	// A missing user dir must still be named when onlyExisting is false: the
	// project scope being the only one present must not drop the user scope.
	if got, want := describeConfigScopes(
		userDirs(missing),
		existing,
		false,
	), fmt.Sprintf(
		"user: %q, project: %q",
		missing,
		existing,
	); got != want {
		t.Errorf("targets with only the project present = %q, want %q", got, want)
	}
	// An existing project dir must be named when onlyExisting is true: the
	// user scope being absent must not drop the project scope.
	if got, want := describeConfigScopes(
		userDirs(missing),
		existing,
		true,
	), fmt.Sprintf(
		"project: %q",
		existing,
	); got != want {
		t.Errorf("onlyExisting with only the project present = %q, want %q", got, want)
	}
	// Every user scope dir is named, in order, before the project dir.
	if got, want := describeConfigScopes(
		userDirs(existing, missing),
		existing,
		true,
	), fmt.Sprintf("user: %q, project: %q", existing, existing); got != want {
		t.Errorf("onlyExisting with a per-slug scope = %q, want %q", got, want)
	}
	// An empty projectDir must not be named as if it were a read scope: the
	// project entry is emitted only when a dir is present.
	if got := describeConfigScopes(userDirs(existing), "", false); strings.Contains(got, "project:") {
		t.Errorf("an empty project dir must not be described, got %q", got)
	}
}

// TestSplitConfigScopesNoProject pins that a scope set without a project scope
// reports hasProject=false and the zero project scope, so a caller gates the
// project candidate on the flag instead of a zero-value dir.
func TestSplitConfigScopesNoProject(t *testing.T) {
	scopes := []configpaths.ConfigScope{
		{Kind: configpaths.ScopeGenericUser, Dir: "/generic"},
		{Kind: configpaths.ScopePerSlugUser, Dir: "/per-slug"},
	}
	users, project, hasProject := splitConfigScopes(scopes)
	if hasProject {
		t.Error("splitConfigScopes reported a project scope for a scope set without one")
	}
	if project != (configpaths.ConfigScope{}) {
		t.Errorf("projectScope = %+v, want the zero scope", project)
	}
	if !slices.Equal(users, scopes) {
		t.Errorf("userScopes = %+v, want every scope in order", users)
	}
}

func TestSourceDirsMirrorResolvedSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	src := ConfigSource{
		UserDirs:   []string{filepath.Join(cp.UserConfigDir(), "opencode")},
		ProjectDir: filepath.Join(cp.ProjectConfigDirAbs(), "opencode"),
		Fallback:   "opencode",
	}
	userDirs, project := SourceDirs(src)
	if !slices.Equal(userDirs, src.UserDirs) || project != src.ProjectDir {
		t.Errorf("SourceDirs = (%q, %q), want the resolved source dirs", userDirs, project)
	}
}

// TestLoadConfigFilesRemovesStaleConfigWithSnippets verifies that when snippets
// produce a merged config, the config-file family is excluded from the drop-in
// copy and marked for removal so host config cannot shadow the merged config,
// while unrelated host config files are still provisioned.
func TestLoadConfigFilesRemovesStaleConfigWithSnippets(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")

	// Snippets exist so hasSnippets=true and the merged config is written.
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"opencode-model.json",
		`{"model":"x"}`,
	)

	// Host files the drop-in copy would pick up: opencode.jsonc is the merged
	// config filename, other.json is unrelated.
	ocConfig := filepath.Join(hostHome, ".config/opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.jsonc", `{"model":"host"}`)
	testutil.WriteFile(t, ocConfig, "other.json", `{"host":1}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}

	mergedPath := filepath.Join(vmHome, ".config", "opencode", "opencode.jsonc")
	for _, name := range resolveConfigSubject(agent.Resolve(opencodeTestAgent()), vmHome).familyNames {
		want := filepath.Join(vmHome, ".config", "opencode", name)
		if !slices.Contains(cf.Remove, want) {
			t.Errorf("expected config file %s in Remove, got %v", want, cf.Remove)
		}
		if _, ok := cf.Provisioned[want]; ok {
			t.Errorf("config file %s must not be in Provisioned when snippets exist, got %v", want, cf.Provisioned)
		}
	}
	if _, ok := cf.Provisioned[mergedPath]; ok {
		t.Errorf(
			"merged config path %s must not be in Provisioned (merged config wins), got %v",
			mergedPath,
			cf.Provisioned,
		)
	}
	otherPath := filepath.Join(vmHome, ".config", "opencode", "other.json")
	if _, ok := cf.Provisioned[otherPath]; !ok {
		t.Errorf("unrelated host config %s should still be provisioned, got %v", otherPath, cf.Provisioned)
	}
}

// TestLoadConfigFilesProvisioningDisabled verifies that with host config
// provisioning disabled the drop-in copy is skipped entirely, the config-file
// family and auth.json are marked for removal, and snippet merging still works.
func TestLoadConfigFilesProvisioningDisabled(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")

	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"opencode-model.json",
		`{"model":"x"}`,
	)
	ocConfig := filepath.Join(hostHome, ".config/opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.json", `{"a":1}`)
	ocShare := filepath.Join(hostHome, ".local", "share", "opencode")
	if err := os.MkdirAll(ocShare, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocShare, "auth.json", `{"t":"x"}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", hostHome, vmHome, &ui, false)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}

	if len(cf.Provisioned) != 0 {
		t.Errorf("expected no drop-in copy when provisioning disabled, got %v", cf.Provisioned)
	}
	for _, name := range resolveConfigSubject(agent.Resolve(opencodeTestAgent()), vmHome).familyNames {
		want := filepath.Join(vmHome, ".config", "opencode", name)
		if !slices.Contains(cf.Remove, want) {
			t.Errorf("expected config file %s in Remove, got %v", want, cf.Remove)
		}
	}
	authPath := filepath.Join(vmHome, ".local", "share", "opencode", "auth.json")
	if !slices.Contains(cf.Remove, authPath) {
		t.Errorf("expected auth.json in Remove when provisioning disabled, got %v", cf.Remove)
	}
	if !cf.HasSnippets || len(cf.Merged) == 0 {
		t.Error("expected merged config to be built despite disabled provisioning")
	}
}

// TestResolveConfigSubjectMaterializesMergerFacts verifies the subject derives
// the merger's VM config path, snippet pattern and config-file family, and the
// provisioner's rules, once from the agent, so the builders consume them
// instead of re-asserting the capabilities and recomputing the paths.
func TestResolveConfigSubjectMaterializesMergerFacts(t *testing.T) {
	vmHome := filepath.Join(string(filepath.Separator), "home", "dev")
	a, _ := agent.Lookup("")
	subject := resolveConfigSubject(agent.Resolve(a), vmHome)

	if subject.merger == nil {
		t.Fatal("opencode should resolve a merger")
	}
	if subject.provisioner == nil {
		t.Fatal("opencode should resolve a provisioner")
	}
	if want := filepath.Join(vmHome, ".config", "opencode", "opencode.jsonc"); subject.vmConfigPath != want {
		t.Errorf("vmConfigPath = %q, want %q", subject.vmConfigPath, want)
	}
	if want := "opencode*.json*"; subject.snippetPattern != want {
		t.Errorf("snippetPattern = %q, want %q", subject.snippetPattern, want)
	}
	if !slices.Contains(subject.familyNames, "opencode.json") {
		t.Errorf("familyNames = %v, want to include opencode.json", subject.familyNames)
	}
	if len(subject.provisionRules) == 0 {
		t.Error("provisionRules should be non-empty for opencode")
	}
}

// TestResolveConfigSubjectZeroForPlainAgent verifies an agent with neither
// capability yields a subject whose derived facts are empty, so the builders
// keep their "no merger/provisioner" behavior.
func TestResolveConfigSubjectZeroForPlainAgent(t *testing.T) {
	subject := resolveConfigSubject(
		agent.Resolve(plainAgent{}),
		filepath.Join(string(filepath.Separator), "home", "dev"),
	)
	if subject.merger != nil || subject.provisioner != nil {
		t.Errorf("plain agent should resolve no capabilities, got %+v", subject)
	}
	if subject.vmConfigPath != "" || subject.snippetPattern != "" || subject.familyNames != nil {
		t.Errorf("plain agent should derive no merger facts, got %+v", subject)
	}
	if subject.provisionRules != nil {
		t.Errorf("plain agent should have no provision rules, got %v", subject.provisionRules)
	}
}

// TestHostFilesFromProvisioner verifies hostFilesFromProvisioner reports each
// host file the drop-in copy would pick up, marking config-family destinations
// as Merged when snippets produce a merged config.
func TestHostFilesFromProvisioner(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")
	ocConfig := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.json", `{"a":1}`)
	testutil.WriteFile(t, ocConfig, "other.json", `{"host":1}`)

	mergedPath := filepath.Join(vmHome, ".config", "opencode", "opencode.jsonc")
	cf := &ConfigFiles{HasSnippets: true, MergedPath: mergedPath}
	files := resolveConfigSubject(agent.Resolve(a), vmHome).hostFilesFromProvisioner(hostHome, cf)

	if len(files) != 2 {
		t.Fatalf("hostFilesFromProvisioner = %+v, want 2 files", files)
	}
	var configFile, otherFile *HostFile
	for i := range files {
		f := &files[i]
		switch f.VMPath {
		case filepath.Join(vmHome, ".config", "opencode", "opencode.json"):
			configFile = f
		case filepath.Join(vmHome, ".config", "opencode", "other.json"):
			otherFile = f
		}
	}
	if configFile == nil || !configFile.Merged {
		t.Errorf("config-family file should be marked Merged, got %+v", files)
	}
	if configFile != nil && configFile.HostPath != filepath.Join(hostHome, ".config", "opencode", "opencode.json") {
		t.Errorf("config-file HostPath = %q", configFile.HostPath)
	}
	if otherFile == nil || otherFile.Merged {
		t.Errorf("non-family file should not be Merged, got %+v", files)
	}
}

func TestHostFilesFromProvisionerNoSnippets(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")
	ocConfig := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.json", `{"a":1}`)

	cf := &ConfigFiles{HasSnippets: false}
	files := resolveConfigSubject(agent.Resolve(a), vmHome).hostFilesFromProvisioner(hostHome, cf)
	if len(files) != 1 || files[0].Merged {
		t.Errorf("hostFilesFromProvisioner = %+v, want 1 non-merged file", files)
	}
}

func TestLoadConfigFilesPopulatesHooks(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	testutil.WritePath(t, filepath.Join(cp.ProjectConfigDir(), "connect.sh"), "#!/bin/sh\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .vpn/connect.sh:\n"+
			"    source: connect.sh\n"+
			"    hook: startup\n"+
			"    root: true\n")

	ui := termio.NewTestMock(t)
	a, _ := agent.Lookup("")
	cf, err := LoadConfigFiles(agent.Resolve(a), "", &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	want := []homeconfig.HookSpec{
		{
			Target:     "/home/dev/.vpn/connect.sh",
			Source:     filepath.Join(cp.ProjectConfigDir(), "connect.sh"),
			HasShebang: true,
			Root:       true,
		},
	}
	if !reflect.DeepEqual(cf.Hooks, want) {
		t.Errorf("Hooks = %v, want %v", cf.Hooks, want)
	}
}

// TestLoadConfigFilesMarksHookScriptsExecutable verifies that startup-hook
// targets gain the owner-execute bit so the VM can execute them directly even
// when the host source is not executable.
func TestLoadConfigFilesMarksHookScriptsExecutable(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	testutil.WritePath(t, filepath.Join(cp.ProjectConfigDir(), "connect.sh"), "#!/bin/sh\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "plain.txt", "plain\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .vpn/connect.sh:\n"+
			"    source: connect.sh\n"+
			"    hook: startup\n"+
			"  .config: plain.txt\n")

	ui := termio.NewTestMock(t)
	a, _ := agent.Lookup("")
	cf, err := LoadConfigFiles(agent.Resolve(a), "", &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	target := "/home/dev/.vpn/connect.sh"
	if mode, ok := cf.Modes[target]; !ok || mode.Perm()&0o100 == 0 {
		t.Errorf("hook target %s mode = %v (present=%v), want the owner-execute bit", target, mode, ok)
	}
	plain := "/home/dev/.config"
	if mode, ok := cf.Modes[plain]; !ok || mode.Perm()&0o100 != 0 {
		t.Errorf("non-hook home file %s mode = %v (present=%v), want no exec bit", plain, mode, ok)
	}
}

func TestLoadConfigFilesFallsBackToLegacyDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	a, _ := agent.Lookup("opencode2")

	legacy := filepath.Join(cp.UserConfigDir(), "opencode")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, legacy, "opencode-model.json", `{"model":"v2-legacy"}`)
	testutil.WriteFile(t, legacy, "tui.json", `{"theme":"legacy"}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFiles(agent.Resolve(a), "", &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	if cf.Source.Fallback != "opencode" {
		t.Errorf("Source.Fallback = %q, want opencode", cf.Source.Fallback)
	}
	if !strings.Contains(string(cf.Merged), "v2-legacy") {
		t.Errorf("merged config did not read the legacy dir: %s", cf.Merged)
	}
	mirrorPath := filepath.Join(reprovisionVMConfigDir(t, a), "tui.json")
	if _, ok := cf.Mirror[mirrorPath]; !ok {
		t.Errorf("mirror missing legacy file %q; have %v", mirrorPath, cf.Mirror)
	}
	if n := countWarns(ui.WarnCalls, "legacy shared"); n != 1 {
		t.Errorf("legacy fallback warning count = %d, want 1; warnings: %v", n, ui.WarnCalls)
	}
	warn := ui.WarnCalls[0]
	legacyProject := filepath.Join(cp.ProjectConfigDirAbs(), "opencode")
	primaryProject := filepath.Join(cp.ProjectConfigDirAbs(), "opencode2")
	if !strings.Contains(warn, legacy) || !strings.Contains(warn, legacyProject) {
		t.Errorf("warning should name the legacy dirs actually read; got %q", warn)
	}
	if !strings.Contains(warn, primaryProject) || !strings.Contains(warn, "do not move") {
		t.Errorf("warning should name the primary targets and say not to move; got %q", warn)
	}
}

func TestLoadConfigFilesPrimarySuppressesLegacy(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	a, _ := agent.Lookup("opencode2")

	for _, dir := range []string{filepath.Join(cp.UserConfigDir(), "opencode2"), filepath.Join(cp.UserConfigDir(), "opencode")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	testutil.WriteFile(
		t,
		filepath.Join(cp.UserConfigDir(), "opencode2"),
		"opencode-model.json",
		`{"model":"v2-primary"}`,
	)
	testutil.WriteFile(t, filepath.Join(cp.UserConfigDir(), "opencode"), "opencode-model.json", `{"model":"v1-legacy"}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFiles(agent.Resolve(a), "", &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	if cf.Source.Fallback != "" {
		t.Errorf("Source.Fallback = %q, want empty (primary wins)", cf.Source.Fallback)
	}
	if !strings.Contains(string(cf.Merged), "v2-primary") || strings.Contains(string(cf.Merged), "v1-legacy") {
		t.Errorf("merged config should read only the primary dir: %s", cf.Merged)
	}
	if n := countWarns(ui.WarnCalls, "legacy shared"); n != 0 {
		t.Errorf("unexpected legacy warning: %v", ui.WarnCalls)
	}
}

func TestLoadConfigFilesInfosWhenNoLauncherConfigDirExists(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	a, _ := agent.Lookup("opencode2")

	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFiles(agent.Resolve(a), "", &ui, true); err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	if len(ui.InfoCalls) != 1 {
		t.Fatalf("info count = %d, want 1; infos: %v", len(ui.InfoCalls), ui.InfoCalls)
	}
	info := ui.InfoCalls[0]
	userDir := filepath.Join(cp.UserConfigDir(), "opencode2")
	projectDir := filepath.Join(cp.ProjectConfigDirAbs(), "opencode2")
	for _, want := range []string{userDir, projectDir, "agents-sandbox config agent"} {
		if !strings.Contains(info, want) {
			t.Errorf("info should mention %q; got %q", want, info)
		}
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("unexpected warnings: %v", ui.WarnCalls)
	}
}

func TestLoadConfigFilesNoInfoWhenALauncherConfigDirExists(t *testing.T) {
	cases := map[string]func(configpaths.ConfigPaths) string{
		"user":            func(cp configpaths.ConfigPaths) string { return filepath.Join(cp.UserConfigDir(), "opencode2") },
		"project":         func(cp configpaths.ConfigPaths) string { return filepath.Join(cp.ProjectConfigDirAbs(), "opencode2") },
		"legacy fallback": func(cp configpaths.ConfigPaths) string { return filepath.Join(cp.UserConfigDir(), "opencode") },
	}
	for name, dirOf := range cases {
		t.Run(name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			if err := os.MkdirAll(dirOf(configpaths.Get()), 0o755); err != nil {
				t.Fatal(err)
			}
			a, _ := agent.Lookup("opencode2")
			ui := termio.NewTestMock(t)
			if _, err := LoadConfigFiles(agent.Resolve(a), "", &ui, true); err != nil {
				t.Fatalf("LoadConfigFiles: %v", err)
			}
			if len(ui.InfoCalls) != 0 {
				t.Errorf("unexpected info: %v", ui.InfoCalls)
			}
		})
	}
}

// TestLoadConfigFilesProvisioning verifies the default drop-in copy: host files
// under the agent's config dirs are provisioned into the VM home, while
// excluded paths (node_modules) are not.
func TestLoadConfigFilesProvisioning(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	vmHome := t.TempDir()
	ocConfig := filepath.Join(hostHome, ".config/opencode")
	if err := os.MkdirAll(filepath.Join(ocConfig, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.json", `{"a":1}`)
	launcher := filepath.Join(ocConfig, "bin", "launcher")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, launcher, "#!/bin/sh\necho launcher\n")
	if err := os.Chmod(launcher, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(ocConfig, "node_modules"), "x/index.js", "//x")

	a, _ := agent.Lookup("opencode")
	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	wantKey := filepath.Join(vmHome, ".config", "opencode", "opencode.json")
	if _, ok := cf.Provisioned[wantKey]; !ok {
		t.Errorf("expected opencode.json provisioned at %s, got %v", wantKey, cf.Provisioned)
	}
	wantLauncher := filepath.Join(vmHome, ".config", "opencode", "bin", "launcher")
	if cf.Modes[wantLauncher].Perm() != 0o755 {
		t.Errorf("launcher mode = %o, want 755", cf.Modes[wantLauncher].Perm())
	}
	for p := range cf.Provisioned {
		if strings.Contains(p, "node_modules") {
			t.Errorf("node_modules must not be provisioned, got %s", p)
		}
	}
}

// TestLoadConfigFilesProvisioningPrecedence verifies the precedence rules: a
// home-file key overrides a provisioned key at the same VM path, and when
// snippets exist the merged agent config wins over the provisioned default at
// its path (so the merged config path is absent from Provisioned).
func TestLoadConfigFilesProvisioningPrecedence(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")

	// Snippets exist so hasSnippets=true and the merged config is written.
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"opencode-model.json",
		`{"model":"x"}`,
	)

	// Host files that the drop-in copy would pick up.
	ocConfig := filepath.Join(hostHome, ".config/opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.json", `{"a":1}`)
	testutil.WriteFile(t, ocConfig, "somefile.json", `{"host":1}`)

	// A home file at the same VM path as one of the provisioned files, which
	// must override it.
	project := cp.ProjectConfigDir()
	testutil.WriteFile(t, project, "somefile-home.json", `{"home":1}`)
	if err := os.Chmod(filepath.Join(project, "somefile-home.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, project, "config.yaml",
		"home:\n"+
			"  .config/opencode/somefile.json:\n"+
			"    source: somefile-home.json\n")

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}

	mergedPath := filepath.Join(vmHome, ".config", "opencode", "opencode.jsonc")
	if _, ok := cf.Provisioned[mergedPath]; ok {
		t.Errorf(
			"merged config path %s must not be in Provisioned (merged config wins), got %v",
			mergedPath,
			cf.Provisioned,
		)
	}
	if !slices.Contains(cf.Keys, mergedPath) {
		t.Errorf("expected merged config path %s in Keys, got %v", mergedPath, cf.Keys)
	}

	homePath := filepath.Join(vmHome, ".config", "opencode", "somefile.json")
	if _, ok := cf.Provisioned[homePath]; ok {
		t.Errorf("home-file path %s must not be in Provisioned (home file wins), got %v", homePath, cf.Provisioned)
	}
	if !bytes.Equal(cf.HomeFiles[homePath], []byte(`{"home":1}`)) {
		t.Errorf("expected home file to override provisioned content at %s, got %q", homePath, cf.HomeFiles[homePath])
	}
	if cf.Modes[homePath].Perm() != 0o755 {
		t.Errorf("home file mode = %o, want 755", cf.Modes[homePath].Perm())
	}
}

// TestLoadConfigFilesShadowingCopiedWithoutSnippets verifies that without
// snippets the host opencode.jsonc is drop-in copied as the default config.
func TestLoadConfigFilesShadowingCopiedWithoutSnippets(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")

	ocConfig := filepath.Join(hostHome, ".config/opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.jsonc", `{"model":"host"}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf.HasSnippets {
		t.Fatal("expected HasSnippets=false without snippets")
	}

	shadowPath := filepath.Join(vmHome, ".config", "opencode", "opencode.jsonc")
	if _, ok := cf.Provisioned[shadowPath]; !ok {
		t.Errorf("expected host %s provisioned when no snippets exist, got %v", shadowPath, cf.Provisioned)
	}
}

// TestLoadConfigFilesPIMergedConfig verifies that a pi snippet is merged into
// the pi settings path (not the opencode path) and that the config family it
// supersedes is removed from the VM.
func TestLoadConfigFilesPIMergedConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("pi")
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"settings.json",
		`{"model":"claude-sonnet-4-5"}`,
	)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if !cf.HasSnippets {
		t.Fatal("expected HasSnippets=true for a pi settings snippet")
	}
	wantMerged := filepath.Join(vmHome, ".pi", "agent", "settings.json")
	if cf.MergedPath != wantMerged {
		t.Errorf("MergedPath = %q, want %q", cf.MergedPath, wantMerged)
	}
	if !slices.Contains(cf.Keys, wantMerged) {
		t.Errorf("Keys = %v, want to include %s", cf.Keys, wantMerged)
	}
	if !slices.Contains(cf.Remove, wantMerged) {
		t.Errorf("Remove = %v, want to include %s", cf.Remove, wantMerged)
	}
}

// TestLoadConfigFilesCodexMergesTOMLSnippets verifies that codex config.toml
// snippets merge into ~/.codex/config.toml as TOML and that the result counts
// as equal to a reformatted copy of itself in the VM.
func TestLoadConfigFilesCodexMergesTOMLSnippets(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("codex")
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"config-model.toml",
		"model = \"gpt-5\"\n",
	)
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"config-sandbox.toml",
		"[sandbox_workspace_write]\nnetwork_access = true\n",
	)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", t.TempDir(), vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	wantMerged := filepath.Join(vmHome, ".codex", "config.toml")
	if !cf.HasSnippets || cf.MergedPath != wantMerged {
		t.Fatalf("HasSnippets=%v MergedPath=%q, want true and %q", cf.HasSnippets, cf.MergedPath, wantMerged)
	}
	want := "model = 'gpt-5'\n\n[sandbox_workspace_write]\nnetwork_access = true\n"
	if string(cf.Merged) != want {
		t.Errorf("Merged = %q, want %q", cf.Merged, want)
	}
	reformatted := []byte("sandbox_workspace_write = { network_access = true }\nmodel = \"gpt-5\"\n")
	if !agentConfigEqual(cf, map[string][]byte{wantMerged: reformatted}) {
		t.Error("agentConfigEqual should compare the TOML config semantically")
	}
}

// TestLoadConfigFilesHermesMergedConfig verifies that a hermes snippet is
// merged into the hermes config path as YAML and compared semantically: the
// file is hermes' input and user-editable, so a JSON document wearing a .yaml
// name would be wrong and a reformatted but equal copy must not read as a
// change.
func TestLoadConfigFilesHermesMergedConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("hermes")
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"config-model.yaml",
		"model: opus\n",
	)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", t.TempDir(), vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if !cf.HasSnippets {
		t.Fatal("expected HasSnippets=true for a hermes config snippet")
	}
	wantMerged := filepath.Join(vmHome, ".hermes", "config.yaml")
	if cf.MergedPath != wantMerged {
		t.Errorf("MergedPath = %q, want %q", cf.MergedPath, wantMerged)
	}
	if got := string(cf.Merged); got != "model: opus\n" {
		t.Errorf("Merged = %q, want %q (YAML, not JSON)", got, "model: opus\n")
	}
	reformatted := []byte("model: \"opus\"\n")
	if !agentConfigEqual(cf, map[string][]byte{wantMerged: reformatted}) {
		t.Error("agentConfigEqual should compare the YAML config semantically")
	}
}

// TestLoadConfigFilesOhMyPiMergedConfig verifies that oh-my-pi snippets merge
// into ~/.omp/agent/config.yml as YAML and that both names omp reads, config.yml
// and its config.yaml fallback, are removed from the VM, so a stale copy of
// either cannot shadow the merged config.
func TestLoadConfigFilesOhMyPiMergedConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("oh-my-pi")
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"config-startup.yaml",
		"startup:\n  quiet: true\n",
	)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", t.TempDir(), vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	wantMerged := filepath.Join(vmHome, ".omp", "agent", "config.yml")
	if !cf.HasSnippets || cf.MergedPath != wantMerged {
		t.Fatalf("HasSnippets=%v MergedPath=%q, want true and %q", cf.HasSnippets, cf.MergedPath, wantMerged)
	}
	if got, want := string(cf.Merged), "startup:\n    quiet: true\n"; got != want {
		t.Errorf("Merged = %q, want %q (YAML, not JSON)", got, want)
	}
	for _, name := range []string{"config.yml", "config.yaml"} {
		if path := filepath.Join(vmHome, ".omp", "agent", name); !slices.Contains(cf.Remove, path) {
			t.Errorf("Remove = %v, want to include %s", cf.Remove, path)
		}
	}
}

// TestLoadConfigFilesRejectsReservedMergedPath verifies that a home target
// colliding with the agent's merged-config path is rejected.
func TestLoadConfigFilesRejectsReservedMergedPath(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "home:\n  .config/opencode/opencode.jsonc:\n")

	ui := termio.NewTestMock(t)
	_, err := LoadConfigFilesForHost(agent.Resolve(a), "", t.TempDir(), vmHome, &ui, true)
	if err == nil {
		t.Fatal("expected an error for a home target colliding with the merged-config path")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("expected 'reserved' in error, got: %v", err)
	}
}

// TestLoadConfigFilesPIMergedPathReserved verifies the reserved path follows the
// agent: pi's home config may not target its merged settings.json path.
func TestLoadConfigFilesPIMergedPathReserved(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("pi")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "home:\n  .pi/agent/settings.json:\n")

	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(agent.Resolve(a), "", t.TempDir(), vmHome, &ui, true); err == nil {
		t.Error("expected an error for a pi home target colliding with its merged settings.json")
	}
}

// TestLoadConfigFilesNoReservedForNonConfigMerger verifies that an agent without
// a ConfigMerger reserves nothing, so a previously-reserved opencode target is
// accepted for it.
func TestLoadConfigFilesNoReservedForNonConfigMerger(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	vmHome := t.TempDir()

	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "home:\n  .config/opencode/opencode.jsonc:\n")

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(plainAgent{}), "", t.TempDir(), vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf.HasSnippets {
		t.Error("expected HasSnippets=false for a non-ConfigMerger agent")
	}
}

// TestHostFilesFromProvisionerNoSnippets verifies that without snippets the
// drop-in files are not marked merged.
func TestLoadConfigFilesMirror(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")
	// Snippet (merged, not mirrored) plus mirror files at top level and nested.
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"opencode-model.json",
		`{"model":"x"}`,
	)
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"tui.json",
		`{"theme":"dark"}`,
	)
	if err := os.Chmod(
		filepath.Join(configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())), "tui.json"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(
		filepath.Join(configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())), "agents"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(
		t,
		filepath.Join(configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())), "agents"),
		"coder.md",
		"# coder\n",
	)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	wantTUI := filepath.Join(vmHome, ".config", "opencode", "tui.json")
	if !bytes.Equal(cf.Mirror[wantTUI], []byte(`{"theme":"dark"}`)) {
		t.Errorf("Mirror[%s] = %q, want dark theme", wantTUI, cf.Mirror[wantTUI])
	}
	if cf.Modes[wantTUI].Perm() != 0o755 {
		t.Errorf("Mirror[%s] mode = %o, want 755", wantTUI, cf.Modes[wantTUI].Perm())
	}
	wantAgent := filepath.Join(vmHome, ".config", "opencode", "agents", "coder.md")
	if !bytes.Equal(cf.Mirror[wantAgent], []byte("# coder\n")) {
		t.Errorf("Mirror[%s] = %q, want coder agent", wantAgent, cf.Mirror[wantAgent])
	}
	// The snippet file must NOT be mirrored.
	snippetVM := filepath.Join(vmHome, ".config", "opencode", "opencode-model.json")
	if _, ok := cf.Mirror[snippetVM]; ok {
		t.Errorf("snippet file must not be mirrored, got %v", cf.Mirror[snippetVM])
	}
	// Mirror paths appear in Keys.
	if !slices.Contains(cf.Keys, wantTUI) || !slices.Contains(cf.Keys, wantAgent) {
		t.Errorf("Keys = %v, want mirror paths %s and %s", cf.Keys, wantTUI, wantAgent)
	}
}

func TestLoadConfigFilesMirrorPrecedence(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")
	// Mirror file at tui.json, plus a host drop-in copy at the same VM path.
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"tui.json",
		`{"theme":"mirror"}`,
	)
	if err := os.Chmod(
		filepath.Join(configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())), "tui.json"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	ocConfig := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "tui.json", `{"theme":"host"}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	wantTUI := filepath.Join(vmHome, ".config", "opencode", "tui.json")
	if _, ok := cf.Provisioned[wantTUI]; ok {
		t.Errorf(
			"mirror path %s must not be in Provisioned (mirror wins over drop-in), got %v",
			wantTUI,
			cf.Provisioned,
		)
	}
	if !bytes.Equal(cf.Mirror[wantTUI], []byte(`{"theme":"mirror"}`)) {
		t.Errorf("Mirror[%s] = %q, want mirror content", wantTUI, cf.Mirror[wantTUI])
	}
	if cf.Modes[wantTUI].Perm() != 0o700 {
		t.Errorf("Mirror[%s] mode = %o, want 700", wantTUI, cf.Modes[wantTUI].Perm())
	}
}

func TestLoadConfigFilesMirrorHomeYAMLWins(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"tui.json",
		`{"theme":"mirror"}`,
	)
	// home maps the same VM path explicitly; it must win over the mirror.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "tui-home.json", `{"theme":"home"}`)
	if err := os.Chmod(filepath.Join(cp.ProjectConfigDir(), "tui-home.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .config/opencode/tui.json:\n"+
			"    source: tui-home.json\n")

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	wantTUI := filepath.Join(vmHome, ".config", "opencode", "tui.json")
	if _, ok := cf.Mirror[wantTUI]; ok {
		t.Errorf("mirror path %s must not be in Mirror (home wins), got %v", wantTUI, cf.Mirror)
	}
	if !bytes.Equal(cf.HomeFiles[wantTUI], []byte(`{"theme":"home"}`)) {
		t.Errorf("HomeFiles[%s] = %q, want home content", wantTUI, cf.HomeFiles[wantTUI])
	}
	if cf.Modes[wantTUI].Perm() != 0o755 {
		t.Errorf("home override mode = %o, want 755", cf.Modes[wantTUI].Perm())
	}
}

func TestLoadConfigFilesMirrorWithProvisioningDisabled(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"tui.json",
		`{"theme":"dark"}`,
	)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", hostHome, vmHome, &ui, false)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	wantTUI := filepath.Join(vmHome, ".config", "opencode", "tui.json")
	if !bytes.Equal(cf.Mirror[wantTUI], []byte(`{"theme":"dark"}`)) {
		t.Errorf("mirror must be provisioned even when host config provisioning is disabled, Mirror = %v", cf.Mirror)
	}
}

// TestLoadConfigFilesKeysMergedFirstAndSorted pins the ordering contract of
// ConfigFiles.Keys as returned to callers: the merged config leads, and the
// remaining paths (home/mirror/drop-in, all map-backed and therefore unordered)
// are sorted. A mirror path such as agents/coder.md sorts before the merged
// config, so this fails if the result is ever re-sorted after configKeys and
// the merged config slips out of position.
func TestLoadConfigFilesKeysMergedFirstAndSorted(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	vmHome := t.TempDir()
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"opencode-model.json",
		`{"model":"x"}`,
	)
	if err := os.MkdirAll(
		filepath.Join(configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())), "agents"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(
		t,
		filepath.Join(configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())), "agents"),
		"coder.md",
		"# coder\n",
	)
	testutil.WriteFile(t, cp.ProjectConfigDir(), "gitconfig", "[user]\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .gitconfig:\n"+
			"    source: gitconfig\n")

	ui := termio.NewTestMock(t)
	for range 20 {
		cf, err := LoadConfigFilesForHost(agent.Resolve(a), "", t.TempDir(), vmHome, &ui, true)
		if err != nil {
			t.Fatalf("LoadConfigFilesForHost: %v", err)
		}
		merged := filepath.Join(vmHome, ".config", "opencode", "opencode.jsonc")
		if len(cf.Keys) < 3 {
			t.Fatalf("Keys = %v, want merged, mirror, and home paths", cf.Keys)
		}
		if cf.Keys[0] != merged {
			t.Fatalf("Keys[0] = %q, want merged config %q (a mirror path sorts before it)", cf.Keys[0], merged)
		}
		if !slices.IsSorted(cf.Keys[1:]) {
			t.Fatalf("Keys[1:] must be sorted for a stable order, got %v", cf.Keys)
		}
	}
}

func TestLoadConfigFilesRejectsInvalidProfileName(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(
		agent.Resolve(invalidConfigDirAgent{}),
		"",
		t.TempDir(),
		t.TempDir(),
		&ui,
		true,
	); err == nil {
		t.Fatal("LoadConfigFilesForHost with an invalid config-dir name = nil error, want error")
	}
}

func TestLoadAndDescribePropagateResolveError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	a := invalidConfigDirAgent{}
	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(agent.Resolve(a), "", t.TempDir(), t.TempDir(), &ui, true); err == nil {
		t.Error("expected LoadConfigFilesForHost to propagate the resolution error")
	}
	if _, _, _, _, _, err := Describe(a, "", t.TempDir(), t.TempDir(), &ui, true); err == nil {
		t.Error("expected Describe to propagate the resolution error")
	}
}

// TestLoadConfigFilesWithSnippet verifies that a snippet produces a non-empty
// OpenCode config and includes the opencode.json key.
func TestLoadConfigFilesWithSnippet(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), opencodeTestAgent().ConfigDirName())),
		"opencode-model.json",
		`{"model":"x"}`,
	)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(opencodeTestAgent()), "", t.TempDir(), VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	if !cf.HasSnippets {
		t.Error("expected HasSnippets=true when a snippet exists")
	}
	if len(cf.Merged) == 0 {
		t.Error("expected non-empty OpenCode config")
	}
	if len(cf.Keys) == 0 || cf.Keys[0] != opencodeMergedPath {
		t.Errorf("expected opencode config key in Keys, got %v", cf.Keys)
	}
}

// TestLoadConfigFilesWarnsMissingSource verifies that a home entry referencing a
// non-existent source produces a warning.
func TestLoadConfigFilesWarnsMissingSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .tool/x:\n"+
			"    source: does-not-exist\n")

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(opencodeTestAgent()), "", t.TempDir(), VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	if len(cf.HomeFiles) != 0 {
		t.Errorf("expected no home files for missing source, got %v", cf.HomeFiles)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning for the missing home source")
	}
}

// TestLoadConfigFilesBuildHomeFilesError verifies the error path when the
// home config is malformed.
func TestLoadConfigFilesBuildHomeFilesError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	// parseEntry rejects a non-string source value, surfacing a BuildHomeFilesWithModes error.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .tool/x:\n"+
			"    source: 123\n")

	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(
		agent.Resolve(opencodeTestAgent()),
		"",
		t.TempDir(),
		VMHomeDir,
		&ui,
		true,
	); err == nil {
		t.Error("expected an error for a malformed home source type")
	}
}

// TestLoadConfigFilesBuildHooksError verifies the error path when the manifest
// used by BuildHooks is malformed (invalid hook value).
func TestLoadConfigFilesBuildHooksError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	// A manifest that BuildHomeFilesWithModes accepts but BuildHooks rejects via the
	// hook validation. An unknown hook value is rejected by parseEntry, so use a
	// hook value that is invalid for BuildHooks filtering.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .vpn/x:\n"+
			"    source: s\n"+
			"    hook: 123\n")

	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(
		agent.Resolve(opencodeTestAgent()),
		"",
		t.TempDir(),
		VMHomeDir,
		&ui,
		true,
	); err == nil || !strings.HasPrefix(err.Error(), "build hooks: ") || errors.Unwrap(err) == nil {
		t.Errorf("LoadConfigFilesForHost error = %v, want the wrapped build hooks failure", err)
	}
}

// TestLoadConfigFilesMergedConfigError verifies the error path when merging the
// agent's snippet files fails: the opencode snippet pattern (opencode*.json*)
// matches the .json.yaml name, and the YAML parser accepts the non-finite float
// the JSON marshaler then rejects.
func TestLoadConfigFilesMergedConfigError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.UserConfigDir(), opencodeTestAgent().ConfigDirName())),
		"opencode-inf.json.yaml",
		"v: !!float .inf\n",
	)

	ui := termio.NewTestMock(t)
	_, err := LoadConfigFilesForHost(agent.Resolve(opencodeTestAgent()), "", t.TempDir(), VMHomeDir, &ui, true)
	if err == nil || !strings.Contains(err.Error(), "merge agent config") {
		t.Fatalf("LoadConfigFilesForHost error = %v, want merge agent config error", err)
	}
	// The wrapped cause is the JSON marshaler's rejection of the non-finite
	// float; asserting it kills an ERRORF_WRAP ("%w" -> "%v") that leaves the
	// message identical.
	if _, ok := errors.AsType[*json.UnsupportedValueError](err); !ok {
		t.Errorf("error %v does not wrap *json.UnsupportedValueError", err)
	}
}

// TestLoadConfigFilesReservedTargetError verifies the error path when the
// merged-config path cannot be made relative to the VM home.
func TestLoadConfigFilesReservedTargetError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	_, err := LoadConfigFilesForHost(agent.Resolve(relMergedConfigAgent{}), "", t.TempDir(), t.TempDir(), &ui, true)
	if err == nil || !strings.Contains(err.Error(), "reserved home target") || errors.Unwrap(err) == nil {
		t.Fatalf("LoadConfigFilesForHost error = %v, want the wrapped reserved home target error", err)
	}
}

// TestLoadConfigFilesHomeFilesError verifies that a non-hook home target that
// fails VM-target resolution surfaces from the home-files loader (which runs
// after the hook loader).
func TestLoadConfigFilesHomeFilesError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  /absolute/target:\n"+
			"    source: somefile\n")

	ui := termio.NewTestMock(t)
	_, err := LoadConfigFilesForHost(agent.Resolve(opencodeTestAgent()), "", t.TempDir(), t.TempDir(), &ui, true)
	if err == nil || !strings.HasPrefix(err.Error(), "build home files: ") ||
		!strings.Contains(err.Error(), "must be relative to the home directory") || errors.Unwrap(err) == nil {
		t.Fatalf("LoadConfigFilesForHost error = %v, want the wrapped home-target resolution error", err)
	}
}

// TestLoadConfigFilesProvisionError verifies the error path when evaluating the
// agent's provision rules fails.
func TestLoadConfigFilesProvisionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	restore := evalProvisionRules
	t.Cleanup(func() { evalProvisionRules = restore })
	boom := errors.New("provision boom")
	evalProvisionRules = func(
		[]agent.ProvisionRule,
		string,
		string,
		func(string, []byte, os.FileMode) error,
	) (int, error) {
		return 0, boom
	}

	ui := termio.NewTestMock(t)
	_, err := LoadConfigFilesForHost(agent.Resolve(badProvisionerAgent{}), "", t.TempDir(), t.TempDir(), &ui, true)
	if !errors.Is(err, boom) {
		t.Fatalf("LoadConfigFilesForHost error = %v, want %v", err, boom)
	}
}

func TestLoadConfigFilesNoMergedForNonConfigMerger(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	vmHome := t.TempDir()
	hostHome := t.TempDir()

	// No snippets: a non-ConfigMerger agent yields no merged config.
	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(plainAgent{}), "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf.HasSnippets {
		t.Error("expected HasSnippets=false when no snippet exists")
	}

	// Even with snippet files present, a non-ConfigMerger agent produces no
	// merged config because only ConfigMerger agents merge snippets.
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.UserConfigDir(), plainAgent{}.ConfigDirName())),
		"opencode-model.json",
		`{"model":"x"}`,
	)
	cf, err = LoadConfigFilesForHost(agent.Resolve(plainAgent{}), "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf.HasSnippets {
		t.Error("expected HasSnippets=false for a non-ConfigMerger agent")
	}
	if cf.Merged != nil {
		t.Errorf("expected nil merged config, got %q", cf.Merged)
	}
}

func TestLoadConfigFilesWarnsOnMalformedProvisionRules(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	vmHome := t.TempDir()
	// A real file under the rule dir so the valid pattern copies something.
	cfgPath := filepath.Join(hostHome, ".config/tool/config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(agent.Resolve(badProvisionerAgent{}), "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf == nil {
		t.Fatal("expected a ConfigFiles result")
	}
	// A valid pattern copies host files; malformed ones only warn.
	if len(cf.Provisioned) == 0 {
		t.Errorf("expected some provisioned files, got %v", cf.Provisioned)
	}
	// Every malformed pattern warns, not just the first: the empty pattern and
	// the bare '!' are both reported.
	if n := countWarns(ui.WarnCalls, "empty pattern in provision rule"); n != 1 {
		t.Errorf("warnings = %v, want one empty-pattern warning", ui.WarnCalls)
	}
	if n := countWarns(ui.WarnCalls, "bare '!' in provision rule"); n != 1 {
		t.Errorf("warnings = %v, want one bare-'!' warning", ui.WarnCalls)
	}
}

func TestReservedHomeTargetsRejectsMixedRelAbs(t *testing.T) {
	if _, err := reservedHomeTargets("relative/config.json", "/absolute/vmhome"); err == nil {
		t.Fatal("expected error when mergedPath is not relative to an absolute vmHome")
	}
}

func TestEnsureHookExecutable(t *testing.T) {
	modes := map[string]os.FileMode{
		"/home/dev/.a": 0o600,
		"/home/dev/.b": 0o644,
	}
	ensureHookExecutable(modes, []homeconfig.HookSpec{
		{Target: "/home/dev/.a"},
		{Target: "/home/dev/.b"},
		{Target: "/home/dev/.c"},
	})
	for _, tc := range []struct {
		path string
		want os.FileMode
	}{
		{"/home/dev/.a", 0o700},
		{"/home/dev/.b", 0o744},
		{"/home/dev/.c", 0o700},
	} {
		if got := modes[tc.path].Perm(); got != tc.want {
			t.Errorf("mode of %s = %o, want %o", tc.path, got, tc.want)
		}
	}
}

// TestDescribe verifies Describe returns the merged config, snippet sources,
// host drop-in files and mirror files without touching a VM.
func TestDescribe(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"opencode-model.json",
		`{"model":"x"}`,
	)
	testutil.WriteFile(
		t,
		configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
		"tui.json",
		`{"theme":"dark"}`,
	)
	ocConfig := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.json", `{"a":1}`)

	ui := termio.NewTestMock(t)
	merged, sources, hostFiles, mirrorFiles, source, err := Describe(a, "", hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	wantSource, err := resolveConfigSource(a)
	if err != nil {
		t.Fatalf("ResolveConfigSource: %v", err)
	}
	if !reflect.DeepEqual(source, wantSource) {
		t.Errorf("Describe source = %+v, want the resolved source %+v", source, wantSource)
	}
	if len(merged) == 0 {
		t.Error("Describe returned empty merged config")
	}
	if len(sources) == 0 {
		t.Error("Describe returned no snippet sources")
	}
	if len(hostFiles) == 0 {
		t.Error("Describe returned no host files")
	}
	wantMirror := MirrorFile{
		HostPath: filepath.Join(
			configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())),
			"tui.json",
		),
		VMPath: filepath.Join(vmHome, ".config", "opencode", "tui.json"),
	}
	if !slices.Equal(mirrorFiles, []MirrorFile{wantMirror}) {
		t.Errorf("Describe mirror files = %+v, want exactly %+v", mirrorFiles, wantMirror)
	}
}

func TestHostPathForDst(t *testing.T) {
	got := hostPathForDst("/home/dev/.config/opencode/opencode.json", "/home/user", "/home/dev")
	want := filepath.Join("/home/user", ".config", "opencode", "opencode.json")
	if got != want {
		t.Errorf("hostPathForDst = %q, want %q", got, want)
	}
}

func TestHostPathForDstRoot(t *testing.T) {
	got := hostPathForDst("/home/dev", "/home/user", "/home/dev")
	if got != "/home/user" {
		t.Errorf("hostPathForDst(root) = %q, want /home/user", got)
	}
}

func TestReadVMConfigReadsPaths(t *testing.T) {
	data := []byte("{}")
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		FSValue_: msb.NewTestFS(
			map[string][]byte{
				opencodeMergedPath:     data,
				"/home/dev/.gitconfig": []byte("x=y\n"),
			},
		),
	}
	want := map[string][]byte{
		opencodeMergedPath:     data,
		"/home/dev/.gitconfig": []byte("x=y\n"),
	}
	got := ReadVMConfig(context.Background(), sb,
		[]string{"/home/dev/missing", opencodeMergedPath, "/home/dev/.gitconfig"},
		&termio.Mock{})
	if len(got) != 2 {
		t.Fatalf("expected 2 files read, got %d", len(got))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadVMConfig = %v, want %v", got, want)
	}
}

func TestAgentConfigEqualIgnoresHomeFiles(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  opencodeMergedPath,
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig": []byte("user.name=X\n"),
		},
		Keys: []string{"/home/dev/.gitconfig"},
	}
	vmData := map[string][]byte{
		opencodeMergedPath:     []byte(`{"model":"x"}`),
		"/home/dev/.gitconfig": []byte("user.name=Y\n"),
	}
	if !agentConfigEqual(cf, vmData) {
		t.Error("expected opencode config equality despite differing home file content")
	}
}

func TestAgentConfigEqualDetectsConfigChange(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  opencodeMergedPath,
		HomeFiles:   map[string][]byte{},
	}
	vmData := map[string][]byte{
		opencodeMergedPath: []byte(`{"model":"y"}`),
	}
	if agentConfigEqual(cf, vmData) {
		t.Error("expected mismatch when the opencode config differs")
	}
}

// TestAgentConfigEqualYAMLTargetComparesSemantically verifies that a merged
// config destined for a YAML file is compared as YAML: parsing it as JSON fails,
// which used to read as "always changed" and would report a config change on
// every attach for hermes.
func TestAgentConfigEqualYAMLTargetComparesSemantically(t *testing.T) {
	hermesPath := "/home/dev/.hermes/config.yaml"
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte("model: opus\ntools:\n    web: true\n"),
		MergedPath:  hermesPath,
		HomeFiles:   map[string][]byte{},
	}

	same := map[string][]byte{hermesPath: []byte("model: opus\ntools: {web: true}\n")}
	if !agentConfigEqual(cf, same) {
		t.Error("expected YAML config equality for semantically identical documents")
	}

	changed := map[string][]byte{hermesPath: []byte("model: sonnet\ntools:\n    web: true\n")}
	if agentConfigEqual(cf, changed) {
		t.Error("expected mismatch when the YAML config value differs")
	}
}

func TestAgentConfigEqualNoSnippets(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: false,
		Merged:      nil,
		HomeFiles:   map[string][]byte{},
	}
	if !agentConfigEqual(cf, nil) {
		t.Error("expected equality when no snippets are configured")
	}
}

func TestAgentConfigEqualMirrorMatch(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  opencodeMergedPath,
		Mirror: map[string][]byte{
			"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"dark"}`),
		},
	}
	vmData := map[string][]byte{
		opencodeMergedPath:                    []byte(`{"model":"x"}`),
		"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"dark"}`),
	}
	if !agentConfigEqual(cf, vmData) {
		t.Error("expected equality when merged config and mirror match")
	}
}

func TestAgentConfigEqualMirrorMismatch(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  opencodeMergedPath,
		Mirror: map[string][]byte{
			"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"dark"}`),
		},
	}
	vmData := map[string][]byte{
		opencodeMergedPath:                    []byte(`{"model":"x"}`),
		"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"light"}`),
	}
	if agentConfigEqual(cf, vmData) {
		t.Error("expected mismatch when a mirror file differs")
	}
}

func TestAgentConfigEqualMirrorNoSnippets(t *testing.T) {
	cf := &ConfigFiles{
		Mirror: map[string][]byte{
			"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"dark"}`),
		},
	}
	match := map[string][]byte{"/home/dev/.config/opencode/tui.json": []byte(`{"theme":"dark"}`)}
	if !agentConfigEqual(cf, match) {
		t.Error("expected equality when mirror matches without snippets")
	}
	if agentConfigEqual(cf, nil) {
		t.Error("expected mismatch when the VM mirror is missing")
	}
}

// TestAgentConfigEqualMissingVMConfig verifies that a missing VM opencode
// config is reported as a mismatch.
func TestAgentConfigEqualMissingVMConfig(t *testing.T) {
	cf := &ConfigFiles{HasSnippets: true, Merged: []byte(`{"model":"x"}`)}
	if agentConfigEqual(cf, map[string][]byte{}) {
		t.Error("expected mismatch when the VM config is absent")
	}
}

// TestBuildMergedConfigNoMergerYieldsEmpty verifies the subject's merger guard:
// a subject with no merger produces no merged config even when it carries a
// snippet pattern that would match a host snippet, so capability, not pattern,
// decides whether snippets merge.
func TestBuildMergedConfigNoMergerYieldsEmpty(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "opencode-model.json", `{"model":"x"}`)

	subject := configSubject{
		snippetPattern: "opencode*.json*",
		vmConfigPath:   "/home/dev/.config/opencode/opencode.jsonc",
	}
	got, err := subject.buildMergedConfig(ConfigSource{UserDirs: []string{dir}})
	if err != nil {
		t.Fatalf("buildMergedConfig: %v", err)
	}
	if !reflect.DeepEqual(got, mergedConfig{}) {
		t.Errorf("buildMergedConfig without a merger = %+v, want the zero mergedConfig", got)
	}
}

// TestBuildMirrorNoMergerYieldsEmpty verifies the subject's merger guard for the
// mirror: a subject with no merger mirrors nothing, even when the source config
// dir holds files a pattern-less scan would otherwise mirror.
func TestBuildMirrorNoMergerYieldsEmpty(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "tui.json", `{"theme":"dark"}`)

	mirror := configSubject{}.buildMirror(ConfigSource{UserDirs: []string{dir}})
	if len(mirror) != 0 {
		t.Errorf("buildMirror without a merger = %v, want no entries", mirror)
	}
}

// TestMirrorFilesFromConfigNoMergerYieldsEmpty verifies the display mirror list
// is empty for a subject with no merger.
func TestMirrorFilesFromConfigNoMergerYieldsEmpty(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "tui.json", `{"theme":"dark"}`)

	files := configSubject{}.mirrorFilesFromConfig(ConfigSource{UserDirs: []string{dir}})
	if len(files) != 0 {
		t.Errorf("mirrorFilesFromConfig without a merger = %v, want none", files)
	}
}

// TestLoadProvisionedNoProvisionerYieldsNothing verifies the subject's
// provisioner guard: even with rules present, a subject with no provisioner
// copies nothing.
func TestLoadProvisionedNoProvisionerYieldsNothing(t *testing.T) {
	hostHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hostHome, ".config", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(hostHome, ".config", "tool", "config.json"), `{"a":1}`)

	subject := configSubject{
		provisionRules: []agent.ProvisionRule{{Dir: ".config/tool", Patterns: []string{"**"}}},
		vmHome:         "/home/dev",
	}
	ui := termio.NewTestMock(t)
	provisioned, modes, err := subject.loadProvisioned(hostHome, &ui, true)
	if err != nil {
		t.Fatalf("loadProvisioned: %v", err)
	}
	if len(provisioned) != 0 || len(modes) != 0 {
		t.Errorf("loadProvisioned without a provisioner = %v / %v, want empty", provisioned, modes)
	}
}

// TestProvisionDestinationsNoProvisionerYieldsNone verifies the subject's
// provisioner guard for the removal destinations.
func TestProvisionDestinationsNoProvisionerYieldsNone(t *testing.T) {
	hostHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hostHome, ".config", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(hostHome, ".config", "tool", "config.json"), `{"a":1}`)

	subject := configSubject{
		provisionRules: []agent.ProvisionRule{{Dir: ".config/tool", Patterns: []string{"**"}}},
		vmHome:         "/home/dev",
	}
	if dsts := subject.provisionDestinations(hostHome); len(dsts) != 0 {
		t.Errorf("provisionDestinations without a provisioner = %v, want none", dsts)
	}
}

// TestHostFilesFromProvisionerNoProvisionerYieldsNone verifies the subject's
// provisioner guard for the host-file report.
func TestHostFilesFromProvisionerNoProvisionerYieldsNone(t *testing.T) {
	hostHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hostHome, ".config", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(hostHome, ".config", "tool", "config.json"), `{"a":1}`)

	subject := configSubject{
		provisionRules: []agent.ProvisionRule{{Dir: ".config/tool", Patterns: []string{"**"}}},
		vmHome:         "/home/dev",
	}
	files := subject.hostFilesFromProvisioner(hostHome, &ConfigFiles{})
	if len(files) != 0 {
		t.Errorf("hostFilesFromProvisioner without a provisioner = %v, want none", files)
	}
}

// TestApplyPrecedenceMergedConfigOverridesMirror verifies the precedence
// authority drops the merged-config path from the drop-in copy, the mirror and
// the mode map under hasSnippets, so a lower-priority entry at that path cannot
// shadow or re-mode the merged config.
func TestApplyPrecedenceMergedConfigOverridesMirror(t *testing.T) {
	mergedPath := filepath.Join(string(filepath.Separator), "home", "dev", ".config", "opencode", "opencode.jsonc")
	subject := configSubject{vmConfigPath: mergedPath}

	provisioned := map[string][]byte{mergedPath: []byte("drop-in")}
	homeFiles := map[string][]byte{}
	mirror := map[string][]byte{mergedPath: []byte("mirror")}
	modes := map[string]os.FileMode{mergedPath: 0o600}
	mirrorModes := map[string]os.FileMode{mergedPath: 0o644}
	homeModes := map[string]os.FileMode{}

	subject.applyPrecedence(provisioned, homeFiles, mirror, modes, mirrorModes, homeModes, true)

	if _, ok := provisioned[mergedPath]; ok {
		t.Errorf("merged config path must not remain in Provisioned, got %v", provisioned)
	}
	if _, ok := mirror[mergedPath]; ok {
		t.Errorf("merged config path must not remain in Mirror, got %v", mirror)
	}
	if _, ok := modes[mergedPath]; ok {
		t.Errorf("merged config path must not carry a drop-in/mirror mode, got %v", modes)
	}
}

func reprovisionVMConfigDir(t *testing.T, a agent.Agent) string {
	t.Helper()
	subject := resolveConfigSubject(agent.Resolve(a), VMHomeDir)
	if subject.merger == nil {
		t.Fatal("agent has no ConfigMerger")
	}
	return filepath.Dir(subject.vmConfigPath)
}

func countWarns(warns []string, substr string) int {
	n := 0
	for _, w := range warns {
		if strings.Contains(w, substr) {
			n++
		}
	}
	return n
}

func withConfigBases(t *testing.T, user, project string) {
	t.Helper()
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return configBases{user: user, project: project} }
	t.Cleanup(func() { configpaths.Get = orig })
}

// TestIsDirIsFalseForARegularFile pins that IsDir names only directories.
func TestIsDirIsFalseForARegularFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	testutil.WritePath(t, file, "x")
	if !IsDir(dir) || IsDir(file) || IsDir(filepath.Join(dir, "missing")) {
		t.Errorf("IsDir(dir, file, missing) = %v, %v, %v; want true, false, false",
			IsDir(dir), IsDir(file), IsDir(filepath.Join(dir, "missing")))
	}
}

// TestConfigKeys pins the provisioning and compare order: the merged config
// leads when snippets exist, then every home, mirror and provisioned path in
// sorted order.
func TestConfigKeys(t *testing.T) {
	home := map[string][]byte{"/home/dev/.gitconfig": nil}
	mirror := map[string][]byte{"/home/dev/.config/opencode/tui.json": nil}
	provisioned := map[string][]byte{"/home/dev/.config/tool/a.toml": nil, "/home/dev/.config/tool/b.toml": nil}
	rest := []string{
		"/home/dev/.config/opencode/tui.json",
		"/home/dev/.config/tool/a.toml",
		"/home/dev/.config/tool/b.toml",
		"/home/dev/.gitconfig",
	}
	if got := configKeys(opencodeMergedPath, false, home, mirror, provisioned); !slices.Equal(got, rest) {
		t.Errorf("configKeys without snippets = %v, want %v", got, rest)
	}
	want := append([]string{opencodeMergedPath}, rest...)
	if got := configKeys(opencodeMergedPath, true, home, mirror, provisioned); !slices.Equal(got, want) {
		t.Errorf("configKeys with snippets = %v, want %v", got, want)
	}
}

// TestAgentConfigEqualEmptyMirrorFileMustExist pins that a mirror file is
// compared by presence too, so an empty one missing from the VM is a change.
func TestAgentConfigEqualEmptyMirrorFileMustExist(t *testing.T) {
	cf := &ConfigFiles{Mirror: map[string][]byte{"/home/dev/.config/opencode/empty.json": {}}}
	if agentConfigEqual(cf, map[string][]byte{}) {
		t.Error("agentConfigEqual = true, want false when an empty mirror file is missing in the VM")
	}
}

// TestResolveConfigSourceOneNamespaceAcrossScopes pins the global namespace
// decision: a per-slug primary dir wins over a generic legacy dir, and the chosen
// primary namespace applies to every scope (the generic legacy dir is dropped).
func TestResolveConfigSourceOneNamespaceAcrossScopes(t *testing.T) {
	generic := t.TempDir()
	perSlug := t.TempDir()
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(generic, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(perSlug, "opencode2"), 0o755); err != nil {
		t.Fatal(err)
	}
	scopes := []configpaths.ConfigScope{
		{Kind: configpaths.ScopeGenericUser, Dir: generic},
		{Kind: configpaths.ScopePerSlugUser, Dir: perSlug},
		{Kind: configpaths.ScopeProject, Dir: project},
	}

	a, _ := agent.Lookup("opencode2")
	src, err := ResolveConfigSource(a, scopes)
	if err != nil {
		t.Fatalf("ResolveConfigSource: %v", err)
	}
	if src.Fallback != "" {
		t.Errorf("Fallback = %q, want empty (the primary namespace wins globally)", src.Fallback)
	}
	wantUserDirs := []string{
		filepath.Join(generic, "opencode2"),
		filepath.Join(perSlug, "opencode2"),
	}
	if !slices.Equal(src.UserDirs, wantUserDirs) {
		t.Errorf("UserDirs = %v, want %v", src.UserDirs, wantUserDirs)
	}
	if want := filepath.Join(project, "opencode2"); src.ProjectDir != want {
		t.Errorf("ProjectDir = %q, want %q", src.ProjectDir, want)
	}
}

// TestResolveConfigSourceFallbackWarningNamesLegacy pins that a legacy namespace
// found in any scope selects that namespace for every scope and that the scope
// description names the legacy dir actually read.
func TestResolveConfigSourceFallbackWarningNamesLegacy(t *testing.T) {
	generic := t.TempDir()
	perSlug := t.TempDir()
	project := t.TempDir()
	legacy := filepath.Join(generic, "opencode")
	if err := os.Mkdir(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	scopes := []configpaths.ConfigScope{
		{Kind: configpaths.ScopeGenericUser, Dir: generic},
		{Kind: configpaths.ScopePerSlugUser, Dir: perSlug},
		{Kind: configpaths.ScopeProject, Dir: project},
	}

	a, _ := agent.Lookup("opencode2")
	src, err := ResolveConfigSource(a, scopes)
	if err != nil {
		t.Fatalf("ResolveConfigSource: %v", err)
	}
	if src.Fallback != "opencode" {
		t.Errorf("Fallback = %q, want opencode", src.Fallback)
	}
	described := describeConfigScopes(src.UserDirs, src.ProjectDir, true)
	if !strings.Contains(described, legacy) {
		t.Errorf("describeConfigScopes = %q, want the legacy dir %q", described, legacy)
	}
}

// TestResolveConfigSourceDegradesWhenCwdUnreadable pins the graceful degrade:
// a relative project scope dir whose Abs resolution fails (the CWD cannot be
// read) must be kept as-is rather than aborting resolution, mirroring
// ProjectConfigDirAbs.
func TestResolveConfigSourceDegradesWhenCwdUnreadable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	generic := t.TempDir()
	if err := os.Mkdir(filepath.Join(generic, "opencode2"), 0o755); err != nil {
		t.Fatal(err)
	}
	scopes := []configpaths.ConfigScope{
		{Kind: configpaths.ScopeGenericUser, Dir: generic},
		{Kind: configpaths.ScopeProject, Dir: ".agents-sandbox"},
	}

	a, _ := agent.Lookup("opencode2")
	src, err := ResolveConfigSource(a, scopes)
	if err != nil {
		t.Fatalf("ResolveConfigSource: %v", err)
	}
	if want := filepath.Join(".agents-sandbox", "opencode2"); src.ProjectDir != want {
		t.Errorf("ProjectDir = %q, want the relative candidate %q", src.ProjectDir, want)
	}
	if src.Fallback != "" {
		t.Errorf("Fallback = %q, want empty (the primary user dir exists)", src.Fallback)
	}
}

// agentConfigEqual reports whether no agent config file differs from the VM.
func agentConfigEqual(cf *ConfigFiles, vmData map[string][]byte) bool {
	return len(ChangedAgentConfigPaths(cf, vmData)) == 0
}

// TestChangedAgentConfigPathsNamesEveryDifferingFile pins that the change is
// reported per guest file: the merged config first, then the differing or
// missing mirror files sorted, leaving out the files that match.
func TestChangedAgentConfigPathsNamesEveryDifferingFile(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  opencodeMergedPath,
		Mirror: map[string][]byte{
			"/home/dev/.config/opencode/tui.json":       []byte(`{"theme":"dark"}`),
			"/home/dev/.config/opencode/plugin/b.ts":    []byte("b"),
			"/home/dev/.config/opencode/plugin/a.ts":    []byte("a"),
			"/home/dev/.config/opencode/plugin/same.ts": []byte("same"),
		},
	}
	vmData := map[string][]byte{
		opencodeMergedPath:                          []byte(`{"model":"y"}`),
		"/home/dev/.config/opencode/tui.json":       []byte(`{"theme":"light"}`),
		"/home/dev/.config/opencode/plugin/a.ts":    []byte("old"),
		"/home/dev/.config/opencode/plugin/same.ts": []byte("same"),
	}
	want := []string{
		opencodeMergedPath,
		"/home/dev/.config/opencode/plugin/a.ts",
		"/home/dev/.config/opencode/plugin/b.ts",
		"/home/dev/.config/opencode/tui.json",
	}
	if got := ChangedAgentConfigPaths(cf, vmData); !slices.Equal(got, want) {
		t.Errorf("ChangedAgentConfigPaths = %q, want %q", got, want)
	}
}
