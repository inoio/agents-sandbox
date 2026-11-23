package driver

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// dataDirName is the XDG data directory IsolatedEnv puts under the root; the
// private microsandbox home is nested inside it.
const dataDirName = "data"

// homeDirName is the isolated HOME directory under a run's home base; the
// child's home files (including a seeded harness config) live beneath it.
const homeDirName = "home"

// The XDG dir names IsolatedEnv roots under a run's home base, plus the child's
// TMPDIR. They share these names with PrepareHome, which creates them before the
// child starts: a TMPDIR that does not exist fails sandbox creation.
const (
	configDirName = "config"
	stateDirName  = "state"
	cacheDirName  = "cache"
	tmpDirName    = "tmp"
)

// runsDirName holds one directory per harness run, so concurrently running
// harnesses get separate HOME/XDG_CONFIG/XDG_STATE/XDG_CACHE trees. The
// microsandbox home stays shared under dataDirName so every harness reuses one
// image cache (see IsolatedEnv/HomeBase).
const runsDirName = "runs"

// HomeBase is the per-harness directory holding HOME and the per-harness XDG
// dirs, so concurrently running harnesses never share a home.
func HomeBase(root, name string) string {
	return filepath.Join(root, runsDirName, name)
}

// isolatedEnvKeep lists the inherited variables the child may keep: the
// terminal and locale settings a TUI needs, the proxy settings any egress
// uses, and the docker endpoint variables the runner-image build needs. The
// docker endpoint variables carry a host endpoint, not agents-sandbox config or
// state, so keeping them does not weaken the isolation; dropping HOME, XDG_*,
// AGENTS_SANDBOX_*, OPENCODE_SANDBOX_*, and MSB_* is the point of it.
var isolatedEnvKeep = []string{
	"PATH", "TERM", "LANG", "LC_ALL", "LC_CTYPE",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "no_proxy",
	"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG",
}

// smokeLauncherReadyTimeouts forwards a smoke-suite-namespaced override into
// the launcher's own config variable name for the child. The suite reads the
// harness var (set by the in-guest `make test-msb-in-vm` target) and writes the
// launcher var into the child, so a host run that happens to export the
// launcher's variable keeps the isolation the allowlist provides: only the
// suite's own namespace crosses into the child.
var smokeLauncherReadyTimeouts = []struct{ harness, launcher string }{
	{"AGENTS_SANDBOX_MSB_DOCKER_READY_TIMEOUT", "AGENTS_SANDBOX_DOCKER_READY_TIMEOUT"},
	{"AGENTS_SANDBOX_MSB_DAEMON_READY_TIMEOUT", "AGENTS_SANDBOX_DAEMON_READY_TIMEOUT"},
}

// launcherReadyTimeouts returns the launcher config variables to append to a
// child environment: for each pair whose smoke-suite variable is set in
// inheritedByName, the launcher variable with that value.
func launcherReadyTimeouts(inheritedByName map[string]string) []string {
	var overrides []string
	for _, pair := range smokeLauncherReadyTimeouts {
		if value, ok := inheritedByName[pair.harness]; ok && value != "" {
			overrides = append(overrides, pair.launcher+"="+value)
		}
	}
	return overrides
}

