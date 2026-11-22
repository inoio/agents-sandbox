// Package harness holds end-to-end smoke tests that start every registered
// coding-agent harness in a real microsandbox VM and assert it reaches its
// full-screen TUI. The VM-driven test lives behind the `msb` build tag; the
// untagged helpers here are compiled only into test binaries — the child
// environment allowlist, the isolated home seeding, and the suite root and
// reuse lock.
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/hostlock"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

const (
	// smokeRootPrefix names the suite's shared temp roots; smokeRootBaseDefault
	// is the base they are created under when smokeRootBaseEnv is unset.
	//
	// microsandbox derives its sandbox control-socket path from the private home
	// under XDG_DATA_HOME (<root>/data/agents-sandbox/msb) and rejects a derived
	// path once it approaches a Unix socket's sun_path limit (104 bytes on
	// macOS, the tighter of the two supported platforms). A long TMPDIR —
	// macOS's default is already ~49 bytes — would push the derived path over
	// the limit and fail every harness before its TUI with "sandbox runtime
	// socket path is too long", so the root stays short.
	smokeRootPrefix      = "asb-"
	smokeRootBaseDefault = "/tmp"
	// smokeRootBaseEnv overrides the base a fresh suite root is created under.
	// /tmp is short and disk-backed on a stock host, but it is a RAM-backed tmpfs
	// where systemd mounts it as one (and in the KVM guest), where the suite's
	// 10+ GiB image cache would exhaust it or spend the RAM; the Makefile points
	// this at the disk-backed $(MSB_REUSE_ROOT). The override must still keep the
	// root within the socket budget, so a created root is validated like a reuse
	// root.
	smokeRootBaseEnv = "AGENTS_SANDBOX_MSB_ROOT_BASE"

	// dataDirName is the XDG data directory isolatedEnv puts under the root; the
	// private microsandbox home is nested inside it.
	dataDirName = "data"

	// homeDirName is the isolated HOME directory under a run's home base; the
	// child's home files (including a seeded harness config) live beneath it.
	homeDirName = "home"

	// The XDG dir names isolatedEnv roots under a run's home base, plus the
	// child's TMPDIR. They share these names with prepareHome, which creates
	// them before the child starts: a TMPDIR that does not exist fails sandbox
	// creation.
	configDirName = "config"
	stateDirName  = "state"
	cacheDirName  = "cache"
	tmpDirName    = "tmp"

	// runsDirName holds one directory per harness run, so concurrently running
	// harnesses get separate HOME/XDG_CONFIG/XDG_STATE/XDG_CACHE trees. The
	// microsandbox home stays shared under dataDirName so every harness reuses
	// one image cache (see isolatedEnv/harnessHomeBase).
	runsDirName = "runs"
)

// smokeRootBase returns the base a fresh suite root is created under: the
// smokeRootBaseEnv override when set, else smokeRootBaseDefault.
func smokeRootBase() string {
	//testenv:allow the smoke suite reads its own fresh-root base override
	if dir := os.Getenv(smokeRootBaseEnv); dir != "" {
		return dir
	}
	return smokeRootBaseDefault
}

// smokeRootReuseEnv, when set, points the suite at an existing directory it
// keeps between runs instead of a fresh temp root. The microsandbox image cache
// lives under the root's data dir, so a reused root skips the multi-GB
// `docker save | msb ImageLoad` that each harness prebuild otherwise repays.
// Local iteration only: unset (the default) creates and removes a fresh root.
const smokeRootReuseEnv = "AGENTS_SANDBOX_SMOKE_ROOT"

// A sandbox's control-socket path is microsandbox's private home (msbHomeUnderRoot)
// plus a fixed suffix microsandbox appends; it must stay below a Unix socket's
// sun_path limit (104 bytes on macOS, the tighter of the two targets) or every
// harness fails before its TUI with "sandbox runtime socket path is too long".
const (
	sandboxSocketPathSuffix = 50
	sunPathLimit            = 104
)

// smokeMemoryGiB is the memory each smoke harness VM boots with — the VM's
// ceiling, passed to the launcher as --memory. A harness only has to reach its
// TUI, and the launcher's default project-VM size (4 GiB) multiplies across the
// parallel harnesses, so the smoke run boots each smaller.
const smokeMemoryGiB = 2

// smokeWorkingSetMiB is the host memory a smoke VM is assumed to occupy when
// the parallel-harness count is sized. A guest allocates only the pages it
// touches (virtio-mem), so the divisor is an expected working set, not
// smokeMemoryGiB's ceiling. It tracks the heaviest harness, not the lightest:
// measured peaks are under 0.5 GiB for pi and over 1 GiB for opencode2, so
// sizing on pi would oversubscribe the host.
const smokeWorkingSetMiB = 1536

