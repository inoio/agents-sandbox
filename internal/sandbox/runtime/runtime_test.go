package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestInspectRuntimeEqual(t *testing.T) {
	home := writeRuntimeFixture(t, "0.6.17", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	inspection, err := InspectRuntime()
	if err != nil {
		t.Fatalf("InspectRuntime() error = %v", err)
	}
	if inspection.Relation != RuntimeEqual {
		t.Fatalf("Relation = %q, want %q", inspection.Relation, RuntimeEqual)
	}
	if inspection.InstalledVersion != "0.6.17" {
		t.Errorf("InstalledVersion = %q, want 0.6.17", inspection.InstalledVersion)
	}
}

func TestInspectRuntimeCustomMSBHomeRemainsManaged(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	libDir := filepath.Join(home, "lib")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(binDir, "msb"),
		[]byte("#!/bin/sh\nprintf 'msb 0.6.17\\n'\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "libkrunfw.so.5.6.1"), []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MSB_HOME", home)
	unsetEnv(t, "MSB_PATH")

	inspection, err := InspectRuntime()
	if err != nil {
		t.Fatalf("InspectRuntime() error = %v", err)
	}
	if inspection.External {
		t.Fatal("MSB_HOME alone must not mark the runtime external")
	}
}

func TestInspectRuntimeNewerAndOlder(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    Relation
	}{
		{name: "newer", version: "0.6.18", want: RuntimeNewer},
		{name: "older", version: "0.6.16", want: RuntimeOlder},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := writeRuntimeFixture(t, test.version, true)
			t.Setenv("HOME", home)
			unsetEnv(t, "MSB_HOME")
			unsetEnv(t, "MSB_PATH")

			inspection, err := InspectRuntime()
			if err != nil {
				t.Fatalf("InspectRuntime() error = %v", err)
			}
			if inspection.Relation != test.want {
				t.Errorf("Relation = %q, want %q", inspection.Relation, test.want)
			}
		})
	}
}

func TestInspectRuntimeMissingLibraryIsIncomplete(t *testing.T) {
	home := writeRuntimeFixture(t, "0.6.17", false)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	inspection, err := InspectRuntime()
	if err != nil {
		t.Fatalf("InspectRuntime() error = %v", err)
	}
	if inspection.Relation != RuntimeIncomplete {
		t.Fatalf("Relation = %q, want %q", inspection.Relation, RuntimeIncomplete)
	}
}