// IsolatedEnv builds the child environment from inherited: it keeps only the
// allowlisted variables (defaulting TERM when it is absent, empty, or "dumb"),
// appends the launcher readiness-timeout overrides the suite was given, then a
// private HOME/XDG_*/TMPDIR tree rooted at homeBase, so the child reads and
// writes no real agents-sandbox config or state. XDG_DATA_HOME stays rooted at
// root so every harness shares one microsandbox home (the image cache) while
// keeping its own HOME and config/state/cache dirs.
func IsolatedEnv(root, homeBase string, inherited []string) []string {
	inheritedByName := make(map[string]string, len(inherited))
	for _, entry := range inherited {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			inheritedByName[name] = value
		}
	}

	env := make([]string, 0, len(isolatedEnvKeep)+7)
	for _, name := range isolatedEnvKeep {
		if name == EnvTerm {
			continue
		}
		if value, ok := inheritedByName[name]; ok {
			env = append(env, name+"="+value)
		}
	}
	env = append(env, EnvTerm+"="+UsableTerm(inheritedByName[EnvTerm]))
	env = append(env, launcherReadyTimeouts(inheritedByName)...)
	return append(env,
		"HOME="+filepath.Join(homeBase, homeDirName),
		"XDG_CONFIG_HOME="+filepath.Join(homeBase, configDirName),
		"XDG_DATA_HOME="+filepath.Join(root, dataDirName),
		"XDG_STATE_HOME="+filepath.Join(homeBase, stateDirName),
		"XDG_CACHE_HOME="+filepath.Join(homeBase, cacheDirName),
		"TMPDIR="+filepath.Join(homeBase, tmpDirName),
	)
}

// smokeGitHubTokenEnvVars are the environment variables the smoke suite reads a
// GitHub API token from, in the launcher's precedence order (GH_TOKEN wins over
// GITHUB_TOKEN, matching internal/agent's githubToken).
var smokeGitHubTokenEnvVars = []string{"GH_TOKEN", "GITHUB_TOKEN"}

// WithGitHubToken appends the environment's GitHub API token, when one is set, to
// a prebuild child's env. The prebuild's `build` resolves each unpinned agent's
// latest release, and the GitHub-backed agents' requests would otherwise share
// the anonymous 60-requests/hour api.github.com limit; the token lifts them to
// the authenticated limit. It is a deliberate exception to IsolatedEnv's
// allowlist — the launcher sends the token only to api.github.com (never into a
// VM, whose secrets come from config files), so keeping it host-side does not
// weaken the isolation. An absent token leaves env unchanged: the launcher falls
// back to anonymous access.
func WithGitHubToken(env, inherited []string) []string {
	for _, name := range smokeGitHubTokenEnvVars {
		if value := envValue(inherited, name); value != "" {
			return append(env, name+"="+value)
		}
	}
	return env
}

// PrepareHome creates the per-harness home tree IsolatedEnv points the child at:
// its HOME, TMPDIR, and the XDG config/state/cache dirs. TMPDIR must exist before
// the child starts — the launcher and the microsandbox runtime create temporary
// files through os.TempDir() while setting up a sandbox, so a TMPDIR that does
// not exist fails the run with `create prefill sandbox: io error: no such file
// or directory`.
func PrepareHome(homeBase string) error {
	for _, dir := range []string{homeDirName, tmpDirName, configDirName, stateDirName, cacheDirName} {
		if err := mkdirAll(filepath.Join(homeBase, dir), 0o750); err != nil {
			return fmt.Errorf("create %s under %s: %w", dir, homeBase, err)
		}
	}
	return nil
}

