package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func sanitizeDigest(digest string) string {
	return strings.ReplaceAll(digest, ":", "-")
}

func HomeVolumeName(projectSlug, imageDigest string) string {
	return projectSlug + "-opencode-home-" + sanitizeDigest(imageDigest)
}

type VolumeManager struct {
	fallback bool
	stateDir string
	logger   *log.Logger
}

func NewVolumeManager(fallback bool, stateDir string, logger *log.Logger) *VolumeManager {
	return &VolumeManager{fallback: fallback, stateDir: stateDir, logger: logger}
}

func (vm *VolumeManager) fallbackHomePath(projectSlug, imageDigest string) string {
	return filepath.Join(vm.stateDir, "state", projectSlug, "home", sanitizeDigest(imageDigest))
}

func (vm *VolumeManager) EnsureHome(ctx context.Context, projectSlug, imageDigest, imageTag string, reset bool) (string, error) {
	name := HomeVolumeName(projectSlug, imageDigest)

	if vm.fallback {
		return vm.ensureFallbackHome(ctx, name, projectSlug, imageDigest, imageTag, reset)
	}

	if reset {
		_ = msb.RemoveVolume(ctx, name)
	}

	_, err := msb.GetVolume(ctx, name)
	if err == nil {
		return name, nil
	}

	vol, err := msb.CreateVolume(ctx, name,
		msb.WithVolumeKind(msb.VolumeKindDir),
	)
	if err != nil {
		vm.logger.Warn("msb volume creation failed; using host-directory fallback.")
		vm.fallback = true
		return vm.ensureFallbackHome(ctx, name, projectSlug, imageDigest, imageTag, reset)
	}

	if err := vm.prefillVolume(ctx, vol.Name(), imageTag); err != nil {
		return "", fmt.Errorf("prefill volume %s: %w", name, err)
	}

	return name, nil
}

func (vm *VolumeManager) ensureFallbackHome(ctx context.Context, _, projectSlug, imageDigest, imageTag string, reset bool) (string, error) {
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

	var mountConfig msb.MountConfig
	if isBind {
		mountConfig = msb.Mount.Bind(ref, msb.MountOptions{})
	} else {
		mountConfig = msb.Mount.Named(ref, msb.MountOptions{})
	}

	spin := log.NewSpinner(vm.logger)
	spin.Start("Preparing home volume")
	sb, err := msb.CreateSandbox(ctx, prefillName,
		msb.WithImage(imageTag),
		msb.WithMounts(map[string]msb.MountConfig{
			"/mnt/home": mountConfig,
		}),
		msb.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("create prefill sandbox: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), prefillName)
	}()

	out, err := sb.Exec(ctx, "sh", []string{"-c", "cp -a /home/dev/. /mnt/home/ && chown -R dev:dev /mnt/home"})
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("prefill cp: %w", err)
	}
	if !out.Success() {
		err := fmt.Errorf("prefill cp failed (exit %d): %s", out.ExitCode(), out.Stderr())
		spin.StopError(err)
		return err
	}
	spin.Stop()
	return nil
}
