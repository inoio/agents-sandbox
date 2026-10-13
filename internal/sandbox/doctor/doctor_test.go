package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/termio"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestShellRcFile(t *testing.T) {
	home := "/home/test"
	tests := []struct {
		name  string
		shell string
		want  string
	}{
		{"bash defaults to platform rc", "/bin/bash", filepath.Join(home, shellRcDefault)},
		{"zsh", "/bin/zsh", filepath.Join(home, ".zshrc")},
		{"fish", "/usr/bin/fish", filepath.Join(home, ".config", "fish", "config.fish")},
		{"empty shell falls back to default rc", "", filepath.Join(home, shellRcDefault)},
		{"unknown shell falls back to default rc", "/bin/sh", filepath.Join(home, shellRcDefault)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellRcFile(home, tc.shell); got != tc.want {
				t.Errorf("shellRcFile(%q, %q) = %q, want %q", home, tc.shell, got, tc.want)
			}
		})
	}
}

func TestCheckMsbEnsureInstalledErrorSurfacesErrorWithoutInstallHint(t *testing.T) {
	MockedEnsureInstalled(t, true)

	_, err := checkMsb(context.Background())
	if err == nil {
		t.Fatal("expected checkMsb to return an error when ensureInstalled fails")
	}
	if !strings.Contains(err.Error(), "msb runtime setup failed") {
		t.Errorf("expected error to mention 'msb runtime setup failed', got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "mock") {
		t.Errorf("expected error to contain the underlying error, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "Install microsandbox") ||
		strings.Contains(err.Error(), "github.com/microsandbox") {
		t.Errorf("expected no 'install msb' instruction, got %q", err.Error())
	}
}

func TestCheckMsbOnPathReturnsTrueSilently(t *testing.T) {
	MockedEnsureInstalled(t, false)

	dir := t.TempDir()
	testutil.WriteFile(t, dir, "msb", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(dir, "msb"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	warnings, err := checkMsb(context.Background())
	if err != nil {
		t.Fatal("expected checkMsb to return nil error when msb is on PATH")
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when msb is on PATH, got %q", warnings)
	}
}

func TestCheckMsbNotOnPathMessagesAndReturnsTrue(t *testing.T) {
	MockedEnsureInstalled(t, false)

	home := t.TempDir()
	binDir := filepath.Join(home, ".microsandbox", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(binDir, "msb")
	testutil.WritePath(t, binPath, "#!/bin/sh\n")
	if err := os.Chmod(binPath, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", "/nonexistent")

	warnings, err := checkMsb(context.Background())
	if err != nil {
		t.Fatal("expected checkMsb to return nil error when msb is installed but not on PATH")
	}
	outStr := strings.Join(warnings, " ")
	if !strings.Contains(outStr, "not in PATH") {
		t.Errorf("expected a warning that msb is not on PATH, got %q", outStr)
	}
	if !strings.Contains(outStr, ".zshrc") {
		t.Errorf("expected hint to mention .zshrc for a zsh shell, got %q", outStr)
	}
	if !strings.Contains(outStr, "ln -s") {
		t.Errorf("expected hint to include a symlink alternative, got %q", outStr)
	}
}

func TestCheckMsbUsesConfiguredMSBHome(t *testing.T) {
	MockedEnsureInstalled(t, false)

	home := t.TempDir()
	runtimeHome := filepath.Join(home, "custom-runtime")
	binDir := filepath.Join(runtimeHome, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(binDir, "msb")
	testutil.WritePath(t, binPath, "#!/bin/sh\n")
	if err := os.Chmod(binPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("MSB_HOME", runtimeHome)
	t.Setenv("MSB_PATH", "")
	t.Setenv("PATH", "/nonexistent")

	warnings, err := checkMsb(context.Background())
	if err != nil {
		t.Fatalf("checkMsb() error = %v", err)
	}
	if !strings.Contains(strings.Join(warnings, " "), binDir) {
		t.Errorf("warnings = %q, want configured runtime path", warnings)
	}
}

func TestCheckMsbUsesExplicitMSBPath(t *testing.T) {
	MockedEnsureInstalled(t, false)

	home := t.TempDir()
	binDir := filepath.Join(home, "explicit")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(binDir, "msb")
	testutil.WritePath(t, binPath, "#!/bin/sh\n")
	if err := os.Chmod(binPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("MSB_HOME", "")
	t.Setenv("MSB_PATH", binPath)
	t.Setenv("PATH", "/nonexistent")

	warnings, err := checkMsb(context.Background())
	if err != nil {
		t.Fatalf("checkMsb() error = %v", err)
	}
	if !strings.Contains(strings.Join(warnings, " "), binDir) {
		t.Errorf("warnings = %q, want explicit runtime path", warnings)
	}
}

func TestCheckMsbExplicitPathMissingReturnsError(t *testing.T) {
	MockedEnsureInstalled(t, false)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("MSB_PATH", filepath.Join(t.TempDir(), "missing-msb"))
	t.Setenv("MSB_HOME", "")
	t.Setenv("PATH", "/nonexistent")

	_, err := checkMsb(context.Background())
	if err == nil {
		t.Fatal("checkMsb() error = nil, want missing explicit path error")
	}
	if !strings.Contains(err.Error(), "binary missing") {
		t.Errorf("error = %q, want binary missing", err)
	}
}

func TestCheckMsbNotOnPathAndBinaryMissingReturnsFalse(t *testing.T) {
	MockedEnsureInstalled(t, false)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/nonexistent")

	_, err := checkMsb(context.Background())
	if err == nil {
		t.Fatal("expected checkMsb to return an error when msb binary is missing")
	}
	if !strings.Contains(err.Error(), "binary missing") {
		t.Errorf("expected 'binary missing' in error, got %q", err.Error())
	}
}

func mockedCollectChecks(t *testing.T, warnings []string, errs []error) {
	orig := collectChecksFunc
	collectChecksFunc = collectChecksMockFunc(warnings, errs)
	t.Cleanup(func() { collectChecksFunc = orig })
}

func collectChecksMockFunc(warnings []string, errs []error) func(context.Context) ([]string, []error) {
	return func(context.Context) ([]string, []error) { return warnings, errs }
}

func TestCheckAllAggregatesAllFailures(t *testing.T) {
	testUI := termio.NewTestMock(t)
	mockedCollectChecks(t, nil, []error{
		errors.New("docker broken"),
		errors.New("git broken"),
	})

	if realCheckAll(context.Background(), &testUI) {
		t.Fatal("expected CheckAll to return false when checks fail")
	}

	got := make([]string, 0, len(testUI.ErrorCalls))
	for _, e := range testUI.ErrorCalls {
		got = append(got, e.Msg)
	}
	if !slices.Contains(got, "docker broken") || !slices.Contains(got, "git broken") {
		t.Errorf("expected every failure rendered, got %q", got)
	}
}

func TestCheckAllRendersWarningsWhenAllPass(t *testing.T) {
	testUI := termio.NewTestMock(t)
	mockedCollectChecks(t, []string{"msb not in PATH"}, nil)

	if !realCheckAll(context.Background(), &testUI) {
		t.Fatal("expected CheckAll to return true when there are no fatal failures")
	}
	if !slices.Contains(testUI.InfoCalls, "msb not in PATH") {
		t.Errorf("expected warning rendered, got %q", testUI.WarnCalls)
	}
}

func TestCollectChecksAggregatesFailures(t *testing.T) {
	MockedEnsureInstalled(t, false)
	MockedSelectedBackend(t, "msb")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/nonexistent")

	warnings, errs := collectChecks(context.Background())
	if len(errs) == 0 {
		t.Fatal("expected collectChecks to collect the msb failures")
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when the check fails, got %q", warnings)
	}
	joined := strings.Join(errStrings(errs), " ")
	if !strings.Contains(joined, "binary missing") {
		t.Errorf("expected an msb failure, got %q", joined)
	}
}

func TestCollectChecksMsbWarning(t *testing.T) {
	MockedEnsureInstalled(t, false)
	MockedSelectedBackend(t, "msb")

	home := t.TempDir()
	binDir := filepath.Join(home, ".microsandbox", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(binDir, "msb"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(binDir, "msb"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", "/nonexistent")

	messages, _ := collectChecks(context.Background())
	if len(messages) == 0 {
		t.Fatal("expected an msb PATH warning")
	}
	if !strings.Contains(strings.Join(messages, " "), "not in PATH") {
		t.Errorf("expected a 'not in PATH' warning, got %q", messages)
	}
}

func putFakeMsbOnPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "msb")
	testutil.WritePath(t, binPath, "#!/bin/sh\n")
	if err := os.Chmod(binPath, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCollectChecksSkipsDockerForMsb(t *testing.T) {
	MockedEnsureInstalled(t, false)
	MockedSelectedBackend(t, "msb")
	MockedCheckDocker(t, errors.New("mocked docker failure"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", putFakeMsbOnPath(t))

	_, errs := collectChecks(context.Background())
	for _, err := range errs {
		if strings.Contains(strings.ToLower(err.Error()), "docker") {
			t.Fatalf("msb backend must not report a docker failure, got %q", err)
		}
	}
}

func TestCollectChecksRunsDockerForMoby(t *testing.T) {
	MockedEnsureInstalled(t, false)
	MockedSelectedBackend(t, "moby")
	MockedCheckDocker(t, errors.New("mocked docker failure"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", putFakeMsbOnPath(t))

	_, errs := collectChecks(context.Background())
	if !strings.Contains(strings.Join(errStrings(errs), " "), "mocked docker failure") {
		t.Fatalf("moby backend must report the docker failure, got %q", errStrings(errs))
	}
}

func errStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

func TestCheckPlatform(t *testing.T) {
	_, err := os.Stat("/dev/kvm")
	switch {
	case err == nil:
		if err := checkPlatform(); err != nil {
			t.Errorf("expected checkPlatform to pass with /dev/kvm present, got %v", err)
		}
	case os.IsNotExist(err):
		if err := checkPlatform(); err == nil {
			t.Error("expected checkPlatform to fail without /dev/kvm")
		}
	default:
		t.Skipf("cannot stat /dev/kvm: %v", err)
	}
}

func TestCheckAllDelegates(t *testing.T) {
	testUI := termio.NewTestMock(t)
	mockedCollectChecks(t, nil, []error{errors.New("broken")})

	orig := checkAllFunc
	checkAllFunc = realCheckAll
	t.Cleanup(func() { checkAllFunc = orig })

	if CheckAll(context.Background(), &testUI) {
		t.Fatal("expected CheckAll to return false when a check fails")
	}
}
