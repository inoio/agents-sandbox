package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/Masterminds/semver/v3"

	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/upgrade"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	runtimeIssueURL = "https://github.com/inoio/agents-sandbox/issues/new?template=bug_report.yml"
	windowsOS       = "windows"
	msbBinaryName   = "msb"
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
type Inspection struct {
	AgentsSandboxVersion string
	RequiredVersion      string
	InstalledVersion     string
	MSBPath              string
	MSBHome              string
	LibkrunfwPath        string
	Relation             Relation
	External             bool
}

// PreparationResult reports whether the caller must exit so a replaced
// agents-sandbox binary can be used by a fresh process.
type PreparationResult struct {
	Restart bool
}

//nolint:gochecknoglobals // process-wide SDK initialization is intentional
var runtimeState = struct {
	mutex    sync.Mutex
	prepared bool
}{mutex: sync.Mutex{}, prepared: false}

// Command seams keep runtime recovery unit-testable without touching the host
// runtime or replacing the test executable.
//
//nolint:gochecknoglobals // command seams are required for safe recovery tests
var (
	runMSBVersion = func(path string) (string, error) {
		output, err := exec.Command(path, "--version").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(output)), nil
	}
	runMSBDowngrade = func(ctx context.Context, path, version string, stdout, stderr io.Writer) error {
		command := exec.CommandContext(ctx, path, "downgrade", version, "--yes")
		command.Stdout = stdout
		command.Stderr = stderr
		return command.Run()
	}
	latestAgentsSandboxVersion = upgrade.LatestVersion
	updateAgentsSandbox        = upgrade.Update
	ensureRuntime              = func(ctx context.Context) error {
		return msbSdk.EnsureInstalled(ctx)
	}
	validateRuntime = sandboxmsb.ValidateInstalled
) //nolint:gochecknoglobals // command seams are required for safe recovery tests

// runtimeClientIsReal is a seam for testing the public preparation gate while
// keeping normal command tests on their mocked msb client.
var runtimeClientIsReal = sandboxmsb.IsRealClient //nolint:gochecknoglobals // test seam

var userHomeDir = os.UserHomeDir //nolint:gochecknoglobals // test seam

// InspectRuntime identifies the msb executable selected by the SDK before the
// SDK itself initializes. It intentionally does not call any SDK operation.
func InspectRuntime() (Inspection, error) {
	requiredVersion := msbSdk.SDKVersion()
	return inspectRuntime(requiredVersion)
}

// inspectRuntime contains the version-independent inspection logic so invalid
// SDK metadata can be tested without mutating the linked SDK package.
func inspectRuntime(requiredVersion string) (Inspection, error) {
	home, err := runtimeHome()
	if err != nil {
		return Inspection{}, err
	}
	msbPath, external, err := runtimeMSBPath(home)
	if err != nil {
		return Inspection{}, err
	}
	inspection := Inspection{ //nolint:exhaustruct // optional fields are populated below
		RequiredVersion: requiredVersion,
		MSBHome:         home,
		MSBPath:         msbPath,
		External:        external,
		Relation:        RuntimeMissing,
	}
	inspection.External = inspection.External || os.Getenv("MSB_LIBKRUNFW_PATH") != ""
	inspection.LibkrunfwPath = runtimeLibraryPath(home, msbPath)

	if _, statErr := os.Stat(msbPath); statErr != nil {
		if os.IsNotExist(statErr) {
			return inspection, nil
		}
		return Inspection{}, fmt.Errorf("inspect msb executable %s: %w", msbPath, statErr)
	}
	if inspection.LibkrunfwPath == "" {
		inspection.Relation = RuntimeIncomplete
		return inspection, nil
	}
	versionOutput, versionErr := runMSBVersion(msbPath)
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
	inspection.Relation, err = compareRuntimeVersions(requiredVersion, installedVersion)
	if err != nil {
		inspection.Relation = RuntimeUnknown
	}
	return inspection, nil
}

//nolint:gochecknoglobals // test seam for pre-SDK runtime inspection
var inspectRuntimeFunc = inspectRuntime

// PrepareRuntime inspects and resolves the runtime before any SDK operation.
// Mock msb clients used by command tests do not need host runtime setup.
func PrepareRuntime(ctx context.Context, ui termio.UI, launcherVersion string) (PreparationResult, error) {
	if !runtimeClientIsReal() {
		return PreparationResult{}, nil
	}
	return prepareRuntime(ctx, ui, launcherVersion)
}