// harnessHostReserveMiB is held back from the host's free memory before the
// parallel-harness count is sized: it covers the host OS, the docker daemon,
// and the internal/sandbox/msb test binary, which runs one real VM at the same
// time as this suite.
const harnessHostReserveMiB = 2048

// harnessConcurrencyOverrideEnv forces the parallel-harness count, for a host
// the memory heuristic sizes badly or for debugging. Unset (or empty) uses the
// memory-derived default.
const harnessConcurrencyOverrideEnv = "AGENTS_SANDBOX_MSB_PARALLEL"

// smokeMemorySpec is the launcher --memory value for a smoke VM.
func smokeMemorySpec() string {
	return fmt.Sprintf("%dG", smokeMemoryGiB)
}

// harnessConcurrencyFor returns how many harnesses may run at once given the
// host's free memory in MiB: as many smoke VMs as fit after the host reserve,
// never fewer than one. The msb-tagged suite wraps it with the host reading, so
// the memory policy is testable without the host.
func harnessConcurrencyFor(freeMiB int) int {
	if usable := freeMiB - harnessHostReserveMiB; usable >= smokeWorkingSetMiB {
		return usable / smokeWorkingSetMiB
	}
	return 1
}

// harnessRunArgs is the `run` invocation a harness smoke subtest drives: the
// agent and network under test, the smoke memory cap, --yes, and --no-rebuild.
//
// --yes makes the launcher non-interactive, which is what keeps the first-run
// onboarding menu (internal/firstrun) from blocking the pty: termio treats
// assumeYes as non-interactive, so the menu is skipped and the attach proceeds
// as a plain run with no learning session and no host-config provisioning. A
// run without it would sit at "How would you like to set up this checkout?"
// until the readiness wait times out and the harness never starts.
//
// --no-rebuild reuses the image the suite already prebuilt and skips the agent
// upgrade check. The check runs against the run's fresh machine-global root
// state, so its once-a-day window is always expired and each harness would
// re-resolve its agent's latest release; for the GitHub-backed agents that
// spends the shared anonymous 60-requests/hour api.github.com budget the suite
// as a whole has to stay under.
func harnessRunArgs(name, network string) []string {
	return []string{
		"run", "--agent", name, "--network", network, "--memory", smokeMemorySpec(), "--yes", "--no-rebuild",
	}
}

// dockerStorageShellArgs is the `shell` invocation the docker-storage sibling
// subtest drives: a docker-enabled opencode VM under the given network. It
// carries --no-rebuild for the same reason harnessRunArgs does.
func dockerStorageShellArgs(network string) []string {
	return []string{"shell", "--agent", "opencode", "--docker", "--network", network, "--no-rebuild"}
}

// smokeVMCPUsEnv caps each smoke VM's vCPU count. Unset (the default) leaves the
// launcher's own default, every host CPU. The KVM guest sets it: a nested guest
// has few vCPUs next to the host, so parallel harnesses each claiming every vCPU
// oversubscribe it and slow every VM start.
const smokeVMCPUsEnv = "AGENTS_SANDBOX_MSB_VM_CPUS"

// cpusFlag is the launcher flag a vCPU-capped smoke run appends (see
// withVMCPUs).
const cpusFlag = "cpus"

// smokeVMCPUsFromValue parses the vCPU cap: a positive, 8-bit integer caps each
// smoke VM; anything else (unset, zero, non-numeric, out of range) leaves the
// launcher's default. Kept pure so it is testable without the host, like
// harnessConcurrencyFor.
func smokeVMCPUsFromValue(value string) uint8 {
	n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 8)
	if err != nil || n == 0 {
		return 0
	}
	return uint8(n)
}

// withVMCPUs appends the launcher's --cpus flag when cpus is non-zero, leaving
// args unchanged otherwise, so a run with no cap set is byte-for-byte what the
// suite has always driven.
func withVMCPUs(args []string, cpus uint8) []string {
	if cpus == 0 {
		return args
	}
	return append(args, "--"+cpusFlag, strconv.Itoa(int(cpus)))
}

// msbHomeUnderRoot is the private microsandbox home a suite root derives.
func msbHomeUnderRoot(root string) string {
	return filepath.Join(root, dataDirName, "agents-sandbox", "msb")
}

// rootFitsSandboxSocket reports whether root's derived sandbox control-socket
// path stays below the sun_path limit.
func rootFitsSandboxSocket(root string) bool {
	return len(msbHomeUnderRoot(root))+sandboxSocketPathSuffix < sunPathLimit
}