func TestInspectRuntimeUsesExplicitMSBPathAndLibrary(t *testing.T) {
	root := t.TempDir()
	msbPath := filepath.Join(root, "custom", "msb")
	libPath := filepath.Join(root, "custom", "libkrunfw.so.5.6.1")
	if err := os.MkdirAll(filepath.Dir(msbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(msbPath, []byte("#!/bin/sh\nprintf 'msb 0.6.17\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libPath, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MSB_PATH", msbPath)
	t.Setenv("MSB_LIBKRUNFW_PATH", libPath)

	inspection, err := InspectRuntime()
	if err != nil {
		t.Fatalf("InspectRuntime() error = %v", err)
	}
	if !inspection.External || inspection.Relation != RuntimeEqual {
		t.Fatalf("inspection = %+v, want external equal runtime", inspection)
	}
	if inspection.LibkrunfwPath != libPath {
		t.Errorf("LibkrunfwPath = %q, want %q", inspection.LibkrunfwPath, libPath)
	}
}

func TestInspectRuntimeRejectsEmptyMSBPath(t *testing.T) {
	t.Setenv("MSB_PATH", " ")
	if _, err := InspectRuntime(); err == nil {
		t.Fatal("InspectRuntime() error = nil, want empty MSB_PATH error")
	}
}

func TestInspectRuntimeUnknownVersion(t *testing.T) {
	home := writeRuntimeFixture(t, "not-a-version", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	inspection, err := InspectRuntime()
	if err != nil {
		t.Fatalf("InspectRuntime() error = %v", err)
	}
	if inspection.Relation != RuntimeUnknown {
		t.Fatalf("Relation = %q, want %q", inspection.Relation, RuntimeUnknown)
	}
}

func TestInspectRuntimeVersionCommandFailure(t *testing.T) {
	home := writeRuntimeFixture(t, "0.6.17", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")
	oldVersion := runMSBVersion
	runMSBVersion = func(string) (string, error) { return "", errors.New("exec failed") }
	t.Cleanup(func() { runMSBVersion = oldVersion })

	inspection, err := InspectRuntime()
	if err != nil {
		t.Fatalf("InspectRuntime() error = %v", err)
	}
	if inspection.Relation != RuntimeUnknown {
		t.Fatalf("Relation = %q, want %q", inspection.Relation, RuntimeUnknown)
	}
}

func TestInspectRuntimeInvalidRequiredVersion(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.17", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")
	inspection, err := inspectRuntime("not-a-semver")
	if err != nil {
		t.Fatalf("inspectRuntime() error = %v", err)
	}
	if inspection.Relation != RuntimeUnknown {
		t.Fatalf("Relation = %q, want %q", inspection.Relation, RuntimeUnknown)
	}
}

func TestInspectRuntimeMissingBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	inspection, err := InspectRuntime()
	if err != nil {
		t.Fatalf("InspectRuntime() error = %v", err)
	}
	if inspection.Relation != RuntimeMissing {
		t.Fatalf("Relation = %q, want %q", inspection.Relation, RuntimeMissing)
	}
}

func TestInspectRuntimeHomeResolutionError(t *testing.T) {
	oldHome := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("home unavailable") }
	t.Cleanup(func() { userHomeDir = oldHome })
	unsetEnv(t, "MSB_HOME")

	if _, err := InspectRuntime(); err == nil {
		t.Fatal("InspectRuntime() error = nil, want home resolution error")
	}
}

func TestInspectRuntimeStatError(t *testing.T) {
	oldHome := userHomeDir
	userHomeDir = func() (string, error) { return string([]byte{'\x00'}), nil }
	t.Cleanup(func() { userHomeDir = oldHome })
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	if _, err := InspectRuntime(); err == nil {
		t.Fatal("InspectRuntime() error = nil, want stat error")
	}
}

func TestDefaultRuntimeCommandSeams(t *testing.T) {
	root := t.TempDir()
	versionPath := filepath.Join(root, "msb-version")
	if err := os.WriteFile(versionPath, []byte("#!/bin/sh\nprintf 'msb 0.6.17\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := runMSBVersion(versionPath); err != nil || output != "msb 0.6.17" {
		t.Fatalf("runMSBVersion() = %q, %v", output, err)
	}
	if _, err := runMSBVersion(filepath.Join(root, "missing")); err == nil {
		t.Fatal("runMSBVersion() missing command error = nil")
	}

	downgradePath := filepath.Join(root, "msb-downgrade")
	if err := os.WriteFile(downgradePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runMSBDowngrade(context.Background(), downgradePath, "0.6.17", io.Discard, io.Discard); err != nil {
		t.Fatalf("runMSBDowngrade() error = %v", err)
	}
	failingDowngrade := filepath.Join(root, "msb-downgrade-fail")
	if err := os.WriteFile(failingDowngrade, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runMSBDowngrade(context.Background(), failingDowngrade, "0.6.17", io.Discard, io.Discard); err == nil {
		t.Fatal("runMSBDowngrade() failure error = nil")
	}
}

func TestPrepareRuntimePublicGateSkipsMockClient(t *testing.T) {
	oldReal := runtimeClientIsReal
	runtimeClientIsReal = func() bool { return false }
	t.Cleanup(func() { runtimeClientIsReal = oldReal })

	if _, err := PrepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("PrepareRuntime() error = %v", err)
	}
}

func TestPrepareRuntimeReturnsExistingPreparedState(t *testing.T) {
	resetRuntimeState(t)
	runtimeState.prepared = true
	t.Cleanup(func() { resetRuntimeState(t) })

	result, err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0")
	if err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
	if result.Restart {
		t.Fatal("prepared runtime unexpectedly requested restart")
	}
}

func TestPrepareRuntimeBraveModeSkipsInstaller(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.18", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldGet := sandboxmsb.Get
	sandboxmsb.Get = func() sandboxmsb.Client { return &sandboxmsb.MockMsbClient{} }
	t.Cleanup(func() { sandboxmsb.Get = oldGet })

	oldDowngrade := runMSBDowngrade
	runMSBDowngrade = func(context.Context, string, string, io.Writer, io.Writer) error {
		return errors.New("downgrade must not run")
	}
	t.Cleanup(func() { runMSBDowngrade = oldDowngrade })

	ui := &termio.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(_ string, choices []termio.Choice, defaultKey string) (string, error) {
		if defaultKey != "q" {
			t.Errorf("default key = %q, want q", defaultKey)
		}
		if !containsChoice(choices, "b") {
			t.Fatal("brave choice missing")
		}
		return "b", nil
	}

	result, err := prepareRuntime(context.Background(), ui, "0.2.0")
	if err != nil {
		t.Fatalf("PrepareRuntime() error = %v", err)
	}
	if result.Restart {
		t.Fatal("brave mode unexpectedly requested restart")
	}
}

func TestPrepareRuntimeNonInteractivePrintsIssueURL(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.18", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldGet := sandboxmsb.Get
	sandboxmsb.Get = func() sandboxmsb.Client { return &sandboxmsb.MockMsbClient{} }
	t.Cleanup(func() { sandboxmsb.Get = oldGet })

	ui := &termio.Mock{}
	_, err := prepareRuntime(context.Background(), ui, "0.2.0")
	if err == nil {
		t.Fatal("PrepareRuntime() error = nil, want mismatch error")
	}
	if !strings.Contains(strings.Join(errorMessages(ui), "\n"), runtimeIssueURL) {
		t.Errorf("issue URL missing from error output: %v", ui.ErrorCalls)
	}
}

func TestPrepareRuntimePublicGateUsesRealClient(t *testing.T) {
	resetRuntimeState(t)
	oldReal := runtimeClientIsReal
	runtimeClientIsReal = func() bool { return true }
	t.Cleanup(func() { runtimeClientIsReal = oldReal })
	home := writeRuntimeFixture(t, "0.6.17", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")
	oldValidate := validateRuntime
	validateRuntime = func(context.Context) error { return nil }
	t.Cleanup(func() { validateRuntime = oldValidate })

	if _, err := PrepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("PrepareRuntime() error = %v", err)
	}
}

func TestPrepareRuntimeMissingManagedRuntimeInstallsAndValidates(t *testing.T) {
	resetRuntimeState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldEnsure, oldValidate := ensureRuntime, validateRuntime
	ensureRuntime = func(context.Context) error {
		writeRuntimeFiles(t, filepath.Join(home, ".microsandbox"), "0.6.17")
		return nil
	}
	validateRuntime = func(context.Context) error { return nil }
	t.Cleanup(func() {
		ensureRuntime = oldEnsure
		validateRuntime = oldValidate
	})

	if _, err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
}

func TestDefaultEnsureRuntimeSeamFailsDeterministically(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MSB_HOME", home)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ensureRuntime(ctx); err == nil {
		t.Fatal("ensureRuntime() error = nil, want canceled setup error")
	}
}

func TestPrepareRuntimeMissingManagedRuntimeInstallFails(t *testing.T) {
	resetRuntimeState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")
	oldEnsure := ensureRuntime
	ensureRuntime = func(context.Context) error { return errors.New("install failed") }
	t.Cleanup(func() { ensureRuntime = oldEnsure })
	if _, err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want install error")
	}
}

func TestPrepareRuntimeMissingManagedRuntimeValidationFails(t *testing.T) {
	resetRuntimeState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldEnsure, oldValidate := ensureRuntime, validateRuntime
	ensureRuntime = func(context.Context) error {
		writeRuntimeFiles(t, filepath.Join(home, ".microsandbox"), "0.6.17")
		return nil
	}
	validateRuntime = func(context.Context) error { return errors.New("validation failed") }
	t.Cleanup(func() {
		ensureRuntime = oldEnsure
		validateRuntime = oldValidate
	})
	if _, err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want post-install validation error")
	}
}

func TestPrepareRuntimeInspectionFailure(t *testing.T) {
	resetRuntimeState(t)
	oldInspect := inspectRuntimeFunc
	inspectRuntimeFunc = func(string) (Inspection, error) { return Inspection{}, errors.New("inspect failed") }
	t.Cleanup(func() { inspectRuntimeFunc = oldInspect })
	if _, err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want inspection error")
	}
}