func prepareRuntime(ctx context.Context, ui termio.UI, launcherVersion string) (PreparationResult, error) {
	runtimeState.mutex.Lock()
	defer runtimeState.mutex.Unlock()
	if runtimeState.prepared {
		return PreparationResult{}, nil
	}

	inspection, err := inspectRuntimeFunc(msbSdk.SDKVersion())
	if err != nil {
		return PreparationResult{}, err
	}
	inspection.AgentsSandboxVersion = launcherVersion
	ui.Verbosef(
		"microsandbox runtime: required=%s installed=%s path=%s home=%s",
		inspection.RequiredVersion,
		valueOr(inspection.InstalledVersion, "missing"),
		inspection.MSBPath,
		inspection.MSBHome,
	)

	switch inspection.Relation {
	case RuntimeEqual:
		if !inspection.External {
			if err := validateRuntime(ctx); err != nil {
				return PreparationResult{}, fmt.Errorf("validate msb runtime: %w", err)
			}
		}
		sandboxmsb.SkipRuntimeInstall()
		runtimeState.prepared = true
		return PreparationResult{}, nil
	case RuntimeMissing:
		if inspection.External {
			return recoverRuntimeMismatch(ctx, ui, launcherVersion, inspection)
		}
		if err := ensureRuntime(ctx); err != nil {
			return PreparationResult{}, fmt.Errorf("install msb runtime: %w", err)
		}
		if err := validatePreparedRuntime(ctx); err != nil {
			return PreparationResult{}, err
		}
		sandboxmsb.SkipRuntimeInstall()
		runtimeState.prepared = true
		return PreparationResult{}, nil
	case RuntimeNewer, RuntimeOlder, RuntimeUnknown, RuntimeIncomplete:
		return recoverRuntimeMismatch(ctx, ui, launcherVersion, inspection)
	default:
		return PreparationResult{}, fmt.Errorf("unknown msb runtime relation %q", inspection.Relation)
	}
}

func recoverRuntimeMismatch(
	ctx context.Context,
	ui termio.UI,
	launcherVersion string,
	inspection Inspection,
) (PreparationResult, error) {
	if !ui.IsInteractive() {
		return PreparationResult{}, nonInteractiveMismatchError(ui, inspection)
	}

	choices := mismatchChoices(ctx, launcherVersion, inspection)
	key, err := ui.Select(mismatchPrompt(inspection), choices, "q")
	if err != nil {
		return PreparationResult{}, fmt.Errorf("select msb recovery action: %w", err)
	}

	switch key {
	case "a":
		latest, latestErr := latestAgentsSandboxVersion(ctx)
		if latestErr != nil {
			return PreparationResult{}, fmt.Errorf("find newer agents-sandbox release: %w", latestErr)
		}
		ui.Infof("upgrading agents-sandbox %s -> %s", launcherVersion, latest)
		if err := updateAgentsSandbox(ctx, latest); err != nil {
			return PreparationResult{}, fmt.Errorf("upgrade agents-sandbox: %w", err)
		}
		ui.Infof("agents-sandbox upgraded to %s; restart to retry with the new runtime", latest)
		return PreparationResult{Restart: true}, nil
	case "u":
		if err := ensureRuntime(ctx); err != nil {
			return PreparationResult{}, fmt.Errorf("upgrade msb runtime: %w", err)
		}
		return completeRuntimePreparation(ctx)
	case "d":
		if inspection.External {
			return PreparationResult{}, runtimeMismatchError(
				inspection,
				"the selected runtime is externally configured; downgrade it manually",
			)
		}
		if err := runMSBDowngrade(
			ctx,
			inspection.MSBPath,
			inspection.RequiredVersion,
			ui.StdOut(),
			ui.StdErr(),
		); err != nil {
			return PreparationResult{}, fmt.Errorf("downgrade msb runtime: %w", err)
		}
		return completeRuntimePreparation(ctx)
	case "b":
		ui.Warnf(
			"brave mode is unsupported: using msb %s with agents-sandbox SDK %s without changing the runtime",
			inspection.InstalledVersion,
			inspection.RequiredVersion,
		)
		sandboxmsb.SkipRuntimeInstall()
		runtimeState.prepared = true
		return PreparationResult{}, nil
	case "i":
		showIssueReport(ui, inspection)
		return PreparationResult{}, runtimeMismatchError(inspection, "user chose to submit an issue")
	case "q", "":
		return PreparationResult{}, runtimeMismatchError(inspection, "user chose not to change the runtime")
	default:
		return PreparationResult{}, fmt.Errorf("unknown msb recovery action %q", key)
	}
}

