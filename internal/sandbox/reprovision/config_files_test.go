package reprovision

import (
	"bytes"
	"context"
	"errors"
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
	cf, err := LoadConfigFiles(a, &ui, true)
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
	cf, err := LoadConfigFiles(a, &ui, true)
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

// TestProvisionChownsHomeFiles verifies that Provision writes the home files and
// the merged opencode config, then chowns every provisioned path to dev:dev so
// the files are readable by the runtime user.
func TestProvisionChownsHomeFiles(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  opencodeMergedPath,
		HomeFiles: map[string][]byte{
			"/home/dev/.gitconfig":            []byte("user.name=X\n"),
			"/home/dev/.config/tool/cfg.toml": []byte("k=v\n"),
		},
		Modes: map[string]os.FileMode{"/home/dev/.gitconfig": 0o755},
	}
	fs := msb.NewTestFS(nil, nil)
	var calls []string
	var execCalls []string
	sb := &msb.MockSandbox{FSValue_: fs, ShellCalls: &calls, ExecCalls: &execCalls}

	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	for path := range cf.HomeFiles {
		if _, ok := fs.Writes[path]; !ok {
			t.Errorf("home file %s was not written", path)
		}
	}
	if _, ok := fs.Writes[opencodeMergedPath]; !ok {
		t.Error("opencode config was not written")
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 chown shell call, got %d: %v", len(calls), calls)
	}
	wantChown := "chown -R dev:dev /home /home/dev /home/dev/.config /home/dev/.config/opencode " +
		"/home/dev/.config/opencode/opencode.jsonc /home/dev/.config/tool /home/dev/.config/tool/cfg.toml " +
		"/home/dev/.gitconfig"
	if calls[0] != wantChown {
		t.Errorf("shell command = %q, want %q", calls[0], wantChown)
	}
	if !slices.Contains(execCalls, "chmod 0755 /home/dev/.gitconfig") {
		t.Errorf("exec calls = %v, want home-file chmod", execCalls)
	}
}

func TestProvisionReturnsHomeChmodError(t *testing.T) {
	path := "/home/dev/.gitconfig"
	cf := &ConfigFiles{
		HomeFiles: map[string][]byte{path: []byte("user.name=X\n")},
		Modes:     map[string]os.FileMode{path: 0o755},
	}
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{
		FSValue_: fs,
		ExecOut: map[string]msb.ShellResult{
			"chmod 0755 " + path: msb.NewTestResult(false, 1, "", "chmod failed", nil),
		},
	}

	err := Provision(context.Background(), sb, cf)
	if err == nil || !strings.Contains(err.Error(), "chmod home file") {
		t.Fatalf("Provision error = %v, want home-file chmod error", err)
	}
}

// TestProvisionNoFilesSkipsChown verifies that Provision does not run a chown
// when there are no files to provision.
func TestProvisionNoFilesSkipsChown(t *testing.T) {
	var calls []string
	sb := &msb.MockSandbox{FSValue_: msb.NewTestFS(nil, nil), ShellCalls: &calls}
	if err := Provision(context.Background(), sb, &ConfigFiles{}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("expected no shell call, got %v", calls)
	}
}

// TestProvisionJoinsErrors verifies that when a write fails and the deferred
// chown also fails, both errors are surfaced via errors.Join.
func TestProvisionJoinsErrors(t *testing.T) {
	writeErr := errors.New("write boom")
	chownErr := errors.New("chown boom")
	cf := &ConfigFiles{HomeFiles: map[string][]byte{"/home/dev/.gitconfig": []byte("x\n")}}
	fs := msb.NewTestFS(nil, nil)
	fs.WriteErr = writeErr
	sb := &msb.MockSandbox{FSValue_: fs, ShellErr: chownErr}

	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, writeErr) {
		t.Errorf("Provision error does not wrap write error: %v", err)
	}
	if !errors.Is(err, chownErr) {
		t.Errorf("Provision error does not wrap chown error: %v", err)
	}
}

