package driver

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/hostlock"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
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

// SmokeRootReuseEnv points the suite at an existing directory it keeps between
// runs instead of a fresh temp root. The microsandbox image cache lives under
// the root's data dir, so a reused root skips the multi-GB
// `docker save | msb ImageLoad` that each harness prebuild otherwise repays.
// Local iteration only: unset (the default) creates and removes a fresh root.
const SmokeRootReuseEnv = "AGENTS_SANDBOX_SMOKE_ROOT"

// A sandbox's control-socket path is microsandbox's private home (MsbHomeUnderRoot)
// plus a fixed suffix microsandbox appends; it must stay below a Unix socket's
// sun_path limit (104 bytes on macOS, the tighter of the two targets) or every
// harness fails before its TUI with "sandbox runtime socket path is too long".
const (
	sandboxSocketPathSuffix = 50
	sunPathLimit            = 104
)

// SmokeMemoryGiB is the memory each smoke harness VM boots with — the VM's
// ceiling, passed to the launcher as --memory. A harness only has to reach its
// TUI, and the launcher's default project-VM size (4 GiB) multiplies across the
// parallel harnesses, so the smoke run boots each smaller.
const SmokeMemoryGiB = 2

// SmokeWorkingSetMiB is the host memory a smoke VM is assumed to occupy when
// the parallel-harness count is sized. A guest allocates only the pages it
// touches (virtio-mem), so the divisor is an expected working set, not
// SmokeMemoryGiB's ceiling. It tracks the heaviest harness, not the lightest:
// measured peaks are under 0.5 GiB for pi and over 1 GiB for opencode2, so
// sizing on pi would oversubscribe the host.
const SmokeWorkingSetMiB = 1536

// HarnessHostReserveMiB is held back from the host's free memory before the
// parallel-harness count is sized: it covers the host OS, the docker daemon,
// and the internal/sandbox/msb test binary, which runs one real VM at the same
// time as this suite.
const HarnessHostReserveMiB = 2048

// HarnessConcurrencyOverrideEnv forces the parallel-harness count, for a host
// the memory heuristic sizes badly or for debugging. Unset (or empty) uses the
// memory-derived default.
const HarnessConcurrencyOverrideEnv = "AGENTS_SANDBOX_MSB_PARALLEL"

// agentFlag is the launcher flag naming the agent a run or build drives.
const agentFlag = "--agent"

// SmokeMemorySpec is the launcher --memory value for a smoke VM.
func SmokeMemorySpec() string {
	return fmt.Sprintf("%dG", SmokeMemoryGiB)
}

// HarnessConcurrencyFor returns how many harnesses may run at once given the
// host's free memory in MiB: as many smoke VMs as fit after the host reserve,
// never fewer than one. HarnessConcurrency wraps it with the host reading, so
// the memory policy is testable without the host.
func HarnessConcurrencyFor(freeMiB int) int {
	if usable := freeMiB - HarnessHostReserveMiB; usable >= SmokeWorkingSetMiB {
		return usable / SmokeWorkingSetMiB
	}
	return 1
}

// HarnessConcurrency is the number of harness VMs to run at once: an explicit
// HarnessConcurrencyOverrideEnv override, else the count the host's free memory
// fits, else one. It is enforced by a semaphore in the smoke suite, not only by
// `-parallel`, so even a bare `go test -tags msb ./...` cannot start a VM per
// harness.
func HarnessConcurrency() int {
	//testenv:allow the smoke suite sizes its parallelism from its own override
	if override := os.Getenv(HarnessConcurrencyOverrideEnv); override != "" {
		if n, err := strconv.Atoi(override); err == nil && n > 0 {
			return n
		}
	}
	//testenv:allow the smoke suite sizes its parallelism from the host's RAM
	if free, ok := sysinfo.AvailableMemoryMiB(); ok {
		return HarnessConcurrencyFor(int(free))
	}
	//testenv:allow the smoke suite sizes its parallelism from the host's RAM
	if total := sysinfo.TotalMemoryGiB(); total > 0 {
		return HarnessConcurrencyFor(total * 1024)
	}
	return 1
}

// HarnessRunArgs is the `run` invocation a harness smoke subtest drives: the
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
func HarnessRunArgs(name, network string) []string {
	return []string{
		"run", agentFlag, name, "--network", network, "--memory", SmokeMemorySpec(), "--yes", "--no-rebuild",
	}
}

