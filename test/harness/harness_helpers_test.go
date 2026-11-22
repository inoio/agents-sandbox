// Package harness holds end-to-end smoke tests that start every registered
// coding-agent harness in a real microsandbox VM and assert it reaches its
// full-screen TUI. The VM-driven test lives behind the `msb` build tag; the
// untagged helpers here are compiled only into test binaries — the suite root
// and reuse lock.
package harness

import (
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
	return filepath.Join(root, driver.DataDirName, "agents-sandbox", "msb")
}

// rootFitsSandboxSocket reports whether root's derived sandbox control-socket
// path stays below the sun_path limit.
func rootFitsSandboxSocket(root string) bool {
	return len(msbHomeUnderRoot(root))+sandboxSocketPathSuffix < sunPathLimit
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

// mkdtemp is the seam resolveSuiteRoot creates a fresh root through, and absPath
// the seam it resolves the reuse root through. A test replaces one to assert its
// failure branch.
var (
	mkdtemp = os.MkdirTemp
	absPath = filepath.Abs
)