// TestLoadConfigFilesProvisioning verifies the enabled host drop-in copy: host files
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
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
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
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "opencode-model.json", `{"model":"x"}`)

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
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
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
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "opencode-model.json", `{"model":"x"}`)

	// Host files the drop-in copy would pick up: every config-family filename
	// must be excluded, while other.json remains a normal drop-in file.
	ocConfig := filepath.Join(hostHome, ".config/opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range configFamilyNames(opencodeTestAgent()) {
		testutil.WriteFile(t, ocConfig, name, `{"model":"host"}`)
	}
	testutil.WriteFile(t, ocConfig, "other.json", `{"host":1}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}

	mergedPath := filepath.Join(vmHome, ".config", "opencode", "opencode.jsonc")
	for _, name := range configFamilyNames(opencodeTestAgent()) {
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

// TestLoadConfigFilesShadowingCopiedWithoutSnippets verifies that without
// snippets the host opencode.jsonc is drop-in copied as the config.
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
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
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

// TestLoadConfigFilesProvisioningDisabled verifies that with host config
// provisioning disabled the drop-in copy is skipped entirely, the config-file
// family and auth.json are marked for removal, and snippet merging still works.
func TestLoadConfigFilesProvisioningDisabled(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")

	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "opencode-model.json", `{"model":"x"}`)
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
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, false)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}

	if len(cf.Provisioned) != 0 {
		t.Errorf("expected no drop-in copy when provisioning disabled, got %v", cf.Provisioned)
	}
	for _, name := range configFamilyNames(opencodeTestAgent()) {
		want := filepath.Join(vmHome, ".config", "opencode", name)
		if !slices.Contains(cf.Remove, want) {
			t.Errorf("expected config file %s in Remove, got %v", want, cf.Remove)
		}
	}
	// A login inside the VM must survive restarts: only VM files identical
	// to the host file, i.e. copies from an earlier provisioning run, are
	// marked for removal.
	authPath := filepath.Join(vmHome, ".local", "share", "opencode", "auth.json")
	if slices.Contains(cf.Remove, authPath) {
		t.Errorf("Remove = %v, must not unconditionally contain auth.json", cf.Remove)
	}
	if got := string(cf.RemoveHostCopies[authPath]); got != `{"t":"x"}` {
		t.Errorf("RemoveHostCopies[auth.json] = %q, want host content", got)
	}
	if !cf.HasSnippets || len(cf.Merged) == 0 {
		t.Error("expected merged config to be built despite disabled provisioning")
	}
}

// TestProvisionRemovesStalePaths verifies that Provision removes the marked
// stale paths before writing the merged config.
func TestProvisionRemovesStalePaths(t *testing.T) {
	cf := &ConfigFiles{
		HasSnippets: true,
		Merged:      []byte(`{"model":"x"}`),
		MergedPath:  opencodeMergedPath,
		Remove: configFileFamilyPaths(
			opencodeMergedPath,
			configFamilyNames(opencodeTestAgent()),
		),
	}
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(fs.Removed) != len(cf.Remove) {
		t.Fatalf("expected %d removals, got %v", len(cf.Remove), fs.Removed)
	}
	for i, p := range cf.Remove {
		if i >= len(fs.Removed) || fs.Removed[i] != p {
			t.Errorf("Remove order = %v, want %v", fs.Removed, cf.Remove)
			break
		}
	}
	if fs.Writes[opencodeMergedPath] == nil {
		t.Error("expected merged config written after removal")
	}
}

// TestPiSensitiveFilesAreOnlyRemovedWhenTheyAreHostCopies verifies that Pi's
// provisioning-excluded secrets are never removed unconditionally (a login
// inside the VM must survive restarts); only VM files identical to the host
// file, i.e. stale copies from an older provisioning run, are marked.
func TestPiSensitiveFilesAreOnlyRemovedWhenTheyAreHostCopies(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("pi")
	hostHome := t.TempDir()
	vmHome := t.TempDir()
	hostPiDir := filepath.Join(hostHome, ".pi", "agent")
	if err := os.MkdirAll(hostPiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, hostPiDir, "auth.json", `{"secret":"host"}`)
	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, false)
	if err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(vmHome, ".pi", "agent", "auth.json")
	modelsPath := filepath.Join(vmHome, ".pi", "agent", "models.json")
	for _, path := range []string{authPath, modelsPath} {
		if slices.Contains(cf.Remove, path) {
			t.Errorf("Remove = %v, must not unconditionally contain %s", cf.Remove, path)
		}
	}
	if got := string(cf.RemoveHostCopies[authPath]); got != `{"secret":"host"}` {
		t.Errorf("RemoveHostCopies[auth.json] = %q, want host content", got)
	}
	if _, ok := cf.RemoveHostCopies[modelsPath]; ok {
		t.Errorf("RemoveHostCopies = %v, want no entry for models.json missing on the host", cf.RemoveHostCopies)
	}
}

// TestProvisionRemovesOnlyStaleHostCopies verifies that Provision removes a
// VM file marked as a potential host copy only when its content matches.
func TestProvisionRemovesOnlyStaleHostCopies(t *testing.T) {
	staleCopy := "/home/agent/.pi/agent/auth.json"
	vmLogin := "/home/agent/.pi/agent/models.json"
	missing := "/home/agent/.pi/agent/other.json"
	cf := &ConfigFiles{
		RemoveHostCopies: map[string][]byte{
			staleCopy: []byte("host"),
			vmLogin:   []byte("host"),
			missing:   []byte("host"),
		},
	}
	fs := msb.NewTestFS(map[string][]byte{
		staleCopy: []byte("host"),
		vmLogin:   []byte("logged in inside the VM"),
	}, nil)
	sb := &msb.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !slices.Equal(fs.Removed, []string{staleCopy}) {
		t.Errorf("Removed = %v, want only %s", fs.Removed, staleCopy)
	}
}

func TestClaudeCredentialsAreNotRemovedFromPersistentHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("claude-code")
	hostHome := t.TempDir()
	vmHome := t.TempDir()
	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, false)
	if err != nil {
		t.Fatal(err)
	}
	credentialsPath := filepath.Join(vmHome, ".claude", ".credentials.json")
	if slices.Contains(cf.Remove, credentialsPath) {
		t.Fatalf("Claude credentials would be removed from persistent home: %v", cf.Remove)
	}
}

// TestProvisionWritesProvisioned verifies that Provision writes host drop-in
// files just like home files.
func TestProvisionWritesProvisioned(t *testing.T) {
	cf := &ConfigFiles{
		Provisioned: map[string][]byte{
			"/home/dev/.config/opencode/opencode.json":  []byte(`{"a":1}`),
			"/home/dev/.local/share/opencode/auth.json": []byte(`{"t":"x"}`),
		},
	}
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for p := range cf.Provisioned {
		if _, ok := fs.Writes[p]; !ok {
			t.Errorf("provisioned file %s was not written", p)
		}
	}
}

func TestProvisionAppliesProvisionedFileMode(t *testing.T) {
	path := "/home/dev/.config/opencode/bin/launcher"
	cf := &ConfigFiles{
		Provisioned: map[string][]byte{path: []byte("#!/bin/sh\n")},
		Modes:       map[string]os.FileMode{path: 0o755},
	}
	fs := msb.NewTestFS(nil, nil)
	var execCalls []string
	sb := &msb.MockSandbox{
		FSValue_:  fs,
		ExecCalls: &execCalls,
	}

	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !slices.Contains(execCalls, "chmod 0755 "+path) {
		t.Errorf("exec calls = %v, want chmod for %s", execCalls, path)
	}
}

func TestProvisionReturnsChmodError(t *testing.T) {
	path := "/home/dev/.config/opencode/bin/launcher"
	cf := &ConfigFiles{
		Provisioned: map[string][]byte{path: []byte("#!/bin/sh\n")},
		Modes:       map[string]os.FileMode{path: 0o755},
	}
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{
		FSValue_: fs,
		ExecOut: map[string]msb.ShellResult{
			"chmod 0755 " + path: msb.NewTestResult(false, 1, "", "chmod failed", nil),
		},
	}

	err := Provision(context.Background(), sb, cf)
	if err == nil || !strings.Contains(err.Error(), "chmod") {
		t.Fatalf("Provision error = %v, want chmod error", err)
	}
}

func TestProvisionReturnsChmodExecError(t *testing.T) {
	path := "/home/dev/.config/opencode/bin/launcher"
	wantErr := errors.New("exec failed")
	cf := &ConfigFiles{
		Provisioned: map[string][]byte{path: []byte("#!/bin/sh\n")},
		Modes:       map[string]os.FileMode{path: 0o755},
	}
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{FSValue_: fs, ExecErr: wantErr}

	err := Provision(context.Background(), sb, cf)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Provision error = %v, want %v", err, wantErr)
	}
}

func TestProvisionReturnsMirrorChmodError(t *testing.T) {
	path := "/home/dev/.config/opencode/bin/launcher"
	cf := &ConfigFiles{
		Mirror: map[string][]byte{path: []byte("#!/bin/sh\n")},
		Modes:  map[string]os.FileMode{path: 0o755},
	}
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{
		FSValue_: fs,
		ExecOut: map[string]msb.ShellResult{
			"chmod 0755 " + path: msb.NewTestResult(false, 1, "", "chmod failed", nil),
		},
	}

	err := Provision(context.Background(), sb, cf)
	if err == nil || !strings.Contains(err.Error(), "chmod mirror file") {
		t.Fatalf("Provision error = %v, want mirror-file chmod error", err)
	}
}

// TestProvisionWritesMirror verifies that Provision writes the verbatim mirror
// files just like home files and drop-in copies.
func TestProvisionWritesMirror(t *testing.T) {
	launcherPath := "/home/dev/.config/opencode/bin/launcher"
	cf := &ConfigFiles{
		Mirror: map[string][]byte{
			"/home/dev/.config/opencode/tui.json":        []byte(`{"theme":"dark"}`),
			"/home/dev/.config/opencode/agents/coder.md": []byte("# coder\n"),
			launcherPath: []byte("#!/bin/sh\n"),
		},
		Modes: map[string]os.FileMode{launcherPath: 0o755},
	}
	fs := msb.NewTestFS(nil, nil)
	var execCalls []string
	sb := &msb.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}, ExecCalls: &execCalls}

	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for p := range cf.Mirror {
		if _, ok := fs.Writes[p]; !ok {
			t.Errorf("mirror file %s was not written", p)
		}
	}
	if !slices.Contains(execCalls, "chmod 0755 "+launcherPath) {
		t.Errorf("exec calls = %v, want chmod for %s", execCalls, launcherPath)
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
		configpaths.MustDir(cp.ProjectAgentConfigDir(a)),
		"settings.json",
		`{"model":"claude-sonnet-4-5"}`,
	)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
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
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "config-model.toml", "model = \"gpt-5\"\n")
	testutil.WriteFile(
		t,
		configpaths.MustDir(cp.ProjectAgentConfigDir(a)),
		"config-sandbox.toml",
		"[sandbox_workspace_write]\nnetwork_access = true\n",
	)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, t.TempDir(), vmHome, &ui, true)
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
	if !AgentConfigEqual(cf, map[string][]byte{wantMerged: reformatted}) {
		t.Error("AgentConfigEqual should compare the TOML config semantically")
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
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "config-model.yaml", "model: opus\n")

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, t.TempDir(), vmHome, &ui, true)
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
	if !AgentConfigEqual(cf, map[string][]byte{wantMerged: reformatted}) {
		t.Error("AgentConfigEqual should compare the YAML config semantically")
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
	_, err := LoadConfigFilesForHost(a, t.TempDir(), vmHome, &ui, true)
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
	if _, err := LoadConfigFilesForHost(a, t.TempDir(), vmHome, &ui, true); err == nil {
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
	cf, err := LoadConfigFilesForHost(plainAgent{}, t.TempDir(), vmHome, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if cf.HasSnippets {
		t.Error("expected HasSnippets=false for a non-ConfigMerger agent")
	}
}

// TestLoadConfigFilesNoHostCopiesForNonProvisioner verifies that an agent
// without provision rules has no stale host copies to remove when host config
// provisioning is disabled.
func TestLoadConfigFilesNoHostCopiesForNonProvisioner(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(plainAgent{}, t.TempDir(), t.TempDir(), &ui, false)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	if len(cf.RemoveHostCopies) != 0 {
		t.Errorf("RemoveHostCopies = %v, want none for a non-Provisioner agent", cf.RemoveHostCopies)
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
	files := hostFilesFromProvisioner(a, hostHome, vmHome, cf)

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

// TestHostFilesFromProvisionerNoSnippets verifies that without snippets the
// drop-in files are not marked merged.
func TestLoadConfigFilesMirror(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	hostHome := t.TempDir()
	vmHome := t.TempDir()

	a, _ := agent.Lookup("opencode")
	// Snippet (merged, not mirrored) plus mirror files at top level and nested.
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "opencode-model.json", `{"model":"x"}`)
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "tui.json", `{"theme":"dark"}`)
	if err := os.Chmod(filepath.Join(configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "tui.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(
		filepath.Join(configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "agents"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(
		t,
		filepath.Join(configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "agents"),
		"coder.md",
		"# coder\n",
	)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
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
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "tui.json", `{"theme":"mirror"}`)
	if err := os.Chmod(filepath.Join(configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "tui.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	ocConfig := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "tui.json", `{"theme":"host"}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
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
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "tui.json", `{"theme":"mirror"}`)
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
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, true)
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
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "tui.json", `{"theme":"dark"}`)

	ui := termio.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(a, hostHome, vmHome, &ui, false)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	wantTUI := filepath.Join(vmHome, ".config", "opencode", "tui.json")
	if !bytes.Equal(cf.Mirror[wantTUI], []byte(`{"theme":"dark"}`)) {
		t.Errorf("mirror must be provisioned even when host config provisioning is disabled, Mirror = %v", cf.Mirror)
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
	files := hostFilesFromProvisioner(a, hostHome, vmHome, cf)
	if len(files) != 1 || files[0].Merged {
		t.Errorf("hostFilesFromProvisioner = %+v, want 1 non-merged file", files)
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
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "opencode-model.json", `{"model":"x"}`)
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "tui.json", `{"theme":"dark"}`)
	ocConfig := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(ocConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ocConfig, "opencode.json", `{"a":1}`)

	ui := termio.NewTestMock(t)
	merged, sources, hostFiles, mirrorFiles, _, err := Describe(a, hostHome, vmHome, &ui, true)
	if err != nil {
		t.Fatalf("Describe: %v", err)
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
		HostPath: filepath.Join(configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "tui.json"),
		VMPath:   filepath.Join(vmHome, ".config", "opencode", "tui.json"),
	}
	if !slices.ContainsFunc(mirrorFiles, func(mf MirrorFile) bool {
		return mf.HostPath == wantMirror.HostPath && mf.VMPath == wantMirror.VMPath
	}) {
		t.Errorf("Describe mirror files = %+v, want to include %+v", mirrorFiles, wantMirror)
	}
}

