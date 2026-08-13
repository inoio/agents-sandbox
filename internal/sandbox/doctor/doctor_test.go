package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
)

// renderErrorCall mirrors termio.printer.Error, which renders "msg: err", so
// tests can assert on both the guidance text and the underlying error.
func renderErrorCall(e termio.ErrorCall) string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Err.Error()
}

func TestCheckDockerLogsUnderlyingError(t *testing.T) {
	testUI := testutil.TermUIMock(t)

	t.Setenv("PATH", "/nonexistent")
	if CheckDocker(&testUI) {
		t.Fatal("expected CheckDocker to return false when docker is not on PATH")
	}

	var out []string
	for _, e := range testUI.ErrorCalls {
		out = append(out, renderErrorCall(e))
	}
	outStr := strings.Join(out, " ")
	if !strings.Contains(outStr, "docker not found") {
		t.Errorf("expected log to contain 'docker not found', got %q", outStr)
	}
	if !strings.Contains(outStr, "executable file not found") {
		t.Errorf("expected log to contain the underlying LookPath error, got %q", outStr)
	}
}

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
	testUI := testutil.TermUIMock(t)

	prev := SetEnsureInstalled(func(context.Context) error { return errors.New("network unreachable") })
	t.Cleanup(func() { SetEnsureInstalled(prev) })

	if checkMsb(context.Background(), &testUI) {
		t.Fatal("expected CheckMsb to return false when ensureInstalled fails")
	}
	var out []string
	for _, e := range testUI.ErrorCalls {
		out = append(out, renderErrorCall(e))
	}
	outStr := strings.Join(out, " ")
	if !strings.Contains(outStr, "msb runtime setup failed") {
		t.Errorf("expected log to mention 'msb runtime setup failed', got %q", outStr)
	}
	if !strings.Contains(outStr, "network unreachable") {
		t.Errorf("expected log to contain the underlying error, got %q", outStr)
	}
	if strings.Contains(outStr, "Install microsandbox") || strings.Contains(outStr, "github.com/microsandbox") {
		t.Errorf("expected no 'install msb' instruction, got %q", outStr)
	}
}

func TestCheckMsbOnPathReturnsTrueSilently(t *testing.T) {
	testUI := testutil.TermUIMock(t)

	prev := SetEnsureInstalled(func(context.Context) error { return nil })
	t.Cleanup(func() { SetEnsureInstalled(prev) })

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "msb"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if !checkMsb(context.Background(), &testUI) {
		t.Fatal("expected CheckMsb to return true when msb is on PATH")
	}
	if len(testUI.ErrorCalls)+len(testUI.WarnCalls)+len(testUI.InfoCalls) != 0 {
		t.Errorf("expected no output when msb is on PATH, got errors=%d warns=%d infos=%d",
			len(testUI.ErrorCalls), len(testUI.WarnCalls), len(testUI.InfoCalls))
	}
}