func TestPrepareRuntimeRejectsUnknownFutureRelation(t *testing.T) {
	resetRuntimeState(t)
	oldInspect := inspectRuntimeFunc
	inspectRuntimeFunc = func(string) (Inspection, error) {
		return Inspection{Relation: Relation("future")}, nil
	}
	t.Cleanup(func() { inspectRuntimeFunc = oldInspect })
	if _, err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want unknown relation error")
	}
}

func TestPrepareRuntimeEqualValidatesWithoutInstalling(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.17", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldEnsure, oldValidate := ensureRuntime, validateRuntime
	ensureRuntime = func(context.Context) error { return errors.New("installer must not run") }
	validated := false
	validateRuntime = func(context.Context) error {
		validated = true
		return nil
	}
	t.Cleanup(func() {
		ensureRuntime = oldEnsure
		validateRuntime = oldValidate
	})

	if _, err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
	if !validated {
		t.Fatal("expected equal runtime validation")
	}
}

func TestPrepareRuntimeUpgradeMSBAction(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.16", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldEnsure, oldValidate := ensureRuntime, validateRuntime
	ensureRuntime = func(context.Context) error {
		writeRuntimeFiles(t, filepath.Join(home, ".microsandbox"), "0.6.17")
		return nil
	}
	validateRuntime = func(context.Context) error { return nil }
	t.Cleanup(func() {
		ensureRuntime = oldEnsure
		validateRuntime = oldValidate
	})
	ui := selectRuntimeAction("u")
	if _, err := prepareRuntime(context.Background(), ui, "0.2.0"); err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
}

