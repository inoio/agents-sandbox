package doctor

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// ensureInstalled is indirected so tests can stub the SDK runtime download.
// It defaults to the real EnsureInstalled; tests reassign it and restore via
// t.Cleanup.
var ensureInstalled = func(ctx context.Context) error { //nolint:gochecknoglobals // test seam, swapped in tests
	return msb.Get().EnsureInstalled(ctx)
}

// CheckDocker reports whether the docker binary is on PATH, logging a
// descriptive error when it is not.
func CheckDocker(ui termio.UI) bool {
	if _, err := exec.LookPath("docker"); err != nil {
		ui.Errorf("docker not found. Install Docker or Podman with docker-compatible CLI: %v", err)
		return false
	}
	return true
}

func checkGit(ui termio.UI) bool {
	if _, err := exec.LookPath("git"); err != nil {
		ui.Errorf("git not found. Install git via your system package manager: %v", err)
		return false
	}
	return true
}

func checkMsb(ctx context.Context, ui termio.UI) bool {
	if err := ensureMsbInstalled(ctx, ui); err != nil {
		return false
	}
	if _, err := exec.LookPath("msb"); err == nil {
		return true
	}
	home, binDir, binPath, ok := msbBinPath(ui)
	if !ok {
		return false
	}
	appendPathHint(home, os.Getenv("SHELL"), binDir, binPath, ui)
	return true
}

// SetEnsureInstalled replaces the ensureInstalled factory used by the
// doctor package. The original factory is returned so callers can restore
// it after their test.
//
// Usage from an external test package:
//
//	orig := doctor.SetEnsureInstalled(func(ctx context.Context) error {
//	    return nil // succeed
//	})
//	t.Cleanup(func() { doctor.SetEnsureInstalled(orig) })
func SetEnsureInstalled(f func(ctx context.Context) error) func(ctx context.Context) error {
	orig := ensureInstalled
	ensureInstalled = f
	return orig
}

// CheckAllFunc is an overridable CheckAll function for testing.
// Tests override it and restore via t.Cleanup.
//
//nolint:gochecknoglobals // test seam, swapped in external test packages
var CheckAllFunc = checkAllReal

// checkAllReal contains the actual CheckAll logic. This allows the exported
// CheckAllFunc to be reassigned in tests without redefining the checks.
func checkAllReal(ctx context.Context, ui termio.UI) bool {
	if !checkDoctor(ctx, ui) {
		return false
	}
	return checkOrphans(ctx, ui)
}

// CheckAll runs all prerequisite checks and reports orphaned VMs.
func CheckAll(ctx context.Context, ui termio.UI) bool {
	return CheckAllFunc(ctx, ui)
}

func isOrphanedSandbox(name string) bool {
	if !strings.HasPrefix(name, naming.SbPrefix) {
		return false
	}
	// vm- sandboxes are the current model; task- sandboxes are operational (prefill).
	return !strings.HasPrefix(name, naming.VmPrefix) &&
		!strings.HasPrefix(name, naming.TaskPrefix)
}

func isOrphanedVolume(name string) bool {
	if strings.HasPrefix(name, naming.HomePrefix) {
		return false
	}
	// clone- volumes are obsolete (clone-on-use removed) → orphaned.
	if strings.HasPrefix(name, naming.ClonePrefix) {
		return true
	}
	return strings.Contains(name, "-opencode-home-")
}

func isOrphanedImage(ref string) bool {
	// Old format images used ":" directly after the namespace (e.g. "opencode-msb/runner:base").
	// These predate the current naming and are always orphaned.
	return strings.HasPrefix(ref, naming.Prefix+"/runner:")
}

func checkOrphans(ctx context.Context, ui termio.UI) bool {
	client := msb.Get()
	hasOrphans := false

	sandboxHandles, err := client.ListSandboxes(ctx)
	if err != nil {
		ui.Warnf("Failed to list sandboxes for orphan check: %v", err)
	} else {
		for _, h := range sandboxHandles {
			name := h.Name()
			if isOrphanedSandbox(name) {
				ui.Warnf("Found orphaned sandbox: %s", name)
				hasOrphans = true
			}
		}
	}

	volumeHandles, err := client.ListVolumes(ctx)
	if err != nil {
		ui.Warnf("Failed to list volumes for orphan check: %v", err)
	} else {
		for _, h := range volumeHandles {
			name := h.Name()
			if isOrphanedVolume(name) {
				ui.Warnf("Found orphaned volume: %s", name)
				hasOrphans = true
			}
		}
	}

	imageHandles, err := client.ImageList(ctx)
	if err != nil {
		ui.Warnf("Failed to list images for orphan check: %v", err)
	} else {
		for _, h := range imageHandles {
			ref := h.Reference()
			if isOrphanedImage(ref) {
				ui.Warnf("Found orphaned image: %s", ref)
				hasOrphans = true
			}
		}
	}

	return !hasOrphans
}
