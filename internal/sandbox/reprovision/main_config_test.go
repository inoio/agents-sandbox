package reprovision

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

// codexMainConfig is codex's main config in the VM, and codexBase where its
// base is recorded.
const (
	codexMainConfig = "/home/dev/.codex/config.toml"
	codexBase       = "/home/dev/.local/state/agents-sandbox/config-base/.codex/config.toml"
)

// agentTrust is a setting codex writes into its own main config.
const agentTrust = "[projects.\"/workspace\"]\ntrust_level = \"trusted\"\n"

func codexConfigFiles(snippet string) *ConfigFiles {
	return &ConfigFiles{
		HasSnippets:    snippet != "",
		Merged:         bytesOrNil(snippet),
		MergedPath:     codexMainConfig,
		MergedBasePath: codexBase,
	}
}

func provisionInto(t *testing.T, fs *msbtest.TestFS, cf *ConfigFiles) []string {
	t.Helper()
	var execCalls []string
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}, ExecCalls: &execCalls}
	if err := Provision(context.Background(), sb, cf, testLogger(t)); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	return execCalls
}

func TestProvisionKeepsTheAgentsOwnMainConfigWithoutSnippets(t *testing.T) {
	fs := msbtest.NewTestFS(map[string][]byte{codexMainConfig: []byte(agentTrust)})

	provisionInto(t, fs, codexConfigFiles(""))

	if _, written := fs.Writes[codexMainConfig]; written {
		t.Error("the agent's own main config was rewritten")
	}
	if slices.Contains(fs.Removed, codexMainConfig) {
		t.Error("the agent's own main config was removed")
	}
}

func TestProvisionMergesASnippetChangeBesideTheAgentsSettings(t *testing.T) {
	fs := msbtest.NewTestFS(map[string][]byte{
		codexMainConfig: []byte("model = \"gpt-5\"\n" + agentTrust),
		codexBase:       []byte("model = \"gpt-5\"\n"),
	})

	execCalls := provisionInto(t, fs, codexConfigFiles("model = \"gpt-5.5\"\n"))

	written := string(fs.Writes[codexMainConfig])
	if !strings.Contains(written, "gpt-5.5") || !strings.Contains(written, "trusted") {
		t.Errorf("main config = %q, want the new model and the agent's trust", written)
	}
	if string(fs.Writes[codexBase]) != "model = \"gpt-5.5\"\n" {
		t.Errorf("base = %q, want the launcher's new content", fs.Writes[codexBase])
	}
	if !slices.Contains(execCalls, "chmod 0600 "+codexBase) {
		t.Errorf("exec calls = %v, want the base kept private", execCalls)
	}
}

func TestProvisionLeavesTheMainConfigWhenTheSnippetsAreUnchanged(t *testing.T) {
	fs := msbtest.NewTestFS(map[string][]byte{
		codexMainConfig: []byte("model = \"gpt-5\"\n" + agentTrust),
		codexBase:       []byte("model = \"gpt-5\"\n"),
	})

	provisionInto(t, fs, codexConfigFiles("model = \"gpt-5\"\n"))

	if _, written := fs.Writes[codexMainConfig]; written {
		t.Error("an unchanged snippet rewrote the main config")
	}
}

func TestProvisionDropsTheLauncherKeysOfRemovedSnippets(t *testing.T) {
	fs := msbtest.NewTestFS(map[string][]byte{
		codexMainConfig: []byte("model = \"gpt-5\"\n" + agentTrust),
		codexBase:       []byte("model = \"gpt-5\"\n"),
	})

	provisionInto(t, fs, codexConfigFiles(""))

	written := string(fs.Writes[codexMainConfig])
	if strings.Contains(written, "gpt-5") || !strings.Contains(written, "trusted") {
		t.Errorf("main config = %q, want the snippet's model gone and the trust kept", written)
	}
	if !slices.Contains(fs.Removed, codexBase) {
		t.Errorf("removed = %v, want the base removed with the last snippet", fs.Removed)
	}
}