func TestPrepareRuntimeDowngradeMSBAction(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.18", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldDowngrade, oldValidate := runMSBDowngrade, validateRuntime
	runMSBDowngrade = func(_ context.Context, _, version string, _, _ io.Writer) error {
		writeRuntimeFiles(t, filepath.Join(home, ".microsandbox"), version)
		return nil
	}
	validateRuntime = func(context.Context) error { return nil }
	t.Cleanup(func() {
		runMSBDowngrade = oldDowngrade
		validateRuntime = oldValidate
	})
	if _, err := prepareRuntime(context.Background(), selectRuntimeAction("d"), "0.2.0"); err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
}

func TestPrepareRuntimeLauncherUpgradeRequestsRestart(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.18", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldLatest, oldUpdate := latestAgentsSandboxVersion, updateAgentsSandbox
	latestAgentsSandboxVersion = func(context.Context) (string, error) { return "0.3.0", nil }
	updated := false
	updateAgentsSandbox = func(context.Context, string) error {
		updated = true
		return nil
	}
	t.Cleanup(func() {
		latestAgentsSandboxVersion = oldLatest
		updateAgentsSandbox = oldUpdate
	})
	result, err := prepareRuntime(context.Background(), selectRuntimeAction("a"), "0.2.0")
	if err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
	if !result.Restart || !updated {
		t.Fatalf("result = %+v, updated = %v, want restart after update", result, updated)
	}
}

func TestPrepareRuntimePreparedStateShortCircuits(t *testing.T) {
	resetRuntimeState(t)
	runtimeState.prepared = true
	oldReal := runtimeClientIsReal
	runtimeClientIsReal = func() bool { return true }
	t.Cleanup(func() {
		runtimeClientIsReal = oldReal
		resetRuntimeState(t)
	})
	if _, err := PrepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("PrepareRuntime() error = %v", err)
	}
}

func TestPrepareRuntimeSelectionErrorsAndQuit(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.18", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	ui := &termio.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "q", nil }
	if _, err := prepareRuntime(context.Background(), ui, "0.2.0"); err == nil {
		t.Fatal("quit action returned nil error")
	}

	resetRuntimeState(t)
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "", errors.New("selection failed") }
	if _, err := prepareRuntime(context.Background(), ui, "0.2.0"); err == nil {
		t.Fatal("selection error returned nil error")
	}
}

func TestPrepareRuntimeUnknownAction(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.18", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")
	if _, err := prepareRuntime(context.Background(), selectRuntimeAction("x"), "0.2.0"); err == nil {
		t.Fatal("unknown action returned nil error")
	}
}

func TestPrepareRuntimeExternalMissingRuntimeOffersIssueAndQuit(t *testing.T) {
	resetRuntimeState(t)
	t.Setenv("MSB_PATH", filepath.Join(t.TempDir(), "missing-msb"))
	t.Setenv("MSB_LIBKRUNFW_PATH", filepath.Join(t.TempDir(), "missing-lib"))
	ui := selectRuntimeAction("q")
	if _, err := prepareRuntime(context.Background(), ui, "0.2.0"); err == nil {
		t.Fatal("external missing runtime quit returned nil error")
	}
}

