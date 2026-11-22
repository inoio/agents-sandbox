package driver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/inoio/agents-sandbox/internal/homeconfig"
)

// TestSeedHomeFiles pins that seeds are written under the isolated HOME
// (<root>/home/...) with parents created, so the launcher provisions them into
// the VM.
func TestSeedHomeFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		HermesConfigPath: "model: {}\n",
		".config/x":      "y",
	}
	if err := SeedHomeFiles(root, files); err != nil {
		t.Fatalf("SeedHomeFiles: %v", err)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(root, homeDirName, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
}

// TestSeedLauncherConfig pins that the isolated launcher config enables
// host-config provisioning and carries the home: manifest. The opt-in is what
// makes the agent's ProvisionRules copy a harness's SeedHomeFiles; the manifest
// seeds a target outside those rules. The written config is parsed back with the
// launcher's own home-manifest reader, so a shape the launcher would reject
// fails here.
func TestSeedLauncherConfig(t *testing.T) {
	homeBase := t.TempDir()
	files := map[string]string{
		ClaudeCodeConfigPath: ClaudeCodeSmokeConfig,
		".nested/seed":       "content",
	}
	if err := SeedLauncherConfig(homeBase, files); err != nil {
		t.Fatalf("SeedLauncherConfig: %v", err)
	}
	for target, want := range files {
		got, err := os.ReadFile(filepath.Join(homeBase, seedDirName, target))
		if err != nil {
			t.Fatalf("read seed %s: %v", target, err)
		}
		if string(got) != want {
			t.Errorf("seed %s = %q, want %q", target, got, want)
		}
	}
	configPath := filepath.Join(homeBase, configDirName, launcherConfigDirName, launcherConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read launcher config: %v", err)
	}
	assertProvisioningEnabled(t, data)
	manifest, ok, err := homeconfig.ParseHomeConfig(configPath, data)
	if err != nil {
		t.Fatalf("parse launcher config: %v", err)
	}
	if !ok {
		t.Fatalf("launcher config has no home: key:\n%s", data)
	}
	for target := range files {
		entry, ok := manifest[target]
		if !ok {
			t.Errorf("home manifest missing target %s", target)
			continue
		}
		if want := filepath.Join(homeBase, seedDirName, target); entry.Source != want {
			t.Errorf("manifest source for %s = %q, want %q", target, entry.Source, want)
		}
	}
}

// TestSeedLauncherConfigWithoutManifest pins that a harness with no
// SeedHomeManifest still gets a launcher config that enables host-config
// provisioning (its SeedHomeFiles depend on it) and declares no home: manifest.
func TestSeedLauncherConfigWithoutManifest(t *testing.T) {
	homeBase := t.TempDir()
	if err := SeedLauncherConfig(homeBase, nil); err != nil {
		t.Fatalf("SeedLauncherConfig(nil): %v", err)
	}
	configPath := filepath.Join(homeBase, configDirName, launcherConfigDirName, launcherConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read launcher config: %v", err)
	}
	assertProvisioningEnabled(t, data)
	if _, ok, err := homeconfig.ParseHomeConfig(configPath, data); err != nil {
		t.Fatalf("parse launcher config: %v", err)
	} else if ok {
		t.Errorf("launcher config has a home: key for an empty manifest:\n%s", data)
	}
}

// assertProvisioningEnabled fails unless data is a launcher config that turns
// host-config provisioning on, the opt-in a SeedHomeFiles seed needs.
func assertProvisioningEnabled(t *testing.T, data []byte) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal launcher config: %v", err)
	}
	if got, ok := raw["provision-host-config"]; !ok || got != true {
		t.Errorf("launcher config provision-host-config = %v (present %v), want true:\n%s", got, ok, data)
	}
}