// SeedHomeFiles writes each home-relative file under the run's isolated HOME
// (<homeBase>/home/<rel>), creating parent directories, so the launcher
// provisions it into the VM (see Spec.SeedHomeFiles).
//
// It writes through mkdirAll and writeFile, seams a test can replace to assert
// the seed's private modes without depending on the process umask.
func SeedHomeFiles(homeBase string, files map[string]string) error {
	for rel, content := range files {
		path := filepath.Join(homeBase, homeDirName, rel)
		if err := mkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("create parent for %s: %w", rel, err)
		}
		if err := writeFile(path, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

// SeedAgentSnippets writes each snippet into the agent's config dir under the
// run's isolated launcher config dir, where the launcher's snippet merge reads
// it (see Spec.SeedAgentSnippets). A nil or empty map writes nothing.
//
// It uses the same mkdirAll/writeFile seams as SeedHomeFiles.
func SeedAgentSnippets(homeBase, agentConfigDirName string, snippets map[string]string) error {
	agentDir := filepath.Join(homeBase, configDirName, launcherConfigDirName, agentConfigDirName)
	for name, content := range snippets {
		if err := mkdirAll(agentDir, 0o750); err != nil {
			return fmt.Errorf("create snippet dir for %s: %w", name, err)
		}
		if err := writeFile(filepath.Join(agentDir, name), []byte(content), 0o600); err != nil {
			return fmt.Errorf("write snippet %s: %w", name, err)
		}
	}
	return nil
}

// The seed tree and isolated launcher config dir SeedLauncherConfig writes: the
// home-manifest seed sources live under <homeBase>/seed, and the launcher config
// under <homeBase>/config/<launcherConfigDirName>, the agents-sandbox dir inside
// XDG_CONFIG_HOME (configpaths.UserConfigDir).
const (
	seedDirName            = "seed"
	launcherConfigDirName  = "agents-sandbox"
	launcherConfigFileName = "config.json"
	launcherEnvFileName    = "env"
)

// SeedLauncherConfig writes the run's isolated launcher config. It always
// enables host-config provisioning (provision-host-config: true), so the agent's
// ProvisionRules copy a harness's SeedHomeFiles into the VM: provisioning is
// opt-in since the first-run change, and an isolated home has no managed config
// layer, so without the opt-in the seeded host config never reaches the VM and
// the harness stops at its own first-run setup.
//
// When files is non-empty it also declares the `home:` manifest, provisioning
// each VM-home-relative target outside the agent's ProvisionRules: it writes the
// content to a private seed file under the run's seed dir and maps the target to
// that seed file's absolute path, so the launcher copies it into the VM home.
//
// It uses the same mkdirAll/writeFile seams as SeedHomeFiles.
func SeedLauncherConfig(homeBase string, files map[string]string) error {
	config := map[string]any{"provision-host-config": true}
	if len(files) > 0 {
		manifest := make(map[string]string, len(files))
		for target, content := range files {
			source := filepath.Join(homeBase, seedDirName, target)
			if err := mkdirAll(filepath.Dir(source), 0o750); err != nil {
				return fmt.Errorf("create seed parent for %s: %w", target, err)
			}
			if err := writeFile(source, []byte(content), 0o600); err != nil {
				return fmt.Errorf("write seed %s: %w", target, err)
			}
			manifest[target] = source
		}
		config["home"] = manifest
	}
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshal launcher config: %w", err)
	}
	dir := filepath.Join(homeBase, configDirName, launcherConfigDirName)
	if err := mkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create launcher config dir: %w", err)
	}
	if err := writeFile(filepath.Join(dir, launcherConfigFileName), data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", launcherConfigFileName, err)
	}
	return nil
}

// mkdirAll and writeFile are the filesystem seams SeedHomeFiles writes through.
// A test replaces one to assert its failure branch.
var (
	mkdirAll  = os.MkdirAll
	writeFile = os.WriteFile
)

// envValue returns the value of name in env, or "" when it is absent.
func envValue(env []string, name string) string {
	for _, entry := range env {
		if k, v, _ := strings.Cut(entry, "="); k == name {
			return v
		}
	}
	return ""
}

// SeedLauncherEnv writes vars as the isolated home's launcher env file
// (KEY=value lines, sorted), which the launcher forwards into every VM. Like any
// launcher-managed layer it counts as configuration, so a run on that home is
// not offered the first-run menu.
func SeedLauncherEnv(homeBase string, vars map[string]string) error {
	dir := filepath.Join(homeBase, configDirName, launcherConfigDirName)
	if err := mkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create launcher config dir: %w", err)
	}
	var lines strings.Builder
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		lines.WriteString(name + "=" + vars[name] + "\n")
	}
	if err := writeFile(filepath.Join(dir, launcherEnvFileName), []byte(lines.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", launcherEnvFileName, err)
	}
	return nil
}