func TestPrepareRuntimeLauncherUpgradeLookupAndInstallErrors(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.18", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldLatest, oldUpdate := latestAgentsSandboxVersion, updateAgentsSandbox
	latestAgentsSandboxVersion = func(context.Context) (string, error) { return "", errors.New("lookup failed") }
	updateAgentsSandbox = func(context.Context, string) error { return nil }
	t.Cleanup(func() {
		latestAgentsSandboxVersion = oldLatest
		updateAgentsSandbox = oldUpdate
	})
	if _, err := prepareRuntime(context.Background(), selectRuntimeAction("a"), "0.2.0"); err == nil {
		t.Fatal("launcher lookup failure returned nil error")
	}

	resetRuntimeState(t)
	latestAgentsSandboxVersion = func(context.Context) (string, error) { return "0.3.0", nil }
	updateAgentsSandbox = func(context.Context, string) error { return errors.New("update failed") }
	if _, err := prepareRuntime(context.Background(), selectRuntimeAction("a"), "0.2.0"); err == nil {
		t.Fatal("launcher update failure returned nil error")
	}
}

func TestPrepareRuntimeUpgradeAndValidationErrors(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.16", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldEnsure, oldValidate := ensureRuntime, validateRuntime
	ensureRuntime = func(context.Context) error { return errors.New("ensure failed") }
	validateRuntime = func(context.Context) error { return errors.New("validate failed") }
	t.Cleanup(func() {
		ensureRuntime = oldEnsure
		validateRuntime = oldValidate
	})
	if _, err := prepareRuntime(context.Background(), selectRuntimeAction("u"), "0.2.0"); err == nil {
		t.Fatal("upgrade failure returned nil error")
	}

	resetRuntimeState(t)
	ensureRuntime = func(context.Context) error {
		writeRuntimeFiles(t, filepath.Join(home, ".microsandbox"), "0.6.17")
		return nil
	}
	validateRuntime = func(context.Context) error { return errors.New("aligned validation failed") }
	if _, err := prepareRuntime(context.Background(), selectRuntimeAction("u"), "0.2.0"); err == nil {
		t.Fatal("aligned validation failure returned nil error")
	}
}

func TestPrepareRuntimeDowngradeErrors(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.18", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldDowngrade := runMSBDowngrade
	runMSBDowngrade = func(context.Context, string, string, io.Writer, io.Writer) error {
		return errors.New("downgrade failed")
	}
	t.Cleanup(func() { runMSBDowngrade = oldDowngrade })
	if _, err := prepareRuntime(context.Background(), selectRuntimeAction("d"), "0.2.0"); err == nil {
		t.Fatal("downgrade failure returned nil error")
	}
}

func TestPrepareRuntimeExternalDowngradeIsRejected(t *testing.T) {
	resetRuntimeState(t)
	root := t.TempDir()
	msbPath := filepath.Join(root, "msb")
	libPath := filepath.Join(root, "libkrunfw.so.5.6.1")
	if err := os.WriteFile(msbPath, []byte("#!/bin/sh\nprintf 'msb 0.6.18\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libPath, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MSB_PATH", msbPath)
	t.Setenv("MSB_LIBKRUNFW_PATH", libPath)
	if _, err := prepareRuntime(context.Background(), selectRuntimeAction("d"), "0.2.0"); err == nil {
		t.Fatal("external downgrade returned nil error")
	}
}

func TestPrepareRuntimeExternalEqualSkipsValidationInstaller(t *testing.T) {
	resetRuntimeState(t)
	root := t.TempDir()
	msbPath := filepath.Join(root, "msb")
	libPath := filepath.Join(root, "libkrunfw.so.5.6.1")
	if err := os.WriteFile(msbPath, []byte("#!/bin/sh\nprintf 'msb 0.6.17\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libPath, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MSB_PATH", msbPath)
	t.Setenv("MSB_LIBKRUNFW_PATH", libPath)
	oldValidate := validateRuntime
	validateRuntime = func(context.Context) error { return errors.New("external validation must not run") }
	t.Cleanup(func() { validateRuntime = oldValidate })
	if _, err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
}

func TestPrepareRuntimeValidationErrorOnEqualRuntime(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.17", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")
	oldValidate := validateRuntime
	validateRuntime = func(context.Context) error { return errors.New("validation failed") }
	t.Cleanup(func() { validateRuntime = oldValidate })
	if _, err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("equal runtime validation returned nil error")
	}
}

