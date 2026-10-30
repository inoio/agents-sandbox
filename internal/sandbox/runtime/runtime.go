package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/inoio/agents-sandbox/internal/hostlock"

	"github.com/Masterminds/semver/v3"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	windowsOS     = "windows"
	msbBinaryName = "msb"
)

// Relation describes the relationship between the runtime selected for
// this process and the SDK version linked into agents-sandbox.
type Relation string

const (
	RuntimeEqual      Relation = "equal"
	RuntimeNewer      Relation = "newer"
	RuntimeOlder      Relation = "older"
	RuntimeUnknown    Relation = "unknown"
	RuntimeMissing    Relation = "missing"
	RuntimeIncomplete Relation = "incomplete"
)

// Inspection contains the runtime paths and version information used by
// the mismatch decision. It is collected before the SDK is allowed to load its
// FFI library or open the runtime database.
//
//exhaustruct:ignore
type Inspection struct {
	AgentsSandboxVersion string
	RequiredVersion      string
	InstalledVersion     string
	MSBPath              string
	MSBHome              string
	LibkrunfwPath        string
	Relation             Relation
}

type selectedRuntime struct {
	Home          string
	MSBPath       string
	LibkrunfwPath string
}

var runtimeState = struct {
	mutex    sync.Mutex
	prepared bool
}{mutex: sync.Mutex{}, prepared: false}

// Command seams keep runtime inspection unit-testable without touching the host
// runtime or replacing the test executable.
var (
	runMSBVersion = func(path string) (string, error) {
		output, err := exec.Command(path, "--version").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(output)), nil
	}
	// msbEnsureRuntime/msbInstallRuntime wrap the SDK setup calls so tests can
	// observe the RuntimeConfig passed to them. Every setup call names the
	// private home explicitly (see runtimeConfig) rather than relying on the
	// process-global MSB_HOME.
	msbEnsureRuntime  = msbSdk.EnsureRuntime
	msbInstallRuntime = msbSdk.InstallRuntime
	ensureRuntime     = func(ctx context.Context) error {
		_, err := msbEnsureRuntime(ctx, runtimeConfig(), msbSdk.InstallOptions{})
		return err
	}
	installRuntime = func(ctx context.Context) error {
		_, err := msbInstallRuntime(ctx, runtimeConfig(), msbSdk.InstallOptions{Force: true})
		return err
	}
	validateRuntime = sandboxmsb.ValidateInstalled
)

// runtimeConfig names the private home explicitly so SDK runtime resolution and
// installation are deterministic and order-independent, not dependent on whether
// ApplyHome has set the process-global MSB_HOME yet.
func runtimeConfig() msbSdk.RuntimeConfig {
	return msbSdk.RuntimeConfig{Home: PrivateHome()}
}

// runtimeClientIsReal is a seam for testing the public preparation gate while
// keeping normal command tests on their mocked msb client.
var runtimeClientIsReal = sandboxmsb.IsRealClient

// inspectRuntime contains the version-independent inspection logic so invalid
// SDK metadata can be tested without mutating the linked SDK package.
func inspectRuntime(requiredVersion string) (Inspection, error) {
	selected := selectRuntime()
	inspection := Inspection{
		RequiredVersion: requiredVersion,
		MSBHome:         selected.Home,
		MSBPath:         selected.MSBPath,
		LibkrunfwPath:   selected.LibkrunfwPath,
		Relation:        RuntimeMissing,
	}

	msbPresent, statErr := runtimeFilePresent(selected.MSBPath)
	if statErr != nil {
		return Inspection{}, fmt.Errorf("inspect msb executable %s: %w", selected.MSBPath, statErr)
	}
	libkrunfwPresent, statErr := runtimeFilePresent(selected.LibkrunfwPath)
	if statErr != nil {
		return Inspection{}, fmt.Errorf("inspect libkrunfw %s: %w", selected.LibkrunfwPath, statErr)
	}
	if !msbPresent && !libkrunfwPresent {
		return inspection, nil
	}
	if !msbPresent || !libkrunfwPresent {
		inspection.Relation = RuntimeIncomplete
		return inspection, nil
	}
	versionOutput, versionErr := runMSBVersion(inspection.MSBPath)
	if versionErr != nil {
		inspection.Relation = RuntimeUnknown
		return inspection, nil //nolint:nilerr // malformed version output is classified as unknown
	}
	installedVersion, parseErr := parseMSBVersion(versionOutput)
	if parseErr != nil {
		inspection.Relation = RuntimeUnknown
		return inspection, nil //nolint:nilerr // malformed version output is classified as unknown
	}
	inspection.InstalledVersion = installedVersion
	relation, err := compareRuntimeVersions(requiredVersion, installedVersion)
	if err != nil {
		inspection.Relation = RuntimeUnknown
		return inspection, nil //nolint:nilerr // invalid version metadata is classified as unknown
	}
	inspection.Relation = relation
	return inspection, nil
}

