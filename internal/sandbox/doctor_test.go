package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testhelpers"
)

func TestCheckDockerLogsUnderlyingError(t *testing.T) {
	var buf bytes.Buffer
	testUI := testhelpers.NewTestio(t)

	t.Setenv("PATH", "/nonexistent")
	if CheckDocker(&testUI) {
		t.Fatal("expected CheckDocker to return false when docker is not on PATH")
	}

	out := buf.String()
	if !strings.Contains(out, "docker not found") {
		t.Errorf("expected log to contain 'docker not found', got %q", out)
	}
	if !strings.Contains(out, "executable file not found") {
		t.Errorf("expected log to contain the underlying LookPath error, got %q", out)
	}
}

func TestShellRcFile(t *testing.T) {
	home := "/home/test"
	tests := []struct {
		name  string
		shell string
		want  string
	}{
		{"bash", "/bin/bash", filepath.Join(home, ".bashrc")},
		{"zsh", "/bin/zsh", filepath.Join(home, ".zshrc")},
		{"fish", "/usr/bin/fish", filepath.Join(home, ".config", "fish", "config.fish")},
		{"empty shell falls back to bashrc", "", filepath.Join(home, ".bashrc")},
		{"unknown shell falls back to bashrc", "/bin/sh", filepath.Join(home, ".bashrc")},
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
	var buf bytes.Buffer
	testUI := testhelpers.NewTestio(t)

	prev := ensureInstalled
	t.Cleanup(func() { ensureInstalled = prev })
	ensureInstalled = func(context.Context) error { return errors.New("network unreachable") }

	if CheckMsb(context.Background(), &testUI) {
		t.Fatal("expected CheckMsb to return false when ensureInstalled fails")
	}
	out := buf.String()
	if !strings.Contains(out, "msb runtime setup failed") {
		t.Errorf("expected log to mention 'msb runtime setup failed', got %q", out)
	}
	if !strings.Contains(out, "network unreachable") {
		t.Errorf("expected log to contain the underlying error, got %q", out)
	}
	if strings.Contains(out, "Install microsandbox") || strings.Contains(out, "github.com/microsandbox") {
		t.Errorf("expected no 'install msb' instruction, got %q", out)
	}
}

func TestCheckMsbOnPathReturnsTrueSilently(t *testing.T) {
	var buf bytes.Buffer
	testUI := testhelpers.NewTestio(t)

	prev := ensureInstalled
	t.Cleanup(func() { ensureInstalled = prev })
	ensureInstalled = func(context.Context) error { return nil }

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "msb"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if !CheckMsb(context.Background(), &testUI) {
		t.Fatal("expected CheckMsb to return true when msb is on PATH")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output when msb is on PATH, got %q", buf.String())
	}
}

func TestCheckMsbNotOnPathExtendsPathAndReturnsTrue(t *testing.T) {
	var buf bytes.Buffer
	testUI := testhelpers.NewTestio(t)

	prev := ensureInstalled
	t.Cleanup(func() { ensureInstalled = prev })
	ensureInstalled = func(context.Context) error { return nil }

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

	if !CheckMsb(context.Background(), &testUI) {
		t.Fatal("expected CheckMsb to return true when msb is installed but not on PATH")
	}
	out := buf.String()
	if !strings.Contains(out, "not on your PATH") {
		t.Errorf("expected a warning that msb is not on PATH, got %q", out)
	}
	if !strings.Contains(out, ".zshrc") {
		t.Errorf("expected hint to mention .zshrc for a zsh shell, got %q", out)
	}
	if !strings.Contains(out, "ln -s") {
		t.Errorf("expected hint to include a symlink alternative, got %q", out)
	}
	got := os.Getenv("PATH")
	if !strings.HasPrefix(got, binDir) {
		t.Errorf("expected PATH to start with %q, got %q", binDir, got)
	}
}

func TestCheckMsbNotOnPathAndBinaryMissingReturnsFalse(t *testing.T) {
	var buf bytes.Buffer
	testUI := testhelpers.NewTestio(t)

	prev := ensureInstalled
	t.Cleanup(func() { ensureInstalled = prev })
	ensureInstalled = func(context.Context) error { return nil }

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/nonexistent")

	if CheckMsb(context.Background(), &testUI) {
		t.Fatal("expected CheckMsb to return false when msb binary is missing")
	}
	out := buf.String()
	if !strings.Contains(out, "binary missing") {
		t.Errorf("expected 'binary missing' in output, got %q", out)
	}
}

func TestIsOrphanedSandboxVM(t *testing.T) {
	if isOrphanedSandbox("opencode-msb-vm-proj-main") {
		t.Error("expected vm- sandbox to NOT be orphaned")
	}
}

func TestIsOrphanedSandboxOldSBPrefix(t *testing.T) {
	if !isOrphanedSandbox("opencode-msb-sb-proj-main") {
		t.Error("expected old sb- sandbox to be orphaned")
	}
}

func TestIsOrphanedSandboxTaskPrefix(t *testing.T) {
	if isOrphanedSandbox("opencode-msb-task-prefill-proj-123") {
		t.Error("expected task sandbox to NOT be orphaned")
	}
}

func TestIsOrphanedSandboxForeign(t *testing.T) {
	if isOrphanedSandbox("someone-elses-sandbox") {
		t.Error("expected foreign sandbox to NOT be orphaned")
	}
}

func TestIsOrphanedVolumeHome(t *testing.T) {
	if isOrphanedVolume("opencode-msb-home-proj-aBc1234D") {
		t.Error("expected home volume to NOT be orphaned")
	}
}

func TestIsOrphanedVolumeClone(t *testing.T) {
	if !isOrphanedVolume("opencode-msb-clone-proj-aBc1234D-123") {
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
	if !isOrphanedImage("opencode-msb/runner:base") {
		t.Error("expected :base image to be orphaned")
	}
	if !isOrphanedImage("opencode-msb/runner:latest") {
		t.Error("expected :latest image to be orphaned")
	}
	if !isOrphanedImage("opencode-msb/runner:sha256-abc123") {
		t.Error("expected sha256-prefixed image to be orphaned")
	}
	if isOrphanedImage("opencode-msb/runner-base:latest") {
		t.Error("expected new runner-base image to not be orphaned")
	}
	if isOrphanedImage("opencode-msb/runner-proj:latest") {
		t.Error("expected project-specific runner image to not be orphaned")
	}
	if isOrphanedImage("some-other-image:latest") {
		t.Error("expected foreign image to not be orphaned")
	}
}