// harnessHomeBase is the per-harness directory holding HOME and the per-harness
// XDG dirs, so concurrently running harnesses never share a home.
func harnessHomeBase(root, name string) string {
	return filepath.Join(root, runsDirName, name)
}

// reuseLockSuffix names the lock file that serializes runs sharing a reused
// suite root. The lock sits beside the root, not inside it, so it never lands
// in the microsandbox home under it.
const reuseLockSuffix = ".lock"

// reuseLockPath is the lock file that serializes runs sharing the reused root
// dir.
func reuseLockPath(dir string) string {
	return filepath.Join(filepath.Dir(dir), filepath.Base(dir)+reuseLockSuffix)
}

// acquireReuseLock blocks until this run holds the exclusive lock for the reused
// root dir, waiting out any other run that shares it. Two runs that share a root
// would otherwise race on the sandbox sweep: whichever finishes first removes
// every sandbox in the home, including the other's live VMs. dir is absolute,
// so the lock path is stable across working directories. It returns the release.
func acquireReuseLock(dir string) (func(), error) {
	return hostlock.AcquireExclusive(filepath.Dir(dir), filepath.Base(dir)+reuseLockSuffix)
}

// resolveSuiteRoot returns the suite's shared root, whether it is a reused
// directory, and the release for the exclusive lock a reused directory is held
// under (nil for a fresh root). A set smokeRootReuseEnv reuses that directory —
// resolved to an absolute path and validated to fit the sandbox socket path
// budget — so the microsandbox image cache under it survives between runs;
// otherwise a fresh temp root is created under smokeRootBase() (the
// smokeRootBaseEnv override, else smokeRootBaseDefault) for the caller to remove,
// validated to the same budget. The lock serializes runs that share a root, so a
// finishing run's sandbox sweep cannot destroy VMs another run is still using.
func resolveSuiteRoot() (string, bool, func(), error) {
	//testenv:allow the smoke suite reads its own reuse-root override
	if dir := os.Getenv(smokeRootReuseEnv); dir != "" {
		reuseRoot, err := absPath(dir)
		if err != nil {
			return "", false, nil, fmt.Errorf("resolve reuse root %s: %w", dir, err)
		}
		if !rootFitsSandboxSocket(reuseRoot) {
			return "", false, nil, fmt.Errorf(
				"%s=%q derives a %d-byte sandbox control-socket path, at or above the %d-byte sun_path limit",
				smokeRootReuseEnv, reuseRoot,
				len(msbHomeUnderRoot(reuseRoot))+sandboxSocketPathSuffix, sunPathLimit)
		}
		// reuseRoot is an explicit operator opt-in (the reuse root), not request
		// data.
		if err = os.MkdirAll(reuseRoot, 0o750); err != nil {
			return "", false, nil, fmt.Errorf("create reuse root %s: %w", reuseRoot, err)
		}
		release, err := acquireReuseLock(reuseRoot)
		if err != nil {
			return "", false, nil, fmt.Errorf("lock reuse root %s: %w", reuseRoot, err)
		}
		return reuseRoot, true, release, nil
	}
	root, err := mkdtemp(smokeRootBase(), smokeRootPrefix)
	if err != nil {
		return "", false, nil, err
	}
	if !rootFitsSandboxSocket(root) {
		_ = os.Remove(root)
		return "", false, nil, fmt.Errorf(
			"suite root %q derives a %d-byte sandbox control-socket path, at or above the %d-byte sun_path limit; set %s to a shorter base",
			root,
			len(msbHomeUnderRoot(root))+sandboxSocketPathSuffix,
			sunPathLimit,
			smokeRootBaseEnv,
		)
	}
	return root, false, nil, nil
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

// The quit loop taps the harness's key in pairs until the process exits (see
// quit). quitPressInterval keeps the two presses of a pair within the window a
// repeated-key exit's "press again" prompt allows — qwen-code's was measured
// under 1.2s, and a slower cadence stops the exit chaining — while
// quitRetryInterval spaces the pairs out, so a harness that has caught the quit
// can finish tearing down (and emit its alternate-screen leave) before the loop
// writes another press. quitTimeout bounds the whole sequence.
const (
	quitPressInterval = 400 * time.Millisecond
	quitRetryInterval = 2 * time.Second
	quitTimeout       = 15 * time.Second
)

// quitIntervalAfterPress returns the wait before the next quit press: a pair's
// two presses are one quitPressInterval apart, and the even press ends the pair,
// after which the loop waits quitRetryInterval for the harness to exit or to
// signal its teardown.
func quitIntervalAfterPress(presses int) time.Duration {
	if presses%2 == 0 {
		return quitRetryInterval
	}
	return quitPressInterval
}

// isolatedEnv builds the child environment from inherited: it keeps only the
// allowlisted variables (defaulting TERM when it is absent, empty, or "dumb"),
// then appends a private HOME/XDG_*/TMPDIR tree rooted at homeBase, so the child
// reads and writes no real agents-sandbox config or state. XDG_DATA_HOME stays
// rooted at root so every harness shares one microsandbox home (the image cache)
// while keeping its own HOME and config/state/cache dirs.
func isolatedEnv(root, homeBase string, inherited []string) []string {
	inheritedByName := make(map[string]string, len(inherited))
	for _, entry := range inherited {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			inheritedByName[name] = value
		}
	}

	env := make([]string, 0, len(isolatedEnvKeep)+7)
	for _, name := range isolatedEnvKeep {
		if name == driver.EnvTerm {
			continue
		}
		if value, ok := inheritedByName[name]; ok {
			env = append(env, name+"="+value)
		}
	}
	env = append(env, driver.EnvTerm+"="+driver.UsableTerm(inheritedByName[driver.EnvTerm]))
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

// withGitHubToken appends the environment's GitHub API token, when one is set, to
// a prebuild child's env. The prebuild's `build` resolves each unpinned agent's
// latest release, and the GitHub-backed agents' requests would otherwise share
// the anonymous 60-requests/hour api.github.com limit; the token lifts them to
// the authenticated limit. It is a deliberate exception to isolatedEnv's
// allowlist — the launcher sends the token only to api.github.com (never into a
// VM, whose secrets come from config files), so keeping it host-side does not
// weaken the isolation. An absent token leaves env unchanged: the launcher falls
// back to anonymous access.
func withGitHubToken(env, inherited []string) []string {
	for _, name := range smokeGitHubTokenEnvVars {
		if value := envValue(inherited, name); value != "" {
			return append(env, name+"="+value)
		}
	}
	return env
}

// prepareHome creates the per-harness home tree isolatedEnv points the child at:
// its HOME, TMPDIR, and the XDG config/state/cache dirs. TMPDIR must exist before
// the child starts — the launcher and the microsandbox runtime create temporary
// files through os.TempDir() while setting up a sandbox, so a TMPDIR that does
// not exist fails the run with `create prefill sandbox: io error: no such file
// or directory`.
func prepareHome(homeBase string) error {
	for _, dir := range []string{homeDirName, tmpDirName, configDirName, stateDirName, cacheDirName} {
		if err := mkdirAll(filepath.Join(homeBase, dir), 0o750); err != nil {
			return fmt.Errorf("create %s under %s: %w", dir, homeBase, err)
		}
	}
	return nil
}

// seedHomeFiles writes each home-relative file under the run's isolated HOME
// (<homeBase>/home/<rel>), creating parent directories, so the launcher
// provisions it into the VM (see driver.Spec.SeedHomeFiles).
//
// It writes through mkdirAll and writeFile, seams a test can replace to assert
// the seed's private modes without depending on the process umask.
func seedHomeFiles(homeBase string, files map[string]string) error {
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

// seedAgentSnippets writes each snippet into the agent's config dir under the
// run's isolated launcher config dir, where the launcher's snippet merge reads
// it (see driver.Spec.SeedAgentSnippets). A nil or empty map writes nothing.
//
// It uses the same mkdirAll/writeFile seams as seedHomeFiles.
func seedAgentSnippets(homeBase, agentConfigDirName string, snippets map[string]string) error {
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

// The seed tree and isolated launcher config dir seedLauncherConfig writes: the
// home-manifest seed sources live under <homeBase>/seed, and the launcher config
// under <homeBase>/config/<launcherConfigDirName>, the agents-sandbox dir inside
// XDG_CONFIG_HOME (configpaths.UserConfigDir).
const (
	seedDirName            = "seed"
	launcherConfigDirName  = "agents-sandbox"
	launcherConfigFileName = "config.json"
)

// seedLauncherConfig writes the run's isolated launcher config. It always
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
// It uses the same mkdirAll/writeFile seams as seedHomeFiles.
func seedLauncherConfig(homeBase string, files map[string]string) error {
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

// mkdirAll and writeFile are the filesystem seams seedHomeFiles writes through;
// mkdtemp is the seam resolveSuiteRoot creates a fresh root through, and absPath
// the seam it resolves the reuse root through. A test replaces one to assert its
// failure branch.
var (
	mkdirAll  = os.MkdirAll
	writeFile = os.WriteFile
	mkdtemp   = os.MkdirTemp
	absPath   = filepath.Abs
)
