package image

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/opener"
)

const hostOpenerShimPath = "data/xdg-open.sh"

func TestWriteDockerfileTarShipsHostOpenerShimExecutable(t *testing.T) {
	var buildContext bytes.Buffer
	if err := writeDockerfileTar(&buildContext, []byte("FROM scratch\n")); err != nil {
		t.Fatalf("writeDockerfileTar: %v", err)
	}
	entries, modes := readTarEntries(t, &buildContext)
	want, err := os.ReadFile(hostOpenerShimPath)
	if err != nil {
		t.Fatal(err)
	}
	if entries[hostOpenerShimAsset] != string(want) {
		t.Errorf("tar entry %q does not carry the shim", hostOpenerShimAsset)
	}
	if modes[hostOpenerShimAsset] != 0o755 {
		t.Errorf("tar entry %q mode = %o, want 755", hostOpenerShimAsset, modes[hostOpenerShimAsset])
	}
}

// TestRenderDockerfileInstallsHostOpenerShim covers the final-stage wiring: the
// shim lands in /usr/local/bin, which precedes /usr/bin on PATH so it shadows a
// real xdg-open a custom base might ship, under every name agents call, and
// BROWSER points at it so claude-code skips its headless no_display guard.
func TestRenderDockerfileInstallsHostOpenerShim(t *testing.T) {
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	final := finalStage(t, mustRender(t, a, nil, dockerOn))
	for _, want := range []string{
		"COPY " + hostOpenerShimAsset + " /usr/local/bin/xdg-open",
		"RUN ln -sf xdg-open /usr/local/bin/x-www-browser",
		"ln -sf xdg-open /usr/local/bin/sensible-browser",
		`ENV BROWSER="xdg-open"`,
	} {
		if !strings.Contains(final, want) {
			t.Errorf("final stage lacks %q; got:\n%s", want, final)
		}
	}
}

func TestHostOpenerShimUsesTheLauncherSpoolPath(t *testing.T) {
	if !strings.Contains(string(hostOpenerShimScript()), "/home/dev/"+opener.SpoolRelativePath) {
		t.Fatalf("shim spool path differs from opener.SpoolRelativePath %q", opener.SpoolRelativePath)
	}
}

// runShim runs the shim against a spool in a temporary home volume served by a
// real opener.Watcher with handle, and returns its exit code and stderr.
func runShim(t *testing.T, handle opener.Handler, serve bool, arguments ...string) (int, string) {
	t.Helper()
	homeVolume := t.TempDir()
	if serve {
		ctx, cancel := context.WithCancel(context.Background())
		watcher := &opener.Watcher{HomeVolumePath: homeVolume, Handle: handle, PollInterval: 10 * time.Millisecond}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = watcher.Watch(ctx)
		}()
		t.Cleanup(func() {
			cancel()
			<-done
		})
	}
	command := exec.Command("sh", append([]string{hostOpenerShimPath}, arguments...)...)
	command.Env = append(os.Environ(), "AGENTS_SANDBOX_OPEN_SPOOL="+filepath.Join(homeVolume, opener.SpoolRelativePath))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	if exitError, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitError.ExitCode(), stderr.String()
	}
	if err != nil {
		t.Fatalf("run shim: %v", err)
	}
	return 0, stderr.String()
}

func TestHostOpenerShimRoundTrip(t *testing.T) {
	var opened []string
	code, stderr := runShim(t, func(_ context.Context, target string) error {
		opened = append(opened, target)
		return nil
	}, true, "https://example.com/?a=1&b=2")
	if code != 0 || len(opened) != 1 || opened[0] != "https://example.com/?a=1&b=2" {
		t.Fatalf("code %d, opened %q, stderr %s", code, opened, stderr)
	}
}

func TestHostOpenerShimReportsRefusal(t *testing.T) {
	code, stderr := runShim(t, opener.NewHostHandler(func(string) error {
		t.Fatal("refused target must not be opened")
		return nil
	}, nil), true, "file:///etc/passwd")
	if code != 4 || !strings.Contains(stderr, "scheme not allowed") || !strings.Contains(stderr, "file:///etc/passwd") {
		t.Fatalf("code %d, stderr %s", code, stderr)
	}
}

func TestHostOpenerShimUsage(t *testing.T) {
	if code, _ := runShim(t, nil, false); code != 1 {
		t.Fatalf("no argument: code %d, want 1", code)
	}
	if code, _ := runShim(t, nil, false, "a", "b"); code != 1 {
		t.Fatalf("two arguments: code %d, want 1", code)
	}
	if code, _ := runShim(t, nil, false, strings.Repeat("a", opener.MaxTargetBytes+1)); code != 1 {
		t.Fatalf("oversized target: code %d, want 1", code)
	}
}

func TestHostOpenerShimTimesOutWithoutLauncher(t *testing.T) {
	t.Setenv("AGENTS_SANDBOX_OPEN_TIMEOUT_TENTHS", "2")
	code, stderr := runShim(t, nil, false, "https://example.com")
	if code != 3 || !strings.Contains(stderr, "open-links") {
		t.Fatalf("code %d, stderr %s", code, stderr)
	}
}