func TestReservedHomeTargetsRejectsMixedRelAbs(t *testing.T) {
	if _, err := reservedHomeTargets("relative/config.json", "/absolute/vmhome"); err == nil {
		t.Fatal("expected error when mergedPath is not relative to an absolute vmHome")
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
	testutil.WriteFile(t, configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "opencode-model.json", `{"model":"x"}`)
	if err := os.MkdirAll(
		filepath.Join(configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "agents"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(
		t,
		filepath.Join(configpaths.MustDir(cp.ProjectAgentConfigDir(a)), "agents"),
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
		cf, err := LoadConfigFilesForHost(a, t.TempDir(), vmHome, &ui, true)
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
	cf, err := LoadConfigFiles(a, &ui, true)
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
	cf, err := LoadConfigFiles(a, &ui, true)
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

func reprovisionVMConfigDir(t *testing.T, a agent.Agent) string {
	t.Helper()
	cm, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("agent has no ConfigMerger")
	}
	return filepath.Dir(cm.VMConfigPath(VMHomeDir))
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

func TestLoadConfigFilesInfosWhenNoLauncherConfigDirExists(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	a, _ := agent.Lookup("opencode2")

	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFiles(a, &ui, true); err != nil {
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
			if _, err := LoadConfigFiles(a, &ui, true); err != nil {
				t.Fatalf("LoadConfigFiles: %v", err)
			}
			if len(ui.InfoCalls) != 0 {
				t.Errorf("unexpected info: %v", ui.InfoCalls)
			}
		})
	}
}
