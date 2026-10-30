package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestInspectRuntimeEqual(t *testing.T) {
	home := writeRuntimeFixture(t, currentMSBVersion(), true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	inspection, err := inspectRuntime(msbSdk.SDKVersion())
	if err != nil {
		t.Fatalf("inspectRuntime() error = %v", err)
	}
	if inspection.Relation != RuntimeEqual {
		t.Fatalf("Relation = %q, want %q", inspection.Relation, RuntimeEqual)
	}
	if inspection.InstalledVersion != currentMSBVersion() {
		t.Errorf("InstalledVersion = %q, want %s", inspection.InstalledVersion, currentMSBVersion())
	}
}

func TestRuntimeFilePresentClassifiesPaths(t *testing.T) {
	if present, err := runtimeFilePresent(""); err != nil || present {
		t.Fatalf("empty path = %v, %v; want false, nil", present, err)
	}
	root := t.TempDir()
	filePath := filepath.Join(root, "file")
	if err := os.WriteFile(filePath, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, err := runtimeFilePresent(filePath); err != nil || !present {
		t.Fatalf("regular file = %v, %v; want true, nil", present, err)
	}
	if present, err := runtimeFilePresent(root); err != nil || present {
		t.Fatalf("directory = %v, %v; want false, nil", present, err)
	}
	if present, err := runtimeFilePresent(filepath.Join(root, "missing")); err != nil || present {
		t.Fatalf("missing path = %v, %v; want false, nil", present, err)
	}
	if present, err := runtimeFilePresent(string([]byte{'\x00'})); err == nil || present {
		t.Fatalf("invalid path = %v, %v; want false, error", present, err)
	}
}

func TestInspectRuntimeNewerAndOlder(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    Relation
	}{
		{name: "newer", version: newerMSBVersion(t), want: RuntimeNewer},
		{name: "older", version: olderMSBVersion(t), want: RuntimeOlder},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := writeRuntimeFixture(t, test.version, true)
			t.Setenv("HOME", home)
			unsetEnv(t, "MSB_HOME")
			unsetEnv(t, "MSB_PATH")

			inspection, err := inspectRuntime(msbSdk.SDKVersion())
			if err != nil {
				t.Fatalf("inspectRuntime() error = %v", err)
			}
			if inspection.Relation != test.want {
				t.Errorf("Relation = %q, want %q", inspection.Relation, test.want)
			}
		})
	}
}

func TestInspectRuntimeMissingLibraryIsIncomplete(t *testing.T) {
	home := writeRuntimeFixture(t, currentMSBVersion(), false)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	inspection, err := inspectRuntime(msbSdk.SDKVersion())
	if err != nil {
		t.Fatalf("inspectRuntime() error = %v", err)
	}
	if inspection.Relation != RuntimeIncomplete {
		t.Fatalf("Relation = %q, want %q", inspection.Relation, RuntimeIncomplete)
	}
}

