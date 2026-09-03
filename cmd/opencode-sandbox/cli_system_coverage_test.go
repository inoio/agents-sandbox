package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/doctor"
	"github.com/inoio/opencode-sandbox/internal/sandbox/image"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
	launcherconfig "github.com/inoio/opencode-sandbox/internal/viperconfig"
)

func TestVolumeMigrateUnknownAgent(t *testing.T) {
	initTestRepo(t)
	cmd, _ := setupCommandFixtures(t, "volume", "migrate", "--agent", "bogus")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expected unknown agent error, got %v", err)
	}
}

func TestVolumeMigrateWithPositionalArg(t *testing.T) {
	initTestRepo(t)
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	mock := &msb.MockMsbClient{}
	doctor.MockedCheckAll(t, true)
	image.WithMockAgentVersion(t, "0.0.0-test")
	docker.WithNoopDockerMock(t)
	slug := git.ProjectSlug()
	state.WriteState(slug, state.HomeState{
		HomeVolume: fmt.Sprintf("opencode-sandbox-home-%s-20260101T000000", slug),
	})
	msb.WithMsbMock(t, mock)

	root := buildRootCmd(ui)
	root.SetArgs([]string{"volume", "migrate", "explicit-vol-name"})
	if err := root.Execute(); err != nil {
		t.Fatalf("volume migrate with positional arg: %v", err)
	}
}

func TestBuildCommandOpenCodeVersionFallback(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "build", "--opencode-version", "1.2.3", "--dry-run")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build --opencode-version --dry-run: %v", err)
	}
}

func TestBuildCommandUnknownAgent(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "build", "--agent", "bogus")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expected unknown agent error, got %v", err)
	}
}

func TestBuildDockerfileUnknownAgent(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "build", "dockerfile", "--agent", "bogus")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expected unknown agent error, got %v", err)
	}
}

// errWriter returns the configured error on every write.
type errWriter struct{ err error }

func (w errWriter) Write(_ []byte) (int, error) { return 0, w.err }

// errStdOutUI embeds termio.Mock but returns an erroring StdOut writer.
type errStdOutUI struct {
	termio.Mock

	err error
}

func (e *errStdOutUI) StdOut() io.Writer { return errWriter{e.err} }

func TestBuildDockerfileStdOutError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	image.WithMockAgentVersion(t, "0.0.0-test")
	ui := &errStdOutUI{err: errors.New("write failed")}

	root := buildRootCmd(ui)
	root.SetArgs([]string{"build", "dockerfile"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected StdOut write error, got %v", err)
	}
}

func TestImagePruneInvalidAge(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "image", "prune", "--age", "bogus")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid age") {
		t.Fatalf("expected invalid age error, got %v", err)
	}
}

func TestSandboxPruneInvalidAge(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "sandbox", "prune", "--age", "bogus")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid age") {
		t.Fatalf("expected invalid age error, got %v", err)
	}
}

func TestShellCommandInvalidSize(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "shell", "--tmp-size", "bogus")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --tmp-size") {
		t.Fatalf("expected invalid --tmp-size error, got %v", err)
	}
}

func TestResolvePruneAgeFromResolver(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildPruneCmd(ui)
	ctx := context.WithValue(context.Background(), (*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{ManualPruneAge: 5 * time.Hour}))
	cmd.SetContext(ctx)

	d, err := resolvePruneAge(cmd)
	if err != nil {
		t.Fatalf("resolvePruneAge: %v", err)
	}
	if d != 5*time.Hour {
		t.Errorf("resolvePruneAge = %v, want 5h", d)
	}
}

func TestConfigAgentHomeManifestError(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "agent", "opencode")
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "home.yaml", "../escape:\n")

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error from an escaping home.yaml target")
	}
}

// TestRunFuncServeOnlyContextError drives runFunc directly with --serve-only
// and an invalid resolver value so it takes the serve-only context branch and
// then fails inside runFunc's own launcherconfig.NewResolver.
func TestRunFuncServeOnlyContextError(t *testing.T) {
	initTestRepo(t)
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_CPUS", "999")
	ui := &termio.Mock{}

	cmd := buildRunCmd(ui)
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set(flagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	fn := runFunc(ui)
	if err := fn(cmd, nil); err == nil {
		t.Fatal("expected an error from the invalid resolver config")
	}
}