func selectRuntime() selectedRuntime {
	home := PrivateHome()
	return selectedRuntime{
		Home:          home,
		MSBPath:       filepath.Join(home, "bin", msbFilename()),
		LibkrunfwPath: filepath.Join(home, "lib", libkrunfwFilename()),
	}
}

func runtimeFilePresent(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	info, err := os.Stat(path)
	if err == nil {
		return info.Mode().IsRegular(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

var inspectRuntimeFunc = inspectRuntime

// PrepareRuntime inspects and resolves the runtime before any SDK operation.
// Mock msb clients used by command tests do not need host runtime setup.
func PrepareRuntime(ctx context.Context, ui termio.UI, launcherVersion string) error {
	if !runtimeClientIsReal() {
		return nil
	}
	return prepareRuntime(ctx, ui, launcherVersion)
}

// ApplyPreviewHome points the SDK at the private runtime home for a read-only
// preview without inspecting or installing the runtime. The preview still reads
// the private home (volumes, sandboxes) so it reports the objects a real run
// would use, but it must not download or install the runtime (#424). Mock msb
// clients used by command tests need no host runtime setup.
func ApplyPreviewHome() error {
	if !runtimeClientIsReal() {
		return nil
	}
	if _, err := ApplyHome(); err != nil {
		return fmt.Errorf("apply microsandbox home: %w", err)
	}
	return nil
}

func prepareRuntime(ctx context.Context, ui termio.UI, launcherVersion string) error {
	runtimeState.mutex.Lock()
	defer runtimeState.mutex.Unlock()
	if runtimeState.prepared {
		return nil
	}

	if _, err := ApplyHome(); err != nil {
		return fmt.Errorf("apply microsandbox home: %w", err)
	}
	inspection, err := inspectRuntimeFunc(msbSdk.SDKVersion())
	if err != nil {
		return err
	}
	inspection.AgentsSandboxVersion = launcherVersion
	ui.Verbosef(
		"microsandbox runtime: launcher=%s required=%s installed=%s path=%s home=%s",
		inspection.AgentsSandboxVersion,
		inspection.RequiredVersion,
		valueOr(inspection.InstalledVersion, "missing"),
		inspection.MSBPath,
		inspection.MSBHome,
	)

	switch inspection.Relation {
	case RuntimeEqual:
		if err := validateRuntime(ctx); err != nil {
			return fmt.Errorf("validate msb runtime: %w", err)
		}
		sandboxmsb.SkipRuntimeInstall()
		runtimeState.prepared = true
		return nil
	case RuntimeMissing:
		if err := ensureRuntime(ctx); err != nil {
			return fmt.Errorf("install msb runtime: %w", err)
		}
		if err := validatePreparedRuntime(ctx); err != nil {
			return err
		}
		sandboxmsb.SkipRuntimeInstall()
		runtimeState.prepared = true
		return nil
	case RuntimeNewer, RuntimeOlder, RuntimeUnknown, RuntimeIncomplete:
		if err := repairRuntime(ctx); err != nil {
			return err
		}
		sandboxmsb.SkipRuntimeInstall()
		runtimeState.prepared = true
		return nil
	default:
		return fmt.Errorf("unknown msb runtime relation %q", inspection.Relation)
	}
}

// repairRuntime provisions the SDK-pinned runtime into the private home when the
// pair there is stale or incomplete. The private home is ours alone, so a
// mismatch can only come from one of our own earlier releases pinning a
// different SDK — never from the user — and is repaired silently. The installer
// replaces a complete pair under Force but refuses to repair an incomplete one,
// so any stray file from a partial install is removed first.
//
// Repair is serialized across processes by a host-only flock, and the runtime is
// re-inspected under it: a concurrent session may have repaired the pair between
// our first inspection and acquiring the lock, and acting on that stale
// inspection could delete the pair it just installed.
func repairRuntime(ctx context.Context) error {
	release, err := acquireRuntimeLock()
	if err != nil {
		return err
	}
	defer release()

	current, err := inspectRuntimeFunc(msbSdk.SDKVersion())
	if err != nil {
		return err
	}
	if current.Relation == RuntimeEqual {
		return validateRuntime(ctx)
	}
	if current.Relation == RuntimeIncomplete {
		if err := removeRuntimeFiles(current); err != nil {
			return fmt.Errorf("remove incomplete msb runtime: %w", err)
		}
	}
	if err := installRuntime(ctx); err != nil {
		return fmt.Errorf("repair msb runtime: %w", err)
	}
	return validatePreparedRuntime(ctx)
}

// removeRuntimeFiles deletes the private-home runtime pair, tolerating files
// that are already absent.
func removeRuntimeFiles(inspection Inspection) error {
	for _, path := range []string{inspection.MSBPath, inspection.LibkrunfwPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// acquireRuntimeLock takes the host-only advisory lock that serializes runtime
// repair across processes. The returned function releases the lock.
func acquireRuntimeLock() (func(), error) {
	release, err := hostlock.AcquireExclusive(runtimeLockDir(), runtimeLockName)
	if err != nil {
		return nil, fmt.Errorf("acquire runtime lock: %w", err)
	}
	return release, nil
}

const runtimeLockName = "runtime.lock"

func runtimeLockDir() string {
	return filepath.Join(PrivateHome(), ".agents-sandbox")
}

func runtimeMismatchError(inspection Inspection, reason string) error {
	return fmt.Errorf(
		"msb runtime mismatch: required %s, installed %s at %s: %s",
		inspection.RequiredVersion,
		valueOr(inspection.InstalledVersion, "unknown"),
		sanitizeRuntimePath(inspection.MSBPath, inspection.MSBHome),
		reason,
	)
}

func validatePreparedRuntime(ctx context.Context) error {
	inspection, err := inspectRuntimeFunc(msbSdk.SDKVersion())
	if err != nil {
		return err
	}
	if inspection.Relation != RuntimeEqual {
		return runtimeMismatchError(inspection, "runtime alignment did not complete")
	}
	if err := validateRuntime(ctx); err != nil {
		return fmt.Errorf("validate aligned msb runtime: %w", err)
	}
	return nil
}

func sanitizeRuntimePath(path, home string) string {
	if path == "" {
		return "<unset>"
	}
	cleanPath := filepath.Clean(path)
	if home != "" {
		if relative, err := filepath.Rel(
			filepath.Clean(home),
			cleanPath,
		); err == nil && relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return filepath.Join("$MSB_HOME", relative)
		}
	}
	if userHome, err := configpaths.Get().UserHomeDir(); err == nil {
		if relative, relErr := filepath.Rel(
			filepath.Clean(userHome),
			cleanPath,
		); relErr == nil && relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return filepath.Join("$HOME", relative)
		}
	}
	return filepath.Base(cleanPath)
}

func msbFilename() string {
	return msbFilenameFor(runtime.GOOS)
}

func libkrunfwFilename() string {
	return libkrunfwFilenameFor(runtime.GOOS)
}

func msbFilenameFor(goos string) string {
	if goos == windowsOS {
		return "msb.exe"
	}
	return msbBinaryName
}

func libkrunfwFilenameFor(goos string) string {
	switch goos {
	case "darwin":
		return "libkrunfw.5.dylib"
	case windowsOS:
		return "libkrunfw.dll"
	default:
		return "libkrunfw.so.5.6.1"
	}
}

func parseMSBVersion(output string) (string, error) {
	output = strings.TrimSpace(output)
	if !strings.HasPrefix(output, "msb ") {
		return "", fmt.Errorf("unexpected msb version output %q", output)
	}
	version := strings.TrimSpace(strings.TrimPrefix(output, "msb "))
	if _, err := semver.NewVersion(version); err != nil {
		return "", fmt.Errorf("invalid msb version %q: %w", version, err)
	}
	return version, nil
}

func compareRuntimeVersions(required, installed string) (Relation, error) {
	requiredVersion, err := semver.NewVersion(required)
	if err != nil {
		return RuntimeUnknown, err
	}
	installedVersion, err := semver.NewVersion(installed)
	if err != nil {
		return RuntimeUnknown, err
	}
	switch {
	case installedVersion.Equal(requiredVersion):
		return RuntimeEqual, nil
	case installedVersion.GreaterThan(requiredVersion):
		return RuntimeNewer, nil
	default:
		return RuntimeOlder, nil
	}
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