func TestInspectRuntimeUnknownVersion(t *testing.T) {
	home := writeRuntimeFixture(t, "not-a-version", true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	inspection, err := inspectRuntime(msbSdk.SDKVersion())
	if err != nil {
		t.Fatalf("inspectRuntime() error = %v", err)
	}
	if inspection.Relation != RuntimeUnknown {
		t.Fatalf("Relation = %q, want %q", inspection.Relation, RuntimeUnknown)
	}
}

func TestInspectRuntimeVersionCommandFailure(t *testing.T) {
	home := writeRuntimeFixture(t, currentMSBVersion(), true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")
	oldVersion := runMSBVersion
	runMSBVersion = func(string) (string, error) { return "", errors.New("exec failed") }
	t.Cleanup(func() { runMSBVersion = oldVersion })

	inspection, err := inspectRuntime(msbSdk.SDKVersion())
	if err != nil {
		t.Fatalf("inspectRuntime() error = %v", err)
	}
	if inspection.Relation != RuntimeUnknown {
		t.Fatalf("Relation = %q, want %q", inspection.Relation, RuntimeUnknown)
	}
}

func TestInspectRuntimeInvalidRequiredVersion(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, currentMSBVersion(), true)
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
	mockRuntimeHome(t)

	inspection, err := inspectRuntime(msbSdk.SDKVersion())
	if err != nil {
		t.Fatalf("inspectRuntime() error = %v", err)
	}
	if inspection.Relation != RuntimeMissing {
		t.Fatalf("Relation = %q, want %q", inspection.Relation, RuntimeMissing)
	}
}

// TestInspectRuntimeMsbStatError covers an unexpected stat failure on the msb
// binary path (a symlink loop yields ELOOP, not "not exist").
func TestInspectRuntimeMsbStatError(t *testing.T) {
	home := mockRuntimeHome(t)
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("msb", filepath.Join(binDir, "msb")); err != nil {
		t.Fatal(err)
	}

	if _, err := inspectRuntime(msbSdk.SDKVersion()); err == nil {
		t.Fatal("inspectRuntime() error = nil, want msb stat error")
	}
}

// TestInspectRuntimeLibraryStatError covers an unexpected stat failure on the
// libkrunfw path after a valid msb is present.
func TestInspectRuntimeLibraryStatError(t *testing.T) {
	home := mockRuntimeHome(t)
	writeRuntimeFiles(t, home, currentMSBVersion())

	lib := filepath.Join(home, "lib", "libkrunfw.so.5.6.1")
	if err := os.Remove(lib); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libkrunfw.so.5.6.1", lib); err != nil {
		t.Fatal(err)
	}

	if _, err := inspectRuntime(msbSdk.SDKVersion()); err == nil {
		t.Fatal("inspectRuntime() error = nil, want libkrunfw stat error")
	}
}

func TestPrepareRuntimeApplyHomeError(t *testing.T) {
	resetRuntimeState(t)
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("setenv boom")
	orig := setenv
	setenv = func(string, string) error { return wantErr }
	t.Cleanup(func() { setenv = orig })

	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want ApplyHome error")
	}
}

func TestDefaultRuntimeCommandSeams(t *testing.T) {
	root := t.TempDir()
	versionPath := filepath.Join(root, "msb-version")
	if err := os.WriteFile(versionPath, msbVersionScript(currentMSBVersion()), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := runMSBVersion(versionPath); err != nil || output != "msb "+currentMSBVersion() {
		t.Fatalf("runMSBVersion() = %q, %v", output, err)
	}
	if _, err := runMSBVersion(filepath.Join(root, "missing")); err == nil {
		t.Fatal("runMSBVersion() missing command error = nil")
	}
}

func TestDefaultEnsureRuntimeSeamFailsDeterministically(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	home := t.TempDir()
	t.Setenv("MSB_HOME", home)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ensureRuntime(ctx); err == nil {
		t.Fatal("ensureRuntime() error = nil, want canceled setup error")
	}
}

func TestDefaultInstallRuntimeSeamFailsDeterministically(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	home := t.TempDir()
	t.Setenv("MSB_HOME", home)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := installRuntime(ctx); err == nil {
		t.Fatal("installRuntime() error = nil, want canceled setup error")
	}
}

// TestRuntimeSetupSeamsPassPrivateHome asserts both setup seams hand the SDK the
// private home explicitly, so resolution cannot depend on MSB_HOME having been
// applied first.
func TestRuntimeSetupSeamsPassPrivateHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantHome := PrivateHome()

	var ensureConfig, installConfig msbSdk.RuntimeConfig
	origEnsure, origInstall := msbEnsureRuntime, msbInstallRuntime
	msbEnsureRuntime = func(
		_ context.Context, config msbSdk.RuntimeConfig, _ msbSdk.InstallOptions,
	) (msbSdk.ResolvedRuntime, error) {
		ensureConfig = config
		return msbSdk.ResolvedRuntime{}, nil
	}
	msbInstallRuntime = func(
		_ context.Context, config msbSdk.RuntimeConfig, opts msbSdk.InstallOptions,
	) (msbSdk.ResolvedRuntime, error) {
		installConfig = config
		if !opts.Force {
			t.Error("install seam did not force-install")
		}
		return msbSdk.ResolvedRuntime{}, nil
	}
	t.Cleanup(func() { msbEnsureRuntime = origEnsure; msbInstallRuntime = origInstall })

	if err := ensureRuntime(context.Background()); err != nil {
		t.Fatalf("ensureRuntime() error = %v", err)
	}
	if err := installRuntime(context.Background()); err != nil {
		t.Fatalf("installRuntime() error = %v", err)
	}
	if ensureConfig.Home != wantHome {
		t.Errorf("ensure RuntimeConfig.Home = %q, want %q", ensureConfig.Home, wantHome)
	}
	if installConfig.Home != wantHome {
		t.Errorf("install RuntimeConfig.Home = %q, want %q", installConfig.Home, wantHome)
	}
}

