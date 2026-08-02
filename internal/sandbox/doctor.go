package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// ensureInstalled is indirected so tests can stub the SDK runtime download.
// It defaults to the real EnsureInstalled; tests reassign it and restore via
// t.Cleanup.
var ensureInstalled = func(ctx context.Context) error { //nolint:gochecknoglobals // test seam, swapped in tests
	return msb.EnsureInstalled(ctx)
}

// shellRcFile returns the rc file a PATH export should be appended to, chosen by
// the basename of the login shell (e.g. "/bin/zsh" -> "~/.zshrc"). Unknown or
// empty shells fall back to ~/.bashrc, the most common default on Linux.
func shellRcFile(home, shell string) string {
	switch filepath.Base(shell) {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish")
	default:
		return filepath.Join(home, ".bashrc")
	}
}

func CheckDocker(ui stdio.UI) bool {
	if _, err := exec.LookPath("docker"); err != nil {
		ui.Errorf("docker not found. Install Docker or Podman with docker-compatible CLI: %v", err)
		return false
	}
	return true
}

func CheckKvm(ui stdio.UI) bool {
	if runtime.GOOS != "linux" {
		return true
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		ui.Errorf("/dev/kvm not found. Load kvm module and ensure user is in the kvm group: %v", err)
		return false
	}
	return true
}

func CheckGit(ui stdio.UI) bool {
	if _, err := exec.LookPath("git"); err != nil {
		ui.Errorf("git not found. Install git via your system package manager: %v", err)
		return false
	}
	return true
}

func CheckMsb(ctx context.Context, ui stdio.UI) bool {
	if err := ensureInstalled(ctx); err != nil {
		ui.Errorf("msb runtime setup failed: %v", err)
		return false
	}
	if _, err := exec.LookPath("msb"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		ui.Errorf("msb not on PATH and home directory cannot be resolved: %v", err)
		return false
	}
	binDir := filepath.Join(home, ".microsandbox", "bin")
	binPath := filepath.Join(binDir, "msb")
	if _, err := os.Stat(binPath); err != nil {
		ui.Errorf("msb not on PATH and binary missing at %s: %v", binDir, err)
		return false
	}
	if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		ui.Warnf("could not add %s to PATH for this session (msb CLI may be unavailable): %v", binDir, err)
	}
	rc := shellRcFile(home, os.Getenv("SHELL"))
	ui.Warnf("msb CLI is not on your PATH. Added %s for this session.", binDir)
	ui.Infof("To make this permanent in other shells:")
	ui.Infof("  echo 'export PATH=\"$PATH:%s\"' >> %s", binDir, rc)
	ui.Infof("  or: ln -s %s ~/.local/bin/msb", binPath)
	return true
}

func CheckAll(ctx context.Context, ui stdio.UI) bool {
	basicChecks := CheckMsb(ctx, ui) && CheckDocker(ui) && CheckKvm(ui) && CheckGit(ui)
	if !basicChecks {
		return false
	}
	CheckOrphans(ctx, ui)
	return true
}

func isOrphanedSandbox(name string) bool {
	if !strings.HasPrefix(name, "opencode-msb-") {
		return false
	}
	// vm- sandboxes are the current model; task- sandboxes are operational (prefill).
	return !strings.HasPrefix(name, projectVMPrefix) &&
		!strings.HasPrefix(name, "opencode-msb-task-")
}

func isOrphanedVolume(name string) bool {
	if strings.HasPrefix(name, "opencode-msb-home-") {
		return false
	}
	// clone- volumes are obsolete (clone-on-use removed) → orphaned.
	if strings.HasPrefix(name, "opencode-msb-clone-") {
		return true
	}
	return strings.Contains(name, "-opencode-home-")
}

func isOrphanedImage(ref string) bool {
	if ref == "opencode-msb/runner:base" || ref == "opencode-msb/runner:latest" {
		return true
	}
	if strings.HasPrefix(ref, "opencode-msb/runner:sha256-") {
		return true
	}
	return false
}

func CheckOrphans(ctx context.Context, ui stdio.UI) bool {
	client := newMsbClient()
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