func TestProvisionReconcilesTheDropInCopyOfTheMainConfig(t *testing.T) {
	fs := msbtest.NewTestFS(map[string][]byte{codexMainConfig: []byte(agentTrust)})
	cf := codexConfigFiles("")
	cf.Provisioned = map[string][]byte{
		codexMainConfig:              []byte("model = \"gpt-5\"\n"),
		"/home/dev/.codex/AGENTS.md": []byte("Be brief.\n"),
	}

	provisionInto(t, fs, cf)

	if string(fs.Writes["/home/dev/.codex/AGENTS.md"]) != "Be brief.\n" {
		t.Error("the drop-in's other files were not written")
	}

	written := string(fs.Writes[codexMainConfig])
	if !strings.Contains(written, "gpt-5") || !strings.Contains(written, "trusted") {
		t.Errorf("main config = %q, want the host copy's model beside the agent's trust", written)
	}
	if string(fs.Writes[codexBase]) != "model = \"gpt-5\"\n" {
		t.Errorf("base = %q, want the drop-in copy", fs.Writes[codexBase])
	}
}

func TestProvisionReplacesAMainConfigItCannotDecode(t *testing.T) {
	fs := msbtest.NewTestFS(map[string][]byte{codexMainConfig: []byte("model = [unterminated\n")})

	provisionInto(t, fs, codexConfigFiles("model = \"gpt-5\"\n"))

	if string(fs.Writes[codexMainConfig]) != "model = \"gpt-5\"\n" {
		t.Errorf("main config = %q, want the launcher's content", fs.Writes[codexMainConfig])
	}
}

// TestProvisionLeavesAMainConfigItCannotRead: the agent's settings may be in a
// file a transient read failure hides, so neither it nor its base is touched.
func TestProvisionLeavesAMainConfigItCannotRead(t *testing.T) {
	fs := msbtest.NewTestFS(map[string][]byte{codexMainConfig: []byte(agentTrust)})
	fs.ReadErr = errors.New("read boom")

	provisionInto(t, fs, codexConfigFiles("model = \"gpt-5\"\n"))

	if len(fs.Writes) != 0 || len(fs.Removed) != 0 {
		t.Errorf(
			"writes = %v, removed = %v; want the unreadable main config and its base left alone",
			fs.Writes,
			fs.Removed,
		)
	}
}

func TestProvisionLeavesAMainConfigWhoseExistenceCannotBeChecked(t *testing.T) {
	fs := existsFailingFS{msbtest.NewTestFS(nil)}

	sb := &msbtest.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}
	if err := Provision(context.Background(), sb, codexConfigFiles("model = \"gpt-5\"\n"), testLogger(t)); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(fs.Writes) != 0 {
		t.Errorf("writes = %v, want nothing written", fs.Writes)
	}
}

// existsFailingFS cannot tell whether a file exists.
type existsFailingFS struct{ *msbtest.TestFS }

func (existsFailingFS) Exists(context.Context, string) (bool, error) {
	return false, errors.New("exists boom")
}

func TestProvisionRemovesAMainConfigLeftWithoutKeys(t *testing.T) {
	fs := msbtest.NewTestFS(map[string][]byte{
		codexMainConfig: []byte("model = \"gpt-5\"\n"),
		codexBase:       []byte("model = \"gpt-5\"\n"),
	})

	provisionInto(t, fs, codexConfigFiles(""))

	if !slices.Contains(fs.Removed, codexMainConfig) {
		t.Errorf("removed = %v, want the main config holding only dropped launcher keys removed", fs.Removed)
	}
}

func TestProvisionReportsABaseWriteFailure(t *testing.T) {
	cf := codexConfigFiles("model = \"gpt-5\"\n")
	sb := &msbtest.MockSandbox{
		FSValue_:   pathFailingFS{TestFS: msbtest.NewTestFS(nil), failPath: codexBase},
		ShellCalls: &[]string{},
	}

	if err := Provision(context.Background(), sb, cf, testLogger(t)); err == nil {
		t.Error("a failed base write was not reported")
	}
}

func TestProvisionWrapsAMainConfigWriteFailure(t *testing.T) {
	fs := msbtest.NewTestFS(nil)
	fs.WriteErr = errors.New("write boom")
	sb := &msbtest.MockSandbox{FSValue_: fs, ShellCalls: &[]string{}}

	if err := Provision(
		context.Background(),
		sb,
		codexConfigFiles("model = \"gpt-5\"\n"),
		testLogger(t),
	); !errors.Is(
		err,
		fs.WriteErr,
	) {
		t.Errorf("Provision = %v, want the wrapped write failure", err)
	}
}

