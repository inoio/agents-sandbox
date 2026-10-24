package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtime"
	"github.com/inoio/agents-sandbox/internal/termio"
)

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
	if !strings.Contains(err.Error(), "troubleshooting.html#microsandbox-runtime") {
		t.Errorf("expected the msb failure to link its troubleshooting section, got %q", err.Error())
	}
}

func TestCheckMsbReportsPrivateHomeBinPathWithoutPathHints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	MockedEnsureInstalled(t, false)
	t.Setenv("PATH", "/nonexistent")
	t.Setenv("SHELL", "/bin/zsh")

	warnings, err := checkMsb(context.Background())
	if err != nil {
		t.Fatalf("checkMsb() error = %v", err)
	}
	home := runtime.PrivateHome()
	binDir := filepath.Join(home, "bin")
	outStr := strings.Join(warnings, " ")
	if !strings.Contains(outStr, binDir) {
		t.Errorf("expected the private-home bin path %q, got %q", binDir, outStr)
	}
	for _, hint := range []string{"PATH", "shellRc", "zshrc", "rc file", "ln -s", "symlink"} {
		if strings.Contains(outStr, hint) {
			t.Errorf("expected no %q hint, got %q", hint, outStr)
		}
	}
}

func TestCheckMsbIgnoresMsbHomeAndMsbPathEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	MockedEnsureInstalled(t, false)

	legacyHome := filepath.Join(t.TempDir(), "legacy-home")
	t.Setenv("MSB_HOME", legacyHome)
	t.Setenv("MSB_PATH", filepath.Join(legacyHome, "bin", "msb"))
	t.Setenv("PATH", "/nonexistent")

	warnings, err := checkMsb(context.Background())
	if err != nil {
		t.Fatalf("checkMsb() error = %v", err)
	}
	home := runtime.PrivateHome()
	outStr := strings.Join(warnings, " ")
	if !strings.Contains(outStr, filepath.Join(home, "bin")) {
		t.Errorf("expected the private-home bin path, got %q", outStr)
	}
	if strings.Contains(outStr, legacyHome) {
		t.Errorf("expected MSB_HOME/MSB_PATH to be ignored, got %q", outStr)
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

func TestRealCheckDockerPingUnreachable(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		PingFn: func(context.Context, client.PingOptions) (client.PingResult, error) {
			return client.PingResult{}, errors.New("boom")
		},
	})

	err := realCheckDocker(context.Background())
	if err == nil {
		t.Fatal("expected realCheckDocker to fail when the daemon is unreachable")
	}
	if !strings.Contains(err.Error(), "docker API unreachable") {
		t.Errorf("expected error to mention 'docker API unreachable', got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected error to wrap the underlying ping failure, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "docker info") {
		t.Errorf("expected a copy-pasteable verification command, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "troubleshooting.html#docker-not-running") {
		t.Errorf("expected the docker failure to link its troubleshooting section, got %q", err.Error())
	}
}

func TestRealCheckDockerPingOK(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{})

	if err := realCheckDocker(context.Background()); err != nil {
		t.Errorf("expected realCheckDocker to succeed when the ping succeeds, got %v", err)
	}
}

func TestCollectChecksAggregatesFailures(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	MockedCheckDocker(t, false)
	MockedEnsureInstalled(t, false)
	t.Setenv("PATH", "/nonexistent")

	warnings, errs := collectChecks(context.Background())
	if len(errs) == 0 {
		t.Fatal("expected collectChecks to collect the docker failure")
	}
	joined := strings.Join(errStrings(errs), " ")
	if !strings.Contains(joined, "docker not available") {
		t.Errorf("expected a docker failure, got %q", joined)
	}
	if strings.Contains(joined, "binary missing") {
		t.Errorf("expected no msb binary failure, got %q", joined)
	}
	if len(warnings) == 0 {
		t.Fatal("expected an informational msb message even when the binary is missing")
	}
}

func TestCollectChecksMsbInfoNamesPrivateHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	MockedCheckDocker(t, true)
	MockedEnsureInstalled(t, false)
	t.Setenv("PATH", "/nonexistent")

	messages, errs := collectChecks(context.Background())
	for _, err := range errs {
		if strings.Contains(err.Error(), "binary missing") || strings.Contains(err.Error(), "msb") {
			t.Fatalf("expected no msb error, got %v", err)
		}
	}
	home := runtime.PrivateHome()
	joined := strings.Join(messages, " ")
	if !strings.Contains(joined, filepath.Join(home, "bin")) {
		t.Errorf("expected the private-home bin path, got %q", joined)
	}
	if strings.Contains(joined, "PATH") {
		t.Errorf("expected no PATH hint, got %q", joined)
	}
}

// TestCollectChecksCollectsMsbError covers collectChecks appending the msb check
// failure when the private runtime cannot be ensured.
func TestCollectChecksCollectsMsbError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	MockedCheckDocker(t, true)
	MockedEnsureInstalled(t, true)

	_, errs := collectChecks(context.Background())
	joined := strings.Join(errStrings(errs), " ")
	if !strings.Contains(joined, "msb runtime setup failed") {
		t.Errorf("expected an msb setup failure, got %q", joined)
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