func mismatchChoices(ctx context.Context, launcherVersion string, inspection Inspection) []termio.Choice {
	choices := make([]termio.Choice, 0, 5)
	issueDescription := "show a sanitized compatibility report and issue URL"
	if inspection.Relation == RuntimeNewer && launcherVersion != "" && launcherVersion != "dev" {
		if latest, err := latestAgentsSandboxVersion(ctx); err == nil {
			if newer, compareErr := upgrade.IsNewer(launcherVersion, latest); compareErr == nil && newer {
				choices = append(choices, termio.Choice{
					Label:       "Upgrade agents-sandbox and restart",
					Key:         "a",
					Description: "install " + latest + " and retry with its microsandbox SDK",
				})
			} else if compareErr == nil {
				issueDescription = "request an agents-sandbox release supporting msb " + inspection.InstalledVersion
			}
		}
	}
	if inspection.Relation == RuntimeNewer && !inspection.External {
		choices = append(choices, termio.Choice{
			Label:       "Downgrade msb",
			Key:         "d",
			Description: "run the official rollback to " + inspection.RequiredVersion + " with a database backup",
		})
	}
	if (inspection.Relation == RuntimeOlder || inspection.Relation == RuntimeIncomplete) && !inspection.External {
		choices = append(choices, termio.Choice{
			Label:       "Upgrade msb",
			Key:         "u",
			Description: "install the SDK-required runtime " + inspection.RequiredVersion,
		})
	}
	if inspection.Relation == RuntimeNewer || inspection.Relation == RuntimeOlder {
		choices = append(choices, termio.Choice{
			Label:       "Brave mode",
			Key:         "b",
			Description: "use the installed runtime without changing it (unsupported)",
		})
	}
	choices = append(choices, termio.Choice{
		Label:       "Submit an issue",
		Key:         "i",
		Description: issueDescription,
	})
	choices = append(choices, termio.Choice{
		Label:       "Quit",
		Key:         "q",
		Description: "leave the runtime unchanged",
	})
	return choices
}

func mismatchPrompt(inspection Inspection) string {
	return fmt.Sprintf(
		"agents-sandbox requires msb %s, but the selected runtime is %s at %s",
		inspection.RequiredVersion,
		valueOr(inspection.InstalledVersion, "unknown"),
		inspection.MSBPath,
	)
}

func nonInteractiveMismatchError(ui termio.UI, inspection Inspection) error {
	ui.Warnf("%s", mismatchPrompt(inspection))
	showIssueReport(ui, inspection)
	return runtimeMismatchError(inspection, "interactive runtime recovery is unavailable")
}

func showIssueReport(ui termio.UI, inspection Inspection) {
	ui.Errorf("No runtime change was made. Submit a compatibility issue: %s", runtimeIssueURL)
	ui.Errorf(
		"diagnostics: agents-sandbox=%s; SDK requires msb %s; selected msb=%s; installed msb=%s; MSB_HOME=%s; host=%s/%s",
		valueOr(inspection.AgentsSandboxVersion, "unknown"),
		inspection.RequiredVersion,
		sanitizeRuntimePath(inspection.MSBPath, inspection.MSBHome),
		valueOr(inspection.InstalledVersion, "unknown"),
		sanitizeRuntimePath(inspection.MSBHome, inspection.MSBHome),
		runtime.GOOS,
		runtime.GOARCH,
	)
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

func completeRuntimePreparation(ctx context.Context) (PreparationResult, error) {
	if err := validatePreparedRuntime(ctx); err != nil {
		return PreparationResult{}, err
	}
	sandboxmsb.SkipRuntimeInstall()
	runtimeState.prepared = true
	return PreparationResult{}, nil
}

func validatePreparedRuntime(ctx context.Context) error {
	inspection, err := inspectRuntimeFunc(msbSdk.SDKVersion())
	if err != nil {
		return err
	}
	if inspection.Relation != RuntimeEqual {
		return runtimeMismatchError(inspection, "runtime alignment did not complete")
	}
	if !inspection.External {
		if err := validateRuntime(ctx); err != nil {
			return fmt.Errorf("validate aligned msb runtime: %w", err)
		}
	}
	return nil
}

func runtimeHome() (string, error) {
	if home := os.Getenv("MSB_HOME"); home != "" {
		return home, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".microsandbox"), nil
}

func runtimeMSBPath(home string) (string, bool, error) {
	if path, ok := os.LookupEnv("MSB_PATH"); ok {
		if strings.TrimSpace(path) == "" {
			return "", true, errors.New("MSB_PATH is set but empty")
		}
		return path, true, nil
	}
	return filepath.Join(home, "bin", msbFilename()), false, nil
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
	if userHome, err := userHomeDir(); err == nil {
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

func runtimeLibraryPath(home, msbPath string) string {
	if path := os.Getenv("MSB_LIBKRUNFW_PATH"); path != "" {
		if _, err := os.Stat(
			filepath.Clean(path),
		); err == nil {
			return path
		}
		return ""
	}
	filename := libkrunfwFilename()
	resolvedMSBPath := msbPath
	if resolved, err := filepath.EvalSymlinks(msbPath); err == nil {
		resolvedMSBPath = resolved
	}
	candidates := make([]string, 0, 3)
	if _, explicitPath := os.LookupEnv("MSB_PATH"); explicitPath {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(resolvedMSBPath), filename),
			filepath.Join(filepath.Dir(resolvedMSBPath), "..", "lib", filename),
			filepath.Join(home, "lib", filename),
		)
	} else {
		candidates = append(candidates,
			filepath.Join(home, "lib", filename),
			filepath.Join(filepath.Dir(resolvedMSBPath), filename),
			filepath.Join(filepath.Dir(resolvedMSBPath), "..", "lib", filename),
		)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
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
