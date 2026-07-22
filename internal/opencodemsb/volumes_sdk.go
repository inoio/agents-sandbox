//go:build cgo

package opencodemsb

import (
	"context"
	"fmt"
	"os"
	"time"

	m "github.com/superradcompany/microsandbox/sdk/go"
)

func (vm *VolumeManager) EnsureHome(ctx context.Context, projectSlug, imageDigest, imageTag string, reset bool) (string, error) {
	name := HomeVolumeName(projectSlug, imageDigest)

	if vm.fallback {
		return vm.ensureFallbackHome(ctx, name, projectSlug, imageDigest, imageTag, reset)
	}

	if reset {
		_ = m.RemoveVolume(ctx, name)
	}

	_, err := m.GetVolume(ctx, name)
	if err == nil {
		return name, nil
	}

	vol, err := m.CreateVolume(ctx, name,
		m.WithVolumeKind(m.VolumeKindDir),
	)
	if err != nil {
		warn("msb volume creation failed; using host-directory fallback.")
		vm.fallback = true
		return vm.ensureFallbackHome(ctx, name, projectSlug, imageDigest, imageTag, reset)
	}

	if err := vm.prefillVolume(ctx, vol.Name(), imageTag); err != nil {
		return "", fmt.Errorf("prefill volume %s: %w", name, err)
	}

	return name, nil
}

func (vm *VolumeManager) ensureFallbackHome(ctx context.Context, name, projectSlug, imageDigest, imageTag string, reset bool) (string, error) {
	path := vm.fallbackHomePath(projectSlug, imageDigest)
	if reset {
		os.RemoveAll(path)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("create fallback home dir: %w", err)
	}
	entries, _ := os.ReadDir(path)
	if len(entries) == 0 {
		if err := vm.prefillFallback(ctx, path, imageTag); err != nil {
			return "", fmt.Errorf("prefill fallback home: %w", err)
		}
	}
	return path, nil
}

func (vm *VolumeManager) prefillVolume(ctx context.Context, volumeName, imageTag string) error {
	return vm.prefill(ctx, volumeName, imageTag, false)
}

func (vm *VolumeManager) prefillFallback(ctx context.Context, hostPath, imageTag string) error {
	return vm.prefill(ctx, hostPath, imageTag, true)
}

func (vm *VolumeManager) prefill(ctx context.Context, ref, imageTag string, isBind bool) error {
	prefillName := fmt.Sprintf("opencode-msb-prefill-%d", time.Now().UnixNano())

	var mountConfig m.MountConfig
	if isBind {
		mountConfig = m.Mount.Bind(ref, m.MountOptions{})
	} else {
		mountConfig = m.Mount.Named(ref, m.MountOptions{})
	}

	spin := startSpinner("Preparing home volume")
	sb, err := m.CreateSandbox(ctx, prefillName,
		m.WithImage(imageTag),
		m.WithMounts(map[string]m.MountConfig{
			"/mnt/home": mountConfig,
		}),
		m.WithReplace(),
	)
	if err != nil {
		spin.stopError(err)
		return fmt.Errorf("create prefill sandbox: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = m.RemoveSandbox(context.Background(), prefillName)
	}()

	out, err := sb.Exec(ctx, "sh", []string{"-c", "cp -a /home/dev/. /mnt/home/ && chown -R dev:dev /mnt/home"})
	if err != nil {
		spin.stopError(err)
		return fmt.Errorf("prefill cp: %w", err)
	}
	if !out.Success() {
		err := fmt.Errorf("prefill cp failed (exit %d): %s", out.ExitCode(), out.Stderr())
		spin.stopError(err)
		return err
	}
	spin.stop()
	return nil
}
