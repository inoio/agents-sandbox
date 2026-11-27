package reprovision

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// chmodPath is the file-permission seam. A real filesystem makes Chmod's failure
// branch impractical to trigger, and it cannot fail as root in the hermetic
// envelope, so tests substitute it to exercise the error handling.
var chmodPath = os.Chmod

// Materialize writes cf's desired config into a host directory, the host-target
// counterpart of Provision. Provision writes the same ConfigFiles into a VM via
// the sandbox SDK; Materialize writes them under targetDir instead, mapping each
// VM-absolute path relative to vmHome. It is for host-side tooling that needs the
// exact config the launcher would provision into a sandbox, such as seeding a
// KVM guest's home.
func Materialize(cf *ConfigFiles, vmHome, targetDir string) error {
	store := hostConfigStore{vmHome: vmHome, targetDir: targetDir}
	// Remove stale targets first so a previous materialization cannot shadow the
	// files written below. Best-effort, as Provision's removal is: the merged
	// config is written to the last-loaded filename, so a failed removal is
	// non-fatal.
	for _, vmPath := range cf.Remove {
		dest, err := targetPath(vmPath, vmHome, targetDir)
		if err != nil {
			return err
		}
		_ = os.Remove(dest)
	}
	if err := reconcileMainConfig(store, cf); err != nil {
		return fmt.Errorf("materialize the agent's main config: %w", err)
	}
	groups := []struct {
		files map[string][]byte
		kind  string
	}{
		{homeFilesToWrite(store.read, cf.HomeFiles, cf.KeepLogin), "home file"},
		{cf.filesBesideMainConfig(cf.Provisioned), "provisioned file"},
		{cf.Mirror, "mirror file"},
	}
	for _, group := range groups {
		for vmPath, data := range group.files {
			if err := writeMaterializedFile(vmPath, data, cf.Modes, vmHome, targetDir); err != nil {
				return fmt.Errorf("materialize %s: %w", group.kind, err)
			}
		}
	}
	return materializeStateDefaults(cf.StateDefaults, vmHome, targetDir)
}

// writeMaterializedFile creates the destination's parent directories and writes
// data, applying the mapped mode or the 0644 default.
func writeMaterializedFile(
	vmPath string,
	data []byte,
	modes map[string]os.FileMode,
	vmHome, targetDir string,
) error {
	dest, err := targetPath(vmPath, vmHome, targetDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", dest, err)
	}
	mode := os.FileMode(0o644)
	if mapped, ok := modes[vmPath]; ok {
		mode = mapped
	}
	if err := os.WriteFile(dest, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	// os.WriteFile applies the mode only when it creates the file and lets the
	// umask trim it, so chmod afterwards to honour the mapped mode exactly.
	if err := chmodPath(dest, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", dest, err)
	}
	return nil
}

// targetPath maps a VM-absolute path to its host destination under targetDir by
// taking the path relative to vmHome. A path that is not under vmHome is an
// error rather than silently escaping targetDir through a ".." relative path.
func targetPath(vmPath, vmHome, targetDir string) (string, error) {
	rel, err := filepath.Rel(vmHome, vmPath)
	if err != nil {
		return "", fmt.Errorf("map VM path %q under %q: %w", vmPath, vmHome, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("VM path %q is not under home %q", vmPath, vmHome)
	}
	return filepath.Join(targetDir, rel), nil
}

// hostConfigStore is the mainConfigStore over a host directory, mapping each VM
// path under vmHome into targetDir.
type hostConfigStore struct{ vmHome, targetDir string }

func (s hostConfigStore) read(vmPath string) ([]byte, bool) {
	// A path outside the home maps to "", which reads as absent; a write to
	// it reports the error.
	dest, _ := targetPath(vmPath, s.vmHome, s.targetDir)
	content, err := os.ReadFile(dest)
	// A parent that is not a directory means the file cannot exist either.
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, true
	}
	return content, err == nil
}

func (s hostConfigStore) write(vmPath string, data []byte, mode os.FileMode) error {
	modes := map[string]os.FileMode{}
	if mode != 0 {
		modes[vmPath] = mode
	}
	return writeMaterializedFile(vmPath, data, modes, s.vmHome, s.targetDir)
}

func (s hostConfigStore) remove(vmPath string) {
	if dest, err := targetPath(vmPath, s.vmHome, s.targetDir); err == nil {
		_ = os.Remove(dest)
	}
}