func TestAcquireRuntimeLockBlocksSecondHolder(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	release, err := acquireRuntimeLock()
	if err != nil {
		t.Fatalf("acquireRuntimeLock: %v", err)
	}
	done := make(chan struct{})
	go func() {
		r, _ := acquireRuntimeLock()
		if r != nil {
			r()
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("second acquire returned while the first held the lock")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire did not proceed after release")
	}
}

// TestAcquireRuntimeLockError covers the error wrapping: a directory occupying
// the lock file path makes the acquisition fail.
func TestAcquireRuntimeLockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	blockRuntimeLock(t)
	_, err := acquireRuntimeLock()
	if err == nil || !strings.Contains(err.Error(), "acquire runtime lock") {
		t.Fatalf("acquireRuntimeLock() error = %v, want a wrapped lock failure", err)
	}
}

// blockRuntimeLock makes the runtime lock unacquirable by occupying its file
// path with a directory.
func blockRuntimeLock(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(runtimeLockDir(), runtimeLockName), 0o700); err != nil {
		t.Fatalf("mkdir at lock path: %v", err)
	}
}

func TestPrepareRuntimePublicGateSkipsMockClient(t *testing.T) {
	oldReal := runtimeClientIsReal
	runtimeClientIsReal = func() bool { return false }
	t.Cleanup(func() { runtimeClientIsReal = oldReal })

	if err := PrepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("PrepareRuntime() error = %v", err)
	}
}

func TestPrepareRuntimeReturnsExistingPreparedState(t *testing.T) {
	resetRuntimeState(t)
	runtimeState.prepared = true
	t.Cleanup(func() { resetRuntimeState(t) })

	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
}

func TestPrepareRuntimePublicGateUsesRealClient(t *testing.T) {
	resetRuntimeState(t)
	oldReal := runtimeClientIsReal
	runtimeClientIsReal = func() bool { return true }
	t.Cleanup(func() { runtimeClientIsReal = oldReal })
	home := writeRuntimeFixture(t, currentMSBVersion(), true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")
	oldValidate := validateRuntime
	validateRuntime = func(context.Context) error { return nil }
	t.Cleanup(func() { validateRuntime = oldValidate })

	if err := PrepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("PrepareRuntime() error = %v", err)
	}
}

func TestApplyPreviewHomeSkipsMockClient(t *testing.T) {
	oldReal := runtimeClientIsReal
	runtimeClientIsReal = func() bool { return false }
	t.Cleanup(func() { runtimeClientIsReal = oldReal })

	unsetEnv(t, "MSB_HOME")

	if err := ApplyPreviewHome(); err != nil {
		t.Fatalf("ApplyPreviewHome() error = %v", err)
	}
	if got := os.Getenv("MSB_HOME"); got != "" {
		t.Errorf("MSB_HOME = %q; want unset for a mock client", got)
	}
}

func TestApplyPreviewHomePointsAtPrivateHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	unsetEnv(t, "MSB_HOME")
	oldReal := runtimeClientIsReal
	runtimeClientIsReal = func() bool { return true }
	t.Cleanup(func() { runtimeClientIsReal = oldReal })

	if err := ApplyPreviewHome(); err != nil {
		t.Fatalf("ApplyPreviewHome() error = %v", err)
	}
	if got := os.Getenv("MSB_HOME"); got != PrivateHome() {
		t.Errorf("MSB_HOME = %q; want the private home %q", got, PrivateHome())
	}
}