// DockerStorageShellArgs is the `shell` invocation the docker-storage sibling
// subtest drives: a docker-enabled opencode VM under the given network. It
// carries --no-rebuild for the same reason HarnessRunArgs does.
func DockerStorageShellArgs(network string) []string {
	return []string{"shell", agentFlag, opencodeAgentName, "--docker", "--network", network, "--no-rebuild"}
}

// smokeVMCPUsEnv caps each smoke VM's vCPU count. Unset (the default) leaves the
// launcher's own default, every host CPU. The KVM guest sets it: a nested guest
// has few vCPUs next to the host, so parallel harnesses each claiming every vCPU
// oversubscribe it and slow every VM start.
const smokeVMCPUsEnv = "AGENTS_SANDBOX_MSB_VM_CPUS"

// cpusFlag is the launcher flag a vCPU-capped smoke run appends (see
// WithVMCPUs).
const cpusFlag = "cpus"

// SmokeVMCPUs is the per-VM vCPU cap each smoke run carries, read from the
// environment: the KVM guest sets smokeVMCPUsEnv so parallel harnesses do not
// each claim every guest vCPU. Zero (unset or invalid) leaves the launcher's own
// default; smokeVMCPUsFromValue holds the parsing policy.
func SmokeVMCPUs() uint8 {
	//testenv:allow the smoke suite reads its own per-VM vCPU cap
	return smokeVMCPUsFromValue(os.Getenv(smokeVMCPUsEnv))
}

// smokeVMCPUsFromValue parses the vCPU cap: a positive, 8-bit integer caps each
// smoke VM; anything else (unset, zero, non-numeric, out of range) leaves the
// launcher's default. Kept pure so it is testable without the host, like
// HarnessConcurrencyFor.
func smokeVMCPUsFromValue(value string) uint8 {
	n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 8)
	if err != nil || n == 0 {
		return 0
	}
	return uint8(n)
}

// WithVMCPUs appends the launcher's --cpus flag when cpus is non-zero, leaving
// args unchanged otherwise, so a run with no cap set is byte-for-byte what the
// suite has always driven.
func WithVMCPUs(args []string, cpus uint8) []string {
	if cpus == 0 {
		return args
	}
	return append(args, "--"+cpusFlag, strconv.Itoa(int(cpus)))
}

// smokeTimeoutFactorEnv scales the smoke suite's readiness and quit ceilings.
// Unset (the default) leaves them at 1, every host value. The KVM guest sets
// it: a nested guest starts a VM and its services far slower, so the fixed
// ceilings that fit a host time out there.
const smokeTimeoutFactorEnv = "AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR"

// SmokeTimeoutFactor is the multiplier every smoke readiness and quit ceiling
// carries, read from the environment: the KVM guest sets smokeTimeoutFactorEnv,
// so a nested guest's slower VM start does not blow a budget sized for a host.
// One (unset or invalid) leaves every ceiling unchanged.
func SmokeTimeoutFactor() float64 {
	//testenv:allow the smoke suite reads its own timeout factor
	return timeoutFactorFromValue(os.Getenv(smokeTimeoutFactorEnv))
}

// timeoutFactorFromValue parses the timeout factor: a positive, finite float
// scales the ceilings; anything else (unset, zero, negative, NaN, infinite,
// non-numeric) leaves them at 1, so a host run is unchanged. Kept pure so it is
// testable without the host, like smokeVMCPUsFromValue.
func timeoutFactorFromValue(value string) float64 {
	factor, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(factor) || math.IsInf(factor, 0) || factor <= 0 {
		return 1
	}
	return factor
}

// ScaleTimeout returns d multiplied by factor to the nearest nanosecond. A
// factor of 1 is the identity, so an unset factor leaves every ceiling exactly
// what the suite has always used.
func ScaleTimeout(d time.Duration, factor float64) time.Duration {
	if factor == 1 {
		return d
	}
	return time.Duration(float64(d) * factor)
}