// TestSeedAgentSnippets pins that snippets land in the agent's config dir
// under the isolated launcher config dir (XDG_CONFIG_HOME/agents-sandbox),
// where the launcher's snippet merge reads them, and that none writes nothing.
func TestSeedAgentSnippets(t *testing.T) {
	homeBase := t.TempDir()
	if err := SeedAgentSnippets(homeBase, "kimi-code", map[string]string{"config-smoke.toml": "a = 1\n"}); err != nil {
		t.Fatalf("SeedAgentSnippets: %v", err)
	}
	path := filepath.Join(homeBase, configDirName, launcherConfigDirName, "kimi-code", "config-smoke.toml")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snippet: %v", err)
	}
	if string(got) != "a = 1\n" {
		t.Errorf("snippet = %q, want %q", got, "a = 1\n")
	}

	emptyBase := t.TempDir()
	if err := SeedAgentSnippets(emptyBase, "kimi-code", nil); err != nil {
		t.Fatalf("SeedAgentSnippets(nil): %v", err)
	}
	if _, err := os.Stat(filepath.Join(emptyBase, configDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("SeedAgentSnippets(nil) created a config dir: stat err = %v", err)
	}
}

// TestPrepareHomeCreatesTheChildEnvTree pins that PrepareHome materialises every
// directory IsolatedEnv points the child at. A missing TMPDIR is fatal: the
// launcher and the microsandbox runtime create temporary files through
// os.TempDir() while setting up a sandbox, so an uncreated TMPDIR fails the run
// with "create prefill sandbox: io error: no such file or directory".
func TestPrepareHomeCreatesTheChildEnvTree(t *testing.T) {
	homeBase := filepath.Join(t.TempDir(), "run")
	if err := PrepareHome(homeBase); err != nil {
		t.Fatalf("PrepareHome: %v", err)
	}
	for _, dir := range []string{homeDirName, tmpDirName, configDirName, stateDirName, cacheDirName} {
		info, err := os.Stat(filepath.Join(homeBase, dir))
		if err != nil {
			t.Errorf("stat %s: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

// TestPrepareHomeReportsFailures pins that a home base that cannot be created
// names the directory and preserves the filesystem cause, so the smoke test
// reports why preparation failed rather than a bare message.
func TestPrepareHomeReportsFailures(t *testing.T) {
	// A file where the home base must go makes the first MkdirAll fail.
	homeBase := filepath.Join(t.TempDir(), "run")
	if err := os.WriteFile(homeBase, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := PrepareHome(homeBase)
	if err == nil {
		t.Fatal("PrepareHome = nil, want an error when the home base cannot be created")
	}
	if !strings.Contains(err.Error(), "create "+homeDirName) {
		t.Errorf("PrepareHome error %q, want it to name the directory it could not create", err)
	}
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("PrepareHome error %q, want it to wrap the filesystem cause ENOTDIR", err)
	}
}

// TestSeedHomeFilesReportsFailures pins both failure surfaces: a parent that
// cannot be created, and a target that cannot be written. Each error names the
// seed it was writing and preserves the filesystem cause, so the smoke test
// reports why provisioning failed rather than a bare message.
func TestSeedHomeFilesReportsFailures(t *testing.T) {
	t.Run("parent cannot be created", func(t *testing.T) {
		root := t.TempDir()
		// A file where the seed's parent directory must go makes MkdirAll fail.
		if err := os.WriteFile(filepath.Join(root, homeDirName), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := SeedHomeFiles(root, map[string]string{HermesConfigPath: "y"})
		if err == nil {
			t.Fatal("SeedHomeFiles = nil, want an error when a parent cannot be created")
		}
		if !strings.Contains(err.Error(), "create parent for .hermes") {
			t.Errorf("SeedHomeFiles error %q, want it to name the parent it could not create", err)
		}
		if !errors.Is(err, syscall.ENOTDIR) {
			t.Errorf("SeedHomeFiles error %q, want it to wrap the filesystem cause ENOTDIR", err)
		}
	})
	t.Run("target cannot be written", func(t *testing.T) {
		root := t.TempDir()
		// A directory at the target path makes the file write fail.
		if err := os.MkdirAll(filepath.Join(root, homeDirName, ".hermes", "config.yaml"), 0o755); err != nil {
			t.Fatal(err)
		}
		err := SeedHomeFiles(root, map[string]string{HermesConfigPath: "y"})
		if err == nil {
			t.Fatal("SeedHomeFiles = nil, want an error when the target cannot be written")
		}
		if !strings.Contains(err.Error(), "write .hermes/config.yaml") {
			t.Errorf("SeedHomeFiles error %q, want it to name the file it could not write", err)
		}
		if !errors.Is(err, syscall.EISDIR) {
			t.Errorf("SeedHomeFiles error %q, want it to wrap the filesystem cause EISDIR", err)
		}
	})
}

// TestSeedHomeFilesUsesPrivateModes pins the modes the seed writes with through
// the filesystem seams, so the assertion does not depend on the process umask:
// the seeded parent directory is group-traversable but not world-readable, and
// the file is owner-only.
func TestSeedHomeFilesUsesPrivateModes(t *testing.T) {
	root := t.TempDir()

	var dirMode, fileMode os.FileMode
	originalMkdirAll, originalWriteFile := mkdirAll, writeFile
	t.Cleanup(func() { mkdirAll, writeFile = originalMkdirAll, originalWriteFile })
	mkdirAll = func(path string, perm os.FileMode) error {
		dirMode = perm
		return os.MkdirAll(path, perm)
	}
	writeFile = func(name string, data []byte, perm os.FileMode) error {
		fileMode = perm
		return os.WriteFile(name, data, perm)
	}

	if err := SeedHomeFiles(root, map[string]string{HermesConfigPath: "model: {}\n"}); err != nil {
		t.Fatalf("SeedHomeFiles: %v", err)
	}
	if dirMode != 0o750 {
		t.Errorf("seed parent mode = %#o, want 0o750", dirMode)
	}
	if fileMode != 0o600 {
		t.Errorf("seed file mode = %#o, want 0o600", fileMode)
	}
}

func TestIsolatedEnvAllowlist(t *testing.T) {
	inherited := []string{
		"PATH=/usr/bin:/bin",
		"TERM=dumb",
		"LANG=en_US.UTF-8",
		"LC_ALL=C.UTF-8",
		"HTTP_PROXY=http://proxy.example:3128",
		"HOME=/real/home",
		"XDG_DATA_HOME=/real/data",
		"AGENTS_SANDBOX_NETWORK_PROFILE=public",
		"OPENCODE_SANDBOX_NOTIFY=on",
		"MSB_HOME=/real/msb",
		"SECRET_TOKEN=leak",
	}
	env := IsolatedEnv("/root", "/root", inherited)
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	want := map[string]string{
		"PATH":            "/usr/bin:/bin",
		"TERM":            "xterm-256color",
		"LANG":            "en_US.UTF-8",
		"LC_ALL":          "C.UTF-8",
		"HTTP_PROXY":      "http://proxy.example:3128",
		"HOME":            "/root/home",
		"XDG_CONFIG_HOME": "/root/config",
		"XDG_DATA_HOME":   "/root/data",
		"XDG_STATE_HOME":  "/root/state",
		"XDG_CACHE_HOME":  "/root/cache",
		"TMPDIR":          "/root/tmp",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{
		"AGENTS_SANDBOX_NETWORK_PROFILE",
		"OPENCODE_SANDBOX_NOTIFY",
		"MSB_HOME",
		"SECRET_TOKEN",
	} {
		if _, ok := got[k]; ok {
			t.Errorf("%s must not be inherited (got %q)", k, got[k])
		}
	}
}

// TestIsolatedEnvSharesDataKeepsHome pins the per-harness split: two harnesses
// sharing one root keep separate HOME/config/state/cache dirs (so a seeded config
// cannot clobber another harness) while sharing XDG_DATA_HOME (so the
// microsandbox image cache stays common).
func TestIsolatedEnvSharesDataKeepsHome(t *testing.T) {
	root := "/suite"
	first := envMap(IsolatedEnv(root, HomeBase(root, "opencode"), nil))
	second := envMap(IsolatedEnv(root, HomeBase(root, "pi"), nil))

	if first["HOME"] == second["HOME"] {
		t.Errorf("HOME = %q for both harnesses, want a distinct per-harness home", first["HOME"])
	}
	if first["XDG_STATE_HOME"] == second["XDG_STATE_HOME"] {
		t.Errorf(
			"XDG_STATE_HOME = %q for both harnesses, want a distinct per-harness state dir",
			first["XDG_STATE_HOME"],
		)
	}
	wantData := filepath.Join(root, dataDirName)
	for name, env := range map[string]map[string]string{"opencode": first, "pi": second} {
		if env["XDG_DATA_HOME"] != wantData {
			t.Errorf("%s XDG_DATA_HOME = %q, want the shared %q", name, env["XDG_DATA_HOME"], wantData)
		}
	}
}

// envMap flattens a KEY=VALUE slice into a map.
func envMap(env []string) map[string]string {
	got := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	return got
}

// TestIsolatedEnvKeepsDockerEndpoint pins that the docker endpoint variables
// survive: the child needs the same daemon the preflight reaches, and they
// carry no agents-sandbox config or state.
func TestIsolatedEnvKeepsDockerEndpoint(t *testing.T) {
	inherited := []string{
		"DOCKER_HOST=unix:///run/user/1000/docker.sock",
		"DOCKER_CONTEXT=colima",
		"DOCKER_CONFIG=/real/.docker",
	}
	got := map[string]string{}
	for _, kv := range IsolatedEnv("/root", "/root", inherited) {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for k, want := range map[string]string{
		"DOCKER_HOST":    "unix:///run/user/1000/docker.sock",
		"DOCKER_CONTEXT": "colima",
		"DOCKER_CONFIG":  "/real/.docker",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

// TestIsolatedEnvDefaultsTerm pins the TERM fallback: an absent, empty, or
// "dumb" inherited value is replaced with a modern terminfo entry, and any
// other value passes through.
func TestIsolatedEnvDefaultsTerm(t *testing.T) {
	for _, test := range []struct {
		name  string
		set   bool
		value string
		want  string
	}{
		{name: "absent", set: false, value: "", want: DefaultTerm},
		{name: "empty", set: true, value: "", want: DefaultTerm},
		{name: "dumb", set: true, value: TermDumb, want: DefaultTerm},
		{name: "present", set: true, value: "screen-256color", want: "screen-256color"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inherited := []string{"PATH=/usr/bin:/bin"}
			if test.set {
				inherited = append(inherited, EnvTerm+"="+test.value)
			}
			if got := envValue(IsolatedEnv("/root", "/root", inherited), EnvTerm); got != test.want {
				t.Errorf("TERM = %q, want %q", got, test.want)
			}
		})
	}
}

// TestWithGitHubTokenAddsTheTokenWhenPresent pins that a prebuild child gets the
// environment's GitHub API token, so its release-resolution requests
// authenticate instead of sharing the anonymous api.github.com limit. GH_TOKEN
// wins over GITHUB_TOKEN, matching the launcher's own precedence.
func TestWithGitHubTokenAddsTheTokenWhenPresent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inherited []string
		wantName  string
		wantValue string
	}{
		{"gh-token", []string{"GH_TOKEN=ghp_x"}, "GH_TOKEN", "ghp_x"},
		{"github-token", []string{"GITHUB_TOKEN=ghp_y"}, "GITHUB_TOKEN", "ghp_y"},
		{"gh-token-wins", []string{"GH_TOKEN=ghp_x", "GITHUB_TOKEN=ghp_y"}, "GH_TOKEN", "ghp_x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := WithGitHubToken([]string{"PATH=/usr/bin"}, tc.inherited)
			if value := envValue(got, tc.wantName); value != tc.wantValue {
				t.Errorf("%s = %q, want %q", tc.wantName, value, tc.wantValue)
			}
		})
	}
}

// TestWithGitHubTokenLeavesEnvUnchangedWhenAbsent pins that no token variable is
// injected when the environment has none, so an anonymous host stays anonymous.
func TestWithGitHubTokenLeavesEnvUnchangedWhenAbsent(t *testing.T) {
	env := []string{"PATH=/usr/bin"}
	got := WithGitHubToken(env, []string{"PATH=/usr/bin"})
	if !slices.Equal(got, env) {
		t.Errorf("WithGitHubToken = %v, want the env unchanged (%v)", got, env)
	}
}