func TestApplyPreviewHomeWrapsApplyHomeError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	oldReal := runtimeClientIsReal
	runtimeClientIsReal = func() bool { return true }
	t.Cleanup(func() { runtimeClientIsReal = oldReal })
	orig := setenv
	setenv = func(string, string) error { return errors.New("setenv boom") }
	t.Cleanup(func() { setenv = orig })

	err := ApplyPreviewHome()
	if err == nil || !strings.Contains(err.Error(), "apply microsandbox home") {
		t.Fatalf("ApplyPreviewHome() error = %v, want a wrapped ApplyHome failure", err)
	}
}

func TestPrepareRuntimeMissingInstallsAndValidates(t *testing.T) {
	resetRuntimeState(t)
	home := mockRuntimeHome(t)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldEnsure, oldValidate := ensureRuntime, validateRuntime
	ensureCalls, validateCalls := 0, 0
	ensureRuntime = func(context.Context) error {
		ensureCalls++
		writeRuntimeFiles(t, home, currentMSBVersion())
		return nil
	}
	validateRuntime = func(context.Context) error {
		validateCalls++
		return nil
	}
	t.Cleanup(func() {
		ensureRuntime = oldEnsure
		validateRuntime = oldValidate
	})

	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
	if ensureCalls != 1 || validateCalls != 1 {
		t.Fatalf("ensureRuntime calls = %d, validateRuntime calls = %d; want 1 each", ensureCalls, validateCalls)
	}
}

func TestPrepareRuntimeMissingManagedRuntimeInstallFails(t *testing.T) {
	resetRuntimeState(t)
	// Resolve the private home to an isolated, empty directory. Setting HOME
	// alone is not enough: configpaths honors XDG_DATA_HOME, so on a host where
	// it points at a real install the runtime would be found and the install
	// path never reached.
	configpaths.WithMockConfigPaths(t)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")
	oldEnsure := ensureRuntime
	ensureRuntime = func(context.Context) error { return errors.New("install failed") }
	t.Cleanup(func() { ensureRuntime = oldEnsure })
	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want install error")
	}
}

func TestPrepareRuntimeMissingManagedRuntimeValidationFails(t *testing.T) {
	resetRuntimeState(t)
	home := mockRuntimeHome(t)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldEnsure, oldValidate := ensureRuntime, validateRuntime
	ensureRuntime = func(context.Context) error {
		writeRuntimeFiles(t, home, currentMSBVersion())
		return nil
	}
	validateRuntime = func(context.Context) error { return errors.New("validation failed") }
	t.Cleanup(func() {
		ensureRuntime = oldEnsure
		validateRuntime = oldValidate
	})
	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want post-install validation error")
	}
}

func TestPrepareRuntimeInspectionFailure(t *testing.T) {
	resetRuntimeState(t)
	configpaths.WithMockConfigPaths(t)
	oldInspect := inspectRuntimeFunc
	inspectRuntimeFunc = func(string) (Inspection, error) { return Inspection{}, errors.New("inspect failed") }
	t.Cleanup(func() { inspectRuntimeFunc = oldInspect })
	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want inspection error")
	}
}

func TestPrepareRuntimeRejectsUnknownFutureRelation(t *testing.T) {
	resetRuntimeState(t)
	configpaths.WithMockConfigPaths(t)
	oldInspect := inspectRuntimeFunc
	inspectRuntimeFunc = func(string) (Inspection, error) {
		return Inspection{Relation: Relation("future")}, nil
	}
	t.Cleanup(func() { inspectRuntimeFunc = oldInspect })
	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want unknown relation error")
	}
}

func TestPrepareRuntimeMismatchRepairsByForceInstalling(t *testing.T) {
	for _, test := range []struct {
		name    string
		version string
	}{
		{name: "older", version: olderMSBVersion(t)},
		{name: "newer", version: newerMSBVersion(t)},
		{name: "unknown", version: "not-a-version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetRuntimeState(t)
			home := writeRuntimeFixture(t, test.version, true)
			t.Setenv("HOME", home)
			unsetEnv(t, "MSB_HOME")
			unsetEnv(t, "MSB_PATH")

			oldInstall, oldValidate := installRuntime, validateRuntime
			installCalls, validateCalls := 0, 0
			installRuntime = func(context.Context) error {
				installCalls++
				writeRuntimeFiles(t, home, currentMSBVersion())
				return nil
			}
			validateRuntime = func(context.Context) error {
				validateCalls++
				return nil
			}
			t.Cleanup(func() {
				installRuntime = oldInstall
				validateRuntime = oldValidate
			})

			if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
				t.Fatalf("prepareRuntime() error = %v", err)
			}
			if installCalls != 1 || validateCalls != 1 {
				t.Fatalf(
					"installRuntime calls = %d, validateRuntime calls = %d; want 1 each",
					installCalls,
					validateCalls,
				)
			}
		})
	}
}