func TestValidatePreparedRuntimeRejectsMisalignedRuntime(t *testing.T) {
	home := writeRuntimeFixture(t, "0.6.18", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")
	if err := validatePreparedRuntime(context.Background()); err == nil {
		t.Fatal("validatePreparedRuntime() returned nil for mismatched runtime")
	}
}

func TestValidatePreparedRuntimeInspectionFails(t *testing.T) {
	oldInspect := inspectRuntimeFunc
	inspectRuntimeFunc = func(string) (Inspection, error) { return Inspection{}, errors.New("inspect failed") }
	t.Cleanup(func() { inspectRuntimeFunc = oldInspect })
	if err := validatePreparedRuntime(context.Background()); err == nil {
		t.Fatal("validatePreparedRuntime() error = nil, want inspection error")
	}
}

func TestSanitizeRuntimePathEmpty(t *testing.T) {
	if got := sanitizeRuntimePath("", ""); got != "<unset>" {
		t.Errorf("sanitizeRuntimePath() = %q, want <unset>", got)
	}
}

func TestRuntimeLibraryPathUsesExplicitMSBPathCandidates(t *testing.T) {
	root := t.TempDir()
	msbPath := filepath.Join(root, "bin", "msb")
	libPath := filepath.Join(root, "lib", "libkrunfw.so.5.6.1")
	if err := os.MkdirAll(filepath.Dir(msbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(libPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libPath, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MSB_PATH", msbPath)
	unsetEnv(t, "MSB_LIBKRUNFW_PATH")
	if got := runtimeLibraryPath(filepath.Join(root, "other"), msbPath); got != libPath {
		t.Errorf("runtimeLibraryPath() = %q, want %q", got, libPath)
	}
}

func TestPrepareRuntimeBraveAndIssueActions(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, "0.6.18", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	if _, err := prepareRuntime(context.Background(), selectRuntimeAction("i"), "0.2.0"); err == nil {
		t.Fatal("issue action returned nil error")
	}
	resetRuntimeState(t)
	if _, err := prepareRuntime(context.Background(), selectRuntimeAction("unknown"), "0.2.0"); err == nil {
		t.Fatal("unknown action returned nil error")
	}
}

func TestIssueReportRedactsAbsolutePaths(t *testing.T) {
	resetRuntimeState(t)
	inspection := Inspection{
		AgentsSandboxVersion: "0.2.0",
		RequiredVersion:      "0.6.17",
		InstalledVersion:     "0.6.18",
		MSBHome:              "/home/alice/private-microsandbox",
		MSBPath:              "/home/alice/private-microsandbox/bin/msb",
		Relation:             RuntimeNewer,
	}
	ui := &termio.Mock{}
	showIssueReport(ui, inspection)
	report := strings.Join(errorMessages(ui), "\n")
	if strings.Contains(report, "/home/alice") {
		t.Fatalf("report exposed an absolute home path: %s", report)
	}
	if !strings.Contains(report, "$MSB_HOME/bin/msb") {
		t.Fatalf("report did not preserve a useful relative path: %s", report)
	}
}

func TestSanitizeRuntimePathUsesHomeAndBasenameFallbacks(t *testing.T) {
	oldHome := userHomeDir
	userHomeDir = func() (string, error) { return "/home/user", nil }
	t.Cleanup(func() { userHomeDir = oldHome })
	if got := sanitizeRuntimePath("/home/user/.microsandbox/bin/msb", "/other"); got != "$HOME/.microsandbox/bin/msb" {
		t.Errorf("home path = %q", got)
	}
	if got := sanitizeRuntimePath("/opt/custom/msb", "/other"); got != "msb" {
		t.Errorf("external path = %q, want basename", got)
	}
	userHomeDir = func() (string, error) { return "", errors.New("home unavailable") }
	if got := sanitizeRuntimePath("/opt/custom/msb", "/other"); got != "msb" {
		t.Errorf("fallback path = %q, want basename", got)
	}
}

func TestRuntimePlatformFilenameHelpers(t *testing.T) {
	for _, test := range []struct {
		goos      string
		msb       string
		libkrunfw string
	}{
		{goos: "linux", msb: "msb", libkrunfw: "libkrunfw.so.5.6.1"},
		{goos: "darwin", msb: "msb", libkrunfw: "libkrunfw.5.dylib"},
		{goos: "windows", msb: "msb.exe", libkrunfw: "libkrunfw.dll"},
	} {
		t.Run(test.goos, func(t *testing.T) {
			if got := msbFilenameFor(test.goos); got != test.msb {
				t.Errorf("msbFilenameFor() = %q, want %q", got, test.msb)
			}
			if got := libkrunfwFilenameFor(test.goos); got != test.libkrunfw {
				t.Errorf("libkrunfwFilenameFor() = %q, want %q", got, test.libkrunfw)
			}
		})
	}
}

func TestRuntimeVersionParsingAndComparisonErrors(t *testing.T) {
	for _, output := range []string{"", "msb", "msb nope", "msb "} {
		if _, err := parseMSBVersion(output); err == nil {
			t.Errorf("parseMSBVersion(%q) error = nil", output)
		}
	}
	if _, err := compareRuntimeVersions("nope", "0.6.17"); err == nil {
		t.Fatal("compareRuntimeVersions() accepted invalid required version")
	}
	if _, err := compareRuntimeVersions("0.6.17", "nope"); err == nil {
		t.Fatal("compareRuntimeVersions() accepted invalid installed version")
	}
}

func TestMismatchChoicesOfferLauncherUpgradeWhenNewerReleaseExists(t *testing.T) {
	oldLatest := latestAgentsSandboxVersion
	latestAgentsSandboxVersion = func(context.Context) (string, error) { return "0.3.0", nil }
	t.Cleanup(func() { latestAgentsSandboxVersion = oldLatest })

	choices := mismatchChoices(context.Background(), "0.2.0", Inspection{
		RequiredVersion:  "0.6.17",
		InstalledVersion: "0.6.18",
		Relation:         RuntimeNewer,
	})
	if !containsChoice(choices, "a") {
		t.Fatal("expected launcher upgrade choice")
	}
}

func TestMismatchChoicesUseIssueRequestWhenLauncherIsCurrent(t *testing.T) {
	oldLatest := latestAgentsSandboxVersion
	latestAgentsSandboxVersion = func(context.Context) (string, error) { return "0.2.0", nil }
	t.Cleanup(func() { latestAgentsSandboxVersion = oldLatest })

	choices := mismatchChoices(context.Background(), "0.2.0", Inspection{
		RequiredVersion:  "0.6.17",
		InstalledVersion: "0.6.18",
		Relation:         RuntimeNewer,
	})
	if containsChoice(choices, "a") {
		t.Fatal("did not expect launcher upgrade choice")
	}
	if !containsChoice(choices, "i") {
		t.Fatal("expected issue choice")
	}
}

func writeRuntimeFixture(t *testing.T, version string, includeLibrary bool) string {
	t.Helper()
	home := t.TempDir()
	binDir := filepath.Join(home, ".microsandbox", "bin")
	libDir := filepath.Join(home, ".microsandbox", "lib")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	msbPath := filepath.Join(binDir, "msb")
	if err := os.WriteFile(msbPath, []byte("#!/bin/sh\nprintf 'msb %s\\n' '"+version+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if includeLibrary {
		if err := os.WriteFile(filepath.Join(libDir, "libkrunfw.so.5.6.1"), []byte("fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func writeRuntimeFiles(t *testing.T, home, version string) {
	t.Helper()
	binDir := filepath.Join(home, "bin")
	libDir := filepath.Join(home, "lib")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(binDir, "msb"),
		[]byte("#!/bin/sh\nprintf 'msb "+version+"\\n'\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "libkrunfw.so.5.6.1"), []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func selectRuntimeAction(key string) *termio.Mock {
	ui := &termio.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return key, nil
	}
	return ui
}

func containsChoice(choices []termio.Choice, key string) bool {
	for _, choice := range choices {
		if choice.Key == key {
			return true
		}
	}
	return false
}

func errorMessages(ui *termio.Mock) []string {
	messages := make([]string, 0, len(ui.ErrorCalls))
	for _, call := range ui.ErrorCalls {
		messages = append(messages, call.Msg)
	}
	return messages
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	value, present := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			t.Setenv(name, value)
			return
		}
		_ = os.Unsetenv(name)
	})
}

func resetRuntimeState(t *testing.T) {
	t.Helper()
	runtimeState.mutex.Lock()
	runtimeState.prepared = false
	runtimeState.mutex.Unlock()
}
