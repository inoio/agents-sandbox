package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/opener"
)

func TestAgentsSandboxPathMergeScript(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		imagePath string
		setImage  bool
		sources   int
		want      string
	}{
		{
			name:      "appends missing image entries in image order",
			path:      "/usr/local/bin:/usr/bin",
			imagePath: "/usr/local/bin:/opt/agents-sandbox/bin:/usr/local/go/bin",
			setImage:  true,
			sources:   1,
			want:      "/usr/local/bin:/usr/bin:/opt/agents-sandbox/bin:/usr/local/go/bin",
		},
		{
			name:      "preserves login additions and their order",
			path:      "/.msb/scripts:/usr/local/bin:/usr/bin",
			imagePath: "/usr/local/go/bin:/usr/local/bin",
			setImage:  true,
			sources:   1,
			want:      "/.msb/scripts:/usr/local/bin:/usr/bin:/usr/local/go/bin",
		},
		{
			name:      "adds nothing when all entries present",
			path:      "/usr/local/bin:/opt/agents-sandbox/bin",
			imagePath: "/usr/local/bin:/opt/agents-sandbox/bin",
			setImage:  true,
			sources:   1,
			want:      "/usr/local/bin:/opt/agents-sandbox/bin",
		},
		{
			name:      "prefix of an existing entry is still appended",
			path:      "/opt/agents-sandbox/bin",
			imagePath: "/opt",
			setImage:  true,
			sources:   1,
			want:      "/opt/agents-sandbox/bin:/opt",
		},
		{
			name:     "no-op when image path is unset",
			path:     "/usr/local/bin:/usr/bin",
			setImage: false,
			sources:  1,
			want:     "/usr/local/bin:/usr/bin",
		},
		{
			name:      "no-op when image path is empty",
			path:      "/usr/local/bin",
			imagePath: "",
			setImage:  true,
			sources:   1,
			want:      "/usr/local/bin",
		},
		{
			name:      "idempotent when sourced twice",
			path:      "/usr/local/bin:/usr/bin",
			imagePath: "/usr/local/bin:/opt/agents-sandbox/bin",
			setImage:  true,
			sources:   2,
			want:      "/usr/local/bin:/usr/bin:/opt/agents-sandbox/bin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runMergeScript(t, tc.path, tc.imagePath, tc.setImage, tc.sources)
			if got != tc.want {
				t.Errorf("PATH = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestVendoredTrustHelperMatchesRecordedHash enforces the verbatim-vendoring
// contract: the helper is upstream's file unmodified, and the hash beside it
// names the revision we shipped. Re-syncing the script has to update the
// recorded hash; hand-editing it — a tempting way to "fix" the messages that say
// `sh msb-trust.sh` — fails here instead of silently making the license lie.
func TestVendoredTrustHelperMatchesRecordedHash(t *testing.T) {
	script, err := os.ReadFile(vendoredTrustHelperPath)
	if err != nil {
		t.Fatalf("read vendored trust helper: %v", err)
	}
	license, err := os.ReadFile(vendoredTrustHelperLicensePath)
	if err != nil {
		t.Fatalf("read trust helper license: %v", err)
	}

	want := recordedTrustHelperHash(t, string(license))
	if got := fmt.Sprintf("%x", sha256.Sum256(script)); got != want {
		t.Errorf(
			"%s hashes to %s, but %s records %s",
			vendoredTrustHelperPath,
			got,
			vendoredTrustHelperLicensePath,
			want,
		)
	}
}

func TestHostOpenerShimUsesTheLauncherSpoolPath(t *testing.T) {
	if !strings.Contains(string(hostOpenerShimScript()), "/home/dev/"+opener.SpoolRelativePath) {
		t.Fatalf("shim spool path differs from opener.SpoolRelativePath %q", opener.SpoolRelativePath)
	}
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
	t.Setenv("AGENTS_SANDBOX_OPEN_TIMEOUT_TENTHS", "1")
	code, stderr := runShim(t, nil, false, "https://example.com")
	if code != 3 || !strings.Contains(stderr, "open-links") {
		t.Fatalf("code %d, stderr %s", code, stderr)
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
	//testenv:allow the shim child needs the parent's environment
	command.Env = append(os.Environ(),
		"AGENTS_SANDBOX_OPEN_SPOOL="+filepath.Join(homeVolume, opener.SpoolRelativePath),
	)
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

// runMergeScript sources data/agents-sandbox-path.sh in a fresh POSIX shell
// with the given environment and returns the resulting PATH. A source count > 1
// exercises re-sourcing (nested login shells).
func runMergeScript(t *testing.T, path, imagePath string, setImage bool, sources int) string {
	t.Helper()
	script, err := filepath.Abs("data/agents-sandbox-path.sh")
	if err != nil {
		t.Fatalf("abs script path: %v", err)
	}
	shell := ". " + script
	if sources > 1 {
		shell += "; . " + script
	}
	shell += `; printf '%s' "$PATH"`

	cmd := exec.Command("sh", "-c", shell)
	cmd.Env = []string{"PATH=" + path}
	if setImage {
		cmd.Env = append(cmd.Env, "AGENTS_SANDBOX_IMAGE_PATH="+imagePath)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh merge script: %v", err)
	}
	return string(out)
}

// recordedTrustHelperHash returns the SHA-256 recorded in the license file's
// provenance header.
func recordedTrustHelperHash(t *testing.T, license string) string {
	t.Helper()
	for line := range strings.SplitSeq(license, "\n") {
		hash, found := strings.CutPrefix(strings.TrimSpace(line), "SHA-256:")
		if found {
			return strings.TrimSpace(hash)
		}
	}
	t.Fatalf("%s records no SHA-256 line", vendoredTrustHelperLicensePath)
	return ""
}