func TestPrepareRuntimeIncompleteRepairsAfterRemovingStrayFile(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, currentMSBVersion(), false)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldInstall, oldValidate := installRuntime, validateRuntime
	installRuntime = func(context.Context) error {
		if _, err := os.Stat(filepath.Join(home, "bin", "msb")); !os.IsNotExist(err) {
			t.Fatalf("stray runtime file not removed before install: err = %v", err)
		}
		writeRuntimeFiles(t, home, currentMSBVersion())
		return nil
	}
	validateRuntime = func(context.Context) error { return nil }
	t.Cleanup(func() {
		installRuntime = oldInstall
		validateRuntime = oldValidate
	})

	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
}

func TestPrepareRuntimeRepairLockFails(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, olderMSBVersion(t), true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	blockRuntimeLock(t)

	err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0")
	if err == nil || !strings.Contains(err.Error(), "acquire runtime lock") {
		t.Fatalf("prepareRuntime() error = %v, want a runtime lock failure", err)
	}
}

func TestPrepareRuntimeRepairReinspectFails(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, olderMSBVersion(t), true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldInspect := inspectRuntimeFunc
	inspectionCalls := 0
	inspectRuntimeFunc = func(string) (Inspection, error) {
		inspectionCalls++
		if inspectionCalls == 1 {
			return Inspection{Relation: RuntimeOlder}, nil
		}
		return Inspection{}, errors.New("inspect failed")
	}
	t.Cleanup(func() { inspectRuntimeFunc = oldInspect })

	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want re-inspection error")
	}
}

func TestPrepareRuntimeIncompleteRemovalFails(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, currentMSBVersion(), true)
	binMSB := filepath.Join(home, "bin", "msb")
	if err := os.Remove(binMSB); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binMSB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binMSB, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldInstall := installRuntime
	installRuntime = func(context.Context) error {
		t.Fatal("installRuntime must not run when the incomplete runtime cannot be removed")
		return nil
	}
	t.Cleanup(func() { installRuntime = oldInstall })

	err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0")
	if err == nil {
		t.Fatal("prepareRuntime() error = nil, want incomplete runtime removal error")
	}
	if !strings.Contains(err.Error(), "remove incomplete msb runtime") {
		t.Fatalf("error = %v, want incomplete runtime removal error", err)
	}
}

func TestPrepareRuntimeRepairSeesConcurrentRepair(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, olderMSBVersion(t), true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldInspect, oldInstall, oldValidate := inspectRuntimeFunc, installRuntime, validateRuntime
	inspectionCalls := 0
	inspectRuntimeFunc = func(string) (Inspection, error) {
		inspectionCalls++
		if inspectionCalls == 1 {
			return Inspection{Relation: RuntimeOlder}, nil
		}
		return Inspection{Relation: RuntimeEqual}, nil
	}
	installRuntime = func(context.Context) error {
		t.Fatal("installRuntime must not run when the runtime was repaired concurrently")
		return nil
	}
	validated := false
	validateRuntime = func(context.Context) error {
		validated = true
		return nil
	}
	t.Cleanup(func() {
		inspectRuntimeFunc = oldInspect
		installRuntime = oldInstall
		validateRuntime = oldValidate
	})

	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
	if !validated {
		t.Fatal("expected validation of the concurrently repaired runtime")
	}
}

func TestPrepareRuntimeRepairValidationFails(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, olderMSBVersion(t), true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldInstall, oldValidate := installRuntime, validateRuntime
	installRuntime = func(context.Context) error {
		writeRuntimeFiles(t, home, currentMSBVersion())
		return nil
	}
	validateRuntime = func(context.Context) error { return errors.New("validation failed") }
	t.Cleanup(func() {
		installRuntime = oldInstall
		validateRuntime = oldValidate
	})

	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want post-repair validation error")
	}
}