func TestCheckMsbNotOnPathExtendsPathAndReturnsTrue(t *testing.T) {
	testUI := testutil.TermUIMock(t)

	prev := SetEnsureInstalled(func(context.Context) error { return nil })
	t.Cleanup(func() { SetEnsureInstalled(prev) })

	home := t.TempDir()
	binDir := filepath.Join(home, ".microsandbox", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(binDir, "msb")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", "/nonexistent")

	if !checkMsb(context.Background(), &testUI) {
		t.Fatal("expected CheckMsb to return true when msb is installed but not on PATH")
	}
	out := make([]string, 0, len(testUI.WarnCalls)+len(testUI.InfoCalls))
	out = append(out, testUI.WarnCalls...)
	out = append(out, testUI.InfoCalls...)
	outStr := strings.Join(out, " ")
	if !strings.Contains(outStr, "not on your PATH") {
		t.Errorf("expected a warning that msb is not on PATH, got %q", outStr)
	}
	if !strings.Contains(outStr, ".zshrc") {
		t.Errorf("expected hint to mention .zshrc for a zsh shell, got %q", outStr)
	}
	if !strings.Contains(outStr, "ln -s") {
		t.Errorf("expected hint to include a symlink alternative, got %q", outStr)
	}
	got := os.Getenv("PATH")
	if !strings.HasPrefix(got, binDir) {
		t.Errorf("expected PATH to start with %q, got %q", binDir, got)
	}
}

func TestCheckMsbNotOnPathAndBinaryMissingReturnsFalse(t *testing.T) {
	testUI := testutil.TermUIMock(t)

	prev := SetEnsureInstalled(func(context.Context) error { return nil })
	t.Cleanup(func() { SetEnsureInstalled(prev) })

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/nonexistent")

	if checkMsb(context.Background(), &testUI) {
		t.Fatal("expected CheckMsb to return false when msb binary is missing")
	}
	var out []string
	for _, e := range testUI.ErrorCalls {
		out = append(out, e.Msg)
	}
	outStr := strings.Join(out, " ")
	if !strings.Contains(outStr, "binary missing") {
		t.Errorf("expected 'binary missing' in output, got %q", outStr)
	}
}

func TestIsOrphanedSandboxVM(t *testing.T) {
	if isOrphanedSandbox("opencode-sandbox-vm-proj-main") {
		t.Error("expected vm- sandbox to NOT be orphaned")
	}
}

func TestIsOrphanedSandboxOldSBPrefix(t *testing.T) {
	if !isOrphanedSandbox("opencode-sandbox-sb-proj-main") {
		t.Error("expected old sb- sandbox to be orphaned")
	}
}

func TestIsOrphanedSandboxTaskPrefix(t *testing.T) {
	if isOrphanedSandbox("opencode-sandbox-task-prefill-proj-123") {
		t.Error("expected task sandbox to NOT be orphaned")
	}
}

func TestIsOrphanedSandboxForeign(t *testing.T) {
	if isOrphanedSandbox("someone-elses-sandbox") {
		t.Error("expected foreign sandbox to NOT be orphaned")
	}
}

func TestIsOrphanedVolumeHome(t *testing.T) {
	if isOrphanedVolume("opencode-sandbox-home-proj-aBc1234D") {
		t.Error("expected home volume to NOT be orphaned")
	}
}

func TestIsOrphanedVolumeClone(t *testing.T) {
	if !isOrphanedVolume("opencode-sandbox-clone-proj-aBc1234D-123") {
		t.Error("expected clone volume to be orphaned (clone-on-use removed)")
	}
}

func TestIsOrphanedVolumeOldPattern(t *testing.T) {
	if !isOrphanedVolume("proj-opencode-home-sha256-abc") {
		t.Error("expected old-pattern volume to be orphaned")
	}
}

func TestIsOrphanedVolumeForeign(t *testing.T) {
	if isOrphanedVolume("random-volume") {
		t.Error("expected foreign volume to NOT be orphaned")
	}
}

func TestIsOrphanedImageOldPatterns(t *testing.T) {
	if !isOrphanedImage("opencode-sandbox/runner:base") {
		t.Error("expected :base image to be orphaned")
	}
	if !isOrphanedImage("opencode-sandbox/runner:latest") {
		t.Error("expected :latest image to be orphaned")
	}
	if !isOrphanedImage("opencode-sandbox/runner:sha256-abc123") {
		t.Error("expected sha256-prefixed image to be orphaned")
	}
	if isOrphanedImage("opencode-sandbox/runner-base:latest") {
		t.Error("expected new runner-base image to not be orphaned")
	}
	if isOrphanedImage("opencode-sandbox/runner-proj:latest") {
		t.Error("expected project-specific runner image to not be orphaned")
	}
	if isOrphanedImage("some-other-image:latest") {
		t.Error("expected foreign image to not be orphaned")
	}
}