// sandboxNames parses `sandbox list -q` output into one trimmed name per
// non-blank line. A name is a whole line, so it is trimmed, not split.
func sandboxNames(output string) []string {
	var names []string
	for line := range strings.SplitSeq(output, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// MsbHomeUnderRoot is the private microsandbox home a suite root derives.
func MsbHomeUnderRoot(root string) string {
	return filepath.Join(root, dataDirName, "agents-sandbox", "msb")
}

// RootFitsSandboxSocket reports whether root's derived sandbox control-socket
// path stays below the sun_path limit.
func RootFitsSandboxSocket(root string) bool {
	return len(MsbHomeUnderRoot(root))+sandboxSocketPathSuffix < sunPathLimit
}

// reuseLockSuffix names the lock file that serializes runs sharing a reused
// suite root. The lock sits beside the root, not inside it, so it never lands
// in the microsandbox home under it.
const reuseLockSuffix = ".lock"

// reuseLockName is the lock file's base name for the root dir: the root's own
// base name plus reuseLockSuffix. The lock is keyed by name and held in the
// root's parent dir (see ReuseLockPath and AcquireReuseLock).
func reuseLockName(dir string) string {
	return filepath.Base(dir) + reuseLockSuffix
}

// ReuseLockPath is the lock file that serializes runs sharing the reused root
// dir.
func ReuseLockPath(dir string) string {
	return filepath.Join(filepath.Dir(dir), reuseLockName(dir))
}

// AcquireReuseLock blocks until this run holds the exclusive lock for the reused
// root dir, waiting out any other run that shares it. Two runs that share a root
// would otherwise race on the sandbox sweep: whichever finishes first removes
// every sandbox in the home, including the other's live VMs. dir is absolute,
// so the lock path is stable across working directories. It returns the release.
func AcquireReuseLock(dir string) (func(), error) {
	return hostlock.AcquireExclusive(filepath.Dir(dir), reuseLockName(dir))
}

// ResolveSuiteRoot returns the suite's shared root, whether it is a reused
// directory, and the release for the exclusive lock a reused directory is held
// under (nil for a fresh root). A set SmokeRootReuseEnv reuses that directory —
// resolved to an absolute path and validated to fit the sandbox socket path
// budget — so the microsandbox image cache under it survives between runs;
// otherwise a fresh temp root is created under smokeRootBase() (the
// smokeRootBaseEnv override, else smokeRootBaseDefault) for the caller to remove,
// validated to the same budget. The lock serializes runs that share a root, so a
// finishing run's sandbox sweep cannot destroy VMs another run is still using.
func ResolveSuiteRoot() (string, bool, func(), error) {
	//testenv:allow the smoke suite reads its own reuse-root override
	if dir := os.Getenv(SmokeRootReuseEnv); dir != "" {
		reuseRoot, err := absPath(dir)
		if err != nil {
			return "", false, nil, fmt.Errorf("resolve reuse root %s: %w", dir, err)
		}
		if !RootFitsSandboxSocket(reuseRoot) {
			return "", false, nil, fmt.Errorf(
				"%s=%q derives a %d-byte sandbox control-socket path, at or above the %d-byte sun_path limit",
				SmokeRootReuseEnv, reuseRoot,
				len(MsbHomeUnderRoot(reuseRoot))+sandboxSocketPathSuffix, sunPathLimit)
		}
		// reuseRoot is an explicit operator opt-in (the reuse root), not request
		// data.
		if err = os.MkdirAll(reuseRoot, 0o750); err != nil {
			return "", false, nil, fmt.Errorf("create reuse root %s: %w", reuseRoot, err)
		}
		release, err := AcquireReuseLock(reuseRoot)
		if err != nil {
			return "", false, nil, fmt.Errorf("lock reuse root %s: %w", reuseRoot, err)
		}
		return reuseRoot, true, release, nil
	}
	root, err := mkdtemp(smokeRootBase(), smokeRootPrefix)
	if err != nil {
		return "", false, nil, err
	}
	if !RootFitsSandboxSocket(root) {
		_ = os.Remove(root)
		return "", false, nil, fmt.Errorf(
			"suite root %q derives a %d-byte sandbox control-socket path, at or above the %d-byte sun_path limit; set %s to a shorter base",
			root,
			len(MsbHomeUnderRoot(root))+sandboxSocketPathSuffix,
			sunPathLimit,
			smokeRootBaseEnv,
		)
	}
	return root, false, nil, nil
}

// mkdtemp is the seam ResolveSuiteRoot creates a fresh root through, and absPath
// the seam it resolves the reuse root through. A test replaces one to assert its
// failure branch.
var (
	mkdtemp = os.MkdirTemp
	absPath = filepath.Abs
)