func TestPrepareRuntimeRepairInstallFails(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, olderMSBVersion(t), true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")

	oldInstall := installRuntime
	installRuntime = func(context.Context) error { return errors.New("install failed") }
	t.Cleanup(func() { installRuntime = oldInstall })

	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("prepareRuntime() error = nil, want repair install error")
	}
}

func TestPrepareRuntimeEqualValidates(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, currentMSBVersion(), true)
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

	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("prepareRuntime() error = %v", err)
	}
	if !validated {
		t.Fatal("expected equal runtime validation")
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
	if err := PrepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err != nil {
		t.Fatalf("PrepareRuntime() error = %v", err)
	}
}

func TestPrepareRuntimeValidationErrorOnEqualRuntime(t *testing.T) {
	resetRuntimeState(t)
	home := writeRuntimeFixture(t, currentMSBVersion(), true)
	t.Setenv("HOME", home)
	unsetEnv(t, "MSB_HOME")
	unsetEnv(t, "MSB_PATH")
	oldValidate := validateRuntime
	validateRuntime = func(context.Context) error { return errors.New("validation failed") }
	t.Cleanup(func() { validateRuntime = oldValidate })
	if err := prepareRuntime(context.Background(), &termio.Mock{}, "0.2.0"); err == nil {
		t.Fatal("equal runtime validation returned nil error")
	}
}

func TestValidatePreparedRuntimeRejectsMisalignedRuntime(t *testing.T) {
	home := writeRuntimeFixture(t, newerMSBVersion(t), true)
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

func TestSanitizeRuntimePathUsesHomeAndBasenameFallbacks(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	home, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	insideHome := filepath.Join(home, ".microsandbox", "bin", "msb")
	if got := sanitizeRuntimePath(insideHome, "/other"); got != "$HOME/.microsandbox/bin/msb" {
		t.Errorf("home path = %q", got)
	}
	if got := sanitizeRuntimePath("/opt/custom/msb", "/other"); got != "msb" {
		t.Errorf("external path = %q, want basename", got)
	}
	configpaths.WithMockConfigPathsHomeError(t, errors.New("home unavailable"))
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
	if _, err := compareRuntimeVersions("nope", currentMSBVersion()); err == nil {
		t.Fatal("compareRuntimeVersions() accepted invalid required version")
	}
	if _, err := compareRuntimeVersions(currentMSBVersion(), "nope"); err == nil {
		t.Fatal("compareRuntimeVersions() accepted invalid installed version")
	}
}

func currentMSBVersion() string {
	return msbSdk.SDKVersion()
}

func newerMSBVersion(t *testing.T) string {
	t.Helper()
	version, err := semver.NewVersion(currentMSBVersion())
	if err != nil {
		t.Fatalf("parse SDK version: %v", err)
	}
	return version.IncPatch().String()
}

func olderMSBVersion(t *testing.T) string {
	t.Helper()
	version, err := semver.NewVersion(currentMSBVersion())
	if err != nil {
		t.Fatalf("parse SDK version: %v", err)
	}
	if version.Patch() > 0 {
		return semver.New(version.Major(), version.Minor(), version.Patch()-1, "", "").String()
	}
	if version.Minor() > 0 {
		return semver.New(version.Major(), version.Minor()-1, 0, "", "").String()
	}
	if version.Major() > 0 {
		return semver.New(version.Major()-1, 0, 0, "", "").String()
	}
	t.Fatalf("cannot derive older version from %s", currentMSBVersion())
	return ""
}

func msbVersionScript(version string) []byte {
	return []byte("#!/bin/sh\nprintf 'msb " + version + "\\n'\n")
}

func mockRuntimeHome(t *testing.T) string {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	return filepath.Join(configpaths.Get().UserDataDir(), "msb")
}

func writeRuntimeFixture(t *testing.T, version string, includeLibrary bool) string {
	t.Helper()
	home := mockRuntimeHome(t)
	writeRuntimeFiles(t, home, version)
	if !includeLibrary {
		if err := os.Remove(filepath.Join(home, "lib", "libkrunfw.so.5.6.1")); err != nil {
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
		msbVersionScript(version),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "libkrunfw.so.5.6.1"), []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
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