func TestMainConfigBasePath(t *testing.T) {
	cases := map[string]string{
		"/home/dev/.codex/config.toml": "/home/dev/.local/state/agents-sandbox/config-base/.codex/config.toml",
		"/etc/codex/config.toml":       "",
		"":                             "",
	}
	for mergedPath, want := range cases {
		if got := mainConfigBasePath("/home/dev", mergedPath); got != want {
			t.Errorf("mainConfigBasePath(%q) = %q, want %q", mergedPath, got, want)
		}
	}
}

func TestMaterializeKeepsTheAgentsOwnMainConfig(t *testing.T) {
	target := t.TempDir()
	hostMain := filepath.Join(target, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(hostMain), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hostMain, []byte(agentTrust), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Materialize(codexConfigFiles("model = \"gpt-5\"\n"), VMHomeDir, target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	content, err := os.ReadFile(hostMain)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "gpt-5") || !strings.Contains(string(content), "trusted") {
		t.Errorf("main config = %q, want the launcher's model beside the agent's trust", content)
	}
	base, err := os.ReadFile(filepath.Join(target, mainConfigBaseDir, ".codex", "config.toml"))
	if err != nil || string(base) != "model = \"gpt-5\"\n" {
		t.Errorf("base = %q, %v; want the launcher's content", base, err)
	}
}

func TestHostConfigStoreTellsAnUnreadableFileFromAMissingOne(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ".codex", "config.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := hostConfigStore{vmHome: VMHomeDir, targetDir: target}

	if _, readable := store.read(codexMainConfig); readable {
		t.Error("a directory at the main config path read as readable")
	}
	if content, readable := store.read("/home/dev/.missing"); !readable || content != nil {
		t.Errorf("read of a missing file = %q, %v; want absent", content, readable)
	}
	if content, readable := store.read("/home/dev/.codex/config.toml/below"); !readable || content != nil {
		t.Errorf("read below a file = %q, %v; want absent", content, readable)
	}
	if content, readable := store.read("/elsewhere/x"); !readable || content != nil {
		t.Errorf("read outside the home = %q, %v; want absent", content, readable)
	}
}

func TestProvisionWithoutLauncherContentLeavesAnUnreadableMainConfig(t *testing.T) {
	fs := msbtest.NewTestFS(map[string][]byte{
		codexMainConfig: []byte("model = [unterminated\n"),
		codexBase:       []byte("model = \"gpt-5\"\n"),
	})

	provisionInto(t, fs, codexConfigFiles(""))

	if _, written := fs.Writes[codexMainConfig]; written {
		t.Error("a main config was written although the launcher has no content")
	}
}

func TestProvisionWritesNoModeForAMainConfigWithoutOne(t *testing.T) {
	execCalls := provisionInto(t, msbtest.NewTestFS(nil), codexConfigFiles("model = \"gpt-5\"\n"))

	if slices.ContainsFunc(execCalls, func(call string) bool { return strings.HasSuffix(call, codexMainConfig) }) {
		t.Errorf("exec calls = %v, want no chmod of the main config", execCalls)
	}
}

func TestProvisionReportsABaseChmodFailure(t *testing.T) {
	chmodErr := errors.New("chmod boom")
	sb := &msbtest.MockSandbox{FSValue_: msbtest.NewTestFS(nil), ShellCalls: &[]string{}, ExecErr: chmodErr}

	if err := Provision(
		context.Background(),
		sb,
		codexConfigFiles("model = \"gpt-5\"\n"),
		testLogger(t),
	); !errors.Is(
		err,
		chmodErr,
	) {
		t.Errorf("Provision = %v, want the wrapped chmod failure of the base", err)
	}
}

func TestMaterializeKeepsTheBasePrivateAndTheMainConfigDefault(t *testing.T) {
	target := t.TempDir()

	if err := Materialize(codexConfigFiles("model = \"gpt-5\"\n"), VMHomeDir, target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	for rel, want := range map[string]os.FileMode{
		".codex/config.toml":                      0o644,
		mainConfigBaseDir + "/.codex/config.toml": 0o600,
	} {
		info, err := os.Stat(filepath.Join(target, rel))
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s mode = %v, %v; want %v", rel, info, err, want)
		}
	}
}

func TestReconcileMainConfigWithoutAMainConfigDoesNothing(t *testing.T) {
	if err := reconcileMainConfig(hostConfigStore{}, &ConfigFiles{}); err != nil {
		t.Errorf("reconcileMainConfig = %v", err)
	}
}

// memoryMainConfigStore is an in-memory mainConfigStore whose contents persist
// across reconciliations, so a test can drive the same VM file and base through
// consecutive runs.
type memoryMainConfigStore struct {
	files map[string][]byte
}

func newMemoryMainConfigStore(files map[string][]byte) *memoryMainConfigStore {
	contents := make(map[string][]byte, len(files))
	maps.Copy(contents, files)
	return &memoryMainConfigStore{files: contents}
}

// read reports a missing key as an absent file, as hostConfigStore does.
func (s *memoryMainConfigStore) read(path string) ([]byte, bool) { return s.files[path], true }

func (s *memoryMainConfigStore) write(path string, data []byte, _ os.FileMode) error {
	s.files[path] = data
	return nil
}

func (s *memoryMainConfigStore) remove(path string) { delete(s.files, path) }

// TestReconcileMainConfigKeylessLauncherIsAFixedPoint pins the review finding
// that a keyless launcher document (a snippet merging to no keys, or a host
// drop-in decoding to none) must not be recorded as the base: recording its raw
// bytes would make the next run read them as a base and delete an agent file the
// first run kept. A keyless live document is present-but-nonempty, so the
// byte-empty check alone would not have kept it.
func TestReconcileMainConfigKeylessLauncherIsAFixedPoint(t *testing.T) {
	const opencodeBase = "/home/dev/.local/state/agents-sandbox/config-base/.config/opencode/opencode.jsonc"
	for name, live := range map[string]string{
		"keyless JSON document": `{}`,
		"null JSON document":    `null`,
		"whitespace document":   " \n",
		"comment-only document": "// nothing\n",
	} {
		t.Run(name, func(t *testing.T) {
			store := newMemoryMainConfigStore(map[string][]byte{opencodeMergedPath: []byte(live)})
			cf := &ConfigFiles{
				HasSnippets:    true,
				Merged:         []byte(`{}`),
				MergedPath:     opencodeMergedPath,
				MergedBasePath: opencodeBase,
			}

			for run := range 4 {
				if err := reconcileMainConfig(store, cf); err != nil {
					t.Fatalf("run %d: reconcileMainConfig = %v", run, err)
				}
				if got, _ := store.read(opencodeMergedPath); string(got) != live {
					t.Fatalf("run %d: main config = %q, want the agent's own %q kept", run, got, live)
				}
				if recorded, _ := store.read(opencodeBase); recorded != nil {
					t.Fatalf("run %d: a keyless launcher was recorded as the base", run)
				}
			}
		})
	}
}

// TestReconcileMainConfigKeylessDropInIsAFixedPoint pins the same rule for the
// drop-in copy of a host file: a host file at the main config path that decodes
// to zero keys is no launcher content, so it is not recorded as a base and does
// not delete the agent's file on a later run.
func TestReconcileMainConfigKeylessDropInIsAFixedPoint(t *testing.T) {
	const opencodeBase = "/home/dev/.local/state/agents-sandbox/config-base/.config/opencode/opencode.jsonc"
	store := newMemoryMainConfigStore(map[string][]byte{opencodeMergedPath: []byte(`{"theme":"dark"}`)})
	cf := &ConfigFiles{
		MergedPath:     opencodeMergedPath,
		MergedBasePath: opencodeBase,
		Provisioned:    map[string][]byte{opencodeMergedPath: []byte("{}\n")},
	}

	for run := range 4 {
		if err := reconcileMainConfig(store, cf); err != nil {
			t.Fatalf("run %d: reconcileMainConfig = %v", run, err)
		}
		if got, _ := store.read(opencodeMergedPath); string(got) != `{"theme":"dark"}` {
			t.Fatalf("run %d: main config = %q, want the agent's own settings kept", run, got)
		}
		if recorded, _ := store.read(opencodeBase); recorded != nil {
			t.Fatalf("run %d: a keyless drop-in was recorded as the base", run)
		}
	}
}

func bytesOrNil(text string) []byte {
	if text == "" {
		return nil
	}
	return []byte(text)
}

func TestChangedAgentConfigPathsIgnoresTheAgentsOwnWrites(t *testing.T) {
	cf := codexConfigFiles("model = \"gpt-5\"\n")
	vmData := map[string][]byte{
		codexMainConfig: []byte("model = \"gpt-5\"\n" + agentTrust),
		codexBase:       []byte("model = \"gpt-5\"\n"),
	}
	if changed := ChangedAgentConfigPaths(cf, vmData); len(changed) != 0 {
		t.Errorf("changed = %v, want none for the agent's own writes", changed)
	}
	vmData[codexBase] = []byte("model = \"gpt-4\"\n")
	if changed := ChangedAgentConfigPaths(cf, vmData); !slices.Equal(changed, []string{codexMainConfig}) {
		t.Errorf("changed = %v, want the main config for a snippet change", changed)
	}
}

func TestChangedAgentConfigPathsWithoutABaseComparesTheFile(t *testing.T) {
	cf := codexConfigFiles("model = \"gpt-5\"\n")
	vmData := map[string][]byte{codexMainConfig: []byte("model = \"gpt-4\"\n")}
	if changed := ChangedAgentConfigPaths(cf, vmData); !slices.Equal(changed, []string{codexMainConfig}) {
		t.Errorf("changed = %v, want the main config", changed)
	}
}

// TestChangedAgentConfigPathsWithAKeylessLauncher pins the shared keyless rule:
// a launcher document with no keys owns nothing, and recordMainConfigBase removes
// the base for it, so the reconfig comparison must not fall back to comparing the
// raw keyless bytes against the agent's live file and report a change every run.
func TestChangedAgentConfigPathsWithAKeylessLauncher(t *testing.T) {
	const (
		opencodeBase   = "/home/dev/.local/state/agents-sandbox/config-base/.config/opencode/opencode.jsonc"
		agentSettings  = `{"theme":"dark"}`
		launcherWithIt = `{"model":"gpt-5","theme":"dark"}`
	)
	keyless := &ConfigFiles{
		HasSnippets:    true,
		Merged:         []byte("{}\n"),
		MergedPath:     opencodeMergedPath,
		MergedBasePath: opencodeBase,
	}
	for name, live := range map[string]string{
		"the agent's own settings": agentSettings,
		"also the launcher's keys": launcherWithIt,
	} {
		t.Run(name, func(t *testing.T) {
			vmData := map[string][]byte{opencodeMergedPath: []byte(live)}
			if changed := ChangedAgentConfigPaths(keyless, vmData); len(changed) != 0 {
				t.Errorf("changed = %v, want none for a keyless launcher with no recorded base", changed)
			}
		})
	}

	t.Run("a recorded base reports a change", func(t *testing.T) {
		vmData := map[string][]byte{
			opencodeMergedPath: []byte(launcherWithIt),
			opencodeBase:       []byte(`{"model":"gpt-5"}`),
		}
		if changed := ChangedAgentConfigPaths(keyless, vmData); !slices.Equal(changed, []string{opencodeMergedPath}) {
			t.Errorf("changed = %v, want the main config for a dropped key", changed)
		}
	})
}

func TestComparedPathsAddsTheBase(t *testing.T) {
	cf := codexConfigFiles("x = 1\n")
	cf.Keys = []string{codexMainConfig}
	if got := cf.ComparedPaths(); !slices.Equal(got, []string{codexMainConfig, codexBase}) {
		t.Errorf("ComparedPaths = %v", got)
	}
	cf.MergedBasePath = ""
	if got := cf.ComparedPaths(); !slices.Equal(got, []string{codexMainConfig}) {
		t.Errorf("ComparedPaths without a base = %v", got)
	}
}

func TestLoadConfigFilesRecordsTheMainConfigBasePath(t *testing.T) {
	if got := mainConfigBasePath(VMHomeDir, opencodeMergedPath); got !=
		"/home/dev/.local/state/agents-sandbox/config-base/.config/opencode/opencode.jsonc" {
		t.Errorf("base path = %q", got)
	}
}

func TestMaterializeRemovesAMainConfigLeftWithoutKeys(t *testing.T) {
	target := t.TempDir()
	for rel, content := range map[string]string{
		".codex/config.toml":                      "model = \"gpt-5\"\n",
		mainConfigBaseDir + "/.codex/config.toml": "model = \"gpt-5\"\n",
	} {
		path := filepath.Join(target, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := Materialize(codexConfigFiles(""), VMHomeDir, target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	for _, rel := range []string{".codex/config.toml", mainConfigBaseDir + "/.codex/config.toml"} {
		if _, err := os.Stat(filepath.Join(target, rel)); !os.IsNotExist(err) {
			t.Errorf("%s still exists (%v), want it removed", rel, err)
		}
	}
}
